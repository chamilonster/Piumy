// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
package store

import (
	"path/filepath"
	"testing"
)

// TestSetModeNormalizesAdvancedAlias: SetMode accepts the legacy 'advanced'
// value and persists the canonical 'dedicated' (2026-07-08 rename).
func TestSetModeNormalizesAdvancedAlias(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	jid := "alias@s.whatsapp.net"
	if err := st.SetMode(jid, "advanced"); err != nil {
		t.Fatal(err)
	}
	c, ok, err := st.GetChat(jid)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("chat %s not found after SetMode", jid)
	}
	if c.Mode != "dedicated" {
		t.Fatalf("mode: got %q, want %q (advanced alias must normalize)", c.Mode, "dedicated")
	}
}

// TestMigrateModeRenameBackfillsLegacyRows: a chat row persisted with the old
// 'advanced' value (before the rename) is rewritten to 'dedicated' by the
// migration, idempotently.
func TestMigrateModeRenameBackfillsLegacyRows(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Insert a legacy row directly, bypassing SetMode's normalization, to
	// simulate a DB written before the rename landed.
	jid := "legacy@s.whatsapp.net"
	if _, err := st.db.Exec(`INSERT INTO chats (jid, mode) VALUES (?, 'advanced')`, jid); err != nil {
		t.Fatal(err)
	}
	if err := migrate(st.db); err != nil {
		t.Fatal(err)
	}
	c, ok, err := st.GetChat(jid)
	if err != nil || !ok {
		t.Fatalf("get legacy chat: ok=%v err=%v", ok, err)
	}
	if c.Mode != "dedicated" {
		t.Fatalf("mode after migration: got %q, want %q", c.Mode, "dedicated")
	}
	// Idempotent: a second run touches zero rows and does not error.
	if err := migrate(st.db); err != nil {
		t.Fatalf("second migrate run errored: %v", err)
	}
}
