//go:build windows

package main

import (
	"context"
	_ "embed"
	"log"
	"os/exec"

	"fyne.io/systray"
	"golang.org/x/sys/windows/registry"

	"piumy-gateway/internal/config"
	"piumy-gateway/internal/i18n"
	"piumy-gateway/internal/version"
)

// trayIcon is la carita Piumy (círculo verde fósforo sobre negro) en
// 16/32/48 px — generado con un programa descartable stdlib-only
// (image/png + un contenedor ICO escrito a mano) desde el logo de marca y
// committeado como asset estático; ver docs/MANUAL.md para cómo se hizo.
//
//go:embed assets/tray.ico
var trayIcon []byte

// runTrayOrWait shows a Windows system tray icon (ct-2026-07-10-2312, F3) —
// menu version (disabled) / "Abrir dashboard" / "Salir" — and blocks until
// shutdown. Three ways out, all converging on the same graceful-shutdown
// path main() already has: "Salir" calls stop() (identical to Ctrl+C);
// ctx.Done() firing externally (Ctrl+C) calls systray.Quit() so the icon
// never lingers after the rest of the process starts tearing down; and
// Windows itself (shutdown, logoff, or a taskkill without /F) sends
// WM_CLOSE/WM_ENDSESSION, which fyne.io/systray's own wndProc turns into a
// call to onExit below (ct-2026-08-07) — before this, that third path
// reached systray and stopped there, never calling stop(), so the process
// could outlive its own tray icon.
//
// fyne.io/systray is CGO-free on Windows (syscall + golang.org/x/sys/windows
// only — verified with an explicit CGO_ENABLED=0 build before adding this
// dependency; its only cgo file is systray_darwin.go, never compiled here) —
// CGO_ENABLED=0 stays intact, the project's central invariant.
func runTrayOrWait(ctx context.Context, stop context.CancelFunc, dashboardURL string, lang i18n.Lang, langChanged <-chan i18n.Lang, account string) {
	systray.Run(func() {
		// T37 (ct-2026-08-08-1433, boss: "quiero que el tray diga la version
		// de piumy" — acotado después, verbatim: "en el tray en el menú, no
		// al pasar el mouse") — solo el ítem de menú. El tooltip/título
		// quedan sin tocar a propósito, no es un olvido.
		//
		// "Piumy Gateway" NO se traduce (T153 etapa 3c, ct-2026-09-16-1854):
		// es el nombre del producto, en SetTitle/SetTooltip y acá en el
		// texto del ítem de versión — nunca pasa por i18n.T, en ningún
		// idioma. El nombre de cuenta (S2, ct-2026-09-20-1134) tampoco se
		// traduce nunca — es un dato, no texto de interfaz.
		//
		// Tensión con T37, a propósito, no un olvido: T37 acotó la VERSIÓN
		// al ítem de menú porque su trabajo es informar. El nombre de
		// cuenta va en título + tooltip + ítem porque su trabajo es
		// impedir un click equivocado entre dos instancias — y el mouse
		// pasa por encima ANTES del click, así que el tooltip también
		// tiene que decirlo. No "corregir" esto para que quede igual a la
		// versión: son dos jobs distintos.
		title := "Piumy Gateway"
		if account != "" {
			title = "Piumy Gateway — " + account
		}
		// S3 (ct-2026-09-20-1202): config.ColorForAccount is the ONE place
		// that decides what color this account gets — internal/restapi reads
		// the exact same function for the dashboard's accent, so the two
		// surfaces can never disagree. This file only paints.
		icon, err := RecolorTrayIcon(trayIcon, config.ColorForAccount(account).HueDelta)
		if err != nil {
			log.Printf("tray: recolor icon for account %q: %v — usando el ícono normal", account, err)
		}
		systray.SetIcon(icon)
		systray.SetTitle(title)
		systray.SetTooltip(title)
		mVersion := systray.AddMenuItem("Piumy Gateway "+version.Version, i18n.T(lang, "server.tray_version_tooltip"))
		mVersion.Disable()
		var mAccount *systray.MenuItem
		if account != "" {
			mAccount = systray.AddMenuItem(i18n.T(lang, "account.label", "account", account), i18n.T(lang, "account.label", "account", account))
			mAccount.Disable()
		}
		mOpen := systray.AddMenuItem(i18n.T(lang, "server.tray_open_dashboard"), i18n.T(lang, "server.tray_open_dashboard_tooltip"))
		// S4 (ct-2026-09-23-1908): "Abrir otro Piumy". Absent — not disabled —
		// where it can't work (see config.CanOpenAnotherAccount). anotherClicked
		// stays nil then, and a nil channel never fires inside the select below.
		var mAnother *systray.MenuItem
		var anotherClicked <-chan struct{}
		if config.CanOpenAnotherAccount() {
			mAnother = systray.AddMenuItem(i18n.T(lang, "server.tray_open_another"), i18n.T(lang, "server.tray_open_another_tooltip"))
			anotherClicked = mAnother.ClickedCh
		}
		mQuit := systray.AddMenuItem(i18n.T(lang, "server.tray_quit"), i18n.T(lang, "server.tray_quit_tooltip"))

		go func() {
			for {
				select {
				case <-mOpen.ClickedCh:
					openAppWindow(dashboardURL)
				case <-anotherClicked:
					// Own goroutine: it runs powershell (about a second) and must
					// not freeze this loop — Quit and language changes keep working.
					go openAnotherPiumy()
				case <-mQuit.ClickedCh:
					stop()
					systray.Quit()
					return
				case newLang := <-langChanged:
					// Measured before writing (T153 etapa 3c): AddMenuItem's
					// own doc says "can be safely invoked from different
					// goroutines" and SetTitle/SetTooltip route through the
					// identical update() path — safe to call here, off the
					// HTTP handler goroutine that sent newLang. Title only
					// actually moves on screen: read systray_windows.go's
					// addOrUpdateMenuItem — it never passes a tooltip to
					// Win32's SetMenuItemInfo, so SetTooltip on a menu item
					// is a silent no-op on THIS platform (native popup menus
					// don't support per-item tooltips at all). Called anyway
					// for when the tray ships on Linux/Mac (T113), where it
					// does render.
					mOpen.SetTitle(i18n.T(newLang, "server.tray_open_dashboard"))
					mOpen.SetTooltip(i18n.T(newLang, "server.tray_open_dashboard_tooltip"))
					if mAnother != nil {
						mAnother.SetTitle(i18n.T(newLang, "server.tray_open_another"))
						mAnother.SetTooltip(i18n.T(newLang, "server.tray_open_another_tooltip"))
					}
					mQuit.SetTitle(i18n.T(newLang, "server.tray_quit"))
					mQuit.SetTooltip(i18n.T(newLang, "server.tray_quit_tooltip"))
					mVersion.SetTooltip(i18n.T(newLang, "server.tray_version_tooltip"))
					if mAccount != nil {
						mAccount.SetTitle(i18n.T(newLang, "account.label", "account", account))
						mAccount.SetTooltip(i18n.T(newLang, "account.label", "account", account))
					}
				case <-ctx.Done():
					stop()
					systray.Quit()
					return
				}
			}
		}()
	}, func() { stop() })
}

