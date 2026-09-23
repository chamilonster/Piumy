//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
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
