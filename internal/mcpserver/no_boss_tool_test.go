// Historically (F4a) NONE of is_boss/rules/draft-approval were reachable
// via any MCP tool at all — only a privileged REST path. F4c changes that
// deliberately: they're now MCP tools too, gated by construction
// (bossOnlyTools or selfGatedTools, levelgate.go). These tests assert the
// CURRENT invariant: every privileged tool is registered AND gated, never
// ungated.
//
// T148 (ct-2026-09-07-1644, boss verbatim, direct: "quiero que quites ese
// candado... resulta ser que piumy estaba lleno de candados estupidos")
// emptied bossOnlyTools down to set_kill_switch alone — every group/profile
// tool F4c originally put there left. This file's own coverage test used to
// assert they were THERE; it now asserts the opposite (see
// TestGroupProfileToolsAreNoLongerBossOnly below) — the tools themselves
// are unaffected, only the gate map's membership changed.
package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
)

func listTools(t *testing.T) []struct {
	Name        string `json:"name"`
	InputSchema struct {
		Properties map[string]any `json:"properties"`
	} `json:"inputSchema"`
} {
	t.Helper()
	_, srv, ctx, _ := newTestServer(t)
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
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
	return parsed.Result.Tools
}

// intentionallyUngated (T31, ct-2026-08-06-0244) is the ONE deliberate
// exception TestPrivilegedToolsAreAllBossOnly carves out — set_chat_rules
// takes a "rules" argument but is neither bossOnlyTools nor selfGatedTools
// on purpose (the boss's own explicit call, twice rejecting a gated
// version first: "No pongas condiciones... ya es responsabilidad del
// usuario"). A witness set of exactly one, so a FUTURE tool that happens
// to take a "rules"/"is_boss"/etc. argument still trips the generic check
// below instead of silently reusing this carve-out.
var intentionallyUngated = map[string]bool{
	"set_chat_rules": true,
}

// TestPrivilegedToolsAreAllBossOnly is the structural half: every tool that
// can set is_boss, rules (any tier OTHER than per-chat — see
// intentionallyUngated), confirmation_mode, or resolve a draft must be
// gated — either bossOnlyTools (levelGateMiddleware) or selfGatedTools
// (S10, ct-2026-07-30-1349: its own handler enforces per-argument
// authorization instead — see admin_tools.go). Nothing ELSE privileged can
// slip in ungated.
func TestPrivilegedToolsAreAllBossOnly(t *testing.T) {
	for _, tool := range listTools(t) {
		if bossOnlyTools[tool.Name] || selfGatedTools[tool.Name] || intentionallyUngated[tool.Name] {
			continue // gated (or deliberately not) — fine regardless of what it accepts
		}
		for prop := range tool.InputSchema.Properties {
			lower := strings.ToLower(prop)
			for _, sensitive := range []string{"is_boss", "rules", "confirmation_mode", "confirmer"} {
				if strings.Contains(lower, sensitive) {
					t.Errorf("tool %q accepts a %q argument but isn't in bossOnlyTools", tool.Name, prop)
				}
			}
		}
		lower := strings.ToLower(tool.Name)
		if strings.Contains(lower, "approve") || strings.Contains(lower, "discard") {
			t.Errorf("tool %q looks like draft-approval but isn't in bossOnlyTools", tool.Name)
		}
	}
}

