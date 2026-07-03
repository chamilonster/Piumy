// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
//
// Anti-flood middleware — the mcp-go-specific
// adapter around internal/mcpguard. Kept in this file, in this package, on
// purpose: mcp-go types (mcp.CallToolRequest, server.ClientSessionFromContext)
// never leave the seam package that owns the MCP protocol, same reasoning
// as whatsmeow never leaving internal/gateway. mcpguard itself stays a
// plain, mcp-go-free rate limiter that's trivial to unit-test.
package mcpserver

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"pimywa/internal/mcpguard"
)

// emittingTools are the tools that cause real-world side effects (a
// WhatsApp message actually queued, a chat escalated) — these get
// "atención especial": a buggy or malicious agent
// hammering ONLY these should trip the stricter emit limit even while
// comfortably under the general call-rate cap.
var emittingTools = map[string]bool{
	"send_message": true,
	"escalate":     true,
}

// floodGuardMiddleware wraps every registered tool with g's anti-flood
// check, identified by MCP session ID rather than the bearer token: there
// is only one shared PIMYWA_MCP_KEY, so limiting by token would put every
// legitimate client in the same bucket — one flooding agent would throttle
// all the others too, the opposite of the goal (2026-07-01). Registered
// via s.Use() in New, which mcp-go applies to every
// tool regardless of registration order — so tools added later (C/D) are
// covered automatically, no retrofit needed.
func floodGuardMiddleware(g *mcpguard.Guard) server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var clientKey string
			if session := server.ClientSessionFromContext(ctx); session != nil {
				clientKey = session.SessionID()
			}
			// Empty clientKey (no session identifiable) falls back to
			// mcpguard's own shared "unknown" bucket — never a hard block,
			// never unlimited passage either (the fail-safe: don't
			// block legitimate traffic, don't allow unlimited flood).
			v := g.Check(clientKey, emittingTools[req.Params.Name])
			if !v.Allowed {
				return mcp.NewToolResultError(v.Reason), nil
			}
			return next(ctx, req)
		}
	}
}
