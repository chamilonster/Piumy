package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"piumy-gateway/internal/eventbus"
	"piumy-gateway/internal/governor"
	"piumy-gateway/internal/router"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
)

// TestPrivilegedToolsRefuseNonBoss covers the DoD directly: every
// DB-admin/group-profile tool refuses under a danger dispatch (caution
// would behave identically — same bossOnlyTools check).
// set_chat_rules/set_is_boss/set_type_rules/set_confirmation_mode/
// set_config_level moved to their own tests (S10, ct-2026-07-30-1349),
// approve_draft/discard_draft moved to theirs (S12, ct-2026-07-30-1622) —
// none of these five are a uniform "boss-only" refusal anymore.
// TestPrivilegedToolsRefuseNonBoss covers the ONE tool T148
// (ct-2026-09-07-1644) left in bossOnlyTools — set_kill_switch, by the
// boss's own explicit reasoning (the anti-ban emergency stop, "no cagarla
// con whatsapp espamear su ip"). Every group/profile tool that used to be
// tested here left bossOnlyTools; see
// TestFormerlyBossOnlyToolsNowWorkFromNonBoss below for their (opposite)
// coverage.
func TestPrivilegedToolsRefuseNonBoss(t *testing.T) {
	gate := NewGate()
	_, srv, ctx := serverWithGate(t, gate)
	chat := "55500000020@c.us"
	if err := gate.RegisterDispatch("nonce-priv", chat, LevelDanger, "term-priv", 0, ""); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, "term-priv")
	callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-priv"})

	out := callTool(t, termCtx, srv, "set_kill_switch", map[string]any{"kill": true})
	if !strings.Contains(out, "boss-only") {
		t.Errorf("set_kill_switch under danger = %s, want boss-only refusal", out)
	}
}

// TestFormerlyBossOnlyToolsNowWorkFromNonBoss is T148's own regression
// (ct-2026-09-07-1644, boss verbatim, direct: "quiero que quites ese
// candado, si le pido a un agente que cree un grupo, quiero que lo haga" —
// and the same day: "resulta ser que piumy estaba lleno de candados
// estupidos"). These 6 left bossOnlyTools — a danger-level dispatch (the
// least-trusted level) must be able to reach every one of them now, not
// just the group ones (T145's own retry logic — unrelated — is why
// create_group isn't asserted "succeeds" here the same way; a nil
// GroupProfile in this test's server means it refuses for a DIFFERENT
// reason, "not available", which is exactly the point: never "boss-only").
func TestFormerlyBossOnlyToolsNowWorkFromNonBoss(t *testing.T) {
	gate := NewGate()
	_, srv, ctx := serverWithGate(t, gate)
	chat := "55500000021@c.us"
	if err := gate.RegisterDispatch("nonce-open", chat, LevelDanger, "term-open", 0, ""); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, "term-open")
	callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-open"})

	cases := []struct {
		tool string
		args map[string]any
	}{
		{"create_group", map[string]any{"name": "g", "participants": []string{chat}}},
		{"add_participant", map[string]any{"group_id": "g@g.us", "participant_id": chat}},
		{"set_group_icon", map[string]any{"group_id": "g@g.us", "data_url": "data:image/jpeg;base64,x"}},
		{"set_group_description", map[string]any{"group_id": "g@g.us", "description": "d"}},
		{"set_profile_status", map[string]any{"status": "n"}},
		{"promote_group_admin", map[string]any{"group_id": "g@g.us", "participant_id": chat}},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			out := callTool(t, termCtx, srv, c.tool, c.args)
			if strings.Contains(out, "boss-only") {
				t.Errorf("%s under danger = %s, want it to reach its own handler (never a boss-only refusal) — T148 removed this lock", c.tool, out)
			}
		})
	}
}

// ── S10 (ct-2026-07-30-1349) — the six self-gated config/rules tools ──────

