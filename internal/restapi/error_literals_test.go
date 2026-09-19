package restapi

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// errorLiteralPattern finds every `"error": "literal"` writeJSON sends —
// the API-error sink T160 (ct-2026-09-16-1828) classifies. err.Error(),
// i18n.T(...) calls and the `status` variable never match this (none of
// them open with a quote right after the colon), so they're excluded by
// construction, same as the contract says: they aren't literals.
var errorLiteralPattern = regexp.MustCompile(`"error":\s*"((?:[^"\\]|\\.)*)"`)

// errorLiteralExceptions is Class B of T160's A/B cut: error literals that
// do NOT go through the i18n catalog, on purpose, each with why. Two
// reasons appear here:
//
//   - "técnico" (the majority): internal state broken (nil dependency, "not
//     available") or an API contract only violated by calling the endpoint
//     directly (a required field the UI always sends, an enum value only a
//     <select> can produce) — Citrino's Class B: a developer reads it in a
//     bug report, a user never should have seen it, translating it would
//     make that report harder to search for, not easier.
//   - "ya traducido en otro lado" (three entries): app.js's login/recovery
//     .catch() handlers show their OWN fixed auth.* text regardless of what
//     this field says (verified in app.js: submitLogin, recover_submit,
//     requestRecoveryCode) — a server.* key for these would translate text
//     nobody sees and could drift from auth.* without anyone noticing.
//
// Class A (the literals NOT here) went through i18n.T(...) instead — see
// TestServerKeysMatchGoCallSites (internal/i18n/server_usage_test.go) for
// that side's own guard.
var errorLiteralExceptions = map[string]string{
	"store not available":         "técnico — dependencia interna sin inicializar, no algo que el usuario cause",
	"state not available":         "técnico — dependencia interna sin inicializar, no algo que el usuario cause",
	"whatsapp not available":      "técnico — cliente de WhatsApp sin inicializar, no algo que el usuario cause",
	"router not available":        "técnico — router.json sin cargar, no algo que el usuario cause",
	"gateway not available":       "técnico — adaptador de mensajería sin inicializar, no algo que el usuario cause",
	"events not available":        "técnico — event bus sin inicializar, no algo que el usuario cause",
	"media fetcher not available": "técnico — dependencia interna sin inicializar, no algo que el usuario cause",
	"streaming not supported":     "técnico — el ResponseWriter no implementa http.Flusher; no puede pasar en un deploy HTTP normal",

	"invalid JSON body":                           "técnico — contrato de API violado solo llamando el endpoint directo; el tablero siempre manda JSON válido",
	"jid is required":                             "técnico — contrato de API violado solo llamando el endpoint directo; el tablero siempre manda el jid",
	"chat_id is required":                         "técnico — contrato de API violado solo llamando el endpoint directo; el tablero siempre manda chat_id",
	"chat is required":                            "técnico — contrato de API violado solo llamando el endpoint directo; el tablero siempre manda chat",
	"chat and msg are required":                   "técnico — contrato de API violado solo llamando el endpoint directo; el tablero siempre manda ambos",
	"agent_id is required":                        "técnico — contrato de API violado solo llamando el endpoint directo; el tablero siempre manda agent_id",
	"data_url is required":                        "técnico — contrato de API violado solo llamando el endpoint directo; el input de archivo siempre produce un data_url",
	"chat_jid and a positive tokens are required": "técnico — endpoint de metering, nunca llamado desde app.js (ningún caller real en el tablero)",
	"agent_id, endpoint, antenna_terminal_id and pinpass are required": "técnico — app.js valida los 4 campos ANTES de mandar el POST (createBtn.onclick), este contrato solo se viola llamando el endpoint directo",

	"mode must be none|discretion|always":                "técnico — contrato de API, el valor sale de un <select> del tablero",
	"mode must be dedicated|auto":                        "técnico — contrato de API, el valor sale de un <select> del tablero",
	"level must be boss|auto|confirm|unattended|ignored": "técnico — contrato de API, el valor sale de un <select> del tablero",
	"level must be auto|confirm|unattended|ignored":      "técnico — contrato de API, el valor sale de un <select> del tablero",

	"no QR pending": "técnico — estado interno transitorio del polling de QR, servido como <img src>, nunca leído como texto por app.js",

	"media not found":               "técnico — inconsistencia interna (la DB dice descargado pero el archivo no está); servido como <img>/<video> src, nunca leído como texto por app.js",
	"media file not found on disk":  "técnico — inconsistencia interna (la DB dice descargado pero el archivo no está); servido como <img>/<video> src, nunca leído como texto por app.js",
	"no avatar cached":              "técnico — servido como <img src>, nunca leído como texto por app.js",
	"avatar file not found on disk": "técnico — servido como <img src>, nunca leído como texto por app.js",

	"invalid credentials":         "ya traducido en otro lado — submitLogin() en app.js ignora este campo y muestra siempre t(\"auth.invalid_credentials\")",
	"invalid or expired code":     "ya traducido en otro lado — recover_submit() en app.js ignora este campo y muestra siempre t(\"auth.invalid_or_expired_code\")",
	"unsupported recovery method": "ya traducido en otro lado — requestRecoveryCode() en app.js muestra el MISMO t(\"auth.code_sent_generic\") en éxito y en error, por diseño (no revelar si un método está configurado); además solo se llega acá llamando el endpoint directo, el tablero solo manda \"whatsapp\"/\"email\"",
}

// TestErrorLiteralsAreClassified is T160's own test (ct-2026-09-16-1828) —
// the reason 3b is the LAST pass over these errors, not one more area cut
// that could leave a gap the way the 2b-i..iv area cuts did (2b-v existed
// to fix exactly that for the frontend). It scans every .go file in this
// package BY PATTERN, not by a hand-kept list of call sites: every
// `"error": "literal"` writeJSON sends must be either Class A (gone through
// i18n.T — invisible to this regex, by construction) or declared here with
// a reason. A literal that is neither means someone added a new error
// message and forgot to classify it.
func TestErrorLiteralsAreClassified(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range errorLiteralPattern.FindAllSubmatch(data, -1) {
			found[string(m[1])] = true
		}
	}
	if len(found) == 0 {
		t.Fatal("ningún literal \"error\": \"...\" encontrado — el regex o el directorio están rotos, no el código")
	}
	for lit := range found {
		if _, ok := errorLiteralExceptions[lit]; !ok {
			t.Errorf("literal de error %q no está ni traducido (i18n.T) ni declarado en errorLiteralExceptions con motivo — clasificalo (T160, ct-2026-09-16-1828)", lit)
		}
	}
}

// TestErrorLiteralExceptionsHaveReasons is TestAllowedNonProseLiteralsHaveReasons's
// sibling for this list (prose_sink_test.go, internal/i18n) — a declared
// exception with an empty reason is indistinguishable from one nobody
// looked at.
func TestErrorLiteralExceptionsHaveReasons(t *testing.T) {
	for lit, reason := range errorLiteralExceptions {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("errorLiteralExceptions[%q] no tiene motivo escrito", lit)
		}
	}
}
