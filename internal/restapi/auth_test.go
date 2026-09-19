package restapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"piumy-gateway/internal/store"
)

// clientWithJar returns an http.Client that carries cookies across
// requests — needed to exercise the login -> session -> gated-endpoint
// flow the same way a real browser would.
func clientWithJar(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

func loginAs(t *testing.T, client *http.Client, base, username, password string) *http.Response {
	t.Helper()
	resp, err := client.Post(base+"/api/auth/login", "application/json", jsonBody(t, map[string]any{"username": username, "password": password}))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func jsonBody(t *testing.T, v any) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(b)
}

func TestLoginDefaultCredentials(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	client := clientWithJar(t)
	resp := loginAs(t, client, srv.URL, "admin", "piumy")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 on default admin/piumy", resp.StatusCode)
	}
	if len(resp.Cookies()) == 0 {
		t.Fatal("no Set-Cookie on successful login")
	}
	c := resp.Cookies()[0]
	if c.Name != sessionCookieName || !c.HttpOnly {
		t.Errorf("cookie = %+v, want HttpOnly %s", c, sessionCookieName)
	}
}

// TestPassHashSeedsFromEnvOnFirstBoot (Windows installer, ct-2026-07-31-
// 1643): a brand new store with PIUMY_DASHBOARD_PASSWORD set seeds THAT
// password, not the factory default — a fresh install never lands on
// admin/piumy when the installer passed a seed.
func TestPassHashSeedsFromEnvOnFirstBoot(t *testing.T) {
	t.Setenv(dashboardPasswordSeedEnv, "correcto-caballo-batería-grapadora")
	st := newTestStore(t)

	hash, err := passHash(st)
	if err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("correcto-caballo-batería-grapadora")) != nil {
		t.Error("passHash did not seed from PIUMY_DASHBOARD_PASSWORD")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(dashboardDefaultPassword)) == nil {
		t.Error("passHash seeded the factory default even though PIUMY_DASHBOARD_PASSWORD was set")
	}
}

// TestPassHashIgnoresEnvSeedWhenHashAlreadyExists is the precision that
// makes this safe: the env var is SEED-ONLY. An install that already has a
// hash (the boss's own, or any other already-running instance) must never
// have its password silently replaced by whatever happens to be in the
// process env — that would let anyone who can read the startup script
// change the password without knowing the current one.
func TestPassHashIgnoresEnvSeedWhenHashAlreadyExists(t *testing.T) {
	st := newTestStore(t)
	// First boot, no seed — lands on the factory default, same as any
	// existing install today.
	if _, err := passHash(st); err != nil {
		t.Fatal(err)
	}

	t.Setenv(dashboardPasswordSeedEnv, "no-deberia-pisar-nada")
	hash, err := passHash(st)
	if err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(dashboardDefaultPassword)) != nil {
		t.Error("passHash changed an EXISTING hash from the env seed — must be seed-only")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("no-deberia-pisar-nada")) == nil {
		t.Error("passHash's env seed overwrote an existing hash")
	}
}

// TestIsFactoryPasswordTrueOnFreshInstall is T9 (ct-2026-08-05-1137): a
// brand new store, never logged in and never given PIUMY_DASHBOARD_PASSWORD
// (T8's silent-install fallback, or simply a fresh interactive install that
// hasn't changed the password yet) lands on the factory default — the
// dashboard alarm must fire for it.
func TestIsFactoryPasswordTrueOnFreshInstall(t *testing.T) {
	st := newTestStore(t)
	factory, err := isFactoryPassword(st)
	if err != nil {
		t.Fatal(err)
	}
	if !factory {
		t.Error("isFactoryPassword = false on a fresh store, want true (never changed from piumy)")
	}
}

