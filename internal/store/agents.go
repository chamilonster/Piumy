package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

type Agent struct {
	AgentID  string
	Name     string // display name (ct-2026-07-22-1301, M1) — optional, "" until set
	Endpoint string
	// AntennaTerminalID is CleverCoder's own handshake id, copied verbatim
	// from the antenna's clipboard credentials — a live GUID, OR (T32,
	// ct-2026-08-06-1109 — protocol §2, ct-2026-08-06-0221) a STABLE chat_id
	// CleverCoder computes: capi-<proyecto>-<agente>-<hash> with party
	// (absolute identity, "the mineral is the mineral always"), but
	// capi-<proyecto>-<proyecto>-<hash>-<N> WITHOUT party — that second form
	// names a POSITION (the Nth terminal open in that project), not a
	// specific one. A position can be valid and simply empty right now
	// (nothing open there yet) — that's exactly what handshake's
	// position_empty means, a normal transient state, not a misconfigured
	// credential.
	AntennaTerminalID string
	Pinpass           string
	Role              string // "principal" | "secondary"
}

func (s *Store) UpsertAgent(a Agent) error {
	_, err := s.db.Exec(`
		INSERT INTO agents(agent_id, name, endpoint, antenna_terminal_id, pinpass, role)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET
			name=excluded.name,
			endpoint=excluded.endpoint,
			antenna_terminal_id=excluded.antenna_terminal_id,
			pinpass=excluded.pinpass,
			role=excluded.role`,
		a.AgentID, a.Name, a.Endpoint, a.AntennaTerminalID, a.Pinpass, a.Role)
	return err
}

func (s *Store) GetAgent(agentID string) (Agent, bool, error) {
	var a Agent
	err := s.db.QueryRow(
		`SELECT agent_id, name, endpoint, antenna_terminal_id, pinpass, role FROM agents WHERE agent_id=?`,
		agentID).Scan(&a.AgentID, &a.Name, &a.Endpoint, &a.AntennaTerminalID, &a.Pinpass, &a.Role)
	if err == sql.ErrNoRows {
		return Agent{}, false, nil
	}
	return a, err == nil, err
}

