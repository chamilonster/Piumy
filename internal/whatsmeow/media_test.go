package whatsmeow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	wmeow "go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waAdv"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	_ "modernc.org/sqlite"

	"piumy-gateway/internal/store"
)

// newTestWmeowClient builds a real, unconnected *wmeow.Client backed by an
// in-memory session store — enough for DownloadAny to run its real
// pre-network checks (GetMediaType/GetDirectPath) and fail deterministically
// with ErrNoURLPresent when a sub-message carries no DirectPath, without
// ever touching the network. wmeow.NewClient panics on a nil device store
// (client.go), so this is the minimum real one that avoids that.
func newTestWmeowClient(t *testing.T) *wmeow.Client {
	t.Helper()
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// Same reasoning as store.Open (schema.go): a SQLite ":memory:" DB is
	// per-connection, not per-DSN — without this, database/sql's default
	// unlimited pool can hand out a second connection that sees an empty
	// database, not the one Upgrade/GetFirstDevice just populated.
	db.SetMaxOpenConns(1)
	container := sqlstore.NewWithDB(db, "sqlite", waLog.Noop)
	if err := container.Upgrade(ctx); err != nil {
		t.Fatal(err)
	}
	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// container.NewDevice() (GetFirstDevice's path on a brand-new, empty DB
	// — always the case here) doesn't wire LIDs; only the read-an-existing-
	// device path does (container.go's scanDevice). S7b (ct-2026-07-30-0332)
	// needs a working Store.LIDs to test the GetPNForLID fallback.
	deviceStore.LIDs = container.LIDMap
	// Same gap for Contacts (ct-2026-07-31, "no llegan contactos en una
	// instalación nueva"): SetAllStores only ever runs inside
	// initializeDevice (the read-an-existing-device path), never for
	// NewDevice()'s fresh device. A never-paired device also has no ID yet
	// (container.go's NewDevice doesn't set one — only a real pairing does),
	// and NewSQLStore needs a JID to key its queries by, so a synthetic one
	// is assigned here — test-only, this device never really pairs. Save is
	// required too: whatsmeow_contact has a FOREIGN KEY on whatsmeow_device,
	// so a synthetic ID with no matching row fails every Contacts write.
	// PutDevice also dereferences device.Account (real pairing sets it via
	// the ADV signed identity exchange, never run here) — an empty, non-nil
	// stub is enough to satisfy the INSERT; its contents are never verified
	// by anything this package's tests exercise.
	testDeviceJID := types.NewJID("testdevice", types.DefaultUserServer)
	deviceStore.ID = &testDeviceJID
	deviceStore.Account = &waAdv.ADVSignedDeviceIdentity{
		Details:             []byte{},
		AccountSignatureKey: make([]byte, 32),
		AccountSignature:    make([]byte, 64),
		DeviceSignature:     make([]byte, 64),
	}
	if err := deviceStore.Save(ctx); err != nil {
		t.Fatal(err)
	}
	deviceStore.Contacts = sqlstore.NewSQLStore(container, testDeviceJID)
	return wmeow.NewClient(deviceStore, waLog.Noop)
}

func TestDetectMedia(t *testing.T) {
	cases := []struct {
		name        string
		msg         *waE2E.Message
		wantOK      bool
		wantMime    string
		wantCaption string
	}{
		{
			name:   "plain text: no media",
			msg:    &waE2E.Message{Conversation: proto.String("hola")},
			wantOK: false,
		},
		{
			name:        "image with caption",
			msg:         &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg"), Caption: proto.String("foto genial")}},
			wantOK:      true,
			wantMime:    "image/jpeg",
			wantCaption: "foto genial",
		},
		{
			name:     "video without caption",
			msg:      &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Mimetype: proto.String("video/mp4")}},
			wantOK:   true,
			wantMime: "video/mp4",
		},
		{
			name:     "audio",
			msg:      &waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: proto.String("audio/ogg; codecs=opus")}},
			wantOK:   true,
			wantMime: "audio/ogg; codecs=opus",
		},
		{
			name:        "document: filename as caption",
			msg:         &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Mimetype: proto.String("application/pdf"), FileName: proto.String("factura.pdf")}},
			wantOK:      true,
			wantMime:    "application/pdf",
			wantCaption: "factura.pdf",
		},
		{
			name:     "sticker: always image/webp",
			msg:      &waE2E.Message{StickerMessage: &waE2E.StickerMessage{}},
			wantOK:   true,
			wantMime: "image/webp",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evt := &events.Message{
				Info:    types.MessageInfo{MessageSource: types.MessageSource{Chat: types.NewJID("1", "s.whatsapp.net")}},
				Message: tc.msg,
			}
			m, ok := detectMedia(evt)
			if ok != tc.wantOK {
				t.Fatalf("detectMedia ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if m.mime != tc.wantMime {
				t.Errorf("mime = %q, want %q", m.mime, tc.wantMime)
			}
			if m.caption != tc.wantCaption {
				t.Errorf("caption = %q, want %q", m.caption, tc.wantCaption)
			}
			if m.proto == nil {
				t.Error("proto is nil, want the original *waE2E.Message")
			}
		})
	}
}

