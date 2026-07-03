// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
//
// Package store: switchboard persistence in SQLite (modernc.org/sqlite,
// pure Go — no cgo, builds and cross-compiles to ARM painlessly).
//
// It stores chats (with their auto/advanced mode), messages (including the
// unhandled "advanced" queue), and the outbox (messages to send, which the
// gateway drains while respecting the anti-ban governor).
package store

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

type Chat struct {
	JID    string `json:"jid"`
	Name   string `json:"name"`
	Mode   string `json:"mode"`
	LastTS int64  `json:"last_ts"`
	Unread int    `json:"unread"`

	// Active: Piumy's own attribute — is the agent allowed to handle this
	// chat? Default false (anti-ban: silent until explicitly marked active).
	Active bool `json:"active"`
	// Archived: WhatsApp's own attribute (independent of Active — a chat can
	// be archived on WhatsApp and still active for Piumy, or vice versa).
	Archived bool `json:"archived"`
	// Status is triage, not permission: whitelist|blacklist|new|ignored|
	// agent_exclusive:<id>. Distinct from router.json's whitelist (the
	// anti-ban inbound gate) — this never replaces it.
	Status string `json:"status"`

	// IsBoss marks this chat as the trusted owner — the agent takes
	// instructions from / escalates to this contact. READ-ONLY for agents:
	// settable only via the privileged REST/dashboard path (SetIsBoss),
	// never via an MCP tool — an agent must never be able to set a contact's
	// is_boss flag itself (2026-07-01).
	IsBoss bool `json:"is_boss"`

	// Origin is where this chat came from — derived on read (never cached,
	// always correct): inbound_spoke (has ≥1 inbound message — the contact
	// spoke), group_discovered (in chat_groups but never spoke 1-1), or
	// synced_contact (empty chat, not in any group — came from contact sync).
	Origin string `json:"origin"`
	// LastSpeaker is "them" or "us" (from_me) for the chat's most recent
	// message, or "" if the chat has no messages yet. Ties into the owner's
	// rule that the agent must NOT always have the last word.
	LastSpeaker string `json:"last_speaker,omitempty"`
	// LastModel is messages.model of the most recent OUTBOUND message in this
	// chat — which model gave the last reply, even if the contact has since
	// sent a newer inbound message that hasn't been answered yet. Empty if
	// nothing has ever been sent to this chat.
	LastModel string `json:"last_model,omitempty"`

	// Memory: particular facts learned about this contact (real name,
	// purchases, preferences). The agent CAN write this via MCP — the system
	// is meant to learn/build it (2026-07-01).
	Memory string `json:"memory,omitempty"`
	// Context: general/explanatory situation of the relationship — broader
	// than Memory's discrete facts. Agent-writable via MCP, same as Memory.
	Context string `json:"context,omitempty"`
	// Rules: behavior instructions for how the AI should treat this specific
	// chat — "like a skill". READ-ONLY for agents: settable
	// only via the privileged REST path, same trust gate as IsBoss — an
	// agent must never rewrite the rules it's judged against.
	Rules string `json:"rules,omitempty"`

	// ConfirmationMode: this chat's confirmation BASELINE — "required" or
	// "none". Defaulted BY TYPE in TouchChat (0810): a WhatsApp group starts
	// "required" (confirm before sending), a 1-1 chat starts "none" (reply
	// on its own). Owner-overridable via the privileged REST path. The
	// bridge's read of this chat's rules can still flip away from this
	// baseline for a specific reply, in either direction — this is only the
	// starting point, not an absolute lock. The owner's own term: confirmation,
	// NOT "supervision" (reviewing every reply).
	ConfirmationMode string `json:"confirmation_mode"`
	// Confirmer: JID to direct a held reply's confirmation to. Empty means
	// the default (the owner). A dynamic confirmer parsed from rules by the
	// bridge (e.g. "if stock, confirm with the warehouse guy, number X")
	// always takes precedence over this stored default.
	Confirmer string `json:"confirmer,omitempty"`

	// Description: a group's topic (WhatsApp's own field, can be long and
	// carry links) — populated by the gateway from sync/push events. Empty
	// for a 1-1 chat. Also "a note of your own about the chat" —
	// settable via the privileged REST path too (0130).
	Description string `json:"description,omitempty"`
	// GroupInviteLink: a group's invite link — read-only, populated by the
	// gateway (best-effort: requires being a group admin). Empty for a 1-1
	// chat or a group the gateway isn't admin on. No REST/MCP setter — it
	// only ever comes from WhatsApp (0130).
	GroupInviteLink string `json:"group_invite_link,omitempty"`

	// ClaimedBy / ClaimedUntil: a transient, TTL-based lock so two connected
	// MCP agents/models don't both work this chat at once (gap #6
	// "evitar doble-atención"). Identified by "model" — the same identity
	// send_message already requires — set via claim_chat/release_chat, NOT
	// the persistent Status="agent_exclusive:<id>" triage label (that one is
	// owner-set and unrelated; see validChatStatus's doc comment). Already
	// EFFECTIVE by the time a caller sees it (GetChat/ListChats/PendingChats
	// zero these out once ClaimedUntil has passed — see effectiveClaim) so
	// no caller ever needs to compare timestamps itself.
	ClaimedBy    string `json:"claimed_by,omitempty"`
	ClaimedUntil int64  `json:"claimed_until,omitempty"`
}

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
}