func (s *Store) ListAgents() ([]Agent, error) {
	rows, err := s.db.Query(
		`SELECT agent_id, name, endpoint, antenna_terminal_id, pinpass, role FROM agents ORDER BY agent_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Agent
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.AgentID, &a.Name, &a.Endpoint, &a.AntennaTerminalID, &a.Pinpass, &a.Role); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AgentsByAntenna returns every registered SECONDARY agent (never the
// principal — it has no real `agents` row, PrincipalAgent synthesizes it
// from KV, and a caller that also cares about the principal checks it
// separately, same split identity.go's own candidate list already makes)
// whose antenna_terminal_id equals antennaID. More than one row means two
// agents share the same antenna — an ambiguous match a caller must never
// resolve silently (T129, ct-2026-09-03-0200): the gate uses len(result)
// to decide reject-vs-resolve, never picks the first row.
func (s *Store) AgentsByAntenna(antennaID string) ([]Agent, error) {
	if antennaID == "" {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT agent_id, name, endpoint, antenna_terminal_id, pinpass, role FROM agents WHERE antenna_terminal_id = ?`,
		antennaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Agent
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.AgentID, &a.Name, &a.Endpoint, &a.AntennaTerminalID, &a.Pinpass, &a.Role); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAgent(agentID string) error {
	_, err := s.db.Exec(`DELETE FROM agents WHERE agent_id=?`, agentID)
	return err
}

// PrincipalAgent synthesizes the principal's Agent-shaped info from KV — it
// never has a real `agents` row (register_agent/set_agent_capi reject the
// principal's own id: the principal's identity/injector is wired at
// startup, not self-registered like a secondary). Shared by GET /api/agents
// (restapi) and the list_agents/set_capi_connector MCP tools (ct-2026-07-29,
// agentes paso 3) so "how do I read the principal" lives in exactly one
// place — before this, restapi.handleAgents read the 4 KV keys inline and
// MCP couldn't see the principal at all. Pinpass comes back in clear, same
// convention GetAgent/ListAgents already use for secondaries — redacting to
// a bool is the CALLER's job at the output boundary, not this layer's.
// ok=false when principalID is empty (no principal configured), same
// convention as GetAgent's own ok.
func (s *Store) PrincipalAgent(principalID string) (a Agent, ok bool, err error) {
	if principalID == "" {
		return Agent{}, false, nil
	}
	endpoint, err := s.KVGet(SettingCAPIEndpoint)
	if err != nil {
		return Agent{}, false, err
	}
	terminalID, err := s.KVGet(SettingCAPITerminalID)
	if err != nil {
		return Agent{}, false, err
	}
	pinpass, err := s.KVGet(SettingCAPIPinpass)
	if err != nil {
		return Agent{}, false, err
	}
	name, err := s.KVGet(SettingPrincipalName)
	if err != nil {
		return Agent{}, false, err
	}
	return Agent{
		AgentID: principalID, Name: name, Endpoint: endpoint,
		AntennaTerminalID: terminalID, Pinpass: pinpass, Role: "principal",
	}, true, nil
}

// IsAllowedPrincipalEndpoint is the ONE place that decides "is this address
// allowed as a cAPI antenna endpoint" (ct-2026-07-29, agentes paso 3 —
// corrected same day: the first cut required literal 127.0.0.1, which broke
// the product's actual target deploy — a Raspberry Pi running the gateway
// with the principal agent on a different machine of the same LAN, boss
// verbatim: "este proyecto está hecho para correr en una raspberry py...
// y ahi no será local la antenita". The real invariant is "never a public
// address", not "always this machine"). Allowed: loopback, RFC1918/RFC4193
// private ranges, link-local, and the "localhost"/"*.local" names mDNS
// discovery on a Pi uses. Rejected: any public IP or public domain — a
// tunnel/public-endpoint case is coming (boss: "el mcp estará dentro de un
// tunel, despues vemos eso de otra manera") but is NOT built yet (YAGNI —
// CLAUDE.md); when that requirement lands, it's a change to THIS function,
// not a hunt through call sites.
//
// Exported (T77, ct-2026-08-27-1753, background security review — SSRF):
// send_to_boss's ephemeral-antenna attach lets ANY MCP-key-holding agent
// supply an arbitrary endpoint that main.go pings and, on success,
// registers as a live dispatch target — an unvalidated URL there is a
// classic SSRF (probe the LAN, or a cloud metadata endpoint, from the
// gateway's own network position). The invariant is IDENTICAL to the
// principal's own endpoint ("never a public address"), so this reuses the
// same, already-hardened function rather than a second copy of the same
// IP-range logic — main.go calls it directly before ever constructing an
// injector from caller-supplied credentials.
//
// T78 (ct-2026-08-27-1952) closes the gap T77's own report flagged: link-local
// (169.254.0.0/16) stays allowed WHOLESALE — it's what the Raspberry Pi's
// mDNS/local-discovery case needs — except for the single address
// 169.254.169.254, the cloud-metadata endpoint every major provider (AWS/GCP/
// Azure) serves instance credentials from with NO authentication. That one
// address is the classic SSRF payoff and exists nowhere on the owner's actual
// Windows/LAN/Pi deployment, so blocking it costs nothing real while closing
// the one link-local address that's actually dangerous. Not the whole range —
// that WOULD break the Pi case, and is explicitly not what this does.
func IsAllowedPrincipalEndpoint(endpoint string) (allowed bool, host string, err error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return false, "", err
	}
	host = u.Hostname()
	if host == "" {
		return false, "", fmt.Errorf("no host in %q", endpoint)
	}
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".local") {
		return true, host, nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false, host, nil // an unresolved name — treated as a public domain
	}
	if ip.Equal(cloudMetadataIP) {
		return false, host, fmt.Errorf("%s is the cloud-metadata endpoint (AWS/GCP/Azure serve unauthenticated instance credentials there) — blocked even though 169.254.0.0/16 is otherwise allowed for local discovery", host)
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast(), host, nil
}

