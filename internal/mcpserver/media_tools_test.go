package mcpserver

import (
	"strings"
	"testing"

	"piumy-gateway/internal/store"
)

func TestGetMediaFullReturnsOriginalAndMetersImage(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000083@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMedia(store.Media{MsgID: "m1", ChatJID: chat, Path: "low.jpg", FullPath: "full.jpg", Mime: "image/jpeg", Size: 999, TS: 1}); err != nil {
		t.Fatal(err)
	}
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)

	out := callTool(t, termCtx, srv, "get_media_full", map[string]any{"chat_id": chat, "msg_id": "m1"})
	if !strings.Contains(out, "full.jpg") {
		t.Errorf("get_media_full = %s, want it to include the full_path", out)
	}

	u, err := st.UsageForDay(chat, store.Today())
	if err != nil {
		t.Fatal(err)
	}
	if u.Images != 1 {
		t.Errorf("Images usage after get_media_full = %d, want 1", u.Images)
	}
}

func TestGetMediaFullNotFound(t *testing.T) {
	gate := NewGate()
	_, srv, ctx := serverWithGate(t, gate)
	chat := "55500000084@c.us"
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)

	out := callTool(t, termCtx, srv, "get_media_full", map[string]any{"chat_id": chat, "msg_id": "nope"})
	if !strings.Contains(out, "media not found") {
		t.Errorf("get_media_full for a missing row = %s, want a not-found error", out)
	}
}

// TestGetMediaDoesNotMeterImages covers the metering incentive (F4-DESIGN
// §8): the low-q get_media is "free" in the usage model — only
// get_media_full charges the image cost.
func TestGetMediaDoesNotMeterImages(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000085@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMedia(store.Media{MsgID: "m1", ChatJID: chat, Path: "low.jpg", FullPath: "full.jpg", TS: 1}); err != nil {
		t.Fatal(err)
	}
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)

	out := callTool(t, termCtx, srv, "get_media", map[string]any{"chat_id": chat})

	u, err := st.UsageForDay(chat, store.Today())
	if err != nil {
		t.Fatal(err)
	}
	if u.Images != 0 {
		t.Errorf("Images usage after get_media (low-q) = %d, want 0 (free)", u.Images)
	}

	// Regression test for the F4d audit HIGH: get_media's response body
	// must never contain full_path — leaking it would let the agent read
	// the uncompressed original directly and never pay get_media_full's
	// usage cost, killing the metering incentive entirely.
	if strings.Contains(out, "full_path") {
		t.Errorf("get_media response contains full_path: %s — the metering incentive requires it stay get_media_full-only", out)
	}
	if strings.Contains(out, "full.jpg") {
		t.Errorf("get_media response leaks the FullPath value: %s", out)
	}
	if !strings.Contains(out, "low.jpg") {
		t.Errorf("get_media response = %s, want it to still include the low-q Path", out)
	}
}

func TestGetMediaFullChatScopedForDanger(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chatA, chatB := "55500000086@c.us", "55500000087@c.us"
	if err := st.AddMedia(store.Media{MsgID: "m1", ChatJID: chatB, Path: "low.jpg", FullPath: "full.jpg", TS: 1}); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, "term-media-scope")
	if err := gate.RegisterDispatch("nonce-media-scope", chatA, LevelDanger, "term-media-scope", 0, ""); err != nil {
		t.Fatal(err)
	}
	callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-media-scope"})

	out := callTool(t, termCtx, srv, "get_media_full", map[string]any{"chat_id": chatB, "msg_id": "m1"})
	if !strings.Contains(out, "anti-leakage") {
		t.Errorf("get_media_full on a DIFFERENT chat under danger = %s, want it blocked", out)
	}
}
