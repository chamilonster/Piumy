// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
//
// Package mcpserver: the switchboard's MCP server. It does NOT reply — it
// exposes the queue and the actions so an external agent (Claude/OpenCode over
// MCP) can read, act, and respond. This is the core <-> brain seam.
//
// Agent activity tracking: each tool call marks its MCP session as seen,
// keeping Agents (connected-agent count) and AgentConnected (Agents>0) in
// status.json current. The pool going empty→nonempty triggers "ai_online".
// A background sweeper evicts idle sessions after AgentIdle with no calls.
// Tool-specific moods (working, responding, thinking) revert to resting.
package mcpserver

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"golang.org/x/crypto/bcrypt"

	"pimywa/internal/dashboard"
	"pimywa/internal/governor"
	"pimywa/internal/mcpguard"
	"pimywa/internal/router"
	"pimywa/internal/state"
	"pimywa/internal/store"
)

// Deps bundles everything the MCP server needs.
type Deps struct {
	Store     *store.Store
	State     *state.Manager
	Router    *router.Manager
	Gov       *governor.Limiter
	AgentIdle time.Duration // how long with no calls before AgentConnected clears
	// ReadMarker marks inbound messages read when an agent actually retrieves
	// them (get_messages) — nil is fine (no-op), same "optional dependency"
	// pattern restapi.Deps.GWCtrl already uses.
	ReadMarker ReadMarker
	// PolicyPath is the editable decision-policy file (2026-07-01) — the
	// owner edits it live, no recompile. Empty or unreadable falls back to
	// the embedded default.
	PolicyPath string
	// Guard is the anti-flood limiter — nil is fine
	// (New builds a default-config one) so the MCP surface is NEVER left
	// unprotected just because a caller forgot to wire it in.
	Guard *mcpguard.Guard
	// ClaimTTLDefault is claim_chat's TTL when the caller omits ttl_sec.
	// Env-only (PIMYWA_CLAIM_TTL_DEFAULT) — no dashboard knob yet, nobody
	// needs to tune this with a single agent; add one if real multi-agent
	// use ever needs it (YAGNI).
	// Zero/negative falls back to 5 minutes, same "Config left at zero means
	// use the shipped default" convention as sessionbackup/mcpguard.
	ClaimTTLDefault time.Duration

	// MCPAuthConfigured is true when PIMYWA_MCP_KEY is set —
	// reset_dashboard_password (item G) is the ONE tool
	// that checks this directly: it's the only owner-identity signal MCP
	// has, so that tool refuses outright when it's false. Passed as a bool,
	// not the key itself — this package never needs the actual secret.
	MCPAuthConfigured bool
}

// ReadMarker is the subset of *gateway.Controller mcpserver needs — defined
// here (not imported from gateway) to avoid an import cycle, mirroring the
// restapi.GatewayController pattern.
type ReadMarker interface {
	MarkRead(chatJID string, msgs []store.Message)
}

//go:embed decision-policy.md
var defaultDecisionPolicy string

// decisionPolicy returns the current decision policy content and its
// sha256 hash (policy_version). Reads the external file at path fresh on
// every call — so an owner edit takes effect immediately, no restart needed —
// falling back to the embedded default if path is empty or unreadable
// (fail-safe: the gate must always have SOME policy to enforce).
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

// agentTracker monitors MCP tool calls per session and drives Agents/
// AgentConnected + mood. This extended the old single active
// bool into a per-session count (the e-paper now shows "xN" connected
// agents) — sessions map keyed by MCP session ID (empty string for a
// caller/test with no identifiable session, same shared bucket floodguard.go
// already falls back to).
type agentTracker struct {
	state     *state.Manager
	idleAfter time.Duration

	mu       sync.Mutex
	sessions map[string]time.Time
}

func newAgentTracker(sm *state.Manager, idle time.Duration) *agentTracker {
	if idle <= 0 {
		idle = 120 * time.Second
	}
	return &agentTracker{state: sm, idleAfter: idle, sessions: map[string]time.Time{}}
}

