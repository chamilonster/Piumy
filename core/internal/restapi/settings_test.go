// SPDX-License-Identifier: AGPL-3.0-only
package restapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"pimywa/internal/governor"
	"pimywa/internal/store"
)

func newSettingsDeps(t *testing.T) (Deps, *store.Store, *governor.Limiter) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	gov := governor.NewLimiter(10, time.Minute)
	gov.SetDailyMax(500)

	return Deps{
		Store: st, Gov: gov,

		DispatchDelayMinDefault: 1 * time.Second,
		DispatchDelayMaxDefault: 5 * time.Second,
		ReadDelayMinDefault:     2 * time.Second,
		ReadDelayMaxDefault:     8 * time.Second,
		ActionDelayMinDefault:   1 * time.Second,
		ActionDelayMaxDefault:   4 * time.Second,

		MediaMaxMBDefault: 512,
		MediaMaxMBFloor:   16,

		RateLimitPerMinDefault: 10,
		RateLimitPerMinCeiling: 30,
		RateLimitPerDayDefault: 500,
		RateLimitPerDayCeiling: 2000,
	}, st, gov
}

func getSettings(t *testing.T, h http.Handler) settingsResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/settings status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp settingsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestGetSettingsDefaults covers the DoD directly: with nothing overridden,
// GET reflects the configured defaults plus the floors/ceilings.
func TestGetSettingsDefaults(t *testing.T) {
	d, _, _ := newSettingsDeps(t)
	h := Handler(d)
	resp := getSettings(t, h)

	if resp.DispatchDelayMinSec != 1 || resp.DispatchDelayMaxSec != 5 {
		t.Errorf("dispatch delay = [%v,%v], want [1,5]", resp.DispatchDelayMinSec, resp.DispatchDelayMaxSec)
	}
	if resp.MediaMaxMB != 512 {
		t.Errorf("media_max_mb = %d, want 512 (default)", resp.MediaMaxMB)
	}
	if resp.RateLimitPerMin != 10 || resp.RateLimitPerDay != 500 {
		t.Errorf("rate limits = [%d,%d], want [10,500]", resp.RateLimitPerMin, resp.RateLimitPerDay)
	}
	if resp.Floors.DispatchDelaySec != 1 || resp.Floors.MediaMaxMB != 16 {
		t.Errorf("floors = %+v, want dispatch=1 media_max_mb=16", resp.Floors)
	}
	if resp.Ceilings.RateLimitPerMin != 30 || resp.Ceilings.RateLimitPerDay != 2000 {
		t.Errorf("ceilings = %+v, want per_min=30 per_day=2000", resp.Ceilings)
	}
	if resp.MediaSkipVideoGroup || resp.MediaSkipVideoChat || resp.MediaSkipPhotoGroup || resp.MediaSkipPhotoChat {
		t.Errorf("media skip toggles = %+v, want all false by default", resp)
	}
}

// TestPostSettingsClampsDelayFloor covers the DoD's anti-ban floor
// (0753): a requested delay below the shipped default is floored up, and
// max is re-clamped to stay >= min after flooring.
func TestPostSettingsClampsDelayFloor(t *testing.T) {
	d, _, _ := newSettingsDeps(t)
	h := Handler(d)

	body := `{"dispatch_delay_min_sec":0.1,"dispatch_delay_max_sec":0.2,
		"read_delay_min_sec":2,"read_delay_max_sec":8,
		"action_delay_min_sec":1,"action_delay_max_sec":4,
		"media_max_mb":512,"rate_limit_per_min":10,"rate_limit_per_day":500}`
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body=%s", rec.Code, rec.Body.String())
	}

	var resp settingsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.DispatchDelayMinSec != 1 {
		t.Errorf("dispatch_delay_min_sec = %v, want floored to 1 (the shipped default)", resp.DispatchDelayMinSec)
	}
	if resp.DispatchDelayMaxSec != 1 {
		t.Errorf("dispatch_delay_max_sec = %v, want floored to 1 too (max >= min after flooring)", resp.DispatchDelayMaxSec)
	}

	// GET afterward must reflect the same clamped (persisted) values.
	got := getSettings(t, h)
	if got.DispatchDelayMinSec != 1 || got.DispatchDelayMaxSec != 1 {
		t.Errorf("GET after clamp = [%v,%v], want [1,1] (persisted)", got.DispatchDelayMinSec, got.DispatchDelayMaxSec)
	}
}

