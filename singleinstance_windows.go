//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

// singleInstanceMutexPrefix is deliberately its OWN mutex family, separate
// from appMutexName (appmutex_windows.go) — T59 (ct-2026-08-10-2116). That
// one is wired 1:1 to Inno's [Setup] AppMutex and stays best-effort/never-
// blocking on purpose, for the installer's benefit. This one has the
// opposite job: it MUST be authoritative, because it's the only thing
// standing between two live processes fighting over the same WhatsApp
// session (whatsmeow.db). Reusing one mutex for both would tangle an
// installer-detection concern with a runtime-enforcement one — a single
// failure mode change to either risks silently breaking the other.
//
// S1 (ct-2026-09-20-1100): no longer a single fixed name — that made the
// lock global to the MACHINE, blocking two live accounts that don't share
// anything. The mutex now scopes to the effective DATA DIRECTORY instead
// (dataDirHash below): two different accounts get two different mutexes and
// both start; two processes pointed at the SAME directory (whatever they're
// each called) still collide on the same mutex, exactly like today.
const singleInstanceMutexPrefix = "PiumyGatewayRuntimeInstanceMutex-"

// dataDirHash turns dataDir into the mutex name's suffix — normalized
// (Clean + lowercased, since Windows paths are case-insensitive: two
// spellings of the SAME directory must still collide on one mutex) and
// hashed rather than embedded raw, because a Windows mutex name can't
// contain "\" and has a length cap a real filesystem path could exceed.
func dataDirHash(dataDir string) string {
	norm := strings.ToLower(filepath.Clean(dataDir))
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:])
}

// acquireSingleInstance reports whether THIS process is the one holding
// piumy-gateway's runtime single-instance mutex for dataDir — the effective
// data directory this run resolved (config.DataDir()), NOT the account
// name: two processes naming the SAME directory differently must still
// collide (S1, ct-2026-09-20-1100). A named Windows mutex is the right
// primitive here specifically because of what happens when a process dies:
// the OS kernel releases every handle it held — including this one — the
// instant the process is gone, however it went (clean exit,
// TerminateProcess/taskkill /F, power cut). There is no file, no PID, no
// timestamp to go stale, so there is no "is this stale?" judgment call to
// get wrong — the exact failure mode the contract calls out as worse than
// the bug it's fixing (a Piumy that died ugly permanently locking out every
// future launch).
//
// Any failure to even ask the question (can't build the name, the API call
// itself errors out) fails OPEN — returns true, exactly like acquireAppMutex
// does — because refusing to start over an unrelated OS hiccup would be the
// same "worse than the problem" outcome, just reached a different way.
func acquireSingleInstance(dataDir string) bool {
	namePtr, err := syscall.UTF16PtrFromString(singleInstanceMutexPrefix + dataDirHash(dataDir))
	if err != nil {
		return true
	}
	// bInitialOwner=1: if we're the ones creating it, take ownership now —
	// nothing here needs the ownership itself (no WaitForSingleObject), only
	// the object's existence for the instant after this call returns.
	handle, _, callErr := createMutexW.Call(0, 1, uintptr(unsafe.Pointer(namePtr)))
	if handle == 0 {
		return true
	}
	if errno, ok := callErr.(syscall.Errno); ok && errno == syscall.ERROR_ALREADY_EXISTS {
		return false
	}
	return true
}
