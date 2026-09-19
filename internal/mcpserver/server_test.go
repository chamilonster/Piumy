package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"piumy-gateway/internal/router"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
	"piumy-gateway/internal/version"
)

// serverWithRouter builds a server against a caller-supplied router.json
// path — newTestServer's throwaway path is fine for tests that don't care
// about the whitelist, but the guardrail tests below need real content on
// disk before router.NewManager reads it.
func serverWithRouter(t *testing.T, dir, routerPath string) (*store.Store, *server.MCPServer, context.Context, *Gate) {
	t.Helper()
	st, err := store.Open(filepath.Join(dir, "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	rtMgr := router.NewManager(routerPath)
	sm := state.NewManager(filepath.Join(dir, "status.json"), 8)
	gate := NewGate()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	srv := New(ctx, Deps{Store: st, State: sm, Router: rtMgr, AgentIdle: time.Minute, Gate: gate})
	return st, srv, ctx, gate
}

// serverWithAuth builds a server with MCPAuthConfigured=true, simulating
// PIUMY_MCP_KEY being set (mcpserver never sees the actual secret).
func serverWithAuth(t *testing.T, dir string) (*store.Store, *server.MCPServer, context.Context, *Gate) {
	t.Helper()
	st, err := store.Open(filepath.Join(dir, "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	rtMgr := router.NewManager(filepath.Join(dir, "router.json"))
	sm := state.NewManager(filepath.Join(dir, "status.json"), 8)
	gate := NewGate()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	srv := New(ctx, Deps{Store: st, State: sm, Router: rtMgr, AgentIdle: time.Minute, MCPAuthConfigured: true, Gate: gate})
	return st, srv, ctx, gate
}

func TestGetChatToolNotFound(t *testing.T) {
	_, srv, ctx, gate := newTestServer(t)
	termCtx := bossDispatchContext(t, gate, srv, ctx, "nobody@c.us")
	out := callTool(t, termCtx, srv, "get_chat", map[string]any{"chat_id": "nobody@c.us"})
	if !strings.Contains(out, "chat not found") {
		t.Errorf("get_chat on unknown JID = %s, want a not-found error", out)
	}
}

// TestGetChatGroupsReturnsRealData is T18B (ct-2026-08-05-1243) — before
// this, get_chat_groups always returned empty in any real installation
// (its backing table, chat_groups, had no production writer). Now it reads
// group_members, the same table whatsmeow.seedGroups actually populates.
func TestGetChatGroupsReturnsRealData(t *testing.T) {
	st, srv, ctx, gate := newTestServer(t)
	member := "111@c.us"
	if err := st.UpsertGroupMember("g1@g.us", member, "Alice", 1); err != nil {
		t.Fatal(err)
	}
	termCtx := bossDispatchContext(t, gate, srv, ctx, member)

	out := callTool(t, termCtx, srv, "get_chat_groups", map[string]any{"chat_id": member})
	if !strings.Contains(out, "g1@g.us") {
		t.Errorf("get_chat_groups(%s) = %s, want g1@g.us listed", member, out)
	}
}

func TestDecisionPolicyHashStable(t *testing.T) {
	c1, v1 := decisionPolicy("")
	c2, v2 := decisionPolicy("")
	if v1 != v2 || c1 != c2 {
		t.Errorf("decisionPolicy(\"\") not stable across calls: v1=%q v2=%q", v1, v2)
	}
	if v1 == "" {
		t.Error("policy_version is empty, want a real sha256 hash")
	}
}

func TestGetDecisionPolicyTool(t *testing.T) {
	_, srv, ctx, _ := newTestServer(t)
	out := callTool(t, ctx, srv, "get_decision_policy", map[string]any{})
	_, wantVersion := decisionPolicy("")
	if !strings.Contains(out, wantVersion) {
		t.Errorf("get_decision_policy = %s, want policy_version %q in it", out, wantVersion)
	}
}

// TestGetManualTool (ct-2026-07-31-1541): never gated, same as
// get_decision_policy above — no dispatch bound at all here, and it still
// has to work, since an agent needs its manual BEFORE it has any work
// assigned.
func TestGetManualTool(t *testing.T) {
	_, srv, ctx, _ := newTestServer(t)
	wantStamp := "<!-- piumy-skill-version: " + version.Version + " -->"

	orch := callTool(t, ctx, srv, "get_manual", map[string]any{"role": "orchestrator"})
	for _, want := range []string{"Piumy — el que conduce", "Círculo cercano", "quién aprueba", "arrancar", "Dirección"} {
		if !strings.Contains(orch, want) {
			t.Errorf("get_manual(orchestrator) missing %q — want the SKILL/escenarios/perillas/operacion/direccion content joined", want)
		}
	}
	if !strings.Contains(decodeToolText(t, orch), wantStamp) {
		t.Errorf("get_manual(orchestrator) missing version stamp %q", wantStamp)
	}

	op := callTool(t, ctx, srv, "get_manual", map[string]any{"role": "operator"})
	if !strings.Contains(op, "Piumy — operador") {
		t.Errorf("get_manual(operator) = %s, want the operator manual content", op)
	}
	if strings.Contains(op, "Círculo cercano") {
		t.Error("get_manual(operator) contains orchestrator content — roles must not bleed into each other")
	}
	if !strings.Contains(decodeToolText(t, op), wantStamp) {
		t.Errorf("get_manual(operator) missing version stamp %q", wantStamp)
	}

	conn := callTool(t, ctx, srv, "get_manual", map[string]any{"role": "connect"})
	for _, want := range []string{"Piumy — conectarse", "agent-connect.json", "capi_power", "register_agent", "capi-connector-line", "capi-ping"} {
		if !strings.Contains(conn, want) {
			t.Errorf("get_manual(connect) missing %q", want)
		}
	}
	if strings.Contains(conn, "Círculo cercano") || strings.Contains(conn, "Piumy — operador") {
		t.Error("get_manual(connect) contains orchestrator/operator content — roles must not bleed into each other")
	}
	if !strings.Contains(decodeToolText(t, conn), wantStamp) {
		t.Errorf("get_manual(connect) missing version stamp %q", wantStamp)
	}

	out := callTool(t, ctx, srv, "get_manual", map[string]any{"role": "bogus"})
	if !strings.Contains(out, "unknown role") {
		t.Errorf("get_manual(bogus) = %s, want an unknown-role refusal", out)
	}
}

// TestGetManualVersionStampTracksVersion (ct-2026-08-07, sello de versión):
// guards against the stamp being hardcoded instead of read live from
// version.Version — flips the package var mid-test (it's a var, not a
// const, exactly so build-all.sh's -ldflags -X can set it) and checks the
// served manual's stamp follows.
func TestGetManualVersionStampTracksVersion(t *testing.T) {
	_, srv, ctx, _ := newTestServer(t)

	original := version.Version
	version.Version = "9.9.9-test"
	t.Cleanup(func() { version.Version = original })

	out := callTool(t, ctx, srv, "get_manual", map[string]any{"role": "operator"})
	if text := decodeToolText(t, out); !strings.Contains(text, "<!-- piumy-skill-version: 9.9.9-test -->") {
		t.Errorf("get_manual(operator) = %s, want it to reflect the overridden version.Version", text)
	}
}

// TestResolveChatAllowedReflectsChatIsOff is T67 (ct-2026-08-11-172135):
// resolve_chat's "allowed" field used to be the router whitelist's
// permission (dec.Allowed) — T65 removed the three gates that enforced it,
// so an agent reading allowed:false from a chat that simply was never
// whitelisted (but isn't ignored/blacklisted) would wrongly conclude it
// couldn't write there, when send_message would have let it through. allowed
// now mirrors store.ChatIsOff — the SAME definition send_message itself
// enforces, so the two can't disagree.
func TestResolveChatAllowedReflectsChatIsOff(t *testing.T) {
	dir := t.TempDir()
	st, srv, ctx, gate := serverWithRouter(t, dir, filepath.Join(dir, "router.json"))
	// Empty router.json — no whitelist, allow_all=false: under the OLD
	// field this would have read allowed:false for every case below.

	if err := st.TouchChat("555000201@s.whatsapp.net", "NeverWhitelisted", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("555000202@s.whatsapp.net", "Blacklisted", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatus("555000202@s.whatsapp.net", "blacklist"); err != nil {
		t.Fatal(err)
	}

	// resolve_chat is chat-scoped (levelgate.go's chatScopedArg) — it needs
	// its own active dispatch like send_message does, one fresh boss
	// dispatch per call (single-use, ST-A).
	call := func(chatID string) string {
		termCtx := bossDispatchContext(t, gate, srv, ctx, chatID)
		return callTool(t, termCtx, srv, "resolve_chat", map[string]any{"chat_id": chatID})
	}

	out := decodeToolText(t, call("555000201@s.whatsapp.net"))
	if !strings.Contains(out, `"allowed": true`) {
		t.Errorf("resolve_chat(never whitelisted, not off) = %s, want allowed: true", out)
	}

	out2 := decodeToolText(t, call("555000202@s.whatsapp.net"))
	if !strings.Contains(out2, `"allowed": false`) {
		t.Errorf("resolve_chat(blacklisted) = %s, want allowed: false", out2)
	}

	// A chat never touched at all (no store row) — also allowed, same
	// "absence isn't a block" convention as everywhere else in this package.
	out3 := decodeToolText(t, call("555000203@s.whatsapp.net"))
	if !strings.Contains(out3, `"allowed": true`) {
		t.Errorf("resolve_chat(never touched) = %s, want allowed: true", out3)
	}
}

// TestSendMessageGuardrails covers the 6 checks ported from Piumy: bare
// number rejection, the no-rules law, and the ignored/blacklist status gate
// — open-wa JID format (@c.us/@g.us). A boss dispatch clears the F4b gate so
// these pre-existing checks stay reachable.
func TestSendMessageGuardrails(t *testing.T) {
	dir := t.TempDir()
	routerPath := filepath.Join(dir, "router.json")
	cfg := `{"default_mode":"dedicated","whitelist":["55500000044@c.us","12345-67890@g.us","99999-11111@g.us"]}`
	if err := os.WriteFile(routerPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	st, srv, ctx, gate := serverWithRouter(t, dir, routerPath)

	_, policyVersion := decisionPolicy("")

	if err := st.TouchChat("55500000044@c.us", "Boss", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatRules("55500000044@c.us", "responder normalmente"); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("12345-67890@g.us", "Group", 1); err != nil {
		t.Fatal(err) // defaults to status "ignored"
	}
	if err := st.SetChatRules("12345-67890@g.us", "solo contestar si preguntan"); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("99999-11111@g.us", "ActiveGroup", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatRules("99999-11111@g.us", "solo contestar si preguntan"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatus("99999-11111@g.us", "whitelist"); err != nil {
		t.Fatal(err)
	}
	// This test is about the ignored-status gate specifically — a group's
	// confirmation_mode default (now "always", see
	// TestFreshGroupDefaultHoldsForConfirmation) would otherwise hold this
	// one for confirmation instead of sending, which isn't what's under test here.
	if err := st.SetConfirmationMode("99999-11111@g.us", "none"); err != nil {
		t.Fatal(err)
	}
	// T65 (ct-2026-08-11-1642): ignored/blacklist used to gate ONLY groups
	// (isGroupJID(to) && c.Status == "ignored") — a 1:1 the owner blacklisted
	// or ignored still got sent to, exactly the bug the owner reported
	// ("para algo esta ignorar, eso ya apaga el chat" — it didn't). These two
	// 1:1 chats prove the fix: the gate now applies with no group condition.
	if err := st.TouchChat("55500000120@c.us", "Blacklisted1to1", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatRules("55500000120@c.us", "responder normalmente"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatus("55500000120@c.us", "blacklist"); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("55500000121@c.us", "Ignored1to1", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatRules("55500000121@c.us", "responder normalmente"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatus("55500000121@c.us", "ignored"); err != nil {
		t.Fatal(err)
	}

	// ST-A (ct-2026-07-11-0740): boss dispatches are now single-use like
	// every other level (Ready goes false on Consume) — a fresh dispatch
	// per case keeps each guardrail check reachable instead of every case
	// after the first successful send hitting "already consumed".
	call := func(to string) string {
		termCtx := bossDispatchContext(t, gate, srv, ctx, "any@c.us")
		return callTool(t, termCtx, srv, "send_message", map[string]any{
			"to": to, "message": "hola", "model": "claude-opus-4-8", "policy_version": policyVersion,
		})
	}

	cases := []struct {
		name    string
		to      string
		wantSub string
	}{
		{"no rules at all blocked", "55500000109@c.us", "no rules on this chat"},
		{"group with rules but still ignored blocked", "12345-67890@g.us", "is ignored"},
		{"group with rules and not ignored passes", "99999-11111@g.us", "queued for sending"},
		{"bare number blocked", "55500000044", "full JID"},
		{"whitelisted passes", "55500000044@c.us", "queued for sending"},
		{"1:1 blacklisted blocked (used to be group-only)", "55500000120@c.us", "is blacklist"},
		{"1:1 ignored blocked (used to be group-only)", "55500000121@c.us", "is ignored"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if out := call(c.to); !strings.Contains(out, c.wantSub) {
				t.Errorf("send_message(to=%q) = %s, want substring %q", c.to, out, c.wantSub)
			}
		})
	}
}

// TestSendMessageAllowedViaEffectiveRules: a chat with no particular rules
// still sends if it inherits the global default.
func TestSendMessageAllowedViaEffectiveRules(t *testing.T) {
	dir := t.TempDir()
	jid := "55500000111@c.us"
	routerPath := filepath.Join(dir, "router.json")
	if err := os.WriteFile(routerPath, []byte(`{"default_mode":"dedicated","whitelist":["`+jid+`"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st, srv, ctx, gate := serverWithRouter(t, dir, routerPath)

	_, policyVersion := decisionPolicy("")
	if err := st.TouchChat(jid, "NoParticularRules", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "default: responder con cortesía")
	termCtx := bossDispatchContext(t, gate, srv, ctx, jid)

	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": jid, "message": "hola", "model": "claude-opus-4-8", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "queued for sending") {
		t.Errorf("send_message with only default rules = %s, want it allowed", out)
	}
}

func TestSendMessageRequiresCurrentPolicyVersion(t *testing.T) {
	jid := "55500000044@c.us"
	dir := t.TempDir()
	routerPath := filepath.Join(dir, "router.json")
	if err := os.WriteFile(routerPath, []byte(`{"default_mode":"dedicated","whitelist":["`+jid+`"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st, srv, ctx, gate := serverWithRouter(t, dir, routerPath)

	if err := st.TouchChat(jid, "Boss", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatRules(jid, "responder normalmente"); err != nil {
		t.Fatal(err)
	}
	_, currentVersion := decisionPolicy("")
	termCtx := bossDispatchContext(t, gate, srv, ctx, jid)

	t.Run("missing policy_version rejected", func(t *testing.T) {
		args := map[string]any{"to": jid, "message": "hola", "model": "claude-opus-4-8"}
		if out := callTool(t, termCtx, srv, "send_message", args); !strings.Contains(out, "policy_version") {
			t.Errorf("got %s, want a policy_version error", out)
		}
	})
	t.Run("stale policy_version rejected", func(t *testing.T) {
		args := map[string]any{"to": jid, "message": "hola", "model": "claude-opus-4-8", "policy_version": "stale"}
		if out := callTool(t, termCtx, srv, "send_message", args); !strings.Contains(out, "stale/missing policy_version") {
			t.Errorf("got %s, want stale rejection", out)
		}
	})
	t.Run("current policy_version passes", func(t *testing.T) {
		args := map[string]any{"to": jid, "message": "hola", "model": "claude-opus-4-8", "policy_version": currentVersion}
		if out := callTool(t, termCtx, srv, "send_message", args); !strings.Contains(out, "queued for sending") {
			t.Errorf("got %s, want it to pass", out)
		}
	})
}

func TestClaimAndReleaseChat(t *testing.T) {
	_, srv, ctx, gate := newTestServer(t)
	jid := "55500000002@c.us"
	termCtx := bossDispatchContext(t, gate, srv, ctx, jid)

	// claim_chat requires the chat to already exist.
	if out := callTool(t, termCtx, srv, "claim_chat", map[string]any{"chat_id": jid, "model": "claude"}); !strings.Contains(out, "chat not found") {
		t.Fatalf("claim on a never-touched chat = %s, want chat not found", out)
	}

	callTool(t, termCtx, srv, "set_chat_status", map[string]any{"chat_id": jid, "status": "whitelist"})

	out := callTool(t, termCtx, srv, "claim_chat", map[string]any{"chat_id": jid, "model": "claude-a"})
	if !strings.Contains(out, "claimed until") {
		t.Fatalf("claim_chat = %s, want success", out)
	}

	blocked := callTool(t, termCtx, srv, "claim_chat", map[string]any{"chat_id": jid, "model": "claude-b"})
	if !strings.Contains(blocked, "claimed by claude-a") {
		t.Errorf("second model's claim = %s, want it blocked naming the holder", blocked)
	}

	callTool(t, termCtx, srv, "release_chat", map[string]any{"chat_id": jid, "model": "claude-a"})
	freed := callTool(t, termCtx, srv, "claim_chat", map[string]any{"chat_id": jid, "model": "claude-b"})
	if !strings.Contains(freed, "claimed until") {
		t.Errorf("claim after release = %s, want it to succeed", freed)
	}
}

// TestSetChatStatusMarksConfigManualExceptAgentExclusive (M5, ct-2026-07-22-1903):
// set_chat_status marks config_level_source 'manual' for a real triage value
// (ignored/whitelist/blacklist/new) — freezing it against a later origin
// default — but NOT for agent_exclusive:<id>, a different axis (M3/M4).
func TestSetChatStatusMarksConfigManualExceptAgentExclusive(t *testing.T) {
	st, srv, ctx, gate := newTestServer(t)
	if err := st.KVSet(store.SettingConfigLevelDefaultContact, "auto"); err != nil {
		t.Fatal(err)
	}

	ignored := "1@s.whatsapp.net"
	if err := st.TouchChat(ignored, "Alguien", 1); err != nil {
		t.Fatal(err)
	}
	termCtx := bossDispatchContext(t, gate, srv, ctx, ignored)
	callTool(t, termCtx, srv, "set_chat_status", map[string]any{"chat_id": ignored, "status": "ignored"})
	if err := st.SetContactName(ignored, "Juan Pérez"); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := st.GetChat(ignored); c.Status != "ignored" {
		t.Errorf("Status = %q after a later contact sync, want ignored to survive (set_chat_status must mark manual)", c.Status)
	}

	assigned := "2@s.whatsapp.net"
	if err := st.TouchChat(assigned, "Otro", 1); err != nil {
		t.Fatal(err)
	}
	termCtx2 := bossDispatchContext(t, gate, srv, ctx, assigned)
	callTool(t, termCtx2, srv, "set_chat_status", map[string]any{"chat_id": assigned, "status": "agent_exclusive:some-agent"})
	if err := st.SetContactName(assigned, "Juan Pérez"); err != nil {
		t.Fatal(err)
	}
	c, _, _ := st.GetChat(assigned)
	if c.Status != "agent_exclusive:some-agent" {
		t.Fatalf("Status = %q, want the agent_exclusive assignment to survive too (SetContactName only touches active/confirmation_mode, never status here)", c.Status)
	}
	if !c.Active {
		t.Errorf("Active = false after contact sync, want true (auto contact default) — agent_exclusive must NOT have marked config_level_source manual")
	}
}

// TestResetDashboardPasswordRequiresAuthConfigured covers the fail-closed
// owner-scoping: no PIUMY_MCP_KEY configured means no owner boundary, so
// the tool refuses outright. A boss dispatch clears the (separate) F4b
// gate so this test reaches that check specifically, not default-DENY.
func TestResetDashboardPasswordRequiresAuthConfigured(t *testing.T) {
	_, srv, ctx, gate := newTestServer(t)
	termCtx := bossDispatchContext(t, gate, srv, ctx, "any@c.us")
	out := callTool(t, termCtx, srv, "reset_dashboard_password", map[string]any{})
	if !strings.Contains(out, "PIUMY_MCP_KEY is not set") {
		t.Errorf("got %s, want refusal for unconfigured auth", out)
	}
}

func TestResetDashboardPasswordGeneratesAndStores(t *testing.T) {
	dir := t.TempDir()
	st, srv, ctx, gate := serverWithAuth(t, dir)
	termCtx := bossDispatchContext(t, gate, srv, ctx, "any@c.us")

	out := callTool(t, termCtx, srv, "reset_dashboard_password", map[string]any{})
	if strings.Contains(out, "error") || strings.Contains(out, "refused") {
		t.Fatalf("got %s, want a generated password", out)
	}
	hash, err := st.KVGet(store.SettingDashPassHash)
	if err != nil || hash == "" {
		t.Errorf("dash_pass_hash not stored: hash=%q err=%v", hash, err)
	}
}

// TestResetDashboardPasswordRotatesSessionSecret: ct-2026-07-19-1616 (S1d) —
// an emergency reset via MCP must end every existing browser session too,
// same invariant the dashboard's own POST /api/admin/password enforces.
func TestResetDashboardPasswordRotatesSessionSecret(t *testing.T) {
	dir := t.TempDir()
	st, srv, ctx, gate := serverWithAuth(t, dir)
	termCtx := bossDispatchContext(t, gate, srv, ctx, "any@c.us")

	before, _ := st.KVGet(store.SettingDashSessionSecret)

	out := callTool(t, termCtx, srv, "reset_dashboard_password", map[string]any{})
	if strings.Contains(out, "error") || strings.Contains(out, "refused") {
		t.Fatalf("got %s, want a generated password", out)
	}
	after, err := st.KVGet(store.SettingDashSessionSecret)
	if err != nil || after == "" {
		t.Fatalf("dash_session_secret not stored: secret=%q err=%v", after, err)
	}
	if before == after {
		t.Error("reset_dashboard_password did not rotate dash_session_secret — old browser sessions would survive an emergency reset")
	}
}

// TestGetStatusReportsRealVersion is the version-unification regression
// (ct-2026-08-06): get_status is the one tool an agent can call without an
// active dispatch (levelgate.go), so it's what CleverCoder reads on
// connect to compare against the repo's VERSION. A hardcoded literal here
// silently drifting from VERSION is exactly the bug that motivated
// internal/version — this pins the two together.
func TestGetStatusReportsRealVersion(t *testing.T) {
	_, srv, ctx, _ := newTestServer(t)
	out := callTool(t, ctx, srv, "get_status", map[string]any{})

	var envelope struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("decoding get_status envelope: %v (raw: %s)", err, out)
	}
	if len(envelope.Result.Content) == 0 {
		t.Fatalf("get_status returned no content: %s", out)
	}
	var status struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &status); err != nil {
		t.Fatalf("decoding get_status payload: %v (raw: %s)", err, envelope.Result.Content[0].Text)
	}
	if status.Version != version.Version {
		t.Errorf("get_status version = %q, want %q (version.Version)", status.Version, version.Version)
	}
}

// ── T122 (ct-2026-09-02-2045) — get_drafts media two-tier shape ─────────

// TestGetDraftsCheapListingOmitsImageBytes: the default listing must NOT
// embed the image — only media_mime/media_size, cheap the same way
// get_media's own default listing is (media_tools.go's mediaSummary).
func TestGetDraftsCheapListingOmitsImageBytes(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "555000050@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMediaDraftWithConfirmer(chat, "una foto", "m", "", "",
		"/tmp/does-not-need-to-exist.jpg", "image/jpeg", "photo", 0, 0, 1); err != nil {
		t.Fatal(err)
	}
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)

	out := callTool(t, termCtx, srv, "get_drafts", map[string]any{})
	if !strings.Contains(out, "media_mime") || !strings.Contains(out, "image/jpeg") {
		t.Errorf("get_drafts cheap listing = %s, want media_mime=image/jpeg present", out)
	}
	if strings.Contains(out, "media_data_url") {
		t.Errorf("get_drafts cheap listing = %s, want NO media_data_url — that's draft_id-only", out)
	}
}

// TestGetDraftsTextOnlyOmitsMediaFieldsEntirely: a plain draft's JSON has
// no media_mime/media_kind/media_size at all (omitempty) — the cheap-
// listing change must be invisible for the common, non-photo case.
func TestGetDraftsTextOnlyOmitsMediaFieldsEntirely(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "555000051@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.AddDraft(chat, "solo texto", "m", 1); err != nil {
		t.Fatal(err)
	}
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)

	out := callTool(t, termCtx, srv, "get_drafts", map[string]any{})
	for _, key := range []string{"media_mime", "media_kind", "media_size", "media_data_url"} {
		if strings.Contains(out, key) {
			t.Errorf("get_drafts for a text-only draft = %s, want no %q key at all", out, key)
		}
	}
}

// TestGetDraftsWithDraftIDEmbedsDataURLAndMeters: passing draft_id fetches
// the photo as a data_url and charges the SAME image usage cost
// get_media_full charges for an original — one real expense, counted once,
// whichever door it's read through.
func TestGetDraftsWithDraftIDEmbedsDataURLAndMeters(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "555000052@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "photo.jpg")
	photoBytes := []byte("bytes de una foto")
	if err := os.WriteFile(path, photoBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMediaDraftWithConfirmer(chat, "una foto", "m", "", "",
		path, "image/jpeg", "photo", 0, 0, 1); err != nil {
		t.Fatal(err)
	}
	drafts, err := st.PendingDrafts(10)
	if err != nil || len(drafts) != 1 {
		t.Fatalf("setup: PendingDrafts = %+v, err=%v, want 1", drafts, err)
	}
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)

	out := callTool(t, termCtx, srv, "get_drafts", map[string]any{"draft_id": drafts[0].ID})
	wantB64 := base64.StdEncoding.EncodeToString(photoBytes)
	if !strings.Contains(out, wantB64) {
		t.Errorf("get_drafts(draft_id=%d) = %s, want the photo's base64 embedded", drafts[0].ID, out)
	}
	if !strings.Contains(out, "data:image/jpeg;base64,") {
		t.Errorf("get_drafts(draft_id=%d) = %s, want a proper data: URL prefix", drafts[0].ID, out)
	}

	u, err := st.UsageForDay(chat, store.Today())
	if err != nil {
		t.Fatal(err)
	}
	if u.Images != 1 || u.Audio != 0 {
		t.Errorf("usage after get_drafts(draft_id) on a photo draft = %+v, want images=1 audio=0", u)
	}
}

// TestGetDraftsWithDraftIDMetersAudioNotImages is T123's own regression
// (ct-2026-09-02-2121): fetching an AUDIO draft's data_url must charge
// Audio usage, not Images — store.UsageDelta has a separate counter for
// exactly this, and get_drafts must pick the same axis
// corepipeline.sendMediaItem does.
func TestGetDraftsWithDraftIDMetersAudioNotImages(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "555000054@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "voice.ogg")
	if err := os.WriteFile(path, []byte("bytes de un audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMediaDraftWithConfirmer(chat, "", "m", "", "",
		path, "audio/ogg; codecs=opus", "audio", 9, 0, 1); err != nil {
		t.Fatal(err)
	}
	drafts, err := st.PendingDrafts(10)
	if err != nil || len(drafts) != 1 {
		t.Fatalf("setup: PendingDrafts = %+v, err=%v, want 1", drafts, err)
	}
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)

	callTool(t, termCtx, srv, "get_drafts", map[string]any{"draft_id": drafts[0].ID})

	u, err := st.UsageForDay(chat, store.Today())
	if err != nil {
		t.Fatal(err)
	}
	if u.Audio != 1 || u.Images != 0 {
		t.Errorf("usage after get_drafts(draft_id) on an audio draft = %+v, want audio=1 images=0", u)
	}
}

// TestGetDraftsMissingDraftIDReturnsError: an id that doesn't exist must
// error legibly, never a silent empty success.
func TestGetDraftsMissingDraftIDReturnsError(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "555000053@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)

	out := callTool(t, termCtx, srv, "get_drafts", map[string]any{"draft_id": 999999})
	if !strings.Contains(out, "draft not found") {
		t.Errorf("get_drafts(draft_id=999999) = %s, want a legible not-found error", out)
	}
}
