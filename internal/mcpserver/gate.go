// The gate state machine — the hard gate CLAUDE.md #4 requires in code,
// not in a skill/prompt: locked -> get_instructions(nonce) -> unlock(token)
// -> noting -> remember|skip -> ready -> send_message consumes it.
//
// State is tracked per dispatch (per nonce), never a global flag, so two
// dispatches never step on each other. Correlating later calls
// (unlock/remember/skip/send_message, none of which carry the nonce) to the
// right dispatch is by terminal_id (F4b — not MCP session ID as F4a had it:
// capipush registers a dispatch knowing only the destination terminal_id,
// never an MCP session, which doesn't exist yet at registration time).
// get_instructions(nonce) both binds the calling terminal to that dispatch
// AND validates the dispatch was actually registered FOR that terminal —
// this is what makes it impossible for terminal B to consume a dispatch
// meant for terminal A, even with the right nonce. send_message adds its
// own belt-and-suspenders chat-match check on top (see server.go).
package mcpserver

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"piumy-gateway/internal/store"
)

// Dispatch levels — who this dispatch is talking to, from the router +
// is_boss + chat state (computed by capipush, F4b; passed in verbatim here).
//
// LevelApprover (Aprobador P1, ct-2026-07-31-0610): a chat marked
// is_approver but not is_boss — trusted for exactly one extra thing over a
// normal dispatch (approving/discarding drafts, including other chats'),
// nothing else. Sits between Boss and Caution in privilege, but does NOT
// share Boss's blanket bypass in levelGateMiddleware — it's a single-purpose
// grant, not "boss but smaller". Born gateLocked like Caution/Danger (only
// Boss is born gateReady below): an approver still reads rules/memory/
// context through the normal ritual before it can act.
const (
	LevelBoss     = "boss"
	LevelApprover = "approver"
	LevelCaution  = "caution"
	LevelDanger   = "danger"
)

const (
	gateLocked = "locked"
	gateNoting = "noting"
	gateReady  = "ready"
	gateDone   = "done" // consumed by a successful send — one-shot
)

// dispatchStaleAfter bounds how long a dispatch survives with no activity
// before opportunistic cleanup reclaims it — same idea as mcpguard's
// clientStaleAfter. Covers two cases (H5 hardening, ct-2026-07-10-0540):
// never pulled via get_instructions (the original case), AND bound but
// stuck (an agent that crashed after get_instructions, or a failed
// Encrypt/Inject in capipush before this fix, leaving InFlight(terminalID)
// == true forever with zero chance of ever progressing).
//
// S4b (ct-2026-07-30-1255, defect 4): 1h → 15m. An hour was "last-resort
// net so it never happens", not "recovers fast" — for messaging, that's
// too long: while ANY dispatch sits stuck on a terminal, InFlight blocks
// EVERY chat routed there, not just the stuck one. 15m is still well past
// the longest Fibonacci redispatch gap (13m, capipush.redispatchBackoff) —
// a legitimately slow agent mid-ritual doesn't get yanked, but a genuinely
// crashed one recovers in minutes, not most of an hour. This is only the
// value before any SetStaleAfter call (main.go/capipush's own sweep both
// apply the real, live-settings-driven value almost immediately).
const dispatchStaleAfter = 15 * time.Minute

// gateSweepInterval is how often Sweep's own ticker reclaims stale
// dispatches — independent of RegisterDispatch being called (see Sweep's
// doc for why that independence is the actual H5 fix).
const gateSweepInterval = 5 * time.Minute

// dispatch tracks one in-flight cAPI dispatch through the gate.
type dispatch struct {
	nonce      string
	chatJID    string
	level      string
	terminalID string
	state      string
	token      string
	// lastActivity stamps every transition (registration, get_instructions,
	// unlock, remember/skip) — sweepLocked reclaims a dispatch idle longer
	// than dispatchStaleAfter regardless of whether it was ever pulled.
	lastActivity time.Time
	boundToTerm  bool  // true once get_instructions has bound this dispatch
	burstMaxTS   int64 // TS of the last message in the dispatched burst (ct-2026-07-13-2243)
	// sender (T108, ct-2026-09-01-1413): the canonical group participant this
	// dispatch is FOR — "" for a 1:1 chat (a single sender needs no
	// scoping) or a group whose burst somehow arrived without one (never
	// happens via capipush.dueChats, but never assumed). Lets send.go/
	// silent_act close only this speaker's messages via
	// MarkHandledBeforeForSender instead of the whole chat's.
	sender string
	// antennaAlias (T129, ct-2026-09-03-0200): the second byTerminal key
	// this dispatch is ALSO reachable under — the registered agent's
	// antenna_terminal_id, when it differs from terminalID and resolves
	// unambiguously (see registerAntennaAliasLocked). "" when there is no
	// alias (no agentStore wired, agent unknown, antenna==terminalID
	// already, or the antenna is ambiguous between agents). Recorded here,
	// not just written into byTerminal, so evictLocked can clean up BOTH
	// keys — an alias nobody removes on eviction is a dangling entry that
	// would resolve a future terminal to a dead dispatch forever.
	antennaAlias string
	// sends (T167, ct-2026-09-17-1255, boss verbatim: "quiero que lo quiten
	// o aumenten a 4 mensajes") counts how many send_message/draft calls
	// RecordSend has recorded for this dispatch — send.go's validateSend
	// checks it (via ActiveDispatch.SendCount) to let up to
	// maxSendsPerDispatch calls through for the dispatch's OWN chat even
	// after Consume has already marked it done, so one reply can land as
	// several pieces (text + sticker) without a fresh dispatch. Deliberately
	// separate from the state machine below: Consume still retires the
	// dispatch (state -> gateDone, InFlight -> false) on the very FIRST
	// call, exactly as before this contract — the cap lives entirely in
	// send.go's policy check, never in the lock/ready/done transitions
	// here. silent_act never calls RecordSend, so it never sees this cap;
	// its own one-shot behavior (send.go) is untouched.
	sends int
}