type Outbox struct {
	Seq       int64  `json:"seq"`
	ToJID     string `json:"to_jid"`
	Text      string `json:"text"`
	CreatedTS int64  `json:"created_ts"`
	// Model is which model queued this reply (empty for a human-sent
	// REST/dashboard message). Carried through to messages.model once the
	// gateway actually sends it and knows the real WhatsApp message ID.
	Model string `json:"model,omitempty"`
	// RetryCount / NextRetryTS / LastError / DeadLetter: anti-ban retry
	// state (never resend in a tight loop). NextRetryTS is a backoff
	// deadline (0 = eligible immediately). DeadLetter=true means the gateway
	// gave up after too many failures — the row stays for inspection, never
	// deleted, and is excluded from the send loop.
	RetryCount  int    `json:"retry_count"`
	NextRetryTS int64  `json:"next_retry_ts,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	DeadLetter  bool   `json:"dead_letter"`
}

// Media references a downloaded image/video/sticker on disk (never a DB
// blob — keeps the SQLite file small; the file itself is GC'd by size, while
// the message text/metadata row it points at is never deleted).
type Media struct {
	MsgID   string `json:"msg_id"`
	ChatJID string `json:"chat_jid"`
	Path    string `json:"path"`
	Mime    string `json:"mime"`
	Size    int64  `json:"size"`
	TS      int64  `json:"ts"`
}

// Draft is an auto-reply worker's candidate reply — NOT the outbox. A draft
// only ever reaches the outbox (and gets sent) once approved via the
// privileged path (a separate, later piece); nothing here queues it for
// actual sending.
type Draft struct {
	ID        int64  `json:"id"`
	ChatJID   string `json:"chat_jid"`
	Text      string `json:"text"`
	Model     string `json:"model,omitempty"`
	CreatedTS int64  `json:"created_ts"`
	Status    string `json:"status"` // pending | approved | discarded
	// Confirmer: who this draft's confirmation is directed to (a JID) — set
	// when the auto-reply worker holds a reply because the chat/rules
	// require confirmation (0748). Empty for a draft with no confirmer
	// resolved (falls back to the owner).
	Confirmer string `json:"confirmer,omitempty"`
}

const schema = `
CREATE TABLE IF NOT EXISTS chats (
  jid     TEXT PRIMARY KEY,
  name    TEXT,
  mode    TEXT    NOT NULL DEFAULT 'auto',
  last_ts INTEGER NOT NULL DEFAULT 0,
  unread  INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS messages (
  chat_jid TEXT    NOT NULL,
  id       TEXT    NOT NULL,
  from_me  INTEGER NOT NULL,
  sender   TEXT,
  text     TEXT,
  ts       INTEGER NOT NULL,
  type     TEXT,
  handled  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (chat_jid, id)
);
CREATE TABLE IF NOT EXISTS outbox (
  seq        INTEGER PRIMARY KEY AUTOINCREMENT,
  to_jid     TEXT    NOT NULL,
  text       TEXT    NOT NULL,
  created_ts INTEGER NOT NULL,
  sent       INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS kv (key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE IF NOT EXISTS chat_groups (
  member_jid TEXT NOT NULL,
  group_jid  TEXT NOT NULL,
  UNIQUE(member_jid, group_jid)
);
CREATE TABLE IF NOT EXISTS media (
  msg_id   TEXT    NOT NULL,
  chat_jid TEXT    NOT NULL,
  path     TEXT    NOT NULL,
  mime     TEXT,
  size     INTEGER NOT NULL DEFAULT 0,
  ts       INTEGER NOT NULL,
  PRIMARY KEY (chat_jid, msg_id)
);
CREATE TABLE IF NOT EXISTS drafts (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  chat_jid   TEXT    NOT NULL,
  text       TEXT    NOT NULL,
  model      TEXT,
  created_ts INTEGER NOT NULL,
  status     TEXT    NOT NULL DEFAULT 'pending'
);
`

// columnMigrations backfills columns added after the initial schema onto
// chats/messages/outbox tables that may already exist on a deployed DB
// (CREATE TABLE IF NOT EXISTS is a no-op there — it never adds columns).
var columnMigrations = []string{
	`ALTER TABLE chats ADD COLUMN active INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE chats ADD COLUMN archived INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE chats ADD COLUMN status TEXT NOT NULL DEFAULT 'new'`,
	`ALTER TABLE messages ADD COLUMN model TEXT`,
	`ALTER TABLE messages ADD COLUMN delivered_ts INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE messages ADD COLUMN read_ts INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE outbox ADD COLUMN model TEXT`,
	`ALTER TABLE outbox ADD COLUMN retry_count INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE outbox ADD COLUMN next_retry_ts INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE outbox ADD COLUMN last_error TEXT`,
	`ALTER TABLE outbox ADD COLUMN dead_letter INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE chats ADD COLUMN is_boss INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE chats ADD COLUMN memory TEXT`,
	`ALTER TABLE chats ADD COLUMN context TEXT`,
	`ALTER TABLE chats ADD COLUMN rules TEXT`,
	`ALTER TABLE chats ADD COLUMN confirmer TEXT`,
	`ALTER TABLE drafts ADD COLUMN confirmer TEXT`,
	`ALTER TABLE chats ADD COLUMN description TEXT`,
	`ALTER TABLE chats ADD COLUMN group_invite_link TEXT`,
	`ALTER TABLE chats ADD COLUMN claimed_by TEXT`,
	`ALTER TABLE chats ADD COLUMN claimed_until INTEGER NOT NULL DEFAULT 0`,
}

// confirmationModeMigration adds chats.confirmation_mode with a flat
// 'required' default — correct for groups, wrong for 1-1 chats (0810's
// default is BY TYPE). A single ALTER...DEFAULT can't express a per-row
// value, so migrate() runs one follow-up backfill UPDATE, but only the
// FIRST time this exact ALTER succeeds (see migrate).
const confirmationModeMigration = `ALTER TABLE chats ADD COLUMN confirmation_mode TEXT NOT NULL DEFAULT 'required'`

// migrate applies columnMigrations. Idempotent: SQLite has no "ADD COLUMN IF
// NOT EXISTS", so a rerun's "duplicate column name" error is expected on
// every statement and ignored; any other error is real and returned.
func migrate(db *sql.DB) error {
	for _, ddl := range columnMigrations {
		if _, err := db.Exec(ddl); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return err
		}
	}
	return migrateConfirmationMode(db)
}

// migrateConfirmationMode adds chats.confirmation_mode and backfills 1-1
// chats away from the ALTER's flat 'required' default to 'none' (0810),
// ATOMICALLY (3rd commandment: the Pi can lose power mid-migration). SQLite
// supports transactional DDL, so the ALTER + backfill run in ONE
// transaction: either both land or neither does. A power cut between them
// is impossible — a cut during the tx just rolls back entirely, and the
// column not existing on the next start makes this whole function re-run
// from scratch, same as any other idempotent migration here. Without the
// transaction, a cut right between the two statements would leave the
// column created but pre-existing 1-1 chats stuck at 'required' forever
// (TouchChat never touches an existing row's confirmation_mode).
func migrateConfirmationMode(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op if Commit already ran

	if _, err := tx.Exec(confirmationModeMigration); err != nil {
		if strings.Contains(err.Error(), "duplicate column name") {
			return nil // already migrated on a prior run — nothing to backfill
		}
		return err
	}
	if _, err := tx.Exec(`UPDATE chats SET confirmation_mode = 'none' WHERE jid NOT LIKE '%@g.us'`); err != nil {
		return err
	}
	return tx.Commit()
}

func Open(path string) (*Store, error) {
	// Power-loss resilience (3rd commandment): the Pi has no safe shutdown.
	// WAL + synchronous=NORMAL survives a sudden power cut without corrupting the
	// DB; busy_timeout avoids "database is locked" under concurrent access. Use
	// modernc's `_pragma=name(val)` DSN form so these apply to EVERY pooled
	// connection (a one-off PRAGMA Exec would only affect a single connection).
	dsn := "file:" + path +
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TouchChat creates/updates a chat (name + last timestamp) without touching
// the mode. A group JID SEEN FOR THE FIRST TIME defaults to status
// "ignored" instead of the regular "new" (0800: groups are chats too, but
// the AI does nothing with one until the owner hands out rules and un-ignores
// it) — ON CONFLICT never touches status, so this never overwrites one the
// owner already set. Confirmation default is also by type (0810): a 1-1
// chat starts "none" (replies on its own once it has rules); a group starts
// "required" (drafts, but a reply still needs confirmation before sending) —
// same ON CONFLICT protection, never overwrites an owner override.
func (s *Store) TouchChat(jid, name string, ts int64) error {
	status := "new"
	confirmMode := "none"
	if isGroupJID(jid) {
		status = "ignored"
		confirmMode = "required"
	}
	_, err := s.db.Exec(`INSERT INTO chats (jid, name, last_ts, status, confirmation_mode) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(jid) DO UPDATE SET
			name = CASE WHEN excluded.name != '' THEN excluded.name ELSE chats.name END,
			last_ts = MAX(chats.last_ts, excluded.last_ts)`, jid, name, ts, status, confirmMode)
	return err
}

// isGroupJID reports whether jid is a WhatsApp group chat (per the JID
// suffix convention, @g.us vs @s.whatsapp.net) — mirrors the same-named
// helpers in mcpserver/autoreply; store has no dependency on either package
// so it needs its own.
func isGroupJID(jid string) bool {
	return strings.HasSuffix(jid, "@g.us")
}

// SetMode sets a chat's mode (auto | advanced). Creates the chat if missing.
func (s *Store) SetMode(jid, mode string) error {
	_, err := s.db.Exec(`INSERT INTO chats (jid, mode) VALUES (?, ?)
		ON CONFLICT(jid) DO UPDATE SET mode = excluded.mode`, jid, mode)
	return err
}

// SetActive marks whether Piumy's agent is allowed to handle this chat.
// Distinct from Archived (WhatsApp's own flag) and from router.json's
// whitelist (the anti-ban inbound gate) — this never replaces either.
func (s *Store) SetActive(jid string, active bool) error {
	_, err := s.db.Exec(`INSERT INTO chats (jid, active) VALUES (?, ?)
		ON CONFLICT(jid) DO UPDATE SET active = excluded.active`, jid, b2i(active))
	return err
}

// SetArchived records WhatsApp's own archived flag for a chat.
func (s *Store) SetArchived(jid string, archived bool) error {
	_, err := s.db.Exec(`INSERT INTO chats (jid, archived) VALUES (?, ?)
		ON CONFLICT(jid) DO UPDATE SET archived = excluded.archived`, jid, b2i(archived))
	return err
}

// SetStatus sets a chat's triage status (whitelist|blacklist|new|ignored|
// agent_exclusive:<id>). Creates the chat if missing.
func (s *Store) SetStatus(jid, status string) error {
	_, err := s.db.Exec(`INSERT INTO chats (jid, status) VALUES (?, ?)
		ON CONFLICT(jid) DO UPDATE SET status = excluded.status`, jid, status)
	return err
}

// ClaimChat attempts to claim jid for model, valid until now+ttl
// ("avoid double-attention" between multiple MCP agents/
// models working the same queue; see the Chat.ClaimedBy doc comment for why
// this is separate from status="agent_exclusive:<id>"). Succeeds (true) if
// the chat is unclaimed, already claimed by this SAME model (renews —
// idempotent, lets an agent re-claim periodically to extend its own hold
// instead of needing a separate "renew" call), or the existing claim has
// already expired. Fails (false, nil error — an ordinary "someone else has
// it" outcome, not a Go error) if actively claimed by a DIFFERENT model.
// Deliberately UPDATE-only (no upsert, unlike SetStatus/SetActive/etc.):
// claiming a chat that doesn't exist is meaningless, so the caller (the
// claim_chat tool) checks existence itself first and reports "chat not
// found" rather than a misleading "claimed by someone else". The single
// WHERE clause makes the claim atomic against a concurrent attempt from
// another connection: two simultaneous ClaimChat calls both race the same
// UPDATE, and SQLite's single-writer semantics let only one of them
// actually match a still-open row — whichever runs first wins, the other
// legitimately sees 0 rows affected.
func (s *Store) ClaimChat(jid, model string, ttl time.Duration) (bool, error) {
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
// claim (lock semantics: only the current owner releases). A release for a
// chat that isn't claimed by model — because it expired, was never claimed,
// or is held by someone else — is a harmless no-op, not an error.
func (s *Store) ReleaseChat(jid, model string) error {
	_, err := s.db.Exec(`UPDATE chats SET claimed_by = '', claimed_until = 0
		WHERE jid = ? AND claimed_by = ?`, jid, model)
	return err
}

// SetIsBoss marks/unmarks a chat as the trusted owner. Deliberately has no
// MCP tool wired to it anywhere — only callable from the privileged REST
// path (API key / dashboard session), never by an agent
// (2026-07-01: an agent must not be able to set a contact's is_boss flag itself).
func (s *Store) SetIsBoss(jid string, isBoss bool) error {
	_, err := s.db.Exec(`INSERT INTO chats (jid, is_boss) VALUES (?, ?)
		ON CONFLICT(jid) DO UPDATE SET is_boss = excluded.is_boss`, jid, b2i(isBoss))
	return err
}

// SetChatMemory sets a chat's memory (particular facts learned about the
// contact). Agent-writable via MCP — the system is meant to learn/build this.
func (s *Store) SetChatMemory(jid, memory string) error {
	_, err := s.db.Exec(`INSERT INTO chats (jid, memory) VALUES (?, ?)
		ON CONFLICT(jid) DO UPDATE SET memory = excluded.memory`, jid, memory)
	return err
}

// SetChatContext sets a chat's context (general/explanatory situation of the
// relationship). Agent-writable via MCP, same as SetChatMemory.
func (s *Store) SetChatContext(jid, context string) error {
	_, err := s.db.Exec(`INSERT INTO chats (jid, context) VALUES (?, ?)
		ON CONFLICT(jid) DO UPDATE SET context = excluded.context`, jid, context)
	return err
}

// SetChatRules sets a chat's rules — behavior instructions for how the AI
// should treat this specific chat ("like a skill"). Deliberately has no MCP
// tool wired to it anywhere — only callable from the privileged REST path,
// same trust gate as SetIsBoss (an agent must not rewrite the rules it's
// judged against).
func (s *Store) SetChatRules(jid, rules string) error {
	_, err := s.db.Exec(`INSERT INTO chats (jid, rules) VALUES (?, ?)
		ON CONFLICT(jid) DO UPDATE SET rules = excluded.rules`, jid, rules)
	return err
}

// SetConfirmationMode sets a chat's confirmation baseline: "required" or
// "none" (defaulted by type in TouchChat — 0810). See Chat.ConfirmationMode.
func (s *Store) SetConfirmationMode(jid, mode string) error {
	_, err := s.db.Exec(`INSERT INTO chats (jid, confirmation_mode) VALUES (?, ?)
		ON CONFLICT(jid) DO UPDATE SET confirmation_mode = excluded.confirmation_mode`, jid, mode)
	return err
}

// SetConfirmer sets who a held reply's confirmation is directed to by
// default for this chat (a JID; empty falls back to the owner). See
// Chat.Confirmer (0748).
func (s *Store) SetConfirmer(jid, confirmer string) error {
	_, err := s.db.Exec(`INSERT INTO chats (jid, confirmer) VALUES (?, ?)
		ON CONFLICT(jid) DO UPDATE SET confirmer = excluded.confirmer`, jid, confirmer)
	return err
}

// SetChatDescription sets a group's topic/description (0130). Also usable
// as "a note of your own about the chat" via the
// privileged REST path; the gateway itself calls this from WhatsApp's own
// sync/push events.
func (s *Store) SetChatDescription(jid, description string) error {
	_, err := s.db.Exec(`INSERT INTO chats (jid, description) VALUES (?, ?)
		ON CONFLICT(jid) DO UPDATE SET description = excluded.description`, jid, description)
	return err
}

// SetGroupInviteLink sets a group's invite link (0130). Only ever called
// from the gateway (WhatsApp is the sole source of truth) — no REST/MCP
// setter exists for this field.
func (s *Store) SetGroupInviteLink(jid, link string) error {
	_, err := s.db.Exec(`INSERT INTO chats (jid, group_invite_link) VALUES (?, ?)
		ON CONFLICT(jid) DO UPDATE SET group_invite_link = excluded.group_invite_link`, jid, link)
	return err
}

func (s *Store) AddMessage(m Message) error {
	if err := s.TouchChat(m.ChatJID, "", m.TS); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT OR IGNORE INTO messages
		(chat_jid, id, from_me, sender, text, ts, type, model, delivered_ts, read_ts)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ChatJID, m.ID, b2i(m.FromMe), m.Sender, m.Text, m.TS, m.Type,
		nullIfEmpty(m.Model), m.DeliveredTS, m.ReadTS)
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

// nullIfEmpty maps "" to a real SQL NULL so Model stays unset (not "") for
// inbound messages that never pass a model.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Store) ListChats(limit int) ([]Chat, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT jid, COALESCE(name,''), mode, last_ts, unread,
		active, archived, status, is_boss,
		COALESCE(memory,''), COALESCE(context,''), COALESCE(rules,''),
		confirmation_mode, COALESCE(confirmer,''),
		COALESCE(description,''), COALESCE(group_invite_link,''),
		COALESCE(claimed_by,''), claimed_until
		FROM chats ORDER BY last_ts DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Chat{}
	for rows.Next() {
		var c Chat
		var active, archived, isBoss int
		if err := rows.Scan(&c.JID, &c.Name, &c.Mode, &c.LastTS, &c.Unread, &active, &archived, &c.Status, &isBoss,
			&c.Memory, &c.Context, &c.Rules, &c.ConfirmationMode, &c.Confirmer,
			&c.Description, &c.GroupInviteLink, &c.ClaimedBy, &c.ClaimedUntil); err != nil {
			return nil, err
		}
		c.Active = active != 0
		c.Archived = archived != 0
		c.IsBoss = isBoss != 0
		if err := s.enrichChat(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetChat returns a single chat by JID. ok is false if the chat doesn't exist.
func (s *Store) GetChat(jid string) (c Chat, ok bool, err error) {
	var active, archived, isBoss int
	err = s.db.QueryRow(`SELECT jid, COALESCE(name,''), mode, last_ts, unread,
		active, archived, status, is_boss,
		COALESCE(memory,''), COALESCE(context,''), COALESCE(rules,''),
		confirmation_mode, COALESCE(confirmer,''),
		COALESCE(description,''), COALESCE(group_invite_link,''),
		COALESCE(claimed_by,''), claimed_until
		FROM chats WHERE jid = ?`, jid).
		Scan(&c.JID, &c.Name, &c.Mode, &c.LastTS, &c.Unread, &active, &archived, &c.Status, &isBoss,
			&c.Memory, &c.Context, &c.Rules, &c.ConfirmationMode, &c.Confirmer,
			&c.Description, &c.GroupInviteLink, &c.ClaimedBy, &c.ClaimedUntil)
	if err == sql.ErrNoRows {
		return Chat{}, false, nil
	}
	if err != nil {
		return Chat{}, false, err
	}
	c.Active = active != 0
	c.Archived = archived != 0
	c.IsBoss = isBoss != 0
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
	}

	model, err := s.LastOutboundModel(c.JID)
	if err != nil {
		return err
	}
	c.LastModel = model

	// Resolve the claim to its EFFECTIVE value: an expired claim
	// reads back as unclaimed here, so no caller (agent or Go code) ever has
	// to compare ClaimedUntil against "now" itself — see effectiveClaim.
	c.ClaimedBy, c.ClaimedUntil = effectiveClaim(c.ClaimedBy, c.ClaimedUntil, time.Now().Unix())
	return nil
}

// effectiveClaim resolves a raw (claimedBy, claimedUntil) pair against now,
// treating an expired claim exactly as if it were never set — the
// single place this "has the TTL passed?" comparison happens, shared by
// enrichChat (Chat) and PendingChats (Pending) so the two can never drift.
func effectiveClaim(claimedBy string, claimedUntil, now int64) (string, int64) {
	if claimedBy == "" || claimedUntil <= now {
		return "", 0
	}
	return claimedBy, claimedUntil
}

// ChatOrigin derives where a chat came from: inbound_spoke (has ≥1 inbound
// message — the contact spoke), group_discovered (in chat_groups but never
// spoke 1-1), or synced_contact (empty chat, not in any group — came from
// the contact sync).
func (s *Store) ChatOrigin(jid string) (string, error) {
	var spoke bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM messages WHERE chat_jid = ? AND from_me = 0)`, jid).Scan(&spoke); err != nil {
		return "", err
	}
	if spoke {
		return "inbound_spoke", nil
	}
	var inGroup bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM chat_groups WHERE member_jid = ?)`, jid).Scan(&inGroup); err != nil {
		return "", err
	}
	if inGroup {
		return "group_discovered", nil
	}
	return "synced_contact", nil
}

// LastMessage returns the most recent message in a chat (by ts, with rowid
// as a tiebreaker for same-ts messages). ok is false if the chat has no
// messages yet.
func (s *Store) LastMessage(chatJID string) (m Message, ok bool, err error) {
	var fromMe int
	err = s.db.QueryRow(`SELECT chat_jid, id, from_me, COALESCE(sender,''),
		COALESCE(text,''), ts, COALESCE(type,''), COALESCE(model,''), delivered_ts, read_ts
		FROM messages WHERE chat_jid = ? ORDER BY ts DESC, rowid DESC LIMIT 1`, chatJID).
		Scan(&m.ChatJID, &m.ID, &fromMe, &m.Sender, &m.Text, &m.TS, &m.Type, &m.Model, &m.DeliveredTS, &m.ReadTS)
	if err == sql.ErrNoRows {
		return Message{}, false, nil
	}
	if err != nil {
		return Message{}, false, err
	}
	m.FromMe = fromMe != 0
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

// Pending is a chat waiting for a reply: its most recent message is inbound
// (from_me=0) — the contact has the last word. The golden rule: the
// agent must NOT always have the last word, so a chat where WE spoke last is
// never pending, no matter how long ago that was.
type Pending struct {
	JID     string `json:"jid"`
	Name    string `json:"name"`
	Origin  string `json:"origin"`
	Mode    string `json:"mode"`
	Status  string `json:"status"`
	Active  bool   `json:"active"`
	LastTS  int64  `json:"last_ts"`
	AgeSec  int64  `json:"age_sec"`
	Preview string `json:"preview"`

	// ClaimedBy / ClaimedUntil: same effective (already-expiry-resolved)
	// claim state as Chat's — see its doc comment. Additive: a
	// solo agent that never calls claim_chat always sees these empty/zero,
	// same as before this field existed.
	ClaimedBy    string `json:"claimed_by,omitempty"`
	ClaimedUntil int64  `json:"claimed_until,omitempty"`
}

// PendingChats returns chats whose most recent message is inbound, oldest
// first (longest-waiting first — the age the agent should weigh most when
// judging what to attend to). now is the reference timestamp for AgeSec
// (pass time.Now().Unix()).
func (s *Store) PendingChats(limit int, now int64) ([]Pending, error) {
	if limit <= 0 {
		limit = 20
	}
	// A chat's most recent message = the one with the largest (ts, rowid).
	// Matching m.rowid against that per-chat max picks exactly the latest
	// message per chat_jid; filtering to from_me=0 then keeps only chats
	// where the contact — not us — has the last word.
	rows, err := s.db.Query(`
		SELECT m.chat_jid, COALESCE(c.name,''), c.mode, c.status, c.active, m.ts, COALESCE(m.text,''),
			COALESCE(c.claimed_by,''), c.claimed_until
		FROM messages m
		JOIN chats c ON c.jid = m.chat_jid
		WHERE m.from_me = 0
		AND m.rowid = (
			SELECT m2.rowid FROM messages m2
			WHERE m2.chat_jid = m.chat_jid
			ORDER BY m2.ts DESC, m2.rowid DESC LIMIT 1
		)
		ORDER BY m.ts ASC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Pending{}
	for rows.Next() {
		var p Pending
		var active int
		if err := rows.Scan(&p.JID, &p.Name, &p.Mode, &p.Status, &active, &p.LastTS, &p.Preview,
			&p.ClaimedBy, &p.ClaimedUntil); err != nil {
			return nil, err
		}
		p.Active = active != 0
		if len(p.Preview) > 80 {
			p.Preview = p.Preview[:80] + "…"
		}
		p.AgeSec = now - p.LastTS
		// Always inbound_spoke here: the WHERE clause above already requires
		// an inbound message to exist (from_me=0), which is exactly what
		// makes ChatOrigin return inbound_spoke — no extra query needed.
		p.Origin = "inbound_spoke"
		// Effective claim — see effectiveClaim's doc comment.
		p.ClaimedBy, p.ClaimedUntil = effectiveClaim(p.ClaimedBy, p.ClaimedUntil, now)
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanMessages(rows *sql.Rows) ([]Message, error) {
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		var fromMe int
		if err := rows.Scan(&m.ChatJID, &m.ID, &fromMe, &m.Sender, &m.Text, &m.TS, &m.Type,
			&m.Model, &m.DeliveredTS, &m.ReadTS); err != nil {
			return nil, err
		}
		m.FromMe = fromMe != 0
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) GetMessages(jid string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT chat_jid, id, from_me, COALESCE(sender,''),
		COALESCE(text,''), ts, COALESCE(type,''), COALESCE(model,''), delivered_ts, read_ts
		FROM messages WHERE chat_jid = ? ORDER BY ts DESC LIMIT ?`, jid, limit)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// PendingAdvanced returns incoming messages from chats in advanced mode that
// have not been handled yet (the queue an agent pulls over MCP).
func (s *Store) PendingAdvanced(limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT m.chat_jid, m.id, m.from_me, COALESCE(m.sender,''),
		COALESCE(m.text,''), m.ts, COALESCE(m.type,''), COALESCE(m.model,''), m.delivered_ts, m.read_ts
		FROM messages m
		JOIN chats c ON c.jid = m.chat_jid
		WHERE m.from_me = 0 AND m.handled = 0 AND c.mode = 'advanced'
		ORDER BY m.ts ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

func (s *Store) MarkHandled(jid, id string) error {
	_, err := s.db.Exec(`UPDATE messages SET handled = 1 WHERE chat_jid = ? AND id = ?`, jid, id)
	return err
}

// Enqueue queues an outbound message with no model attribution — for callers
// with no model concept (e.g. a human manually sending via REST/dashboard).
func (s *Store) Enqueue(toJID, text string, ts int64) error {
	return s.EnqueueWithModel(toJID, text, ts, "")
}

// EnqueueWithModel queues an outbound message the same way Enqueue does, but
// also records which model produced it (send_message via MCP). The model
// travels with the outbox row until the gateway actually sends it, at which
// point it's copied onto the resulting messages row (with the real WhatsApp
// message ID, so delivery/read receipts can match it).
func (s *Store) EnqueueWithModel(toJID, text string, ts int64, model string) error {
	_, err := s.db.Exec(`INSERT INTO outbox (to_jid, text, created_ts, model) VALUES (?, ?, ?, ?)`,
		toJID, text, ts, nullIfEmpty(model))
	return err
}

const outboxColumns = `seq, to_jid, text, created_ts, COALESCE(model,''),
	retry_count, next_retry_ts, COALESCE(last_error,''), dead_letter`

func scanOutbox(rows *sql.Rows) ([]Outbox, error) {
	defer rows.Close()
	out := []Outbox{}
	for rows.Next() {
		var o Outbox
		var deadLetter int
		if err := rows.Scan(&o.Seq, &o.ToJID, &o.Text, &o.CreatedTS, &o.Model,
			&o.RetryCount, &o.NextRetryTS, &o.LastError, &deadLetter); err != nil {
			return nil, err
		}
		o.DeadLetter = deadLetter != 0
		out = append(out, o)
	}
	return out, rows.Err()
}

// PendingOutbox returns every unsent item (including ones still backing off
// or dead-lettered) — a plain "what's in the outbox" view for MCP/REST
// visibility. The gateway's send loop uses DueOutbox instead, which filters
// out anything not eligible to send yet.
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
// any) has already elapsed as of now — what the gateway's send loop should
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

// CountPendingAdvanced returns the total number of unhandled inbound messages
// in chats that are in "advanced" mode (the queue an agent drains over MCP).
func (s *Store) CountPendingAdvanced() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM messages m
		JOIN chats c ON c.jid = m.chat_jid
		WHERE m.from_me = 0 AND m.handled = 0 AND c.mode = 'advanced'`).Scan(&count)
	return count, err
}

// CountOutboundSince counts outbound (from_me=1) messages sent at or after
// ts. Used to reconstruct the governor's daily send count across a restart
// (0753, 3rd commandment): the daily anti-ban cap must survive a power cut —
// an in-memory-only counter would silently reset to 0 and let the bot blow
// past its daily limit right after a crash/reboot.
func (s *Store) CountOutboundSince(ts int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE from_me = 1 AND ts >= ?`, ts).Scan(&n)
	return n, err
}

func (s *Store) KVGet(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM kv WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (s *Store) KVSet(key, val string) error {
	_, err := s.db.Exec(`INSERT INTO kv (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, val)
	return err
}

// Setting keys (0753): KV-override names shared by gateway (readers) and
// restapi (writers), defined once here so neither side can typo a mismatch.
// env = default (config.go); dashboard edits persist here; the core rereads
// them live wherever it matters (dispatch/read/action delay, media skip,
// media retention) so a change applies without a restart.
const (
	SettingMediaSkipVideoGroup = "media_skip_video_group"
	SettingMediaSkipVideoChat  = "media_skip_video_chat"
	SettingMediaSkipPhotoGroup = "media_skip_photo_group"
	SettingMediaSkipPhotoChat  = "media_skip_photo_chat"
	SettingMediaMaxMB          = "media_max_mb"

	SettingDispatchDelayMin = "delay_dispatch_min"
	SettingDispatchDelayMax = "delay_dispatch_max"
	SettingReadDelayMin     = "delay_read_min"
	SettingReadDelayMax     = "delay_read_max"
	SettingActionDelayMin   = "delay_action_min"
	SettingActionDelayMax   = "delay_action_max"

	SettingRateLimitPerMin = "rate_limit_per_min"
	SettingRateLimitPerDay = "rate_limit_per_day"

	// MCP anti-flood — own namespace, deliberately
	// separate from the rate-limit keys above (those are the WhatsApp-
	// outbound governor; these are the MCP-inbound flood guard).
	SettingMCPGuardRatePerMin     = "mcpguard_rate_per_min"
	SettingMCPGuardEmitRatePerMin = "mcpguard_emit_rate_per_min"
	SettingMCPGuardBlockThreshold = "mcpguard_block_threshold"
	SettingMCPGuardBlockCooldown  = "mcpguard_block_cooldown"

	// Rules hierarchy (1959): "by type" tiers — a chat with no rules of its
	// own (chats.rules) inherits these by isGroupJID — plus a default
	// catch-all for anything matching neither. See EffectiveRules.
	SettingRulesTypeIndividual = "rules_type_individual"
	SettingRulesTypeGroup      = "rules_type_group"
	SettingRulesDefault        = "rules_default"

	// SettingDashPassHash is a runtime override for the dashboard's login
	// bcrypt hash — written ONLY by the
	// owner-scoped MCP tool reset_dashboard_password (mcpserver package),
	// read by dashboard.makeLoginHandler on every login attempt. Empty/unset
	// means "no override yet" — the dashboard falls back to the hash
	// resolved at startup (Config.PassHash from PIMYWA_DASH_PASS/_HASH).
	SettingDashPassHash = "dash_pass_hash"
)

// EffectiveRules resolves a chat's rules through the hierarchy (1959,
// "sin rules no actúa" stays intact — type/default ARE rules,
// just applied in bulk): particular (chats.rules) → by type (individual or
// group, by isGroupJID) → global default → "" (the AI does not act). The
// caller never sees the hierarchy — only this resolved value.
func (s *Store) EffectiveRules(jid string) (string, error) {
	c, ok, err := s.GetChat(jid)
	if err != nil {
		return "", err
	}
	if ok && c.Rules != "" {
		return c.Rules, nil
	}
	typeKey := SettingRulesTypeIndividual
	if isGroupJID(jid) {
		typeKey = SettingRulesTypeGroup
	}
	typeRules, err := s.KVGet(typeKey)
	if err != nil {
		return "", err
	}
	if typeRules != "" {
		return typeRules, nil
	}
	return s.KVGet(SettingRulesDefault)
}

// SetTypeRules sets the "by type" rules tier (1959): chatType must be
// "individual" (non-group chats) or "group". Privileged-only caller (REST),
// same trust gate as SetChatRules — never settable via MCP.
func (s *Store) SetTypeRules(chatType, rules string) error {
	var key string
	switch chatType {
	case "individual":
		key = SettingRulesTypeIndividual
	case "group":
		key = SettingRulesTypeGroup
	default:
		return fmt.Errorf("store: invalid chat type %q, want individual|group", chatType)
	}
	return s.KVSet(key, rules)
}

// SetDefaultRules sets the global default rules tier (1959) — the catch-all
// for a chat with no particular or by-type rules. Same trust gate as
// SetChatRules/SetTypeRules.
func (s *Store) SetDefaultRules(rules string) error {
	return s.KVSet(SettingRulesDefault, rules)
}

// SettingBool returns a KV-overridden boolean setting, falling back to def
// when unset — never an error, so callers on a message-handling path (the
// media skip check) never need to special-case a DB hiccup.
func (s *Store) SettingBool(key string, def bool) bool {
	v, err := s.KVGet(key)
	if err != nil || v == "" {
		return def
	}
	return v == "1" || v == "true"
}

// SetSettingBool persists a boolean setting.
func (s *Store) SetSettingBool(key string, b bool) error {
	if b {
		return s.KVSet(key, "1")
	}
	return s.KVSet(key, "0")
}

// SettingDuration is SettingBool for a time.Duration (Go's duration string
// format, e.g. "1s"). Falls back to def if unset or unparseable.
func (s *Store) SettingDuration(key string, def time.Duration) time.Duration {
	v, err := s.KVGet(key)
	if err != nil || v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

// SetSettingDuration persists a duration setting.
func (s *Store) SetSettingDuration(key string, d time.Duration) error {
	return s.KVSet(key, d.String())
}

// SettingInt is SettingBool for an integer count (rate limits, media MB).
// Falls back to def if unset or unparseable.
func (s *Store) SettingInt(key string, def int) int {
	v, err := s.KVGet(key)
	if err != nil || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// SetSettingInt persists an integer setting.
func (s *Store) SetSettingInt(key string, n int) error {
	return s.KVSet(key, strconv.Itoa(n))
}

// AddGroupMember records that memberJID is a participant of groupJID.
func (s *Store) AddGroupMember(memberJID, groupJID string) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO chat_groups (member_jid, group_jid) VALUES (?, ?)`,
		memberJID, groupJID)
	return err
}

// RemoveGroupMember is AddGroupMember's counterpart — removes the
// membership row when someone leaves/is removed from a group.
func (s *Store) RemoveGroupMember(memberJID, groupJID string) error {
	_, err := s.db.Exec(`DELETE FROM chat_groups WHERE member_jid = ? AND group_jid = ?`, memberJID, groupJID)
	return err
}

// GroupsOf returns the group JIDs memberJID is known to participate in.
func (s *Store) GroupsOf(memberJID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT group_jid FROM chat_groups WHERE member_jid = ?`, memberJID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// AddMedia records a downloaded media file (the text/metadata row it belongs
// to lives in messages and is never deleted; only this row is GC-eligible).
func (s *Store) AddMedia(m Media) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO media (msg_id, chat_jid, path, mime, size, ts)
		VALUES (?, ?, ?, ?, ?, ?)`, m.MsgID, m.ChatJID, m.Path, m.Mime, m.Size, m.TS)
	return err
}

func (s *Store) ListMedia(chatJID string, limit int) ([]Media, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT msg_id, chat_jid, path, COALESCE(mime,''), size, ts
		FROM media WHERE chat_jid = ? ORDER BY ts DESC LIMIT ?`, chatJID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Media{}
	for rows.Next() {
		var m Media
		if err := rows.Scan(&m.MsgID, &m.ChatJID, &m.Path, &m.Mime, &m.Size, &m.TS); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeleteMedia removes a media row (used by the GC policy — size-only, text
// is never deleted). Does not touch the file on disk; the caller does that.
func (s *Store) DeleteMedia(chatJID, msgID string) error {
	_, err := s.db.Exec(`DELETE FROM media WHERE chat_jid = ? AND msg_id = ?`, chatJID, msgID)
	return err
}

// AddDraft records an auto-reply worker's candidate reply as pending — NOT
// sent, NOT in the outbox. Approval (a separate, later piece) is what moves
// a draft into the outbox.
func (s *Store) AddDraft(chatJID, text, model string, ts int64) error {
	return s.AddDraftWithConfirmer(chatJID, text, model, "", ts)
}

// AddDraftWithConfirmer is AddDraft plus a confirmer JID (0748) — who this
// draft is held pending confirmation from. Empty confirmer means the
// default (the owner) — nothing here resolves that to an actual JID.
func (s *Store) AddDraftWithConfirmer(chatJID, text, model, confirmer string, ts int64) error {
	_, err := s.db.Exec(`INSERT INTO drafts (chat_jid, text, model, created_ts, status, confirmer)
		VALUES (?, ?, ?, ?, 'pending', ?)`, chatJID, text, nullIfEmpty(model), ts, nullIfEmpty(confirmer))
	return err
}

// PendingDrafts returns drafts still awaiting approval, oldest first.
func (s *Store) PendingDrafts(limit int) ([]Draft, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT id, chat_jid, text, COALESCE(model,''), created_ts, status, COALESCE(confirmer,'')
		FROM drafts WHERE status = 'pending' ORDER BY created_ts ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Draft{}
	for rows.Next() {
		var d Draft
		if err := rows.Scan(&d.ID, &d.ChatJID, &d.Text, &d.Model, &d.CreatedTS, &d.Status, &d.Confirmer); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ApproveDraft moves a pending draft into the outbox — to actually be sent,
// with the existing anti-ban pacing — and marks it approved. textOverride,
// if non-empty, replaces the draft's text before enqueueing (edit-before-
// send). ok is false if the draft doesn't exist or isn't pending (already
// approved/discarded); nothing is enqueued in that case.
func (s *Store) ApproveDraft(id int64, textOverride string, ts int64) (ok bool, err error) {
	var chatJID, text, model string
	err = s.db.QueryRow(`SELECT chat_jid, text, COALESCE(model,'') FROM drafts WHERE id = ? AND status = 'pending'`, id).
		Scan(&chatJID, &text, &model)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if textOverride != "" {
		text = textOverride
	}
	if err := s.EnqueueWithModel(chatJID, text, ts, model); err != nil {
		return false, err
	}
	if _, err := s.db.Exec(`UPDATE drafts SET status = 'approved' WHERE id = ?`, id); err != nil {
		return false, err
	}
	return true, nil
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
