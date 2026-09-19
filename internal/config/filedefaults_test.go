package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func clearPiumyEnv(t *testing.T) {
	t.Helper()
	for _, v := range knownBatVars {
		t.Setenv(v, "")
		os.Unsetenv(v)
	}
}

// TestApplyFileDefaultsEnvVarWins is T11's own explicit rule: an env var
// already set must never be overridden by the file — dev, rl.bat, every
// existing setup keeps working unchanged.
func TestApplyFileDefaultsEnvVarWins(t *testing.T) {
	clearPiumyEnv(t)
	dir := t.TempDir()
	writeJSONFile(t, filepath.Join(dir, configFileName), map[string]string{
		"PIUMY_MCP_KEY": "from-file",
	})
	t.Setenv("PIUMY_MCP_KEY", "from-env")

	if err := ApplyFileDefaultsIn(dir); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("PIUMY_MCP_KEY"); got != "from-env" {
		t.Errorf("PIUMY_MCP_KEY = %q, want the env var to survive untouched", got)
	}
}

// TestApplyFileDefaultsFillsFromFile: an unset env var gets filled in from
// piumy-config.json.
func TestApplyFileDefaultsFillsFromFile(t *testing.T) {
	clearPiumyEnv(t)
	dir := t.TempDir()
	writeJSONFile(t, filepath.Join(dir, configFileName), map[string]string{
		"PIUMY_MCP_KEY":  "aaaa1111",
		"PIUMY_REST_KEY": "bbbb2222",
	})

	if err := ApplyFileDefaultsIn(dir); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("PIUMY_MCP_KEY"); got != "aaaa1111" {
		t.Errorf("PIUMY_MCP_KEY = %q, want aaaa1111", got)
	}
	if got := os.Getenv("PIUMY_REST_KEY"); got != "bbbb2222" {
		t.Errorf("PIUMY_REST_KEY = %q, want bbbb2222", got)
	}
}

// TestApplyFileDefaultsNoFilesIsNoop: neither piumy-config.json nor
// run-piumy.bat exist — dev/container/PIUMY_DB_PATH-set-directly must be
// completely unaffected.
func TestApplyFileDefaultsNoFilesIsNoop(t *testing.T) {
	clearPiumyEnv(t)
	dir := t.TempDir()

	if err := ApplyFileDefaultsIn(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, configFileName)); !os.IsNotExist(err) {
		t.Error("piumy-config.json got created out of nothing, want it to stay absent")
	}
}

// TestApplyFileDefaultsMigratesFromBat is T11's item 3: no config file yet,
// but a run-piumy.bat exists (T6/T21/T22's launcher) — its keys migrate to
// the new file, and the env vars get filled from it in the same pass.
func TestApplyFileDefaultsMigratesFromBat(t *testing.T) {
	clearPiumyEnv(t)
	dir := t.TempDir()
	writeBat(t, dir, "@echo off\r\n"+
		"set PIUMY_DB_PATH=%~dp0secrets\\piumy.db\r\n"+
		"set PIUMY_MCP_KEY=aaaa1111\r\n"+
		"set PIUMY_REST_KEY=bbbb2222\r\n"+
		"set PIUMY_BACKUP_KEY=cccc3333\r\n"+
		"start \"\" /B \"%~dp0Piumy.exe\"\r\n")

	if err := ApplyFileDefaultsIn(dir); err != nil {
		t.Fatal(err)
	}

	if got := os.Getenv("PIUMY_MCP_KEY"); got != "aaaa1111" {
		t.Errorf("PIUMY_MCP_KEY = %q, want aaaa1111 (migrated)", got)
	}
	if got := os.Getenv("PIUMY_BACKUP_KEY"); got != "cccc3333" {
		t.Errorf("PIUMY_BACKUP_KEY = %q, want cccc3333 (migrated) — losing this one is unrecoverable data loss", got)
	}

	saved := readJSONFile(t, filepath.Join(dir, configFileName))
	if saved["PIUMY_BACKUP_KEY"] != "cccc3333" {
		t.Errorf("piumy-config.json PIUMY_BACKUP_KEY = %q, want cccc3333 — the migration must persist, not just apply for this run", saved["PIUMY_BACKUP_KEY"])
	}
}

