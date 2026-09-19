// Terminal identity — how an MCP connection tells the gate which agent
// terminal it is. Real form (F5/smoke wires the actual HTTP transport, see
// docs/F4B-DIAGRAMA-FAILCLOSED.md): a fixed X-Piumy-Terminal-Id header,
// same per-connection-header pattern as auth.go's Bearer token, read via
// mcp-go's server.WithHTTPContextFunc into the request context.
//
// Wired for real in main.go's server.NewStreamableHTTPServer(mcpSrv,
// server.WithHTTPContextFunc(mcpserver.ExtractTerminalID), ...).
package mcpserver

import (
	"context"
	"net/http"
)

// TerminalIDHeader is the HTTP header a terminal's MCP client presents its
// stable terminal_id on (CleverCoder configures this per terminal's
// .mcp.json, alongside the Authorization Bearer token).
const TerminalIDHeader = "X-Piumy-Terminal-Id"

type terminalIDCtxKey struct{}

// ExtractTerminalID is an mcp-go HTTPContextFunc: it reads TerminalIDHeader
// off the incoming request and carries it in ctx for every tool call on
// that connection. Passed to server.WithHTTPContextFunc where main.go wires
// the real HTTP transport (see this file's own top comment).
func ExtractTerminalID(ctx context.Context, r *http.Request) context.Context {
	if id := r.Header.Get(TerminalIDHeader); id != "" {
		return context.WithValue(ctx, terminalIDCtxKey{}, id)
	}
	return ctx
}

// terminalIDFromContext returns the calling terminal's id, or "" if none
// was presented (a bare/manual MCP call, or a test that never went through
// the real HTTP transport — see withTerminalID below for how tests fake it).
func terminalIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(terminalIDCtxKey{}).(string)
	return id
}

// withTerminalID is the test-only equivalent of a connection presenting
// its terminal_id via the real header — mirrors how tests never go
// through a real MCP session either (sessionKey(ctx) is "" in every test
// in this package).
func withTerminalID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, terminalIDCtxKey{}, id)
}