// cloudMetadataIP is the ONE link-local address IsAllowedPrincipalEndpoint
// singles out for rejection (T78) — parsed once, compared by value
// (net.IP.Equal) so no textual quirk in how a caller wrote the address
// (leading zeros, alternate notation) slips past a naive string compare.
var cloudMetadataIP = net.ParseIP("169.254.169.254")

// ErrPrincipalEndpointPublic is SetPrincipalAgent's validation failure — a
// sentinel (errors.Is-checkable) so callers can tell "bad input, 400" from
// a genuine store failure, "something broke, 500" — same distinction
// restapi's other validated writes already make, just via a typed error
// instead of a pre-check, so the RULE itself still lives in exactly one
// place (SetPrincipalAgent/IsAllowedPrincipalEndpoint), never duplicated at
// the caller.
var ErrPrincipalEndpointPublic = errors.New("principal endpoint must be loopback or a private-network address — a public IP or domain isn't allowed yet")

// SetPrincipalAgent persists the principal's cAPI config + display name
// together — the single write path POST /api/admin/agent-update (principal
// branch), the set_capi_connector MCP tool (ct-2026-07-29, agentes paso 3)
// and PromoteToPrincipal (T76, below) share, so "how do I update the
// principal" can't drift between REST/MCP/role-swap. Rejects a disallowed
// endpoint outright (ErrPrincipalEndpointPublic, wrapping the specific
// reason) instead of silently overriding it — an honest error, not a value
// the caller didn't ask for. endpoint=="" is exempt from that check — an
// UNCONFIGURED principal (nothing to validate) is an already-supported
// state (S6, ct-2026-07-30-031048: the live injector starts this way at
// every fresh boot), not an invalid one; T76 needs this exact path to
// promote a secondary that has no antenna wired yet ("subilo igual, el
// dueño la carga después").
func (s *Store) SetPrincipalAgent(name, endpoint, terminalID, pinpass string) error {
	if endpoint != "" {
		allowed, host, err := IsAllowedPrincipalEndpoint(endpoint)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrPrincipalEndpointPublic, err)
		}
		if !allowed {
			return fmt.Errorf("%w: %q resolves to a public address", ErrPrincipalEndpointPublic, host)
		}
	}
	if err := s.SetCAPIConnector(endpoint, terminalID, pinpass); err != nil {
		return err
	}
	return s.KVSet(SettingPrincipalName, name)
}

