package whatsmeow

import (
	"bytes"
	"context"
	"log"
	"path/filepath"
	"strings"
	"testing"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"piumy-gateway/internal/eventbus"
	"piumy-gateway/internal/gateway"
	"piumy-gateway/internal/store"
)

// TestPersistHistoryMessagePersistsTextAndTouchesChat covers the base case
// (ct-2026-07-19-0148, backup Sub 3): a history message writes DIRECT to
// the store (never through a.inbound — history must not reach the agent),
// with the chat touched using the historical PushName.
func TestPersistHistoryMessagePersistsTextAndTouchesChat(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &Adapter{inbound: make(chan gateway.Inbound, 4), store: st}

	chat := types.NewJID("123", "s.whatsapp.net")
	sender := types.NewJID("456", "s.whatsapp.net")
	ts := time.Unix(1600000000, 0)
	evt := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: sender},
			ID:            "HISTMSG1",
			Type:          "text",
			PushName:      "Old Contact",
			Timestamp:     ts,
		},
		Message: &waE2E.Message{Conversation: proto.String("mensaje viejo")},
	}

	a.persistHistoryMessage(evt)

	msgs, err := st.GetMessages(chat.String(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("GetMessages = %d, want 1", len(msgs))
	}
	if msgs[0].Text != "mensaje viejo" || msgs[0].ID != "HISTMSG1" {
		t.Errorf("msgs[0] = %+v, unexpected fields", msgs[0])
	}
	if msgs[0].TS != ts.Unix() {
		t.Errorf("TS = %d, want %d", msgs[0].TS, ts.Unix())
	}

	chatRow, ok, err := st.GetChat(chat.String())
	if err != nil || !ok {
		t.Fatalf("GetChat = ok=%v err=%v", ok, err)
	}
	if chatRow.Name != "Old Contact" {
		t.Errorf("chat.Name = %q, want the historical PushName", chatRow.Name)
	}

	select {
	case got := <-a.inbound:
		t.Errorf("persistHistoryMessage must never push to a.inbound (would dispatch old history to the agent), got %+v", got)
	default:
		// expected: nothing pushed
	}
}

// TestPersistHistoryMessageDefaultsTypeToTextWhenInfoTypeEmpty is S9's own
// regression (ct-2026-07-30-031143, Citrino's own catch): ParseWebMessage
// (whatsmeow's real history-message constructor) never sets Info.Type at
// all — unlike TestPersistHistoryMessagePersistsTextAndTouchesChat's
// fixture above, which sets Type:"text" by hand and so never actually
// exercised this gap. Without the fix, the SAME real text message ends up
// type:"" via history and type:"text" via the live path — two identical
// messages treated differently by anything that filters on type.
func TestPersistHistoryMessageDefaultsTypeToTextWhenInfoTypeEmpty(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &Adapter{inbound: make(chan gateway.Inbound, 4), store: st}

	chat := types.NewJID("789", "s.whatsapp.net")
	evt := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: chat},
			ID:            "HISTMSG-REALISTIC",
			// Type deliberately left "" — matches whatsmeow's real
			// ParseWebMessage, which never sets Info.Type for a history
			// message (unlike a live stanza's own "type" attribute).
			Timestamp: time.Unix(1600000000, 0),
		},
		Message: &waE2E.Message{Conversation: proto.String("mensaje real del histórico")},
	}

	a.persistHistoryMessage(evt)

	msgs, err := st.GetMessages(chat.String(), 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("GetMessages = %+v, err=%v, want exactly 1", msgs, err)
	}
	if msgs[0].Type != "text" {
		t.Errorf("Type = %q, want \"text\" — a real text message must not persist with an empty type just because it came from history", msgs[0].Type)
	}
}

