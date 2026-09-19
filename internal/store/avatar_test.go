package store

import "testing"

func TestGetAvatarNoRow(t *testing.T) {
	s := openTestStore(t)
	_, ok, err := s.GetAvatar("111@s.whatsapp.net")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("ok = true for a jid never checked, want false")
	}
}

func TestUpsertAvatarRoundTrips(t *testing.T) {
	s := openTestStore(t)
	jid := "111@s.whatsapp.net"
	want := Avatar{JID: jid, PictureID: "pic-abc", Path: "avatars/111.jpg", FetchedAt: 1000, NextCheckAt: 2000}
	if err := s.UpsertAvatar(want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetAvatar(jid)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got != want {
		t.Errorf("GetAvatar = %+v, ok=%v, want %+v, ok=true", got, ok, want)
	}
}

// TestUpsertAvatarOverwritesOnConflict covers the "unchanged" and "no
// photo" outcomes — both are a re-upsert of an existing jid, not a second
// write path (UpsertAvatar's own doc).
func TestUpsertAvatarOverwritesOnConflict(t *testing.T) {
	s := openTestStore(t)
	jid := "111@s.whatsapp.net"
	if err := s.UpsertAvatar(Avatar{JID: jid, PictureID: "pic-1", Path: "a.jpg", FetchedAt: 100, NextCheckAt: 200}); err != nil {
		t.Fatal(err)
	}
	// "confirmed no photo now" — PictureID/Path both cleared, next_check_at bumped.
	if err := s.UpsertAvatar(Avatar{JID: jid, PictureID: "", Path: "", FetchedAt: 300, NextCheckAt: 400}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetAvatar(jid)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got.PictureID != "" || got.Path != "" || got.NextCheckAt != 400 {
		t.Errorf("GetAvatar after clearing = %+v, want picture_id/path cleared, next_check_at=400", got)
	}
}
