//go:build !windows

package main

// acquireAppMutex is a no-op off Windows — the installer/uninstaller
// running-instance check (T21, ct-2026-08-05-1308) is Windows-only.
func acquireAppMutex() {}
