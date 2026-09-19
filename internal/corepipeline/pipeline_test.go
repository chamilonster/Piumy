package corepipeline

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"piumy-gateway/internal/eventbus"
	"piumy-gateway/internal/gateway"
	"piumy-gateway/internal/governor"
	"piumy-gateway/internal/router"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
)

// testConfig keeps every anti-ban delay tiny so tests don't pay real
// wall-clock time for them; the defaults are exercised separately via
// TestDefaultConfigFallsBack.
func testConfig() Config {
	return Config{
		OutboxPoll:       time.Hour, // tests call processOutbox directly, never via the ticker
		OutboxMaxRetry:   3,
		DispatchDelayMin: time.Millisecond,
		DispatchDelayMax: 2 * time.Millisecond,
		ReadDelayMin:     time.Millisecond,
		ReadDelayMax:     2 * time.Millisecond,
		ComposingMin:     time.Millisecond,
		ComposingMax:     2 * time.Millisecond,
		ChunkDelayMin:    time.Millisecond,
		ChunkDelayMax:    2 * time.Millisecond,
	}
}

func newTestPipeline(t *testing.T) (*store.Store, *router.Manager, *governor.Limiter, *fakeGateway, *Pipeline) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	rt := router.NewManager(filepath.Join(t.TempDir(), "router.json"))
	gov := governor.NewLimiter(100, time.Minute)
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
	fgw := newFakeGateway()

	p := New(fgw, st, rt, gov, sm, testConfig())
	return st, rt, gov, fgw, p
}

