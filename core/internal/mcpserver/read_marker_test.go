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
	"pimywa/internal/router"
	"pimywa/internal/state"
	"pimywa/internal/store"
)

// fakeReadMarker records MarkRead calls instead of touching a real gateway —
// lets get_messages' trigger be tested without a live WhatsApp session.
type fakeReadMarker struct {
	calledChat string
	calledMsgs []store.Message
}

func (f *fakeReadMarker) MarkRead(chatJID string, msgs []store.Message) {
	f.calledChat = chatJID
	f.calledMsgs = msgs
}

// TestGetMessagesTriggersReadMarker covers the DoD: retrieving a chat's
// messages via MCP is what marks them read (not automatic on receipt).
func TestGetMessagesTriggersReadMarker(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	jid := "56955147132@s.whatsapp.net"
	if err := st.AddMessage(store.Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}

	rtMgr := router.NewManager(filepath.Join(dir, "router.json"))
	sm := state.NewManager(filepath.Join(dir, "status.json"), 8)
	gov := governor.NewLimiter(10, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	marker := &fakeReadMarker{}
	srv := New(ctx, Deps{Store: st, State: sm, Router: rtMgr, Gov: gov, AgentIdle: time.Minute, ReadMarker: marker})

	req, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "get_messages", "arguments": map[string]any{"chat_id": jid}},
	})
	srv.HandleMessage(ctx, req)

	if marker.calledChat != jid {
		t.Fatalf("ReadMarker.MarkRead chat = %q, want %q — get_messages must trigger it", marker.calledChat, jid)
	}
	if len(marker.calledMsgs) != 1 || marker.calledMsgs[0].ID != "m1" {
		t.Errorf("ReadMarker.MarkRead msgs = %+v, want the retrieved message", marker.calledMsgs)
	}
}

// TestGetMessagesNilReadMarkerIsSafe covers the "optional dependency"
// contract — a nil ReadMarker (no gateway wired, e.g. dev/sandbox mode) must
// not break get_messages.
func TestGetMessagesNilReadMarkerIsSafe(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	jid := "56955147132@s.whatsapp.net"
	if err := st.AddMessage(store.Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}

	rtMgr := router.NewManager(filepath.Join(dir, "router.json"))
	sm := state.NewManager(filepath.Join(dir, "status.json"), 8)
	gov := governor.NewLimiter(10, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := New(ctx, Deps{Store: st, State: sm, Router: rtMgr, Gov: gov, AgentIdle: time.Minute}) // ReadMarker left nil

	req, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "get_messages", "arguments": map[string]any{"chat_id": jid}},
	})
	resp := srv.HandleMessage(ctx, req)
	out, _ := json.Marshal(resp)
	if !strings.Contains(string(out), "hola") {
		t.Fatalf("get_messages with nil ReadMarker = %s, want it to still return messages", out)
	}
}