// Gate is the per-dispatch lock/unlock/noting/ready state machine. Safe for
// concurrent use. The zero value is not usable — build one with NewGate.
type Gate struct {
	mu         sync.Mutex
	byNonce    map[string]*dispatch
	byTerminal map[string]*dispatch
	staleAfter time.Duration
	// startedAt (T87, ct-2026-08-28) stamps construction — the ONLY signal
	// this Gate has for telling a genuinely fake nonce apart from a real one
	// its restart just forgot. See recentlyStartedLocked's doc for why
	// this, and not a persisted nonce ledger.
	startedAt time.Time
	// agentStore (T129, ct-2026-09-03-0200): optional, wired once via
	// SetAgentStore (server.go's New, so both main.go's production Gate and
	// every test's serverWithGate get it for free). nil is a valid,
	// deliberately-supported state — RegisterDispatch just skips antenna
	// resolution, the exact behavior this Gate always had before T129.
	agentStore *store.Store
	// principalTerminalID (T133, ct-2026-09-03-0634): wired once via
	// SetPrincipalTerminalID, same call site as SetAgentStore. Needed
	// because the principal has no row in the `agents` table (PrincipalAgent
	// synthesizes it from KV) — registerAntennaAliasLocked can't find it via
	// agentStore.GetAgent like it does for a secondary, so it has to know
	// WHICH terminalID is the principal's own before it can look up ITS
	// antenna a different way. "" is a valid, deliberately-supported state
	// (no principal configured) — same graceful no-op convention as
	// agentStore==nil.
	principalTerminalID string
	// retiredReason remembers WHY a nonce left byNonce, keyed by the nonce
	// itself (T147, ct-2026-09-07): a get_instructions miss used to GUESS at
	// the cause ("probable force-replace") — a guess that was wrong twice in
	// T147's own investigation and cost real diagnostic time. Every path
	// that removes a nonce from byNonce (Consume, RegisterDispatch's
	// force-replace, sweepLocked's stale reclaim) records why here FIRST, so
	// a later lookup can say what actually happened instead of guessing.
	// Naturally bounded: nonces are 4 hex chars (randomHex(2)), so this can
	// never hold more than 65536 entries regardless of uptime — a later
	// reuse of the same nonce value just overwrites its own prior reason.
	retiredReason map[string]string
}

func NewGate() *Gate {
	return &Gate{byNonce: map[string]*dispatch{}, byTerminal: map[string]*dispatch{}, staleAfter: dispatchStaleAfter, startedAt: time.Now(), retiredReason: map[string]string{}}
}

// retireLocked records why nonce left byNonce — called by every path that
// removes an entry (Consume, RegisterDispatch's force-replace, sweepLocked),
// BEFORE the delete, so a later get_instructions miss can report the real
// cause (T147) instead of guessing. Caller must hold g.mu.
func (g *Gate) retireLocked(nonce, reason string) {
	g.retiredReason[nonce] = reason
}

// SetAgentStore wires the agents table lookup RegisterDispatch uses to
// resolve a terminal by antenna_terminal_id as well as agent_id (T129,
// ct-2026-09-03-0200). Optional — never called means antenna resolution is
// simply off, same as before this contract.
func (g *Gate) SetAgentStore(st *store.Store) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.agentStore = st
}

// SetPrincipalTerminalID wires the principal's own configured routing id
// (T133, ct-2026-09-03-0634) — the identity registerAntennaAliasLocked
// treats specially, since the principal has no `agents` row to look up via
// GetAgent. "" (no principal configured) means that branch never engages.
func (g *Gate) SetPrincipalTerminalID(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.principalTerminalID = id
}

// SetStaleAfter overrides the reclaim window sweepLocked uses (default
// dispatchStaleAfter, 1h) — post-incident hardening (ct-2026-07-11): a
// dispatch stuck mid-ritual (agent crashed after get_instructions) silently
// blocks new dispatches to that terminal for the full window with nothing
// logged. Wired from config (PIUMY_GATE_STALE_AFTER) so the window can be
// tuned without a rebuild. d<=0 is ignored (keeps the current value).
func (g *Gate) SetStaleAfter(d time.Duration) {
	if d <= 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.staleAfter = d
}

