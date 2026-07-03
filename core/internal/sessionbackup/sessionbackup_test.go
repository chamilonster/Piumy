// SPDX-License-Identifier: AGPL-3.0-only
package sessionbackup

import (
	"bytes"
	"context"
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// newFakeSession creates a tiny real SQLite file at path (WAL mode, like
// the real session DB) with one distinguishing row, standing in for the
// WhatsApp session for tests.
func newFakeSession(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE device(jid TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO device VALUES ('56999999999@s.whatsapp.net')"); err != nil {
		t.Fatal(err)
	}
}

// TestBackupRestoreRoundTrip covers the DoD directly: backup then
// restore reconstructs a session DB with the SAME data as the original —
// proven by querying it, not just comparing raw bytes (VACUUM INTO doesn't
// guarantee byte-identical output, just equivalent data).
func TestBackupRestoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sessionDB := filepath.Join(dir, "wa.db")
	newFakeSession(t, sessionDB)

	b := New(Config{
		SessionDBPath: sessionDB,
		Key:           "test passphrase, not the real one",
		Dir:           filepath.Join(dir, "backups"),
		Keep:          5,
	})
	if !b.Enabled() {
		t.Fatal("Backuper not enabled despite a key being set")
	}

	res, err := b.BackupNow(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Path == "" || res.SizeBytes == 0 {
		t.Fatalf("BackupNow result = %+v, want a real path and non-zero size", res)
	}

	restoredPath := filepath.Join(dir, "restored.db")
	if err := Restore(res.Path, restoredPath, "test passphrase, not the real one"); err != nil {
		t.Fatal(err)
	}

	restoredDB, err := sql.Open("sqlite", "file:"+restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restoredDB.Close()
	var jid string
	if err := restoredDB.QueryRow("SELECT jid FROM device").Scan(&jid); err != nil {
		t.Fatal(err)
	}
	if jid != "56999999999@s.whatsapp.net" {
		t.Errorf("restored jid = %q, want the original", jid)
	}
}

// TestBackupNowDisabledWithoutKeyIsNoOp covers the DoD directly: no key set
// → BackupNow does nothing (no file written), zero Result, no error.
func TestBackupNowDisabledWithoutKeyIsNoOp(t *testing.T) {
	dir := t.TempDir()
	sessionDB := filepath.Join(dir, "wa.db")
	newFakeSession(t, sessionDB)

	b := New(Config{SessionDBPath: sessionDB, Dir: filepath.Join(dir, "backups")}) // no Key
	if b.Enabled() {
		t.Fatal("Backuper reports enabled with no key set")
	}

	res, err := b.BackupNow(context.Background())
	if err != nil {
		t.Fatalf("BackupNow with no key: err = %v, want nil (silent no-op)", err)
	}
	if res.Path != "" {
		t.Errorf("BackupNow with no key produced a result: %+v, want the zero value", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "backups")); !os.IsNotExist(err) {
		t.Error("backup dir was created despite backups being disabled")
	}
}

// TestNewWarnsWhenKeyMissing covers the DoD's startup warning (mirroring
// RequireBearerToken's pattern from 0133): constructing a disabled Backuper
// logs a warning, so an operator never silently ships with backups off.
func TestNewWarnsWhenKeyMissing(t *testing.T) {
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	New(Config{SessionDBPath: "irrelevant", Dir: "irrelevant"})

	if !strings.Contains(buf.String(), "backups DISABLED") {
		t.Errorf("log output = %q, want a warning about backups being disabled", buf.String())
	}
}

// TestRotationKeepsOnlyNewestN covers the DoD directly: after exceeding
// Keep, only the newest N backups remain.
func TestRotationKeepsOnlyNewestN(t *testing.T) {
	dir := t.TempDir()
	sessionDB := filepath.Join(dir, "wa.db")
	newFakeSession(t, sessionDB)

	b := New(Config{
		SessionDBPath: sessionDB,
		Key:           "test passphrase",
		Dir:           filepath.Join(dir, "backups"),
		Keep:          2,
	})

	var paths []string
	for i := 0; i < 4; i++ {
		res, err := b.BackupNow(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, res.Path)
	}

	names, err := b.listBackups()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 {
		t.Fatalf("got %d backups retained, want 2 (Keep=2)", len(names))
	}
	// The two retained must be the two NEWEST (the last two BackupNow calls).
	if names[0] != filepath.Base(paths[2]) || names[1] != filepath.Base(paths[3]) {
		t.Errorf("retained backups = %v, want the two newest (%s, %s)", names, filepath.Base(paths[2]), filepath.Base(paths[3]))
	}
}

// TestBackupNeverLogsPassphraseOrKey covers the DoD's hard rule
// (SENSITIVE): a full backup→restore cycle, including a WRONG-passphrase
// restore attempt, must never leak the passphrase into logs.
func TestBackupNeverLogsPassphraseOrKey(t *testing.T) {
	const secretPassphrase = "sk-super-secret-do-not-log-xyz789"

	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	dir := t.TempDir()
	sessionDB := filepath.Join(dir, "wa.db")
	newFakeSession(t, sessionDB)

	b := New(Config{
		SessionDBPath: sessionDB,
		Key:           secretPassphrase,
		Dir:           filepath.Join(dir, "backups"),
		Keep:          5,
	})
	res, err := b.BackupNow(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Correct restore, then a wrong-passphrase restore (error path is
	// exactly where a careless implementation would leak the passphrase
	// into an error message).
	if err := Restore(res.Path, filepath.Join(dir, "restored.db"), secretPassphrase); err != nil {
		t.Fatal(err)
	}
	if err := Restore(res.Path, filepath.Join(dir, "restored2.db"), "wrong passphrase"); err == nil {
		t.Fatal("restore with wrong passphrase unexpectedly succeeded")
	} else if strings.Contains(err.Error(), secretPassphrase) {
		t.Fatalf("error message leaked the passphrase: %v", err)
	}

	if strings.Contains(buf.String(), secretPassphrase) {
		t.Fatalf("passphrase leaked into logs: %s", buf.String())
	}
}
