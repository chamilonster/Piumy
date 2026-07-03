// SPDX-License-Identifier: AGPL-3.0-only
package gateway

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// TestMessageText covers the container-unwrapping that fixes the "empty text"
// bug: groups with disappearing messages wrap every text in EphemeralMessage,
// so top-level GetConversation()/GetExtendedTextMessage() return empty.
func TestMessageText(t *testing.T) {
	cases := []struct {
		name string
		msg  *waE2E.Message
		want string
	}{
		{"plain", &waE2E.Message{Conversation: proto.String("hi")}, "hi"},
		{"extended", &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: proto.String("formatted"),
		}}, "formatted"},
		{"ephemeral-extended", &waE2E.Message{EphemeralMessage: &waE2E.FutureProofMessage{
			Message: &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
				Text: proto.String("vecinos"),
			}},
		}}, "vecinos"},
		{"image-caption", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			Caption: proto.String("aros de plata"),
		}}, "aros de plata"},
		{"media-no-caption", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{}}, ""},
		{"nil", nil, ""},
	}
	for _, c := range cases {
		if got := messageText(c.msg); got != c.want {
			t.Errorf("%s: messageText = %q, want %q", c.name, got, c.want)
		}
	}
}
