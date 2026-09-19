// Package gateway: the seam between corepipeline and whatever messaging
// client is actually plugged in. The core NEVER imports whatsmeow (or any
// vendor client) directly — only this interface. The one real
// implementation is the whatsmeow adapter (F5.x, ct-2026-07-10-0420 —
// replaced the original open-wa adapter, deleted in ST-E,
// ct-2026-07-11-1444); tests use a fake. No pluggability-for-its-own-sake:
// this exists because corepipeline must not know whatsmeow, not because a
// second real backend is planned.
package gateway

import "context"

// Inbound is a messaging-vendor-agnostic incoming message. The adapter
// parses whatever raw event its client delivers into this before handing it
// to the pipeline — no vendor type ever crosses this seam. ChatJID/SenderJID
// arrive already resolved to the format router.json matches against (the
// adapter's job, not the pipeline's).
type Inbound struct {
	ChatJID   string
	SenderJID string
	MsgID     string
	Text      string
	Type      string // "text" for plain text; MIME type (e.g. "image/jpeg") for media
	TS        int64
	PushName  string // best-effort display name, for TouchChat

	// QuotedID/QuotedPreview/Forwarded (ct-2026-07-21-1610, S6a): reply and
	// forward metadata read from the vendor's ContextInfo, already reduced to
	// plain strings/bool here — no vendor type crosses this seam.
	// QuotedID/QuotedPreview are "" for a message that isn't a reply.
	QuotedID      string
	QuotedPreview string
	Forwarded     bool

	// FromMe (T100, ct-2026-08-29-1649): true when this is the OWNER's own
	// outbound message, sent from another linked device (e.g. the phone) to
	// a real contact — not new inbound work, but the pipeline needs to know:
	// it closes that chat's pending queue (the owner already answered, the
	// agent must not answer on top of it) and stores the message so the
	// conversation stays complete. False for a real inbound message and for
	// a Note-to-Self written from another device (still ordinary inbound,
	// unchanged by T100).
	FromMe bool
}

// SendResult is what a successful Send returns.
type SendResult struct {
	MsgID string
	TS    int64
}

// OutboundMedia bundles a SendMedia call's media fields (T123, ct-2026-09-
// 02-2121) — a struct instead of growing SendMedia's own positional list
// forever. T122 shipped SendMedia with 6 positional params (ctx, toJID,
// kind, data, mime, caption); the FIRST time a second media type (T123's
// audio) needed a field the first one didn't (Seconds — a voice note's
// duration), that signature was already past the point where one more
// blind position is safe to add or read at a call site. A struct absorbs
// that: a new kind's own field (T123: Seconds; a future video: same field,
// reused) never forces every existing caller/implementer to touch a
// positional list again.
type OutboundMedia struct {
	Kind    string // "photo" (T122), "audio" (T123)
	Data    []byte
	Mime    string
	Caption string
	// Seconds is a voice-note/video duration — 0 means omit it (WhatsApp
	// shows no duration), never guessed from the file. Meaningless for
	// "photo".
	Seconds int
}

// Status is the client's connection state.
type Status struct {
	Connected bool
}

// Gateway abstracts the messaging client. Implementations: the whatsmeow
// adapter (F5.x) and a fake for tests.
type Gateway interface {
	// Start connects the client and begins delivering messages via
	// Inbound(). Idempotent. ctx cancellation tears the connection down.
	Start(ctx context.Context) error
	// Stop disconnects and closes the Inbound() channel.
	Stop()
	// Connected reports the current connection state — the outbox drain
	// does not send while this is false.
	Connected() bool
	// Inbound streams incoming messages the adapter feeds in.
	Inbound() <-chan Inbound
	// Send delivers text to toJID and returns the client's message id.
	Send(ctx context.Context, toJID, text string) (SendResult, error)
	// SendMedia delivers media to toJID — ONE method for every media type,
	// distinguished by media.Kind ("photo" — T122; T123 adds "audio"), not
	// one method per format: a caller doesn't pick the underlying client
	// call, just describes what it's sending. An implementation that
	// doesn't support media.Kind returns an error naming it, never a
	// silent no-op.
	SendMedia(ctx context.Context, toJID string, media OutboundMedia) (SendResult, error)
	// SetTyping toggles the "composing" indicator — best-effort; a client
	// that doesn't support it is a no-op.
	SetTyping(ctx context.Context, toJID string, on bool) error
	// MarkRead sends read receipts for msgIDs in chatJID, addressed to
	// senderJID — WhatsApp routes a receipt per-sender, not per-chat (T127,
	// ct-2026-09-02-2249): for a 1:1 chat sender==chat, but a group's
	// receipt has to name the participant who sent msgIDs, not the group.
	// Honest receipts: called when an agent actually attended the
	// messages, never on mere receipt.
	MarkRead(ctx context.Context, chatJID, senderJID string, msgIDs []string) error
	// MarkDelivered acks delivery — a no-op on whatsmeow (WhatsApp sends
	// delivery receipts automatically as part of receiving a message, no
	// separate call needed) — kept on the seam for completeness.
	MarkDelivered(ctx context.Context, chatJID string, msgIDs []string) error
	// QRChannel streams QR pairing codes for first-time login — whatsmeow
	// (F5.x) needs this, open-wa didn't (its own Node process shows the QR
	// out of band). Returns (nil, nil) when no pairing step is needed right
	// now (already paired, or the implementation doesn't use QR pairing at
	// all). The channel closes once pairing succeeds or Start's context is
	// cancelled — never call this after Start has already been called.
	QRChannel(ctx context.Context) (<-chan string, error)
}