func TestHandleInboundStoresRoutesTouchesAndUpdatesState(t *testing.T) {
	st, _, _, _, p := newTestPipeline(t)
	// CountPendingDedicated (what feeds state.Queue) only counts active chats
	// in "dedicated" mode (ct-2026-07-21-1853) — set both before the chat
	// exists, TouchChat's upsert never overwrites mode on conflict (F1a).
	if err := st.SetMode("111@c.us", "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetActive("111@c.us", true); err != nil {
		t.Fatal(err)
	}

	p.handleInbound(gateway.Inbound{
		ChatJID:   "111@c.us",
		SenderJID: "111@c.us",
		MsgID:     "m1",
		Text:      "hola",
		Type:      "text",
		TS:        100,
		PushName:  "Ana",
	})

	msgs, err := st.GetMessages("111@c.us", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Text != "hola" {
		t.Fatalf("got messages=%+v, want the inbound message stored", msgs)
	}

	c, ok, err := st.GetChat("111@c.us")
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	if c.Name != "Ana" {
		t.Errorf("chat name = %q, want Ana (TouchChat with PushName)", c.Name)
	}

	snap := pipelineState(p).Snapshot()
	if snap.Queue != 1 || snap.LastMsg != "hola" {
		t.Errorf("state snapshot = %+v, want Queue=1 LastMsg=hola", snap)
	}
	if snap.Mood != "new_msg" {
		t.Errorf("mood = %q, want new_msg (non-VIP sender)", snap.Mood)
	}
}

// TestHandleInboundPropagatesReplyAndForwarded covers ct-2026-07-21-1610
// (S6a backend): handleInbound must carry gateway.Inbound's
// QuotedID/QuotedPreview/Forwarded through to the stored message, not just
// the fields TestHandleInboundStoresRoutesTouchesAndUpdatesState already
// covers.
func TestHandleInboundPropagatesReplyAndForwarded(t *testing.T) {
	st, _, _, _, p := newTestPipeline(t)

	p.handleInbound(gateway.Inbound{
		ChatJID: "111@c.us", SenderJID: "111@c.us", MsgID: "m1",
		Text: "respuesta", Type: "text", TS: 100,
		QuotedID: "QUOTED1", QuotedPreview: "mensaje original", Forwarded: true,
	})

	msgs, err := st.GetMessages("111@c.us", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if msgs[0].QuotedID != "QUOTED1" || msgs[0].QuotedPreview != "mensaje original" || !msgs[0].Forwarded {
		t.Errorf("stored message = %+v, want QuotedID=QUOTED1 QuotedPreview='mensaje original' Forwarded=true", msgs[0])
	}
}

// TestHandleInboundOwnerReplyClosesPendingAndStoresMessage is T100's core
// case (ct-2026-08-29-1649): when the OWNER answers a contact from another
// device (the phone), whatsmeow.handleMessage now lets that echo through
// flagged FromMe=true instead of silently dropping it — this is the
// pipeline's half. The reply must be STORED (the DoD is explicit: not just
// closing the pending queue — the dashboard/agent lose context of what was
// already said otherwise) and it must close the chat's earlier pending
// messages, so the agent doesn't dispatch a second reply on top of the
// owner's.
func TestHandleInboundOwnerReplyClosesPendingAndStoresMessage(t *testing.T) {
	st, _, _, _, p := newTestPipeline(t)
	chat := "111@c.us"
	if err := st.SetMode(chat, "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetActive(chat, true); err != nil {
		t.Fatal(err)
	}

	// The contact's original message — what the owner just answered from
	// the phone.
	p.handleInbound(gateway.Inbound{ChatJID: chat, SenderJID: chat, MsgID: "m1", Text: "hola?", Type: "text", TS: 50})

	p.handleInbound(gateway.Inbound{ChatJID: chat, MsgID: "m-boss", Text: "ya te contesto", Type: "text", TS: 100, FromMe: true})

	msgs, err := st.GetMessages(chat, 10)
	if err != nil {
		t.Fatal(err)
	}
	var bossMsg *store.Message
	for i := range msgs {
		if msgs[i].ID == "m-boss" {
			bossMsg = &msgs[i]
		}
	}
	if bossMsg == nil {
		t.Fatalf("owner reply was not stored: %+v", msgs)
	}
	if !bossMsg.FromMe || bossMsg.Text != "ya te contesto" || bossMsg.Model != "boss" {
		t.Errorf("stored owner reply = %+v, want FromMe=true Text=\"ya te contesto\" Model=boss", bossMsg)
	}

	pending, err := st.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("pending after owner reply = %+v, want empty — the agent must not dispatch on top of what the owner already answered", pending)
	}
}

// TestHandleInboundOwnerReplyUsesItsOwnTimestampNotNow is the DoD's explicit
// bound requirement: MarkHandledBefore must use the outgoing message's OWN
// ts, never time.Now() — same rule the five existing MarkHandledBefore
// callers already follow. A message that arrives to the SAME chat AFTER the
// owner's reply must stay pending, not get silently swallowed by a
// too-generous bound.
func TestHandleInboundOwnerReplyUsesItsOwnTimestampNotNow(t *testing.T) {
	st, _, _, _, p := newTestPipeline(t)
	chat := "111@c.us"
	if err := st.SetMode(chat, "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetActive(chat, true); err != nil {
		t.Fatal(err)
	}

	p.handleInbound(gateway.Inbound{ChatJID: chat, SenderJID: chat, MsgID: "m1", Text: "primera pregunta", Type: "text", TS: 50})
	p.handleInbound(gateway.Inbound{ChatJID: chat, MsgID: "m-boss", Text: "contesto la primera", Type: "text", TS: 100, FromMe: true})
	// Arrives AFTER the owner's ts=100 reply — a different question the
	// owner hasn't seen yet.
	p.handleInbound(gateway.Inbound{ChatJID: chat, SenderJID: chat, MsgID: "m2", Text: "otra pregunta distinta", Type: "text", TS: 150})

	pending, err := st.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "m2" {
		t.Errorf("pending = %+v, want only m2 (it arrived after the owner's ts=100 reply, not before it)", pending)
	}
}

// TestHandleInboundDoesNotClobberManualModeOverride is the ST-B regression
// (ct-2026-07-11-0741): handleInbound used to re-apply the router's mode on
// EVERY inbound (SetMode, unconditional) — an owner/agent's deliberate
// set_mode/escalate call got silently reverted by the very next message
// from that chat. SyncRouterMode is a no-op once mode_source is 'manual'.
func TestHandleInboundDoesNotClobberManualModeOverride(t *testing.T) {
	st, rt, _, _, p := newTestPipeline(t)
	if err := rt.Update(func(c *router.Config) { c.DefaultMode = "dedicated" }); err != nil {
		t.Fatal(err)
	}
	chat := "222@c.us"
	// The owner/agent explicitly chose "auto" — the OPPOSITE of what the
	// router would resolve ("dedicated") — via set_mode/escalate/REST (all
	// of which call store.SetMode, never SyncRouterMode).
	if err := st.SetMode(chat, "auto"); err != nil {
		t.Fatal(err)
	}

	p.handleInbound(gateway.Inbound{
		ChatJID: chat, SenderJID: chat, MsgID: "m1", Text: "hola", Type: "text", TS: 100,
	})

	c, ok, err := st.GetChat(chat)
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	if c.Mode != "auto" {
		t.Errorf("Mode after inbound = %q, want %q (manual override must survive the router mirror)", c.Mode, "auto")
	}
	if c.ModeSource != "manual" {
		t.Errorf("ModeSource after inbound = %q, want %q (unchanged by the no-op sync)", c.ModeSource, "manual")
	}
}

// TestHandleInboundDoesNotOverwriteGroupNameWithSenderPushName is the
// ct-2026-07-10-1758 regression (boss escalation from the ct-2026-07-10-1656
// smoke test): msg.PushName is the SENDER's own display name, not the
// group's — passing it straight to TouchChat for a group JID clobbered the
// real group name (seeded by whatsmeow's seedGroups/GetJoinedGroups) with
// whoever happened to send the next message ("QUELENTARO INFORMADO" ->
// "Bakery" in the real smoke test). The 1:1 case (pushname DOES set the
// chat name) is already covered by
// TestHandleInboundStoresRoutesTouchesAndUpdatesState above.
func TestHandleInboundDoesNotOverwriteGroupNameWithSenderPushName(t *testing.T) {
	st, _, _, _, p := newTestPipeline(t)
	group := "555000000000000001@g.us"
	// Simulates seedGroups having already touched this chat with the real
	// group name at connect time, before any message ever arrived.
	if err := st.TouchChat(group, "Grupo De Prueba", 1); err != nil {
		t.Fatal(err)
	}

	p.handleInbound(gateway.Inbound{
		ChatJID:   group,
		SenderJID: "111@s.whatsapp.net",
		MsgID:     "m1",
		Text:      "hola",
		Type:      "text",
		TS:        100,
		PushName:  "Bakery", // the SENDER's own pushname, not the group's
	})

	c, ok, err := st.GetChat(group)
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	if c.Name != "Grupo De Prueba" {
		t.Errorf("group chat name after an inbound message = %q, want the seeded group name (Grupo De Prueba) left untouched by the sender's pushname", c.Name)
	}
}

// TestHandleInboundAppliesRouterMode: a fresh inbound chat must adopt the
// router's resolved mode with NO manual SetMode. This was the F5-smoke wiring
// gap — handleInbound resolved dec.Mode but discarded it, so every new chat
// stayed at the schema default 'auto' and capipush (dedicated-only) never
// dispatched it. The two JIDs prove the mode is sourced from the router, not
// hardcoded: a 'dedicated' route lands dedicated, an 'auto' route lands auto.
func TestHandleInboundAppliesRouterMode(t *testing.T) {
	st, rt, _, _, p := newTestPipeline(t)
	if err := rt.Update(func(c *router.Config) {
		c.Routes = []router.Route{
			{Match: "ded@c.us", Mode: "dedicated"},
			{Match: "au@c.us", Mode: "auto"},
		}
	}); err != nil {
		t.Fatal(err)
	}

	p.handleInbound(gateway.Inbound{ChatJID: "ded@c.us", MsgID: "m1", Text: "hola", TS: 1})
	p.handleInbound(gateway.Inbound{ChatJID: "au@c.us", MsgID: "m2", Text: "hola", TS: 2})

	for _, tc := range []struct{ jid, want string }{
		{"ded@c.us", "dedicated"},
		{"au@c.us", "auto"},
	} {
		c, ok, err := st.GetChat(tc.jid)
		if err != nil || !ok {
			t.Fatalf("GetChat %s: ok=%v err=%v", tc.jid, ok, err)
		}
		if c.Mode != tc.want {
			t.Errorf("chat %s mode = %q, want %q (router decision applied by handleInbound)", tc.jid, c.Mode, tc.want)
		}
	}
}

// sm exposes the *state.Manager a test-built Pipeline holds, without adding
// an exported getter to the production type just for tests.
func pipelineState(p *Pipeline) *state.Manager { return p.state }

// TestHandleInboundStoresRegardlessOfWhitelist is T65 (ct-2026-08-11-1642,
// boss verbatim: "yo quierp todo en witelist... para algo esta ignorar, eso
// ya apaga el chat") — replaces the old TestHandleInboundRespectsRouterGate,
// which asserted the OPPOSITE (a non-whitelisted chat's message got
// silently dropped here, before it ever reached the store). That was the
// exact bug the owner complained about: numbers that wrote to him never
// showed up anywhere, because the message never existed past this point.
// Fresh router.Manager on an empty path defaults to whitelist-only, nothing
// whitelisted — this used to mean Resolve(...).Allowed is false for
// everyone; now nothing here even calls Resolve for a gate, so an empty
// whitelist has zero effect on whether a message is stored.
func TestHandleInboundStoresRegardlessOfWhitelist(t *testing.T) {
	st, _, _, _, p := newTestPipeline(t)

	p.handleInbound(gateway.Inbound{ChatJID: "999@c.us", MsgID: "m1", Text: "hola", TS: 1})

	msgs, err := st.GetMessages("999@c.us", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Text != "hola" {
		t.Fatalf("got messages=%+v for a chat never added to the whitelist, want the message stored — nothing gates entry anymore", msgs)
	}
}

func TestHandleInboundReactsVIPForWhitelistedSender(t *testing.T) {
	_, rt, _, _, p := newTestPipeline(t)
	const vip = "111@c.us"
	if err := rt.Update(func(c *router.Config) {
		c.Whitelist = []string{vip} // whitelist membership alone confers VIP (router.IsVIP)
	}); err != nil {
		t.Fatal(err)
	}

	p.handleInbound(gateway.Inbound{ChatJID: vip, MsgID: "m1", Text: "hola jefe", TS: 1})

	if got := pipelineState(p).Snapshot().Mood; got != "vip" {
		t.Errorf("mood for whitelisted sender = %q, want vip", got)
	}
}

func TestHandleInboundPublishesOnEventbus(t *testing.T) {
	_, _, _, _, p := newTestPipeline(t)
	bus := eventbus.New()
	p.SetBus(bus)
	ch, unsubscribe := bus.Subscribe()
	defer unsubscribe()

	p.handleInbound(gateway.Inbound{ChatJID: "111@c.us", MsgID: "m1", Text: "hola", TS: 42})

	select {
	case e := <-ch:
		if e.JID != "111@c.us" || e.TS != 42 {
			t.Errorf("event = %+v, want jid=111@c.us ts=42", e)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the eventbus nudge")
	}
}

// TestMarkReadSkipsWhenKillSwitchActive is the M1 hardening regression
// (escalated into ct-2026-07-10-0540 by Citrino's audit of H6: a
// LoggedOut/TemporaryBan trips the kill switch, but before this fix
// MarkRead's background goroutine only checked ctx.Err() — read receipts
// kept going out to WhatsApp even while "everything" was supposed to be
// stopped). Uses newTestPipeline's own governor (gov.SetKill covers the
// shared killSwitchActive() check — already exercised for both halves,
// gov.Killed/state.Muted, by outbox_test.go's own tests).
func TestMarkReadSkipsWhenKillSwitchActive(t *testing.T) {
	st, _, gov, fgw, p := newTestPipeline(t)
	chat := "111@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}
	gov.SetKill(true)

	p.MarkRead(chat, []store.Message{{ChatJID: chat, ID: "m1", FromMe: false, ReadTS: 0}})

	// MarkRead's real work happens in a background goroutine after
	// readDelay().Sleep (testConfig: 1-2ms) — 50ms is a generous margin
	// without actually waiting on anything to poll for (a negative
	// assertion: nothing should have happened).
	time.Sleep(50 * time.Millisecond)

	if calls := fgw.markReadCalls(); len(calls) != 0 {
		t.Errorf("gw.MarkRead calls while the kill switch is active = %+v, want none", calls)
	}
	msgs, err := st.GetMessages(chat, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].ReadTS != 0 {
		t.Errorf("messages after MarkRead while killed = %+v, want ReadTS still 0 (never persisted)", msgs)
	}
}

// TestMarkReadRoutesEachSenderSeparately (T127, ct-2026-09-02-2249): unlike
// capipush's own dispatch burst (single-sender by construction, T108), this
// is called with a chat's raw message list (get_messages) — a group mixes
// participants freely. Each participant's unread messages must reach
// gw.MarkRead addressed to THAT participant, never lumped under the group.
func TestMarkReadRoutesEachSenderSeparately(t *testing.T) {
	st, _, _, fgw, p := newTestPipeline(t)
	group := "555001@g.us"
	alice := "55500000055@s.whatsapp.net"
	bob := "55500000056@s.whatsapp.net"
	if err := st.TouchChat(group, "Grupo", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: group, ID: "m1", FromMe: false, Sender: alice, Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: group, ID: "m2", FromMe: false, Sender: bob, Text: "hola2", TS: 2}); err != nil {
		t.Fatal(err)
	}
	msgs, err := st.GetMessages(group, 10)
	if err != nil {
		t.Fatal(err)
	}

	p.MarkRead(group, msgs)
	time.Sleep(50 * time.Millisecond) // background goroutine, same margin as the kill-switch test above

	calls := fgw.markReadCalls()
	if len(calls) != 2 {
		t.Fatalf("gw.MarkRead calls = %+v, want 2 (one per sender)", calls)
	}
	bySender := map[string][]string{}
	for _, c := range calls {
		if c.chatJID != group {
			t.Errorf("call chatJID = %q, want %q", c.chatJID, group)
		}
		bySender[c.sender] = c.msgIDs
	}
	if len(bySender[alice]) != 1 || bySender[alice][0] != "m1" {
		t.Errorf("alice's ids = %v, want [m1]", bySender[alice])
	}
	if len(bySender[bob]) != 1 || bySender[bob][0] != "m2" {
		t.Errorf("bob's ids = %v, want [m2]", bySender[bob])
	}
}

// ── T91: recovering a read receipt a first MarkRead attempt lost ───────────

func TestRetryReadReceiptsRecoversFailedMarkRead(t *testing.T) {
	st, _, _, fgw, p := newTestPipeline(t)
	chat := "111@c.us"
	old := time.Now().Add(-2 * time.Minute).Unix()
	if err := st.TouchChat(chat, "C", old); err != nil {
		t.Fatal(err)
	}
	// Simulates the T91 bug: a first attempt already happened and failed
	// (this is why ReadTS is still 0 despite the message being old — a
	// fresh, never-attempted message would ALSO show ReadTS=0, which is
	// exactly what the age floor below distinguishes it from).
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "m1", FromMe: false, Text: "hola", TS: old}); err != nil {
		t.Fatal(err)
	}

	p.retryReadReceipts(context.Background())

	calls := fgw.markReadCalls()
	if len(calls) != 1 || calls[0].chatJID != chat || len(calls[0].msgIDs) != 1 || calls[0].msgIDs[0] != "m1" {
		t.Fatalf("gw.MarkRead calls = %+v, want one call for chat=%s id=m1", calls, chat)
	}
	m, ok, err := st.GetMessageByID(chat, "m1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || m.ReadTS == 0 {
		t.Errorf("message after retry = %+v, want ReadTS set", m)
	}
}