// TestRulesAndIsBossToolsAlwaysBlockedViaMCP is S10's core regression: the
// boss's own decision ("nunca por MCP, ni el principal") — set_type_rules/
// set_is_boss must refuse under EVERY circumstance an MCP caller can be in:
// no dispatch, a danger dispatch, a boss dispatch, AND the exact incident
// Citrino reproduced live — calling from the PRINCIPAL terminal with no
// dispatch bound at all (the old principal-bypass let this straight
// through with zero gating). set_default_rules WAS in this list too — T79
// (ct-2026-08-27-2034) removed the tool entirely along with the global
// rules tier it gated, nothing left to test the refusal of. set_chat_rules
// WAS also in this list, S10 through T30 — T31 (ct-2026-08-06-0244)
// unblocked it, unconditionally; see
// TestSetChatRulesUnconditionallyAllowedViaMCP below for its (opposite)
// coverage.
func TestRulesAndIsBossToolsAlwaysBlockedViaMCP(t *testing.T) {
	cases := []struct {
		tool string
		args map[string]any
	}{
		{"set_type_rules", map[string]any{"rules": "x"}},
		{"set_is_boss", map[string]any{"chat_id": "55500000020@c.us", "is_boss": true}},
	}

	for _, c := range cases {
		t.Run(c.tool+"/no dispatch", func(t *testing.T) {
			gate := NewGate()
			_, srv, ctx := serverWithGate(t, gate)
			out := callTool(t, withTerminalID(ctx, "term-nodispatch"), srv, c.tool, c.args)
			if !strings.Contains(out, "MCP") {
				t.Errorf("%s with no dispatch = %s, want the MCP-blocked refusal", c.tool, out)
			}
		})
		t.Run(c.tool+"/danger dispatch", func(t *testing.T) {
			gate := NewGate()
			_, srv, ctx := serverWithGate(t, gate)
			chat := "55500000020@c.us"
			if err := gate.RegisterDispatch("nonce-danger", chat, LevelDanger, "term-danger", 0, ""); err != nil {
				t.Fatal(err)
			}
			termCtx := withTerminalID(ctx, "term-danger")
			callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-danger"})
			out := callTool(t, termCtx, srv, c.tool, c.args)
			if !strings.Contains(out, "MCP") {
				t.Errorf("%s under danger = %s, want the MCP-blocked refusal", c.tool, out)
			}
		})
		t.Run(c.tool+"/boss dispatch", func(t *testing.T) {
			gate := NewGate()
			_, srv, ctx := serverWithGate(t, gate)
			chat := "55500000047@c.us"
			termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
			out := callTool(t, termCtx, srv, c.tool, c.args)
			if !strings.Contains(out, "MCP") {
				t.Errorf("%s under an active BOSS dispatch = %s, want the MCP-blocked refusal (never via MCP, not even boss-level)", c.tool, out)
			}
		})
		t.Run(c.tool+"/principal terminal, no dispatch (the live incident)", func(t *testing.T) {
			gate := NewGate()
			_, srv, ctx := serverWithGateAndPrincipal(t, gate)
			// Deliberately NO gate.RegisterDispatch call — this is exactly
			// the scenario Citrino reproduced live: the principal terminal
			// calling with nothing bound, which the old principal bypass
			// let straight through.
			out := callTool(t, withTerminalID(ctx, principalTerm), srv, c.tool, c.args)
			if !strings.Contains(out, "MCP") {
				t.Errorf("%s from the principal terminal with no dispatch = %s, want the MCP-blocked refusal", c.tool, out)
			}
		})
	}
}

// TestSetChatRulesUnconditionallyAllowedViaMCP is T31 (ct-2026-08-06-0244)
// — the mirror image of TestRulesAndIsBossToolsAlwaysBlockedViaMCP above,
// same 4 scenarios, opposite outcome. The boss rejected two prior gated
// versions of this contract (a single key, then a double key) verbatim:
// "No pongas condiciones, que la skill recomiende nada mas... ya es
// responsabilidad del usuario." set_chat_rules must work from EVERY one of
// these, including a terminal with no dispatch bound at all — there is no
// code-level condition left to test for its absence, so each case also
// confirms the store actually got written, not just a success string.
func TestSetChatRulesUnconditionallyAllowedViaMCP(t *testing.T) {
	t.Run("no dispatch", func(t *testing.T) {
		gate := NewGate()
		st, srv, ctx := serverWithGate(t, gate)
		chat := "55500000020@c.us"
		out := callTool(t, withTerminalID(ctx, "term-scr-none"), srv, "set_chat_rules", map[string]any{"chat_id": chat, "rules": "regla sin dispatch"})
		if !strings.Contains(out, "rules set") {
			t.Errorf("set_chat_rules with no dispatch = %s, want success", out)
		}
		c, ok, err := st.GetChat(chat)
		if err != nil || !ok || c.Rules != "regla sin dispatch" {
			t.Errorf("chat rules after set_chat_rules = %+v, ok=%v, err=%v, want %q", c, ok, err, "regla sin dispatch")
		}
	})
	t.Run("danger dispatch, different chat than its own", func(t *testing.T) {
		gate := NewGate()
		st, srv, ctx := serverWithGate(t, gate)
		ownChat := "55500000048@c.us"
		otherChat := "55500000049@c.us"
		if err := gate.RegisterDispatch("nonce-scr-danger", ownChat, LevelDanger, "term-scr-danger", 0, ""); err != nil {
			t.Fatal(err)
		}
		termCtx := withTerminalID(ctx, "term-scr-danger")
		callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-scr-danger"})
		// No chat-scoping either — a danger dispatch for ownChat can still
		// set rules on otherChat, per the boss's explicit "no conditions".
		out := callTool(t, termCtx, srv, "set_chat_rules", map[string]any{"chat_id": otherChat, "rules": "regla de otro chat"})
		if !strings.Contains(out, "rules set") {
			t.Errorf("set_chat_rules under danger, targeting another chat = %s, want success (no scoping)", out)
		}
		c, ok, err := st.GetChat(otherChat)
		if err != nil || !ok || c.Rules != "regla de otro chat" {
			t.Errorf("otherChat rules after set_chat_rules = %+v, ok=%v, err=%v, want %q", c, ok, err, "regla de otro chat")
		}
	})
	t.Run("boss dispatch", func(t *testing.T) {
		gate := NewGate()
		st, srv, ctx := serverWithGate(t, gate)
		chat := "55500000047@c.us"
		termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
		out := callTool(t, termCtx, srv, "set_chat_rules", map[string]any{"chat_id": chat, "rules": "regla desde boss"})
		if !strings.Contains(out, "rules set") {
			t.Errorf("set_chat_rules under boss = %s, want success", out)
		}
		c, ok, err := st.GetChat(chat)
		if err != nil || !ok || c.Rules != "regla desde boss" {
			t.Errorf("chat rules after set_chat_rules = %+v, ok=%v, err=%v, want %q", c, ok, err, "regla desde boss")
		}
	})
	t.Run("principal terminal, no dispatch", func(t *testing.T) {
		gate := NewGate()
		st, srv, ctx := serverWithGateAndPrincipal(t, gate)
		chat := "55500000020@c.us"
		out := callTool(t, withTerminalID(ctx, principalTerm), srv, "set_chat_rules", map[string]any{"chat_id": chat, "rules": "regla desde principal"})
		if !strings.Contains(out, "rules set") {
			t.Errorf("set_chat_rules from the principal terminal with no dispatch = %s, want success", out)
		}
		c, ok, err := st.GetChat(chat)
		if err != nil || !ok || c.Rules != "regla desde principal" {
			t.Errorf("chat rules after set_chat_rules = %+v, ok=%v, err=%v, want %q", c, ok, err, "regla desde principal")
		}
	})
}

