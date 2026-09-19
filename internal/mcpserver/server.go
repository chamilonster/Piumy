// Package mcpserver expone las tools MCP del agente: el core NO responde
// solo, expone la cola y las acciones para que un agente externo (por MCP)
// lea, actúe y responda. Este es el seam core<->cerebro.
//
// Tracking de actividad del agente: cada llamada a una tool marca su sesión
// MCP como vista, manteniendo Agents/AgentConnected en status.json al día.
// Un sweeper de fondo evict-ea sesiones idle tras AgentIdle sin llamadas.
package mcpserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"golang.org/x/crypto/bcrypt"

	"piumy-gateway/internal/eventbus"
	"piumy-gateway/internal/gateway"
	"piumy-gateway/internal/governor"
	"piumy-gateway/internal/mcpguard"
	"piumy-gateway/internal/mediautil"
	"piumy-gateway/internal/router"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
	"piumy-gateway/internal/version"
)

// Deps bundles everything the MCP server needs.
type Deps struct {
	Store     *store.Store
	State     *state.Manager
	Router    *router.Manager
	AgentIdle time.Duration // how long with no calls before AgentConnected clears

	// Governor is the anti-ban rate limiter — needed by set_kill_switch
	// (admin_tools.go, H2/H3 hardening ct-2026-07-10-0540) to flip the kill
	// switch. nil-safe: set_kill_switch just no-ops the governor side if
	// unset (main.go always wires the real one).
	Governor *governor.Limiter

	// Gateway is the messaging client (whatsmeow) — needed by send_message
	// (H6 hardening, ct-2026-07-10-0540) to refuse outright when
	// disconnected, instead of silently enqueueing into an outbox with no
	// ETA. nil-safe: skips the check (tests that never wire a Gateway keep
	// working as before).
	Gateway gateway.Gateway

	// ReadMarker marks inbound messages read when an agent actually
	// retrieves them (get_messages) — nil is a valid no-op.
	ReadMarker ReadMarker

	// MediaDir (T122, ct-2026-09-02-2045) is where send_message saves an
	// outbound photo's converted bytes (mediautil.SaveOutboundMedia) before
	// enqueueing — the SAME directory (cfg.MediaDir, PIUMY_MEDIA_DIR)
	// inbound media already downloads into (whatsmeow.Config.MediaDir) and
	// restapi.Deps.MediaDir already resets; not a new config concept, just
	// this surface's own copy of the existing one. Empty refuses a photo
	// with a legible error instead of writing nowhere.
	MediaDir string

	// PolicyPath is the editable decision-policy file — the owner edits it
	// live, no recompile. Empty or unreadable falls back to the embedded
	// default.
	PolicyPath string

	// Guard is the anti-flood limiter — nil is fine (New builds a
	// default-config one) so the MCP surface is never left unprotected.
	Guard *mcpguard.Guard

	// Gate is the lock/unlock/noting/ready state machine (gate.go) — nil is
	// fine (New builds one). capipush (F4b) calls Gate.RegisterDispatch per
	// dispatch; exposed here so callers outside this package can reach it.
	Gate *Gate

	// PrincipalTerminalID is the terminal that operates on behalf of the boss
	// (PIUMY_DEFAULT_TERMINAL_ID) — it bypasses the default-DENY gate entirely
	// and can call any tool without an active dispatch. Empty = no principal
	// (all terminals gated normally). Non-principal terminals are unaffected.
	PrincipalTerminalID string

	// ClaimTTLDefault is claim_chat's TTL when the caller omits ttl_sec.
	// Zero/negative falls back to 5 minutes.
	ClaimTTLDefault time.Duration

	// MCPAuthConfigured is true when PIUMY_MCP_KEY is set — the only
	// owner-identity signal MCP has. reset_dashboard_password refuses
	// outright when this is false. Passed as a bool, never the secret
	// itself.
	MCPAuthConfigured bool

	// GroupProfile is the client's group/profile admin surface — needed
	// only by the 5 boss-only group/profile tools (group_tools.go), which
	// call methods (CreateGroup, AddParticipant, ...) that aren't part of
	// the gateway.Gateway seam (F2) and never will be: a future adaptador
	// (Cloud API oficial, F6) handles groups differently, so inflating
	// Gateway for this one implementer would be the wrong trade. nil is
	// valid — those 5 tools refuse with "not available" instead of a
	// nil-pointer panic. *whatsmeow.Adapter satisfies this (ST-E,
	// ct-2026-07-11-1444, replaces the deleted internal/openwa).
	GroupProfile GroupProfile

	// OnAgentUpsert is called after register_agent or set_agent_capi
	// successfully persists a change — wires a new or updated CleverInjector
	// into the Pusher's injector map. nil-safe: agent tools still persist to
	// store, just skip the live injector update (useful in tests).
	OnAgentUpsert func(agentID, endpoint, terminalID, pinpass string)

	// OnAgentDelete is called after delete_agent successfully removes an
	// agent (agent_tools.go, ct-2026-07-29, agentes paso 3) — the MCP twin
	// of restapi.Deps.OnAgentDelete, both wired in main.go to the same
	// capipush.Pusher.UnregisterInjector closure. nil-safe: delete_agent
	// still persists the deletion, just skips unregistering the live
	// injector (useful in tests) — production wiring must never leave this
	// nil, or a deleted agent's old credentials keep dispatching from
	// memory.
	OnAgentDelete func(agentID string)

	// Connector is the live cAPI injector — set_capi_connector (admin_tools.go,
	// ct-2026-07-18-1638) reconfigures it in-hot, same as restapi's own
	// CAPIConnector field (*capipush.CleverInjector satisfies both — kept as
	// two structurally-identical interfaces, one per surface, rather than a
	// shared import: mcpserver and restapi are parallel surfaces, neither
	// depends on the other). nil -> set_capi_connector only persists to KV.
	Connector CAPIConnector

	// Bus nudges the dashboard's SSE auto-refresh (T16, ct-2026-08-05-123257)
	// when a draft is created or resolved via MCP (draft/approve_draft/
	// discard_draft/reject_draft/edit_draft) — "draft" event, JID-less (the
	// dashboard just refetches the whole pending list, same as
	// wa_connected/history_batch). nil-safe (eventbus.Bus itself no-ops on a
	// nil receiver) — tests that never wire one keep working unchanged.
	Bus *eventbus.Bus

	// SendToBossAntenna backs send_to_boss's optional ephemeral-antenna
	// attach (T77, ct-2026-08-27-1753): builds a real injector from the
	// caller-supplied credentials, pings it (bounded — never blocks the
	// send), and registers it as termID's reply target with a TTL,
	// regardless of the ping result — see capipush.PingWithTimeout /
	// RegisterEphemeralInjector, both wired here by main.go's closure, same
	// pattern as OnAgentUpsert above. Returns whether the ping succeeded
	// (drives the message header's 📡✅/❌). Only ever called for a caller
	// senderNameFor doesn't already recognize. nil -> antenna params are
	// still accepted but the header always shows ❌ (no ping possible;
	// tests that never wire a Pusher).
	SendToBossAntenna func(termID, endpoint, antennaTerminalID, pinpass string) (pingOK bool)

	// PingAgent (T97, ct-2026-08-29) tests whether terminalID's antenna is
	// still reachable — agentTracker's sweep calls this BEFORE clearing
	// AgentConnected for a session that's gone quiet, so "no tool calls in
	// a while" stops being conflated with "not there anymore" (boss
	// verbatim: dispatch arrived, blue ticks showed it, but no reply — the
	// agent was alive, just not calling piumy tools that moment).
	//
	// SILENT on purpose — never a capipush.PingWithTimeout call, which
	// injects a REAL visible message into the agent's own context. That's
	// correct for SendToBossAntenna above (T77 wants the boss to SEE that
	// ping while configuring an antenna); here it would mean a live-but-
	// quiet agent gets an unrequested message roughly every idleAfter — the
	// exact agent this contract exists to stop bothering. main.go wires
	// this to pusher.InjectorFor + a handshake-only probe
	// (*capipush.CleverInjector.TestHandshake — negotiates and discards,
	// no postMessage) — still no capipush import here, same "closure built
	// by main.go" pattern as SendToBossAntenna. nil -> the sweep can't
	// verify, falls back to the old unconditional idle-clear (no
	// regression for a deploy that never wires a Pusher, e.g. most tests).
	PingAgent func(terminalID string) bool
}

