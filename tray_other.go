//go:build !windows

package main

import (
	"context"
	"log"
	"os/exec"
	"runtime"

	"piumy-gateway/internal/i18n"
)

// runTrayOrWait is the non-Windows no-op: the tray (ct-2026-07-10-2312, F3)
// is Windows-desktop only. Same blocking contract as before it existed —
// wait for shutdown, nothing else. lang/langChanged match tray_windows.go's
// own signature (T153 etapa 3c, ct-2026-09-16-1854) — unused here, there's
// no menu to translate yet, but main.go calls this unconditionally and Go
// needs one signature for both build tags.
func runTrayOrWait(ctx context.Context, stop context.CancelFunc, dashboardURL string, lang i18n.Lang, langChanged <-chan i18n.Lang) {
	<-ctx.Done()
}

// openAppWindow launches url as a chromeless "app window" on Linux/macOS —
// misma idea que la versión de tray_windows.go, con la lista de candidatos
// propia de cada SO. El tray todavía no corre fuera de Windows (systray es
// Windows-only por ahora), pero la función queda lista para cuando lo haga:
// solo os/exec (stdlib), cero deps nuevas, CGO_ENABLED=0 intacto.
func openAppWindow(url string) {
	var candidates [][]string
	if runtime.GOOS == "darwin" {
		candidates = [][]string{
			{"open", "-na", "Google Chrome", "--args", "--app=" + url},
			{"open", "-na", "Microsoft Edge", "--args", "--app=" + url},
		}
	} else {
		candidates = [][]string{
			{"google-chrome", "--app=" + url},
			{"chromium-browser", "--app=" + url},
			{"chromium", "--app=" + url},
			{"brave-browser", "--app=" + url},
		}
	}
	for _, args := range candidates {
		if err := exec.Command(args[0], args[1:]...).Start(); err == nil {
			return
		}
	}
	fallback := []string{"xdg-open", url}
	if runtime.GOOS == "darwin" {
		fallback = []string{"open", url}
	}
	if err := exec.Command(fallback[0], fallback[1:]...).Start(); err != nil {
		log.Printf("tray: open dashboard: %v", err)
	}
}