// TestApplyFileDefaultsMigrationDropsLegacyCapiKey covers T28
// (ct-2026-08-05-2242): a launcher from before that decision may still have
// a PIUMY_CAPI_KEY line (nothing reads that env var anymore) — migration
// must succeed on the 3 base keys alone and must NOT carry the stale value
// forward into the new piumy-config.json.
func TestApplyFileDefaultsMigrationDropsLegacyCapiKey(t *testing.T) {
	clearPiumyEnv(t)
	dir := t.TempDir()
	writeBat(t, dir, "@echo off\r\n"+
		"set PIUMY_MCP_KEY=aaaa1111\r\n"+
		"set PIUMY_REST_KEY=bbbb2222\r\n"+
		"set PIUMY_BACKUP_KEY=cccc3333\r\n"+
		"set PIUMY_CAPI_KEY=stale-from-before-t28\r\n")

	if err := ApplyFileDefaultsIn(dir); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("PIUMY_CAPI_KEY"); got != "" {
		t.Errorf("PIUMY_CAPI_KEY = %q, want empty — nothing should migrate or generate this anymore", got)
	}
	vals := readJSONFile(t, filepath.Join(dir, configFileName))
	if _, ok := vals["PIUMY_CAPI_KEY"]; ok {
		t.Errorf("piumy-config.json has PIUMY_CAPI_KEY = %q, want the key absent entirely", vals["PIUMY_CAPI_KEY"])
	}
}

// TestApplyFileDefaultsMigrationAbortsOnMissingBaseKey: the .bat exists but
// is missing one of the 3 safety-critical keys (truncated/damaged) — must
// not migrate anything rather than silently lose the missing one.
func TestApplyFileDefaultsMigrationAbortsOnMissingBaseKey(t *testing.T) {
	clearPiumyEnv(t)
	dir := t.TempDir()
	writeBat(t, dir, "@echo off\r\n"+
		"set PIUMY_MCP_KEY=aaaa1111\r\n"+
		"set PIUMY_REST_KEY=bbbb2222\r\n") // falta PIUMY_BACKUP_KEY

	err := ApplyFileDefaultsIn(dir)
	if err == nil {
		t.Fatal("want an error — the .bat is missing PIUMY_BACKUP_KEY")
	}
	if _, statErr := os.Stat(filepath.Join(dir, configFileName)); !os.IsNotExist(statErr) {
		t.Error("piumy-config.json got written despite a missing base key — must never write a half-migrated file")
	}
	if got := os.Getenv("PIUMY_MCP_KEY"); got != "" {
		t.Errorf("PIUMY_MCP_KEY = %q, want empty — a failed migration must not leak partial env values either", got)
	}
}

