package gwlog

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestRotationForcedBySize does exactly what the contract asked for:
// forces the size, doesn't reason about the code. Writes past maxSize twice
// and checks the backups this rotation actually produces on disk.
func TestRotationForcedBySize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, fileName)
	w, err := newRotatingWriter(path)
	if err != nil {
		t.Fatalf("newRotatingWriter: %v", err)
	}
	// Windows won't let TempDir's cleanup remove an open file.
	t.Cleanup(func() { w.f.Close() })

	line := bytes.Repeat([]byte("x"), 1024)
	line = append(line, '\n')
	linesPerRotation := maxSize/len(line) + 1

	write := func() {
		for i := 0; i < linesPerRotation; i++ {
			if _, err := w.Write(line); err != nil {
				t.Fatalf("Write: %v", err)
			}
		}
	}

	write() // crosses maxSize once -> path.1 appears
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected %s.1 after crossing maxSize, got: %v", path, err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() > int64(maxSize) {
		t.Fatalf("current file should have rotated to near-empty, stat=%v err=%v", info, err)
	}

	write() // crosses again -> path.1 shifts to path.2, new path.1 appears
	if _, err := os.Stat(path + ".2"); err != nil {
		t.Fatalf("expected %s.2 after a second rotation, got: %v", path, err)
	}

	for i := 0; i < maxBackups+2; i++ {
		write()
	}
	if _, err := os.Stat(path + "." + strconv.Itoa(maxBackups+1)); !os.IsNotExist(err) {
		t.Fatalf("backups must be capped at %d, found .%d", maxBackups, maxBackups+1)
	}
}

// TestSetupAppendsAcrossRestarts covers the DoD line "reiniciar no borra el
// historial anterior": two Setup calls against the same dir (simulating two
// process starts) must both land in the same growing file, not overwrite it.
func TestSetupAppendsAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	closeCurrent := func() {
		if w, ok := log.Writer().(*rotatingWriter); ok {
			w.f.Close()
		}
	}

	if err := Setup(dir); err != nil {
		t.Fatalf("Setup (1st start): %v", err)
	}
	log.Print("primera corrida")
	closeCurrent() // simulates the process exiting between starts

	if err := Setup(dir); err != nil {
		t.Fatalf("Setup (2nd start): %v", err)
	}
	log.Print("segunda corrida")
	t.Cleanup(closeCurrent)

	raw, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(raw)
	if !strings.Contains(got, "primera corrida") || !strings.Contains(got, "segunda corrida") {
		t.Fatalf("expected both runs in the log, got: %q", got)
	}
}
