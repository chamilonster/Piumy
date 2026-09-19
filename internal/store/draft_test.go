package store

import "testing"

// TestAddDraftStartsAtRoundOne: a fresh chat's first draft has no
// reject→redraft chain to continue.
func TestAddDraftStartsAtRoundOne(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddDraft("111@c.us", "hola", "claude-opus-4-8", 1); err != nil {
		t.Fatal(err)
	}
	drafts, err := s.PendingDrafts(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 || drafts[0].Round != 1 {
		t.Fatalf("PendingDrafts = %+v, want exactly one draft at round 1", drafts)
	}
}

// TestRejectDraftMarksStatusAndReason confirms the reason lands on the
// draft row itself (T15, ct-2026-08-05-123241: "el motivo tiene que viajar
// con el mensaje") — RejectDraft is the write path, PendingRejectionNote
// (tested below) is what capipush reads back.
func TestRejectDraftMarksStatusAndReason(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddDraftWithConfirmer("111@c.us", "hola", "m", "", "", 100, 1); err != nil {
		t.Fatal(err)
	}
	drafts, err := s.PendingDrafts(10)
	if err != nil || len(drafts) != 1 {
		t.Fatalf("PendingDrafts = %+v, err=%v", drafts, err)
	}
	id := drafts[0].ID

	chatJID, burstMaxTS, round, ok, err := s.RejectDraft(id, "muy largo, resumilo")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("RejectDraft ok=false, want true for a pending draft")
	}
	if chatJID != "111@c.us" || burstMaxTS != 100 || round != 1 {
		t.Errorf("RejectDraft = chatJID=%q burstMaxTS=%d round=%d, want 111@c.us/100/1", chatJID, burstMaxTS, round)
	}

	// Rejected drops out of PendingDrafts — it's no longer awaiting approval.
	if drafts, err := s.PendingDrafts(10); err != nil || len(drafts) != 0 {
		t.Fatalf("PendingDrafts after reject = %+v, err=%v, want empty", drafts, err)
	}

	reason, prevText, ok, err := s.PendingRejectionNote("111@c.us")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || reason != "muy largo, resumilo" || prevText != "hola" {
		t.Errorf("PendingRejectionNote = reason=%q text=%q ok=%v, want the rejection reason + original text", reason, prevText, ok)
	}
}

// TestRejectDraftRejectsOnlyPending: a draft that isn't pending anymore
// (already approved/discarded/rejected) can't be rejected again.
func TestRejectDraftRejectsOnlyPending(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddDraft("111@c.us", "hola", "m", 1); err != nil {
		t.Fatal(err)
	}
	drafts, _ := s.PendingDrafts(10)
	id := drafts[0].ID
	if ok, err := s.DiscardDraft(id); err != nil || !ok {
		t.Fatalf("DiscardDraft: ok=%v err=%v", ok, err)
	}
	if _, _, _, ok, err := s.RejectDraft(id, "tarde"); err != nil || ok {
		t.Errorf("RejectDraft on an already-discarded draft: ok=%v err=%v, want ok=false", ok, err)
	}
}

// TestRejectThenRedraftContinuesRound: a redraft answering a rejection
// inherits round+1; the chain resets once a draft resolves any other way.
func TestRejectThenRedraftContinuesRound(t *testing.T) {
	s := openTestStore(t)
	chatJID := "111@c.us"

	if err := s.AddDraftWithConfirmer(chatJID, "intento 1", "m", "", "", 100, 1); err != nil {
		t.Fatal(err)
	}
	first, _ := s.PendingDrafts(10)
	if _, _, round, ok, err := s.RejectDraft(first[0].ID, "no"); err != nil || !ok || round != 1 {
		t.Fatalf("reject round 1: round=%d ok=%v err=%v", round, ok, err)
	}

	// The redraft — same chat, agent tries again.
	if err := s.AddDraftWithConfirmer(chatJID, "intento 2", "m", "", "", 100, 2); err != nil {
		t.Fatal(err)
	}
	second, err := s.PendingDrafts(10)
	if err != nil || len(second) != 1 {
		t.Fatalf("PendingDrafts = %+v, err=%v", second, err)
	}
	if second[0].Round != 2 {
		t.Errorf("redraft round = %d, want 2 (continues the chain the rejection started)", second[0].Round)
	}

	if _, _, round, ok, err := s.RejectDraft(second[0].ID, "sigue mal"); err != nil || !ok || round != 2 {
		t.Fatalf("reject round 2: round=%d ok=%v err=%v", round, ok, err)
	}

	if err := s.AddDraftWithConfirmer(chatJID, "intento 3", "m", "", "", 100, 3); err != nil {
		t.Fatal(err)
	}
	third, err := s.PendingDrafts(10)
	if err != nil || len(third) != 1 {
		t.Fatalf("PendingDrafts = %+v, err=%v", third, err)
	}
	if third[0].Round != MaxDraftRounds {
		t.Errorf("redraft round = %d, want %d (the cap)", third[0].Round, MaxDraftRounds)
	}

	// Approving ends the chain — a later, unrelated draft for the same chat
	// starts fresh at round 1, not round 4.
	if _, _, _, ok, err := s.ApproveDraft(third[0].ID, "", 4); err != nil || !ok {
		t.Fatalf("ApproveDraft: ok=%v err=%v", ok, err)
	}
	if err := s.AddDraft(chatJID, "tema nuevo", "m", 5); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.PendingDrafts(10)
	if err != nil || len(fresh) != 1 {
		t.Fatalf("PendingDrafts = %+v, err=%v", fresh, err)
	}
	if fresh[0].Round != 1 {
		t.Errorf("draft after an approved thread = round %d, want 1 (fresh thread, not a continuation)", fresh[0].Round)
	}
}

