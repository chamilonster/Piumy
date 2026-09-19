package mcpserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"piumy-gateway/internal/router"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
)

const (
	principalTerm      = "principal-term"
	secondaryTerm      = "secondary-term"
	otherSecondaryTerm = "other-secondary-term"
)

func agentCtx(ctx context.Context, termID string) context.Context {
	return withTerminalID(ctx, termID)
}

// buildAgentServer returns a ready MCP server with PrincipalTerminalID set.
func buildAgentServer(t *testing.T, st *store.Store, onUpsert func(string, string, string, string)) (context.Context, *server.MCPServer) {
	t.Helper()
	return buildAgentServerFull(t, st, onUpsert, nil)
}

// buildAgentServerFull is buildAgentServer plus an OnAgentDelete hook — a
// separate constructor (not a buildAgentServer signature change) so every
// existing call site stays untouched (ct-2026-07-29, agentes paso 3).
func buildAgentServerFull(t *testing.T, st *store.Store, onUpsert func(string, string, string, string), onDelete func(string)) (context.Context, *server.MCPServer) {
	t.Helper()
	dir := t.TempDir()
	sm := state.NewManager(filepath.Join(dir, "status.json"), 8)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv := New(ctx, Deps{
		Store:               st,
		State:               sm,
		Router:              router.NewManager(filepath.Join(dir, "router.json")),
		AgentIdle:           time.Minute,
		Gate:                NewGate(),
		PrincipalTerminalID: principalTerm,
		OnAgentUpsert:       onUpsert,
		OnAgentDelete:       onDelete,
	})
	return ctx, srv
}

