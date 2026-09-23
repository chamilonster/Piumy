//go:build windows

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"piumy-gateway/internal/config"
)

// readShortcut reads a .lnk back through the same WScript.Shell the code under
// test writes with — values passed by env, same reason as createShortcutScript.
func readShortcut(t *testing.T, lnk string) (target, args, workDir, icon string) {
	t.Helper()
	// UTF-8 output: a child's stdout goes through the console's OEM code page
	// by default (é comes back as byte 0x82), which reads as a wrong path here.
	script := `[Console]::OutputEncoding=[Text.Encoding]::UTF8;` +
		`$s=(New-Object -ComObject WScript.Shell).CreateShortcut($env:PIUMY_LNK);` +
		`Write-Output $s.TargetPath;Write-Output $s.Arguments;Write-Output $s.WorkingDirectory;Write-Output $s.IconLocation`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), "PIUMY_LNK="+lnk)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("leer %s: %v: %s", lnk, err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 4 {
		t.Fatalf("lectura de %s: %d líneas, want 4: %q", lnk, len(lines), out)
	}
	return strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1]), strings.TrimSpace(lines[2]), strings.TrimSpace(lines[3])
}

// A profile folder with an apostrophe, a space and an accent — the paths a
// script that pasted values into its own text would break (or be injected)
// on. Also proves both folders (Desktop and Start Menu stand-ins) get their
// .lnk, with the args the flag needs and the account's icon.
func TestCreateAccountShortcutsRoundTripsAwkwardPaths(t *testing.T) {
	base := filepath.Join(t.TempDir(), "O'Brien é")
	desktop := filepath.Join(base, "Escritorio")
	startMenu := filepath.Join(base, "Programas")
	appDir := filepath.Join(base, "Piumy app")
	for _, d := range []string{desktop, startMenu, appDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	exe := filepath.Join(appDir, "Piumy.exe")
	if err := os.WriteFile(exe, []byte("MZ"), 0o755); err != nil {
		t.Fatal(err)
	}
	icon, err := writeAccountIcon(appDir, "cuenta-2")
	if err != nil {
		t.Fatalf("writeAccountIcon: %v", err)
	}

	if err := createAccountShortcuts([]string{desktop, startMenu}, exe, "cuenta-2", icon); err != nil {
		t.Fatalf("createAccountShortcuts: %v", err)
	}

	for _, dir := range []string{desktop, startMenu} {
		lnk := filepath.Join(dir, "Piumy (cuenta-2).lnk")
		target, args, workDir, gotIcon := readShortcut(t, lnk)
		if !strings.EqualFold(target, exe) {
			t.Errorf("%s: TargetPath = %q, want %q", lnk, target, exe)
		}
		if args != "--account cuenta-2" {
			t.Errorf("%s: Arguments = %q, want %q", lnk, args, "--account cuenta-2")
		}
		if !strings.EqualFold(workDir, appDir) {
			t.Errorf("%s: WorkingDirectory = %q, want %q (the exe's own folder)", lnk, workDir, appDir)
		}
		if want := icon + ",0"; !strings.EqualFold(gotIcon, want) {
			t.Errorf("%s: IconLocation = %q, want %q", lnk, gotIcon, want)
		}
	}
}

// The color is the point: a named account's icon file must NOT be the default
// green icon — and must be the very bytes the tray paints with, so the three
// surfaces (tray, dashboard, shortcut) can't disagree.
func TestWriteAccountIconIsTheAccountsRecoloredTrayIcon(t *testing.T) {
	dir := t.TempDir()
	path, err := writeAccountIcon(dir, "cuenta-2")
	if err != nil {
		t.Fatalf("writeAccountIcon: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Errorf("icon written to %q, want inside %q (the account's own folder)", path, dir)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := RecolorTrayIcon(trayIcon, config.ColorForAccount("cuenta-2").HueDelta)
	if err != nil {
		t.Fatalf("RecolorTrayIcon: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Error("the .ico on disk is not what the tray paints for this account")
	}
	if bytes.Equal(got, trayIcon) {
		t.Error("the account's .ico is the default icon — no distinctive color")
	}
	if len(got) < 4 || got[0] != 0 || got[1] != 0 || got[2] != 1 || got[3] != 0 {
		t.Errorf("not an ICO container (header % x)", got[:min(4, len(got))])
	}
}
