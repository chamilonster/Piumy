package mcpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"piumy-gateway/internal/eventbus"
	"piumy-gateway/internal/gateway"
	"piumy-gateway/internal/mediautil"
	"piumy-gateway/internal/router"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
)

// fakeGateway is a minimal gateway.Gateway stub — only Connected() is
// exercised by TestSendMessageRejectsWhenGatewayDisconnected, everything
// else is an unused no-op to satisfy the interface.
type fakeGateway struct{ connected bool }

func (f *fakeGateway) Start(ctx context.Context) error { return nil }
func (f *fakeGateway) Stop()                           {}
func (f *fakeGateway) Connected() bool                 { return f.connected }
func (f *fakeGateway) Inbound() <-chan gateway.Inbound { return nil }
func (f *fakeGateway) Send(ctx context.Context, toJID, text string) (gateway.SendResult, error) {
	return gateway.SendResult{}, nil
}
func (f *fakeGateway) SendMedia(ctx context.Context, toJID string, media gateway.OutboundMedia) (gateway.SendResult, error) {
	return gateway.SendResult{}, nil
}
func (f *fakeGateway) SetTyping(ctx context.Context, toJID string, on bool) error { return nil }
func (f *fakeGateway) MarkRead(ctx context.Context, chatJID, senderJID string, msgIDs []string) error {
	return nil
}
func (f *fakeGateway) MarkDelivered(ctx context.Context, chatJID string, msgIDs []string) error {
	return nil
}
func (f *fakeGateway) QRChannel(ctx context.Context) (<-chan string, error) { return nil, nil }

// TestSendMessageDoesNotMeterAtEnqueueTime is the ST-D regression
// (ct-2026-07-11-074139): usage used to be recorded HERE, at enqueue time
// (F4-DESIGN §8's original design — "charged even for a held draft, the
// model already spent tokens producing it"). Moved to
// corepipeline.processOutbox, the one real-send choke point, so a message
// that's enqueued but never actually leaves (killed, dead-lettered) isn't
// counted as output, and a message that DOES leave is counted exactly
// once — not once here AND once again at the real send. See
// TestProcessOutboxMetersUsageOnSuccessfulSend (corepipeline) for the
// other half of this regression.
func TestSendMessageDoesNotMeterAtEnqueueTime(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000020@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")

	callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola mundo", "model": "m", "policy_version": policyVersion,
	})

	u, err := st.UsageForDay(chat, store.Today())
	if err != nil {
		t.Fatal(err)
	}
	if u.Messages != 0 {
		t.Errorf("usage right after send_message (before any real send) = %+v, want zero", u)
	}
}

// TestDraftDoesNotMeterEvenIfDiscarded covers the "held-then-discarded"
// case explicitly: a draft never reaches WhatsApp regardless of what
// happens to it, so it must never contribute usage — at creation, or ever,
// since discard_draft doesn't touch the outbox at all.
func TestDraftDoesNotMeterEvenIfDiscarded(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000025@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")

	callTool(t, termCtx, srv, "draft", map[string]any{
		"to": chat, "message": "borrador que nunca sale", "model": "m", "policy_version": policyVersion,
	})

	drafts, err := st.PendingDrafts(10)
	if err != nil || len(drafts) != 1 {
		t.Fatalf("setup: PendingDrafts = %+v, err=%v", drafts, err)
	}
	if ok, err := st.DiscardDraft(drafts[0].ID); err != nil || !ok {
		t.Fatalf("setup: DiscardDraft ok=%v err=%v", ok, err)
	}

	u, err := st.UsageForDay(chat, store.Today())
	if err != nil {
		t.Fatal(err)
	}
	if u.Messages != 0 {
		t.Errorf("usage after a discarded draft = %+v, want zero — it never reached WhatsApp", u)
	}
}

// TestSendMessageRejectsWhenGatewayDisconnected is the H6 hardening
// regression (ct-2026-07-10-0540): before this fix, send_message always
// enqueued regardless of gateway connectivity, so a deauthed/banned session
// silently piled messages into an outbox with no ETA — the agent read
// "queued for sending" as success. serverWithGate doesn't wire a Gateway
// (nil-safe, every other send_test.go case relies on that), so this test
// builds its own server with a fakeGateway instead of extending that helper
// for one caller.
func TestSendMessageRejectsWhenGatewayDisconnected(t *testing.T) {
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
	gw := &fakeGateway{connected: false}
	gate := NewGate()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := New(ctx, Deps{Store: st, State: sm, Router: rt, Gate: gate, Gateway: gw, AgentIdle: time.Minute})

	chat := "55500000048@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")

	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "gateway is disconnected") {
		t.Errorf("send_message while disconnected = %s, want refusal", out)
	}
	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("PendingOutbox after a disconnected send = %+v, want empty — never enqueued", pending)
	}

	gw.connected = true
	out2 := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out2, "queued for sending") {
		t.Errorf("send_message once reconnected = %s, want it to send normally", out2)
	}
}

// ── T122 (ct-2026-09-02-2045) — send_message with a photo ──────────────

// serverWithGateAndMediaDir is serverWithGate plus a wired MediaDir — the
// media tests below need send_message to actually be able to save a photo,
// which plain serverWithGate (MediaDir == "") deliberately can't.
func serverWithGateAndMediaDir(t *testing.T, gate *Gate) (*store.Store, *server.MCPServer, context.Context, string) {
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
	rtMgr := router.NewManager(routerPath)
	sm := state.NewManager(filepath.Join(dir, "status.json"), 8)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	mediaDir := filepath.Join(dir, "media")
	srv := New(ctx, Deps{Store: st, State: sm, Router: rtMgr, AgentIdle: time.Minute, Gate: gate, MediaDir: mediaDir})
	return st, srv, ctx, mediaDir
}

// tinyPNGDataURL is a 1x1 red PNG, base64'd into a data: URL — a real,
// decodable image, deliberately NOT a JPEG, so a passing test also proves
// EnsureJPEG's conversion actually ran.
func tinyPNGDataURL(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{200, 30, 30, 255})
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// TestSendMessageWithPhotoAutoModeEnqueuesMedia: a photo to a chat that
// sends directly (confirmation_mode=none) enqueues as media, not text —
// PendingOutbox carries the saved file's path/mime/kind, and the file
// genuinely exists (EnsureJPEG really ran, not a stub).
func TestSendMessageWithPhotoAutoModeEnqueuesMedia(t *testing.T) {
	gate := NewGate()
	st, srv, ctx, mediaDir := serverWithGateAndMediaDir(t, gate)
	chat := "555000040@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")

	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "mirá esto", "model": "m", "policy_version": policyVersion,
		"image_data_url": tinyPNGDataURL(t),
	})
	if !strings.Contains(out, "queued for sending") {
		t.Fatalf("send_message with a photo (auto mode) = %s, want it queued", out)
	}

	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("got %d pending, want 1", len(pending))
	}
	o := pending[0]
	if o.MediaKind != "photo" || o.MediaMime != "image/jpeg" || o.Text != "mirá esto" {
		t.Errorf("outbox item = %+v, want kind=photo mime=image/jpeg caption preserved", o)
	}
	if !strings.HasPrefix(o.MediaPath, mediaDir) {
		t.Errorf("MediaPath = %q, want it under %q", o.MediaPath, mediaDir)
	}
	if _, err := os.Stat(o.MediaPath); err != nil {
		t.Errorf("saved photo file missing: %v", err)
	}
}