// TestRetryReadReceiptsSkipsFreshMessages guards the age floor
// (readReceiptRetryAge): a message that just arrived hasn't necessarily had
// its FIRST mark-read attempt yet (MarkRead's own readDelay, or capipush's
// immediate call, may still be in flight) — retrying it this early would
// race that attempt, not recover from a failed one.
func TestRetryReadReceiptsSkipsFreshMessages(t *testing.T) {
	st, _, _, fgw, p := newTestPipeline(t)
	chat := "111@c.us"
	now := time.Now().Unix()
	if err := st.TouchChat(chat, "C", now); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "m1", FromMe: false, Text: "hola", TS: now}); err != nil {
		t.Fatal(err)
	}

	p.retryReadReceipts(context.Background())

	if calls := fgw.markReadCalls(); len(calls) != 0 {
		t.Errorf("gw.MarkRead calls for a fresh message = %+v, want none (too young to retry yet)", calls)
	}
}

func TestRetryReadReceiptsSkipsWhenDisconnected(t *testing.T) {
	st, _, _, fgw, p := newTestPipeline(t)
	chat := "111@c.us"
	old := time.Now().Add(-2 * time.Minute).Unix()
	if err := st.TouchChat(chat, "C", old); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "m1", FromMe: false, Text: "hola", TS: old}); err != nil {
		t.Fatal(err)
	}
	fgw.setConnected(false)

	p.retryReadReceipts(context.Background())

	if calls := fgw.markReadCalls(); len(calls) != 0 {
		t.Errorf("gw.MarkRead calls while disconnected = %+v, want none", calls)
	}
}