// TestIsFactoryPasswordFalseAfterChange: the whole point of the alarm is
// that it goes away once the owner acts — changing the password must flip
// this to false.
func TestIsFactoryPasswordFalseAfterChange(t *testing.T) {
	st := newTestStore(t)
	if _, err := passHash(st); err != nil { // seed the factory default first
		t.Fatal(err)
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte("una-clave-elegida-por-el-dueño"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.KVSet(store.SettingDashPassHash, string(newHash)); err != nil {
		t.Fatal(err)
	}

	factory, err := isFactoryPassword(st)
	if err != nil {
		t.Fatal(err)
	}
	if factory {
		t.Error("isFactoryPassword = true after changing the password, want false")
	}
}

// TestIsFactoryPasswordFalseWithEnvSeed: T8's silent install with
// /DASHBOARDPASSWORD= given seeds THAT password, not "piumy" — the alarm
// must not fire for an install that was never on the factory default to
// begin with.
func TestIsFactoryPasswordFalseWithEnvSeed(t *testing.T) {
	t.Setenv(dashboardPasswordSeedEnv, "correcto-caballo-batería-grapadora")
	st := newTestStore(t)

	factory, err := isFactoryPassword(st)
	if err != nil {
		t.Fatal(err)
	}
	if factory {
		t.Error("isFactoryPassword = true with a non-factory env seed, want false")
	}
}

// TestFactoryPasswordAlarmIgnoresEnvSeedWhenOwnerAlreadyChangedIt is T25
// (ct-2026-08-05-1833, hallazgo 3): the smoke saw factory_password=true on
// an install where the owner's OWN password was preserved (login with
// "piumy" correctly failed) — Citrino's suspicion was PIUMY_DASHBOARD_PASSWORD
// lingering in the process env (a silent install sets it; T8's own
// installer/piumy.iss NextButtonClick falls back to "piumy" if
// /DASHBOARDPASSWORD is empty). This proves passHash/isFactoryPassword
// genuinely ignore the env var once a real hash exists — the alarm and the
// login gate use the exact same comparison (bcrypt against the stored
// hash), so if this test is green the two can never disagree from THIS
// mechanism; the discrepancy Citrino saw has to come from somewhere else
// (worth confirming directly against the boss's stored hash, not this env
// var).
func TestFactoryPasswordAlarmIgnoresEnvSeedWhenOwnerAlreadyChangedIt(t *testing.T) {
	t.Setenv(dashboardPasswordSeedEnv, dashboardDefaultPassword) // "piumy" lingering in env
	st := newTestStore(t)
	ownPassword := "la-clave-real-del-dueño"
	ownHash, err := bcrypt.GenerateFromPassword([]byte(ownPassword), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.KVSet(store.SettingDashPassHash, string(ownHash)); err != nil {
		t.Fatal(err)
	}

	factory, err := isFactoryPassword(st)
	if err != nil {
		t.Fatal(err)
	}
	if factory {
		t.Error("isFactoryPassword = true with an existing owner password and PIUMY_DASHBOARD_PASSWORD=piumy lingering in env, want false")
	}

	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	respPiumy := loginAs(t, clientWithJar(t), srv.URL, dashboardUsername, dashboardDefaultPassword)
	defer respPiumy.Body.Close()
	if respPiumy.StatusCode != http.StatusUnauthorized {
		t.Errorf("login with the factory password = %d, want 401 (it must not work once the owner changed it)", respPiumy.StatusCode)
	}

	respOwn := loginAs(t, clientWithJar(t), srv.URL, dashboardUsername, ownPassword)
	defer respOwn.Body.Close()
	if respOwn.StatusCode != http.StatusOK {
		t.Errorf("login with the owner's real password = %d, want 200", respOwn.StatusCode)
	}
}

func TestLoginWrongPasswordAndWrongUsernameSameError(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	client := clientWithJar(t)
	respBadPass := loginAs(t, client, srv.URL, "admin", "not-piumy")
	defer respBadPass.Body.Close()
	var bodyBadPass map[string]string
	json.NewDecoder(respBadPass.Body).Decode(&bodyBadPass)

	client2 := clientWithJar(t)
	respBadUser := loginAs(t, client2, srv.URL, "root", "piumy")
	defer respBadUser.Body.Close()
	var bodyBadUser map[string]string
	json.NewDecoder(respBadUser.Body).Decode(&bodyBadUser)

	if respBadPass.StatusCode != http.StatusUnauthorized || respBadUser.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d/%d, want both 401", respBadPass.StatusCode, respBadUser.StatusCode)
	}
	if bodyBadPass["error"] != bodyBadUser["error"] {
		t.Errorf("wrong-password error %q != wrong-username error %q — must not distinguish (no user enumeration)", bodyBadPass["error"], bodyBadUser["error"])
	}
}

func TestSessionGatesAdminEndpointAlongsideAPIKey(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st, APIKey: "s3cr3t"}))
	defer srv.Close()

	// No credential at all -> 401.
	plain := &http.Client{}
	resp, err := plain.Get(srv.URL + "/api/admin/capi-connector")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no credential: status = %d, want 401", resp.StatusCode)
	}

	// X-API-Key alone (programmatic access) still works, unchanged.
	req, _ := http.NewRequest("GET", srv.URL+"/api/admin/capi-connector", nil)
	req.Header.Set("X-API-Key", "s3cr3t")
	resp2, err := plain.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("X-API-Key: status = %d, want 200 (must not break programmatic access)", resp2.StatusCode)
	}

	// A valid session cookie (no key) works too — the new human path.
	client := clientWithJar(t)
	loginResp := loginAs(t, client, srv.URL, "admin", "piumy")
	loginResp.Body.Close()
	resp3, err := client.Get(srv.URL + "/api/admin/capi-connector")
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("session cookie: status = %d, want 200", resp3.StatusCode)
	}
}

