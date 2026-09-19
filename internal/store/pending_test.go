package store

import "testing"

// TestMarkHandledBeforeBounded verifies ct-2026-07-13-2105: MarkHandledBefore
// marks messages with ts<=tsResp handled but leaves later messages untouched.
func TestMarkHandledBeforeBounded(t *testing.T) {
	s := openTestStore(t)

	if err := s.SetMode("chat@c.us", "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActive("chat@c.us", true); err != nil {
		t.Fatal(err)
	}
	msgs := []Message{
		{ChatJID: "chat@c.us", ID: "m1", FromMe: false, Text: "one", TS: 100},
		{ChatJID: "chat@c.us", ID: "m2", FromMe: false, Text: "two", TS: 200},
		{ChatJID: "chat@c.us", ID: "m3", FromMe: false, Text: "three", TS: 300},
	}
	for _, m := range msgs {
		if err := s.AddMessage(m); err != nil {
			t.Fatal(err)
		}
	}

	// mark handled up to ts=200 — m1 and m2 handled, m3 must stay pending.
	if err := s.MarkHandledBefore("chat@c.us", 200); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "m3" {
		t.Fatalf("got %+v, want only m3 still pending", pending)
	}
}

// TestMarkPendingBeforeReversesMarkHandledBefore is T15 (ct-2026-08-05-123241):
// a rejected draft reopens exactly the messages its own creation closed
// (same ts bound), and nothing that arrived later or was handled through a
// different path.
func TestMarkPendingBeforeReversesMarkHandledBefore(t *testing.T) {
	s := openTestStore(t)
	if err := s.SetMode("chat@c.us", "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActive("chat@c.us", true); err != nil {
		t.Fatal(err)
	}
	msgs := []Message{
		{ChatJID: "chat@c.us", ID: "m1", FromMe: false, Text: "one", TS: 100},
		{ChatJID: "chat@c.us", ID: "m2", FromMe: false, Text: "two", TS: 200},
		{ChatJID: "chat@c.us", ID: "m3", FromMe: false, Text: "three", TS: 300},
	}
	for _, m := range msgs {
		if err := s.AddMessage(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.MarkHandledBefore("chat@c.us", 200); err != nil {
		t.Fatal(err)
	}

	if err := s.MarkPendingBefore("chat@c.us", 200); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 3 {
		t.Fatalf("PendingDedicated after MarkPendingBefore = %+v, want all 3 messages pending again", pending)
	}
}

func TestPendingDedicatedOnlyUnhandled(t *testing.T) {
	s := openTestStore(t)

	if err := s.SetMode("dedicated@c.us", "dedicated"); err != nil {
		t.Fatalf("SetMode dedicated: %v", err)
	}
	if err := s.SetActive("dedicated@c.us", true); err != nil {
		t.Fatalf("SetActive dedicated: %v", err)
	}
	if err := s.AddMessage(Message{ChatJID: "dedicated@c.us", ID: "m1", FromMe: false, Text: "hola", TS: 100}); err != nil {
		t.Fatalf("AddMessage dedicated: %v", err)
	}

	if err := s.MarkHandled("dedicated@c.us", "m1"); err != nil {
		t.Fatalf("MarkHandled: %v", err)
	}
	pending, err := s.PendingDedicated(10)
	if err != nil {
		t.Fatalf("PendingDedicated after handled: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("got %d pending after MarkHandled, want 0", len(pending))
	}
}

// TestPendingDedicatedIncludesAutoMode is T5 (ct-2026-08-05-0311, boss
// verbatim: "todos los mensajes automaticos y por confirmacion deben
// empezar a entrar solos") — entering the agent and being sent are
// different things: an 'auto' chat's unhandled message now reaches
// PendingDedicated exactly like a 'dedicated' one. Before this, 'auto'
// chats only went to internal/autoreply — inert without
// PIUMY_BRIDGE=direct-api (off in production), so an 'auto' chat went
// unanswered with nothing to show for it.
func TestPendingDedicatedIncludesAutoMode(t *testing.T) {
	s := openTestStore(t)

	if err := s.SetMode("dedicated@c.us", "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActive("dedicated@c.us", true); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: "dedicated@c.us", ID: "m1", FromMe: false, Text: "hola", TS: 100}); err != nil {
		t.Fatal(err)
	}

	if err := s.SetMode("auto@c.us", "auto"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActive("auto@c.us", true); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: "auto@c.us", ID: "m2", FromMe: false, Text: "hola", TS: 100}); err != nil {
		t.Fatal(err)
	}

	pending, err := s.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 {
		t.Fatalf("PendingDedicated = %+v, want both dedicated@c.us and auto@c.us", pending)
	}

	count, err := s.CountPendingDedicated()
	if err != nil {
		t.Fatal(err)
	}
	if count != len(pending) {
		t.Errorf("CountPendingDedicated = %d, want %d (must match PendingDedicated exactly, or the queue count lies)", count, len(pending))
	}
}

// TestCountRecentPendingNonBossIncludesAutoMode is T20 (ct-2026-08-05-1301,
// Amatista's R1): CountRecentPendingNonBoss — capipush's own backpressure
// counter — is a THIRD query with the same mode filter T5 widened on
// PendingDedicated/CountPendingDedicated, missed when T5 was written. Before
// this fix, an 'auto' chat's pending traffic was invisible to the
// saturation counter — the semáforo lied by omission about how loaded the
// channel actually was.
func TestCountRecentPendingNonBossIncludesAutoMode(t *testing.T) {
	s := openTestStore(t)

	if err := s.SetMode("auto@c.us", "auto"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActive("auto@c.us", true); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: "auto@c.us", ID: "m1", FromMe: false, Text: "hola", TS: 100}); err != nil {
		t.Fatal(err)
	}

	count, err := s.CountRecentPendingNonBoss(0)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("CountRecentPendingNonBoss = %d, want 1 — an 'auto' chat's pending message must count toward backpressure", count)
	}
}