func TestRetryReadReceiptsSkipsWhenKillSwitchActive(t *testing.T) {
	st, _, gov, fgw, p := newTestPipeline(t)
	chat := "111@c.us"
	old := time.Now().Add(-2 * time.Minute).Unix()
	if err := st.TouchChat(chat, "C", old); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "m1", FromMe: false, Text: "hola", TS: old}); err != nil {
		t.Fatal(err)
	}
	gov.SetKill(true)

	p.retryReadReceipts(context.Background())

	if calls := fgw.markReadCalls(); len(calls) != 0 {
		t.Errorf("gw.MarkRead calls while killed = %+v, want none", calls)
	}
}

// TestRetryReadReceiptsGroupsByChat is the anti-ban core of T91: a burst
// recovered as ONE call per chat (not one per message, and not the whole
// backlog in a single call spanning chats) — same coalescing MarkRead
// already does, extended across chats instead of within one.
func TestRetryReadReceiptsGroupsByChat(t *testing.T) {
	st, _, _, fgw, p := newTestPipeline(t)
	old := time.Now().Add(-2 * time.Minute).Unix()
	for _, chat := range []string{"111@c.us", "222@c.us"} {
		if err := st.TouchChat(chat, "C", old); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.AddMessage(store.Message{ChatJID: "111@c.us", ID: "a1", FromMe: false, Text: "hola", TS: old}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: "111@c.us", ID: "a2", FromMe: false, Text: "hola2", TS: old}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: "222@c.us", ID: "b1", FromMe: false, Text: "hola3", TS: old}); err != nil {
		t.Fatal(err)
	}

	p.retryReadReceipts(context.Background())

	calls := fgw.markReadCalls()
	if len(calls) != 2 {
		t.Fatalf("gw.MarkRead calls = %+v, want exactly 2 (one per chat)", calls)
	}
	byChat := map[string][]string{}
	for _, c := range calls {
		byChat[c.chatJID] = c.msgIDs
	}
	if len(byChat["111@c.us"]) != 2 || len(byChat["222@c.us"]) != 1 {
		t.Errorf("ids per chat = %+v, want 2 for 111@c.us and 1 for 222@c.us", byChat)
	}
}

