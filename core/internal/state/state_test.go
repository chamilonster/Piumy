// SPDX-License-Identifier: AGPL-3.0-only
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newTestManager returns a Manager writing to a throwaway status.json.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	return NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
}

// TestSystemTierGuardsReact covers moodTier's core precedence
// guarantee: while a tier-3 system mood (error/qr/sleeping/muted/paused) is
// active, React() must never override it — a transient event notification
// (the agent reading a message, sending a reply, etc.) must not mask
// "WhatsApp is down" or "muted".
func TestSystemTierGuardsReact(t *testing.T) {
	for _, systemMood := range []string{"error", "qr", "sleeping", "muted", "paused"} {
		t.Run(systemMood, func(t *testing.T) {
			m := newTestManager(t)
			if err := m.SetMood(systemMood); err != nil {
				t.Fatal(err)
			}
			if err := m.React("reading", "reading...", 10*time.Millisecond); err != nil {
				t.Fatal(err)
			}
			if got := m.Snapshot().Mood; got != systemMood {
				t.Errorf("React while mood=%q: mood is now %q, want unchanged (%q)", systemMood, got, systemMood)
			}
		})
	}
}

// TestSystemTierGuardsSetResting covers the same guarantee for
// SetResting() — a queue-depth refresh (e.g. the agent tracker going idle)
// must not override a system mood either.
func TestSystemTierGuardsSetResting(t *testing.T) {
	for _, systemMood := range []string{"error", "qr", "sleeping", "muted", "paused"} {
		t.Run(systemMood, func(t *testing.T) {
			m := newTestManager(t)
			if err := m.SetMood(systemMood); err != nil {
				t.Fatal(err)
			}
			if err := m.Update(func(s *Status) { s.Queue = 3 }); err != nil {
				t.Fatal(err)
			}
			if err := m.SetResting(); err != nil {
				t.Fatal(err)
			}
			if got := m.Snapshot().Mood; got != systemMood {
				t.Errorf("SetResting while mood=%q (queue=3): mood is now %q, want unchanged (%q)", systemMood, got, systemMood)
			}
		})
	}
}

// TestQueueDoesNotBlockTransientEvents covers the precedence read
// confirmed here: "queue moods > transient events" describes what a transient
// event reverts TO, not something that blocks it from showing in the first
// place. An agent almost always has a nonzero queue when it calls the
// tools that trigger these events — if queue blocked them, the whole
// feature would never fire.
func TestQueueDoesNotBlockTransientEvents(t *testing.T) {
	m := newTestManager(t)
	if err := m.Update(func(s *Status) { s.Queue = 3 }); err != nil { // -> resting mood "few"
		t.Fatal(err)
	}
	if err := m.SetResting(); err != nil {
		t.Fatal(err)
	}
	if got := m.Snapshot().Mood; got != "few" {
		t.Fatalf("setup: resting mood with queue=3 = %q, want few", got)
	}

	ttl := 30 * time.Millisecond
	if err := m.React("reading", "reading...", ttl); err != nil {
		t.Fatal(err)
	}
	if got := m.Snapshot().Mood; got != "reading" {
		t.Fatalf("React(\"reading\") with queue=3 (nonzero): mood = %q, want reading (queue must NOT block it)", got)
	}

	// After ttl, it decays back to the CURRENT queue-derived resting mood
	// ("few"), not to "idle" — confirms "queue > transient" as a revert
	// target, not a block.
	time.Sleep(ttl + 50*time.Millisecond)
	if got := m.Snapshot().Mood; got != "few" {
		t.Errorf("mood after ttl expired = %q, want few (the queue-derived resting mood)", got)
	}
}

// TestPausedSurvivesStaleRevert covers the OTHER half of the precedence
// guarantee: a transient event's revert-after-ttl goroutine, scheduled
// BEFORE a system mood was set, must not stomp that system mood once it
// fires late. This works via reactGen (UpdateMood/SetMood bump it too),
// not the tier guard itself — this test pins that mechanism specifically
// for "paused", the newest system mood.
func TestPausedSurvivesStaleRevert(t *testing.T) {
	m := newTestManager(t)
	ttl := 20 * time.Millisecond
	if err := m.React("reading", "reading...", ttl); err != nil {
		t.Fatal(err)
	}
	// Immediately supersede with a system mood BEFORE the revert fires.
	if err := m.SetMood("paused"); err != nil {
		t.Fatal(err)
	}
	// Wait past the original ttl -- the stale revert goroutine wakes here.
	time.Sleep(ttl + 60*time.Millisecond)
	if got := m.Snapshot().Mood; got != "paused" {
		t.Errorf("mood after a stale revert fired late = %q, want paused (must not have been stomped)", got)
	}
}