// TestOpenModeUnaffected is the explicit regression guard for Citrino's own
// curl workflow: APIKey=="" must stay fully open, exactly as before S1d.
func TestOpenModeUnaffected(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/admin/capi-connector")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("APIKey empty: status = %d, want 200 (open dev/LAN mode must survive S1d untouched)", resp.StatusCode)
	}
}

func TestDashboardShellPublicEvenWithAPIKey(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st, APIKey: "s3cr3t"}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/dashboard/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the shell must load with zero credential so its own JS can show the login overlay", resp.StatusCode)
	}
}

func TestChangePasswordRequiresCurrentPassword(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	client := clientWithJar(t)
	loginResp := loginAs(t, client, srv.URL, "admin", "piumy")
	loginResp.Body.Close()

	resp := postJSON(t, srv.URL+"/api/admin/password", map[string]any{"current_password": "wrong", "new_password": "brand-new-pass"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 on wrong current_password", resp.StatusCode)
	}

	// The old password must still work — nothing was changed.
	client2 := clientWithJar(t)
	stillWorks := loginAs(t, client2, srv.URL, "admin", "piumy")
	defer stillWorks.Body.Close()
	if stillWorks.StatusCode != http.StatusOK {
		t.Errorf("old password login after failed change = %d, want 200", stillWorks.StatusCode)
	}
}

func TestChangePasswordInvalidatesExistingSessions(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st, APIKey: "s3cr3t"}))
	defer srv.Close()

	client := clientWithJar(t)
	loginResp := loginAs(t, client, srv.URL, "admin", "piumy")
	loginResp.Body.Close()

	changeResp, err := client.Post(srv.URL+"/api/admin/password", "application/json",
		jsonBody(t, map[string]any{"current_password": "piumy", "new_password": "brand-new-pass"}))
	if err != nil {
		t.Fatal(err)
	}
	changeResp.Body.Close()
	if changeResp.StatusCode != http.StatusOK {
		t.Fatalf("change password status = %d, want 200", changeResp.StatusCode)
	}

	// The SAME client (same cookie jar, old cookie) must now be rejected —
	// the secret rotation invalidated it, including this very session.
	resp, err := client.Get(srv.URL + "/api/admin/capi-connector")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old session after password change: status = %d, want 401", resp.StatusCode)
	}

	// Logging in again with the OLD password must fail...
	client2 := clientWithJar(t)
	oldLogin := loginAs(t, client2, srv.URL, "admin", "piumy")
	defer oldLogin.Body.Close()
	if oldLogin.StatusCode != http.StatusUnauthorized {
		t.Errorf("re-login with old password = %d, want 401", oldLogin.StatusCode)
	}

	// ...but the NEW password logs in fine.
	client3 := clientWithJar(t)
	newLogin := loginAs(t, client3, srv.URL, "admin", "brand-new-pass")
	defer newLogin.Body.Close()
	if newLogin.StatusCode != http.StatusOK {
		t.Errorf("re-login with new password = %d, want 200", newLogin.StatusCode)
	}
}
