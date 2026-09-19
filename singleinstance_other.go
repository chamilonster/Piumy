//go:build !windows

package main

// acquireSingleInstance is a no-op off Windows, same reasoning as
// acquireAppMutex (appmutex_other.go) — T59's launch surfaces (tray,
// installer) are Windows-only for now.
func acquireSingleInstance() bool { return true }
