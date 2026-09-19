package autoreply

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"piumy-gateway/internal/bridge"
	"piumy-gateway/internal/store"
)

// fakeBridge records which chat it was asked to draft for (and when) plus
// the ChatInfo it received, and always returns the same canned decision — a
// mock so tests don't need a real DeepSeek key.
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
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
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

// TestWorkerSendsWhenRulesFreeConfirmation covers: a reply the bridge
// explicitly frees from confirmation (its read of this chat's rules) goes
// STRAIGHT to the outbox — no draft, no human step.
func TestWorkerSendsWhenRulesFreeConfirmation(t *testing.T) {
	fb := &fakeBridge{should: true, draft: "hola, gracias por escribir", needsConfirmation: false}
	st, w := newTestWorker(t, fb)

	jid := "55500000001@c.us"
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

// TestWorkerHoldsWhenBridgeNeedsConfirmation covers the default confirmation
// path: the bridge's own read of the rules (NeedsConfirmation, true by
// default) holds the reply as a pending draft directed at the confirmer it
// named, instead of sending it.
func TestWorkerHoldsWhenBridgeNeedsConfirmation(t *testing.T) {
	fb := &fakeBridge{
		should:            true,
		draft:             "reviso el stock y te confirmo",
		needsConfirmation: true,
		confirmer:         "55500000002@c.us",
	}
	st, w := newTestWorker(t, fb)

	jid := "55500000001@c.us"
	if err := st.TouchChat(jid, "Contact", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMode(jid, "auto"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetActive(jid, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatRules(jid, "si involucra stock, confirmar con el bodeguero 55500000002"); err != nil {
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
	if len(drafts) != 1 || drafts[0].ChatJID != jid || drafts[0].Text != "reviso el stock y te confirmo" || drafts[0].Confirmer != "55500000002@c.us" || drafts[0].Status != "pending" {
		t.Fatalf("got drafts=%+v, want one pending draft directed at the bridge's confirmer", drafts)
	}
}

// TestWorkerPassesConfirmationBaselineToBridge covers the confirmation
// baseline plumbing: the worker computes ChatInfo.DefaultConfirm from this
// chat's ConfirmationMode (by-type default from TouchChat, or an owner
// override) and hands it to the bridge — the bridge's own verdict (trusted
// as-is, per TestWorkerSendsWhenRulesFreeConfirmation /
// TestWorkerHoldsWhenBridge...) is what the worker actually acts on, not a
// re-check of this baseline.
func TestWorkerPassesConfirmationBaselineToBridge(t *testing.T) {
	fb := &fakeBridge{should: false} // shouldReply=false: never sends/holds, only DefaultConfirm matters here
	st, w := newTestWorker(t, fb)

	oneOnOne := "55500000001@c.us"
	if err := st.TouchChat(oneOnOne, "Contact", 1); err != nil {
		t.Fatal(err) // defaults ConfirmationMode="none"
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
		t.Fatalf("1-1 chat's baseline passed to Draft = %+v, want DefaultConfirm=false (default)", fb.infos)
	}

	// The owner overrides this same chat to "always" — the baseline must
	// follow, even though it's a 1-1 chat.
	if err := st.SetConfirmationMode(oneOnOne, "always"); err != nil {
		t.Fatal(err)
	}
	w.sweep(context.Background())
	if len(fb.infos) != 2 || !fb.infos[1].DefaultConfirm {
		t.Fatalf("after owner override to always, baseline passed to Draft = %+v, want DefaultConfirm=true", fb.infos)
	}
}

// TestWorkerConfirmationBaselineTreatsDiscretionAsConfirm covers the
// worker's mapping of the 3-state scheme onto its plain bool: "discretion"
// has no live agent here to exercise per-message judgment, so it leans
// conservative (DefaultConfirm=true), same as "always" — only "none"
// auto-sends.
func TestWorkerConfirmationBaselineTreatsDiscretionAsConfirm(t *testing.T) {
	fb := &fakeBridge{should: false}
	st, w := newTestWorker(t, fb)

	jid := "55500000003@c.us"
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
	if err := st.SetConfirmationMode(jid, "discretion"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}

	w.sweep(context.Background())

	if len(fb.infos) != 1 || !fb.infos[0].DefaultConfirm {
		t.Fatalf("discretion baseline passed to Draft = %+v, want DefaultConfirm=true", fb.infos)
	}
}

// TestWorkerSkipsIneligibleChats covers the negative cases in one table:
// not-auto, inactive, no-rules, ignored-group, and blacklisted chats never
// reach the bridge, so no draft is created for any of them.
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

	notAuto := "notauto@c.us"
	setup(notAuto, "dedicated", true, "", true)

	inactive := "inactive@c.us"
	setup(inactive, "auto", false, "", true)

	noRules := "norules@c.us"
	setup(noRules, "auto", true, "", false)

	// A group defaults to status "ignored" even with rules set.
	group := "12345-67890@g.us"
	setup(group, "auto", true, "", true)

	// ST-B regression (ct-2026-07-11-0741): a 1-1 chat explicitly silenced
	// by the owner (status "ignored") — before the fix, eligible()'s
	// `!isGroupJID(p.JID) || p.Status != "ignored"` was vacuously true for
	// any 1-1, so this one still got autoreplied.
	ignoredOneOnOne := "ignored1on1@c.us"
	setup(ignoredOneOnOne, "auto", true, "ignored", true)

	blacklisted := "blacklisted@c.us"
	setup(blacklisted, "auto", true, "blacklist", true)

	w.sweep(context.Background())

	if len(fb.calls) != 0 {
		t.Fatalf("bridge calls = %v, want none — all 6 chats are ineligible", fb.calls)
	}
	drafts, err := st.PendingDrafts(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 0 {
		t.Fatalf("got %d drafts, want 0", len(drafts))
	}
}

// TestWorkerDraftsWithOnlyDefaultOrTypeRules covers: a chat with NO
// particular rules of its own still gets a draft attempt if it inherits
// type or default rules — this is exactly the case eligible() can no longer
// filter cheaply (see its doc comment), so the worker must still act
// correctly end to end via draftFor's EffectiveRules.
func TestWorkerDraftsWithOnlyDefaultOrTypeRules(t *testing.T) {
	t.Run("default rules only", func(t *testing.T) {
		fb := &fakeBridge{should: true, draft: "ok", needsConfirmation: false}
		st, w := newTestWorker(t, fb)

		jid := "55500000001@c.us"
		if err := st.TouchChat(jid, "Contact", 1); err != nil {
			t.Fatal(err)
		}
		if err := st.SetMode(jid, "auto"); err != nil {
			t.Fatal(err)
		}
		if err := st.SetActive(jid, true); err != nil {
			t.Fatal(err)
		}
		// T79 (ct-2026-08-27-2034) removed SetDefaultRules — jid has no
		// ContactName, so the new-number origin tier is what EffectiveRules
		// actually reads for it.
		if err := st.KVSet(store.SettingRulesDefaultNewNumber, "default: responder con cortesía"); err != nil {
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

	// "type rules only" (rules_type_individual) was replaced by the origin
	// axis for individual chats (M5, ct-2026-07-22-1903, EffectiveRules'
	// own doc) — rules_type_individual is no longer read for a 1-1 chat;
	// this subtest now exercises the tier that took its exact place:
	// rules_default_new_number (this jid has no contact_name, so it's a
	// "new number" for the origin split).
	t.Run("origin rules only (new number)", func(t *testing.T) {
		fb := &fakeBridge{should: true, draft: "ok", needsConfirmation: false}
		st, w := newTestWorker(t, fb)

		jid := "55500000001@c.us"
		if err := st.TouchChat(jid, "Contact", 1); err != nil {
			t.Fatal(err)
		}
		if err := st.SetMode(jid, "auto"); err != nil {
			t.Fatal(err)
		}
		if err := st.SetActive(jid, true); err != nil {
			t.Fatal(err)
		}
		if err := st.KVSet(store.SettingRulesDefaultNewNumber, "número nuevo: sé breve"); err != nil {
			t.Fatal(err)
		}
		if err := st.AddMessage(store.Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
			t.Fatal(err)
		}

		w.sweep(context.Background())

		if len(fb.calls) != 1 || fb.calls[0] != jid {
			t.Fatalf("bridge calls = %v, want exactly one call — a chat with only origin (new number) rules must still be eligible", fb.calls)
		}
		if len(fb.infos) != 1 || fb.infos[0].Rules != "número nuevo: sé breve" {
			t.Errorf("ChatInfo.Rules = %+v, want the resolved origin rules", fb.infos)
		}
	})
}

// TestWorkerDraftsActivatedGroup covers the group half: a group with rules
// AND a non-"ignored" status IS eligible — groups are chats too.
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

// TestWorkerRespectsShouldReplyFalse covers the bridge saying "don't reply"
// — no draft even for an otherwise-eligible chat.
func TestWorkerRespectsShouldReplyFalse(t *testing.T) {
	fb := &fakeBridge{should: false, draft: ""}
	st, w := newTestWorker(t, fb)

	jid := "55500000001@c.us"
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

// TestWorkerPassesChatInfoToBridge covers: a chat's persisted memory/
// context/rules must reach the bridge's Draft call as ChatInfo.
func TestWorkerPassesChatInfoToBridge(t *testing.T) {
	fb := &fakeBridge{should: false}
	st, w := newTestWorker(t, fb)

	jid := "55500000001@c.us"
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

// TestWorkerPacesBetweenCalls covers: with two eligible chats, the second
// Draft call must not fire immediately after the first.
func TestWorkerPacesBetweenCalls(t *testing.T) {
	fb := &fakeBridge{should: false}
	st, w := newTestWorker(t, fb)
	w.Delay = 50 * time.Millisecond

	for i, jid := range []string{"a@c.us", "b@c.us"} {
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
