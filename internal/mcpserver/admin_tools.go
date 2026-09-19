// DB-admin tools (F4-DESIGN §3/§5) — boss-only, no caution/danger exception
// ever, EXCEPT set_chat_rules (T31, ct-2026-08-06-0244 — see its own doc).
// All call store methods already migrated in F1a; nothing new there. Same
// methods are also exposed via the privileged REST path (restapi, this
// same contract) for the owner administering directly from the LAN.
//
// S10 (ct-2026-07-30-1349): set_type_rules/set_is_boss/set_confirmation_mode/
// set_config_level enforce their own authorization HERE, in the handler —
// not just via levelGateMiddleware's bossOnlyTools map (see its own doc for
// why: verified live that its principal-terminal bypass let ANY MCP caller
// through with zero gating). "OWNER-ONLY" in a description is a prompt;
// this check is the code CLAUDE.md #4 requires it to be. set_chat_rules WAS
// on this list too, S10 through T30 — T31 unblocked it, unconditionally,
// per-chat only (the type tier stays exactly as S10 left it; set_default_rules,
// its sibling, was removed entirely in T79, ct-2026-08-27-2034 — the global
// tier it gated had nothing left to reach).
package mcpserver

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"piumy-gateway/internal/capiconn"
	"piumy-gateway/internal/store"
)

// mcpBlockedRules/mcpBlockedIsBoss are the refusals set_type_rules/
// set_is_boss return unconditionally — no dispatch, no principal terminal,
// no argument value ever lets these through via MCP (the boss's decision,
// verbatim: "que sea manual solo por el boss, el unico gate no ia"). The
// type tier is a broader, system-wide change the boss never asked to open
// (T31, ct-2026-08-06-0244 — see set_chat_rules' own doc for what DID open
// and why); is_boss is the master key. Both point at the dashboard's own
// REST admin path, which calls the exact same store method directly
// (restapi/admin.go) — this refusal never blocks the owner, only MCP.
const (
	mcpBlockedRules  = "refused: rules are never settable via MCP, not even by the principal — use the dashboard (POST /api/admin/%s)"
	mcpBlockedIsBoss = "refused: is_boss is never settable via MCP, not even by the principal — use the dashboard (POST /api/admin/is-boss)"
)

// isActiveBossDispatch reports whether terminalID's CURRENTLY bound dispatch
// is boss-level and still usable (S10, ct-2026-07-30-1349) — the gate for
// "liberating" a chat to auto/none: the boss saying "atendé a este número"
// registers a boss-level dispatch on the calling terminal, so this is true;
// the chat's OWN interested party asking to be freed arrives as a
// caution/danger dispatch, so this is false. Same Ready requirement as
// ST-A (ct-2026-07-11-0740)/validateSend: a consumed (done) boss dispatch
// must not keep granting this any more than it keeps granting send access.
func isActiveBossDispatch(gate *Gate, ctx context.Context) bool {
	active, ok := gate.Active(terminalIDFromContext(ctx))
	return ok && active.Level == LevelBoss && active.Ready
}

// isActiveApproverDispatch reports whether terminalID's currently bound
// dispatch is boss- OR approver-level and still usable (Aprobador P1,
// ct-2026-07-31-0610) — used ONLY by approve_draft. Deliberately NOT reused
// by set_confirmation_mode("none")/set_config_level("auto")/set_is_approver:
// those stay isActiveBossDispatch-only, boss-exclusive — an approver
// approves the text that goes out, nothing about supervision itself
// (boss's own decision, "lo que tiene que quedar cierto" #3). Widening this
// helper's callers is widening what "aprobador" means; don't.
func isActiveApproverDispatch(gate *Gate, ctx context.Context) bool {
	active, ok := gate.Active(terminalIDFromContext(ctx))
	return ok && (active.Level == LevelBoss || active.Level == LevelApprover) && active.Ready
}

