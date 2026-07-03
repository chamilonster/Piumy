// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
package sessionbackup

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// MarkServing writes a lock file at sessionDBPath+".lock" containing the
// current process's PID — restore-session checks this before touching the
// live session DB ("the gateway is stopped" must be actually
// enforced, not assumed from "restore runs as a different OS process" —
// two processes can both be alive at once and corrupt the DB together).
// Call once at server startup.
func MarkServing(sessionDBPath string) error {
	pid := os.Getpid()
	if err := os.WriteFile(lockPath(sessionDBPath), []byte(strconv.Itoa(pid)), 0o600); err != nil {
		return fmt.Errorf("sessionbackup: write lock file: %w", err)
	}
	return nil
}

// UnmarkServing removes the lock file — call on a clean shutdown. A missing
// file is not an error (nothing to clean up, e.g. backups were never
// enabled or this is a fresh install).
func UnmarkServing(sessionDBPath string) error {
	err := os.Remove(lockPath(sessionDBPath))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("sessionbackup: remove lock file: %w", err)
	}
	return nil
}

func lockPath(sessionDBPath string) string {
	return sessionDBPath + ".lock"
}

// CheckNotServing refuses (returns a non-nil error) if the session DB looks
// like it's still in use by a running `pimywa serve` process. force=true
// skips the check entirely — a deliberate human override for a lock the
// operator is SURE is stale (e.g. this platform's liveness check can't
// confirm it, see processAlive). Fail-safe direction: when uncertain,
// refuse rather than risk two processes touching the session DB at once.
func CheckNotServing(sessionDBPath string, force bool) error {
	if force {
		return nil
	}
	data, err := os.ReadFile(lockPath(sessionDBPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no lock recorded — nothing to refuse on
		}
		return fmt.Errorf("sessionbackup: read lock file: %w", err)
	}
	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return fmt.Errorf("sessionbackup: lock file %s exists but its contents are unreadable — stop the service first (or pass --force if you're certain it's stale)", lockPath(sessionDBPath))
	}
	if !processAlive(pid) {
		return nil // stale lock left by an unclean shutdown (3rd commandment) — proceed
	}
	return fmt.Errorf("sessionbackup: pimywa serve appears to be running (pid %d) — stop it first, then retry (or pass --force if you're certain this is a stale lock)", pid)
}
