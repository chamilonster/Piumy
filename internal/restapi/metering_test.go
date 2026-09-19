package restapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"piumy-gateway/internal/store"
)

func TestReportTokensEndpointRecordsUsage(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/metering/tokens", map[string]any{"chat_jid": "1@c.us", "day": "2026-01-01", "tokens": 500})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	u, err := st.UsageForDay("1@c.us", "2026-01-01")
	if err != nil {
		t.Fatal(err)
	}
	if u.TokensReal != 500 {
		t.Errorf("TokensReal = %v, want 500", u.TokensReal)
	}
}

func TestReportTokensEndpointDefaultsToToday(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/metering/tokens", map[string]any{"chat_jid": "1@c.us", "tokens": 10})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	u, err := st.UsageForDay("1@c.us", store.Today())
	if err != nil {
		t.Fatal(err)
	}
	if u.TokensReal != 10 {
		t.Errorf("TokensReal for today (no day given) = %v, want 10", u.TokensReal)
	}
}

func TestReportTokensEndpointRejectsInvalidBody(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	cases := []map[string]any{
		{"chat_jid": "", "tokens": 10},
		{"chat_jid": "1@c.us", "tokens": 0},
		{"chat_jid": "1@c.us", "tokens": -5},
	}
	for _, body := range cases {
		resp := postJSON(t, srv.URL+"/api/metering/tokens", body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body=%v status = %d, want 400", body, resp.StatusCode)
		}
	}
}
