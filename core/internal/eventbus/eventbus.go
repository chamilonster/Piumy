// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
//
// Package eventbus: a tiny in-process pub/sub so a connected MCP agent can
// be notified over SSE (entregable D) instead of polling. Standalone
// on purpose — no dependency on store/gateway/mcpserver/restapi, so any of
// them can import it directly with zero cycle risk (the same reasoning
// already used for sessionbackup/mcpguard).
//
// An Event is a low-latency NUDGE, not a second source of truth: it carries
// no message content, just "something changed in this chat, go check" — the
// agent still calls get_pending/get_chat/get_messages (all still gated the
// same way they always were) to find out what actually happened. A dropped
// event is never a correctness problem for that reason; the agent's next
// ordinary poll finds it anyway.
package eventbus

import "sync"

// subscriberBuffer bounds how many unread events a slow subscriber can queue
// before Publish starts dropping ITS events (never blocking the publisher —
// see Publish's doc comment). 8 is generous for a "go check" nudge: a
// subscriber that's behind by more than 8 chat-changed notices is already
// going to re-sync via get_pending on its next poll regardless.
const subscriberBuffer = 8

// Event is deliberately minimal: type/jid/ts only, never message text — see
// the package doc comment for why.
type Event struct {
	Type string `json:"type"`          // "message" (v1's only kind; forward-compatible for more later)
	JID  string `json:"jid,omitempty"` // the chat this event is about
	TS   int64  `json:"ts"`            // event time, unix seconds
}

// Bus fans Publish out to every current Subscribe-r. The zero value is not
// usable — construct with New. A nil *Bus is safe to use (both methods
// no-op) so callers that never wire one up (tests, a stripped build) don't
// need to nil-check before every call — same pattern as mcpguard.Guard /
// sessionbackup.Backuper.
type Bus struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

func New() *Bus {
	return &Bus{subs: make(map[chan Event]struct{})}
}

// Subscribe registers a new listener and returns its channel plus an
// unsubscribe func the caller MUST call when done (typically via defer) —
// otherwise the channel and its map entry leak for the lifetime of the
// process, which matters on a long-running Pi service serving many
// short-lived SSE connections over time.
func (b *Bus) Subscribe() (ch <-chan Event, unsubscribe func()) {
	if b == nil {
		// A closed, already-done channel: ranging/receiving from it returns
		// immediately with ok=false, so a caller that forgot to nil-check
		// still behaves correctly (just never receives anything) instead of
		// panicking on a nil map.
		c := make(chan Event)
		close(c)
		return c, func() {}
	}
	c := make(chan Event, subscriberBuffer)
	b.mu.Lock()
	b.subs[c] = struct{}{}
	b.mu.Unlock()
	return c, func() {
		b.mu.Lock()
		if _, ok := b.subs[c]; ok {
			delete(b.subs, c)
			close(c)
		}
		b.mu.Unlock()
	}
}

// Publish fans e out to every current subscriber and NEVER blocks — this is
// the guarantee the whole package exists to provide: Publish is called from
// the WhatsApp message-receiving path (gateway.onMessage), the hottest path
// in the system, and a slow/stuck SSE client must never be able to stall it.
// A subscriber whose buffer is already full (subscriberBuffer) simply misses
// THIS event — nobody else is affected, and per the package doc comment
// that's an acceptable trade-off (the agent's next poll catches up).
func (b *Bus) Publish(e Event) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// Subscribers reports how many listeners are currently registered — for
// tests/observability (proving a handler's deferred unsubscribe actually
// ran on disconnect, and a future dashboard status if that's ever wanted).
// Nil-safe like everything else here.
func (b *Bus) Subscribers() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
