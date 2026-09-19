package store

import (
	"testing"
	"time"
)

func TestAddMessageDedup(t *testing.T) {
	s := openTestStore(t)

	m := Message{ChatJID: "111@c.us", ID: "wamid-1", FromMe: false, Text: "hola", TS: 100}
	if err := s.AddMessage(m); err != nil {
		t.Fatalf("first AddMessage: %v", err)
	}
	// Same (chat_jid, id) redelivered with different text — open-wa can
	// resend on reconnect. Must be a no-op, not a duplicate row or an error.
	dup := m
	dup.Text = "hola (redelivered)"
	if err := s.AddMessage(dup); err != nil {
		t.Fatalf("duplicate AddMessage: %v", err)
	}

	msgs, err := s.GetMessages("111@c.us", 10)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1 (dedup by chat_jid+id)", len(msgs))
	}
	if msgs[0].Text != "hola" {
		t.Errorf("got text=%q, want original %q (INSERT OR IGNORE keeps first insert)", msgs[0].Text, "hola")
	}
}

// TestAddMessageStripsDeviceSuffixConsistently is T45's reproduction for
// the real inbound path (ct-2026-08-10-1424): an incoming message whose
// event carried a device-qualified JID must land under the CLEAN jid in
// BOTH chats and messages — not just chats (TouchChat alone) — or the two
// tables desync and PendingDedicated's JOIN never matches this message.
func TestAddMessageStripsDeviceSuffixConsistently(t *testing.T) {
	s := openTestStore(t)
	raw := "55500000092:3@s.whatsapp.net"
	clean := "55500000092@s.whatsapp.net"
	// Recent, not TS:100 — SetActive below marks old messages handled on
	// activation (S3's own anti-avalanche sweep), which would hide this
	// message from PendingDedicated for a reason unrelated to what this
	// test checks.
	now := time.Now().Unix()

	if err := s.AddMessage(Message{ChatJID: raw, ID: "m1", FromMe: false, Text: "hola", TS: now}); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}

	if _, ok, err := s.GetChat(raw); err != nil || ok {
		t.Errorf("GetChat(jid con sufijo) ok=%v err=%v, want ok=false", ok, err)
	}
	if _, ok, err := s.GetChat(clean); err != nil || !ok {
		t.Fatalf("GetChat(jid limpio) ok=%v err=%v, want ok=true", ok, err)
	}

	msgsRaw, err := s.GetMessages(raw, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgsRaw) != 0 {
		t.Errorf("GetMessages(jid con sufijo) = %d, want 0 — el mensaje no debe quedar bajo el jid crudo", len(msgsRaw))
	}
	msgsClean, err := s.GetMessages(clean, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgsClean) != 1 {
		t.Fatalf("GetMessages(jid limpio) = %d, want 1", len(msgsClean))
	}

	// PendingDedicated JOINs chats ON messages.chat_jid — a real proof the
	// two tables agree, not just that both happen to read back clean.
	if err := s.SetMode(clean, "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActive(clean, true); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ChatJID != clean {
		t.Errorf("PendingDedicated = %v, want exactamente un mensaje con chat_jid=%q — chats.jid y messages.chat_jid deben coincidir para el JOIN", pending, clean)
	}
}

