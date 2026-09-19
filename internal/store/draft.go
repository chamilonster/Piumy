package store

import "database/sql"

// Draft is an auto-reply worker's candidate reply — NOT the outbox. A draft
// only ever reaches the outbox (and gets sent) once approved via the
// privileged path; nothing here queues it for actual sending.
type Draft struct {
	ID        int64  `json:"id"`
	ChatJID   string `json:"chat_jid"`
	Text      string `json:"text"`
	Model     string `json:"model,omitempty"`
	CreatedTS int64  `json:"created_ts"`
	Status    string `json:"status"` // pending | approved | discarded | rejected
	// Confirmer: who this draft's confirmation is directed to (a JID) — set
	// when the auto-reply worker holds a reply because the chat/rules
	// require confirmation. Empty for a draft with no confirmer resolved
	// (falls back to the owner).
	Confirmer string `json:"confirmer,omitempty"`
	// BurstMaxTS is the TS of the last message in the burst that triggered
	// this draft (ct-2026-07-13-2243) — approve_draft uses it for
	// MarkHandledBefore instead of time.Now() so only the dispatched messages
	// are marked, not any that arrived while the draft was pending.
	BurstMaxTS int64 `json:"burst_max_ts,omitempty"`
	// Round is this draft's position in a reject→redraft chain for its chat
	// (T15, ct-2026-08-05-123241) — 1 for a fresh thread, incremented only
	// when the immediately-preceding draft for the same chat was rejected.
	// See nextDraftRound/MaxDraftRounds.
	Round int `json:"round"`
	// RejectReason is why this draft was rejected — set only when
	// Status == "rejected". Recorded here, not anywhere else, so
	// capipush.dispatchPayload can read it straight off the draft and splice
	// it into the redispatch (the reason travels WITH the message, not as a
	// separate fetch — Citrino's explicit requirement for T15).
	RejectReason string `json:"reject_reason,omitempty"`
	// Sender is the canonical (whatsmeow.resolveSenderJID'd) group
	// participant this draft answers (T108, ct-2026-09-01-1413) — "" for a
	// 1:1 draft (a single sender needs no scoping) and for any draft created
	// before this field existed. approve_draft reads it back to close only
	// THAT speaker's messages, never the whole chat's — the same defect
	// T108 fixes for a direct send, now closed for the confirmation path too.
	Sender string `json:"sender,omitempty"`
	// MediaPath/MediaMime/MediaKind (T122, ct-2026-09-02-2045): a photo
	// held pending confirmation — MediaPath is a file under PIUMY_MEDIA_DIR.
	// MediaPath is NEVER marshaled (json:"-"): get_drafts (mcpserver/
	// server.go) must not leak a local filesystem path to an MCP caller
	// that may be on a different machine — same reasoning get_media's
	// mediaSummary already applies to FullPath. get_drafts exposes the
	// photo itself only via its own draft_id-scoped data_url, never this
	// raw path. "" on every draft with no media, including every draft
	// created before this field existed.
	MediaPath string `json:"-"`
	MediaMime string `json:"media_mime,omitempty"`
	MediaKind string `json:"media_kind,omitempty"`
	// MediaSeconds (T123, ct-2026-09-02-2121): a voice note's duration held
	// pending confirmation — 0 means unknown/omitted, never guessed.
	// Meaningless for a "photo" draft.
	MediaSeconds int `json:"media_seconds,omitempty"`
}

// MaxDraftRounds caps how many times a rejection automatically redispatches
// for another attempt (T15, ct-2026-08-05-123241) — round 3 rejected does
// NOT trigger a 4th: the back-and-forth isn't converging, and the owner has
// edit_draft/discard_draft to resolve it directly instead of asking the
// agent to keep guessing.
const MaxDraftRounds = 3

// AddDraft records an auto-reply worker's candidate reply as pending — NOT
// sent, NOT in the outbox. Approval (a separate, later piece) is what moves
// a draft into the outbox.
func (s *Store) AddDraft(chatJID, text, model string, ts int64) error {
	return s.AddDraftWithConfirmer(chatJID, text, model, "", "", 0, ts)
}