// TestRetryReadReceiptsGroupsBySenderWithinAChat (T127, ct-2026-09-02-2249):
// same coalescing as TestRetryReadReceiptsGroupsByChat above, but within a
// SINGLE group chat — two participants' unread backlogs must retry as two
// separate calls, each addressed to its own sender, never merged under the
// group JID.
func TestRetryReadReceiptsGroupsBySenderWithinAChat(t *testing.T) {
	st, _, _, fgw, p := newTestPipeline(t)
	old := time.Now().Add(-2 * time.Minute).Unix()
	group := "555001@g.us"
	alice := "55500000055@s.whatsapp.net"
	bob := "55500000056@s.whatsapp.net"
	if err := st.TouchChat(group, "Grupo", old); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: group, ID: "a1", FromMe: false, Sender: alice, Text: "hola", TS: old}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: group, ID: "b1", FromMe: false, Sender: bob, Text: "hola2", TS: old}); err != nil {
		t.Fatal(err)
	}

	p.retryReadReceipts(context.Background())

	calls := fgw.markReadCalls()
	if len(calls) != 2 {
		t.Fatalf("gw.MarkRead calls = %+v, want 2 (one per sender)", calls)
	}
	bySender := map[string][]string{}
	for _, c := range calls {
		if c.chatJID != group {
			t.Errorf("call chatJID = %q, want %q", c.chatJID, group)
		}
		bySender[c.sender] = c.msgIDs
	}
	if len(bySender[alice]) != 1 || bySender[alice][0] != "a1" {
		t.Errorf("alice's ids = %v, want [a1]", bySender[alice])
	}
	if len(bySender[bob]) != 1 || bySender[bob][0] != "b1" {
		t.Errorf("bob's ids = %v, want [b1]", bySender[bob])
	}
}

