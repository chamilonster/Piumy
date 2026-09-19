// set_config_level tests — the translation-layer MCP tool over store.
// SetConfigLevel. S10 (ct-2026-07-30-1349) split it: restricting
// (confirm/unattended/ignored) is always allowed, liberating (auto) needs
// the CURRENT dispatch to be boss-level, and level=boss is MCP-blocked
// unconditionally (same as set_is_boss) — no longer a uniform boss-only
// gate, see admin_tools.go's own doc.
package mcpserver

import (
	"strings"
	"testing"
)

// TestSetConfigLevelAutoRequiresBossDispatch covers Part C directly:
// liberating to auto is refused under a non-boss dispatch, allowed under boss.
func TestSetConfigLevelAutoRequiresBossDispatch(t *testing.T) {
	t.Run("refused under danger", func(t *testing.T) {
		gate := NewGate()
		_, srv, ctx := serverWithGate(t, gate)
		chat := "55500000021@c.us"
		if err := gate.RegisterDispatch("nonce-cl-danger", chat, LevelDanger, "term-cl-danger", 0, ""); err != nil {
			t.Fatal(err)
		}
		termCtx := withTerminalID(ctx, "term-cl-danger")
		callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-cl-danger"})

		out := callTool(t, termCtx, srv, "set_config_level", map[string]any{"chat_id": chat, "level": "auto"})
		if !strings.Contains(out, "boss-level") {
			t.Errorf("set_config_level(auto) under danger = %s, want the boss-level-required refusal", out)
		}
	})
	t.Run("allowed under boss", func(t *testing.T) {
		gate := NewGate()
		st, srv, ctx := serverWithGate(t, gate)
		chat := "55500000022@c.us"
		if err := st.TouchChat(chat, "C", 1); err != nil {
			t.Fatal(err)
		}
		termCtx := bossDispatchContext(t, gate, srv, ctx, chat)

		out := callTool(t, termCtx, srv, "set_config_level", map[string]any{"chat_id": chat, "level": "auto"})
		if !strings.Contains(out, "config_level set to auto") {
			t.Errorf("set_config_level(auto) under boss = %s, want success (the boss can free it)", out)
		}
	})
}

// TestSetConfigLevelRestrictAllowedUnderNonBoss covers Part B: confirm/
// unattended/ignored never need any special permission.
func TestSetConfigLevelRestrictAllowedUnderNonBoss(t *testing.T) {
	gate := NewGate()
	_, srv, ctx := serverWithGate(t, gate)
	chat := "55500000023@c.us"
	if err := gate.RegisterDispatch("nonce-cl-restrict", chat, LevelDanger, "term-cl-restrict", 0, ""); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, "term-cl-restrict")
	callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-cl-restrict"})

	for _, level := range []string{"confirm", "unattended", "ignored"} {
		out := callTool(t, termCtx, srv, "set_config_level", map[string]any{"chat_id": chat, "level": level})
		if !strings.Contains(out, "config_level set to "+level) {
			t.Errorf("set_config_level(%s) under danger = %s, want success (restricting is always allowed)", level, out)
		}
	}
}

// TestSetConfigLevelBossLevelAlwaysBlocked: level=boss sets is_boss under
// the hood — same MCP-blocked rule as set_is_boss, no dispatch level
// (including an active boss dispatch) ever allows it.
func TestSetConfigLevelBossLevelAlwaysBlocked(t *testing.T) {
	gate := NewGate()
	_, srv, ctx := serverWithGate(t, gate)
	chat := "55500000024@c.us"
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)

	out := callTool(t, termCtx, srv, "set_config_level", map[string]any{"chat_id": chat, "level": "boss"})
	if !strings.Contains(out, "MCP") {
		t.Errorf("set_config_level(boss) under an active BOSS dispatch = %s, want the MCP-blocked refusal", out)
	}
}

// TestSetConfigLevelWorksForBoss covers the positive path: a boss dispatch
// reaches the handler, which persists via store.SetConfigLevel.
func TestSetConfigLevelWorksForBoss(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000074@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)

	out := callTool(t, termCtx, srv, "set_config_level", map[string]any{"chat_id": chat, "level": "confirm"})
	if !strings.Contains(out, "config_level set to confirm") {
		t.Errorf("set_config_level(confirm) under boss = %s, want success", out)
	}
	c, _, err := st.GetChat(chat)
	if err != nil {
		t.Fatal(err)
	}
	if c.ConfirmationMode != "always" || !c.Active || c.IsBoss {
		t.Errorf("chat after set_config_level(confirm) = %+v, want active=true confirmation_mode=always is_boss=false", c)
	}
}

// TestSetConfigLevelRegisteredAndSelfGated is the structural check: the
// tool exists and is gated (via its own handler, not bossOnlyTools — S10),
// mirroring TestPrivilegedToolsExistAndRegistered for the other DB-admin
// tools.
func TestSetConfigLevelRegisteredAndSelfGated(t *testing.T) {
	found := false
	for _, tool := range listTools(t) {
		if tool.Name == "set_config_level" {
			found = true
		}
	}
	if !found {
		t.Error("set_config_level tool not registered")
	}
	if !selfGatedTools["set_config_level"] {
		t.Error("set_config_level registered but not in selfGatedTools")
	}
	if bossOnlyTools["set_config_level"] {
		t.Error("set_config_level should no longer be in bossOnlyTools (S10) — its authorization is per-argument, enforced in its own handler")
	}
}
