package store

import "database/sql"

type Outbox struct {
	Seq       int64  `json:"seq"`
	ToJID     string `json:"to_jid"`
	Text      string `json:"text"`
	CreatedTS int64  `json:"created_ts"`
	// Model is which model queued this reply (empty for a human-sent
	// REST/dashboard message). Carried through to messages.model once the
	// adapter actually sends it and knows the real WhatsApp message ID.
	Model string `json:"model,omitempty"`
	// RetryCount / NextRetryTS / LastError / DeadLetter: anti-ban retry
	// state (never resend in a tight loop). NextRetryTS is a backoff
	// deadline (0 = eligible immediately). DeadLetter=true means the drain
	// loop gave up after too many failures — the row stays for inspection,
	// never deleted, and is excluded from the send loop.
	RetryCount  int    `json:"retry_count"`
	NextRetryTS int64  `json:"next_retry_ts,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	DeadLetter  bool   `json:"dead_letter"`
	// OriginTerminalID (T39, ct-2026-08-08-1619): the agent terminal that
	// queued this via send_to_boss — "" for everything else. Copied onto
	// the resulting messages row once sent (sentMessageRow) so a later
	// reply-routing feature can find which agent to route the owner's
	// answer back to.
	OriginTerminalID string `json:"origin_terminal_id,omitempty"`
	// ChunksSent (T101, ct-2026-08-29-1651): how many pieces of a long,
	// split reply already went out successfully — 0 for an ordinary
	// (unsplit) item. A later chunk failing resumes from here instead of
	// re-sending pieces the contact already received. See
	// SetOutboxChunkProgress.
	ChunksSent int `json:"chunks_sent,omitempty"`
	// MediaPath/MediaMime/MediaKind (T122, ct-2026-09-02-2045): a photo
	// (T123: audio too) attached to this item — MediaPath is a file under
	// PIUMY_MEDIA_DIR, never the bytes themselves. MediaKind is "" for an
	// ordinary text item (empty means text, checked by processOutbox to
	// pick the send path — chunked text vs. a single gw.SendMedia call);
	// "photo" for an image. Text still carries the caption in Text — a
	// media item's caption isn't a separate field.
	MediaPath string `json:"media_path,omitempty"`
	MediaMime string `json:"media_mime,omitempty"`
	MediaKind string `json:"media_kind,omitempty"`
	// MediaSeconds (T123, ct-2026-09-02-2121): a voice note's duration —
	// 0 means unknown/omitted, never guessed. Meaningless for "photo".
	MediaSeconds int `json:"media_seconds,omitempty"`
}

// Enqueue queues an outbound message with no model attribution — for
// callers with no model concept (e.g. a human manually sending via
// REST/dashboard).
func (s *Store) Enqueue(toJID, text string, ts int64) error {
	return s.EnqueueWithModel(toJID, text, ts, "")
}

// EnqueueWithModel queues an outbound message the same way Enqueue does, but
// also records which model produced it (send_message via MCP). The model
// travels with the outbox row until the pipeline actually sends it, at which
// point it's copied onto the resulting messages row (with the real WhatsApp
// message ID, so delivery/read receipts can match it). Device suffixes are
// stripped (T52, ct-2026-08-10-1837) — symmetric with T45's normalization
// on the inbound path.
func (s *Store) EnqueueWithModel(toJID, text string, ts int64, model string) error {
	_, err := s.db.Exec(`INSERT INTO outbox (to_jid, text, created_ts, model) VALUES (?, ?, ?, ?)`,
		StripDeviceSuffix(toJID), text, ts, nullIfEmpty(model))
	return err
}

// EnqueueMediaWithModel queues an outbound MEDIA message (T122,
// ct-2026-09-02-2045) — same queue, model attribution and anti-ban pacing
// as EnqueueWithModel, plus the file path/mime/kind processOutbox needs to
// send it via gateway.SendMedia instead of the chunked-text path. caption
// travels in the same `text` column an ordinary message uses — a media
// item has no separate caption field. mediaPath must already point at a
// saved file (mediautil.SaveOutboundMedia) — this only records the
// reference, never touches the bytes. seconds (T123, ct-2026-09-02-2121)
// is a voice note's duration — 0 for "unknown/omitted" (never guessed)
// and always 0 for "photo", which has no duration concept.
func (s *Store) EnqueueMediaWithModel(toJID, caption string, mediaPath, mediaMime, mediaKind string, seconds int, ts int64, model string) error {
	_, err := s.db.Exec(`INSERT INTO outbox (to_jid, text, created_ts, model, media_path, media_mime, media_kind, media_seconds) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		StripDeviceSuffix(toJID), caption, ts, nullIfEmpty(model), mediaPath, mediaMime, mediaKind, seconds)
	return err
}

