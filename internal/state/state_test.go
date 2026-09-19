package state

import (
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

// TestSystemTierGuardsReact covers moodTier's core precedence guarantee:
// while a tier-3 system mood (error/qr/sleeping/muted/alert) is
// active, React() must never override it — a transient event notification
// must not mask "the connection is down" or "muted". "alert" (T98,
// ct-2026-08-29-0621) is StreamReplaced's own signal.
func TestSystemTierGuardsReact(t *testing.T) {
	for _, systemMood := range []string{"error", "qr", "sleeping", "muted", "alert"} {
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
// SetResting() — a queue-depth refresh must not override a system mood either.
func TestSystemTierGuardsSetResting(t *testing.T) {
	for _, systemMood := range []string{"error", "qr", "sleeping", "muted", "alert"} {
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

// TestQueueDoesNotBlockTransientEvents covers the precedence read: "queue
// moods > transient events" describes what a transient event reverts TO,
// not something that blocks it from showing in the first place.
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

// TestSystemMoodSurvivesStaleRevert covers the OTHER half of the precedence
// guarantee: a transient event's revert-after-ttl goroutine, scheduled
// BEFORE a system mood was set, must not stomp that system mood once it
// fires late. Uses "error" as the tier-3 mood under test — was "paused"
// until T106 (ct-2026-08-29-2234) removed that mood entirely (T99 made
// reconnection never give up, so it stopped having a case where it could
// occur); this test was never ABOUT "paused" specifically, any tier-3 mood
// exercises the same mechanism.
func TestSystemMoodSurvivesStaleRevert(t *testing.T) {
	m := newTestManager(t)
	ttl := 20 * time.Millisecond
	if err := m.React("reading", "reading...", ttl); err != nil {
		t.Fatal(err)
	}
	// Immediately supersede with a system mood BEFORE the revert fires.
	if err := m.SetMood("error"); err != nil {
		t.Fatal(err)
	}
	// Wait past the original ttl -- the stale revert goroutine wakes here.
	time.Sleep(ttl + 60*time.Millisecond)
	if got := m.Snapshot().Mood; got != "error" {
		t.Errorf("mood after a stale revert fired late = %q, want error (must not have been stomped)", got)
	}
}

// TestSetMutedForcesSystemTierAndReverts covers SetMuted's own contract: it
// IS the tier-3 authority (never gated, unlike React/SetResting), and
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

// TestSentPopulatedViaUpdate covers the mechanism a boot-time SENT seed
// relies on (store.CountOutboundSince(0) -> sm.Update(...)) — a plain Update
// can set Sent and it round-trips through Snapshot, independent of
// mood/reactGen (Update, unlike UpdateMood, must not disturb an in-flight React).
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

// TestNewManagerSeedsOwnIdentityFromExistingFile is T17's Part 1 fix
// (ct-2026-08-05-1240): a restart must not start OwnName/OwnJID blank when
// a previous run's status.json already has them — otherwise the header
// shows nothing for the entire window until the next successful reconnect
// (which, per recordOwnIdentity's own T17 fix, isn't even guaranteed to
// ever refresh the name again).
func TestNewManagerSeedsOwnIdentityFromExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	if err := Write(path, Status{Mood: "responding", OwnName: "Bigot Mckuco, Clever.cat", OwnJID: "55500000042@s.whatsapp.net", WAConnected: true}); err != nil {
		t.Fatal(err)
	}

	m := NewManager(path, 8)
	snap := m.Snapshot()

	if snap.OwnName != "Bigot Mckuco, Clever.cat" {
		t.Errorf("OwnName = %q, want the seeded name from the existing file", snap.OwnName)
	}
	if snap.OwnJID != "55500000042@s.whatsapp.net" {
		t.Errorf("OwnJID = %q, want the seeded jid from the existing file", snap.OwnJID)
	}
	// Deliberately narrow (state.go's own doc on NewManager): everything
	// EXCEPT OwnName/OwnJID must start fresh, not carry over stale
	// live/session state from a crashed previous run.
	if snap.Mood != "idle" {
		t.Errorf("Mood = %q after seeding, want idle (only identity fields are carried over)", snap.Mood)
	}
	if snap.WAConnected {
		t.Error("WAConnected = true after seeding from a stale file, want false — must not claim to be connected before a real reconnect happens")
	}
}

// TestNewManagerMissingFileStartsBlank confirms the fallback path (no
// status.json yet — first run ever) is unchanged: idle mood, blank
// identity, same as before this fix existed.
func TestNewManagerMissingFileStartsBlank(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "never-written.json"), 8)
	snap := m.Snapshot()
	if snap.Mood != "idle" || snap.OwnName != "" || snap.OwnJID != "" {
		t.Errorf("fresh Manager with no existing file = %+v, want Mood=idle and blank OwnName/OwnJID", snap)
	}
}

// TestNewManagerCorruptFileStartsBlank: a hand-edited or truncated
// status.json must never block startup — same fallback as a missing file.
func TestNewManagerCorruptFileStartsBlank(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(path, 8)
	snap := m.Snapshot()
	if snap.Mood != "idle" || snap.OwnName != "" || snap.OwnJID != "" {
		t.Errorf("Manager seeded from a corrupt file = %+v, want the same blank fallback as a missing file", snap)
	}
}