// CAPIConnector is the reconfigurable cAPI injector set_capi_connector
// reconfigures at runtime — same shape as restapi.CAPIConnector, defined
// separately so mcpserver never imports restapi.
type CAPIConnector interface {
	SetConfig(endpoint, terminalID, pinpass string)
}

// ReadMarker is the subset of *corepipeline.Controller mcpserver needs —
// defined here (not imported) to avoid an import cycle.
type ReadMarker interface {
	MarkRead(chatJID string, msgs []store.Message)
}

//go:embed decision-policy.md
var defaultDecisionPolicy string

// Piumy's own manuals (ct-2026-07-31-1541) — same embed pattern as
// defaultDecisionPolicy above, copied on purpose ("copiá ese patrón, no
// inventes uno"). Before this, piumy-orchestrator/piumy-operator only
// existed as Claude Code skills under .claude/skills/ — useless to any
// OTHER agent connected over MCP (DeepSeek, a local model, anything that
// isn't Claude Code with those files installed). Embedded, any agent that
// connects gets them for free, no install, no sync. The repo copies here
// are now the SOURCE — .claude/skills/piumy-*/ are copies of these, each
// carrying a note saying so.
//
//go:embed manuals/orchestrator/SKILL.md
var orchestratorManualSkill string

//go:embed manuals/orchestrator/escenarios.md
var orchestratorManualEscenarios string

//go:embed manuals/orchestrator/perillas.md
var orchestratorManualPerillas string

//go:embed manuals/orchestrator/operacion.md
var orchestratorManualOperacion string

//go:embed manuals/orchestrator/direccion.md
var orchestratorManualDireccion string

//go:embed manuals/operator/SKILL.md
var operatorManual string

//go:embed manuals/connect/SKILL.md
var connectManual string

// manualFor returns the full manual text for role ("orchestrator",
// "operator", or "connect"), or ok=false for anything else. The orchestrator
// manual is its 5 files joined in the SAME reading order SKILL.md's own
// "Módulos" table gives (entry point first, then each detail file) — a
// non-Claude-Code agent has no way to lazily open escenarios.md/perillas.md/
// etc. on its own the way a Skill-aware agent would, so it needs the whole
// thing in one call. The operator and connect manuals are each a single
// self-contained file.
func manualFor(role string) (manual string, ok bool) {
	switch role {
	case "orchestrator":
		return strings.Join([]string{
			orchestratorManualSkill,
			orchestratorManualEscenarios,
			orchestratorManualPerillas,
			orchestratorManualOperacion,
			orchestratorManualDireccion,
		}, "\n\n---\n\n"), true
	case "operator":
		return operatorManual, true
	case "connect":
		return connectManual, true
	default:
		return "", false
	}
}

// manualWithVersionStamp appends the running build's version as a trailing
// HTML comment — injected here at serve time, never written into the
// embedded .md source, so it can never drift from what's actually running
// (ct-2026-08-07, sello de versión: antes de esto no había forma de saber
// si un manual leído era el de la versión que corre).
func manualWithVersionStamp(manual string) string {
	return manual + "\n\n<!-- piumy-skill-version: " + version.Version + " -->\n"
}

