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

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("lnk"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Autostart detection hangs on this being the folder the installer's
// {userstartup} task writes to: Inno expands it from the same per-user Start
// Menu\Programs\Startup, under %APPDATA%.
func TestStartupFolderIsTheOneTheInstallerWritesTo(t *testing.T) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		t.Skip("no %APPDATA%")
	}
	got, err := startupFolder()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
	if !strings.EqualFold(got, want) {
		t.Errorf("startupFolder() = %q, want %q", got, want)
	}
}

func TestCleanShortcutLabel(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Contacto Uno", "Contacto Uno"},
		{"Contacto Uno · ...0041", "Contacto Uno · ...0041"}, // the label format survives whole
		{`Ana/Luz: "y" <o> a|b?*`, "AnaLuz y o ab"},          // what Windows refuses in a name
		{"Uno\tDos\n", "Uno Dos"},                            // control characters become a blank
		{"  Uno    Dos  ", "Uno Dos"},
		{"???", ""}, // nothing usable left
		{"", ""},
		{strings.Repeat("ñ", 60), strings.Repeat("ñ", maxShortcutLabelRunes)}, // cut by characters, not bytes
	} {
		if got := cleanShortcutLabel(tc.in); got != tc.want {
			t.Errorf("cleanShortcutLabel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Every folder that has the account's shortcut gets it renamed; the Startup
// folder of someone who didn't choose autostart has none and is left alone.
func TestRenameAccountShortcutsRenamesInEveryFolderThatHasOne(t *testing.T) {
	desktop, programs, startup := t.TempDir(), t.TempDir(), t.TempDir()
	touch(t, filepath.Join(desktop, "Piumy (cuenta-2).lnk"))
	touch(t, filepath.Join(programs, "Piumy (cuenta-2).lnk"))

	if err := renameAccountShortcuts([]string{desktop, programs, startup}, "Contacto Uno", "cuenta-2"); err != nil {
		t.Fatalf("renameAccountShortcuts: %v", err)
	}

	for _, dir := range []string{desktop, programs} {
		if !exists(filepath.Join(dir, "Piumy (Contacto Uno).lnk")) || exists(filepath.Join(dir, "Piumy (cuenta-2).lnk")) {
			t.Errorf("%s: not renamed to the WhatsApp name", dir)
		}
	}
	if entries, _ := os.ReadDir(startup); len(entries) != 0 {
		t.Errorf("a folder without the shortcut got one: %v", entries)
	}
}

// The three shortcuts always share a name: if the new name is taken in ANY
// folder, none is renamed (the contract's "queda Piumy (cuenta-N)").
func TestRenameAccountShortcutsKeepsTheIdEverywhereWhenTheNameIsTakenAnywhere(t *testing.T) {
	desktop, programs := t.TempDir(), t.TempDir()
	touch(t, filepath.Join(desktop, "Piumy (cuenta-2).lnk"))
	touch(t, filepath.Join(programs, "Piumy (cuenta-2).lnk"))
	touch(t, filepath.Join(programs, "Piumy (Contacto Uno).lnk")) // another account's

	if err := renameAccountShortcuts([]string{desktop, programs}, "Contacto Uno", "cuenta-2"); err != nil {
		t.Fatalf("renameAccountShortcuts: %v", err)
	}

	for _, dir := range []string{desktop, programs} {
		if !exists(filepath.Join(dir, "Piumy (cuenta-2).lnk")) {
			t.Errorf("%s: the id-named shortcut is gone although the name was taken", dir)
		}
	}
	if exists(filepath.Join(desktop, "Piumy (Contacto Uno).lnk")) {
		t.Error("Desktop was renamed while the Start Menu couldn't be — the two would disagree")
	}
}

// Nothing usable, or nothing new to say: files untouched.
func TestRenameAccountShortcutsDoesNothingWithoutAUsableName(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "Piumy (cuenta-2).lnk"))
	for _, label := range []string{"", "???", "cuenta-2"} {
		if err := renameAccountShortcuts([]string{dir}, label, "cuenta-2"); err != nil {
			t.Fatalf("label %q: %v", label, err)
		}
		if !exists(filepath.Join(dir, "Piumy (cuenta-2).lnk")) {
			t.Errorf("label %q: the shortcut was renamed or removed", label)
		}
	}
}

// The number reaches the account before its name does, so the label goes
// "cuenta-2" -> "...0041" -> "Contacto Uno · ...0041" within seconds: the file
// must follow it to the end — and never touch another account's shortcut.
func TestRenameAccountShortcutsFollowsTheLabelAsItGrows(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "Piumy (cuenta-2).lnk"))
	touch(t, filepath.Join(dir, "Piumy (cuenta-3).lnk")) // another account's

	steps := []struct {
		label string
		olds  []string
	}{
		{"...0041", []string{"cuenta-2"}},
		{"Contacto Uno · ...0041", []string{"cuenta-2", "...0041", "...0041"}},
		{"Contacto Dos · ...0041", []string{"cuenta-2", "Contacto Uno · ...0041", "...0041"}}, // the name changed later
	}
	for _, s := range steps {
		if err := renameAccountShortcuts([]string{dir}, s.label, s.olds...); err != nil {
			t.Fatalf("label %q: %v", s.label, err)
		}
		if !exists(filepath.Join(dir, "Piumy ("+s.label+").lnk")) {
			t.Fatalf("the shortcut did not end up as Piumy (%s).lnk", s.label)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Errorf("the account's shortcut should be ONE file, plus the other account's: %v", entries)
	}
	if !exists(filepath.Join(dir, "Piumy (cuenta-3).lnk")) {
		t.Error("another account's shortcut was touched")
	}
}

// After a restart the tray calls again with a label that is already on disk:
// nothing to do, and no error.
func TestRenameAccountShortcutsIsANoOpWhenAlreadyRenamed(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "Piumy (Contacto Uno · ...0041).lnk"))
	if err := renameAccountShortcuts([]string{dir}, "Contacto Uno · ...0041", "cuenta-2", "...0041"); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("a re-run changed the folder: %v", entries)
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
