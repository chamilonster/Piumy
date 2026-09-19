// Aprobador P1 (ct-2026-07-31-0610): LevelApprover is a single-purpose
// grant — approve/discard drafts, including other chats' — layered on top
// of an otherwise-normal (caution/danger-shaped) dispatch. The risk this
// file exists to catch, per Citrino's own warning: an approver silently
// inheriting something a plain dispatch (or worse, boss) has. Every
// negative case below asserts the SAME refusal a caution/danger dispatch
// would get, not a weaker one.
package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"

	"piumy-gateway/internal/store"
)

func TestApproverStartsLockedNotReady(t *testing.T) {
	gate := NewGate()
	if err := gate.RegisterDispatch("nonce-app-lock", "55500000066@c.us", LevelApprover, "term-app-lock", 0, ""); err != nil {
		t.Fatal(err)
	}
	active, ok := gate.Active("term-app-lock")
	if !ok {
		t.Fatal("Active() = not ok, want a registered dispatch")
	}
	if active.Ready {
		t.Error("Active().Ready = true right after RegisterDispatch(LevelApprover), want false — only Boss is born ready")
	}
	if active.Level != LevelApprover {
		t.Errorf("Active().Level = %q, want %q", active.Level, LevelApprover)
	}
}

// TestApproverReachesReadyViaNormalRitual: get_instructions -> unlock ->
// skip works for an Approver dispatch exactly like caution/danger — the
// gate state machine doesn't special-case level beyond the initial state
// (parent contract ct-2026-07-30-2239, point 5: verify Ready is actually
// reachable via the normal path, don't assume it).
func TestApproverReachesReadyViaNormalRitual(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000067@c.us"
	if err := st.TouchChat(chat, "Aprobadora", 1); err != nil {
		t.Fatal(err)
	}
	if err := gate.RegisterDispatch("nonce-app-ritual", chat, LevelApprover, "term-app-ritual", 0, ""); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, "term-app-ritual")
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-app-ritual"})
	token := unlockToken(t, instr)
	if out := callTool(t, termCtx, srv, "unlock", map[string]any{"token": token}); !strings.Contains(out, "unlocked") {
		t.Fatalf("unlock = %s, want success", out)
	}
	if out := callTool(t, termCtx, srv, "skip", map[string]any{}); !strings.Contains(out, "ready") {
		t.Fatalf("skip = %s, want ready", out)
	}
	active, ok := gate.Active("term-app-ritual")
	if !ok || !active.Ready {
		t.Fatalf("Active() after the ritual = %+v ok=%v, want Ready", active, ok)
	}
}

// TestApproverLockedCannotUseExtraTools: before completing the ritual, the
// approver carve-out must NOT fire — Ready gates it exactly like Boss does.
func TestApproverLockedCannotUseExtraTools(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chatA := "55500000068@c.us"
	if err := st.TouchChat(chatA, "A", 1); err != nil {
		t.Fatal(err)
	}
	if err := gate.RegisterDispatch("nonce-app-locked", chatA, LevelApprover, "term-app-locked", 0, ""); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, "term-app-locked")
	// Deliberately no unlock/skip — still locked.
	for _, tool := range []string{"get_drafts", "get_pending"} {
		out := callTool(t, termCtx, srv, tool, map[string]any{})
		if !strings.Contains(out, "locked") {
			t.Errorf("%s while locked = %s, want the locked refusal", tool, out)
		}
	}
	out := callTool(t, termCtx, srv, "approve_draft", map[string]any{"id": 1})
	if !strings.Contains(out, "boss- or approver-level") {
		t.Errorf("approve_draft while locked = %s, want the boss-/approver-level refusal (Ready is false)", out)
	}
}

