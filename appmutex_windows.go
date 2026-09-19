//go:build windows

package main

import (
	"log"
	"syscall"
	"unsafe"
)

// appMutexName must match [Setup]'s AppMutex in installer/windows/piumy.iss
// verbatim — T21 (ct-2026-08-05-1308, secondary finding of Amatista's R2):
// reinstalling over a running Piumy hits Windows' "file in use" dialog for
// Piumy.exe; choosing "Ignorar" there leaves the OLD executable running
// paired with the NEW launcher/keys. Inno's AppMutex only warns the user if
// a mutex with this exact name currently exists — nothing else creates or
// reads it.
const appMutexName = "PiumyGatewaySingleInstanceMutex"

var (
	kernel32     = syscall.NewLazyDLL("kernel32.dll")
	createMutexW = kernel32.NewProc("CreateMutexW")
)

// acquireAppMutex creates the named mutex Setup/Uninstall check for, held
// for the process's entire lifetime — the OS reclaims it on exit or crash,
// same as any other process-scoped kernel handle, so there's nothing to
// release explicitly. Best-effort only: a failure here never stops Piumy
// from running, it only means the installer loses its running-instance
// warning.
func acquireAppMutex() {
	namePtr, err := syscall.UTF16PtrFromString(appMutexName)
	if err != nil {
		log.Printf("appmutex: %v", err)
		return
	}
	if handle, _, _ := createMutexW.Call(0, 0, uintptr(unsafe.Pointer(namePtr))); handle == 0 {
		log.Printf("appmutex: CreateMutexW failed")
	}
}