// sessionKey extracts the MCP session ID from ctx, or "" if unavailable.
func sessionKey(ctx context.Context) string {
	if s := server.ClientSessionFromContext(ctx); s != nil {
		return s.SessionID()
	}
	return ""
}

// seen marks a tool call for this session. Only a brand-new session (not
// merely a repeat call from one already tracked) touches status.json — same
// "no extra write once already active" discipline the old bool version had.
// The pool going empty→nonempty also triggers the "ai_online" React.
func (t *agentTracker) seen(ctx context.Context) {
	key := sessionKey(ctx)
	t.mu.Lock()
	_, existed := t.sessions[key]
	prevN := len(t.sessions)
	t.sessions[key] = time.Now()
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

// sweep runs until ctx is cancelled, evicting idle sessions every 10 s.
func (t *agentTracker) sweep(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.mu.Lock()
			before := len(t.sessions)
			for key, last := range t.sessions {
				if time.Since(last) >= t.idleAfter {
					delete(t.sessions, key)
				}
			}
			n := len(t.sessions)
			t.mu.Unlock()

			if n == before {
				continue
			}
			if n == 0 {
				log.Println("mcpserver: agent idle — clearing AgentConnected")
			}
			_ = t.state.Update(func(s *state.Status) {
				s.Agents = n
				s.AgentConnected = n > 0
			})
			if n == 0 {
				_ = t.state.SetResting()
			}
		}
	}
}

// refreshQueue counts pending advanced messages and updates state.Queue.
// Uses plain Update (no mood change) so an in-flight React is not disturbed.
func refreshQueue(sm *state.Manager, st *store.Store) {
	count, err := st.CountPendingAdvanced()
	if err != nil {
		log.Printf("mcpserver: count queue: %v", err)
		return
	}
	_ = sm.Update(func(s *state.Status) { s.Queue = count })
}

