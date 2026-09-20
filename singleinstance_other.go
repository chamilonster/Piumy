//go:build !windows

package main

// acquireSingleInstance is a no-op off Windows, same reasoning as
// acquireAppMutex (appmutex_other.go) — T59's launch surfaces (tray,
// installer) are Windows-only for now. dataDir kept in the signature (S1,
// ct-2026-09-20-1100) only so both build tags match — unused here.
func acquireSingleInstance(dataDir string) bool { return true }
