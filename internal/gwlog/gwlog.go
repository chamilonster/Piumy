// Package gwlog redirects the standard log package's output to a
// size-rotated file next to the gateway's own data (piumy.db/router.json/
// status.json — see internal/agentconnect's DataDir). The binary ships
// -H=windowsgui (no console) and the installer launches it directly, so
// without this every log.Printf line simply evaporates — capipush already
// logs exactly what a dispatch failure needs ("sin antena", "canal caído",
// "en backoff", "hit the redispatch cap"), nothing was missing except a
// place for those lines to land (T53, ct-2026-08-10-1849).
package gwlog

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

const (
	fileName = "piumy.log"

	// maxSize/maxBackups: no PIUMY_* env var for these — nobody asked for a
	// tunable size, and text log lines are cheap (4 files x 5MB caps this at
	// ~20MB on a user's disk, plenty for the startup+sweep lines this logs).
	maxSize    = 5 * 1024 * 1024
	maxBackups = 3
)

// Setup opens dir/piumy.log (creating dir if needed) and makes it the
// standard log package's output, appending across restarts and rotating by
// size. Call it once, as early in main() as cfg.StatusPath (dir's usual
// source) is available — every log.Printf after this call lands in the
// file; nothing before it does.
func Setup(dir string) error {
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	w, err := newRotatingWriter(filepath.Join(dir, fileName))
	if err != nil {
		return err
	}
	log.SetOutput(w)
	return nil
}

// rotatingWriter is an io.Writer over an append-mode file that rotates
// itself once a write would cross maxSize: the current file becomes .1
// (older backups shift down, anything past maxBackups is dropped) and a
// fresh file opens in its place. No mutex: its only caller is the standard
// log package, whose Logger.Output already holds its own lock around the
// entire write — a second one here would just be redundant.
type rotatingWriter struct {
	path string
	f    *os.File
	size int64
}

func newRotatingWriter(path string) (*rotatingWriter, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &rotatingWriter{path: path, f: f, size: info.Size()}, nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	if w.size > 0 && w.size+int64(len(p)) > maxSize {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate closes the current file, shifts .1..maxBackups-1 down one slot
// (dropping whatever sits at maxBackups), moves the current file to .1, and
// opens a fresh one at the original path. Best-effort on the shuffle itself
// (a missing .N file is the common case, not an error worth surfacing) —
// logging must never be the reason the gateway crashes.
func (w *rotatingWriter) rotate() error {
	if err := w.f.Close(); err != nil {
		return err
	}
	os.Remove(fmt.Sprintf("%s.%d", w.path, maxBackups))
	for i := maxBackups - 1; i >= 1; i-- {
		os.Rename(fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1))
	}
	os.Rename(w.path, w.path+".1")

	f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	w.f = f
	w.size = 0
	return nil
}
