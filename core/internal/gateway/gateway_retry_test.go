// SPDX-License-Identifier: AGPL-3.0-only
package gateway

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"pimywa/internal/store"
)

func TestExponentialBackoff(t *testing.T) {
	cases := []struct {
		retryCount int
		want       time.Duration
	}{
		{1, 5 * time.Second},
		{2, 10 * time.Second},
		{3, 20 * time.Second},
		{4, 40 * time.Second},
	}
	for _, c := range cases {
		if got := exponentialBackoff(c.retryCount); got != c.want {
			t.Errorf("exponentialBackoff(%d) = %v, want %v", c.retryCount, got, c.want)
		}
	}
	// Must never exceed the 1h cap, however large retryCount gets.
	if got := exponentialBackoff(30); got != time.Hour {
		t.Errorf("exponentialBackoff(30) = %v, want capped at 1h", got)
	}
}

// TestRetryOrDeadLetter covers the DoD directly: failures bump retry_count
// with backoff (excluded from DueOutbox until it elapses), and after
// OutboxMaxRetry failures the item is dead-lettered and never becomes due
// again, at any future time.
func TestRetryOrDeadLetter(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.Enqueue("j1", "hola", 1); err != nil {
		t.Fatal(err)
	}

	g := &Gateway{msgSt: st, cfg: Config{OutboxMaxRetry: 3}}
	sendErr := errors.New("connection refused")

	// Failure 1: retry_count=1, backed off, not dead-lettered.
	item := mustDue(t, st)
	g.retryOrDeadLetter(item, sendErr)
	item = mustDue(t, st)
	if item.RetryCount != 1 || item.DeadLetter {
		t.Fatalf("after 1 failure: %+v, want retry_count=1 not dead-lettered", item)
	}
	if due, _ := st.DueOutbox(10, item.NextRetryTS-1); len(due) != 0 {
		t.Error("item became due before its backoff elapsed")
	}

	// Failure 2: retry_count=2, still not dead-lettered (max is 3).
	g.retryOrDeadLetter(item, sendErr)
	item = mustDue(t, st)
	if item.RetryCount != 2 || item.DeadLetter {
		t.Fatalf("after 2 failures: %+v, want retry_count=2 not dead-lettered", item)
	}

	// Failure 3: hits OutboxMaxRetry → dead-lettered, never due again.
	g.retryOrDeadLetter(item, sendErr)
	if due, err := st.DueOutbox(10, 9999999999); err != nil || len(due) != 0 {
		t.Fatalf("after reaching OutboxMaxRetry: due=%d err=%v, want dead-lettered (0 due)", len(due), err)
	}
	pending, err := st.PendingOutbox(10)
	if err != nil || len(pending) != 1 || !pending[0].DeadLetter {
		t.Fatalf("dead-lettered item should still be visible via PendingOutbox, got %+v err=%v", pending, err)
	}
}

func mustDue(t *testing.T, st *store.Store) store.Outbox {
	t.Helper()
	due, err := st.DueOutbox(10, 9999999999)
	if err != nil || len(due) != 1 {
		t.Fatalf("expected exactly one due item, got %d err=%v", len(due), err)
	}
	return due[0]
}