// TestSendMessageWithPhotoConfirmModeCreatesMediaDraft: a photo to a chat
// in confirmation_mode=always holds a DRAFT that carries the media, not a
// text-only placeholder — approve_draft must be able to send it later.
func TestSendMessageWithPhotoConfirmModeCreatesMediaDraft(t *testing.T) {
	gate := NewGate()
	st, srv, ctx, _ := serverWithGateAndMediaDir(t, gate)
	chat := "555000041@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	if err := st.SetConfirmationMode(chat, "always"); err != nil {
		t.Fatal(err)
	}
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")

	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "una foto", "model": "m", "policy_version": policyVersion,
		"image_data_url": tinyPNGDataURL(t),
	})
	if !strings.Contains(out, "held for confirmation") {
		t.Fatalf("send_message with a photo (confirm mode) = %s, want it held", out)
	}

	drafts, err := st.PendingDrafts(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 {
		t.Fatalf("got %d drafts, want 1", len(drafts))
	}
	d := drafts[0]
	if d.MediaKind != "photo" || d.MediaMime != "image/jpeg" || d.MediaPath == "" {
		t.Fatalf("draft = %+v, want media fields set — a photo draft that doesn't carry the image forces blind approval", d)
	}
	if _, err := os.Stat(d.MediaPath); err != nil {
		t.Errorf("saved photo file missing: %v", err)
	}

	// approve_draft must be able to send it: ApproveDraft enqueues via the
	// media path, carrying the same path/mime/kind forward.
	_, _, _, ok, err := st.ApproveDraft(d.ID, "", 2)
	if err != nil || !ok {
		t.Fatalf("ApproveDraft: ok=%v err=%v", ok, err)
	}
	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].MediaKind != "photo" || pending[0].MediaPath != d.MediaPath {
		t.Errorf("outbox after approving a media draft = %+v, want the SAME media carried over", pending)
	}
}

// TestSendMessageInvalidImageDataURLReturnsLegibleError: garbage input
// fails with a readable error — no panic, nothing enqueued.
func TestSendMessageInvalidImageDataURLReturnsLegibleError(t *testing.T) {
	gate := NewGate()
	st, srv, ctx, _ := serverWithGateAndMediaDir(t, gate)
	chat := "555000042@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")

	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
		"image_data_url": "not-a-data-url",
	})
	if !strings.Contains(out, "invalid image_data_url") {
		t.Errorf("send_message with garbage image_data_url = %s, want a legible \"invalid image_data_url\" error", out)
	}
	pending, _ := st.PendingOutbox(10)
	if len(pending) != 0 {
		t.Errorf("PendingOutbox = %+v, want empty — a bad data_url must not enqueue anything", pending)
	}
}

// TestSendMessageUndecodableImageReturnsLegibleError: a syntactically valid
// data: URL whose payload isn't any supported image format — EnsureJPEG's
// own rejection surfaces as the tool error, not a panic or a half-sent item.
func TestSendMessageUndecodableImageReturnsLegibleError(t *testing.T) {
	gate := NewGate()
	st, srv, ctx, _ := serverWithGateAndMediaDir(t, gate)
	chat := "555000043@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")

	garbage := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("esto no es una imagen"))
	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
		"image_data_url": garbage,
	})
	if !strings.Contains(out, "invalid image_data_url") {
		t.Errorf("send_message with an undecodable image = %s, want a legible \"invalid image_data_url\" error", out)
	}
	pending, _ := st.PendingOutbox(10)
	if len(pending) != 0 {
		t.Errorf("PendingOutbox = %+v, want empty", pending)
	}
}

// TestSendMessageImageWithNoMediaDirRefuses: plain serverWithGate has no
// MediaDir wired (mirrors an unconfigured PIUMY_MEDIA_DIR) — an image
// must be refused with a legible reason, never silently dropped to a
// text-only send.
func TestSendMessageImageWithNoMediaDirRefuses(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "555000044@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")

	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
		"image_data_url": tinyPNGDataURL(t),
	})
	if !strings.Contains(out, "no media directory configured") {
		t.Errorf("send_message with an image and no MediaDir = %s, want a legible refusal", out)
	}
	pending, _ := st.PendingOutbox(10)
	if len(pending) != 0 {
		t.Errorf("PendingOutbox = %+v, want empty", pending)
	}
}

// ── T123 (ct-2026-09-02-2121) — send_message with a voice note ─────────

// fakeOggOpusDataURL builds a data: URL around magic-byte-correct-but-
// synthetic Ogg/Opus bytes — mediautil.IsOggOpus only sniffs magic bytes
// (never decodes), so a real encoder isn't needed to exercise it, same
// reasoning mediautil's own fakeOggOpus test fixture uses.
func fakeOggOpusDataURL() string {
	raw := "OggS" + string(make([]byte, 23)) + "OpusHead" + "resto del payload, no importa el contenido real"
	return "data:audio/ogg;base64," + base64.StdEncoding.EncodeToString([]byte(raw))
}

// TestSendMessageWithAudioAutoModeEnqueuesMedia: a voice note to a chat
// that sends directly enqueues as media, kind="audio", with the given
// audio_seconds carried through as an honest column.
func TestSendMessageWithAudioAutoModeEnqueuesMedia(t *testing.T) {
	gate := NewGate()
	st, srv, ctx, mediaDir := serverWithGateAndMediaDir(t, gate)
	chat := "555000045@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")

	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "", "model": "m", "policy_version": policyVersion,
		"audio_data_url": fakeOggOpusDataURL(), "audio_seconds": 12,
	})
	if !strings.Contains(out, "queued for sending") {
		t.Fatalf("send_message with audio (auto mode) = %s, want it queued", out)
	}

	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("got %d pending, want 1", len(pending))
	}
	o := pending[0]
	if o.MediaKind != "audio" || o.MediaMime != "audio/ogg; codecs=opus" || o.MediaSeconds != 12 {
		t.Errorf("outbox item = %+v, want kind=audio mime=audio/ogg; codecs=opus media_seconds=12", o)
	}
	if !strings.HasPrefix(o.MediaPath, mediaDir) {
		t.Errorf("MediaPath = %q, want it under %q", o.MediaPath, mediaDir)
	}
	if _, err := os.Stat(o.MediaPath); err != nil {
		t.Errorf("saved audio file missing: %v", err)
	}
}

// TestSendMessageWithAudioOmittedSecondsDefaultsToZero: audio_seconds is
// optional — omitting it must send without a duration, never a guessed one.
func TestSendMessageWithAudioOmittedSecondsDefaultsToZero(t *testing.T) {
	gate := NewGate()
	st, srv, ctx, _ := serverWithGateAndMediaDir(t, gate)
	chat := "555000046@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")

	callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "", "model": "m", "policy_version": policyVersion,
		"audio_data_url": fakeOggOpusDataURL(),
	})

	pending, _ := st.PendingOutbox(10)
	if len(pending) != 1 || pending[0].MediaSeconds != 0 {
		t.Errorf("outbox item without audio_seconds = %+v, want media_seconds=0", pending)
	}
}

// ── T128 iteration 2 (ct-2026-09-03-0133) — send_message's audio_waveform ──

