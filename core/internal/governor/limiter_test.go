// SPDX-License-Identifier: AGPL-3.0-only
package governor

import (
	"testing"
	"time"
)

// TestLimiterAllowsUpToMax covers the existing per-window behavior still
// works after adding the daily cap alongside it.
func TestLimiterAllowsUpToMax(t *testing.T) {
	l := NewLimiter(2, time.Minute)
	if !l.Allow() || !l.Allow() {
		t.Fatal("first two Allow() calls should succeed with max=2")
	}
	if l.Allow() {
		t.Fatal("third Allow() should fail — per-window tokens exhausted")
	}
}

// TestLimiterDailyCapDeniesEvenWithTokensLeft covers the DoD directly
// (0753): once the daily cap is hit, Allow() denies even though the
// per-minute token bucket still has plenty left.
func TestLimiterDailyCapDeniesEvenWithTokensLeft(t *testing.T) {
	l := NewLimiter(100, time.Minute) // plenty of per-minute tokens
	l.SetDailyMax(2)

	if !l.Allow() || !l.Allow() {
		t.Fatal("first two Allow() calls should succeed with dailyMax=2")
	}
	if l.Allow() {
		t.Fatal("third Allow() should fail — daily cap exhausted, even though per-minute tokens remain")
	}
}

// TestLimiterDailyCapDisabledWhenZero covers dailyMax<=0 meaning "no daily
// cap" (a valid, deliberate choice — e.g. POST /api/settings with 0).
func TestLimiterDailyCapDisabledWhenZero(t *testing.T) {
	l := NewLimiter(100, time.Minute)
	l.SetDailyMax(0)
	for i := 0; i < 10; i++ {
		if !l.Allow() {
			t.Fatalf("Allow() denied on call %d with dailyMax=0 (disabled), want always allowed (per-minute tokens permitting)", i)
		}
	}
}

// TestLimiterSetMaxAppliesAtRuntime covers the DoD's "aplica sin reiniciar"
// (0753): SetMax changes the effective per-window limit immediately.
func TestLimiterSetMaxAppliesAtRuntime(t *testing.T) {
	l := NewLimiter(1, time.Minute)
	if !l.Allow() {
		t.Fatal("first Allow() should succeed with max=1")
	}
	if l.Allow() {
		t.Fatal("second Allow() should fail — max=1 exhausted")
	}
	l.SetMax(5)
	if l.Max() != 5 {
		t.Fatalf("Max() = %d, want 5 after SetMax", l.Max())
	}
	// Tokens don't jump to the new max instantly (refill is time-based), but
	// the ceiling itself (used by refill's rate calc) must reflect the change.
}

// TestLimiterMaxAndDailyMaxGetters cover the read side REST's GET
// /api/settings relies on to report the effective values.
func TestLimiterMaxAndDailyMaxGetters(t *testing.T) {
	l := NewLimiter(10, time.Minute)
	if l.Max() != 10 {
		t.Errorf("Max() = %d, want 10", l.Max())
	}
	l.SetDailyMax(500)
	if l.DailyMax() != 500 {
		t.Errorf("DailyMax() = %d, want 500", l.DailyMax())
	}
}

// TestSeedDailyCountSurvivesAcrossInstances covers the DoD directly (0753,
// 3rd commandment): a Limiter seeded with an already-sent count (as if
// reconstructed from message history after a restart) enforces the
// remaining daily budget correctly — it does NOT get a fresh 0-count "free"
// daily allowance just because the process restarted.
func TestSeedDailyCountSurvivesAcrossInstances(t *testing.T) {
	l := NewLimiter(100, time.Minute) // plenty of per-minute tokens
	l.SetDailyMax(3)
	l.SeedDailyCount(2) // as if 2 were already sent today, before this restart

	if !l.Allow() {
		t.Fatal("first Allow() after seeding at 2/3 should succeed (1 remaining)")
	}
	if l.Allow() {
		t.Fatal("second Allow() after seeding at 2/3 should fail — daily cap (3) now reached")
	}
}

// TestSeedDailyCountStampsToday covers that seeding sets dailyDate to today
// — otherwise the very next Allow() would mistake the seed for a stale day
// and immediately wipe it back to 0, defeating the whole restart fix.
func TestSeedDailyCountStampsToday(t *testing.T) {
	l := NewLimiter(100, time.Minute)
	l.SetDailyMax(1)
	l.SeedDailyCount(1) // already at the cap

	if l.Allow() {
		t.Fatal("Allow() right after seeding at the cap should fail, not silently reset to a fresh day")
	}
}