// RegisterDispatch records a dispatch about to be pushed to terminalID —
// called by capipush (F4b) or, in tests, a synthetic stand-in. Not an MCP
// tool: the terminal's agent never calls this itself.
//
// SECURITY (found in F4c audit): immediately binds byTerminal[terminalID]
// to the NEW dispatch, replacing whatever was there — a residual
// higher-privilege binding must never survive a new dispatch's
// registration. Before this fix, RegisterDispatch only touched byNonce;
// byTerminal stayed pointed at the terminal's PREVIOUS dispatch until the
// agent itself called GetInstructions for the new one. A terminal bound to
// a done-but-still-boss dispatch would keep answering Active() as boss even
// after a new, lower-trust (caution/danger) dispatch was registered for it,
// so an agent that simply skipped get_instructions for the new dispatch
// kept operating with the OLD level's privileges — the same fail-open shape
// as F4a, now in the level TRANSITION rather than the initial state. Forcing
// the new dispatch (locked, not ready) into byTerminal here means Active()
// reflects the new level immediately, and every gated tool + send_message
// falls back to DENY/gated-by-level until the agent properly re-gates via
// get_instructions -> unlock -> remember|skip for the new dispatch.
func (g *Gate) RegisterDispatch(nonce, chatJID, level, terminalID string, burstMaxTS int64, sender string) error {
	switch level {
	case LevelBoss, LevelApprover, LevelCaution, LevelDanger:
	default:
		return fmt.Errorf("gate: invalid level %q", level)
	}
	if terminalID == "" {
		return fmt.Errorf("gate: terminalID is required")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sweepLocked(time.Now())
	// ST-A security fix (ct-2026-07-11-0740): boss dispatches start straight
	// in gateReady instead of gateLocked — boss was always meant to skip the
	// unlock/remember/skip checkpoint (F4-DESIGN §3), but starting it
	// gateLocked like everything else meant Ready never reflected boss's
	// real state at all, which is exactly what let validateSend and
	// levelGateMiddleware key off Level==Boss alone (see their own doc
	// comments) — a done (consumed) dispatch kept granting privileges
	// forever because nothing ever looked at Ready for boss. Starting ready
	// makes Ready mean "usable" uniformly for every level: true from
	// registration for boss, true only after the checkpoint for
	// caution/danger, and false for everyone once Consume marks it done.
	initialState := gateLocked
	if level == LevelBoss {
		initialState = gateReady
	}
	d := &dispatch{nonce: nonce, chatJID: chatJID, level: level, terminalID: terminalID, state: initialState, lastActivity: time.Now(), burstMaxTS: burstMaxTS, sender: sender}
	if prev := g.byTerminal[terminalID]; prev != nil && prev.nonce != nonce {
		g.retireLocked(prev.nonce, fmt.Sprintf("replaced — a newer dispatch (chat=%s) was registered for terminal=%s before this one was ever consumed; wait for a fresh nonce, this one is gone for good", chatJID, terminalID))
		delete(g.byNonce, prev.nonce)
		// No residual antenna alias either — same "no leftover binding
		// survives a new dispatch's registration" guarantee this function's
		// own doc comment already makes for the terminalID key.
		if prev.antennaAlias != "" && g.byTerminal[prev.antennaAlias] == prev {
			delete(g.byTerminal, prev.antennaAlias)
		}
	}
	// T147 (ct-2026-09-07, Citrino's own audit): a stale retiredReason for
	// THIS nonce value must never survive a fresh registration under it —
	// nonces are 4 hex chars (65536 values), real traffic reuses them
	// (~50% collision odds by ~300 dispatches), and retiredReason is never
	// pruned on its own. Every CURRENT retirement path (Consume,
	// force-replace above, sweepLocked, CancelDispatch) already records its
	// own reason on eviction — but this line is the structural backstop,
	// not a patch for a known-uncovered path today: it guarantees a nonce's
	// SECOND life can never inherit its first life's reason no matter which
	// path (including one added later and never wired into retireLocked)
	// eventually retires it. The alternative — trusting every future
	// retirement path to remember this — is exactly the kind of guess this
	// whole contract exists to stop making.
	delete(g.retiredReason, nonce)
	g.byNonce[nonce] = d
	g.byTerminal[terminalID] = d
	g.registerAntennaAliasLocked(d)
	return nil
}

// registerAntennaAliasLocked (T129, ct-2026-09-03-0200 — the bug that left
// an agent mute: its agent_id and antenna_terminal_id no longer matched
// after a rename, so the terminal it actually presented never found the
// dispatch registered under its agent_id) ALSO indexes d under its agent's
// antenna_terminal_id, when that differs from terminalID — a terminal
// authenticating with either its (renameable) agent_id or its (stable,
// gateway-computed) antenna now finds the same dispatch. Reuses the exact
// relationship get_status's identity.go already computes to WARN about
// cross-wiring — using what the gateway already knows to resolve, instead
// of only to complain, isn't a new capability.
//
// Two agents sharing one antenna_terminal_id makes the alias AMBIGUOUS:
// which one would byTerminal[antenna] mean? Never guessed — skipped (d
// stays reachable by its own agent_id, exactly as before this fix existed)
// and logged, since an alias that can't be resolved is exactly the kind of
// silent dead end this contract exists to end. No-op if agentStore was
// never wired. Caller must hold g.mu.
func (g *Gate) registerAntennaAliasLocked(d *dispatch) {
	if g.agentStore == nil {
		return
	}
	// T133 (ct-2026-09-03-0634): the principal has no `agents` row —
	// PrincipalAgent synthesizes it from KV — so GetAgent below would never
	// find it. Its own branch, never touching the secondary path.
	if g.principalTerminalID != "" && d.terminalID == g.principalTerminalID {
		g.registerPrincipalAntennaAliasLocked(d)
		return
	}
	agent, ok, err := g.agentStore.GetAgent(d.terminalID)
	if err != nil || !ok || agent.AntennaTerminalID == "" || agent.AntennaTerminalID == d.terminalID {
		return
	}
	matches, err := g.agentStore.AgentsByAntenna(agent.AntennaTerminalID)
	if err != nil {
		return
	}
	if len(matches) != 1 {
		log.Printf("mcpserver: gate: dispatch nonce=%s terminal=%s: su antenna_terminal_id=%q es AMBIGUA (%d agentes la comparten) — no se puede resolver por esa vía, el despacho sigue registrado SOLO bajo agent_id", d.nonce, d.terminalID, agent.AntennaTerminalID, len(matches))
		return
	}
	d.antennaAlias = agent.AntennaTerminalID
	g.byTerminal[d.antennaAlias] = d
}

// registerPrincipalAntennaAliasLocked is T133's own branch (ct-2026-09-03-
// 0634): the principal's PrincipalTerminalID (routing — env
// PIUMY_DEFAULT_TERMINAL_ID) and its real antenna_terminal_id (KV, written
// by set_capi_connector/the dashboard) are two INDEPENDENTLY-set values —
// "el principal funciona por casualidad: sus dos campos coinciden porque
// nadie los tocó" is a coincidence, not a guarantee, and this is what makes
// it one: if they ever diverge (the exact T129 shape, but for the fallback
// every unassigned chat routes to), the terminal presenting the real
// antenna now still resolves.
//
// Same ambiguity rule as the secondary path above, mirrored the other
// direction: if any SECONDARY agent happens to share the principal's own
// antenna, that's a real misconfiguration, and the alias is skipped rather
// than guessed — AgentsByAntenna only ever looks at secondaries (see its
// own doc), so "any match at all" here means a genuine collision, not the
// principal finding itself. Caller must hold g.mu (called only from
// registerAntennaAliasLocked, which already does).
func (g *Gate) registerPrincipalAntennaAliasLocked(d *dispatch) {
	principal, ok, err := g.agentStore.PrincipalAgent(g.principalTerminalID)
	if err != nil || !ok || principal.AntennaTerminalID == "" || principal.AntennaTerminalID == d.terminalID {
		return
	}
	matches, err := g.agentStore.AgentsByAntenna(principal.AntennaTerminalID)
	if err != nil {
		return
	}
	if len(matches) != 0 {
		log.Printf("mcpserver: gate: dispatch nonce=%s terminal=%s (principal): su antenna_terminal_id=%q coincide con la de %d agente(s) secundario(s) — AMBIGUA, no se puede resolver por esa vía, el despacho sigue registrado SOLO bajo el terminal_id del principal", d.nonce, d.terminalID, principal.AntennaTerminalID, len(matches))
		return
	}
	d.antennaAlias = principal.AntennaTerminalID
	g.byTerminal[d.antennaAlias] = d
}

// CancelDispatch undoes a RegisterDispatch that never actually reached the
// terminal — H5 hardening (ct-2026-07-10-0540): if capipush's Inject fails
// AFTER RegisterDispatch already succeeded, the dispatch would otherwise
// sit registered with zero chance of ever being pulled (the agent never
// received it) — InFlight(terminalID) reports true for no reason until
// Sweep's dispatchStaleAfter eventually reclaims it. Calling this
// immediately frees the terminal instead. No-op if nonce/terminalID no
// longer match what's currently registered (e.g. a newer dispatch already
// replaced it) — never cancels the WRONG dispatch.
func (g *Gate) CancelDispatch(nonce, terminalID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	d, ok := g.byNonce[nonce]
	if !ok || d.terminalID != terminalID {
		return
	}
	// T147 (ct-2026-09-07): the ONE production caller of this (capipush.go,
	// Inject failing) never told anyone why — a later get_instructions on
	// this exact nonce fell into the plain "never registered" bucket,
	// wrong (it WAS, briefly). Retire it honestly instead.
	g.retireLocked(d.nonce, "cancelled — delivery to the terminal failed before the agent ever saw it; wait for the automatic retry")
	g.evictLocked(d)
}

// evictLocked removes d from both indices — guarding byTerminal against a
// newer dispatch that may have already replaced it there (RegisterDispatch's
// own force-replace, see its doc comment) is what keeps CancelDispatch and
// sweepLocked from ever evicting the WRONG (current) dispatch for a
// terminal. Caller must hold g.mu.
func (g *Gate) evictLocked(d *dispatch) {
	delete(g.byNonce, d.nonce)
	if g.byTerminal[d.terminalID] == d {
		delete(g.byTerminal, d.terminalID)
	}
	// T129: d may ALSO be indexed under its agent's antenna_terminal_id
	// (registerAntennaAliasLocked) — leaving that key pointing at an
	// evicted dispatch would resolve a future terminal to a dead one.
	if d.antennaAlias != "" && g.byTerminal[d.antennaAlias] == d {
		delete(g.byTerminal, d.antennaAlias)
	}
}

// Sweep runs sweepLocked on its own ticker until ctx is cancelled — the
// independence from RegisterDispatch is the actual H5 fix (ct-2026-07-10-
// 0540): before this, the stale sweep only ran inline inside
// RegisterDispatch, but capipush.dispatch skips calling RegisterDispatch
// entirely for a terminal that's already InFlight — exactly the wedged
// case that needs reclaiming. A terminal stuck InFlight forever meant
// RegisterDispatch (and so the sweep) never ran again for it either. A
// ticker-driven sweep reclaims it regardless of what any terminal is doing.
func (g *Gate) Sweep(ctx context.Context) {
	ticker := time.NewTicker(gateSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.mu.Lock()
			g.sweepLocked(time.Now())
			g.mu.Unlock()
		}
	}
}

