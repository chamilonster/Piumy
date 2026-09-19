package config

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestAllDataPathsAreAbsoluteByDefault is T169's own invariant test
// (ct-2026-09-19-1433, "nunca más un default relativo para un archivo de
// datos"): enumerated BY PATTERN over Config's own fields (anything named
// *Path/*Dir), never a hand-typed list of names — this project already
// learned twice (T108, T114) that a list leaves holes a sweep doesn't.
// Whatever data-path field Config gains tomorrow is covered automatically,
// with zero extra code here.
func TestAllDataPathsAreAbsoluteByDefault(t *testing.T) {
	for _, v := range []string{
		"PIUMY_DB_PATH", "PIUMY_WA_DB_PATH", "PIUMY_ROUTER_PATH",
		"PIUMY_STATUS_PATH", "PIUMY_MEDIA_DIR", "PIUMY_BACKUP_DIR",
		"PIUMY_POLICY_PATH",
	} {
		t.Setenv(v, "")
	}
	t.Setenv("PIUMY_DATA_DIR", t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	v := reflect.ValueOf(cfg)
	ty := v.Type()
	checked := 0
	for i := 0; i < ty.NumField(); i++ {
		f := ty.Field(i)
		if f.Type.Kind() != reflect.String {
			continue
		}
		if !strings.HasSuffix(f.Name, "Path") && !strings.HasSuffix(f.Name, "Dir") {
			continue
		}
		// PolicyPath: "" is a real, deliberate sentinel ("no file — fall
		// back to each package's own embedded default"). It is never given
		// a file default, so there is nothing here for DataDir() to
		// default it under — the ONE named exception to the pattern below.
		if f.Name == "PolicyPath" {
			continue
		}
		checked++
		val := v.Field(i).String()
		if val == "" {
			t.Errorf("Config.%s is empty with no env var set — every data path must have a real default (T169)", f.Name)
			continue
		}
		if !filepath.IsAbs(val) {
			t.Errorf("Config.%s = %q, want an absolute path (DataDir()-based default, T169) — never a relative one", f.Name, val)
		}
	}
	if checked == 0 {
		t.Fatal("no *Path/*Dir field matched the pattern — this test would pass even if the whole check were broken")
	}
}

// TestExplicitPathEnvVarsAreUsedVerbatim is what protects the boss's own
// running install (T169, ct-2026-09-19-1433): his Piumy sets the data-path
// env vars explicitly, pointing at secrets\ under his install dir. Whatever
// DataDir()/envPath do for the DEFAULT case, an explicit env var must come
// back byte-for-byte untouched — never joined onto DataDir(), never
// modified. Deliberately sets PIUMY_DATA_DIR to something else entirely: a
// bug that accidentally joined the explicit path onto DataDir() would still
// produce something plausible-looking — only an EXACT match catches it.
func TestExplicitPathEnvVarsAreUsedVerbatim(t *testing.T) {
	t.Setenv("PIUMY_DATA_DIR", filepath.Join(t.TempDir(), "wrong-place"))

	explicit := map[string]string{
		"PIUMY_DB_PATH":     `C:\install\secrets\piumy.db`,
		"PIUMY_WA_DB_PATH":  `C:\install\secrets\whatsmeow.db`,
		"PIUMY_ROUTER_PATH": `C:\install\secrets\router.json`,
		"PIUMY_STATUS_PATH": `C:\install\secrets\status.json`,
		"PIUMY_MEDIA_DIR":   `C:\install\secrets\media`,
		"PIUMY_BACKUP_DIR":  `C:\install\secrets\backups`,
	}
	for k, v := range explicit {
		t.Setenv(k, v)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{
		"PIUMY_DB_PATH":     cfg.DBPath,
		"PIUMY_WA_DB_PATH":  cfg.WADBPath,
		"PIUMY_ROUTER_PATH": cfg.RouterPath,
		"PIUMY_STATUS_PATH": cfg.StatusPath,
		"PIUMY_MEDIA_DIR":   cfg.MediaDir,
		"PIUMY_BACKUP_DIR":  cfg.BackupDir,
	}
	for k, want := range explicit {
		if got[k] != want {
			t.Errorf("%s ended up as %q, want the explicit value %q untouched — this is the boss's own live install path", k, got[k], want)
		}
	}
}