// TestAddMessageRoundTripsQuotedAndForwarded covers ct-2026-07-21-1610 (S6a
// backend): quoted_id/quoted_preview/forwarded must survive an
// AddMessage→GetMessages round trip, and a plain message must keep them at
// their zero value (not NULL, which COALESCE in messageColumns would also
// paper over — this confirms the INSERT itself writes the real values).
func TestAddMessageRoundTripsQuotedAndForwarded(t *testing.T) {
	s := openTestStore(t)

	reply := Message{
		ChatJID: "111@c.us", ID: "m1", Text: "respuesta", TS: 100,
		QuotedID: "QUOTED1", QuotedPreview: "mensaje original", Forwarded: true,
	}
	plain := Message{ChatJID: "111@c.us", ID: "m2", Text: "normal", TS: 101}
	if err := s.AddMessage(reply); err != nil {
		t.Fatalf("AddMessage(reply): %v", err)
	}
	if err := s.AddMessage(plain); err != nil {
		t.Fatalf("AddMessage(plain): %v", err)
	}

	msgs, err := s.GetMessages("111@c.us", 10)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	byID := map[string]Message{}
	for _, m := range msgs {
		byID[m.ID] = m
	}
	if got := byID["m1"]; got.QuotedID != "QUOTED1" || got.QuotedPreview != "mensaje original" || !got.Forwarded {
		t.Errorf("m1 = %+v, want QuotedID=QUOTED1 QuotedPreview='mensaje original' Forwarded=true", got)
	}
	if got := byID["m2"]; got.QuotedID != "" || got.QuotedPreview != "" || got.Forwarded {
		t.Errorf("m2 = %+v, want empty QuotedID/QuotedPreview and Forwarded=false", got)
	}
}

func TestReceiptsUpdateExistingMessage(t *testing.T) {
	s := openTestStore(t)
	m := Message{ChatJID: "111@c.us", ID: "wamid-1", FromMe: true, Text: "hola", TS: 100}
	if err := s.AddMessage(m); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}
	if err := s.SetDelivered("111@c.us", "wamid-1", 105); err != nil {
		t.Fatalf("SetDelivered: %v", err)
	}
	if err := s.SetRead("111@c.us", "wamid-1", 110); err != nil {
		t.Fatalf("SetRead: %v", err)
	}
	last, ok, err := s.LastMessage("111@c.us")
	if err != nil || !ok {
		t.Fatalf("LastMessage: ok=%v err=%v", ok, err)
	}
	if last.DeliveredTS != 105 || last.ReadTS != 110 {
		t.Errorf("got delivered_ts=%d read_ts=%d, want 105/110", last.DeliveredTS, last.ReadTS)
	}
}

// TestChatJIDsWithMessages covers the S1g criterion (ct-2026-07-19-1801):
// only a chat_jid with a real AddMessage row counts — TouchChat alone
// (contact backfill/whitelist-add, ts real or zero, no message) must not.
func TestChatJIDsWithMessages(t *testing.T) {
	s := openTestStore(t)

	if err := s.AddMessage(Message{ChatJID: "111@c.us", ID: "m1", FromMe: false, Text: "hola", TS: 100}); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchChat("222@c.us", "no mensajes", 500); err != nil {
		t.Fatal(err)
	}

	set, err := s.ChatJIDsWithMessages()
	if err != nil {
		t.Fatal(err)
	}
	if !set["111@c.us"] {
		t.Error(`set["111@c.us"] = false, want true — has a real message`)
	}
	if set["222@c.us"] {
		t.Error(`set["222@c.us"] = true, want false — TouchChat alone is not a message`)
	}
}

// TestMarkDecryptRetry covers T35 (ct-2026-08-08-1258): the retry-receipt
// signal must persist on the message row, and marking an id we never stored
// (e.g. a message sent before this feature existed) must be a silent no-op,
// not an error — MarkDecryptRetry's own doc.
func TestMarkDecryptRetry(t *testing.T) {
	s := openTestStore(t)
	m := Message{ChatJID: "555000001@s.whatsapp.net", ID: "wamid-1", FromMe: true, Text: "hola", TS: 100}
	if err := s.AddMessage(m); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}

	if err := s.MarkDecryptRetry("555000001@s.whatsapp.net", "wamid-1", 200); err != nil {
		t.Fatalf("MarkDecryptRetry: %v", err)
	}
	last, ok, err := s.LastMessage("555000001@s.whatsapp.net")
	if err != nil || !ok {
		t.Fatalf("LastMessage: ok=%v err=%v", ok, err)
	}
	if last.DecryptRetryAt != 200 {
		t.Errorf("got decrypt_retry_at=%d, want 200", last.DecryptRetryAt)
	}

	if err := s.MarkDecryptRetry("555000001@s.whatsapp.net", "no-existe", 300); err != nil {
		t.Errorf("MarkDecryptRetry on unknown id: %v, want nil (no row touched is not an error)", err)
	}
}

