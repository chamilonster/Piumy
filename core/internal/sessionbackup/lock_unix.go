// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
//go:build !windows

package sessionbackup

import (
	"os"
	"syscall"
)

// processAlive reports whether pid is a currently running process, using
// the standard Unix idiom: signal 0 checks existence/permission without
// actually delivering a signal to the process.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
