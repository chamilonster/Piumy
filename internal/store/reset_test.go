package store

import "testing"

// tableRowCount is a white-box test helper (same package) — reset_test.go
// verifies actual table contents, something no public Store method exposes
// on purpose (this is test-only introspection, not a real API).
func tableRowCount(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestResetMessagingDataWipesOnlyTheAgreedTables (D4, ct-2026-07-22-2100,
// checkpointed with Citrino): seeds every one of piumy.db's tables with at
// least one row, resets, and asserts EXACTLY the agreed split — every
// resetTables entry empty, kv/agents/usage untouched. Guards against a
// future accidental addition to (or removal from) resetTables silently
// changing what a boss-triggered "partir de 0" actually wipes. (chat_groups
// retired in T18B, ct-2026-08-05-1243 — resetTables went from 8 to 7.)
func TestResetMessagingDataWipesOnlyTheAgreedTables(t *testing.T) {
	s := openTestStore(t)

	// chats + messages
	jid := "1@s.whatsapp.net"
	if err := s.TouchChat(jid, "Ana", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: jid, ID: "m1", Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}
	// group_members
	group := "123@g.us"
	if err := s.UpsertGroupMember(group, jid, "Ana", 1); err != nil {
		t.Fatal(err)
	}
	// media + media_pending
	if err := s.AddMedia(Media{MsgID: "m1", ChatJID: jid, Path: "p", FullPath: "p", Mime: "image/jpeg", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMediaPending(MediaPending{ChatJID: jid, MsgID: "m2", Mime: "image/jpeg", Kind: "photo", TS: 1}); err != nil {
		t.Fatal(err)
	}
	// drafts
	if err := s.AddDraft(jid, "borrador", "claude", 1); err != nil {
		t.Fatal(err)
	}
	// outbox
	if err := s.Enqueue(jid, "saliente", 1); err != nil {
		t.Fatal(err)
	}
	// PRESERVE: kv, agents, usage
	if err := s.KVSet("some_setting", "value"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAgent(Agent{AgentID: "term-a", Role: "secondary"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddUsage(jid, "2026-07-22", UsageDelta{OutChars: 100, Messages: 1}); err != nil {
		t.Fatal(err)
	}

	// Precondition: every table has at least 1 row before reset.
	allTables := append(append([]string{}, resetTables...), "kv", "agents", "usage")
	for _, table := range allTables {
		if got := tableRowCount(t, s, table); got == 0 {
			t.Fatalf("precondition: table %s has 0 rows, want >=1 before reset", table)
		}
	}

	if err := s.ResetMessagingData(); err != nil {
		t.Fatal(err)
	}

	for _, table := range resetTables {
		if got := tableRowCount(t, s, table); got != 0 {
			t.Errorf("table %s after reset = %d rows, want 0", table, got)
		}
	}
	for _, table := range []string{"kv", "agents", "usage"} {
		if got := tableRowCount(t, s, table); got == 0 {
			t.Errorf("table %s after reset = 0 rows, want preserved (>=1)", table)
		}
	}
}
