package store

import "database/sql"

type Message struct {
	ChatJID string `json:"chat_jid"`
	ID      string `json:"id"`
	FromMe  bool   `json:"from_me"`
	Sender  string `json:"sender"`
	Text    string `json:"text"`
	TS      int64  `json:"ts"`
	Type    string `json:"type"`

	// Model is which model sent this message (outbound only; empty/NULL for
	// inbound). Set by the caller before insert — no default is guessed here.
	Model string `json:"model,omitempty"`
	// DeliveredTS / ReadTS are WhatsApp receipt timestamps; 0 = no receipt yet.
	DeliveredTS int64 `json:"delivered_ts,omitempty"`
	ReadTS      int64 `json:"read_ts,omitempty"`
	// DecryptRetryAt (T35, ct-2026-08-08-1258): unix ts of the last WhatsApp
	// retry receipt for THIS outbound message — the device that received it
	// couldn't decrypt it. 0 = no such receipt. See MarkDecryptRetry.
	DecryptRetryAt int64 `json:"decrypt_retry_at,omitempty"`

	// QuotedID/QuotedPreview/Forwarded (ct-2026-07-21-1610, S6a): reply and
	// forward metadata read from the vendor's ContextInfo at ingestion time
	// (whatsmeow.detectReply) — "" / false for a message that is neither.
	QuotedID      string `json:"quoted_id,omitempty"`
	QuotedPreview string `json:"quoted_preview,omitempty"`
	Forwarded     bool   `json:"forwarded,omitempty"`

	// OriginTerminalID (T39, ct-2026-08-08-1619): the agent terminal that
	// sent this message via send_to_boss — "" for everything else (a normal
	// AI reply, a human via REST, inbound). Copied from outbox.
	// origin_terminal_id at send time (sentMessageRow) — not read anywhere
	// yet, it's what a later reply-routing feature (the owner answers by
	// quoting a message, it reaches THAT agent) will key off.
	OriginTerminalID string `json:"origin_terminal_id,omitempty"`
}

// AddMessage inserts a message, deduped by the messages PRIMARY KEY
// (chat_jid, id) — INSERT OR IGNORE makes a re-delivered/re-synced message
// (open-wa can redeliver on reconnect) a harmless no-op instead of an error
// or a duplicate row. Also touches the parent chat's name/last_ts.
//
// m.ChatJID is normalized (StripDeviceSuffix, T45) BEFORE it's used
// anywhere in this function — TouchChat below normalizes its own jid
// parameter too, but that's a local variable inside TouchChat; without
// normalizing m.ChatJID here first, the INSERT below would still write the
// device-qualified form into messages.chat_jid, orphaned from the
// now-normalized chats.jid it's supposed to join against.
func (s *Store) AddMessage(m Message) error {
	m.ChatJID = StripDeviceSuffix(m.ChatJID)
	if err := s.TouchChat(m.ChatJID, "", m.TS); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT OR IGNORE INTO messages
		(chat_jid, id, from_me, sender, text, ts, type, model, delivered_ts, read_ts,
		 quoted_id, quoted_preview, forwarded, origin_terminal_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ChatJID, m.ID, b2i(m.FromMe), m.Sender, m.Text, m.TS, m.Type,
		nullIfEmpty(m.Model), m.DeliveredTS, m.ReadTS,
		m.QuotedID, m.QuotedPreview, b2i(m.Forwarded), m.OriginTerminalID)
	return err
}

// SetDelivered records a WhatsApp delivery receipt timestamp for a message.
func (s *Store) SetDelivered(chatJID, id string, ts int64) error {
	_, err := s.db.Exec(`UPDATE messages SET delivered_ts = ? WHERE chat_jid = ? AND id = ?`, ts, chatJID, id)
	return err
}

// SetRead records a WhatsApp read receipt timestamp for a message.
func (s *Store) SetRead(chatJID, id string, ts int64) error {
	_, err := s.db.Exec(`UPDATE messages SET read_ts = ? WHERE chat_jid = ? AND id = ?`, ts, chatJID, id)
	return err
}

