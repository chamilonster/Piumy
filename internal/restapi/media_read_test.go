package restapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"piumy-gateway/internal/store"
)

func TestMediaEndpointServesLowQByDefaultAndFullWithFlag(t *testing.T) {
	dir := t.TempDir()
	lowPath := filepath.Join(dir, "m1_lowq.jpg")
	fullPath := filepath.Join(dir, "m1.jpg")
	if err := os.WriteFile(lowPath, []byte("lowq-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte("full-original-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := newTestStore(t)
	if err := st.AddMedia(store.Media{
		MsgID: "m1", ChatJID: "chat1@s.whatsapp.net",
		Path: lowPath, FullPath: fullPath, Mime: "image/jpeg", Size: 123, TS: 100,
	}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/media?chat=chat1@s.whatsapp.net&msg=m1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q, want image/jpeg", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "lowq-bytes" {
		t.Errorf("body = %q, want the low-q file's bytes", body)
	}

	respFull, err := http.Get(srv.URL + "/api/media?chat=chat1@s.whatsapp.net&msg=m1&full=1")
	if err != nil {
		t.Fatal(err)
	}
	defer respFull.Body.Close()
	if respFull.StatusCode != http.StatusOK {
		t.Fatalf("full=1 status = %d, want 200", respFull.StatusCode)
	}
	bodyFull, _ := io.ReadAll(respFull.Body)
	if string(bodyFull) != "full-original-bytes" {
		t.Errorf("full=1 body = %q, want the original file's bytes", bodyFull)
	}
}

func TestMediaEndpointNotFoundForMissingDBRow(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/media?chat=nope@s.whatsapp.net&msg=nope")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status for a missing media row = %d, want 404", resp.StatusCode)
	}
}

// TestMediaEndpointNotFoundForDiskDrift covers the known disk<->DB drift
// (not every DB row is guaranteed to have a file still on disk): a row
// pointing at a file that doesn't exist must 404 cleanly, never 500/panic.
func TestMediaEndpointNotFoundForDiskDrift(t *testing.T) {
	st := newTestStore(t)
	if err := st.AddMedia(store.Media{
		MsgID: "m1", ChatJID: "chat1@s.whatsapp.net",
		Path: filepath.Join(t.TempDir(), "does-not-exist.jpg"), FullPath: filepath.Join(t.TempDir(), "does-not-exist-full.jpg"),
		Mime: "image/jpeg", Size: 1, TS: 1,
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/media?chat=chat1@s.whatsapp.net&msg=m1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status for a DB row whose file is missing on disk = %d, want 404", resp.StatusCode)
	}
}

func TestMediaEndpointRequiresChatAndMsg(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/media")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status without chat/msg = %d, want 400", resp.StatusCode)
	}
}

// TestMediaEndpointAuthGate mirrors dashboard.go's handleQRImage auth
// convention (S1d/S1e's session-or-?key=) — an <img>/<audio> src can't set
// X-API-Key, so ?key= must work exactly like it does for /api/qr/image.
func TestMediaEndpointAuthGate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m1.jpg")
	if err := os.WriteFile(path, []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := newTestStore(t)
	if err := st.AddMedia(store.Media{MsgID: "m1", ChatJID: "chat1@s.whatsapp.net", Path: path, FullPath: path, Mime: "image/jpeg", TS: 1}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st, APIKey: "secret"}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/media?chat=chat1@s.whatsapp.net&msg=m1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status without any key = %d, want 401", resp.StatusCode)
	}

	respKeyed, err := http.Get(srv.URL + "/api/media?chat=chat1@s.whatsapp.net&msg=m1&key=secret")
	if err != nil {
		t.Fatal(err)
	}
	defer respKeyed.Body.Close()
	if respKeyed.StatusCode != http.StatusOK {
		t.Errorf("status with ?key=secret = %d, want 200", respKeyed.StatusCode)
	}
}
