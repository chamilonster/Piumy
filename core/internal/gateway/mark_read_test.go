// SPDX-License-Identifier: AGPL-3.0-only
package gateway

import (
	"testing"

	"pimywa/internal/store"
)

func TestNeedsReadReceipt(t *testing.T) {
	cases := []struct {
		name string
		m    store.Message
		want bool
	}{
		{"inbound unread", store.Message{FromMe: false, ReadTS: 0}, true},
		{"inbound already read", store.Message{FromMe: false, ReadTS: 100}, false},
		{"outbound", store.Message{FromMe: true, ReadTS: 0}, false},
		{"outbound also read", store.Message{FromMe: true, ReadTS: 100}, false},
	}
	for _, c := range cases {
		if got := needsReadReceipt(c.m); got != c.want {
			t.Errorf("%s: needsReadReceipt = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestMarkReadMessagesSkipsWhatDoesntNeedIt covers the DoD's "not marked
// automatically" side from the gateway's angle: messages that don't need a
// receipt (outbound, or already read) must never reach scheduleMarkRead —
// proven here by using a zero-value Gateway (nil client): touching the
// client would panic, so a clean return proves nothing was scheduled.
func TestMarkReadMessagesSkipsWhatDoesntNeedIt(t *testing.T) {
	g := &Gateway{} // client is nil — scheduleMarkRead would panic if reached
	msgs := []store.Message{
		{ChatJID: "j", ID: "m1", FromMe: true, Sender: "j", TS: 1},                // outbound
		{ChatJID: "j", ID: "m2", FromMe: false, Sender: "j", TS: 2, ReadTS: 100}, // already read
	}
	g.markReadMessages("j", msgs) // must not panic
}