func TestExtensionFor(t *testing.T) {
	cases := [][2]string{
		{"image/jpeg", ".jpg"},
		{"image/png", ".png"},
		{"image/webp", ".webp"},
		{"video/mp4", ".mp4"},
		{"audio/ogg; codecs=opus", ".ogg"},
		{"application/pdf", ".pdf"},
		{"application/zip", ".zip"},            // generic cut
		{"totally/unknown thing here", ".bin"}, // space → fallback
	}
	for _, tc := range cases {
		if got := extensionFor(tc[0]); got != tc[1] {
			t.Errorf("extensionFor(%q) = %q, want %q", tc[0], got, tc[1])
		}
	}
}

func TestSafeMediaName(t *testing.T) {
	name := safeMediaName("3EB0/msg:id*here", ".jpg")
	if strings.ContainsAny(name, "/*:") {
		t.Errorf("safeMediaName still has unsafe chars: %q", name)
	}
	if !strings.HasSuffix(name, ".jpg") {
		t.Errorf("safeMediaName missing extension: %q", name)
	}
}

// TestCaptureMediaPendingByType covers ct-2026-07-21-1437 parte 1 for every
// concrete media sub-message: the exact directPath/mediaKey/fileSHA256/
// fileEncSHA256/fileLength whatsmeow's DownloadableMessage carries must
// round-trip through media_pending untouched.
func TestCaptureMediaPendingByType(t *testing.T) {
	cases := []struct {
		name     string
		msg      *waE2E.Message
		wantKind string
	}{
		{
			name: "image",
			msg: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
				Mimetype: proto.String("image/jpeg"), DirectPath: proto.String("/v/t1/img"),
				MediaKey: []byte("imgkey"), FileSHA256: []byte("imgsha"), FileEncSHA256: []byte("imgenc"),
				FileLength: proto.Uint64(111),
			}},
			wantKind: "photo",
		},
		{
			name: "video",
			msg: &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
				Mimetype: proto.String("video/mp4"), DirectPath: proto.String("/v/t1/vid"),
				MediaKey: []byte("vidkey"), FileSHA256: []byte("vidsha"), FileEncSHA256: []byte("videnc"),
				FileLength: proto.Uint64(222),
			}},
			wantKind: "video",
		},
		{
			name: "audio",
			msg: &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
				Mimetype: proto.String("audio/ogg; codecs=opus"), DirectPath: proto.String("/v/t1/aud"),
				MediaKey: []byte("audkey"), FileSHA256: []byte("audsha"), FileEncSHA256: []byte("audenc"),
				FileLength: proto.Uint64(333),
			}},
			wantKind: "audio",
		},
		{
			name: "document",
			msg: &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
				Mimetype: proto.String("application/pdf"), DirectPath: proto.String("/v/t1/doc"),
				MediaKey: []byte("dockey"), FileSHA256: []byte("docsha"), FileEncSHA256: []byte("docenc"),
				FileLength: proto.Uint64(444),
			}},
			wantKind: "doc",
		},
		{
			name: "sticker",
			msg: &waE2E.Message{StickerMessage: &waE2E.StickerMessage{
				DirectPath: proto.String("/v/t1/stk"),
				MediaKey:   []byte("stkkey"), FileSHA256: []byte("stksha"), FileEncSHA256: []byte("stkenc"),
				FileLength: proto.Uint64(555),
			}},
			wantKind: "sticker",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			a := &Adapter{store: st}

			evt := &events.Message{
				Info:    types.MessageInfo{MessageSource: types.MessageSource{Chat: types.NewJID("1", "s.whatsapp.net")}},
				Message: tc.msg,
			}
			m, ok := detectMedia(evt)
			if !ok {
				t.Fatal("detectMedia: want ok=true")
			}
			a.captureMediaPending("1@s.whatsapp.net", "MSG1", 1234, m)

			got, ok, err := st.GetMediaPending("1@s.whatsapp.net", "MSG1")
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				t.Fatal("GetMediaPending: want ok=true after captureMediaPending")
			}
			if got.Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q", got.Kind, tc.wantKind)
			}
			if got.Mime != m.mime {
				t.Errorf("Mime = %q, want %q", got.Mime, m.mime)
			}
			if got.DirectPath != m.ref.GetDirectPath() {
				t.Errorf("DirectPath = %q, want %q", got.DirectPath, m.ref.GetDirectPath())
			}
			if string(got.MediaKey) != string(m.ref.GetMediaKey()) {
				t.Errorf("MediaKey = %q, want %q", got.MediaKey, m.ref.GetMediaKey())
			}
			if string(got.FileSHA256) != string(m.ref.GetFileSHA256()) {
				t.Errorf("FileSHA256 = %q, want %q", got.FileSHA256, m.ref.GetFileSHA256())
			}
			if string(got.FileEncSHA256) != string(m.ref.GetFileEncSHA256()) {
				t.Errorf("FileEncSHA256 = %q, want %q", got.FileEncSHA256, m.ref.GetFileEncSHA256())
			}
			if got.FileLength != m.ref.GetFileLength() {
				t.Errorf("FileLength = %d, want %d", got.FileLength, m.ref.GetFileLength())
			}
			if got.TS != 1234 {
				t.Errorf("TS = %d, want 1234", got.TS)
			}
		})
	}
}