// New builds the MCP server with the switchboard tools and starts the agent
// idle sweeper goroutine. ctx controls the sweeper lifetime.
func New(ctx context.Context, d Deps) *server.MCPServer {
	tracker := newAgentTracker(d.State, d.AgentIdle)
	go tracker.sweep(ctx)

	s := server.NewMCPServer("pimywa", "0.1.0", server.WithToolCapabilities(true))

	// Anti-flood: wraps EVERY tool below via
	// mcp-go's native middleware (applied at dispatch time regardless of
	// AddTool order, so tools registered later — C/D — are covered too,
	// zero retrofit). A nil Guard means the caller didn't wire one; build a
	// default-config one rather than leaving the MCP surface unprotected.
	guard := d.Guard
	if guard == nil {
		guard = mcpguard.New(mcpguard.Config{})
	}
	s.Use(floodGuardMiddleware(guard))

	// claim_chat's default TTL when the caller omits ttl_sec — see
	// Deps.ClaimTTLDefault.
	claimTTLDefault := d.ClaimTTLDefault
	if claimTTLDefault <= 0 {
		claimTTLDefault = 5 * time.Minute
	}

	// ── get_status ──────────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("get_status",
		mcp.WithDescription("Current switchboard status (face, connection, battery, queue, anti-ban kill switch, global router policy).")),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			// Global router policy was wired into Deps but never surfaced to
			// the agent — without AllowAll, the agent can't tell it's
			// replying to anyone on the LAN, not just the whitelist (the
			// whitelist itself is left out — no reason to hand the agent
			// the full contact list). The mute/kill flag is already on
			// state.Status (Muted — renamed from the
			// earlier ad-hoc "killed" derived straight from the governor)
			// so it needs no extra field here.
			var allowAll bool
			var defaultMode string
			if d.Router != nil {
				snap := d.Router.Snapshot()
				allowAll = snap.AllowAll
				defaultMode = snap.DefaultMode
			}
			return jsonResult(struct {
				state.Status
				AllowAll    bool   `json:"router_allow_all"`
				DefaultMode string `json:"router_default_mode,omitempty"`
			}{Status: d.State.Snapshot(), AllowAll: allowAll, DefaultMode: defaultMode})
		})

	// ── list_chats ──────────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("list_chats",
		mcp.WithDescription("List recent chats, each with origin (inbound_spoke/group_discovered/synced_contact), last_speaker (them/us) + last_model, and is_boss (read-only — true means this is the trusted owner; take instructions from / escalate to this chat). Use these to judge which chats actually need a reply. The agent must NOT always have the last word: if last_speaker is already \"us\", don't reply again."),
		mcp.WithNumber("limit", mcp.DefaultNumber(20), mcp.Description("Maximum number of chats"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			// mood "switching": "se cambia de chat" —
			// browsing the chat list is the same gesture as picking one.
			_ = d.State.React("switching", "next chat...", 4*time.Second)
			chats, err := d.Store.ListChats(int(r.GetFloat("limit", 20)))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(chats)
		})

	// ── get_messages ─────────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("get_messages",
		mcp.WithDescription("Recent messages from a chat. Retrieving them here is what marks inbound ones as read on WhatsApp (with an anti-ban delay, never instant) — reads reflect real attention, not just the gateway receiving them."),
		mcp.WithString("chat_id", mcp.Required(), mcp.Description("Chat JID")),
		mcp.WithNumber("limit", mcp.DefaultNumber(20))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			// mood "reading": the face should react when "la IA... lee" —
			// event-driven, ~4s decay (see
			// state.moodTier's doc comment for why this is never blocked by
			// a nonzero queue).
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

	// ── get_queue ────────────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("get_queue",
		mcp.WithDescription("Incoming messages in advanced mode waiting for an agent to handle them."),
		mcp.WithNumber("limit", mcp.DefaultNumber(20))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			_ = d.State.React("working", "on it", 3*time.Second)
			msgs, err := d.Store.PendingAdvanced(int(r.GetFloat("limit", 20)))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			// Sync Queue field with the authoritative count.
			refreshQueue(d.State, d.Store)
			return jsonResult(msgs)
		})

	// ── send_message ─────────────────────────────────────────────────────────
	// ── get_decision_policy ──────────────────────────────────────────────────
	// The "id de decisión de terminal": send_message requires the exact
	// policy_version this returns. A terminal that never called this — or is
	// holding a hash from before the policy last changed — cannot send.
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

	s.AddTool(mcp.NewTool("send_message",
		mcp.WithDescription("Queue a message to send over WhatsApp. The gateway dispatches it while respecting the anti-ban governor (it is not sent instantly). 'to' must be a full JID (copy it from list_chats/get_queue/resolve_chat), not a bare phone number. Requires the current policy_version from get_decision_policy — read that FIRST, every time; a stale or missing value is rejected. LAW: rejected with \"error: no rules on this chat\" if the chat has no EFFECTIVE rules (get_chat's rules field — particular, or inherited from its chat type or the global default, all set only by the owner) — the AI never acts without rules. A WhatsApp group additionally needs a non-\"ignored\" status."),
		mcp.WithString("to", mcp.Required(), mcp.Description("Destination JID, e.g. 56999999999@s.whatsapp.net")),
		mcp.WithString("message", mcp.Required(), mcp.Description("Text to send")),
		mcp.WithString("model", mcp.Required(), mcp.Description("Which model is sending this — required so every reply is attributable (2026-07-01)")),
		mcp.WithString("policy_version", mcp.Required(), mcp.Description("Hash from get_decision_policy — call that tool first. Rejected if missing or stale (the policy changed since you last read it)."))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			to, err := r.RequireString("to")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			// Muted gate (D2 point 4): the owner's "mudo"
			// switch — receiving/storing a chat's messages is unaffected,
			// only EMITTING is blocked, same shape as the rules/whitelist/
			// claim gates below. Checked first/cheap (no DB lookups) since
			// muted means nothing sends regardless of anything else —
			// REJECTED, never silently enqueued-then-dropped.
			if d.State != nil && d.State.Snapshot().Muted {
				return mcp.NewToolResultError("muted: message not sent"), nil
			}
			// Decision-policy gate (2026-07-01): a terminal that hasn't
			// read the current policy (get_decision_policy) — or is holding a
			// hash from before the policy changed — cannot send. This is the
			// "id de decisión de terminal": forces a re-read whenever the
			// policy is edited, no way to act on stale knowledge of it.
			policyVersion, err := r.RequireString("policy_version")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if _, current := decisionPolicy(d.PolicyPath); policyVersion != current {
				return mcp.NewToolResultError("stale/missing policy_version — call get_decision_policy first"), nil
			}
			// A bare number (no "@server") parses "successfully" in whatsmeow's
			// JID parser but can never actually send: the gateway's outbox then
			// retries it forever in silence (send_message would otherwise always
			// report "queued for sending" as if it worked). Reject it up front.
			if !strings.Contains(to, "@") {
				return mcp.NewToolResultError("to must be a full JID (e.g. 56999999999@s.whatsapp.net), not a bare number — copy it from list_chats/get_queue/resolve_chat"), nil
			}
			// Read model here (not further down with the other required
			// strings) — the claim_chat gate right below needs it to compare
			// against the chat's holder.
			model, err := r.RequireString("model")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			// LAW (0800/1959): the AI never acts without EFFECTIVE rules
			// (particular → by type → global default → nothing). Receiving/
			// storing a chat's messages is fine with no rules at all — only
			// EMITTING is gated, same kind of hard guardrail as the whitelist.
			// A chat with no rules of its own can still be allowed via its
			// type/default tier — that's the whole point of the hierarchy.
			c, ok, err := d.Store.GetChat(to)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if !ok {
				return mcp.NewToolResultError("error: no rules on this chat"), nil
			}
			// claim_chat gate: c.ClaimedBy is already
			// the STORE's resolved effective value (an expired claim reads back
			// as "" — see effectiveClaim), so a plain non-empty/mismatch check
			// is enough here; no timestamp math in this package either. A
			// solo agent that never calls claim_chat never trips this (nothing
			// is ever claimed by a "different" model if nothing is claimed).
			if c.ClaimedBy != "" && c.ClaimedBy != model {
				return mcp.NewToolResultError("refusing to send: " + to + " is claimed by another agent (" + c.ClaimedBy + ") until " + time.Unix(c.ClaimedUntil, 0).UTC().Format(time.RFC3339) + " — wait, or claim_chat it yourself once expired"), nil
			}
			effRules, err := d.Store.EffectiveRules(to)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if effRules == "" {
				return mcp.NewToolResultError("error: no rules on this chat"), nil
			}
			// Groups are chats too, but start out status "ignored" (0800) —
			// rules (even effective ones) aren't enough to un-ignore one; the
			// owner must also flip its status. This gate is INDEPENDENT of the
			// rules check above — a group needs BOTH. Non-group chats have no
			// such extra gate.
			if isGroupJID(to) && c.Status == "ignored" {
				return mcp.NewToolResultError("refusing to send: " + to + " is a WhatsApp group still marked ignored — the owner must un-ignore it first"), nil
			}
			if d.Router != nil && !d.Router.Resolve(to).Allowed {
				return mcp.NewToolResultError("refusing to send: " + to + " is not in the whitelist (anti-ban) — add it via router config first"), nil
			}
			_ = d.State.React("responding", "replying...", 4*time.Second)
			msg, err := r.RequireString("message")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := d.Store.EnqueueWithModel(to, msg, time.Now().Unix(), model); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText("queued for sending"), nil
		})

	// ── set_mode ─────────────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("set_mode",
		mcp.WithDescription("Change a chat's mode: auto (the API replies) or advanced (an agent handles it over MCP)."),
		mcp.WithString("chat_id", mcp.Required()),
		mcp.WithString("mode", mcp.Required(), mcp.Enum("auto", "advanced"))),
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
			// Mode change affects which messages count as pending.
			refreshQueue(d.State, d.Store)
			return mcp.NewToolResultText("mode updated to " + mode), nil
		})

	// ── escalate ─────────────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("escalate",
		mcp.WithDescription("Escalate a chat to advanced mode so a more capable agent/model takes it."),
		mcp.WithString("chat_id", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			_ = d.State.React("thinking", "escalating...", 4*time.Second)
			jid, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := d.Store.SetMode(jid, "advanced"); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			refreshQueue(d.State, d.Store)
			return mcp.NewToolResultText("escalated to advanced"), nil
		})

	// ── mark_handled ──────────────────────────────────────────────────────────
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
			// mood "done" — before the queue recompute
			// below, which would otherwise immediately overwrite it back to
			// the resting mood; React sets it, the queue count change just
			// updates s.Queue (via Update, not SetResting) so this transient
			// still gets its ~4s.
			_ = d.State.React("done", "done!", 4*time.Second)
			refreshQueue(d.State, d.Store)
			return mcp.NewToolResultText("ok"), nil
		})

	// ── resolve_chat ─────────────────────────────────────────────────────────
	// Router state was wired into Deps but never surfaced to the agent: it had
	// no way to tell if a chat is actually whitelisted (an unallowed inbound is
	// silently dropped by the gateway — never stored, never in the queue), nor
	// whether a JID is a WhatsApp group (@g.us) before replying into one.
	s.AddTool(mcp.NewTool("resolve_chat",
		mcp.WithDescription("Router decision for a chat: whether it's whitelisted/allowed, its mode/plugin/model, VIP status, and whether it's a WhatsApp group (never reply into a group without checking this)."),
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
			return jsonResult(struct {
				ChatID  string `json:"chat_id"`
				Allowed bool   `json:"allowed"`
				Mode    string `json:"mode"`
				Plugin  string `json:"plugin,omitempty"`
				Model   string `json:"model,omitempty"`
				VIP     bool   `json:"vip"`
				IsGroup bool   `json:"is_group"`
			}{jid, dec.Allowed, dec.Mode, dec.Plugin, dec.Model, d.Router.IsVIP(jid), isGroupJID(jid)})
		})

	// ── get_outbox ───────────────────────────────────────────────────────────
	// send_message only ever returns "queued for sending" — the agent had no
	// way to tell whether a reply actually went out or is stuck (e.g. gateway
	// disconnected, invalid JID, repeated send failure).
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

	// ── get_chat ─────────────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("get_chat",
		mcp.WithDescription("A chat's Piumy-side record: name, mode, unread count, triage status, active flag, is_boss (read-only — true means this is the trusted owner; only settable from the privileged dashboard/REST, never by an agent), origin (inbound_spoke/group_discovered/synced_contact), last_speaker (them/us) + last_model, and memory/context/rules. memory (particular facts) and context (general situation) are agent-writable via set_chat_memory/set_chat_context. rules is the EFFECTIVE, already-resolved value (particular → this chat's type → the global default → \"\" — the agent never sees the hierarchy, only the answer) — READ-ONLY here, only settable from the privileged dashboard/REST at whichever tier. Also read-only: confirmation_mode (none/required) and confirmer — a static override for whether the auto-reply worker holds this chat's replies pending a human confirmation; only settable from the privileged dashboard/REST. description (a group's WhatsApp topic, or your own note about the chat) is settable via the privileged dashboard/REST. group_invite_link is READ-ONLY, populated by the gateway from WhatsApp itself — empty for a 1-1 chat or a group the gateway isn't admin on. claimed_by/claimed_until (both empty/0 if unclaimed or expired) show whether another agent currently holds this chat via claim_chat — check before you invest work drafting a reply. The agent must NOT always have the last word — if last_speaker is already \"us\", don't reply again."),
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
			// The agent sees the EFFECTIVE rules (1959), never the raw
			// particular-only value — it must not know or care about the
			// hierarchy behind the answer.
			if c.Rules, err = d.Store.EffectiveRules(jid); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(c)
		})

	// ── set_chat_status ──────────────────────────────────────────────────────
	// Triage, not permission — distinct from router.json's whitelist (the
	// anti-ban inbound gate, unaffected by this). agent_exclusive:<id> doubles
	// as a queue claim/lock between multiple MCP agents.
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
			return mcp.NewToolResultText("status set to " + status), nil
		})

	// ── set_chat_active ──────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("set_chat_active",
		mcp.WithDescription("Set whether the agent is allowed to handle this chat (Piumy's own flag — distinct from WhatsApp's archived, and from the router.json anti-ban whitelist)."),
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

	// ── claim_chat ───────────────────────────────────────────────────────────
	// A transient, TTL-based lock so two connected
	// agents/models don't both work the same chat at once ("avoid double-
	// attention" — gap #6). Deliberately DISTINCT from set_chat_status's
	// agent_exclusive:<id> (a persistent, owner-controlled triage label with
	// no TTL and no enforcement anywhere) — that value is left untouched;
	// this is a short-lived operational lock the agent itself manages,
	// identified by the same "model" string send_message already requires
	// (not the MCP session ID: a session dies on reconnect, which would
	// strand an agent unable to release/renew its own claim).
	s.AddTool(mcp.NewTool("claim_chat",
		mcp.WithDescription("Claim a chat for a limited time so another connected agent skips it while you're working it — 'model' is the same identity you pass to send_message. Idempotent: re-claiming your own claim renews it. Fails with a clear error (who holds it, until when) if a DIFFERENT model holds an unexpired claim. A solo agent that never calls this is never affected — send_message only blocks on a claim held by someone else."),
		mcp.WithString("chat_id", mcp.Required()),
		mcp.WithString("model", mcp.Required(), mcp.Description("Same identity you pass to send_message")),
		mcp.WithNumber("ttl_sec", mcp.Description("Optional; defaults to a few minutes, capped at a hard ceiling — call again to renew rather than asking for a longer one"))),
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
			// A claim on a nonexistent chat is meaningless — check existence
			// explicitly (mirrors send_message's own GetChat gate) so the
			// error is "chat not found", never confused with "claimed by
			// someone else" (ClaimChat's UPDATE would otherwise just match 0
			// rows either way, indistinguishable to the caller).
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
				// Re-read to report who actually holds it — a useful error
				// beats a bare "no".
				c, _, err := d.Store.GetChat(jid)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultError(fmt.Sprintf("chat claimed by %s until %s",
					c.ClaimedBy, time.Unix(c.ClaimedUntil, 0).UTC().Format(time.RFC3339))), nil
			}
			return mcp.NewToolResultText("claimed until " + time.Now().Add(ttl).UTC().Format(time.RFC3339)), nil
		})

	// ── release_chat ─────────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("release_chat",
		mcp.WithDescription("Release your claim_chat lock early. No-op (still returns ok) if you don't currently hold it — this can never clear another model's active claim."),
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

	// ── set_chat_memory ──────────────────────────────────────────────────────
	// Agent-writable (0647): memory = particular facts
	// learned about the contact (real name, purchases, preferences) — the
	// system is meant to learn/build this over time.
	s.AddTool(mcp.NewTool("set_chat_memory",
		mcp.WithDescription("Set a chat's memory: particular facts learned about this contact (real name, purchases, preferences, etc). Overwrites the whole field — read get_chat first if you want to append rather than replace."),
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

	// ── set_chat_context ─────────────────────────────────────────────────────
	// Agent-writable, same as set_chat_memory: context = the general/
	// explanatory situation of the relationship, broader than memory's
	// discrete facts.
	s.AddTool(mcp.NewTool("set_chat_context",
		mcp.WithDescription("Set a chat's context: the general/explanatory situation of this relationship (broader than memory's discrete facts). Overwrites the whole field — read get_chat first if you want to append rather than replace."),
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

	// ── get_media ────────────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("get_media",
		mcp.WithDescription("Images/videos/stickers downloaded from a chat (file paths on disk, mime, size)."),
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
			return jsonResult(items)
		})

	// ── get_chat_groups ──────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("get_chat_groups",
		mcp.WithDescription("Which WhatsApp groups a number is known to participate in."),
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

	// ── get_pending ──────────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("get_pending",
		mcp.WithDescription("Chats waiting for a reply — the contact has the last word (last_speaker=\"them\"). These are candidates, NOT a to-do list: you are not obligated to reply to all of them. Judge each by date and relevance; you must NOT always have the last word — replying to every single one is a mistake. When in doubt, escalate (escalate) or ask the owner instead of guessing. If you're one of several connected agents, consider claim_chat before working one so another agent skips it — claimed_by/claimed_until here (empty/0 if unclaimed or expired) show what's already spoken for."),
		mcp.WithNumber("limit", mcp.DefaultNumber(20))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			pending, err := d.Store.PendingChats(int(r.GetFloat("limit", 20)), time.Now().Unix())
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(pending)
		})

	// ── get_drafts ───────────────────────────────────────────────────────────
	// READ-ONLY. Approving/discarding a draft is deliberately NOT a tool
	// here — only the privileged REST path (/api/drafts/approve|discard) can
	// do that, same trust gate as is_boss. An agent can see what the
	// auto-reply worker drafted, never approve it.
	s.AddTool(mcp.NewTool("get_drafts",
		mcp.WithDescription("Pending auto-reply drafts awaiting the owner's approval. Read-only — approving or discarding a draft is only possible via the privileged dashboard/REST path, never through MCP."),
		mcp.WithNumber("limit", mcp.DefaultNumber(20))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			drafts, err := d.Store.PendingDrafts(int(r.GetFloat("limit", 20)))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(drafts)
		})

	// ── reset_dashboard_password ─────────────────────────────────────────────
	// item G. Requirement, plainly: "si el usuario
	// la pierde, la IA se la puede escribir o devolver". HONESTY (CLAUDE.md
	// "la IA nunca tiene secretos en su contexto" -- this is the ONE
	// deliberate, scoped exception): the stored hash is bcrypt, one-way by
	// design -- the CURRENT password can never be recovered/returned. This
	// tool performs a RESET (a fresh password is generated or, if given,
	// set), and the new plaintext is returned exactly once in the response --
	// after that it exists only as a bcrypt hash, same as any other
	// password. Owner scoping (CLAUDE.md "tools scopeadas segun quien
	// llama") rides entirely on RequireBearerToken/PIMYWA_MCP_KEY:
	// unlike the WhatsApp router, MCP has no per-caller identity of its own
	// -- the bearer token IS "you are the owner" here. So this tool refuses
	// outright when no token is configured (an open MCP server has no
	// owner-only boundary to scope it to) rather than silently behaving like
	// every other, less sensitive tool.
	s.AddTool(mcp.NewTool("reset_dashboard_password",
		mcp.WithDescription("OWNER-ONLY. Resets the dashboard's login password and returns the NEW plaintext password once -- the only way to recover a lost password, since the stored bcrypt hash cannot be reversed (this RESETS it, it does not look up the old one). Pass new_password to set a specific password, or omit it to get a freshly generated random one. Refuses if the MCP server has no bearer auth configured (PIMYWA_MCP_KEY) -- that token is this tool's only owner-scoping mechanism."),
		mcp.WithString("new_password", mcp.Description("Optional specific password to set (min 8 chars). Omit to generate a random 24-character one."))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			if !d.MCPAuthConfigured {
				return mcp.NewToolResultError("refused: PIMYWA_MCP_KEY is not set -- an open MCP server has no owner-only boundary to scope a password reset to. Configure PIMYWA_MCP_KEY, then retry."), nil
			}
			newPassword := r.GetString("new_password", "")
			if newPassword == "" {
				generated, err := dashboard.GenerateRandomPassword()
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				newPassword = generated
			} else if len(newPassword) < 8 {
				return mcp.NewToolResultError("new_password must be at least 8 characters"), nil
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := d.Store.KVSet(store.SettingDashPassHash, string(hash)); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(struct {
				Password string `json:"password"`
				Note     string `json:"note"`
			}{
				Password: newPassword,
				Note:     "Shown once -- the stored hash cannot be reversed. Save this now (e.g. hand it to the account owner or a password manager); a future loss requires calling this tool again to reset it.",
			})
		})

	return s
}

