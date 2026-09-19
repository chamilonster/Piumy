package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Chat struct {
	JID    string `json:"jid"`
	Name   string `json:"name"`
	Mode   string `json:"mode"`
	LastTS int64  `json:"last_ts"`
	Unread int    `json:"unread"`

	// ContactName: the phone address book's own name for this number/group
	// (ct-2026-07-19-0102, backup — "respaldar el nombre de contacto si esta
	// en el telefono", boss verbatim). Distinct from Name (the WhatsApp
	// display name TouchChat keeps current on every inbound message) —
	// ContactName is the backup/scrape target, populated by the backfill
	// (Sub 2), never touched by the normal inbound flow.
	ContactName string `json:"contact_name,omitempty"`

	// ModeSource: "router" (the router-driven mirror on every inbound) or
	// "manual" (an explicit set_mode/escalate MCP call, or the REST admin
	// endpoint) — ST-B (ct-2026-07-11-0741). Lets the router-driven mirror
	// (SyncRouterMode) tell the two apart so it never clobbers a deliberate
	// owner/agent choice. See SetMode/SyncRouterMode.
	ModeSource string `json:"mode_source"`

	// ConfigLevelSource: "default" (never explicitly touched — TouchChat's
	// starting value for a fresh individual chat) or "manual" (SetActive/
	// SetIsBoss/SetConfirmationMode/MarkConfigManual — see their docs) — M5
	// (ct-2026-07-22-1903). A GROUP's row is always "manual" from creation
	// (groups sit outside the origin-default axis). Same role as ModeSource
	// above, for the separate config-level axis (T66b, ct-2026-08-11-1827:
	// this was previously store-internal only — applyOriginDefaultIfUnset's
	// own manual check — with no way for a caller outside this package to
	// read it; exposed now because restapi/read.go needs it too, to tell a
	// chat the owner actually decided something about apart from one he
	// never touched).
	ConfigLevelSource string `json:"config_level_source"`

	// Active: is the agent allowed to handle this chat? Default false
	// (anti-ban: silent until explicitly marked active).
	Active bool `json:"active"`
	// Archived: WhatsApp's own attribute (independent of Active — a chat can
	// be archived on WhatsApp and still active for piumy-gateway, or vice versa).
	Archived bool `json:"archived"`
	// Status is triage, not permission: whitelist|blacklist|new|ignored|
	// agent_exclusive:<id>. Distinct from router's whitelist (the anti-ban
	// inbound gate) — this never replaces it.
	Status string `json:"status"`

	// IsBoss marks this chat as the trusted owner — the agent takes
	// instructions from / escalates to this contact. READ-ONLY for agents:
	// settable only via the privileged REST path (SetIsBoss), never via an
	// MCP tool — an agent must never set a contact's is_boss flag itself.
	IsBoss bool `json:"is_boss"`

	// IsApprover marks this chat as able to approve/discard drafts —
	// including other chats' drafts — WITHOUT being the owner (Aprobador
	// P1, ct-2026-07-31-0610, boss verbatim: "aprueba pero no es boss, util
	// si la cuenta la administran muchas personas"). Orthogonal to
	// IsBoss/ConfigLevel — a chat can be an approver whether it's boss,
	// automatic, or anything else (capipush.LevelFor checks IsBoss first,
	// then this). Settable via the privileged REST path (SetIsApprover) and
	// via MCP (set_is_approver) — unlike IsBoss, the boss explicitly wants
	// this one reachable by the agent too, but only while the agent's
	// CURRENT dispatch is itself boss-level (isActiveBossDispatch gates the
	// MCP tool's handler, same pattern as approve_draft).
	IsApprover bool `json:"is_approver"`

	// Origin is where this chat came from — derived on read (never cached,
	// always correct): inbound_spoke (a real message exists, in EITHER
	// direction — see ChatOrigin's doc, T18), group_discovered (in
	// group_members but no real message, T18B), or synced_contact (no real
	// message, not in any group).
	Origin string `json:"origin"`
	// LastSpeaker is "them" or "us" (from_me) for the chat's most recent
	// message, or "" if the chat has no messages yet.
	LastSpeaker string `json:"last_speaker,omitempty"`
	// LastText is the text of the chat's most recent message (same row
	// enrichChat already fetches for LastSpeaker — no extra query), or ""
	// if the chat has no messages yet.
	LastText string `json:"last_text,omitempty"`
	// LastModel is messages.model of the most recent OUTBOUND message in this
	// chat — which model gave the last reply, even if the contact has since
	// sent a newer inbound message that hasn't been answered yet.
	LastModel string `json:"last_model,omitempty"`

	// Memory: particular facts learned about this contact. Agent-writable via MCP.
	Memory string `json:"memory,omitempty"`
	// Context: general/explanatory situation of the relationship — broader
	// than Memory's discrete facts. Agent-writable via MCP, same as Memory.
	Context string `json:"context,omitempty"`
	// Rules: behavior instructions for how the AI should treat this specific
	// chat — "like a skill". READ-ONLY for agents: settable only via the
	// privileged REST path, same trust gate as IsBoss.
	Rules string `json:"rules,omitempty"`

	// ConfirmationMode: this chat's confirmation BASELINE — "none",
	// "discretion", or "always" (F4-DESIGN §4; send_message's gate reads
	// this). Defaulted BY TYPE in TouchChat: a group starts "always", a 1-1
	// chat starts "none". Owner-overridable via set_confirmation_mode
	// (boss-only MCP) or the privileged REST path.
	ConfirmationMode string `json:"confirmation_mode"`
	// Confirmer: JID to direct a held reply's confirmation to. Empty means
	// the default (the owner).
	Confirmer string `json:"confirmer,omitempty"`

	// Description: a group's topic. Also "a note of your own about the
	// chat" — settable via the privileged REST path too.
	Description string `json:"description,omitempty"`
	// GroupInviteLink: a group's invite link — read-only, populated from
	// WhatsApp sync/push events. No REST/MCP setter.
	GroupInviteLink string `json:"group_invite_link,omitempty"`

	// ClaimedBy / ClaimedUntil: a transient, TTL-based lock so two connected
	// MCP agents/models don't both work this chat at once. Already
	// EFFECTIVE by the time a caller sees it (GetChat/ListChats/PendingChats
	// zero these out once ClaimedUntil has passed — see effectiveClaim).
	ClaimedBy    string `json:"claimed_by,omitempty"`
	ClaimedUntil int64  `json:"claimed_until,omitempty"`

	// HistoryState: gradual on-demand history backfill progress (ct-2026-07-21-1306)
	// — pending/downloading/loaded. See ConfigLevel-adjacent HistorySummary
	// and internal/whatsmeow's history worker (the only writer, via
	// SetHistoryState).
	HistoryState string `json:"history_state"`

	// SilenceReason/SilenceAt: the silent_act's own audit trail (S11,
	// ct-2026-07-30-1619) — why the agent chose NOT to reply the last time
	// it declined, and when. Without this, silence is indistinguishable
	// from a stuck agent or a dispatch that never arrived; with it, the
	// owner can review the agent's judgment instead of trusting it blindly.
	// SilenceReason is free text, optional (may be ""); SilenceAt is 0 if
	// silent_act was never used on this chat. Overwritten by each new
	// silent_act call — a single last-reason slot, not a history.
	SilenceReason string `json:"silence_reason,omitempty"`
	SilenceAt     int64  `json:"silence_at,omitempty"`
}