// TestPendingRejectionNoteSelfClears: once a redraft exists for the chat,
// the note stops pointing at the old rejection — the most-recent-draft
// lookup naturally moves on, no separate "resolved" flag needed.
func TestPendingRejectionNoteSelfClears(t *testing.T) {
	s := openTestStore(t)
	chatJID := "111@c.us"

	if err := s.AddDraft(chatJID, "intento 1", "m", 1); err != nil {
		t.Fatal(err)
	}
	drafts, _ := s.PendingDrafts(10)
	if _, _, _, ok, err := s.RejectDraft(drafts[0].ID, "motivo"); err != nil || !ok {
		t.Fatalf("RejectDraft: ok=%v err=%v", ok, err)
	}
	if _, _, ok, err := s.PendingRejectionNote(chatJID); err != nil || !ok {
		t.Fatalf("PendingRejectionNote before redraft: ok=%v err=%v, want true", ok, err)
	}

	if err := s.AddDraft(chatJID, "intento 2", "m", 2); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := s.PendingRejectionNote(chatJID); err != nil || ok {
		t.Errorf("PendingRejectionNote after redraft: ok=%v err=%v, want false (self-cleared)", ok, err)
	}
}

// TestPendingRejectionNoteEmptyForFreshChat: a chat with no drafts at all
// (or whose only draft is still pending/approved/discarded) has nothing to
// report — capipush must not fabricate a rejection note out of nothing.
func TestPendingRejectionNoteEmptyForFreshChat(t *testing.T) {
	s := openTestStore(t)
	if _, _, ok, err := s.PendingRejectionNote("nobody@c.us"); err != nil || ok {
		t.Errorf("PendingRejectionNote for an unknown chat: ok=%v err=%v, want false", ok, err)
	}

	if err := s.AddDraft("222@c.us", "hola", "m", 1); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := s.PendingRejectionNote("222@c.us"); err != nil || ok {
		t.Errorf("PendingRejectionNote for a still-pending draft: ok=%v err=%v, want false", ok, err)
	}
}

// TestEditDraftReplacesTextWithoutApproving is "editar sin aprobar" — text
// changes, status stays pending, still needs approve_draft.
func TestEditDraftReplacesTextWithoutApproving(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddDraft("111@c.us", "borrador original", "m", 1); err != nil {
		t.Fatal(err)
	}
	drafts, _ := s.PendingDrafts(10)
	id := drafts[0].ID

	if ok, err := s.EditDraft(id, "texto corregido"); err != nil || !ok {
		t.Fatalf("EditDraft: ok=%v err=%v", ok, err)
	}

	after, err := s.PendingDrafts(10)
	if err != nil || len(after) != 1 {
		t.Fatalf("PendingDrafts = %+v, err=%v", after, err)
	}
	if after[0].Text != "texto corregido" {
		t.Errorf("text after edit = %q, want %q", after[0].Text, "texto corregido")
	}
	if after[0].Status != "pending" {
		t.Errorf("status after edit = %q, want pending (edit never approves)", after[0].Status)
	}
}

