package mcpserver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"piumy-gateway/internal/router"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
)

// newSendToBossAntennaTestServer mirrors newTestServer but wires a stub
// SendToBossAntenna (T77) the test controls — capturing what it was called
// with (calls) and returning stubPingOK, so the antenna-attach path is
// testable without a real capipush.Pusher/network round-trip.
func newSendToBossAntennaTestServer(t *testing.T, stubPingOK bool) (st *store.Store, srv *server.MCPServer, ctx context.Context, calls *[]sendToBossAntennaCall) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sm := state.NewManager(filepath.Join(dir, "status.json"), 8)
	rtMgr := router.NewManager(filepath.Join(dir, "router.json"))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	got := []sendToBossAntennaCall{}
	srv = New(ctx, Deps{
		Store: st, State: sm, Router: rtMgr, AgentIdle: time.Minute,
		SendToBossAntenna: func(termID, endpoint, antennaTerminalID, pinpass string) bool {
			got = append(got, sendToBossAntennaCall{termID, endpoint, antennaTerminalID, pinpass})
			return stubPingOK
		},
	})
	return st, srv, ctx, &got
}

type sendToBossAntennaCall struct {
	termID, endpoint, antennaTerminalID, pinpass string
}

// TestSendToBossRegisteredAgentQueuesWithSignedOriginAndFanOut is send_to_boss's
// main DoD case (T39, ct-2026-08-08-1619): a registered agent sends and the
// message ends up queued, once per is_boss chat, prefixed with the agent's
// name, with the origin recorded.
//
// AntennaTerminalID is DELIBERATELY set to a DIFFERENT value than AgentID —
// this is the regression for the identity-resolution bug Citrino's contract
// warned about: matching the connecting terminal against agents.agent_id
// (fixed at registration) instead of agents.antenna_terminal_id (the field
// set_agent_capi actually updates) would refuse a legitimately registered
// agent whose antenna terminal_id changed since registering. The connecting
// context here presents "term-live" — the CURRENT antenna_terminal_id, not
// the original agent_id "agent-A".
func TestSendToBossRegisteredAgentQueuesWithSignedOriginAndFanOut(t *testing.T) {
	st, mcpSrv, ctx, _ := newTestServer(t)

	if err := st.UpsertAgent(store.Agent{
		AgentID: "agent-A", Name: "Agente Uno",
		AntennaTerminalID: "term-live", Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}
	// Two boss chats — send_to_boss has no destination argument, so BOTH
	// must receive the message (store.BossJIDs' own fan-out, MANUAL.md).
	if err := st.SetIsBoss("555000010@s.whatsapp.net", true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIsBoss("555000011@s.whatsapp.net", true); err != nil {
		t.Fatal(err)
	}

	termCtx := withTerminalID(ctx, "term-live")
	out := callTool(t, termCtx, mcpSrv, "send_to_boss", map[string]any{"text": "ya terminé la tarea"})
	if strings.Contains(out, "isError\":true") {
		t.Fatalf("send_to_boss for a registered agent (matched via AntennaTerminalID) failed: %s", out)
	}

	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 {
		t.Fatalf("got %d queued items, want 2 (one per is_boss chat): %+v", len(pending), pending)
	}
	byJID := map[string]store.Outbox{}
	for _, o := range pending {
		byJID[o.ToJID] = o
	}
	for _, jid := range []string{"555000010@s.whatsapp.net", "555000011@s.whatsapp.net"} {
		o, ok := byJID[jid]
		if !ok {
			t.Fatalf("no queued item for boss chat %s — got %+v", jid, pending)
		}
		if o.OriginTerminalID != "term-live" {
			t.Errorf("%s: origin_terminal_id = %q, want the connecting terminal_id %q", jid, o.OriginTerminalID, "term-live")
		}
		if o.Text != "[Agente Uno] ya terminé la tarea" {
			t.Errorf("%s: text = %q, want signed with the agent's registered Name", jid, o.Text)
		}
	}
}

// TestSendToBossFallsBackToTerminalIDWhenNameEmpty covers the signature's
// fallback: no registered Name -> the raw terminal_id, per the contract's
// own text ("el ID de capi... con el id como respaldo").
func TestSendToBossFallsBackToTerminalIDWhenNameEmpty(t *testing.T) {
	st, mcpSrv, ctx, _ := newTestServer(t)
	if err := st.UpsertAgent(store.Agent{
		AgentID: "term-noname", AntennaTerminalID: "term-noname", Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIsBoss("555000012@s.whatsapp.net", true); err != nil {
		t.Fatal(err)
	}

	termCtx := withTerminalID(ctx, "term-noname")
	out := callTool(t, termCtx, mcpSrv, "send_to_boss", map[string]any{"text": "hola"})
	if strings.Contains(out, "isError\":true") {
		t.Fatalf("send_to_boss failed: %s", out)
	}
	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Text != "[term-noname] hola" {
		t.Fatalf("got %+v, want one item signed with the raw terminal_id (no Name registered)", pending)
	}
}

// TestSendToBossUnregisteredTerminalSendsWithFallbackNameAndNoHeader is T77's
// (ct-2026-08-27-1753) core reversal of the OLD refusal below (renamed, not
// deleted — same DoD case, opposite expectation): Citrino measured that
// send_to_boss delivering fine WAS the whole feature, the refusal is what
// read as broken from outside, and the owner's own design explicitly wants
// "cualquier agente... sin registro previo" to be able to write. No antenna
// attached (this test) -> queues anyway, signed with the raw terminal_id
// (senderNameFor's ok=false fallback) and a "❌" header — never blocked,
// never silently promoted to a lie.
func TestSendToBossUnregisteredTerminalSendsWithFallbackNameAndNoHeader(t *testing.T) {
	st, mcpSrv, ctx, _ := newTestServer(t)
	if err := st.SetIsBoss("555000013@s.whatsapp.net", true); err != nil {
		t.Fatal(err)
	}

	termCtx := withTerminalID(ctx, "term-never-registered")
	out := callTool(t, termCtx, mcpSrv, "send_to_boss", map[string]any{"text": "hola sin registrarme"})
	if strings.Contains(out, "isError\":true") {
		t.Fatalf("send_to_boss for an unregistered terminal_id (no antenna) = %s, want it to succeed (T77)", out)
	}

	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("got %d queued items, want 1: %+v", len(pending), pending)
	}
	want := "[term-never-registered] 📡 ❌\nhola sin registrarme"
	if pending[0].Text != want {
		t.Errorf("text = %q, want %q (fallback name + false header, no antenna attached)", pending[0].Text, want)
	}
	if pending[0].OriginTerminalID != "term-never-registered" {
		t.Errorf("origin_terminal_id = %q, want the connecting terminal_id", pending[0].OriginTerminalID)
	}
}

// TestSendToBossNoTerminalIDRefusesAndQueuesNothing covers the other half of
// the identity requirement: no X-Piumy-Terminal-Id at all (a bare/manual MCP
// call) is refused exactly like an unregistered one, nothing queued.
func TestSendToBossNoTerminalIDRefusesAndQueuesNothing(t *testing.T) {
	st, mcpSrv, ctx, _ := newTestServer(t)
	if err := st.SetIsBoss("555000014@s.whatsapp.net", true); err != nil {
		t.Fatal(err)
	}

	out := callTool(t, ctx, mcpSrv, "send_to_boss", map[string]any{"text": "sin terminal_id"})
	if !strings.Contains(out, "isError\":true") {
		t.Fatalf("send_to_boss with no terminal_id in context = %s, want an explicit error", out)
	}

	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("got %d queued items with no terminal_id, want 0: %+v", len(pending), pending)
	}
}

// TestSendToBossNoDispatchRequired confirms the whole point of the feature:
// a terminal with NO active dispatch at all — the default-DENY every other
// gated tool enforces — can still reach send_to_boss, as long as it's a
// registered agent. Never calls gate.RegisterDispatch/get_instructions.
func TestSendToBossNoDispatchRequired(t *testing.T) {
	st, mcpSrv, ctx, _ := newTestServer(t)
	if err := st.UpsertAgent(store.Agent{
		AgentID: "agent-nodispatch", AntennaTerminalID: "agent-nodispatch", Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIsBoss("555000015@s.whatsapp.net", true); err != nil {
		t.Fatal(err)
	}

	termCtx := withTerminalID(ctx, "agent-nodispatch")
	out := callTool(t, termCtx, mcpSrv, "send_to_boss", map[string]any{"text": "avisando sin dispatch activo"})
	if strings.Contains(out, "isError\":true") {
		t.Fatalf("send_to_boss with no active dispatch = %s, want it to succeed (the entire point of the tool)", out)
	}
}

// TestSendToBossNoBossChatConfiguredRefuses covers the edge case where
// BossJIDs() is empty — reporting "queued" while nothing actually went out
// would be a silent lie, so this must refuse explicitly instead.
func TestSendToBossNoBossChatConfiguredRefuses(t *testing.T) {
	st, mcpSrv, ctx, _ := newTestServer(t)
	if err := st.UpsertAgent(store.Agent{
		AgentID: "agent-noboss", AntennaTerminalID: "agent-noboss", Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}

	termCtx := withTerminalID(ctx, "agent-noboss")
	out := callTool(t, termCtx, mcpSrv, "send_to_boss", map[string]any{"text": "no hay a quién"})
	if !strings.Contains(out, "isError\":true") {
		t.Fatalf("send_to_boss with no is_boss chat configured = %s, want an explicit error, not a silent no-op", out)
	}
	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("got %d queued items with no boss chat configured, want 0: %+v", len(pending), pending)
	}
}

// TestSendToBossAntennaPingSucceedsShowsCheckmark is T77's other core case:
// an unregistered caller attaches its antenna, the (stubbed) ping succeeds,
// and the header shows "✅" — driven by the ping result, never by "the
// field came filled" (owner verbatim: "no es de papel").
func TestSendToBossAntennaPingSucceedsShowsCheckmark(t *testing.T) {
	st, mcpSrv, ctx, calls := newSendToBossAntennaTestServer(t, true)
	if err := st.SetIsBoss("555000016@s.whatsapp.net", true); err != nil {
		t.Fatal(err)
	}

	termCtx := withTerminalID(ctx, "term-ephemeral-1")
	out := callTool(t, termCtx, mcpSrv, "send_to_boss", map[string]any{
		"text": "hola con antena", "endpoint": "http://127.0.0.1:9701",
		"antenna_terminal_id": "555000016-efimero-term", "pinpass": "cGluZWY=",
	})
	if strings.Contains(out, "isError\":true") {
		t.Fatalf("send_to_boss with a valid antenna = %s, want success", out)
	}

	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	want := "[term-ephemeral-1] 📡 ✅\nhola con antena"
	if len(pending) != 1 || pending[0].Text != want {
		t.Fatalf("got %+v, want one item with text %q", pending, want)
	}
	if len(*calls) != 1 {
		t.Fatalf("SendToBossAntenna called %d times, want 1: %+v", len(*calls), *calls)
	}
	got := (*calls)[0]
	want2 := sendToBossAntennaCall{"term-ephemeral-1", "http://127.0.0.1:9701", "555000016-efimero-term", "cGluZWY="}
	if got != want2 {
		t.Errorf("SendToBossAntenna called with %+v, want %+v", got, want2)
	}
}

// TestSendToBossAntennaPingFailsShowsX confirms the ping — not the presence
// of the fields — decides the header: a stubbed-failing ping still lets the
// message through (antenna is never a requirement to send) but marks it "❌".
func TestSendToBossAntennaPingFailsShowsX(t *testing.T) {
	st, mcpSrv, ctx, _ := newSendToBossAntennaTestServer(t, false)
	if err := st.SetIsBoss("555000017@s.whatsapp.net", true); err != nil {
		t.Fatal(err)
	}

	termCtx := withTerminalID(ctx, "term-ephemeral-2")
	out := callTool(t, termCtx, mcpSrv, "send_to_boss", map[string]any{
		"text": "antena apagada", "endpoint": "http://127.0.0.1:9702",
		"antenna_terminal_id": "555000017-efimero-term", "pinpass": "cGluZWY=",
	})
	if strings.Contains(out, "isError\":true") {
		t.Fatalf("send_to_boss with a failing ping = %s, want it to still succeed (ping never blocks the send)", out)
	}
	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	want := "[term-ephemeral-2] 📡 ❌\nantena apagada"
	if len(pending) != 1 || pending[0].Text != want {
		t.Fatalf("got %+v, want one item with text %q", pending, want)
	}
}

// TestSendToBossAntennaPartialFieldsRefuses covers the "all three or none"
// validation — a half-pasted antenna can't be pinged, so it must be an
// explicit error, not silently ignored or silently pinged with a blank field.
func TestSendToBossAntennaPartialFieldsRefuses(t *testing.T) {
	st, mcpSrv, ctx, calls := newSendToBossAntennaTestServer(t, true)
	if err := st.SetIsBoss("555000018@s.whatsapp.net", true); err != nil {
		t.Fatal(err)
	}

	termCtx := withTerminalID(ctx, "term-ephemeral-3")
	out := callTool(t, termCtx, mcpSrv, "send_to_boss", map[string]any{
		"text": "antena a medias", "endpoint": "http://127.0.0.1:9703",
	})
	if !strings.Contains(out, "isError\":true") {
		t.Fatalf("send_to_boss with only endpoint set = %s, want an explicit error", out)
	}
	if len(*calls) != 0 {
		t.Errorf("SendToBossAntenna called with a partial antenna, want it never called: %+v", *calls)
	}
	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("got %d queued items for a partial-antenna refusal, want 0", len(pending))
	}
}

// TestSendToBossRegisteredAgentIgnoresAntennaParams confirms the antenna
// attach path is exclusively for a caller senderNameFor doesn't already
// recognize — a REGISTERED agent's message keeps its pre-T77 shape exactly
// ("[name] text", no header) even if it also passes antenna params, and
// SendToBossAntenna is never called (its permanent injector is untouched,
// never shadowed by a short-lived one).
func TestSendToBossRegisteredAgentIgnoresAntennaParams(t *testing.T) {
	st, mcpSrv, ctx, calls := newSendToBossAntennaTestServer(t, true)
	if err := st.UpsertAgent(store.Agent{
		AgentID: "agent-registered", Name: "Agente Registrado",
		AntennaTerminalID: "term-registered", Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIsBoss("555000019@s.whatsapp.net", true); err != nil {
		t.Fatal(err)
	}

	termCtx := withTerminalID(ctx, "term-registered")
	out := callTool(t, termCtx, mcpSrv, "send_to_boss", map[string]any{
		"text": "ya registrado", "endpoint": "http://127.0.0.1:9704",
		"antenna_terminal_id": "otra-antena", "pinpass": "cGluMw==",
	})
	if strings.Contains(out, "isError\":true") {
		t.Fatalf("send_to_boss for a registered agent = %s, want success", out)
	}
	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	want := "[Agente Registrado] ya registrado"
	if len(pending) != 1 || pending[0].Text != want {
		t.Fatalf("got %+v, want one item with text %q (no header, antenna params ignored)", pending, want)
	}
	if len(*calls) != 0 {
		t.Errorf("SendToBossAntenna called for an already-registered agent, want it never called: %+v", *calls)
	}
}
