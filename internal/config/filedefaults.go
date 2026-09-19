package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// configFileName is T11's own input config file (ct-2026-08-05-1214) —
// piumy-gateway reads it at startup and fills in whatever PIUMY_* env vars
// aren't already set. Deliberately NOT agent-connect.json: that one is
// OUTPUT (the gateway writes it, for an agent elsewhere to read); this one
// is INPUT (the gateway reads it, nothing else touches it) — opposite
// directions, opposite files, never merge them (boss/Citrino's explicit
// warning on this contract).
const configFileName = "piumy-config.json"

// legacyBatName is T6's original launcher, kept only as this feature's
// migration SOURCE (item 3) and as a manual-launch convenience the
// installer still writes (item 4) — no longer in the normal startup path.
// A shell script in the launch path doesn't travel to Linux/Mac/Raspberry,
// and Windows 11's default console host doesn't always honor a hidden-
// window request for the cmd.exe that ran it — the black flash the boss
// saw ("se nota que se ejecuta un bat o algo").
const legacyBatName = "run-piumy.bat"

// knownBatVars are the PIUMY_* variables run-piumy.bat's Launcher template
// (installer/windows/piumy.iss) ever set — the full set migrateFromBat
// looks for, paths and keys alike. PIUMY_CAPI_KEY dropped in T28
// (ct-2026-08-05-2242): a launcher from before that decision may still have
// the line, but nothing reads that env var anymore — migrating it forward
// would just be dead data sitting in piumy-config.json.
var knownBatVars = []string{
	"PIUMY_DB_PATH", "PIUMY_WA_DB_PATH", "PIUMY_ROUTER_PATH", "PIUMY_STATUS_PATH",
	"PIUMY_MEDIA_DIR", "PIUMY_MCP_KEY", "PIUMY_REST_KEY", "PIUMY_BACKUP_DIR",
	"PIUMY_BACKUP_KEY", "PIUMY_REST_ADDR",
}

// ApplyFileDefaults resolves this running binary's own directory and fills
// in whatever PIUMY_* env vars aren't already set — from piumy-config.json,
// or by migrating one from a legacy run-piumy.bat sitting next to it if the
// config file doesn't exist yet. A no-op, not an error, when neither file
// exists (dev, containers, PIUMY_DB_PATH already set directly — none of
// that changes). Call BEFORE config.Load(), same as
// restapi.SeedRecoveryEmailFromEnv/store.SeedFactoryRulesIfUnset run before
// whatever they seed gets read.
func ApplyFileDefaults() error {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	return ApplyFileDefaultsIn(filepath.Dir(exe))
}

// ApplyFileDefaultsIn is ApplyFileDefaults' testable core — dir is where to
// look for piumy-config.json/run-piumy.bat.
func ApplyFileDefaultsIn(dir string) error {
	path := filepath.Join(dir, configFileName)
	vals, err := loadConfigFile(path)
	if err != nil {
		return err
	}
	if vals == nil {
		migrated, err := migrateFromBat(filepath.Join(dir, legacyBatName))
		if err != nil {
			return err
		}
		if migrated != nil {
			if err := saveConfigFile(path, migrated); err != nil {
				return err
			}
			vals = migrated
		}
	}
	// Env var wins, always (T11's explicit rule) — the file only fills in
	// what's missing, so dev, rl.bat, every existing setup keeps working
	// exactly as it does today.
	for k, v := range vals {
		if os.Getenv(k) == "" {
			if err := os.Setenv(k, v); err != nil {
				return err
			}
		}
	}
	return nil
}

// loadConfigFile reads piumy-config.json — nil, nil if it doesn't exist yet
// (the normal "not migrated/configured this way" case, not an error).
func loadConfigFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var vals map[string]string
	if err := json.Unmarshal(data, &vals); err != nil {
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	return vals, nil
}

