// Package store: persistencia SQLite (modernc.org/sqlite, pure Go — sin
// cgo). Esquema y migraciones copiados literales de Piumy
// (core/internal/store/store.go): es contrato de datos, no se reinventa. La
// lógica de acceso (CRUD por tabla) llega en F1.
package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

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
  decrypt_retry_at INTEGER NOT NULL DEFAULT 0,
  origin_terminal_id TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (chat_jid, id)
);
CREATE TABLE IF NOT EXISTS outbox (
  seq        INTEGER PRIMARY KEY AUTOINCREMENT,
  to_jid     TEXT    NOT NULL,
  text       TEXT    NOT NULL,
  created_ts INTEGER NOT NULL,
  sent       INTEGER NOT NULL DEFAULT 0,
  origin_terminal_id TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS kv (key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE IF NOT EXISTS media (
  msg_id    TEXT    NOT NULL,
  chat_jid  TEXT    NOT NULL,
  path      TEXT    NOT NULL,
  full_path TEXT    NOT NULL DEFAULT '',
  mime      TEXT,
  size      INTEGER NOT NULL DEFAULT 0,
  ts        INTEGER NOT NULL,
  PRIMARY KEY (chat_jid, msg_id)
);
CREATE TABLE IF NOT EXISTS usage (
  chat_jid    TEXT    NOT NULL,
  day         TEXT    NOT NULL,
  out_chars   INTEGER NOT NULL DEFAULT 0,
  in_chars    INTEGER NOT NULL DEFAULT 0,
  images      INTEGER NOT NULL DEFAULT 0,
  audio       INTEGER NOT NULL DEFAULT 0,
  messages    INTEGER NOT NULL DEFAULT 0,
  tokens_real REAL    NOT NULL DEFAULT 0,
  PRIMARY KEY (chat_jid, day)
);
CREATE TABLE IF NOT EXISTS drafts (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  chat_jid   TEXT    NOT NULL,
  text       TEXT    NOT NULL,
  model      TEXT,
  created_ts INTEGER NOT NULL,
  status     TEXT    NOT NULL DEFAULT 'pending'
);
CREATE TABLE IF NOT EXISTS agents (
  agent_id            TEXT PRIMARY KEY,
  name                TEXT NOT NULL DEFAULT '',
  endpoint            TEXT NOT NULL DEFAULT '',
  antenna_terminal_id TEXT NOT NULL DEFAULT '',
  pinpass             TEXT NOT NULL DEFAULT '',
  role                TEXT NOT NULL DEFAULT 'secondary'
);
CREATE TABLE IF NOT EXISTS group_members (
  group_jid   TEXT    NOT NULL,
  member_jid  TEXT    NOT NULL,
  member_name TEXT,
  added_ts    INTEGER NOT NULL,
  PRIMARY KEY (group_jid, member_jid)
);
CREATE TABLE IF NOT EXISTS media_pending (
  chat_jid        TEXT    NOT NULL,
  msg_id          TEXT    NOT NULL,
  mime            TEXT    NOT NULL DEFAULT '',
  kind            TEXT    NOT NULL DEFAULT '',
  direct_path     TEXT    NOT NULL DEFAULT '',
  media_key       BLOB,
  file_sha256     BLOB,
  file_enc_sha256 BLOB,
  file_length     INTEGER NOT NULL DEFAULT 0,
  ts              INTEGER NOT NULL DEFAULT 0,
  attempts        INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (chat_jid, msg_id)
);
CREATE TABLE IF NOT EXISTS avatars (
  jid           TEXT    PRIMARY KEY,
  picture_id    TEXT    NOT NULL DEFAULT '',
  path          TEXT    NOT NULL DEFAULT '',
  fetched_at    INTEGER NOT NULL DEFAULT 0,
  next_check_at INTEGER NOT NULL DEFAULT 0
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
	`ALTER TABLE media ADD COLUMN full_path TEXT NOT NULL DEFAULT ''`,
	// ST-B (ct-2026-07-11-0741): mode_source tracks who last set chats.mode
	// — 'router' (the router-driven mirror on every inbound) or 'manual'
	// (an explicit set_mode/escalate MCP call or the REST admin endpoint).
	// 'router' is the correct default for every pre-existing row: nobody
	// had override-tracking before, so there is no manual choice to
	// preserve retroactively — see SyncRouterMode/SetMode (chat.go).
	`ALTER TABLE chats ADD COLUMN mode_source TEXT NOT NULL DEFAULT 'router'`,
	// ct-2026-07-13-2243: burstMaxTS stored in draft so approve_draft can
	// call MarkHandledBefore with the correct TS (not time.Now()).
	`ALTER TABLE drafts ADD COLUMN burst_max_ts INTEGER NOT NULL DEFAULT 0`,
	// ct-2026-07-19-0102: contact_name is the phone address book's own name
	// for this number/group ("respaldar el nombre de contacto si esta en el
	// telefono", boss verbatim) — the backup/scrape target (Sub 2 populates
	// it), distinct from chats.name (the WhatsApp display name TouchChat
	// keeps current on every inbound message).
	`ALTER TABLE chats ADD COLUMN contact_name TEXT`,
	// ct-2026-07-21-1306: history_state tracked the gradual on-demand history
	// backfill (internal/whatsmeow's history worker) — pending (never
	// requested) / downloading (a page came back full, more to fetch) /
	// loaded (WhatsApp's own phone returned a short/empty page, nothing
	// older left). UNUSED since ct-2026-07-24-2004 (the on-demand history
	// worker was removed — docs/HISTORY-SYNC-REGRESION-2026-07-24.md) — kept
	// in the schema, no writer left, not worth a destructive migration.
	`ALTER TABLE chats ADD COLUMN history_state TEXT NOT NULL DEFAULT 'pending'`,
	// ct-2026-07-21-1437 parte 2: attempts caps how many times the background
	// worker retries a media_pending row — a WhatsApp directPath EXPIRES
	// after a while, so without a cap a single stale reference (very likely
	// for media the history worker just brought back from old messages)
	// would retry forever every tick and starve the entire FIFO queue behind
	// it (Citrino catch, ct-2026-07-21). See NextMediaPending's doc.
	`ALTER TABLE media_pending ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0`,
	// ct-2026-07-21-1610 (S6a backend): reply/forward metadata read from
	// whatsmeow's ContextInfo at ingestion time (whatsmeow.detectReply).
	// quoted_preview is stored (not resolved on read) so a later lookup never
	// needs the quoted message to still exist in this chat's own history.
	`ALTER TABLE messages ADD COLUMN quoted_id TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE messages ADD COLUMN quoted_preview TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE messages ADD COLUMN forwarded INTEGER NOT NULL DEFAULT 0`,
	// ct-2026-07-21-2120: history_empty_pages was the history worker's belt
	// fallback — counted consecutive ON_DEMAND pages with zero messages for
	// a chat. UNUSED since ct-2026-07-24-2004 (the on-demand history worker
	// was removed — docs/HISTORY-SYNC-REGRESION-2026-07-24.md) — kept in the
	// schema, no writer left, not worth a destructive migration.
	`ALTER TABLE chats ADD COLUMN history_empty_pages INTEGER NOT NULL DEFAULT 0`,
	// ct-2026-07-22-0114: history_requested_at (unix ts, 0 = none outstanding)
	// + history_request_attempts tracked a chat's UNANSWERED ON_DEMAND
	// requests — distinct from history_empty_pages above, which counted
	// pages RECEIVED empty. UNUSED since ct-2026-07-24-2004 (the on-demand
	// history worker was removed — docs/HISTORY-SYNC-REGRESION-2026-07-24.md)
	// — kept in the schema, no writer left, not worth a destructive
	// migration.
	`ALTER TABLE chats ADD COLUMN history_requested_at INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE chats ADD COLUMN history_request_attempts INTEGER NOT NULL DEFAULT 0`,
	// ct-2026-07-22-1301 (M1, pestaña Agentes): "los agentes se registran por
	// nombre (después lo cableamos bien)", boss verbatim — display name only,
	// optional (default ''), no identidad/auth atada a él todavía.
	`ALTER TABLE agents ADD COLUMN name TEXT NOT NULL DEFAULT ''`,
	// ct-2026-07-22-1903 (M5, defaults por origen): 'manual' means "an
	// explicit owner decision fixes this chat's mode — never silently
	// override it" (SetIsBoss/SetActive/SetConfirmationMode/MarkConfigManual
	// all write it); 'default' means "still eligible for the origin-based
	// KV default (config_level_default_new/contact) to apply" — TouchChat's
	// INSERT branch is the only writer of 'default', and only for
	// individual chats (groups stay 'manual', out of this axis entirely).
	// DEFAULT 'manual' here is the SAFE choice for every row that already
	// exists: the boss has been triaging chats by hand all through the MVP
	// — retroactively marking them 'default' would risk a future contact
	// re-sync silently overwriting an already-established choice, breaking
	// the "chats ya configurados a mano: sin cambios" acceptance criterion.
	`ALTER TABLE chats ADD COLUMN config_level_source TEXT NOT NULL DEFAULT 'manual'`,
	// S11 (ct-2026-07-30-1619): silent_act's own audit trail — why (and
	// when) the agent last chose silence over send_message on this chat.
	// See Chat.SilenceReason/SilenceAt and SetChatSilence.
	`ALTER TABLE chats ADD COLUMN silence_reason TEXT`,
	`ALTER TABLE chats ADD COLUMN silence_at INTEGER NOT NULL DEFAULT 0`,
	// ct-2026-07-31-0610 (Aprobador P1): is_approver — orthogonal to is_boss
	// and to config_level, see Chat.IsApprover's doc for the full reasoning.
	`ALTER TABLE chats ADD COLUMN is_approver INTEGER NOT NULL DEFAULT 0`,
	// T15 (ct-2026-08-05-123241): round tracks how many times THIS chat's
	// confirmation thread has gone reject→redraft — 1 for a fresh draft,
	// incremented only when the previous draft in the chain was rejected
	// (see nextDraftRound). reject_reason is the motivo RejectDraft records —
	// dispatchPayload (capipush) reads it back so it travels WITH the
	// redispatch, not as something the agent has to fetch separately.
	`ALTER TABLE drafts ADD COLUMN round INTEGER NOT NULL DEFAULT 1`,
	`ALTER TABLE drafts ADD COLUMN reject_reason TEXT`,
	// T35 (ct-2026-08-08-1258): decrypt_retry_at persists the one observable
	// signal that a message WE sent reached the recipient's device but
	// couldn't be decrypted there (WhatsApp's retry receipt — see
	// whatsmeow.handleRetryReceipt). Before this it was log.Printf-only,
	// invisible in production (no log file, -H windowsgui has no console) —
	// the exact defect this migration fixes.
	`ALTER TABLE messages ADD COLUMN decrypt_retry_at INTEGER NOT NULL DEFAULT 0`,
	// T39 (ct-2026-08-08-1619): origin_terminal_id records which agent
	// terminal originated an outbound message via send_to_boss — "" for
	// everything else (send_message/draft/autoreply/a human via REST). Not
	// read anywhere yet: it's the data a later reply-routing feature (the
	// owner answers by quoting a message, it reaches THAT agent) needs —
	// stored now on purpose, per Citrino's explicit instruction not to skip
	// it just because nothing consumes it today. outbox carries it from
	// enqueue to send; messages gets the same value copied at send time
	// (sentMessageRow, corepipeline/outbox.go) alongside the real MsgID.
	`ALTER TABLE outbox ADD COLUMN origin_terminal_id TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE messages ADD COLUMN origin_terminal_id TEXT NOT NULL DEFAULT ''`,
	// T101 (ct-2026-08-29-1651): chunks_sent survives a long reply's split
	// across several WhatsApp messages — how many pieces already went out,
	// so a later chunk failing (retry/backoff) never re-sends the ones the
	// contact already received. Independent of retry_count/next_retry_ts
	// (SetOutboxChunkProgress never touches those, SetOutboxRetry never
	// touches this).
	`ALTER TABLE outbox ADD COLUMN chunks_sent INTEGER NOT NULL DEFAULT 0`,
	// T108 (ct-2026-09-01-1413): sender records WHICH group participant this
	// draft answers — approve_draft needs it to close only that speaker's
	// messages, not the whole chat's. "" for a 1:1 draft (no scoping needed,
	// one sender) and for any draft created before this migration — both
	// degrade to the pre-T108 whole-chat close, never break.
	`ALTER TABLE drafts ADD COLUMN sender TEXT NOT NULL DEFAULT ''`,
	// T122 (ct-2026-09-02-2045): outbound media — a photo (T123: audio too)
	// attached to an outbox item or a draft. media_path is a file under
	// PIUMY_MEDIA_DIR (mediautil.SaveOutboundMedia), never the bytes
	// themselves — outbox/drafts stay text tables, no BLOBs. Empty on every
	// pre-existing row (DEFAULT ''), same as every migration above: a
	// text-only item/draft behaves exactly as before — send/draft code
	// branches on media_path == "" to tell the two apart.
	`ALTER TABLE outbox ADD COLUMN media_path TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE outbox ADD COLUMN media_mime TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE outbox ADD COLUMN media_kind TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE drafts ADD COLUMN media_path TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE drafts ADD COLUMN media_mime TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE drafts ADD COLUMN media_kind TEXT NOT NULL DEFAULT ''`,
	// T123 (ct-2026-09-02-2121): a voice note's duration — an HONEST column,
	// not packed into media_mime (rejected mid-contract: media_mime is
	// read verbatim by store.AddMedia/get_media/get_media_full/the
	// dashboard's message rendering, all of which expect a real mime, not
	// one carrying a smuggled suffix). Named media_seconds, not
	// audio_seconds: a future video kind has the same "duration" concept
	// and reuses this column instead of forcing another migration. 0 (the
	// DEFAULT, and every pre-existing row) means "no duration known" —
	// SendAudio leaves AudioMessage.Seconds unset in that case, never a
	// guessed value.
	`ALTER TABLE outbox ADD COLUMN media_seconds INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE drafts ADD COLUMN media_seconds INTEGER NOT NULL DEFAULT 0`,
}

// confirmationModeMigration adds chats.confirmation_mode with a flat
// 'always' default — correct for groups, wrong for 1-1 chats (0810's
// default is BY TYPE). A single ALTER...DEFAULT can't express a per-row
// value, so migrate() runs one follow-up backfill UPDATE, but only the
// FIRST time this exact ALTER succeeds (see migrate).
//
// The literal default here was 'required' until the F4c audit found the
// bug this now fixes: send_message's confirmation_mode gate (mcpserver/
// send.go) checks the 3-state scheme (none/discretion/always — F4-DESIGN
// §4), but this column and TouchChat's group default were still writing
// Piumy's original 2-state value ('required'/'none'). 'required' never
// matched send.go's `== "always"` check, so a freshly-seen group's
// fail-safe was silently dead — it sent directly instead of holding a
// draft. migrateRequiredToAlways (below) reconciles any row a
// pre-fix build already wrote.
const confirmationModeMigration = `ALTER TABLE chats ADD COLUMN confirmation_mode TEXT NOT NULL DEFAULT 'always'`

// migrate applies columnMigrations. Idempotent: SQLite has no "ADD COLUMN IF
// NOT EXISTS", so a rerun's "duplicate column name" error is expected on
// every statement and ignored; any other error is real and returned.
func migrate(db *sql.DB) error {
	for _, ddl := range columnMigrations {
		if _, err := db.Exec(ddl); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return err
		}
	}
	if err := migrateConfirmationMode(db); err != nil {
		return err
	}
	if err := migrateRequiredToAlways(db); err != nil {
		return err
	}
	if err := migrateIsBossTouched(db); err != nil {
		return err
	}
	return migrateMimeInText(db)
	// ponytail: Piumy's migrateModeRename ('advanced'->'dedicated') se omite
	// a propósito — piumy-gateway nace sin filas legacy que migrar. Si algún
	// día se importa una DB vieja de Piumy, portarla desde store.go.
}

// migrateConfirmationMode adds chats.confirmation_mode and backfills 1-1
// chats away from the ALTER's flat 'always' default to 'none' (0810),
// ATOMICALLY: the ALTER + backfill run in ONE transaction so a crash
// between them is impossible — a cut mid-tx just rolls back, and the
// column not existing on the next start makes this re-run from scratch.
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

// migrateRequiredToAlways reconciles Piumy's legacy 2-state value
// ('required') into the 3-state scheme (none/discretion/always) that
// send_message's confirmation_mode gate actually checks (F4c audit
// finding — see confirmationModeMigration's doc comment). Runs on every
// startup, unconditionally (not gated behind "column just added"): a row
// written by a pre-fix build has 'required' regardless of when the column
// itself was created. Idempotent — a no-op once no row has it.
func migrateRequiredToAlways(db *sql.DB) error {
	_, err := db.Exec(`UPDATE chats SET confirmation_mode = 'always' WHERE confirmation_mode = 'required'`)
	return err
}

// migrateIsBossTouched adds chats.is_boss_touched and backfills every
// row that already exists to 1 (touched) — T12 (ct-2026-08-05-1231).
// Deliberately NOT expressed as a plain `DEFAULT 1` in columnMigrations:
// SQLite's ALTER...DEFAULT becomes the column's ongoing default for every
// FUTURE insert that doesn't name it too, not just this migration's one-time
// backfill — TouchChat's own INSERT never names it, so `DEFAULT 1` would
// poison every brand-new chat row (including a genuinely fresh OwnJID row
// on a clean install) as "already decided", permanently defeating
// MarkOwnerIfUntouched's guard (chat.go). The raw column default here stays
// 0 (untouched — correct for a brand-new row); this ATOMIC backfill (same
// ALTER+UPDATE-in-one-tx pattern as migrateConfirmationMode) is what makes
// every PRE-EXISTING row start "touched" instead — same safety reasoning as
// config_level_source's DEFAULT 'manual' above: no way to tell
// retroactively "is_boss=0 because nobody ever touched it" from "is_boss=0
// because the owner deliberately unmarked it" on an already-running
// install, so it's assumed decided and left exactly as it is.
func migrateIsBossTouched(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op if Commit already ran

	if _, err := tx.Exec(`ALTER TABLE chats ADD COLUMN is_boss_touched INTEGER NOT NULL DEFAULT 0`); err != nil {
		if strings.Contains(err.Error(), "duplicate column name") {
			return nil // already migrated on a prior run — nothing to backfill
		}
		return err
	}
	if _, err := tx.Exec(`UPDATE chats SET is_boss_touched = 1`); err != nil {
		return err
	}
	return tx.Commit()
}

// migrateMimeInText repairs messages.text/type pairs a pre-fix build wrote
// via whatsmeow.Adapter.handleMessage's swapped downloadAndStoreMedia
// assignment (ct-2026-07-21-1727): mime landed in text (shown as raw
// "image/jpeg" instead of a photo bubble) and caption (usually empty)
// landed in type, so store.MediaKind(type) never recognizes the message as
// media even though its file downloaded fine. Scoped to rows with a
// matching `media` row whose mime equals the corrupted text — the only
// reliable way to tell a real swap apart from an ordinary text message that
// happens to contain a slash. Swapping text/type back also makes this a
// no-op on rerun: once type holds the mime, it no longer matches the
// `type NOT LIKE '%/%'` guard.
func migrateMimeInText(db *sql.DB) error {
	_, err := db.Exec(`
		UPDATE messages
		SET text = type, type = text
		WHERE (type IS NULL OR type = '' OR type NOT LIKE '%/%')
		  AND EXISTS (
			SELECT 1 FROM media m
			WHERE m.chat_jid = messages.chat_jid AND m.msg_id = messages.id AND m.mime = messages.text
		  )`)
	return err
}

// Open creates/migrates the SQLite DB at path (WAL + synchronous=NORMAL for
// power-loss resilience; busy_timeout avoids "database is locked" under
// concurrent access) and applies schema + migrate.
//
// MkdirAll's own directory first (T169, ct-2026-09-19-1433): path now
// usually resolves under config.DataDir(), which — unlike the old relative
// defaults, always inside the already-existing working directory — may not
// exist yet on a genuinely fresh install (no Windows installer's own
// ForceDirectories step ran first). Same pattern every other writer in this
// codebase already uses for its own directory (mediautil, sessionbackup,
// state, agentconnect, gwlog); sqlite itself never creates a missing
// parent, only the file.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
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
	// ct-2026-07-24-0041: serializar acceso SQLite para evitar SQLITE_BUSY
	// bajo carga concurrente (history sync + media worker + outbox). Idem
	// adapter.go:309 (session DB). modernc.org/sqlite + pool multi-conn =
	// SQLITE_BUSY; una conexion serializa y elimina la clase de error.
	db.SetMaxOpenConns(1)
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }
