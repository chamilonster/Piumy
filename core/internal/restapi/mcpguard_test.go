// SPDX-License-Identifier: AGPL-3.0-only
package restapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"pimywa/internal/mcpguard"
	"pimywa/internal/store"
)

// TestMCPGuardEndpointsNilUnavailable covers the DoD's degrade-gracefully
// requirement: with no Guard wired, the endpoints 503 rather than panicking
// (same pattern as backup_test.go's nil-Backup case).
func TestMCPGuardEndpointsNilUnavailable(t *testing.T) {
	h := Handler(Deps{APIKey: "secret"})

	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/mcp-guard", nil),
		httptest.NewRequest(http.MethodPost, "/api/mcp-guard", nil),
	} {
		req.Header.Set("X-API-Key", "secret")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s with nil Guard: status = %d, want 503", req.Method, req.URL.Path, rec.Code)
		}
	}
}

// TestGetMCPGuardStatus covers the DoD's visibility requirement: GET
// reflects the effective config and any tracked clients, with no auth =
// rejected (same gate as every other privileged endpoint).
func TestGetMCPGuardStatus(t *testing.T) {
	guard := mcpguard.New(mcpguard.Config{RatePerMin: 42})
	guard.Check("some-session", false) // one tracked client

	h := Handler(Deps{APIKey: "secret", Guard: guard})

	req := httptest.NewRequest(http.MethodGet, "/api/mcp-guard", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("without API key: status = %d, want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/mcp-guard", nil)
	req.Header.Set("X-API-Key", "secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var st mcpguard.Status
	if err := json.NewDecoder(rec.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	if st.RatePerMin != 42 {
		t.Errorf("rate_per_min = %d, want 42", st.RatePerMin)
	}
	if len(st.Clients) != 1 || st.Clients[0].Key != "some-session" {
		t.Errorf("clients = %+v, want exactly one tracked (some-session)", st.Clients)
	}
}

// TestPostMCPGuardClampsAndPersists covers both guardrail directions (floor
// AND ceiling, unlike settings' single-direction clamps) plus that the
// change applies to the LIVE Guard immediately, and is persisted so a
// restart doesn't silently forget a dashboard edit (KV-override, same
// mechanism as /api/settings).
func TestPostMCPGuardClampsAndPersists(t *testing.T) {
	stDB, err := store.Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stDB.Close() })
	guard := mcpguard.New(mcpguard.Config{})
	h := Handler(Deps{APIKey: "secret", Store: stDB, Guard: guard})

	// Requests way outside [floor, ceiling] on every field.
	body := `{"rate_per_min":1,"emit_rate_per_min":1,"block_threshold":0,"block_cooldown_sec":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/mcp-guard", bytes.NewReader([]byte(body)))
	req.Header.Set("X-API-Key", "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var got mcpguard.Status
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.RatePerMin != 10 {
		t.Errorf("rate_per_min = %d, want floored to 10", got.RatePerMin)
	}
	if got.EmitRatePerMin != 2 {
		t.Errorf("emit_rate_per_min = %d, want floored to 2", got.EmitRatePerMin)
	}
	if got.BlockThreshold != 1 {
		t.Errorf("block_threshold = %d, want floored to 1", got.BlockThreshold)
	}
	if got.BlockCooldownSec != 30 {
		t.Errorf("block_cooldown_sec = %v, want floored to 30", got.BlockCooldownSec)
	}
	// Live Guard reflects it immediately.
	if guard.RatePerMin() != 10 {
		t.Errorf("live guard RatePerMin() = %d, want 10", guard.RatePerMin())
	}
	// And it's persisted (KV), not just applied in memory.
	if v := stDB.SettingInt(store.SettingMCPGuardRatePerMin, -1); v != 10 {
		t.Errorf("persisted rate_per_min = %d, want 10", v)
	}

	// Now way above ceiling on every field.
	body = `{"rate_per_min":99999,"emit_rate_per_min":99999,"block_threshold":99999,"block_cooldown_sec":999999}`
	req = httptest.NewRequest(http.MethodPost, "/api/mcp-guard", bytes.NewReader([]byte(body)))
	req.Header.Set("X-API-Key", "secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.RatePerMin != 600 || got.EmitRatePerMin != 100 || got.BlockThreshold != 20 || got.BlockCooldownSec != 3600 {
		t.Errorf("ceilinged status = %+v, want [600,100,20,3600]", got)
	}
}