func openAgentDB(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// TestRegisterAgentPersistsAndCallsOnUpsert: register_agent stores the agent
// and fires OnAgentUpsert with the right args.
func TestRegisterAgentPersistsAndCallsOnUpsert(t *testing.T) {
	var called [4]string
	st := openAgentDB(t)
	ctx, srv := buildAgentServer(t, st, func(agentID, endpoint, termID, pinpass string) {
		called = [4]string{agentID, endpoint, termID, pinpass}
	})

	resp := callTool(t, agentCtx(ctx, secondaryTerm), srv, "register_agent", map[string]any{
		"endpoint":            "http://192.168.1.10:8787",
		"antenna_terminal_id": "ant-guid",
		"pinpass":             "abc123",
	})
	if strings.Contains(resp, `"isError":true`) {
		t.Fatalf("register_agent failed: %s", resp)
	}

	a, ok, err := st.GetAgent(secondaryTerm)
	if err != nil || !ok {
		t.Fatalf("GetAgent after register_agent: ok=%v err=%v", ok, err)
	}
	if a.Role != "secondary" || a.Endpoint != "http://192.168.1.10:8787" {
		t.Errorf("Agent = %+v, want role=secondary endpoint=http://192.168.1.10:8787", a)
	}
	if called[0] != secondaryTerm {
		t.Errorf("OnAgentUpsert agentID = %q, want %q", called[0], secondaryTerm)
	}
}

// TestRegisterAgentKeepsStoreAndInjectorUnderTheSameID is T130's own
// "constancia" (ct-2026-09-03-0207) — Camino B's classic "delivered but
// never resolvable" failure can't happen for a secondary agent because
// register_agent writes the agents-table row AND wires the live injector
// under the SAME id, in the SAME call: whenever capipush has an injector
// to dispatch through, the gate already has a matching agents row to
// resolve against. That's a claim about BEHAVIOR, not a reading of the
// code — this test doesn't just assert against a hardcoded constant (the
// sibling test above does that), it compares the id OnAgentUpsert actually
// received against what GetAgent actually finds, so a FUTURE change that
// breaks the pairing (e.g. wiring the wrong variable into OnAgentUpsert)
// fails this test, not just a code review.
func TestRegisterAgentKeepsStoreAndInjectorUnderTheSameID(t *testing.T) {
	var wiredID string
	st := openAgentDB(t)
	ctx, srv := buildAgentServer(t, st, func(agentID, endpoint, termID, pinpass string) {
		wiredID = agentID
	})

	resp := callTool(t, agentCtx(ctx, secondaryTerm), srv, "register_agent", map[string]any{
		"endpoint":            "http://192.168.1.10:8787",
		"antenna_terminal_id": "ant-guid",
		"pinpass":             "abc123",
	})
	if strings.Contains(resp, `"isError":true`) {
		t.Fatalf("register_agent failed: %s", resp)
	}
	if wiredID == "" {
		t.Fatal("setup: OnAgentUpsert never fired")
	}

	a, ok, err := st.GetAgent(wiredID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("no agents-table row for %q — the id wired into the live injector has no matching row for the gate to resolve against", wiredID)
	}
	if a.AgentID != wiredID {
		t.Errorf("GetAgent(%q).AgentID = %q, want the exact same id", wiredID, a.AgentID)
	}
}

// TestRegisterAgentAllowsPrincipalCaller: T142 (ct-2026-09-07-1222), boss
// verbatim "quiten ese candado, no tiene logica" — the principal used to be
// flatly refused as a register_agent caller; that gate protected nothing
// (POST /api/admin/agent-create already creates any agent unchecked) and
// only stopped the principal from doing legitimately what the boss asked
// for: registering another agent.
func TestRegisterAgentAllowsPrincipalCaller(t *testing.T) {
	var wiredID string
	st := openAgentDB(t)
	ctx, srv := buildAgentServer(t, st, func(agentID, endpoint, termID, pinpass string) {
		wiredID = agentID
	})

	resp := callTool(t, agentCtx(ctx, principalTerm), srv, "register_agent", map[string]any{
		"endpoint":            "http://192.168.1.20:8787",
		"antenna_terminal_id": "ant-guid",
		"pinpass":             "abc123",
		"agent_id":            otherSecondaryTerm,
	})
	if strings.Contains(resp, `"isError":true`) {
		t.Fatalf("register_agent failed: %s", resp)
	}
	a, ok, err := st.GetAgent(otherSecondaryTerm)
	if err != nil || !ok {
		t.Fatalf("GetAgent after register_agent: ok=%v err=%v", ok, err)
	}
	if a.Role != "secondary" {
		t.Errorf("Agent.Role = %q, want secondary", a.Role)
	}
	if wiredID != otherSecondaryTerm {
		t.Errorf("OnAgentUpsert agentID = %q, want %q", wiredID, otherSecondaryTerm)
	}
}

// TestRegisterAgentWithExplicitAgentID: T142 — the optional agent_id lets a
// caller register a DIFFERENT terminal than itself; omitting it keeps
// today's behavior (register the caller), covered by
// TestRegisterAgentPersistsAndCallsOnUpsert above.
func TestRegisterAgentWithExplicitAgentID(t *testing.T) {
	st := openAgentDB(t)
	ctx, srv := buildAgentServer(t, st, nil)

	resp := callTool(t, agentCtx(ctx, secondaryTerm), srv, "register_agent", map[string]any{
		"endpoint":            "http://192.168.1.30:8787",
		"antenna_terminal_id": "ant-guid",
		"pinpass":             "abc123",
		"agent_id":            otherSecondaryTerm,
	})
	if strings.Contains(resp, `"isError":true`) {
		t.Fatalf("register_agent failed: %s", resp)
	}
	if _, ok, _ := st.GetAgent(otherSecondaryTerm); !ok {
		t.Fatalf("expected agent_id %q to be registered", otherSecondaryTerm)
	}
	if _, ok, _ := st.GetAgent(secondaryTerm); ok {
		t.Errorf("caller %q should NOT have been registered — agent_id was explicit", secondaryTerm)
	}
}

// TestSetAgentCAPIModifiesExisting: set_agent_capi updates endpoint and calls OnUpsert.
func TestSetAgentCAPIModifiesExisting(t *testing.T) {
	var called [4]string
	st := openAgentDB(t)
	if err := st.UpsertAgent(store.Agent{
		AgentID: secondaryTerm, Endpoint: "http://old:8787",
		AntennaTerminalID: "ant-old", Pinpass: "p-old", Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}

	ctx, srv := buildAgentServer(t, st, func(agentID, endpoint, termID, pinpass string) {
		called = [4]string{agentID, endpoint, termID, pinpass}
	})

	resp := callTool(t, agentCtx(ctx, secondaryTerm), srv, "set_agent_capi", map[string]any{
		"agent_id": secondaryTerm,
		"endpoint": "http://new:8787",
	})
	if strings.Contains(resp, `"isError":true`) {
		t.Fatalf("set_agent_capi failed: %s", resp)
	}

	a, _, _ := st.GetAgent(secondaryTerm)
	if a.Endpoint != "http://new:8787" {
		t.Errorf("Endpoint = %q, want http://new:8787", a.Endpoint)
	}
	if a.AntennaTerminalID != "ant-old" {
		t.Errorf("AntennaTerminalID changed unexpectedly: %q", a.AntennaTerminalID)
	}
	if called[0] != secondaryTerm {
		t.Errorf("OnAgentUpsert agentID = %q, want %q", called[0], secondaryTerm)
	}
}

// TestSetAgentCAPIRejectsPrincipalTerminalID: agent_id == PrincipalTerminalID
// is always refused — gate duro. S9 (ct-2026-07-30-031143): the refusal must
// also name the real door (set_capi_connector) instead of only pointing at
// the dashboard — Citrino hit this live during the smoke and had to find
// set_capi_connector by reading the tool list.
func TestSetAgentCAPIRejectsPrincipalTerminalID(t *testing.T) {
	st := openAgentDB(t)
	ctx, srv := buildAgentServer(t, st, nil)

	resp := callTool(t, agentCtx(ctx, principalTerm), srv, "set_agent_capi", map[string]any{
		"agent_id": principalTerm,
		"endpoint": "http://evil:8787",
	})
	if !strings.Contains(resp, "reserved") {
		t.Errorf("expected 'reserved' error, got: %s", resp)
	}
	if !strings.Contains(resp, "set_capi_connector") {
		t.Errorf("error doesn't name the real door (set_capi_connector), got: %s", resp)
	}
}

// TestSecondaryCannotUpdateOtherAgent: a secondary calling set_agent_capi on
// a different agent_id must be refused.
// TestSecondaryCanUpdateOtherAgent is T148's own regression
// (ct-2026-09-07-1644, boss verbatim, direct: "quiero que quites ese
// candado... resulta ser que piumy estaba lleno de candados estupidos").
// set_agent_capi's old "a secondary can only update its own" restriction
// is gone — replaces TestSecondaryCannotUpdateOtherAgent, which proved
// exactly that restriction.
func TestSecondaryCanUpdateOtherAgent(t *testing.T) {
	st := openAgentDB(t)
	if err := st.UpsertAgent(store.Agent{
		AgentID: otherSecondaryTerm, Endpoint: "http://other:8787",
		AntennaTerminalID: "ant-other", Pinpass: "p-other", Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}

	ctx, srv := buildAgentServer(t, st, nil)

	resp := callTool(t, agentCtx(ctx, secondaryTerm), srv, "set_agent_capi", map[string]any{
		"agent_id": otherSecondaryTerm,
		"endpoint": "http://reconfigured:8787",
	})
	if strings.Contains(resp, "forbidden") {
		t.Errorf("set_agent_capi on another agent = %s, want it to succeed — T148 removed this restriction", resp)
	}

	a, _, _ := st.GetAgent(otherSecondaryTerm)
	if a.Endpoint != "http://reconfigured:8787" {
		t.Errorf("Endpoint = %q, want %q (the update should have gone through)", a.Endpoint, "http://reconfigured:8787")
	}
}

// TestPrincipalCanUpdateAnyAgent: the principal can set_agent_capi on any secondary.
func TestPrincipalCanUpdateAnyAgent(t *testing.T) {
	st := openAgentDB(t)
	if err := st.UpsertAgent(store.Agent{
		AgentID: secondaryTerm, Endpoint: "http://old:8787",
		AntennaTerminalID: "ant-old", Pinpass: "p", Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}

	ctx, srv := buildAgentServer(t, st, nil)

	resp := callTool(t, agentCtx(ctx, principalTerm), srv, "set_agent_capi", map[string]any{
		"agent_id": secondaryTerm,
		"endpoint": "http://new:8787",
	})
	if strings.Contains(resp, `"isError":true`) {
		t.Fatalf("principal set_agent_capi failed: %s", resp)
	}

	a, _, _ := st.GetAgent(secondaryTerm)
	if a.Endpoint != "http://new:8787" {
		t.Errorf("Endpoint = %q, want http://new:8787", a.Endpoint)
	}
}

// TestRegisterAgentAndSetAgentCAPIPersistName (M1, ct-2026-07-22-1301):
// register_agent's optional "name" persists, and set_agent_capi both
// updates it when passed and preserves it when omitted (same pattern as
// endpoint/antenna_terminal_id/pinpass — TestSetAgentCAPIModifiesExisting).
func TestRegisterAgentAndSetAgentCAPIPersistName(t *testing.T) {
	st := openAgentDB(t)
	ctx, srv := buildAgentServer(t, st, nil)

	resp := callTool(t, agentCtx(ctx, secondaryTerm), srv, "register_agent", map[string]any{
		"endpoint":            "http://192.168.1.10:8787",
		"antenna_terminal_id": "ant-guid",
		"pinpass":             "abc123",
		"name":                "Sonnet",
	})
	if strings.Contains(resp, `"isError":true`) {
		t.Fatalf("register_agent failed: %s", resp)
	}
	a, _, _ := st.GetAgent(secondaryTerm)
	if a.Name != "Sonnet" {
		t.Errorf("Name after register_agent = %q, want Sonnet", a.Name)
	}

	// set_agent_capi WITHOUT name preserves it.
	resp = callTool(t, agentCtx(ctx, secondaryTerm), srv, "set_agent_capi", map[string]any{
		"agent_id": secondaryTerm,
		"endpoint": "http://new:8787",
	})
	if strings.Contains(resp, `"isError":true`) {
		t.Fatalf("set_agent_capi failed: %s", resp)
	}
	a, _, _ = st.GetAgent(secondaryTerm)
	if a.Name != "Sonnet" {
		t.Errorf("Name after set_agent_capi without name = %q, want preserved Sonnet", a.Name)
	}

	// set_agent_capi WITH name updates it.
	resp = callTool(t, agentCtx(ctx, secondaryTerm), srv, "set_agent_capi", map[string]any{
		"agent_id": secondaryTerm,
		"name":     "Sonnet renombrado",
	})
	if strings.Contains(resp, `"isError":true`) {
		t.Fatalf("set_agent_capi failed: %s", resp)
	}
	a, _, _ = st.GetAgent(secondaryTerm)
	if a.Name != "Sonnet renombrado" {
		t.Errorf("Name after set_agent_capi with name = %q, want %q", a.Name, "Sonnet renombrado")
	}
}

// TestListAgentsHidesPinpass: list_agents returns pinpass_set:bool, never raw pinpass.
func TestListAgentsHidesPinpass(t *testing.T) {
	st := openAgentDB(t)
	if err := st.UpsertAgent(store.Agent{
		AgentID: secondaryTerm, Endpoint: "http://x:8787",
		AntennaTerminalID: "ant-x", Pinpass: "supersecret", Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}

	ctx, srv := buildAgentServer(t, st, nil)

	resp := callTool(t, agentCtx(ctx, secondaryTerm), srv, "list_agents", nil)

	// Parse the inner text (list_agents returns a JSON array as the tool text).
	var out struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		t.Fatalf("parse response: %v\nraw: %s", err, resp)
	}
	if len(out.Result.Content) == 0 {
		t.Fatalf("list_agents: no content, got: %s", resp)
	}
	inner := out.Result.Content[0].Text

	if strings.Contains(inner, "supersecret") {
		t.Error("list_agents leaked the raw pinpass in inner text")
	}
	if !strings.Contains(inner, `"pinpass_set": true`) {
		t.Errorf("list_agents: expected pinpass_set:true in inner text, got: %s", inner)
	}
	if !strings.Contains(inner, secondaryTerm) {
		t.Errorf("list_agents: expected agent %q in inner text, got: %s", secondaryTerm, inner)
	}
}

// TestListAgentsIncludesPrincipal is the regression test for ct-2026-07-29
// (agentes paso 3): before this, list_agents only ever saw secondaries —
// the principal was invisible by MCP even though GET /api/agents (REST)
// always showed it. Principal comes first, same order handleAgents (REST)
// already uses.
func TestListAgentsIncludesPrincipal(t *testing.T) {
	st := openAgentDB(t)
	if err := st.SetPrincipalAgent("Boss", "http://127.0.0.1:8787", "ant-principal", "p1n=="); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAgent(store.Agent{
		AgentID: secondaryTerm, Name: "Sonnet", Endpoint: "http://x:8787",
		AntennaTerminalID: "ant-x", Pinpass: "p2", Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}
	ctx, srv := buildAgentServer(t, st, nil)

	resp := callTool(t, agentCtx(ctx, secondaryTerm), srv, "list_agents", nil)
	var out struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		t.Fatalf("parse response: %v\nraw: %s", err, resp)
	}
	if len(out.Result.Content) == 0 {
		t.Fatalf("list_agents: no content, got: %s", resp)
	}
	var rows []struct {
		AgentID string `json:"agent_id"`
		Name    string `json:"name"`
		Role    string `json:"role"`
	}
	if err := json.Unmarshal([]byte(out.Result.Content[0].Text), &rows); err != nil {
		t.Fatalf("parse inner list: %v\nraw: %s", err, out.Result.Content[0].Text)
	}
	if len(rows) != 2 {
		t.Fatalf("list_agents = %+v, want 2 rows (principal + secondary)", rows)
	}
	if rows[0].AgentID != principalTerm || rows[0].Name != "Boss" || rows[0].Role != "principal" {
		t.Errorf("rows[0] = %+v, want the principal first", rows[0])
	}
	if rows[1].AgentID != secondaryTerm || rows[1].Name != "Sonnet" || rows[1].Role != "secondary" {
		t.Errorf("rows[1] = %+v, want the secondary", rows[1])
	}
}

// TestRegisterAgentRejectsEmptyCallerID: a request with no terminal_id in
// context (callerID=="") must be refused — registering with an empty agent_id
// would pollute the store and the injector map.
func TestRegisterAgentRejectsEmptyCallerID(t *testing.T) {
	st := openAgentDB(t)
	ctx, srv := buildAgentServer(t, st, nil)

	// agentCtx with "" simulates a request with no X-Piumy-Terminal-Id header.
	resp := callTool(t, agentCtx(ctx, ""), srv, "register_agent", map[string]any{
		"endpoint":            "http://evil:8787",
		"antenna_terminal_id": "ant-evil",
		"pinpass":             "evil",
	})
	if !strings.Contains(resp, "no terminal_id") {
		t.Errorf("expected 'no terminal_id' error, got: %s", resp)
	}
}

// ── assign_chat_to_agent (M4, ct-2026-07-22-1301) ───────────────────────────

// TestSecondaryCanAssignChatToAgent is T148's own regression
// (ct-2026-09-07-1644, boss verbatim, August: "la asignacion es RUTEO, no
// permiso... los agentes se asignan para que capi los inyecte al terminal
// / pero no para hacer puertas de bloqueo"). assign_chat_to_agent's old
// PRINCIPAL-ONLY restriction is gone — replaces
// TestAssignChatToAgentPrincipalOnly, which proved exactly that
// restriction.
func TestSecondaryCanAssignChatToAgent(t *testing.T) {
	st := openAgentDB(t)
	if err := st.UpsertAgent(store.Agent{AgentID: secondaryTerm, Role: "secondary"}); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("1@s.whatsapp.net", "Juan", 1); err != nil {
		t.Fatal(err)
	}
	ctx, srv := buildAgentServer(t, st, nil)

	// A secondary calling it — even to assign to ITSELF — must now succeed.
	resp := callTool(t, agentCtx(ctx, secondaryTerm), srv, "assign_chat_to_agent", map[string]any{
		"chat_id": "1@s.whatsapp.net", "agent_id": secondaryTerm,
	})
	if strings.Contains(resp, "forbidden") {
		t.Errorf("assign_chat_to_agent from a secondary = %s, want it to succeed — T148 removed this restriction", resp)
	}
	c, _, _ := st.GetChat("1@s.whatsapp.net")
	if c.Status != "agent_exclusive:"+secondaryTerm {
		t.Errorf("chat status = %q, want %q (the assignment should have persisted)", c.Status, "agent_exclusive:"+secondaryTerm)
	}
}

func TestAssignChatToAgentByPrincipal(t *testing.T) {
	st := openAgentDB(t)
	if err := st.UpsertAgent(store.Agent{AgentID: secondaryTerm, Role: "secondary"}); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("1@s.whatsapp.net", "Juan", 1); err != nil {
		t.Fatal(err)
	}
	ctx, srv := buildAgentServer(t, st, nil)

	resp := callTool(t, agentCtx(ctx, principalTerm), srv, "assign_chat_to_agent", map[string]any{
		"chat_id": "1@s.whatsapp.net", "agent_id": secondaryTerm,
	})
	if strings.Contains(resp, `"isError":true`) {
		t.Fatalf("assign_chat_to_agent (principal) failed: %s", resp)
	}
	c, _, _ := st.GetChat("1@s.whatsapp.net")
	if c.Status != "agent_exclusive:"+secondaryTerm {
		t.Errorf("Status = %q, want agent_exclusive:%s", c.Status, secondaryTerm)
	}

	// agent_id omitted (empty) unassigns — same tool, both directions.
	resp = callTool(t, agentCtx(ctx, principalTerm), srv, "assign_chat_to_agent", map[string]any{
		"chat_id": "1@s.whatsapp.net",
	})
	if strings.Contains(resp, `"isError":true`) {
		t.Fatalf("assign_chat_to_agent (unassign) failed: %s", resp)
	}
	c, _, _ = st.GetChat("1@s.whatsapp.net")
	if c.Status != "new" {
		t.Errorf("Status after unassign = %q, want new", c.Status)
	}
}

func TestAssignChatToAgentRejectsPrincipalAsTarget(t *testing.T) {
	st := openAgentDB(t)
	if err := st.TouchChat("1@s.whatsapp.net", "Juan", 1); err != nil {
		t.Fatal(err)
	}
	ctx, srv := buildAgentServer(t, st, nil)

	resp := callTool(t, agentCtx(ctx, principalTerm), srv, "assign_chat_to_agent", map[string]any{
		"chat_id": "1@s.whatsapp.net", "agent_id": principalTerm,
	})
	if strings.Contains(resp, `"isError":true`) == false {
		t.Errorf("expected an error assigning to the principal itself, got: %s", resp)
	}
	c, _, _ := st.GetChat("1@s.whatsapp.net")
	if c.Status == "agent_exclusive:"+principalTerm {
		t.Error("assignment to the principal must be rejected, not persisted")
	}
}

func TestAssignChatToAgentRejectsUnknownAgent(t *testing.T) {
	st := openAgentDB(t)
	if err := st.TouchChat("1@s.whatsapp.net", "Juan", 1); err != nil {
		t.Fatal(err)
	}
	ctx, srv := buildAgentServer(t, st, nil)

	resp := callTool(t, agentCtx(ctx, principalTerm), srv, "assign_chat_to_agent", map[string]any{
		"chat_id": "1@s.whatsapp.net", "agent_id": "nunca-registrado",
	})
	if strings.Contains(resp, `"isError":true`) == false {
		t.Errorf("expected an error for an unknown agent_id, got: %s", resp)
	}
}

// TestDeleteAgentUnassignsChatsAndNotifies is the MCP-side twin of
// restapi's TestDeleteAgentEndpoint(UnassignsOrphanedChats) — same store
// path (UnassignAllChatsForAgent → DeleteAgent → OnAgentDelete), so both
// callers get the identical real effect (ct-2026-07-29, agentes paso 3
// criterio de listo: "el eliminar por MCP tiene el mismo efecto real que
// por REST"). Whether that OnAgentDelete hook actually stops dispatch is
// proven once, at the source, by capipush's
// TestUnregisterInjectorStopsDispatchToOldCredentials — both REST and MCP
// wire the exact same closure in main.go, so proving the closure fires
// with the right agentID here is sufficient; it would be redundant to
// re-prove capipush's own behavior from this package.
func TestDeleteAgentUnassignsChatsAndNotifies(t *testing.T) {
	st := openAgentDB(t)
	if err := st.UpsertAgent(store.Agent{AgentID: secondaryTerm, Name: "Sonnet", Role: "secondary"}); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("1@s.whatsapp.net", "Juan", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatus("1@s.whatsapp.net", "agent_exclusive:"+secondaryTerm); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("2@s.whatsapp.net", "Ana", 2); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatus("2@s.whatsapp.net", "agent_exclusive:"+secondaryTerm); err != nil {
		t.Fatal(err)
	}
	// A chat assigned to a DIFFERENT agent must not be touched.
	if err := st.UpsertAgent(store.Agent{AgentID: otherSecondaryTerm, Role: "secondary"}); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("3@s.whatsapp.net", "Otro", 3); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatus("3@s.whatsapp.net", "agent_exclusive:"+otherSecondaryTerm); err != nil {
		t.Fatal(err)
	}
	var deleted string
	ctx, srv := buildAgentServerFull(t, st, nil, func(agentID string) { deleted = agentID })

	resp := callTool(t, agentCtx(ctx, principalTerm), srv, "delete_agent", map[string]any{"agent_id": secondaryTerm})
	if strings.Contains(resp, `"isError":true`) {
		t.Fatalf("delete_agent failed: %s", resp)
	}
	if !strings.Contains(resp, `chats_unassigned\":2`) {
		t.Errorf("expected chats_unassigned:2 in response, got: %s", resp)
	}
	if _, ok, _ := st.GetAgent(secondaryTerm); ok {
		t.Error("agent must be gone from the store")
	}
	if deleted != secondaryTerm {
		t.Errorf("OnAgentDelete called with %q, want %q — the live injector must be unregistered", deleted, secondaryTerm)
	}
	c1, _, _ := st.GetChat("1@s.whatsapp.net")
	if c1.Status != "new" {
		t.Errorf("chat 1 Status = %q, want new", c1.Status)
	}
	c2, _, _ := st.GetChat("2@s.whatsapp.net")
	if c2.Status != "new" {
		t.Errorf("chat 2 Status = %q, want new", c2.Status)
	}
	c3, _, _ := st.GetChat("3@s.whatsapp.net")
	if c3.Status != "agent_exclusive:"+otherSecondaryTerm {
		t.Errorf("chat 3 (a different agent's) Status = %q, must not be touched", c3.Status)
	}
}

// TestDeleteAgentAllowsNonPrincipalCaller is T148's own regression
// (ct-2026-09-07-1644, boss verbatim, direct: "quiero que quites ese
// candado... resulta ser que piumy estaba lleno de candados estupidos").
// delete_agent's old PRINCIPAL-ONLY restriction is gone — replaces
// TestDeleteAgentRejectsNonPrincipalCaller, which proved exactly that
// restriction.
func TestDeleteAgentAllowsNonPrincipalCaller(t *testing.T) {
	st := openAgentDB(t)
	if err := st.UpsertAgent(store.Agent{AgentID: secondaryTerm, Role: "secondary"}); err != nil {
		t.Fatal(err)
	}
	var deleted string
	ctx, srv := buildAgentServerFull(t, st, nil, func(agentID string) { deleted = agentID })

	resp := callTool(t, agentCtx(ctx, otherSecondaryTerm), srv, "delete_agent", map[string]any{"agent_id": secondaryTerm})
	if strings.Contains(resp, "forbidden") {
		t.Errorf("delete_agent from a non-principal caller = %s, want it to succeed — T148 removed this restriction", resp)
	}
	if _, ok, _ := st.GetAgent(secondaryTerm); ok {
		t.Error("delete_agent from a non-principal caller did not persist")
	}
	if deleted != secondaryTerm {
		t.Errorf("OnAgentDelete = %q, want %q", deleted, secondaryTerm)
	}
}

// TestDeleteAgentAllowsPrincipalAsTarget (T76, ct-2026-08-27-1752): the
// principal can now be deleted by MCP too — boss verbatim, no gate to
// soften it: "a mí no me interesa [los problemas]... yo lo quiero, como
// yo lo digo". "Deleted" means back to the fresh-install unconfigured
// state (ClearPrincipalAgent), not an error.
func TestDeleteAgentAllowsPrincipalAsTarget(t *testing.T) {
	st := openAgentDB(t)
	if err := st.SetPrincipalAgent("Boss", "http://192.168.1.10:8787", principalTerm, "s3cr3t=="); err != nil {
		t.Fatal(err)
	}
	ctx, srv := buildAgentServer(t, st, nil)

	resp := callTool(t, agentCtx(ctx, principalTerm), srv, "delete_agent", map[string]any{"agent_id": principalTerm})
	if strings.Contains(resp, `"isError":true`) {
		t.Fatalf("expected deleting the principal to succeed, got: %s", resp)
	}
	got, ok, err := st.PrincipalAgent(principalTerm)
	if err != nil || !ok || got.Endpoint != "" || got.Name != "" {
		t.Errorf("PrincipalAgent after delete = %+v, ok=%v, err=%v, want empty (fresh-install state)", got, ok, err)
	}
}

func TestDeleteAgentRejectsUnknownAgent(t *testing.T) {
	st := openAgentDB(t)
	ctx, srv := buildAgentServer(t, st, nil)

	resp := callTool(t, agentCtx(ctx, principalTerm), srv, "delete_agent", map[string]any{"agent_id": "nunca-registrado"})
	if !strings.Contains(resp, `"isError":true`) {
		t.Errorf("expected an error for an unknown agent_id, got: %s", resp)
	}
}

// TestPromoteToPrincipalTool (T76, ct-2026-08-27-1752) — self-service via
// MCP: boss verbatim "que también el agente lo puede hacer por MCP". The
// caller (secondaryTerm) becomes principal with its own credentials; the
// old principal's data survives at secondaryTerm's own vacated agent_id.
func TestPromoteToPrincipalTool(t *testing.T) {
	st := openAgentDB(t)
	if err := st.SetPrincipalAgent("Vieja", "http://192.168.1.10:8787", principalTerm, "pin-old"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAgent(store.Agent{AgentID: secondaryTerm, Name: "Nueva", Endpoint: "http://192.168.1.20:8787", AntennaTerminalID: "ant-new", Pinpass: "pin-new", Role: "secondary"}); err != nil {
		t.Fatal(err)
	}
	ctx, srv := buildAgentServer(t, st, nil)

	resp := callTool(t, agentCtx(ctx, secondaryTerm), srv, "promote_to_principal", map[string]any{})
	if strings.Contains(resp, `"isError":true`) {
		t.Fatalf("promote_to_principal failed: %s", resp)
	}

	newPrincipal, ok, err := st.PrincipalAgent(principalTerm)
	if err != nil || !ok || newPrincipal.Name != "Nueva" || newPrincipal.AntennaTerminalID != "ant-new" {
		t.Errorf("principal after promote = %+v, ok=%v, err=%v, want the caller's own data", newPrincipal, ok, err)
	}
	demoted, ok, err := st.GetAgent(secondaryTerm)
	if err != nil || !ok || demoted.Name != "Vieja" || demoted.Endpoint != "http://192.168.1.10:8787" || demoted.Role != "secondary" {
		t.Errorf("demoted agent = %+v, ok=%v, err=%v, want the old principal's data intact", demoted, ok, err)
	}
}

// TestPromoteToPrincipalRejectsAlreadyPrincipal: no-op guard, not a "you
// can't" gate — the principal calling this on itself is just meaningless
// (nothing to swap with), not something T76's "sin candados" applies to.
func TestPromoteToPrincipalRejectsAlreadyPrincipal(t *testing.T) {
	st := openAgentDB(t)
	if err := st.SetPrincipalAgent("Boss", "http://192.168.1.10:8787", principalTerm, "pin"); err != nil {
		t.Fatal(err)
	}
	ctx, srv := buildAgentServer(t, st, nil)

	resp := callTool(t, agentCtx(ctx, principalTerm), srv, "promote_to_principal", map[string]any{})
	if !strings.Contains(resp, `"isError":true`) {
		t.Errorf("expected an error promoting the principal to itself, got: %s", resp)
	}
}

// TestPromoteToPrincipalRejectsUnregisteredCaller: a caller with no
// `agents` row (never called register_agent) has nothing to promote.
func TestPromoteToPrincipalRejectsUnregisteredCaller(t *testing.T) {
	st := openAgentDB(t)
	if err := st.SetPrincipalAgent("Boss", "http://192.168.1.10:8787", principalTerm, "pin"); err != nil {
		t.Fatal(err)
	}
	ctx, srv := buildAgentServer(t, st, nil)

	resp := callTool(t, agentCtx(ctx, "nunca-registrado"), srv, "promote_to_principal", map[string]any{})
	if !strings.Contains(resp, `"isError":true`) {
		t.Errorf("expected an error for an unregistered caller, got: %s", resp)
	}
}

// TestAgentToolsAreNotBossOnly: register_agent/set_agent_capi/list_agents/
// assign_chat_to_agent/delete_agent/promote_to_principal (T76) must NOT be
// in bossOnlyTools — their access control is role-based internally
// (PrincipalTerminalID checks, not the boss-chat bossOnlyTools gate — see
// assign_chat_to_agent's own doc comment for why those two are different
// axes). promote_to_principal specifically MUST be self-service (any
// registered secondary, not just the principal) — boss verbatim: "que
// también el agente lo puede hacer por MCP".
func TestAgentToolsAreNotBossOnly(t *testing.T) {
	agentTools := []string{"register_agent", "set_agent_capi", "list_agents", "assign_chat_to_agent", "delete_agent", "promote_to_principal"}
	for _, name := range agentTools {
		if bossOnlyTools[name] {
			t.Errorf("tool %q must not be boss-only", name)
		}
	}
	found := map[string]bool{}
	for _, tool := range listTools(t) {
		found[tool.Name] = true
	}
	for _, name := range agentTools {
		if !found[name] {
			t.Errorf("tool %q not registered in the MCP server", name)
		}
	}
}