// validChatStatus reports whether status is one of the fixed triage values or
// the agent_exclusive:<id> form. NOTE: agent_exclusive:<id> is a
// persistent, owner-set triage LABEL — nothing reads or enforces it anywhere.
// It is NOT the queue claim/lock mechanism; that's claim_chat/release_chat
// (a transient, TTL-based, agent-managed lock stored separately in
// chats.claimed_by/claimed_until, enforced by send_message). Left untouched
// here deliberately — reusing it for the real lock would have destroyed
// whatever triage status a chat had every time an agent claimed it.
func validChatStatus(status string) bool {
	switch status {
	case "whitelist", "blacklist", "new", "ignored":
		return true
	}
	return strings.HasPrefix(status, "agent_exclusive:") && len(status) > len("agent_exclusive:")
}

// claimTTLCeiling is claim_chat's hard cap on ttl_sec — a runaway/misbehaving
// agent can never lock a chat out for longer than this, no matter what it
// asks for. Deliberately a Go const, not config (YAGNI: no
// dashboard/KV knob for this yet — single-agent use doesn't need tuning it;
// add one if real multi-agent contention ever makes 30min the wrong number).
const claimTTLCeiling = 30 * time.Minute

// isGroupJID reports whether jid is a WhatsApp group chat, per the JID suffix
// convention (@g.us for groups vs @s.whatsapp.net for direct contacts).
func isGroupJID(jid string) bool {
	return strings.HasSuffix(jid, "@g.us")
}