// PendingReadReceipts returns inbound messages still missing a read receipt
// (T91, ct-2026-08-28) — olderThan excludes anything too fresh to have had
// its FIRST mark-read attempt yet (corepipeline.Pipeline.MarkRead's own
// readDelay, or capipush's immediate post-dispatch call), so this only
// surfaces messages whose attempt already happened and failed (e.g. "mark
// read chat=...: websocket not connected", the T91 bug — no retry, the
// receipt was lost for good). Oldest first, capped at limit — the caller
// paces retries one chat at a time, this just answers "what's still due".
func (s *Store) PendingReadReceipts(olderThan int64, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT `+messageColumns+`
		FROM messages WHERE from_me = 0 AND read_ts = 0 AND ts <= ?
		ORDER BY ts ASC LIMIT ?`, olderThan, limit)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// MarkDecryptRetry records that a WhatsApp retry receipt came back for a
// message WE sent — the recipient's device got it but couldn't decrypt it
// (T35, ct-2026-08-08-1258). The receipt names a message ID we may or may
// not have persisted (e.g. one sent before this feature existed); an UPDATE
// that touches no row is simply nothing to mark, not an error.
func (s *Store) MarkDecryptRetry(chatJID, msgID string, ts int64) error {
	_, err := s.db.Exec(`UPDATE messages SET decrypt_retry_at = ? WHERE chat_jid = ? AND id = ?`, ts, chatJID, msgID)
	return err
}

const messageColumns = `chat_jid, id, from_me, COALESCE(sender,''),
	COALESCE(text,''), ts, COALESCE(type,''), COALESCE(model,''), delivered_ts, read_ts,
	COALESCE(quoted_id,''), COALESCE(quoted_preview,''), forwarded, decrypt_retry_at, origin_terminal_id`

func scanMessages(rows *sql.Rows) ([]Message, error) {
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		var fromMe, forwarded int
		if err := rows.Scan(&m.ChatJID, &m.ID, &fromMe, &m.Sender, &m.Text, &m.TS, &m.Type,
			&m.Model, &m.DeliveredTS, &m.ReadTS,
			&m.QuotedID, &m.QuotedPreview, &forwarded, &m.DecryptRetryAt, &m.OriginTerminalID); err != nil {
			return nil, err
		}
		m.FromMe = fromMe != 0
		m.Forwarded = forwarded != 0
		out = append(out, m)
	}
	return out, rows.Err()
}

// LastMessage returns the most recent message in a chat (by ts, with rowid
// as a tiebreaker for same-ts messages). ok is false if the chat has no
// messages yet.
func (s *Store) LastMessage(chatJID string) (m Message, ok bool, err error) {
	var fromMe, forwarded int
	err = s.db.QueryRow(`SELECT `+messageColumns+`
		FROM messages WHERE chat_jid = ? ORDER BY ts DESC, rowid DESC LIMIT 1`, chatJID).
		Scan(&m.ChatJID, &m.ID, &fromMe, &m.Sender, &m.Text, &m.TS, &m.Type, &m.Model, &m.DeliveredTS, &m.ReadTS,
			&m.QuotedID, &m.QuotedPreview, &forwarded, &m.DecryptRetryAt, &m.OriginTerminalID)
	if err == sql.ErrNoRows {
		return Message{}, false, nil
	}
	if err != nil {
		return Message{}, false, err
	}
	m.FromMe = fromMe != 0
	m.Forwarded = forwarded != 0
	return m, true, nil
}

// GetMessageByID looks up one message by its (chat_jid, id) primary key —
// T43 (ct-2026-08-08-2043)'s reply-routing lookup: given an inbound
// message's QuotedID, find the quoted row to read its OriginTerminalID.
// ok=false for no such row (id from before this chat's history window, or
// simply wrong) — never an error on its own.
func (s *Store) GetMessageByID(chatJID, id string) (m Message, ok bool, err error) {
	var fromMe, forwarded int
	err = s.db.QueryRow(`SELECT `+messageColumns+`
		FROM messages WHERE chat_jid = ? AND id = ?`, chatJID, id).
		Scan(&m.ChatJID, &m.ID, &fromMe, &m.Sender, &m.Text, &m.TS, &m.Type, &m.Model, &m.DeliveredTS, &m.ReadTS,
			&m.QuotedID, &m.QuotedPreview, &forwarded, &m.DecryptRetryAt, &m.OriginTerminalID)
	if err == sql.ErrNoRows {
		return Message{}, false, nil
	}
	if err != nil {
		return Message{}, false, err
	}
	m.FromMe = fromMe != 0
	m.Forwarded = forwarded != 0
	return m, true, nil
}

// LastOutboundModel returns messages.model of the most recent message this
// chat SENT (from_me=1) — which model gave the last reply, even if a newer
// inbound message has arrived since. "" if nothing has been sent yet.
func (s *Store) LastOutboundModel(chatJID string) (string, error) {
	var model string
	err := s.db.QueryRow(`SELECT COALESCE(model,'') FROM messages
		WHERE chat_jid = ? AND from_me = 1 ORDER BY ts DESC, rowid DESC LIMIT 1`, chatJID).Scan(&model)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return model, err
}

// ChatJIDsWithMessages returns the set of chat_jids that have at least one
// real message (inbound or outbound) — the dashboard's "1:1 con mensaje
// directo guardado" criterion (S1g, ct-2026-07-19-1801): a chat backfilled
// purely as a known WhatsApp contact (syncContacts, ts=0, never messaged)
// or manually whitelisted (handleWhitelistAdd, real ts but still no
// message) has no row here — only AddMessage ever inserts into `messages`.
// messages' PK is (chat_jid, id), so chat_jid already leads its own index —
// no new index needed for this to stay fast.
//
// realMessageSQL-filtered (ct-2026-07-29): a `messages` row with no real
// text/type is protocol noise, not a message — see realMessageSQL's doc.
func (s *Store) ChatJIDsWithMessages() (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT DISTINCT chat_jid FROM messages WHERE ` + realMessageSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var jid string
		if err := rows.Scan(&jid); err != nil {
			return nil, err
		}
		out[jid] = true
	}
	return out, rows.Err()
}

func (s *Store) GetMessages(jid string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT `+messageColumns+`
		FROM messages WHERE chat_jid = ? ORDER BY ts DESC LIMIT ?`, jid, limit)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}
