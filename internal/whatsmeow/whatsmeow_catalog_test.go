package whatsmeow

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"log"
	"strings"
	"testing"

	wmeow "go.mau.fi/whatsmeow"
)

// encodeTestJPEG builds a real JPEG of the given dimensions — used to give
// wrapSetPhotoError's dimension-logging something real to decode.
func encodeTestJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{100, 150, 200, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// ── T111 (ct-2026-09-01-1442) — checkPhotoSize ──────────────────────────────

// TestCheckPhotoSizeRejectsOverFrameMaxSize: the one ceiling this codebase
// can verify with certainty (whatsmeow's own socket frame limit) — the IQ
// carrying a profile/group photo embeds the raw bytes directly in the
// stanza (unlike SendImage, which uploads to a CDN first), so it's bound by
// this before a single byte reaches the network.
func TestCheckPhotoSizeRejectsOverFrameMaxSize(t *testing.T) {
	big := make([]byte, profilePhotoMaxBytes+1)
	err := checkPhotoSize(big)
	if err == nil {
		t.Fatal("checkPhotoSize over the limit: want an error")
	}
	if !strings.Contains(err.Error(), "grande") {
		t.Errorf("error = %q, want it to mention the image is too large", err)
	}
}

// TestCheckPhotoSizeAllowsAtOrUnderLimit: the boundary itself, and comfortably
// under it, must both pass — off-by-one errors here would reject valid images.
func TestCheckPhotoSizeAllowsAtOrUnderLimit(t *testing.T) {
	atLimit := make([]byte, profilePhotoMaxBytes)
	if err := checkPhotoSize(atLimit); err != nil {
		t.Errorf("checkPhotoSize at exactly the limit = %v, want nil", err)
	}
	if err := checkPhotoSize([]byte("small")); err != nil {
		t.Errorf("checkPhotoSize for a tiny payload = %v, want nil", err)
	}
	// nil (the remove-photo case) must never be flagged as oversized.
	if err := checkPhotoSize(nil); err != nil {
		t.Errorf("checkPhotoSize(nil) = %v, want nil (removing the photo)", err)
	}
}

// ── T111 — wrapSetPhotoError ─────────────────────────────────────────────

// TestWrapSetPhotoErrorRewritesInvalidImageFormat is Citrino's own
// requirement: whatsmeow maps ANY server rejection (406) to
// ErrInvalidImageFormat, so telling the dueño "invalid format" would be
// misleading whenever the real cause was size/dimensions — the same shape
// as T104/T87's "an error that points in the wrong direction". The
// rewritten message must name BOTH possible causes, honestly, not invent
// which one it was.
func TestWrapSetPhotoErrorRewritesInvalidImageFormat(t *testing.T) {
	jpegBytes := encodeTestJPEG(t, 4, 3)
	got := wrapSetPhotoError(wmeow.ErrInvalidImageFormat, jpegBytes)
	if got == nil {
		t.Fatal("wrapSetPhotoError(ErrInvalidImageFormat): want an error")
	}
	msg := got.Error()
	if !strings.Contains(msg, "formato") || !strings.Contains(msg, "tamaño") {
		t.Errorf("error = %q, want it to name BOTH possible causes (formato, tamaño)", msg)
	}
}

// TestWrapSetPhotoErrorLogsSizeAndDimensions is Citrino's second ask: the
// first real rejection should leave the data we don't have today (whether
// the server's real limit is size or dimension, and where it sits) —
// logged, not discovered by deliberately provoking it against production.
func TestWrapSetPhotoErrorLogsSizeAndDimensions(t *testing.T) {
	jpegBytes := encodeTestJPEG(t, 4, 3)
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })

	_ = wrapSetPhotoError(wmeow.ErrInvalidImageFormat, jpegBytes)

	out := buf.String()
	if !strings.Contains(out, "4x3") {
		t.Errorf("log = %q, want the real decoded dimensions (4x3)", out)
	}
	wantBytes := len(jpegBytes)
	if !strings.Contains(out, "bytes") {
		t.Errorf("log = %q, want the byte size mentioned", out)
	}
	_ = wantBytes
}

// TestWrapSetPhotoErrorLeavesOtherErrorsUnchanged: an unrelated failure
// (e.g. no connection) must pass through verbatim — only the specific,
// misleading ErrInvalidImageFormat gets rewritten.
func TestWrapSetPhotoErrorLeavesOtherErrorsUnchanged(t *testing.T) {
	other := errors.New("websocket not connected")
	got := wrapSetPhotoError(other, nil)
	if !errors.Is(got, other) {
		t.Errorf("wrapSetPhotoError on an unrelated error = %v, want it passed through unchanged", got)
	}
}

// TestWrapSetPhotoErrorNilIsNil: no error in, no error out.
func TestWrapSetPhotoErrorNilIsNil(t *testing.T) {
	if err := wrapSetPhotoError(nil, nil); err != nil {
		t.Errorf("wrapSetPhotoError(nil, nil) = %v, want nil", err)
	}
}

// ── T111 — SetGroupPhoto's own size guard (end to end, no network) ─────────

// TestSetGroupPhotoRejectsOversizedImageBeforeParsingJID confirms the guard
// runs inside the real method, not just in the standalone helper.
func TestSetGroupPhotoRejectsOversizedImageBeforeParsingJID(t *testing.T) {
	a := &Adapter{client: newTestWmeowClient(t)}
	big := make([]byte, profilePhotoMaxBytes+1)
	_, err := a.SetGroupPhoto(context.Background(), "123@g.us", big)
	if err == nil {
		t.Fatal("SetGroupPhoto with an oversized payload: want an error")
	}
	if !strings.Contains(err.Error(), "grande") {
		t.Errorf("error = %q, want it to mention the image is too large", err)
	}
}

// ── T111 — SetProfilePhoto reuses SetGroupPhoto against the OWN JID ────────

// TestSetProfilePhotoRefusesWithoutOwnJID: no client.Store.ID (not paired
// yet) must error cleanly, not panic — same nil-safe convention as the rest
// of this package.
func TestSetProfilePhotoRefusesWithoutOwnJID(t *testing.T) {
	a := &Adapter{client: newTestWmeowClient(t)}
	_, err := a.SetProfilePhoto(context.Background(), []byte("whatever"))
	if err == nil {
		t.Fatal("SetProfilePhoto with no own JID: want an error")
	}
}
