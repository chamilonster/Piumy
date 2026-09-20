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
	got, err := dataDirFor("windows", `C:\Users\boss\AppData\Local`, `C:\Users\boss`, "")
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
	got, err := dataDirFor("windows", "", `C:\Users\boss`, "")
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
	got, err := dataDirFor("darwin", "", "/Users/boss", "")
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
	got, err := dataDirFor("linux", "", "/home/boss", "")
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
	if _, err := dataDirFor("linux", "", "", ""); err == nil {
		t.Error("dataDirFor with no home and no LOCALAPPDATA: want an error, got nil")
	}
}

// TestDataDirForAccountAddsAccountsSubfolder is S1's own case
// (ct-2026-09-20-1100): PIUMY_ACCOUNT set gets its own accounts/<name>
// subfolder under the SAME OS root dataDirFor already resolves — two
// accounts on one machine end up with two completely separate data trees.
func TestDataDirForAccountAddsAccountsSubfolder(t *testing.T) {
	got, err := dataDirFor("linux", "", "/home/boss", "trabajo")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/home/boss", ".local", "share", "piumy", "accounts", "trabajo")
	if got != want {
		t.Errorf("dataDirFor(account=trabajo) = %q, want %q", got, want)
	}
}

// TestDataDirForHostileAccountFailsLoud covers the contract's explicit
// list: a value that collapses to empty (whitespace-only), "..", any path
// separator, or a colon must fail at startup with a clear error — never
// silently collapse to a path outside accounts/, and never a silent no-op.
// A literal empty string is NOT in this list — os.Getenv can't tell "unset"
// from "set to empty" apart, so dataDirFor treats account=="" as "no
// account" (today's single-root behavior), not a hostile value.
func TestDataDirForHostileAccountFailsLoud(t *testing.T) {
	for _, account := range []string{"   ", "..", "../escape", "a/b", `a\b`, "a:b"} {
		if _, err := dataDirFor("linux", "", "/home/boss", account); err == nil {
			t.Errorf("dataDirFor(account=%q): want an error, got nil", account)
		}
	}
}

// TestDataDirForEmptyAccountIsNotAnError: an empty account string resolves
// to the plain OS root, same as PIUMY_ACCOUNT never being set at all — not
// a startup error.
func TestDataDirForEmptyAccountIsNotAnError(t *testing.T) {
	got, err := dataDirFor("linux", "", "/home/boss", "")
	if err != nil {
		t.Fatalf("dataDirFor(account=\"\"): want no error, got %v", err)
	}
	want := filepath.Join("/home/boss", ".local", "share", "piumy")
	if got != want {
		t.Errorf("dataDirFor(account=\"\") = %q, want %q", got, want)
	}
}

// TestDataDirPIUMYDataDirWinsEvenWithAccountSet: PIUMY_DATA_DIR is still
// the unconditional override (T169) — PIUMY_ACCOUNT never gets a chance to
// append accounts/<slug> on top of it.
func TestDataDirPIUMYDataDirWinsEvenWithAccountSet(t *testing.T) {
	dir := filepath.Join("some", "custom", "place")
	t.Setenv("PIUMY_DATA_DIR", dir)
	t.Setenv("PIUMY_ACCOUNT", "trabajo")

	got, err := DataDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Errorf("DataDir() = %q, want %q verbatim (PIUMY_DATA_DIR must win over PIUMY_ACCOUNT too)", got, dir)
	}
}