// InFlight reports whether terminalID currently has a dispatch that's
// bound and not yet done (locked/noting/ready) — capipush (F4b) checks
// this before registering a new dispatch, so a burst of inbound messages
// doesn't force-regate a terminal mid-flow on legitimate, still-in-
// progress work (an efficiency/UX refinement; RegisterDispatch's
// force-replace above is the actual security guarantee and applies
// regardless of what InFlight says — capipush choosing to call
// RegisterDispatch anyway while in-flight is still safe, just noisier).
func (g *Gate) InFlight(terminalID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	d := g.byTerminal[terminalID]
	return d != nil && d.state != gateDone
}

// NonceActive reports whether nonce is currently registered to a dispatch
// (ct-2026-07-18-1851-B) — capipush checks this before committing to a short
// (4-hex) nonce, regenerating on a collision instead of registering over an
// unrelated in-flight dispatch.
func (g *Gate) NonceActive(nonce string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.byNonce[nonce]
	return ok
}

// sweepLocked drops dispatches idle for more than dispatchStaleAfter —
// never pulled via get_instructions, OR bound but stuck (H5 hardening,
// ct-2026-07-10-0540 — see dispatchStaleAfter's doc). A gateDone dispatch
// is never in byNonce (Consume deletes it there immediately), so this
// never needs to special-case "already finished". Caller must hold g.mu.
//
// S2 (ct-2026-07-30-030928, criterio de listo #3): this eviction IS the
// "se libera solo, sin reiniciar el gateway" path — it just ran silently
// before S1 gave capipush a log channel. gateSweepInterval is 5 MINUTES
// (not capipush's 5s), so logging every reclaim here is an event, not
// per-tick noise — no transition tracking needed.
func (g *Gate) sweepLocked(now time.Time) {
	for _, d := range g.byNonce {
		if now.Sub(d.lastActivity) > g.staleAfter {
			log.Printf("mcpserver: gate: dispatch nonce=%s chat=%s terminal=%s nivel=%s liberado por timeout (idle %s > %s) — el terminal queda libre sin reiniciar el gateway", d.nonce, d.chatJID, d.terminalID, d.level, now.Sub(d.lastActivity).Round(time.Second), g.staleAfter)
			g.retireLocked(d.nonce, fmt.Sprintf("expired — idle more than %s, reclaimed by the stale-dispatch sweep; wait for a fresh dispatch", g.staleAfter))
			g.evictLocked(d)
		}
	}
}