// TestPersistHistoryMessageDropsProtocolMessageWithNoText extends S5 (ct-
// 2026-07-30-031027) to the history path, per Citrino's explicit call: the
// exact same waE2E.Message oneof variants that produce type:"text" text:""
// on the live path (inbound.go) arrive through ParseWebMessage too —
// UnwrapRaw runs inside it (whatsmeow's client.go), so evt.Message has the
// identical shape either way. The backfill is MORE exposed, not less: one
// HistorySync chunk can carry thousands of messages.
func TestPersistHistoryMessageDropsProtocolMessageWithNoText(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &Adapter{inbound: make(chan gateway.Inbound, 4), store: st}

	chat := types.NewJID("55500000042", "s.whatsapp.net")
	evt := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: chat},
			ID:            "HISTMSG-PROTO",
		},
		Message: &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{}},
	}

	a.persistHistoryMessage(evt)

	msgs, err := st.GetMessages(chat.String(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Errorf("GetMessages = %d, want 0 — a ProtocolMessage must never be persisted as a blank text message", len(msgs))
	}
}

// TestPersistHistoryMessageDiagnosticLogNamesRealPayloadField confirms
// firstSetFieldName has something useful to name in the history path even
// though ParseWebMessage populates types.MessageInfo differently from a
// live stanza (Citrino's explicit caution before integrating) — UnwrapRaw
// still runs, so evt.Message keeps the real oneof field regardless.
func TestPersistHistoryMessageDiagnosticLogNamesRealPayloadField(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &Adapter{inbound: make(chan gateway.Inbound, 4), store: st}

	chat := types.NewJID("55500000042", "s.whatsapp.net")
	evt := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: chat},
			ID:            "HISTMSG-REACT",
		},
		Message: &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Text: proto.String("👍")}},
	}

	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })

	a.persistHistoryMessage(evt)

	if !strings.Contains(buf.String(), "reactionMessage") {
		t.Errorf("diagnostic log = %q, want it to name reactionMessage", buf.String())
	}
}

// TestPersistHistoryMessageDoesNotFilterFromMe is the critical difference
// from handleMessage (the live path): history must keep the owner's own
// old messages too — the backup wants "absolutamente toda la info" (boss
// verbatim), and history never goes through the outbox path that already
// captures live self-sends.
func TestPersistHistoryMessageDoesNotFilterFromMe(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &Adapter{inbound: make(chan gateway.Inbound, 4), store: st}

	chat := types.NewJID("123", "s.whatsapp.net")
	evt := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, IsFromMe: true},
			ID:            "HISTMSG2",
			Timestamp:     time.Unix(1600000001, 0),
		},
		Message: &waE2E.Message{Conversation: proto.String("mi propio mensaje viejo")},
	}

	a.persistHistoryMessage(evt)

	msgs, err := st.GetMessages(chat.String(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("GetMessages = %d, want 1 (FromMe must NOT be filtered for history)", len(msgs))
	}
	if !msgs[0].FromMe {
		t.Error("msgs[0].FromMe = false, want true")
	}
}

