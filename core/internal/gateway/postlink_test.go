// SPDX-License-Identifier: AGPL-3.0-only
package gateway

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"pimywa/internal/governor"
	"pimywa/internal/router"
	"pimywa/internal/state"
	"pimywa/internal/store"
)

// newPostLinkTestDir returns a fresh temp dir OUTSIDE t.TempDir()'s
// auto-cleanup tree — NewController opens the session SQLite file and
// nothing in this package exposes a way to close it again (no test needed
// one before), so on Windows t.TempDir()'s cleanup fails on the still-open
// file handle. Best-effort cleanup here just ignores that; the OS temp dir
// gets reaped eventually, and the real deployment target (Linux) doesn't
// have this file-locking behavior at all.
func newPostLinkTestDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "pimywa-postlink-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestSetPostLinkHookFiresOnConnected covers the DoD directly: a
// hook registered via SetPostLinkHook runs when the gateway's connected
// callback fires — alongside the existing QR-timer-cancel logic, not
// instead of it. Drives the real chained closure NewController builds
// (invoked here via the unexported onConnectedHook field, same package).
func TestSetPostLinkHookFiresOnConnected(t *testing.T) {
	dir := newPostLinkTestDir(t)
	st, err := store.Open(filepath.Join(dir, "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sm := state.NewManager(filepath.Join(dir, "status.json"), 8)
	rtMgr := router.NewManager(filepath.Join(dir, "router.json"))
	gov := governor.NewLimiter(10, time.Minute)

	c, err := NewController(Config{SessionDB: filepath.Join(dir, "wa.db")}, st, sm, rtMgr, gov)
	if err != nil {
		t.Fatal(err)
	}

	called := make(chan struct{}, 1)
	c.SetPostLinkHook(func() { called <- struct{}{} })

	// Fire the same hook NewController wired onto the gateway — this is
	// exactly what onConnected() calls, without needing a real WhatsApp
	// connection to reach it.
	c.gw.onConnectedHook()

	select {
	case <-called:
	default:
		t.Fatal("post-link hook did not run")
	}
}

// TestSetPostLinkHookNilIsSafe covers that firing onConnectedHook with no
// post-link hook registered (the default) doesn't panic — the QR-timer-
// cancel logic must still be the only thing that runs.
func TestSetPostLinkHookNilIsSafe(t *testing.T) {
	dir := newPostLinkTestDir(t)
	st, err := store.Open(filepath.Join(dir, "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sm := state.NewManager(filepath.Join(dir, "status.json"), 8)
	rtMgr := router.NewManager(filepath.Join(dir, "router.json"))
	gov := governor.NewLimiter(10, time.Minute)

	c, err := NewController(Config{SessionDB: filepath.Join(dir, "wa.db")}, st, sm, rtMgr, gov)
	if err != nil {
		t.Fatal(err)
	}

	c.gw.onConnectedHook() // must not panic with no hook set
}
