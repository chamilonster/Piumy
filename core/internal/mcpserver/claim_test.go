// SPDX-License-Identifier: AGPL-3.0-only
package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pimywa/internal/governor"
	"pimywa/internal/router"
	"pimywa/internal/state"
	"pimywa/internal/store"
)

// newClaimTestServer builds a real MCP server plus a whitelisted, ruled chat
// ready for send_message — the same fixture shape send_message_test.go uses,
// factored out here since claim tests need it repeatedly.
func newClaimTestServer(t *testing.T, claimTTLDefault time.Duration) (*store.Store, func(tool string, args map[string]any) string, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	jid := "56911112222@s.whatsapp.net"
	routerPath := filepath.Join(dir, "router.json")
	if err := os.WriteFile(routerPath, []byte(`{"allow_all":false,"default_mode":"advanced","whitelist":["`+jid+`"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rtMgr := router.NewManager(routerPath)
	sm := state.NewManager(filepath.Join(dir, "status.json"), 8)
	gov := governor.NewLimiter(1000, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	mcpSrv := New(ctx, Deps{Store: st, State: sm, Router: rtMgr, Gov: gov, AgentIdle: time.Minute, ClaimTTLDefault: claimTTLDefault})

	if err := st.TouchChat(jid, "Test", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDefaultRules("responder normalmente"); err != nil {
		t.Fatal(err)
	}

	call := func(tool string, args map[string]any) string {
		req := map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": tool, "arguments": args},
		}
		raw, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		resp := mcpSrv.HandleMessage(ctx, raw)
		out, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	return st, call, jid
}

func sendArgs(jid, model, policyVersion string) map[string]any {
	return map[string]any{"to": jid, "message": "hola", "model": model, "policy_version": policyVersion}
}

// TestClaimChatToolBasics covers claim_chat end-to-end via the real
// registered tool: unclaimed succeeds, a different model is refused with a
// clear "claimed by / until" error.
func TestClaimChatToolBasics(t *testing.T) {
	_, call, jid := newClaimTestServer(t, time.Minute)

	out := call("claim_chat", map[string]any{"chat_id": jid, "model": "claude-opus-4-8"})
	if !strings.Contains(out, "claimed until") {
		t.Fatalf("1st claim = %s, want success", out)
	}

	out = call("claim_chat", map[string]any{"chat_id": jid, "model": "deepseek-chat"})
	if !strings.Contains(out, "claimed by claude-opus-4-8") {
		t.Errorf("2nd claim by a different model = %s, want a 'claimed by claude-opus-4-8 until ...' error", out)
	}
}

// TestClaimChatToolUnknownChat covers the "chat not found" distinction: a
// claim on a chat that was never touched must not be confused with
// "claimed by someone else".
func TestClaimChatToolUnknownChat(t *testing.T) {
	_, call, _ := newClaimTestServer(t, time.Minute)
	out := call("claim_chat", map[string]any{"chat_id": "56900000000@s.whatsapp.net", "model": "claude-opus-4-8"})
	if !strings.Contains(out, "chat not found") {
		t.Errorf("claim on an unknown chat = %s, want 'chat not found'", out)
	}
}

// TestClaimChatToolTTLCeiling covers the hard ceiling: a caller cannot
// request a longer lock than claimTTLCeiling, no matter what ttl_sec says.
func TestClaimChatToolTTLCeiling(t *testing.T) {
	st, call, jid := newClaimTestServer(t, time.Minute)
	// Way past the 30-minute ceiling.
	call("claim_chat", map[string]any{"chat_id": jid, "model": "claude-opus-4-8", "ttl_sec": 999999})

	c, _, err := st.GetChat(jid)
	if err != nil {
		t.Fatal(err)
	}
	maxAllowed := time.Now().Add(claimTTLCeiling + time.Minute).Unix() // +1min slack for test execution time
	if c.ClaimedUntil > maxAllowed {
		t.Errorf("claimed_until = %d, want capped at ~claimTTLCeiling (%s) from now, not the requested 999999s", c.ClaimedUntil, claimTTLCeiling)
	}
}

// TestSendMessageBlockedByForeignClaim is the core guarantee:
// send_message refuses when the chat is claimed by a DIFFERENT model, and
// succeeds once that model releases it.
func TestSendMessageBlockedByForeignClaim(t *testing.T) {
	_, call, jid := newClaimTestServer(t, time.Minute)
	_, policyVersion := decisionPolicy("")

	call("claim_chat", map[string]any{"chat_id": jid, "model": "claude-opus-4-8"})

	out := call("send_message", sendArgs(jid, "deepseek-chat", policyVersion))
	if !strings.Contains(out, "claimed by another agent") {
		t.Fatalf("send_message from a non-claiming model = %s, want refused as claimed", out)
	}

	// The claim holder itself can still send — a claim never blocks its own owner.
	out = call("send_message", sendArgs(jid, "claude-opus-4-8", policyVersion))
	if !strings.Contains(out, "queued for sending") {
		t.Errorf("send_message from the claim holder = %s, want it to succeed", out)
	}

	call("release_chat", map[string]any{"chat_id": jid, "model": "claude-opus-4-8"})
	out = call("send_message", sendArgs(jid, "deepseek-chat", policyVersion))
	if !strings.Contains(out, "queued for sending") {
		t.Errorf("send_message after release = %s, want it to succeed now", out)
	}
}

// TestSendMessageUnaffectedWithoutClaim is THE regression guard for "no
// perder funcionalidades": a solo agent that never calls claim_chat must
// see send_message behave exactly as it did before this feature existed.
func TestSendMessageUnaffectedWithoutClaim(t *testing.T) {
	_, call, jid := newClaimTestServer(t, time.Minute)
	_, policyVersion := decisionPolicy("")

	out := call("send_message", sendArgs(jid, "claude-opus-4-8", policyVersion))
	if !strings.Contains(out, "queued for sending") {
		t.Fatalf("send_message with no claim ever made = %s, want it to succeed (unaffected)", out)
	}
	// A different model, still nobody claimed anything: also unaffected.
	out = call("send_message", sendArgs(jid, "deepseek-chat", policyVersion))
	if !strings.Contains(out, "queued for sending") {
		t.Errorf("send_message from a 2nd model with no claim ever made = %s, want it to succeed too", out)
	}
}

// TestReleaseChatToolForeignIsNoOp covers the same owner-only guarantee at
// the tool layer (store-level already covered in store's claim_test.go).
func TestReleaseChatToolForeignIsNoOp(t *testing.T) {
	_, call, jid := newClaimTestServer(t, time.Minute)
	_, policyVersion := decisionPolicy("")

	call("claim_chat", map[string]any{"chat_id": jid, "model": "claude-opus-4-8"})
	out := call("release_chat", map[string]any{"chat_id": jid, "model": "deepseek-chat"})
	if !strings.Contains(out, "released") {
		t.Fatalf("foreign release call = %s, want a plain 'released' response (no-op, not an error)", out)
	}
	// The claim must still be held by the original owner — sending as the
	// foreign model is still refused.
	out = call("send_message", sendArgs(jid, "deepseek-chat", policyVersion))
	if !strings.Contains(out, "claimed by another agent") {
		t.Errorf("send after a foreign release attempt = %s, want still blocked (release must not have cleared it)", out)
	}
}

// TestClaimChatDefaultTTL covers Deps.ClaimTTLDefault actually being used
// when ttl_sec is omitted.
func TestClaimChatDefaultTTL(t *testing.T) {
	st, call, jid := newClaimTestServer(t, 90*time.Second)
	call("claim_chat", map[string]any{"chat_id": jid, "model": "claude-opus-4-8"})
	c, _, err := st.GetChat(jid)
	if err != nil {
		t.Fatal(err)
	}
	wantAround := time.Now().Add(90 * time.Second).Unix()
	if c.ClaimedUntil < wantAround-5 || c.ClaimedUntil > wantAround+5 {
		t.Errorf("claimed_until = %d, want ~%d (Deps.ClaimTTLDefault=90s applied)", c.ClaimedUntil, wantAround)
	}
}
