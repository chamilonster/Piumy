package whatsmeow

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"piumy-gateway/internal/governor"
	"piumy-gateway/internal/store"
)

// TestFetchPendingMediaParallelOnlyThisChat confirms the on-demand fetch
// only touches the given chat's own media_pending backlog — a failing
// download (empty DirectPath, deterministic, no network) still proves WHICH
// rows were attempted, by checking attempts advanced only for the target
// chat.
func TestFetchPendingMediaParallelOnlyThisChat(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.AddMediaPending(store.MediaPending{ChatJID: "1@c.us", MsgID: "img1", Mime: "image/jpeg", Kind: "photo", TS: 100}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMediaPending(store.MediaPending{ChatJID: "2@c.us", MsgID: "other", Mime: "image/jpeg", Kind: "photo", TS: 50}); err != nil {
		t.Fatal(err)
	}

	a := &Adapter{store: st, mediaDir: t.TempDir(), client: newTestWmeowClient(t)}
	a.fetchPendingMediaParallel(context.Background(), "1@c.us")

	got, ok, err := st.GetMediaPending("1@c.us", "img1")
	if err != nil || !ok {
		t.Fatalf("GetMediaPending 1@c.us/img1: ok=%v err=%v", ok, err)
	}
	if got.Attempts != 1 {
		t.Errorf("1@c.us/img1 Attempts = %d, want 1 (attempted)", got.Attempts)
	}
	other, ok, err := st.GetMediaPending("2@c.us", "other")
	if err != nil || !ok {
		t.Fatalf("GetMediaPending 2@c.us/other: ok=%v err=%v", ok, err)
	}
	if other.Attempts != 0 {
		t.Errorf("2@c.us/other Attempts = %d, want 0 (a different chat, untouched)", other.Attempts)
	}
}

// TestFetchPendingMediaParallelDrainsMultipleItems confirms every pending
// row in the chat gets a turn through the concurrent pool, not just the
// first one.
func TestFetchPendingMediaParallelDrainsMultipleItems(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	chat := "1@c.us"
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		if err := st.AddMediaPending(store.MediaPending{ChatJID: chat, MsgID: id, Mime: "image/jpeg", Kind: "photo", TS: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}

	a := &Adapter{store: st, mediaDir: t.TempDir(), client: newTestWmeowClient(t)}
	a.fetchPendingMediaParallel(context.Background(), chat)

	for _, id := range []string{"a", "b", "c", "d", "e"} {
		got, ok, err := st.GetMediaPending(chat, id)
		if err != nil || !ok {
			t.Fatalf("GetMediaPending %s: ok=%v err=%v", id, ok, err)
		}
		if got.Attempts != 1 {
			t.Errorf("%s Attempts = %d, want 1 — every item must get a turn through the pool", id, got.Attempts)
		}
	}
}

// TestFetchPendingMediaParallelSkipsRowsAtAttemptsCap: a row the background
// worker already gave up on must not be retried here either — retrying
// faster/in-parallel doesn't make a stale directPath valid again.
func TestFetchPendingMediaParallelSkipsRowsAtAttemptsCap(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	chat := "1@c.us"
	if err := st.AddMediaPending(store.MediaPending{ChatJID: chat, MsgID: "capped", Mime: "image/jpeg", Kind: "photo", TS: 1}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < store.MaxMediaPendingAttempts; i++ {
		if err := st.IncrementMediaPendingAttempts(chat, "capped"); err != nil {
			t.Fatal(err)
		}
	}

	a := &Adapter{store: st, mediaDir: t.TempDir(), client: newTestWmeowClient(t)}
	a.fetchPendingMediaParallel(context.Background(), chat)

	got, ok, err := st.GetMediaPending(chat, "capped")
	if err != nil || !ok {
		t.Fatalf("GetMediaPending: ok=%v err=%v", ok, err)
	}
	if got.Attempts != store.MaxMediaPendingAttempts {
		t.Errorf("Attempts = %d, want unchanged at %d — a capped row must not be retried", got.Attempts, store.MaxMediaPendingAttempts)
	}
}

// TestFetchPendingMediaParallelRespectsKillSwitch confirms the fetch stops
// starting new downloads once the kill switch trips.
func TestFetchPendingMediaParallelRespectsKillSwitch(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	chat := "1@c.us"
	if err := st.AddMediaPending(store.MediaPending{ChatJID: chat, MsgID: "img1", Mime: "image/jpeg", Kind: "photo", TS: 1}); err != nil {
		t.Fatal(err)
	}

	gov := governor.NewLimiter(10, time.Minute)
	gov.SetKill(true)
	a := &Adapter{store: st, governor: gov, mediaDir: t.TempDir(), client: newTestWmeowClient(t)}

	done := make(chan struct{})
	go func() {
		a.fetchPendingMediaParallel(context.Background(), chat)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fetchPendingMediaParallel hung with the kill switch active")
	}

	got, ok, err := st.GetMediaPending(chat, "img1")
	if err != nil || !ok {
		t.Fatalf("GetMediaPending: ok=%v err=%v", ok, err)
	}
	if got.Attempts != 0 {
		t.Errorf("Attempts = %d, want 0 — the kill switch must stop it before any attempt", got.Attempts)
	}
}

// TestFetchPendingMediaParallelNilStoreIsNoOp confirms the nil-safe
// convention (same as downloadNextPendingMedia).
func TestFetchPendingMediaParallelNilStoreIsNoOp(t *testing.T) {
	a := &Adapter{}
	a.fetchPendingMediaParallel(context.Background(), "1@c.us") // must not panic
}

// TestFetchPendingMediaParallelEmptyBacklogIsNoOp confirms no panic/error
// once there's nothing left to download for the chat.
func TestFetchPendingMediaParallelEmptyBacklogIsNoOp(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &Adapter{store: st}
	a.fetchPendingMediaParallel(context.Background(), "1@c.us") // must not panic
}

// TestFetchPendingMediaReturnsImmediately confirms FetchPendingMedia is
// fire-and-forget — the HTTP handler calling it must never block on however
// long the chat's backlog takes.
func TestFetchPendingMediaReturnsImmediately(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	// No message seeded for "1@c.us" — MediaPendingForChat finds nothing, so
	// the detached goroutine below finishes on its own near-instantly.

	a := &Adapter{store: st}
	done := make(chan struct{})
	go func() {
		a.FetchPendingMedia("1@c.us")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("FetchPendingMedia blocked instead of returning immediately")
	}
	time.Sleep(50 * time.Millisecond) // let the detached goroutine's empty-backlog return finish
	st.Close()
}

// TestFetchOnePendingMediaInFlightIsNotCountedAsFailure is parte 3's
// coordination contract (Citrino: "que no se pisen bajando el mismo
// ítem"): if the row is already claimed by the OTHER consumer (simulated
// directly here — see TestClaimMediaDownloadOnlyOneWinner in media_test.go
// for the claim/release mechanism itself), fetchOnePendingMedia must not
// increment attempts.
func TestFetchOnePendingMediaInFlightIsNotCountedAsFailure(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p := store.MediaPending{ChatJID: "1@c.us", MsgID: "img1", Mime: "image/jpeg", Kind: "photo"}
	if err := st.AddMediaPending(p); err != nil {
		t.Fatal(err)
	}
	a := &Adapter{store: st, mediaDir: t.TempDir(), client: newTestWmeowClient(t)}

	if !a.claimMediaDownload(p.ChatJID, p.MsgID) {
		t.Fatal("claimMediaDownload: want true on a fresh claim")
	}
	defer a.releaseMediaDownload(p.ChatJID, p.MsgID)

	a.fetchOnePendingMedia(context.Background(), p)

	got, ok, err := st.GetMediaPending(p.ChatJID, p.MsgID)
	if err != nil || !ok {
		t.Fatalf("GetMediaPending: ok=%v err=%v", ok, err)
	}
	if got.Attempts != 0 {
		t.Errorf("Attempts = %d, want 0 — an in-flight collision must not count as a failure", got.Attempts)
	}
}