// TestSetConfirmationModeSplitByValue: "always"/"discretion" restrict and
// are always allowed (Part B); "none" liberates and requires the CURRENT
// dispatch to be boss-level (Part C).
func TestSetConfirmationModeSplitByValue(t *testing.T) {
	t.Run("restrict values allowed under danger", func(t *testing.T) {
		gate := NewGate()
		_, srv, ctx := serverWithGate(t, gate)
		chat := "55500000020@c.us"
		if err := gate.RegisterDispatch("nonce-cm-danger", chat, LevelDanger, "term-cm-danger", 0, ""); err != nil {
			t.Fatal(err)
		}
		termCtx := withTerminalID(ctx, "term-cm-danger")
		callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-cm-danger"})

		for _, mode := range []string{"always", "discretion"} {
			out := callTool(t, termCtx, srv, "set_confirmation_mode", map[string]any{"chat_id": chat, "mode": mode})
			if !strings.Contains(out, "confirmation_mode set to "+mode) {
				t.Errorf("set_confirmation_mode(%s) under danger = %s, want success (restricting is always allowed)", mode, out)
			}
		}
	})
	t.Run("none refused under danger", func(t *testing.T) {
		gate := NewGate()
		_, srv, ctx := serverWithGate(t, gate)
		chat := "55500000048@c.us"
		if err := gate.RegisterDispatch("nonce-cm-danger2", chat, LevelDanger, "term-cm-danger2", 0, ""); err != nil {
			t.Fatal(err)
		}
		termCtx := withTerminalID(ctx, "term-cm-danger2")
		callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-cm-danger2"})

		out := callTool(t, termCtx, srv, "set_confirmation_mode", map[string]any{"chat_id": chat, "mode": "none"})
		if !strings.Contains(out, "boss-level") {
			t.Errorf("set_confirmation_mode(none) under danger = %s, want the boss-level-required refusal", out)
		}
	})
	t.Run("none allowed under boss", func(t *testing.T) {
		gate := NewGate()
		_, srv, ctx := serverWithGate(t, gate)
		chat := "55500000049@c.us"
		termCtx := bossDispatchContext(t, gate, srv, ctx, chat)

		out := callTool(t, termCtx, srv, "set_confirmation_mode", map[string]any{"chat_id": chat, "mode": "none"})
		if !strings.Contains(out, "confirmation_mode set to none") {
			t.Errorf("set_confirmation_mode(none) under boss = %s, want success (the boss can free it)", out)
		}
	})
}

// TestPrivilegedToolsWorkForBoss is the positive half: a boss dispatch
// reaches each handler (DB-admin tools actually succeed; group/profile
// ones report "not available" since no fake openwa.Adapter is wired here —
// that still proves the gate let them through, not the point being tested).
func TestPrivilegedToolsWorkForBoss(t *testing.T) {
	gate := NewGate()
	_, srv, ctx := serverWithGate(t, gate)
	chat := "55500000047@c.us"
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)

	if out := callTool(t, termCtx, srv, "set_confirmation_mode", map[string]any{"chat_id": chat, "mode": "always"}); !strings.Contains(out, "confirmation_mode set") {
		t.Errorf("set_confirmation_mode under boss = %s, want success", out)
	}

	for _, tool := range []string{"create_group", "add_participant", "set_group_icon", "set_group_description", "set_profile_status"} {
		args := map[string]any{
			"name": "g", "participants": []string{chat}, "group_id": "g@g.us", "participant_id": chat,
			"data_url": "data:image/jpeg;base64,x", "description": "d", "status": "n",
		}
		out := callTool(t, termCtx, srv, tool, args)
		if strings.Contains(out, "boss-only") {
			t.Errorf("%s under boss = %s, want it to reach the handler (not be gated)", tool, out)
		}
		if !strings.Contains(out, "not available") {
			t.Errorf("%s under boss with no GroupProfile wired = %s, want the nil-safe \"not available\" message", tool, out)
		}
	}
}