// TestCountRecentPendingNonBossExcludesStatusBroadcast is T84
// (ct-2026-08-27-2314): status@broadcast is is_boss=0 like any other 1:1,
// so its 34-hour redispatch loop would ALSO have inflated this exact
// backpressure counter, throttling every real non-boss chat on a reading
// that was never real.
func TestCountRecentPendingNonBossExcludesStatusBroadcast(t *testing.T) {
	s := openTestStore(t)

	if err := s.SetMode(StatusBroadcastJID, "auto"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActive(StatusBroadcastJID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: StatusBroadcastJID, ID: "s1", FromMe: false, Type: "media", TS: 100}); err != nil {
		t.Fatal(err)
	}

	count, err := s.CountRecentPendingNonBoss(0)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("CountRecentPendingNonBoss = %d, want 0 — status@broadcast must never count toward backpressure", count)
	}
}

// TestPendingDedicatedIncludesConfirmationRequired is T5's other half of the
// same principle: confirmation_mode governs how a reply goes OUT
// (send_message's own gate, untouched by this contract) — it must never
// decide whether the agent gets to see the message. A chat with
// confirmation_mode="always" still has to reach PendingDedicated so the
// agent can draft, even though nothing will send without approval.
func TestPendingDedicatedIncludesConfirmationRequired(t *testing.T) {
	s := openTestStore(t)
	jid := "confirm-required@c.us"

	if err := s.SetMode(jid, "auto"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActive(jid, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetConfirmationMode(jid, "always"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}

	pending, err := s.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("PendingDedicated = %+v, want the confirmation-required chat's message dispatched", pending)
	}
}

// TestPendingDedicatedStillExcludesSilencedAutoChats confirms T5's mode
// widening didn't loosen the OTHER gate: an ignored or never-activated
// chat stays out regardless of mode — is_boss remains the only bypass.
// TestPendingDedicatedRespectsConfigLevel already covers this for
// 'dedicated'; this extends it to 'auto', the mode T5 actually changed.
func TestPendingDedicatedStillExcludesSilencedAutoChats(t *testing.T) {
	cases := []struct {
		name   string
		active bool
		status string
	}{
		{"never activated", false, "new"},
		{"explicitly ignored", true, "ignored"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			jid := "silenced-auto@c.us"
			if err := s.SetMode(jid, "auto"); err != nil {
				t.Fatal(err)
			}
			if err := s.SetActive(jid, tc.active); err != nil {
				t.Fatal(err)
			}
			if err := s.SetStatus(jid, tc.status); err != nil {
				t.Fatal(err)
			}
			if err := s.AddMessage(Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
				t.Fatal(err)
			}

			pending, err := s.PendingDedicated(10)
			if err != nil {
				t.Fatal(err)
			}
			if len(pending) != 0 {
				t.Errorf("PendingDedicated = %+v, want empty (silenced auto chat)", pending)
			}

			count, err := s.CountPendingDedicated()
			if err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Errorf("CountPendingDedicated = %d, want 0", count)
			}
		})
	}
}

