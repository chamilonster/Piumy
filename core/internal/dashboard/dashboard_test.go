// SPDX-License-Identifier: AGPL-3.0-only
package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"pimywa/internal/state"
	"pimywa/internal/store"
)

// TestLoginPrefersStoreOverride covers the DoD directly:
// reset_dashboard_password (mcpserver package) writes a new bcrypt
// hash to store.SettingDashPassHash; the login handler must use THAT hash
// over the one resolved once at startup (Config.PassHash) — otherwise a
// reset would silently do nothing until a restart, defeating the whole
// point of "the AI can reset it for you right now".
func TestLoginPrefersStoreOverride(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	startupHash, err := bcrypt.GenerateFromPassword([]byte("startup-password"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Username: "admin", PassHash: startupHash}
	deps := Deps{Store: st, State: state.NewManager(filepath.Join(dir, "status.json"), 8)}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := Handler(ctx, cfg, deps)

	login := func(password string) int {
		form := url.Values{"username": {"admin"}, "password": {password}}
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	// Before any reset: the startup password works, a not-yet-set one doesn't.
	if code := login("startup-password"); code != http.StatusSeeOther {
		t.Fatalf("startup password: status = %d, want 303", code)
	}
	if code := login("reset-password"); code != http.StatusUnauthorized {
		t.Fatalf("unset reset password: status = %d, want 401", code)
	}

	// Simulate reset_dashboard_password writing the override directly (same
	// call the MCP tool makes).
	resetHash, err := bcrypt.GenerateFromPassword([]byte("reset-password"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.KVSet(store.SettingDashPassHash, string(resetHash)); err != nil {
		t.Fatal(err)
	}

	// After the reset: the NEW password works, the OLD startup one no longer does.
	if code := login("reset-password"); code != http.StatusSeeOther {
		t.Fatalf("reset password after override: status = %d, want 303", code)
	}
	if code := login("startup-password"); code != http.StatusUnauthorized {
		t.Fatalf("startup password after override: status = %d, want 401 (override must win)", code)
	}
}