// TestPostSettingsClampsRateLimitCeiling covers the mirror-image guardrail
// (0753): a requested rate limit above the ceiling is capped, and the
// governor itself is updated to the effective (capped) value immediately.
func TestPostSettingsClampsRateLimitCeiling(t *testing.T) {
	d, _, gov := newSettingsDeps(t)
	h := Handler(d)

	body := `{"dispatch_delay_min_sec":1,"dispatch_delay_max_sec":5,
		"read_delay_min_sec":2,"read_delay_max_sec":8,
		"action_delay_min_sec":1,"action_delay_max_sec":4,
		"media_max_mb":512,"rate_limit_per_min":9999,"rate_limit_per_day":99999}`
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body=%s", rec.Code, rec.Body.String())
	}

	var resp settingsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.RateLimitPerMin != 30 {
		t.Errorf("rate_limit_per_min = %d, want ceilinged to 30", resp.RateLimitPerMin)
	}
	if resp.RateLimitPerDay != 2000 {
		t.Errorf("rate_limit_per_day = %d, want ceilinged to 2000", resp.RateLimitPerDay)
	}
	if gov.Max() != 30 || gov.DailyMax() != 2000 {
		t.Errorf("governor Max()=%d DailyMax()=%d, want the ceilinged values applied immediately", gov.Max(), gov.DailyMax())
	}
}

// TestPostSettingsRateLimitPerMinFloorsAtOne covers the sanity floor (0753):
// 0/negative would be a silent de-facto kill switch, so per-min is floored
// at 1 regardless of what was requested.
func TestPostSettingsRateLimitPerMinFloorsAtOne(t *testing.T) {
	d, _, gov := newSettingsDeps(t)
	h := Handler(d)

	body := `{"dispatch_delay_min_sec":1,"dispatch_delay_max_sec":5,
		"read_delay_min_sec":2,"read_delay_max_sec":8,
		"action_delay_min_sec":1,"action_delay_max_sec":4,
		"media_max_mb":512,"rate_limit_per_min":0,"rate_limit_per_day":0}`
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp settingsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.RateLimitPerMin != 1 {
		t.Errorf("rate_limit_per_min = %d, want floored to 1 (never a silent kill switch)", resp.RateLimitPerMin)
	}
	if resp.RateLimitPerDay != 0 {
		t.Errorf("rate_limit_per_day = %d, want 0 (a valid, deliberate 'no daily cap')", resp.RateLimitPerDay)
	}
	if gov.Max() != 1 || gov.DailyMax() != 0 {
		t.Errorf("governor Max()=%d DailyMax()=%d, want [1,0] applied", gov.Max(), gov.DailyMax())
	}
}

// TestPostSettingsMediaMaxMBFloor covers the 3rd-commandment guardrail: GC
// retention can never be set low enough to effectively disable it.
func TestPostSettingsMediaMaxMBFloor(t *testing.T) {
	d, st, _ := newSettingsDeps(t)
	h := Handler(d)

	body := `{"dispatch_delay_min_sec":1,"dispatch_delay_max_sec":5,
		"read_delay_min_sec":2,"read_delay_max_sec":8,
		"action_delay_min_sec":1,"action_delay_max_sec":4,
		"media_max_mb":0,"rate_limit_per_min":10,"rate_limit_per_day":500}`
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp settingsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.MediaMaxMB != 16 {
		t.Errorf("media_max_mb = %d, want floored to 16", resp.MediaMaxMB)
	}
	if got := st.SettingInt(store.SettingMediaMaxMB, -1); got != 16 {
		t.Errorf("persisted media_max_mb = %d, want 16", got)
	}
}

// TestPostSettingsPersistsMediaToggles covers the DoD's media control:
// toggles round-trip through GET, one per type × origin.
func TestPostSettingsPersistsMediaToggles(t *testing.T) {
	d, _, _ := newSettingsDeps(t)
	h := Handler(d)

	body := `{"media_skip_video_group":true,"media_skip_photo_chat":true,
		"dispatch_delay_min_sec":1,"dispatch_delay_max_sec":5,
		"read_delay_min_sec":2,"read_delay_max_sec":8,
		"action_delay_min_sec":1,"action_delay_max_sec":4,
		"media_max_mb":512,"rate_limit_per_min":10,"rate_limit_per_day":500}`
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body=%s", rec.Code, rec.Body.String())
	}

	got := getSettings(t, h)
	if !got.MediaSkipVideoGroup || !got.MediaSkipPhotoChat {
		t.Errorf("toggles = %+v, want video_group=true photo_chat=true", got)
	}
	if got.MediaSkipVideoChat || got.MediaSkipPhotoGroup {
		t.Errorf("toggles = %+v, want video_chat/photo_group untouched (false)", got)
	}
}