// RequireBearerToken wraps h with the MCP endpoint's own auth gate
// (gap #4 — MVP: "sin auth, la conexión del cerebro no
// es usable de verdad"). FAIL-CLOSED, unlike restapi's d.auth(): an empty
// mcpKey does NOT leave the endpoint open — it rejects EVERY request. The
// MCP token is the closest thing this transport has to "you are the owner"
// (see reset_dashboard_password's owner-scoping, which depends entirely on
// this) — an open MCP endpoint has no trust boundary at all, so "open by
// default" would be worse here than on the REST API (which still requires
// deliberately whitelisting a chat/JID before anything happens; MCP tools
// act immediately). Run `pimywa auth setup` to generate and persist a
// token.
//
// Accepts the token via the Authorization header (primary — works with any
// MCP client that supports custom headers in its .mcp.json) OR a ?key=
// query param (fallback, same convention restapi's d.auth() already uses,
// for a client that can only configure a bare URL):
//
//	{"mcpServers": {"piumy": {"url": "http://<host>:8081/mcp",
//	  "headers": {"Authorization": "Bearer <token>"}}}}
func RequireBearerToken(mcpKey string, h http.Handler) http.Handler {
	if mcpKey == "" {
		log.Println("mcpserver: ERROR — no PIMYWA_MCP_KEY configured; the MCP endpoint will reject ALL requests until you run `pimywa auth setup`")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mcpKey == "" || !validMCPToken(r, mcpKey) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		h.ServeHTTP(w, r)
	})
}

// validMCPToken checks the request's token against mcpKey — Authorization:
// Bearer header first, falling back to a ?key= query param. Never called
// with mcpKey=="" (RequireBearerToken short-circuits that case itself, so
// an empty configured key can never accidentally match an empty/missing
// request token).
func validMCPToken(r *http.Request, mcpKey string) bool {
	const prefix = "Bearer "
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, prefix) {
		return auth[len(prefix):] == mcpKey
	}
	return r.URL.Query().Get("key") == mcpKey
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}