// TestSendMessageWithAudioWaveformSavesSidecar: a well-formed 64-value
// audio_waveform saves alongside the audio (mediautil's sidecar, keyed by
// the audio's own content hash) — this is the whole join mechanism that
// lets SendAudio find it later without any new DB column.
func TestSendMessageWithAudioWaveformSavesSidecar(t *testing.T) {
	gate := NewGate()
	st, srv, ctx, mediaDir := serverWithGateAndMediaDir(t, gate)
	chat := "555000047@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")

	waveform := make([]any, 64)
	for i := range waveform {
		waveform[i] = i % 101
	}

	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "", "model": "m", "policy_version": policyVersion,
		"audio_data_url": fakeOggOpusDataURL(), "audio_waveform": waveform,
	})
	if !strings.Contains(out, "queued for sending") {
		t.Fatalf("send_message with audio_waveform = %s, want it queued", out)
	}

	pending, err := st.PendingOutbox(10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending outbox = %v, err=%v, want 1 item", pending, err)
	}
	audioBytes, err := os.ReadFile(pending[0].MediaPath)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := mediautil.LoadWaveformSidecar(mediaDir, audioBytes)
	if !ok {
		t.Fatal("LoadWaveformSidecar after send_message with a valid audio_waveform: want ok=true")
	}
	for i, v := range got {
		if int(v) != i%101 {
			t.Errorf("sidecar bar %d = %d, want %d", i, v, i%101)
		}
	}
}

// TestSendMessageWithMalformedAudioWaveformStillSendsPlain: a malformed
// audio_waveform (wrong length here) must never fail the send — it's
// silently discarded, no sidecar gets written, SendAudio falls back to
// computing its own later.
func TestSendMessageWithMalformedAudioWaveformStillSendsPlain(t *testing.T) {
	gate := NewGate()
	st, srv, ctx, mediaDir := serverWithGateAndMediaDir(t, gate)
	chat := "555000048@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")

	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "", "model": "m", "policy_version": policyVersion,
		"audio_data_url": fakeOggOpusDataURL(), "audio_waveform": []any{1, 2, 3}, // wrong length
	})
	if !strings.Contains(out, "queued for sending") {
		t.Fatalf("send_message with a malformed audio_waveform = %s, want it queued anyway", out)
	}

	pending, err := st.PendingOutbox(10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending outbox = %v, err=%v, want 1 item", pending, err)
	}
	audioBytes, err := os.ReadFile(pending[0].MediaPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := mediautil.LoadWaveformSidecar(mediaDir, audioBytes); ok {
		t.Error("LoadWaveformSidecar after send_message with a malformed audio_waveform: want ok=false (no sidecar saved)")
	}
}

// TestSendMessageWithAudioConfirmModeCreatesMediaDraft: a voice note to a
// chat in confirmation_mode=always holds a draft carrying the media and
// its duration — approve_draft must be able to send it later.
func TestSendMessageWithAudioConfirmModeCreatesMediaDraft(t *testing.T) {
	gate := NewGate()
	st, srv, ctx, _ := serverWithGateAndMediaDir(t, gate)
	chat := "555000047@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	if err := st.SetConfirmationMode(chat, "always"); err != nil {
		t.Fatal(err)
	}
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")

	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "", "model": "m", "policy_version": policyVersion,
		"audio_data_url": fakeOggOpusDataURL(), "audio_seconds": 7,
	})
	if !strings.Contains(out, "held for confirmation") {
		t.Fatalf("send_message with audio (confirm mode) = %s, want it held", out)
	}

	drafts, err := st.PendingDrafts(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 {
		t.Fatalf("got %d drafts, want 1", len(drafts))
	}
	d := drafts[0]
	if d.MediaKind != "audio" || d.MediaMime != "audio/ogg; codecs=opus" || d.MediaSeconds != 7 || d.MediaPath == "" {
		t.Fatalf("draft = %+v, want an audio draft with media_seconds=7", d)
	}

	if _, _, _, ok, err := st.ApproveDraft(d.ID, "", 2); err != nil || !ok {
		t.Fatalf("ApproveDraft: ok=%v err=%v", ok, err)
	}
	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].MediaKind != "audio" || pending[0].MediaSeconds != 7 {
		t.Errorf("outbox after approving an audio draft = %+v, want the SAME media+seconds carried over", pending)
	}
}

// TestSendMessageNonOpusAudioReturnsLegibleError: the contract's own
// explicit trap — a WAV (talk(save_to=...) output) must be rejected, never
// sent as-is.
func TestSendMessageNonOpusAudioReturnsLegibleError(t *testing.T) {
	gate := NewGate()
	st, srv, ctx, _ := serverWithGateAndMediaDir(t, gate)
	chat := "555000048@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")

	wav := "data:audio/wav;base64," + base64.StdEncoding.EncodeToString([]byte("RIFF"+string(make([]byte, 4))+"WAVEfmt "))
	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "", "model": "m", "policy_version": policyVersion,
		"audio_data_url": wav,
	})
	if !strings.Contains(out, "invalid audio_data_url") || !strings.Contains(out, "OGG/Opus") {
		t.Errorf("send_message with a WAV audio = %s, want a legible OGG/Opus refusal", out)
	}
	pending, _ := st.PendingOutbox(10)
	if len(pending) != 0 {
		t.Errorf("PendingOutbox = %+v, want empty — a bad format must not enqueue anything", pending)
	}
}

// TestSendMessageBothImageAndAudioReturnsError: the two media params are
// mutually exclusive — ambiguous which one to send.
func TestSendMessageBothImageAndAudioReturnsError(t *testing.T) {
	gate := NewGate()
	st, srv, ctx, _ := serverWithGateAndMediaDir(t, gate)
	chat := "555000049@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")

	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
		"image_data_url": tinyPNGDataURL(t), "audio_data_url": fakeOggOpusDataURL(),
	})
	if !strings.Contains(out, "at most one of image_data_url or audio_data_url") {
		t.Errorf("send_message with both image and audio = %s, want the mutual-exclusion refusal", out)
	}
	pending, _ := st.PendingOutbox(10)
	if len(pending) != 0 {
		t.Errorf("PendingOutbox = %+v, want empty", pending)
	}
}

// TestConfirmationModeAlwaysHoldsDraft covers F4-DESIGN §4: "always" never
// sends directly — send_message creates a draft instead, fail-safe by code.
func TestConfirmationModeAlwaysHoldsDraft(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000021@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	if err := st.SetConfirmationMode(chat, "always"); err != nil {
		t.Fatal(err)
	}
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")

	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "held for confirmation") {
		t.Errorf("send_message under confirmation_mode=always = %s, want it held", out)
	}

	drafts, err := st.PendingDrafts(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 || drafts[0].ChatJID != chat || drafts[0].Text != "hola" {
		t.Errorf("PendingDrafts = %+v, want one draft for %s with text %q", drafts, chat, "hola")
	}

	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("PendingOutbox after a held message = %+v, want empty (never enqueued)", pending)
	}
}

// TestConfirmationModeNoneSendsDirectly is the control: default/"none"
// still sends as before.
func TestConfirmationModeNoneSendsDirectly(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000022@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")

	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "queued for sending") {
		t.Errorf("send_message under confirmation_mode=none = %s, want it sent", out)
	}
}

// TestFreshGroupDefaultHoldsForConfirmation is the regression test for the
// HIGH finding in the F4c audit: a group chat TouchChat has never had its
// confirmation_mode explicitly set — its BY-TYPE DEFAULT (the actual,
// common case for a group the agent has never interacted with before)
// must hold for confirmation, not send directly. Before the fix, TouchChat
// wrote the legacy "required" value, which send_message's `== "always"`
// check never matched, so the fail-safe silently never fired for this
// exact, everyday case.
func TestFreshGroupDefaultHoldsForConfirmation(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	group := "111222333-444555@g.us"
	if err := st.TouchChat(group, "Grupo", 1); err != nil {
		t.Fatal(err)
	}
	// The group starts "ignored" (0800) — whitelist it and give it rules so
	// the OTHER 6 checks pass, isolating this test to the confirmation_mode
	// behavior specifically, not a different gate.
	if err := st.SetStatus(group, "whitelist"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatRules(group, "responder normalmente"); err != nil {
		t.Fatal(err)
	}
	termCtx := bossDispatchContext(t, gate, srv, ctx, group)
	_, policyVersion := decisionPolicy("")

	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": group, "message": "hola grupo", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "held for confirmation") {
		t.Errorf("send_message to a fresh, never-configured group = %s, want it held for confirmation (default confirmation_mode), not sent directly", out)
	}

	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("PendingOutbox after a fresh-group send = %+v, want empty — it must never have sent directly", pending)
	}
}