func addAdminTools(s *server.MCPServer, d Deps, gate *Gate, tracker *agentTracker) {
	// ── set_chat_rules ───────────────────────────────────────────────────
	// T31 (ct-2026-08-06-0244) reverses S10 (ct-2026-07-30-1349) for this
	// ONE tool — unconditional, no dispatch level, no chat scoping, no
	// principal check. Two prior versions of this contract (a single key —
	// boss-chat dispatch — then a double key — principal AND boss-chat
	// dispatch) were both rejected by the boss himself, verbatim: "No
	// pongas condiciones, que la skill recomiende nada mas, me cargan que
	// metan tantas limitaciones y frenos miedosos... ya es responsabilidad
	// del usuario." Same day's evidence backs him: the router whitelist
	// that locked HIM out (T30), the empty-rules dead end (T5), the
	// mandatory encryption (T28) — all precaution added ahead of a real
	// need, all later spent a contract getting undone. set_type_rules/
	// set_default_rules stay exactly as S10 left them (MCP-BLOCKED,
	// unconditionally) — broader, system-wide changes the boss never asked
	// to open; only per-chat rules did. The architecture he actually wants
	// (a separate agent for known vs. unknown contacts) lives as a
	// recommendation in the piumy-operator skill, not as a code gate here.
	s.AddTool(mcp.NewTool("set_chat_rules",
		mcp.WithDescription("Set a chat's rules — the permission the agent operates under for that chat. Unconditional (T31, ct-2026-08-06-0244): any chat_id, any dispatch level, no code-level restriction — the boss's own call, verbatim: \"ya es responsabilidad del usuario\". See the piumy-operator skill for his architecture recommendation (a separate agent for chats you don't already trust) before reaching for this on one. Overwrites the whole field."),
		mcp.WithString("chat_id", mcp.Required()),
		mcp.WithString("rules", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			jid, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			rules, err := r.RequireString("rules")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := d.Store.SetChatRules(jid, rules); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText("rules set"), nil
		})

	// ── set_is_boss ──────────────────────────────────────────────────────
	// S10 (ct-2026-07-30-1349): MCP-BLOCKED, unconditionally — the master
	// key. An agent that can grant itself (or anyone) boss trust has no
	// gate left; this is the one setter that must never have an MCP path,
	// principal included.
	s.AddTool(mcp.NewTool("set_is_boss",
		mcp.WithDescription("MCP-BLOCKED, always. is_boss is the master key — grants boss-level trust, exempt from the confirmation gate. Never settable via MCP, not even by the principal — set it from the dashboard (POST /api/admin/is-boss)."),
		mcp.WithString("chat_id", mcp.Required()),
		mcp.WithBoolean("is_boss", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			return mcp.NewToolResultError(mcpBlockedIsBoss), nil
		})

	// ── set_type_rules ───────────────────────────────────────────────────
	// S10 (ct-2026-07-30-1349): MCP-BLOCKED, same reasoning as set_chat_rules.
	// Group-only since T79 (ct-2026-08-27-2034) removed the "individual"
	// tier (dead since M5) and set_default_rules entirely — chat_type
	// dropped, there's nothing left to choose between.
	s.AddTool(mcp.NewTool("set_type_rules",
		mcp.WithDescription("MCP-BLOCKED, always. Rules are the permission an agent operates under, not something it writes about itself — set the group rules tier from the dashboard (POST /api/admin/type-rules), never from here, not even by the principal."),
		mcp.WithString("rules", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			return mcp.NewToolResultError(fmt.Sprintf(mcpBlockedRules, "type-rules")), nil
		})

	// ── set_confirmation_mode ────────────────────────────────────────────
	// S10 (ct-2026-07-30-1349): split by target value, not boss-or-nothing.
	// "always"/"discretion" RESTRICT (require confirmation before sending)
	// — always allowed, no permission needed, same reasoning as triage.
	// "none" LIBERATES (lets send_message send unconfirmed) — only while
	// the CURRENT dispatch is boss-level (isActiveBossDispatch): the boss
	// saying "atendé a este número" must be able to free it, but the
	// chat's own interested party asking the same thing must not be able
	// to free itself.
	s.AddTool(mcp.NewTool("set_confirmation_mode",
		mcp.WithDescription("Set a chat's confirmation_mode. 'always'/'discretion' (require confirmation before sending) restrict and are always allowed. 'none' (send_message sends directly, no confirmation) liberates — only allowed while the CURRENT dispatch is boss-level (e.g. the boss saying 'atendé a este número'); a caution/danger dispatch can never grant itself this."),
		mcp.WithString("chat_id", mcp.Required()),
		mcp.WithString("mode", mcp.Required(), mcp.Enum("none", "discretion", "always"))),
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
			if mode == "none" && !isActiveBossDispatch(gate, ctx) {
				return mcp.NewToolResultError("refused: liberating to confirmation_mode=none requires the CURRENT dispatch to be boss-level — a caution/danger dispatch cannot grant this to itself"), nil
			}
			if err := d.Store.SetConfirmationMode(jid, mode); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText("confirmation_mode set to " + mode), nil
		})

	// ── set_config_level ─────────────────────────────────────────────────
	// Translation layer (config_level unification) over the existing 4
	// fields — TRANSLATES the chosen level to SetIsBoss/SetActive/
	// SetConfirmationMode/SetStatus (store.SetConfigLevel), reusing those
	// setters verbatim. Does not replace set_is_boss/set_confirmation_mode/
	// set_chat_status/set_chat_active — they keep working standalone.
	//
	// S10 (ct-2026-07-30-1349): level=boss is MCP-BLOCKED (same as
	// set_is_boss — it sets is_boss=true underneath). confirm/unattended/
	// ignored RESTRICT — always allowed. auto LIBERATES — only allowed
	// while the CURRENT dispatch is boss-level, same isActiveBossDispatch
	// check as set_confirmation_mode's "none" (this is what a "level=auto"
	// call ultimately sets anyway).
	s.AddTool(mcp.NewTool("set_config_level",
		mcp.WithDescription("Set a chat's unified config level. 'confirm'/'unattended'/'ignored' restrict and are always allowed. 'auto' (agent replies on its own, no confirmation) liberates — only allowed while the CURRENT dispatch is boss-level (e.g. the boss saying 'atendé a este número'); a caution/danger dispatch can never grant itself this. 'boss' is MCP-BLOCKED, always — set it from the dashboard (POST /api/admin/config-level), not even the principal can set it here."),
		mcp.WithString("chat_id", mcp.Required()),
		mcp.WithString("level", mcp.Required(), mcp.Enum("boss", "auto", "confirm", "unattended", "ignored"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			jid, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			level, err := r.RequireString("level")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if level == "boss" {
				return mcp.NewToolResultError("refused: level=boss sets is_boss, never settable via MCP, not even by the principal — use the dashboard (POST /api/admin/config-level)"), nil
			}
			if level == "auto" && !isActiveBossDispatch(gate, ctx) {
				return mcp.NewToolResultError("refused: liberating to level=auto requires the CURRENT dispatch to be boss-level — a caution/danger dispatch cannot grant this to itself"), nil
			}
			if err := d.Store.SetConfigLevel(jid, level); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText("config_level set to " + level), nil
		})

	// ── approve_draft ────────────────────────────────────────────────────
	// S12 (ct-2026-07-30-1622): sends → liberates, so it requires
	// isActiveBossDispatch — same gate as set_confirmation_mode's "none"/
	// set_config_level's "auto". Before this, approve_draft's ONLY real
	// gate was bossOnlyTools, which the principal-terminal bypass (above)
	// skips entirely — the principal could approve and send a retained
	// draft with no boss ask at all, emptying the point of confirm-mode for
	// unknown numbers (an unknown contact just has to talk the agent into
	// approving the reply that was addressed to it). The boss's own wanted
	// feature ("aprobá los pendientes") still works unchanged: that request
	// arrives as a boss-level dispatch, so isActiveBossDispatch is true.
	//
	// Aprobador P1 (ct-2026-07-31-0610): widened to isActiveApproverDispatch
	// (boss OR approver) — this is the ONE thing an approver pin grants.
	s.AddTool(mcp.NewTool("approve_draft",
		mcp.WithDescription("Approve a pending draft, moving it to the outbox to actually send. Optionally override its text first. Requires the CURRENT dispatch to be boss- or approver-level (e.g. the boss saying 'aprobá los pendientes', or a chat pinned as approver) — a plain caution/danger dispatch can never approve its own held draft."),
		mcp.WithNumber("id", mcp.Required()),
		mcp.WithString("text_override", mcp.Description("Optional: replace the draft's text before sending"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			if !isActiveApproverDispatch(gate, ctx) {
				return mcp.NewToolResultError("refused: approving a draft requires the CURRENT dispatch to be boss- or approver-level — a plain caution/danger dispatch cannot approve its own held draft"), nil
			}
			id, err := r.RequireInt("id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			textOverride := r.GetString("text_override", "")
			now := time.Now().Unix()
			chatJID, burstMaxTS, sender, ok, err := d.Store.ApproveDraft(int64(id), textOverride, now)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if !ok {
				return mcp.NewToolResultError("draft not found or already resolved"), nil
			}
			// ct-2026-07-13-2243: mark only the burst that was dispatched.
			// burstMaxTS==0 means pre-ct-2243 draft → fall back to now.
			markTS := burstMaxTS
			if markTS == 0 {
				markTS = now
			}
			// T108 (ct-2026-09-01-1413): scoped to the draft's own sender in a
			// group — a group chat is born confirmation_mode="always" (the
			// NORMAL path there), so leaving this whole-chat would reopen the
			// exact defect this contract exists to fix, just one hop later.
			// sender is "" for a 1:1 draft or one created before this field
			// existed — markHandledForDispatch degrades to the pre-T108
			// whole-chat close in both cases, never breaks.
			if err := markHandledForDispatch(d, chatJID, sender, markTS); err != nil {
				log.Printf("approve_draft: mark_handled_before %s: %v", chatJID, err)
			}
			refreshQueue(d.State, d.Store)
			publishDraftChanged(d.Bus)
			return mcp.NewToolResultText("approved, moved to outbox"), nil
		})

	// ── discard_draft ────────────────────────────────────────────────────
	// S12 (ct-2026-07-30-1622): never sends → restricts, same reasoning as
	// "always"/"discretion"/"confirm"/"unattended"/"ignored" — always
	// allowed, no dispatch level required. If a bad actor talks the agent
	// into this, it costs one reply that doesn't go out; annoying, not
	// dangerous — the asymmetry with approve_draft is the whole point.
	s.AddTool(mcp.NewTool("discard_draft",
		mcp.WithDescription("Discard a pending draft — it will never be sent. Always allowed, at any dispatch level: discarding only prevents a send, it never causes one."),
		mcp.WithNumber("id", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			id, err := r.RequireInt("id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			ok, err := d.Store.DiscardDraft(int64(id))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if !ok {
				return mcp.NewToolResultError("draft not found or already resolved"), nil
			}
			publishDraftChanged(d.Bus)
			return mcp.NewToolResultText("discarded"), nil
		})

	// ── reject_draft ─────────────────────────────────────────────────────
	// T15 (ct-2026-08-05-123241): unlike discard_draft (final, never sent
	// again), rejecting asks for ANOTHER attempt — reason travels back to
	// the agent attached to the redispatch (capipush.dispatchPayload), up to
	// store.MaxDraftRounds rounds. Never sends → restricts, same reasoning
	// as discard_draft: always allowed, no dispatch level required.
	s.AddTool(mcp.NewTool("reject_draft",
		mcp.WithDescription("Reject a pending draft with a reason and ask for another attempt — unlike discard_draft, this redispatches the agent with the reason attached, up to 3 rounds total for the same chat. Always allowed, at any dispatch level: rejecting only prevents a send, it never causes one."),
		mcp.WithNumber("id", mcp.Required()),
		mcp.WithString("reason", mcp.Required(), mcp.Description("Why this draft is being rejected — travels back to the agent with the redispatch, verbatim"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			id, err := r.RequireInt("id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			reason, err := r.RequireString("reason")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			chatJID, burstMaxTS, round, ok, err := d.Store.RejectDraft(int64(id), reason)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if !ok {
				return mcp.NewToolResultError("draft not found or already resolved"), nil
			}
			publishDraftChanged(d.Bus)
			if round >= store.MaxDraftRounds {
				return mcp.NewToolResultText(fmt.Sprintf("rejected — round %d reached the %d-round cap, no automatic redispatch; edit_draft or discard_draft to resolve it directly", round, store.MaxDraftRounds)), nil
			}
			if err := d.Store.MarkPendingBefore(chatJID, burstMaxTS); err != nil {
				log.Printf("reject_draft: mark_pending_before %s: %v", chatJID, err)
			}
			refreshQueue(d.State, d.Store)
			return mcp.NewToolResultText("rejected — redispatched for another attempt"), nil
		})

	// ── edit_draft ───────────────────────────────────────────────────────
	// T15 (ct-2026-08-05-123241): replaces text in place, no status change —
	// still pending, still needs approve_draft. Never sends → restricts,
	// same reasoning as discard_draft: always allowed, no dispatch level
	// required.
	s.AddTool(mcp.NewTool("edit_draft",
		mcp.WithDescription("Replace a pending draft's text in place, without approving or sending it — it still waits for approve_draft afterward. Always allowed, at any dispatch level."),
		mcp.WithNumber("id", mcp.Required()),
		mcp.WithString("text", mcp.Required(), mcp.Description("New text for the draft"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			id, err := r.RequireInt("id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			text, err := r.RequireString("text")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			ok, err := d.Store.EditDraft(int64(id), text)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if !ok {
				return mcp.NewToolResultError("draft not found or not pending"), nil
			}
			publishDraftChanged(d.Bus)
			return mcp.NewToolResultText("edited"), nil
		})

	// ── set_is_approver ──────────────────────────────────────────────────
	// Aprobador P1 (ct-2026-07-31-0610): UNLIKE set_is_boss (MCP-BLOCKED,
	// unconditionally, above), the boss explicitly wants this pin reachable
	// via MCP too — verbatim: "la ia tambien puede cambiar el pin por mcp
	// pero solo si el boss lo manda". Gated by isActiveBossDispatch, same
	// lock as set_confirmation_mode's "none"/set_config_level's "auto"/
	// approve_draft's boss half: only a CURRENTLY boss-level dispatch can
	// grant or revoke the pin — an approver dispatch can never touch it,
	// not even its own (no self-escalation). Same self-gated pattern as the
	// tools above: the check lives HERE, not in bossOnlyTools, because it's
	// per-argument-value logic (well, per-tool here, but the same reasoning
	// as S10's split — restricting is never the question, only the effect
	// of granting is), not a binary boss-or-nothing refusal.
	s.AddTool(mcp.NewTool("set_is_approver",
		mcp.WithDescription("Grant or revoke the approver pin on a chat — lets it approve/discard drafts, including OTHER chats' drafts, without being the owner. Requires the CURRENT dispatch to be boss-level (e.g. the boss saying 'hacé aprobador a este número') — an approver dispatch can never change the pin, not even its own."),
		mcp.WithString("chat_id", mcp.Required()),
		mcp.WithBoolean("is_approver", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			if !isActiveBossDispatch(gate, ctx) {
				return mcp.NewToolResultError("refused: changing the approver pin requires the CURRENT dispatch to be boss-level — an approver dispatch cannot grant or revoke this, not even for itself"), nil
			}
			jid, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			approver, err := r.RequireBool("is_approver")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := d.Store.SetIsApprover(jid, approver); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("is_approver set to %v", approver)), nil
		})

	// ── set_kill_switch ──────────────────────────────────────────────────
	// H2+H3 hardening (ct-2026-07-10-0540): governor.SetKill and
	// state.SetMuted existed with no production caller — this is that
	// caller, and it flips BOTH together (they were two divergent flags
	// before this: the governor gate outbound sends, state.Muted only ever
	// mirrored mood for display). true halts corepipeline.processOutbox
	// (checks gov.Killed() already) AND now also state's Muted (added to
	// processOutbox in this same hardening pass) — the drain itself stops,
	// not just new sends being blocked from enqueuing.
	//
	// T19 (ct-2026-08-05-1249): also persists to store.SettingKillSwitch,
	// BEFORE the live governor/state calls — same reasoning as the REST
	// twin (restapi/admin.go's handleSetKillSwitch): main.go's
	// restoreKillSwitch reads this back on the next boot, before the
	// pipeline can send anything, so a restart never silently drops a
	// brake that was set for a real reason. Persisted best-effort (logged,
	// never blocks the emergency stop itself on a DB hiccup).
	s.AddTool(mcp.NewTool("set_kill_switch",
		mcp.WithDescription("OWNER-ONLY. Anti-ban emergency stop. true halts ALL outbound WhatsApp sends immediately (the outbox drain itself stops, nothing queues up to send later once un-killed silently) — use if the account looks at risk of a ban. false resumes normal operation. Survives a gateway restart."),
		mcp.WithBoolean("kill", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			kill, err := r.RequireBool("kill")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if d.Store != nil {
				if err := d.Store.SetSettingBool(store.SettingKillSwitch, kill); err != nil {
					log.Printf("mcpserver: persist kill switch: %v", err)
				}
			}
			if d.Governor != nil {
				d.Governor.SetKill(kill)
			}
			if d.State != nil {
				if err := d.State.SetMuted(kill); err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
			}
			return mcp.NewToolResultText(fmt.Sprintf("kill switch set to %v", kill)), nil
		})

	// ── set_capi_connector ───────────────────────────────────────────────
	// Re-cablea la antena cAPI en 1 paso (ct-2026-07-18-1638, verbatim boss:
	// "un solo comando pequeñito, que lo haga automáticamente"). Reusa
	// Store.SetPrincipalAgent — el mismo write path que POST
	// /api/admin/agent-update usa para el principal (ct-2026-07-29, agentes
	// paso 3) — sin duplicar la lógica de cableo ni la validación del
	// endpoint (SetPrincipalAgent → isAllowedPrincipalEndpoint es el ÚNICO
	// lugar que decide qué se acepta — S6, ct-2026-07-30-031048: ANTES esta
	// tool forzaba http://127.0.0.1:<puerto> y descartaba la IP del string
	// antes de que esa validación la viera, anulando el fix que la habilita
	// a aceptar rangos privados — bloqueaba justo el caso Raspberry Pi
	// (gateway en la Pi, agente en otra máquina de la LAN) que ese fix vino
	// a habilitar. Ahora el endpoint se arma con la IP tal cual vino en el
	// string, y SetPrincipalAgent decide si se acepta).
	//
	// connector_string pasa a OPCIONAL (agentes paso 3) — antes esta era la
	// ÚNICA forma de tocar al principal por MCP y exigía repegar
	// credenciales completas solo para cambiarle el nombre. name (nuevo)
	// también opcional; se necesita al menos uno de los dos. Omitir un
	// campo == dejarlo como está (mismo contrato que set_agent_capi ya usa
	// para los secundarios) — se lee el estado actual vía
	// Store.PrincipalAgent antes de aplicar el override.
	s.AddTool(mcp.NewTool("set_capi_connector",
		mcp.WithDescription("Re-cablea la antena cAPI del agente principal y/o le cambia el nombre. connector_string: pegá el string tal cual lo imprime capi_credentials ('<ip:puerto> chat_id:<uuid> pin:<base64>') — se parsea, y el endpoint (con la IP tal cual vino) se valida contra el mismo criterio que /api/admin/agent-update: loopback o rango de red privada (RFC1918/RFC4193, link-local) se acepta — incluida la LAN de una Raspberry Pi —, cualquier dirección pública se rechaza. Si pasa, aplica el cambio en caliente + lo persiste. name: nombre de display del principal. Al menos uno de los dos es requerido; el que se omite queda como estaba."),
		mcp.WithString("connector_string", mcp.Description("String pegado tal cual, ej: '192.168.1.83:8787 chat_id:57582399-1400-485c-ab6a-22febe672344 pin:3y+X4bmS0Yau91l/6cJAjw=='. Omitir para no tocar endpoint/terminal/pin.")),
		mcp.WithString("name", mcp.Description("Nuevo nombre de display del principal. Omitir para no tocar el nombre actual."))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			if d.PrincipalTerminalID == "" {
				return mcp.NewToolResultError("no principal configured (PIUMY_DEFAULT_TERMINAL_ID unset)"), nil
			}
			raw := r.GetString("connector_string", "")
			name := r.GetString("name", "")
			if raw == "" && name == "" {
				return mcp.NewToolResultError("at least one of connector_string or name is required"), nil
			}

			current, _, err := d.Store.PrincipalAgent(d.PrincipalTerminalID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("store: %v", err)), nil
			}
			endpoint, terminalID, pinpass := current.Endpoint, current.AntennaTerminalID, current.Pinpass
			if raw != "" {
				ip, port, tid, pp, err := capiconn.ParseConnectorString(raw)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				endpoint, terminalID, pinpass = "http://"+ip+":"+port, tid, pp
			}
			if name == "" {
				name = current.Name
			}

			if err := d.Store.SetPrincipalAgent(name, endpoint, terminalID, pinpass); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if d.Connector != nil {
				d.Connector.SetConfig(endpoint, terminalID, pinpass)
			}
			return mcp.NewToolResultText(fmt.Sprintf("cAPI connector actualizado — endpoint: %s, terminal_id: %s, name: %s", endpoint, terminalID, name)), nil
		})
}
