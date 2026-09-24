// Dashboard login (ct-2026-07-19-1616, S1d) — SEGURIDAD, no negociable:
// bcrypt for the password, an HMAC-signed HttpOnly session cookie for the
// human via the browser, layered ALONGSIDE the existing X-API-Key gate
// (restapi.go's auth()) which keeps working unchanged for programmatic
// callers (MCP/curl) — Deps.APIKey=="" still means fully open, same as
// before this ticket. Recovery (WhatsApp+email) is S1e, a separate
// subcontrat; this file is only login + session + change password.
package restapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"piumy-gateway/internal/config"
	"piumy-gateway/internal/i18n"
	"piumy-gateway/internal/store"
)

const (
	dashboardUsername        = "admin"
	dashboardDefaultPassword = "piumy"
	sessionCookieBase        = "piumy_session"
	sessionTTL               = 30 * 24 * time.Hour

	// dashboardPasswordSeedEnv (Windows installer, ct-2026-07-31-1643) — the
	// installer's ONE-SHOT seed for the very first boot, never written to
	// the permanent launch script (only present in the process env of that
	// first run — the installer passes it directly when it starts the
	// program the first time). Not a general override: passHash below only
	// ever reads it while no hash exists yet, closing the specific gap of a
	// BRAND NEW install ever landing on the factory admin/piumy — existing
	// installs (including the boss's own) that already have a hash keep
	// working exactly as before, untouched.
	dashboardPasswordSeedEnv = "PIUMY_DASHBOARD_PASSWORD"
)

func (d Deps) registerAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/login", d.handleLogin)
	mux.HandleFunc("POST /api/admin/password", d.auth(d.handleChangePassword))
}