// PromoteToPrincipal swaps the principal role between the CURRENT principal
// (principalID) and a registered secondary (secondaryAgentID) — T76,
// ct-2026-08-27-1752, boss verbatim: "que también el agente lo puede hacer
// por MCP... intercambiando el principal entre ellos". Not a column
// UPDATE: the principal isn't a real `agents` row (PrincipalAgent
// synthesizes it from KV — T70 already hit this), so the swap crosses two
// representations. What actually happens:
//   - secondary's own cAPI credentials (endpoint/antenna_terminal_id/
//     pinpass/name) become the principal's, via SetPrincipalAgent (same
//     write path set_capi_connector already uses — empty endpoint allowed,
//     see its own doc).
//   - the OLD principal's data (endpoint/pinpass/name) moves into
//     secondaryAgentID's OWN `agents` row — the identity slot the promoted
//     agent just vacated, reused rather than inventing a new id (boss:
//     "no se borra... es un intercambio, no un reemplazo") — UNLESS there's
//     nothing to preserve (see below).
//
// T88 (ct-2026-08-28-0626): the boss's own quote above ("no se borra") only
// holds when there's something worth not deleting. If the demoted principal
// is EMPTY (no endpoint AND no antenna_terminal_id — the state a fresh
// install starts in, or the one left after ClearPrincipalAgent/deleting the
// only agent), the swap has nothing to preserve: writing that emptiness
// into secondaryAgentID's row is not "keeping the old principal's data
// safe", it's manufacturing a ghost — an agent that exists, shows up in
// every selector (dashboard dropdown, MCP list_agents), and can never
// receive a dispatch (no endpoint or antenna means capipush.InjectorFor
// registers it but Configured() is always false — dispatch(), not this
// layer, is what actually distinguishes "no antenna" and quiet-retains).
// Name alone doesn't rescue it into the swap either — a named-but-
// unreachable agent is exactly as useless as an unnamed one, so the empty
// check below (Citrino's own criterion) ignores Name and Pinpass on
// purpose. In that case: delete the promoted agent's now-vacated row
// instead of ghosting it — nothing else to do, the principal identity
// already moved via SetPrincipalAgent above.
//
// T89 (ct-2026-08-28-0628): also migrates chats.status — any chat
// explicitly assigned to secondaryAgentID (agent_exclusive:<id>, a manual
// per-chat decision) is repointed to AgentExclusiveStatus(principalID),
// the SAME stable slot value picking "Principal — <name>" from the
// dashboard's own agent-assign dropdown already writes (buildAgentSelect,
// app.js). That assignment was made about a PERSON, and the person is now
// at principalID — leaving it on the vacated secondaryAgentID is T88's own
// finding (boss: "hay 2 citrinos... " led to it): silent retention or a
// silent fall-through to some other tier, never actually reaching the
// promoted person again.
//
// Returns the demoted agent's id (== secondaryAgentID — same value, named
// for the caller's clarity) so it can hot-reload the live injectors
// (Connector.SetConfig for the new principal, OnAgentUpsert for the
// demoted one, skipped automatically when the row was deleted instead of
// written) — this method only touches the store, it doesn't know about
// injectors.
func (s *Store) PromoteToPrincipal(principalID, secondaryAgentID string) (demotedAgentID string, err error) {
	secondary, ok, err := s.GetAgent(secondaryAgentID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("agent_id desconocido: %s", secondaryAgentID)
	}
	oldPrincipal, _, err := s.PrincipalAgent(principalID)
	if err != nil {
		return "", err
	}
	if err := s.SetPrincipalAgent(secondary.Name, secondary.Endpoint, secondary.AntennaTerminalID, secondary.Pinpass); err != nil {
		return "", err
	}
	// T89 (ct-2026-08-28-0628) — a chat manually assigned to secondaryAgentID
	// was assigned to a PERSON, not to whatever slot they'd occupy later.
	// That person is now at principalID (AgentExclusiveStatus(principalID)
	// is the SAME stable value buildAgentSelect's "Principal — <name>" option
	// already writes — it survives every future promotion, unlike a frozen
	// secondaryAgentID). Without this, the assignment stays pointing at the
	// now-vacated slot — before T88's ghost fix that meant silent retention
	// forever (an empty-but-registered injector, Configured()==false);
	// after it, a silent fall-through to whatever the next dispatch tier
	// resolves, which may not be this person at all. Scoped to the EXACT
	// vacated id — a chat assigned to some other agent is untouched.
	if _, err := s.db.Exec(`UPDATE chats SET status = ? WHERE status = ?`,
		AgentExclusiveStatus(principalID), AgentExclusiveStatus(secondaryAgentID)); err != nil {
		return "", err
	}
	if oldPrincipal.Endpoint == "" && oldPrincipal.AntennaTerminalID == "" {
		return secondaryAgentID, s.DeleteAgent(secondaryAgentID)
	}
	if err := s.UpsertAgent(Agent{
		AgentID: secondaryAgentID, Name: oldPrincipal.Name, Endpoint: oldPrincipal.Endpoint,
		AntennaTerminalID: oldPrincipal.AntennaTerminalID, Pinpass: oldPrincipal.Pinpass, Role: "secondary",
	}); err != nil {
		return "", err
	}
	return secondaryAgentID, nil
}

