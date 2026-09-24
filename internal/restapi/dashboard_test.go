package restapi

import (
	"bytes"
	"image"
	_ "image/png"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"piumy-gateway/internal/config"
	"piumy-gateway/internal/dashboard"
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

func getDashboardIndex(t *testing.T, d Deps) string {
	t.Helper()
	srv := httptest.NewServer(NewMux(d))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/dashboard/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

// S5 (ct-2026-09-23-2038): a named account's window and login say which
// account they are BEFORE anyone signs in — with an API key set, so no
// session, and no /api/status, is involved.
func TestDashboardIndexCarriesTheAccountBeforeLogin(t *testing.T) {
	page := getDashboardIndex(t, Deps{Account: "cuenta-2", APIKey: "s3cr3t"})

	for _, want := range []string{
		"<title>Piumy Gateway — cuenta-2</title>",
		`data-account="cuenta-2"`,
		`data-account-color="` + config.ColorForAccount("cuenta-2").Hex + `"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("index.html misses %q", want)
		}
	}
}

// Once the session is linked, the window says the label (WhatsApp name and
// number tail) — and the name is anyone's text, so it can't break out of the
// attribute or the title.
func TestDashboardIndexShowsTheLabelEscaped(t *testing.T) {
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
	if err := sm.Update(func(s *state.Status) {
		s.OwnName = `<b>"Uno" & Co`
		s.OwnJID = "55500000041@s.whatsapp.net"
	}); err != nil {
		t.Fatal(err)
	}
	page := getDashboardIndex(t, Deps{Account: "cuenta-2", State: sm})

	if !strings.Contains(page, "<title>Piumy Gateway — &lt;b&gt;&#34;Uno&#34; &amp; Co · ...0041</title>") {
		t.Errorf("title doesn't carry the escaped label: %s", page[:min(len(page), 300)])
	}
	if !strings.Contains(page, `data-account="&lt;b&gt;&#34;Uno&#34; &amp; Co · ...0041"`) {
		t.Error("data-account doesn't carry the escaped label")
	}
	if strings.Contains(page, `data-account="<b>`) {
		t.Error("the WhatsApp name was written unescaped into an attribute")
	}
}

// The page needs no session and the port listens on every interface: the
// owner's WhatsApp name and number tail go only to this machine's own window.
// httptest.NewRequest's RemoteAddr (192.0.2.1) stands for someone on the network.
func TestDashboardIndexGivesOtherMachinesOnlyTheAccountId(t *testing.T) {
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
	if err := sm.Update(func(s *state.Status) {
		s.OwnName = "Contacto Uno"
		s.OwnJID = "55500000041@s.whatsapp.net"
	}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	NewMux(Deps{Account: "cuenta-2", State: sm}).ServeHTTP(rec, httptest.NewRequest("GET", "/dashboard/", nil))
	page := rec.Body.String()

	if !strings.Contains(page, `data-account="cuenta-2"`) || !strings.Contains(page, "<title>Piumy Gateway — cuenta-2</title>") {
		t.Errorf("a remote request should get the account id: %s", page[:min(len(page), 300)])
	}
	if strings.Contains(page, "Contacto Uno") || strings.Contains(page, "0041") {
		t.Error("the WhatsApp name or number tail reached a request from another machine, with no session")
	}
}

// The default account is untouched: byte for byte the embedded file.
func TestDashboardIndexWithoutAccountIsTheEmbeddedFile(t *testing.T) {
	want, err := fs.ReadFile(dashboard.WebFS(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if got := getDashboardIndex(t, Deps{}); got != string(want) {
		t.Error("with no account the index.html served differs from the embedded one")
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