// TestCaptureMediaPendingNilStoreIsNoOp confirms the nil-safe convention
// (same as downloadAndStoreMedia/AddMedia) — without a store, must not panic.
func TestCaptureMediaPendingNilStoreIsNoOp(t *testing.T) {
	a := &Adapter{}
	evt := &events.Message{
		Info:    types.MessageInfo{MessageSource: types.MessageSource{Chat: types.NewJID("1", "s.whatsapp.net")}},
		Message: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg")}},
	}
	m, ok := detectMedia(evt)
	if !ok {
		t.Fatal("detectMedia: want ok=true")
	}
	a.captureMediaPending("1@s.whatsapp.net", "MSG1", 1234, m)
}

// TestDownloadAndStoreMediaFailureCapturesPending is the live-path half of
// ct-2026-07-21-1437 parte 1: when the synchronous download fails (here,
// deterministically — no DirectPath, so whatsmeow's own DownloadAny returns
// ErrNoURLPresent before ever touching the network), the raw reference must
// still land in media_pending so a later worker can retry it.
func TestDownloadAndStoreMediaFailureCapturesPending(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &Adapter{
		store:    st,
		mediaDir: t.TempDir(),
		client:   newTestWmeowClient(t),
	}

	evt := &events.Message{
		Message: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			Mimetype: proto.String("image/jpeg"), Caption: proto.String("sin url"),
			MediaKey: []byte("k"), FileSHA256: []byte("s"), FileEncSHA256: []byte("e"),
			FileLength: proto.Uint64(99),
			// DirectPath deliberately unset — forces cli.Download's own
			// ErrNoURLPresent check, no network call ever attempted.
		}},
	}
	m, ok := detectMedia(evt)
	if !ok {
		t.Fatal("detectMedia: want ok=true")
	}

	mime, caption := a.downloadAndStoreMedia(context.Background(), "MSG1", "1@s.whatsapp.net", 1000, m)
	if mime != "image/jpeg" || caption != "sin url" {
		t.Errorf("downloadAndStoreMedia = (%q, %q), want (image/jpeg, sin url) even on failure", mime, caption)
	}

	media, err := st.ListMedia("1@s.whatsapp.net", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(media) != 0 {
		t.Errorf("ListMedia = %d, want 0 — the download failed, nothing to serve yet", len(media))
	}

	pending, ok, err := st.GetMediaPending("1@s.whatsapp.net", "MSG1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("GetMediaPending: want ok=true — the failed download's reference must be captured")
	}
	if pending.Kind != "photo" || pending.FileLength != 99 {
		t.Errorf("GetMediaPending = %+v, unexpected fields", pending)
	}
}

