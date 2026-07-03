// SPDX-License-Identifier: AGPL-3.0-only
package store

import (
	"path/filepath"
	"testing"
	"time"
)

// TestMigrateIdempotent covers the DoD requirement directly: opening the same
// DB twice (simulating a service restart against an already-migrated file)
// must not error, and the resulting columns/tables must exist either way.
func TestMigrateIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pimywa.db")

	st1, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	st1.Close()

	st2, err := Open(path)
	if err != nil {
		t.Fatalf("second open (must be idempotent): %v", err)
	}
	defer st2.Close()

	if err := st2.SetActive("x@s.whatsapp.net", true); err != nil {
		t.Fatalf("columns from migration not usable: %v", err)
	}
}

// TestTouchChatGroupDefaultsIgnored covers the DoD's group half (0800):
// TouchChat gives a WhatsApp group JID status "ignored" on first insert
// (instead of the regular "new"), and never overwrites a status the
// owner already set on a re-touch.
func TestTouchChatGroupDefaultsIgnored(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	contactJID := "56999999999@s.whatsapp.net"
	if err := st.TouchChat(contactJID, "Contact", 1); err != nil {
		t.Fatal(err)
	}
	if c, ok, err := st.GetChat(contactJID); err != nil || !ok || c.Status != "new" {
		t.Fatalf("non-group default: Status=%q, want new", c.Status)
	}

	groupJID := "12345-67890@g.us"
	if err := st.TouchChat(groupJID, "Group", 1); err != nil {
		t.Fatal(err)
	}
	c, ok, err := st.GetChat(groupJID)
	if err != nil || !ok || c.Status != "ignored" {
		t.Fatalf("group default: Status=%q, want ignored", c.Status)
	}

	// The owner activates the group — a re-touch (e.g. a new message
	// arriving) must not reset it back to ignored.
	if err := st.SetStatus(groupJID, "whitelist"); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat(groupJID, "Group", 2); err != nil {
		t.Fatal(err)
	}
	c, ok, err = st.GetChat(groupJID)
	if err != nil || !ok || c.Status != "whitelist" {
		t.Fatalf("after re-touch: Status=%q, want whitelist (not reset to ignored)", c.Status)
	}
}

