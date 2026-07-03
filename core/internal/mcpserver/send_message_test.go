// SPDX-License-Identifier: AGPL-3.0-only
package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pimywa/internal/governor"
	"pimywa/internal/router"
	"pimywa/internal/state"
	"pimywa/internal/store"
)

// TestSendMessageGuardrails covers the anti-ban rules set (2026-07-01
// and 0800): never send outside the whitelist, never emit to a chat with no
// rules, and a WhatsApp group additionally needs a non-"ignored" status.
// Drives the real registered tool end-to-end via HandleMessage (no HTTP
// transport needed) against a real store/router/governor.
func TestSendMessageGuardrails(t *testing.T) {
	dir := t.TempDir()

	st, err := store.Open(filepath.Join(dir, "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	routerPath := filepath.Join(dir, "router.json")
	cfg := `{"allow_all":false,"default_mode":"advanced","whitelist":["56955147132@s.whatsapp.net","12345-67890@g.us","99999-11111@g.us"]}`
	if err := os.WriteFile(routerPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	rtMgr := router.NewManager(routerPath)

	sm := state.NewManager(filepath.Join(dir, "status.json"), 8)
	gov := governor.NewLimiter(10, time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mcpSrv := New(ctx, Deps{Store: st, State: sm, Router: rtMgr, Gov: gov, AgentIdle: time.Minute})
	_, policyVersion := decisionPolicy("")

	// Fixtures: a whitelisted 1-1 contact WITH rules, a whitelisted group
	// that still has its default "ignored" status even though it has rules,
	// and a second whitelisted group with rules AND a non-ignored status.
	if err := st.TouchChat("56955147132@s.whatsapp.net", "Boss", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatRules("56955147132@s.whatsapp.net", "responder normalmente"); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("12345-67890@g.us", "Group", 1); err != nil {
		t.Fatal(err) // defaults to status "ignored" (0800)
	}
	if err := st.SetChatRules("12345-67890@g.us", "solo contestar si te preguntan"); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("99999-11111@g.us", "ActiveGroup", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatRules("99999-11111@g.us", "solo contestar si te preguntan"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatus("99999-11111@g.us", "whitelist"); err != nil {
		t.Fatal(err)
	}

	call := func(to string) string {
		req := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      "send_message",
				"arguments": map[string]any{"to": to, "message": "hola", "model": "claude-opus-4-8", "policy_version": policyVersion},
			},
		}
		raw, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		resp := mcpSrv.HandleMessage(ctx, raw)
		out, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}

	cases := []struct {
		name    string
		to      string
		wantSub string
	}{
		{"no rules at all blocked", "56988887777@s.whatsapp.net", "no rules on this chat"},
		{"group with rules but still ignored blocked", "12345-67890@g.us", "still marked ignored"},
		{"group with rules and not ignored passes", "99999-11111@g.us", "queued for sending"},
		{"non-whitelisted blocked", "56999999999@s.whatsapp.net", "no rules on this chat"},
		{"bare number blocked", "56955147132", "full JID"},
		{"whitelisted passes", "56955147132@s.whatsapp.net", "queued for sending"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if out := call(c.to); !strings.Contains(out, c.wantSub) {
				t.Errorf("send_message(to=%q) = %s, want substring %q", c.to, out, c.wantSub)
			}
		})
	}
}

// TestSendMessageWhitelistGateStillAppliesWithRules covers that having rules
// alone isn't enough — the whitelist gate (independent of 0800's rules law)
// still blocks a chat that has rules but was never whitelisted.
func TestSendMessageWhitelistGateStillAppliesWithRules(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	routerPath := filepath.Join(dir, "router.json")
	if err := os.WriteFile(routerPath, []byte(`{"allow_all":false,"default_mode":"advanced","whitelist":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rtMgr := router.NewManager(routerPath)
	sm := state.NewManager(filepath.Join(dir, "status.json"), 8)
	gov := governor.NewLimiter(10, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mcpSrv := New(ctx, Deps{Store: st, State: sm, Router: rtMgr, Gov: gov, AgentIdle: time.Minute})
	_, policyVersion := decisionPolicy("")

	jid := "56977776666@s.whatsapp.net"
	if err := st.TouchChat(jid, "NotWhitelisted", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatRules(jid, "responder normalmente"); err != nil {
		t.Fatal(err)
	}

	req := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "send_message",
			"arguments": map[string]any{"to": jid, "message": "hola", "model": "claude-opus-4-8", "policy_version": policyVersion},
		},
	}
	raw, _ := json.Marshal(req)
	resp := mcpSrv.HandleMessage(ctx, raw)
	out, _ := json.Marshal(resp)
	if !strings.Contains(string(out), "not in the whitelist") {
		t.Errorf("send_message with rules but no whitelist = %s, want it still blocked by the whitelist gate", out)
	}
}

// TestSendMessageAllowedViaEffectiveRules covers the DoD directly (1959): a
// chat with NO particular rules of its own is still allowed to send if it
// inherits rules from its type or the global default — the hierarchy is
// what the "no rules on this chat" gate actually checks now, not just
// chats.rules.
func TestSendMessageAllowedViaEffectiveRules(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	jid := "56966665555@s.whatsapp.net"
	routerPath := filepath.Join(dir, "router.json")
	if err := os.WriteFile(routerPath, []byte(`{"allow_all":false,"default_mode":"advanced","whitelist":["`+jid+`"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rtMgr := router.NewManager(routerPath)
	sm := state.NewManager(filepath.Join(dir, "status.json"), 8)
	gov := governor.NewLimiter(10, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mcpSrv := New(ctx, Deps{Store: st, State: sm, Router: rtMgr, Gov: gov, AgentIdle: time.Minute})
	_, policyVersion := decisionPolicy("")

	if err := st.TouchChat(jid, "NoParticularRules", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDefaultRules("default: responder con cortesía"); err != nil {
		t.Fatal(err)
	}

	req := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "send_message",
			"arguments": map[string]any{"to": jid, "message": "hola", "model": "claude-opus-4-8", "policy_version": policyVersion},
		},
	}
	raw, _ := json.Marshal(req)
	resp := mcpSrv.HandleMessage(ctx, raw)
	out, _ := json.Marshal(resp)
	if !strings.Contains(string(out), "queued for sending") {
		t.Errorf("send_message with only default rules (no particular) = %s, want it allowed", out)
	}
}
