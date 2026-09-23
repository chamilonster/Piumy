package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// DataDir resolves the root piumy-gateway keeps its OWN data under — never
// the code tree it's built from (T169, ct-2026-09-19-1433, boss verbatim:
// "el problema es que no separaron el programa instalado de lo que se
// programa... los archivos personales, obvios que no van al coderoot").
//
// PIUMY_DATA_DIR wins outright when set — one switch for a test instance to
// point everywhere it needs, replacing the "set five loose PIUMY_*_PATH
// vars" trick used until now (T164/T165's isolated-instance runs). This is
// unconditional, even with PIUMY_ACCOUNT also set (S1, ct-2026-09-20-1100)
// — an explicit override stays an override, verbatim, the account layer
// below never gets a chance to touch it.
//
// Otherwise, the OS's own standard per-app data location, resolved HERE at
// the source — never left to whatever directory happens to be the working
// one when the binary gets launched (that's the exact bug: running from
// coderoot wrote the WhatsApp session and the conversation store straight
// into the repo). Cross-platform from the start: T113 already means
// Mac/Linux/Raspberry, not just Windows. With PIUMY_ACCOUNT set, that root
// gains one more segment (accounts/<slug>) — see dataDirFor — so two
// accounts on the same machine get two completely separate data trees,
// each with its own whatsmeow.db.
//
// Everything under this root gets the SAME secrets/logs split the Windows
// installer already uses (piumy.iss: DB/WA-DB/router/status/media under
// secrets\, logs as its sibling, never inside — a shared log must never sit
// next to credentials) — see the individual field defaults in config.go.
func DataDir() (string, error) {
	if v := os.Getenv("PIUMY_DATA_DIR"); v != "" {
		return v, nil
	}
	home, _ := os.UserHomeDir() // "" on failure is fine — dataDirFor errors clearly if it actually needs it
	return dataDirFor(runtime.GOOS, os.Getenv("LOCALAPPDATA"), home, os.Getenv("PIUMY_ACCOUNT"))
}

// dataDirFor is DataDir's pure decision core — goos/localAppData/home/
// account passed in (instead of read live) so every branch, including the
// two this machine isn't running, is unit-testable without faking
// runtime.GOOS or the real environment. account empty = today's single
// root; set = that same root's accounts/<slug> subfolder (S1,
// ct-2026-09-20-1100).
func dataDirFor(goos, localAppData, home, account string) (string, error) {
	base, err := baseDir(goos, localAppData, home)
	if err != nil {
		return "", err
	}
	if account == "" {
		return base, nil
	}
	slug, err := accountSlug(account)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, accountsDirName, slug), nil
}

// accountsDirName is the folder every named account lives under, next to the
// default account's own data (S1) — also where ReserveAccount (S4) looks for
// the next free name.
const accountsDirName = "accounts"

// baseDir is the OS's own per-app data root — the default account's data dir
// itself, and the parent of accountsDirName. Split out of dataDirFor so
// ReserveAccount (S4) reads the SAME root without repeating the per-OS switch.
func baseDir(goos, localAppData, home string) (string, error) {
	if goos == "windows" && localAppData != "" {
		return filepath.Join(localAppData, "Piumy"), nil
	}
	if home == "" {
		return "", fmt.Errorf("config: no se pudo resolver el directorio de datos del sistema operativo (sin HOME ni %%LOCALAPPDATA%%) — seteá PIUMY_DATA_DIR a mano")
	}
	switch goos {
	case "windows":
		return filepath.Join(home, "AppData", "Local", "Piumy"), nil
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Piumy"), nil
	default:
		return filepath.Join(home, ".local", "share", "piumy"), nil
	}
}

// accountSlug validates PIUMY_ACCOUNT as a filesystem-safe folder name —
// hard validation, not silent munging: a hostile or malformed value is a
// clear startup error, never a quietly-transformed path that could escape
// the accounts/ folder (S1, ct-2026-09-20-1100).
func accountSlug(account string) (string, error) {
	s := strings.TrimSpace(account)
	if s == "" {
		return "", fmt.Errorf("config: PIUMY_ACCOUNT está vacío — seteá un nombre de cuenta o no la definas")
	}
	if strings.ContainsAny(s, `/\:`) || strings.Contains(s, "..") {
		return "", fmt.Errorf("config: PIUMY_ACCOUNT %q no es un nombre de cuenta válido (sin \"/\", \"\\\", \":\" ni \"..\")", account)
	}
	return s, nil
}