// browserAppPath resolves exeName's full path via the Windows registry's App
// Paths key (T62, ct-2026-08-11-1527) — the actual source of truth for
// "where is msedge.exe", NOT the process PATH: Windows deliberately never
// puts browsers there, it registers them under
// SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\<exe>, the same key
// Explorer/"Run" itself resolves a bare exe name through. CURRENT_USER
// checked before LOCAL_MACHINE — Chrome installs per-user often enough that
// checking only LOCAL_MACHINE would miss it.
func browserAppPath(exeName string) (string, bool) {
	const appPathsKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\`
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		k, err := registry.OpenKey(root, appPathsKey+exeName, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		raw, _, err := k.GetStringValue("")
		k.Close()
		if err != nil || raw == "" {
			continue
		}
		if expanded, err := registry.ExpandString(raw); err == nil {
			return expanded, true
		}
		return raw, true
	}
	return "", false
}

// openAppWindow launches url as a chromeless "app window" — msedge first
// (ships with every modern Windows), chrome fallback, then the OS default
// browser as a last resort. Each Start() is fire-and-forget — the spawned
// browser outlives this process's own lifecycle checks, same as
// double-clicking a shortcut.
//
// T62 (ct-2026-08-11-1527, boss: "no me gusta que al querer verlo una
// ventana negra se abra, es como que en vez de abrir la web, usan un
// terminal para hacerlo") — this used to run exec.Command("msedge", ...)/
// ("chrome", ...) directly, which searches the process PATH. Since neither
// browser is ever on the PATH (see browserAppPath's doc), both Start() calls
// failed SILENTLY every single time, and every launch fell through to the
// third branch, which used to be `cmd /c start <url>` — a visible console
// window. The "app window" this function's own name promises had never
// opened once since it was written. Fixed at both ends: real paths from the
// registry instead of a PATH search, and a last resort that can't ever
// spawn a console — rundll32.exe (System32, which unlike browsers IS always
// on the PATH) delegates to the OS default browser via
// url.dll,FileProtocolHandler without a console window of its own.
func openAppWindow(url string) {
	for _, exe := range []string{"msedge.exe", "chrome.exe"} {
		path, ok := browserAppPath(exe)
		if !ok {
			continue
		}
		if err := exec.Command(path, "--app="+url).Start(); err == nil {
			return
		}
	}
	if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start(); err != nil {
		log.Printf("tray: open dashboard: %v", err)
	}
}