// gateNoDispatchExplain (T87, ct-2026-08-28) is the honest half of every
// "no dispatch bound" message — what an agent should DO, not just that it
// was refused. Boss verbatim, on hitting "refused ... (default DENY)" after
// a real restart mid-ritual: "y con lo de media que no pudiste ver la
// foto" — read literally as a permissions problem when the truth was a
// technical one. The message the burst came from was NEVER marked
// attended, so capipush's normal PendingDedicated flow redispatches it with
// a fresh nonce on its own (unchanged, out of this contract's scope) —
// nothing for the agent to fix, just wait.
const gateNoDispatchExplain = "not denied: this dispatch is no longer tracked here — most likely the gateway restarted, or your turn expired, while you were mid-ritual. Nothing was lost: the message was never marked attended, so it will be redispatched to you with a fresh nonce shortly. Wait for it — this is not a permissions problem, don't report it as one."

// gateWrongTerminalMessage (T104, ct-2026-08-29-1818) is gateNoDispatchExplain's
// sibling for the OTHER "not really denied" case the gate's own log already
// distinguishes: the nonce exists, it's just bound to a DIFFERENT terminal.
// The old agent-facing text ("this dispatch was not registered for this
// terminal") threw that distinction away — Citrino hit it twice in
// production, read it as a permissions problem, and told the boss twice he
// wasn't the principal agent, when the real cause (visible in the log the
// whole time: "dispatch registrado para otro terminal (principal)") was a
// wiring mismatch between an agent's agent_id and its antenna_terminal_id.
// Same shape as gateNoDispatchExplain: name what actually happened, name
// the likely cause, say what to do, close the door on the wrong diagnosis.
func gateWrongTerminalMessage(registeredFor, presented string) string {
	return fmt.Sprintf("not a permissions problem: this dispatch exists, it's just registered for a different terminal (%s) — this call presented itself as %s. Most likely a wiring mismatch, not an attack: the id your client presents (often an agent's antenna_terminal_id) doesn't match the agent_id the gateway expects. Call get_status — it reports your own_terminal_id/is_principal/matched_agent_id — to check which id this terminal is actually presenting before assuming anything about permissions; retrying with this same nonce won't help.", registeredFor, presented)
}

