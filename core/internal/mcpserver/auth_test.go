// SPDX-License-Identifier: AGPL-3.0-only
package mcpserver

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRequireBearerToken covers the DoD directly:
// FAIL-CLOSED — an empty key rejects EVERY request (not "open", unlike
// restapi's optional key), missing/wrong bearer token = 401, correct token
// via header OR ?key= query param = pass.
func TestRequireBearerToken(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cases := []struct {
		name       string
		mcpKey     string
		authHeader string
		queryKey   string
		wantStatus int
	}{
		{"empty key rejects even a request with no token at all", "", "", "", http.StatusUnauthorized},
		{"empty key rejects even a request with SOME token", "", "Bearer anything", "", http.StatusUnauthorized},
		{"missing header rejected", "secret", "", "", http.StatusUnauthorized},
		{"wrong token rejected", "secret", "Bearer nope", "", http.StatusUnauthorized},
		{"missing Bearer prefix rejected", "secret", "secret", "", http.StatusUnauthorized},
		{"correct token via header passes", "secret", "Bearer secret", "", http.StatusOK},
		{"correct token via query param passes", "secret", "", "secret", http.StatusOK},
		{"wrong token via query param rejected", "secret", "", "nope", http.StatusUnauthorized},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := RequireBearerToken(c.mcpKey, inner)
			target := "/mcp"
			if c.queryKey != "" {
				target += "?key=" + c.queryKey
			}
			req := httptest.NewRequest(http.MethodPost, target, nil)
			if c.authHeader != "" {
				req.Header.Set("Authorization", c.authHeader)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, c.wantStatus)
			}
		})
	}
}

// TestRequireBearerTokenErrorsOnEmptyKey covers the DoD's fail-closed
// startup signal: calling RequireBearerToken with an
// empty key must log an ERROR (not a soft warning) pointing at the fix —
// an unusable MCP endpoint should never be a silent/ambiguous state.
func TestRequireBearerTokenErrorsOnEmptyKey(t *testing.T) {
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	RequireBearerToken("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	if !strings.Contains(buf.String(), "pimywa auth setup") {
		t.Errorf("log output = %q, want it to point at `pimywa auth setup`", buf.String())
	}
}

// TestIsGroupJID covers the resolve_chat group-detection the agent relies on
// before deciding whether it's safe to reply into a chat.
func TestIsGroupJID(t *testing.T) {
	cases := []struct {
		jid  string
		want bool
	}{
		{"56999999999@s.whatsapp.net", false},
		{"12345-67890@g.us", true},
		{"", false},
	}
	for _, c := range cases {
		if got := isGroupJID(c.jid); got != c.want {
			t.Errorf("isGroupJID(%q) = %v, want %v", c.jid, got, c.want)
		}
	}
}
