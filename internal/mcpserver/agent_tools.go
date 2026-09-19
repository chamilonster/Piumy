package mcpserver

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"piumy-gateway/internal/store"
)

func addAgentTools(s *server.MCPServer, d Deps) {
	// ── register_agent ───────────────────────────────────────────────────
	// Registers an agent with its own cAPI antenna — the calling terminal
	// by default, or a different one via agent_id (T142, ct-2026-09-07-1222,
	// boss verbatim: "quiten ese candado, no tiene logica" — the old refusal
	// of the principal as caller protected nothing, since
	// POST /api/admin/agent-create already creates any agent unchecked).
	// The principal's live injector still can't be hijacked through here:
	// that protection lives in capipush.Pusher.RegisterInjector, which
	// no-ops on agentID == PortFallback regardless of what this tool writes.
	s.AddTool(mcp.NewTool("register_agent",
		mcp.WithDescription("Register an agent with its own cAPI antenna. Role is always 'secondary'. Registers the calling terminal by default; pass agent_id to register a different one."),
		mcp.WithString("endpoint", mcp.Required(), mcp.Description("cAPI endpoint URL, e.g. http://192.168.1.10:8787")),
		mcp.WithString("antenna_terminal_id", mcp.Required(), mcp.Description("Antenna's terminal_id (chat GUID from CleverCoder)")),
		mcp.WithString("pinpass", mcp.Required(), mcp.Description("Antenna's pinpass (base64 form as copied)")),
		mcp.WithString("name", mcp.Description("Optional display name for this agent (default empty — can be set later via set_agent_capi)")),
		mcp.WithString("agent_id", mcp.Description("Terminal_id to register (default: the calling terminal)"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			callerID := terminalIDFromContext(ctx)
			if callerID == "" {
				return mcp.NewToolResultError("no terminal_id in context; set X-Piumy-Terminal-Id header"), nil
			}
			// targetID can name a DIFFERENT agent than callerID — any caller
			// can overwrite an existing agent's credentials this way. That's
			// deliberate (T144, ct-2026-09-07-1245), not an oversight an
			// automated review will keep re-flagging: Piumy is single-account
			// — every agent here is the SAME owner's, connected by him, so
			// one agent overwriting another IS the owner changing his own
			// setup, not cross-tenant access. Reaching this call already
			// requires PIUMY_MCP_KEY (an agent the owner wired in), and
			// POST /api/admin/agent-create did the same unchecked write
			// already — the old caller-must-be-self gate (T142, boss
			// verbatim "quiten ese candado, no tiene logica") was a rodeo,
			// not a barrier. Revisit ONLY if multi-tenancy (backlog, not
			// promoted) makes agents belong to different owners.
			targetID := r.GetString("agent_id", callerID)

			endpoint, err := r.RequireString("endpoint")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			antTerminalID, err := r.RequireString("antenna_terminal_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			pinpass, err := r.RequireString("pinpass")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			a := store.Agent{
				AgentID:           targetID,
				Name:              r.GetString("name", ""),
				Endpoint:          endpoint,
				AntennaTerminalID: antTerminalID,
				Pinpass:           pinpass,
				Role:              "secondary",
			}
			if err := d.Store.UpsertAgent(a); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("store: %v", err)), nil
			}
			if d.OnAgentUpsert != nil {
				d.OnAgentUpsert(targetID, endpoint, antTerminalID, pinpass)
			}
			return mcp.NewToolResultText(fmt.Sprintf(`{"agent_id":%q,"role":"secondary","status":"registered"}`, targetID)), nil
		})

	// ── set_agent_capi ───────────────────────────────────────────────────
	// Updates the cAPI credentials of an existing agent — any agent, not
	// just the caller's own (T148, ct-2026-09-07-1644, boss verbatim,
	// direct: "quiero que quites ese candado... resulta ser que piumy
	// estaba lleno de candados estupidos"; amended minutes later: "quiero
	// que sea amplio, lo unico que hay que cuidar realmente es no cagarla
	// con whatsapp espamear su ip" — this tool touches no outbox, no send
	// rate, so it's squarely inside what he asked opened). The principal
	// terminal_id stays reserved below regardless — that's a structural
	// identity invariant (T148's own contract: "no se toca"), not the
	// authority lock this removes.
	s.AddTool(mcp.NewTool("set_agent_capi",
		mcp.WithDescription("Update the cAPI credentials of an existing agent — any registered agent, not just the caller's own."),
		mcp.WithString("agent_id", mcp.Required(), mcp.Description("The agent to update (terminal_id)")),
		mcp.WithString("endpoint", mcp.Description("New cAPI endpoint URL (omit to keep current)")),
		mcp.WithString("antenna_terminal_id", mcp.Description("New antenna terminal_id (omit to keep current)")),
		mcp.WithString("pinpass", mcp.Description("New pinpass (omit to keep current)")),
		mcp.WithString("name", mcp.Description("New display name (omit to keep current)"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			agentID := r.GetString("agent_id", "")
			if agentID == "" {
				return mcp.NewToolResultError("agent_id required"), nil
			}

			// INVARIANTE: principal terminal_id is RESERVED — nobody can
			// route its injector away, not even the principal itself via MCP.
			// Structural identity guard (T148, ct-2026-09-07-1644: "no se
			// toca"), not the authority lock T148 removes below.
			//
			// S9 (ct-2026-07-30-031143): the old message ("update via the
			// dashboard") sent the caller to a door that doesn't mention the
			// one that actually exists by MCP — set_capi_connector
			// (admin_tools.go). T148 opened that door wider too (it left
			// bossOnlyTools the same day this comment's own "secondary can
			// only update its own" check came out) — the message below still
			// points there, it's just no longer boss-only either.
			if d.PrincipalTerminalID != "" && agentID == d.PrincipalTerminalID {
				return mcp.NewToolResultError("forbidden: principal terminal_id is reserved; use set_capi_connector instead — or the dashboard"), nil
			}

			existing, ok, err := d.Store.GetAgent(agentID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("store: %v", err)), nil
			}
			if !ok {
				return mcp.NewToolResultError(fmt.Sprintf("agent %q not found; call register_agent first", agentID)), nil
			}

			if v := r.GetString("endpoint", ""); v != "" {
				existing.Endpoint = v
			}
			if v := r.GetString("antenna_terminal_id", ""); v != "" {
				existing.AntennaTerminalID = v
			}
			if v := r.GetString("pinpass", ""); v != "" {
				existing.Pinpass = v
			}
			if v := r.GetString("name", ""); v != "" {
				existing.Name = v
			}

			if err := d.Store.UpsertAgent(existing); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("store: %v", err)), nil
			}
			if d.OnAgentUpsert != nil {
				d.OnAgentUpsert(agentID, existing.Endpoint, existing.AntennaTerminalID, existing.Pinpass)
			}
			return mcp.NewToolResultText(fmt.Sprintf(`{"agent_id":%q,"status":"updated"}`, agentID)), nil
		})

	// ── list_agents ──────────────────────────────────────────────────────
	// Principal FIRST, then every secondary (ct-2026-07-29, agentes paso 3
	// — before this, list_agents only ever saw secondaries; the principal
	// was invisible by MCP even though GET /api/agents, REST, always showed
	// it). store.PrincipalAgent is the SAME synthesis restapi.handleAgents
	// uses — one place reads the principal, not two.
	s.AddTool(mcp.NewTool("list_agents",
		mcp.WithDescription("List every agent — the principal first (if configured), then every registered secondary — with role and antenna info. pinpass is never returned in clear — only pinpass_set:bool.")),
		func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			type row struct {
				AgentID           string `json:"agent_id"`
				Name              string `json:"name"`
				Role              string `json:"role"`
				Endpoint          string `json:"endpoint"`
				AntennaTerminalID string `json:"antenna_terminal_id"`
				PinpassSet        bool   `json:"pinpass_set"`
			}
			var rows []row
			if principal, ok, err := d.Store.PrincipalAgent(d.PrincipalTerminalID); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("store: %v", err)), nil
			} else if ok {
				rows = append(rows, row{
					AgentID: principal.AgentID, Name: principal.Name, Role: principal.Role,
					Endpoint: principal.Endpoint, AntennaTerminalID: principal.AntennaTerminalID,
					PinpassSet: principal.Pinpass != "",
				})
			}
			agents, err := d.Store.ListAgents()
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("store: %v", err)), nil
			}
			for _, a := range agents {
				rows = append(rows, row{
					AgentID:           a.AgentID,
					Name:              a.Name,
					Role:              a.Role,
					Endpoint:          a.Endpoint,
					AntennaTerminalID: a.AntennaTerminalID,
					PinpassSet:        a.Pinpass != "",
				})
			}
			if rows == nil {
				rows = []row{}
			}
			return jsonResult(rows)
		})

	// ── assign_chat_to_agent ─────────────────────────────────────────────
	// T148 (ct-2026-09-07-1644, boss verbatim, direct: "quiero que quites
	// ese candado... resulta ser que piumy estaba lleno de candados
	// estupidos") removed the old PRINCIPAL-ONLY restriction (M4,
	// ct-2026-07-22-1301) — the boss's own August reasoning is WHY: "la
	// asignacion es RUTEO, no permiso... los agentes se asignan para que
	// capi los inyecte al terminal / pero no para hacer puertas de
	// bloqueo". Using it as a gate was exactly the mistake he asked not to
	// repeat. Any registered agent may reassign a chat now — same write
	// path as the dashboard's POST /api/admin/agent-assign (M3):
	// chats.status' agent_exclusive:<id> form (store.AgentExclusiveStatus),
	// the one capipush.dispatch reads (M4's core change). The principal
	// itself staying an invalid target below is a STRUCTURAL invariant
	// (T148's own contract: "no se toca"), not the authority lock removed
	// here.
	s.AddTool(mcp.NewTool("assign_chat_to_agent",
		mcp.WithDescription("Manually route a chat's dispatch to one agent (writes agent_exclusive:<id>), or clear the assignment back to the default fallback (omit agent_id or pass an empty string). The principal itself is never a valid assignment target — an unassigned chat already falls back to it."),
		mcp.WithString("chat_id", mcp.Required()),
		mcp.WithString("agent_id", mcp.Description("The agent to assign to. Omit or pass \"\" to unassign."))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			chatID, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			agentID := r.GetString("agent_id", "")

			if agentID == "" {
				if err := d.Store.SetStatus(chatID, "new"); err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("store: %v", err)), nil
				}
				return mcp.NewToolResultText(fmt.Sprintf(`{"chat_id":%q,"status":"unassigned"}`, chatID)), nil
			}
			if agentID == d.PrincipalTerminalID {
				return mcp.NewToolResultError("cannot assign to the principal — it's already the default fallback for an unassigned chat"), nil
			}
			if _, ok, err := d.Store.GetAgent(agentID); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("store: %v", err)), nil
			} else if !ok {
				return mcp.NewToolResultError(fmt.Sprintf("unknown agent_id %q", agentID)), nil
			}
			if err := d.Store.SetStatus(chatID, store.AgentExclusiveStatus(agentID)); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("store: %v", err)), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf(`{"chat_id":%q,"agent_id":%q,"status":"assigned"}`, chatID, agentID)), nil
		})

	// ── delete_agent ─────────────────────────────────────────────────────
	// T148 (ct-2026-09-07-1644, boss verbatim, direct: "quiero que quites
	// ese candado... resulta ser que piumy estaba lleno de candados
	// estupidos"; amended minutes later, "quiero que sea amplio") removed
	// the old PRINCIPAL-ONLY restriction (ct-2026-07-29, agentes paso 3).
	// Any registered agent may delete any agent now, principal included —
	// T76 (ct-2026-08-27-1752, boss verbatim: "no puedo borrar a la gente
	// principal... a mí no me interesa [los problemas], yo lo quiero, como
	// yo lo digo") already established that deleting the principal is a
	// deliberate, boss-approved capability of this same tool, not an
	// oversight — T148 only widens WHO may reach it. Reuses the EXACT same
	// path POST /api/admin/agent-delete (restapi/admin.go) uses
	// — store.UnassignAllChatsForAgent, then either branch. Without the
	// unassign step, a deleted agent leaves dangling agent_exclusive:<id>
	// chats (boss: "ningún chat queda apuntando a un agente que ya no
	// existe"). Secondary: store.DeleteAgent + OnAgentDelete
	// (capipush.Pusher.UnregisterInjector) — without it, its OLD
	// credentials keep dispatching from memory after the DB row is gone
	// (boss: "un borrado que deja las credenciales vivas es un borrado que
	// miente"). Principal (T76, ct-2026-08-27-1752, boss verbatim: "no
	// puedo borrar a la gente principal... a mí no me interesa [los
	// problemas], yo lo quiero, como yo lo digo") — it has no `agents` row
	// (T70 already hit this): "deleted" means store.ClearPrincipalAgent,
	// back to the fresh-install unconfigured state, plus resetting the
	// live injector so it stops looking configured. No new gate for "don't
	// leave zero agents" — if that happens, dispatch's own existing "no
	// antenna" quiet retention takes it from there.
	s.AddTool(mcp.NewTool("delete_agent",
		mcp.WithDescription("Permanently delete an agent — a secondary, or the principal itself. Unassigns every chat exclusively routed to it (they revert to the normal router/principal fallback). A secondary's live injector stops dispatching immediately; the principal's live injector is reset to unconfigured (same as a fresh install) instead of being deleted, since it isn't a real agent row."),
		mcp.WithString("agent_id", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			agentID, err := r.RequireString("agent_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			isPrincipal := agentID == d.PrincipalTerminalID
			if !isPrincipal {
				if _, ok, err := d.Store.GetAgent(agentID); err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("store: %v", err)), nil
				} else if !ok {
					return mcp.NewToolResultError(fmt.Sprintf("unknown agent_id %q", agentID)), nil
				}
			}
			unassigned, err := d.Store.UnassignAllChatsForAgent(agentID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("store: %v", err)), nil
			}
			if isPrincipal {
				if err := d.Store.ClearPrincipalAgent(); err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("store: %v", err)), nil
				}
				if d.Connector != nil {
					d.Connector.SetConfig("", "", "")
				}
			} else {
				if err := d.Store.DeleteAgent(agentID); err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("store: %v", err)), nil
				}
				if d.OnAgentDelete != nil {
					d.OnAgentDelete(agentID)
				}
			}
			return mcp.NewToolResultText(fmt.Sprintf(`{"agent_id":%q,"status":"deleted","chats_unassigned":%d}`, agentID, unassigned)), nil
		})

	// ── promote_to_principal ─────────────────────────────────────────────
	// T76 (ct-2026-08-27-1752), boss verbatim: "que también el agente lo
	// puede hacer por MCP... intercambiando el principal entre ellos" —
	// self-service, no target parameter: the caller always promotes
	// ITSELF, matching "y después venga otro y se cambie principal"
	// (another one comes and switches ITSELF to principal). No
	// confirmation, no "are you sure", no gate beyond identifying the
	// caller — the boss anticipated and rejected exactly that: "me da lo
	// mismo que piensen que van a haber problemas... yo lo quiero, como yo
	// lo digo". Same store.PromoteToPrincipal the dashboard's
	// POST /api/admin/agent-promote calls — one swap implementation, not
	// two.
	s.AddTool(mcp.NewTool("promote_to_principal",
		mcp.WithDescription("Self-promote: swap roles with the current principal. The caller (must already be a registered secondary — register_agent first) becomes principal and starts receiving is_boss dispatches; the previous principal becomes a secondary agent at the caller's own vacated agent_id, keeping its own endpoint/pinpass/name — UNLESS the previous principal was unconfigured (no endpoint and no antenna), in which case the vacated agent_id is deleted instead of ghosted (T88). No confirmation required.")),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			callerID := terminalIDFromContext(ctx)
			if callerID == "" {
				return mcp.NewToolResultError("no terminal_id in context; set X-Piumy-Terminal-Id header"), nil
			}
			if d.PrincipalTerminalID != "" && callerID == d.PrincipalTerminalID {
				return mcp.NewToolResultError("ya sos el principal"), nil
			}
			demotedID, err := d.Store.PromoteToPrincipal(d.PrincipalTerminalID, callerID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("store: %v", err)), nil
			}
			newPrincipal, _, err := d.Store.PrincipalAgent(d.PrincipalTerminalID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("store: %v", err)), nil
			}
			if d.Connector != nil {
				d.Connector.SetConfig(newPrincipal.Endpoint, newPrincipal.AntennaTerminalID, newPrincipal.Pinpass)
			}
			if d.OnAgentUpsert != nil {
				if demoted, ok, err := d.Store.GetAgent(demotedID); err == nil && ok {
					d.OnAgentUpsert(demotedID, demoted.Endpoint, demoted.AntennaTerminalID, demoted.Pinpass)
				}
			}
			return mcp.NewToolResultText(fmt.Sprintf(`{"status":"promoted","new_principal":%q,"demoted_to":%q}`, callerID, demotedID)), nil
		})
}
