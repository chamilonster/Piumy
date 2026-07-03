// SPDX-License-Identifier: AGPL-3.0-only
package gateway

import (
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"

	"pimywa/internal/store"
)

// TestMediaSkipped covers the DoD directly (0753): video/photo are gated by
// type × origin, stickers are never gated (never gated: "se tienen que
// guardar también todos los stickers").
func TestMediaSkipped(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	g := &Gateway{msgSt: st}

	cases := []struct {
		name    string
		kind    string
		isGroup bool
		setting string
		want    bool
	}{
		{"video from group, not skipped by default", "video", true, "", false},
		{"video from group, skipped when set", "video", true, store.SettingMediaSkipVideoGroup, true},
		{"video from chat, skipped when set", "video", false, store.SettingMediaSkipVideoChat, true},
		{"image from group, skipped when set", "image", true, store.SettingMediaSkipPhotoGroup, true},
		{"image from chat, skipped when set", "image", false, store.SettingMediaSkipPhotoChat, true},
		{"sticker never skipped", "sticker", true, store.SettingMediaSkipVideoGroup, false},
		{"unknown kind never skipped", "", true, store.SettingMediaSkipVideoGroup, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.setting != "" {
				if err := st.SetSettingBool(c.setting, true); err != nil {
					t.Fatal(err)
				}
				defer st.SetSettingBool(c.setting, false)
			}
			if got := g.mediaSkipped(c.kind, c.isGroup); got != c.want {
				t.Errorf("mediaSkipped(%q, isGroup=%v) = %v, want %v", c.kind, c.isGroup, got, c.want)
			}
		})
	}
}

// TestScheduleMediaDownloadSkipsWhenConfigured covers the DoD's "no
// descarga" from scheduleMediaDownload's own angle: with the group-video
// skip on, it must return before ever reaching the download goroutine —
// proven by a nil client (g.dl.Download would panic if reached) and no
// media row ending up in the store.
func TestScheduleMediaDownloadSkipsWhenConfigured(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetSettingBool(store.SettingMediaSkipVideoGroup, true); err != nil {
		t.Fatal(err)
	}

	g := &Gateway{msgSt: st} // dl is the zero value — Download would panic if reached

	groupJID := "12345-67890@g.us"
	g.scheduleMediaDownload(groupJID, "m1", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{}}, 1)

	media, err := st.ListMedia(groupJID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(media) != 0 {
		t.Fatalf("got %d media rows, want 0 — the download must never have been scheduled", len(media))
	}
}