// TestRetryReadReceiptsContinuesPastOneChatFailure: one chat's retry failing
// again must not stop the pass for every OTHER chat's backlog — same
// per-item continue processOutbox already uses for a send failure.
func TestRetryReadReceiptsContinuesPastOneChatFailure(t *testing.T) {
	st, _, _, fgw, p := newTestPipeline(t)
	old := time.Now().Add(-2 * time.Minute).Unix()
	for _, chat := range []string{"111@c.us", "222@c.us"} {
		if err := st.TouchChat(chat, "C", old); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.AddMessage(store.Message{ChatJID: "111@c.us", ID: "a1", FromMe: false, Text: "hola", TS: old}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: "222@c.us", ID: "b1", FromMe: false, Text: "hola2", TS: old}); err != nil {
		t.Fatal(err)
	}
	fgw.setMarkReadErr(errFakeSend) // reused sentinel error, this file's own convention

	p.retryReadReceipts(context.Background())

	// Every chat was ATTEMPTED (both failed, since the fake fails every
	// call) — the point is the second one wasn't skipped because of the
	// first, not that either succeeded.
	if calls := fgw.markReadCalls(); len(calls) != 2 {
		t.Errorf("gw.MarkRead calls = %+v, want 2 attempts despite both failing", calls)
	}
	for _, id := range []struct{ chat, msg string }{{"111@c.us", "a1"}, {"222@c.us", "b1"}} {
		m, ok, err := st.GetMessageByID(id.chat, id.msg)
		if err != nil {
			t.Fatal(err)
		}
		if !ok || m.ReadTS != 0 {
			t.Errorf("message %s/%s after a failed retry = %+v, want ReadTS still 0", id.chat, id.msg, m)
		}
	}
}