// TestPendingDedicatedRespectsConfigLevel is ct-2026-07-21-1853: a chat's
// config_level (boss/auto/confirm/unattended/ignored — ConfigLevel/
// SetConfigLevel) must gate PendingDedicated/CountPendingDedicated, not just
// its mode. Before this fix, an unattended (active=false) or ignored chat in
// dedicated mode still dispatched to the agent — the level showed in the UI
// but the engine never actually enforced it.
func TestPendingDedicatedRespectsConfigLevel(t *testing.T) {
	levels := []struct {
		level      string
		dispatched bool
	}{
		{"boss", true},
		{"auto", true},
		{"confirm", true},
		{"unattended", false},
		{"ignored", false},
	}
	for _, tc := range levels {
		t.Run(tc.level, func(t *testing.T) {
			s := openTestStore(t)
			jid := tc.level + "@c.us"
			if err := s.SetMode(jid, "dedicated"); err != nil {
				t.Fatal(err)
			}
			if err := s.SetConfigLevel(jid, tc.level); err != nil {
				t.Fatal(err)
			}
			if err := s.AddMessage(Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
				t.Fatal(err)
			}

			pending, err := s.PendingDedicated(10)
			if err != nil {
				t.Fatal(err)
			}
			gotDispatched := len(pending) == 1
			if gotDispatched != tc.dispatched {
				t.Errorf("PendingDedicated at level %q = %+v, want dispatched=%v", tc.level, pending, tc.dispatched)
			}

			count, err := s.CountPendingDedicated()
			if err != nil {
				t.Fatal(err)
			}
			wantCount := 0
			if tc.dispatched {
				wantCount = 1
			}
			if count != wantCount {
				t.Errorf("CountPendingDedicated at level %q = %d, want %d", tc.level, count, wantCount)
			}
		})
	}
}

// TestPendingDedicatedExcludesStatusBroadcast is T84 (ct-2026-08-27-2314):
// status@broadcast (WhatsApp's Status/Stories pseudo-chat) satisfies every
// other PendingDedicated gate exactly like a real chat once its config
// level lands on "auto" (the same default-new-number path a real 1:1 gets)
// — measured on the real install as a 34-hour redispatch loop, 44.359 of
// ~44.4k log lines across 3 rotations, once nobody was watching. Receiving
// and archiving status@broadcast (AddMessage/TouchChat) stays untouched —
// this only keeps it out of the dispatch queue.
func TestPendingDedicatedExcludesStatusBroadcast(t *testing.T) {
	s := openTestStore(t)
	if err := s.SetConfigLevel(StatusBroadcastJID, "auto"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: StatusBroadcastJID, ID: "s1", FromMe: false, Type: "media", TS: 100}); err != nil {
		t.Fatal(err)
	}

	pending, err := s.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("PendingDedicated with only status@broadcast pending = %+v, want empty — a status update is never a conversation, nobody wrote to anybody", pending)
	}
	if count, err := s.CountPendingDedicated(); err != nil {
		t.Fatal(err)
	} else if count != 0 {
		t.Errorf("CountPendingDedicated = %d, want 0 — the cola: N badge must not count status@broadcast either", count)
	}
}

