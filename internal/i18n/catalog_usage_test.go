package i18n

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// tCallKeyPattern finds t("clave") / t("clave", {...}) calls in app.js.
// dataI18nKeyPattern finds data-i18n / data-i18n-placeholder / -title /
// -aria-label / -alt attribute values in index.html. Both patterns look
// only for the plain-ASCII key names app.js/index.html actually write —
// they never touch catalog.go's Go literal syntax, so the "two literal
// forms" trap (double-quoted vs. backtick raw strings for VALUES with
// embedded quotes, ct-2026-09-08-1656) that bit Citrino's own hand-run
// script doesn't apply here: this test reads esCatalog/enCatalog as
// already-compiled Go maps, never as source text.
var (
	tCallKeyPattern    = regexp.MustCompile(`\bt\(\s*"([a-zA-Z0-9_.]+)"`)
	dataI18nKeyPattern = regexp.MustCompile(`data-i18n(?:-[a-z-]+)?="([a-zA-Z0-9_.]+)"`)
)

// TestAllKeysMatchBetweenFrontendAndCatalog (T153 2b-iii, ct-2026-09-08-1656)
// is the repo-checked replacement for the manual cross-check Citrino ran by
// hand after each sub-pass — a script run once doesn't scale for the ~100
// keys still ahead across 2b-iii/iv. Fails if:
//   - a key referenced by t(...) in app.js or data-i18n* in index.html is
//     missing from either catalog (the exact failure mode that shows the
//     literal "[clave]" on screen instead of text), or
//   - a catalog key is never referenced anywhere (a leftover from a rename
//     or a deleted call site — the desync this whole catalog exists to
//     prevent, just the reverse direction).
//
// server.* is excluded from the "never referenced" direction (T153 etapa
// 3a, ct-2026-09-16-1803): those keys are texto que GENERA Go and reaches
// WhatsApp/email directly — no front-end t()/data-i18n* ever asks for them,
// by design, so this test would flag every one of them as dead. Their own
// guard is TestServerKeysMatchGoCallSites (server_usage_test.go), the
// Go-side mirror of this same test.
func TestAllKeysMatchBetweenFrontendAndCatalog(t *testing.T) {
	used := map[string]bool{}
	for _, path := range []string{
		"../dashboard/web/app.js",
		"../dashboard/web/index.html",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("leyendo %s: %v", path, err)
		}
		for _, m := range tCallKeyPattern.FindAllSubmatch(data, -1) {
			used[string(m[1])] = true
		}
		for _, m := range dataI18nKeyPattern.FindAllSubmatch(data, -1) {
			used[string(m[1])] = true
		}
	}
	if len(used) == 0 {
		t.Fatal("ninguna clave encontrada en app.js/index.html — la ruta o el regex están rotos, no el catálogo")
	}

	for k := range used {
		if _, ok := esCatalog[k]; !ok {
			t.Errorf("clave %q pedida por t()/data-i18n* pero falta en esCatalog", k)
		}
		if _, ok := enCatalog[k]; !ok {
			t.Errorf("clave %q pedida por t()/data-i18n* pero falta en enCatalog", k)
		}
	}
	for k := range esCatalog {
		if strings.HasPrefix(k, "server.") {
			continue
		}
		if !used[k] {
			t.Errorf("clave %q existe en esCatalog pero nadie la pide (ni t() ni data-i18n*)", k)
		}
	}
}
