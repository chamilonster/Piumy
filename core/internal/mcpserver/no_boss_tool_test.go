// SPDX-License-Identifier: AGPL-3.0-only
package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestNoMCPToolSetsIsBoss is the negative half of the DoD: is_boss must
// never be settable through any MCP tool (only the privileged REST path,
// tested in the restapi package). Enumerates every registered tool's schema
// and fails if any property name could set the is_boss flag — this is the
// contract's whole point, so it's asserted directly rather than assumed.
func TestNoMCPToolSetsIsBoss(t *testing.T) {
	_, srv, ctx := newTestServer(t)

	req, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	resp := srv.HandleMessage(ctx, req)
	out, _ := json.Marshal(resp)

	var parsed struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				InputSchema struct {
					Properties map[string]any `json:"properties"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("parse tools/list: %v\nraw: %s", err, out)
	}
	if len(parsed.Result.Tools) == 0 {
		t.Fatal("tools/list returned no tools — test setup broken")
	}

	for _, tool := range parsed.Result.Tools {
		for prop := range tool.InputSchema.Properties {
			lower := strings.ToLower(prop)
			if strings.Contains(lower, "is_boss") || strings.Contains(lower, "boss") {
				t.Errorf("tool %q accepts a %q argument — is_boss must never be settable via MCP", tool.Name, prop)
			}
		}
	}
}

// TestNoMCPToolApprovesDrafts is the same negative-assertion pattern as
// TestNoMCPToolSetsIsBoss, applied to draft approval (0647 pieza 3): no tool
// name or argument may approve/discard a draft — only the privileged REST
// path can (tested in the restapi package). get_drafts itself is read-only
// and must keep existing; this only guards against something ELSE sneaking
// in a way to approve.
func TestNoMCPToolApprovesDrafts(t *testing.T) {
	_, srv, ctx := newTestServer(t)

	req, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	resp := srv.HandleMessage(ctx, req)
	out, _ := json.Marshal(resp)

	var parsed struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				InputSchema struct {
					Properties map[string]any `json:"properties"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("parse tools/list: %v\nraw: %s", err, out)
	}

	foundGetDrafts := false
	for _, tool := range parsed.Result.Tools {
		name := strings.ToLower(tool.Name)
		if name == "get_drafts" {
			foundGetDrafts = true
		}
		if strings.Contains(name, "approve") || strings.Contains(name, "discard") {
			t.Errorf("tool %q exists — approving/discarding drafts must only be possible via the privileged REST path", tool.Name)
		}
		for prop := range tool.InputSchema.Properties {
			lower := strings.ToLower(prop)
			if strings.Contains(lower, "approve") || strings.Contains(lower, "discard") {
				t.Errorf("tool %q accepts a %q argument — must never be able to approve/discard a draft via MCP", tool.Name, prop)
			}
		}
	}
	if !foundGetDrafts {
		t.Error("get_drafts tool not found — test setup broken")
	}
}

// TestNoMCPToolSetsRules is the same negative-assertion pattern as
// TestNoMCPToolSetsIsBoss, applied to chat rules (0647/1959): rules is "like
// a skill" for how the AI treats a chat — an agent must never be able to
// rewrite the rules it's judged against, AT ANY TIER of the hierarchy
// (particular, by type, or the global default — the property-name substring
// check below is blanket, so it already covers the type/default setters
// too: there's simply no MCP tool for them, by design). Only the privileged
// REST path (restapi package) may set any of it. set_chat_memory/
// set_chat_context ARE expected to exist (agent-writable) — this only
// guards "rules".
func TestNoMCPToolSetsRules(t *testing.T) {
	_, srv, ctx := newTestServer(t)

	req, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	resp := srv.HandleMessage(ctx, req)
	out, _ := json.Marshal(resp)

	var parsed struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				InputSchema struct {
					Properties map[string]any `json:"properties"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("parse tools/list: %v\nraw: %s", err, out)
	}

	foundSetMemory, foundSetContext := false, false
	for _, tool := range parsed.Result.Tools {
		switch tool.Name {
		case "set_chat_memory":
			foundSetMemory = true
		case "set_chat_context":
			foundSetContext = true
		}
		for prop := range tool.InputSchema.Properties {
			if strings.Contains(strings.ToLower(prop), "rules") {
				t.Errorf("tool %q accepts a %q argument — rules must never be settable via MCP", tool.Name, prop)
			}
		}
	}
	if !foundSetMemory {
		t.Error("set_chat_memory tool not found — memory must be agent-writable via MCP")
	}
	if !foundSetContext {
		t.Error("set_chat_context tool not found — context must be agent-writable via MCP")
	}
}

// TestNoMCPToolSetsConfirmation is the same negative-assertion pattern as
// TestNoMCPToolSetsIsBoss, applied to the auto-reply confirmation override
// (0748): an agent must never be able to turn off its own confirmation
// requirement (ConfirmationMode) or redirect who confirms (Confirmer) — only
// the privileged REST path may set either.
func TestNoMCPToolSetsConfirmation(t *testing.T) {
	_, srv, ctx := newTestServer(t)

	req, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	resp := srv.HandleMessage(ctx, req)
	out, _ := json.Marshal(resp)

	var parsed struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				InputSchema struct {
					Properties map[string]any `json:"properties"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("parse tools/list: %v\nraw: %s", err, out)
	}

	for _, tool := range parsed.Result.Tools {
		for prop := range tool.InputSchema.Properties {
			lower := strings.ToLower(prop)
			if strings.Contains(lower, "confirmation_mode") || strings.Contains(lower, "confirmer") {
				t.Errorf("tool %q accepts a %q argument — confirmation mode/confirmer must never be settable via MCP", tool.Name, prop)
			}
		}
	}
}