// TestChatFields covers the new chats.active/archived/status round-trip.
func TestChatFields(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	jid := "56955147132@s.whatsapp.net"
	if err := st.TouchChat(jid, "Boss", 100); err != nil {
		t.Fatal(err)
	}

	chats, err := st.ListChats(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 {
		t.Fatalf("got %d chats, want 1", len(chats))
	}
	if chats[0].Active || chats[0].Archived || chats[0].Status != "new" {
		t.Errorf("defaults wrong: active=%v archived=%v status=%q, want false/false/new",
			chats[0].Active, chats[0].Archived, chats[0].Status)
	}

	if err := st.SetActive(jid, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetArchived(jid, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatus(jid, "whitelist"); err != nil {
		t.Fatal(err)
	}

	chats, err = st.ListChats(10)
	if err != nil {
		t.Fatal(err)
	}
	c := chats[0]
	if !c.Active || !c.Archived || c.Status != "whitelist" {
		t.Errorf("after set: active=%v archived=%v status=%q, want true/true/whitelist",
			c.Active, c.Archived, c.Status)
	}
}

// TestSetIsBoss covers the trust-flag round-trip via GetChat. This accessor
// is deliberately the ONLY way to set it — no MCP tool calls it (verified in
// the mcpserver package).
func TestSetIsBoss(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	jid := "56955147132@s.whatsapp.net"
	if err := st.TouchChat(jid, "Boss", 1); err != nil {
		t.Fatal(err)
	}
	c, ok, err := st.GetChat(jid)
	if err != nil || !ok || c.IsBoss {
		t.Fatalf("default: IsBoss=%v, want false", c.IsBoss)
	}

	if err := st.SetIsBoss(jid, true); err != nil {
		t.Fatal(err)
	}
	c, ok, err = st.GetChat(jid)
	if err != nil || !ok || !c.IsBoss {
		t.Fatalf("after SetIsBoss(true): IsBoss=%v, want true", c.IsBoss)
	}

	if err := st.SetIsBoss(jid, false); err != nil {
		t.Fatal(err)
	}
	c, ok, err = st.GetChat(jid)
	if err != nil || !ok || c.IsBoss {
		t.Fatalf("after SetIsBoss(false): IsBoss=%v, want false", c.IsBoss)
	}
}

// TestChatMemoryContextRulesPersist covers the DoD's "los 3 persisten"
// (0647): SetChatMemory/SetChatContext/SetChatRules each round-trip through
// GetChat, and default to empty for a chat that never set them.
func TestChatMemoryContextRulesPersist(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	jid := "56999999999@s.whatsapp.net"
	if err := st.TouchChat(jid, "Contact", 1); err != nil {
		t.Fatal(err)
	}
	c, ok, err := st.GetChat(jid)
	if err != nil || !ok || c.Memory != "" || c.Context != "" || c.Rules != "" {
		t.Fatalf("default: Memory=%q Context=%q Rules=%q, want all empty", c.Memory, c.Context, c.Rules)
	}

	if err := st.SetChatMemory(jid, "le gusta el café"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatContext(jid, "cliente frecuente"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatRules(jid, "tratarlo de usted"); err != nil {
		t.Fatal(err)
	}
	c, ok, err = st.GetChat(jid)
	if err != nil || !ok {
		t.Fatalf("GetChat after setting all three: ok=%v err=%v", ok, err)
	}
	if c.Memory != "le gusta el café" || c.Context != "cliente frecuente" || c.Rules != "tratarlo de usted" {
		t.Fatalf("got Memory=%q Context=%q Rules=%q, want the values set", c.Memory, c.Context, c.Rules)
	}

	chats, err := st.ListChats(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 || chats[0].Memory != "le gusta el café" || chats[0].Context != "cliente frecuente" || chats[0].Rules != "tratarlo de usted" {
		t.Fatalf("ListChats = %+v, want the same memory/context/rules as GetChat", chats)
	}
}

// TestConfirmationModeAndConfirmerPersist covers the DoD's store half of
// 0810: a 1-1 chat's default is "none" (no confirmer), and
// SetConfirmationMode/SetConfirmer round-trip through GetChat. The by-type
// default itself (1-1 vs group) is covered by TestTouchChatConfirmationByType.
func TestConfirmationModeAndConfirmerPersist(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	jid := "56999999999@s.whatsapp.net"
	if err := st.TouchChat(jid, "Contact", 1); err != nil {
		t.Fatal(err)
	}
	c, ok, err := st.GetChat(jid)
	if err != nil || !ok || c.ConfirmationMode != "none" || c.Confirmer != "" {
		t.Fatalf("default: ConfirmationMode=%q Confirmer=%q, want none/empty (0810: 1-1 default is no confirmation)", c.ConfirmationMode, c.Confirmer)
	}

	if err := st.SetConfirmationMode(jid, "required"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetConfirmer(jid, "56911112222@s.whatsapp.net"); err != nil {
		t.Fatal(err)
	}
	c, ok, err = st.GetChat(jid)
	if err != nil || !ok || c.ConfirmationMode != "required" || c.Confirmer != "56911112222@s.whatsapp.net" {
		t.Fatalf("after Set*: ConfirmationMode=%q Confirmer=%q, want required/56911112222@s.whatsapp.net", c.ConfirmationMode, c.Confirmer)
	}
}

// TestChatDescriptionAndGroupInviteLinkPersist covers the DoD's store half
// of 0130: both fields default empty, SetChatDescription/SetGroupInviteLink
// round-trip through GetChat AND ListChats, and re-opening the DB (exercising
// the idempotent migration) doesn't break either.
func TestChatDescriptionAndGroupInviteLinkPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pimywa.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	jid := "12345-67890@g.us"
	if err := st.TouchChat(jid, "Group", 1); err != nil {
		t.Fatal(err)
	}
	c, ok, err := st.GetChat(jid)
	if err != nil || !ok || c.Description != "" || c.GroupInviteLink != "" {
		t.Fatalf("default: Description=%q GroupInviteLink=%q, want both empty", c.Description, c.GroupInviteLink)
	}

	if err := st.SetChatDescription(jid, "el grupo de la oficina, con link: https://example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetGroupInviteLink(jid, "https://chat.whatsapp.com/abc123"); err != nil {
		t.Fatal(err)
	}
	c, ok, err = st.GetChat(jid)
	if err != nil || !ok || c.Description != "el grupo de la oficina, con link: https://example.com" || c.GroupInviteLink != "https://chat.whatsapp.com/abc123" {
		t.Fatalf("after Set*: Description=%q GroupInviteLink=%q, want the values set", c.Description, c.GroupInviteLink)
	}

	chats, err := st.ListChats(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 || chats[0].Description != c.Description || chats[0].GroupInviteLink != c.GroupInviteLink {
		t.Fatalf("ListChats = %+v, want the same Description/GroupInviteLink as GetChat", chats)
	}
	st.Close()

	// Re-open (exercises the idempotent migration) must not break either field.
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("second open (must be idempotent): %v", err)
	}
	defer st2.Close()
	c, ok, err = st2.GetChat(jid)
	if err != nil || !ok || c.Description == "" || c.GroupInviteLink == "" {
		t.Fatalf("after re-open: Description=%q GroupInviteLink=%q, want both to survive", c.Description, c.GroupInviteLink)
	}
}

// TestTouchChatConfirmationByType covers the DoD directly (0810): TouchChat
// defaults confirmation_mode by chat type — group ("required"), 1-1
// ("none") — and never resets it on a re-touch once the owner overrides it.
func TestTouchChatConfirmationByType(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	oneOnOne := "56999999999@s.whatsapp.net"
	if err := st.TouchChat(oneOnOne, "Contact", 1); err != nil {
		t.Fatal(err)
	}
	if c, ok, err := st.GetChat(oneOnOne); err != nil || !ok || c.ConfirmationMode != "none" {
		t.Fatalf("1-1 default: ConfirmationMode=%q, want none", c.ConfirmationMode)
	}

	group := "12345-67890@g.us"
	if err := st.TouchChat(group, "Group", 1); err != nil {
		t.Fatal(err)
	}
	if c, ok, err := st.GetChat(group); err != nil || !ok || c.ConfirmationMode != "required" {
		t.Fatalf("group default: ConfirmationMode=%q, want required", c.ConfirmationMode)
	}

	// Boss overrides the group to "none" — a re-touch (new message arriving)
	// must not reset it back to required.
	if err := st.SetConfirmationMode(group, "none"); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat(group, "Group", 2); err != nil {
		t.Fatal(err)
	}
	if c, ok, err := st.GetChat(group); err != nil || !ok || c.ConfirmationMode != "none" {
		t.Fatalf("after re-touch: ConfirmationMode=%q, want none (not reset to required)", c.ConfirmationMode)
	}
}

// TestGetChat covers the single-chat read accessor: found vs not-found, and
// that it reflects the same fields ListChats does.
func TestGetChat(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, ok, err := st.GetChat("missing@s.whatsapp.net"); err != nil || ok {
		t.Fatalf("GetChat on missing chat: ok=%v err=%v, want ok=false err=nil", ok, err)
	}

	jid := "56955147132@s.whatsapp.net"
	if err := st.TouchChat(jid, "Boss", 100); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatus(jid, "whitelist"); err != nil {
		t.Fatal(err)
	}

	c, ok, err := st.GetChat(jid)
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v, want ok=true", ok, err)
	}
	if c.Name != "Boss" || c.Status != "whitelist" {
		t.Errorf("GetChat = %+v, want name=Boss status=whitelist", c)
	}
}

// TestMessageFields covers the new messages.model/delivered_ts/read_ts round-trip.
func TestMessageFields(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	jid := "56955147132@s.whatsapp.net"
	if err := st.AddMessage(Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(Message{ChatJID: jid, ID: "m2", FromMe: true, Text: "hola de vuelta", TS: 2, Model: "claude-opus-4-8"}); err != nil {
		t.Fatal(err)
	}

	msgs, err := st.GetMessages(jid, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	// ORDER BY ts DESC → m2 first.
	if msgs[0].Model != "claude-opus-4-8" {
		t.Errorf("m2.Model = %q, want claude-opus-4-8", msgs[0].Model)
	}
	if msgs[1].Model != "" {
		t.Errorf("m1.Model = %q, want empty (inbound)", msgs[1].Model)
	}
	if msgs[0].DeliveredTS != 0 || msgs[0].ReadTS != 0 {
		t.Errorf("fresh message should have no receipts yet, got delivered=%d read=%d",
			msgs[0].DeliveredTS, msgs[0].ReadTS)
	}

	if err := st.SetDelivered(jid, "m2", 50); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRead(jid, "m2", 60); err != nil {
		t.Fatal(err)
	}
	msgs, err = st.GetMessages(jid, 10)
	if err != nil {
		t.Fatal(err)
	}
	if msgs[0].DeliveredTS != 50 || msgs[0].ReadTS != 60 {
		t.Errorf("receipts = delivered=%d read=%d, want 50/60", msgs[0].DeliveredTS, msgs[0].ReadTS)
	}
}

// TestEnqueueWithModel covers the outbox.model round-trip: EnqueueWithModel
// carries the model through PendingOutbox, and Enqueue (no model concept —
// a human-sent REST/dashboard message) leaves it empty.
func TestEnqueueWithModel(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.EnqueueWithModel("j1", "hola", 1, "claude-opus-4-8"); err != nil {
		t.Fatal(err)
	}
	if err := st.Enqueue("j2", "manual", 2); err != nil {
		t.Fatal(err)
	}

	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 {
		t.Fatalf("got %d pending, want 2", len(pending))
	}
	// PendingOutbox orders by seq ASC, so [0]=j1 (model), [1]=j2 (no model).
	if pending[0].Model != "claude-opus-4-8" {
		t.Errorf("pending[0].Model = %q, want claude-opus-4-8", pending[0].Model)
	}
	if pending[1].Model != "" {
		t.Errorf("pending[1].Model = %q, want empty (human-sent)", pending[1].Model)
	}
}

// TestDueOutboxRespectsBackoff covers the core anti-ban requirement: a failed
// item is excluded from DueOutbox until its next_retry_ts elapses, but
// PendingOutbox (visibility) still shows it regardless.
func TestDueOutboxRespectsBackoff(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.Enqueue("j1", "hola", 1); err != nil {
		t.Fatal(err)
	}
	due, err := st.DueOutbox(10, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("fresh item should be immediately due, got %d", len(due))
	}
	seq := due[0].Seq

	// Simulate a failed attempt: backoff until ts=2000.
	if err := st.SetOutboxRetry(seq, 1, 2000, "connection refused"); err != nil {
		t.Fatal(err)
	}

	if due, err := st.DueOutbox(10, 1500); err != nil || len(due) != 0 {
		t.Fatalf("before next_retry_ts: due=%d err=%v, want 0 items", len(due), err)
	}
	due, err = st.DueOutbox(10, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].RetryCount != 1 || due[0].LastError != "connection refused" {
		t.Fatalf("at/after next_retry_ts: got %+v, want 1 item with retry state", due)
	}

	// PendingOutbox (visibility) shows it regardless of backoff timing.
	pending, err := st.PendingOutbox(10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("PendingOutbox should still show the backing-off item, got %d err=%v", len(pending), err)
	}
}

// TestDeadLetterOutbox covers "tras N fallos → dead-letter y no vuelve al
// flujo": dead-lettering removes an item from DueOutbox permanently (not
// just until some future ts) but never deletes it.
func TestDeadLetterOutbox(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.Enqueue("j1", "hola", 1); err != nil {
		t.Fatal(err)
	}
	due, err := st.DueOutbox(10, 1)
	if err != nil || len(due) != 1 {
		t.Fatalf("setup: due=%d err=%v", len(due), err)
	}
	seq := due[0].Seq

	if err := st.DeadLetterOutbox(seq, "gave up after 5 failures"); err != nil {
		t.Fatal(err)
	}

	// Even far in the future, a dead-lettered item never becomes due again.
	if due, err := st.DueOutbox(10, 999999999); err != nil || len(due) != 0 {
		t.Fatalf("dead-lettered item resurfaced in DueOutbox: due=%d err=%v", len(due), err)
	}

	// But it's not deleted — still visible for inspection via PendingOutbox.
	pending, err := st.PendingOutbox(10)
	if err != nil || len(pending) != 1 || !pending[0].DeadLetter || pending[0].LastError != "gave up after 5 failures" {
		t.Fatalf("dead-lettered item should stay visible, got %+v err=%v", pending, err)
	}
}

// TestChatGroups covers the member↔group relation ("is on group: x,y,z").
func TestChatGroups(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	member := "56955147132@s.whatsapp.net"
	if err := st.AddGroupMember(member, "group1@g.us"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddGroupMember(member, "group2@g.us"); err != nil {
		t.Fatal(err)
	}
	// Duplicate insert must not error (UNIQUE + INSERT OR IGNORE).
	if err := st.AddGroupMember(member, "group1@g.us"); err != nil {
		t.Fatal(err)
	}

	groups, err := st.GroupsOf(member)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2 (%v)", len(groups), groups)
	}

	// RemoveGroupMember: AddGroupMember's counterpart. GroupsOf reflects the exit.
	if err := st.RemoveGroupMember(member, "group1@g.us"); err != nil {
		t.Fatal(err)
	}
	groups, err = st.GroupsOf(member)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0] != "group2@g.us" {
		t.Fatalf("after leaving group1: got %v, want only [group2@g.us]", groups)
	}

	// Removing a membership that doesn't exist must not error (idempotent).
	if err := st.RemoveGroupMember(member, "group1@g.us"); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveGroupMember("nobody@s.whatsapp.net", "group1@g.us"); err != nil {
		t.Fatal(err)
	}
}

// TestMedia covers add/list/delete for the media table (GC target — text in
// messages is never touched by these calls).
func TestMedia(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	jid := "56955147132@s.whatsapp.net"
	if err := st.AddMedia(Media{MsgID: "m1", ChatJID: jid, Path: "/data/media/m1.jpg", Mime: "image/jpeg", Size: 1024, TS: 10}); err != nil {
		t.Fatal(err)
	}

	list, err := st.ListMedia(jid, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Size != 1024 {
		t.Fatalf("got %+v, want 1 item with size 1024", list)
	}

	if err := st.DeleteMedia(jid, "m1"); err != nil {
		t.Fatal(err)
	}
	list, err = st.ListMedia(jid, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("got %d media after delete, want 0", len(list))
	}
}

// TestChatOrigin covers all 3 origin categories.
func TestChatOrigin(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	synced := "synced@s.whatsapp.net"
	if err := st.TouchChat(synced, "Synced", 0); err != nil {
		t.Fatal(err)
	}
	if got, err := st.ChatOrigin(synced); err != nil || got != "synced_contact" {
		t.Errorf("synced_contact: got %q err=%v", got, err)
	}

	grouped := "grouped@s.whatsapp.net"
	if err := st.AddGroupMember(grouped, "group1@g.us"); err != nil {
		t.Fatal(err)
	}
	if got, err := st.ChatOrigin(grouped); err != nil || got != "group_discovered" {
		t.Errorf("group_discovered: got %q err=%v", got, err)
	}

	spoke := "spoke@s.whatsapp.net"
	if err := st.AddMessage(Message{ChatJID: spoke, ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if got, err := st.ChatOrigin(spoke); err != nil || got != "inbound_spoke" {
		t.Errorf("inbound_spoke: got %q err=%v", got, err)
	}

	// A chat that's both in a group AND has spoken is inbound_spoke — having
	// actually talked outranks merely being discovered via group sync.
	both := "both@s.whatsapp.net"
	if err := st.AddGroupMember(both, "group1@g.us"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(Message{ChatJID: both, ID: "m1", FromMe: false, Text: "hi", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if got, err := st.ChatOrigin(both); err != nil || got != "inbound_spoke" {
		t.Errorf("spoke+grouped: got %q err=%v, want inbound_spoke", got, err)
	}
}

// TestGetChatLastSpeakerAndModel covers the DoD directly: last_speaker
// them/us, and last_model tracking the last OUTBOUND message specifically —
// even after a newer inbound message arrives (the agent must not
// always have the last word, so we need to know who spoke last vs who
// answered last, separately).
func TestGetChatLastSpeakerAndModel(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	jid := "56955147132@s.whatsapp.net"

	// No messages yet.
	c, ok, err := st.GetChat(jid)
	if err != nil {
		t.Fatal(err)
	}
	if ok && c.LastSpeaker != "" {
		t.Errorf("no messages: LastSpeaker = %q, want empty", c.LastSpeaker)
	}

	// Inbound: last_speaker = them.
	if err := st.AddMessage(Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}
	c, ok, err = st.GetChat(jid)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if c.LastSpeaker != "them" || c.Origin != "inbound_spoke" {
		t.Errorf("after inbound: LastSpeaker=%q Origin=%q, want them/inbound_spoke", c.LastSpeaker, c.Origin)
	}
	if c.LastModel != "" {
		t.Errorf("no outbound yet: LastModel = %q, want empty", c.LastModel)
	}

	// Outbound reply: last_speaker = us, last_model set.
	if err := st.AddMessage(Message{ChatJID: jid, ID: "m2", FromMe: true, Text: "hi", TS: 2, Model: "claude-opus-4-8"}); err != nil {
		t.Fatal(err)
	}
	c, ok, err = st.GetChat(jid)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if c.LastSpeaker != "us" {
		t.Errorf("after our reply: LastSpeaker = %q, want us", c.LastSpeaker)
	}
	if c.LastModel != "claude-opus-4-8" {
		t.Errorf("LastModel = %q, want claude-opus-4-8", c.LastModel)
	}

	// A newer inbound arrives: last_speaker flips back to them, but
	// last_model still reflects our (still most recent) outbound reply.
	if err := st.AddMessage(Message{ChatJID: jid, ID: "m3", FromMe: false, Text: "de nuevo", TS: 3}); err != nil {
		t.Fatal(err)
	}
	c, ok, err = st.GetChat(jid)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if c.LastSpeaker != "them" {
		t.Errorf("after new inbound: LastSpeaker = %q, want them", c.LastSpeaker)
	}
	if c.LastModel != "claude-opus-4-8" {
		t.Errorf("LastModel after new inbound = %q, want it to still show the last outbound model", c.LastModel)
	}
}

// TestPendingChats covers the DoD directly: a chat with last inbound shows
// up; one we replied to does not (the golden rule — never always have
// the last word); it reappears if the contact writes again.
func TestPendingChats(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	jid := "56955147132@s.whatsapp.net"
	if err := st.AddMessage(Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola boss", TS: 100}); err != nil {
		t.Fatal(err)
	}

	pending, err := st.PendingChats(20, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].JID != jid {
		t.Fatalf("after inbound: got %+v, want 1 pending chat", pending)
	}
	if pending[0].AgeSec != 100 || pending[0].Origin != "inbound_spoke" {
		t.Errorf("pending fields = %+v, want AgeSec=100 Origin=inbound_spoke", pending[0])
	}
	if pending[0].Preview != "hola boss" {
		t.Errorf("Preview = %q, want %q", pending[0].Preview, "hola boss")
	}

	// We reply: no longer pending (never always have the last word).
	if err := st.AddMessage(Message{ChatJID: jid, ID: "m2", FromMe: true, Text: "hi", TS: 150, Model: "claude-opus-4-8"}); err != nil {
		t.Fatal(err)
	}
	pending, err = st.PendingChats(20, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("after our reply: got %+v, want 0 pending (we have the last word)", pending)
	}

	// Contact writes again: pending again.
	if err := st.AddMessage(Message{ChatJID: jid, ID: "m3", FromMe: false, Text: "de nuevo", TS: 180}); err != nil {
		t.Fatal(err)
	}
	pending, err = st.PendingChats(20, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].AgeSec != 20 {
		t.Fatalf("after contact re-writes: got %+v, want 1 pending with AgeSec=20", pending)
	}
}

// TestPendingChatsOrdering covers "oldest first" (longest-waiting first).
func TestPendingChatsOrdering(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.AddMessage(Message{ChatJID: "newer@s.whatsapp.net", ID: "m1", FromMe: false, Text: "x", TS: 200}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(Message{ChatJID: "older@s.whatsapp.net", ID: "m1", FromMe: false, Text: "y", TS: 100}); err != nil {
		t.Fatal(err)
	}

	pending, err := st.PendingChats(20, 300)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 || pending[0].JID != "older@s.whatsapp.net" || pending[1].JID != "newer@s.whatsapp.net" {
		t.Fatalf("got %+v, want older first", pending)
	}
}

// TestApproveDraft covers the DoD: approving moves a draft into the outbox
// with its model, and marks it approved (no longer pending).
func TestApproveDraft(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	jid := "56999999999@s.whatsapp.net"
	if err := st.AddDraft(jid, "hola, gracias por escribir", "deepseek-chat", 1); err != nil {
		t.Fatal(err)
	}
	drafts, err := st.PendingDrafts(10)
	if err != nil || len(drafts) != 1 {
		t.Fatalf("setup: drafts=%+v err=%v", drafts, err)
	}
	id := drafts[0].ID

	ok, err := st.ApproveDraft(id, "", 100)
	if err != nil || !ok {
		t.Fatalf("ApproveDraft: ok=%v err=%v", ok, err)
	}

	outbox, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(outbox) != 1 || outbox[0].ToJID != jid || outbox[0].Text != "hola, gracias por escribir" || outbox[0].Model != "deepseek-chat" {
		t.Fatalf("outbox after approve = %+v, want the draft's content", outbox)
	}

	if pending, err := st.PendingDrafts(10); err != nil || len(pending) != 0 {
		t.Fatalf("draft should no longer be pending, got %+v err=%v", pending, err)
	}

	// Approving again (already approved) is a no-op, not an error.
	if ok, err := st.ApproveDraft(id, "", 200); err != nil || ok {
		t.Fatalf("re-approving: ok=%v err=%v, want ok=false", ok, err)
	}
}

// TestApproveDraftTextOverride covers the optional edit-before-send.
func TestApproveDraftTextOverride(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	jid := "56999999999@s.whatsapp.net"
	if err := st.AddDraft(jid, "borrador original", "deepseek-chat", 1); err != nil {
		t.Fatal(err)
	}
	drafts, _ := st.PendingDrafts(10)
	id := drafts[0].ID

	if ok, err := st.ApproveDraft(id, "texto editado por el dueño", 100); err != nil || !ok {
		t.Fatalf("ApproveDraft with override: ok=%v err=%v", ok, err)
	}
	outbox, err := st.PendingOutbox(10)
	if err != nil || len(outbox) != 1 || outbox[0].Text != "texto editado por el dueño" {
		t.Fatalf("outbox = %+v err=%v, want the overridden text", outbox, err)
	}
}

// TestDiscardDraft covers the DoD: discarding never sends.
func TestDiscardDraft(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	jid := "56999999999@s.whatsapp.net"
	if err := st.AddDraft(jid, "no enviar esto", "deepseek-chat", 1); err != nil {
		t.Fatal(err)
	}
	drafts, _ := st.PendingDrafts(10)
	id := drafts[0].ID

	ok, err := st.DiscardDraft(id)
	if err != nil || !ok {
		t.Fatalf("DiscardDraft: ok=%v err=%v", ok, err)
	}

	outbox, err := st.PendingOutbox(10)
	if err != nil || len(outbox) != 0 {
		t.Fatalf("outbox after discard = %+v err=%v, want empty — discarded drafts are never sent", outbox, err)
	}
	if pending, err := st.PendingDrafts(10); err != nil || len(pending) != 0 {
		t.Fatalf("discarded draft should no longer be pending, got %+v err=%v", pending, err)
	}

	// Discarding something that doesn't exist / isn't pending: ok=false, no error.
	if ok, err := st.DiscardDraft(id); err != nil || ok {
		t.Fatalf("re-discarding: ok=%v err=%v, want ok=false", ok, err)
	}
	if ok, err := st.DiscardDraft(99999); err != nil || ok {
		t.Fatalf("discarding nonexistent id: ok=%v err=%v, want ok=false", ok, err)
	}
}

// TestAddDraftWithConfirmerPersists covers the DoD's confirmer half (0748):
// AddDraftWithConfirmer's confirmer round-trips through PendingDrafts, and
// plain AddDraft still defaults to empty (unaffected callers).
func TestAddDraftWithConfirmerPersists(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	jid := "56999999999@s.whatsapp.net"
	if err := st.AddDraftWithConfirmer(jid, "reviso el stock y te confirmo", "deepseek-chat", "56911112222@s.whatsapp.net", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.AddDraft(jid, "sin confirmador", "deepseek-chat", 2); err != nil {
		t.Fatal(err)
	}

	drafts, err := st.PendingDrafts(10)
	if err != nil || len(drafts) != 2 {
		t.Fatalf("drafts=%+v err=%v", drafts, err)
	}
	if drafts[0].Confirmer != "56911112222@s.whatsapp.net" {
		t.Errorf("drafts[0].Confirmer = %q, want the resolved confirmer", drafts[0].Confirmer)
	}
	if drafts[1].Confirmer != "" {
		t.Errorf("drafts[1].Confirmer = %q, want empty (plain AddDraft)", drafts[1].Confirmer)
	}
}

// TestSettingBoolRoundTrip covers the DoD's KV round-trip (0753): unset
// falls back to def, Set persists, and a bogus stored value still falls
// back to def rather than erroring.
func TestSettingBoolRoundTrip(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if got := st.SettingBool("media_skip_video_group", false); got != false {
		t.Errorf("unset default = %v, want false", got)
	}
	if err := st.SetSettingBool("media_skip_video_group", true); err != nil {
		t.Fatal(err)
	}
	if got := st.SettingBool("media_skip_video_group", false); got != true {
		t.Errorf("after Set = %v, want true", got)
	}
}

// TestSettingDurationRoundTrip covers the same contract for durations.
func TestSettingDurationRoundTrip(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	def := 3 * time.Second
	if got := st.SettingDuration("delay_dispatch_min", def); got != def {
		t.Errorf("unset default = %v, want %v", got, def)
	}
	if err := st.SetSettingDuration("delay_dispatch_min", 9*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := st.SettingDuration("delay_dispatch_min", def); got != 9*time.Second {
		t.Errorf("after Set = %v, want 9s", got)
	}

	// A bogus stored value (not a valid Go duration) falls back to def
	// rather than erroring — callers on a message-dispatch path can't
	// afford to handle this specially.
	if err := st.KVSet("delay_dispatch_min", "not-a-duration"); err != nil {
		t.Fatal(err)
	}
	if got := st.SettingDuration("delay_dispatch_min", def); got != def {
		t.Errorf("bogus stored value = %v, want fallback to def %v", got, def)
	}
}

// TestSettingIntRoundTrip covers the same contract for integer counts
// (rate limits, media retention MB).
func TestSettingIntRoundTrip(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if got := st.SettingInt("rate_limit_per_min", 10); got != 10 {
		t.Errorf("unset default = %d, want 10", got)
	}
	if err := st.SetSettingInt("rate_limit_per_min", 20); err != nil {
		t.Fatal(err)
	}
	if got := st.SettingInt("rate_limit_per_min", 10); got != 20 {
		t.Errorf("after Set = %d, want 20", got)
	}
}

// TestCountOutboundSince covers the DoD directly (0753): the governor's
// daily-cap restart reconstruction counts only OUTBOUND messages at or
// after the given timestamp — inbound and earlier-than-cutoff messages
// don't count.
func TestCountOutboundSince(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	jid := "56999999999@s.whatsapp.net"
	if err := st.AddMessage(Message{ChatJID: jid, ID: "before", FromMe: true, TS: 100}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(Message{ChatJID: jid, ID: "after1", FromMe: true, TS: 200}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(Message{ChatJID: jid, ID: "after2", FromMe: true, TS: 300}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(Message{ChatJID: jid, ID: "inbound", FromMe: false, TS: 250}); err != nil {
		t.Fatal(err)
	}

	n, err := st.CountOutboundSince(200)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("CountOutboundSince(200) = %d, want 2 (the two outbound at/after ts=200, not the earlier one or the inbound)", n)
	}
}

// TestEffectiveRulesHierarchy covers the DoD directly (1959): particular
// beats type beats default beats "" — "no rules → no acting"
// stays intact because type/default ARE rules, just applied in bulk.
func TestEffectiveRulesHierarchy(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	oneOnOne := "56999999999@s.whatsapp.net"
	group := "12345-67890@g.us"
	if err := st.TouchChat(oneOnOne, "Contact", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat(group, "Group", 1); err != nil {
		t.Fatal(err)
	}

	// Nothing set anywhere: "" (the law holds).
	if got, err := st.EffectiveRules(oneOnOne); err != nil || got != "" {
		t.Fatalf("no rules anywhere: got %q err=%v, want empty", got, err)
	}

	// Default set: both chat types fall back to it.
	if err := st.SetDefaultRules("default: sé amable"); err != nil {
		t.Fatal(err)
	}
	if got, err := st.EffectiveRules(oneOnOne); err != nil || got != "default: sé amable" {
		t.Errorf("1-1 with only default: got %q err=%v", got, err)
	}
	if got, err := st.EffectiveRules(group); err != nil || got != "default: sé amable" {
		t.Errorf("group with only default: got %q err=%v", got, err)
	}

	// Type rules set: beat default, resolved by isGroupJID.
	if err := st.SetTypeRules("individual", "tipo individual: formal"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTypeRules("group", "tipo grupo: solo si preguntan"); err != nil {
		t.Fatal(err)
	}
	if got, err := st.EffectiveRules(oneOnOne); err != nil || got != "tipo individual: formal" {
		t.Errorf("1-1 with type set: got %q err=%v, want the individual type rules", got, err)
	}
	if got, err := st.EffectiveRules(group); err != nil || got != "tipo grupo: solo si preguntan" {
		t.Errorf("group with type set: got %q err=%v, want the group type rules", got, err)
	}

	// Particular rules set: beats everything, for that one chat only.
	if err := st.SetChatRules(oneOnOne, "particular: este cliente es VIP"); err != nil {
		t.Fatal(err)
	}
	if got, err := st.EffectiveRules(oneOnOne); err != nil || got != "particular: este cliente es VIP" {
		t.Errorf("1-1 with particular set: got %q err=%v, want the particular rules", got, err)
	}
	// The group is untouched by the 1-1's particular rules — still its type tier.
	if got, err := st.EffectiveRules(group); err != nil || got != "tipo grupo: solo si preguntan" {
		t.Errorf("group after 1-1 got particular rules: got %q err=%v, want unchanged", got, err)
	}
}

// TestSetTypeRulesRejectsInvalidType covers the DoD's validation: chatType
// must be exactly "individual" or "group".
func TestSetTypeRulesRejectsInvalidType(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.SetTypeRules("banana", "whatever"); err == nil {
		t.Error("SetTypeRules with an invalid type should error, got nil")
	}
	if err := st.SetTypeRules("individual", "ok"); err != nil {
		t.Errorf("SetTypeRules(individual, ...) should succeed: %v", err)
	}
	if err := st.SetTypeRules("group", "ok"); err != nil {
		t.Errorf("SetTypeRules(group, ...) should succeed: %v", err)
	}
}
