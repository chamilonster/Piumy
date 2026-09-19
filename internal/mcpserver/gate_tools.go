// The 4 MCP tools that drive the gate (gate.go): get_instructions, unlock,
// remember, skip. Registered by addGateTools, called from New.
package mcpserver

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func addGateTools(s *server.MCPServer, d Deps, gate *Gate, tracker *agentTracker) {
	// ── get_instructions ─────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("get_instructions",
		mcp.WithDescription("Pull the current dispatch's rules/memory/context and the unlock token needed to send — the token is at the END of the response, on purpose: read everything first. Call unlock(token) next, then remember/skip, before send_message will work (boss-level dispatches are exempt from this gate)."),
		mcp.WithString("nonce", mcp.Required(), mcp.Description("The dispatch nonce from the injected header"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			nonce, err := r.RequireString("nonce")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			instr, err := gate.GetInstructions(terminalIDFromContext(ctx), nonce, d.Store)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(instr)
		})

	// ── unlock ───────────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("unlock",
		mcp.WithDescription("Unlock the current dispatch with the token from get_instructions. Required before remember/skip/send_message (unless this is a boss-level dispatch)."),
		mcp.WithString("token", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			token, err := r.RequireString("token")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := gate.Unlock(terminalIDFromContext(ctx), token); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText("unlocked"), nil
		})

	// ── remember ─────────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("remember",
		mcp.WithDescription("Checkpoint after unlock: optionally save memory/context learned from this message, then move to ready so send_message can work. Always targets the CURRENT dispatch's chat — no override."),
		mcp.WithString("memory", mcp.Description("Optional: particular facts learned about this contact")),
		mcp.WithString("context", mcp.Description("Optional: general/explanatory situation of this relationship"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			memory := r.GetString("memory", "")
			chatContext := r.GetString("context", "")
			if err := gate.Remember(terminalIDFromContext(ctx), memory, chatContext, d.Store); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText("ready"), nil
		})

	// ── skip ─────────────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("skip",
		mcp.WithDescription("Checkpoint after unlock: nothing worth remembering — move straight to ready so send_message can work.")),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			if err := gate.Skip(terminalIDFromContext(ctx)); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText("ready"), nil
		})
}