// IsGroupJID reports whether jid is a WhatsApp group chat (@g.us vs
// @s.whatsapp.net / open-wa's other suffixes). Exported (ct-2026-07-10-1758)
// so callers outside this package (corepipeline) share the same criterion
// instead of duplicating the suffix check.
func IsGroupJID(jid string) bool {
	return strings.HasSuffix(jid, "@g.us")
}

// IsLIDJID reports whether jid is WhatsApp's "hidden user" LID form
// (ct-2026-07-11-0030 Fase 2) rather than a real phone-number JID
// (@s.whatsapp.net). Survives the F2 revert (ct-2026-07-18-171940) because
// capipush.plaintextPayload (ct-2026-07-18-1416, kept) uses it to decide
// whether the dispatch payload's "numero" needs resolving.
func IsLIDJID(jid string) bool {
	return strings.HasSuffix(jid, "@lid")
}

// StripDeviceSuffix removes WhatsApp's device suffix — "usuario:NN" becomes
// "usuario" — from a phone-number JID (T45, ct-2026-08-10-1424). The suffix
// names which DEVICE of an account (phone, web, tablet) sent something,
// never a conversation: a chat is with the account, never with one of its
// devices. Measured against the owner's live installation: exactly one of
// 200 chats carried it — the owner's OWN account, from client.Store.ID
// (always device-qualified by WhatsApp's own multi-device design), creating
// a second is_boss row nothing could ever deliver to, alongside the real
// one. @lid and @g.us never carry a device suffix and are returned
// unchanged — this ONLY touches the @s.whatsapp.net form.
func StripDeviceSuffix(jid string) string {
	const server = "@s.whatsapp.net"
	if !strings.HasSuffix(jid, server) {
		return jid
	}
	user := strings.TrimSuffix(jid, server)
	if colon := strings.IndexByte(user, ':'); colon >= 0 {
		user = user[:colon]
	}
	return user + server
}