// TestEditDraftOnlyPending: an already-resolved draft can't be edited —
// same "AND status = 'pending'" guard ApproveDraft/DiscardDraft/RejectDraft
// all share.
func TestEditDraftOnlyPending(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddDraft("111@c.us", "hola", "m", 1); err != nil {
		t.Fatal(err)
	}
	drafts, _ := s.PendingDrafts(10)
	id := drafts[0].ID
	if ok, err := s.DiscardDraft(id); err != nil || !ok {
		t.Fatalf("DiscardDraft: ok=%v err=%v", ok, err)
	}
	if ok, err := s.EditDraft(id, "intento tardío"); err != nil || ok {
		t.Errorf("EditDraft on a discarded draft: ok=%v err=%v, want ok=false", ok, err)
	}
}

// ── T108 (ct-2026-09-01-1413) — Draft.Sender ────────────────────────────────

// TestAddDraftWithConfirmerPersistsSender: a group draft created for a
// specific speaker carries that sender through PendingDrafts.
func TestAddDraftWithConfirmerPersistsSender(t *testing.T) {
	s := openTestStore(t)
	alice := "555000000001@s.whatsapp.net"
	if err := s.AddDraftWithConfirmer("555001@g.us", "hola alice", "m", "", alice, 100, 1); err != nil {
		t.Fatal(err)
	}
	drafts, err := s.PendingDrafts(10)
	if err != nil || len(drafts) != 1 {
		t.Fatalf("PendingDrafts = %+v, err=%v", drafts, err)
	}
	if drafts[0].Sender != alice {
		t.Errorf("Sender = %q, want %q", drafts[0].Sender, alice)
	}
}

// TestAddDraftDefaultsToEmptySender: the plain 1:1 wrapper (AddDraft, used
// by autoreply and the fresh-thread test helpers) leaves Sender empty —
// same "no scoping needed for a single-sender chat" degrade every other
// T108 call site relies on.
func TestAddDraftDefaultsToEmptySender(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddDraft("111@c.us", "hola", "m", 1); err != nil {
		t.Fatal(err)
	}
	drafts, err := s.PendingDrafts(10)
	if err != nil || len(drafts) != 1 {
		t.Fatalf("PendingDrafts = %+v, err=%v", drafts, err)
	}
	if drafts[0].Sender != "" {
		t.Errorf("Sender = %q, want empty", drafts[0].Sender)
	}
}

// TestApproveDraftReturnsSender: ApproveDraft hands the caller the sender
// it was created with — approve_draft's own MarkHandledBeforeForSender call
// has nothing else to key off.
func TestApproveDraftReturnsSender(t *testing.T) {
	s := openTestStore(t)
	alice := "555000000001@s.whatsapp.net"
	if err := s.AddDraftWithConfirmer("555001@g.us", "hola alice", "m", "", alice, 100, 1); err != nil {
		t.Fatal(err)
	}
	drafts, _ := s.PendingDrafts(10)

	_, _, sender, ok, err := s.ApproveDraft(drafts[0].ID, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("ApproveDraft ok=false, want true")
	}
	if sender != alice {
		t.Errorf("ApproveDraft sender = %q, want %q", sender, alice)
	}
}

// TestApproveDraftEmptySenderForOneOnOne: a 1:1 draft (AddDraft, no sender)
// approves with an empty sender — the caller's own IsGroupJID check is what
// keeps this from ever being treated as "scope to nobody".
func TestApproveDraftEmptySenderForOneOnOne(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddDraft("111@c.us", "hola", "m", 1); err != nil {
		t.Fatal(err)
	}
	drafts, _ := s.PendingDrafts(10)

	_, _, sender, ok, err := s.ApproveDraft(drafts[0].ID, "", 2)
	if err != nil || !ok {
		t.Fatalf("ApproveDraft: ok=%v err=%v", ok, err)
	}
	if sender != "" {
		t.Errorf("ApproveDraft sender = %q, want empty for a 1:1 draft", sender)
	}
}

// ── T122 (ct-2026-09-02-2045) — media drafts ────────────────────────────

