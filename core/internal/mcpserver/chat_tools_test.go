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

	"pimywa/internal/governor"
	"pimywa/internal/router"
	"pimywa/internal/state"
	"pimywa/internal/store"
)

// callTool drives a registered tool end-to-end via HandleMessage (no HTTP
// transport needed) and returns the raw JSON-RPC response.
func callTool(t *testing.T, ctx context.Context, srv *server.MCPServer, name string, args map[string]any) string {
	t.Helper()
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": args},
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	resp := srv.HandleMessage(ctx, raw)
	out, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func newTestServer(t *testing.T) (*store.Store, *server.MCPServer, context.Context) {
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

	mcpSrv := New(ctx, Deps{Store: st, State: sm, Router: rtMgr, Gov: gov, AgentIdle: time.Minute})
	return st, mcpSrv, ctx
}

func TestGetChatToolNotFound(t *testing.T) {
	_, srv, ctx := newTestServer(t)
	out := callTool(t, ctx, srv, "get_chat", map[string]any{"chat_id": "nobody@s.whatsapp.net"})
	if !strings.Contains(out, "chat not found") {
		t.Errorf("get_chat on unknown JID = %s, want a not-found error", out)
	}
}

// TestGetChatReturnsEffectiveRules covers the DoD directly (1959): get_chat
// resolves particular → type → default, never the raw per-chat value —
// the agent must not see the hierarchy, only the answer.
func TestGetChatReturnsEffectiveRules(t *testing.T) {
	st, srv, ctx := newTestServer(t)
	jid := "56999999999@s.whatsapp.net"
	if err := st.TouchChat(jid, "Contact", 1); err != nil {
		t.Fatal(err)
	}

	// Default set: get_chat reflects it even with no particular rules.
	if err := st.SetDefaultRules("default: sé breve"); err != nil {
		t.Fatal(err)
	}
	if out := callTool(t, ctx, srv, "get_chat", map[string]any{"chat_id": jid}); !strings.Contains(out, "default: sé breve") {
		t.Errorf("get_chat with only default rules = %s, want the resolved default", out)
	}

	// Type rules set: beats default.
	if err := st.SetTypeRules("individual", "tipo: formal"); err != nil {
		t.Fatal(err)
	}
	if out := callTool(t, ctx, srv, "get_chat", map[string]any{"chat_id": jid}); !strings.Contains(out, "tipo: formal") || strings.Contains(out, "sé breve") {
		t.Errorf("get_chat with type rules set = %s, want the type rules, not the default", out)
	}

	// Particular rules set: beats everything.
	if err := st.SetChatRules(jid, "particular: VIP"); err != nil {
		t.Fatal(err)
	}
	if out := callTool(t, ctx, srv, "get_chat", map[string]any{"chat_id": jid}); !strings.Contains(out, "particular: VIP") || strings.Contains(out, "tipo: formal") {
		t.Errorf("get_chat with particular rules set = %s, want the particular rules, not the type", out)
	}
}

func TestSetChatStatusAndGetChat(t *testing.T) {
	st, srv, ctx := newTestServer(t)
	jid := "56955147132@s.whatsapp.net"
	if err := st.TouchChat(jid, "Boss", 1); err != nil {
		t.Fatal(err)
	}

	if out := callTool(t, ctx, srv, "set_chat_status", map[string]any{"chat_id": jid, "status": "whitelist"}); !strings.Contains(out, "status set") {
		t.Fatalf("set_chat_status = %s", out)
	}
	// The tool result text is itself JSON-encoded inside the outer JSON-RPC
	// response, so quotes come through backslash-escaped.
	if out := callTool(t, ctx, srv, "get_chat", map[string]any{"chat_id": jid}); !strings.Contains(out, `\"status\": \"whitelist\"`) {
		t.Errorf("get_chat after set_chat_status = %s, want status=whitelist", out)
	}

	// agent_exclusive:<id> claim form.
	if out := callTool(t, ctx, srv, "set_chat_status", map[string]any{"chat_id": jid, "status": "agent_exclusive:opus-1"}); !strings.Contains(out, "status set") {
		t.Fatalf("set_chat_status agent_exclusive = %s", out)
	}

	// Rejected: not one of the fixed values, not agent_exclusive:<id>.
	if out := callTool(t, ctx, srv, "set_chat_status", map[string]any{"chat_id": jid, "status": "banana"}); !strings.Contains(out, "must be") {
		t.Errorf("set_chat_status invalid = %s, want a validation error", out)
	}
}

func TestSetChatActive(t *testing.T) {
	st, srv, ctx := newTestServer(t)
	jid := "56955147132@s.whatsapp.net"
	if err := st.TouchChat(jid, "Boss", 1); err != nil {
		t.Fatal(err)
	}

	if out := callTool(t, ctx, srv, "set_chat_active", map[string]any{"chat_id": jid, "active": true}); !strings.Contains(out, "active set") {
		t.Fatalf("set_chat_active = %s", out)
	}
	if out := callTool(t, ctx, srv, "get_chat", map[string]any{"chat_id": jid}); !strings.Contains(out, `\"active\": true`) {
		t.Errorf("get_chat after set_chat_active = %s, want active=true", out)
	}
}

func TestGetChatGroupsTool(t *testing.T) {
	st, srv, ctx := newTestServer(t)
	member := "56955147132@s.whatsapp.net"
	if err := st.AddGroupMember(member, "group1@g.us"); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, ctx, srv, "get_chat_groups", map[string]any{"chat_id": member})
	if !strings.Contains(out, "group1@g.us") {
		t.Errorf("get_chat_groups = %s, want group1@g.us listed", out)
	}
}