// TestPersistHistoryMessageNeverDownloadsMedia is the other critical
// difference from handleMessage: a HistorySync can carry thousands of
// messages, so downloading their media would flood the server (anti-ban).
// A message with an image must still persist as text/metadata only — no
// store.Media row — but Type/Text DO reflect the media (mime/caption, same
// as handleMessage — ct-2026-07-21-1437 parte 1) and the download reference
// lands in media_pending for a later worker to use.
func TestPersistHistoryMessageNeverDownloadsMedia(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &Adapter{inbound: make(chan gateway.Inbound, 4), store: st, mediaDir: t.TempDir()}

	chat := types.NewJID("123", "s.whatsapp.net")
	evt := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat},
			ID:            "HISTMSG3",
			Type:          "image",
			Timestamp:     time.Unix(1600000002, 0),
		},
		Message: &waE2E.Message{
			ImageMessage: &waE2E.ImageMessage{
				Mimetype:   proto.String("image/jpeg"),
				Caption:    proto.String("una foto vieja"),
				DirectPath: proto.String("/v/t1/img"),
				MediaKey:   []byte("k"), FileSHA256: []byte("s"), FileEncSHA256: []byte("e"),
				FileLength: proto.Uint64(77),
			},
		},
	}

	a.persistHistoryMessage(evt)

	media, err := st.ListMedia(chat.String(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(media) != 0 {
		t.Errorf("ListMedia = %d, want 0 — historical media must never be downloaded", len(media))
	}

	msgs, err := st.GetMessages(chat.String(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("GetMessages = %d, want 1 — the message row itself must still persist", len(msgs))
	}
	if msgs[0].Type != "image/jpeg" {
		t.Errorf("Type = %q, want the real mime image/jpeg (so PendingMedia's LIKE filter finds it)", msgs[0].Type)
	}
	if msgs[0].Text != "una foto vieja" {
		t.Errorf("Text = %q, want the caption as fallback", msgs[0].Text)
	}

	pending, ok, err := st.GetMediaPending(chat.String(), "HISTMSG3")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("GetMediaPending: want ok=true — the download reference must be captured even without downloading")
	}
	if pending.Kind != "photo" || pending.DirectPath != "/v/t1/img" || pending.FileLength != 77 {
		t.Errorf("GetMediaPending = %+v, unexpected fields", pending)
	}
}

// TestPersistHistoryMessageCapturesMediaPendingByType covers every concrete
// media sub-message (ct-2026-07-21-1437 parte 1) — same coverage as
// TestCaptureMediaPendingByType (media_test.go) but exercised through the
// real ingestion entry point, confirming Type is aligned to the real mime
// for every kind, not just image.
func TestPersistHistoryMessageCapturesMediaPendingByType(t *testing.T) {
	cases := []struct {
		name     string
		msg      *waE2E.Message
		wantMime string
		wantKind string
	}{
		{
			name:     "video",
			msg:      &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Mimetype: proto.String("video/mp4"), DirectPath: proto.String("/v")}},
			wantMime: "video/mp4",
			wantKind: "video",
		},
		{
			name:     "audio",
			msg:      &waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: proto.String("audio/ogg; codecs=opus"), DirectPath: proto.String("/v")}},
			wantMime: "audio/ogg; codecs=opus",
			wantKind: "audio",
		},
		{
			name:     "document",
			msg:      &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Mimetype: proto.String("application/pdf"), FileName: proto.String("factura.pdf"), DirectPath: proto.String("/v")}},
			wantMime: "application/pdf",
			wantKind: "doc",
		},
		{
			name:     "sticker",
			msg:      &waE2E.Message{StickerMessage: &waE2E.StickerMessage{DirectPath: proto.String("/v")}},
			wantMime: "image/webp",
			wantKind: "sticker",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			a := &Adapter{inbound: make(chan gateway.Inbound, 4), store: st}

			chat := types.NewJID("123", "s.whatsapp.net")
			evt := &events.Message{
				Info: types.MessageInfo{
					MessageSource: types.MessageSource{Chat: chat},
					ID:            "HIST-" + tc.name,
					Timestamp:     time.Unix(1600000010, 0),
				},
				Message: tc.msg,
			}
			a.persistHistoryMessage(evt)

			msgs, err := st.GetMessages(chat.String(), 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(msgs) != 1 || msgs[0].Type != tc.wantMime {
				t.Fatalf("GetMessages = %+v, want one row with Type=%q", msgs, tc.wantMime)
			}

			pending, ok, err := st.GetMediaPending(chat.String(), "HIST-"+tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				t.Fatal("GetMediaPending: want ok=true")
			}
			if pending.Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q", pending.Kind, tc.wantKind)
			}
		})
	}
}