// TestSetMutedForcesSystemTierAndReverts covers SetMuted's own contract:
// it IS the tier-3 authority (never gated, unlike React/SetResting), and
// unmuting reverts to whatever the current queue-derived resting mood is.
func TestSetMutedForcesSystemTierAndReverts(t *testing.T) {
	m := newTestManager(t)
	if err := m.Update(func(s *Status) { s.Queue = 0 }); err != nil {
		t.Fatal(err)
	}

	if err := m.SetMuted(true); err != nil {
		t.Fatal(err)
	}
	snap := m.Snapshot()
	if !snap.Muted || snap.Mood != "muted" {
		t.Fatalf("after SetMuted(true): Muted=%v Mood=%q, want true/muted", snap.Muted, snap.Mood)
	}
	// A transient event must not override it while muted (tier-3 guard).
	if err := m.React("reading", "reading...", time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := m.Snapshot().Mood; got != "muted" {
		t.Errorf("React while muted: mood = %q, want still muted", got)
	}

	if err := m.SetMuted(false); err != nil {
		t.Fatal(err)
	}
	snap = m.Snapshot()
	if snap.Muted || snap.Mood != "zero" { // queue=0 -> resting mood "zero"
		t.Errorf("after SetMuted(false) with queue=0: Muted=%v Mood=%q, want false/zero", snap.Muted, snap.Mood)
	}
}

// TestReadBatteryFile covers the D3 single-writer fix's read side: a
// missing or stale sidecar reads as "no reading" (the core must show no
// battery at all, never a placeholder); a fresh one reads through cleanly.
func TestReadBatteryFile(t *testing.T) {
	dir := t.TempDir()
	maxAge := 2 * time.Minute

	t.Run("missing file", func(t *testing.T) {
		_, ok := ReadBatteryFile(filepath.Join(dir, "does-not-exist.json"), maxAge)
		if ok {
			t.Error("missing battery.json: want ok=false")
		}
	})

	t.Run("fresh reading", func(t *testing.T) {
		path := filepath.Join(dir, "fresh.json")
		writeBatteryFileForTest(t, path, 61, time.Now().Unix())
		r, ok := ReadBatteryFile(path, maxAge)
		if !ok || r.Battery != 61 {
			t.Errorf("fresh reading: battery=%d ok=%v, want 61/true", r.Battery, ok)
		}
	})

	t.Run("voltage charging time_remaining round-trip", func(t *testing.T) {
		// (milestones C/D): the extra fields merge in
		// too, and a null voltage_mv/time_remaining_min must stay nil, not
		// decode to a fabricated zero.
		path := filepath.Join(dir, "extras.json")
		mv, tr := 3900, 42
		data, err := json.Marshal(batteryFile{
			Battery: 55, VoltageMV: &mv, Charging: true, TimeRemainingMin: &tr,
			TS: time.Now().Unix(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		r, ok := ReadBatteryFile(path, maxAge)
		if !ok || r.VoltageMV == nil || *r.VoltageMV != 3900 || !r.Charging || r.TimeRemainingMin == nil || *r.TimeRemainingMin != 42 {
			t.Errorf("extras reading = %+v ok=%v, want battery=55 voltage=3900 charging=true time_remaining=42", r, ok)
		}

		pathNull := filepath.Join(dir, "extras_null.json")
		dataNull, err := json.Marshal(batteryFile{Battery: 10, TS: time.Now().Unix()})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pathNull, dataNull, 0o644); err != nil {
			t.Fatal(err)
		}
		rNull, ok := ReadBatteryFile(pathNull, maxAge)
		if !ok || rNull.VoltageMV != nil || rNull.TimeRemainingMin != nil {
			t.Errorf("missing voltage/time_remaining must stay nil, got %+v", rNull)
		}
	})

	t.Run("stale reading", func(t *testing.T) {
		path := filepath.Join(dir, "stale.json")
		writeBatteryFileForTest(t, path, 61, time.Now().Add(-time.Hour).Unix())
		_, ok := ReadBatteryFile(path, maxAge)
		if ok {
			t.Error("stale (1h old, maxAge=2m) reading: want ok=false, not a placeholder")
		}
	})

	t.Run("corrupt file", func(t *testing.T) {
		path := filepath.Join(dir, "corrupt.json")
		if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, ok := ReadBatteryFile(path, maxAge)
		if ok {
			t.Error("corrupt battery.json: want ok=false, not a crash or a fabricated value")
		}
	})
}

func writeBatteryFileForTest(t *testing.T, path string, battery int, ts int64) {
	t.Helper()
	data, err := json.Marshal(batteryFile{Battery: battery, TS: ts})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestReadFaceFile covers the same single-writer/no-placeholder discipline
// as TestReadBatteryFile, applied to face.json: a
// missing/stale/corrupt sidecar reads as "no face" (nil), never a
// fabricated string; a fresh reading (including the legitimate null face
// for mood "qr") round-trips cleanly.
func TestReadFaceFile(t *testing.T) {
	dir := t.TempDir()
	maxAge := 2 * time.Minute

	t.Run("missing file", func(t *testing.T) {
		_, ok := ReadFaceFile(filepath.Join(dir, "does-not-exist.json"), maxAge)
		if ok {
			t.Error("missing face.json: want ok=false")
		}
	})

	t.Run("fresh reading", func(t *testing.T) {
		path := filepath.Join(dir, "fresh.json")
		face := "(◕‿◕)"
		data, err := json.Marshal(faceFile{Face: &face, TS: time.Now().Unix()})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		f, ok := ReadFaceFile(path, maxAge)
		if !ok || f == nil || *f != face {
			t.Errorf("fresh reading: face=%v ok=%v, want %q/true", f, ok, face)
		}
	})

	t.Run("null face (qr mood) is a legitimate value, not a failure", func(t *testing.T) {
		path := filepath.Join(dir, "qr.json")
		data, err := json.Marshal(faceFile{Face: nil, TS: time.Now().Unix()})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		f, ok := ReadFaceFile(path, maxAge)
		if !ok || f != nil {
			t.Errorf("null face: got face=%v ok=%v, want nil/true (fresh but no face for qr mood)", f, ok)
		}
	})

	t.Run("stale reading", func(t *testing.T) {
		path := filepath.Join(dir, "stale.json")
		face := "(-_-)"
		data, err := json.Marshal(faceFile{Face: &face, TS: time.Now().Add(-time.Hour).Unix()})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		_, ok := ReadFaceFile(path, maxAge)
		if ok {
			t.Error("stale (1h old, maxAge=2m) reading: want ok=false, not a placeholder")
		}
	})

	t.Run("corrupt file", func(t *testing.T) {
		path := filepath.Join(dir, "corrupt.json")
		if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, ok := ReadFaceFile(path, maxAge)
		if ok {
			t.Error("corrupt face.json: want ok=false, not a crash or a fabricated value")
		}
	})
}

// TestSentPopulatedViaUpdate covers the mechanism main.go's boot-time SENT
// seed relies on (store.CountOutboundSince(0) -> sm.Update(...)) — a plain
// Update can set Sent and it round-trips through Snapshot, independent of
// mood/reactGen (Update, unlike UpdateMood, must not disturb an in-flight
// React).
func TestSentPopulatedViaUpdate(t *testing.T) {
	m := newTestManager(t)
	if err := m.React("reading", "reading...", time.Hour); err != nil { // long-lived, should survive
		t.Fatal(err)
	}
	if err := m.Update(func(s *Status) { s.Sent = 42 }); err != nil {
		t.Fatal(err)
	}
	snap := m.Snapshot()
	if snap.Sent != 42 {
		t.Errorf("Sent after Update = %d, want 42", snap.Sent)
	}
	if snap.Mood != "reading" {
		t.Errorf("Mood after a plain Update (Sent only) = %q, want unchanged (reading) — Update must not touch mood", snap.Mood)
	}
}
