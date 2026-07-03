// SPDX-License-Identifier: AGPL-3.0-only
package mcpserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pimywa/internal/governor"
	"pimywa/internal/mcpguard"
	"pimywa/internal/router"
	"pimywa/internal/state"
	"pimywa/internal/store"
)

func newFloodGuardTestServer(t *testing.T, guard *mcpguard.Guard) (*store.Store, func(tool string, args map[string]any) string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	rtMgr := router.NewManager(filepath.Join(dir, "router.json"))
	sm := state.NewManager(filepath.Join(dir, "status.json"), 8)
	gov := governor.NewLimiter(10, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	mcpSrv := New(ctx, Deps{Store: st, State: sm, Router: rtMgr, Gov: gov, AgentIdle: time.Minute, Guard: guard})

	call := func(tool string, args map[string]any) string {
		req := map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": tool, "arguments": args},
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
	return st, call
}

// TestFloodGuardThrottlesGeneralCalls covers the "middleware transversal"
// requirement end-to-end: a tight general rate
// limit throttles a tool call even for get_status, a tool that has nothing
// to do with anti-flood logic itself — proof the middleware wraps every
// registered tool, not just send_message/escalate.
func TestFloodGuardThrottlesGeneralCalls(t *testing.T) {
	guard := mcpguard.New(mcpguard.Config{RatePerMin: 2, EmitRatePerMin: 100, BlockThreshold: 100})
	_, call := newFloodGuardTestServer(t, guard)

	for i := 0; i < 2; i++ {
		out := call("get_status", nil)
		if strings.Contains(out, "rate limited") {
			t.Fatalf("call %d: got throttled early: %s", i, out)
		}
	}
	out := call("get_status", nil)
	if !strings.Contains(out, "rate limited, slow down") {
		t.Errorf("3rd call over the 2/min cap = %s, want a rate-limited error", out)
	}
}

// TestFloodGuardEmitToolsAreStricter covers the "atención especial"
// requirement: send_message trips the stricter emit cap before the general
// cap would ever kick in.
func TestFloodGuardEmitToolsAreStricter(t *testing.T) {
	guard := mcpguard.New(mcpguard.Config{RatePerMin: 100, EmitRatePerMin: 1, BlockThreshold: 100})
	st, call := newFloodGuardTestServer(t, guard)
	_, policyVersion := decisionPolicy("")

	jid := "56911112222@s.whatsapp.net"
	if err := st.TouchChat(jid, "Test", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDefaultRules("responder normalmente"); err != nil {
		t.Fatal(err)
	}
	sendArgs := map[string]any{"to": jid, "message": "hola", "model": "claude-opus-4-8", "policy_version": policyVersion}

	// 1st send_message: within the emit cap, but the router isn't
	// whitelisted so it fails for an UNRELATED reason — that's fine, the
	// point here is only that the flood guard itself didn't block it.
	out := call("send_message", sendArgs)
	if strings.Contains(out, "rate limited") {
		t.Fatalf("1st send_message: got flood-guard-throttled: %s", out)
	}
	out = call("send_message", sendArgs)
	if !strings.Contains(out, "rate limited, slow down") {
		t.Errorf("2nd send_message within the same minute = %s, want throttled by the emit-specific cap", out)
	}
}

// TestFloodGuardNilDoesNotPanic covers the fail-safe fallback (mcpserver.New
// builds a default-config Guard when Deps.Guard is nil) — the MCP surface
// must never run unprotected just because a caller forgot to wire one.
func TestFloodGuardNilDoesNotPanic(t *testing.T) {
	_, call := newFloodGuardTestServer(t, nil)
	out := call("get_status", nil)
	if strings.Contains(out, "rate limited") {
		t.Errorf("a single call under the default (120/min) cap: got throttled: %s", out)
	}
}
