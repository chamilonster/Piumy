package restapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"piumy-gateway/internal/store"
)

// fakeAvatarRequester records every RequestAvatar call, mirroring the
// project's own fake-injector test convention (capipush_test.go's
// fakeInjector).
type fakeAvatarRequester struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeAvatarRequester) RequestAvatar(jid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, jid)
}

func (f *fakeAvatarRequester) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func TestAvatarEndpointServesCachedBytesAndNudgesRecheck(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "111.jpg")
	if err := os.WriteFile(path, []byte("avatar-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := newTestStore(t)
	jid := "111@s.whatsapp.net"
	if err := st.UpsertAvatar(store.Avatar{JID: jid, PictureID: "pic-1", Path: path, FetchedAt: 100, NextCheckAt: 200}); err != nil {
		t.Fatal(err)
	}
	req := &fakeAvatarRequester{}
	srv := httptest.NewServer(NewMux(Deps{Store: st, Avatars: req}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/avatar?jid=" + jid)
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
	if string(body) != "avatar-bytes" {
		t.Errorf("body = %q, want the cached file's bytes", body)
	}
	if got := req.count(); got != 1 {
		t.Errorf("RequestAvatar calls = %d, want 1 — the handler must nudge a re-check even when serving from cache", got)
	}
}

// TestAvatarEndpointNotFoundWithNoCachedRow is T17's own "sin foto, sin
// hueco": no cache yet must be a clean, immediate 404 (so the frontend
// falls back to initials right away), never a wait on the network.
func TestAvatarEndpointNotFoundWithNoCachedRow(t *testing.T) {
	st := newTestStore(t)
	req := &fakeAvatarRequester{}
	srv := httptest.NewServer(NewMux(Deps{Store: st, Avatars: req}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/avatar?jid=999@s.whatsapp.net")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status with no cached row = %d, want 404", resp.StatusCode)
	}
	if got := req.count(); got != 1 {
		t.Errorf("RequestAvatar calls = %d, want 1 — a miss must still queue a check for next time", got)
	}
}

// TestAvatarEndpointNotFoundForConfirmedNoPhoto covers a row that exists
// but was written by the "confirmed no photo" outcome (Path=="") — same
// 404-not-500 shape as a missing row, and the same frontend fallback.
func TestAvatarEndpointNotFoundForConfirmedNoPhoto(t *testing.T) {
	st := newTestStore(t)
	jid := "111@s.whatsapp.net"
	if err := st.UpsertAvatar(store.Avatar{JID: jid, NextCheckAt: 200}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/avatar?jid=" + jid)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status for a confirmed-no-photo row = %d, want 404", resp.StatusCode)
	}
}

// TestAvatarEndpointNotFoundForDiskDrift: same disk<->DB drift guard as
// handleMedia's own TestMediaEndpointNotFoundForDiskDrift.
func TestAvatarEndpointNotFoundForDiskDrift(t *testing.T) {
	st := newTestStore(t)
	jid := "111@s.whatsapp.net"
	if err := st.UpsertAvatar(store.Avatar{JID: jid, Path: filepath.Join(t.TempDir(), "gone.jpg"), NextCheckAt: 200}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/avatar?jid=" + jid)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status for a row whose file is missing on disk = %d, want 404", resp.StatusCode)
	}
}

func TestAvatarEndpointRequiresJID(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/avatar")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status without jid = %d, want 400", resp.StatusCode)
	}
}

// TestAvatarEndpointNilAvatarsIsSafe: d.Avatars unwired must still serve
// the cache, just without nudging a re-check (Deps.Avatars' own doc).
func TestAvatarEndpointNilAvatarsIsSafe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "111.jpg")
	if err := os.WriteFile(path, []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := newTestStore(t)
	jid := "111@s.whatsapp.net"
	if err := st.UpsertAvatar(store.Avatar{JID: jid, Path: path, NextCheckAt: 200}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st})) // Avatars left nil
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/avatar?jid=" + jid)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 even with Avatars unwired", resp.StatusCode)
	}
}

// TestAvatarEndpointAuthGate mirrors handleMedia's own convention
// (TestMediaEndpointAuthGate): an <img> src can't set X-API-Key, so
// ?key= must work.
func TestAvatarEndpointAuthGate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "111.jpg")
	if err := os.WriteFile(path, []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := newTestStore(t)
	jid := "111@s.whatsapp.net"
	if err := st.UpsertAvatar(store.Avatar{JID: jid, Path: path, NextCheckAt: 200}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st, APIKey: "secret"}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/avatar?jid=" + jid)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status without any key = %d, want 401", resp.StatusCode)
	}

	respKeyed, err := http.Get(srv.URL + "/api/avatar?jid=" + jid + "&key=secret")
	if err != nil {
		t.Fatal(err)
	}
	defer respKeyed.Body.Close()
	if respKeyed.StatusCode != http.StatusOK {
		t.Errorf("status with ?key=secret = %d, want 200", respKeyed.StatusCode)
	}
}