// MergeDeviceSuffixedOwnChat (T118, ct-2026-09-01-2205) is a once-per-boot
// repair, not a migration to run and forget: resolveChatJID/resolveSenderJID
// (whatsmeow/inbound.go, T117) now stop a NEW device-suffixed own-jid row
// from ever being created, but an installation that already had one before
// that fix keeps carrying it forever without this — the duplicate chat
// Citrino measured against the real database doesn't heal itself.
//
// ownJID must already be clean (StripDeviceSuffix'd/ToNonAD'd — the caller,
// recordOwnIdentity, already computes exactly that). Finds any `chats` row
// whose jid is ownJID with a ":<device>" suffix spliced in, re-points any
// `messages` rows under that dirty jid to the clean one (never silently
// drops history — this IS the fusion, not just a deletion), then deletes
// the dirty `chats` row and any matching `avatars` row. A no-op (nil error)
// when no dirty row exists — the common case after the first boot that
// runs this.
func (s *Store) MergeDeviceSuffixedOwnChat(ownJID string) error {
	const server = "@s.whatsapp.net"
	user, ok := strings.CutSuffix(ownJID, server)
	if !ok {
		return nil // @lid/@g.us never carry a device suffix — nothing to merge
	}
	pattern := user + ":%" + server

	rows, err := s.db.Query(`SELECT jid FROM chats WHERE jid LIKE ?`, pattern)
	if err != nil {
		return err
	}
	var dirty []string
	for rows.Next() {
		var jid string
		if err := rows.Scan(&jid); err != nil {
			rows.Close()
			return err
		}
		dirty = append(dirty, jid)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()

	for _, dirtyJID := range dirty {
		// OR IGNORE: messages' PRIMARY KEY is (chat_jid, id) — if the exact
		// same message id somehow already exists under the clean jid (the
		// same event double-delivered under both spellings before T117's
		// fix), skip that one row rather than aborting the whole cleanup.
		// It stays under the dirty jid, orphaned once the row below is
		// deleted — a rare leftover, not a reason to leave everything else
		// unmerged.
		if _, err := s.db.Exec(`UPDATE OR IGNORE messages SET chat_jid = ? WHERE chat_jid = ?`, ownJID, dirtyJID); err != nil {
			return err
		}
		if _, err := s.db.Exec(`DELETE FROM chats WHERE jid = ?`, dirtyJID); err != nil {
			return err
		}
		if _, err := s.db.Exec(`DELETE FROM avatars WHERE jid = ?`, dirtyJID); err != nil {
			return err
		}
	}
	return nil
}

// StatusBroadcastJID is WhatsApp's reserved pseudo-chat for Status/Stories
// updates — every contact's status lands here under this ONE shared jid,
// never under a real person's or group's own jid. A `chats` row for it is
// a side effect of ever seeing ANY contact's status; its `name` column
// just reflects whoever's status was seen most recently (misleading, not
// a real identity). Never a conversation, contact, or group — excluded
// wherever a "chat" is counted or listed (ct-2026-07-29, measured: showed
// up in CHATS as a fake contact "Sara Olave").
const StatusBroadcastJID = "status@broadcast"

// realMessageSQL is the WHERE-clause fragment (no leading "WHERE") for "this
// messages row is a genuine conversation message" — text or type actually
// carries content, AND it isn't status@broadcast (a status/story update,
// categorically never a conversation even when it has a real media type —
// ct-2026-07-29, measured: a "media" message under status@broadcast showed
// up in CHATS as a fake contact "Sara Olave", whoever's status was last
// seen). WhatsApp protocol noise (delivery receipts, reactions, poll-vote
// echoes) persists as a `messages` row with BOTH columns empty and would
// otherwise masquerade as a real conversation too (measured: 401 of 405
// "@lid chats with a message" were 100% empty-content rows — group members
// WhatsApp happened to deliver a receipt/reaction for, not people the boss
// ever actually talked to). Uses a bare `chat_jid` column reference — every
// current call site has exactly one unaliased-or-not `messages` table in
// scope, so it resolves regardless of alias. Shared by every "does this
// chat have a message" query (ChatJIDsWithMessages, BackupCounts,
// HistorySummary) so the criterion can't drift between them.
const realMessageSQL = `((text IS NOT NULL AND text != '') OR (type IS NOT NULL AND type != '')) AND chat_jid != '` + StatusBroadcastJID + `'`

// TouchChat creates/updates a chat (name + last timestamp) without touching
// the mode. A group JID SEEN FOR THE FIRST TIME defaults to status "ignored"
// instead of "new" (groups are chats too, but the AI does nothing with one
// until the owner hands out rules and un-ignores it) — ON CONFLICT never
// touches status, so this never overwrites one the owner already set.
// Confirmation default is also by type: a 1-1 chat starts "none" (replies on
// its own once it has rules); a group starts "always" (send_message holds a
// draft instead of sending — F4-DESIGN §4) — same ON CONFLICT protection,
// never overwrites an owner override.
//
// config_level_source (M5, ct-2026-07-22-1903): a group's row is always
// 'manual' — groups stay OUT of the origin-default axis entirely, per
// Citrino's ratified checkpoint. An individual chat's FRESH row starts
// 'default'; applyOriginDefaultIfUnset below then stamps its actual
// status/active/confirmation_mode from the current origin (contact vs new
// number) — called on every TouchChat, not just creation, since a LATER
// message from an already-known contact must keep re-affirming the contact
// default (harmless no-op once source flips to 'manual' or the columns
// already match).
func (s *Store) TouchChat(jid, name string, ts int64) error {
	jid = StripDeviceSuffix(jid)
	status := "new"
	confirmMode := "none"
	configSource := "default"
	isGroup := IsGroupJID(jid)
	if isGroup {
		status = "ignored"
		confirmMode = "always"
		configSource = "manual"
	}
	_, err := s.db.Exec(`INSERT INTO chats (jid, name, last_ts, status, confirmation_mode, config_level_source) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(jid) DO UPDATE SET
			name = CASE WHEN excluded.name != '' THEN excluded.name ELSE chats.name END,
			last_ts = MAX(chats.last_ts, excluded.last_ts)`, jid, name, ts, status, confirmMode, configSource)
	if err != nil || isGroup {
		return err
	}
	return s.applyOriginDefaultIfUnset(jid)
}

// SetMode sets a chat's mode (auto | dedicated) as a DELIBERATE, explicit
// choice — the set_mode/escalate MCP tools and the REST admin endpoint all
// call this. Always wins: marks mode_source='manual' so SyncRouterMode's
// per-inbound mirror never overwrites it again (ST-B, ct-2026-07-11-0741 —
// before this, the router silently reverted any manual change on the very
// next message). Creates the chat if missing. 'advanced' (Piumy's legacy
// name for this mode) is accepted as an inbound alias and normalized to
// 'dedicated' — router.json or an older caller may still emit the old name.
func (s *Store) SetMode(jid, mode string) error {
	jid = StripDeviceSuffix(jid)
	if mode == "advanced" {
		mode = "dedicated"
	}
	_, err := s.db.Exec(`INSERT INTO chats (jid, mode, mode_source) VALUES (?, ?, 'manual')
		ON CONFLICT(jid) DO UPDATE SET mode = excluded.mode, mode_source = 'manual'`, jid, mode)
	return err
}

// SyncRouterMode mirrors the router's mode decision onto a chat — called
// once per inbound message (corepipeline.handleInbound) and once per
// group at connect time (whatsmeow.seedGroups). A NO-OP if the chat's
// mode_source is already 'manual' (ST-B, ct-2026-07-11-0741): the owner/
// agent's explicit choice (SetMode) always outranks the router's default,
// forever, until SetMode is called again. Creates the chat if missing
// (mode_source='router', the correct starting state for a fresh chat).
func (s *Store) SyncRouterMode(jid, mode string) error {
	jid = StripDeviceSuffix(jid)
	if mode == "advanced" {
		mode = "dedicated"
	}
	_, err := s.db.Exec(`INSERT INTO chats (jid, mode, mode_source) VALUES (?, ?, 'router')
		ON CONFLICT(jid) DO UPDATE SET mode = excluded.mode
		WHERE chats.mode_source != 'manual'`, jid, mode)
	return err
}

// activationSweepWindow is SetActive's code-level fallback for
// SettingActivationSweepWindow (S8, ct-2026-07-30-031126) — see its own
// doc and SetActive's for why the cutoff can't just be "now".
const activationSweepWindow = time.Hour

// SetActive marks whether the agent is allowed to handle this chat.
// Distinct from Archived (WhatsApp's own flag) and from router's whitelist
// (the anti-ban inbound gate) — this never replaces either.
// config_level_source='manual' (M5, ct-2026-07-22-1903): every current
// caller of SetActive (set_chat_active MCP tool, the REST admin path, and
// SetConfigLevel's own internal composition) IS an explicit decision about
// this chat's mode — never an internal/system write — so marking it here is
// safe unconditionally. applyOriginDefaultIfUnset/applyConfigLevelDefault
// (M5's own automatic default application) deliberately bypass this setter,
// writing the raw column directly, so they never re-trip 'manual' on
// themselves.
// S8 (ct-2026-07-30-031126): activating a chat means "de ahora en
// adelante", not "reprocesá su historia completa" — a chat inactive for
// months (or never activated) can carry a real backlog of unhandled
// inbound messages, and PendingDedicated (gated on active=1) would dump
// ALL of it into the dispatch queue the instant active flips, ts ASC —
// months-old messages ahead of today's conversation, and enough volume to
// trip S3's backpressure on their own. Checked first: S3's SwampedWindow
// only bounds the BACKPRESSURE THRESHOLD, not which messages
// PendingDedicated actually returns — it does not cover this.
//
// Fix: on a genuine inactive→active transition (never on active=false, and
// never a no-op re-activation of an already-active chat — that would
// silently swallow messages the agent hasn't gotten to yet), sweep
// whatever's OLDER than activationSweepWindow to handled=1 BEFORE flipping
// the flag, so PendingDedicated never sees the stale backlog. Reuses
// MarkHandledBefore verbatim — the same "handled means out of the live
// queue, not gone" contract send_message/approve_draft already rely on.
// Never deletes or hides anything: GetMessages has no handled filter at
// all, so the full history (and the dashboard's own message list) stays
// exactly as it was — only the FUTURE the agent is asked to act on changes.
//
// The cutoff is `now - window`, NOT `now` (Citrino's catch on the first
// pass): sweeping everything up to the instant of activation swallowed the
// EXACT message that prompted it — the boss's own real flow is "spot a
// message from someone, THEN say 'atendé a este número'", seconds to
// minutes apart, never simultaneous. activationSweepWindow (default 1h,
// live-overridable via SettingActivationSweepWindow, same pattern as S3's
// SwampedWindow) is generous enough that anything recent enough to matter
// survives and dispatches normally; only backlog clearly older than that
// gets swept.
func (s *Store) SetActive(jid string, active bool) error {
	jid = StripDeviceSuffix(jid)
	if active {
		var wasActive int
		err := s.db.QueryRow(`SELECT active FROM chats WHERE jid = ?`, jid).Scan(&wasActive)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if wasActive == 0 {
			window := s.SettingDuration(SettingActivationSweepWindow, activationSweepWindow)
			if err := s.MarkHandledBefore(jid, time.Now().Add(-window).Unix()); err != nil {
				return err
			}
		}
	}
	_, err := s.db.Exec(`INSERT INTO chats (jid, active, config_level_source) VALUES (?, ?, 'manual')
		ON CONFLICT(jid) DO UPDATE SET active = excluded.active, config_level_source = 'manual'`, jid, b2i(active))
	return err
}

// SetArchived records WhatsApp's own archived flag for a chat.
func (s *Store) SetArchived(jid string, archived bool) error {
	jid = StripDeviceSuffix(jid)
	_, err := s.db.Exec(`INSERT INTO chats (jid, archived) VALUES (?, ?)
		ON CONFLICT(jid) DO UPDATE SET archived = excluded.archived`, jid, b2i(archived))
	return err
}

// SetStatus sets a chat's triage status (whitelist|blacklist|new|ignored|
// agent_exclusive:<id>). Creates the chat if missing.
func (s *Store) SetStatus(jid, status string) error {
	jid = StripDeviceSuffix(jid)
	_, err := s.db.Exec(`INSERT INTO chats (jid, status) VALUES (?, ?)
		ON CONFLICT(jid) DO UPDATE SET status = excluded.status`, jid, status)
	return err
}

// ClaimChat attempts to claim jid for model, valid until now+ttl (avoid
// double-attention between multiple MCP agents/models working the same
// queue). Succeeds (true) if the chat is unclaimed, already claimed by this
// SAME model (renews — idempotent), or the existing claim has already
// expired. Fails (false, nil error — an ordinary "someone else has it"
// outcome) if actively claimed by a DIFFERENT model. Deliberately
// UPDATE-only (no upsert): claiming a chat that doesn't exist is
// meaningless, so the caller checks existence itself first. The single
// WHERE clause makes the claim atomic against a concurrent attempt from
// another connection.
func (s *Store) ClaimChat(jid, model string, ttl time.Duration) (bool, error) {
	jid = StripDeviceSuffix(jid)
	now := time.Now().Unix()
	until := now + int64(ttl.Seconds())
	res, err := s.db.Exec(`UPDATE chats SET claimed_by = ?, claimed_until = ?
		WHERE jid = ? AND (COALESCE(claimed_by,'') = '' OR claimed_by = ? OR claimed_until <= ?)`,
		model, until, jid, model, now)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ReleaseChat clears jid's claim, but ONLY if model currently holds it — a
// stale or foreign release call must never clear someone ELSE's active
// claim. A release for a chat that isn't claimed by model is a harmless
// no-op, not an error.
func (s *Store) ReleaseChat(jid, model string) error {
	jid = StripDeviceSuffix(jid)
	_, err := s.db.Exec(`UPDATE chats SET claimed_by = '', claimed_until = 0
		WHERE jid = ? AND claimed_by = ?`, jid, model)
	return err
}

// SetIsBoss marks/unmarks a chat as the trusted owner. Deliberately has no
// MCP tool wired to it anywhere — only callable from the privileged REST
// path, never by an agent.
// config_level_source='manual': same reasoning as SetActive above — every
// caller (set_is_boss MCP tool, REST, SetConfigLevel) is an explicit owner
// decision (M5, ct-2026-07-22-1903).
// is_boss_touched=1 (T12, ct-2026-08-05-1231): every call here IS an
// explicit decision about is_boss specifically, true or false — freezes it
// so MarkOwnerIfUntouched never revisits this chat again, whichever way it
// landed.
// SetIsBoss is the one write path every "level=boss" caller funnels
// through (the REST admin endpoint, SetConfigLevel's "boss" case) — so the
// group guard lives here, not duplicated at each caller (T121,
// ct-2026-09-02-1722, boss verbatim: "boss es un numero", a group isn't a
// person). Only MARKING a group boss is refused — isBoss=false always
// succeeds, so unmarking (a cleanup, an owner correcting a mistake) is
// never itself blocked by the guard it undoes.
func (s *Store) SetIsBoss(jid string, isBoss bool) error {
	jid = StripDeviceSuffix(jid)
	if isBoss && IsGroupJID(jid) {
		return fmt.Errorf("store: a group cannot be boss — boss identifies a person by number, not a group (%s)", jid)
	}
	_, err := s.db.Exec(`INSERT INTO chats (jid, is_boss, is_boss_touched, config_level_source) VALUES (?, ?, 1, 'manual')
		ON CONFLICT(jid) DO UPDATE SET is_boss = excluded.is_boss, is_boss_touched = 1, config_level_source = 'manual'`, jid, b2i(isBoss))
	return err
}

// MarkOwnerIfUntouched marks jid as the trusted owner UNLESS is_boss has
// already been explicitly decided for it — true or false, by SetIsBoss or a
// prior call to this same function (T12, ct-2026-08-05-1231, boss verbatim:
// "un problema, el selfnumber no se auto define como boss automaticamente").
// Called once per connect, from the same point recordOwnIdentity already
// records state.OwnJID (internal/whatsmeow) — the number a WhatsApp session
// is PAIRED WITH is the owner, by definition, nothing to guess.
//
// Requires jid's row to already exist (the caller runs TouchChat first) —
// an INSERT here would carry the raw column defaults meant for a
// just-added chat with unknown type (confirmation_mode='always', the
// group-oriented default), and send_message's gate (send.go) checks
// confirmation_mode regardless of is_boss — every reply to the owner would
// silently turn into a draft awaiting his own approval.
func (s *Store) MarkOwnerIfUntouched(jid string) error {
	jid = StripDeviceSuffix(jid)
	_, err := s.db.Exec(`UPDATE chats SET is_boss = 1, is_boss_touched = 1
		WHERE jid = ? AND is_boss_touched = 0`, jid)
	return err
}

// SetIsApprover grants/revokes the approver pin (Aprobador P1, ct-2026-07-31-
// 0610) — orthogonal to config_level_source, deliberately NOT touched here:
// unlike IsBoss, IsApprover is not one of the 5 unified config levels (it's
// a separate axis — "aprueba pero no es boss"), so marking a chat approver
// must never look like a manual config-level override to ConfigLevel/
// SetConfigLevel's own bookkeeping.
func (s *Store) SetIsApprover(jid string, isApprover bool) error {
	jid = StripDeviceSuffix(jid)
	_, err := s.db.Exec(`INSERT INTO chats (jid, is_approver) VALUES (?, ?)
		ON CONFLICT(jid) DO UPDATE SET is_approver = excluded.is_approver`, jid, b2i(isApprover))
	return err
}

// BossJIDs returns the JIDs of every chat marked is_boss — the password
// recovery code's fan-out list (S1e-1, ct-2026-07-19-1652), alongside the
// gateway's own number. Device suffixes are stripped and duplicates
// collapsed (T52, ct-2026-08-10-1837): a ghost row with the device-suffix
// form and the real row are the same destination — sending to both would
// duplicate the message.
func (s *Store) BossJIDs() ([]string, error) {
	rows, err := s.db.Query(`SELECT jid FROM chats WHERE is_boss = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]struct{}{}
	var jids []string
	for rows.Next() {
		var jid string
		if err := rows.Scan(&jid); err != nil {
			return nil, err
		}
		jid = StripDeviceSuffix(jid)
		if _, dup := seen[jid]; dup {
			continue
		}
		seen[jid] = struct{}{}
		jids = append(jids, jid)
	}
	return jids, rows.Err()
}

// SetChatMemory sets a chat's memory (particular facts learned about the
// contact). Agent-writable via MCP — the system is meant to learn/build this.
func (s *Store) SetChatMemory(jid, memory string) error {
	jid = StripDeviceSuffix(jid)
	_, err := s.db.Exec(`INSERT INTO chats (jid, memory) VALUES (?, ?)
		ON CONFLICT(jid) DO UPDATE SET memory = excluded.memory`, jid, memory)
	return err
}

// SetChatContext sets a chat's context (general/explanatory situation of the
// relationship). Agent-writable via MCP, same as SetChatMemory.
func (s *Store) SetChatContext(jid, context string) error {
	jid = StripDeviceSuffix(jid)
	_, err := s.db.Exec(`INSERT INTO chats (jid, context) VALUES (?, ?)
		ON CONFLICT(jid) DO UPDATE SET context = excluded.context`, jid, context)
	return err
}

// SetChatSilence records the reason (and when) an agent last chose
// silent_act over send_message for this chat (S11, ct-2026-07-30-1619) —
// overwrites any previous reason, same "one current slot" convention as
// Memory/Context, since only the MOST RECENT silence needs auditing, not a
// history of every one.
func (s *Store) SetChatSilence(jid, reason string, ts int64) error {
	jid = StripDeviceSuffix(jid)
	_, err := s.db.Exec(`INSERT INTO chats (jid, silence_reason, silence_at) VALUES (?, ?, ?)
		ON CONFLICT(jid) DO UPDATE SET silence_reason = excluded.silence_reason, silence_at = excluded.silence_at`, jid, reason, ts)
	return err
}

// SetChatRules sets a chat's rules — behavior instructions for how the AI
// should treat this specific chat ("like a skill"). Callable from the
// privileged REST path AND, since T31 (ct-2026-08-06-0244, the boss's own
// call — unconditional, no gate), the MCP tool set_chat_rules
// (mcpserver/admin_tools.go). The type-tier rules (SetTypeRules) and
// SetIsBoss stay REST-only — T31 only opened this one.
func (s *Store) SetChatRules(jid, rules string) error {
	jid = StripDeviceSuffix(jid)
	_, err := s.db.Exec(`INSERT INTO chats (jid, rules) VALUES (?, ?)
		ON CONFLICT(jid) DO UPDATE SET rules = excluded.rules`, jid, rules)
	return err
}

// SetConfirmationMode sets a chat's confirmation baseline: "none",
// "discretion", or "always" (defaulted by type in TouchChat). Callers
// validate mode against that enum (mcpserver/restapi) — this layer just
// persists whatever it's given. See Chat.ConfirmationMode.
// config_level_source='manual': same reasoning as SetActive/SetIsBoss above
// (M5, ct-2026-07-22-1903).
func (s *Store) SetConfirmationMode(jid, mode string) error {
	jid = StripDeviceSuffix(jid)
	_, err := s.db.Exec(`INSERT INTO chats (jid, confirmation_mode, config_level_source) VALUES (?, ?, 'manual')
		ON CONFLICT(jid) DO UPDATE SET confirmation_mode = excluded.confirmation_mode, config_level_source = 'manual'`, jid, mode)
	return err
}

// SetConfirmer sets who a held reply's confirmation is directed to by
// default for this chat (a JID; empty falls back to the owner).
func (s *Store) SetConfirmer(jid, confirmer string) error {
	jid = StripDeviceSuffix(jid)
	_, err := s.db.Exec(`INSERT INTO chats (jid, confirmer) VALUES (?, ?)
		ON CONFLICT(jid) DO UPDATE SET confirmer = excluded.confirmer`, jid, confirmer)
	return err
}

// SetChatDescription sets a group's topic/description. Also usable as "a
// note of your own about the chat" via the privileged REST path.
func (s *Store) SetChatDescription(jid, description string) error {
	jid = StripDeviceSuffix(jid)
	_, err := s.db.Exec(`INSERT INTO chats (jid, description) VALUES (?, ?)
		ON CONFLICT(jid) DO UPDATE SET description = excluded.description`, jid, description)
	return err
}

// SetGroupInviteLink sets a group's invite link. Only ever called from the
// adapter (WhatsApp is the sole source of truth) — no REST/MCP setter exists.
func (s *Store) SetGroupInviteLink(jid, link string) error {
	jid = StripDeviceSuffix(jid)
	_, err := s.db.Exec(`INSERT INTO chats (jid, group_invite_link) VALUES (?, ?)
		ON CONFLICT(jid) DO UPDATE SET group_invite_link = excluded.group_invite_link`, jid, link)
	return err
}

// SetContactName sets a chat's contact_name — see Chat.ContactName's doc.
//
// M5 (ct-2026-07-22-1903): a chat becoming a contact re-affirms the origin
// default toward "contacto" (applyOriginDefaultIfUnset — no-op once
// config_level_source is 'manual', or for a group). ponytail: the REVERSE
// transition (a contact removed from the phone's agenda, name cleared to
// "") does NOT re-classify back toward "número nuevo" — syncContacts never
// clears contact_name today, and a manual clear via the dashboard is
// rare/deliberate. Revisit if that becomes a real need.
func (s *Store) SetContactName(jid, name string) error {
	jid = StripDeviceSuffix(jid)
	_, err := s.db.Exec(`INSERT INTO chats (jid, contact_name) VALUES (?, ?)
		ON CONFLICT(jid) DO UPDATE SET contact_name = excluded.contact_name`, jid, name)
	if err != nil || name == "" {
		return err
	}
	return s.applyOriginDefaultIfUnset(jid)
}

// ChatIsOff reports whether the owner turned this chat off — the single
// definition of "apagado" (T67, ct-2026-08-11-172135) shared by dispatch
// (pending.go's SQL queries, via the offStatusSQL fragment right below
// them) and send (mcpserver/send.go) and resolve_chat
// (mcpserver/server.go). Before this, three separate SQL queries in
// pending.go each spelled out their own `status != 'ignored'` condition —
// none of them mentioned 'blacklist', so a blacklisted chat (SetStatus
// never touches active) kept dispatching to an agent, only failing later
// at send.go's own (correct) check. One Go predicate here, one matching
// SQL fragment there, both named after the same concept, makes that kind
// of silent divergence structurally harder to repeat.
func ChatIsOff(status string) bool {
	return status == "ignored" || status == "blacklist"
}

// ConfigLevel unifies the 4 underlying per-chat config fields (is_boss,
// status, active, confirmation_mode) into ONE 5-value level for the
// interface (MCP/REST) — boss/auto/confirm/unattended/ignored. Pure
// projection: the engine itself (capipush.LevelFor, the gate, send.go's
// validation) keeps reading the 4 fields directly and is untouched by this;
// ConfigLevel/SetConfigLevel are a translation layer on top, not a new
// source of truth.
//
// Mapping (first match wins):
//   - boss       c.IsBoss
//   - ignored    c.Status == "ignored"
//   - unattended !c.Active (active=false = silence — the current anti-ban
//     default; a chat that isn't active never gets a reply regardless of
//     its confirmation_mode)
//   - confirm    c.ConfirmationMode is "always" or "discretion"
//   - auto       c.ConfirmationMode == "none"
//   - unattended anything else (a chat with an unrecognized
//     confirmation_mode — same fail-safe-to-silent default as above)
func ConfigLevel(c Chat) string {
	switch {
	case c.IsBoss:
		return "boss"
	case c.Status == "ignored":
		return "ignored"
	case !c.Active:
		return "unattended"
	case c.ConfirmationMode == "always" || c.ConfirmationMode == "discretion":
		return "confirm"
	case c.ConfirmationMode == "none":
		return "auto"
	default:
		return "unattended"
	}
}

// SetConfigLevel translates one of the 5 unified config levels
// (boss/auto/confirm/unattended/ignored) into the 4 underlying fields,
// REUSING the existing setters (SetIsBoss/SetActive/SetConfirmationMode/
// SetStatus) — no new SQL, no duplicated write path. See ConfigLevel for
// the reverse (read) direction.
//
// boss/auto/confirm all clear a leftover "ignored"/"blacklist" Status (via
// clearIgnoredStatus) — a group is born with Status "ignored" (TouchChat),
// so without this, choosing any of these three levels for a group silently
// keeps it "ignored" underneath: ConfigLevel reads back "ignored" for
// confirm (T120), and PendingDedicated's queue keeps skipping it once the
// level moves off boss/auto again. unattended/ignored are exempt — going
// quiet is their whole job.
func (s *Store) SetConfigLevel(jid, level string) error {
	// T125 (ct-2026-09-02-2221): normalized ONCE here, at the top — every
	// case below calls another setter (SetIsBoss/SetActive/...) that ALSO
	// normalizes internally, but clearIgnoredStatus's own GetChat READ
	// does not; without this, a device-suffixed jid would let the writes
	// above land correctly on the clean row while clearIgnoredStatus's
	// read looked at the (now nonexistent) dirty one and silently skipped
	// clearing status.
	jid = StripDeviceSuffix(jid)
	switch level {
	case "boss":
		if err := s.SetIsBoss(jid, true); err != nil {
			return err
		}
		if err := s.SetActive(jid, true); err != nil {
			return err
		}
		if err := s.SetConfirmationMode(jid, "none"); err != nil {
			return err
		}
		return s.clearIgnoredStatus(jid)
	case "auto":
		if err := s.SetIsBoss(jid, false); err != nil {
			return err
		}
		if err := s.SetActive(jid, true); err != nil {
			return err
		}
		if err := s.SetConfirmationMode(jid, "none"); err != nil {
			return err
		}
		return s.clearIgnoredStatus(jid)
	case "confirm":
		if err := s.SetIsBoss(jid, false); err != nil {
			return err
		}
		if err := s.SetActive(jid, true); err != nil {
			return err
		}
		if err := s.SetConfirmationMode(jid, "always"); err != nil {
			return err
		}
		return s.clearIgnoredStatus(jid)
	case "unattended":
		if err := s.SetIsBoss(jid, false); err != nil {
			return err
		}
		if err := s.SetActive(jid, false); err != nil {
			return err
		}
		c, ok, err := s.GetChat(jid)
		if err != nil {
			return err
		}
		if ok && c.Status == "ignored" {
			return s.SetStatus(jid, "new")
		}
		return nil
	case "ignored":
		if err := s.SetStatus(jid, "ignored"); err != nil {
			return err
		}
		if err := s.SetActive(jid, false); err != nil {
			return err
		}
		return s.SetIsBoss(jid, false)
	default:
		return fmt.Errorf("store: invalid config level %q, want boss|auto|confirm|unattended|ignored", level)
	}
}

// clearIgnoredStatus promotes a chat's Status from "ignored"/"blacklist" to
// "whitelist" — shared by SetConfigLevel's boss/auto/confirm cases, the
// three levels whose confirmation_mode/active/is_boss combination implies
// the agent should actually see the chat's messages.
func (s *Store) clearIgnoredStatus(jid string) error {
	c, ok, err := s.GetChat(jid)
	if err != nil {
		return err
	}
	if ok && (c.Status == "ignored" || c.Status == "blacklist") {
		return s.SetStatus(jid, "whitelist")
	}
	return nil
}

// MarkConfigManual marks a chat's config_level_source as 'manual' — an
// explicit owner decision that applyOriginDefaultIfUnset (M5,
// ct-2026-07-22-1903) must never silently override afterwards.
// SetIsBoss/SetActive/SetConfirmationMode already mark it themselves as
// part of their own write; this is for the SetStatus-based paths, where
// SetStatus itself stays deliberately NEUTRAL (TouchChat's row-creation
// write and M3/M4's agent_exclusive assignment both call SetStatus for
// reasons that are NOT a mode decision and must never trip this) — callers
// that represent a real explicit ignore/un-ignore/triage action (
// handleSetIgnored, the set_chat_status MCP tool for its non-agent_exclusive
// values) call this alongside their own SetStatus call.
func (s *Store) MarkConfigManual(jid string) error {
	jid = StripDeviceSuffix(jid)
	_, err := s.db.Exec(`INSERT INTO chats (jid, config_level_source) VALUES (?, 'manual')
		ON CONFLICT(jid) DO UPDATE SET config_level_source = 'manual'`, jid)
	return err
}

// applyOriginDefaultIfUnset re-applies the M5 (ct-2026-07-22-1903) origin
// default (config_level_default_contact vs config_level_default_new) for
// jid — UNLESS its config_level_source is already 'manual' (an explicit
// owner decision, see MarkConfigManual's doc) or jid is a group (the origin
// axis is individuals-only, Citrino's ratified checkpoint). Called from
// TouchChat (every touch, not just creation — a later message from an
// already-known contact must keep re-affirming the contact default) and
// SetContactName (the contact_name->non-empty transition). Reads
// contact_name itself so neither caller has to compute is_contact — same
// criterion GET /api/chats already uses (read.go: ContactName != "").
func (s *Store) applyOriginDefaultIfUnset(jid string) error {
	if IsGroupJID(jid) {
		return nil
	}
	var source, contactName string
	err := s.db.QueryRow(`SELECT COALESCE(config_level_source,'default'), COALESCE(contact_name,'') FROM chats WHERE jid = ?`, jid).
		Scan(&source, &contactName)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if source == "manual" {
		return nil
	}
	settingKey := SettingConfigLevelDefaultNew
	if contactName != "" {
		settingKey = SettingConfigLevelDefaultContact
	}
	level, err := s.EffectiveConfigLevelDefault(settingKey)
	if err != nil {
		return err
	}
	return s.applyConfigLevelDefault(jid, level)
}

// EffectiveConfigLevelDefault reads settingKey (config_level_default_new or
// _contact) and substitutes "unattended" when unset — the safety default
// (ct-2026-07-22-2100, boss verbatim: "por defecto el agente NO atiende,
// hasta que configure explícitamente"). ONE place for this substitution,
// shared by applyOriginDefaultIfUnset (the pipeline effect) and the GET
// admin.go endpoints (so the header combo box shows "Desatendido" selected
// rather than defaulting to whatever option happens to be first in the
// list, and never lies about what the pipeline will actually do).
func (s *Store) EffectiveConfigLevelDefault(settingKey string) (string, error) {
	level, err := s.KVGet(settingKey)
	if err != nil {
		return "", err
	}
	if level == "" {
		return "unattended", nil
	}
	return level, nil
}

// applyConfigLevelDefault writes level's status/active/confirmation_mode
// columns directly — mirrors SetConfigLevel's own level->columns mapping
// (minus "boss", never valid here: a default is never the owner's
// identity) but bypasses SetIsBoss/SetActive/SetConfirmationMode entirely
// so it does NOT mark config_level_source 'manual' — the whole point is
// this stays silently re-appliable until a real owner decision freezes it.
func (s *Store) applyConfigLevelDefault(jid, level string) error {
	jid = StripDeviceSuffix(jid)
	switch level {
	case "auto":
		_, err := s.db.Exec(`UPDATE chats SET active = 1, confirmation_mode = 'none' WHERE jid = ?`, jid)
		return err
	case "confirm":
		_, err := s.db.Exec(`UPDATE chats SET active = 1, confirmation_mode = 'always' WHERE jid = ?`, jid)
		return err
	case "unattended":
		_, err := s.db.Exec(`UPDATE chats SET active = 0 WHERE jid = ?`, jid)
		return err
	case "ignored":
		_, err := s.db.Exec(`UPDATE chats SET status = 'ignored', active = 0 WHERE jid = ?`, jid)
		return err
	default:
		return fmt.Errorf("store: invalid config level default %q, want auto|confirm|unattended|ignored", level)
	}
}

// HistorySummary is the dashboard's "X de Y" — how many chats have at
// least one message on file, over the total number of chats. Replaced the
// old ON_DEMAND-worker-progress meaning (ct-2026-07-24-2004, history-sync
// regression: the worker was removed — WhatsApp only ever mirrors a
// partial slice of the phone's history to a companion device, by design,
// so "backfill progress" was never a real signal). The `history_state`
// column stays in the schema (dead weight, not worth a destructive
// migration) but nothing writes to it anymore — this is the honest
// replacement: depth the boss's own goal builds FORWARD, accumulating
// what arrives live, so "has at least one message" is real progress.
// realMessageSQL-filtered (ct-2026-07-29): a chat "has a message" only
// counts when that message carries real content — see realMessageSQL's
// doc. Same criterion as ChatJIDsWithMessages/BackupCounts, so this badge
// and the Chats-tab list never disagree on the number.
func (s *Store) HistorySummary() (withMessages, total int, err error) {
	err = s.db.QueryRow(`SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN EXISTS (SELECT 1 FROM messages m WHERE m.chat_jid = c.jid AND `+realMessageSQL+`) THEN 1 ELSE 0 END), 0)
		FROM chats c`).Scan(&total, &withMessages)
	return withMessages, total, err
}

const chatColumns = `jid, COALESCE(name,''), mode, last_ts, unread,
	active, archived, status, is_boss, is_approver,
	COALESCE(memory,''), COALESCE(context,''), COALESCE(rules,''),
	confirmation_mode, COALESCE(confirmer,''),
	COALESCE(description,''), COALESCE(group_invite_link,''),
	COALESCE(claimed_by,''), claimed_until, mode_source, COALESCE(contact_name,''),
	history_state, COALESCE(silence_reason,''), silence_at, config_level_source`

func scanChat(scan func(dest ...any) error) (Chat, error) {
	var c Chat
	var active, archived, isBoss, isApprover int
	err := scan(&c.JID, &c.Name, &c.Mode, &c.LastTS, &c.Unread, &active, &archived, &c.Status, &isBoss, &isApprover,
		&c.Memory, &c.Context, &c.Rules, &c.ConfirmationMode, &c.Confirmer,
		&c.Description, &c.GroupInviteLink, &c.ClaimedBy, &c.ClaimedUntil, &c.ModeSource, &c.ContactName,
		&c.HistoryState, &c.SilenceReason, &c.SilenceAt, &c.ConfigLevelSource)
	if err != nil {
		return Chat{}, err
	}
	c.Active = active != 0
	c.Archived = archived != 0
	c.IsBoss = isBoss != 0
	c.IsApprover = isApprover != 0
	return c, nil
}

func (s *Store) ListChats(limit int) ([]Chat, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT `+chatColumns+` FROM chats ORDER BY last_ts DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chat
	for rows.Next() {
		c, err := scanChat(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.enrichChat(&out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// GetChat returns a single chat by JID. ok is false if the chat doesn't exist.
func (s *Store) GetChat(jid string) (c Chat, ok bool, err error) {
	row := s.db.QueryRow(`SELECT `+chatColumns+` FROM chats WHERE jid = ?`, jid)
	c, err = scanChat(row.Scan)
	if err == sql.ErrNoRows {
		return Chat{}, false, nil
	}
	if err != nil {
		return Chat{}, false, err
	}
	if err := s.enrichChat(&c); err != nil {
		return Chat{}, false, err
	}
	return c, true, nil
}

// enrichChat fills Origin/LastSpeaker/LastModel — derived on read (never
// cached), so they're always correct with no write-path to keep in sync.
func (s *Store) enrichChat(c *Chat) error {
	origin, err := s.ChatOrigin(c.JID)
	if err != nil {
		return err
	}
	c.Origin = origin

	last, ok, err := s.LastMessage(c.JID)
	if err != nil {
		return err
	}
	if ok {
		if last.FromMe {
			c.LastSpeaker = "us"
		} else {
			c.LastSpeaker = "them"
		}
		c.LastText = last.Text
	}

	model, err := s.LastOutboundModel(c.JID)
	if err != nil {
		return err
	}
	c.LastModel = model

	// Resolve the claim to its EFFECTIVE value: an expired claim reads back
	// as unclaimed here, so no caller ever has to compare ClaimedUntil
	// against "now" itself — see effectiveClaim.
	c.ClaimedBy, c.ClaimedUntil = effectiveClaim(c.ClaimedBy, c.ClaimedUntil, time.Now().Unix())
	return nil
}

// effectiveClaim resolves a raw (claimedBy, claimedUntil) pair against now,
// treating an expired claim exactly as if it were never set — the single
// place this "has the TTL passed?" comparison happens, shared by enrichChat
// (Chat) and PendingChats (Pending) so the two can never drift.
func effectiveClaim(claimedBy string, claimedUntil, now int64) (string, int64) {
	if claimedBy == "" || claimedUntil <= now {
		return "", 0
	}
	return claimedBy, claimedUntil
}

// ChatOrigin derives where a chat came from: inbound_spoke (a real message
// exists, in EITHER direction — see realMessageSQL), group_discovered (in
// group_members but no real message), or synced_contact (no real message,
// not in any group).
//
// T18 (ct-2026-08-05-1243): used to check ONLY from_me=0 (inbound) — a chat
// the OWNER started that never got a reply read as group_discovered/
// synced_contact instead of the real conversation it is. Fixed to check
// both directions, reusing realMessageSQL verbatim — the exact criterion
// ChatJIDsWithMessages already uses (excludes protocol noise: receipts/
// reactions with no text/type, and status@broadcast), so "did this chat
// happen" can't drift into two different answers depending on which
// function you ask. The VALUE "inbound_spoke" is kept as-is on purpose
// (Citrino/boss: renaming ripples into list_chats/get_chat/
// decision-policy.md for no real gain) even though it no longer strictly
// means inbound — see those callers' own docs for what it actually means
// now, and note it says nothing about whose turn it is to reply (that's
// last_speaker, a separate field).
//
// T18B (ct-2026-08-05-1243, follow-up): the group check used to read
// chat_groups (AddGroupMember) — a table with NO production writer, ever
// (verified: only tests called AddGroupMember). group_discovered could
// never actually fire; everything fell through to synced_contact instead,
// and the label lied. Retired chat_groups entirely, rewired onto
// group_members (whatsmeow.seedGroups' real write path) — the same
// question, one table now, matching UpsertGroupMember's own doc. Known
// limitation, inherited rather than introduced: group_members only
// refreshes on connect/reconnect (or an explicit KickResync), not live per
// group-membership-change event — a brand-new group or member won't show
// here until the next one of those. Still strictly better than before
// (which never worked at all).
func (s *Store) ChatOrigin(jid string) (string, error) {
	var hasRealMessage bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM messages WHERE chat_jid = ? AND `+realMessageSQL+`)`, jid).Scan(&hasRealMessage); err != nil {
		return "", err
	}
	if hasRealMessage {
		return "inbound_spoke", nil
	}
	var inGroup bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM group_members WHERE member_jid = ?)`, jid).Scan(&inGroup); err != nil {
		return "", err
	}
	if inGroup {
		return "group_discovered", nil
	}
	return "synced_contact", nil
}

// EffectiveRules resolves a chat's rules through the hierarchy ("sin rules
// no actúa" stays intact — type/origin ARE rules, just applied in bulk):
// particular (chats.rules) -> by-type for a GROUP (rules_type_group) or
// by-ORIGIN for an INDIVIDUAL (M5, ct-2026-07-22-1903: contact vs new
// number, contact WINS — boss verbatim "si te habla un chat nuevo pero es
// contacto, tiene prioridad ser contacto ya guardado") -> "" (the agent
// does not act). The caller never sees the hierarchy — only this resolved
// value.
//
// T79 (ct-2026-08-27-2034, boss verbatim: "regla general ya no va por que
// no tiene a donde ir") removed the global-default tier this chain used to
// fall through to below the type/origin slot. The partition above it is
// exhaustive — a chat is a group or it isn't; if it isn't, it has a
// ContactName or it doesn't (status@broadcast and an unresolved @lid both
// land in the new-number bucket, verified) — so there was never a fourth
// case only the general tier could serve. rules_type_individual (the
// pre-M5 tier) was ALSO already dead — no longer read for individual chats
// since M5, the origin split replaced it — removed in the same contract.
func (s *Store) EffectiveRules(jid string) (string, error) {
	c, ok, err := s.GetChat(jid)
	if err != nil {
		return "", err
	}
	if ok && c.Rules != "" {
		return c.Rules, nil
	}

	if IsGroupJID(jid) {
		return s.KVGet(SettingRulesTypeGroup)
	}

	// Individual chat — is_contact criterion matches GET /api/chats' own
	// IsContact (read.go: ContactName != "").
	originKey := SettingRulesDefaultNewNumber
	if ok && c.ContactName != "" {
		originKey = SettingRulesDefaultContact
	}
	return s.KVGet(originKey)
}

// EffectiveAgentDefault resolves the default agent_id to dispatch a chat to
// when nothing more specific applies (T71, ct-2026-08-27-1410, boss: "poder
// asignar a agente por defecto para mensajes nuevos, para mensajes de
// contactos"; T72, ct-2026-08-27-1625, sums grupos/boss: "que en la parte
// de agentes se le asignen los mensajes nuevos, de contacto, de grupos o al
// boss"). Four types, same shape EffectiveRules already uses above (boss →
// GROUP → contact/new-number, most specific first — a boss chat is never a
// group, order between those two never actually matters, boss checked
// first anyway to mirror LevelFor's own precedence). Only that one tier
// each: the caller already checks agent_exclusive (particular, M4) before
// calling this for non-boss chats, and "" here means fall through to
// whatever general fallback the caller already has (PortFallback) — there
// is no separate agent-default-general key for any of the four types,
// PortFallback already is that tier.
func (s *Store) EffectiveAgentDefault(jid string) (string, error) {
	c, ok, err := s.GetChat(jid)
	if err != nil {
		return "", err
	}
	if ok && c.IsBoss {
		return s.KVGet(SettingAgentDefaultBoss)
	}
	if IsGroupJID(jid) {
		return s.KVGet(SettingAgentDefaultGroup)
	}
	originKey := SettingAgentDefaultNewNumber
	if ok && c.ContactName != "" {
		originKey = SettingAgentDefaultContact
	}
	return s.KVGet(originKey)
}

// SetTypeRules sets the "by type" rules tier for groups — the only type
// left since T79 (ct-2026-08-27-2034) removed rules_type_individual (dead
// since M5, replaced by the contact/new-number origin split) and the
// global default tier entirely. Privileged-only caller (REST) — never
// settable via MCP. Unlike SetChatRules (per-chat, unblocked since T31,
// ct-2026-08-06-0244), this is a broader, system-wide change the boss
// never asked to open.
func (s *Store) SetTypeRules(rules string) error {
	return s.KVSet(SettingRulesTypeGroup, rules)
}