// AddDraftWithConfirmer is AddDraft plus a confirmer JID, a sender, and
// burstMaxTS.
// confirmer: who holds the draft pending confirmation (empty = owner).
// sender (T108, ct-2026-09-01-1413): the canonical group participant this
// draft answers — "" for a 1:1 draft (no scoping needed, one sender).
// burstMaxTS: TS of the last dispatched burst message (ct-2026-07-13-2243) —
// used by approve_draft for MarkHandledBefore; 0 means use time of approval.
// The new draft's round continues its chat's reject→redraft chain
// (nextDraftRound) — a redraft answering a rejection, not a fresh thread.
func (s *Store) AddDraftWithConfirmer(chatJID, text, model, confirmer, sender string, burstMaxTS int64, ts int64) error {
	round, err := s.nextDraftRound(chatJID)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO drafts (chat_jid, text, model, created_ts, status, confirmer, burst_max_ts, round, sender)
		VALUES (?, ?, ?, ?, 'pending', ?, ?, ?, ?)`, chatJID, text, nullIfEmpty(model), ts, nullIfEmpty(confirmer), burstMaxTS, round, sender)
	return err
}

// AddMediaDraftWithConfirmer is AddDraftWithConfirmer plus a photo (T122,
// ct-2026-09-02-2045) — same confirmation-hold semantics, plus the file
// mediaPath/mediaMime/mediaKind approve_draft needs to enqueue it as media
// instead of plain text. text is the caption (may be empty), same field a
// text-only draft already uses. seconds (T123, ct-2026-09-02-2121) is a
// voice note's duration — 0 for "unknown/omitted" (never guessed) and
// always 0 for a photo draft.
func (s *Store) AddMediaDraftWithConfirmer(chatJID, text, model, confirmer, sender, mediaPath, mediaMime, mediaKind string, seconds int, burstMaxTS int64, ts int64) error {
	round, err := s.nextDraftRound(chatJID)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO drafts (chat_jid, text, model, created_ts, status, confirmer, burst_max_ts, round, sender, media_path, media_mime, media_kind, media_seconds)
		VALUES (?, ?, ?, ?, 'pending', ?, ?, ?, ?, ?, ?, ?, ?)`,
		chatJID, text, nullIfEmpty(model), ts, nullIfEmpty(confirmer), burstMaxTS, round, sender, mediaPath, mediaMime, mediaKind, seconds)
	return err
}

// nextDraftRound continues chatJID's reject→redraft chain: if its most
// recent draft (any status) was rejected, the new one is the next round
// answering that rejection. Any other status — or no prior draft at all —
// starts a fresh thread at round 1; approving or discarding a draft ends
// its chain the same way a brand-new topic would.
func (s *Store) nextDraftRound(chatJID string) (int, error) {
	var status string
	var round int
	err := s.db.QueryRow(`SELECT status, round FROM drafts WHERE chat_jid = ? ORDER BY created_ts DESC LIMIT 1`, chatJID).
		Scan(&status, &round)
	if err == sql.ErrNoRows {
		return 1, nil
	}
	if err != nil {
		return 0, err
	}
	if status != "rejected" {
		return 1, nil
	}
	return round + 1, nil
}

const draftColumns = `id, chat_jid, text, COALESCE(model,''), created_ts, status, COALESCE(confirmer,''), COALESCE(burst_max_ts,0), round, sender,
	media_path, media_mime, media_kind, media_seconds`

func scanDraft(scan func(dest ...any) error) (Draft, error) {
	var d Draft
	err := scan(&d.ID, &d.ChatJID, &d.Text, &d.Model, &d.CreatedTS, &d.Status, &d.Confirmer, &d.BurstMaxTS, &d.Round, &d.Sender,
		&d.MediaPath, &d.MediaMime, &d.MediaKind, &d.MediaSeconds)
	return d, err
}