// TestPendingDedicatedStillIncludesNormalChats is T84's regression guard:
// excluding status@broadcast must not take a real chat down with it — the
// filter is scoped to one exact JID, not a broader condition that happens
// to also match status@broadcast.
func TestPendingDedicatedStillIncludesNormalChats(t *testing.T) {
	s := openTestStore(t)
	if err := s.SetConfigLevel(StatusBroadcastJID, "auto"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: StatusBroadcastJID, ID: "s1", FromMe: false, Type: "media", TS: 100}); err != nil {
		t.Fatal(err)
	}
	realChat := "555000001@s.whatsapp.net"
	if err := s.SetConfigLevel(realChat, "auto"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: realChat, ID: "m1", FromMe: false, Text: "hola", TS: 101}); err != nil {
		t.Fatal(err)
	}

	pending, err := s.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ChatJID != realChat {
		t.Errorf("PendingDedicated = %+v, want exactly the real chat's message, status@broadcast excluded", pending)
	}
}

// TestPendingDedicatedDoesNotSweepOtherBroadcastJIDs is T84's exact-JID
// check, not a suffix one — a user-created broadcast list is ALSO
// `<id>@broadcast` (whatsmeow's types.IsBroadcastList: a broadcast JID
// whose user isn't "status") and IS a real destination the owner sends to.
// WhatsApp never delivers a broadcast list's replies under the list's own
// JID today, so this can't happen in practice yet — but the cut is by
// StatusBroadcastJID exactly, not "ends with @broadcast", so it never
// would even if that changed without anyone remembering to re-check here.
func TestPendingDedicatedDoesNotSweepOtherBroadcastJIDs(t *testing.T) {
	s := openTestStore(t)
	broadcastList := "5550000000000000001@broadcast"
	if err := s.SetConfigLevel(broadcastList, "auto"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: broadcastList, ID: "b1", FromMe: false, Text: "hola lista", TS: 100}); err != nil {
		t.Fatal(err)
	}

	pending, err := s.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ChatJID != broadcastList {
		t.Errorf("PendingDedicated = %+v, want the broadcast list's message included — only status@broadcast is excluded, not any @broadcast suffix", pending)
	}
}

// TestDispatchQueriesExcludeBlacklist is T67 (ct-2026-08-11-172135) — the
// bug Tourmaline found and reported (not fixed) in T65: all four dispatch
// queries in this file excluded status='ignored' but NONE of them excluded
// 'blacklist'. Since SetStatus never touches active, a blacklisted chat
// with active=1 (the common case — nobody deactivates a chat by hand before
// blacklisting it) kept dispatching to an agent, only failing later at
// send.go's own (correct) check. Covers all four functions that share
// offStatusSQL, proving they actually agree now instead of just claiming to
// via a comment (ct-2026-07-30-030948's own doc on CountRecentPendingNonBoss
// already promised lockstep with PendingDedicated once, for the 'auto' mode
// gate, and drifted anyway — Amatista's R1 catch. A promise in a comment
// isn't the same as one shared definition; offStatusSQL is the fix, this
// test is what keeps it honest.)
func TestDispatchQueriesExcludeBlacklist(t *testing.T) {
	s := openTestStore(t)
	jid := "555000005@s.whatsapp.net"

	if err := s.SetMode(jid, "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActive(jid, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(jid, "blacklist"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 100}); err != nil {
		t.Fatal(err)
	}

	// PendingChats stays broad on purpose (autoreply's own eligible() filter
	// applies blacklist separately) — the message must still show up there.
	raw, err := s.PendingChats(10, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 1 {
		t.Fatalf("PendingChats = %d, want 1 — blacklist doesn't hide the chat from the raw feed", len(raw))
	}

	dispatch, err := s.PendingChatsDispatch(10, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatch) != 0 {
		t.Errorf("PendingChatsDispatch = %+v, want empty (status=blacklist)", dispatch)
	}

	pending, err := s.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("PendingDedicated = %+v, want empty (status=blacklist) — this is the actual dispatch queue capipush reads", pending)
	}

	count, err := s.CountPendingDedicated()
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("CountPendingDedicated = %d, want 0 (status=blacklist)", count)
	}

	backpressure, err := s.CountRecentPendingNonBoss(0)
	if err != nil {
		t.Fatal(err)
	}
	if backpressure != 0 {
		t.Errorf("CountRecentPendingNonBoss = %d, want 0 (status=blacklist)", backpressure)
	}
}