// passHash returns the current bcrypt hash, seeding it on first use —
// self-healing, no separate startup step needed (mirrors
// reset_dashboard_password's own bcrypt usage). SEED-ONLY (ct-2026-07-31-
// 1643): an EXISTING hash is never overwritten by dashboardPasswordSeedEnv
// — only the empty-hash (never-set-yet) case ever looks at it. Letting the
// env var overwrite an existing hash would let anyone who can read the
// startup script change the password without knowing the current one,
// bypassing handleChangePassword's whole point. If the env var is empty
// AND no hash exists yet, the behavior is UNCHANGED from before this
// contract: seeds the documented default (admin/piumy) — this is not
// becoming an error, existing installs (the boss's included) depend on
// that path continuing to work for a chat that already has no hash yet.
func passHash(st *store.Store) (string, error) {
	v, err := st.KVGet(store.SettingDashPassHash)
	if err != nil {
		return "", err
	}
	if v != "" {
		// Sin log acá (R11, hallazgo de Peridot): passHash corre en CADA
		// request autenticado y el tablero hace poll cada ~15s, así que
		// esta línea sola era ~200 de 522 líneas del log real — el 40% del
		// archivo diciendo "todo normal". Enterraba lo único por lo que
		// existe el log (canal caído, debounce, despachos). El camino que
		// SÍ vale la pena anunciar es el de abajo: cuando se siembra un
		// hash nuevo, que pasa una vez.
		return v, nil
	}
	password, source := dashboardDefaultPassword, "el default de fábrica (admin/piumy)"
	if seed := os.Getenv(dashboardPasswordSeedEnv); seed != "" {
		password, source = seed, dashboardPasswordSeedEnv+" (siembra del instalador, primer arranque)"
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	if err := st.KVSet(store.SettingDashPassHash, string(hash)); err != nil {
		return "", err
	}
	log.Printf("restapi: dashboard password: sembrado desde %s", source)
	return string(hash), nil
}

// SeedDashPassHashFromEnv gives a NEW account the login of the Piumy it was
// opened from (S5, ct-2026-09-23-2038): without it every new account started
// on the factory admin/piumy and no screen said so. Same seed-only shape as
// SeedRecoveryEmailFromEnv — an existing hash is never overwritten.
//
// It runs eagerly at boot, not lazily inside passHash, so the env var can
// leave the process environment whether or not it was used: a lazy seed would
// never read (or clear) it when the DB already has a hash, and every child of
// this Piumy — the browser openAppWindow starts included — would inherit it.
// Running before passHash also makes it win over an inherited
// PIUMY_DASHBOARD_PASSWORD. The value is never logged, and one that isn't a
// bcrypt hash is ignored rather than written as a login nobody can pass.
func SeedDashPassHashFromEnv(st *store.Store) error {
	seed := os.Getenv(config.DashHashSeedEnv)
	if seed == "" {
		return nil
	}
	os.Unsetenv(config.DashHashSeedEnv)
	existing, err := st.KVGet(store.SettingDashPassHash)
	if err != nil {
		return err
	}
	if existing != "" {
		return nil
	}
	if _, err := bcrypt.Cost([]byte(seed)); err != nil {
		log.Printf("restapi: clave del tablero: %s no trae un hash válido, se ignora", config.DashHashSeedEnv)
		return nil
	}
	if err := st.KVSet(store.SettingDashPassHash, seed); err != nil {
		return err
	}
	log.Println("restapi: clave del tablero: heredada de la cuenta desde la que se abrió este Piumy")
	return nil
}

// isFactoryPassword reports whether the dashboard's current password still
// matches the factory default ("piumy") — bcrypt.CompareHashAndPassword
// against the stored hash, entirely server-side; the password itself never
// leaves this function, let alone the process (T9, ct-2026-08-05-1137: T8
// made a silent-install fall back to this exact default, and it's public —
// it's in the installer and the open-source code). Reuses passHash instead
// of reading+seeding the hash a second way — Citrino's own call on T8: two
// sources for the same default is how they drift apart.
func isFactoryPassword(st *store.Store) (bool, error) {
	hash, err := passHash(st)
	if err != nil {
		return false, err
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(dashboardDefaultPassword)) == nil, nil
}

// sessionSecret returns the current HMAC secret, lazily generating one via
// the same rotation path a password change uses (store.RotateDashSessionSecret).
func sessionSecret(st *store.Store) ([]byte, error) {
	v, err := st.KVGet(store.SettingDashSessionSecret)
	if err != nil {
		return nil, err
	}
	if v == "" {
		if err := st.RotateDashSessionSecret(); err != nil {
			return nil, err
		}
		v, err = st.KVGet(store.SettingDashSessionSecret)
		if err != nil {
			return nil, err
		}
	}
	return base64.StdEncoding.DecodeString(v)
}

// signSession HMAC-signs an expiry timestamp — the payload carries no
// identity (single hardcoded admin user, nothing else to encode) and no
// server-side session table: verifying the signature IS verifying the
// session, and rotating the secret is what "log everyone out" means here.
func signSession(secret []byte, expiry time.Time) string {
	payload := strconv.FormatInt(expiry.Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	sig := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func verifySession(secret []byte, token string) bool {
	payloadPart, sigPart, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	payloadRaw, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return false
	}
	sigGiven, err := base64.RawURLEncoding.DecodeString(sigPart)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(payloadRaw)
	if !hmac.Equal(sigGiven, mac.Sum(nil)) {
		return false
	}
	expUnix, err := strconv.ParseInt(string(payloadRaw), 10, 64)
	if err != nil {
		return false
	}
	return time.Now().Unix() < expUnix
}

// sessionCookieName is the cookie an account keeps its session in (S5,
// ct-2026-09-23-2038). Browsers key cookies by host, not port, so two Piumy on
// localhost share one jar: with a single name each login overwrote the other
// account's cookie and signing in to B kicked A out (measured: A 200, B 200,
// A again 401). A named account adds 8 hex of sha256(account) — the id itself
// can't go in the name: accountSlug lets spaces, ";", "=" and unicode through,
// and a cookie name takes none of them. No account keeps the original name, so
// the live install's sessions survive this change.
func sessionCookieName(account string) string {
	if account == "" {
		return sessionCookieBase
	}
	sum := sha256.Sum256([]byte(account))
	return sessionCookieBase + "_" + hex.EncodeToString(sum[:4])
}

// validSession is the alternate credential auth() checks alongside
// X-API-Key — a valid signed cookie is enough, no key needed.
func (d Deps) validSession(r *http.Request) bool {
	if d.Store == nil {
		return false
	}
	c, err := r.Cookie(sessionCookieName(d.Account))
	if err != nil {
		return false
	}
	secret, err := sessionSecret(d.Store)
	if err != nil {
		return false
	}
	return verifySession(secret, c.Value)
}

func (d Deps) setSessionCookie(w http.ResponseWriter, secret []byte) {
	expiry := time.Now().Add(sessionTTL)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName(d.Account),
		Value:    signSession(secret, expiry),
		Path:     "/",
		Expires:  expiry,
		HttpOnly: true,
		// ponytail: Secure omitted (false) — this gateway serves plain HTTP
		// on the LAN (no TLS termination anywhere in main.go); a Secure
		// cookie would never be sent and login would silently never work.
		SameSite: http.SameSiteStrictMode,
	})
}

// handleLogin never distinguishes "wrong user" from "wrong password" — the
// username is hardcoded (single admin account, no user table), so any
// other username fails the exact same bcrypt-compare-shaped path as a wrong
// password: nothing for a caller to enumerate.
func (d Deps) handleLogin(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	hash, err := passHash(d.Store)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if body.Username != dashboardUsername || bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)) != nil {
		// "invalid credentials" stays a bare literal on purpose (T153 etapa
		// 3b, ct-2026-09-16-1828): app.js's submitLogin() never reads this
		// field — its .catch() unconditionally shows t("auth.invalid_credentials")
		// regardless of what the response says. A server.* key here would
		// translate text nobody sees and could drift from auth.invalid_credentials
		// without anyone noticing. See error_literals_test.go's exception list.
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	secret, err := sessionSecret(d.Store)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	d.setSessionCookie(w, secret)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleChangePassword requires the CURRENT password regardless of which
// door auth() let the caller through (session or X-API-Key) — knowing the
// API key alone doesn't let you silently take over the dashboard login.
// Rotates the session secret on success: every existing browser session,
// including the one that just changed it, must log in again.
func (d Deps) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !decode(w, r, &body) {
		return
	}
	hash, err := passHash(d.Store)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.CurrentPassword)) != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": i18n.T(effectiveLang(d.Store), "server.current_password_incorrect")})
		return
	}
	if len(body.NewPassword) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(effectiveLang(d.Store), "server.new_password_required")})
		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := d.Store.KVSet(store.SettingDashPassHash, string(newHash)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := d.Store.RotateDashSessionSecret(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "password changed"})
}
