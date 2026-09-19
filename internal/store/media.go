package store

import (
	"database/sql"
	"errors"
	"strings"
)

// MaxMediaPendingAttempts caps how many times a media_pending row gets
// retried before NextMediaPending/MediaPendingForChat start skipping it
// (ct-2026-07-21-1437 parte 2, Citrino catch: a WhatsApp directPath expires
// after a while — likely for media the history worker brings back from OLD
// messages — so an uncapped retry would let one stale reference starve the
// entire FIFO queue behind it forever). Moved here from internal/whatsmeow
// (ct-2026-07-29) so restapi can share the exact same threshold when
// deciding whether a still-undownloaded attachment is "still trying" or
// "gave up" (see MediaPendingFailedMsgIDs), without restapi importing
// whatsmeow (same layering the rest of restapi's Deps interfaces already
// keep). Not config: a fixed small number, no reason for an owner to ever
// tune it.
const MaxMediaPendingAttempts = 3

// Media references a downloaded image/video/sticker on disk (never a DB
// blob — keeps the SQLite file small; the file itself is GC'd by size, while
// the message text/metadata row it points at is never deleted).
type Media struct {
	MsgID   string `json:"msg_id"`
	ChatJID string `json:"chat_jid"`
	// Path is what get_media/ListMedia show by default — the low-q JPEG
	// for images (F4-DESIGN §6: fewer tokens for the agent to interpret),
	// or the original as-is for non-image media (no low-q equivalent yet).
	Path string `json:"path"`
	// FullPath is always the original, uncompressed file — get_media_full
	// serves this. Equal to Path for non-image media (nothing to compress).
	FullPath string `json:"full_path"`
	Mime     string `json:"mime"`
	Size     int64  `json:"size"`
	TS       int64  `json:"ts"`
}

// AddMedia records a downloaded media file (the text/metadata row it belongs
// to lives in messages and is never deleted; only this row is GC-eligible).
func (s *Store) AddMedia(m Media) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO media (msg_id, chat_jid, path, full_path, mime, size, ts)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, m.MsgID, m.ChatJID, m.Path, m.FullPath, m.Mime, m.Size, m.TS)
	return err
}

