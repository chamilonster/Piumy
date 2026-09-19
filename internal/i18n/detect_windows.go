//go:build windows

package i18n

import "golang.org/x/sys/windows/registry"

// detectSystemLang reads the Windows UI locale straight from the registry
// (Control Panel\International\LocaleName, e.g. "es-AR", "en-US" — the same
// value Windows Settings > Idioma shows) instead of a kernel32 syscall: no
// new dependency, golang.org/x/sys/windows/registry is already imported by
// tray_windows.go.
func detectSystemLang() Lang {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Control Panel\International`, registry.QUERY_VALUE)
	if err != nil {
		return ES
	}
	defer k.Close()
	name, _, err := k.GetStringValue("LocaleName")
	if err != nil {
		return ES
	}
	return parseLangPrefix(name)
}
