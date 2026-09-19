// Bearer auth for the MCP endpoint — FAIL-CLOSED, unlike a typical REST
// API's optional key: an empty mcpKey does NOT leave the endpoint open, it
// rejects EVERY request. The MCP token is the closest thing this transport
// has to "you are the owner" (reset_dashboard_password's owner-scoping
// depends entirely on this).
package mcpserver

import (
	"log"
	"net/http"
	"strings"
)

// RequireBearerToken wraps h with the MCP endpoint's own auth gate.
// Accepts the token via the Authorization header (primary) or a ?key=
// query param (fallback, for a client that can only configure a bare URL):
//
//	{"mcpServers": {"piumy-gateway": {"url": "http://<host>:8081/mcp",
//	  "headers": {"Authorization": "Bearer <token>"}}}}
func RequireBearerToken(mcpKey string, h http.Handler) http.Handler {
	if mcpKey == "" {
		log.Println("mcpserver: ERROR — no PIUMY_MCP_KEY configured; the MCP endpoint will reject ALL requests until one is set")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mcpKey == "" || !validMCPToken(r, mcpKey) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		h.ServeHTTP(w, r)
	})
}

// validMCPToken checks the request's token against mcpKey — Authorization:
// Bearer header first, falling back to a ?key= query param. Never called
// with mcpKey=="" (RequireBearerToken short-circuits that case itself).
func validMCPToken(r *http.Request, mcpKey string) bool {
	const prefix = "Bearer "
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, prefix) {
		return auth[len(prefix):] == mcpKey
	}
	return r.URL.Query().Get("key") == mcpKey
}
