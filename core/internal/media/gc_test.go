// SPDX-License-Identifier: AGPL-3.0-only
package media

import (
	"os"
	"path/filepath"
	"testing"

	"pimywa/internal/store"
)

// fakeStore is an in-memory stand-in for store.Store's media-relevant
// methods, so GC can be tested without a real SQLite file.
type fakeStore struct {
	chats []store.Chat
	media map[string][]store.Media
}

func (f *fakeStore) ListChats(limit int) ([]store.Chat, error) { return f.chats, nil }

func (f *fakeStore) ListMedia(chatJID string, limit int) ([]store.Media, error) {
	return f.media[chatJID], nil
}

func (f *fakeStore) DeleteMedia(chatJID, msgID string) error {
	items := f.media[chatJID]
	for i, m := range items {
		if m.MsgID == msgID {
			f.media[chatJID] = append(items[:i], items[i+1:]...)
			return nil
		}
	}
	return nil
}

func writeFile(t *testing.T, dir, name string, size int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestGCDeletesOldestUntilUnderCap is the core DoD requirement: GC respects
// the size cap, deletes oldest-first, and stops as soon as it's under.
func TestGCDeletesOldestUntilUnderCap(t *testing.T) {
	dir := t.TempDir()
	chatJID := "56999999999@s.whatsapp.net"

	m1 := store.Media{MsgID: "m1", ChatJID: chatJID, Path: writeFile(t, dir, "m1.jpg", 100), Size: 100, TS: 1}
	m2 := store.Media{MsgID: "m2", ChatJID: chatJID, Path: writeFile(t, dir, "m2.jpg", 100), Size: 100, TS: 2}
	m3 := store.Media{MsgID: "m3", ChatJID: chatJID, Path: writeFile(t, dir, "m3.jpg", 100), Size: 100, TS: 3}

	st := &fakeStore{
		chats: []store.Chat{{JID: chatJID}},
		media: map[string][]store.Media{chatJID: {m1, m2, m3}},
	}

	// Total 300, cap 150 → must delete the two oldest (m1, m2); m3 (100) fits.
	deleted, freed, err := GC(st, 150)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 || freed != 200 {
		t.Fatalf("deleted=%d freed=%d, want 2/200", deleted, freed)
	}
	if _, err := os.Stat(m1.Path); !os.IsNotExist(err) {
		t.Errorf("m1 file should be removed")
	}
	if _, err := os.Stat(m2.Path); !os.IsNotExist(err) {
		t.Errorf("m2 file should be removed")
	}
	if _, err := os.Stat(m3.Path); err != nil {
		t.Errorf("m3 file should survive: %v", err)
	}
	if got := st.media[chatJID]; len(got) != 1 || got[0].MsgID != "m3" {
		t.Errorf("DB should only have m3 left, got %+v", got)
	}
}

func TestGCDisabledWhenMaxBytesNonPositive(t *testing.T) {
	st := &fakeStore{chats: []store.Chat{{JID: "j"}}, media: map[string][]store.Media{"j": {{MsgID: "m1", ChatJID: "j", Size: 999}}}}
	deleted, freed, err := GC(st, 0)
	if err != nil || deleted != 0 || freed != 0 {
		t.Errorf("maxBytes<=0 should no-op, got deleted=%d freed=%d err=%v", deleted, freed, err)
	}
}

func TestGCUnderCapDeletesNothing(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "m1.jpg", 50)
	st := &fakeStore{
		chats: []store.Chat{{JID: "j"}},
		media: map[string][]store.Media{"j": {{MsgID: "m1", ChatJID: "j", Path: p, Size: 50, TS: 1}}},
	}
	deleted, _, err := GC(st, 1000)
	if err != nil || deleted != 0 {
		t.Errorf("under cap should delete nothing, got deleted=%d err=%v", deleted, err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("file should survive: %v", err)
	}
}

// TestGCNeverTouchesMessages documents/enforces the "text is never
// deleted" priority at the type level: mediaStore has no method that could
// touch the messages table at all, so GC structurally cannot delete text.
func TestGCNeverTouchesMessages(t *testing.T) {
	var _ mediaStore = (*fakeStore)(nil) // fakeStore only implements media-scoped methods
}