// TestChatJIDsWithMessagesExcludesEmptyContentAndStatusBroadcast is the
// regression test for ct-2026-07-29 (boss: "las conversaciones son muchas
// por el grupo... mis conversaciones reales son como 4 nada más"). Measured
// on the real DB: 401 of 405 "@lid chats with a message" were rows with
// BOTH text AND type empty — WhatsApp protocol noise (receipts/reactions/
// poll-vote echoes) that AddMessage persisted like a real message, making
// group members the boss never actually talked to look like 1:1
// conversations. status@broadcast (WhatsApp's shared Status/Stories
// pseudo-chat) had a real "media" message but must never count either — see
// StatusBroadcastJID's doc.
func TestChatJIDsWithMessagesExcludesEmptyContentAndStatusBroadcast(t *testing.T) {
	s := openTestStore(t)

	if err := s.AddMessage(Message{ChatJID: "111@lid", ID: "noise1", FromMe: true, Text: "", Type: "", TS: 100}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: "222@lid", ID: "real1", FromMe: false, Text: "hola", TS: 100}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: "333@lid", ID: "media1", FromMe: false, Text: "", Type: "image/jpeg", TS: 100}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: StatusBroadcastJID, ID: "status1", FromMe: false, Text: "", Type: "media", TS: 100}); err != nil {
		t.Fatal(err)
	}

	set, err := s.ChatJIDsWithMessages()
	if err != nil {
		t.Fatal(err)
	}
	if set["111@lid"] {
		t.Error(`set["111@lid"] = true, want false — empty text AND type is protocol noise, not a message`)
	}
	if !set["222@lid"] {
		t.Error(`set["222@lid"] = false, want true — real text`)
	}
	if !set["333@lid"] {
		t.Error(`set["333@lid"] = false, want true — real media type, even with empty text`)
	}
	if set[StatusBroadcastJID] {
		t.Error(`set[StatusBroadcastJID] = true, want false — status/story updates are never a conversation`)
	}
}

// TestPendingReadReceipts (T91, ct-2026-08-28): read_ts=0 on an inbound
// message IS the durable "still owes a receipt" signal a retry sweep reads
// back — no separate queue. Covers the 3 things that must NOT come back:
// an outbound message (from_me=1), one already marked read, and one too
// fresh for olderThan.
func TestPendingReadReceipts(t *testing.T) {
	s := openTestStore(t)
	chat := "111@c.us"

	if err := s.AddMessage(Message{ChatJID: chat, ID: "old-unread", FromMe: false, Text: "a", TS: 100}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: chat, ID: "old-already-read", FromMe: false, Text: "b", TS: 100}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRead(chat, "old-already-read", 150); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: chat, ID: "old-outbound", FromMe: true, Text: "c", TS: 100}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: chat, ID: "too-fresh", FromMe: false, Text: "d", TS: 500}); err != nil {
		t.Fatal(err)
	}

	got, err := s.PendingReadReceipts(200, 10) // olderThan=200: only ts<=200 qualifies
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "old-unread" {
		t.Errorf("PendingReadReceipts = %+v, want exactly [old-unread]", got)
	}
}

// TestPendingReadReceiptsOrderAndLimit: oldest first, capped — same shape
// DueOutbox already guarantees for the send queue, mirrored here so a big
// backlog drains in arrival order instead of an arbitrary one.
func TestPendingReadReceiptsOrderAndLimit(t *testing.T) {
	s := openTestStore(t)
	chat := "111@c.us"
	for i, ts := range []int64{300, 100, 200} {
		id := "m" + string(rune('a'+i))
		if err := s.AddMessage(Message{ChatJID: chat, ID: id, FromMe: false, Text: "x", TS: ts}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.PendingReadReceipts(1000, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("PendingReadReceipts limit=2 returned %d, want 2", len(got))
	}
	if got[0].TS != 100 || got[1].TS != 200 {
		t.Errorf("order = ts %d,%d — want oldest first (100, 200)", got[0].TS, got[1].TS)
	}
}