// saveConfigFile writes vals as piumy-config.json, atomically (tmp +
// rename) — same pattern state.Write/agentconnect.Write already use for
// their own files in this same directory.
func saveConfigFile(path string, vals map[string]string) error {
	data, err := json.MarshalIndent(vals, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// hasBOM detects a byte-order mark (UTF-16 LE/BE, or UTF-8+BOM) at the start
// of raw — same three cases piumy.iss's HasBOM checks (T21). A .bat with a
// BOM can't be parsed with certainty (Notepad "guardar como Unicode" is the
// real-world cause, per T21's own finding), so migrateFromBat refuses to
// guess rather than risk generating keys that replace the ones protecting
// existing backups.
func hasBOM(raw []byte) bool {
	switch {
	case len(raw) >= 2 && raw[0] == 0xFF && raw[1] == 0xFE:
		return true
	case len(raw) >= 2 && raw[0] == 0xFE && raw[1] == 0xFF:
		return true
	case len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF:
		return true
	}
	return false
}

// parseSetLine mirrors piumy.iss's ParseSetLine (T21/T22) exactly: case-
// insensitive "set ", quotes wrapping the WHOLE "VAR=value" get stripped
// (cmd's `set "VAR=value"` form), quotes around just the value do NOT —
// T22's own finding, verified against real cmd behavior: cmd never strips
// those, so neither does this. Trailing \r from a CRLF line is handled by
// strings.TrimSpace, same as everywhere else in this function.
func parseSetLine(line string) (key, value string, ok bool) {
	t := strings.TrimSpace(line)
	if len(t) < 4 || !strings.EqualFold(t[:4], "set ") {
		return "", "", false
	}
	rest := strings.TrimSpace(t[4:])
	if rest == "" {
		return "", "", false
	}
	if len(rest) >= 2 && rest[0] == '"' && rest[len(rest)-1] == '"' {
		rest = rest[1 : len(rest)-1]
	}
	eq := strings.IndexByte(rest, '=')
	if eq < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(rest[:eq])
	value = strings.TrimSpace(rest[eq+1:])
	return key, value, true
}

// migrateFromBat reads batPath's raw bytes and extracts every knownBatVars
// entry it can parse tolerantly (mayús/minús, comillas, espacios, última
// coincidencia gana — same rules as ResolveLauncherKeys, installer/windows/
// piumy.iss, T21/T22). nil, nil if batPath doesn't exist — nothing to
// migrate, a normal dev/fresh/non-Windows setup, not an error. Requires the
// 3 base keys (MCP/REST/BACKUP) present — same safety-critical set T21/T22
// treat as non-negotiable; if any is missing, aborts (nil, error) instead of
// writing a half-migrated file that would silently lose a key's real value.
func migrateFromBat(batPath string) (map[string]string, error) {
	raw, err := os.ReadFile(batPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if hasBOM(raw) {
		return nil, fmt.Errorf("config: %s está guardado en un formato distinto al esperado (Unicode/UTF-16, o UTF-8 con marca de orden de bytes) — no se puede migrar con certeza. Volvé a guardarlo como texto ANSI, o borralo si de verdad querés claves nuevas (se pierden los backups existentes cifrados con las de hoy)", batPath)
	}

	found := make(map[string]string)
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := parseSetLine(line)
		if !ok {
			continue
		}
		for _, want := range knownBatVars {
			if strings.EqualFold(key, want) {
				found[want] = value // última coincidencia gana — nunca corta antes
				break
			}
		}
	}

	_, hasMCP := found["PIUMY_MCP_KEY"]
	_, hasREST := found["PIUMY_REST_KEY"]
	_, hasBACKUP := found["PIUMY_BACKUP_KEY"]
	if !hasMCP || !hasREST || !hasBACKUP {
		return nil, fmt.Errorf("config: %s existe pero no se pudieron reconocer sus claves (MCP=%t REST=%t BACKUP=%t) — puede estar dañado o truncado, no se migra para no arriesgar las claves existentes", batPath, hasMCP, hasREST, hasBACKUP)
	}

	return found, nil
}
