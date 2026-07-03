// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
//
// Package dashboard: lightweight LAN-only web dashboard served by the same
// binary as the switchboard core. Vanilla HTML + JS via go:embed, no build
// step, no external CDN. Total footprint: a few KB of HTML/CSS/JS.
//
// Auth model: username + bcrypt password (HttpOnly session cookie, SameSite=Lax).
// All /api/* calls from the UI go to port 80 (same origin), so no CORS issues.
// Secrets are never exposed in any response.
package dashboard

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"pimywa/internal/governor"
	"pimywa/internal/restapi"
	"pimywa/internal/router"
	"pimywa/internal/state"
	"pimywa/internal/store"
)

//go:embed index.html
var indexHTML []byte

const (
	cookieName   = "pimywa_session"
	sessionTTL   = 12 * time.Hour
	cookieMaxAge = 43200 // 12 hours in seconds
	gcInterval   = 30 * time.Minute
)

// Deps holds in-process dependencies the dashboard shares with the switchboard.
type Deps struct {
	Store     *store.Store
	State     *state.Manager
	Gov       *governor.Limiter
	GWCtrl    restapi.GatewayController // optional; forwarded to the REST API layer
	RouterMgr *router.Manager           // optional; forwarded to the REST API layer for runtime whitelist edits

	// BatteryLogFile is the discharge/charge trace CSV path; forwarded to
	// the REST API layer for GET /api/battery/log.
	BatteryLogFile string
}

// Config holds dashboard-specific authentication configuration.
// Addr and credential fields come from environment variables via config.Load.
type Config struct {
	Addr     string
	Username string
	PassHash []byte // bcrypt hash ready to use
}

// ResolvePassHash derives the bcrypt hash to use at startup, in priority order:
//
//  1. passHash non-empty → treat as bcrypt string, use as-is.
//  2. pass non-empty → hash it with bcrypt.DefaultCost.
//  3. Both empty → generate a random 24-char password, log it once (journalctl
//     will show it on first run), and return the hash.
//
// Call this once from main; the result is stored in Config.PassHash.
func ResolvePassHash(passHash, pass string) ([]byte, error) {
	if passHash != "" {
		// Validate that it's a valid bcrypt hash by parsing.
		_, err := bcrypt.Cost([]byte(passHash))
		if err != nil {
			return nil, err
		}
		return []byte(passHash), nil
	}
	if pass != "" {
		return bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	}
	// No password configured — generate a random one.
	pwd, err := GenerateRandomPassword()
	if err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pwd), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	log.Printf("Dashboard: no password set — generated random password: %s  (shown once; set PIMYWA_DASH_PASS to pin it)", pwd)
	return hash, nil
}

// GenerateRandomPassword returns a random 24-hex-char password (12 random
// bytes) — the startup fallback above and the owner-scoped MCP password-
// reset tool (mcpserver package) both call this so a reset password is
// exactly as strong as a fresh-install one.
func GenerateRandomPassword() (string, error) {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// ── Session store ─────────────────────────────────────────────────────────────

type sessionStore struct {
	mu   sync.Mutex
	data map[string]time.Time // token → expiry
}

func newSessionStore() *sessionStore {
	return &sessionStore{data: make(map[string]time.Time)}
}

func (ss *sessionStore) create() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	ss.mu.Lock()
	ss.data[token] = time.Now().Add(sessionTTL)
	ss.mu.Unlock()
	return token, nil
}

// valid checks whether a session token exists and has not expired.
// Expired tokens are lazily removed on access.
func (ss *sessionStore) valid(token string) bool {
	if token == "" {
		return false
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	exp, ok := ss.data[token]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(ss.data, token)
		return false
	}
	return true
}

func (ss *sessionStore) delete(token string) {
	ss.mu.Lock()
	delete(ss.data, token)
	ss.mu.Unlock()
}

func (ss *sessionStore) gc() {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	now := time.Now()
	for k, exp := range ss.data {
		if now.After(exp) {
			delete(ss.data, k)
		}
	}
}

// ── Handler ───────────────────────────────────────────────────────────────────

// Handler returns the HTTP handler for the dashboard listener.
// A background GC goroutine runs until ctx is cancelled.
func Handler(ctx context.Context, cfg Config, deps Deps) http.Handler {
	ss := newSessionStore()

	// Periodic GC of expired sessions.
	go func() {
		t := time.NewTicker(gcInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				ss.gc()
			}
		}
	}()

	// REST API handler with no API-key auth (the session cookie gates it here).
	apiH := restapi.Handler(restapi.Deps{
		Store:          deps.Store,
		State:          deps.State,
		Gov:            deps.Gov,
		GWCtrl:         deps.GWCtrl,
		RouterMgr:      deps.RouterMgr,
		BatteryLogFile: deps.BatteryLogFile,
		APIKey:         "", // dashboard session auth is sufficient
	})

	mux := http.NewServeMux()

	// ── Unauthenticated endpoints ─────────────────────────────────────────
	mux.HandleFunc("GET /login", loginPage)
	mux.HandleFunc("POST /login", makeLoginHandler(cfg, deps.Store, ss))
	mux.HandleFunc("/logout", makeLogoutHandler(ss)) // GET + POST

	// ── Authenticated endpoints ───────────────────────────────────────────
	auth := makeAuthMiddleware(ss)

	// Dashboard UI: exact root only; everything else 404s cleanly.
	mux.Handle("GET /{$}", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(indexHTML)
	})))

	// All /api/* calls go through session auth then to the REST handler.
	mux.Handle("/api/", auth(apiH))

	return mux
}