// TestApproverApprovesOtherChatsDraft is the positive half of the criterio
// de listo: "un chat marcado aprobador aprueba un borrador de OTRO chat.
// Sale."
func TestApproverApprovesOtherChatsDraft(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	approverChat := "55500000069@c.us"
	otherChat := "55500000070@c.us"
	if err := st.TouchChat(approverChat, "Secretaria", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIsApprover(approverChat, true); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat(otherChat, "Cliente", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.AddDraft(otherChat, "hola, gracias por escribir", "m", 1); err != nil {
		t.Fatal(err)
	}
	drafts, err := st.PendingDrafts(10)
	if err != nil || len(drafts) != 1 {
		t.Fatalf("setup: PendingDrafts = %+v, err=%v, want 1", drafts, err)
	}

	if err := gate.RegisterDispatch("nonce-app-cross", approverChat, LevelApprover, "term-app-cross", 0, ""); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, "term-app-cross")
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-app-cross"})
	token := unlockToken(t, instr)
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})
	callTool(t, termCtx, srv, "skip", map[string]any{})

	// get_drafts: sees the OTHER chat's draft — not scoped to its own chat.
	out := callTool(t, termCtx, srv, "get_drafts", map[string]any{})
	if !strings.Contains(out, otherChat) {
		t.Errorf("get_drafts as approver = %s, want it to include %s's draft", out, otherChat)
	}

	// approve_draft on the OTHER chat's draft: succeeds.
	out = callTool(t, termCtx, srv, "approve_draft", map[string]any{"id": drafts[0].ID})
	if !strings.Contains(out, "approved") {
		t.Fatalf("approve_draft as approver on another chat's draft = %s, want success", out)
	}
	remaining, err := st.PendingDrafts(10)
	if err != nil || len(remaining) != 0 {
		t.Errorf("PendingDrafts after approval = %+v, err=%v, want empty", remaining, err)
	}
}

// approverSetup builds one ready (Ready=true) Approver dispatch bound to
// ownChat, plus a second plain chat (otherChat) the dispatch is NOT bound
// to — the fixture every TestApproverCannotEscalate subtest shares, so
// each case tests against the exact same server/store/ready-dispatch
// instead of re-deriving it (and risking the store/gate mismatch that
// would silently make every negative case pass for the wrong reason).
func approverSetup(t *testing.T) (st *store.Store, srv *server.MCPServer, ctx context.Context, ownChat, otherChat string) {
	t.Helper()
	gate := NewGate()
	st, srv, ctx = serverWithGate(t, gate)
	ownChat = "55500000071@c.us"
	otherChat = "55500000072@c.us"
	if err := st.TouchChat(ownChat, "A", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIsApprover(ownChat, true); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat(otherChat, "B", 1); err != nil {
		t.Fatal(err)
	}
	term := "term-app-esc"
	if err := gate.RegisterDispatch("nonce-app-esc", ownChat, LevelApprover, term, 0, ""); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, term)
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-app-esc"})
	token := unlockToken(t, instr)
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})
	callTool(t, termCtx, srv, "skip", map[string]any{})
	return st, srv, termCtx, ownChat, otherChat
}