// TestMediaPendingType covers ct-2026-07-21-1437 parte 2's kind→MediaType
// mapping — every store.MediaKind output must resolve, sticker included
// (classified as MediaImage, same as the library's own classToMediaType for
// StickerMessage), and an unknown kind must fail closed.
func TestMediaPendingType(t *testing.T) {
	cases := []struct {
		kind     string
		wantType wmeow.MediaType
		wantOK   bool
	}{
		{"photo", wmeow.MediaImage, true},
		{"sticker", wmeow.MediaImage, true},
		{"video", wmeow.MediaVideo, true},
		{"audio", wmeow.MediaAudio, true},
		{"doc", wmeow.MediaDocument, true},
		{"", "", false},
		{"bogus", "", false},
	}
	for _, tc := range cases {
		got, ok := mediaPendingType(tc.kind)
		if got != tc.wantType || ok != tc.wantOK {
			t.Errorf("mediaPendingType(%q) = (%q, %v), want (%q, %v)", tc.kind, got, ok, tc.wantType, tc.wantOK)
		}
	}
}

// TestDownloadMediaPendingUnknownKindFails confirms downloadMediaPending
// fails closed on a row with a kind that isn't one of MediaKind's own
// outputs (defensive — media_pending's kind always comes from MediaKind, so
// this should never happen in practice, but must not silently proceed).
func TestDownloadMediaPendingUnknownKindFails(t *testing.T) {
	a := &Adapter{}
	err := a.downloadMediaPending(context.Background(), store.MediaPending{ChatJID: "1@c.us", MsgID: "m1", Kind: "bogus"})
	if err == nil {
		t.Fatal("downloadMediaPending with an unknown kind: want an error")
	}
}

// TestDownloadMediaPendingDownloadFailureLeavesRowIntact is the retry half
// of ct-2026-07-21-1437 parte 2 (ponytail-documented: no backoff, the row
// simply survives for the next tick). A malformed directPath (empty, so
// DownloadMediaWithPath's own "must start with slash" check fails before
// any network call) must not touch store.Media nor delete media_pending.
func TestDownloadMediaPendingDownloadFailureLeavesRowIntact(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p := store.MediaPending{ChatJID: "1@s.whatsapp.net", MsgID: "MSG1", Mime: "image/jpeg", Kind: "photo", DirectPath: ""}
	if err := st.AddMediaPending(p); err != nil {
		t.Fatal(err)
	}
	a := &Adapter{store: st, mediaDir: t.TempDir(), client: newTestWmeowClient(t)}

	if err := a.downloadMediaPending(context.Background(), p); err == nil {
		t.Fatal("downloadMediaPending with an empty DirectPath: want an error")
	}

	if _, ok, err := st.GetMediaPending(p.ChatJID, p.MsgID); err != nil || !ok {
		t.Fatalf("media_pending row must survive a failed download: ok=%v err=%v", ok, err)
	}
	if media, err := st.ListMedia(p.ChatJID, 10); err != nil || len(media) != 0 {
		t.Errorf("ListMedia = %+v (err=%v), want 0 — nothing was actually downloaded", media, err)
	}
}

// TestClaimMediaDownloadOnlyOneWinner is the coordination mechanism itself
// (ct-2026-07-21-1437 parte 3, Citrino: "que no se pisen bajando el mismo
// ítem") — a second claim on the same key must fail while the first still
// holds it, and succeed again once released.
func TestClaimMediaDownloadOnlyOneWinner(t *testing.T) {
	a := &Adapter{}
	if !a.claimMediaDownload("1@c.us", "img1") {
		t.Fatal("first claim: want true")
	}
	if a.claimMediaDownload("1@c.us", "img1") {
		t.Error("second claim while the first still holds it: want false")
	}
	// A different key must never collide with an unrelated claim.
	if !a.claimMediaDownload("2@c.us", "img1") {
		t.Error("claim on a different chat_jid: want true, must not collide")
	}
	a.releaseMediaDownload("1@c.us", "img1")
	if !a.claimMediaDownload("1@c.us", "img1") {
		t.Error("claim after release: want true")
	}
}