// ── S12 (ct-2026-07-30-1622) — approve_draft/discard_draft, same split as S10 ──

// TestApproveDraftRequiresBossDispatch is S12's core regression: approve_draft
// sends → liberates a confirm-mode chat's held reply, same shape as S10's
// set_confirmation_mode("none")/set_config_level("auto") — before this, its
// ONLY real gate was bossOnlyTools, which the principal-terminal bypass
// skips entirely (verified live, Citrino, 2026-07-30: the principal could
// approve and send a retained draft with no boss ask at all). Covers every
// way a non-boss caller can reach it, including the exact incident shape
// (principal terminal, nothing bound).
// TestApproveDraftRequiresBossOrApproverDispatch (Aprobador P1,
// ct-2026-07-31-0610, widened isActiveBossDispatch -> isActiveApproverDispatch:
// see approver_test.go for the approver POSITIVE case and the full
// no-escalation suite) — a plain caution/danger dispatch, or no dispatch at
// all, must still refuse. Only boss or approver-level get through.
func TestApproveDraftRequiresBossOrApproverDispatch(t *testing.T) {
	t.Run("no dispatch", func(t *testing.T) {
		gate := NewGate()
		_, srv, ctx := serverWithGate(t, gate)
		out := callTool(t, withTerminalID(ctx, "term-ad-none"), srv, "approve_draft", map[string]any{"id": 1})
		if !strings.Contains(out, "boss- or approver-level") {
			t.Errorf("approve_draft with no dispatch = %s, want the boss-/approver-level-required refusal", out)
		}
	})
	t.Run("danger dispatch", func(t *testing.T) {
		gate := NewGate()
		_, srv, ctx := serverWithGate(t, gate)
		chat := "55500000050@c.us"
		if err := gate.RegisterDispatch("nonce-ad-danger", chat, LevelDanger, "term-ad-danger", 0, ""); err != nil {
			t.Fatal(err)
		}
		termCtx := withTerminalID(ctx, "term-ad-danger")
		callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-ad-danger"})
		out := callTool(t, termCtx, srv, "approve_draft", map[string]any{"id": 1})
		if !strings.Contains(out, "boss- or approver-level") {
			t.Errorf("approve_draft under danger = %s, want the boss-/approver-level-required refusal", out)
		}
	})
	t.Run("principal terminal, no dispatch (the live incident)", func(t *testing.T) {
		gate := NewGate()
		_, srv, ctx := serverWithGateAndPrincipal(t, gate)
		// Deliberately NO gate.RegisterDispatch — this is the exact shape
		// Citrino found: the principal terminal calling with nothing bound,
		// approving and sending a draft the boss never asked about.
		out := callTool(t, withTerminalID(ctx, principalTerm), srv, "approve_draft", map[string]any{"id": 1})
		if !strings.Contains(out, "boss- or approver-level") {
			t.Errorf("approve_draft from the principal terminal with no dispatch = %s, want the boss-/approver-level-required refusal", out)
		}
	})
	// The positive case (boss dispatch → approved) is covered by
	// TestApproveAndDiscardDraft below — the boss's own wanted feature
	// ("aprobá los pendientes") still works unchanged.
}

// TestDiscardDraftAlwaysAllowed: discard never sends — restricting is
// always allowed, same reasoning as set_confirmation_mode's
// "always"/"discretion" values. No dispatch level required, at all.
func TestDiscardDraftAlwaysAllowed(t *testing.T) {
	t.Run("no dispatch", func(t *testing.T) {
		gate := NewGate()
		st, srv, ctx := serverWithGate(t, gate)
		chat := "55500000051@c.us"
		if err := st.TouchChat(chat, "C", 1); err != nil {
			t.Fatal(err)
		}
		if err := st.AddDraft(chat, "hola", "m", 1); err != nil {
			t.Fatal(err)
		}
		drafts, err := st.PendingDrafts(10)
		if err != nil || len(drafts) != 1 {
			t.Fatalf("setup: PendingDrafts = %+v, err=%v, want 1", drafts, err)
		}
		out := callTool(t, withTerminalID(ctx, "term-dd-none"), srv, "discard_draft", map[string]any{"id": drafts[0].ID})
		if !strings.Contains(out, "discarded") {
			t.Errorf("discard_draft with no dispatch = %s, want success (restricting is always allowed)", out)
		}
	})
	t.Run("danger dispatch", func(t *testing.T) {
		gate := NewGate()
		st, srv, ctx := serverWithGate(t, gate)
		chat := "55500000052@c.us"
		if err := st.TouchChat(chat, "C", 1); err != nil {
			t.Fatal(err)
		}
		if err := st.AddDraft(chat, "hola", "m", 1); err != nil {
			t.Fatal(err)
		}
		drafts, err := st.PendingDrafts(10)
		if err != nil || len(drafts) != 1 {
			t.Fatalf("setup: PendingDrafts = %+v, err=%v, want 1", drafts, err)
		}
		if err := gate.RegisterDispatch("nonce-dd-danger", chat, LevelDanger, "term-dd-danger", 0, ""); err != nil {
			t.Fatal(err)
		}
		termCtx := withTerminalID(ctx, "term-dd-danger")
		callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-dd-danger"})
		out := callTool(t, termCtx, srv, "discard_draft", map[string]any{"id": drafts[0].ID})
		if !strings.Contains(out, "discarded") {
			t.Errorf("discard_draft under danger = %s, want success (restricting is always allowed)", out)
		}
	})
}

