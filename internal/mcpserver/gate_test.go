package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"piumy-gateway/internal/router"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
)

// serverWithGate builds a server wired to a caller-supplied Gate (so tests
// can RegisterDispatch synthetic {nonce, level, chat, terminal_id}
// dispatches — cAPI doesn't exist yet, F4b) and a whitelist-everything
// router (guardrails other than the gate aren't what these tests are
// about). Callers inject a terminal id into their own per-call context via
// withTerminalID before calling callTool — no real MCP transport involved.
func serverWithGate(t *testing.T, gate *Gate) (*store.Store, *server.MCPServer, context.Context) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	routerPath := filepath.Join(dir, "router.json")
	if err := os.WriteFile(routerPath, []byte(`{"default_mode":"dedicated"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rtMgr := router.NewManager(routerPath)
	sm := state.NewManager(filepath.Join(dir, "status.json"), 8)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	srv := New(ctx, Deps{Store: st, State: sm, Router: rtMgr, AgentIdle: time.Minute, Gate: gate})
	return st, srv, ctx
}

// unlockToken extracts the token from get_instructions' JSON-RPC response —
// the tool result's text content is itself JSON (Instructions), so this
// unwraps twice: outer JSON-RPC envelope, then the inner Instructions.
func unlockToken(t *testing.T, out string) string {
	t.Helper()
	var envelope struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("parse JSON-RPC envelope: %v\nraw: %s", err, out)
	}
	if len(envelope.Result.Content) == 0 {
		t.Fatalf("no content in get_instructions response: %s", out)
	}
	var instr Instructions
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &instr); err != nil {
		t.Fatalf("parse Instructions: %v\nraw text: %s", err, envelope.Result.Content[0].Text)
	}
	if instr.Token == "" {
		t.Fatalf("empty token in Instructions: %+v", instr)
	}
	return instr.Token
}

func TestGateDefaultDenyWithNoDispatch(t *testing.T) {
	gate := NewGate()
	// T87: a Gate this young reads "no dispatch" as "probably a restart",
	// not a hard reject (see TestGateNewlyStartedGetsRestartMessageNotDeny)
	// — this test wants the OLD, unambiguous hard-reject path, so it ages
	// the gate past staleAfter first.
	gate.startedAt = time.Now().Add(-2 * dispatchStaleAfter)
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000045@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := withTerminalID(ctx, "term-nodispatch")

	// send_message: NOT gated by the default-DENY middleware at all — it's
	// not in chatScopedArg/enumerationTools. Its own initiation check lives
	// inside validateSend, and (T64, ct-2026-08-11-1627) no longer requires
	// a bound dispatch to initiate — a chat with rules now succeeds here
	// with no dispatch registered.
	_, policyVersion := decisionPolicy("")
	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "queued for sending") {
		t.Errorf("send_message with no dispatch registered = %s, want queued for sending", out)
	}

	// A gated (chat-scoped) tool: also denied.
	if out := callTool(t, termCtx, srv, "get_chat", map[string]any{"chat_id": chat}); !strings.Contains(out, "default DENY") {
		t.Errorf("get_chat with no dispatch = %s, want default DENY", out)
	}
	// An enumeration tool: also denied.
	if out := callTool(t, termCtx, srv, "list_chats", map[string]any{}); !strings.Contains(out, "default DENY") {
		t.Errorf("list_chats with no dispatch = %s, want default DENY", out)
	}
	// An UNGATED tool (no chat concept): still open, never required a dispatch.
	if out := callTool(t, termCtx, srv, "get_status", map[string]any{}); strings.Contains(out, "DENY") {
		t.Errorf("get_status with no dispatch = %s, want it unaffected (never gated)", out)
	}
}

// TestGateNewlyStartedGetsRestartMessageNotDeny is T87's core positive case
// (criterio de listo: "un nonce contra un Gate recién creado recibe el
// mensaje nuevo") — companion to TestGateDefaultDenyWithNoDispatch above,
// which proves the OTHER case (aged gate, same conditions, still hard
// denies). A fresh NewGate() with nothing registered at all — the real bug
// the boss hit was exactly this shape: the gateway restarts, the maps are
// empty, and the very next gated call from an agent that had a legitimate
// dispatch a moment ago reads as a security refusal instead of "wait".
func TestGateNewlyStartedGetsRestartMessageNotDeny(t *testing.T) {
	gate := NewGate() // startedAt == now, deliberately NOT aged
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000046@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, "term-freshly-restarted")

	out := callTool(t, termCtx, srv, "get_chat", map[string]any{"chat_id": chat})
	if strings.Contains(out, "default DENY") {
		t.Errorf("get_chat against a freshly-started gate = %s, want the T87 restart message, not default DENY", out)
	}
	if !strings.Contains(out, "not denied") || !strings.Contains(out, "redispatched") {
		t.Errorf("get_chat against a freshly-started gate = %s, want it to explain the dispatch will come back", out)
	}
}

// TestGateNewlyStartedLogsTheDistinction is the log-line half of the same
// criterio de listo ("que quede en el LOG una línea cuando pasa: hoy este
// caso es invisible desde afuera").
func TestGateNewlyStartedLogsTheDistinction(t *testing.T) {
	gate := NewGate()
	_, srv, ctx := serverWithGate(t, gate)
	termCtx := withTerminalID(ctx, "term-freshly-restarted-log")

	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })

	callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-from-before-restart"})

	if !strings.Contains(buf.String(), "T87") {
		t.Errorf("log for a nonce miss against a freshly-started gate = %q, want a T87-tagged line distinguishing it from an ordinary unknown nonce", buf.String())
	}
}

func TestGateSendWithoutUnlockFails(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000004@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := withTerminalID(ctx, "term-1")
	if err := gate.RegisterDispatch("nonce-1", chat, LevelCaution, "term-1", 0, ""); err != nil {
		t.Fatal(err)
	}
	callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-1"})

	_, policyVersion := decisionPolicy("")
	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "locked:") {
		t.Errorf("send_message before unlock = %s, want the locked error", out)
	}
}

func TestGateFullFlowThenSendSucceeds(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000005@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := withTerminalID(ctx, "term-2")
	if err := gate.RegisterDispatch("nonce-2", chat, LevelCaution, "term-2", 0, ""); err != nil {
		t.Fatal(err)
	}
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-2"})
	token := unlockToken(t, instr)

	if out := callTool(t, termCtx, srv, "unlock", map[string]any{"token": token}); !strings.Contains(out, "unlocked") {
		t.Fatalf("unlock = %s, want success", out)
	}
	if out := callTool(t, termCtx, srv, "skip", map[string]any{}); !strings.Contains(out, "ready") {
		t.Fatalf("skip = %s, want ready", out)
	}

	_, policyVersion := decisionPolicy("")
	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "queued for sending") {
		t.Errorf("send_message after full gate flow = %s, want it to pass", out)
	}

	// T167 (ct-2026-09-17-1255): the gate's own Consume still retires the
	// dispatch on this very first send (InFlight already false — see
	// TestInFlightReportsBoundNonDoneDispatch), but send.go's own cap lets up to
	// maxSendsPerDispatch land on THIS SAME chat before refusing — see
	// TestSendMessageLockedDistinguishesAlreadyConsumedFromNeverTouched
	// (send_test.go) for the full budget/exhaustion coverage. Here, just the
	// basic shape: a second call with no new get_instructions still succeeds.
	replay := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "otra vez", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(replay, "queued for sending") {
		t.Errorf("send_message replay after consume (still within the T167 cap) = %s, want it to succeed", replay)
	}
}

func TestGateCheckpointRequiredBeforeReady(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000006@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, "term-3")
	if err := gate.RegisterDispatch("nonce-3", chat, LevelDanger, "term-3", 0, ""); err != nil {
		t.Fatal(err)
	}
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-3"})
	token := unlockToken(t, instr)
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})

	_, policyVersion := decisionPolicy("")
	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "locked:") {
		t.Errorf("send_message after unlock but before remember/skip = %s, want it still locked", out)
	}
}

// TestGateAntiReplay covers the anti-replay DoD scenario: a token from a
// nonce this terminal no longer holds (replaced by a newer get_instructions)
// must fail — terminal-scoped lookup makes this fall out for free, no
// separate token index needed.
func TestGateAntiReplay(t *testing.T) {
	gate := NewGate()
	_, srv, ctx := serverWithGate(t, gate)
	termCtx := withTerminalID(ctx, "term-4")

	if err := gate.RegisterDispatch("nonce-a", "chatA@c.us", LevelCaution, "term-4", 0, ""); err != nil {
		t.Fatal(err)
	}
	instrA := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-a"})
	tokenA := unlockToken(t, instrA)

	if err := gate.RegisterDispatch("nonce-b", "chatB@c.us", LevelCaution, "term-4", 0, ""); err != nil {
		t.Fatal(err)
	}
	callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-b"}) // replaces A

	out := callTool(t, termCtx, srv, "unlock", map[string]any{"token": tokenA})
	if !strings.Contains(out, "invalid token") {
		t.Errorf("unlock with nonce-a's stale token after nonce-b replaced it = %s, want invalid token", out)
	}
}

// TestGateUnlockRejectsEmptyTokenWithoutGetInstructions is the H4
// hardening regression (ct-2026-07-10-0540): d.token is "" until
// GetInstructions sets it, so an agent that never calls get_instructions
// could previously call unlock(token="") and slip past locked -> noting
// without ever ingesting rules/memory/context.
func TestGateUnlockRejectsEmptyTokenWithoutGetInstructions(t *testing.T) {
	gate := NewGate()
	_, srv, ctx := serverWithGate(t, gate)
	termCtx := withTerminalID(ctx, "term-5")

	if err := gate.RegisterDispatch("nonce-5", "chat5@c.us", LevelCaution, "term-5", 0, ""); err != nil {
		t.Fatal(err)
	}
	// Deliberately skip get_instructions — d.token is still its zero value.

	out := callTool(t, termCtx, srv, "unlock", map[string]any{"token": ""})
	if !strings.Contains(out, "invalid token") {
		t.Errorf("unlock(token=\"\") without get_instructions = %s, want invalid token", out)
	}
}

// TestGateCrossTerminalHijackFails is the critical F4b evidence: terminal B
// must never be able to consume a dispatch registered for terminal A, even
// knowing the correct nonce.
func TestGateCrossTerminalHijackFails(t *testing.T) {
	gate := NewGate()
	_, srv, ctx := serverWithGate(t, gate)

	if err := gate.RegisterDispatch("nonce-x", "chatX@c.us", LevelCaution, "term-A", 0, ""); err != nil {
		t.Fatal(err)
	}

	// Terminal B tries to pull terminal A's dispatch by guessing/knowing the nonce.
	// Refused either way (the security guarantee this test exists for) — the
	// exact wording is TestGetInstructionsWrongTerminalNamesTheCause's job
	// (T104): this test only cares that B never gets the dispatch.
	termB := withTerminalID(ctx, "term-B")
	out := callTool(t, termB, srv, "get_instructions", map[string]any{"nonce": "nonce-x"})
	if !strings.Contains(out, "registered for a different terminal") {
		t.Errorf("terminal B pulling terminal A's dispatch = %s, want a wrong-terminal refusal", out)
	}

	// Terminal A (the rightful owner) still works normally.
	termA := withTerminalID(ctx, "term-A")
	instr := callTool(t, termA, srv, "get_instructions", map[string]any{"nonce": "nonce-x"})
	if strings.Contains(instr, "not registered") {
		t.Errorf("terminal A pulling its OWN dispatch = %s, want it to succeed", instr)
	}
}

func TestLevelGateDangerBlocksEnumerationAndPrivileged(t *testing.T) {
	gate := NewGate()
	_, srv, ctx := serverWithGate(t, gate)
	termCtx := withTerminalID(ctx, "term-d")
	if err := gate.RegisterDispatch("nonce-d", "chatD@c.us", LevelDanger, "term-d", 0, ""); err != nil {
		t.Fatal(err)
	}
	callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-d"})

	if out := callTool(t, termCtx, srv, "list_chats", map[string]any{}); !strings.Contains(out, "anti-leakage") {
		t.Errorf("list_chats under danger = %s, want it blocked", out)
	}
	// set_kill_switch (T148, ct-2026-09-07-1644): the ONE bossOnlyTools
	// survivor — reset_dashboard_password used to stand in for "a
	// boss-only tool" here, but it left the map along with every
	// group/profile tool.
	if out := callTool(t, termCtx, srv, "set_kill_switch", map[string]any{"kill": true}); !strings.Contains(out, "boss-only") {
		t.Errorf("set_kill_switch under danger = %s, want boss-only refusal", out)
	}
	// get_outbox/get_drafts take no chat_id at all — PendingOutbox/
	// PendingDrafts return every chat's queue unfiltered, so danger/caution
	// must not see them either (found in F4b audit, ct-2026-07-09-1641).
	if out := callTool(t, termCtx, srv, "get_outbox", map[string]any{}); !strings.Contains(out, "anti-leakage") {
		t.Errorf("get_outbox under danger = %s, want it blocked (unfiltered cross-chat data)", out)
	}
	if out := callTool(t, termCtx, srv, "get_drafts", map[string]any{}); !strings.Contains(out, "anti-leakage") {
		t.Errorf("get_drafts under danger = %s, want it blocked (unfiltered cross-chat data)", out)
	}
}

func TestLevelGateBossUnrestricted(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000017@c.us"
	if err := st.TouchChat(chat, "Boss", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := withTerminalID(ctx, "term-boss")
	if err := gate.RegisterDispatch("nonce-boss", chat, LevelBoss, "term-boss", 0, ""); err != nil {
		t.Fatal(err)
	}
	callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-boss"})

	if out := callTool(t, termCtx, srv, "list_chats", map[string]any{}); strings.Contains(out, "anti-leakage") {
		t.Errorf("list_chats under boss = %s, want it unrestricted", out)
	}
	if out := callTool(t, termCtx, srv, "get_outbox", map[string]any{}); strings.Contains(out, "anti-leakage") {
		t.Errorf("get_outbox under boss = %s, want it unrestricted", out)
	}

	// Boss sends WITHOUT unlock — "sin gate" (level=boss must come from an
	// explicit dispatch, which this test registers — never from absence).
	_, policyVersion := decisionPolicy("")
	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "avisale a Juan", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "queued for sending") {
		t.Errorf("boss send_message without unlock = %s, want it to pass (sin gate)", out)
	}
}

func TestLevelGateChatScopedToolsBlockCrossChat(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chatA, chatB := "55500000075@c.us", "55500000076@c.us"
	if err := st.TouchChat(chatA, "A", 1); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, "term-scope")
	if err := gate.RegisterDispatch("nonce-scope", chatA, LevelCaution, "term-scope", 0, ""); err != nil {
		t.Fatal(err)
	}
	callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-scope"})

	if out := callTool(t, termCtx, srv, "get_chat", map[string]any{"chat_id": chatB}); !strings.Contains(out, "anti-leakage") {
		t.Errorf("get_chat on a DIFFERENT chat under caution = %s, want it blocked", out)
	}
	if out := callTool(t, termCtx, srv, "get_chat", map[string]any{"chat_id": chatA}); strings.Contains(out, "anti-leakage") {
		t.Errorf("get_chat on the dispatch's OWN chat under caution = %s, want it allowed through", out)
	}
}

func TestGateRememberWritesMemoryAndContext(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000077@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, "term-r")
	if err := gate.RegisterDispatch("nonce-r", chat, LevelCaution, "term-r", 0, ""); err != nil {
		t.Fatal(err)
	}
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-r"})
	token := unlockToken(t, instr)
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})
	callTool(t, termCtx, srv, "remember", map[string]any{"memory": "le gusta el café", "context": "cliente frecuente"})

	c, _, err := st.GetChat(chat)
	if err != nil {
		t.Fatal(err)
	}
	if c.Memory != "le gusta el café" || c.Context != "cliente frecuente" {
		t.Errorf("got memory=%q context=%q, want them written by remember", c.Memory, c.Context)
	}
}

// TestGateBossConsumedDispatchNoLongerGrantsPrivileges is the ST-A security
// regression (ct-2026-07-11-0740, CRITICAL — Amatista's audit): Consume()
// marks a dispatch done but left it bound in byTerminal with Level still
// Boss. validateSend and levelGateMiddleware both keyed off Level==Boss
// alone, never checking Ready — so a terminal that consumed ONE boss
// dispatch could keep sending anywhere and calling boss-only tools forever,
// no new dispatch ever required. Unlike
// TestRegisterDispatchClosesResidualPrivilegeWindow (which needs a NEW
// dispatch to arrive to demonstrate the fix), this is the direct case: the
// SAME terminal, SAME (already-consumed) dispatch, no new registration at
// all.
//
// T167 (ct-2026-09-17-1255) narrowed, not removed, the guarantee this test
// checks: a consumed dispatch's residual window is no longer ZERO extra
// sends, it's maxSendsPerDispatch total — but still bounded (not "forever"),
// and still scoped to this exact chat (see
// TestSendMessageLockedDistinguishesAlreadyConsumedFromNeverTouched and
// TestSendMessageToADifferentChatSucceedsAfterConsumedDispatch, send_test.go,
// for the send_message-specific coverage of both limits). What this test
// still must prove: the window ends somewhere, and boss-only ADMIN tools
// (set_is_boss below) never widen with it.
func TestGateBossConsumedDispatchNoLongerGrantsPrivileges(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	bossChat := "55500000078@c.us"
	if err := st.TouchChat(bossChat, "Boss", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := withTerminalID(ctx, "term-boss-done")

	if err := gate.RegisterDispatch("nonce-boss-done", bossChat, LevelBoss, "term-boss-done", 0, ""); err != nil {
		t.Fatal(err)
	}
	_, policyVersion := decisionPolicy("")
	first := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": bossChat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(first, "queued for sending") {
		t.Fatalf("setup: first boss send (no unlock needed, sin gate) = %s, want it to pass", first)
	}

	if active, ok := gate.Active("term-boss-done"); !ok || active.Ready {
		t.Fatalf("setup: Active after consume = %+v, ok=%v, want bound with Ready=false", active, ok)
	}

	// T167: calls 2-4, still with no new dispatch, land within the shared
	// budget — the residual window is bounded, not zero.
	for i := 2; i <= maxSendsPerDispatch; i++ {
		out := callTool(t, termCtx, srv, "send_message", map[string]any{
			"to": bossChat, "message": "otra vez, sin nuevo dispatch", "model": "m", "policy_version": policyVersion,
		})
		if !strings.Contains(out, "queued for sending") {
			t.Fatalf("send #%d after boss consume, no new dispatch = %s, want it within the T167 cap", i, out)
		}
	}

	// The vulnerability this test guards: past maxSendsPerDispatch, a
	// FURTHER send with no new dispatch must be denied — the window is
	// bounded, never infinite, regardless of the residual Level=boss.
	overCap := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": bossChat, "message": "una mas, ya deberia estar cerrado", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(overCap, "locked:") {
		t.Errorf("send_message past the T167 cap after boss consume, no new dispatch = %s, want locked/denied", overCap)
	}

	// set_is_boss (T150, ct-2026-09-07-1839): this used to assert "locked:"
	// — accurate when this test was written (ST-A, 2026-07-11), when
	// set_is_boss was still a bossOnlyTools entry and levelGateMiddleware's
	// own boss-Ready gate is what refused it. S10 (ct-2026-07-30-1349, three
	// weeks later) moved set_is_boss OUT of bossOnlyTools entirely — its own
	// handler (admin_tools.go) has refused it unconditionally ever since,
	// no dispatch state involved at all. T150's fix (isGated checked before
	// gate.Active) means levelGateMiddleware now lets it straight through to
	// that handler instead of intercepting it first — so the message changes,
	// but the guarantee this line checks (set_is_boss stays blocked, always)
	// does not: it was always the handler doing the real blocking here, this
	// assertion just used to observe it one layer too early.
	setBoss := callTool(t, termCtx, srv, "set_is_boss", map[string]any{"chat_id": bossChat, "is_boss": true})
	if !strings.Contains(setBoss, "refused: is_boss is never settable via MCP") {
		t.Errorf("set_is_boss after boss consume, no new dispatch = %s, want the handler's own unconditional MCP-blocked refusal", setBoss)
	}
}

// TestRegisterDispatchClosesResidualPrivilegeWindow covers the security fix
// found while writing F4c: a terminal bound to a boss dispatch (even
// consumed/done) must NOT keep answering as boss once a new, lower-trust
// dispatch is registered for it. Before the fix, RegisterDispatch only
// touched byNonce, leaving byTerminal pointed at the stale boss dispatch
// until the agent itself called GetInstructions for the new one — a
// prompt-injected agent that simply skipped that call kept operating with
// boss privileges. See TestGateBossConsumedDispatchNoLongerGrantsPrivileges
// for the ST-A follow-up (ct-2026-07-11-0740): that fix closes the same hole
// even WITHOUT a new dispatch ever arriving.
func TestRegisterDispatchClosesResidualPrivilegeWindow(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	bossChat := "55500000079@c.us"
	dangerChat := "55500000080@c.us"
	if err := st.TouchChat(bossChat, "Boss", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := withTerminalID(ctx, "term-residual")

	// 1) Boss dispatch, pulled and consumed via a real send.
	if err := gate.RegisterDispatch("nonce-boss", bossChat, LevelBoss, "term-residual", 0, ""); err != nil {
		t.Fatal(err)
	}
	callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-boss"})
	_, policyVersion := decisionPolicy("")
	callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": bossChat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})
	// ST-A fix (ct-2026-07-11-0740): Level legitimately stays Boss after
	// consume (Consume never changes level, only state) — that part was
	// never the bug. The bug was that Ready ALSO stayed irrelevant for
	// boss forever, so a done dispatch kept granting access. Ready must
	// now read false post-consume, same as any other level.
	active, ok := gate.Active("term-residual")
	if !ok || active.Level != LevelBoss {
		t.Fatalf("setup: Active after consume = %+v, ok=%v, want still bound with Level=boss (level itself doesn't change on consume)", active, ok)
	}
	if active.Ready {
		t.Fatal("setup: Active().Ready after consume = true, want false (ST-A: a done dispatch must not read as ready, even for boss)")
	}

	// 2) A NEW danger dispatch arrives for the SAME terminal — WITHOUT the
	// agent calling get_instructions for it yet.
	if err := gate.RegisterDispatch("nonce-danger", dangerChat, LevelDanger, "term-residual", 0, ""); err != nil {
		t.Fatal(err)
	}

	// Active() must reflect the NEW dispatch immediately — not the stale boss one.
	active, ok = gate.Active("term-residual")
	if !ok {
		t.Fatal("Active after registering the new dispatch: want bound=true")
	}
	if active.Level != LevelDanger {
		t.Errorf("Active().Level = %q, want %q (residual boss privilege must not survive)", active.Level, LevelDanger)
	}
	if active.Ready {
		t.Error("Active().Ready = true, want false (new dispatch is locked, not gated yet)")
	}
	if active.ChatJID != dangerChat {
		t.Errorf("Active().ChatJID = %q, want %q", active.ChatJID, dangerChat)
	}

	// A boss-only tool called before get_instructions(danger) must be
	// refused. set_kill_switch (T148, ct-2026-09-07-1644): the ONE
	// bossOnlyTools survivor — reset_dashboard_password used to stand in
	// for "a boss-only tool" here, but it left the map. THIS is the real
	// guarantee this test protects — residual boss privilege must not keep
	// granting a boss-only TOOL — and T150 does not touch it: isGated is
	// checked before gate.Active, so a gated tool's Ready/Level logic runs
	// exactly as before. Confirmed unchanged by T150 (Citrino, reviewing the
	// fix): do not loosen this assertion — if a future change ever makes
	// this one need to, that change opened something it should not have.
	if out := callTool(t, termCtx, srv, "set_kill_switch", map[string]any{"kill": true}); !strings.Contains(out, "boss-only") {
		t.Errorf("set_kill_switch on the residual-privilege terminal = %s, want boss-only refusal", out)
	}

	// send_message to the OLD (boss) chat (T150, ct-2026-09-07-1839): this
	// used to assert "locked:" — the second, DIFFERENT thing this test
	// checked, not the same guarantee as set_kill_switch above. It was
	// protecting against writing to a chat with no valid dispatch for it,
	// which made sense before T64 (ct-2026-08-11-1627): back then, no
	// dispatch (or the wrong one) for a chat meant DENY, full stop. T64
	// opened writing to ANY chat with NO dispatch at all, unconditionally
	// ("any registered agent may INITIATE, no dispatch required") — so
	// bossChat here is already reachable by this exact terminal via that
	// path regardless of whatever stale dispatch it's carrying. T150's fix
	// (a dispatch that isn't Ready no longer blocks a DIFFERENT chat than
	// its own) just lets that same T64 door open through this route too —
	// nothing new is exposed, one more path reaches an already-open door.
	// The privilege-residual guarantee this test's NAME refers to is the
	// set_kill_switch assertion above, untouched.
	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": bossChat, "message": "otra vez", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "queued for sending") {
		t.Errorf("send_message to the old boss chat after a new (not-yet-ready) danger dispatch = %s, want it to succeed — T64 already allows writing there with no dispatch at all", out)
	}
}

func TestInFlightReportsBoundNonDoneDispatch(t *testing.T) {
	gate := NewGate()
	if gate.InFlight("term-x") {
		t.Error("InFlight with nothing registered: want false")
	}
	if err := gate.RegisterDispatch("n1", "chat@c.us", LevelCaution, "term-x", 0, ""); err != nil {
		t.Fatal(err)
	}
	if !gate.InFlight("term-x") {
		t.Error("InFlight right after RegisterDispatch (locked, not done): want true")
	}
	gate.Consume("term-x", "n1")
	if gate.InFlight("term-x") {
		t.Error("InFlight after Consume (done): want false")
	}
}

// TestNonceActive covers ct-2026-07-18-1851-B: capipush.newNonce checks this
// before committing to a short (4-hex) nonce, regenerating on a collision.
func TestNonceActive(t *testing.T) {
	gate := NewGate()
	if gate.NonceActive("ab12") {
		t.Error("NonceActive with nothing registered: want false")
	}
	if err := gate.RegisterDispatch("ab12", "chat@c.us", LevelCaution, "term-y", 0, ""); err != nil {
		t.Fatal(err)
	}
	if !gate.NonceActive("ab12") {
		t.Error("NonceActive right after RegisterDispatch: want true")
	}
	if gate.NonceActive("cd34") {
		t.Error("NonceActive for an unrelated nonce: want false")
	}
	gate.Consume("term-y", "ab12")
	if gate.NonceActive("ab12") {
		t.Error("NonceActive after Consume (done, evicted from byNonce): want false")
	}
}

// TestSweepReclaimsStaleBoundDispatch is the H5 hardening regression
// (ct-2026-07-10-0540): an agent that calls get_instructions and then
// crashes (or never calls unlock/remember/skip/send_message) used to leave
// InFlight(terminalID) == true forever — sweepLocked previously only
// reclaimed a dispatch that was NEVER pulled (!boundToTerm), never a
// bound-but-stuck one. Backdates lastActivity directly (same package,
// white-box) instead of actually waiting dispatchStaleAfter (1h).
func TestSweepReclaimsStaleBoundDispatch(t *testing.T) {
	gate := NewGate()
	_, srv, ctx := serverWithGate(t, gate)
	termCtx := withTerminalID(ctx, "term-stale")

	if err := gate.RegisterDispatch("nonce-stale", "chat-stale@c.us", LevelCaution, "term-stale", 0, ""); err != nil {
		t.Fatal(err)
	}
	callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-stale"})
	if !gate.InFlight("term-stale") {
		t.Fatal("setup: want the terminal in-flight after get_instructions")
	}

	gate.mu.Lock()
	gate.byNonce["nonce-stale"].lastActivity = time.Now().Add(-dispatchStaleAfter - time.Minute)
	gate.sweepLocked(time.Now())
	gate.mu.Unlock()

	if gate.InFlight("term-stale") {
		t.Error("InFlight after sweeping a stale bound dispatch = true, want false (freed)")
	}
	if _, ok := gate.Active("term-stale"); ok {
		t.Error("Active after sweeping a stale bound dispatch = ok, want nothing bound")
	}
}

// TestSweepDoesNotReclaimActiveBoundDispatch is the control: a dispatch
// well within dispatchStaleAfter must survive the sweep untouched — a
// legitimately slow agent (still thinking between get_instructions and
// send_message) must never get force-denied mid-flow.
func TestSweepDoesNotReclaimActiveBoundDispatch(t *testing.T) {
	gate := NewGate()
	if err := gate.RegisterDispatch("nonce-fresh", "chat-fresh@c.us", LevelCaution, "term-fresh", 0, ""); err != nil {
		t.Fatal(err)
	}

	gate.mu.Lock()
	gate.sweepLocked(time.Now())
	gate.mu.Unlock()

	if !gate.InFlight("term-fresh") {
		t.Error("InFlight after sweeping a fresh dispatch = false, want true (not stale yet)")
	}
}

// TestSetStaleAfterOverridesReclaimWindow is the configurable-knob
// regression (ct-2026-07-11-074123, post-incident): dispatchStaleAfter used to
// be a hardcoded 1h with no way to shorten it — a dispatch orphaned by a
// crashed agent silently wedged its terminal for up to an hour. Confirms
// SetStaleAfter actually changes what sweepLocked reclaims against.
func TestSetStaleAfterOverridesReclaimWindow(t *testing.T) {
	gate := NewGate()
	gate.SetStaleAfter(10 * time.Minute)

	if err := gate.RegisterDispatch("nonce-short", "chat@c.us", LevelCaution, "term-short", 0, ""); err != nil {
		t.Fatal(err)
	}

	gate.mu.Lock()
	gate.byNonce["nonce-short"].lastActivity = time.Now().Add(-15 * time.Minute)
	gate.sweepLocked(time.Now())
	gate.mu.Unlock()

	if gate.InFlight("term-short") {
		t.Error("InFlight after sweeping past the overridden 10m stale window = true, want false (reclaimed)")
	}
}

// TestSetStaleAfterIgnoresNonPositive confirms the default (dispatchStaleAfter,
// 1h) survives a bogus override instead of silently disabling reclamation.
func TestSetStaleAfterIgnoresNonPositive(t *testing.T) {
	gate := NewGate()
	gate.SetStaleAfter(0)
	gate.SetStaleAfter(-5 * time.Minute)

	if err := gate.RegisterDispatch("nonce-default", "chat@c.us", LevelCaution, "term-default", 0, ""); err != nil {
		t.Fatal(err)
	}

	gate.mu.Lock()
	gate.byNonce["nonce-default"].lastActivity = time.Now().Add(-15 * time.Minute)
	gate.sweepLocked(time.Now())
	gate.mu.Unlock()

	if !gate.InFlight("term-default") {
		t.Error("InFlight after 15m with a non-positive SetStaleAfter (should be ignored, default 1h stands) = false, want it still in-flight")
	}
}

// TestCancelDispatchFreesTerminal is capipush's revert path (H5 hardening,
// ct-2026-07-10-0540): if Inject fails right after RegisterDispatch
// succeeded, CancelDispatch must free the terminal immediately instead of
// leaving it wedged until the hourly sweep.
func TestCancelDispatchFreesTerminal(t *testing.T) {
	gate := NewGate()
	if err := gate.RegisterDispatch("nonce-cancel", "chat-cancel@c.us", LevelCaution, "term-cancel", 0, ""); err != nil {
		t.Fatal(err)
	}
	if !gate.InFlight("term-cancel") {
		t.Fatal("setup: want the terminal in-flight after RegisterDispatch")
	}

	gate.CancelDispatch("nonce-cancel", "term-cancel")

	if gate.InFlight("term-cancel") {
		t.Error("InFlight after CancelDispatch = true, want false (freed)")
	}
	if _, ok := gate.Active("term-cancel"); ok {
		t.Error("Active after CancelDispatch = ok, want nothing bound")
	}
}

// TestCancelDispatchIgnoresMismatch confirms CancelDispatch never touches
// the WRONG dispatch: a late cancel for a nonce a newer dispatch has
// already superseded (RegisterDispatch's own force-replace already dropped
// it from byNonce — see its doc comment) is a no-op, not an eviction of
// the terminal's current, still-valid dispatch.
func TestCancelDispatchIgnoresMismatch(t *testing.T) {
	gate := NewGate()
	if err := gate.RegisterDispatch("nonce-old", "chat@c.us", LevelCaution, "term-shared", 0, ""); err != nil {
		t.Fatal(err)
	}
	// A newer dispatch replaces the old one for the same terminal — mirrors
	// RegisterDispatch's own force-replace (gate.go doc comment).
	if err := gate.RegisterDispatch("nonce-new", "chat@c.us", LevelCaution, "term-shared", 0, ""); err != nil {
		t.Fatal(err)
	}

	// A late CancelDispatch for the stale nonce (e.g. a slow Inject error
	// arriving after a newer dispatch already took over) must not evict it.
	gate.CancelDispatch("nonce-old", "term-shared")

	if !gate.InFlight("term-shared") {
		t.Error("InFlight after CancelDispatch on a superseded nonce = false, want true (the newer dispatch must survive)")
	}
	active, ok := gate.Active("term-shared")
	if !ok || active.ChatJID != "chat@c.us" {
		t.Errorf("Active after CancelDispatch on a superseded nonce = %+v, ok=%v, want the newer dispatch untouched", active, ok)
	}
}

// TestRegisterDispatchStoresBurstMaxTS — ct-2026-07-13-2243 Fix 2: the
// burstMaxTS passed to RegisterDispatch is visible via Active so send_message
// can use it for MarkHandledBefore instead of time.Now(), preventing
// messages that arrived during the in-flight period from being marked handled.
func TestRegisterDispatchStoresBurstMaxTS(t *testing.T) {
	gate := NewGate()
	const wantTS int64 = 9999
	if err := gate.RegisterDispatch("nonce-bts", "chat@c.us", LevelCaution, "term-bts", wantTS, ""); err != nil {
		t.Fatal(err)
	}
	active, ok := gate.Active("term-bts")
	if !ok {
		t.Fatal("Active returned nothing after RegisterDispatch")
	}
	if active.BurstMaxTS != wantTS {
		t.Errorf("Active.BurstMaxTS = %d, want %d", active.BurstMaxTS, wantTS)
	}
}

// TestRegisterDispatchStoresSender is T108's (ct-2026-09-01-1413) own
// analogue of the BurstMaxTS test above — the gate must remember WHICH
// group participant a dispatch is for, or send.go/silent_act have nothing
// to scope MarkHandledBeforeForSender to.
func TestRegisterDispatchStoresSender(t *testing.T) {
	gate := NewGate()
	const sender = "555000000001@s.whatsapp.net"
	if err := gate.RegisterDispatch("nonce-sender", "555001@g.us", LevelCaution, "term-sender", 0, sender); err != nil {
		t.Fatal(err)
	}
	active, ok := gate.Active("term-sender")
	if !ok {
		t.Fatal("Active returned nothing after RegisterDispatch")
	}
	if active.Sender != sender {
		t.Errorf("Active.Sender = %q, want %q", active.Sender, sender)
	}
}

// TestRegisterDispatchSenderEmptyForOneOnOne confirms the ordinary 1:1 case
// (every existing caller in this file passes "") leaves Sender empty — no
// behavior change for the half of the system T108 must not touch.
func TestRegisterDispatchSenderEmptyForOneOnOne(t *testing.T) {
	gate := NewGate()
	if err := gate.RegisterDispatch("nonce-11", "chat@c.us", LevelCaution, "term-11", 0, ""); err != nil {
		t.Fatal(err)
	}
	active, ok := gate.Active("term-11")
	if !ok {
		t.Fatal("Active returned nothing after RegisterDispatch")
	}
	if active.Sender != "" {
		t.Errorf("Active.Sender = %q, want empty for a 1:1 dispatch", active.Sender)
	}
}

// TestBossUnlockAndSkipAreIdempotentNoLongerContradict is S2's core
// regression (ct-2026-07-30-030928, reproduces the smoke's live deadlock):
// a boss dispatch is BORN gateReady (ST-A, ct-2026-07-11-0740), so
// unlock/skip used to disagree about the exact same state — "already
// unlocked" then "not unlocked — call unlock first". Both must now succeed
// as harmless no-ops, and the ritual must still end in a normal send.
func TestBossUnlockAndSkipAreIdempotentNoLongerContradict(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000081@c.us"
	if err := st.TouchChat(chat, "Boss", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := withTerminalID(ctx, "term-boss-idempotent")
	if err := gate.RegisterDispatch("nonce-boss-idem", chat, LevelBoss, "term-boss-idempotent", 0, ""); err != nil {
		t.Fatal(err)
	}
	callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-boss-idem"})

	// The habitual ritual an agent might still run out of habit for a
	// born-ready dispatch — an empty token proves the idempotent path never
	// even looks at it (this isn't the real locked->noting transition).
	unlockOut := callTool(t, termCtx, srv, "unlock", map[string]any{"token": ""})
	if strings.Contains(unlockOut, "already unlocked") || !strings.Contains(unlockOut, "unlocked") {
		t.Errorf("unlock on a born-ready boss dispatch = %s, want a plain success (no contradiction)", unlockOut)
	}
	skipOut := callTool(t, termCtx, srv, "skip", map[string]any{})
	if strings.Contains(skipOut, "not unlocked") || !strings.Contains(skipOut, "ready") {
		t.Errorf("skip on a born-ready boss dispatch = %s, want a plain success (no contradiction)", skipOut)
	}

	_, policyVersion := decisionPolicy("")
	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "queued for sending") {
		t.Errorf("send_message after the (idempotent) unlock/skip ritual = %s, want it to pass", out)
	}
}

// TestUnlockAndSkipIdempotentOnDoubleCall covers S2's fix beyond boss: a
// SECOND unlock (already noting) or a second skip (already ready) on the
// same dispatch is a harmless no-op, not an error — same mechanism, no
// special-casing by level.
func TestUnlockAndSkipIdempotentOnDoubleCall(t *testing.T) {
	gate := NewGate()
	_, srv, ctx := serverWithGate(t, gate)
	termCtx := withTerminalID(ctx, "term-double")
	if err := gate.RegisterDispatch("nonce-double", "chat@c.us", LevelCaution, "term-double", 0, ""); err != nil {
		t.Fatal(err)
	}
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-double"})
	token := unlockToken(t, instr)

	if out := callTool(t, termCtx, srv, "unlock", map[string]any{"token": token}); !strings.Contains(out, "unlocked") {
		t.Fatalf("first unlock = %s, want success", out)
	}
	if out := callTool(t, termCtx, srv, "unlock", map[string]any{"token": token}); !strings.Contains(out, "unlocked") {
		t.Errorf("second unlock (already noting) = %s, want idempotent success", out)
	}
	if out := callTool(t, termCtx, srv, "skip", map[string]any{}); !strings.Contains(out, "ready") {
		t.Fatalf("first skip = %s, want success", out)
	}
	if out := callTool(t, termCtx, srv, "skip", map[string]any{}); !strings.Contains(out, "ready") {
		t.Errorf("second skip (already ready) = %s, want idempotent success", out)
	}
}

// TestUnlockAndSkipErrorDistinctlyAfterConsume is the other half of S2's
// fix: a truly consumed (done) dispatch must still be refused, but with its
// OWN message — before this, it was indistinguishable from the harmless
// boss/double-call case ("already unlocked" either way).
func TestUnlockAndSkipErrorDistinctlyAfterConsume(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000082@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := withTerminalID(ctx, "term-consumed")
	if err := gate.RegisterDispatch("nonce-consumed", chat, LevelCaution, "term-consumed", 0, ""); err != nil {
		t.Fatal(err)
	}
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-consumed"})
	token := unlockToken(t, instr)
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})
	callTool(t, termCtx, srv, "skip", map[string]any{})
	_, policyVersion := decisionPolicy("")
	callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})

	unlockOut := callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})
	if !strings.Contains(unlockOut, "already consumed") {
		t.Errorf("unlock after consume = %s, want the distinct \"already consumed\" error", unlockOut)
	}
	skipOut := callTool(t, termCtx, srv, "skip", map[string]any{})
	if !strings.Contains(skipOut, "already consumed") {
		t.Errorf("skip after consume = %s, want the distinct \"already consumed\" error", skipOut)
	}
}

// TestStaleSweepLogsReclaim is S2's criterio de listo #3: a stuck dispatch
// already released itself via the stale sweep without a gateway restart
// (H5, ct-2026-07-10-0540) — it just did so in total silence. S1's log
// channel now covers it too.
func TestStaleSweepLogsReclaim(t *testing.T) {
	gate := NewGate()
	if err := gate.RegisterDispatch("nonce-stale-log", "chat-stale-log@c.us", LevelCaution, "term-stale-log", 0, ""); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })

	gate.mu.Lock()
	gate.byNonce["nonce-stale-log"].lastActivity = time.Now().Add(-dispatchStaleAfter - time.Minute)
	gate.sweepLocked(time.Now())
	gate.mu.Unlock()

	if !strings.Contains(buf.String(), "liberado por timeout") {
		t.Errorf("log tras evictar un dispatch stale = %q, want mención de \"liberado por timeout\"", buf.String())
	}
	if gate.InFlight("term-stale-log") {
		t.Error("InFlight tras el sweep stale = true, want false (liberado)")
	}
}

// TestGetInstructionsLogsUnknownNonce covers the diagnosability half of
// Citrino's second S2 finding: the failure was silent server-side even
// though the agent got an MCP error back.
//
// T147 (ct-2026-09-07): this nonce genuinely never existed — no
// RegisterDispatch, no Consume, no force-replace, nothing retired it — so
// the log (and the agent-facing error) must say exactly that, not guess at
// force-replace as the OLD wording did for every "!ok" case indiscriminately
// (the guess that cost T147's own investigation three rounds).
func TestGetInstructionsLogsUnknownNonce(t *testing.T) {
	gate := NewGate()
	gate.startedAt = time.Now().Add(-2 * dispatchStaleAfter) // T87: see TestGateDefaultDenyWithNoDispatch
	_, srv, ctx := serverWithGate(t, gate)
	termCtx := withTerminalID(ctx, "term-orphan")

	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })

	callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-nunca-registrado"})

	if !strings.Contains(buf.String(), "nunca registrado") {
		t.Errorf("log tras get_instructions con nonce desconocido = %q, want mención de que nunca se registró", buf.String())
	}
}

// TestGetInstructionsWrongTerminalNamesTheCause is T104's fix for the
// opaque half of the gate's two "no dispatch" errors (ct-2026-08-29-1818,
// evidence from Citrino's own production log): the gate ALREADY knows and
// logs which OTHER terminal a nonce is registered for — the agent-facing
// message used to throw that away ("this dispatch was not registered for
// this terminal"), reading exactly like a permissions problem when it's
// almost always a wiring mismatch (agent_id vs antenna_terminal_id). The
// new message must name both terminals and rule out "denied", same family
// as gateNoDispatchExplain (T87).
func TestGetInstructionsWrongTerminalNamesTheCause(t *testing.T) {
	gate := NewGate()
	_, srv, ctx := serverWithGate(t, gate)

	if err := gate.RegisterDispatch("nonce-cross", "chat@c.us", LevelCaution, "term-registered", 0, ""); err != nil {
		t.Fatal(err)
	}

	termCtx := withTerminalID(ctx, "term-presented")
	out := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-cross"})
	text := decodeToolText(t, out)

	if !strings.Contains(text, "term-registered") {
		t.Errorf("get_instructions wrong-terminal error = %q, want it to name the terminal the dispatch IS registered for (term-registered)", text)
	}
	if !strings.Contains(text, "term-presented") {
		t.Errorf("get_instructions wrong-terminal error = %q, want it to name the terminal that presented itself (term-presented)", text)
	}
	if !strings.Contains(text, "not a permissions problem") {
		t.Errorf("get_instructions wrong-terminal error = %q, want it to explicitly rule out a permissions problem, same as gateNoDispatchExplain (T87)", text)
	}
	if strings.Contains(text, "this dispatch was not registered for this terminal") {
		t.Errorf("get_instructions wrong-terminal error = %q, still the old opaque message — the cause the gate already logs never reached the agent", text)
	}
}

// TestGetInstructionsWrongTerminalLogOrdersTheLikelyCauseFirst is the log
// half of T104's finding: the log line put the alarming hypothesis
// ("intento de hijack") before the frequent, mundane one ("nonce mal
// dirigido") — Citrino's own call to invert the order.
func TestGetInstructionsWrongTerminalLogOrdersTheLikelyCauseFirst(t *testing.T) {
	gate := NewGate()
	_, srv, ctx := serverWithGate(t, gate)

	if err := gate.RegisterDispatch("nonce-order", "chat@c.us", LevelCaution, "term-registered-2", 0, ""); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })

	termCtx := withTerminalID(ctx, "term-presented-2")
	callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-order"})

	logged := buf.String()
	if !strings.Contains(logged, "nonce mal dirigido o intento de hijack") {
		t.Errorf("log = %q, want the mundane cause (nonce mal dirigido) named before the alarming one (intento de hijack)", logged)
	}
	if strings.Contains(logged, "intento de hijack o nonce mal dirigido") {
		t.Errorf("log = %q, still has the alarming hypothesis first", logged)
	}
}

// decodeInstructions unwraps a get_instructions JSON-RPC response into its
// Instructions payload — same double-unwrap as unlockToken, but keeping
// every field instead of discarding all but Token.
func decodeInstructions(t *testing.T, out string) Instructions {
	t.Helper()
	var envelope struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("parse JSON-RPC envelope: %v\nraw: %s", err, out)
	}
	if len(envelope.Result.Content) == 0 {
		t.Fatalf("no content in get_instructions response: %s", out)
	}
	var instr Instructions
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &instr); err != nil {
		t.Fatalf("parse Instructions: %v\nraw text: %s", err, envelope.Result.Content[0].Text)
	}
	return instr
}

// TestGetInstructionsReportsIsBossAndIsApprover is the get_instructions
// half of the ct-2026-08-06 preamble fix (boss verbatim: "todo mensaje con
// su preámbulo") — an agent that reconnects mid-dispatch (nonce still
// valid, the original cAPI preamble long gone) has no other way to learn
// who it's talking to, so get_instructions needs the same identity the
// dispatch preamble carries.
func TestGetInstructionsReportsIsBossAndIsApprover(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)

	bossChat := "55500000090@c.us"
	if err := st.TouchChat(bossChat, "Boss", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIsBoss(bossChat, true); err != nil {
		t.Fatal(err)
	}
	bossTermCtx := withTerminalID(ctx, "term-boss")
	if err := gate.RegisterDispatch("nonce-boss", bossChat, LevelBoss, "term-boss", 0, ""); err != nil {
		t.Fatal(err)
	}
	bossOut := callTool(t, bossTermCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-boss"})
	bossInstr := decodeInstructions(t, bossOut)
	if !bossInstr.IsBoss {
		t.Errorf("boss dispatch: IsBoss = %v, want true", bossInstr.IsBoss)
	}
	if bossInstr.IsApprover {
		t.Errorf("boss dispatch: IsApprover = %v, want false (never set for this chat)", bossInstr.IsApprover)
	}

	approverChat := "55500000091@c.us"
	if err := st.TouchChat(approverChat, "Approver", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIsApprover(approverChat, true); err != nil {
		t.Fatal(err)
	}
	approverTermCtx := withTerminalID(ctx, "term-approver")
	if err := gate.RegisterDispatch("nonce-approver", approverChat, LevelApprover, "term-approver", 0, ""); err != nil {
		t.Fatal(err)
	}
	approverOut := callTool(t, approverTermCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-approver"})
	approverInstr := decodeInstructions(t, approverOut)
	if approverInstr.IsBoss {
		t.Errorf("approver dispatch: IsBoss = %v, want false", approverInstr.IsBoss)
	}
	if !approverInstr.IsApprover {
		t.Errorf("approver dispatch: IsApprover = %v, want true", approverInstr.IsApprover)
	}
}

// ── T129 (ct-2026-09-03-0200) — the gate resolves by agent_id OR antenna ───

// TestRegisterDispatchResolvesByAntennaTerminalID is the actual bug: a
// terminal presenting its antenna_terminal_id (not its, renameable,
// agent_id) must reach the SAME dispatch and complete the full ritual —
// exactly what left the CleverCoder agent mute (registered under agent_id
// "citrino2", presented antenna "capi-clevercoder-citrino-caaed305").
func TestRegisterDispatchResolvesByAntennaTerminalID(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	if err := st.UpsertAgent(store.Agent{
		AgentID: "citrino2", Name: "Citrino", Endpoint: "http://x:8787",
		AntennaTerminalID: "capi-clevercoder-citrino-caaed305", Pinpass: "p", Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}
	chat := "55500000200@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")

	// capipush registers under the agent_id, same as always — this contract
	// never changes what capipush does.
	if err := gate.RegisterDispatch("nonce-antenna", chat, LevelCaution, "citrino2", 0, ""); err != nil {
		t.Fatal(err)
	}

	// The terminal presents its ANTENNA, not "citrino2".
	termCtx := withTerminalID(ctx, "capi-clevercoder-citrino-caaed305")
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-antenna"})
	token := unlockToken(t, instr)
	if out := callTool(t, termCtx, srv, "unlock", map[string]any{"token": token}); !strings.Contains(out, "unlocked") {
		t.Fatalf("unlock via antenna = %s, want success", out)
	}
	if out := callTool(t, termCtx, srv, "skip", map[string]any{}); !strings.Contains(out, "ready") {
		t.Fatalf("skip via antenna = %s, want ready", out)
	}
	_, policyVersion := decisionPolicy("")
	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "queued for sending") {
		t.Errorf("send_message via antenna after full gate flow = %s, want it to pass", out)
	}
}

// TestRegisterDispatchByAgentIDStillWorksWithAntennaConfigured is the
// regression DoD item: an agent that has a DIFFERENT antenna configured
// must still work identically when it presents its plain agent_id — the
// antenna alias is an ADDITION, never a replacement for the existing path.
func TestRegisterDispatchByAgentIDStillWorksWithAntennaConfigured(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	if err := st.UpsertAgent(store.Agent{
		AgentID: "agent-normal", Name: "Normal", Endpoint: "http://x:8787",
		AntennaTerminalID: "antenna-normal", Pinpass: "p", Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}
	chat := "55500000201@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")

	if err := gate.RegisterDispatch("nonce-plain", chat, LevelCaution, "agent-normal", 0, ""); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, "agent-normal")
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-plain"})
	token := unlockToken(t, instr)
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})
	callTool(t, termCtx, srv, "skip", map[string]any{})
	_, policyVersion := decisionPolicy("")
	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "queued for sending") {
		t.Errorf("send_message presenting agent_id (antenna also configured) = %s, want it to pass", out)
	}
}

// TestRegisterDispatchAmbiguousAntennaNeverAliasesSilently is the DoD's
// explicit ambiguity case: two agents sharing one antenna_terminal_id.
// Decided behavior — reject the alias (a terminal presenting that antenna
// resolves to NOTHING, same as any unknown id), never guess which of the
// two agents it means. The dispatch stays reachable by its own agent_id
// either way — this contract only ADDS a path, an ambiguous antenna simply
// doesn't get one.
func TestRegisterDispatchAmbiguousAntennaNeverAliasesSilently(t *testing.T) {
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })

	gate := NewGate()
	st, _, _ := serverWithGate(t, gate)
	sharedAntenna := "antenna-compartida"
	if err := st.UpsertAgent(store.Agent{
		AgentID: "agent-uno", Name: "Uno", AntennaTerminalID: sharedAntenna, Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAgent(store.Agent{
		AgentID: "agent-dos", Name: "Dos", AntennaTerminalID: sharedAntenna, Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}

	if err := gate.RegisterDispatch("nonce-amb", "chat-amb@c.us", LevelCaution, "agent-uno", 0, ""); err != nil {
		t.Fatal(err)
	}

	// The shared antenna resolves to NOTHING — never a silent pick.
	if _, ok := gate.Active(sharedAntenna); ok {
		t.Error("Active(sharedAntenna) with an ambiguous antenna = ok, want nothing bound (never resolve silently)")
	}
	// The dispatch is still reachable by its own agent_id — ambiguity on
	// the antenna path doesn't break the path that always worked.
	if _, ok := gate.Active("agent-uno"); !ok {
		t.Error("Active(\"agent-uno\") after an ambiguous-antenna registration = not ok, want still bound by its own agent_id")
	}
	if !strings.Contains(buf.String(), sharedAntenna) || !strings.Contains(buf.String(), "AMBIGUA") {
		t.Errorf("log = %q, want a visible line naming the ambiguous antenna — silence is the actual defect here (T129)", buf.String())
	}
}

// TestRegisterDispatchUnknownIDStaysRejected is the DoD's own "unchanged"
// control: a terminal id that matches NEITHER any agent_id NOR any
// antenna_terminal_id must be denied exactly like before this contract —
// T129 only ADDS a resolution path, it never widens what counts as known.
func TestRegisterDispatchUnknownIDStaysRejected(t *testing.T) {
	gate := NewGate()
	st, _, _ := serverWithGate(t, gate)
	if err := st.UpsertAgent(store.Agent{
		AgentID: "agent-conocido", Name: "Conocido", AntennaTerminalID: "antenna-conocida", Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}
	if err := gate.RegisterDispatch("nonce-known", "chat-known@c.us", LevelCaution, "agent-conocido", 0, ""); err != nil {
		t.Fatal(err)
	}

	if _, ok := gate.Active("nadie-lo-conoce"); ok {
		t.Error("Active on a completely unknown id = ok, want nothing bound (unchanged by T129)")
	}
}

// TestEvictLockedRemovesAntennaAliasToo guards the leak T129's own dual
// indexing could introduce: a dispatch reclaimed by the stale sweep must
// stop resolving by EITHER key, not just its agent_id — a dangling antenna
// alias would resolve a future terminal to a dead dispatch forever.
func TestEvictLockedRemovesAntennaAliasToo(t *testing.T) {
	gate := NewGate()
	st, _, _ := serverWithGate(t, gate)
	if err := st.UpsertAgent(store.Agent{
		AgentID: "agent-evict", Name: "Evict", AntennaTerminalID: "antenna-evict", Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}
	if err := gate.RegisterDispatch("nonce-evict", "chat-evict@c.us", LevelCaution, "agent-evict", 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := gate.Active("antenna-evict"); !ok {
		t.Fatal("setup: want the antenna alias bound right after registration")
	}

	gate.mu.Lock()
	gate.byNonce["nonce-evict"].lastActivity = time.Now().Add(-dispatchStaleAfter - time.Minute)
	gate.sweepLocked(time.Now())
	gate.mu.Unlock()

	if _, ok := gate.Active("agent-evict"); ok {
		t.Error("Active(agent_id) after sweeping a stale dispatch = ok, want nothing bound")
	}
	if _, ok := gate.Active("antenna-evict"); ok {
		t.Error("Active(antenna) after sweeping a stale dispatch = ok, want nothing bound (dangling alias)")
	}
}

// ── T133 (ct-2026-09-03-0634) — la resolución de antena cubre al principal ──

// TestRegisterDispatchResolvesPrincipalByAntennaWhenDivergent is the bug
// T133 exists for: PrincipalTerminalID (routing, env
// PIUMY_DEFAULT_TERMINAL_ID) and the principal's real antenna (KV, set via
// set_capi_connector/the dashboard) are two independently-set values — "el
// principal funciona por casualidad" was true right up until they diverge.
// A terminal presenting the DIVERGENT antenna, not the routing id, must
// still find its dispatch and complete the full ritual — the exact T129
// shape, but for the fallback every unassigned chat routes to.
func TestRegisterDispatchResolvesPrincipalByAntennaWhenDivergent(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGateAndPrincipal(t, gate)
	if err := st.SetPrincipalAgent("Citrino", "http://192.168.1.77:8788", "principal-antenna-divergente", "pin"); err != nil {
		t.Fatal(err)
	}
	chat := "55500000210@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")

	// capipush registers under PrincipalTerminalID (principalTerm), same as always.
	if err := gate.RegisterDispatch("nonce-principal-antenna", chat, LevelCaution, principalTerm, 0, ""); err != nil {
		t.Fatal(err)
	}

	// The terminal presents the principal's REAL antenna, not its routing id.
	termCtx := withTerminalID(ctx, "principal-antenna-divergente")
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-principal-antenna"})
	token := unlockToken(t, instr)
	if out := callTool(t, termCtx, srv, "unlock", map[string]any{"token": token}); !strings.Contains(out, "unlocked") {
		t.Fatalf("unlock via the principal's antenna = %s, want success", out)
	}
	if out := callTool(t, termCtx, srv, "skip", map[string]any{}); !strings.Contains(out, "ready") {
		t.Fatalf("skip via the principal's antenna = %s, want ready", out)
	}
	_, policyVersion := decisionPolicy("")
	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "queued for sending") {
		t.Errorf("send_message via the principal's antenna after full gate flow = %s, want it to pass", out)
	}
}

// TestRegisterDispatchPrincipalByRoutingIDStillWorksWhenCoincide is the
// regression DoD item: when the principal's routing id and antenna
// coincide (today's normal case, "por casualidad"), presenting the
// routing id must keep working identically — T133 only ADDS a path.
func TestRegisterDispatchPrincipalByRoutingIDStillWorksWhenCoincide(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGateAndPrincipal(t, gate)
	if err := st.SetPrincipalAgent("Citrino", "http://192.168.1.77:8788", principalTerm, "pin"); err != nil {
		t.Fatal(err)
	}
	chat := "55500000211@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")

	if err := gate.RegisterDispatch("nonce-principal-coincide", chat, LevelCaution, principalTerm, 0, ""); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, principalTerm)
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-principal-coincide"})
	token := unlockToken(t, instr)
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})
	callTool(t, termCtx, srv, "skip", map[string]any{})
	_, policyVersion := decisionPolicy("")
	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "queued for sending") {
		t.Errorf("send_message presenting the principal's routing id (coincides with antenna) = %s, want it to pass", out)
	}
}

// TestPrincipalAntennaHijackStillFails is the DoD's own anti-hijack check:
// T133 must not loosen T129's guarantee. A terminal presenting the
// principal's antenna can complete the ritual (proven above) — but a
// DIFFERENT, unrelated terminal must still be unable to consume that same
// dispatch via its nonce, exactly like TestGateCrossTerminalHijackFails
// already proves for the plain case.
func TestPrincipalAntennaHijackStillFails(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGateAndPrincipal(t, gate)
	if err := st.SetPrincipalAgent("Citrino", "http://192.168.1.77:8788", "principal-antenna-hijack", "pin"); err != nil {
		t.Fatal(err)
	}
	chat := "55500000212@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}

	if err := gate.RegisterDispatch("nonce-hijack", chat, LevelCaution, principalTerm, 0, ""); err != nil {
		t.Fatal(err)
	}

	// An unrelated terminal — NOT the principal's routing id, NOT its
	// antenna — must still be refused, same as TestGateCrossTerminalHijackFails.
	attackerCtx := withTerminalID(ctx, "attacker-terminal")
	out := callTool(t, attackerCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-hijack"})
	if !strings.Contains(out, "registered for a different terminal") {
		t.Errorf("an unrelated terminal pulling the principal's dispatch = %s, want a wrong-terminal refusal", out)
	}

	// The principal, presenting its REAL antenna (T133's own new path),
	// still works normally — the anti-hijack check didn't collateral-damage it.
	principalCtx := withTerminalID(ctx, "principal-antenna-hijack")
	instr := callTool(t, principalCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-hijack"})
	if strings.Contains(instr, "not registered") || strings.Contains(instr, "different terminal") {
		t.Errorf("the principal pulling its OWN dispatch via its antenna = %s, want it to succeed", instr)
	}
}

// TestPrincipalAntennaAmbiguousWhenSharedWithSecondaryNeverAliasesSilently:
// if a SECONDARY agent happens to share the principal's own antenna (a real
// misconfiguration, not hypothetical — the whole point of T129/T133 is that
// these fields get edited by hand), the alias is skipped, never guessed —
// same "never resolve silently" rule the secondary-vs-secondary case
// already follows.
func TestPrincipalAntennaAmbiguousWhenSharedWithSecondaryNeverAliasesSilently(t *testing.T) {
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })

	gate := NewGate()
	sharedAntenna := "antenna-compartida-con-principal"
	st, _, _ := serverWithGateAndPrincipal(t, gate)
	if err := st.SetPrincipalAgent("Citrino", "http://192.168.1.77:8788", sharedAntenna, "pin"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAgent(store.Agent{
		AgentID: "agente-secundario", Name: "Secundario", AntennaTerminalID: sharedAntenna, Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}

	if err := gate.RegisterDispatch("nonce-principal-amb", "chat-amb@c.us", LevelCaution, principalTerm, 0, ""); err != nil {
		t.Fatal(err)
	}

	if _, ok := gate.Active(sharedAntenna); ok {
		t.Error("Active(sharedAntenna) with the principal's antenna ALSO claimed by a secondary = ok, want nothing bound (never resolve silently)")
	}
	if _, ok := gate.Active(principalTerm); !ok {
		t.Error("Active(principalTerm) after an ambiguous-antenna registration = not ok, want still bound by its own routing id")
	}
	if !strings.Contains(buf.String(), sharedAntenna) || !strings.Contains(buf.String(), "AMBIGUA") {
		t.Errorf("log = %q, want a visible line naming the ambiguous antenna", buf.String())
	}
}

// ── T147 (ct-2026-09-07) — Consume por identidad, no por terminal ──────────

// TestConsumeIgnoresAMismatchedNonce is T147's core mechanism, reproduced at
// the Gate's own level: a handler reads Active() (as validateSend does),
// validates a dispatch as Ready — then, before it gets to call Consume, a
// NEWER dispatch legitimately replaces it (RegisterDispatch's own
// force-replace, which does not check the previous dispatch's state at
// all). Before this fix, Consume(terminalID) closed "whatever's there now"
// — the newer, never-opened dispatch — silently. This is EXACTLY how the
// owner's own message vanished in T147: validated, replaced, then
// swallowed by a stale Consume call that thought it was closing its own
// turn.
func TestConsumeIgnoresAMismatchedNonce(t *testing.T) {
	gate := NewGate()
	st, _, _ := serverWithGate(t, gate)
	chatOld, chatNew := "555000000041@c.us", "555000000042@c.us"
	if err := st.TouchChat(chatOld, "Old", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat(chatNew, "New", 1); err != nil {
		t.Fatal(err)
	}
	term := "term-mismatch"

	// OLD reaches Ready — exactly the state validateSend requires to pass.
	if err := gate.RegisterDispatch("nonce-old", chatOld, LevelCaution, term, 0, ""); err != nil {
		t.Fatal(err)
	}
	instr, err := gate.GetInstructions(term, "nonce-old", st)
	if err != nil {
		t.Fatal(err)
	}
	if err := gate.Unlock(term, instr.Token); err != nil {
		t.Fatal(err)
	}
	if err := gate.Skip(term); err != nil {
		t.Fatal(err)
	}
	oldActive, ok := gate.Active(term)
	if !ok || !oldActive.Ready {
		t.Fatalf("setup: want OLD bound and ready, got bound=%v ready=%v", ok, oldActive.Ready)
	}

	// A NEWER dispatch legitimately registers for the SAME terminal before
	// OLD's own handler ever reaches Consume — capipush's real
	// RegisterDispatch shape, unmodified.
	if err := gate.RegisterDispatch("nonce-new", chatNew, LevelBoss, term, 0, ""); err != nil {
		t.Fatal(err)
	}

	// OLD's handler finally reaches Consume, using the identity it captured
	// BEFORE the replacement — never re-reading Active().
	gate.Consume(term, oldActive.Nonce)

	// NEW must be untouched: still bound, still openable via its own nonce.
	after, bound := gate.Active(term)
	if !bound {
		t.Fatal("Active(term) after the mismatched Consume = not bound, want NEW still bound")
	}
	if after.ChatJID != chatNew {
		t.Errorf("Active(term).ChatJID after the mismatched Consume = %s, want %s (NEW) — a stale Consume must never close a dispatch it didn't validate", after.ChatJID, chatNew)
	}
	if after.Done {
		t.Error("Active(term).Done after the mismatched Consume = true, want false — NEW was never touched")
	}
	if _, err := gate.GetInstructions(term, "nonce-new", st); err != nil {
		t.Errorf("GetInstructions(nonce-new) after the mismatched Consume = %v, want success — NEW must still be openable, this is the exact loss T147 was born from", err)
	}
}

// TestConsumeStillClosesTheMatchingDispatch is the control for the fix
// above: the ordinary, correct case (Consume called for the SAME dispatch
// Active() reported) must keep working exactly as before.
func TestConsumeStillClosesTheMatchingDispatch(t *testing.T) {
	gate := NewGate()
	st, _, _ := serverWithGate(t, gate)
	chat := "555000000043@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	term := "term-match"
	if err := gate.RegisterDispatch("nonce-match", chat, LevelCaution, term, 0, ""); err != nil {
		t.Fatal(err)
	}
	active, ok := gate.Active(term)
	if !ok {
		t.Fatal("setup: want bound")
	}
	gate.Consume(term, active.Nonce)

	if _, err := gate.GetInstructions(term, "nonce-match", st); err == nil {
		t.Error("GetInstructions after a matching Consume succeeded, want it gone (one-shot)")
	}
	after, bound := gate.Active(term)
	if !bound || !after.Done {
		t.Errorf("Active(term) after a matching Consume = bound=%v done=%v, want bound=true done=true", bound, after.Done)
	}
}

// TestGetInstructionsAfterConsumeSaysConsumedNotUnknown is T147 item 4: a
// nonce that was properly, successfully consumed must say so — not fall
// into the same "unknown nonce" bucket as one that never existed, or one
// force-replaced by a newer dispatch. Three different causes, three
// different messages.
func TestGetInstructionsAfterConsumeSaysConsumedNotUnknown(t *testing.T) {
	gate := NewGate()
	st, _, _ := serverWithGate(t, gate)
	chat := "555000000044@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	term := "term-consumed-reason"
	if err := gate.RegisterDispatch("nonce-cr", chat, LevelCaution, term, 0, ""); err != nil {
		t.Fatal(err)
	}
	gate.Consume(term, "nonce-cr")

	_, err := gate.GetInstructions(term, "nonce-cr", st)
	if err == nil {
		t.Fatal("GetInstructions on a consumed nonce succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "consumed") {
		t.Errorf("GetInstructions error after Consume = %v, want it to say \"consumed\", not guess at force-replace", err)
	}
}

// TestGetInstructionsAfterForceReplaceSaysReplaced is T147 item 4's other
// half: a nonce actually evicted by RegisterDispatch's force-replace must
// say THAT — the one case where "force-replace" as a cause is no longer a
// guess, because the code now knows it for a fact.
func TestGetInstructionsAfterForceReplaceSaysReplaced(t *testing.T) {
	gate := NewGate()
	st, _, _ := serverWithGate(t, gate)
	chatOld, chatNew := "555000000045@c.us", "555000000046@c.us"
	if err := st.TouchChat(chatOld, "Old", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat(chatNew, "New", 1); err != nil {
		t.Fatal(err)
	}
	term := "term-replaced-reason"
	if err := gate.RegisterDispatch("nonce-replaced", chatOld, LevelCaution, term, 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := gate.RegisterDispatch("nonce-replacer", chatNew, LevelCaution, term, 0, ""); err != nil {
		t.Fatal(err)
	}

	_, err := gate.GetInstructions(term, "nonce-replaced", st)
	if err == nil {
		t.Fatal("GetInstructions on a replaced nonce succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "replaced") {
		t.Errorf("GetInstructions error after a force-replace = %v, want it to say \"replaced\"", err)
	}
}

// TestRegisterDispatchClearsStaleRetiredReasonOnNonceReuse is Citrino's own
// audit finding on this same contract (T147, ct-2026-09-07): nonces are 4
// hex chars (65536 values) and retiredReason is never pruned on its own —
// real traffic reuses a nonce value well before a gateway restart would
// clear it (~50% collision odds by ~300 dispatches). This is exactly the
// bug the whole contract exists to kill: a message confidently asserting a
// false cause.
//
// White-box on purpose: EVERY current retirement path (Consume,
// force-replace, sweepLocked, CancelDispatch) already writes its own honest
// reason on its own eviction — so a black-box "register, consume, register
// again, GetInstructions succeeds" passes today with or without this fix
// (byNonce simply finds the fresh dispatch and never even looks at
// retiredReason). What this fix guards is the SECOND life's reason once
// it, in turn, gets retired later — this test reaches into gate.retiredReason
// directly (same package) to prove the FIRST life's leftover entry is gone
// the moment the SECOND life registers, independent of whatever retires the
// second life afterward (including a future path nobody wired into
// retireLocked yet).
func TestRegisterDispatchClearsStaleRetiredReasonOnNonceReuse(t *testing.T) {
	gate := NewGate()
	st, _, _ := serverWithGate(t, gate)
	chatFirst, chatSecond := "555000000047@c.us", "555000000048@c.us"
	if err := st.TouchChat(chatFirst, "First", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat(chatSecond, "Second", 1); err != nil {
		t.Fatal(err)
	}
	term := "term-reuse"
	const reused = "ab12"

	// First life: register, consume for real.
	if err := gate.RegisterDispatch(reused, chatFirst, LevelCaution, term, 0, ""); err != nil {
		t.Fatal(err)
	}
	gate.Consume(term, reused)

	// Confirm the "consumed" reason is on record — the setup this test
	// actually needs to matter.
	gate.mu.Lock()
	firstLifeReason, recorded := gate.retiredReason[reused]
	gate.mu.Unlock()
	if !recorded || !strings.Contains(firstLifeReason, "consumed") {
		t.Fatalf("setup: retiredReason[%q] after the first life's Consume = %q, recorded=%v — want a \"consumed\" reason on record", reused, firstLifeReason, recorded)
	}

	// Second life: the SAME 4-hex value, reused for a brand-new, unrelated
	// dispatch — a real collision, not a hypothetical one.
	if err := gate.RegisterDispatch(reused, chatSecond, LevelCaution, term, 0, ""); err != nil {
		t.Fatal(err)
	}

	// THE assertion: the first life's leftover reason must be gone the
	// instant the nonce is reused, before anything ever retires the SECOND
	// life — this is what "delete(g.retiredReason, nonce)" in
	// RegisterDispatch buys, and nothing else in this test would catch its
	// absence.
	gate.mu.Lock()
	_, stillThere := gate.retiredReason[reused]
	gate.mu.Unlock()
	if stillThere {
		t.Errorf("retiredReason[%q] still holds the FIRST life's reason (%q) right after the SECOND life registered — a later eviction of the second life could report this stale, false cause", reused, firstLifeReason)
	}

	if _, err := gate.GetInstructions(term, reused, st); err != nil {
		t.Fatalf("GetInstructions on the reused nonce's second life = %v, want success", err)
	}
	active, bound := gate.Active(term)
	if !bound || active.ChatJID != chatSecond {
		t.Errorf("Active(term) after reuse = bound=%v chat=%s, want bound=true chat=%s (the SECOND life, not a ghost of the first)", bound, active.ChatJID, chatSecond)
	}
}

// TestCancelDispatchRetiresItsOwnReason is item 2 of the same audit:
// CancelDispatch (capipush.go's own H5 hardening path, live in production
// when Inject fails) evicted a dispatch WITHOUT recording why — a later
// get_instructions on that exact nonce fell into the generic "never
// registered" bucket, which is wrong: it briefly WAS. Cancellation is
// useful, actionable information ("wait for the automatic retry"), not
// nothing.
func TestCancelDispatchRetiresItsOwnReason(t *testing.T) {
	gate := NewGate()
	st, _, _ := serverWithGate(t, gate)
	chat := "555000000049@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	term := "term-cancelled"
	if err := gate.RegisterDispatch("nonce-cancelled", chat, LevelCaution, term, 0, ""); err != nil {
		t.Fatal(err)
	}
	gate.CancelDispatch("nonce-cancelled", term)

	_, err := gate.GetInstructions(term, "nonce-cancelled", st)
	if err == nil {
		t.Fatal("GetInstructions on a cancelled nonce succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("GetInstructions error after CancelDispatch = %v, want it to say \"cancelled\", not fall back to the generic never-registered wording", err)
	}
}
