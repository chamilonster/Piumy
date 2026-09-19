// Unit tests for profileStatusCache — direct, no MCP/server plumbing, so
// the timeout/TTL-expiry branches are cheap and fast to exercise (the
// end-to-end "get_status never hangs" case lives in group_tools_test.go,
// where it also proves the real wiring). Citrino's audit on T104/T103
// (ct-2026-08-29-1818/1759): GetProfileStatus is a live WhatsApp network
// call with no timeout, called from get_status — the exact tool the
// manual designates for diagnosing a dead connection.
package mcpserver

import (
	"context"
	"testing"
	"time"
)

func TestProfileStatusCacheReusesFreshValue(t *testing.T) {
	c := &profileStatusCache{}
	fgp := &fakeGroupProfile{getProfileStatus: "disponible"}

	v1, ok1 := c.get(context.Background(), fgp)
	v2, ok2 := c.get(context.Background(), fgp)

	if !ok1 || v1 != "disponible" {
		t.Fatalf("first get() = %q/%v, want disponible/true", v1, ok1)
	}
	if !ok2 || v2 != "disponible" {
		t.Fatalf("second get() = %q/%v, want disponible/true", v2, ok2)
	}
	if calls := fgp.callCount(); calls != 1 {
		t.Errorf("GetProfileStatus called %d times, want 1 — second get() should hit the cache", calls)
	}
}

func TestProfileStatusCacheRefetchesAfterTTL(t *testing.T) {
	c := &profileStatusCache{}
	fgp := &fakeGroupProfile{getProfileStatus: "v1"}

	if v, ok := c.get(context.Background(), fgp); !ok || v != "v1" {
		t.Fatalf("first get() = %q/%v, want v1/true", v, ok)
	}

	// Force staleness directly — same pattern gate_test.go uses on
	// gate.startedAt — rather than sleeping profileStatusTTL for real.
	c.fetchedAt = time.Now().Add(-2 * profileStatusTTL)
	fgp.getProfileStatus = "v2"

	v, ok := c.get(context.Background(), fgp)
	if !ok || v != "v2" {
		t.Errorf("get() after TTL expiry = %q/%v, want fresh v2/true", v, ok)
	}
	if calls := fgp.callCount(); calls != 2 {
		t.Errorf("GetProfileStatus called %d times, want 2 — TTL expiry should trigger a refetch", calls)
	}
}

func TestProfileStatusCacheTimesOutOnSlowRead(t *testing.T) {
	c := &profileStatusCache{}
	fgp := &fakeGroupProfile{blockUntilCtxDone: true}

	start := time.Now()
	v, ok := c.get(context.Background(), fgp)
	elapsed := time.Since(start)

	if ok {
		t.Error("get() reported available=true for a read that never completed")
	}
	if v != "" {
		t.Errorf("get() value = %q, want empty on a timed-out read", v)
	}
	if elapsed > profileStatusTimeout+2*time.Second {
		t.Errorf("get() took %s, want it bounded near the %s timeout", elapsed, profileStatusTimeout)
	}
}

func TestProfileStatusCacheReportsUnavailableOnError(t *testing.T) {
	c := &profileStatusCache{}
	fgp := &fakeGroupProfile{getProfileStatusErr: context.DeadlineExceeded}

	v, ok := c.get(context.Background(), fgp)
	if ok || v != "" {
		t.Errorf("get() on a failing read = %q/%v, want empty/false", v, ok)
	}
}