// TestSendMessageToAnotherChatAlsoClosesDispatchChat is T33's own
// regression (ct-2026-08-06-1526) — reproduces exactly what the boss hit
// live: a boss dispatch on chat A ("escribile a mi amiga"), send_message
// replies to chat B (the third number). Before this fix, only B got marked
// handled — A's own dispatched message stayed pending and the sweep
// re-dispatched it once the terminal freed up, sending the boss's message
// to the agent a second time.
func TestSendMessageToAnotherChatAlsoClosesDispatchChat(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	dispatchChat := "55500000095@c.us" // the boss's own chat — the dispatch
	otherChat := "55500000096@c.us"    // the third number he asked to write to
	for _, jid := range []string{dispatchChat, otherChat} {
		if err := st.TouchChat(jid, "C", 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetMode(dispatchChat, "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetActive(dispatchChat, true); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: dispatchChat, ID: "m1", Text: "escribile a mi amiga", TS: 5}); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")

	termID := "term-cross-chat"
	if err := gate.RegisterDispatch("nonce-cross-chat", dispatchChat, LevelBoss, termID, 5, ""); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, termID)
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-cross-chat"})
	token := unlockToken(t, instr)
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})
	callTool(t, termCtx, srv, "skip", map[string]any{})
	_, policyVersion := decisionPolicy("")

	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": otherChat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "queued for sending") {
		t.Fatalf("send_message = %s, want success", out)
	}

	pending, err := st.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("PendingDedicated after send_message to a different chat = %+v, want empty — the dispatch chat's own message must be marked handled too, not just the destination", pending)
	}
}

// TestSendMessageToAnotherChatDoesNotMarkDispatchMessagesAfterBurst is T33's
// own "no marques de más" requirement (listo cuando #4): a message that
// arrived in the dispatch's chat AFTER the burst that was actually
// dispatched must stay pending — markDispatchChatIfDifferent uses
// active.BurstMaxTS, never `now`, same discipline silent_act already
// established.
func TestSendMessageToAnotherChatDoesNotMarkDispatchMessagesAfterBurst(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	dispatchChat := "55500000097@c.us"
	otherChat := "55500000098@c.us"
	for _, jid := range []string{dispatchChat, otherChat} {
		if err := st.TouchChat(jid, "C", 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetMode(dispatchChat, "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetActive(dispatchChat, true); err != nil {
		t.Fatal(err)
	}
	// The dispatched burst (ts=5) plus a message that arrived AFTER the
	// dispatch was already sent to the agent (ts=100, > burstMaxTS below).
	if err := st.AddMessage(store.Message{ChatJID: dispatchChat, ID: "m1", Text: "escribile a mi amiga", TS: 5}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: dispatchChat, ID: "m2", Text: "epa, otra cosa", TS: 100}); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")

	termID := "term-cross-chat-burst"
	if err := gate.RegisterDispatch("nonce-cross-chat-burst", dispatchChat, LevelBoss, termID, 5, ""); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, termID)
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-cross-chat-burst"})
	token := unlockToken(t, instr)
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})
	callTool(t, termCtx, srv, "skip", map[string]any{})
	_, policyVersion := decisionPolicy("")

	callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": otherChat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})

	pending, err := st.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "m2" {
		t.Errorf("PendingDedicated after closing the dispatch chat = %+v, want exactly m2 (arrived after the dispatched burst, must stay pending)", pending)
	}
}

// TestSendMessageSameChatDoesNotDoubleMark: the everyday case (reply in the
// same chat the dispatch came from) must behave exactly as before —
// markDispatchChatIfDifferent's own active.ChatJID != to guard must never
// fire here.
func TestSendMessageSameChatDoesNotDoubleMark(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000099@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")

	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "queued for sending") {
		t.Errorf("send_message in the same chat as the dispatch = %s, want it sent same as always", out)
	}
}

// TestDraftToolAlwaysDrafts covers the agent's own opt-in to hold a
// message, regardless of confirmation_mode (default here is "none").
func TestDraftToolAlwaysDrafts(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000023@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")

	out := callTool(t, termCtx, srv, "draft", map[string]any{"to": chat, "message": "borrador", "model": "m", "policy_version": policyVersion})
	if !strings.Contains(out, "drafted") {
		t.Errorf("draft tool = %s, want it held as a draft", out)
	}

	drafts, err := st.PendingDrafts(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 || drafts[0].Text != "borrador" {
		t.Errorf("PendingDrafts = %+v, want one draft with text %q", drafts, "borrador")
	}
}

// TestDraftToAnotherChatAlsoClosesDispatchChat is T33's own "aplicá el
// mismo criterio a draft" requirement — the identical bug send_message had,
// reproduced against draft: a boss dispatch on chat A, draft written for
// chat B, A's own dispatched message must still end up marked handled.
func TestDraftToAnotherChatAlsoClosesDispatchChat(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	dispatchChat := "55500000100@c.us"
	otherChat := "55500000101@c.us"
	for _, jid := range []string{dispatchChat, otherChat} {
		if err := st.TouchChat(jid, "C", 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetMode(dispatchChat, "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetActive(dispatchChat, true); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: dispatchChat, ID: "m1", Text: "dejale un borrador a mi amiga", TS: 5}); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")

	termID := "term-draft-cross-chat"
	if err := gate.RegisterDispatch("nonce-draft-cross-chat", dispatchChat, LevelBoss, termID, 5, ""); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, termID)
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-draft-cross-chat"})
	token := unlockToken(t, instr)
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})
	callTool(t, termCtx, srv, "skip", map[string]any{})
	_, policyVersion := decisionPolicy("")

	out := callTool(t, termCtx, srv, "draft", map[string]any{
		"to": otherChat, "message": "borrador", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "drafted") {
		t.Fatalf("draft = %s, want it held as a draft", out)
	}

	pending, err := st.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("PendingDedicated after draft to a different chat = %+v, want empty — the dispatch chat's own message must be marked handled too", pending)
	}
}

// TestDraftToolPublishesDraftEvent is T16 (ct-2026-08-05-123257): the
// dashboard's SSE auto-refresh needs a nudge the moment a draft is
// created, not just a status change nobody signals — "un borrador que
// aparece y no se ve hasta recargar es una respuesta que salió tarde"
// (Citrino).
func TestDraftToolPublishesDraftEvent(t *testing.T) {
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

	chat := "55500000102@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")

	callTool(t, termCtx, srv, "draft", map[string]any{"to": chat, "message": "borrador", "model": "m", "policy_version": policyVersion})

	select {
	case e := <-ch:
		if e.Type != "draft" {
			t.Errorf("event = %+v, want type=draft", e)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the draft eventbus nudge")
	}
}