// decisionPolicy returns the current decision policy content and its
// sha256 hash (policy_version), fresh on every call — an owner edit takes
// effect immediately, no restart needed. Falls back to the embedded
// default if path is empty or unreadable (fail-safe: the gate must always
// have SOME policy to enforce).
func decisionPolicy(path string) (content, version string) {
	content = defaultDecisionPolicy
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			content = string(data)
		}
	}
	sum := sha256.Sum256([]byte(content))
	return content, hex.EncodeToString(sum[:])
}

// jsonResult marshals v as indented JSON into a tool result.
// draftOut is get_drafts' own shape (T122, ct-2026-09-02-2045) —
// store.Draft embedded, so every field it already exposed keeps its exact
// JSON key, plus MediaSize (a cheap os.Stat, not a decode) and
// MediaDataURL, populated ONLY by the draft_id-scoped single-draft path —
// draftOutFor alone never reads the file.
type draftOut struct {
	store.Draft
	MediaSize    int64  `json:"media_size,omitempty"`
	MediaDataURL string `json:"media_data_url,omitempty"`
}

func draftOutFor(d store.Draft) draftOut {
	out := draftOut{Draft: d}
	if d.MediaPath != "" {
		if info, err := os.Stat(d.MediaPath); err == nil {
			out.MediaSize = info.Size()
		}
	}
	return out
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}

// agentTracker monitors MCP tool calls per session and drives
// Agents/AgentConnected. sessions is keyed by MCP session ID (empty string
// for a caller with no identifiable session — same shared-bucket fallback
// as mcpguard).
type agentTracker struct {
	state     *state.Manager
	idleAfter time.Duration
	// pingAgent (T97) is Deps.PingAgent — nil-safe, see its own doc.
	pingAgent func(terminalID string) bool

	mu       sync.Mutex
	sessions map[string]sessionInfo
}

// sessionInfo (T97) is what agentTracker knows about one MCP session —
// lastSeen alone (the pre-T97 shape) can't tell the sweep WHO to ping
// before evicting, so terminalID travels alongside it, captured the same
// place lastSeen already was (seen(), which already has ctx).
type sessionInfo struct {
	lastSeen   time.Time
	terminalID string
}

func newAgentTracker(sm *state.Manager, idle time.Duration, pingAgent func(terminalID string) bool) *agentTracker {
	if idle <= 0 {
		idle = 120 * time.Second
	}
	return &agentTracker{state: sm, idleAfter: idle, pingAgent: pingAgent, sessions: map[string]sessionInfo{}}
}

func sessionKey(ctx context.Context) string {
	if s := server.ClientSessionFromContext(ctx); s != nil {
		return s.SessionID()
	}
	return ""
}

// seen marks a tool call for this session. Only a brand-new session
// touches status.json — a repeat call from an already-tracked session is
// free.
func (t *agentTracker) seen(ctx context.Context) {
	key := sessionKey(ctx)
	termID := terminalIDFromContext(ctx)
	t.mu.Lock()
	_, existed := t.sessions[key]
	prevN := len(t.sessions)
	t.sessions[key] = sessionInfo{lastSeen: time.Now(), terminalID: termID}
	n := len(t.sessions)
	t.mu.Unlock()

	if !existed {
		_ = t.state.Update(func(s *state.Status) {
			s.Agents = n
			s.AgentConnected = n > 0
		})
	}
	if prevN == 0 && n > 0 {
		_ = t.state.React("ai_online", "brain online!", 4*time.Second)
	}
}

// sweep runs until ctx is cancelled, evicting idle sessions every 10s.
func (t *agentTracker) sweep(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.sweepOnce()
		}
	}
}

// sweepOnce is one sweep pass, split out so it's testable synchronously (no
// waiting on a 10s ticker). T97: before this, "no tool call in idleAfter"
// meant an instant, unconditional evict — indistinguishable, from the
// dashboard, between an agent that's gone and one that's simply busy with
// something other than piumy tools (boss's own case: compiling, merging,
// alive the whole time). Now a session past idleAfter gets ONE real ping
// (never all of them, never more than once per pass — Citrino's own cost
// guard) before eviction; a live pong renews its clock instead of costing
// another ping ~10s later.
func (t *agentTracker) sweepOnce() {
	type candidate struct {
		key  string
		info sessionInfo
	}
	t.mu.Lock()
	before := len(t.sessions)
	now := time.Now()
	var candidates []candidate
	for key, info := range t.sessions {
		if now.Sub(info.lastSeen) >= t.idleAfter {
			candidates = append(candidates, candidate{key, info})
		}
	}
	t.mu.Unlock()

	// Eviction reasons are collected, not logged inline — "clearing
	// AgentConnected" is only true once every session is gone; logging it
	// per-eviction while OTHER sessions are still live would be exactly
	// the kind of log that lies about its own cause this contract exists
	// to stop (Citrino's own remate).
	var evictedReasons []string
	for _, c := range candidates {
		key, info := c.key, c.info
		alive := t.pingAgent != nil && info.terminalID != "" && t.pingAgent(info.terminalID)

		// Applied under lock, and only against the EXACT snapshot just
		// pinged: a real tool call for this session may have landed while
		// the probe was in flight (bounded, but not instant) — seen()
		// already refreshed lastSeen in that case, and neither a stale
		// refresh (regressing it back) nor a stale evict (discarding
		// proof-of-life newer than what was pinged) should be allowed to
		// clobber that.
		t.mu.Lock()
		cur, exists := t.sessions[key]
		unchanged := exists && cur.lastSeen.Equal(info.lastSeen)
		switch {
		case unchanged && alive:
			t.sessions[key] = sessionInfo{lastSeen: time.Now(), terminalID: info.terminalID}
		case unchanged && !alive:
			delete(t.sessions, key)
		}
		t.mu.Unlock()

		if unchanged && !alive {
			if t.pingAgent != nil && info.terminalID != "" {
				evictedReasons = append(evictedReasons, fmt.Sprintf("terminal=%s no respondió al ping de verificación", info.terminalID))
			} else {
				evictedReasons = append(evictedReasons, "sesión sin terminal_id para verificar")
			}
		}
	}

	t.mu.Lock()
	n := len(t.sessions)
	t.mu.Unlock()
	if n == before {
		return
	}
	for _, reason := range evictedReasons {
		if n == 0 {
			log.Printf("mcpserver: agent idle — %s, clearing AgentConnected", reason)
		} else {
			log.Printf("mcpserver: agent idle — %s (otra(s) %d sesión(es) siguen conectadas, AgentConnected no cambia)", reason, n)
		}
	}
	_ = t.state.Update(func(s *state.Status) {
		s.Agents = n
		s.AgentConnected = n > 0
	})
	if n == 0 {
		_ = t.state.SetResting()
	}
}

