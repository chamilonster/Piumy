// SPDX-License-Identifier: AGPL-3.0-only
package mcpserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"golang.org/x/crypto/bcrypt"

	"pimywa/internal/governor"
	"pimywa/internal/router"
	"pimywa/internal/state"
	"pimywa/internal/store"
)

// newTestServerWithAuth is newTestServer plus a configurable MCPAuthConfigured
// (item G — reset_dashboard_password is the one tool
// that behaves differently based on it).
func newTestServerWithAuth(t *testing.T, authConfigured bool) (*store.Store, *server.MCPServer, context.Context) {
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

	srv := New(ctx, Deps{
		Store: st, State: sm, Router: rtMgr, Gov: gov, AgentIdle: time.Minute,
		MCPAuthConfigured: authConfigured,
	})
	return st, srv, ctx
}

// TestResetDashboardPasswordRefusedWithoutMCPAuth covers the DoD directly
// (item G's fail-closed rule, 2026-07-03): an open MCP server
// (no PIMYWA_MCP_KEY) has no owner-identity boundary, so this tool must
// refuse outright rather than silently behave like every other tool — and
// must not touch the store either way.
func TestResetDashboardPasswordRefusedWithoutMCPAuth(t *testing.T) {
	st, srv, ctx := newTestServerWithAuth(t, false)
	out := callTool(t, ctx, srv, "reset_dashboard_password", map[string]any{})
	if !strings.Contains(out, "PIMYWA_MCP_KEY") {
		t.Fatalf("expected refusal mentioning PIMYWA_MCP_KEY, got: %s", out)
	}
	if v, err := st.KVGet(store.SettingDashPassHash); err != nil || v != "" {
		t.Fatalf("store must be untouched on refusal: value=%q err=%v", v, err)
	}
}

// TestResetDashboardPasswordGeneratesAndPersists covers the DoD directly:
// with MCP auth configured, omitting new_password returns a freshly
// generated password whose bcrypt hash is exactly what gets persisted to
// the store override key (dashboard.makeLoginHandler reads this key first).
func TestResetDashboardPasswordGeneratesAndPersists(t *testing.T) {
	st, srv, ctx := newTestServerWithAuth(t, true)
	out := callTool(t, ctx, srv, "reset_dashboard_password", map[string]any{})

	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil || len(resp.Result.Content) == 0 {
		t.Fatalf("unexpected tool response shape: %s (err=%v)", out, err)
	}
	var payload struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &payload); err != nil {
		t.Fatalf("could not parse payload: %v — raw: %s", err, resp.Result.Content[0].Text)
	}
	if len(payload.Password) != 24 {
		t.Fatalf("generated password = %q, want 24 hex chars", payload.Password)
	}

	hash, err := st.KVGet(store.SettingDashPassHash)
	if err != nil || hash == "" {
		t.Fatalf("expected a persisted hash, got %q err=%v", hash, err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(payload.Password)); err != nil {
		t.Fatalf("persisted hash does not match the returned password: %v", err)
	}
}

// TestResetDashboardPasswordAcceptsSpecificPassword covers the DoD's second
// mode ("la IA se la puede escribir o devolver" — WRITE a
// specific one, not just generate/return): new_password is honored verbatim
// when it meets the minimum length, and rejected when it doesn't.
func TestResetDashboardPasswordAcceptsSpecificPassword(t *testing.T) {
	st, srv, ctx := newTestServerWithAuth(t, true)

	out := callTool(t, ctx, srv, "reset_dashboard_password", map[string]any{"new_password": "short"})
	if !strings.Contains(out, "at least 8 characters") {
		t.Fatalf("expected a min-length rejection, got: %s", out)
	}

	out = callTool(t, ctx, srv, "reset_dashboard_password", map[string]any{"new_password": "correct-horse-battery-staple"})
	if !strings.Contains(out, "correct-horse-battery-staple") {
		t.Fatalf("expected the specific password echoed back, got: %s", out)
	}
	hash, _ := st.KVGet(store.SettingDashPassHash)
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("correct-horse-battery-staple")); err != nil {
		t.Fatalf("persisted hash does not match the specific password: %v", err)
	}
}