// TestAddMediaDraftWithConfirmerPersistsMedia: PendingDrafts reads back the
// media fields a media draft was created with.
func TestAddMediaDraftWithConfirmerPersistsMedia(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddMediaDraftWithConfirmer("555000005@c.us", "mirá esto", "gpt-5", "", "",
		"media/out/abc.jpg", "image/jpeg", "photo", 0, 0, 1); err != nil {
		t.Fatal(err)
	}
	drafts, err := s.PendingDrafts(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 {
		t.Fatalf("got %d drafts, want 1", len(drafts))
	}
	d := drafts[0]
	if d.Text != "mirá esto" || d.MediaPath != "media/out/abc.jpg" || d.MediaMime != "image/jpeg" || d.MediaKind != "photo" {
		t.Errorf("got %+v, want caption/path/mime/kind to round-trip", d)
	}
}

// TestPendingDraftsTextOnlyHasNoMedia is the T122 regression guard for the
// read side, mirroring TestEnqueueTextOutboxHasNoMedia: an ordinary
// AddDraftWithConfirmer must come back with every media field empty.
func TestPendingDraftsTextOnlyHasNoMedia(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddDraft("555000006@c.us", "solo texto", "m", 1); err != nil {
		t.Fatal(err)
	}
	drafts, err := s.PendingDrafts(10)
	if err != nil {
		t.Fatal(err)
	}
	d := drafts[0]
	if d.MediaPath != "" || d.MediaMime != "" || d.MediaKind != "" {
		t.Errorf("text-only draft has media fields = %+v, want all empty", d)
	}
}

// TestApproveDraftWithMediaEnqueuesMedia: approving a media draft must
// enqueue it via the media path — DueOutbox's row carries the same
// path/mime/kind the draft had, not a plain text item.
func TestApproveDraftWithMediaEnqueuesMedia(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddMediaDraftWithConfirmer("555000007@c.us", "una foto", "gpt-5", "", "",
		"media/out/xyz.jpg", "image/jpeg", "photo", 0, 0, 1); err != nil {
		t.Fatal(err)
	}
	drafts, _ := s.PendingDrafts(10)
	if _, _, _, ok, err := s.ApproveDraft(drafts[0].ID, "", 2); err != nil || !ok {
		t.Fatalf("ApproveDraft: ok=%v err=%v", ok, err)
	}
	due, err := s.DueOutbox(10, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("got %d due, want 1", len(due))
	}
	o := due[0]
	if o.Text != "una foto" || o.MediaPath != "media/out/xyz.jpg" || o.MediaMime != "image/jpeg" || o.MediaKind != "photo" {
		t.Errorf("outbox item after approving a media draft = %+v, want the draft's media to carry over", o)
	}
}

// TestApproveDraftWithAudioCarriesSecondsToOutbox is T123's own regression
// (ct-2026-09-02-2121): a voice note draft's duration survives
// AddMediaDraftWithConfirmer -> ApproveDraft -> DueOutbox as the same
// honest media_seconds column, end to end.
func TestApproveDraftWithAudioCarriesSecondsToOutbox(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddMediaDraftWithConfirmer("555000009@c.us", "", "gpt-5", "", "",
		"media/out/voice.ogg", "audio/ogg; codecs=opus", "audio", 12, 0, 1); err != nil {
		t.Fatal(err)
	}
	drafts, err := s.PendingDrafts(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 || drafts[0].MediaSeconds != 12 {
		t.Fatalf("got %+v, want 1 draft with media_seconds=12", drafts)
	}
	if _, _, _, ok, err := s.ApproveDraft(drafts[0].ID, "", 2); err != nil || !ok {
		t.Fatalf("ApproveDraft: ok=%v err=%v", ok, err)
	}
	due, err := s.DueOutbox(10, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].MediaKind != "audio" || due[0].MediaSeconds != 12 {
		t.Fatalf("outbox item after approving an audio draft = %+v, want kind=audio media_seconds=12 carried over", due)
	}
}

// TestGetDraftFindsAnyStatus: get_drafts' draft_id lookup must find an
// approved/rejected/discarded draft too, not only a pending one — the
// caller already knows the id from a PRIOR PendingDrafts call, and by the
// time it asks for the photo the draft may have just been resolved.
func TestGetDraftFindsAnyStatus(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddDraft("555000008@c.us", "hola", "m", 1); err != nil {
		t.Fatal(err)
	}
	drafts, _ := s.PendingDrafts(10)
	id := drafts[0].ID
	if _, _, _, ok, err := s.ApproveDraft(id, "", 2); err != nil || !ok {
		t.Fatalf("ApproveDraft: ok=%v err=%v", ok, err)
	}
	d, ok, err := s.GetDraft(id)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("GetDraft ok=false for an approved draft, want true")
	}
	if d.Status != "approved" {
		t.Errorf("GetDraft status = %q, want approved", d.Status)
	}
}

// TestGetDraftMissingReturnsNotOK: a nonexistent id must not error, just
// report ok=false — same contract as ApproveDraft/DiscardDraft's own
// not-found case.
func TestGetDraftMissingReturnsNotOK(t *testing.T) {
	s := openTestStore(t)
	_, ok, err := s.GetDraft(999999)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("GetDraft on a missing id: ok=true, want false")
	}
}