// TestApproveAndDiscardDraft: boss can resolve a pending draft either way.
func TestApproveAndDiscardDraft(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000053@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.AddDraft(chat, "hola", "m", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.AddDraft(chat, "chau", "m", 2); err != nil {
		t.Fatal(err)
	}
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)

	drafts, err := st.PendingDrafts(10)
	if err != nil || len(drafts) != 2 {
		t.Fatalf("setup: PendingDrafts = %+v, err=%v, want 2", drafts, err)
	}

	if out := callTool(t, termCtx, srv, "approve_draft", map[string]any{"id": drafts[0].ID}); !strings.Contains(out, "approved") {
		t.Errorf("approve_draft = %s, want success", out)
	}
	pending, err := st.PendingOutbox(10)
	if err != nil || len(pending) != 1 {
		t.Errorf("PendingOutbox after approve = %+v, err=%v, want 1", pending, err)
	}

	if out := callTool(t, termCtx, srv, "discard_draft", map[string]any{"id": drafts[1].ID}); !strings.Contains(out, "discarded") {
		t.Errorf("discard_draft = %s, want success", out)
	}

	remaining, err := st.PendingDrafts(10)
	if err != nil || len(remaining) != 0 {
		t.Errorf("PendingDrafts after resolving both = %+v, err=%v, want empty", remaining, err)
	}
}

// ── T15 (ct-2026-08-05-123241) — reject_draft/edit_draft ──

