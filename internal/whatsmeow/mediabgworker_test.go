package whatsmeow

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"piumy-gateway/internal/store"
)

// TestMediaBgWorkerLoopExitsOnCancel mirrors
// TestHistoryWorkerLoopExitsOnCancel's discipline — an already-cancelled ctx
// must end the loop promptly, even mid-wait. The huge 1h window (never
// actually slept — DelayWindow.Sleep returns early on ctx.Done()) is what
// proves cancellation ends it, not the timer expiring.
func TestMediaBgWorkerLoopExitsOnCancel(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	a := &Adapter{store: st, mediaDir: t.TempDir(), actionDelayMin: time.Hour, actionDelayMax: time.Hour}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		a.mediaBgWorkerLoop(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("mediaBgWorkerLoop hung after an already-cancelled ctx")
	}
}

// TestMediaBgWorkerLoopNilStoreIsNoOp confirms the nil-safe convention (same
// as syncLoop) — without a store, must return immediately.
func TestMediaBgWorkerLoopNilStoreIsNoOp(t *testing.T) {
	a := &Adapter{}
	done := make(chan struct{})
	go func() {
		a.mediaBgWorkerLoop(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("mediaBgWorkerLoop with a nil store did not return immediately")
	}
}

// TestMediaBgWorkerLoopEmptyMediaDirIsNoOp: MediaDir="" means media download
// is disabled (Config.MediaDir's own doc, media.go) — the background worker
// must not run either, same guard downloadAndStoreMedia already applies.
func TestMediaBgWorkerLoopEmptyMediaDirIsNoOp(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &Adapter{store: st}

	done := make(chan struct{})
	go func() {
		a.mediaBgWorkerLoop(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("mediaBgWorkerLoop with an empty MediaDir did not return immediately")
	}
}

// TestDownloadNextPendingMediaNilStoreIsNoOp confirms the nil-safe
// convention — without a store, must not panic. Not reachable from
// mediaBgWorkerLoop (guarded earlier), but downloadNextPendingMedia is its
// own testable unit.
func TestDownloadNextPendingMediaNilStoreIsNoOp(t *testing.T) {
	a := &Adapter{}
	a.downloadNextPendingMedia(context.Background()) // must not panic
}

// TestDownloadNextPendingMediaEmptyBacklogIsNoOp confirms no panic/error
// once there's nothing left to download.
func TestDownloadNextPendingMediaEmptyBacklogIsNoOp(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &Adapter{store: st}
	a.downloadNextPendingMedia(context.Background()) // must not panic
}

// TestDownloadNextPendingMediaPicksGloballyOldest confirms the worker reads
// from store.NextMediaPending (cross-chat FIFO), not any per-chat view —
// a failing download (empty DirectPath, deterministic, no network) still
// proves WHICH item was picked, by checking it (and only it) survives.
func TestDownloadNextPendingMediaPicksGloballyOldest(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.AddMediaPending(store.MediaPending{ChatJID: "2@c.us", MsgID: "new", Mime: "image/jpeg", Kind: "photo", TS: 300}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMediaPending(store.MediaPending{ChatJID: "1@c.us", MsgID: "old", Mime: "image/jpeg", Kind: "photo", TS: 100}); err != nil {
		t.Fatal(err)
	}
	a := &Adapter{store: st, mediaDir: t.TempDir(), client: newTestWmeowClient(t)}

	a.downloadNextPendingMedia(context.Background())

	old, ok, err := st.GetMediaPending("1@c.us", "old")
	if err != nil || !ok {
		t.Fatalf("the globally oldest item must still be the one attempted (and left, since it fails): ok=%v err=%v", ok, err)
	}
	if old.Attempts != 1 {
		t.Errorf("old.Attempts = %d, want 1 — the failed attempt must be counted", old.Attempts)
	}
	fresh, ok, err := st.GetMediaPending("2@c.us", "new")
	if err != nil || !ok {
		t.Fatalf("the newer item must be untouched this tick: ok=%v err=%v", ok, err)
	}
	if fresh.Attempts != 0 {
		t.Errorf("fresh.Attempts = %d, want 0 — untouched this tick", fresh.Attempts)
	}
}

// TestDownloadNextPendingMediaGivesUpAfterMaxAttempts is the retry-cap fix
// (Citrino catch, ct-2026-07-21-1437 parte 2): a permanently failing row
// (empty DirectPath, deterministic) must stop blocking the queue once it
// hits store.MaxMediaPendingAttempts — the row itself survives (GetMediaPending
// still finds it, for a future re-capture to reset), but NextMediaPending
// stops returning it, so a fresher row behind it gets a turn.
func TestDownloadNextPendingMediaGivesUpAfterMaxAttempts(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.AddMediaPending(store.MediaPending{ChatJID: "1@c.us", MsgID: "stale", Mime: "image/jpeg", Kind: "photo", TS: 100}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMediaPending(store.MediaPending{ChatJID: "2@c.us", MsgID: "fresh", Mime: "image/jpeg", Kind: "photo", TS: 200}); err != nil {
		t.Fatal(err)
	}
	a := &Adapter{store: st, mediaDir: t.TempDir(), client: newTestWmeowClient(t)}

	for i := 0; i < store.MaxMediaPendingAttempts; i++ {
		a.downloadNextPendingMedia(context.Background())
	}

	stale, ok, err := st.GetMediaPending("1@c.us", "stale")
	if err != nil || !ok {
		t.Fatalf("the stale row must survive (not deleted): ok=%v err=%v", ok, err)
	}
	if stale.Attempts != store.MaxMediaPendingAttempts {
		t.Errorf("stale.Attempts = %d, want %d", stale.Attempts, store.MaxMediaPendingAttempts)
	}

	// One more tick: NextMediaPending must now skip "stale" (at the cap) and
	// reach "fresh" instead.
	a.downloadNextPendingMedia(context.Background())
	fresh, ok, err := st.GetMediaPending("2@c.us", "fresh")
	if err != nil || !ok {
		t.Fatalf("GetMediaPending: ok=%v err=%v", ok, err)
	}
	if fresh.Attempts != 1 {
		t.Errorf("fresh.Attempts = %d, want 1 — it must get its turn once 'stale' hit the cap", fresh.Attempts)
	}
}
