// SPDX-License-Identifier: AGPL-3.0-only
package eventbus

import (
	"testing"
	"time"
)

func TestPublishDeliversToSubscriber(t *testing.T) {
	b := New()
	ch, unsubscribe := b.Subscribe()
	defer unsubscribe()

	b.Publish(Event{Type: "message", JID: "a@s.whatsapp.net", TS: 42})

	select {
	case e := <-ch:
		if e.Type != "message" || e.JID != "a@s.whatsapp.net" || e.TS != 42 {
			t.Errorf("received = %+v, want {message a@s.whatsapp.net 42}", e)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the published event")
	}
}

// TestPublishFansOutToEveryone covers that every subscriber gets its own
// copy — one slow reader must not steal another's event.
func TestPublishFansOutToEveryone(t *testing.T) {
	b := New()
	ch1, unsub1 := b.Subscribe()
	defer unsub1()
	ch2, unsub2 := b.Subscribe()
	defer unsub2()

	b.Publish(Event{Type: "message", JID: "x", TS: 1})

	for i, ch := range []<-chan Event{ch1, ch2} {
		select {
		case e := <-ch:
			if e.JID != "x" {
				t.Errorf("subscriber %d got %+v, want jid=x", i, e)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timed out", i)
		}
	}
}

// TestPublishNeverBlocksOnFullSubscriber is THE guarantee this package
// exists to provide (entregable D): a subscriber that never reads
// must not be able to stall Publish, since Publish runs on the WhatsApp
// message-receiving path.
func TestPublishNeverBlocksOnFullSubscriber(t *testing.T) {
	b := New()
	_, unsubscribe := b.Subscribe() // never read from — simulates a stuck client
	defer unsubscribe()

	done := make(chan struct{})
	go func() {
		for i := 0; i < subscriberBuffer+5; i++ { // overflow the buffer on purpose
			b.Publish(Event{Type: "message", JID: "x", TS: int64(i)})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a full/unread subscriber — it must always be non-blocking")
	}
}

// TestPublishNoSubscribersIsANoOp covers the "zero suscriptores" fail-safe:
// publishing to an empty bus must not panic or error.
func TestPublishNoSubscribersIsANoOp(t *testing.T) {
	b := New()
	b.Publish(Event{Type: "message", JID: "x", TS: 1}) // must not panic
}

// TestUnsubscribeStopsDelivery covers cleanup: after unsubscribing, further
// publishes don't reach the (now closed) channel, and reading from it
// returns immediately (closed), matching the SSE handler's exit path.
func TestUnsubscribeStopsDelivery(t *testing.T) {
	b := New()
	ch, unsubscribe := b.Subscribe()
	unsubscribe()

	b.Publish(Event{Type: "message", JID: "x", TS: 1}) // must not panic (no map access on stale key)

	select {
	case _, ok := <-ch:
		if ok {
			t.Error("channel still open/receiving after unsubscribe")
		}
	case <-time.After(time.Second):
		t.Fatal("reading a closed channel should return immediately")
	}
}

// TestNilBusIsSafe covers the "not wired up anywhere" fail-safe: a nil *Bus
// must never panic, on either Publish or Subscribe.
func TestNilBusIsSafe(t *testing.T) {
	var b *Bus
	b.Publish(Event{Type: "message", JID: "x", TS: 1}) // must not panic

	ch, unsubscribe := b.Subscribe()
	defer unsubscribe() // must not panic either

	select {
	case _, ok := <-ch:
		if ok {
			t.Error("nil bus's Subscribe channel should be immediately closed/empty")
		}
	default:
		t.Error("nil bus's Subscribe channel should be already-closed, not blocking on a plain receive")
	}
	if n := b.Subscribers(); n != 0 {
		t.Errorf("nil bus Subscribers() = %d, want 0", n)
	}
}

// TestSubscribersCountsAndDrops covers Subscribers() itself — the count
// tracks Subscribe/unsubscribe accurately (used by the SSE handler's leak
// test in restapi to prove a disconnect actually cleans up).
func TestSubscribersCountsAndDrops(t *testing.T) {
	b := New()
	if n := b.Subscribers(); n != 0 {
		t.Fatalf("Subscribers() on a fresh bus = %d, want 0", n)
	}
	_, unsub1 := b.Subscribe()
	_, unsub2 := b.Subscribe()
	if n := b.Subscribers(); n != 2 {
		t.Fatalf("Subscribers() with 2 subscribed = %d, want 2", n)
	}
	unsub1()
	if n := b.Subscribers(); n != 1 {
		t.Fatalf("Subscribers() after 1 unsubscribes = %d, want 1", n)
	}
	unsub2()
	if n := b.Subscribers(); n != 0 {
		t.Fatalf("Subscribers() after both unsubscribe = %d, want 0", n)
	}
}
