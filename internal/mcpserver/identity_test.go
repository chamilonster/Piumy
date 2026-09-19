// T104 (ct-2026-08-29-1818): an agent had no way to learn its own effective
// identity — which terminal_id the gateway sees it as, whether that's the
// principal, or whether its header presents an agent's antenna_terminal_id
// instead of its agent_id (a wiring mismatch that silently starves every
// dispatch to that terminal — Citrino hit this twice in production and
// told the boss, wrongly, that he wasn't the principal agent). Directive
// from the boss (verbatim, ct-2026-08-29): "a veces es mejor menos
// herramientas que entreguen mas informacion, asi es mas simple" — no new
// tool, these fields ride inside get_status, which every agent already
// calls.
package mcpserver

import (
	"bytes"
	"encoding/json"
	"log"
	"testing"

	"piumy-gateway/internal/store"
)

// ownIdentityFields decodes the subset of get_status's payload this file's
// tests care about.
type ownIdentityFields struct {
	OwnTerminalID          string `json:"own_terminal_id"`
	IsPrincipal            bool   `json:"is_principal"`
	MatchedAgentID         string `json:"matched_agent_id,omitempty"`
	MatchedAgentName       string `json:"matched_agent_name,omitempty"`
	AntennaResolvedAgentID string `json:"antenna_resolved_agent_id,omitempty"`
	AntennaResolvedNote    string `json:"antenna_resolved_note,omitempty"`
}

func decodeOwnIdentity(t *testing.T, out string) ownIdentityFields {
	t.Helper()
	text := decodeToolText(t, out)
	var f ownIdentityFields
	if err := json.Unmarshal([]byte(text), &f); err != nil {
		t.Fatalf("decoding get_status identity fields: %v\nraw: %s", err, text)
	}
	return f
}

func TestGetStatusReportsOwnTerminalIDAndIsPrincipal(t *testing.T) {
	st := openAgentDB(t)
	ctx, srv := buildAgentServer(t, st, nil)

	out := callTool(t, agentCtx(ctx, principalTerm), srv, "get_status", nil)
	f := decodeOwnIdentity(t, out)

	if f.OwnTerminalID != principalTerm {
		t.Errorf("own_terminal_id = %q, want %q", f.OwnTerminalID, principalTerm)
	}
	if !f.IsPrincipal {
		t.Errorf("is_principal = false, want true when calling terminal == PrincipalTerminalID")
	}
}

func TestGetStatusIsPrincipalFalseForSecondary(t *testing.T) {
	st := openAgentDB(t)
	ctx, srv := buildAgentServer(t, st, nil)

	out := callTool(t, agentCtx(ctx, secondaryTerm), srv, "get_status", nil)
	f := decodeOwnIdentity(t, out)

	if f.OwnTerminalID != secondaryTerm {
		t.Errorf("own_terminal_id = %q, want %q", f.OwnTerminalID, secondaryTerm)
	}
	if f.IsPrincipal {
		t.Error("is_principal = true for a terminal that isn't the configured principal")
	}
}

func TestGetStatusReportsOwnTerminalIDEmptyWithoutHeader(t *testing.T) {
	_, srv, ctx, _ := newTestServer(t)

	out := callTool(t, ctx, srv, "get_status", nil)
	f := decodeOwnIdentity(t, out)

	if f.OwnTerminalID != "" {
		t.Errorf("own_terminal_id = %q, want empty when no X-Piumy-Terminal-Id was presented", f.OwnTerminalID)
	}
	if f.IsPrincipal {
		t.Error("is_principal = true with no terminal_id at all")
	}
}

// TestGetStatusMatchesRegisteredSecondaryAgent: a terminal presenting its
// own agent_id (the id it registered under) is recognized, by name.
func TestGetStatusMatchesRegisteredSecondaryAgent(t *testing.T) {
	st := openAgentDB(t)
	if err := st.UpsertAgent(store.Agent{
		AgentID: secondaryTerm, Name: "Sonnet", Endpoint: "http://x:8787",
		AntennaTerminalID: "ant-x", Pinpass: "p2", Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}
	ctx, srv := buildAgentServer(t, st, nil)

	out := callTool(t, agentCtx(ctx, secondaryTerm), srv, "get_status", nil)
	f := decodeOwnIdentity(t, out)

	if f.MatchedAgentID != secondaryTerm || f.MatchedAgentName != "Sonnet" {
		t.Errorf("matched_agent_id/name = %q/%q, want %q/%q", f.MatchedAgentID, f.MatchedAgentName, secondaryTerm, "Sonnet")
	}
	if f.AntennaResolvedAgentID != "" || f.AntennaResolvedNote != "" {
		t.Errorf("antenna-resolved fields set for a terminal presenting its own agent_id correctly: %+v", f)
	}
}

// TestGetStatusReportsAntennaResolvedPrincipalTerminal reproduces the exact
// production shape from T104's own evidence: PIUMY_DEFAULT_TERMINAL_ID
// (agent_id) was "principal", but the terminal presented the ANTENNA's
// terminal_id instead. T129 (ct-2026-09-03-0200) changed what this MEANS:
// the gate now resolves this path too (see gate_test.go), so get_status
// reports it as informative, not a warning — and logs nothing, since a
// terminal that legitimately authenticates via its antenna forever would
// otherwise spam the log on every single get_status call.
func TestGetStatusReportsAntennaResolvedPrincipalTerminal(t *testing.T) {
	st := openAgentDB(t)
	if err := st.SetPrincipalAgent("Citrino", "http://192.168.1.77:8788", "capi-piumy-gateway-citrino-6220d378", "pin"); err != nil {
		t.Fatal(err)
	}
	ctx, srv := buildAgentServer(t, st, nil)

	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })

	// The terminal presents the antenna's id (antenna_terminal_id), NOT the
	// configured agent_id (principalTerm) — exactly the mismatch T104 exists
	// to surface, now via the informative fields instead of a warning.
	out := callTool(t, agentCtx(ctx, "capi-piumy-gateway-citrino-6220d378"), srv, "get_status", nil)
	f := decodeOwnIdentity(t, out)

	if f.IsPrincipal {
		t.Error("is_principal = true for the antenna id, want false — only the configured agent_id counts")
	}
	if f.MatchedAgentID != "" {
		t.Errorf("matched_agent_id = %q, want empty — this id is NOT anyone's agent_id", f.MatchedAgentID)
	}
	if f.AntennaResolvedAgentID != principalTerm {
		t.Errorf("antenna_resolved_agent_id = %q, want %q (the principal's real agent_id)", f.AntennaResolvedAgentID, principalTerm)
	}
	if f.AntennaResolvedNote == "" {
		t.Error("antenna_resolved_note is empty, want an explicit note naming which id this terminal presents")
	}
	if buf.String() != "" {
		t.Errorf("get_status logged %q for a routine antenna match — want silence (T129 removed the per-call log, it's no longer a problem)", buf.String())
	}
}

func TestGetStatusUnknownTerminalLeavesIdentityFieldsEmpty(t *testing.T) {
	st := openAgentDB(t)
	if err := st.SetPrincipalAgent("Boss", "http://127.0.0.1:8787", "ant-principal", "pin"); err != nil {
		t.Fatal(err)
	}
	ctx, srv := buildAgentServer(t, st, nil)

	out := callTool(t, agentCtx(ctx, "term-nadie-lo-conoce"), srv, "get_status", nil)
	f := decodeOwnIdentity(t, out)

	if f.MatchedAgentID != "" || f.AntennaResolvedAgentID != "" {
		t.Errorf("an unrecognized terminal_id matched something: %+v", f)
	}
}