func TestGetMediaTool(t *testing.T) {
	st, srv, ctx := newTestServer(t)
	jid := "56955147132@s.whatsapp.net"
	if err := st.AddMedia(store.Media{MsgID: "m1", ChatJID: jid, Path: "/data/media/m1.jpg", Mime: "image/jpeg", Size: 1024, TS: 10}); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, ctx, srv, "get_media", map[string]any{"chat_id": jid})
	if !strings.Contains(out, "image/jpeg") {
		t.Errorf("get_media = %s, want the media entry", out)
	}
}

func TestValidChatStatus(t *testing.T) {
	cases := map[string]bool{
		"whitelist":         true,
		"blacklist":         true,
		"new":               true,
		"ignored":           true,
		"agent_exclusive:x": true,
		"agent_exclusive:":  false,
		"agent_exclusive":   false,
		"banana":            false,
		"":                  false,
	}
	for in, want := range cases {
		if got := validChatStatus(in); got != want {
			t.Errorf("validChatStatus(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestGetPendingTool(t *testing.T) {
	st, srv, ctx := newTestServer(t)
	jid := "56955147132@s.whatsapp.net"

	if err := st.AddMessage(store.Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if out := callTool(t, ctx, srv, "get_pending", map[string]any{}); !strings.Contains(out, jid) {
		t.Fatalf("get_pending after inbound = %s, want the chat listed", out)
	}

	if err := st.AddMessage(store.Message{ChatJID: jid, ID: "m2", FromMe: true, Text: "hi", TS: 2, Model: "claude-opus-4-8"}); err != nil {
		t.Fatal(err)
	}
	if out := callTool(t, ctx, srv, "get_pending", map[string]any{}); strings.Contains(out, jid) {
		t.Fatalf("get_pending after our reply = %s, want the chat gone (we have the last word)", out)
	}
}

// TestSetChatMemoryAndContextTools covers the DoD's "memory/context sí por
// MCP": both are agent-writable and get_chat reflects them.
func TestSetChatMemoryAndContextTools(t *testing.T) {
	st, srv, ctx := newTestServer(t)
	jid := "56999999999@s.whatsapp.net"
	if err := st.TouchChat(jid, "Contact", 1); err != nil {
		t.Fatal(err)
	}

	if out := callTool(t, ctx, srv, "set_chat_memory", map[string]any{"chat_id": jid, "memory": "le gusta el café"}); !strings.Contains(out, "memory set") {
		t.Fatalf("set_chat_memory = %s", out)
	}
	if out := callTool(t, ctx, srv, "set_chat_context", map[string]any{"chat_id": jid, "context": "cliente frecuente"}); !strings.Contains(out, "context set") {
		t.Fatalf("set_chat_context = %s", out)
	}

	out := callTool(t, ctx, srv, "get_chat", map[string]any{"chat_id": jid})
	if !strings.Contains(out, "le gusta el caf") || !strings.Contains(out, "cliente frecuente") {
		t.Errorf("get_chat after set_chat_memory/set_chat_context = %s, want both fields reflected", out)
	}
}

// TestGetDraftsTool covers read-only visibility of pending auto-reply drafts.
func TestGetDraftsTool(t *testing.T) {
	st, srv, ctx := newTestServer(t)
	jid := "56999999999@s.whatsapp.net"
	if err := st.AddDraft(jid, "borrador del auto-respondedor", "deepseek-chat", 1); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, ctx, srv, "get_drafts", map[string]any{})
	if !strings.Contains(out, "borrador del auto-respondedor") || !strings.Contains(out, "deepseek-chat") {
		t.Errorf("get_drafts = %s, want the pending draft", out)
	}
}