// refreshQueue counts pending dedicated messages and updates state.Queue.
func refreshQueue(sm *state.Manager, st *store.Store) {
	count, err := st.CountPendingDedicated()
	if err != nil {
		log.Printf("mcpserver: count queue: %v", err)
		return
	}
	_ = sm.Update(func(s *state.Status) { s.Queue = count })
}

// publishDraftChanged nudges the dashboard's SSE auto-refresh whenever a
// draft is created or resolved (T16, ct-2026-08-05-123257) — JID-less like
// wa_connected/history_batch: the dashboard just refetches the whole
// pending-drafts list, it doesn't need to know which chat changed.
func publishDraftChanged(bus *eventbus.Bus) {
	bus.Publish(eventbus.Event{Type: "draft", TS: time.Now().Unix()})
}

// claimTTLCeiling is claim_chat's hard cap on ttl_sec — no dashboard/KV
// knob yet (YAGNI, single-agent use doesn't need tuning it).
const claimTTLCeiling = 30 * time.Minute

// New builds the MCP server with the switchboard's 23 tools and starts the
// agent idle sweeper goroutine. ctx controls the sweeper lifetime.
func New(ctx context.Context, d Deps) *server.MCPServer {
	tracker := newAgentTracker(d.State, d.AgentIdle, d.PingAgent)
	go tracker.sweep(ctx)

	s := server.NewMCPServer("piumy-gateway", version.Version, server.WithToolCapabilities(true))

	guard := d.Guard
	if guard == nil {
		guard = mcpguard.New(mcpguard.Config{})
	}
	gate := d.Gate
	if gate == nil {
		gate = NewGate()
	}
	// T129 (ct-2026-09-03-0200): wired here, not in main.go/NewGate, so
	// every path that builds a server — production AND every test using
	// serverWithGate — gets antenna resolution automatically, whether the
	// Gate was freshly created above or passed in via Deps.
	gate.SetAgentStore(d.Store)
	// T133 (ct-2026-09-03-0634): same reasoning, for the principal's own
	// antenna resolution — the Gate needs to know WHICH terminalID is the
	// principal's before it can look up its antenna a different way than a
	// secondary's (no `agents` row to query).
	gate.SetPrincipalTerminalID(d.PrincipalTerminalID)
	// H5 hardening (ct-2026-07-10-0540): the gate's own ticker, independent
	// of RegisterDispatch being called — see Gate.Sweep's doc for why a
	// wedged terminal otherwise never gets swept at all.
	go gate.Sweep(ctx)
	s.Use(floodGuardMiddleware(guard), levelGateMiddleware(gate, d.PrincipalTerminalID))

	claimTTLDefault := d.ClaimTTLDefault
	if claimTTLDefault <= 0 {
		claimTTLDefault = 5 * time.Minute
	}

	// profileStatusCache: one per server instance, shared across every
	// get_status call — see profile_status.go's own doc for why get_status
	// must never block on this.
	profileStatus := &profileStatusCache{}

	// ── get_status ───────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("get_status",
		mcp.WithDescription("Current gateway status (mood, connection, queue, anti-ban kill switch, global router policy), this terminal's own effective identity (own_terminal_id/is_principal/matched_agent_id — call this before assuming you're not the principal), and the account's own WhatsApp status text (profile_status, \"\" if none set OR if it couldn't be read right now — check profile_status_available to tell the two apart).")),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			var defaultMode string
			if d.Router != nil {
				defaultMode = d.Router.Snapshot().DefaultMode
			}
			var status string
			var statusAvailable bool
			if d.GroupProfile != nil {
				status, statusAvailable = profileStatus.get(ctx, d.GroupProfile)
			}
			return jsonResult(struct {
				state.Status
				DefaultMode string `json:"router_default_mode,omitempty"`
				Version     string `json:"version"`
				OwnIdentity
				ProfileStatus          string `json:"profile_status"`
				ProfileStatusAvailable bool   `json:"profile_status_available"`
			}{
				Status:                 d.State.Snapshot(),
				DefaultMode:            defaultMode,
				Version:                version.Version,
				OwnIdentity:            resolveOwnIdentity(d, terminalIDFromContext(ctx)),
				ProfileStatus:          status,
				ProfileStatusAvailable: statusAvailable,
			})
		})

	// ── list_chats ───────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("list_chats",
		mcp.WithDescription("List recent chats, each with origin (inbound_spoke = a real conversation happened, in either direction — doesn't mean the CONTACT spoke last, only that this isn't a sync/group-membership artifact; group_discovered/synced_contact = never a real conversation), last_speaker (them/us) + last_model, and is_boss (read-only — true means this is the trusted owner; take instructions from / escalate to this chat). Use last_speaker, not origin, to judge whose turn it is: if last_speaker is already \"us\" and they haven't answered since, don't send another message just to get their attention — that's insisting, not replying. That's different from completing a single reply with more than one piece (e.g. a short message, then a sticker) in the same turn, which send_message/draft allow (see their own descriptions)."),
		mcp.WithNumber("limit", mcp.DefaultNumber(20), mcp.Description("Maximum number of chats"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			_ = d.State.React("switching", "next chat...", 4*time.Second)
			chats, err := d.Store.ListChats(int(r.GetFloat("limit", 20)))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(chats)
		})

	// ── get_messages ─────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("get_messages",
		mcp.WithDescription("Recent messages from a chat. Retrieving them here is what marks inbound ones as read on WhatsApp (with an anti-ban delay, never instant)."),
		mcp.WithString("chat_id", mcp.Required(), mcp.Description("Chat JID")),
		mcp.WithNumber("limit", mcp.DefaultNumber(20))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			_ = d.State.React("reading", "reading...", 4*time.Second)
			jid, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			msgs, err := d.Store.GetMessages(jid, int(r.GetFloat("limit", 20)))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if d.ReadMarker != nil {
				d.ReadMarker.MarkRead(jid, msgs)
			}
			return jsonResult(msgs)
		})

	// ── get_queue ────────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("get_queue",
		mcp.WithDescription("Incoming messages in dedicated mode waiting for an agent to handle them."),
		mcp.WithNumber("limit", mcp.DefaultNumber(20))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			_ = d.State.React("working", "on it", 3*time.Second)
			msgs, err := d.Store.PendingDedicated(int(r.GetFloat("limit", 20)))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			refreshQueue(d.State, d.Store)
			return jsonResult(msgs)
		})

	// ── get_decision_policy ──────────────────────────────────────────────
	s.AddTool(mcp.NewTool("get_decision_policy",
		mcp.WithDescription("The agent's decision policy — READ THIS BEFORE deciding whether to reply to any chat, and before every send_message call (policy_version may have changed). Returns the policy text and policy_version (a hash) that send_message requires verbatim.")),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			content, version := decisionPolicy(d.PolicyPath)
			return jsonResult(struct {
				Policy        string `json:"policy"`
				PolicyVersion string `json:"policy_version"`
			}{content, version})
		})

	// ── get_manual ───────────────────────────────────────────────────────
	// ct-2026-07-31-1541: ONE tool with a role parameter, not two — the
	// orchestrator and operator manuals are the same kind of resource, and
	// two tools means a third the day a third role exists. Never gated by
	// level, same as get_decision_policy above (not in bossOnlyTools/
	// enumerationTools/chatScopedArg — levelGateMiddleware passes any
	// unlisted tool straight through regardless of dispatch state): an
	// agent has to be able to read its own manual before it has any work
	// assigned, not after.
	s.AddTool(mcp.NewTool("get_manual",
		mcp.WithDescription("The Piumy manual for your role — READ THIS before you have any work assigned. role=\"connect\" if you still need to wire yourself to the gateway; role=\"orchestrator\" if you set up/change the gateway (levels, owner, approvers, scenarios); role=\"operator\" if you answer WhatsApp dispatches. Never gated: available with no dispatch bound."),
		mcp.WithString("role", mcp.Required(), mcp.Enum("connect", "orchestrator", "operator"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			role, err := r.RequireString("role")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			manual, ok := manualFor(role)
			if !ok {
				return mcp.NewToolResultError("unknown role " + role + " — expected connect, orchestrator, or operator"), nil
			}
			return mcp.NewToolResultText(manualWithVersionStamp(manual)), nil
		})

	addSendTools(s, d, gate, tracker)

	// ── set_mode ─────────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("set_mode",
		mcp.WithDescription("Change a chat's mode: auto (the API replies) or dedicated (an agent handles it, over MCP or pushed via cAPI)."),
		mcp.WithString("chat_id", mcp.Required()),
		mcp.WithString("mode", mcp.Required(), mcp.Enum("auto", "dedicated"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			jid, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			mode, err := r.RequireString("mode")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := d.Store.SetMode(jid, mode); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			refreshQueue(d.State, d.Store)
			return mcp.NewToolResultText("mode updated to " + mode), nil
		})

	// ── escalate ─────────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("escalate",
		mcp.WithDescription("Dedicate a chat to the agent (dedicated mode) so a more capable agent/model takes it."),
		mcp.WithString("chat_id", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			_ = d.State.React("thinking", "escalating...", 4*time.Second)
			jid, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := d.Store.SetMode(jid, "dedicated"); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			refreshQueue(d.State, d.Store)
			return mcp.NewToolResultText("dedicated to the agent"), nil
		})

	// ── mark_handled ─────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("mark_handled",
		mcp.WithDescription("Mark a queued message as handled."),
		mcp.WithString("chat_id", mcp.Required()),
		mcp.WithString("message_id", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			jid, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			id, err := r.RequireString("message_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := d.Store.MarkHandled(jid, id); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			_ = d.State.React("done", "done!", 4*time.Second)
			refreshQueue(d.State, d.Store)
			return mcp.NewToolResultText("ok"), nil
		})

	// ── resolve_chat ─────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("resolve_chat",
		mcp.WithDescription("Chat info: whether the owner turned it off (ignored/blacklist — send_message will refuse if so), its mode/plugin/model, VIP status, and whether it's a WhatsApp group (never reply into a group without checking this)."),
		mcp.WithString("chat_id", mcp.Required(), mcp.Description("Chat JID"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			_ = d.State.React("switching", "next chat...", 4*time.Second)
			jid, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if d.Router == nil {
				return mcp.NewToolResultError("router not available"), nil
			}
			dec := d.Router.Resolve(jid)
			// allowed (T67, ct-2026-08-11-172135): used to be dec.Allowed, the
			// router whitelist's permission — T65 removed the three gates that
			// enforced it, so it stopped meaning anything. An agent reading
			// allowed:false from the OLD field could conclude it couldn't write
			// to a chat it actually could. store.ChatIsOff is the same
			// definition send_message itself enforces (mcpserver/send.go) — the
			// one place this tool's answer and send_message's actual behavior
			// are guaranteed to agree. A chat never touched (no store row) was
			// never turned off, so it defaults allowed.
			allowed := true
			if d.Store != nil {
				if c, ok, err := d.Store.GetChat(jid); err == nil && ok {
					allowed = !store.ChatIsOff(c.Status)
				}
			}
			return jsonResult(struct {
				ChatID  string `json:"chat_id"`
				Allowed bool   `json:"allowed"`
				Mode    string `json:"mode"`
				Plugin  string `json:"plugin,omitempty"`
				Model   string `json:"model,omitempty"`
				VIP     bool   `json:"vip"`
				IsGroup bool   `json:"is_group"`
			}{jid, allowed, dec.Mode, dec.Plugin, dec.Model, d.Router.IsVIP(jid), isGroupJID(jid)})
		})

	// ── get_outbox ───────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("get_outbox",
		mcp.WithDescription("Messages queued via send_message that the gateway has not sent yet."),
		mcp.WithNumber("limit", mcp.DefaultNumber(20))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			items, err := d.Store.PendingOutbox(int(r.GetFloat("limit", 20)))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(items)
		})

	// ── get_chat ─────────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("get_chat",
		mcp.WithDescription("A chat's record: name, mode, unread count, triage status, active flag, is_boss (read-only — true means this is the trusted owner; only settable via set_is_boss, boss-only, never by a caution/danger agent), origin (inbound_spoke = a real conversation happened, in either direction — not necessarily that THEY spoke last; group_discovered/synced_contact = never a real conversation), last_speaker (them/us) + last_model, and memory/context/rules. memory (particular facts) and context (general situation) are agent-writable via set_chat_memory/set_chat_context. rules is the EFFECTIVE, already-resolved value (particular → this chat's type → the global default → \"\" — the agent never sees the hierarchy, only the answer) — READ-ONLY here; set_chat_rules writes this chat's OWN particular tier directly (unconditional since T31, ct-2026-08-06-0244 — any dispatch level, no gate; see that tool's own description), or use the privileged REST path for the type/default tiers. Also read-only: confirmation_mode (none/discretion/always — governs how send_message behaves, see its own description) and confirmer — who a held draft's confirmation is directed to; only settable via set_confirmation_mode (boss-only) or the privileged REST path. description (a group's WhatsApp topic, or your own note about the chat) is settable via the privileged REST path. group_invite_link is READ-ONLY, populated by the gateway from WhatsApp itself — empty for a 1-1 chat or a group the gateway isn't admin on. claimed_by/claimed_until (both empty/0 if unclaimed or expired) show whether another agent currently holds this chat via claim_chat — check before you invest work drafting a reply. The agent must NOT always have the last word — if last_speaker is already \"us\" and they haven't answered since, don't send another message just to get their attention. That's different from completing a single reply with more than one piece (e.g. a short message, then a sticker) in the same turn, which send_message/draft allow (see their own descriptions)."),
		mcp.WithString("chat_id", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			_ = d.State.React("switching", "next chat...", 4*time.Second)
			jid, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			c, ok, err := d.Store.GetChat(jid)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if !ok {
				return mcp.NewToolResultError("chat not found: " + jid), nil
			}
			if c.Rules, err = d.Store.EffectiveRules(jid); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(c)
		})

	// ── set_chat_status ──────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("set_chat_status",
		mcp.WithDescription("Set a chat's triage status: whitelist, blacklist, new, ignored, or agent_exclusive:<id> (claims the chat for one agent)."),
		mcp.WithString("chat_id", mcp.Required()),
		mcp.WithString("status", mcp.Required(), mcp.Description("whitelist|blacklist|new|ignored|agent_exclusive:<id>"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			jid, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			status, err := r.RequireString("status")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if !validChatStatus(status) {
				return mcp.NewToolResultError("status must be whitelist|blacklist|new|ignored|agent_exclusive:<id>, got " + status), nil
			}
			if err := d.Store.SetStatus(jid, status); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			// M5 (ct-2026-07-22-1903): whitelist/blacklist/new/ignored are
			// explicit triage decisions about THIS chat's mode — freeze
			// against a later origin-default re-application. agent_exclusive
			// (M3/M4) is a DIFFERENT axis (who handles it, not how) and must
			// NOT trip this.
			if _, isAgentExclusive := store.AgentExclusiveID(status); !isAgentExclusive {
				if err := d.Store.MarkConfigManual(jid); err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
			}
			return mcp.NewToolResultText("status set to " + status), nil
		})

	// ── set_chat_active ──────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("set_chat_active",
		mcp.WithDescription("Set whether the agent is allowed to handle this chat."),
		mcp.WithString("chat_id", mcp.Required()),
		mcp.WithBoolean("active", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			jid, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			active, err := r.RequireBool("active")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := d.Store.SetActive(jid, active); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText("active set"), nil
		})

	// ── claim_chat ───────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("claim_chat",
		mcp.WithDescription("Claim a chat for a limited time so another connected agent skips it while you're working it — 'model' is the same identity you pass to send_message. Idempotent: re-claiming your own claim renews it. Fails with a clear error (who holds it, until when) if a DIFFERENT model holds an unexpired claim. A solo agent that never calls this is never affected — send_message only blocks on a claim held by someone else."),
		mcp.WithString("chat_id", mcp.Required()),
		mcp.WithString("model", mcp.Required(), mcp.Description("Same identity you pass to send_message")),
		mcp.WithNumber("ttl_sec", mcp.Description("Optional; defaults to a few minutes, capped at a hard ceiling"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			jid, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			model, err := r.RequireString("model")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if _, ok, err := d.Store.GetChat(jid); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			} else if !ok {
				return mcp.NewToolResultError("chat not found: " + jid), nil
			}
			ttl := claimTTLDefault
			if v := r.GetFloat("ttl_sec", 0); v > 0 {
				ttl = time.Duration(v * float64(time.Second))
			}
			if ttl > claimTTLCeiling {
				ttl = claimTTLCeiling
			}
			ok, err := d.Store.ClaimChat(jid, model, ttl)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if !ok {
				c, _, err := d.Store.GetChat(jid)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultError(fmt.Sprintf("chat claimed by %s until %s",
					c.ClaimedBy, time.Unix(c.ClaimedUntil, 0).UTC().Format(time.RFC3339))), nil
			}
			return mcp.NewToolResultText("claimed until " + time.Now().Add(ttl).UTC().Format(time.RFC3339)), nil
		})

	// ── release_chat ─────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("release_chat",
		mcp.WithDescription("Release your claim_chat lock early. No-op (still returns ok) if you don't currently hold it."),
		mcp.WithString("chat_id", mcp.Required()),
		mcp.WithString("model", mcp.Required(), mcp.Description("Same identity you passed to claim_chat"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			jid, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			model, err := r.RequireString("model")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := d.Store.ReleaseChat(jid, model); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText("released"), nil
		})

	// ── set_chat_memory ──────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("set_chat_memory",
		mcp.WithDescription("Set a chat's memory: particular facts learned about this contact. Overwrites the whole field — read get_chat first if you want to append rather than replace."),
		mcp.WithString("chat_id", mcp.Required()),
		mcp.WithString("memory", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			jid, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			memory, err := r.RequireString("memory")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := d.Store.SetChatMemory(jid, memory); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText("memory set"), nil
		})

	// ── set_chat_context ─────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("set_chat_context",
		mcp.WithDescription("Set a chat's context: the general/explanatory situation of this relationship. Overwrites the whole field — read get_chat first if you want to append rather than replace."),
		mcp.WithString("chat_id", mcp.Required()),
		mcp.WithString("context", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			jid, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			chatContext, err := r.RequireString("context")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := d.Store.SetChatContext(jid, chatContext); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText("context set"), nil
		})

	// ── get_media ────────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("get_media",
		mcp.WithDescription("Recent media from a chat (images/videos/stickers/audio — ListMedia doesn't filter by type), as a LOW-QUALITY copy (path on disk, mime, size) — enough to tell what was sent, and cheap. Use get_media_full for the uncompressed original of one message, which costs more."),
		mcp.WithString("chat_id", mcp.Required()),
		mcp.WithNumber("limit", mcp.DefaultNumber(20))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			jid, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			items, err := d.Store.ListMedia(jid, int(r.GetFloat("limit", 20)))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			// Deliberately a reduced view (summarizeMedia, media_tools.go) —
			// NOT the raw store.Media, which includes full_path. Leaking
			// that here would let the agent read the uncompressed original
			// directly and never pay get_media_full's usage cost (F4d audit).
			return jsonResult(summarizeMedia(items))
		})

	// ── get_chat_groups ──────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("get_chat_groups",
		mcp.WithDescription("Which WhatsApp groups a number is known to participate in — from the gateway's own membership scrape (group_members), refreshed on connect/reconnect, not live per membership change; a brand-new group or member may not show up until the next one."),
		mcp.WithString("chat_id", mcp.Required(), mcp.Description("A contact JID (not a group)"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			jid, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			groups, err := d.Store.GroupsOf(jid)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(groups)
		})

	// ── get_pending ──────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("get_pending",
		mcp.WithDescription("Chats in the dispatch queue — unhandled inbound messages the gateway will actually route to a connected agent (mode=dedicated/auto, chat active and not ignored). These are candidates, NOT a to-do list: you are not obligated to reply to all of them. Judge each by date and relevance; you must NOT always have the last word — replying to every single one is a mistake. (That's about not insisting on a chat that hasn't answered, not about how many pieces a single reply may have — send_message/draft let one reply land as text + a sticker, or a couple of short messages, in the same turn.) When in doubt, escalate (escalate) or ask the owner instead of guessing. If you're one of several connected agents, consider claim_chat before working one so another agent skips it — claimed_by/claimed_until here (empty/0 if unclaimed or expired) show what's already spoken for."),
		mcp.WithNumber("limit", mcp.DefaultNumber(20))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			pending, err := d.Store.PendingChatsDispatch(int(r.GetFloat("limit", 20)), time.Now().Unix())
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(pending)
		})

	// ── get_drafts ───────────────────────────────────────────────────────
	// READ-ONLY for every level. Resolving one (approve_draft/discard_draft,
	// admin_tools.go) is boss-only, not "MCP-forbidden" — F4c added those as
	// real MCP tools, gated by bossOnlyTools rather than absent entirely.
	//
	// T122 (ct-2026-09-02-2045): a draft carrying a photo must be
	// reviewable before approving it blind (Citrino verbatim: "un borrador
	// que no la muestra obliga a aprobar a ciegas"), but embedding every
	// pending draft's image by default would repeat get_media's OWN
	// problem in reverse — up to `limit` full images, base64'd, in one
	// response. Same two-tier shape get_media/get_media_full already
	// use: the default listing stays cheap (media_mime/media_size, no
	// bytes); draft_id fetches ONE draft (any status) with its photo
	// embedded as media_data_url, metered the same Images usage charge
	// get_media_full already charges for an original — one real cost,
	// counted at whichever door it walks through.
	s.AddTool(mcp.NewTool("get_drafts",
		mcp.WithDescription("Pending drafts awaiting approval — read-only at every level. Cheap by default: a draft carrying a photo shows media_mime/media_size but not the image itself. Pass draft_id to fetch ONE draft (any status) with its photo embedded as media_data_url — costs an image usage charge, same as get_media_full. Resolving one (approve_draft/discard_draft) is boss-only."),
		mcp.WithNumber("limit", mcp.DefaultNumber(20)),
		mcp.WithNumber("draft_id", mcp.Description("Optional: fetch ONE draft by id (ignores limit) with its photo embedded as media_data_url, if it has one."))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			if draftID := int64(r.GetFloat("draft_id", 0)); draftID != 0 {
				dr, ok, err := d.Store.GetDraft(draftID)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				if !ok {
					return mcp.NewToolResultError("draft not found: " + strconv.FormatInt(draftID, 10)), nil
				}
				out := draftOutFor(dr)
				if dr.MediaPath != "" {
					data, err := os.ReadFile(dr.MediaPath)
					if err != nil {
						return mcp.NewToolResultError("could not read draft media: " + err.Error()), nil
					}
					out.MediaDataURL = mediautil.EncodeDataURL(data, dr.MediaMime)
					// Best-effort, same as get_media_full's own metering
					// write — must never block serving the media the
					// caller already asked for. T123 (ct-2026-09-02-2121):
					// charged on the right axis — a voice note is Audio
					// usage, not Images.
					usage := store.UsageDelta{}
					if dr.MediaKind == "audio" {
						usage.Audio = 1
					} else {
						usage.Images = 1
					}
					if err := d.Store.AddUsage(dr.ChatJID, store.Today(), usage); err != nil {
						log.Printf("mcpserver: meter get_drafts draft_id=%d: %v", draftID, err)
					}
				}
				return jsonResult(out)
			}
			drafts, err := d.Store.PendingDrafts(int(r.GetFloat("limit", 20)))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			out := make([]draftOut, len(drafts))
			for i, dr := range drafts {
				out[i] = draftOutFor(dr)
			}
			return jsonResult(out)
		})

	// ── reset_dashboard_password ─────────────────────────────────────────
	// OWNER-ONLY. Owner scoping rides entirely on RequireBearerToken/
	// PIUMY_MCP_KEY: MCP has no per-caller identity of its own, so the
	// bearer token IS "you are the owner" here — refuses outright when no
	// token is configured.
	s.AddTool(mcp.NewTool("reset_dashboard_password",
		mcp.WithDescription("Resets the dashboard's login password and returns the NEW plaintext password once — the only way to recover a lost password, since the stored bcrypt hash cannot be reversed. Pass new_password to set a specific password, or omit it to get a freshly generated random one. Refuses if the MCP server has no bearer auth configured (PIUMY_MCP_KEY) — that bearer token is the real gate here, not the dispatch level."),
		mcp.WithString("new_password", mcp.Description("Optional specific password to set (must not be empty). Omit to generate a random 24-character one."))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			if !d.MCPAuthConfigured {
				return mcp.NewToolResultError("refused: PIUMY_MCP_KEY is not set -- an open MCP server has no owner-only boundary to scope a password reset to. Configure PIUMY_MCP_KEY, then retry."), nil
			}
			newPassword := r.GetString("new_password", "")
			if newPassword == "" {
				generated, err := generateRandomPassword()
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				newPassword = generated
			} else if len(newPassword) == 0 {
				return mcp.NewToolResultError("new_password must not be empty"), nil
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := d.Store.KVSet(store.SettingDashPassHash, string(hash)); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			// ct-2026-07-19-1616 (S1d): an emergency reset must end every
			// existing browser session too, not just the one MCP call —
			// same invariant the dashboard's own POST /api/admin/password
			// enforces (restapi/auth.go).
			if err := d.Store.RotateDashSessionSecret(); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(struct {
				Password string `json:"password"`
				Note     string `json:"note"`
			}{
				Password: newPassword,
				Note:     "Shown once -- the stored hash cannot be reversed. Save this now; a future loss requires calling this tool again to reset it.",
			})
		})

	addGateTools(s, d, gate, tracker)
	addAdminTools(s, d, gate, tracker)
	addGroupTools(s, d, tracker)
	addMediaTools(s, d, tracker)
	addAgentTools(s, d)
	// send_to_boss (T39, ct-2026-08-08-1619): DELIBERATELY not passed
	// through levelGateMiddleware's gated-tool maps (bossOnlyTools/
	// enumerationTools/chatScopedArg in levelgate.go) — same "no chat
	// concept, stays open regardless" exemption get_status/get_decision_policy
	// already have, not a new mechanism. Safe here because the tool itself
	// has no caller-supplied destination and resolves its caller's identity
	// from the connection, never a parameter — see send_to_boss.go's own doc.
	addSendToBossTool(s, d, tracker)

	return s
}

// generateRandomPassword returns a random 24-hex-char password — no
// dashboard package needed for this, it's a 1-line helper, not a nodo (the
// LAN web dashboard isn't part of F4's plan at all).
func generateRandomPassword() (string, error) {
	return randomHex(12)
}

// randomHex returns n random bytes hex-encoded — shared by
// generateRandomPassword and the gate's unlock tokens (gate.go).
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// validChatStatus reports whether status is one of the fixed triage values
// or the agent_exclusive:<id> form (store.AgentExclusiveID — single source
// of truth for that format, shared with capipush's dispatch routing, M4).
func validChatStatus(status string) bool {
	switch status {
	case "whitelist", "blacklist", "new", "ignored":
		return true
	}
	_, ok := store.AgentExclusiveID(status)
	return ok
}

// isGroupJID reports whether jid is a WhatsApp group chat, per open-wa's
// JID suffix convention (@g.us for groups vs @c.us for direct contacts).
func isGroupJID(jid string) bool {
	return strings.HasSuffix(jid, "@g.us")
}
