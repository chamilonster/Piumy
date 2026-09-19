// Package i18n is Piumy's own text-catalog mechanism (T153, ct-2026-09-08-1656):
// one map[string]string per supported language, served to the dashboard by
// the backend so there is a single list of UI texts instead of two that can
// drift apart (the front-end never keeps its own copy — see restapi's
// GET /api/i18n). The actual text tables (esCatalog/enCatalog) live in
// catalog.go — this file is the mechanism (the Lang type, lookup, validation).
package i18n

import "regexp"

// Lang is a supported dashboard/UI language code (ISO 639-1, lowercase).
type Lang string

const (
	ES Lang = "es"
	EN Lang = "en"
)

// catalogs holds the full text map per language — the ONE list the
// dashboard reads from. See catalog.go for the actual entries.
var catalogs = map[Lang]map[string]string{
	ES: esCatalog,
	EN: enCatalog,
}

// Valid reports whether lang is one of the languages Piumy actually ships —
// used to reject a garbage manual choice from Opciones before it's persisted.
func Valid(lang Lang) bool {
	_, ok := catalogs[lang]
	return ok
}

// Catalog returns the text map for lang, falling back to Spanish — Piumy's
// own default — for anything not in catalogs.
func Catalog(lang Lang) map[string]string {
	if c, ok := catalogs[lang]; ok {
		return c
	}
	return catalogs[ES]
}

// Resolve turns a raw manual-language setting (store.KVGet(SettingLanguage)
// — this package stays a leaf and never imports store, so callers pass the
// raw string) into the Lang actually in effect: the manual choice wins when
// it names a language Piumy ships; empty (never chosen) or garbage falls
// through to Detect(), the OS locale. This is the ONE rule for "which
// language applies right now" — restapi.effectiveLang calls this instead of
// keeping its own copy (T153 etapa 3a, ct-2026-09-16-1803).
func Resolve(raw string) Lang {
	if lang := Lang(raw); raw != "" && Valid(lang) {
		return lang
	}
	return Detect()
}

// holePattern is the {name} placeholder T fills in — identical rule to
// app.js's t(key, vars) (see catalog.go's header: one template with the
// hole inside, never fragments concatenated, since word order isn't the
// same across languages).
var holePattern = regexp.MustCompile(`\{(\w+)\}`)

// T resolves key against lang's catalog and fills any {name} holes from
// vars, given as alternating name/value pairs (T(lang, key, "code", code))
// — the lightest shape for the common case of zero or one hole, no map
// literal needed at call sites. A hole with no matching name is left as-is;
// an unknown key returns "[key]", same fail-loud-and-visible rule as
// app.js's t().
func T(lang Lang, key string, vars ...string) string {
	template, ok := Catalog(lang)[key]
	if !ok {
		return "[" + key + "]"
	}
	if len(vars) == 0 {
		return template
	}
	return holePattern.ReplaceAllStringFunc(template, func(match string) string {
		name := match[1 : len(match)-1]
		for i := 0; i+1 < len(vars); i += 2 {
			if vars[i] == name {
				return vars[i+1]
			}
		}
		return match
	})
}
