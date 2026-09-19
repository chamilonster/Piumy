package main

import (
	"path/filepath"
	"testing"
	"time"

	"piumy-gateway/internal/governor"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
)

func TestTodayStartLocal(t *testing.T) {
	now := time.Date(2026, 7, 10, 15, 30, 45, 0, time.Local)
	got := todayStartLocal(now)
	want := time.Date(2026, 7, 10, 0, 0, 0, 0, time.Local).Unix()
	if got != want {
		t.Errorf("todayStartLocal(%v) = %d, want %d (local midnight)", now, got, want)
	}
}

// TestTodayStartLocalJustBeforeMidnight guards the actual rollover
// boundary governor.Limiter cares about: 23:59:59 must still resolve to
// TODAY's midnight, not tomorrow's.
func TestTodayStartLocalJustBeforeMidnight(t *testing.T) {
	now := time.Date(2026, 7, 10, 23, 59, 59, 0, time.Local)
	got := todayStartLocal(now)
	want := time.Date(2026, 7, 10, 0, 0, 0, 0, time.Local).Unix()
	if got != want {
		t.Errorf("todayStartLocal(%v) = %d, want %d", now, got, want)
	}
}

// TestResolveDefaultTerminalID is T25 (hallazgo 2, ct-2026-08-05-1833):
// PIUMY_DEFAULT_TERMINAL_ID empty must fall back to the already-wired
// principal antenna's terminal, not leave the owner's messages with
// nowhere to go — but the env var, when set, must never be overridden by
// the antenna.
func TestResolveDefaultTerminalID(t *testing.T) {
	cases := []struct {
		name, env, antenna, want string
	}{
		{"env gana si está puesto", "env-term", "antenna-term", "env-term"},
		{"antena de respaldo si el env está vacío", "", "antenna-term", "antenna-term"},
		{"ninguna de las dos — vacío, sin inventar nada", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveDefaultTerminalID(c.env, c.antenna); got != c.want {
				t.Errorf("resolveDefaultTerminalID(%q, %q) = %q, want %q", c.env, c.antenna, got, c.want)
			}
		})
	}
}

func newRestoreKillSwitchDeps(t *testing.T) (*store.Store, *governor.Limiter, *state.Manager) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	gov := governor.NewLimiter(10, time.Minute)
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
	return s, gov, sm
}

// TestRestoreKillSwitchNoOpWhenNeverSet: a fresh install (or a normal
// restart with the brake never touched) must not restore anything —
// restoreKillSwitch only ever REINSTATES a persisted true, never invents one.
func TestRestoreKillSwitchNoOpWhenNeverSet(t *testing.T) {
	s, gov, sm := newRestoreKillSwitchDeps(t)

	restored, err := restoreKillSwitch(s, gov, sm)
	if err != nil {
		t.Fatal(err)
	}
	if restored {
		t.Error("restored = true with nothing persisted, want false")
	}
	if gov.Killed() {
		t.Error("gov.Killed() = true, want false")
	}
	if sm.Snapshot().Muted {
		t.Error("sm.Snapshot().Muted = true, want false")
	}
}

// TestRestoreKillSwitchExplicitFalseIsNoOp: an explicit false persisted
// (the owner turned it back off before the restart) must behave exactly
// like never-set — same code path (store.SettingBool folds both to the
// same default), verified explicitly so a future change to that fold
// can't silently start restoring a brake nobody asked for.
func TestRestoreKillSwitchExplicitFalseIsNoOp(t *testing.T) {
	s, gov, sm := newRestoreKillSwitchDeps(t)
	if err := s.SetSettingBool(store.SettingKillSwitch, false); err != nil {
		t.Fatal(err)
	}

	restored, err := restoreKillSwitch(s, gov, sm)
	if err != nil {
		t.Fatal(err)
	}
	if restored {
		t.Error("restored = true with an explicit false persisted, want false")
	}
	if gov.Killed() || sm.Snapshot().Muted {
		t.Errorf("gov.Killed()=%v sm.Muted=%v, want both false", gov.Killed(), sm.Snapshot().Muted)
	}
}

// TestRestoreKillSwitchAppliesBothFlagsTogether is T19's core guarantee
// (ct-2026-08-05-1249): a persisted true restores BOTH halves the real
// set_kill_switch tool always sets together (governor.SetKill AND
// state.SetMuted) — restoring only one would silently diverge from what
// was actually on before the restart, and corepipeline.killSwitchActive
// checks either flag being enough to keep sending (S4b's own OR gate).
func TestRestoreKillSwitchAppliesBothFlagsTogether(t *testing.T) {
	s, gov, sm := newRestoreKillSwitchDeps(t)
	if err := s.SetSettingBool(store.SettingKillSwitch, true); err != nil {
		t.Fatal(err)
	}

	restored, err := restoreKillSwitch(s, gov, sm)
	if err != nil {
		t.Fatal(err)
	}
	if !restored {
		t.Fatal("restored = false with true persisted, want true")
	}
	if !gov.Killed() {
		t.Error("gov.Killed() = false after restoring a persisted kill, want true")
	}
	if !sm.Snapshot().Muted {
		t.Error("sm.Snapshot().Muted = false after restoring a persisted kill, want true")
	}
}
