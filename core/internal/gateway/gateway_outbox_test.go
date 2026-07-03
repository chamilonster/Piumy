// SPDX-License-Identifier: AGPL-3.0-only
package gateway

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"

	"pimywa/internal/store"
)

// TestSentMessageRow covers the DoD directly: after a simulated successful
// send, the resulting messages row carries the model from the outbox item
// and the REAL WhatsApp message ID from the send response — not anything
// guessed at enqueue time — so delivery/read receipts (which reference that
// real ID) can match it later.
func TestSentMessageRow(t *testing.T) {
	item := store.Outbox{Seq: 7, ToJID: "56955147132@s.whatsapp.net", Text: "hola", Model: "claude-opus-4-8"}
	ts := time.Unix(1700000000, 0)
	resp := whatsmeow.SendResponse{ID: types.MessageID("3EB0ABCDEF123"), Timestamp: ts}

	got := sentMessageRow("56955147132@s.whatsapp.net", item, resp)

	if got.ID != "3EB0ABCDEF123" {
		t.Errorf("ID = %q, want the real send-response ID", got.ID)
	}
	if got.Model != "claude-opus-4-8" {
		t.Errorf("Model = %q, want claude-opus-4-8", got.Model)
	}
	if !got.FromMe {
		t.Error("FromMe = false, want true (this is our own reply)")
	}
	if got.Text != "hola" {
		t.Errorf("Text = %q, want %q", got.Text, "hola")
	}
	if got.TS != ts.Unix() {
		t.Errorf("TS = %d, want %d (the real send timestamp)", got.TS, ts.Unix())
	}
}

// TestSentMessageRowHumanSend covers the human-sent case (REST/dashboard,
// no model concept): Model stays empty, everything else still populates.
func TestSentMessageRowHumanSend(t *testing.T) {
	item := store.Outbox{Seq: 1, ToJID: "j", Text: "manual reply"} // Model left empty
	resp := whatsmeow.SendResponse{ID: types.MessageID("ID1"), Timestamp: time.Unix(1, 0)}

	got := sentMessageRow("j", item, resp)
	if got.Model != "" {
		t.Errorf("Model = %q, want empty for a human-sent message", got.Model)
	}
}