// TestPendingChatsDispatchExcludesHandled is T55 (ct-2026-08-10-2007): a chat
// whose last inbound message is already marked handled still appears in
// PendingChats (the contact has the last word), but must NOT appear in
// PendingChatsDispatch — the gateway has nothing left to dispatch from it.
// This was the "chat stuck days" scenario: mark_handled (or a stuck outbox)
// left handled=1 on the last inbound message, PendingChats kept showing it,
// and nobody could understand why it never got dispatched.
func TestPendingChatsDispatchExcludesHandled(t *testing.T) {
	s := openTestStore(t)
	jid := "555000001@s.whatsapp.net"

	if err := s.SetMode(jid, "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActive(jid, true); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 100}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkHandled(jid, "m1"); err != nil {
		t.Fatal(err)
	}

	raw, err := s.PendingChats(10, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 1 {
		t.Fatalf("PendingChats = %d, want 1 (broad view still shows the chat)", len(raw))
	}

	dispatch, err := s.PendingChatsDispatch(10, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatch) != 0 {
		t.Errorf("PendingChatsDispatch = %+v, want empty (handled=1, nothing left to dispatch)", dispatch)
	}
}

// TestPendingChatsDispatchExcludesIgnored is T55: a chat with status='ignored'
// appears in PendingChats but must be absent from PendingChatsDispatch —
// matching PendingDedicated's own gate.
func TestPendingChatsDispatchExcludesIgnored(t *testing.T) {
	s := openTestStore(t)
	jid := "555000002@s.whatsapp.net"

	if err := s.SetMode(jid, "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActive(jid, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(jid, "ignored"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 100}); err != nil {
		t.Fatal(err)
	}

	raw, err := s.PendingChats(10, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 1 {
		t.Fatalf("PendingChats = %d, want 1", len(raw))
	}

	dispatch, err := s.PendingChatsDispatch(10, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatch) != 0 {
		t.Errorf("PendingChatsDispatch = %+v, want empty (status=ignored)", dispatch)
	}
}

// TestPendingChatsDispatchExcludesManualMode is T55: mode='manual' appears in
// PendingChats but never dispatched — PendingChatsDispatch must exclude it.
func TestPendingChatsDispatchExcludesManualMode(t *testing.T) {
	s := openTestStore(t)
	jid := "555000003@s.whatsapp.net"

	if err := s.SetMode(jid, "manual"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActive(jid, true); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 100}); err != nil {
		t.Fatal(err)
	}

	raw, err := s.PendingChats(10, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 1 {
		t.Fatalf("PendingChats = %d, want 1", len(raw))
	}

	dispatch, err := s.PendingChatsDispatch(10, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatch) != 0 {
		t.Errorf("PendingChatsDispatch = %+v, want empty (mode=manual)", dispatch)
	}
}

// TestPendingChatsDispatchExcludesInactive is T55: an inactive chat (active=0,
// not is_boss) appears in PendingChats but not in PendingChatsDispatch.
func TestPendingChatsDispatchExcludesInactive(t *testing.T) {
	s := openTestStore(t)
	jid := "555000004@s.whatsapp.net"

	if err := s.SetMode(jid, "dedicated"); err != nil {
		t.Fatal(err)
	}
	// Deliberately no SetActive — active stays false (schema default).
	if err := s.AddMessage(Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 100}); err != nil {
		t.Fatal(err)
	}

	raw, err := s.PendingChats(10, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 1 {
		t.Fatalf("PendingChats = %d, want 1", len(raw))
	}

	dispatch, err := s.PendingChatsDispatch(10, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatch) != 0 {
		t.Errorf("PendingChatsDispatch = %+v, want empty (active=false, not is_boss)", dispatch)
	}
}

// TestPendingChatsDispatchIncludesBossEvenInactive is T55: is_boss bypasses
// the active/status gate in PendingChatsDispatch — mirroring PendingDedicated.
func TestPendingChatsDispatchIncludesBossEvenInactive(t *testing.T) {
	s := openTestStore(t)
	jid := "555000005@s.whatsapp.net"

	if err := s.SetMode(jid, "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetIsBoss(jid, true); err != nil {
		t.Fatal(err)
	}
	// No SetActive — stays false.
	if err := s.AddMessage(Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 100}); err != nil {
		t.Fatal(err)
	}

	dispatch, err := s.PendingChatsDispatch(10, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatch) != 1 {
		t.Errorf("PendingChatsDispatch = %+v, want 1 (is_boss bypasses active gate)", dispatch)
	}
}

// TestPendingDedicatedBossDispatchesEvenNeverActivated is the fix to
// ct-2026-07-21-1853's own follow-up: ConfigLevel treats IsBoss as an
// unconditional, first-priority case — a boss chat is "boss" level (must
// dispatch) regardless of active/status. The real boss chat commonly never
// goes through set_chat_active/set_config_level (is_boss is set once,
// directly), so active stays at its schema default (false). Without an
// explicit is_boss bypass, PendingDedicated would silently stop dispatching
// to the boss himself.
func TestPendingDedicatedBossDispatchesEvenNeverActivated(t *testing.T) {
	s := openTestStore(t)
	jid := "boss-never-activated@c.us"

	if err := s.SetMode(jid, "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetIsBoss(jid, true); err != nil {
		t.Fatal(err)
	}
	// Deliberately no SetActive/SetConfigLevel call — active stays false,
	// the untouched schema default.
	if err := s.AddMessage(Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}

	c, ok, err := s.GetChat(jid)
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	if ConfigLevel(c) != "boss" {
		t.Fatalf("setup: ConfigLevel = %q, want boss", ConfigLevel(c))
	}
	if c.Active {
		t.Fatal("setup: active should still be false (never explicitly activated)")
	}

	pending, err := s.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Errorf("PendingDedicated for a never-activated boss chat = %+v, want the message dispatched", pending)
	}

	count, err := s.CountPendingDedicated()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("CountPendingDedicated for a never-activated boss chat = %d, want 1", count)
	}
}