// TestApplyFileDefaultsMigrationAbortsOnBOM mirrors T21's UTF-16 finding —
// a .bat saved as "Unicode" from Notepad must abort the migration, not
// generate fresh keys that replace the ones protecting existing backups.
func TestApplyFileDefaultsMigrationAbortsOnBOM(t *testing.T) {
	clearPiumyEnv(t)
	dir := t.TempDir()
	utf16 := []byte{0xFF, 0xFE} // UTF-16 LE BOM
	content := "@echo off\r\nset PIUMY_MCP_KEY=aaaa1111\r\n"
	for _, r := range content {
		utf16 = append(utf16, byte(r), 0)
	}
	if err := os.WriteFile(filepath.Join(dir, legacyBatName), utf16, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ApplyFileDefaultsIn(dir); err == nil {
		t.Fatal("want an error — the .bat has a UTF-16 BOM, can't be trusted")
	}
	if _, statErr := os.Stat(filepath.Join(dir, configFileName)); !os.IsNotExist(statErr) {
		t.Error("piumy-config.json got written despite a BOM'd .bat")
	}
}

// TestApplyFileDefaultsMigrationTolerantParsing covers the same format
// variations T21/T22 made ParseSetLine tolerant to: SET uppercase, quotes
// wrapping the whole assignment (stripped) vs. quotes around just the value
// (kept literal, T22's own finding).
func TestApplyFileDefaultsMigrationTolerantParsing(t *testing.T) {
	clearPiumyEnv(t)
	dir := t.TempDir()
	writeBat(t, dir, "@ECHO off\r\n"+
		"SET  PIUMY_MCP_KEY = aaaa1111 \r\n"+
		"set \"PIUMY_REST_KEY=bbbb2222\"\r\n"+
		"set PIUMY_BACKUP_KEY=\"cccc3333\"\r\n")

	if err := ApplyFileDefaultsIn(dir); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("PIUMY_MCP_KEY"); got != "aaaa1111" {
		t.Errorf("PIUMY_MCP_KEY (SET uppercase + spaces) = %q, want aaaa1111", got)
	}
	if got := os.Getenv("PIUMY_REST_KEY"); got != "bbbb2222" {
		t.Errorf("PIUMY_REST_KEY (set \"VAR=value\") = %q, want bbbb2222 without quotes", got)
	}
	if got := os.Getenv("PIUMY_BACKUP_KEY"); got != `"cccc3333"` {
		t.Errorf(`PIUMY_BACKUP_KEY (set VAR="value") = %q, want "cccc3333" WITH quotes — cmd never strips those (T22)`, got)
	}
}

// TestApplyFileDefaultsMigrationLastDuplicateWins mirrors T21/T22's own
// last-line-wins rule for a duplicated key.
func TestApplyFileDefaultsMigrationLastDuplicateWins(t *testing.T) {
	clearPiumyEnv(t)
	dir := t.TempDir()
	writeBat(t, dir, "@echo off\r\n"+
		"set PIUMY_MCP_KEY=OLDWRONG\r\n"+
		"set PIUMY_MCP_KEY=aaaa1111\r\n"+
		"set PIUMY_REST_KEY=bbbb2222\r\n"+
		"set PIUMY_BACKUP_KEY=cccc3333\r\n")

	if err := ApplyFileDefaultsIn(dir); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("PIUMY_MCP_KEY"); got != "aaaa1111" {
		t.Errorf("PIUMY_MCP_KEY = %q, want the LAST line's value (aaaa1111)", got)
	}
}

// TestApplyFileDefaultsSecondRunDoesNotReMigrate: once piumy-config.json
// exists, a later run must load it as-is, never re-derive it from the .bat
// (which could itself have been hand-edited since).
func TestApplyFileDefaultsSecondRunDoesNotReMigrate(t *testing.T) {
	clearPiumyEnv(t)
	dir := t.TempDir()
	writeBat(t, dir, "@echo off\r\n"+
		"set PIUMY_MCP_KEY=aaaa1111\r\n"+
		"set PIUMY_REST_KEY=bbbb2222\r\n"+
		"set PIUMY_BACKUP_KEY=cccc3333\r\n")
	if err := ApplyFileDefaultsIn(dir); err != nil {
		t.Fatal(err)
	}

	// El dueño edita el archivo de config a mano después de la migración.
	writeJSONFile(t, filepath.Join(dir, configFileName), map[string]string{
		"PIUMY_MCP_KEY": "edited-by-hand",
	})
	clearPiumyEnv(t)

	if err := ApplyFileDefaultsIn(dir); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("PIUMY_MCP_KEY"); got != "edited-by-hand" {
		t.Errorf("PIUMY_MCP_KEY = %q, want the hand-edited config file value, not a re-migration from the .bat", got)
	}
}

func writeJSONFile(t *testing.T, path string, vals map[string]string) {
	t.Helper()
	data, err := json.Marshal(vals)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readJSONFile(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var vals map[string]string
	if err := json.Unmarshal(data, &vals); err != nil {
		t.Fatal(err)
	}
	return vals
}

func writeBat(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, legacyBatName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
