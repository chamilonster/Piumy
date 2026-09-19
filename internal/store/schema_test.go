package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestOpenCreatesSchemaAndIsIdempotent covers the smoke DoD (7 tables exist
// after Open) plus migrate()'s idempotency contract (a second Open on the
// same file must not error on "duplicate column name").
func TestOpenCreatesSchemaAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "piumy.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	wantTables := []string{"chats", "messages", "outbox", "kv", "media", "drafts", "agents"}
	for _, tbl := range wantTables {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&name); err != nil {
			t.Errorf("table %q missing: %v", tbl, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen: migrate() must be a no-op, not an error, on an already-migrated DB.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (migrate idempotency): %v", err)
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestOpenCreatesMissingParentDirectory is T169's own regression
// (ct-2026-09-19-1433): path now usually resolves under config.DataDir(),
// which may not exist yet on a genuinely fresh install (no installer's
// ForceDirectories step ran first) — sqlite itself never creates a missing
// parent, only the file, so Open must.
func TestOpenCreatesMissingParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does", "not", "exist", "yet", "piumy.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open with a missing parent directory: %v", err)
	}
	defer s.Close()

	if _, err := s.db.Exec("SELECT 1"); err != nil {
		t.Errorf("DB not actually usable after Open: %v", err)
	}
}

// TestMigrateAddsDecryptRetryAtToExistingMessagesTable is the DoD regression
// for T35 (ct-2026-08-08-1258): CREATE TABLE IF NOT EXISTS is a no-op on a
// messages table that already exists — it never adds a new column. The real
// case is the owner's live install, not a fresh DB, so this hand-creates
// messages WITHOUT decrypt_retry_at (the pre-T35 shape) before Open() ever
// runs, then checks migrate() backfilled the column onto that existing table
// and both writing (MarkDecryptRetry) and reading (GetMessages) it work.
func TestMigrateAddsDecryptRetryAtToExistingMessagesTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "piumy.db")

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE messages (
		chat_jid TEXT    NOT NULL,
		id       TEXT    NOT NULL,
		from_me  INTEGER NOT NULL,
		sender   TEXT,
		text     TEXT,
		ts       INTEGER NOT NULL,
		type     TEXT,
		handled  INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (chat_jid, id)
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO messages (chat_jid, id, from_me, text, ts) VALUES (?, ?, ?, ?, ?)`,
		"555000001@s.whatsapp.net", "wamid-pre", 1, "ya estaba antes de la migración", 50); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on a pre-existing messages table without decrypt_retry_at: %v", err)
	}
	defer s.Close()

	if err := s.MarkDecryptRetry("555000001@s.whatsapp.net", "wamid-pre", 999); err != nil {
		t.Fatalf("MarkDecryptRetry after migration: %v", err)
	}
	msgs, err := s.GetMessages("555000001@s.whatsapp.net", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].DecryptRetryAt != 999 {
		t.Errorf("got %+v, want one pre-existing message with decrypt_retry_at=999 after migrate+mark", msgs)
	}
}

// TestMigrateAddsOriginTerminalIDToExistingOutboxAndMessagesTables is the DoD
// regression for T39 (ct-2026-08-08-1619): the owner's live install already
// has outbox/messages without origin_terminal_id — CREATE TABLE IF NOT
// EXISTS is a no-op there, same reasoning as T35's decrypt_retry_at
// migration test. Hand-creates BOTH tables in their pre-T39 shape before
// Open() ever runs, then checks migrate() backfilled both columns and that
// EnqueueFromAgent/AddMessage (write) and DueOutbox/GetMessages (read) work
// against the migrated tables.
func TestMigrateAddsOriginTerminalIDToExistingOutboxAndMessagesTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "piumy.db")

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE messages (
		chat_jid TEXT    NOT NULL,
		id       TEXT    NOT NULL,
		from_me  INTEGER NOT NULL,
		sender   TEXT,
		text     TEXT,
		ts       INTEGER NOT NULL,
		type     TEXT,
		handled  INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (chat_jid, id)
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE outbox (
		seq        INTEGER PRIMARY KEY AUTOINCREMENT,
		to_jid     TEXT    NOT NULL,
		text       TEXT    NOT NULL,
		created_ts INTEGER NOT NULL,
		sent       INTEGER NOT NULL DEFAULT 0
	)`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on pre-existing outbox/messages tables without origin_terminal_id: %v", err)
	}
	defer s.Close()

	if err := s.EnqueueFromAgent("555000001@s.whatsapp.net", "[Agente Uno] hola", 100, "term-555"); err != nil {
		t.Fatalf("EnqueueFromAgent after migration: %v", err)
	}
	due, err := s.DueOutbox(10, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].OriginTerminalID != "term-555" {
		t.Fatalf("got DueOutbox=%+v, want one item with origin_terminal_id=term-555", due)
	}

	if err := s.AddMessage(Message{
		ChatJID: "555000001@s.whatsapp.net", ID: "wamid-1", FromMe: true, Text: "[Agente Uno] hola", TS: 100,
		OriginTerminalID: "term-555",
	}); err != nil {
		t.Fatalf("AddMessage after migration: %v", err)
	}
	msgs, err := s.GetMessages("555000001@s.whatsapp.net", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].OriginTerminalID != "term-555" {
		t.Fatalf("got GetMessages=%+v, want one message with origin_terminal_id=term-555", msgs)
	}
}

// TestMigrateMimeInTextSwapsCorruptedRows is the regression for
// ct-2026-07-21-1727: a pre-fix build's handleMessage stored a media
// message's mime in messages.text and its caption in messages.type
// (swapped). Any row with a matching `media` row whose mime equals the
// corrupted text must come back swapped — text=caption (here empty),
// type=mime — so store.MediaKind(type) recognizes it again. An ordinary
// text message that happens to contain a slash, with no matching media
// row, must be left untouched (the EXISTS guard is what tells the two
// apart).
func TestMigrateMimeInTextSwapsCorruptedRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "piumy.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.AddMessage(Message{ChatJID: "1@lid", ID: "M1", Text: "image/jpeg", Type: "", TS: 100}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMedia(Media{MsgID: "M1", ChatJID: "1@lid", Path: "p", FullPath: "p", Mime: "image/jpeg", TS: 100}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: "1@lid", ID: "M2", Text: "no es un mime, solo 1/3 de la torta", Type: "text", TS: 200}); err != nil {
		t.Fatal(err)
	}

	if err := migrateMimeInText(s.db); err != nil {
		t.Fatal(err)
	}

	byID := func(id string) Message {
		t.Helper()
		msgs, err := s.GetMessages("1@lid", 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range msgs {
			if m.ID == id {
				return m
			}
		}
		t.Fatalf("message %q not found", id)
		return Message{}
	}

	fixed := byID("M1")
	if fixed.Text != "" || fixed.Type != "image/jpeg" {
		t.Errorf("M1 = %+v, want Text=\"\" Type=image/jpeg", fixed)
	}

	untouched := byID("M2")
	if untouched.Text != "no es un mime, solo 1/3 de la torta" || untouched.Type != "text" {
		t.Errorf("M2 = %+v, want left untouched (no matching media row)", untouched)
	}

	// Idempotent: a second run must not re-corrupt the already-fixed row.
	if err := migrateMimeInText(s.db); err != nil {
		t.Fatal(err)
	}
	fixed2 := byID("M1")
	if fixed2.Text != "" || fixed2.Type != "image/jpeg" {
		t.Errorf("M1 after second migrate run = %+v, want unchanged", fixed2)
	}
}
