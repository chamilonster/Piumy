//go:build windows

package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"piumy-gateway/internal/config"
	"piumy-gateway/internal/store"
)

// installerStartupShortcut is the file the installer's "start with Windows"
// task leaves in the Startup folder (installer/windows/piumy.iss: Name:
// "{userstartup}\{#MyAppName}", MyAppName = "Piumy"). Its presence is how a
// running Piumy knows the user asked for autostart.
const installerStartupShortcut = "Piumy.lnk"

// openAnotherPiumy is the tray item "Abrir otro Piumy" (S4, ct-2026-09-23-1908):
// reserve the next account name, leave two "Piumy (cuenta-N)" shortcuts
// (Desktop + Start Menu, in the account's own color) for the next time, and
// start the new Piumy as its own process. S5 (ct-2026-09-23-2038): a third one
// in the Startup folder when this Piumy itself starts with Windows, and the new
// Piumy opens with THIS Piumy's login instead of the factory one. Blocking
// (powershell takes a couple of seconds per shortcut) — the tray calls it in a
// goroutine. Everything after the name is best effort: a shortcut that can't be
// written (locked-down PowerShell, a read-only Desktop) is logged and the new
// Piumy still opens.
func openAnotherPiumy(st *store.Store) {
	name, dir, err := config.ReserveAccount()
	if err != nil {
		log.Printf("tray: abrir otro Piumy: reservar el nombre de la cuenta: %v", err)
		return
	}
	exe, err := os.Executable()
	if err != nil {
		log.Printf("tray: abrir otro Piumy (%s): ruta del ejecutable: %v", name, err)
		return
	}
	icon, err := writeAccountIcon(dir, name)
	if err != nil {
		log.Printf("tray: ícono de %s: %v — los accesos directos usan el ícono normal", name, err)
		icon = ""
	}
	if dirs, err := shortcutFolders(); err != nil {
		log.Printf("tray: accesos directos de %s: %v", name, err)
	} else {
		if dir := autostartFolder(); dir != "" {
			dirs = append(dirs, dir)
		}
		if err := createAccountShortcuts(dirs, exe, name, icon); err != nil {
			log.Printf("tray: accesos directos de %s: %v", name, err)
		}
	}
	// "" when this Piumy has no login stored yet (nobody ever signed in): it
	// sends no seed, and the new account lands on the same default this one will.
	dashPassHash, err := st.KVGet(store.SettingDashPassHash)
	if err != nil {
		log.Printf("tray: clave del tablero de %s: %v — el nuevo Piumy arranca con la de fábrica", name, err)
	}
	if err := launchAccount(exe, name, dashPassHash); err != nil {
		log.Printf("tray: abrir otro Piumy (%s): %v", name, err)
	}
}

// autostartFolder returns the Startup folder when the user chose "start with
// Windows" at install time (S5), "" when they didn't — a new account then does
// not start with Windows either.
func autostartFolder() string {
	dir, err := startupFolder()
	if err != nil {
		return ""
	}
	if _, err := os.Stat(filepath.Join(dir, installerStartupShortcut)); err != nil {
		return ""
	}
	return dir
}

// renameShortcutsToLabel gives this account's shortcuts the label the tray
// shows (S5) — every folder createAccountShortcuts may have written to, the
// Startup one included (a folder without the file is skipped). The shortcut is
// looked up by the account's id and by olds, the labels it may carry so far.
// Best effort, logged: the account works the same with the old file names.
func renameShortcutsToLabel(account, label string, olds ...string) {
	dirs, err := shortcutFolders()
	if err != nil {
		log.Printf("tray: renombrar accesos directos de %s: %v", account, err)
		return
	}
	if dir, err := startupFolder(); err == nil {
		dirs = append(dirs, dir)
	}
	if err := renameAccountShortcuts(dirs, label, append([]string{account}, olds...)...); err != nil {
		log.Printf("tray: renombrar accesos directos de %s: %v", account, err)
	}
}

// writeAccountIcon writes the account's recolored icon into its own data
// folder (S4) and returns the file's path — what the shortcuts point at, so
// the Desktop and Start Menu icons carry the SAME color as the tray icon and
// the dashboard: RecolorTrayIcon and config.ColorForAccount are exactly what
// the tray already uses. A recolor error is logged and the normal icon is
// written instead (RecolorTrayIcon hands it back), same fallback as the tray.
func writeAccountIcon(dir, name string) (string, error) {
	ico, err := RecolorTrayIcon(trayIcon, config.ColorForAccount(name).HueDelta)
	if err != nil {
		log.Printf("tray: recolorear el ícono de %s: %v — se usa el ícono normal", name, err)
	}
	path := filepath.Join(dir, "piumy.ico")
	return path, os.WriteFile(path, ico, 0o644)
}

// createAccountShortcuts writes "Piumy (name).lnk" into every dir, pointing at
// exe --account name with iconPath as its icon ("" = the exe's own). The
// working directory is the exe's own — the same one the installer's shortcuts
// use (ApplyFileDefaults looks for piumy-config.json next to the binary).
func createAccountShortcuts(dirs []string, exe, name, iconPath string) error {
	var errs []error
	for _, dir := range dirs {
		lnk := filepath.Join(dir, shortcutName(name))
		if err := createShortcut(lnk, exe, "--account "+name, filepath.Dir(exe), iconPath); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", lnk, err))
		}
	}
	return errors.Join(errs...)
}

// launchAccount starts exe --account name as its own process — Release()d, not
// waited on: it must outlive this Piumy (Windows doesn't kill a child with its
// parent unless a Job object says so). The environment goes through
// config.EnvForNewAccount: this Piumy already applied piumy-config.json to its
// own env, and inheriting that would open ITS database and WhatsApp session,
// not the new account's. dashPassHash is this Piumy's dashboard login, which
// the new account seeds itself with (S5).
func launchAccount(exe, name, dashPassHash string) error {
	cmd := exec.Command(exe, "--account", name)
	cmd.Dir = filepath.Dir(exe)
	cmd.Env = config.EnvForNewAccount(os.Environ(), dashPassHash)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