// TestSendMessageThenStickerSameTurn is the boss's own literal case (T167,
// ct-2026-09-17-1255, verbatim: "por ejemplo si quiero que la ia envie un
// stiker despues de un mensaje") — a text reply followed by an image, in the
// SAME turn, no new get_instructions in between. The gateway's own
// send_message only carries photos, not WhatsApp's distinct sticker format
// (out of this contract's scope — the boss's complaint was the GATE
// rejecting the second call, not a missing media type), so image_data_url
// stands in for "a second, different piece of the same reply".
func TestSendMessageThenStickerSameTurn(t *testing.T) {
	gate := NewGate()
	st, srv, ctx, _ := serverWithGateAndMediaDir(t, gate)
	chat := "55500000105@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := withTerminalID(ctx, "term-sticker")
	if err := gate.RegisterDispatch("nonce-sticker", chat, LevelCaution, "term-sticker", 0, ""); err != nil {
		t.Fatal(err)
	}
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-sticker"})
	token := unlockToken(t, instr)
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})
	callTool(t, termCtx, srv, "skip", map[string]any{})
	_, policyVersion := decisionPolicy("")

	text := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "ya te mando algo mas", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(text, "queued for sending") {
		t.Fatalf("first piece (text) = %s, want it to pass", text)
	}

	sticker := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "", "model": "m", "policy_version": policyVersion,
		"image_data_url": tinyPNGDataURL(t),
	})
	if !strings.Contains(sticker, "queued for sending") {
		t.Errorf("second piece (image, no new get_instructions) = %s, want it to pass too — this is exactly what T167 exists for", sticker)
	}
}

// TestDraftAndSendMessageShareTheSameFourSendCap is T167 (ct-2026-09-17-1255,
// boss verbatim: "quiero que lo quiten o aumenten a 4 mensajes"): draft
// consumes the dispatch same as send_message (InFlight goes false on the
// very first call, for a caution/danger dispatch same as boss, which is
// "sin gate" end to end already) — but unlike before this contract, that
// consumption isn't the end of the dispatch's own chat: send_message and
// draft, in any mix, share ONE budget of maxSendsPerDispatch total calls to
// it. Calls 2-4 here alternate send_message/draft on purpose, to prove the
// counter is genuinely shared, not two separate ones; the 5th (whichever
// tool) is refused.
func TestDraftAndSendMessageShareTheSameFourSendCap(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000103@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := withTerminalID(ctx, "term-draft-cap")
	if err := gate.RegisterDispatch("nonce-draft", chat, LevelCaution, "term-draft-cap", 0, ""); err != nil {
		t.Fatal(err)
	}
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-draft"})
	token := unlockToken(t, instr)
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})
	callTool(t, termCtx, srv, "skip", map[string]any{})
	_, policyVersion := decisionPolicy("")

	out := callTool(t, termCtx, srv, "draft", map[string]any{"to": chat, "message": "borrador", "model": "m", "policy_version": policyVersion})
	if !strings.Contains(out, "drafted") {
		t.Fatalf("draft = %s, want it held", out)
	}
	if gate.InFlight("term-draft-cap") {
		t.Fatal("InFlight after the first draft: want false — Consume still retires on the very first call, unchanged")
	}

	calls := []struct {
		tool string
		want string
	}{
		{"send_message", "queued for sending"},
		{"draft", "drafted"},
		{"send_message", "queued for sending"},
	}
	for i, c := range calls {
		out := callTool(t, termCtx, srv, c.tool, map[string]any{"to": chat, "message": "otra vez", "model": "m", "policy_version": policyVersion})
		if !strings.Contains(out, c.want) {
			t.Fatalf("%s #%d (call %d/%d overall) = %s, want %q — within the shared T167 cap", c.tool, i+2, i+2, maxSendsPerDispatch, out, c.want)
		}
	}

	// The 5th call, either tool, is past the shared budget.
	over := callTool(t, termCtx, srv, "draft", map[string]any{"to": chat, "message": "una mas", "model": "m", "policy_version": policyVersion})
	if !strings.Contains(over, "locked:") {
		t.Errorf("draft past the shared T167 cap = %s, want it locked", over)
	}
}

// TestSilentActReleasesTerminalImmediately is S11's core regression
// (ct-2026-07-30-1619, the boss's own "falta un silent act"): before this,
// gate.Consume was only ever called from send.go — deciding NOT to reply
// left the dispatch InFlight until dispatchStaleAfter (15min) reclaimed it,
// a mechanical reward for talking over staying silent. silent_act must
// release the terminal exactly as fast as send_message does.
func TestSilentActReleasesTerminalImmediately(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000104@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, "term-silent")
	if err := gate.RegisterDispatch("nonce-silent", chat, LevelCaution, "term-silent", 0, ""); err != nil {
		t.Fatal(err)
	}
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-silent"})
	token := unlockToken(t, instr)
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})
	callTool(t, termCtx, srv, "skip", map[string]any{})

	if !gate.InFlight("term-silent") {
		t.Fatal("setup: InFlight before silent_act = false, want true")
	}
	out := callTool(t, termCtx, srv, "silent_act", map[string]any{"reason": "no me corresponde"})
	if !strings.Contains(out, "silence recorded") {
		t.Fatalf("silent_act = %s, want it to succeed", out)
	}
	if gate.InFlight("term-silent") {
		t.Error("InFlight after silent_act = true, want false — the terminal must release immediately, not wait dispatchStaleAfter")
	}

	replay := callTool(t, termCtx, srv, "silent_act", map[string]any{})
	if !strings.Contains(replay, "locked:") {
		t.Errorf("silent_act replay after consume = %s, want it locked again (one-shot, same as send_message)", replay)
	}
}

// TestSilentActMarksBurstHandled covers criterio de listo #2: the
// dispatched burst must not be re-dispatched after a silent_act, same as it
// wouldn't be after send_message — otherwise "decidí no responder" would
// just get the same messages pushed back at the agent on the next sweep.
func TestSilentActMarksBurstHandled(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000105@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMode(chat, "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetActive(chat, true); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "m1", Text: "hola", TS: 5}); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, "term-silent-mark")
	if err := gate.RegisterDispatch("nonce-silent-mark", chat, LevelCaution, "term-silent-mark", 5, ""); err != nil {
		t.Fatal(err)
	}
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-silent-mark"})
	token := unlockToken(t, instr)
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})
	callTool(t, termCtx, srv, "skip", map[string]any{})

	callTool(t, termCtx, srv, "silent_act", map[string]any{})

	pending, err := st.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("PendingDedicated after silent_act = %d, want 0 (burst marked handled, not re-dispatched)", len(pending))
	}
}

// TestSilentActRecordsReason covers criterio de listo #3: the reason must
// be recoverable afterward (via get_chat/GetChat) — otherwise silence stays
// indistinguishable from a stuck agent, the exact gap S11 exists to close.
func TestSilentActRecordsReason(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000106@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, "term-silent-reason")
	if err := gate.RegisterDispatch("nonce-silent-reason", chat, LevelCaution, "term-silent-reason", 0, ""); err != nil {
		t.Fatal(err)
	}
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-silent-reason"})
	token := unlockToken(t, instr)
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})
	callTool(t, termCtx, srv, "skip", map[string]any{})

	callTool(t, termCtx, srv, "silent_act", map[string]any{"reason": "ya tuve la última palabra"})

	c, ok, err := st.GetChat(chat)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("chat vanished")
	}
	if c.SilenceReason != "ya tuve la última palabra" {
		t.Errorf("SilenceReason = %q, want %q", c.SilenceReason, "ya tuve la última palabra")
	}
	if c.SilenceAt == 0 {
		t.Error("SilenceAt = 0, want a real timestamp")
	}
}

