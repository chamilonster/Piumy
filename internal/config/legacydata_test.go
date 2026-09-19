package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWarnLegacyDataReportsOldFileNotYetMigrated is T169's core scenario
// (ct-2026-09-19-1433): a pre-T169 relative file sits in the CURRENT
// WORKING DIRECTORY, and this run's own (new) resolved location doesn't
// have anything yet — exactly the shape of "about to look like Piumy lost
// everything" that must never pass silently.
func TestWarnLegacyDataReportsOldFileNotYetMigrated(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("piumy.db", []byte("old data"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{DBPath: filepath.Join(t.TempDir(), "secrets", "piumy.db")}

	warnings := WarnLegacyData(cfg)
	if len(warnings) != 1 {
		t.Fatalf("WarnLegacyData = %d warnings, want exactly 1 (piumy.db): %v", len(warnings), warnings)
	}
}

// TestWarnLegacyDataSilentWhenNothingLegacyExists: a clean working
// directory (the normal case for almost everyone) must produce zero noise.
func TestWarnLegacyDataSilentWhenNothingLegacyExists(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := Config{
		DBPath:     filepath.Join(t.TempDir(), "secrets", "piumy.db"),
		WADBPath:   filepath.Join(t.TempDir(), "secrets", "whatsmeow.db"),
		RouterPath: filepath.Join(t.TempDir(), "secrets", "router.json"),
		StatusPath: filepath.Join(t.TempDir(), "secrets", "status.json"),
		MediaDir:   filepath.Join(t.TempDir(), "secrets", "media"),
	}
	if warnings := WarnLegacyData(cfg); len(warnings) != 0 {
		t.Errorf("WarnLegacyData on a clean cwd = %v, want none", warnings)
	}
}

// TestWarnLegacyDataSilentWhenNewLocationAlreadyHasData: the operator
// already has real data at the NEW location too (e.g. already migrated, or
// simply happens to have both) — not the silent-loss case, so no warning.
func TestWarnLegacyDataSilentWhenNewLocationAlreadyHasData(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("piumy.db", []byte("old data"), 0o600); err != nil {
		t.Fatal(err)
	}
	newDir := t.TempDir()
	newPath := filepath.Join(newDir, "piumy.db")
	if err := os.WriteFile(newPath, []byte("already migrated"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{DBPath: newPath}

	if warnings := WarnLegacyData(cfg); len(warnings) != 0 {
		t.Errorf("WarnLegacyData with data already at the new location = %v, want none", warnings)
	}
}

// TestWarnLegacyDataNeverTouchesAnyFile: reading is all it does — no
// migration, no write, no delete, anywhere (T169's own explicit rule:
// "avisar, no decidir por el usuario").
func TestWarnLegacyDataNeverTouchesAnyFile(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("router.json", []byte(`{"old":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	newDir := t.TempDir()
	cfg := Config{RouterPath: filepath.Join(newDir, "router.json")}

	WarnLegacyData(cfg)

	if _, err := os.Stat("router.json"); err != nil {
		t.Errorf("legacy router.json disappeared: %v", err)
	}
	if _, err := os.Stat(cfg.RouterPath); err == nil {
		t.Error("WarnLegacyData created something at the new location — it must only read, never write")
	}
}
