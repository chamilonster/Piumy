package i18n

import "strings"

// Detect reads the machine's own locale — the OS running Piumy, not the
// browser's — and maps it to a supported Lang. detectSystemLang is
// implemented per-OS (detect_windows.go/detect_other.go), same split as the
// root package's tray_windows.go/tray_other.go, so Piumy on Mac/Linux (T113)
// isn't left detecting nothing.
func Detect() Lang {
	return detectSystemLang()
}

// parseLangPrefix extracts the language subtag from a locale string
// ("es-AR", "en_US.UTF-8", "pt-BR") and maps it to a supported Lang,
// falling back to Spanish for anything it doesn't recognize (T153: "si no
// lo reconoce, cae en español").
func parseLangPrefix(locale string) Lang {
	s := strings.ToLower(locale)
	if i := strings.IndexAny(s, "-_."); i >= 0 {
		s = s[:i]
	}
	if Lang(s) == EN {
		return EN
	}
	return ES
}
