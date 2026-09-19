// Own-identity resolution for get_status (T104, ct-2026-08-29-1818): an
// agent had no way to learn which terminal_id the gateway sees it as,
// whether that's the principal, or whether its header presents an agent's
// antenna_terminal_id instead of its agent_id (production evidence:
// Citrino hit this twice, concluded "I'm not the principal" from two
// indirect signals, and told the boss so — wrongly, twice). Directive from
// the boss: no new tool, these fields ride inside get_status.
//
// T129 (ct-2026-09-03-0200) changed what an antenna match MEANS: it used
// to be a silent dead end (nothing routed to that terminal, ever) worth a
// loud warning — gate.go's RegisterDispatch now resolves it, the exact
// same relationship computed here. A terminal presenting its antenna
// instead of its agent_id is informative now, not broken — the fields
// below were renamed from CrossWired*/"CABLEADO CRUZADO" to say that.
package mcpserver

import (
	"fmt"

	"piumy-gateway/internal/store"
)

// OwnIdentity is get_status's answer to "who am I, to this gateway".
type OwnIdentity struct {
	OwnTerminalID string `json:"own_terminal_id"`
	IsPrincipal   bool   `json:"is_principal"`
	// MatchedAgentID/Name are set when OwnTerminalID equals a registered
	// agent's agent_id (including the principal) — the healthy case.
	MatchedAgentID   string `json:"matched_agent_id,omitempty"`
	MatchedAgentName string `json:"matched_agent_name,omitempty"`
	// AntennaResolvedAgentID/Note are set when OwnTerminalID equals a
	// registered agent's antenna_terminal_id but NOT its agent_id (T129,
	// ct-2026-09-03-0200) — the gate resolves this path automatically now
	// (RegisterDispatch's own antenna alias), so this is a fact about which
	// id this terminal happens to present, not a warning: dispatches reach
	// it either way.
	AntennaResolvedAgentID string `json:"antenna_resolved_agent_id,omitempty"`
	AntennaResolvedNote    string `json:"antenna_resolved_note,omitempty"`
}

// resolveOwnIdentity matches the calling terminal against the principal
// (synthetic, from Store.PrincipalAgent) and every registered secondary —
// same candidate set list_agents already exposes, just compared against
// the CALLER's own id instead of listed for a human to eyeball.
//
// No log line for the antenna-match branch (T129 removed the old
// "CABLEADO CRUZADO" one on purpose): get_status runs on every routine
// call from a terminal that may legitimately authenticate via its antenna
// forever now — logging every time would just be noise for a normal,
// working state. gate.go's own RegisterDispatch is where a log line still
// earns its place: only when antenna resolution genuinely CAN'T happen
// (the ambiguous-antenna case), which is the situation actually worth a
// gateway operator's attention.
func resolveOwnIdentity(d Deps, ownTerminalID string) OwnIdentity {
	id := OwnIdentity{OwnTerminalID: ownTerminalID, IsPrincipal: ownTerminalID != "" && ownTerminalID == d.PrincipalTerminalID}
	if ownTerminalID == "" || d.Store == nil {
		return id
	}

	var candidates []store.Agent
	if principal, ok, err := d.Store.PrincipalAgent(d.PrincipalTerminalID); err == nil && ok {
		candidates = append(candidates, principal)
	}
	if agents, err := d.Store.ListAgents(); err == nil {
		candidates = append(candidates, agents...)
	}

	for _, c := range candidates {
		if c.AgentID == ownTerminalID {
			id.MatchedAgentID = c.AgentID
			id.MatchedAgentName = c.Name
			return id // exact agent_id match — the healthy case, nothing more to check
		}
	}
	for _, c := range candidates {
		if c.AntennaTerminalID != "" && c.AntennaTerminalID == ownTerminalID {
			id.AntennaResolvedAgentID = c.AgentID
			id.AntennaResolvedNote = fmt.Sprintf("This terminal presents %q, which is agent %q's (agent_id=%q) antenna_terminal_id, not its agent_id — the gate resolves this automatically (T129, ct-2026-09-03-0200), so dispatches reach it normally either way.", ownTerminalID, c.Name, c.AgentID)
			return id
		}
	}
	return id
}
