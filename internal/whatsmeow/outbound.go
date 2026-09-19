package whatsmeow

import (
	"context"
	"fmt"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"piumy-gateway/internal/gateway"
)

// Send delivers text via whatsmeow's own SendMessage — the ONLY send path.
// The core (corepipeline's outbox drain, paced by governor) decides WHEN
// to call this; this adapter never sends on its own (anti-ban rule, see
// package doc).
func (a *Adapter) Send(ctx context.Context, toJID, text string) (gateway.SendResult, error) {
	jid, err := types.ParseJID(toJID)
	if err != nil {
		return gateway.SendResult{}, fmt.Errorf("whatsmeow: parse JID %q: %w", toJID, err)
	}
	resp, err := a.client.SendMessage(ctx, jid, &waE2E.Message{Conversation: proto.String(text)})
	if err != nil {
		return gateway.SendResult{}, err
	}
	return gateway.SendResult{MsgID: string(resp.ID), TS: resp.Timestamp.Unix()}, nil
}

// SetTyping toggles the composing indicator via SendChatPresence.
func (a *Adapter) SetTyping(ctx context.Context, toJID string, on bool) error {
	jid, err := types.ParseJID(toJID)
	if err != nil {
		return fmt.Errorf("whatsmeow: parse JID %q: %w", toJID, err)
	}
	state := types.ChatPresencePaused
	if on {
		state = types.ChatPresenceComposing
	}
	return a.client.SendChatPresence(ctx, jid, state, types.ChatPresenceMediaText)
}

// MarkRead sends read receipts for msgIDs in chatJID, addressed to
// senderJID (T127, ct-2026-09-02-2249) — whatsmeow.Client.MarkRead wants
// the ORIGINAL SENDER of each message, since WhatsApp routes the receipt
// per-sender, not per-chat: for a 1:1 chat that's the same as chatJID, but
// for a group it's the participant who actually sent msgIDs.
//
// senderJID=="" falls back to chatJID — capipush's own 1:1 dispatch key
// (T108) never carries a sender (there's only ever one), so this is the
// normal path there, not just a safety net.
func (a *Adapter) MarkRead(ctx context.Context, chatJID, senderJID string, msgIDs []string) error {
	jid, err := types.ParseJID(chatJID)
	if err != nil {
		return fmt.Errorf("whatsmeow: parse JID %q: %w", chatJID, err)
	}
	if senderJID == "" {
		senderJID = chatJID
	}
	sender, err := types.ParseJID(senderJID)
	if err != nil {
		return fmt.Errorf("whatsmeow: parse JID %q: %w", senderJID, err)
	}
	// T125's own trap, one seam over: a device suffix must never cross into
	// whatsmeow's own client.MarkRead (same reasoning as resolveSenderJID's
	// .ToNonAD() on the way in).
	sender = sender.ToNonAD()
	ids := make([]types.MessageID, len(msgIDs))
	for i, id := range msgIDs {
		ids[i] = types.MessageID(id)
	}
	return a.client.MarkRead(ctx, ids, time.Now(), jid, sender)
}

// MarkDelivered is a no-op: WhatsApp's own protocol sends delivery
// receipts automatically as part of receiving a message — whatsmeow does
// this internally, there's no separate "ack delivery" call to make. Kept
// on the seam for parity with openwa's adapter (gateway.Gateway doc).
func (a *Adapter) MarkDelivered(ctx context.Context, chatJID string, msgIDs []string) error {
	return nil
}
