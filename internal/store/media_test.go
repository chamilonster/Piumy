package store

import "testing"

func TestAddMediaAndGetMediaRoundTrip(t *testing.T) {
	s := openTestStore(t)
	m := Media{MsgID: "m1", ChatJID: "1@c.us", Path: "media/m1_lowq.jpg", FullPath: "media/m1.jpg", Mime: "image/jpeg", Size: 12345, TS: 100}
	if err := s.AddMedia(m); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetMedia("1@c.us", "m1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("GetMedia: want ok=true")
	}
	if got.Path != m.Path || got.FullPath != m.FullPath || got.Mime != m.Mime || got.Size != m.Size {
		t.Errorf("GetMedia = %+v, want %+v", got, m)
	}
}

func TestGetMediaNotFound(t *testing.T) {
	s := openTestStore(t)
	_, ok, err := s.GetMedia("1@c.us", "nope")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("GetMedia for a nonexistent row: want ok=false")
	}
}

func TestListMediaIncludesFullPath(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddMedia(Media{MsgID: "m1", ChatJID: "1@c.us", Path: "low.jpg", FullPath: "full.jpg", TS: 1}); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListMedia("1@c.us", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].FullPath != "full.jpg" {
		t.Errorf("ListMedia = %+v, want one item with FullPath=full.jpg", items)
	}
}

