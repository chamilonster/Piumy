// SPDX-License-Identifier: AGPL-3.0-only
package gateway

import (
	"context"
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow/types"

	"pimywa/internal/store"
)

// TestSyncGroupRegistersEmptyChatAndMembers covers "the scraped numbers are
// empty chats": a group with no messages yet still gets a chat row (ts=0),
// its topic lands in Description (0130), and every participant gets a
// chat_groups membership row.
func TestSyncGroupRegistersEmptyChatAndMembers(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// syncGroup only touches g.msgSt — no whatsmeow client needed for this test.
	g := &Gateway{msgSt: st}

	groupJID := types.JID{User: "12345", Server: "g.us"}
	p1 := types.JID{User: "111", Server: "s.whatsapp.net"}
	p2 := types.JID{User: "222", Server: "s.whatsapp.net"}

	g.syncGroup(groupJID, "Test Group", "el grupo de la oficina", []types.GroupParticipant{{JID: p1}, {JID: p2}})

	chats, err := st.ListChats(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 || chats[0].JID != groupJID.String() || chats[0].Name != "Test Group" {
		t.Fatalf("got chats=%+v, want one empty chat for the group", chats)
	}
	if chats[0].Description != "el grupo de la oficina" {
		t.Errorf("Description = %q, want the group's topic", chats[0].Description)
	}
	if chats[0].LastTS != 0 {
		t.Errorf("empty chat should have last_ts=0 (no messages yet), got %d", chats[0].LastTS)
	}

	groups, err := st.GroupsOf(p1.String())
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0] != groupJID.String() {
		t.Errorf("p1's groups = %v, want [%s]", groups, groupJID.String())
	}

	groups, err = st.GroupsOf(p2.String())
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0] != groupJID.String() {
		t.Errorf("p2's groups = %v, want [%s]", groups, groupJID.String())
	}
}

// TestSyncGroupIdempotent covers the "continuous + incremental" design: a
// group's channel snapshot is always the full current set, so re-running the
// same sweep must be a harmless no-op, not a duplicate/error.
func TestSyncGroupIdempotent(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	g := &Gateway{msgSt: st}

	groupJID := types.JID{User: "12345", Server: "g.us"}
	p1 := types.JID{User: "111", Server: "s.whatsapp.net"}

	g.syncGroup(groupJID, "Test Group", "topic", []types.GroupParticipant{{JID: p1}})
	g.syncGroup(groupJID, "Test Group", "topic", []types.GroupParticipant{{JID: p1}}) // re-run

	groups, err := st.GroupsOf(p1.String())
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 {
		t.Errorf("re-running sync should not duplicate membership, got %v", groups)
	}
}

// TestSyncGroupInviteLinkSkipsWhenAlreadyKnown covers the "acota llamadas al
// servidor" requirement (0130): if the group already has an invite link,
// syncGroupInviteLink must return before ever touching g.client — proven
// here by a nil client that would panic if the fetch path were reached.
func TestSyncGroupInviteLinkSkipsWhenAlreadyKnown(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	groupJID := types.JID{User: "12345", Server: "g.us"}
	if err := st.TouchChat(groupJID.String(), "Group", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetGroupInviteLink(groupJID.String(), "https://chat.whatsapp.com/already-known"); err != nil {
		t.Fatal(err)
	}

	g := &Gateway{msgSt: st} // client is nil — a real fetch attempt would panic
	g.syncGroupInviteLink(context.Background(), groupJID)

	c, ok, err := st.GetChat(groupJID.String())
	if err != nil || !ok || c.GroupInviteLink != "https://chat.whatsapp.com/already-known" {
		t.Fatalf("GroupInviteLink changed unexpectedly: %+v ok=%v err=%v", c, ok, err)
	}
}
