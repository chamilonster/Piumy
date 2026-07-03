// SPDX-License-Identifier: AGPL-3.0-only
package autoreply

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"pimywa/internal/bridge"
	"pimywa/internal/store"
)

// fakeBridge records which chat it was asked to draft for (and when) plus
// the ChatInfo it received, and always returns the same canned decision — a
// mock so tests don't need a real DeepSeek key/session.
type fakeBridge struct {
	mu     sync.Mutex
	calls  []string
	callTS []time.Time
	infos  []bridge.ChatInfo

	should            bool
	draft             string
	needsConfirmation bool
	confirmer         string
}

func (f *fakeBridge) Draft(ctx context.Context, msgs []store.Message, policy string, info bridge.ChatInfo) (bridge.Decision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	chat := ""
	if len(msgs) > 0 {
		chat = msgs[0].ChatJID
	}
	f.calls = append(f.calls, chat)
	f.callTS = append(f.callTS, time.Now())
	f.infos = append(f.infos, info)
	return bridge.Decision{
		ShouldReply:       f.should,
		Draft:             f.draft,
		NeedsConfirmation: f.needsConfirmation,
		Confirmer:         f.confirmer,
	}, nil
}

func newTestWorker(t *testing.T, fb *fakeBridge) (*store.Store, *Worker) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	return st, &Worker{
		Store:     st,
		Bridge:    fb,
		Policy:    func() string { return "test policy" },
		ModelName: "deepseek-chat",
		Delay:     5 * time.Millisecond,
	}
}

