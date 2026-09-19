// Package whatsmeow: reply/forward extraction (ct-2026-07-21-1610, S6a
// backend). Same shape of problem as media.go's detectMedia — a reply/quote/
// forward's ContextInfo lives on the CONCRETE sub-message (ExtendedText,
// Image, Video, Audio, Document, Sticker), each with its own GetContextInfo()
// getter; there is no single access point on *waE2E.Message itself.
package whatsmeow

import waE2E "go.mau.fi/whatsmeow/proto/waE2E"

// replyInfo is what handleMessage/persistHistoryMessage extract from a
// message's ContextInfo — the zero value means "not a reply, not forwarded".
type replyInfo struct {
	quotedID      string
	quotedPreview string
	forwarded     bool
}

// contextInfoOf returns msg's ContextInfo regardless of which concrete
// sub-message carries it. nil for plain text (Conversation, which has no
// ContextInfo of its own) or a type this gateway doesn't otherwise handle.
func contextInfoOf(msg *waE2E.Message) *waE2E.ContextInfo {
	switch {
	case msg.GetExtendedTextMessage() != nil:
		return msg.GetExtendedTextMessage().GetContextInfo()
	case msg.GetImageMessage() != nil:
		return msg.GetImageMessage().GetContextInfo()
	case msg.GetVideoMessage() != nil:
		return msg.GetVideoMessage().GetContextInfo()
	case msg.GetAudioMessage() != nil:
		return msg.GetAudioMessage().GetContextInfo()
	case msg.GetDocumentMessage() != nil:
		return msg.GetDocumentMessage().GetContextInfo()
	case msg.GetStickerMessage() != nil:
		return msg.GetStickerMessage().GetContextInfo()
	}
	return nil
}

// detectReply reads msg's ContextInfo (if any) for quoted/forwarded
// metadata. quotedPreview reuses the same conversation/extended-text
// fallback handleMessage/persistHistoryMessage already use to read a
// message's own text — empty when the quoted message is media-only.
func detectReply(msg *waE2E.Message) replyInfo {
	ci := contextInfoOf(msg)
	if ci == nil {
		return replyInfo{}
	}
	preview := ""
	if q := ci.GetQuotedMessage(); q != nil {
		preview = q.GetConversation()
		if preview == "" {
			preview = q.GetExtendedTextMessage().GetText()
		}
	}
	return replyInfo{
		quotedID:      ci.GetStanzaID(),
		quotedPreview: preview,
		forwarded:     ci.GetIsForwarded(),
	}
}