// makeAuthMiddleware checks the session cookie.
// /api/* requests get a 401 JSON on failure; all other paths redirect to /login.
func makeAuthMiddleware(ss *sessionStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := r.Cookie(cookieName)
			if err != nil || !ss.valid(c.Value) {
				if strings.HasPrefix(r.URL.Path, "/api/") {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
					return
				}
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ── Login page ────────────────────────────────────────────────────────────────

// loginPageHTML is inlined to avoid a second embedded file for a tiny form.
const loginPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Piumy – Login</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{background:#0f1117;color:#e8eaed;font:16px/1.5 system-ui,sans-serif;
     display:flex;align-items:center;justify-content:center;min-height:100vh}
.card{background:#1a1d24;border:1px solid #2d3142;border-radius:12px;padding:2rem;
      width:100%;max-width:360px}
.owl{text-align:center;font-size:2.5rem;margin-bottom:.5rem;line-height:1}
h1{font-size:1.4rem;margin-bottom:1.5rem;text-align:center;font-weight:700}
label{display:block;font-size:.82rem;color:#9aa0ac;margin-bottom:.25rem}
input{width:100%;padding:.6rem .75rem;background:#0f1117;border:1px solid #2d3142;
      border-radius:6px;color:#e8eaed;font-size:1rem;margin-bottom:1rem}
input:focus{outline:none;border-color:#4ade80}
button{width:100%;padding:.7rem;background:#4ade80;color:#0f1117;border:none;
       border-radius:6px;font-size:1rem;font-weight:700;cursor:pointer}
button:hover{background:#22c55e}
.err{color:#f87171;text-align:center;margin-top:.75rem;font-size:.88rem}
</style>
</head>
<body>
<div class="card">
  <div class="owl">🦉</div>
  <h1>Piumy</h1>
  <form method="POST" action="/login">
    <label for="u">Username</label>
    <input id="u" name="username" type="text" autocomplete="username" required autofocus>
    <label for="p">Password</label>
    <input id="p" name="password" type="password" autocomplete="current-password" required>
    <button type="submit">Sign in</button>
    {{ERROR}}
  </form>
</div>
</body>
</html>`

func loginPage(w http.ResponseWriter, r *http.Request) {
	renderLogin(w, http.StatusOK, "")
}

func renderLogin(w http.ResponseWriter, code int, errMsg string) {
	errHTML := ""
	if errMsg != "" {
		errHTML = `<p class="err">` + errMsg + `</p>`
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(strings.ReplaceAll(loginPageHTML, "{{ERROR}}", errHTML)))
}

func makeLoginHandler(cfg Config, st *store.Store, ss *sessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			renderLogin(w, http.StatusBadRequest, "Invalid form submission.")
			return
		}
		username := r.FormValue("username")
		password := r.FormValue("password")

		// A password RESET via the owner-scoped MCP tool
		// (reset_dashboard_password) writes a new hash to the store; that
		// override always wins over the hash
		// resolved once at startup (cfg.PassHash from PIMYWA_DASH_PASS/
		// _HASH) — otherwise a reset would have no effect until a restart.
		passHash := cfg.PassHash
		if st != nil {
			if override, err := st.KVGet(store.SettingDashPassHash); err == nil && override != "" {
				passHash = []byte(override)
			}
		}

		// Both checks run regardless of each other to avoid timing side-channels.
		userOK := subtle.ConstantTimeCompare([]byte(username), []byte(cfg.Username)) == 1
		passErr := bcrypt.CompareHashAndPassword(passHash, []byte(password))

		if !userOK || passErr != nil {
			renderLogin(w, http.StatusUnauthorized, "Invalid credentials.")
			return
		}

		token, err := ss.create()
		if err != nil {
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     cookieName,
			Value:    token,
			Path:     "/",
			MaxAge:   cookieMaxAge,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

func makeLogoutHandler(ss *sessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(cookieName); err == nil {
			ss.delete(c.Value)
		}
		http.SetCookie(w, &http.Cookie{
			Name:   cookieName,
			Value:  "",
			Path:   "/",
			MaxAge: -1,
		})
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}