// TestSilentActRequiresBoundReadyDispatch: same gate as send_message — no
// active dispatch, or one still locked/noting, must refuse. "No debilites
// el gate" (the contract's own caution) — silence must not become a way to
// skip the unlock/remember checkpoint.
func TestSilentActRequiresBoundReadyDispatch(t *testing.T) {
	gate := NewGate()
	gate.startedAt = time.Now().Add(-2 * dispatchStaleAfter) // T87: hard-reject path, not the young-gate one
	_, srv, ctx := serverWithGate(t, gate)
	termCtx := withTerminalID(ctx, "term-silent-locked")

	out := callTool(t, termCtx, srv, "silent_act", map[string]any{})
	if !strings.Contains(out, "locked:") {
		t.Errorf("silent_act with no active dispatch = %s, want locked", out)
	}

	chat := "55500000107@c.us"
	if err := gate.RegisterDispatch("nonce-silent-locked", chat, LevelCaution, "term-silent-locked", 0, ""); err != nil {
		t.Fatal(err)
	}
	callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-silent-locked"})
	// unlock deliberately not called — still locked, not ready.
	out2 := callTool(t, termCtx, srv, "silent_act", map[string]any{})
	if !strings.Contains(out2, "locked:") {
		t.Errorf("silent_act before unlock/skip = %s, want locked", out2)
	}
}

// TestDraftRespectsNoRulesLaw: draft shares validateSend's guardrails, not
// just send_message's — no rules, no draft either.
func TestDraftRespectsNoRulesLaw(t *testing.T) {
	gate := NewGate()
	_, srv, ctx := serverWithGate(t, gate)
	chat := "55500000024@c.us"
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")

	out := callTool(t, termCtx, srv, "draft", map[string]any{"to": chat, "message": "borrador", "model": "m", "policy_version": policyVersion})
	if !strings.Contains(out, "no rules on this chat") {
		t.Errorf("draft on a chat with no rules = %s, want it blocked", out)
	}
}

// TestDraftRequiresCurrentPolicyVersion is the regression test for the
// Medium finding in the F4c audit: draft's own description claims "same
// guardrails as send_message", but it never actually required
// policy_version. Now both share validateSend's check.
func TestDraftRequiresCurrentPolicyVersion(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000108@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)

	t.Run("missing policy_version rejected", func(t *testing.T) {
		out := callTool(t, termCtx, srv, "draft", map[string]any{"to": chat, "message": "borrador", "model": "m"})
		if !strings.Contains(out, "policy_version") {
			t.Errorf("draft with no policy_version = %s, want a policy_version error", out)
		}
	})
	t.Run("stale policy_version rejected", func(t *testing.T) {
		out := callTool(t, termCtx, srv, "draft", map[string]any{"to": chat, "message": "borrador", "model": "m", "policy_version": "stale"})
		if !strings.Contains(out, "stale/missing policy_version") {
			t.Errorf("draft with a stale policy_version = %s, want it rejected", out)
		}
	})
}

// TestSendMessageToNeverTouchedNumberCreatesChatAndSends is T64's change #2
// (ct-2026-08-11-1627, boss verbatim: "hazte cargo de estos 3 numertos, la
// ia se registra por mcp y atiende a esos numeros") end to end: a JID with
// NO store row at all (never TouchChat'd — nothing has ever spoken to it,
// inbound or outbound), sent to by a NON-principal agent with NO dispatch
// bound (change #1), succeeds — and the chat row now exists, governed by
// the same rules/claim/status laws as any other chat from then on.
func TestSendMessageToNeverTouchedNumberCreatesChatAndSends(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000110@c.us"
	seedAnyChatRules(t, st, "responder normalmente")
	if _, ok, err := st.GetChat(chat); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("test setup: chat should not exist yet")
	}

	termCtx := withTerminalID(ctx, "term-never-touched")
	_, policyVersion := decisionPolicy("")
	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola, nunca hablamos", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "queued for sending") {
		t.Fatalf("send_message to a never-touched number = %s, want queued for sending", out)
	}

	c, ok, err := st.GetChat(chat)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("chat row was not created by send_message")
	}
	if c.Status == "ignored" || c.Status == "blacklist" {
		t.Errorf("newly created chat status = %q, want a live default (not pre-silenced)", c.Status)
	}
}

// ── T108 (ct-2026-09-01-1413) — per-speaker closing in a group ─────────────

// dedicateGroup readies a group chat for PendingDedicated the same way
// capipush_test.go's own dedicate()+SetStatus("new") does — TouchChat
// defaults a fresh group to status "ignored", which send_message's
// ChatIsOff check would otherwise refuse.
func dedicateGroup(t *testing.T, st *store.Store, jid string) {
	t.Helper()
	if err := st.SetStatus(jid, "new"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMode(jid, "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetActive(jid, true); err != nil {
		t.Fatal(err)
	}
}

// TestSendMessageMarksOnlySpeakerHandledInGroup is T108's own end-to-end
// regression: answering Alice in a group must not mark Bob's separate,
// unrelated question as handled — the exact defect the contract exists to
// fix ("el cliente que preguntó algo queda sin respuesta y el sistema cree
// que fue atendido").
func TestSendMessageMarksOnlySpeakerHandledInGroup(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	group := "555001@g.us"
	alice := "555000000001@s.whatsapp.net"
	bob := "555000000002@s.whatsapp.net"
	if err := st.TouchChat(group, "Grupo", 1); err != nil {
		t.Fatal(err)
	}
	dedicateGroup(t, st, group)
	// A group is born confirmation_mode="always" (chat.go's TouchChat) —
	// this test exercises the DIRECT-send branch specifically (the
	// confirmation_mode="always" branch has its own test below).
	if err := st.SetConfirmationMode(group, "none"); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	if err := st.AddMessage(store.Message{ChatJID: group, ID: "m-alice", Sender: alice, Text: "pregunta de alice", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: group, ID: "m-bob", Sender: bob, Text: "pregunta de bob", TS: 2}); err != nil {
		t.Fatal(err)
	}

	termCtx := withTerminalID(ctx, "term-group-alice")
	if err := gate.RegisterDispatch("nonce-group-alice", group, LevelCaution, "term-group-alice", 1, alice); err != nil {
		t.Fatal(err)
	}
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-group-alice"})
	token := unlockToken(t, instr)
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})
	callTool(t, termCtx, srv, "skip", map[string]any{})

	_, policyVersion := decisionPolicy("")
	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": group, "message": "hola alice", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "queued for sending") {
		t.Fatalf("send_message = %s, want queued for sending", out)
	}

	pending, err := st.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "m-bob" {
		t.Fatalf("PendingDedicated = %+v, want only Bob's m-bob still pending", pending)
	}
}

// TestSendMessageAlwaysConfirmModeMarksOnlySpeakerHandledInGroup covers the
// OTHER branch inside send_message — confirmation_mode="always" creates a
// draft instead of sending — with the same per-speaker scoping.
func TestSendMessageAlwaysConfirmModeMarksOnlySpeakerHandledInGroup(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	group := "555001@g.us" // TouchChat defaults a group to confirmation_mode="always"
	alice := "555000000001@s.whatsapp.net"
	bob := "555000000002@s.whatsapp.net"
	if err := st.TouchChat(group, "Grupo", 1); err != nil {
		t.Fatal(err)
	}
	dedicateGroup(t, st, group)
	seedAnyChatRules(t, st, "responder normalmente")
	if err := st.AddMessage(store.Message{ChatJID: group, ID: "m-alice", Sender: alice, Text: "pregunta de alice", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: group, ID: "m-bob", Sender: bob, Text: "pregunta de bob", TS: 2}); err != nil {
		t.Fatal(err)
	}

	termCtx := withTerminalID(ctx, "term-group-confirm")
	if err := gate.RegisterDispatch("nonce-group-confirm", group, LevelCaution, "term-group-confirm", 1, alice); err != nil {
		t.Fatal(err)
	}
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-group-confirm"})
	token := unlockToken(t, instr)
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})
	callTool(t, termCtx, srv, "skip", map[string]any{})

	_, policyVersion := decisionPolicy("")
	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": group, "message": "para alice", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "held for confirmation") {
		t.Fatalf("send_message (confirmation_mode=always) = %s, want held for confirmation", out)
	}

	pending, err := st.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "m-bob" {
		t.Fatalf("PendingDedicated = %+v, want only Bob's m-bob still pending", pending)
	}

	drafts, err := st.PendingDrafts(10)
	if err != nil || len(drafts) != 1 {
		t.Fatalf("PendingDrafts = %+v, err=%v", drafts, err)
	}
	if drafts[0].Sender != alice {
		t.Errorf("draft Sender = %q, want %q — approve_draft needs it later", drafts[0].Sender, alice)
	}
}