// TestWorkerSendsWhenRulesFreeConfirmation covers the DoD (0800): a reply the
// bridge explicitly frees from confirmation (its read of this chat's rules)
// goes STRAIGHT to the outbox — no draft, no human step.
func TestWorkerSendsWhenRulesFreeConfirmation(t *testing.T) {
	fb := &fakeBridge{should: true, draft: "hola, gracias por escribir", needsConfirmation: false}
	st, w := newTestWorker(t, fb)

	jid := "56999999999@s.whatsapp.net"
	if err := st.TouchChat(jid, "Contact", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMode(jid, "auto"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetActive(jid, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatRules(jid, "respondé sola en este chat"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}

	w.sweep(context.Background())

	if len(fb.calls) != 1 || fb.calls[0] != jid {
		t.Fatalf("bridge calls = %v, want exactly one call for %s", fb.calls, jid)
	}
	outbox, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(outbox) != 1 || outbox[0].ToJID != jid || outbox[0].Text != "hola, gracias por escribir" || outbox[0].Model != "deepseek-chat" {
		t.Fatalf("got outbox=%+v, want the reply queued directly for %s (no confirmation)", outbox, jid)
	}
	drafts, err := st.PendingDrafts(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 0 {
		t.Fatalf("got %d drafts, want 0 — default is no confirmation, no draft", len(drafts))
	}
}

// TestWorkerHoldsWhenBridgeNeedsConfirmation covers the DoD's default
// confirmation path (0800): the bridge's own read of the rules
// (NeedsConfirmation, true by default) holds the reply as a pending draft
// directed at the confirmer it named, instead of sending it.
func TestWorkerHoldsWhenBridgeNeedsConfirmation(t *testing.T) {
	fb := &fakeBridge{
		should:            true,
		draft:             "reviso el stock y te confirmo",
		needsConfirmation: true,
		confirmer:         "56911112222@s.whatsapp.net",
	}
	st, w := newTestWorker(t, fb)

	jid := "56999999999@s.whatsapp.net"
	if err := st.TouchChat(jid, "Contact", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMode(jid, "auto"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetActive(jid, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatRules(jid, "si involucra stock, confirmar con el bodeguero 56911112222"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hay stock?", TS: 1}); err != nil {
		t.Fatal(err)
	}

	w.sweep(context.Background())

	outbox, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(outbox) != 0 {
		t.Fatalf("outbox = %+v, want empty — a reply needing confirmation must not send", outbox)
	}
	drafts, err := st.PendingDrafts(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 || drafts[0].ChatJID != jid || drafts[0].Text != "reviso el stock y te confirmo" || drafts[0].Confirmer != "56911112222@s.whatsapp.net" || drafts[0].Status != "pending" {
		t.Fatalf("got drafts=%+v, want one pending draft directed at the bridge's confirmer", drafts)
	}
}

// TestWorkerPassesConfirmationBaselineToBridge covers 0810's baseline
// plumbing: the worker computes ChatInfo.DefaultConfirm from this chat's
// ConfirmationMode (by-type default from TouchChat, or an owner override) and
// hands it to the bridge — the bridge's own verdict (trusted as-is, per
// TestWorkerSendsWhenRulesFreeConfirmation / TestWorkerHoldsWhenBridge...)
// is what the worker actually acts on, not a re-check of this baseline.
func TestWorkerPassesConfirmationBaselineToBridge(t *testing.T) {
	fb := &fakeBridge{should: false} // shouldReply=false: never sends/holds, only DefaultConfirm matters here
	st, w := newTestWorker(t, fb)

	oneOnOne := "56999999999@s.whatsapp.net"
	if err := st.TouchChat(oneOnOne, "Contact", 1); err != nil {
		t.Fatal(err) // defaults ConfirmationMode="none" (0810)
	}
	if err := st.SetMode(oneOnOne, "auto"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetActive(oneOnOne, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatRules(oneOnOne, "responder normalmente"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: oneOnOne, ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}

	w.sweep(context.Background())

	if len(fb.infos) != 1 || fb.infos[0].DefaultConfirm {
		t.Fatalf("1-1 chat's baseline passed to Draft = %+v, want DefaultConfirm=false (0810 default)", fb.infos)
	}

	// The owner overrides this same chat to "required" — the baseline must
	// follow, even though it's a 1-1 chat.
	if err := st.SetConfirmationMode(oneOnOne, "required"); err != nil {
		t.Fatal(err)
	}
	w.sweep(context.Background())
	if len(fb.infos) != 2 || !fb.infos[1].DefaultConfirm {
		t.Fatalf("after owner override to required, baseline passed to Draft = %+v, want DefaultConfirm=true", fb.infos)
	}
}

// TestWorkerSkipsIneligibleChats covers the DoD's negative cases in one
// table: not-auto, inactive, no-rules, ignored-group, and blacklisted chats
// never reach the bridge, so no draft is created for any of them.
func TestWorkerSkipsIneligibleChats(t *testing.T) {
	fb := &fakeBridge{should: true, draft: "should never appear"}
	st, w := newTestWorker(t, fb)

	// setup covers the eligible baseline (auto+active+rules+not blacklisted)
	// so each case below deviates in exactly one dimension.
	setup := func(jid, mode string, active bool, status string, withRules bool) {
		t.Helper()
		if err := st.TouchChat(jid, "X", 1); err != nil {
			t.Fatal(err)
		}
		if err := st.SetMode(jid, mode); err != nil {
			t.Fatal(err)
		}
		if err := st.SetActive(jid, active); err != nil {
			t.Fatal(err)
		}
		if status != "" {
			if err := st.SetStatus(jid, status); err != nil {
				t.Fatal(err)
			}
		}
		if withRules {
			if err := st.SetChatRules(jid, "responder normalmente"); err != nil {
				t.Fatal(err)
			}
		}
		if err := st.AddMessage(store.Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
			t.Fatal(err)
		}
	}

	notAuto := "notauto@s.whatsapp.net"
	setup(notAuto, "advanced", true, "", true)

	inactive := "inactive@s.whatsapp.net"
	setup(inactive, "auto", false, "", true)

	noRules := "norules@s.whatsapp.net"
	setup(noRules, "auto", true, "", false)

	// A group defaults to status "ignored" (0800) even with rules set.
	group := "12345-67890@g.us"
	setup(group, "auto", true, "", true)

	blacklisted := "blacklisted@s.whatsapp.net"
	setup(blacklisted, "auto", true, "blacklist", true)

	w.sweep(context.Background())

	if len(fb.calls) != 0 {
		t.Fatalf("bridge calls = %v, want none — all 5 chats are ineligible", fb.calls)
	}
	drafts, err := st.PendingDrafts(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 0 {
		t.Fatalf("got %d drafts, want 0", len(drafts))
	}
}

// TestWorkerDraftsWithOnlyDefaultOrTypeRules covers the DoD directly
// (1959): a chat with NO particular rules of its own still gets a draft
// attempt if it inherits type or default rules — this is exactly the case
// eligible() can no longer filter cheaply (see its doc comment), so the
// worker must still act correctly end to end via draftFor's EffectiveRules.
func TestWorkerDraftsWithOnlyDefaultOrTypeRules(t *testing.T) {
	t.Run("default rules only", func(t *testing.T) {
		fb := &fakeBridge{should: true, draft: "ok", needsConfirmation: false}
		st, w := newTestWorker(t, fb)

		jid := "56999999999@s.whatsapp.net"
		if err := st.TouchChat(jid, "Contact", 1); err != nil {
			t.Fatal(err)
		}
		if err := st.SetMode(jid, "auto"); err != nil {
			t.Fatal(err)
		}
		if err := st.SetActive(jid, true); err != nil {
			t.Fatal(err)
		}
		if err := st.SetDefaultRules("default: responder con cortesía"); err != nil {
			t.Fatal(err)
		}
		if err := st.AddMessage(store.Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
			t.Fatal(err)
		}

		w.sweep(context.Background())

		if len(fb.calls) != 1 || fb.calls[0] != jid {
			t.Fatalf("bridge calls = %v, want exactly one call — a chat with only default rules must still be eligible", fb.calls)
		}
		if len(fb.infos) != 1 || fb.infos[0].Rules != "default: responder con cortesía" {
			t.Errorf("ChatInfo.Rules = %+v, want the resolved default rules", fb.infos)
		}
	})

	t.Run("type rules only", func(t *testing.T) {
		fb := &fakeBridge{should: true, draft: "ok", needsConfirmation: false}
		st, w := newTestWorker(t, fb)

		jid := "56999999999@s.whatsapp.net"
		if err := st.TouchChat(jid, "Contact", 1); err != nil {
			t.Fatal(err)
		}
		if err := st.SetMode(jid, "auto"); err != nil {
			t.Fatal(err)
		}
		if err := st.SetActive(jid, true); err != nil {
			t.Fatal(err)
		}
		if err := st.SetTypeRules("individual", "tipo individual: sé breve"); err != nil {
			t.Fatal(err)
		}
		if err := st.AddMessage(store.Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
			t.Fatal(err)
		}

		w.sweep(context.Background())

		if len(fb.calls) != 1 || fb.calls[0] != jid {
			t.Fatalf("bridge calls = %v, want exactly one call — a chat with only type rules must still be eligible", fb.calls)
		}
		if len(fb.infos) != 1 || fb.infos[0].Rules != "tipo individual: sé breve" {
			t.Errorf("ChatInfo.Rules = %+v, want the resolved type rules", fb.infos)
		}
	})
}

// TestWorkerDraftsActivatedGroup covers the DoD's group half (0800): a group
// with rules AND a non-"ignored" status IS eligible — groups are chats too.
func TestWorkerDraftsActivatedGroup(t *testing.T) {
	fb := &fakeBridge{should: true, draft: "ok", needsConfirmation: false}
	st, w := newTestWorker(t, fb)

	jid := "12345-67890@g.us"
	if err := st.TouchChat(jid, "Group", 1); err != nil {
		t.Fatal(err) // defaults to status "ignored"
	}
	if err := st.SetMode(jid, "auto"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetActive(jid, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatRules(jid, "solo contestar si te preguntan a @numero"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatus(jid, "whitelist"); err != nil {
		t.Fatal(err) // un-ignores it
	}
	if err := st.AddMessage(store.Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola @numero", TS: 1}); err != nil {
		t.Fatal(err)
	}

	w.sweep(context.Background())

	if len(fb.calls) != 1 || fb.calls[0] != jid {
		t.Fatalf("bridge calls = %v, want exactly one call for the activated group %s", fb.calls, jid)
	}
}

// TestWorkerRespectsShouldReplyFalse covers the bridge saying "don't reply" —
// no draft even for an otherwise-eligible chat.
func TestWorkerRespectsShouldReplyFalse(t *testing.T) {
	fb := &fakeBridge{should: false, draft: ""}
	st, w := newTestWorker(t, fb)

	jid := "56999999999@s.whatsapp.net"
	if err := st.TouchChat(jid, "Contact", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMode(jid, "auto"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetActive(jid, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatRules(jid, "responder normalmente"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}

	w.sweep(context.Background())

	if len(fb.calls) != 1 {
		t.Fatalf("bridge calls = %v, want exactly one call (still asked)", fb.calls)
	}
	drafts, err := st.PendingDrafts(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 0 {
		t.Fatalf("got %d drafts, want 0 — bridge said shouldReply=false", len(drafts))
	}
}

// TestWorkerPassesChatInfoToBridge covers the DoD's "el Draft incluye
// memory+context+rules del chat" (0647): a chat's persisted memory/context/
// rules must reach the bridge's Draft call as ChatInfo.
func TestWorkerPassesChatInfoToBridge(t *testing.T) {
	fb := &fakeBridge{should: false}
	st, w := newTestWorker(t, fb)

	jid := "56999999999@s.whatsapp.net"
	if err := st.TouchChat(jid, "Contact", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMode(jid, "auto"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetActive(jid, true); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatMemory(jid, "le gusta el café"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatContext(jid, "cliente frecuente"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatRules(jid, "tratarlo de usted"); err != nil {
		t.Fatal(err)
	}

	w.sweep(context.Background())

	if len(fb.infos) != 1 {
		t.Fatalf("bridge infos = %v, want exactly one call", fb.infos)
	}
	got := fb.infos[0]
	if got.Memory != "le gusta el café" || got.Context != "cliente frecuente" || got.Rules != "tratarlo de usted" {
		t.Errorf("ChatInfo passed to Draft = %+v, want the chat's persisted memory/context/rules", got)
	}
}

// TestWorkerPacesBetweenCalls covers the DoD's "pacea entre llamadas": with
// two eligible chats, the second Draft call must not fire immediately after
// the first.
func TestWorkerPacesBetweenCalls(t *testing.T) {
	fb := &fakeBridge{should: false}
	st, w := newTestWorker(t, fb)
	w.Delay = 50 * time.Millisecond

	for i, jid := range []string{"a@s.whatsapp.net", "b@s.whatsapp.net"} {
		if err := st.TouchChat(jid, "X", int64(i+1)); err != nil {
			t.Fatal(err)
		}
		if err := st.SetMode(jid, "auto"); err != nil {
			t.Fatal(err)
		}
		if err := st.SetActive(jid, true); err != nil {
			t.Fatal(err)
		}
		if err := st.SetChatRules(jid, "responder normalmente"); err != nil {
			t.Fatal(err)
		}
		if err := st.AddMessage(store.Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: int64(i + 1)}); err != nil {
			t.Fatal(err)
		}
	}

	w.sweep(context.Background())

	if len(fb.callTS) != 2 {
		t.Fatalf("got %d bridge calls, want 2", len(fb.callTS))
	}
	gap := fb.callTS[1].Sub(fb.callTS[0])
	if gap < 40*time.Millisecond { // allow scheduling slack under the 50ms delay
		t.Errorf("gap between Draft calls = %v, want >= ~%v (Delay)", gap, w.Delay)
	}
}
