package mcpserver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"piumy-gateway/internal/mcpguard"
	"piumy-gateway/internal/router"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
)

func newFloodGuardTestServer(t *testing.T, guard *mcpguard.Guard) (*store.Store, *server.MCPServer, context.Context) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	rtMgr := router.NewManager(filepath.Join(dir, "router.json"))
	sm := state.NewManager(filepath.Join(dir, "status.json"), 8)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	srv := New(ctx, Deps{Store: st, State: sm, Router: rtMgr, AgentIdle: time.Minute, Guard: guard})
	return st, srv, ctx
}

// TestFloodGuardThrottlesGeneralCalls proves the middleware wraps every
// registered tool, not just send_message/escalate.
//
// ct-2026-08-07: pinned to a fixed clock (SetClock) instead of the guard's
// default time.Now — the 3 calls below must land within the SAME instant
// from the bucket's point of view, and real elapsed time between them isn't
// guaranteed to stay near-zero under heavy system load. bucket.allow's
// refill is real-elapsed-time-based (by design, so production correctly
// grants a fresh token if a client's calls are genuinely spread out) — with
// RatePerMin:2, just 30 real seconds between the 2nd and 3rd call refills
// exactly the one token the 3rd call needs, which flaked this test under
// load without anything actually wrong in the guard.
func TestFloodGuardThrottlesGeneralCalls(t *testing.T) {
	guard := mcpguard.New(mcpguard.Config{RatePerMin: 2, EmitRatePerMin: 100, BlockThreshold: 100})
	frozen := time.Now()
	guard.SetClock(func() time.Time { return frozen })
	_, srv, ctx := newFloodGuardTestServer(t, guard)

	for i := 0; i < 2; i++ {
		out := callTool(t, ctx, srv, "get_status", nil)
		if strings.Contains(out, "rate limited") {
			t.Fatalf("call %d: got throttled early: %s", i, out)
		}
	}
	out := callTool(t, ctx, srv, "get_status", nil)
	if !strings.Contains(out, "rate limited, slow down") {
		t.Errorf("3rd call over the 2/min cap = %s, want a rate-limited error", out)
	}
}

// TestFloodGuardEmitToolsAreStricter: send_message trips the stricter emit
// cap before the general cap would ever kick in.
func TestFloodGuardEmitToolsAreStricter(t *testing.T) {
	guard := mcpguard.New(mcpguard.Config{RatePerMin: 100, EmitRatePerMin: 1, BlockThreshold: 100})
	st, srv, ctx := newFloodGuardTestServer(t, guard)
	_, policyVersion := decisionPolicy("")

	jid := "55500000002@c.us"
	if err := st.TouchChat(jid, "Test", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	sendArgs := map[string]any{"to": jid, "message": "hola", "model": "claude-opus-4-8", "policy_version": policyVersion}

	// 1st call: within the emit cap, fails for an unrelated reason
	// (unwhitelisted) — the point here is only that flood-guard let it through.
	out := callTool(t, ctx, srv, "send_message", sendArgs)
	if strings.Contains(out, "rate limited") {
		t.Fatalf("1st send_message: got flood-guard-throttled: %s", out)
	}
	out = callTool(t, ctx, srv, "send_message", sendArgs)
	if !strings.Contains(out, "rate limited, slow down") {
		t.Errorf("2nd send_message within the same minute = %s, want throttled by the emit cap", out)
	}
}

// TestFloodGuardNilDoesNotPanic covers the fail-safe fallback: New builds a
// default-config Guard when Deps.Guard is nil.
func TestFloodGuardNilDoesNotPanic(t *testing.T) {
	_, srv, ctx := newFloodGuardTestServer(t, nil)
	out := callTool(t, ctx, srv, "get_status", nil)
	if strings.Contains(out, "rate limited") {
		t.Errorf("a single call under the default (120/min) cap: got throttled: %s", out)
	}
}