// TestDraftMarksOnlySpeakerHandledInGroup: same defect, via the draft tool.
func TestDraftMarksOnlySpeakerHandledInGroup(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	group := "555001@g.us"
	alice := "555000000001@s.whatsapp.net"
	bob := "555000000002@s.whatsapp.net"
	if err := st.TouchChat(group, "Grupo", 1); err != nil {
		t.Fatal(err)
	}
	dedicateGroup(t, st, group)
	seedAnyChatRules(t, st, "responder normalmente")
	if err := st.AddMessage(store.Message{ChatJID: group, ID: "m-alice", Sender: alice, Text: "pregunta de alice", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: group, ID: "m-bob", Sender: bob, Text: "pregunta de bob", TS: 2}); err != nil {
		t.Fatal(err)
	}

	termCtx := withTerminalID(ctx, "term-group-draft")
	if err := gate.RegisterDispatch("nonce-group-draft", group, LevelCaution, "term-group-draft", 1, alice); err != nil {
		t.Fatal(err)
	}
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-group-draft"})
	token := unlockToken(t, instr)
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})
	callTool(t, termCtx, srv, "skip", map[string]any{})

	_, policyVersion := decisionPolicy("")
	out := callTool(t, termCtx, srv, "draft", map[string]any{
		"to": group, "message": "borrador para alice", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "drafted") {
		t.Fatalf("draft = %s, want drafted", out)
	}

	pending, err := st.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "m-bob" {
		t.Fatalf("PendingDedicated = %+v, want only Bob's m-bob still pending", pending)
	}
}

// TestSilentActMarksOnlySpeakerHandledInGroup: staying silent on Alice's
// message must not silently close Bob's separate, unrelated one.
func TestSilentActMarksOnlySpeakerHandledInGroup(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	group := "555001@g.us"
	alice := "555000000001@s.whatsapp.net"
	bob := "555000000002@s.whatsapp.net"
	if err := st.TouchChat(group, "Grupo", 1); err != nil {
		t.Fatal(err)
	}
	dedicateGroup(t, st, group)
	if err := st.AddMessage(store.Message{ChatJID: group, ID: "m-alice", Sender: alice, Text: "spam de alice", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: group, ID: "m-bob", Sender: bob, Text: "pregunta de bob", TS: 2}); err != nil {
		t.Fatal(err)
	}

	termCtx := withTerminalID(ctx, "term-group-silent")
	if err := gate.RegisterDispatch("nonce-group-silent", group, LevelCaution, "term-group-silent", 1, alice); err != nil {
		t.Fatal(err)
	}
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-group-silent"})
	token := unlockToken(t, instr)
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})
	callTool(t, termCtx, srv, "skip", map[string]any{})

	callTool(t, termCtx, srv, "silent_act", map[string]any{"reason": "spam"})

	pending, err := st.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "m-bob" {
		t.Fatalf("PendingDedicated after silent_act = %+v, want only Bob's m-bob still pending", pending)
	}
}

// TestSendMessageToAnotherChatClosesDispatchGroupSpeakerOnly is T33's own
// "answer a different chat" case (markDispatchChatIfDifferent), now inside
// a group: closing the ACTIVE dispatch's own chat must still scope to its
// speaker, never the whole group — the T33 fix and the T108 fix must
// compose, not fight each other.
func TestSendMessageToAnotherChatClosesDispatchGroupSpeakerOnly(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	group := "555001@g.us"
	alice := "555000000001@s.whatsapp.net"
	bob := "555000000002@s.whatsapp.net"
	other := "55500000199@c.us"
	if err := st.TouchChat(group, "Grupo", 1); err != nil {
		t.Fatal(err)
	}
	dedicateGroup(t, st, group)
	if err := st.TouchChat(other, "Otro", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	if err := st.AddMessage(store.Message{ChatJID: group, ID: "m-alice", Sender: alice, Text: "pregunta de alice", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: group, ID: "m-bob", Sender: bob, Text: "pregunta de bob", TS: 2}); err != nil {
		t.Fatal(err)
	}

	// Boss level so send_message may target `other`, a chat different from
	// the active dispatch's — same setup as bossDispatchContext, but with a
	// sender (that helper doesn't take one).
	termID := "term-group-crosschat"
	nonce := "nonce-group-crosschat"
	if err := gate.RegisterDispatch(nonce, group, LevelBoss, termID, 1, alice); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, termID)
	if out := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": nonce}); strings.Contains(out, "isError\":true") {
		t.Fatalf("get_instructions setup failed: %s", out)
	}

	_, policyVersion := decisionPolicy("")
	callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": other, "message": "hola otro chat", "model": "m", "policy_version": policyVersion,
	})

	pending, err := st.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "m-bob" {
		t.Fatalf("PendingDedicated after answering a DIFFERENT chat = %+v, want only Bob's m-bob still pending (Alice's own dispatch closed, Bob's untouched)", pending)
	}
}

// ── T147 (ct-2026-09-07) — send_message no consume ni marca lo que no abrió ──

