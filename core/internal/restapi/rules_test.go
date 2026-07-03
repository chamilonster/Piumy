// SPDX-License-Identifier: AGPL-3.0-only
package restapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"pimywa/internal/store"
)

// TestSetTypeRulesPrivileged covers the DoD directly (1959): by-type rules
// are settable via the privileged REST path only, individual and group are
// independent, and an invalid type is rejected.
func TestSetTypeRulesPrivileged(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	h := Handler(Deps{Store: st, APIKey: "secret"})

	// Without the key: rejected, nothing changes.
	req := httptest.NewRequest(http.MethodPost, "/api/rules/type", strings.NewReader(`{"type":"individual","rules":"sé formal"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("without API key: status = %d, want 401", rec.Code)
	}
	if v, _ := st.KVGet(store.SettingRulesTypeIndividual); v != "" {
		t.Fatal("type rules were set despite the unauthorized request")
	}

	// With the key: succeeds, individual and group are independent.
	req = httptest.NewRequest(http.MethodPost, "/api/rules/type", strings.NewReader(`{"type":"individual","rules":"sé formal"}`))
	req.Header.Set("X-API-Key", "secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("individual: status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/rules/type", strings.NewReader(`{"type":"group","rules":"solo si preguntan"}`))
	req.Header.Set("X-API-Key", "secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("group: status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	if v, err := st.KVGet(store.SettingRulesTypeIndividual); err != nil || v != "sé formal" {
		t.Errorf("individual type rules = %q err=%v, want %q", v, err, "sé formal")
	}
	if v, err := st.KVGet(store.SettingRulesTypeGroup); err != nil || v != "solo si preguntan" {
		t.Errorf("group type rules = %q err=%v, want %q", v, err, "solo si preguntan")
	}

	// Invalid type rejected.
	req = httptest.NewRequest(http.MethodPost, "/api/rules/type", strings.NewReader(`{"type":"banana","rules":"x"}`))
	req.Header.Set("X-API-Key", "secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid type: status = %d, want 400", rec.Code)
	}
}

// TestSetDefaultRulesPrivileged covers the same contract for the global
// default rules tier.
func TestSetDefaultRulesPrivileged(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	h := Handler(Deps{Store: st, APIKey: "secret"})

	req := httptest.NewRequest(http.MethodPost, "/api/rules/default", strings.NewReader(`{"rules":"default: sé breve"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("without API key: status = %d, want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/rules/default", strings.NewReader(`{"rules":"default: sé breve"}`))
	req.Header.Set("X-API-Key", "secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("with API key: status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if v, err := st.KVGet(store.SettingRulesDefault); err != nil || v != "default: sé breve" {
		t.Errorf("default rules = %q err=%v, want %q", v, err, "default: sé breve")
	}
}

// TestGetRules covers the read side: GET /api/rules reports all three
// tiers currently set.
func TestGetRules(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetTypeRules("individual", "individual rules"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDefaultRules("default rules"); err != nil {
		t.Fatal(err)
	}

	h := Handler(Deps{Store: st, APIKey: "secret"})
	req := httptest.NewRequest(http.MethodGet, "/api/rules", nil)
	req.Header.Set("X-API-Key", "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["individual"] != "individual rules" || got["group"] != "" || got["default"] != "default rules" {
		t.Errorf("GET /api/rules = %+v, want individual/default set, group empty", got)
	}
}
