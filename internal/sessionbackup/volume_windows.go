//go:build windows

package sessionbackup

// sameVolume is unsupported on Windows (not this project's deployment
// target) — always ok=false so WarnIfSameVolume silently skips rather than
// guessing.
func sameVolume(a, b string) (same, ok bool) {
	return false, false
}