// recentlyStartedLocked reports whether THIS Gate is young enough that a
// missing nonce/terminal binding is almost certainly a dispatch that
// existed before a restart, not a fabrication (T87, Citrino's own call —
// "casi con certeza", not persisted proof). Gate deliberately does NOT
// persist across a restart (a half-finished ritual surviving one is worse
// than losing it: the agent process may have restarted too, and the burst
// it was mid-way through could have changed) — so there is no record of
// which nonces were ever real. What the Gate CAN know is its own age.
// Within staleAfter of construction, ANY dispatch that predated a
// hypothetical restart would still be "not yet stale" had the process
// never restarted at all — so an unknown nonce in that window reads as
// case 2 far more often than case 1. Past that window the same guess is
// wrong more often than right, so every caller below reverts to the plain,
// unchanged hard reject. Caller must hold g.mu (reads staleAfter, which
// SetStaleAfter mutates live under lock — capipush's sweep calls it on
// every tick).
func (g *Gate) recentlyStartedLocked() bool {
	return time.Since(g.startedAt) < g.staleAfter
}

// logRecentlyStartedLocked is the one T87 log line ("que quede en el LOG
// una línea cuando pasa"), shared by noDispatchMessageLocked and
// GetInstructions so the wording can't drift between the two families of
// caller. Caller must hold g.mu (reads staleAfter).
func (g *Gate) logRecentlyStartedLocked(reason, terminalID string) {
	log.Printf("mcpserver: gate: %s terminal=%s: sin dispatch, Gate recién arrancado (< %s de vida) — probable reinicio a mitad de ritual, no un nonce inventado (T87)", reason, terminalID, g.staleAfter)
}

// noDispatchMessageLocked is noDispatchMessage's body. Caller must hold
// g.mu. hardReject is the site's own PRE-EXISTING wording for "this is
// really nothing" (T87 leaves case 1 byte-for-byte unchanged, "no lo
// aflojes" — each call site keeps its own established prefix/vocabulary,
// e.g. levelGateMiddleware's "refused:" vs silent_act's "locked:", rather
// than this function imposing one). reason is a short tag for the log line
// only, never shown to the agent.
func (g *Gate) noDispatchMessageLocked(terminalID, reason, hardReject string) string {
	if g.recentlyStartedLocked() {
		g.logRecentlyStartedLocked(reason, terminalID)
		return gateNoDispatchExplain
	}
	return hardReject
}

// noDispatchMessage is the agent-facing text for "nothing bound to this
// terminal" (Active()==false) — T87. For callers outside the Gate (levelgate.go,
// send.go) that don't already hold g.mu.
func (g *Gate) noDispatchMessage(terminalID, reason, hardReject string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.noDispatchMessageLocked(terminalID, reason, hardReject)
}

// Instructions is get_instructions' payload — Token last, on purpose: it
// forces the agent to have ingested rules/memory/context to reach it.
// IsBoss/IsApprover (ct-2026-08-06, boss verbatim: "si soy boss tiene que
// decir is boss") mirror the same identity the dispatch preamble already
// carries (capipush.dispatchPayload) — an agent that reconnects mid-way
// (nonce still valid, preamble long gone) looks here instead.
type Instructions struct {
	Rules      string `json:"rules"`
	Memory     string `json:"memory"`
	Context    string `json:"context"`
	IsBoss     bool   `json:"is_boss"`
	IsApprover bool   `json:"is_approver"`
	Token      string `json:"token"`
}

// GetInstructions marks terminalID's dispatch (registered under nonce) as
// pulled and returns a fresh unlock token plus rules/memory/context read
// live from the store. Rejects outright if nonce was registered for a
// DIFFERENT terminal — the core anti-hijack check (a terminal can never
// consume another terminal's dispatch, even knowing its nonce).
// RegisterDispatch already binds byTerminal[terminalID] to this dispatch at
// registration time (see its doc comment), so this only needs to issue the
// token and mark it pulled (for the staleness sweep).
func (g *Gate) GetInstructions(terminalID, nonce string, st *store.Store) (Instructions, error) {
	g.mu.Lock()
	d, ok := g.byNonce[nonce]
	if !ok {
		// T147 (ct-2026-09-07): a KNOWN reason beats every guess below — this
		// nonce left byNonce through Consume, RegisterDispatch's force-replace,
		// or the stale sweep, and each of those recorded WHY before deleting
		// it. "probable force-replace" used to be a guess, not a fact — wrong
		// twice in T147's own investigation, three rounds of diagnosis lost
		// to it. Say what's actually known, or say that nothing is.
		if reason, known := g.retiredReason[nonce]; known {
			log.Printf("mcpserver: gate: get_instructions terminal=%s nonce=%s: %s", terminalID, nonce, reason)
			g.mu.Unlock()
			return Instructions{}, fmt.Errorf("nonce %q is gone: %s", nonce, reason)
		}
		// T87: checked and logged BEFORE unlocking — recentlyStartedLocked
		// reads staleAfter, which SetStaleAfter mutates live under this
		// same lock (capipush's sweep calls it every tick); reading it
		// unlocked would be a real data race, not just a style nit.
		if g.recentlyStartedLocked() {
			g.logRecentlyStartedLocked("get_instructions", terminalID)
			g.mu.Unlock()
			return Instructions{}, fmt.Errorf("dispatch nonce %q not found: %s", nonce, gateNoDispatchExplain)
		}
		log.Printf("mcpserver: gate: get_instructions terminal=%s nonce=%s: nunca registrado — sin rastro de que este nonce haya existido jamás", terminalID, nonce)
		g.mu.Unlock()
		return Instructions{}, fmt.Errorf("unknown nonce %q — never registered (not a stale one either — there is no record of it ever existing)", nonce)
	}
	// T129 (ct-2026-09-03-0200): unlike every other method here, this check
	// doesn't go through byTerminal (it looks the dispatch up by nonce
	// first, on purpose — the anti-hijack guarantee this function's own doc
	// comment describes). d.antennaAlias is the second valid identity for
	// this SAME dispatch (registerAntennaAliasLocked), so it has to be
	// accepted here too — this exact strict-equality check, unfixed, is
	// what left the CleverCoder agent unable to even START its ritual
	// (rejected here as "wrong terminal") despite RegisterDispatch already
	// resolving the dual key everywhere else.
	if d.terminalID != terminalID && d.antennaAlias != terminalID {
		registeredFor := d.terminalID
		g.mu.Unlock()
		log.Printf("mcpserver: gate: get_instructions terminal=%s nonce=%s: dispatch registrado para otro terminal (%s) — nonce mal dirigido o intento de hijack", terminalID, nonce, registeredFor)
		return Instructions{}, fmt.Errorf("%s", gateWrongTerminalMessage(registeredFor, terminalID))
	}
	token, err := randomHex(16)
	if err != nil {
		g.mu.Unlock()
		return Instructions{}, err
	}
	d.token = token
	d.boundToTerm = true
	d.lastActivity = time.Now()
	chatJID := d.chatJID
	g.mu.Unlock()

	rules, err := st.EffectiveRules(chatJID)
	if err != nil {
		return Instructions{}, err
	}
	c, _, err := st.GetChat(chatJID)
	if err != nil {
		return Instructions{}, err
	}
	return Instructions{Rules: rules, Memory: c.Memory, Context: c.Context, IsBoss: c.IsBoss, IsApprover: c.IsApprover, Token: token}, nil
}

