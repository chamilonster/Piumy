package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

// applyAccountFlag turns `--account <name>` into PIUMY_ACCOUNT (S4,
// ct-2026-09-23-1908) — the ONE input S1 already reads to decide the data
// dir, ports and identity. It exists because a .lnk can't set environment
// variables: the shortcut "Piumy (cuenta-2)" needs an argument instead. The
// flag wins over an existing PIUMY_ACCOUNT (a launcher that is itself a named
// account passes its own env down). Call BEFORE config.ApplyFileDefaults(),
// which already looks at PIUMY_ACCOUNT.
//
// Strict on purpose: an unknown flag or a stray argument is an error, not
// ignored — `Piumy.exe --acount x` quietly starting the DEFAULT account is
// worse than not starting. No args at all leaves the environment untouched:
// the running installation starts exactly as before.
func applyAccountFlag(args []string) error {
	fs := flag.NewFlagSet("piumy", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // windowsgui has no console; main logs the returned error
	account := fs.String("account", "", "named account to run (data, ports and identity apart from the default one)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("argumento inesperado %q", fs.Arg(0))
	}
	if *account == "" {
		return nil
	}
	return os.Setenv("PIUMY_ACCOUNT", *account)
}

// openDashboardAtStart: a named account whose WhatsApp isn't linked yet opens
// its dashboard by itself (S4) — a brand-new tray icon that stays silent and
// makes you know to click "Abrir dashboard" is half a product. Covers both
// first launch from the tray and reopening the .lnk before ever linking.
// It opens the SCREEN; the QR round still starts on the "Conectar QR" click
// (P2, ct-2026-07-24-0015: no wasted rounds). No account: never — the
// running installation is untouched.
func openDashboardAtStart(account string, paired bool) bool {
	return account != "" && !paired
}