// ── T108 (ct-2026-09-01-1413) — MarkHandledBeforeForSender ─────────────────

// TestMarkHandledBeforeForSenderOnlyClosesThatSender is the exact defect
// T108 exists to fix: answering one participant in a group must not mark
// the others' messages handled too.
func TestMarkHandledBeforeForSenderOnlyClosesThatSender(t *testing.T) {
	s := openTestStore(t)
	group := "555001@g.us"
	alice := "555000000001@s.whatsapp.net"
	bob := "555000000002@s.whatsapp.net"
	if err := s.SetMode(group, "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActive(group, true); err != nil {
		t.Fatal(err)
	}
	msgs := []Message{
		{ChatJID: group, ID: "m1", Sender: alice, Text: "pregunta de alice", TS: 100},
		{ChatJID: group, ID: "m2", Sender: bob, Text: "pregunta de bob", TS: 150},
	}
	for _, m := range msgs {
		if err := s.AddMessage(m); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.MarkHandledBeforeForSender(group, alice, 200); err != nil {
		t.Fatal(err)
	}

	pending, err := s.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "m2" {
		t.Fatalf("got %+v, want only Bob's m2 still pending — Alice's answer must not close Bob's question", pending)
	}
}

// TestMarkHandledBeforeForSenderRespectsTimestampBound: same ts<=tsResp
// bound as MarkHandledBefore — a later message from the SAME sender that
// arrived after the bound stays pending.
func TestMarkHandledBeforeForSenderRespectsTimestampBound(t *testing.T) {
	s := openTestStore(t)
	group := "555001@g.us"
	alice := "555000000001@s.whatsapp.net"
	if err := s.SetMode(group, "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActive(group, true); err != nil {
		t.Fatal(err)
	}
	msgs := []Message{
		{ChatJID: group, ID: "m1", Sender: alice, Text: "primero", TS: 100},
		{ChatJID: group, ID: "m2", Sender: alice, Text: "llegó mientras se componía la respuesta", TS: 300},
	}
	for _, m := range msgs {
		if err := s.AddMessage(m); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.MarkHandledBeforeForSender(group, alice, 200); err != nil {
		t.Fatal(err)
	}

	pending, err := s.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "m2" {
		t.Fatalf("got %+v, want m2 (ts=300 > bound 200) still pending", pending)
	}
}
