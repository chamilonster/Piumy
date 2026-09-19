//go:build windows

package sessionbackup

import "os"

// processAlive on Windows: os.FindProcess actually opens a handle to the
// process and fails if the PID doesn't exist (unlike Unix, where
// FindProcess never fails) — a reasonable liveness proxy. Windows isn't
// this project's deployment target (the Pi is Linux); this exists mainly
// so local dev/tests on a Windows machine behave sanely too.
func processAlive(pid int) bool {
	_, err := os.FindProcess(pid)
	return err == nil
}
