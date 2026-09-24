package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// CanOpenAnotherAccount reports whether opening a second account can work
// from this process (S4). With PIUMY_DATA_DIR set, DataDir() returns it
// verbatim and ignores the account (S1) — the new Piumy would land in this
// same folder, lose the instance mutex to us, and quit silently. An explicit
// data dir is single-root mode by definition; the tray hides the item there,
// which also means a test instance pinned with PIUMY_DATA_DIR can never touch
// the real OS root's accounts/.
func CanOpenAnotherAccount() bool {
	return os.Getenv("PIUMY_DATA_DIR") == ""
}

// ReserveAccount picks the name for a NEW named account and reserves it by
// creating its data folder (S4, ct-2026-09-23-1908). It returns the name and
// the folder's path: the first "cuenta-N" (N>=2) under the OS root's
// accounts/ folder that doesn't exist yet. Nobody types the name — an .exe
// built windowsgui without cgo has no cheap text dialog, and it keeps
// accountSlug's hostile-name checks moot.
//
// The reservation is os.Mkdir, not "look, then create later": the tray can be
// clicked twice before the launched Piumy has created its own folder, and
// two lookups would both answer cuenta-2. Mkdir is atomic — the first to
// create the folder wins, the other moves on to cuenta-3.
//
// The root comes from the OS (baseDir), never from DataDir(): a Piumy that is
// itself cuenta-2 must answer cuenta-3, not accounts/cuenta-2/accounts/....
func ReserveAccount() (name, dir string, err error) {
	home, _ := os.UserHomeDir()
	base, err := baseDir(runtime.GOOS, os.Getenv("LOCALAPPDATA"), home)
	if err != nil {
		return "", "", err
	}
	root := filepath.Join(base, accountsDirName)
	name, err = reserveAccountIn(root)
	if err != nil {
		return "", "", err
	}
	return name, filepath.Join(root, name), nil
}

// reserveAccountIn is ReserveAccount's testable core — root is the accounts/
// folder itself.
func reserveAccountIn(root string) (string, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	for n := 2; ; n++ {
		name := fmt.Sprintf("cuenta-%d", n)
		err := os.Mkdir(filepath.Join(root, name), 0o755)
		if err == nil {
			return name, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", err
		}
	}
}

// envNotInherited are the variables — on top of accountOwnedPathVars — a NEW
// account must not receive from the process that launches it: a data folder
// or a port pinned for the launcher can never be right for the child (it
// would land in the launcher's own folder and collide with its mutex, or
// fail to bind the launcher's port).
var envNotInherited = map[string]bool{
	"PIUMY_DATA_DIR": true, "PIUMY_MCP_ADDR": true, "PIUMY_REST_ADDR": true,
	DashHashSeedEnv: true,
}

// DashHashSeedEnv carries the launcher's dashboard password hash to the new
// account (S5, ct-2026-09-23-2038), so a second Piumy opens with the SAME
// login as the one it was opened from instead of the factory admin/piumy.
// One-shot: restapi.SeedDashPassHashFromEnv reads it at boot and unsets it.
// Never inherited (envNotInherited) — every launch sets it fresh, so a stale
// value can't travel down a chain of accounts.
const DashHashSeedEnv = "PIUMY_SEED_DASH_HASH"

// EnvForNewAccount returns env without what a new named account must not
// inherit from its launcher (S4, ct-2026-09-23-1908). The launcher — the
// default account — already ran ApplyFileDefaults, which os.Setenv'd
// PIUMY_DB_PATH/WA_DB_PATH/... from piumy-config.json into ITS OWN process
// env; a child spawned with that env would hand envPath() an explicit,
// "verbatim" path and open the launcher's LIVE database and WhatsApp session
// instead of accounts/<name>. The instance mutex doesn't catch it (it hashes
// the child's own data dir, a different one) — two processes on one session
// is exactly the T59 disaster. An .lnk started from Explorer has a clean env
// and never hits this; only a launch from a running Piumy does.
//
// The list of owned paths is accountOwnedPathVars itself — the same one
// ApplyFileDefaults uses to skip those keys in the FILE for a named account —
// so there is one place that says what a named account owns.
//
// dashPassHash is the launcher's own dashboard password hash (S5): when not
// empty it goes out as DashHashSeedEnv, the only way the new account learns
// the login it should open with. Empty (the launcher never had one) sends
// nothing — the new account then falls on the same default the launcher will.
func EnvForNewAccount(env []string, dashPassHash string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		key = strings.ToUpper(key) // Windows env names are case-insensitive
		if accountOwnedPathVars[key] || envNotInherited[key] {
			continue
		}
		out = append(out, kv)
	}
	if dashPassHash != "" {
		out = append(out, DashHashSeedEnv+"="+dashPassHash)
	}
	return out
}
