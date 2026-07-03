// SPDX-License-Identifier: AGPL-3.0-only
package gateway

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"pimywa/internal/eventbus"
	"pimywa/internal/governor"
	"pimywa/internal/router"
	"pimywa/internal/state"
	"pimywa/internal/store"
)

// newEventBusTestGateway builds a real *Gateway (session DB + whatsmeow
// client store, no live connection needed — onMessage's own resolvePN
// short-circuits for a plain @s.whatsapp.net JID, see its doc comment) with
// a router that allows one specific chat.
func newEventBusTestGateway(t *testing.T, chatJID string) *Gateway {
	t.Helper()
	dir := newPostLinkTestDir(t) // reuses postlink_test.go's Windows-safe temp dir helper
	st, err := store.Open(filepath.Join(dir, "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	routerPath := filepath.Join(dir, "router.json")
	cfg := `{"allow_all":false,"default_mode":"advanced","whitelist":["` + chatJID + `"]}`
	if err := os.WriteFile(routerPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	rtMgr := router.NewManager(routerPath)
	sm := state.NewManager(filepath.Join(dir, "status.json"), 8)
	gov := governor.NewLimiter(1000, time.Minute)

	gw, err := New(Config{SessionDB: filepath.Join(dir, "wa.db")}, st, sm, rtMgr, gov)
	if err != nil {
		t.Fatal(err)
	}
	return gw
}

func inboundEvent(chatJID string, id string, ts time.Time) *events.Message {
	jid := types.NewJID(chatJID[:len(chatJID)-len("@s.whatsapp.net")], types.DefaultUserServer)
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: jid, Sender: jid, IsFromMe: false},
			ID:            types.MessageID(id),
			Timestamp:     ts,
		},
		Message: &waE2E.Message{Conversation: proto.String("hola")},
	}
}

// TestOnMessagePublishesEvent is the core wiring proof for entregable D: a
// real inbound message, stored successfully, publishes a
// {type:"message", jid, ts} nudge on the bus — driven through the actual
// onMessage handler, not a mock.
func TestOnMessagePublishesEvent(t *testing.T) {
	chatJID := "56911112222@s.whatsapp.net"
	gw := newEventBusTestGateway(t, chatJID)

	bus := eventbus.New()
	gw.SetBus(bus)
	ch, unsubscribe := bus.Subscribe()
	defer unsubscribe()

	now := time.Now()
	gw.onMessage(inboundEvent(chatJID, "msg1", now))

	select {
	case e := <-ch:
		if e.Type != "message" || e.JID != chatJID {
			t.Errorf("event = %+v, want type=message jid=%s", e, chatJID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the published event")
	}
}

// TestOnMessageNoBusIsSafe covers the fail-safe default: a Gateway with no
// bus wired (SetBus never called, the common case for tests/dev builds) must
// not panic when a message arrives.
func TestOnMessageNoBusIsSafe(t *testing.T) {
	chatJID := "56911112222@s.whatsapp.net"
	gw := newEventBusTestGateway(t, chatJID)
	gw.onMessage(inboundEvent(chatJID, "msg1", time.Now())) // must not panic
}

// TestOnMessageBlockedByRouterDoesNotPublish covers that a message the
// router refuses (not whitelisted) is never stored AND never published —
// the event bus must not leak notifications about traffic the anti-ban
// whitelist gate already dropped.
func TestOnMessageBlockedByRouterDoesNotPublish(t *testing.T) {
	allowedJID := "56911112222@s.whatsapp.net"
	gw := newEventBusTestGateway(t, allowedJID)

	bus := eventbus.New()
	gw.SetBus(bus)
	ch, unsubscribe := bus.Subscribe()
	defer unsubscribe()

	gw.onMessage(inboundEvent("56900000000@s.whatsapp.net", "msg1", time.Now())) // not whitelisted

	select {
	case e := <-ch:
		t.Fatalf("got an event for a non-whitelisted chat: %+v, want none", e)
	case <-time.After(100 * time.Millisecond):
		// expected: no event
	}
}
