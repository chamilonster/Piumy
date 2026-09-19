// The i18n mechanism's REST surface (T153 etapa 1, ct-2026-09-08-1656): one
// endpoint the dashboard bootstraps its texts AND the manual override from
// (GET /api/i18n) plus the write side for Opciones (POST /api/admin/language)
// — same plain-string pattern as identity (admin.go).
package restapi

import (
	"net/http"

	"piumy-gateway/internal/i18n"
	"piumy-gateway/internal/store"
)

// GET /api/i18n is served WITHOUT the auth gate — same reasoning and same
// precedent as the static dashboard shell (dashboard.go): it carries zero
// secrets (136 UI strings, compiled into the binary, identical on every
// install — no owner data, no credentials), and the LOGIN SCREEN ITSELF is
// what needs it, before any session exists. T153 2b-ii (ct-2026-09-08-1656)
// found this the hard way: with GET /api/i18n behind auth(), loadI18n()
// 401s pre-login and never retries, so state.i18n.texts stays empty for
// that whole page load — every t(key) the login/recover screen calls (the
// ONLY t() call sites that can fire before a session exists) rendered the
// literal "[key]" instead of text, in every language, on any real install
// (PIUMY_REST_KEY set — the actual deploy mode, not the empty-key dev
// convenience the test suite and manual QA had both been using). If you're
// tempted to "secure" this GET later: don't — that reintroduces this exact
// bug. TestGetI18nServedWithoutAuth (i18n_test.go) fails first if you do.
// POST /api/admin/language stays behind auth() — that's a WRITE, not a
// read of static text.
func (d Deps) registerI18nRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/i18n", d.handleGetI18n)
	mux.HandleFunc("POST /api/admin/language", d.auth(d.handleSetLanguage))
}

// effectiveLang resolves the language actually in effect: the manual
// override in Opciones always wins; only when the boss never touched it
// (or the stored value isn't one Piumy ships) does the OS locale decide.
// i18n.Resolve is the one implementation — T153 etapa 3a (ct-2026-09-16-1803)
// moved this rule there so the same code decides for the dashboard and for
// the server-generated notices in internal/capipush and this package's own
// recover.go.
//
// nil st falls straight to Detect(): T153 etapa 3b (ct-2026-09-16-1828) adds
// a call site inside auth() (restapi.go), the one gate that runs before any
// handler's own "d.Store == nil" guard — TestEventsRequiresAPIKeyWhenSet
// hits exactly that path with Deps{Store: nil}, so this can't assume a
// non-nil Store the way every other caller here safely does.
func effectiveLang(st *store.Store) i18n.Lang {
	if st == nil {
		return i18n.Detect()
	}
	v, _ := st.KVGet(store.SettingLanguage)
	return i18n.Resolve(v)
}

// handleGetI18n serves the ONE text catalog the dashboard bootstraps from —
// no second copy for the front-end to keep in sync, per T153's design.
// "language" is the raw manual override (empty = never chosen) so Opciones'
// selector can show it without a second endpoint duplicating effectiveLang.
func (d Deps) handleGetI18n(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	rawLang, err := d.Store.KVGet(store.SettingLanguage)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	lang := effectiveLang(d.Store)
	writeJSON(w, http.StatusOK, map[string]any{
		"lang":     string(lang),
		"language": rawLang,
		"texts":    i18n.Catalog(lang),
	})
}

func (d Deps) handleSetLanguage(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		Language string `json:"language"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Language != "" && !i18n.Valid(i18n.Lang(body.Language)) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(effectiveLang(d.Store), "server.unsupported_language")})
		return
	}
	if err := d.Store.KVSet(store.SettingLanguage, body.Language); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// T153 etapa 3c (ct-2026-09-16-1854): the dashboard picks this up on its
	// own (GET /api/i18n, no F5 needed) but the tray menu is built once
	// inside systray.Run — without this nudge it would keep showing the old
	// language until Piumy restarts.
	if d.OnLanguageChanged != nil {
		d.OnLanguageChanged(effectiveLang(d.Store))
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