// TestRejectDraftAlwaysAllowed: same "restricting is free" reasoning as
// discard_draft — reject never sends, so no dispatch level is required.
func TestRejectDraftAlwaysAllowed(t *testing.T) {
	t.Run("no dispatch", func(t *testing.T) {
		gate := NewGate()
		st, srv, ctx := serverWithGate(t, gate)
		chat := "55500000054@c.us"
		if err := st.TouchChat(chat, "C", 1); err != nil {
			t.Fatal(err)
		}
		if err := st.AddMessage(store.Message{ChatJID: chat, ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
			t.Fatal(err)
		}
		if err := st.AddDraftWithConfirmer(chat, "borrador", "m", "", "", 1, 2); err != nil {
			t.Fatal(err)
		}
		drafts, err := st.PendingDrafts(10)
		if err != nil || len(drafts) != 1 {
			t.Fatalf("setup: PendingDrafts = %+v, err=%v, want 1", drafts, err)
		}
		out := callTool(t, withTerminalID(ctx, "term-rd-none"), srv, "reject_draft", map[string]any{"id": drafts[0].ID, "reason": "muy formal"})
		if !strings.Contains(out, "redispatched for another attempt") {
			t.Errorf("reject_draft with no dispatch = %s, want success (restricting is always allowed)", out)
		}
	})
	t.Run("danger dispatch", func(t *testing.T) {
		gate := NewGate()
		st, srv, ctx := serverWithGate(t, gate)
		chat := "55500000055@c.us"
		if err := st.TouchChat(chat, "C", 1); err != nil {
			t.Fatal(err)
		}
		if err := st.AddDraft(chat, "hola", "m", 1); err != nil {
			t.Fatal(err)
		}
		drafts, err := st.PendingDrafts(10)
		if err != nil || len(drafts) != 1 {
			t.Fatalf("setup: PendingDrafts = %+v, err=%v, want 1", drafts, err)
		}
		if err := gate.RegisterDispatch("nonce-rd-danger", chat, LevelDanger, "term-rd-danger", 0, ""); err != nil {
			t.Fatal(err)
		}
		termCtx := withTerminalID(ctx, "term-rd-danger")
		callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-rd-danger"})
		out := callTool(t, termCtx, srv, "reject_draft", map[string]any{"id": drafts[0].ID, "reason": "no"})
		if !strings.Contains(out, "redispatched") {
			t.Errorf("reject_draft under danger = %s, want success (restricting is always allowed)", out)
		}
	})
}

// TestRejectDraftReopensMessageForRedispatch confirms the reason lands on
// the draft (store.RejectDraft) AND the triggering message becomes pending
// again (store.MarkPendingBefore) — the part that actually makes the
// redispatch happen, not just a status flip nobody acts on.
func TestRejectDraftReopensMessageForRedispatch(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000056@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMode(chat, "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetActive(chat, true); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddDraftWithConfirmer(chat, "borrador", "m", "", "", 1, 2); err != nil {
		t.Fatal(err)
	}
	// Mirrors what the real `draft` tool does right after AddDraftWithConfirmer
	// (send.go) — the draft alone doesn't close the gate on m1, this does.
	if err := st.MarkHandledBefore(chat, 1); err != nil {
		t.Fatal(err)
	}
	drafts, err := st.PendingDrafts(10)
	if err != nil || len(drafts) != 1 {
		t.Fatalf("setup: PendingDrafts = %+v, err=%v, want 1", drafts, err)
	}
	// Confirm the setup, so the reopening below is a real assertion, not a
	// no-op: the draft holds m1, it's not sitting in PendingDedicated too.
	if pending, err := st.PendingDedicated(10); err != nil || len(pending) != 0 {
		t.Fatalf("setup: PendingDedicated = %+v, err=%v, want empty (draft holds it)", pending, err)
	}

	out := callTool(t, withTerminalID(ctx, "term-rd-reopen"), srv, "reject_draft", map[string]any{"id": drafts[0].ID, "reason": "sé más breve"})
	if !strings.Contains(out, "redispatched for another attempt") {
		t.Errorf("reject_draft = %s, want success", out)
	}

	pending, err := st.PendingDedicated(10)
	if err != nil || len(pending) != 1 || pending[0].ID != "m1" {
		t.Fatalf("PendingDedicated after reject = %+v, err=%v, want m1 pending again", pending, err)
	}
	reason, _, ok, err := st.PendingRejectionNote(chat)
	if err != nil || !ok || reason != "sé más breve" {
		t.Errorf("PendingRejectionNote after reject = reason=%q ok=%v err=%v, want the rejection reason", reason, ok, err)
	}
}

// TestRejectDraftStopsAtCap: rejecting the MaxDraftRounds-th round records
// the reason but does NOT reopen the message — no 4th automatic attempt.
func TestRejectDraftStopsAtCap(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000057@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMode(chat, "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetActive(chat, true); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkHandledBefore(chat, 1); err != nil {
		t.Fatal(err)
	}

	// Drive the chain to round MaxDraftRounds via 2 prior reject→redraft
	// cycles, then reject the 3rd (capping) round through the tool itself.
	if err := st.AddDraftWithConfirmer(chat, "intento 1", "m", "", "", 1, 2); err != nil {
		t.Fatal(err)
	}
	d1, _ := st.PendingDrafts(10)
	if _, _, _, ok, err := st.RejectDraft(d1[0].ID, "no"); err != nil || !ok {
		t.Fatalf("RejectDraft round 1: ok=%v err=%v", ok, err)
	}
	if err := st.AddDraftWithConfirmer(chat, "intento 2", "m", "", "", 1, 3); err != nil {
		t.Fatal(err)
	}
	d2, _ := st.PendingDrafts(10)
	if _, _, _, ok, err := st.RejectDraft(d2[0].ID, "sigue mal"); err != nil || !ok {
		t.Fatalf("RejectDraft round 2: ok=%v err=%v", ok, err)
	}
	if err := st.AddDraftWithConfirmer(chat, "intento 3", "m", "", "", 1, 4); err != nil {
		t.Fatal(err)
	}
	d3, err := st.PendingDrafts(10)
	if err != nil || len(d3) != 1 || d3[0].Round != store.MaxDraftRounds {
		t.Fatalf("setup: PendingDrafts = %+v, err=%v, want round %d", d3, err, store.MaxDraftRounds)
	}

	out := callTool(t, withTerminalID(ctx, "term-rd-cap"), srv, "reject_draft", map[string]any{"id": d3[0].ID, "reason": "definitivamente no"})
	if !strings.Contains(out, "cap") {
		t.Errorf("reject_draft at round %d = %s, want a cap-reached message, no automatic redispatch", store.MaxDraftRounds, out)
	}

	if pending, err := st.PendingDedicated(10); err != nil || len(pending) != 0 {
		t.Errorf("PendingDedicated after capped reject = %+v, err=%v, want empty (no automatic redispatch)", pending, err)
	}
	reason, _, ok, err := st.PendingRejectionNote(chat)
	if err != nil || !ok || reason != "definitivamente no" {
		t.Errorf("PendingRejectionNote after capped reject = reason=%q ok=%v err=%v, want the reason still recorded", reason, ok, err)
	}
}

// TestEditDraftAlwaysAllowed: same "restricting is free" reasoning — edit
// never sends, so no dispatch level is required.
func TestEditDraftAlwaysAllowed(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000058@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.AddDraft(chat, "borrador original", "m", 1); err != nil {
		t.Fatal(err)
	}
	drafts, err := st.PendingDrafts(10)
	if err != nil || len(drafts) != 1 {
		t.Fatalf("setup: PendingDrafts = %+v, err=%v, want 1", drafts, err)
	}

	out := callTool(t, withTerminalID(ctx, "term-ed-none"), srv, "edit_draft", map[string]any{"id": drafts[0].ID, "text": "texto corregido"})
	if !strings.Contains(out, "edited") {
		t.Errorf("edit_draft with no dispatch = %s, want success (restricting is always allowed)", out)
	}

	after, err := st.PendingDrafts(10)
	if err != nil || len(after) != 1 || after[0].Text != "texto corregido" || after[0].Status != "pending" {
		t.Errorf("PendingDrafts after edit = %+v, err=%v, want text updated and status still pending", after, err)
	}
}

// TestApproveDiscardRejectEditDraftPublishEventbus is T16
// (ct-2026-08-05-123257): every draft resolution the boss can trigger via
// MCP ("aprobá los pendientes") must nudge the dashboard's SSE
// auto-refresh — a draft resolved from WhatsApp that the dashboard only
// notices on its next 15s poll is the exact "salió tarde" Citrino flagged.
func TestApproveDiscardRejectEditDraftPublishEventbus(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	routerPath := filepath.Join(dir, "router.json")
	if err := os.WriteFile(routerPath, []byte(`{"default_mode":"dedicated"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rt := router.NewManager(routerPath)
	sm := state.NewManager(filepath.Join(dir, "status.json"), 8)
	gate := NewGate()
	bus := eventbus.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := New(ctx, Deps{Store: st, State: sm, Router: rt, Gate: gate, AgentIdle: time.Minute, Bus: bus})

	ch, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	drain := func() {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for the draft eventbus nudge")
		}
	}

	chat := "55500000059@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)

	if err := st.AddDraft(chat, "uno", "m", 1); err != nil {
		t.Fatal(err)
	}
	d, _ := st.PendingDrafts(10)
	callTool(t, termCtx, srv, "approve_draft", map[string]any{"id": d[0].ID})
	drain() // approve_draft

	if err := st.AddDraft(chat, "dos", "m", 2); err != nil {
		t.Fatal(err)
	}
	d, _ = st.PendingDrafts(10)
	callTool(t, withTerminalID(ctx, "term-evt-discard"), srv, "discard_draft", map[string]any{"id": d[0].ID})
	drain() // discard_draft

	if err := st.AddDraft(chat, "tres", "m", 3); err != nil {
		t.Fatal(err)
	}
	d, _ = st.PendingDrafts(10)
	callTool(t, withTerminalID(ctx, "term-evt-reject"), srv, "reject_draft", map[string]any{"id": d[0].ID, "reason": "no"})
	drain() // reject_draft

	if err := st.AddDraft(chat, "cuatro", "m", 4); err != nil {
		t.Fatal(err)
	}
	d, _ = st.PendingDrafts(10)
	callTool(t, withTerminalID(ctx, "term-evt-edit"), srv, "edit_draft", map[string]any{"id": d[0].ID, "text": "cuatro corregido"})
	drain() // edit_draft
}

// TestSetKillSwitchFlipsGovernorAndState is the H2+H3 hardening regression
// (ct-2026-07-10-0540): governor.SetKill and state.SetMuted had no
// production caller — this confirms set_kill_switch flips BOTH together,
// not just one (they were divergent flags before this fix).
func TestSetKillSwitchFlipsGovernorAndState(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	routerPath := filepath.Join(dir, "router.json")
	if err := os.WriteFile(routerPath, []byte(`{"default_mode":"dedicated"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rt := router.NewManager(routerPath)
	sm := state.NewManager(filepath.Join(dir, "status.json"), 8)
	gov := governor.NewLimiter(10, time.Minute)
	gate := NewGate()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := New(ctx, Deps{Store: st, State: sm, Router: rt, Gate: gate, Governor: gov, AgentIdle: time.Minute})

	chat := "55500000060@c.us"
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)

	if out := callTool(t, termCtx, srv, "set_kill_switch", map[string]any{"kill": true}); !strings.Contains(out, "kill switch set to true") {
		t.Errorf("set_kill_switch(true) = %s, want success", out)
	}
	if !gov.Killed() {
		t.Error("governor.Killed() = false after set_kill_switch(true)")
	}
	if !sm.Snapshot().Muted {
		t.Error("state Muted = false after set_kill_switch(true)")
	}
	// T19 (ct-2026-08-05-1249): must ALSO persist — main.go's
	// restoreKillSwitch reads exactly this KV key back on the next boot.
	if !st.SettingBool(store.SettingKillSwitch, false) {
		t.Error("store.SettingKillSwitch = false after set_kill_switch(true), want persisted true — a restart would silently release the brake")
	}

	if out := callTool(t, termCtx, srv, "set_kill_switch", map[string]any{"kill": false}); !strings.Contains(out, "kill switch set to false") {
		t.Errorf("set_kill_switch(false) = %s, want success", out)
	}
	if gov.Killed() {
		t.Error("governor.Killed() = true after set_kill_switch(false)")
	}
	if sm.Snapshot().Muted {
		t.Error("state Muted = true after set_kill_switch(false)")
	}
	if st.SettingBool(store.SettingKillSwitch, false) {
		t.Error("store.SettingKillSwitch = true after set_kill_switch(false), want persisted false")
	}
}

// ── set_capi_connector (ct-2026-07-29, agentes paso 3) ──────────────────────
// connector_string became optional and name (new) lets MCP finally cover the
// principal's display name — before this, set_capi_connector was the ONLY
// way to touch the principal by MCP and always required re-pasting full
// credentials just to rename it.

// serverWithGateAndPrincipal is serverWithGate plus PrincipalTerminalID —
// set_capi_connector needs a boss dispatch (bossOnlyTools gate) AND a
// configured principal (its own internal check), neither of which
// serverWithGate alone sets up.
func serverWithGateAndPrincipal(t *testing.T, gate *Gate) (*store.Store, *server.MCPServer, context.Context) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	routerPath := filepath.Join(dir, "router.json")
	if err := os.WriteFile(routerPath, []byte(`{"default_mode":"dedicated"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv := New(ctx, Deps{
		Store: st, State: state.NewManager(filepath.Join(dir, "status.json"), 8),
		Router: router.NewManager(routerPath), AgentIdle: time.Minute, Gate: gate,
		PrincipalTerminalID: principalTerm,
	})
	return st, srv, ctx
}

// TestSetCAPIConnectorFullString is S6's own regression (ct-2026-07-30-
// 031048): before this, the LAN IP a connector_string carried was discarded
// and the endpoint forced to 127.0.0.1 — breaking the Raspberry Pi case
// (gateway on the Pi, agent on another machine of the same LAN) the
// isAllowedPrincipalEndpoint fix (ct-2026-07-29) exists to enable. The IP
// now survives, and store.SetPrincipalAgent (not this tool) decides whether
// it's allowed.
func TestSetCAPIConnectorFullString(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGateAndPrincipal(t, gate)
	chat := "55500000061@c.us"
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)

	out := callTool(t, termCtx, srv, "set_capi_connector", map[string]any{
		"connector_string": "192.168.1.83:8787 chat_id:57582399-1400-485c-ab6a-22febe672344 pin:3y+X4bmS0Yau91l/6cJAjw==",
	})
	if strings.Contains(out, `"isError":true`) {
		t.Fatalf("set_capi_connector failed: %s", out)
	}
	a, ok, err := st.PrincipalAgent(principalTerm)
	if err != nil || !ok {
		t.Fatalf("PrincipalAgent: ok=%v err=%v", ok, err)
	}
	if a.Endpoint != "http://192.168.1.83:8787" {
		t.Errorf("Endpoint = %q, want http://192.168.1.83:8787 (a private LAN IP must survive, not be forced to 127.0.0.1)", a.Endpoint)
	}
	if a.AntennaTerminalID != "57582399-1400-485c-ab6a-22febe672344" || a.Pinpass != "3y+X4bmS0Yau91l/6cJAjw==" {
		t.Errorf("terminal/pin = %q/%q, want the parsed values", a.AntennaTerminalID, a.Pinpass)
	}
}

// TestSetCAPIConnectorRejectsPublicIP: isAllowedPrincipalEndpoint is the ONE
// place that decides what's accepted — a public IP in connector_string must
// still be refused, exactly as SetPrincipalAgent already refuses it via
// POST /api/admin/agent-update (same underlying store call).
func TestSetCAPIConnectorRejectsPublicIP(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGateAndPrincipal(t, gate)
	chat := "55500000062@c.us"
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)

	out := callTool(t, termCtx, srv, "set_capi_connector", map[string]any{
		"connector_string": "203.0.113.7:8787 chat_id:x pin:y",
	})
	if !strings.Contains(out, "isError\":true") {
		t.Fatalf("set_capi_connector with a public IP = %s, want it refused", out)
	}
	a, _, err := st.PrincipalAgent(principalTerm)
	if err != nil {
		t.Fatal(err)
	}
	if a.Endpoint != "" {
		t.Errorf("Endpoint after a refused public-IP connector_string = %q, want empty (never persisted)", a.Endpoint)
	}
}

// TestSetCAPIConnectorNameOnly is the core paso-3 regression: renaming the
// principal must NOT require re-pasting connector_string, and must not
// touch the already-configured endpoint/terminal/pin.
func TestSetCAPIConnectorNameOnly(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGateAndPrincipal(t, gate)
	chat := "55500000063@c.us"
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)

	if err := st.SetPrincipalAgent("", "http://127.0.0.1:8787", "ant-term", "s3cr3t=="); err != nil {
		t.Fatal(err)
	}

	out := callTool(t, termCtx, srv, "set_capi_connector", map[string]any{"name": "Boss"})
	if strings.Contains(out, `"isError":true`) {
		t.Fatalf("set_capi_connector (name only) failed: %s", out)
	}
	a, _, err := st.PrincipalAgent(principalTerm)
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "Boss" {
		t.Errorf("Name = %q, want Boss", a.Name)
	}
	if a.Endpoint != "http://127.0.0.1:8787" || a.AntennaTerminalID != "ant-term" || a.Pinpass != "s3cr3t==" {
		t.Errorf("credentials changed unexpectedly: %+v, want untouched", a)
	}
}

func TestSetCAPIConnectorRequiresAtLeastOneField(t *testing.T) {
	gate := NewGate()
	_, srv, ctx := serverWithGateAndPrincipal(t, gate)
	chat := "55500000064@c.us"
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)

	out := callTool(t, termCtx, srv, "set_capi_connector", map[string]any{})
	if !strings.Contains(out, "isError\":true") {
		t.Errorf("set_capi_connector with neither field = %s, want an error", out)
	}
}

func TestSetCAPIConnectorRequiresPrincipalConfigured(t *testing.T) {
	gate := NewGate()
	_, srv, ctx := serverWithGate(t, gate) // no PrincipalTerminalID
	chat := "55500000065@c.us"
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)

	out := callTool(t, termCtx, srv, "set_capi_connector", map[string]any{"name": "Boss"})
	if !strings.Contains(out, "no principal configured") {
		t.Errorf("set_capi_connector with no PrincipalTerminalID = %s, want the clear 'no principal configured' error", out)
	}
}
