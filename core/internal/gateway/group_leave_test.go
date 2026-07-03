// SPDX-License-Identifier: AGPL-3.0-only
package gateway

import (
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"pimywa/internal/store"
)

// TestReactiveGroupLeave drives the real handleEvent dispatch (not just the
// store accessor) for a *events.GroupInfo with Leave — the DoD's "GroupsOf
// reflects the exit" from the gateway's angle, not just store's.
func TestReactiveGroupLeave(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	groupJID := types.JID{User: "12345", Server: "g.us"}
	member := types.JID{User: "111", Server: "s.whatsapp.net"}
	if err := st.AddGroupMember(member.String(), groupJID.String()); err != nil {
		t.Fatal(err)
	}

	g := &Gateway{msgSt: st} // Leave handling only touches msgSt, no client needed

	g.handleEvent(&events.GroupInfo{JID: groupJID, Leave: []types.JID{member}})

	// The removal runs in its own goroutine — poll briefly instead of a
	// blind sleep.
	deadline := time.Now().Add(2 * time.Second)
	for {
		groups, err := st.GroupsOf(member.String())
		if err != nil {
			t.Fatal(err)
		}
		if len(groups) == 0 {
			return // success
		}
		if time.Now().After(deadline) {
			t.Fatalf("member still in %v after leaving, want removed", groups)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestReactiveGroupTopicAndInviteLinkChange covers 0130's push path: a
// *events.GroupInfo carrying Topic/NewInviteLink updates Description/
// GroupInviteLink directly, with ZERO WhatsApp-server calls (better
// anti-ban than any re-fetch — this is testable with no client at all).
func TestReactiveGroupTopicAndInviteLinkChange(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	groupJID := types.JID{User: "12345", Server: "g.us"}
	if err := st.TouchChat(groupJID.String(), "Group", 1); err != nil {
		t.Fatal(err)
	}

	g := &Gateway{msgSt: st} // no client needed — this path never calls WhatsApp

	newLink := "https://chat.whatsapp.com/abc123"
	g.handleEvent(&events.GroupInfo{
		JID:           groupJID,
		Topic:         &types.GroupTopic{Topic: "nuevo tema del grupo"},
		NewInviteLink: &newLink,
	})

	c, ok, err := st.GetChat(groupJID.String())
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	if c.Description != "nuevo tema del grupo" {
		t.Errorf("Description = %q, want the pushed topic", c.Description)
	}
	if c.GroupInviteLink != newLink {
		t.Errorf("GroupInviteLink = %q, want the pushed link", c.GroupInviteLink)
	}
}

// TestReactiveGroupInfoNilFieldsAreSafe covers the nil-pointer guard: a
// *events.GroupInfo with Topic/NewInviteLink both nil (e.g. a membership-only
// change) must not panic and must not touch either field.
func TestReactiveGroupInfoNilFieldsAreSafe(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	groupJID := types.JID{User: "12345", Server: "g.us"}
	if err := st.TouchChat(groupJID.String(), "Group", 1); err != nil {
		t.Fatal(err)
	}

	g := &Gateway{msgSt: st}
	g.handleEvent(&events.GroupInfo{JID: groupJID}) // Topic, NewInviteLink both nil

	c, ok, err := st.GetChat(groupJID.String())
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	if c.Description != "" || c.GroupInviteLink != "" {
		t.Errorf("got Description=%q GroupInviteLink=%q, want both untouched (empty)", c.Description, c.GroupInviteLink)
	}
}
