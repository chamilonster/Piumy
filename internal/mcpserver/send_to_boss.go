// send_to_boss — T39 (ct-2026-08-08-1619), boss verbatim: "que tal una
// herramienta: send to boss en el mcp, que pueda usarlo cualqueira que
// tenga el mcp?", followed immediately by the amendment that defines the
// design: "pero que el agente se identifique".
//
// Deliberately OUTSIDE levelGateMiddleware (levelgate.go) — the first
// outbound tool in that condition, on purpose, not an oversight: every
// other send path (send_message/draft) requires an active dispatch, so a
// terminal with none can do nothing at all today (default-DENY). The real
// case this closes: an agent mid-task ("avisame por WhatsApp cuando
// termines") has no dispatch to answer and, until now, no way to reach the
// owner either.
//
// It's safe outside the gate for two reasons send_message doesn't share:
//   - No destination argument. The target is always store.BossJIDs() —
//     accepting a chat_id here would just be send_message with the
//     anti-leakage gate removed.
//   - The caller can't declare who it is. Identity comes from the
//     CONNECTION (X-Piumy-Terminal-Id, terminalIDFromContext), never a
//     parameter — nothing an agent types is trusted, only who it's
//     connected as.
//
// T77 (ct-2026-08-27-1753) removed the OLD hard refusal for an unrecognized
// terminal_id ("call register_agent first") — that refusal WAS the "bug"
// the owner reported (measured by Citrino: send_to_boss delivered fine, the
// refusal is what read as broken from outside). The owner's actual design,
// verbatim: "un agente me habla por MCP usando 'to boss'... registra su
// antena... en una pura salida... registración volátil... así se puede
// comunicar cualquier agente sin pisar al agente principal". Any agent with
// the MCP key can call this now, registered or not (no new gate — "los
// agentes son responsabilidad de quien los conecta"); an unregistered
// caller can OPTIONALLY attach its antenna's credentials (endpoint,
// antenna_terminal_id, pinpass — same 3 fields register_agent already
// takes) to this SAME call, and this tool pings that antenna for real
// (owner verbatim: "no es de papel, tiene que ser validado con un PING
// antes") before deciding the header's 📡✅/❌. See addSendToBossTool's own
// doc for the full header/registration shape.
package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// senderNameFor resolves termID's display name for the [name] prefix
// send_to_boss puts on every message — the principal (synthesized, never a
// real `agents` row — store.PrincipalAgent's own doc) or a registered
// secondary. ok=false means termID matches neither: since T77 this is no
// longer refused, the caller falls back to termID itself as its own name
// (an ephemeral agent has no registered display name to show).
//
// Secondaries are matched against AntennaTerminalID (via ListAgents), NOT
// GetAgent(termID)/AgentID: AgentID is fixed at registration (register_agent
// sets it to the CALLER's terminal_id at that moment), while
// AntennaTerminalID is the field set_agent_capi actually updates later. An
// agent whose antenna_terminal_id changed after registering would still
// connect with agent_id != its current X-Piumy-Terminal-Id — GetAgent(termID)
// would wrongly refuse a legitimately registered agent in that case. No new
// query: ListAgents already exists, this just filters it (Citrino's own
// instruction — match the CURRENT connection value, not the fixed identity).
//
// Falls back to termID itself when the matched agent has no Name set — the
// boss asked for "el ID de capi" as the signature; a registered display
// name is used instead when it exists because it's legible, with the raw
// id as the fallback that always works (revisable — flagged to Citrino).
func senderNameFor(d Deps, termID string) (name string, ok bool, err error) {
	if d.PrincipalTerminalID != "" && termID == d.PrincipalTerminalID {
		principal, found, err := d.Store.PrincipalAgent(d.PrincipalTerminalID)
		if err != nil || !found {
			return "", found, err
		}
		if principal.Name != "" {
			return principal.Name, true, nil
		}
		return termID, true, nil
	}
	agents, err := d.Store.ListAgents()
	if err != nil {
		return "", false, err
	}
	for _, a := range agents {
		if a.AntennaTerminalID != termID {
			continue
		}
		if a.Name != "" {
			return a.Name, true, nil
		}
		return termID, true, nil
	}
	return "", false, nil
}

