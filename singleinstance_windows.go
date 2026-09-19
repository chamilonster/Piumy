//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// singleInstanceMutexName is deliberately its OWN mutex, separate from
// appMutexName (appmutex_windows.go) — T59 (ct-2026-08-10-2116). That one is
// wired 1:1 to Inno's [Setup] AppMutex and stays best-effort/never-blocking
// on purpose, for the installer's benefit. This one has the opposite job: it
// MUST be authoritative, because it's the only thing standing between two
// live processes fighting over the same WhatsApp session (whatsmeow.db).
// Reusing one mutex for both would tangle an installer-detection concern
// with a runtime-enforcement one — a single failure mode change to either
// risks silently breaking the other.
const singleInstanceMutexName = "PiumyGatewayRuntimeInstanceMutex"

// acquireSingleInstance reports whether THIS process is the one holding
// piumy-gateway's runtime single-instance mutex. A named Windows mutex is
// the right primitive here specifically because of what happens when a
// process dies: the OS kernel releases every handle it held — including this
// one — the instant the process is gone, however it went (clean exit,
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
func acquireSingleInstance() bool {
	namePtr, err := syscall.UTF16PtrFromString(singleInstanceMutexName)
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
