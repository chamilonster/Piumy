// get_media_full (F4-DESIGN §6) — the ORIGINAL, uncompressed media file;
// get_media (server.go, migrated) serves the low-q copy by default. The
// metering incentive (F4-DESIGN §8) is implemented literally here: only
// THIS tool charges the image usage cost — get_media (already cheap,
// low-q) doesn't.
package mcpserver

import (
	"context"
	"log"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"piumy-gateway/internal/store"
)

// mediaSummary is what get_media (server.go) serializes — deliberately
// WITHOUT FullPath. Found in the F4d audit: get_media used to serialize
// the whole store.Media struct, which includes full_path — the agent
// could read the original directly off that path and never call
// get_media_full, so the metering incentive (only get_media_full charges
// the image cost, F4-DESIGN §6/§8, boss verbatim: low-q free / full costs
// more) was dead on arrival. get_media_full remains the only tool that
// reveals FullPath.
type mediaSummary struct {
	MsgID   string `json:"msg_id"`
	ChatJID string `json:"chat_jid"`
	Path    string `json:"path"`
	Mime    string `json:"mime"`
	Size    int64  `json:"size"`
	TS      int64  `json:"ts"`
}

func summarizeMedia(items []store.Media) []mediaSummary {
	out := make([]mediaSummary, len(items))
	for i, m := range items {
		out[i] = mediaSummary{MsgID: m.MsgID, ChatJID: m.ChatJID, Path: m.Path, Mime: m.Mime, Size: m.Size, TS: m.TS}
	}
	return out
}

func addMediaTools(s *server.MCPServer, d Deps, tracker *agentTracker) {
	s.AddTool(mcp.NewTool("get_media_full",
		mcp.WithDescription("The ORIGINAL, uncompressed media file for a message — get_media serves a low-quality copy by default (fewer tokens to interpret). This costs more in the usage/metering model (an image usage charge); prefer get_media unless you genuinely need full quality (e.g. reading fine text in a photo)."),
		mcp.WithString("chat_id", mcp.Required()),
		mcp.WithString("msg_id", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			chatID, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			msgID, err := r.RequireString("msg_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			m, ok, err := d.Store.GetMedia(chatID, msgID)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if !ok {
				return mcp.NewToolResultError("media not found: " + chatID + "/" + msgID), nil
			}
			// Best-effort: a metering write failure must never block
			// serving the media the agent already asked for.
			if err := d.Store.AddUsage(chatID, store.Today(), store.UsageDelta{Images: 1}); err != nil {
				log.Printf("mcpserver: meter get_media_full %s/%s: %v", chatID, msgID, err)
			}
			return jsonResult(m)
		})
}