// TestSendMessageWithoutDispatchNeverMarksPendingMessagesHandled is T147's
// own regression: the gateway was silently losing the owner's OWN messages.
// T64 lets the principal call send_message with NOTHING bound (no dispatch
// required to initiate). Before this fix, markTS for the principal was
// unconditionally time.Now() — so ANY send_message call, even one with no
// dispatch behind it at all, marked EVERY pending message in the target
// chat with ts <= now as handled, whether or not anyone had ever read it.
// Reproduced live: the boss's own new message in a group vanished with no
// error anywhere, while an unrelated reply went out and made it LOOK like
// something worked.
//
// Fixed by item 2: bound=false means nothing gets marked, full stop,
// principal included — "no abriste un despacho, no tenés derecho a
// declarar leídos mensajes que nadie leyó" (Citrino).
func TestSendMessageWithoutDispatchNeverMarksPendingMessagesHandled(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGateAndPrincipal(t, gate)
	group := "555001@g.us"
	boss := "555000000099@s.whatsapp.net"
	if err := st.TouchChat(group, "Grupo", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetConfirmationMode(group, "none"); err != nil {
		t.Fatal(err)
	}
	dedicateGroup(t, st, group)
	if err := st.TouchChat(boss, "Boss", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIsBoss(boss, true); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")

	// The boss's brand-new message — nobody has read it, and NOTHING is
	// registered in the gate for this terminal at all (bound=false).
	if err := st.AddMessage(store.Message{ChatJID: group, ID: "m-new-from-boss", Sender: boss, Text: "Trinidad tiene que contarnos que quiere hacer", TS: 2}); err != nil {
		t.Fatal(err)
	}

	// The principal calls send_message with NOTHING bound — T64's own "no
	// dispatch required to initiate" path.
	termCtx := withTerminalID(ctx, principalTerm)
	if _, bound := gate.Active(principalTerm); bound {
		t.Fatal("setup: want nothing bound to the principal's terminal")
	}
	_, policyVersion := decisionPolicy("")
	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": group, "message": "un mensaje que no responde al del boss", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "queued for sending") {
		t.Fatalf("send_message = %s, want queued for sending (T64: principal, no dispatch required)", out)
	}

	// The boss's message must still be pending — nobody opened a dispatch
	// for it, so this unrelated send_message must not mark it handled.
	pending, err := st.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range pending {
		if m.ID == "m-new-from-boss" {
			found = true
		}
	}
	if !found {
		t.Errorf("PendingDedicated = %+v, want m-new-from-boss still pending — no dispatch was ever opened for it, an unrelated send_message must not mark it handled", pending)
	}
}

// TestSendMessageLockedDistinguishesAlreadyConsumedFromNeverTouched is T147
// item 4: "locked" used to mean either "never touched" or "already
// consumed" — indistinguishable, exactly the ambiguity that let a
// wrongfully-consumed dispatch look identical to a normal one still waiting
// on get_instructions.
func TestSendMessageLockedDistinguishesAlreadyConsumedFromNeverTouched(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "555000000031@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termID := "term-locked-vs-done"
	if err := gate.RegisterDispatch("nonce-lvd", chat, LevelCaution, termID, 0, ""); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, termID)
	_, policyVersion := decisionPolicy("")

	// Never touched at all.
	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "call get_instructions") || strings.Contains(out, "already consumed") {
		t.Errorf("send_message before any ritual step = %s, want the never-touched wording", out)
	}

	// Now finish the ritual and consume it for real.
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-lvd"})
	token := unlockToken(t, instr)
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})
	callTool(t, termCtx, srv, "skip", map[string]any{})
	callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})

	// T167 (ct-2026-09-17-1255): calls 2-4 to this SAME (now-consumed)
	// dispatch's own chat are within the budget, not "already consumed" —
	// spend it here so the LAST call (the 5th overall) is the one that
	// finally hits the real "already consumed" wording this test is about.
	for i := 2; i <= maxSendsPerDispatch; i++ {
		mid := callTool(t, termCtx, srv, "send_message", map[string]any{
			"to": chat, "message": "otra vez", "model": "m", "policy_version": policyVersion,
		})
		if !strings.Contains(mid, "queued for sending") {
			t.Fatalf("send_message call #%d (within the T167 cap) = %s, want it to succeed", i, mid)
		}
	}

	// Same terminal, same (now-consumed) dispatch, budget exhausted — this
	// one must say WHY it's locked, not repeat the never-touched wording.
	out2 := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "otra vez", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out2, "already consumed") {
		t.Errorf("send_message past the T167 cap = %s, want it to say so explicitly, not the never-touched wording", out2)
	}
}

// ── T150 (ct-2026-09-07-1839) — a consumed dispatch must not block
// initiating on a DIFFERENT chat ────────────────────────────────────────
//
// Reported live by Citrino, five minutes after Citrino de temascal's
// get_status report: writing to a group where the owner was waiting got
// refused with the same "locked: already consumed", from the PRINCIPAL
// terminal. Cause: validateSend's `if !active.Ready { return locked }`
// fired unconditionally, BEFORE ever checking whether `to` was even the
// SAME chat as the stale dispatch — a terminal that ever consumed ONE
// dispatch stayed bound (gate.Consume's own doc: "it stays gated (denied)
// until a NEW get_instructions binds it"), so every later send_message,
// to ANY chat, hit the wall. T64's own principle ("iniciar sin despacho",
// flow 17) must hold even with a stale consumed dispatch sitting on the
// terminal — that dispatch's own chat is a different question, and stays
// locked (see TestSendMessageLockedDistinguishesAlreadyConsumedFromNeverTouched
// above, unchanged).

// TestSendMessageToADifferentChatSucceedsAfterConsumedDispatch is the
// boss-level shape (matches the principal's live incident: a boss-level
// dispatch, once consumed, must not block writing elsewhere).
func TestSendMessageToADifferentChatSucceedsAfterConsumedDispatch(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chatA := "555000000096@c.us"
	chatB := "555000000097@c.us"
	for _, c := range []string{chatA, chatB} {
		if err := st.TouchChat(c, "C", 1); err != nil {
			t.Fatal(err)
		}
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, chatA)
	_, policyVersion := decisionPolicy("")
	first := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chatA, "message": "hola", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(first, "queued for sending") {
		t.Fatalf("setup: first send to chatA = %s, want it to pass", first)
	}

	// chatA's dispatch is now consumed. Writing to chatB — a completely
	// different chat, nothing to do with the stale dispatch — must succeed.
	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chatB, "message": "avisando que tu agente esta caido", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "queued for sending") {
		t.Errorf("send_message to a DIFFERENT chat, with a consumed dispatch bound elsewhere = %s, want success (T64: initiating needs no dispatch; a stale one on another chat must not take that away)", out)
	}
}

// TestSendMessageToADifferentChatSucceedsAfterConsumedDispatchNonBoss is the
// same check for a caution-level dispatch — the bug wasn't boss-specific,
// the Ready check ran unconditionally for every level.
func TestSendMessageToADifferentChatSucceedsAfterConsumedDispatchNonBoss(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chatA := "555000000098@c.us"
	chatB := "555000000099@c.us"
	for _, c := range []string{chatA, chatB} {
		if err := st.TouchChat(c, "C", 1); err != nil {
			t.Fatal(err)
		}
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termID := "term-caution-diff-chat"
	if err := gate.RegisterDispatch("nonce-caution-diff-chat", chatA, LevelCaution, termID, 0, ""); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, termID)
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-caution-diff-chat"})
	token := unlockToken(t, instr)
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})
	callTool(t, termCtx, srv, "skip", map[string]any{})
	_, policyVersion := decisionPolicy("")
	callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chatA, "message": "hola", "model": "m", "policy_version": policyVersion,
	})

	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chatB, "message": "iniciando sin despacho para este chat", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "queued for sending") {
		t.Errorf("send_message to a DIFFERENT chat from a caution terminal with a consumed dispatch elsewhere = %s, want success", out)
	}
}

// TestPrincipalSendMessageToADifferentChatNotBlockedByConsumedDispatch
// reproduces Citrino's exact live report, verbatim: "Soy el principal" —
// the principal terminal isn't special-cased anywhere in validateSend, so
// it hit the same wall as anyone else.
func TestPrincipalSendMessageToADifferentChatNotBlockedByConsumedDispatch(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGateAndPrincipal(t, gate)
	chatA := "555000000100@c.us"
	chatB := "555000000101@c.us"
	for _, c := range []string{chatA, chatB} {
		if err := st.TouchChat(c, "C", 1); err != nil {
			t.Fatal(err)
		}
	}
	seedAnyChatRules(t, st, "responder normalmente")
	if err := gate.RegisterDispatch("nonce-principal-diff-chat", chatA, LevelBoss, principalTerm, 0, ""); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, principalTerm)
	callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-principal-diff-chat"})
	_, policyVersion := decisionPolicy("")
	callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chatA, "message": "hola", "model": "m", "policy_version": policyVersion,
	})

	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chatB, "message": "avisando en el grupo donde el dueno esperaba", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "queued for sending") {
		t.Errorf("principal send_message to a DIFFERENT chat, with a consumed dispatch bound elsewhere = %s, want success", out)
	}
}
