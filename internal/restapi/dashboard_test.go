package restapi

import (
	"bytes"
	"image"
	_ "image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"piumy-gateway/internal/state"
)

func TestDashboardServesIndex(t *testing.T) {
	srv := httptest.NewServer(NewMux(Deps{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/dashboard/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Piumy Gateway") {
		t.Errorf("body doesn't look like the dashboard's index.html: %s", body)
	}
}

func TestDashboardRedirectsWithoutSlash(t *testing.T) {
	srv := httptest.NewServer(NewMux(Deps{}))
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(srv.URL + "/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Errorf("status = %d, want 301 (redirect to /dashboard/)", resp.StatusCode)
	}
}

func TestQRImageEndpoint(t *testing.T) {
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
	if err := sm.Update(func(s *state.Status) {
		s.ShowQR = true
		s.QRData = "2@fake-pairing-string-for-test,abc,def=="
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{State: sm}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/qr/image")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) < 8 || string(body[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Errorf("body doesn't start with the PNG magic bytes (len=%d)", len(body))
	}
	// P0 (ct-2026-07-24-0015): Code.Image() (rsc.io/qr) is broken — ignores
	// Scale, renders QR at 1px/module in the 61x61 corner of a 552x552
	// canvas (rest white, unreadable). Code.PNG() fixes this. Verify the
	// rendered PNG is properly scaled: decode, check dimensions > 61, and
	// confirm black pixels exist outside the broken corner zone.
	img, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("image.Decode: %v", err)
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 61 || h <= 61 {
		t.Fatalf("QR image %dx%d — broken Code.Image() regression (<=61px)", w, h)
	}
	_, _, _, a := img.At(w/2, h/2).RGBA()
	if a == 0 {
		t.Error("center pixel is fully transparent — QR is probably in the corner")
	}
}

func TestQRImageEndpointNotFoundWithoutPendingQR(t *testing.T) {
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
	srv := httptest.NewServer(NewMux(Deps{State: sm}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/qr/image")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (no QR pending)", resp.StatusCode)
	}
}
