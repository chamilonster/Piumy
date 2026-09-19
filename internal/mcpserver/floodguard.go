// Anti-flood middleware — the mcp-go-specific adapter around
// internal/mcpguard. mcp-go types never leave this seam package, same
// reasoning as open-wa never leaving internal/openwa.
package mcpserver

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"piumy-gateway/internal/mcpguard"
)

// emittingTools are the tools that cause real-world side effects — these
// get the stricter emit-rate check even while comfortably under the
// general call-rate cap.
var emittingTools = map[string]bool{
	"send_message": true,
	"escalate":     true,
}

// floodGuardMiddleware wraps every registered tool with g's anti-flood
// check, identified by MCP session ID rather than the shared bearer token
// (limiting by token would put every legitimate client in one bucket).
// Registered via s.Use() in New, which mcp-go applies regardless of
// registration order.
func floodGuardMiddleware(g *mcpguard.Guard) server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var clientKey string
			if session := server.ClientSessionFromContext(ctx); session != nil {
				clientKey = session.SessionID()
			}
			v := g.Check(clientKey, emittingTools[req.Params.Name])
			if !v.Allowed {
				return mcp.NewToolResultError(v.Reason), nil
			}
			return next(ctx, req)
		}
	}
}
