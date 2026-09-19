package whatsmeow

import (
	"context"
	"testing"
)

// invalidJID has a dot in the user part that doesn't parse as a device
// number — types.ParseJID's one real error path (confirmed by reading its
// source; most malformed strings degrade gracefully instead of erroring).
const invalidJID = "123.abc@s.whatsapp.net"

func TestSendRejectsInvalidJID(t *testing.T) {
	a := newTestAdapter()
	if _, err := a.Send(context.Background(), invalidJID, "hola"); err == nil {
		t.Error("Send with an invalid JID: want an error, got nil")
	}
}

func TestSetTypingRejectsInvalidJID(t *testing.T) {
	a := newTestAdapter()
	if err := a.SetTyping(context.Background(), invalidJID, true); err == nil {
		t.Error("SetTyping with an invalid JID: want an error, got nil")
	}
}

func TestMarkReadRejectsInvalidJID(t *testing.T) {
	a := newTestAdapter()
	if err := a.MarkRead(context.Background(), invalidJID, "999@s.whatsapp.net", []string{"m1"}); err == nil {
		t.Error("MarkRead with an invalid chat JID: want an error, got nil")
	}
}

// TestMarkReadRejectsInvalidSenderJID (T127, ct-2026-09-02-2249): the new
// senderJID param is parsed too — a bad one has to error just like a bad
// chatJID, not get silently swallowed.
func TestMarkReadRejectsInvalidSenderJID(t *testing.T) {
	a := newTestAdapter()
	if err := a.MarkRead(context.Background(), "999@s.whatsapp.net", invalidJID, []string{"m1"}); err == nil {
		t.Error("MarkRead with an invalid sender JID: want an error, got nil")
	}
}

func TestMarkDeliveredIsNoOp(t *testing.T) {
	a := newTestAdapter()
	if err := a.MarkDelivered(context.Background(), "123@s.whatsapp.net", []string{"m1"}); err != nil {
		t.Errorf("MarkDelivered = %v, want nil (no-op)", err)
	}
}