func (s *Store) ListMedia(chatJID string, limit int) ([]Media, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT msg_id, chat_jid, path, full_path, COALESCE(mime,''), size, ts
		FROM media WHERE chat_jid = ? ORDER BY ts DESC LIMIT ?`, chatJID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Media{}
	for rows.Next() {
		var m Media
		if err := rows.Scan(&m.MsgID, &m.ChatJID, &m.Path, &m.FullPath, &m.Mime, &m.Size, &m.TS); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetMedia looks up a single media row by its primary key — get_media_full
// needs both chat_jid and msg_id (the actual PK) to avoid any ambiguity a
// bare msg_id lookup could have across chats.
func (s *Store) GetMedia(chatJID, msgID string) (m Media, ok bool, err error) {
	row := s.db.QueryRow(`SELECT msg_id, chat_jid, path, full_path, COALESCE(mime,''), size, ts
		FROM media WHERE chat_jid = ? AND msg_id = ?`, chatJID, msgID)
	if err := row.Scan(&m.MsgID, &m.ChatJID, &m.Path, &m.FullPath, &m.Mime, &m.Size, &m.TS); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Media{}, false, nil
		}
		return Media{}, false, err
	}
	return m, true, nil
}

// DeleteMedia removes a media row (used by the GC policy — size-only, text
// is never deleted). Does not touch the file on disk; the caller does that.
func (s *Store) DeleteMedia(chatJID, msgID string) error {
	_, err := s.db.Exec(`DELETE FROM media WHERE chat_jid = ? AND msg_id = ?`, chatJID, msgID)
	return err
}

// MediaMsgIDs returns the set of msg_ids that already have a downloaded
// media row for chatJID — a bulk membership check for a chat's message list
// (ct-2026-07-21-1358, popup backend), same "one query, not N" pattern as
// ChatJIDsWithMessages (message.go).
func (s *Store) MediaMsgIDs(chatJID string) (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT msg_id FROM media WHERE chat_jid = ?`, chatJID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// MediaKind classifies a messages.type value — the raw MIME string
// downloadAndStoreMedia/handleMessage store there for media (e.g.
// "image/jpeg"), NOT a category — into the popup's display kind
// (ct-2026-07-21-1358). ok is false for a non-media type: a plain
// text/e2e-notification/etc message carries whatsmeow's own frame-type
// keyword here instead (never contains "/"). Keep the prefixes here in
// sync with PendingMedia's SQL WHERE below — same classification, two
// forms (SQL pre-filter vs. Go-side kind label).
func MediaKind(mimeType string) (kind string, ok bool) {
	switch {
	case mimeType == "image/webp":
		return "sticker", true // stickers are always webp (detectMedia, media.go)
	case strings.HasPrefix(mimeType, "image/"):
		return "photo", true
	case strings.HasPrefix(mimeType, "audio/"):
		return "audio", true
	case strings.HasPrefix(mimeType, "video/"):
		return "video", true
	case strings.HasPrefix(mimeType, "application/"):
		return "doc", true
	}
	return "", false
}

// PendingMedia returns chatJID's media-type messages that don't have a
// downloaded media row yet, oldest first — the FIFO backlog the on-demand
// media worker drains one at a time (ct-2026-07-21-1358). The WHERE clause
// mirrors MediaKind's own prefixes (kept in sync, see its doc) so "pending"
// and "displayed kind" never disagree on what counts as media.
func (s *Store) PendingMedia(chatJID string) ([]Message, error) {
	rows, err := s.db.Query(`
		SELECT `+messageColumns+` FROM messages m
		WHERE m.chat_jid = ?
		  AND (m.type LIKE 'image/%' OR m.type LIKE 'video/%' OR m.type LIKE 'audio/%' OR m.type LIKE 'application/%')
		  AND NOT EXISTS (SELECT 1 FROM media WHERE media.chat_jid = m.chat_jid AND media.msg_id = m.id)
		ORDER BY m.ts ASC`, chatJID)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// MediaPending is a message's raw media download reference (ct-2026-07-21-1358
// parte 1) — exactly whatsmeow's DownloadableMessage shape
// (DirectPath/MediaKey/FileSHA256/FileEncSHA256) plus FileLength and the
// Mime/Kind the popup already needs, captured at INGESTION time (history
// sync, or a live message whose synchronous download failed) because the
// original protobuf carrying these is discarded the moment that call
// returns — there is no other way to get them back later. Prerequisite for
// any later on-demand/background download: without this row, a "pending"
// message (messages.type says it's media, no `media` row yet) has nothing
// to call client.DownloadMediaWithPath with.
type MediaPending struct {
	ChatJID string `json:"chat_jid"`
	MsgID   string `json:"msg_id"`
	Mime    string `json:"mime"`
	Kind    string `json:"kind"` // photo|audio|sticker|video|doc — MediaKind(Mime)
	// DirectPath/MediaKey/FileSHA256/FileEncSHA256: whatsmeow.DownloadableMessage
	// verbatim — MediaKey/FileSHA256/FileEncSHA256 are raw bytes (BLOB), never
	// derived from anything else.
	DirectPath    string `json:"direct_path"`
	MediaKey      []byte `json:"media_key,omitempty"`
	FileSHA256    []byte `json:"file_sha256,omitempty"`
	FileEncSHA256 []byte `json:"file_enc_sha256,omitempty"`
	// FileLength isn't part of DownloadableMessage (whatsmeow doesn't need it
	// to download) but every concrete media sub-message exposes it; kept for
	// a future progress display.
	FileLength uint64 `json:"file_length,omitempty"`
	TS         int64  `json:"ts"`
	// Attempts counts the background worker's failed download tries
	// (ct-2026-07-21-1437 parte 2) — NextMediaPending skips a row once this
	// reaches its maxAttempts argument, so a permanently stale reference
	// (an expired WhatsApp directPath) can't starve the FIFO queue behind
	// it. Reset to 0 on any AddMediaPending (a fresh capture deserves fresh
	// tries) — never incremented directly here, see
	// IncrementMediaPendingAttempts.
	Attempts int `json:"attempts,omitempty"`
}

// AddMediaPending records a message's download reference — the ingestion
// paths (persistHistoryMessage, downloadAndStoreMedia's failure branch,
// internal/whatsmeow) are the only callers. INSERT OR REPLACE: reprocessing
// the same message (a HistorySync chunk landing twice, a redelivered live
// message) is a harmless idempotent overwrite, same convention as AddMedia.
// attempts is always 0 on the fresh capture: if the URL is already expired
// (403/410), the media worker's permanent-error skip (mediabgworker.go,
// FailMediaPendingPermanently) marks it giving up in 1 attempt instead of
// 3 — no need to preserve the counter across re-captures.
func (s *Store) AddMediaPending(m MediaPending) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO media_pending
		(chat_jid, msg_id, mime, kind, direct_path, media_key, file_sha256, file_enc_sha256, file_length, ts, attempts)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		m.ChatJID, m.MsgID, m.Mime, m.Kind, m.DirectPath, m.MediaKey, m.FileSHA256, m.FileEncSHA256, m.FileLength, m.TS)
	return err
}

