// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
//go:build !windows

package sessionbackup

import (
	"os"
	"syscall"
)

// sameVolume reports whether a and b live on the same filesystem, via the
// device ID stat exposes on Unix. ok=false means the check itself failed
// (missing path, unsupported Stat_t) — callers must not warn in that case,
// only on a confirmed match.
func sameVolume(a, b string) (same, ok bool) {
	statA, err := os.Stat(a)
	if err != nil {
		return false, false
	}
	statB, err := os.Stat(b)
	if err != nil {
		return false, false
	}
	sysA, okA := statA.Sys().(*syscall.Stat_t)
	sysB, okB := statB.Sys().(*syscall.Stat_t)
	if !okA || !okB {
		return false, false
	}
	return sysA.Dev == sysB.Dev, true
}
