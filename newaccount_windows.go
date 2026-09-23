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
)

// openAnotherPiumy is the tray item "Abrir otro Piumy" (S4, ct-2026-09-23-1908):
// reserve the next account name, leave two "Piumy (cuenta-N)" shortcuts
// (Desktop + Start Menu, in the account's own color) for the next time, and
// start the new Piumy as its own process. Blocking (powershell takes a couple
// of seconds per shortcut) — the tray calls it in a goroutine. Everything
// after the name is best effort: a shortcut that can't be written (locked-down
// PowerShell, a read-only Desktop) is logged and the new Piumy still opens.
func openAnotherPiumy() {
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
	} else if err := createAccountShortcuts(dirs, exe, name, icon); err != nil {
		log.Printf("tray: accesos directos de %s: %v", name, err)
	}
	if err := launchAccount(exe, name); err != nil {
		log.Printf("tray: abrir otro Piumy (%s): %v", name, err)
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
		lnk := filepath.Join(dir, fmt.Sprintf("Piumy (%s).lnk", name))
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
// not the new account's.
func launchAccount(exe, name string) error {
	cmd := exec.Command(exe, "--account", name)
	cmd.Dir = filepath.Dir(exe)
	cmd.Env = config.EnvForNewAccount(os.Environ())
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
