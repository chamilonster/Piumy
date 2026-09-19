package restapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"piumy-gateway/internal/i18n"
)

type i18nResponse struct {
	Lang     string            `json:"lang"`
	Language string            `json:"language"`
	Texts    map[string]string `json:"texts"`
}

// TestSetLanguageRoundTrip (T153 etapa 1, ct-2026-09-08-1656) — GET /api/i18n
// reports both the raw override ("language") and the effective one ("lang");
// POST /api/admin/language is the only write side (no dedicated GET, it
// would just duplicate effectiveLang — see i18n.go's doc).
func TestSetLanguageRoundTrip(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var before i18nResponse
	getJSON(t, srv.URL+"/api/i18n", &before)
	if before.Language != "" {
		t.Errorf("language before setting = %q, want empty (nunca elegido)", before.Language)
	}

	resp := postJSON(t, srv.URL+"/api/admin/language", map[string]any{"language": "en"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST status = %d, want 200", resp.StatusCode)
	}

	var after i18nResponse
	getResp := getJSON(t, srv.URL+"/api/i18n", &after)
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", getResp.StatusCode)
	}
	if after.Language != "en" || after.Lang != "en" {
		t.Errorf("i18n after setting = %+v, want language=en lang=en", after)
	}
}

// TestSetLanguageRejectsUnsupported — un valor que no sea "es"/"en" (ni
// vacío, "nunca elegido") no debe persistirse.
func TestSetLanguageRejectsUnsupported(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/language", map[string]any{"language": "fr"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST status = %d, want 400", resp.StatusCode)
	}
}

// TestI18nEffectiveFallsBackToDetectWhenUnset — sin elección manual, GET
// /api/i18n reporta lo que i18n.Detect() resuelve (la elección manual, una
// vez guardada, siempre gana — probado en TestSetLanguageRoundTrip).
func TestI18nEffectiveFallsBackToDetectWhenUnset(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var got i18nResponse
	getJSON(t, srv.URL+"/api/i18n", &got)
	if got.Lang != string(i18n.Detect()) {
		t.Errorf("lang = %q, want %q (i18n.Detect() sin override)", got.Lang, i18n.Detect())
	}
	if got.Texts == nil {
		t.Error("texts no debería ser nil (etapa 1: catálogo vacío pero presente)")
	}
}

// TestSetLanguageNotifiesOnLanguageChanged (T153 etapa 3c, ct-2026-09-16-1854)
// is the tray's only way to hear about a language change without a restart
// — POST /api/admin/language must call the hook with the newly EFFECTIVE
// language (not the raw body), same value GET /api/i18n's "lang" would
// report right after.
func TestSetLanguageNotifiesOnLanguageChanged(t *testing.T) {
	st := newTestStore(t)
	var got i18n.Lang
	calls := 0
	srv := httptest.NewServer(NewMux(Deps{Store: st, OnLanguageChanged: func(lang i18n.Lang) {
		calls++
		got = lang
	}}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/language", map[string]any{"language": "en"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST status = %d, want 200", resp.StatusCode)
	}
	if calls != 1 {
		t.Fatalf("OnLanguageChanged called %d times, want exactly 1", calls)
	}
	if got != i18n.EN {
		t.Errorf("OnLanguageChanged got %q, want %q", got, i18n.EN)
	}
}

// TestSetLanguageWithoutHookDoesNotPanic — nil OnLanguageChanged (every
// existing Deps literal before this contract) must keep working exactly as
// before; the hook is optional, same convention as Bus/Store/etc.
func TestSetLanguageWithoutHookDoesNotPanic(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/language", map[string]any{"language": "en"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST status = %d, want 200", resp.StatusCode)
	}
}

// TestGetI18nServedWithoutAuth (T153 2b-ii, ct-2026-09-08-1656) fija el
// bug encontrado probando login con PIUMY_REST_KEY puesta (el despliegue
// real): con APIKey configurada, un GET a /api/i18n SIN key ni sesión
// tiene que seguir dando 200 con los textos — es lo que necesita la
// pantalla de login para pintarse, antes de que exista una sesión. Si
// alguien vuelve a poner este GET atrás de auth(), este test es el
// primero en romperse — antes de que un usuario real vea "[clave]" en
// pantalla en vez de texto.
func TestGetI18nServedWithoutAuth(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st, APIKey: "secret-key-set-like-a-real-install"}))
	defer srv.Close()

	var got i18nResponse
	resp := getJSON(t, srv.URL+"/api/i18n", &got)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/i18n sin key con APIKey configurada = %d, want 200", resp.StatusCode)
	}
	if len(got.Texts) == 0 {
		t.Error("texts vino vacío — la pantalla de login no tendría con qué pintarse")
	}
}
