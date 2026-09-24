//go:build windows

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// shortcutFolders are where "Piumy (cuenta-N)" is written (S4,
// ct-2026-09-23-1908): the user's Desktop and Start Menu > Programs. Resolved
// through the shell's own KnownFolderPath, not %USERPROFILE%\Desktop — a
// OneDrive-redirected Desktop (common) isn't under the profile folder.
func shortcutFolders() ([]string, error) {
	var dirs []string
	for _, id := range []*windows.KNOWNFOLDERID{windows.FOLDERID_Desktop, windows.FOLDERID_Programs} {
		dir, err := windows.KnownFolderPath(id, 0)
		if err != nil {
			return nil, fmt.Errorf("carpeta conocida de Windows: %w", err)
		}
		dirs = append(dirs, dir)
	}
	return dirs, nil
}

// startupFolder is the user's Startup folder — where the installer's "start
// with Windows" task (startupicon) leaves Piumy.lnk (S5, ct-2026-09-23-2038).
func startupFolder() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_Startup, 0)
}

// shortcutName is the file name of the shortcut for an account's label — its
// id ("cuenta-2") when created, the account's label once linked.
func shortcutName(label string) string {
	return "Piumy (" + label + ").lnk"
}

// maxShortcutLabelRunes keeps "Piumy (…).lnk" far from MAX_PATH whatever
// someone put in their WhatsApp name.
const maxShortcutLabelRunes = 60

// cleanShortcutLabel drops what Windows doesn't accept in a file name (the
// reserved characters), turns control characters into blanks, collapses runs
// of blanks and cuts a very long name. "" means nothing usable was left.
func cleanShortcutLabel(label string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r < 0x20:
			return ' '
		case strings.ContainsRune(`<>:"/\|?*`, r):
			return -1
		}
		return r
	}, label)
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	if runes := []rune(cleaned); len(runes) > maxShortcutLabelRunes {
		cleaned = strings.TrimSpace(string(runes[:maxShortcutLabelRunes]))
	}
	return cleaned
}

// renameAccountShortcuts gives the account's shortcut the file name of label
// in every dir that has one (S5, ct-2026-09-23-2038). A shortcut is found by
// the labels it may carry so far — olds, in order: the account's id, and what
// the label said before. It has to follow, not rename once: the number reaches
// the account before its name does, so the label goes "cuenta-2" -> "...7132"
// -> "Camilo · ...7132" within seconds, and the file must end on the last.
// A shortcut carrying none of them isn't ours to touch. If the new name
// already exists in ANY dir nothing is renamed in ANY: it is the state this
// function itself leaves behind (so calling it again is a no-op), and a name
// taken by someone else must not make the Desktop disagree with the Start Menu.
// ponytail: labels tell accounts apart by the last 4 digits of the number, a
// distinguisher and not a key — two numbers ending alike could swap shortcuts;
// a shortcut keyed by its arguments would need to read the .lnk back.
func renameAccountShortcuts(dirs []string, label string, olds ...string) error {
	clean := cleanShortcutLabel(label)
	if clean == "" {
		return nil
	}
	for _, dir := range dirs {
		if _, err := os.Stat(filepath.Join(dir, shortcutName(clean))); err == nil {
			return nil
		}
	}
	var errs []error
	for _, dir := range dirs {
		for _, old := range olds {
			err := os.Rename(filepath.Join(dir, shortcutName(cleanShortcutLabel(old))), filepath.Join(dir, shortcutName(clean)))
			if err == nil {
				break
			}
			if !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// createShortcutScript reads every value from the environment: the paths and
// the account name are DATA, and pasting them into the script text would break
// (or inject into) a path with a quote in it — "C:\Users\O'Brien\Desktop" is a
// real profile folder. The icon is optional: without one the .lnk shows the
// exe's own icon.
const createShortcutScript = `$s=(New-Object -ComObject WScript.Shell).CreateShortcut($env:PIUMY_LNK);` +
	`$s.TargetPath=$env:PIUMY_LNK_TARGET;$s.Arguments=$env:PIUMY_LNK_ARGS;` +
	`$s.WorkingDirectory=$env:PIUMY_LNK_WORKDIR;` +
	`if($env:PIUMY_LNK_ICON){$s.IconLocation=$env:PIUMY_LNK_ICON+',0'};$s.Save()`

// createShortcut writes lnkPath — a .lnk that starts target with args from
// workDir, showing iconPath (an .ico; "" = the exe's own icon). powershell +
// WScript.Shell instead of a COM library: no new dependency for a click-time,
// once-per-account job. CREATE_NO_WINDOW: without it every launch flashes a
// console (T11 already paid for that one, with the .bat launcher).
func createShortcut(lnkPath, target, args, workDir, iconPath string) error {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", createShortcutScript)
	cmd.Env = append(os.Environ(),
		"PIUMY_LNK="+lnkPath,
		"PIUMY_LNK_TARGET="+target,
		"PIUMY_LNK_ARGS="+args,
		"PIUMY_LNK_WORKDIR="+workDir,
		"PIUMY_LNK_ICON="+iconPath,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("powershell: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