// TestPrivilegedToolsExistAndRegistered is the coverage half: every tool
// F4c's contract requires actually got registered (a name missing here
// would make the gating test above vacuously pass). S10 (ct-2026-07-30-
// 1349) split the "gated" check: self-gated tools enforce their own
// per-argument (or per-tool, S12 ct-2026-07-30-1622) authorization
// (admin_tools.go) instead of bossOnlyTools.
func TestPrivilegedToolsExistAndRegistered(t *testing.T) {
	selfGatedWant := []string{
		"set_is_boss", "set_type_rules",
		"set_confirmation_mode", "set_config_level",
		"approve_draft", "discard_draft", "reject_draft", "edit_draft", "set_is_approver",
	}
	found := map[string]bool{}
	for _, tool := range listTools(t) {
		found[tool.Name] = true
	}
	for _, name := range selfGatedWant {
		if !found[name] {
			t.Errorf("tool %q not registered", name)
		}
		if !selfGatedTools[name] {
			t.Errorf("tool %q registered but not in selfGatedTools", name)
		}
	}
	// set_chat_rules (T31, ct-2026-08-06-0244): registered, but deliberately
	// in NONE of the three gating buckets — confirms the exemption is real
	// and current, not a name that quietly rotted out of every map.
	if !found["set_chat_rules"] {
		t.Error(`tool "set_chat_rules" not registered`)
	}
	if bossOnlyTools["set_chat_rules"] || selfGatedTools["set_chat_rules"] {
		t.Error(`"set_chat_rules" is gated somewhere — T31 unblocked it unconditionally, it should be in neither map`)
	}
	if !intentionallyUngated["set_chat_rules"] {
		t.Error(`"set_chat_rules" missing from intentionallyUngated — TestPrivilegedToolsAreAllBossOnly would now flag it`)
	}
}

// TestGroupProfileToolsAreNoLongerBossOnly is T148's own coverage half
// (ct-2026-09-07-1644, boss verbatim, direct: "quiero que quites ese
// candado, si le pido a un agente que cree un grupo, quiero que lo haga")
// — the mirror of TestPrivilegedToolsExistAndRegistered above: these 7
// tools are still registered (nothing about the FEATURE changed, only who
// may call it), but must be ABSENT from bossOnlyTools now. set_kill_switch
// is the control — the one lock T148 keeps, by the boss's own reasoning
// (the anti-ban emergency stop), so it must stay exactly where it was.
func TestGroupProfileToolsAreNoLongerBossOnly(t *testing.T) {
	openedUp := []string{
		"create_group", "add_participant", "promote_group_admin", "set_group_icon",
		"set_group_description", "set_profile_status", "set_profile_photo",
	}
	found := map[string]bool{}
	for _, tool := range listTools(t) {
		found[tool.Name] = true
	}
	for _, name := range openedUp {
		if !found[name] {
			t.Errorf("tool %q not registered", name)
		}
		if bossOnlyTools[name] {
			t.Errorf("tool %q is still in bossOnlyTools — T148 asked it removed", name)
		}
	}
	if !bossOnlyTools["set_kill_switch"] {
		t.Error("set_kill_switch left bossOnlyTools — T148 explicitly keeps this ONE lock (the boss's own anti-ban reasoning)")
	}
}

// TestAgentWritableToolsStillExist: set_chat_memory/set_chat_context remain
// agent-writable (unaffected by F4c's privileged additions).
func TestAgentWritableToolsStillExist(t *testing.T) {
	foundSetMemory, foundSetContext := false, false
	for _, tool := range listTools(t) {
		switch tool.Name {
		case "set_chat_memory":
			foundSetMemory = true
		case "set_chat_context":
			foundSetContext = true
		}
	}
	if !foundSetMemory {
		t.Error("set_chat_memory tool not found")
	}
	if !foundSetContext {
		t.Error("set_chat_context tool not found")
	}
	if bossOnlyTools["set_chat_memory"] || bossOnlyTools["set_chat_context"] {
		t.Error("set_chat_memory/set_chat_context must stay agent-writable, not boss-only")
	}
}

// TestGetDraftsStillReadOnly: get_drafts itself must never be boss-only
// (any level can see pending drafts — only resolving one is privileged).
func TestGetDraftsStillReadOnly(t *testing.T) {
	found := false
	for _, tool := range listTools(t) {
		if tool.Name == "get_drafts" {
			found = true
		}
	}
	if !found {
		t.Error("get_drafts tool not found")
	}
	if bossOnlyTools["get_drafts"] {
		t.Error("get_drafts must not be boss-only — it's enumeration-gated (anti-leakage), a different restriction")
	}
}