// TestApproverCannotEscalate is the negative half Citrino flagged as the
// part that matters most: an approver must not gain ANYTHING beyond the
// four named tools. Every case here must refuse EXACTLY like a plain
// caution dispatch would.
func TestApproverCannotEscalate(t *testing.T) {
	t.Run("cannot liberate confirmation_mode to none", func(t *testing.T) {
		_, srv, termCtx, ownChat, _ := approverSetup(t)
		out := callTool(t, termCtx, srv, "set_confirmation_mode", map[string]any{"chat_id": ownChat, "mode": "none"})
		if !strings.Contains(out, "boss-level") {
			t.Errorf("set_confirmation_mode(none) as approver = %s, want the boss-level-required refusal", out)
		}
	})

	t.Run("cannot liberate config_level to auto", func(t *testing.T) {
		_, srv, termCtx, ownChat, _ := approverSetup(t)
		out := callTool(t, termCtx, srv, "set_config_level", map[string]any{"chat_id": ownChat, "level": "auto"})
		if !strings.Contains(out, "boss-level") {
			t.Errorf("set_config_level(auto) as approver = %s, want the boss-level-required refusal", out)
		}
	})

	t.Run("cannot change any pin, not even its own", func(t *testing.T) {
		_, srv, termCtx, ownChat, otherChat := approverSetup(t)
		for _, target := range []string{ownChat, otherChat} {
			out := callTool(t, termCtx, srv, "set_is_approver", map[string]any{"chat_id": target, "is_approver": true})
			if !strings.Contains(out, "boss-level") {
				t.Errorf("set_is_approver(%s) as approver = %s, want the boss-level-required refusal", target, out)
			}
		}
	})

	t.Run("cannot mark an owner", func(t *testing.T) {
		_, srv, termCtx, ownChat, _ := approverSetup(t)
		out := callTool(t, termCtx, srv, "set_is_boss", map[string]any{"chat_id": ownChat, "is_boss": true})
		if !strings.Contains(out, "MCP") {
			t.Errorf("set_is_boss as approver = %s, want the unconditional MCP-blocked refusal", out)
		}
	})

	// T31 (ct-2026-08-06-0244) reverses this: set_chat_rules is unconditional
	// now, an approver included — there's no gate left for this dispatch
	// level, or any other, to fail. See TestSetChatRulesUnconditionallyAllowedViaMCP
	// (admin_tools_test.go) for the full 4-scenario coverage; this just
	// confirms an approver dispatch specifically isn't some leftover
	// exception.
	t.Run("can touch rules like any other dispatch level", func(t *testing.T) {
		st, srv, termCtx, ownChat, _ := approverSetup(t)
		out := callTool(t, termCtx, srv, "set_chat_rules", map[string]any{"chat_id": ownChat, "rules": "regla desde aprobador"})
		if !strings.Contains(out, "rules set") {
			t.Errorf("set_chat_rules as approver = %s, want success (T31: unconditional)", out)
		}
		c, ok, err := st.GetChat(ownChat)
		if err != nil || !ok || c.Rules != "regla desde aprobador" {
			t.Errorf("chat rules after set_chat_rules as approver = %+v, ok=%v, err=%v, want %q", c, ok, err, "regla desde aprobador")
		}
	})

	// T148 (ct-2026-09-07-1644): create_group left bossOnlyTools along with
	// every other group/profile tool — set_kill_switch is the ONE survivor
	// (the boss's own anti-ban reasoning), the only one still worth
	// asserting here.
	t.Run("cannot reach boss-only tools", func(t *testing.T) {
		_, srv, termCtx, _, _ := approverSetup(t)
		out := callTool(t, termCtx, srv, "set_kill_switch", map[string]any{"kill": true})
		if !strings.Contains(out, "boss-only") {
			t.Errorf("set_kill_switch as approver = %s, want boss-only refusal", out)
		}
	})

	t.Run("does not inherit the OTHER enumeration tools", func(t *testing.T) {
		_, srv, termCtx, _, _ := approverSetup(t)
		for _, tool := range []string{"list_chats", "get_queue", "get_chat_groups", "get_outbox"} {
			out := callTool(t, termCtx, srv, tool, map[string]any{})
			if !strings.Contains(out, "anti-leakage") {
				t.Errorf("%s as approver = %s, want the anti-leakage refusal — only get_drafts/get_pending are granted", tool, out)
			}
		}
	})

	t.Run("still scoped to own chat for ordinary chat-scoped tools", func(t *testing.T) {
		_, srv, termCtx, ownChat, otherChat := approverSetup(t)
		if out := callTool(t, termCtx, srv, "get_chat", map[string]any{"chat_id": ownChat}); strings.Contains(out, "anti-leakage") {
			t.Errorf("get_chat on the approver's OWN chat = %s, want it to succeed (unaffected by the pin)", out)
		}
		if out := callTool(t, termCtx, srv, "get_chat", map[string]any{"chat_id": otherChat}); !strings.Contains(out, "anti-leakage") {
			t.Errorf("get_chat on ANOTHER chat as approver = %s, want the anti-leakage refusal — same as caution/danger", out)
		}
	})
}
