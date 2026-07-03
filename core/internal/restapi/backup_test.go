// SPDX-License-Identifier: AGPL-3.0-only
package restapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"pimywa/internal/sessionbackup"
)

func newFakeSessionDB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE device(jid TEXT)"); err != nil {
		t.Fatal(err)
	}
}

// TestBackupEndpointsNilBackupUnavailable covers the DoD's degrade-gracefully
// requirement: with no Backup wired (nil, same as gateway=none), the
// endpoints 503 rather than panicking.
func TestBackupEndpointsNilBackupUnavailable(t *testing.T) {
	h := Handler(Deps{APIKey: "secret"})

	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/backup", nil),
		httptest.NewRequest(http.MethodPost, "/api/backup", nil),
	} {
		req.Header.Set("X-API-Key", "secret")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s with nil Backup: status = %d, want 503", req.Method, req.URL.Path, rec.Code)
		}
	}
}

// TestGetBackupStatus covers the DoD's dashboard status requirement: GET
// reflects enabled/off and the last backup after one runs.
func TestGetBackupStatus(t *testing.T) {
	dir := t.TempDir()
	sessionDB := filepath.Join(dir, "wa.db")
	newFakeSessionDB(t, sessionDB)

	bk := sessionbackup.New(sessionbackup.Config{
		SessionDBPath: sessionDB,
		Key:           "test passphrase",
		Dir:           filepath.Join(dir, "backups"),
		Keep:          5,
	})

	h := Handler(Deps{APIKey: "secret", Backup: bk})

	req := httptest.NewRequest(http.MethodGet, "/api/backup", nil)
	req.Header.Set("X-API-Key", "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var status sessionbackup.Status
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || status.Count != 0 {
		t.Errorf("initial status = %+v, want Enabled=true Count=0", status)
	}
}

// TestPostBackupNowPrivileged covers the DoD directly: POST /api/backup is
// gated the same way as every other endpoint (X-API-Key), triggers a real
// backup, and a disabled Backuper reports 503 rather than a false success.
func TestPostBackupNowPrivileged(t *testing.T) {
	dir := t.TempDir()
	sessionDB := filepath.Join(dir, "wa.db")
	newFakeSessionDB(t, sessionDB)

	bk := sessionbackup.New(sessionbackup.Config{
		SessionDBPath: sessionDB,
		Key:           "test passphrase",
		Dir:           filepath.Join(dir, "backups"),
		Keep:          5,
	})
	h := Handler(Deps{APIKey: "secret", Backup: bk})

	// Without the key: rejected.
	req := httptest.NewRequest(http.MethodPost, "/api/backup", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("without API key: status = %d, want 401", rec.Code)
	}

	// With the key: succeeds, a real backup file exists.
	req = httptest.NewRequest(http.MethodPost, "/api/backup", nil)
	req.Header.Set("X-API-Key", "secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("with API key: status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var res sessionbackup.Result
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if res.Path == "" {
		t.Error("backup result has no path")
	}

	// GET afterward reflects it.
	req = httptest.NewRequest(http.MethodGet, "/api/backup", nil)
	req.Header.Set("X-API-Key", "secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var status sessionbackup.Status
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Count != 1 {
		t.Errorf("Count after one backup = %d, want 1", status.Count)
	}
}

// TestPostBackupNowDisabledReports503 covers a Backuper with no key
// configured — POST must not report success (the DoD requires backups
// stay honestly "off", never a silent no-op reported as 200).
func TestPostBackupNowDisabledReports503(t *testing.T) {
	dir := t.TempDir()
	sessionDB := filepath.Join(dir, "wa.db")
	newFakeSessionDB(t, sessionDB)

	bk := sessionbackup.New(sessionbackup.Config{SessionDBPath: sessionDB, Dir: filepath.Join(dir, "backups")}) // no Key
	h := Handler(Deps{APIKey: "secret", Backup: bk})

	req := httptest.NewRequest(http.MethodPost, "/api/backup", nil)
	req.Header.Set("X-API-Key", "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("disabled backup: status = %d, want 503 (not a silent 200)", rec.Code)
	}
}