// TestPersistHistoryMessagePropagatesReplyAndForwarded covers ct-2026-07-21-1610
// (S6a backend) for the HISTORICAL ingestion path — persistHistoryMessage
// writes store.Message directly (no gateway.Inbound in between), so this
// exercises detectReply's wiring independently from handleMessage's own
// coverage in inbound_test.go.
func TestPersistHistoryMessagePropagatesReplyAndForwarded(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &Adapter{inbound: make(chan gateway.Inbound, 4), store: st}

	chat := types.NewJID("123", "s.whatsapp.net")
	evt := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat},
			ID:            "HISTMSG-REPLY",
			Timestamp:     time.Unix(1600000020, 0),
		},
		Message: &waE2E.Message{
			ExtendedTextMessage: &waE2E.ExtendedTextMessage{
				Text: proto.String("respuesta vieja"),
				ContextInfo: &waE2E.ContextInfo{
					StanzaID:      proto.String("QUOTEDHIST1"),
					QuotedMessage: &waE2E.Message{Conversation: proto.String("original viejo")},
					IsForwarded:   proto.Bool(true),
				},
			},
		},
	}

	a.persistHistoryMessage(evt)

	msgs, err := st.GetMessages(chat.String(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("GetMessages = %d, want 1", len(msgs))
	}
	if msgs[0].QuotedID != "QUOTEDHIST1" || msgs[0].QuotedPreview != "original viejo" || !msgs[0].Forwarded {
		t.Errorf("msgs[0] = %+v, want QuotedID=QUOTEDHIST1 QuotedPreview='original viejo' Forwarded=true", msgs[0])
	}
}

// TestPersistHistoryMessagePlainTextHasEmptyReplyFields confirms a normal
// historical message leaves the reply/forward columns at their zero value.
func TestPersistHistoryMessagePlainTextHasEmptyReplyFields(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &Adapter{inbound: make(chan gateway.Inbound, 4), store: st}

	chat := types.NewJID("123", "s.whatsapp.net")
	evt := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat},
			ID:            "HISTMSG-PLAIN",
			Timestamp:     time.Unix(1600000021, 0),
		},
		Message: &waE2E.Message{Conversation: proto.String("mensaje normal")},
	}

	a.persistHistoryMessage(evt)

	msgs, err := st.GetMessages(chat.String(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("GetMessages = %d, want 1", len(msgs))
	}
	if msgs[0].QuotedID != "" || msgs[0].QuotedPreview != "" || msgs[0].Forwarded {
		t.Errorf("msgs[0] = %+v, want empty QuotedID/QuotedPreview and Forwarded=false", msgs[0])
	}
}

// TestPersistHistoryMessageDedups covers "AddMessage ya deduplica...
// reprocesar un HistorySync es inofensivo" (contract verbatim): the same
// message ID persisted twice (WhatsApp can push overlapping HistorySync
// chunks) must not duplicate the row.
func TestPersistHistoryMessageDedups(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &Adapter{inbound: make(chan gateway.Inbound, 4), store: st}

	chat := types.NewJID("123", "s.whatsapp.net")
	evt := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat},
			ID:            "HISTMSG-DUP",
			Timestamp:     time.Unix(1600000003, 0),
		},
		Message: &waE2E.Message{Conversation: proto.String("repetido")},
	}

	a.persistHistoryMessage(evt)
	a.persistHistoryMessage(evt)

	msgs, err := st.GetMessages(chat.String(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Errorf("GetMessages after persisting the same message twice = %d, want 1 (AddMessage's INSERT OR IGNORE)", len(msgs))
	}
}