func TestMediaMsgIDs(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddMedia(Media{MsgID: "m1", ChatJID: "1@c.us", Path: "a.jpg", FullPath: "a.jpg", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMedia(Media{MsgID: "m2", ChatJID: "1@c.us", Path: "b.jpg", FullPath: "b.jpg", TS: 2}); err != nil {
		t.Fatal(err)
	}
	// Different chat — must not leak into "1@c.us"'s set.
	if err := s.AddMedia(Media{MsgID: "m3", ChatJID: "2@c.us", Path: "c.jpg", FullPath: "c.jpg", TS: 3}); err != nil {
		t.Fatal(err)
	}

	ids, err := s.MediaMsgIDs("1@c.us")
	if err != nil {
		t.Fatal(err)
	}
	if !ids["m1"] || !ids["m2"] || ids["m3"] {
		t.Errorf("MediaMsgIDs(1@c.us) = %v, want {m1:true, m2:true} only", ids)
	}
}

func TestMediaKind(t *testing.T) {
	cases := []struct {
		mime     string
		wantKind string
		wantOK   bool
	}{
		{"image/jpeg", "photo", true},
		{"image/png", "photo", true},
		{"image/webp", "sticker", true},
		{"audio/ogg; codecs=opus", "audio", true},
		{"video/mp4", "video", true},
		{"application/pdf", "doc", true},
		{"text", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		kind, ok := MediaKind(tc.mime)
		if kind != tc.wantKind || ok != tc.wantOK {
			t.Errorf("MediaKind(%q) = (%q, %v), want (%q, %v)", tc.mime, kind, ok, tc.wantKind, tc.wantOK)
		}
	}
}

// TestPendingMediaOrdersOldestFirstAndExcludesDownloaded covers the FIFO
// backlog the on-demand media worker drains (ct-2026-07-21-1358): only
// media-type messages WITHOUT a media row, oldest first, and never a
// plain-text message even if it shares the chat.
func TestPendingMediaOrdersOldestFirstAndExcludesDownloaded(t *testing.T) {
	s := openTestStore(t)
	chat := "1@c.us"
	for _, m := range []Message{
		{ChatJID: chat, ID: "text1", Text: "hola", TS: 50, Type: "text"},
		{ChatJID: chat, ID: "img-new", TS: 300, Type: "image/jpeg"},
		{ChatJID: chat, ID: "img-old", TS: 100, Type: "image/jpeg"},
		{ChatJID: chat, ID: "audio-old", TS: 200, Type: "audio/ogg"},
		{ChatJID: chat, ID: "already-downloaded", TS: 150, Type: "video/mp4"},
	} {
		if err := s.AddMessage(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddMedia(Media{MsgID: "already-downloaded", ChatJID: chat, Path: "x.mp4", FullPath: "x.mp4", TS: 150}); err != nil {
		t.Fatal(err)
	}

	pending, err := s.PendingMedia(chat)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 3 {
		t.Fatalf("PendingMedia = %d items, want 3 (text and already-downloaded excluded): %+v", len(pending), pending)
	}
	var ids []string
	for _, m := range pending {
		ids = append(ids, m.ID)
	}
	want := []string{"img-old", "audio-old", "img-new"} // oldest (ts=100) first
	for i, id := range want {
		if ids[i] != id {
			t.Errorf("PendingMedia order = %v, want %v", ids, want)
			break
		}
	}
}

func TestAddMediaPendingAndGetRoundTrip(t *testing.T) {
	s := openTestStore(t)
	m := MediaPending{
		ChatJID: "1@c.us", MsgID: "m1", Mime: "image/jpeg", Kind: "photo",
		DirectPath: "/v/t1/abc", MediaKey: []byte{1, 2, 3}, FileSHA256: []byte{4, 5, 6},
		FileEncSHA256: []byte{7, 8, 9}, FileLength: 12345, TS: 100,
	}
	if err := s.AddMediaPending(m); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetMediaPending("1@c.us", "m1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("GetMediaPending: want ok=true")
	}
	if got.Mime != m.Mime || got.Kind != m.Kind || got.DirectPath != m.DirectPath ||
		string(got.MediaKey) != string(m.MediaKey) || string(got.FileSHA256) != string(m.FileSHA256) ||
		string(got.FileEncSHA256) != string(m.FileEncSHA256) || got.FileLength != m.FileLength || got.TS != m.TS {
		t.Errorf("GetMediaPending = %+v, want %+v", got, m)
	}
}

func TestGetMediaPendingNotFound(t *testing.T) {
	s := openTestStore(t)
	_, ok, err := s.GetMediaPending("1@c.us", "nope")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("GetMediaPending for a nonexistent row: want ok=false")
	}
}

func TestAddMediaPendingReplacesOnConflict(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddMediaPending(MediaPending{ChatJID: "1@c.us", MsgID: "m1", Mime: "image/jpeg", DirectPath: "/old"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMediaPending(MediaPending{ChatJID: "1@c.us", MsgID: "m1", Mime: "image/jpeg", DirectPath: "/new"}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetMediaPending("1@c.us", "m1")
	if err != nil || !ok {
		t.Fatalf("GetMediaPending: ok=%v err=%v", ok, err)
	}
	if got.DirectPath != "/new" {
		t.Errorf("DirectPath = %q, want /new (re-processing the same message overwrites)", got.DirectPath)
	}
}

// TestNextMediaPendingOrdersOldestFirstAcrossChats covers ct-2026-07-21-1437
// parte 2's global FIFO: the background worker's backlog is ONE chronological
// queue across every chat, not per-chat like PendingMedia.
func TestNextMediaPendingOrdersOldestFirstAcrossChats(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddMediaPending(MediaPending{ChatJID: "2@c.us", MsgID: "new", Mime: "image/jpeg", TS: 300}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMediaPending(MediaPending{ChatJID: "1@c.us", MsgID: "old", Mime: "audio/ogg", TS: 100}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMediaPending(MediaPending{ChatJID: "3@c.us", MsgID: "mid", Mime: "video/mp4", TS: 200}); err != nil {
		t.Fatal(err)
	}

	got, ok, err := s.NextMediaPending(3)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("NextMediaPending: want ok=true")
	}
	if got.ChatJID != "1@c.us" || got.MsgID != "old" {
		t.Errorf("NextMediaPending = %s/%s, want 1@c.us/old (globally oldest ts, across chats)", got.ChatJID, got.MsgID)
	}
}

func TestNextMediaPendingEmptyBacklog(t *testing.T) {
	s := openTestStore(t)
	_, ok, err := s.NextMediaPending(3)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("NextMediaPending on an empty backlog: want ok=false")
	}
}

// TestNextMediaPendingSkipsRowsAtMaxAttempts covers ct-2026-07-21-1437
// parte 2's retry cap (Citrino catch): a row that reached maxAttempts must
// not block the FIFO queue behind it, even though it's still the globally
// oldest by ts.
func TestNextMediaPendingSkipsRowsAtMaxAttempts(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddMediaPending(MediaPending{ChatJID: "1@c.us", MsgID: "stale", Mime: "image/jpeg", TS: 100}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := s.IncrementMediaPendingAttempts("1@c.us", "stale"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddMediaPending(MediaPending{ChatJID: "2@c.us", MsgID: "fresh", Mime: "image/jpeg", TS: 200}); err != nil {
		t.Fatal(err)
	}

	got, ok, err := s.NextMediaPending(3)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("NextMediaPending: want ok=true (the fresh row is still under the cap)")
	}
	if got.ChatJID != "2@c.us" || got.MsgID != "fresh" {
		t.Errorf("NextMediaPending = %s/%s, want 2@c.us/fresh (the older 'stale' row hit the attempts cap)", got.ChatJID, got.MsgID)
	}
}

// TestMediaPendingForChatFiltersByChatOrdersOldestFirstSkipsCapped covers
// ct-2026-07-21-1437 parte 3's on-demand backlog: only the given chat's own
// rows, oldest first, excluding ones at the attempts cap.
func TestMediaPendingForChatFiltersByChatOrdersOldestFirstSkipsCapped(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddMediaPending(MediaPending{ChatJID: "1@c.us", MsgID: "new", Mime: "image/jpeg", TS: 200}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMediaPending(MediaPending{ChatJID: "1@c.us", MsgID: "old", Mime: "audio/ogg", TS: 100}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMediaPending(MediaPending{ChatJID: "1@c.us", MsgID: "capped", Mime: "video/mp4", TS: 50}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := s.IncrementMediaPendingAttempts("1@c.us", "capped"); err != nil {
			t.Fatal(err)
		}
	}
	// Different chat — must not leak in.
	if err := s.AddMediaPending(MediaPending{ChatJID: "2@c.us", MsgID: "other-chat", Mime: "image/jpeg", TS: 10}); err != nil {
		t.Fatal(err)
	}

	got, err := s.MediaPendingForChat("1@c.us", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("MediaPendingForChat = %d items, want 2 (capped and other-chat excluded): %+v", len(got), got)
	}
	if got[0].MsgID != "old" || got[1].MsgID != "new" {
		t.Errorf("MediaPendingForChat order = [%s, %s], want [old, new] (oldest first)", got[0].MsgID, got[1].MsgID)
	}
}

func TestMediaPendingForChatEmpty(t *testing.T) {
	s := openTestStore(t)
	got, err := s.MediaPendingForChat("1@c.us", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("MediaPendingForChat on an empty backlog = %+v, want empty", got)
	}
}

// TestIncrementMediaPendingAttempts confirms the counter actually advances
// and round-trips through GetMediaPending.
func TestIncrementMediaPendingAttempts(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddMediaPending(MediaPending{ChatJID: "1@c.us", MsgID: "m1", Mime: "image/jpeg"}); err != nil {
		t.Fatal(err)
	}
	if err := s.IncrementMediaPendingAttempts("1@c.us", "m1"); err != nil {
		t.Fatal(err)
	}
	if err := s.IncrementMediaPendingAttempts("1@c.us", "m1"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetMediaPending("1@c.us", "m1")
	if err != nil || !ok {
		t.Fatalf("GetMediaPending: ok=%v err=%v", ok, err)
	}
	if got.Attempts != 2 {
		t.Errorf("Attempts = %d, want 2", got.Attempts)
	}
}

// TestFailMediaPendingPermanentlyJumpsToMax covers ct-2026-07-29's one-shot
// give-up: attempts must land exactly on MaxMediaPendingAttempts after a
// SINGLE call, not the plain +1 IncrementMediaPendingAttempts does — the
// whole point is a 403/410 (WhatsApp's CDN copy is gone for good) stops
// being retried immediately instead of burning MaxMediaPendingAttempts-1
// more doomed real attempts first.
func TestFailMediaPendingPermanentlyJumpsToMax(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddMediaPending(MediaPending{ChatJID: "1@c.us", MsgID: "m1", Mime: "audio/ogg"}); err != nil {
		t.Fatal(err)
	}
	if err := s.FailMediaPendingPermanently("1@c.us", "m1"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetMediaPending("1@c.us", "m1")
	if err != nil || !ok {
		t.Fatalf("GetMediaPending: ok=%v err=%v", ok, err)
	}
	if got.Attempts != MaxMediaPendingAttempts {
		t.Errorf("Attempts = %d after one FailMediaPendingPermanently call, want %d", got.Attempts, MaxMediaPendingAttempts)
	}
}

// TestMediaPendingFailedMsgIDs covers the dashboard's honest-state signal
// (ct-2026-07-29, boss: "un adjunto que falló con 403 no puede decir
// 'descargando' para siempre"): only rows that reached the attempts cap
// count as failed — a fresh or still-retriable row must not.
func TestMediaPendingFailedMsgIDs(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddMediaPending(MediaPending{ChatJID: "1@c.us", MsgID: "failed", Mime: "audio/ogg"}); err != nil {
		t.Fatal(err)
	}
	if err := s.FailMediaPendingPermanently("1@c.us", "failed"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMediaPending(MediaPending{ChatJID: "1@c.us", MsgID: "still-trying", Mime: "image/jpeg"}); err != nil {
		t.Fatal(err)
	}
	if err := s.IncrementMediaPendingAttempts("1@c.us", "still-trying"); err != nil {
		t.Fatal(err)
	}
	// Different chat — must not leak in.
	if err := s.AddMediaPending(MediaPending{ChatJID: "2@c.us", MsgID: "other-chat-failed", Mime: "video/mp4"}); err != nil {
		t.Fatal(err)
	}
	if err := s.FailMediaPendingPermanently("2@c.us", "other-chat-failed"); err != nil {
		t.Fatal(err)
	}

	failed, err := s.MediaPendingFailedMsgIDs("1@c.us")
	if err != nil {
		t.Fatal(err)
	}
	if len(failed) != 1 || !failed["failed"] {
		t.Errorf("MediaPendingFailedMsgIDs(1@c.us) = %v, want exactly {failed: true}", failed)
	}
}

// TestAddMediaPendingResetsAttemptsOnReconflict: a re-capture (e.g. the
// history worker brings the same message back with a fresh directPath)
// must reset attempts to 0 — the old failures shouldn't count against a
// brand new reference.
func TestAddMediaPendingResetsAttemptsOnReconflict(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddMediaPending(MediaPending{ChatJID: "1@c.us", MsgID: "m1", Mime: "image/jpeg", DirectPath: "/old"}); err != nil {
		t.Fatal(err)
	}
	if err := s.IncrementMediaPendingAttempts("1@c.us", "m1"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMediaPending(MediaPending{ChatJID: "1@c.us", MsgID: "m1", Mime: "image/jpeg", DirectPath: "/new"}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetMediaPending("1@c.us", "m1")
	if err != nil || !ok {
		t.Fatalf("GetMediaPending: ok=%v err=%v", ok, err)
	}
	if got.Attempts != 0 {
		t.Errorf("Attempts = %d, want 0 (a re-capture resets the counter)", got.Attempts)
	}
}

func TestDeleteMediaPending(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddMediaPending(MediaPending{ChatJID: "1@c.us", MsgID: "m1", Mime: "image/jpeg"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMediaPending("1@c.us", "m1"); err != nil {
		t.Fatal(err)
	}
	_, ok, err := s.GetMediaPending("1@c.us", "m1")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("GetMediaPending after DeleteMediaPending: want ok=false")
	}
}
