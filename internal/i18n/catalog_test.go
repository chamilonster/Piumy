package i18n

import "testing"

// TestCatalogsAreComplete (T153 etapa 2a) guards against the two mistakes
// that are easy to make hand-writing ~115 parallel entries: a key added to
// one language's map and forgotten in the other, or a value left empty.
// Neither would crash anything (applyI18n's `if (text)` guard just leaves
// the original text in place for a missing/empty entry) — they'd silently
// leave one spot untranslated, much harder to notice than a red test.
func TestCatalogsAreComplete(t *testing.T) {
	seen := map[string]bool{}
	for k := range esCatalog {
		seen[k] = true
	}
	for k := range enCatalog {
		seen[k] = true
	}
	for k := range seen {
		es, okES := esCatalog[k]
		en, okEN := enCatalog[k]
		if !okES {
			t.Errorf("key %q is in enCatalog but missing from esCatalog", k)
		} else if es == "" {
			t.Errorf("esCatalog[%q] is empty", k)
		}
		if !okEN {
			t.Errorf("key %q is in esCatalog but missing from enCatalog", k)
		} else if en == "" {
			t.Errorf("enCatalog[%q] is empty", k)
		}
	}
}