// Unlock validates token against terminalID's active dispatch and
// transitions locked -> noting. A token from a different terminal's
// dispatch never matches this terminal's own bound token — anti-replay
// falls out of the terminal-scoped lookup, no separate token index needed.
//
// S2 (ct-2026-07-30-030928): noting/ready are idempotent successes, not
// errors — a dispatch already past the unlock checkpoint (boss is BORN in
// gateReady, ST-A ct-2026-07-11-0740; or a caution/danger dispatch that
// already called unlock once) has nothing left to do here, and saying so
// used to collide with advanceToReady's own error for the exact same state
// (both said "not right" using different, contradictory words — the smoke's
// "already unlocked" / "not unlocked — call unlock first" deadlock). done
// stays a real, distinct error: a consumed dispatch must stay gated.
func (g *Gate) Unlock(terminalID, token string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	d := g.byTerminal[terminalID]
	if d == nil {
		return fmt.Errorf("%s", g.noDispatchMessageLocked(terminalID, "unlock", "no active dispatch — call get_instructions first"))
	}
	switch d.state {
	case gateNoting, gateReady:
		return nil
	case gateDone:
		return fmt.Errorf("dispatch already consumed — call get_instructions for a new one")
	}
	// H4 hardening (ct-2026-07-10-0540): d.token is "" until GetInstructions
	// sets it — without the explicit empty check, an agent that never calls
	// get_instructions (so d.token is still its zero value) can unlock with
	// token="" and skip ingesting rules/memory/context entirely. Rejecting
	// "" outright (not just "doesn't match") closes that regardless of
	// what d.token happens to be.
	if token == "" || d.token != token {
		return fmt.Errorf("invalid token")
	}
	d.state = gateNoting
	d.lastActivity = time.Now()
	return nil
}

// Remember applies the noting->ready checkpoint, optionally writing
// memory/context on the dispatch's own chat — never an override, even for
// boss (F4-DESIGN's "todas las memorias" for boss is about tool
// visibility/scoping elsewhere, not a remember() target override).
func (g *Gate) Remember(terminalID, memory, context string, st *store.Store) error {
	d, err := g.advanceToReady(terminalID)
	if err != nil {
		return err
	}
	if memory != "" {
		if err := st.SetChatMemory(d.chatJID, memory); err != nil {
			return err
		}
	}
	if context != "" {
		if err := st.SetChatContext(d.chatJID, context); err != nil {
			return err
		}
	}
	return nil
}

// Skip applies the noting->ready checkpoint with no writes.
func (g *Gate) Skip(terminalID string) error {
	_, err := g.advanceToReady(terminalID)
	return err
}

// S2 (ct-2026-07-30-030928): mirrors Unlock's idempotency — ready is a
// no-op success (already past this checkpoint: boss is born here, or a
// caution/danger dispatch that already called remember/skip once), done is
// a distinct real error, and locked keeps its real, honest error (never
// unlocked yet — legitimate guidance, not a contradiction).
func (g *Gate) advanceToReady(terminalID string) (*dispatch, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	d := g.byTerminal[terminalID]
	if d == nil {
		return nil, fmt.Errorf("%s", g.noDispatchMessageLocked(terminalID, "advanceToReady", "no active dispatch — call get_instructions first"))
	}
	switch d.state {
	case gateReady:
		return d, nil
	case gateDone:
		return nil, fmt.Errorf("dispatch already consumed — call get_instructions for a new one")
	case gateLocked:
		return nil, fmt.Errorf("not unlocked — call unlock first")
	}
	d.state = gateReady
	d.lastActivity = time.Now()
	return d, nil
}

