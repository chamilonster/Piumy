package whatsmeow

import (
	"testing"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// TestDetectReplyPlainMessageIsEmpty covers a normal message (no
// ContextInfo at all) — must come back as the zero value, never a false
// "is a reply".
func TestDetectReplyPlainMessageIsEmpty(t *testing.T) {
	msg := &waE2E.Message{Conversation: proto.String("hola")}
	got := detectReply(msg)
	if got != (replyInfo{}) {
		t.Errorf("detectReply(plain conversation) = %+v, want zero value", got)
	}
}

// TestDetectReplyTextQuote covers a text reply, which whatsmeow always
// wraps as ExtendedTextMessage (same wrapper handleMessage's own text
// fallback already accounts for).
func TestDetectReplyTextQuote(t *testing.T) {
	msg := &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: proto.String("respuesta"),
			ContextInfo: &waE2E.ContextInfo{
				StanzaID:      proto.String("QUOTED1"),
				QuotedMessage: &waE2E.Message{Conversation: proto.String("mensaje original")},
			},
		},
	}
	got := detectReply(msg)
	want := replyInfo{quotedID: "QUOTED1", quotedPreview: "mensaje original"}
	if got != want {
		t.Errorf("detectReply(text reply) = %+v, want %+v", got, want)
	}
}

// TestDetectReplyImageQuotePreviewFallsBackToExtendedText covers a reply to
// an image whose quoted message itself has no plain Conversation (e.g. the
// quoted message was a link-preview/ExtendedText) — quotedPreview must fall
// back the same way handleMessage's own text extraction does.
func TestDetectReplyImageQuotePreviewFallsBackToExtendedText(t *testing.T) {
	msg := &waE2E.Message{
		ImageMessage: &waE2E.ImageMessage{
			Mimetype: proto.String("image/jpeg"),
			ContextInfo: &waE2E.ContextInfo{
				StanzaID: proto.String("QUOTED2"),
				QuotedMessage: &waE2E.Message{
					ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("con preview de link")},
				},
			},
		},
	}
	got := detectReply(msg)
	want := replyInfo{quotedID: "QUOTED2", quotedPreview: "con preview de link"}
	if got != want {
		t.Errorf("detectReply(image reply) = %+v, want %+v", got, want)
	}
}

// TestDetectReplyQuotedMediaOnlyPreviewEmpty covers replying to a
// media-only quoted message (no text of its own, e.g. a bare photo with no
// caption) — quotedID must still be captured, quotedPreview stays empty.
func TestDetectReplyQuotedMediaOnlyPreviewEmpty(t *testing.T) {
	msg := &waE2E.Message{
		VideoMessage: &waE2E.VideoMessage{
			Mimetype: proto.String("video/mp4"),
			ContextInfo: &waE2E.ContextInfo{
				StanzaID:      proto.String("QUOTED3"),
				QuotedMessage: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg")}},
			},
		},
	}
	got := detectReply(msg)
	want := replyInfo{quotedID: "QUOTED3", quotedPreview: ""}
	if got != want {
		t.Errorf("detectReply(video reply to media-only quote) = %+v, want %+v", got, want)
	}
}

// TestDetectReplyForwarded covers a forwarded message (not a reply — no
// StanzaID/QuotedMessage, just isForwarded=true), for the audio/document/
// sticker sub-types not otherwise exercised above.
func TestDetectReplyForwarded(t *testing.T) {
	cases := []struct {
		name string
		msg  *waE2E.Message
	}{
		{"audio", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
			ContextInfo: &waE2E.ContextInfo{IsForwarded: proto.Bool(true)},
		}}},
		{"document", &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
			ContextInfo: &waE2E.ContextInfo{IsForwarded: proto.Bool(true)},
		}}},
		{"sticker", &waE2E.Message{StickerMessage: &waE2E.StickerMessage{
			ContextInfo: &waE2E.ContextInfo{IsForwarded: proto.Bool(true)},
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectReply(tc.msg)
			if !got.forwarded {
				t.Errorf("detectReply(%s forwarded) = %+v, want forwarded=true", tc.name, got)
			}
			if got.quotedID != "" {
				t.Errorf("detectReply(%s forwarded) quotedID = %q, want empty — not a reply", tc.name, got.quotedID)
			}
		})
	}
}

// TestDetectReplyNotForwardedDefaultsFalse confirms a normal reply (not
// forwarded) leaves forwarded=false — the ContextInfo path must not
// accidentally mark every reply as forwarded too.
func TestDetectReplyNotForwardedDefaultsFalse(t *testing.T) {
	msg := &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("Q1")},
		},
	}
	got := detectReply(msg)
	if got.forwarded {
		t.Error("detectReply(plain reply): forwarded = true, want false")
	}
}
