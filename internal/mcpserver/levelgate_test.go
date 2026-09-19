package mcpserver

import (
	"strings"
	"testing"
	"time"
)

// ── T150 (ct-2026-09-07-1839) — a consumed dispatch must never block an
// ungated tool ─────────────────────────────────────────────────────────
//
// Reported live by Citrino de temascal: get_status (never gated — not in
// bossOnlyTools/enumerationTools/chatScopedArg) refused with "locked: this
// dispatch was already consumed", right when the owner asked whether the
// gateway was stuck. Root cause, already localized in the contract:
// levelGateMiddleware computes isGated up front but only CONSULTS it in the
// no-dispatch branch (!ok) — a CONSUMED dispatch (ok==true, Ready==false)
// skips straight into the boss-Ready gate below, blocking a tool that was
// never supposed to need one at all. The principle: a tool that needs no
// gate is INFORMATION, not permission — dispatch state must never block it.

// TestUngatedToolNeverBlockedByConsumedDispatch is the exact reported
// shape: a boss-level dispatch, consumed by a real send, then get_status on
// the SAME terminal.
func TestUngatedToolNeverBlockedByConsumedDispatch(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "555000000090@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")
	callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})
	if active, ok := gate.Active("term-test"); !ok || active.Ready {
		t.Fatalf("setup: Active after consume = %+v, ok=%v, want bound with Ready=false", active, ok)
	}

	out := callTool(t, termCtx, srv, "get_status", map[string]any{})
	if strings.Contains(out, "locked") || strings.Contains(out, `"isError":true`) {
		t.Errorf("get_status with a consumed boss dispatch bound = %s, want it to answer normally — it is never gated at all, dispatch state is not its business", out)
	}
}

// TestUngatedToolNeverBlockedByConsumedNonBossDispatch is the same check
// for a caution-level dispatch — the bug wasn't boss-specific (the !ok
// branch is the only place isGated used to matter, regardless of level).
func TestUngatedToolNeverBlockedByConsumedNonBossDispatch(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "555000000094@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termID := "term-caution-consumed"
	if err := gate.RegisterDispatch("nonce-caution-consumed", chat, LevelCaution, termID, 0, ""); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, termID)
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-caution-consumed"})
	token := unlockToken(t, instr)
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})
	callTool(t, termCtx, srv, "skip", map[string]any{})
	_, policyVersion := decisionPolicy("")
	callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})

	out := callTool(t, termCtx, srv, "get_status", map[string]any{})
	if strings.Contains(out, "locked") || strings.Contains(out, `"isError":true`) {
		t.Errorf("get_status with a consumed caution dispatch bound = %s, want it to answer normally", out)
	}
}

// TestGatedToolStillBlockedByConsumedBossDispatch is the DoD's own guard
// against overcorrecting: T150 must not become "consumed state never
// matters" — a tool that DOES need gating (bossOnlyTools here) must stay
// refused when the only dispatch on this terminal is a consumed one.
func TestGatedToolStillBlockedByConsumedBossDispatch(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "555000000095@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")
	callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})

	out := callTool(t, termCtx, srv, "set_kill_switch", map[string]any{"kill": true})
	if !strings.Contains(out, "locked") {
		t.Errorf("set_kill_switch (bossOnlyTools, gated) with a consumed boss dispatch = %s, want it to STILL refuse — T150 doesn't loosen actually-gated tools", out)
	}
}

// TestGatedToolStillDeniedWithNoDispatchAtAll: the pre-existing default-DENY
// path (the !ok branch) must keep working exactly as before for gated
// tools — T150 only changes what happens for UNGATED tools, this is the
// untouched control case.
func TestGatedToolStillDeniedWithNoDispatchAtAll(t *testing.T) {
	gate := NewGate()
	gate.startedAt = time.Now().Add(-2 * dispatchStaleAfter) // T87: hard-reject path, not the young-gate one
	_, srv, ctx := serverWithGate(t, gate)
	termCtx := withTerminalID(ctx, "term-no-dispatch-at-all")

	out := callTool(t, termCtx, srv, "set_kill_switch", map[string]any{"kill": true})
	if !strings.Contains(out, "no active dispatch") {
		t.Errorf("set_kill_switch with no dispatch at all = %s, want the default-DENY refusal, unchanged by T150", out)
	}
}
