package config

import (
	"path/filepath"
	"testing"
)

// TestDataDirPIUMYDataDirWinsVerbatim is T169's single escape hatch (a test
// instance's one switch instead of five loose PIUMY_*_PATH vars) — the
// value comes back exactly as set, no OS-guessing, no join.
func TestDataDirPIUMYDataDirWinsVerbatim(t *testing.T) {
	t.Setenv("PIUMY_DATA_DIR", filepath.Join("some", "custom", "place"))
	got, err := DataDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("some", "custom", "place")
	if got != want {
		t.Errorf("DataDir() = %q, want %q verbatim", got, want)
	}
}

// TestDataDirForWindowsPrefersLocalAppData covers the Windows branch of
// dataDirFor directly — %LOCALAPPDATA%\Piumy, not %AppData% (Roaming):
// the boss's real install lives under LOCALAPPDATA, and the two are
// genuinely different Windows folders.
func TestDataDirForWindowsPrefersLocalAppData(t *testing.T) {
	got, err := dataDirFor("windows", `C:\Users\boss\AppData\Local`, `C:\Users\boss`)
	if err != nil {
		t.Fatal(err)
	}
	want := `C:\Users\boss\AppData\Local\Piumy`
	if got != want {
		t.Errorf("dataDirFor(windows) = %q, want %q", got, want)
	}
}

// TestDataDirForWindowsFallsBackToHomeWhenLocalAppDataUnset covers the rare
// case (LOCALAPPDATA missing from the environment) — still resolves,
// derived from home instead of erroring outright.
func TestDataDirForWindowsFallsBackToHomeWhenLocalAppDataUnset(t *testing.T) {
	got, err := dataDirFor("windows", "", `C:\Users\boss`)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(`C:\Users\boss`, "AppData", "Local", "Piumy")
	if got != want {
		t.Errorf("dataDirFor(windows, no LOCALAPPDATA) = %q, want %q", got, want)
	}
}

// TestDataDirForDarwinUsesApplicationSupport — T113's own reach into Mac.
// want is built with filepath.Join, same as dataDirFor itself: on an actual
// darwin build filepath.Join produces "/"-separated paths on its own —
// this test verifies the RIGHT COMPONENTS in the right order, not a
// hardcoded separator that only holds when actually compiled for POSIX
// (this suite runs on Windows too).
func TestDataDirForDarwinUsesApplicationSupport(t *testing.T) {
	got, err := dataDirFor("darwin", "", "/Users/boss")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/Users/boss", "Library", "Application Support", "Piumy")
	if got != want {
		t.Errorf("dataDirFor(darwin) = %q, want %q", got, want)
	}
}

// TestDataDirForLinuxUsesLocalShare — T113's own reach into Linux/Raspberry.
// Same filepath.Join-built want as the Darwin test above, same reason.
func TestDataDirForLinuxUsesLocalShare(t *testing.T) {
	got, err := dataDirFor("linux", "", "/home/boss")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/home/boss", ".local", "share", "piumy")
	if got != want {
		t.Errorf("dataDirFor(linux) = %q, want %q", got, want)
	}
}

// TestDataDirForErrorsWithoutHomeOrLocalAppData: no silent empty-string
// data dir — a real, legible error instead of writing data to "".
func TestDataDirForErrorsWithoutHomeOrLocalAppData(t *testing.T) {
	if _, err := dataDirFor("linux", "", ""); err == nil {
		t.Error("dataDirFor with no home and no LOCALAPPDATA: want an error, got nil")
	}
}