// ActiveDispatch is the read-only view send_message and the level-gating
// middleware need.
type ActiveDispatch struct {
	ChatJID    string
	Level      string
	Ready      bool
	BurstMaxTS int64 // TS of the last dispatched burst message (ct-2026-07-13-2243)
	// Sender (T108, ct-2026-09-01-1413): the canonical group participant
	// this dispatch is FOR — "" for a 1:1 chat. See dispatch.sender's doc.
	Sender string
	// Nonce (T147, ct-2026-09-07) identifies THIS specific dispatch — the
	// identity a handler must carry through to Consume, so Consume closes
	// the dispatch this call actually validated, never just "whatever is
	// currently bound to the terminal" (the bug T147 was born from).
	Nonce string
	// Done (T147, ct-2026-09-07) is true when this dispatch was already
	// consumed — Ready alone collapses "never touched" (locked/noting) and
	// "already finished" (done) into the same false, which is exactly the
	// ambiguity that made T147 unreadable from its own error messages.
	Done bool
	// SendCount (T167, ct-2026-09-17-1255) mirrors dispatch.sends — how many
	// send_message/draft calls RecordSend has recorded for this dispatch so
	// far. send.go's validateSend reads this only once Done is true and the
	// target is this dispatch's own chat, to allow up to maxSendsPerDispatch
	// total before finally refusing with "already consumed". Meaningless
	// (always 0) for silent_act, which never calls RecordSend.
	SendCount int
}

// Active reports terminalID's current dispatch, if any.
//
// false means no dispatch is bound for this terminal — callers MUST treat
// this as DENY for gated tools (default-DENY, F4b), never as unrestricted:
// a terminal with nothing registered has no business calling a gated tool
// at all. The only ungated exception is boss, and boss must come from an
// explicit dispatch with Level == LevelBoss (registered by capipush from
// the chat's is_boss flag) — never from the absence of a dispatch. See
// docs/F4B-DIAGRAMA-FAILCLOSED.md.
func (g *Gate) Active(terminalID string) (ActiveDispatch, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	d := g.byTerminal[terminalID]
	if d == nil {
		return ActiveDispatch{}, false
	}
	return ActiveDispatch{ChatJID: d.chatJID, Level: d.level, Ready: d.state == gateReady, BurstMaxTS: d.burstMaxTS, Sender: d.sender, Nonce: d.nonce, Done: d.state == gateDone, SendCount: d.sends}, true
}

// Consume retires the dispatch identified by nonce — one-shot, so a used
// dispatch can never be replayed. The entry stays in byTerminal (marked
// done, Ready() false) rather than being deleted: a terminal that ever
// engaged the gate must not fall back to the no-dispatch-bound default just
// because its last dispatch finished — it stays gated (denied) until a NEW
// get_instructions binds it to the next one.
//
// SECURITY (T147, ct-2026-09-07): identified by nonce, not just terminalID.
// Before this fix, Consume closed "whatever dispatch currently occupies
// this terminal" — a handler that read Active() early (to validate Ready),
// then took real time to actually finish (compose a long reply, or simply
// the ordinary latency of an agent turn), could find a DIFFERENT, newer,
// never-opened dispatch sitting there by the time it called Consume — and
// silently close IT instead: its nonce gone from byNonce (unknown nonce),
// its underlying message never read by anyone, with no error anywhere. A
// mismatch here is NOT an error for the caller — the caller's own turn
// genuinely finished (its message sent); there is simply nothing THIS
// dispatch to close anymore, because a newer one already took its place.
// Logged either way: Consume used to leave zero trace, which is exactly why
// T147 took three rounds to diagnose from the log alone.
func (g *Gate) Consume(terminalID, nonce string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	d := g.byTerminal[terminalID]
	if d == nil {
		log.Printf("mcpserver: gate: consume terminal=%s nonce=%s: nada bound — el turno ya no existe, nada que cerrar", terminalID, nonce)
		return
	}
	if d.nonce != nonce {
		log.Printf("mcpserver: gate: consume terminal=%s nonce=%s: el despacho actual es otro (nonce=%s, chat=%s) — NO se cierra, un despacho más nuevo ya reemplazó al que este turno abrió (T147)", terminalID, nonce, d.nonce, d.chatJID)
		return
	}
	g.retireLocked(d.nonce, "consumed — this dispatch's own turn already finished (send_message/draft/silent_act); one-shot, wait for a fresh dispatch")
	delete(g.byNonce, d.nonce)
	d.state = gateDone
}

// RecordSend increments the per-dispatch send counter (dispatch.sends,
// exposed as ActiveDispatch.SendCount) that send.go's validateSend checks
// against maxSendsPerDispatch — T167 (ct-2026-09-17-1255), the boss's own
// "envíe un mensaje y despues un sticker" case. Deliberately NOT part of
// Consume: the lock/ready/done state machine, and the instant InFlight
// release it gives on the very first send, are untouched by this contract —
// only send.go's policy for how many MORE sends the dispatch's own chat may
// receive after that first one changes. Same identity guard as Consume
// (T147): a no-op if nonce isn't d's CURRENT nonce, so a stale caller can
// never inflate a newer dispatch's count. Never called for silent_act — it
// has no cap to track.
func (g *Gate) RecordSend(terminalID, nonce string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	d := g.byTerminal[terminalID]
	if d == nil || d.nonce != nonce {
		return
	}
	d.sends++
	d.lastActivity = time.Now()
}