// TestPersistHistoryMessageResolvesLIDChatViaGetPNForLID is S7c's own
// regression (ct-2026-07-30-0524): history.go had the identical unresolved
// chatJID bug as the live path, and ParseWebMessage (whatsmeow's own
// history-message constructor) never sets AddressingMode/SenderAlt/
// RecipientAlt at all — so unlike the live path, the DB fallback
// (GetPNForLID) is the ONLY path that can ever resolve a history message's
// @lid chat. A real client with a seeded mapping proves it still works.
func TestPersistHistoryMessageResolvesLIDChatViaGetPNForLID(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	client := newTestWmeowClient(t)
	lid := types.NewJID("555000000000200", "lid")
	number := types.NewJID("55500000044", "s.whatsapp.net")
	if err := client.Store.LIDs.PutLIDMapping(context.Background(), lid, number); err != nil {
		t.Fatal(err)
	}
	a := &Adapter{inbound: make(chan gateway.Inbound, 4), store: st, client: client}

	evt := &events.Message{
		Info: types.MessageInfo{
			// ParseWebMessage's real shape: Chat/Sender/IsFromMe/IsGroup only.
			MessageSource: types.MessageSource{Chat: lid, Sender: lid},
			ID:            "HISTMSG-LID",
			Timestamp:     time.Unix(1600000004, 0),
		},
		Message: &waE2E.Message{Conversation: proto.String("historial bajo lid")},
	}

	a.persistHistoryMessage(evt)

	msgs, err := st.GetMessages(number.ToNonAD().String(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Text != "historial bajo lid" {
		t.Errorf("GetMessages(%s) = %+v, want the history message saved under the resolved number", number.ToNonAD(), msgs)
	}
	if _, ok, _ := st.GetChat(lid.String()); ok {
		t.Errorf("a chat row was created under the raw @lid %s, want it only under the resolved number", lid)
	}
}

// TestPersistHistoryMessageNilStoreIsNoOp confirms the nil-safe convention
// (same as seedGroups/syncContacts): without a store, must not panic.
func TestPersistHistoryMessageNilStoreIsNoOp(t *testing.T) {
	a := &Adapter{inbound: make(chan gateway.Inbound, 4)}
	a.persistHistoryMessage(&events.Message{
		Info:    types.MessageInfo{MessageSource: types.MessageSource{Chat: types.NewJID("1", "s.whatsapp.net")}},
		Message: &waE2E.Message{},
	})
}

// TestHandleHistorySyncPushNameChunkSchedulesContactsSync is the first half
// of ct-2026-07-31's fix ("no llegan contactos en una instalación nueva"):
// a PUSH_NAME chunk has zero Conversations (the loop over
// evt.Data.GetConversations() never runs, so a.client can safely stay nil
// here — this chunk shape never reaches ParseWebMessage), but it IS the
// real signal that whatsmeow just wrote push names into its own local
// Store.Contacts (the library's handleHistoricalPushNames runs before this
// event ever dispatches — see the doc on the trigger in history.go). A
// long debounce override keeps the timer from actually firing during the
// test; the assertion is only that it got armed.
func TestHandleHistorySyncPushNameChunkSchedulesContactsSync(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &Adapter{inbound: make(chan gateway.Inbound, 4), store: st, contactsSyncDebounce: time.Hour}

	evt := &events.HistorySync{
		Data: &waHistorySync.HistorySync{
			SyncType: waHistorySync.HistorySync_PUSH_NAME.Enum(),
			Pushnames: []*waHistorySync.Pushname{
				{ID: proto.String("111@s.whatsapp.net"), Pushname: proto.String("Alguien")},
			},
		},
	}
	a.handleHistorySync(evt)

	if a.contactsSyncTimer == nil {
		t.Error("a PUSH_NAME HistorySync chunk did not schedule a contacts sync")
	}
}

// TestHandleHistorySyncConversationsChunkDoesNotScheduleContactsSync guards
// the other direction: a normal chunk carrying real conversations/messages
// is not itself a "contacts just landed" signal, and must not trigger an
// extra syncContacts run.
func TestHandleHistorySyncConversationsChunkDoesNotScheduleContactsSync(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &Adapter{inbound: make(chan gateway.Inbound, 4), store: st, client: newTestWmeowClient(t), contactsSyncDebounce: time.Hour}

	evt := &events.HistorySync{
		Data: &waHistorySync.HistorySync{
			SyncType: waHistorySync.HistorySync_RECENT.Enum(),
			Conversations: []*waHistorySync.Conversation{
				{ID: proto.String("123@s.whatsapp.net")},
			},
		},
	}
	a.handleHistorySync(evt)

	if a.contactsSyncTimer != nil {
		t.Error("a conversations-carrying HistorySync chunk (not PUSH_NAME) scheduled a contacts sync")
	}
}

// TestRecordPassiveHistoryActivityAccumulatesAndDedupsChats covers item 1+2's
// feeding function (ct-2026-07-23-0047): message counts sum across calls
// (multiple chunks in one push), but the same chat JID seen twice counts
// once (ChatsSeen is a set, not a counter) — and LastChunkAt advances to
// each call's own time, never staying at the first chunk's timestamp.
func TestRecordPassiveHistoryActivityAccumulatesAndDedupsChats(t *testing.T) {
	a := &Adapter{}
	a.recordPassiveHistoryActivity([]string{"1@c.us", "2@c.us"}, 10)
	a.recordPassiveHistoryActivity([]string{"2@c.us", "3@c.us"}, 5)

	active, messages, chats, ago := a.HistorySyncStatus()
	if messages != 15 {
		t.Errorf("messages = %d, want 15 (10+5 summed across chunks)", messages)
	}
	if chats != 3 {
		t.Errorf("chats = %d, want 3 (1@c.us, 2@c.us, 3@c.us — 2@c.us deduped)", chats)
	}
	// pairedAt was never stamped (no fresh pairing this test) — a passive
	// chunk CAN still arrive on an ordinary reconnect (history.go's own doc:
	// WhatsApp pushes "occasionally afterward", not just at link time), so
	// recordPassiveHistoryActivity must stay callable either way. active
	// correctly stays false regardless (freshPairingSyncWindow's own
	// pairedAt.IsZero() guard) — only a FRESH pairing ever gates the worker.
	if active {
		t.Error("HistorySyncStatus() active = true without a fresh pairing (pairedAt unset), want false")
	}
	if ago < 0 {
		t.Errorf("lastActivityAgo = %v, want >= 0 (a chunk just arrived — time.Since can legitimately read 0 immediately after time.Now())", ago)
	}
}

// TestFreshPairingSyncWindowZeroValueNeverGates covers the common case: an
// Adapter that never went through pairLoop (every ordinary reconnect to an
// already-paired session) must report the window as closed.
func TestFreshPairingSyncWindowZeroValueNeverGates(t *testing.T) {
	a := &Adapter{}
	if a.freshPairingSyncWindow() {
		t.Error("freshPairingSyncWindow() = true with pairedAt unset, want false")
	}
}

// TestFreshPairingSyncWindowActiveThenExpires covers M2's window
// (ct-2026-07-22-2342): true right after pairedAt is stamped, false once the
// window (default 30m) has elapsed.
func TestFreshPairingSyncWindowActiveThenExpires(t *testing.T) {
	a := &Adapter{pairedAt: time.Now()}
	if !a.freshPairingSyncWindow() {
		t.Error("freshPairingSyncWindow() = false right after pairing, want true")
	}

	a.pairedAt = time.Now().Add(-(defaultHistoryFreshPairGraceWindow + time.Minute))
	if a.freshPairingSyncWindow() {
		t.Error("freshPairingSyncWindow() = true past the grace window, want false")
	}
}

// TestFreshPairingSyncWindowLiveOverride mirrors actionDelay's own
// KV-override convention — store.SettingHistoryFreshPairGraceWindow wins
// over the package default without a restart.
func TestFreshPairingSyncWindowLiveOverride(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetSettingDuration(store.SettingHistoryFreshPairGraceWindow, time.Minute); err != nil {
		t.Fatal(err)
	}

	a := &Adapter{store: st, pairedAt: time.Now().Add(-2 * time.Minute)}
	if a.freshPairingSyncWindow() {
		t.Error("freshPairingSyncWindow() = true past the live 1m override, want false")
	}
}

// TestFreshPairingSyncWindowExtendsOnLastChunk covers item 2's auto-extend
// (ct-2026-07-23-0047, Citrino-approved design): pairedAt alone is past the
// default 30m window, but a passive chunk arrived recently — the window
// must still be open, anchored on the more recent of the two.
func TestFreshPairingSyncWindowExtendsOnLastChunk(t *testing.T) {
	a := &Adapter{
		pairedAt: time.Now().Add(-45 * time.Minute),
		historySyncStats: historySyncStats{
			LastChunkAt: time.Now().Add(-5 * time.Minute),
		},
	}
	if !a.freshPairingSyncWindow() {
		t.Error("freshPairingSyncWindow() = false with a recent passive chunk, want true (auto-extended)")
	}
}

// TestFreshPairingSyncWindowRespectsAbsoluteCeiling covers item 2's defensive
// ceiling: even with continued passive activity, the window must close once
// historyFreshPairAbsoluteCeiling has passed since pairedAt itself.
func TestFreshPairingSyncWindowRespectsAbsoluteCeiling(t *testing.T) {
	a := &Adapter{
		pairedAt: time.Now().Add(-(historyFreshPairAbsoluteCeiling + time.Minute)),
		historySyncStats: historySyncStats{
			LastChunkAt: time.Now(), // a chunk just arrived — should NOT matter
		},
	}
	if a.freshPairingSyncWindow() {
		t.Error("freshPairingSyncWindow() = true past the absolute ceiling despite recent activity, want false")
	}
}

// TestHistorySyncStatusReportsLiveCounters covers item 1's visibility signal
// (ct-2026-07-23-0047) — GET /api/status reads this via
// restapi.HistorySyncProgress.
func TestHistorySyncStatusReportsLiveCounters(t *testing.T) {
	a := &Adapter{
		pairedAt: time.Now().Add(-5 * time.Minute),
		historySyncStats: historySyncStats{
			Messages:    42,
			ChatsSeen:   map[string]struct{}{"1@c.us": {}, "2@c.us": {}},
			LastChunkAt: time.Now().Add(-10 * time.Second),
		},
	}
	active, messages, chats, ago := a.HistorySyncStatus()
	if !active {
		t.Error("HistorySyncStatus() active = false, want true (inside the default window)")
	}
	if messages != 42 {
		t.Errorf("HistorySyncStatus() messages = %d, want 42", messages)
	}
	if chats != 2 {
		t.Errorf("HistorySyncStatus() chats = %d, want 2 (deduped by ChatsSeen set)", chats)
	}
	if ago <= 0 || ago > time.Minute {
		t.Errorf("HistorySyncStatus() lastActivityAgo = %v, want roughly 10s", ago)
	}
}

// TestHistorySyncStatusZeroValueBeforeAnyPairing covers the common case: a
// process that never paired fresh this run (every ordinary reconnect)
// reports inactive with a zero lastActivityAgo, not a bogus huge duration.
func TestHistorySyncStatusZeroValueBeforeAnyPairing(t *testing.T) {
	a := &Adapter{}
	active, messages, chats, ago := a.HistorySyncStatus()
	if active || messages != 0 || chats != 0 || ago != 0 {
		t.Errorf("HistorySyncStatus() = (%v, %d, %d, %v), want (false, 0, 0, 0)", active, messages, chats, ago)
	}
}

// TestNudgeHistorySyncPublishesEveryCall: unthrottled by design
// (ct-2026-07-24-0527, Citrino's own architecture call — coalescing lives
// in the dashboard's debounced loaders, not here) — every call publishes,
// with the distinct Type the dashboard's REFRESH_ON table keys on.
func TestNudgeHistorySyncPublishesEveryCall(t *testing.T) {
	bus := eventbus.New()
	ch, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	a := &Adapter{bus: bus}

	a.nudgeHistorySync()
	a.nudgeHistorySync()

	for i := 0; i < 2; i++ {
		select {
		case ev := <-ch:
			if ev.Type != "history_batch" {
				t.Errorf("call %d: event Type = %q, want history_batch", i, ev.Type)
			}
		default:
			t.Fatalf("call %d: nudgeHistorySync published nothing", i)
		}
	}
}

// TestNudgeHistorySyncNilBusIsNoOp confirms the nil-safe convention (same
// as handleDisconnect/clearErrorState in inbound.go) — without a bus wired,
// must not panic.
func TestNudgeHistorySyncNilBusIsNoOp(t *testing.T) {
	a := &Adapter{}
	a.nudgeHistorySync()
}