// PendingDrafts returns drafts still awaiting approval, oldest first.
func (s *Store) PendingDrafts(limit int) ([]Draft, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT `+draftColumns+`
		FROM drafts WHERE status = 'pending' ORDER BY created_ts ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Draft{}
	for rows.Next() {
		d, err := scanDraft(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDraft returns one draft by id, any status — T122 (ct-2026-09-02-2045):
// get_drafts' draft_id-scoped fetch uses this to hand back ONE pending
// draft's photo as a data_url, after the cheap default listing (PendingDrafts)
// already told the caller which one has media. ok is false if id doesn't exist.
func (s *Store) GetDraft(id int64) (d Draft, ok bool, err error) {
	d, err = scanDraft(s.db.QueryRow(`SELECT `+draftColumns+` FROM drafts WHERE id = ?`, id).Scan)
	if err == sql.ErrNoRows {
		return Draft{}, false, nil
	}
	if err != nil {
		return Draft{}, false, err
	}
	return d, true, nil
}

// ApproveDraft moves a pending draft into the outbox — to actually be sent,
// with the existing anti-ban pacing — and marks it approved. textOverride,
// if non-empty, replaces the draft's text before enqueueing (edit-before-
// send). Returns chatJID and burstMaxTS (ct-2026-07-13-2243: the caller uses
// burstMaxTS for MarkHandledBefore instead of time.Now() — 0 means the draft
// was created before this feature) and sender (T108, ct-2026-09-01-1413: the
// caller uses it for MarkHandledBeforeForSender in a group; "" for a 1:1
// draft or one created before this field existed — both mean "close the
// whole chat", never "close nobody"). ok is false if the draft doesn't exist
// or isn't pending; nothing is enqueued in that case.
func (s *Store) ApproveDraft(id int64, textOverride string, ts int64) (chatJID string, burstMaxTS int64, sender string, ok bool, err error) {
	var text, model, mediaPath, mediaMime, mediaKind string
	var mediaSeconds int
	err = s.db.QueryRow(`SELECT chat_jid, text, COALESCE(model,''), COALESCE(burst_max_ts,0), sender, media_path, media_mime, media_kind, media_seconds FROM drafts WHERE id = ? AND status = 'pending'`, id).
		Scan(&chatJID, &text, &model, &burstMaxTS, &sender, &mediaPath, &mediaMime, &mediaKind, &mediaSeconds)
	if err == sql.ErrNoRows {
		return "", 0, "", false, nil
	}
	if err != nil {
		return "", 0, "", false, err
	}
	if textOverride != "" {
		text = textOverride
	}
	// T122 (ct-2026-09-02-2045): a media draft enqueues via
	// EnqueueMediaWithModel so processOutbox sends it as media, not
	// chunked text — mediaPath == "" (every pre-T122 draft, and every
	// text-only one since) takes the exact same EnqueueWithModel call as
	// before, unchanged. mediaSeconds (T123) rides along unconditionally —
	// 0 for a photo draft, same as always.
	if mediaPath != "" {
		err = s.EnqueueMediaWithModel(chatJID, text, mediaPath, mediaMime, mediaKind, mediaSeconds, ts, model)
	} else {
		err = s.EnqueueWithModel(chatJID, text, ts, model)
	}
	if err != nil {
		return "", 0, "", false, err
	}
	if _, err := s.db.Exec(`UPDATE drafts SET status = 'approved' WHERE id = ?`, id); err != nil {
		return "", 0, "", false, err
	}
	return chatJID, burstMaxTS, sender, true, nil
}

// DiscardDraft marks a pending draft as discarded — never sent. ok is false
// if the draft doesn't exist or wasn't pending (nothing to discard).
func (s *Store) DiscardDraft(id int64) (ok bool, err error) {
	res, err := s.db.Exec(`UPDATE drafts SET status = 'discarded' WHERE id = ? AND status = 'pending'`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// EditDraft replaces a pending draft's text in place — no status change,
// still awaiting approval (T15, ct-2026-08-05-123241: "editar sin
// aprobar"). ok is false if the draft doesn't exist or isn't pending (an
// approved/discarded/rejected draft is not editable).
func (s *Store) EditDraft(id int64, text string) (ok bool, err error) {
	res, err := s.db.Exec(`UPDATE drafts SET text = ? WHERE id = ? AND status = 'pending'`, text, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// RejectDraft marks a pending draft rejected and records reason (T15,
// ct-2026-08-05-123241, Citrino verbatim: "el motivo tiene que viajar con
// el mensaje, no aparte") — stored ON the draft row itself so
// capipush.dispatchPayload can read it straight back when redispatching,
// rather than the agent having to fetch it separately. Returns the round
// that was just rejected; the caller (MCP tool / REST handler) compares it
// against MaxDraftRounds to decide whether to reopen the triggering
// messages for a redispatch (round < cap) or leave the chat for the owner
// to resolve directly via edit_draft/discard_draft (round == cap). ok is
// false if the draft doesn't exist or wasn't pending.
func (s *Store) RejectDraft(id int64, reason string) (chatJID string, burstMaxTS int64, round int, ok bool, err error) {
	err = s.db.QueryRow(`SELECT chat_jid, COALESCE(burst_max_ts,0), round FROM drafts WHERE id = ? AND status = 'pending'`, id).
		Scan(&chatJID, &burstMaxTS, &round)
	if err == sql.ErrNoRows {
		return "", 0, 0, false, nil
	}
	if err != nil {
		return "", 0, 0, false, err
	}
	if _, err := s.db.Exec(`UPDATE drafts SET status = 'rejected', reject_reason = ? WHERE id = ?`, reason, id); err != nil {
		return "", 0, 0, false, err
	}
	return chatJID, burstMaxTS, round, true, nil
}

// PendingRejectionNote returns chatJID's outstanding rejection — the reason
// and text of its most recent draft, but ONLY while that draft is still
// 'rejected' with no redraft yet. capipush.dispatchPayload calls this on
// every dispatch to a chat and prepends the result, so the reason arrives
// attached to the very message the agent is about to answer again — not a
// separate lookup the agent has to remember to make. Self-clearing: once a
// new draft exists for the chat (a redraft, any status), the most-recent-
// draft lookup naturally returns that one instead, not the old rejection.
func (s *Store) PendingRejectionNote(chatJID string) (reason, text string, ok bool, err error) {
	var status string
	err = s.db.QueryRow(`SELECT status, COALESCE(reject_reason,''), text FROM drafts WHERE chat_jid = ? ORDER BY created_ts DESC LIMIT 1`, chatJID).
		Scan(&status, &reason, &text)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	if status != "rejected" {
		return "", "", false, nil
	}
	return reason, text, true, nil
}
