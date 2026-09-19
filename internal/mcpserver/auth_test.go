package mcpserver

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRequireBearerToken covers the DoD directly: FAIL-CLOSED — an empty
// key rejects EVERY request, missing/wrong token = 401, correct token via
// header OR ?key= query param = pass.
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

// TestRequireBearerTokenErrorsOnEmptyKey covers the fail-closed startup
// signal: calling RequireBearerToken with an empty key must log an ERROR.
func TestRequireBearerTokenErrorsOnEmptyKey(t *testing.T) {
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	RequireBearerToken("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	if !strings.Contains(buf.String(), "PIUMY_MCP_KEY") {
		t.Errorf("log output = %q, want it to mention PIUMY_MCP_KEY", buf.String())
	}
}

func TestIsGroupJID(t *testing.T) {
	cases := []struct {
		jid  string
		want bool
	}{
		{"55500000001@c.us", false},
		{"12345-67890@g.us", true},
		{"", false},
	}
	for _, c := range cases {
		if got := isGroupJID(c.jid); got != c.want {
			t.Errorf("isGroupJID(%q) = %v, want %v", c.jid, got, c.want)
		}
	}
}
