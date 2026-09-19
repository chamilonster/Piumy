//go:build !windows

package i18n

import "os"

// detectSystemLang reads the POSIX locale env vars in their usual
// precedence (LC_ALL overrides LANG) — covers Linux and macOS (T113)
// without any OS-specific call.
func detectSystemLang() Lang {
	for _, key := range []string{"LC_ALL", "LANG"} {
		if v := os.Getenv(key); v != "" {
			return parseLangPrefix(v)
		}
	}
	return ES
}