// TestDefaultConfigFallsBack covers the anti-ban invariant: an unwired
// (zero-value) Config must never mean "instant" or "no retry cap" — every
// field falls back to a positive default.
func TestDefaultConfigFallsBack(t *testing.T) {
	cfg := defaultConfig(Config{})
	if cfg.OutboxPoll <= 0 || cfg.OutboxMaxRetry <= 0 {
		t.Errorf("zero Config: OutboxPoll=%v OutboxMaxRetry=%d, want both positive", cfg.OutboxPoll, cfg.OutboxMaxRetry)
	}
	if cfg.DispatchDelayMin <= 0 || cfg.DispatchDelayMax < cfg.DispatchDelayMin {
		t.Errorf("zero Config: DispatchDelayMin=%v Max=%v, want positive and Max>=Min", cfg.DispatchDelayMin, cfg.DispatchDelayMax)
	}
	if cfg.ReadDelayMin <= 0 || cfg.ReadDelayMax < cfg.ReadDelayMin {
		t.Errorf("zero Config: ReadDelayMin=%v Max=%v, want positive and Max>=Min", cfg.ReadDelayMin, cfg.ReadDelayMax)
	}
	if cfg.ComposingMin <= 0 || cfg.ComposingMax < cfg.ComposingMin {
		t.Errorf("zero Config: ComposingMin=%v Max=%v, want positive and Max>=Min", cfg.ComposingMin, cfg.ComposingMax)
	}
	if cfg.ChunkMaxLen <= 0 {
		t.Errorf("zero Config: ChunkMaxLen=%d, want positive", cfg.ChunkMaxLen)
	}
	if cfg.ChunkDelayMin <= 0 || cfg.ChunkDelayMax < cfg.ChunkDelayMin {
		t.Errorf("zero Config: ChunkDelayMin=%v Max=%v, want positive and Max>=Min", cfg.ChunkDelayMin, cfg.ChunkDelayMax)
	}
}
