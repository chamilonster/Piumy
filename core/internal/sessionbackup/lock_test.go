// SPDX-License-Identifier: AGPL-3.0-only
package sessionbackup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// TestCheckNotServingNoLockFile covers the default case: nothing recorded
// as serving, restore proceeds.
func TestCheckNotServingNoLockFile(t *testing.T) {
	dir := t.TempDir()
	sessionDB := filepath.Join(dir, "wa.db")
	if err := CheckNotServing(sessionDB, false); err != nil {
		t.Errorf("no lock file: got err %v, want nil", err)
	}
}

// TestCheckNotServingLiveProcessRefuses covers the DoD directly (a
// security correction): a lock file naming a genuinely alive PID (our own,
// via MarkServing) must refuse.
func TestCheckNotServingLiveProcessRefuses(t *testing.T) {
	dir := t.TempDir()
	sessionDB := filepath.Join(dir, "wa.db")
	if err := MarkServing(sessionDB); err != nil {
		t.Fatal(err)
	}
	if err := CheckNotServing(sessionDB, false); err == nil {
		t.Error("live process lock: got nil error, want a refusal")
	}
}

// TestCheckNotServingStaleLockProceeds covers the 3rd commandment: a lock
// file left behind by an unclean shutdown (process no longer alive) must
// NOT permanently brick restores — it proceeds without needing --force.
func TestCheckNotServingStaleLockProceeds(t *testing.T) {
	dir := t.TempDir()
	sessionDB := filepath.Join(dir, "wa.db")

	// A real subprocess that exits immediately — once Run() returns, its
	// PID is guaranteed dead (barring PID reuse in the tiny window, an
	// inherent limitation of PID-based liveness checks in general).
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcessNoop")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	if err := cmd.Run(); err != nil {
		t.Fatalf("helper process: %v", err)
	}
	deadPID := cmd.Process.Pid

	if err := os.WriteFile(lockPath(sessionDB), []byte(strconv.Itoa(deadPID)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckNotServing(sessionDB, false); err != nil {
		t.Errorf("stale lock: got err %v, want nil (a crash must not permanently brick restores)", err)
	}
}

// TestCheckNotServingForceOverridesLiveLock covers the escape hatch: an
// operator certain a lock is stale (but whose platform can't confirm it)
// can override with force=true.
func TestCheckNotServingForceOverridesLiveLock(t *testing.T) {
	dir := t.TempDir()
	sessionDB := filepath.Join(dir, "wa.db")
	if err := MarkServing(sessionDB); err != nil {
		t.Fatal(err)
	}
	if err := CheckNotServing(sessionDB, true); err != nil {
		t.Errorf("force=true: got err %v, want nil", err)
	}
}

// TestMarkUnmarkServing covers the round trip: mark creates the lock file,
// unmark removes it, and unmarking an already-removed lock is not an error.
func TestMarkUnmarkServing(t *testing.T) {
	dir := t.TempDir()
	sessionDB := filepath.Join(dir, "wa.db")

	if err := MarkServing(sessionDB); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath(sessionDB)); err != nil {
		t.Fatalf("lock file not created: %v", err)
	}

	if err := UnmarkServing(sessionDB); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath(sessionDB)); !os.IsNotExist(err) {
		t.Fatalf("lock file still exists after UnmarkServing: err=%v", err)
	}

	if err := UnmarkServing(sessionDB); err != nil {
		t.Errorf("UnmarkServing on an already-removed lock: %v, want nil", err)
	}
}

// TestHelperProcessNoop is spawned as a real subprocess by
// TestCheckNotServingStaleLockProceeds purely to obtain a PID that's
// guaranteed dead once the process exits. Standard Go testing idiom for
// getting a controllable subprocess.
func TestHelperProcessNoop(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	os.Exit(0)
}
