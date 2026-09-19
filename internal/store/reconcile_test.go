package store

import (
	"path/filepath"
	"testing"
)

func newReconcileTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// countOutcomes tallies a ReconcileIdentities result by Action, the same
// shape the old (merged, renamed int) return used to give tests directly.
func countOutcomes(outcomes []ReconcileOutcome) (merged, renamed, failed int) {
	for _, o := range outcomes {
		switch o.Action {
		case "merged":
			merged++
		case "renamed":
			renamed++
		case "failed":
			failed++
		}
	}
	return
}

// TestReconcileIdentitiesNumberConfigWinsEvenWithRichLIDContent is S13's own
// regression (ct-2026-07-30-1835): the boss's explicit, informed decision
// ("el numero gana siempre... sin mezclas") — even when the @lid chat has
// REAL, rich config and the number's is completely empty/default, the
// merged row keeps the number's (empty) config. This is the exact scenario
// Citrino flagged before the boss decided: the boss's own @lid carries
// "Eres el asistente personal de Camilo..." while his number chat has only
// "sé claro y conciso" — post-merge, the number's thin rules survive, the
// @lid's rich ones are gone. That's the decision, not a bug.
func TestReconcileIdentitiesNumberConfigWinsEvenWithRichLIDContent(t *testing.T) {
	st := newReconcileTestStore(t)
	const lidJID = "555000000000041@lid"
	const numberJID = "55500000044@s.whatsapp.net"

	// The @lid chat: rich config, real history.
	if err := st.TouchChat(lidJID, "Ana G.", 100); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatRules(lidJID, "Eres el asistente personal de Camilo..."); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatMemory(lidJID, "le gusta el café"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatContext(lidJID, "contacto de mucha confianza"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIsBoss(lidJID, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetConfirmationMode(lidJID, "discretion"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(Message{ChatJID: lidJID, ID: "m1", Text: "hola", TS: 100}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddDraft(lidJID, "borrador", "", 100); err != nil {
		t.Fatal(err)
	}
	if err := st.AddUsage(lidJID, "2026-07-10", UsageDelta{OutChars: 10, Messages: 1}); err != nil {
		t.Fatal(err)
	}

	// The number chat: bare, thin config, created independently.
	if err := st.TouchChat(numberJID, "", 50); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatRules(numberJID, "sé claro y conciso"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddUsage(numberJID, "2026-07-10", UsageDelta{OutChars: 5, Messages: 1}); err != nil {
		t.Fatal(err)
	}

	resolve := func(lid string) string {
		if lid == lidJID {
			return numberJID
		}
		return ""
	}
	outcomes, err := st.ReconcileIdentities(resolve)
	if err != nil {
		t.Fatalf("ReconcileIdentities: %v", err)
	}
	merged, renamed, failed := countOutcomes(outcomes)
	if merged != 1 || renamed != 0 || failed != 0 {
		t.Fatalf("merged=%d renamed=%d failed=%d, want 1/0/0", merged, renamed, failed)
	}

	if _, ok, err := st.GetChat(lidJID); err != nil || ok {
		t.Errorf("GetChat(%q) ok=%v err=%v, want the @lid row deleted", lidJID, ok, err)
	}

	c, ok, err := st.GetChat(numberJID)
	if err != nil || !ok {
		t.Fatalf("GetChat(%q): ok=%v err=%v", numberJID, ok, err)
	}
	// The core of the new policy: the number's THIN config survives
	// unchanged, the @lid's richer content is discarded — no exception for
	// "but it was the only one with real content".
	if c.Rules != "sé claro y conciso" {
		t.Errorf("Rules = %q, want the number's own (thin) rules to survive unchanged — número gana, sin mezclas", c.Rules)
	}
	if c.Memory != "" {
		t.Errorf("Memory = %q, want empty — the number never had memory, the @lid's must not leak in", c.Memory)
	}
	if c.Context != "" {
		t.Errorf("Context = %q, want empty, same reasoning as Memory", c.Context)
	}
	if c.IsBoss {
		t.Error("IsBoss = true, want false — the number's own is_boss (false) wins, no OR with the @lid's true")
	}
	if c.ConfirmationMode != "none" {
		t.Errorf("ConfirmationMode = %q, want none (the number's own default) — no more-restrictive blending", c.ConfirmationMode)
	}
	// Non-config fields still legitimately carry over.
	if c.Name != "Ana G." {
		t.Errorf("Name = %q, want the @lid row's — the number had none (display fallback, not config)", c.Name)
	}
	if c.LastTS != 100 {
		t.Errorf("LastTS = %d, want 100 (MAX of 100/50 — a fact, not config)", c.LastTS)
	}

	msgs, err := st.GetMessages(numberJID, 10)
	if err != nil || len(msgs) != 1 || msgs[0].Text != "hola" {
		t.Errorf("GetMessages(%q) = %+v, err=%v, want the @lid chat's message moved over", numberJID, msgs, err)
	}
	drafts, err := st.PendingDrafts(10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range drafts {
		if d.ChatJID == numberJID && d.Text == "borrador" {
			found = true
		}
	}
	if !found {
		t.Errorf("drafts = %+v, want the @lid chat's draft moved to %s", drafts, numberJID)
	}
	var outChars int
	if err := st.db.QueryRow(`SELECT out_chars FROM usage WHERE chat_jid=? AND day=?`, numberJID, "2026-07-10").Scan(&outChars); err != nil {
		t.Fatal(err)
	}
	if outChars != 15 {
		t.Errorf("merged usage out_chars = %d, want 15 (10 + 5, both chats metered the same day)", outChars)
	}
	var ghostUsageRows int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM usage WHERE chat_jid=?`, lidJID).Scan(&ghostUsageRows); err != nil {
		t.Fatal(err)
	}
	if ghostUsageRows != 0 {
		t.Errorf("usage rows still under the @lid jid = %d, want 0 (merged away)", ghostUsageRows)
	}
}

// TestReconcileIdentitiesFillsNameOnlyWhenNumberHasNone covers the display-
// only fallback's OTHER direction: when the number's row ALREADY has a
// name, the @lid's is discarded too — Name gets the same "número gana"
// treatment as everything else, the empty-fallback is the only exception.
func TestReconcileIdentitiesFillsNameOnlyWhenNumberHasNone(t *testing.T) {
	st := newReconcileTestStore(t)
	const lidJID = "555000112@lid"
	const numberJID = "55500000020@s.whatsapp.net"

	if err := st.TouchChat(lidJID, "Nombre del lid", 10); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat(numberJID, "Nombre del número", 20); err != nil {
		t.Fatal(err)
	}

	resolve := func(lid string) string {
		if lid == lidJID {
			return numberJID
		}
		return ""
	}
	if _, err := st.ReconcileIdentities(resolve); err != nil {
		t.Fatal(err)
	}

	c, ok, err := st.GetChat(numberJID)
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	if c.Name != "Nombre del número" {
		t.Errorf("Name = %q, want the number's own name to survive (it already had one)", c.Name)
	}
}

// TestReconcileIdentitiesRenamesWhenNoGhostExists covers the simple case:
// no phone-number row exists yet, so the @lid chat is just re-keyed, no
// merge/data-loss risk.
func TestReconcileIdentitiesRenamesWhenNoGhostExists(t *testing.T) {
	st := newReconcileTestStore(t)
	const lidJID = "555000113@lid"
	const numberJID = "55500000002@s.whatsapp.net"

	if err := st.TouchChat(lidJID, "Bruno", 10); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(Message{ChatJID: lidJID, ID: "m1", Text: "hola", TS: 10}); err != nil {
		t.Fatal(err)
	}

	resolve := func(lid string) string {
		if lid == lidJID {
			return numberJID
		}
		return ""
	}
	outcomes, err := st.ReconcileIdentities(resolve)
	if err != nil {
		t.Fatalf("ReconcileIdentities: %v", err)
	}
	merged, renamed, failed := countOutcomes(outcomes)
	if merged != 0 || renamed != 1 || failed != 0 {
		t.Fatalf("merged=%d renamed=%d failed=%d, want 0/1/0", merged, renamed, failed)
	}

	if _, ok, err := st.GetChat(lidJID); err != nil || ok {
		t.Errorf("GetChat(%q) ok=%v err=%v, want it renamed away", lidJID, ok, err)
	}
	c, ok, err := st.GetChat(numberJID)
	if err != nil || !ok || c.Name != "Bruno" {
		t.Fatalf("GetChat(%q) = %+v ok=%v err=%v, want the renamed chat", numberJID, c, ok, err)
	}
	msgs, err := st.GetMessages(numberJID, 10)
	if err != nil || len(msgs) != 1 {
		t.Errorf("messages after rename = %+v err=%v, want the message to have followed", msgs, err)
	}
}

// TestReconcileIdentitiesSkipsUnresolvedLIDs covers idempotency for the
// common case: no message via that LID has arrived yet, so whatsmeow's own
// map has no answer (resolve returns "") — must be a no-op, not an error,
// so a later run (once resolved) can pick it up.
func TestReconcileIdentitiesSkipsUnresolvedLIDs(t *testing.T) {
	st := newReconcileTestStore(t)
	const lidJID = "111222333x@lid"
	if err := st.TouchChat(lidJID, "Alguien", 10); err != nil {
		t.Fatal(err)
	}

	outcomes, err := st.ReconcileIdentities(func(string) string { return "" })
	if err != nil {
		t.Fatalf("ReconcileIdentities: %v", err)
	}
	if len(outcomes) != 0 {
		t.Fatalf("outcomes=%+v, want none (nothing resolved)", outcomes)
	}
	if _, ok, err := st.GetChat(lidJID); err != nil || !ok {
		t.Errorf("GetChat(%q) ok=%v err=%v, want it left untouched", lidJID, ok, err)
	}
}

// TestReconcileIdentitiesIsIdempotent: once a @lid chat is merged/renamed
// away, a second run must find nothing left to do for it.
func TestReconcileIdentitiesIsIdempotent(t *testing.T) {
	st := newReconcileTestStore(t)
	const lidJID = "555000114@lid"
	const numberJID = "55500000115@s.whatsapp.net"
	if err := st.TouchChat(lidJID, "Carla", 10); err != nil {
		t.Fatal(err)
	}
	resolve := func(lid string) string {
		if lid == lidJID {
			return numberJID
		}
		return ""
	}
	if _, err := st.ReconcileIdentities(resolve); err != nil {
		t.Fatal(err)
	}
	outcomes, err := st.ReconcileIdentities(resolve)
	if err != nil {
		t.Fatalf("second ReconcileIdentities: %v", err)
	}
	if len(outcomes) != 0 {
		t.Errorf("second run outcomes=%+v, want none (already reconciled)", outcomes)
	}
}

// TestReconcileIdentitiesDedupesEchoedMessageBeforeRekey is S13 C-3's own
// regression (ct-2026-07-31-0136): reproduces the real production failure
// found running S13 C-2 against live data — WhatsApp echoes a self-sent
// Note-to-Self message back through BOTH the number and the @lid addressing
// forms, so the gateway had stored the SAME message (same id, same ts, same
// from_me) once under each chat_jid. Before the fix, rekeying the @lid's
// copy into the number's chat_jid violated messages' (chat_jid, id) primary
// key and aborted the entire sweep. Must now merge clean: no error, no
// orphaned @lid row, and the destination's message count reflects the
// duplicate dropped, not summed.
func TestReconcileIdentitiesDedupesEchoedMessageBeforeRekey(t *testing.T) {
	st := newReconcileTestStore(t)
	const lidJID = "55500000000116@lid"
	const numberJID = "55500000042@s.whatsapp.net"

	if err := st.TouchChat(lidJID, "🦁Clever.cat", 100); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat(numberJID, "🦁Clever.cat", 100); err != nil {
		t.Fatal(err)
	}
	// The echo: identical message under both chat_jids.
	echo := Message{ID: "3EB0087A501850A16379A3", FromMe: true, TS: 100, Text: "nota", Type: "text"}
	lidEcho, numberEcho := echo, echo
	lidEcho.ChatJID, numberEcho.ChatJID = lidJID, numberJID
	if err := st.AddMessage(lidEcho); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(numberEcho); err != nil {
		t.Fatal(err)
	}
	// A second, genuinely distinct message under the @lid only — must survive
	// the dedupe and get rekeyed normally, proving dedupe targets the
	// colliding id specifically, not every row on the @lid side.
	if err := st.AddMessage(Message{ChatJID: lidJID, ID: "m-unico", FromMe: true, TS: 101, Text: "otro", Type: "text"}); err != nil {
		t.Fatal(err)
	}

	resolve := func(lid string) string {
		if lid == lidJID {
			return numberJID
		}
		return ""
	}
	outcomes, err := st.ReconcileIdentities(resolve)
	if err != nil {
		t.Fatalf("ReconcileIdentities: %v", err)
	}
	if len(outcomes) != 1 || outcomes[0].Action != "merged" {
		t.Fatalf("outcomes=%+v, want exactly one merged", outcomes)
	}
	if outcomes[0].Deduped != 1 {
		t.Errorf("Deduped = %d, want 1 (the echoed duplicate)", outcomes[0].Deduped)
	}

	if _, ok, err := st.GetChat(lidJID); err != nil || ok {
		t.Errorf("GetChat(%q) ok=%v err=%v, want the @lid row gone, not orphaned", lidJID, ok, err)
	}

	msgs, err := st.GetMessages(numberJID, 10)
	if err != nil {
		t.Fatal(err)
	}
	// 2, not 3: the echoed duplicate dropped, the distinct message moved over.
	if len(msgs) != 2 {
		t.Fatalf("messages under %s = %+v, want exactly 2 (duplicate deduped, not summed)", numberJID, msgs)
	}
	ids := map[string]bool{}
	for _, m := range msgs {
		ids[m.ID] = true
	}
	if !ids["3EB0087A501850A16379A3"] || !ids["m-unico"] {
		t.Errorf("message ids = %v, want both the surviving echo copy and the distinct message", ids)
	}
}

// TestReconcileIdentitiesDedupesEchoedMediaBeforeRekey covers media — the
// only other table whose primary key composes with chat_jid (chat_jid,
// msg_id) — with the same echo scenario as messages above.
func TestReconcileIdentitiesDedupesEchoedMediaBeforeRekey(t *testing.T) {
	st := newReconcileTestStore(t)
	const lidJID = "55500000000116@lid"
	const numberJID = "55500000042@s.whatsapp.net"

	if err := st.TouchChat(lidJID, "🦁Clever.cat", 100); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat(numberJID, "🦁Clever.cat", 100); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMedia(Media{MsgID: "3EB0087A501850A16379A3", ChatJID: lidJID, Path: "p1", TS: 100}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMedia(Media{MsgID: "3EB0087A501850A16379A3", ChatJID: numberJID, Path: "p1", TS: 100}); err != nil {
		t.Fatal(err)
	}

	resolve := func(lid string) string {
		if lid == lidJID {
			return numberJID
		}
		return ""
	}
	outcomes, err := st.ReconcileIdentities(resolve)
	if err != nil {
		t.Fatalf("ReconcileIdentities: %v", err)
	}
	if len(outcomes) != 1 || outcomes[0].Action != "merged" {
		t.Fatalf("outcomes=%+v, want exactly one merged", outcomes)
	}
	if outcomes[0].Deduped != 1 {
		t.Errorf("Deduped = %d, want 1 (the echoed duplicate media row)", outcomes[0].Deduped)
	}

	items, err := st.ListMedia(numberJID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("media under %s = %+v, want exactly 1 (duplicate deduped, not summed)", numberJID, items)
	}
}