// TestDownloadMediaPendingReturnsInFlightWhenAlreadyClaimed confirms
// downloadMediaPending itself respects an existing claim — the entry point
// both mediabgworker.go (parte 2) and mediaworker.go (parte 3) share.
func TestDownloadMediaPendingReturnsInFlightWhenAlreadyClaimed(t *testing.T) {
	a := &Adapter{}
	p := store.MediaPending{ChatJID: "1@c.us", MsgID: "img1", Kind: "photo"}
	if !a.claimMediaDownload(p.ChatJID, p.MsgID) {
		t.Fatal("claimMediaDownload: want true on a fresh claim")
	}
	defer a.releaseMediaDownload(p.ChatJID, p.MsgID)

	err := a.downloadMediaPending(context.Background(), p)
	if !errors.Is(err, errMediaDownloadInFlight) {
		t.Errorf("downloadMediaPending on an already-claimed row = %v, want errMediaDownloadInFlight", err)
	}
}

// TestIsPermanentMediaDownloadErrorClassifies covers the 403/410 vs
// everything-else split isPermanentMediaDownloadError makes — the switch
// applyMediaDownloadFailure hinges on to decide FailMediaPendingPermanently
// vs IncrementMediaPendingAttempts.
func TestIsPermanentMediaDownloadErrorClassifies(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"403", fmt.Errorf("download x/y: %w", wmeow.ErrMediaDownloadFailedWith403), true},
		{"410", fmt.Errorf("download x/y: %w", wmeow.ErrMediaDownloadFailedWith410), true},
		{"network blip", errors.New("connection reset"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isPermanentMediaDownloadError(c.err); got != c.want {
				t.Errorf("isPermanentMediaDownloadError(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// TestApplyMediaDownloadFailurePermanentGivesUpInOneCall is the regression
// test for ct-2026-07-29 (boss: "los audios dicen descargando... un adjunto
// que falló con 403 no puede decir 'descargando' para siempre"): a
// permanent error (403/410) must jump attempts straight to
// store.MaxMediaPendingAttempts in ONE call, not the plain +1 a transient
// failure gets — before this fix, mediabgworker.go's own log claimed
// "giving up after 1 attempt" while actually calling the generic +1,
// taking 2 more real (and doomed) retries to actually stop.
func TestApplyMediaDownloadFailurePermanentGivesUpInOneCall(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.AddMediaPending(store.MediaPending{ChatJID: "1@c.us", MsgID: "m1", Mime: "audio/ogg", Kind: "audio"}); err != nil {
		t.Fatal(err)
	}
	a := &Adapter{store: st}

	permanentErr := fmt.Errorf("download 1@c.us/m1: %w", wmeow.ErrMediaDownloadFailedWith403)
	if err := a.applyMediaDownloadFailure("1@c.us", "m1", permanentErr); err != nil {
		t.Fatal(err)
	}

	p, ok, err := st.GetMediaPending("1@c.us", "m1")
	if err != nil || !ok {
		t.Fatalf("GetMediaPending: ok=%v err=%v", ok, err)
	}
	if p.Attempts != store.MaxMediaPendingAttempts {
		t.Errorf("Attempts = %d after ONE permanent-error call, want %d (immediate give-up)", p.Attempts, store.MaxMediaPendingAttempts)
	}

	failed, err := st.MediaPendingFailedMsgIDs("1@c.us")
	if err != nil {
		t.Fatal(err)
	}
	if !failed["m1"] {
		t.Errorf("MediaPendingFailedMsgIDs = %v, want m1 marked failed", failed)
	}
}

// TestApplyMediaDownloadFailureTransientIncrementsByOne covers the sibling
// case: a generic (non-403/410) failure still just +1s, same as always —
// the permanent fast-fail must not swallow normal retries.
func TestApplyMediaDownloadFailureTransientIncrementsByOne(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.AddMediaPending(store.MediaPending{ChatJID: "1@c.us", MsgID: "m1", Mime: "audio/ogg", Kind: "audio"}); err != nil {
		t.Fatal(err)
	}
	a := &Adapter{store: st}

	if err := a.applyMediaDownloadFailure("1@c.us", "m1", errors.New("connection reset")); err != nil {
		t.Fatal(err)
	}

	p, ok, err := st.GetMediaPending("1@c.us", "m1")
	if err != nil || !ok {
		t.Fatalf("GetMediaPending: ok=%v err=%v", ok, err)
	}
	if p.Attempts != 1 {
		t.Errorf("Attempts = %d after one transient failure, want 1", p.Attempts)
	}
	if failed, err := st.MediaPendingFailedMsgIDs("1@c.us"); err != nil {
		t.Fatal(err)
	} else if failed["m1"] {
		t.Errorf("MediaPendingFailedMsgIDs = %v, want m1 NOT failed yet (only 1 of %d attempts)", failed, store.MaxMediaPendingAttempts)
	}
}