// addSendToBossTool wires send_to_boss. T77's ephemeral-antenna shape,
// owner-defined (his own note replaced "you choose the tool shape" once he
// tried it and it half-failed): send_to_boss PIDE the caller's antenna,
// attaching it is OPTIONAL, and the header on the resulting WhatsApp
// message DECLARES up front whether a cited reply has anywhere to go —
// "[Nombre] 📡 ✅" or "[Nombre] 📡 ❌", one line, above the text, ONLY on
// this kind of message (never inside a normal chat with a third party —
// that would break the Turing-test rule already settled elsewhere).
//
//   - Already-registered caller (principal or a real `agents` secondary):
//     UNCHANGED from before T77 — "[name] text", no header, no antenna
//     params processed even if supplied (its replies already route through
//     its permanent injector; nothing here would improve on that, and
//     shadowing it with a short-lived entry would be a straight downgrade).
//   - Unregistered caller: name falls back to termID (senderNameFor's own
//     ok=false case). No antenna supplied -> "❌", nothing registered,
//     message sends anyway (antenna is optional, never a requirement to
//     send). Antenna supplied -> d.SendToBossAntenna pings it for real
//     (bounded — "el mensaje nunca se bloquea por un ping lento", the ping
//     NEVER stops the send either way) and registers it as this terminal's
//     ephemeral reply target regardless of the ping result (Citrino: a
//     known-down destination still gets Store.PrincipalAgent-style
//     bookkeeping so a cited reply gets a real channel-down notice instead
//     of silence) — see capipush.RegisterEphemeralInjector's own doc.
func addSendToBossTool(s *server.MCPServer, d Deps, tracker *agentTracker) {
	s.AddTool(mcp.NewTool("send_to_boss",
		mcp.WithDescription("Send a WhatsApp message to the owner — no active dispatch required, unlike send_message, and no prior register_agent required either. Use it to reach the owner outside a normal conversation turn (e.g. \"tell me when you're done\"). There is no destination argument: it always goes to the owner's own chat(s). The message is prefixed with your registered name (or terminal_id if you're not registered) so the owner knows who's writing when more than one agent uses this. If you're NOT a registered agent, you may optionally attach your antenna's credentials (endpoint/antenna_terminal_id/pinpass — all three or none) in this same call: Piumy pings it for real before sending and marks the message '📡 ✅' (reachable) or '📡 ❌' (not) so the owner knows up front whether replying by quoting this message will reach you. If the ping succeeds, that antenna is registered as YOUR reply target for a limited time (not a permanent agent — it never appears in list_agents) so a quoted reply routes back to you instead of the principal. Queued through the normal anti-ban outbox, not sent instantly."),
		mcp.WithString("text", mcp.Required(), mcp.Description("The message text, sent as-is after the header")),
		mcp.WithString("endpoint", mcp.Description("Optional: your antenna's cAPI endpoint URL (e.g. http://192.168.1.10:8787) — only meaningful if you're not already a registered agent; ignored otherwise")),
		mcp.WithString("antenna_terminal_id", mcp.Description("Optional: your antenna's terminal_id (chat GUID from CleverCoder) — required together with endpoint/pinpass if you attach one")),
		mcp.WithString("pinpass", mcp.Description("Optional: your antenna's pinpass (base64 form as copied) — required together with endpoint/antenna_terminal_id if you attach one"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			termID := terminalIDFromContext(ctx)
			if termID == "" {
				return mcp.NewToolResultError("no terminal_id in context; set X-Piumy-Terminal-Id header"), nil
			}
			name, registered, err := senderNameFor(d, termID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("store: %v", err)), nil
			}
			if !registered {
				name = termID
			}

			text, err := r.RequireString("text")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			bossJIDs, err := d.Store.BossJIDs()
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("store: %v", err)), nil
			}
			if len(bossJIDs) == 0 {
				return mcp.NewToolResultError("refused: no chat is marked as the owner (is_boss) yet"), nil
			}

			signed := "[" + name + "] " + text
			if !registered {
				endpoint := r.GetString("endpoint", "")
				antTerminalID := r.GetString("antenna_terminal_id", "")
				pinpass := r.GetString("pinpass", "")
				hasAntenna := endpoint != "" || antTerminalID != "" || pinpass != ""
				if hasAntenna && (endpoint == "" || antTerminalID == "" || pinpass == "") {
					return mcp.NewToolResultError("endpoint, antenna_terminal_id and pinpass travel together — all three or none"), nil
				}
				pingOK := false
				if hasAntenna && d.SendToBossAntenna != nil {
					pingOK = d.SendToBossAntenna(termID, endpoint, antTerminalID, pinpass)
				}
				status := "❌"
				if pingOK {
					status = "✅"
				}
				signed = "[" + name + "] 📡 " + status + "\n" + text
			}

			now := time.Now().Unix()
			for _, jid := range bossJIDs {
				if err := d.Store.EnqueueFromAgent(jid, signed, now, termID); err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("store: %v", err)), nil
				}
			}
			return mcp.NewToolResultText("queued for sending"), nil
		})
}
