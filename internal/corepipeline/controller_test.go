package corepipeline

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"piumy-gateway/internal/gateway"
	"piumy-gateway/internal/governor"
	"piumy-gateway/internal/router"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
)

func TestControllerRunsPipelineEndToEnd(t *testing.T) {
	st, _, _, fgw, p := newTestPipeline(t)
	c := NewController(fgw, p)

	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	if !c.Status().Connected {
		t.Error("Status().Connected = false, want true (fakeGateway starts connected)")
	}

	fgw.inbound <- gateway.Inbound{ChatJID: "111@c.us", MsgID: "m1", Text: "hola", TS: 1}

	deadline := time.Now().Add(2 * time.Second)
	for {
		msgs, err := st.GetMessages("111@c.us", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("inbound message never reached the store through Controller.Start's pipeline")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestControllerStartStopIdempotent(t *testing.T) {
	_, _, _, fgw, p := newTestPipeline(t)
	c := NewController(fgw, p)

	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	if err := c.Start(); err != nil { // second Start: no-op, not an error
		t.Fatal(err)
	}

	c.Stop()
	c.Stop() // second Stop: no-op, must not hang or panic
}

func TestControllerMarkReadNoOpWhenNotRunning(t *testing.T) {
	_, _, _, fgw, p := newTestPipeline(t)
	c := NewController(fgw, p)
	// Never started — MarkRead must return immediately, not panic on a nil
	// run context or block forever.
	c.MarkRead("111@c.us", nil)
}

// TestControllerStopWaitsForInFlightOutboxSend is a regression test: Run
// used to launch outboxLoop in its own untracked goroutine, so Stop() could
// return while a processOutbox call was still mid-flight — its trailing
// store writes (MarkSent/AddMessage) then race a caller's store.Close()
// right after Stop() returns ("database is closed", the flaky seen in
// openwa's TestEndToEndWithRealPipeline). Fixed by 6b326dc's sync.WaitGroup
// in Run (waits for outboxLoop too, not just inboundLoop) — verified still
// intact here.
//
// ct-2026-07-18-1507: this test itself used to synchronize with a FIXED
// time.Sleep(20ms) before calling Stop(), gambling that outboxLoop's
// ticker + the dispatch/composing pacing delays had reliably reached
// gw.Send by then. Under scheduler load that gamble occasionally lost —
// Stop() was invoked before Send had even started, so it correctly had
// nothing to join and returned fast, which the test misread as "the join
// is broken" (characterized flaky via -count=5, Citrino). fakeGateway's
// sendStarted channel now signals the INSTANT Send is entered, so the test
// waits on a real event instead of guessing a duration — deterministic,
// no production code changed (Run/Stop's join was already correct).
//
// ct-2026-08-07, second flake (Citrino reproduced under 40 parallel full-
// suite runs, two different symptoms — "outbox Send was never entered" and
// "sql: database is closed"): NOT a production race — Run/Stop's join is
// still correct (re-verified: WaitGroup + happens-before hold under the
// exact same heavy-load reproduction). The bug was here, in this test's own
// cleanup. Before this fix, `c` was only ever Stop()ed by the explicit
// goroutine at the bottom — if either of the FIRST two t.Fatal calls fired
// (the "Send was never entered" timeout, extended under load to a couple
// hundred seconds by 15 parallel `go test ./...` runs competing for CPU),
// t.Fatal's Goexit unwound straight past that goroutine, which never ran:
// the Controller kept running, un-stopped, its outboxLoop still ticking
// every 5ms against a store the deferred st.Close() (registered earlier)
// closed out from under it — hence the repeated "database is closed".
// stopOnce (sync.OnceFunc) makes the deferred cleanup below the SAME
// underlying call as the explicit mid-test Stop(): whichever goroutine
// calls it, every OTHER caller blocks until that one real Stop() finishes
// (sync.Once's own documented contract) — so the deferred call actually
// joins outboxLoop instead of racing an already-idempotent no-op, on every
// exit path including an early t.Fatal.
func TestControllerStopWaitsForInFlightOutboxSend(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rt := router.NewManager(filepath.Join(t.TempDir(), "router.json"))
	gov := governor.NewLimiter(100, time.Minute)
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
	fgw := newFakeGateway()
	const sendDelay = 60 * time.Millisecond
	fgw.setSendDelay(sendDelay)
	sendStarted := make(chan struct{}, 1)
	fgw.setSendStarted(sendStarted)

	if err := st.Enqueue("111@c.us", "hola", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}

	p := New(fgw, st, rt, gov, sm, Config{
		OutboxPoll:       5 * time.Millisecond,
		DispatchDelayMin: time.Millisecond, DispatchDelayMax: 2 * time.Millisecond,
		ReadDelayMin: time.Millisecond, ReadDelayMax: 2 * time.Millisecond,
		ComposingMin: time.Millisecond, ComposingMax: 2 * time.Millisecond,
	})
	c := NewController(fgw, p)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	stopOnce := sync.OnceFunc(func() {
		c.Stop()
		close(stopped)
	})
	// Runs on EVERY exit path, including an early t.Fatal below — never
	// leaves the Controller running past this test function, whatever
	// triggered the return. Registered after defer st.Close() above, so it
	// unwinds first (LIFO): Stop() always joins outboxLoop before the store
	// closes.
	defer stopOnce()

	// Wait for outboxLoop to actually be blocked inside gw.Send's artificial
	// delay before we try to stop it — a real event, not a timing guess.
	select {
	case <-sendStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("outbox Send was never entered")
	}

	go stopOnce()

	select {
	case <-stopped:
		t.Fatal("Stop() returned before the in-flight Send finished — outboxLoop isn't joined")
	case <-time.After(sendDelay / 2):
		// Still blocked partway through the delay, as expected.
	}

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() never returned")
	}
}

func TestControllerResumeIsStart(t *testing.T) {
	_, _, _, fgw, p := newTestPipeline(t)
	c := NewController(fgw, p)
	if err := c.Resume(); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	if !c.Status().Connected {
		t.Error("Status().Connected = false after Resume(), want true")
	}
}