// ClearPrincipalAgent empties the principal's cAPI config + display name —
// T76 (ct-2026-08-27-1752), the other half of "borrar el principal": the
// principal has no `agents` row to delete, so "deleted" means back to the
// SAME unconfigured state a fresh install starts in (S6's own convention),
// not a new state to invent. Boss verbatim, no gate to soften it: "me da
// lo mismo que piensen que van a haber problemas... yo lo quiero, como yo
// lo digo" — if nothing is left configured, the gateway keeps receiving
// and simply has nowhere to dispatch (PortFallback's existing quiet
// retention), that's an accepted outcome, not a state to prevent.
func (s *Store) ClearPrincipalAgent() error {
	if err := s.SetCAPIConnector("", "", ""); err != nil {
		return err
	}
	return s.KVSet(SettingPrincipalName, "")
}

// UnassignAllChatsForAgent reverts every chat exclusively assigned to
// agentID back to "new" — AgentExclusiveStatus's inverse, in bulk. Called
// when an agent is deleted so no chat is left pointing at an identity that
// no longer exists (ct-2026-07-29, boss: "ningún chat queda apuntando a un
// agente que ya no existe" — a dangling agent_exclusive silently falls back
// to the router/principal today, capipush's own robustness guard, but the
// boss never finds out his messages changed destination; explicit cleanup
// beats an accident that happens to be safe). Returns how many chats were
// actually reverted, for an honest "N chats unassigned" response.
func (s *Store) UnassignAllChatsForAgent(agentID string) (int64, error) {
	res, err := s.db.Exec(`UPDATE chats SET status = 'new' WHERE status = ?`, AgentExclusiveStatus(agentID))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// agentExclusivePrefix is chats.status' "claimed for one agent" form —
// already validated by mcpserver's validChatStatus and written via
// SetStatus (chat.go); AgentExclusiveID/AgentExclusiveStatus are the one
// place that format is built/parsed, shared by capipush's dispatch routing
// (M4) and the assign/unassign REST+MCP paths (M3/M4, ct-2026-07-22-1301).
const agentExclusivePrefix = "agent_exclusive:"

// AgentExclusiveStatus builds the chats.status value that exclusively
// routes a chat to agentID.
func AgentExclusiveStatus(agentID string) string {
	return agentExclusivePrefix + agentID
}

// AgentExclusiveID extracts the agent id from a chat's status if it's in
// the agent_exclusive:<id> form; ok is false otherwise (including the
// malformed "agent_exclusive:" with no id, same rule validChatStatus applies).
func AgentExclusiveID(status string) (id string, ok bool) {
	if !strings.HasPrefix(status, agentExclusivePrefix) {
		return "", false
	}
	id = status[len(agentExclusivePrefix):]
	return id, id != ""
}

// ChatsForAgent returns chats manually assigned to agentID (M3's "números
// asignados" panel per agent) — chats.status == agent_exclusive:<agentID>,
// most recently active first. Same columns/scan as ListChats/GetChat.
func (s *Store) ChatsForAgent(agentID string) ([]Chat, error) {
	rows, err := s.db.Query(`SELECT `+chatColumns+` FROM chats WHERE status = ? ORDER BY last_ts DESC`,
		AgentExclusiveStatus(agentID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chat
	for rows.Next() {
		c, err := scanChat(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Recolectar-y-actuar (ct-2026-07-24-0127): rows cerradas antes de
	// enrichChat, que hace QueryRow — necesario con SetMaxOpenConns(1)
	// para evitar deadlock (la conn de rows no se libera hasta Close).
	for i := range out {
		if err := s.enrichChat(&out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}