// EnqueueFromAgent queues an outbound message on behalf of a registered
// agent terminal (send_to_boss, T39, ct-2026-08-08-1619) — same queue as
// every other outbound path, paced by the same anti-ban governor on drain.
// originTerminalID travels to the resulting messages row once sent (see
// OriginTerminalID's own doc); it is never a model attribution, so this
// leaves outbox.model unset, unlike EnqueueWithModel. Device suffixes are
// stripped (T52, ct-2026-08-10-1837).
func (s *Store) EnqueueFromAgent(toJID, text string, ts int64, originTerminalID string) error {
	_, err := s.db.Exec(`INSERT INTO outbox (to_jid, text, created_ts, origin_terminal_id) VALUES (?, ?, ?, ?)`,
		StripDeviceSuffix(toJID), text, ts, originTerminalID)
	return err
}

const outboxColumns = `seq, to_jid, text, created_ts, COALESCE(model,''),
	retry_count, next_retry_ts, COALESCE(last_error,''), dead_letter, origin_terminal_id, chunks_sent,
	media_path, media_mime, media_kind, media_seconds`

func scanOutbox(rows *sql.Rows) ([]Outbox, error) {
	defer rows.Close()
	out := []Outbox{}
	for rows.Next() {
		var o Outbox
		var deadLetter int
		if err := rows.Scan(&o.Seq, &o.ToJID, &o.Text, &o.CreatedTS, &o.Model,
			&o.RetryCount, &o.NextRetryTS, &o.LastError, &deadLetter, &o.OriginTerminalID, &o.ChunksSent,
			&o.MediaPath, &o.MediaMime, &o.MediaKind, &o.MediaSeconds); err != nil {
			return nil, err
		}
		o.DeadLetter = deadLetter != 0
		out = append(out, o)
	}
	return out, rows.Err()
}

// PendingOutbox returns every unsent item (including ones still backing off
// or dead-lettered) — a plain "what's in the outbox" view for MCP/REST
// visibility. The pipeline's send loop uses DueOutbox instead, which
// filters out anything not eligible to send yet.
func (s *Store) PendingOutbox(limit int) ([]Outbox, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT `+outboxColumns+` FROM outbox
		WHERE sent = 0 ORDER BY seq ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	return scanOutbox(rows)
}

// DueOutbox returns unsent, non-dead-lettered items whose retry backoff (if
// any) has already elapsed as of now — what the pipeline's send loop should
// actually attempt this tick.
func (s *Store) DueOutbox(limit int, now int64) ([]Outbox, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT `+outboxColumns+` FROM outbox
		WHERE sent = 0 AND dead_letter = 0 AND next_retry_ts <= ?
		ORDER BY seq ASC LIMIT ?`, now, limit)
	if err != nil {
		return nil, err
	}
	return scanOutbox(rows)
}

// SetOutboxRetry records a failed send attempt: bumps retry_count, sets the
// backoff deadline (nextRetryTS) and the error, for inspection.
func (s *Store) SetOutboxRetry(seq int64, retryCount int, nextRetryTS int64, lastError string) error {
	_, err := s.db.Exec(`UPDATE outbox SET retry_count = ?, next_retry_ts = ?, last_error = ? WHERE seq = ?`,
		retryCount, nextRetryTS, lastError, seq)
	return err
}

// SetOutboxChunkProgress records how many pieces of a split reply already
// went out (T101, ct-2026-08-29-1651) — deliberately independent of
// SetOutboxRetry's retry_count/next_retry_ts/last_error: a chunk succeeding
// mid-sequence is not a retry event, and a later chunk failing must not
// erase progress already made. A crash between chunks resumes from
// whatever was last persisted here, never from 0.
func (s *Store) SetOutboxChunkProgress(seq int64, chunksSent int) error {
	_, err := s.db.Exec(`UPDATE outbox SET chunks_sent = ? WHERE seq = ?`, chunksSent, seq)
	return err
}

// DeadLetterOutbox marks an item as permanently failed — excluded from the
// send loop, but never deleted (stays for inspection; anti-ban means "don't
// resend forever," not "silently discard").
func (s *Store) DeadLetterOutbox(seq int64, lastError string) error {
	_, err := s.db.Exec(`UPDATE outbox SET dead_letter = 1, last_error = ? WHERE seq = ?`, lastError, seq)
	return err
}

func (s *Store) MarkSent(seq int64) error {
	_, err := s.db.Exec(`UPDATE outbox SET sent = 1 WHERE seq = ?`, seq)
	return err
}