// NextMediaPending returns the globally oldest media_pending row with fewer
// than maxAttempts recorded failures — oldest ts across EVERY chat, not
// just one (ct-2026-07-21-1437 parte 2: the background worker's FIFO
// backlog is a single chronological queue, unlike PendingMedia's per-chat
// one). Rows at or past maxAttempts are skipped (not deleted — a later
// AddMediaPending re-capture still resets them) so a stale reference can't
// block the rows behind it. ok=false when nothing under the cap is left.
func (s *Store) NextMediaPending(maxAttempts int) (m MediaPending, ok bool, err error) {
	row := s.db.QueryRow(`SELECT chat_jid, msg_id, mime, kind, direct_path, media_key, file_sha256, file_enc_sha256, file_length, ts, attempts
		FROM media_pending WHERE attempts < ? ORDER BY ts ASC LIMIT 1`, maxAttempts)
	if err := row.Scan(&m.ChatJID, &m.MsgID, &m.Mime, &m.Kind, &m.DirectPath,
		&m.MediaKey, &m.FileSHA256, &m.FileEncSHA256, &m.FileLength, &m.TS, &m.Attempts); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MediaPending{}, false, nil
		}
		return MediaPending{}, false, err
	}
	return m, true, nil
}

// MediaPendingForChat returns chatJID's own media_pending backlog, oldest
// first, excluding rows at or past maxAttempts — the on-demand fetch's
// per-chat queue (ct-2026-07-21-1437 parte 3), same attempts cap
// NextMediaPending applies: retrying faster/in-parallel doesn't change
// whether a directPath is still valid, so a reference the background
// worker already gave up on isn't worth another try here either.
func (s *Store) MediaPendingForChat(chatJID string, maxAttempts int) ([]MediaPending, error) {
	rows, err := s.db.Query(`SELECT chat_jid, msg_id, mime, kind, direct_path, media_key, file_sha256, file_enc_sha256, file_length, ts, attempts
		FROM media_pending WHERE chat_jid = ? AND attempts < ? ORDER BY ts ASC`, chatJID, maxAttempts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MediaPending{}
	for rows.Next() {
		var m MediaPending
		if err := rows.Scan(&m.ChatJID, &m.MsgID, &m.Mime, &m.Kind, &m.DirectPath,
			&m.MediaKey, &m.FileSHA256, &m.FileEncSHA256, &m.FileLength, &m.TS, &m.Attempts); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// IncrementMediaPendingAttempts records one more failed download try for a
// row — called after downloadMediaPending fails, so the NEXT
// NextMediaPending call either retries it (still under the cap) or skips it
// (cap reached).
func (s *Store) IncrementMediaPendingAttempts(chatJID, msgID string) error {
	_, err := s.db.Exec(`UPDATE media_pending SET attempts = attempts + 1 WHERE chat_jid = ? AND msg_id = ?`, chatJID, msgID)
	return err
}

// FailMediaPendingPermanently marks a row as exhausted in ONE call — jumps
// attempts straight to MaxMediaPendingAttempts instead of the usual +1
// (ct-2026-07-29 fix: mediabgworker.go's permanent-error branch called
// IncrementMediaPendingAttempts, a plain +1, so a 403/410 — WhatsApp's CDN
// telling us the file is gone for good, never coming back regardless of how
// many more times we ask — still burned 2 more real retries before the cap
// actually took effect; measured on the boss's real log, repeated 403s for
// the SAME msg_id). Callers: mediabgworker.go/mediaworker.go's
// isPermanentMediaDownloadError branch — a row a fresh AddMediaPending
// re-capture still resets to 0, same as any other attempts cap reach.
func (s *Store) FailMediaPendingPermanently(chatJID, msgID string) error {
	_, err := s.db.Exec(`UPDATE media_pending SET attempts = ? WHERE chat_jid = ? AND msg_id = ?`, MaxMediaPendingAttempts, chatJID, msgID)
	return err
}

// MediaPendingFailedMsgIDs returns the set of msg_ids in chatJID's
// media_pending backlog that have exhausted MaxMediaPendingAttempts —
// WhatsApp's CDN copy is gone (403/410, FailMediaPendingPermanently) or
// every generic retry failed (IncrementMediaPendingAttempts reaching the
// cap). Neither NextMediaPending nor MediaPendingForChat will ever pick
// these up again; the row stays in the table as the honest "we gave up"
// record. The dashboard (restapi's handleMessages) needs this to stop
// showing "descargando…" for an attachment that will never arrive
// (ct-2026-07-29, boss: "los audios dicen descargando... un adjunto que
// falló con 403 no puede decir 'descargando' para siempre").
func (s *Store) MediaPendingFailedMsgIDs(chatJID string) (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT msg_id FROM media_pending WHERE chat_jid = ? AND attempts >= ?`, chatJID, MaxMediaPendingAttempts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// GetMediaPending looks up one message's stored download reference.
func (s *Store) GetMediaPending(chatJID, msgID string) (m MediaPending, ok bool, err error) {
	row := s.db.QueryRow(`SELECT chat_jid, msg_id, mime, kind, direct_path, media_key, file_sha256, file_enc_sha256, file_length, ts, attempts
		FROM media_pending WHERE chat_jid = ? AND msg_id = ?`, chatJID, msgID)
	if err := row.Scan(&m.ChatJID, &m.MsgID, &m.Mime, &m.Kind, &m.DirectPath,
		&m.MediaKey, &m.FileSHA256, &m.FileEncSHA256, &m.FileLength, &m.TS, &m.Attempts); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MediaPending{}, false, nil
		}
		return MediaPending{}, false, err
	}
	return m, true, nil
}

// DeleteMediaPending removes a reference once it's no longer needed
// (downloaded successfully, or the message/chat was cleaned up) — called
// after AddMedia succeeds so media_pending only ever holds truly-still-
// pending rows.
func (s *Store) DeleteMediaPending(chatJID, msgID string) error {
	_, err := s.db.Exec(`DELETE FROM media_pending WHERE chat_jid = ? AND msg_id = ?`, chatJID, msgID)
	return err
}
