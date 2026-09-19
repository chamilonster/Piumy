package store

import (
	"path/filepath"
	"testing"
)

// TestUpsertAndListGroupMembers covers the new group_members schema
// (ct-2026-07-19-0102, backup Sub 1) — schema-only sub-change, no
// population logic yet (Sub 2 backfills it), but the access methods Sub 2
// will use must round-trip correctly.
func TestUpsertAndListGroupMembers(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	group := "123@g.us"
	if err := s.UpsertGroupMember(group, "111@s.whatsapp.net", "Alice", 1000); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertGroupMember(group, "222@s.whatsapp.net", "", 2000); err != nil {
		t.Fatal(err)
	}

	members, err := s.ListGroupMembers(group)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("ListGroupMembers = %d, want 2", len(members))
	}
	if members[0].MemberJID != "111@s.whatsapp.net" || members[0].MemberName != "Alice" {
		t.Errorf("members[0] = %+v, want Alice/111@s.whatsapp.net", members[0])
	}
	if members[1].MemberJID != "222@s.whatsapp.net" || members[1].MemberName != "" {
		t.Errorf("members[1] = %+v, want empty name/222@s.whatsapp.net", members[1])
	}

	// A re-scrape that comes back with a blank name never erases a name
	// already known, but the timestamp still refreshes — which also moves
	// this member to the end of the added_ts ordering, so look it up by
	// JID rather than assuming its index.
	if err := s.UpsertGroupMember(group, "111@s.whatsapp.net", "", 3000); err != nil {
		t.Fatal(err)
	}
	members, err = s.ListGroupMembers(group)
	if err != nil {
		t.Fatal(err)
	}
	var alice *GroupMember
	for i := range members {
		if members[i].MemberJID == "111@s.whatsapp.net" {
			alice = &members[i]
		}
	}
	if alice == nil {
		t.Fatal("member 111@s.whatsapp.net vanished after re-scrape")
	}
	if alice.MemberName != "Alice" {
		t.Errorf("after re-scrape with blank name, MemberName = %q, want it to keep \"Alice\"", alice.MemberName)
	}
	if alice.AddedTS != 3000 {
		t.Errorf("after re-scrape, AddedTS = %d, want it refreshed to 3000", alice.AddedTS)
	}

	// An unrelated group is unaffected — group_members is keyed group_jid-first.
	other, err := s.ListGroupMembers("999@g.us")
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Errorf("ListGroupMembers(unrelated group) = %d, want 0", len(other))
	}
}

// ── T139 (ct-2026-09-03-1900) — RemoveGroupMember / ReconcileGroupMembers ──

// TestRemoveGroupMemberDeletesOnlyThatRow is DoD item 1's store half: a
// single removal takes out exactly the one (group, member) pair, leaving
// every other row — including that same member in a DIFFERENT group —
// untouched.
func TestRemoveGroupMemberDeletesOnlyThatRow(t *testing.T) {
	s := openTestStore(t)
	group := "555100000001@g.us"
	other := "555100000002@g.us"
	member := "555000000091@s.whatsapp.net"
	if err := s.UpsertGroupMember(group, member, "Uno", 1000); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertGroupMember(group, "555000000092@s.whatsapp.net", "Dos", 1000); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertGroupMember(other, member, "Uno", 1000); err != nil {
		t.Fatal(err)
	}

	if err := s.RemoveGroupMember(group, member); err != nil {
		t.Fatal(err)
	}

	members, err := s.ListGroupMembers(group)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].MemberJID != "555000000092@s.whatsapp.net" {
		t.Errorf("ListGroupMembers(%s) = %+v, want only 555000000092 left", group, members)
	}
	otherMembers, err := s.ListGroupMembers(other)
	if err != nil {
		t.Fatal(err)
	}
	if len(otherMembers) != 1 || otherMembers[0].MemberJID != member {
		t.Errorf("ListGroupMembers(%s) = %+v, want the same member untouched in the OTHER group", other, otherMembers)
	}
}

// TestRemoveGroupMemberDoesNotTouchChatOrMarks is DoD item 5: sacar la
// membresía nunca borra el chat de esa persona ni sus marcas de boss/
// approver — esos viven en `chats`, una tabla que RemoveGroupMember ni
// lee ni escribe.
func TestRemoveGroupMemberDoesNotTouchChatOrMarks(t *testing.T) {
	s := openTestStore(t)
	group := "555100000003@g.us"
	member := "555000000093@s.whatsapp.net"
	if err := s.TouchChat(member, "Miembro Importante", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetIsBoss(member, true); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertGroupMember(group, member, "Miembro Importante", 1000); err != nil {
		t.Fatal(err)
	}

	if err := s.RemoveGroupMember(group, member); err != nil {
		t.Fatal(err)
	}

	chat, ok, err := s.GetChat(member)
	if err != nil || !ok {
		t.Fatalf("GetChat(%s) after RemoveGroupMember: ok=%v err=%v, want the chat row untouched", member, ok, err)
	}
	if chat.Name != "Miembro Importante" || !chat.IsBoss {
		t.Errorf("chat = %+v after RemoveGroupMember, want Name/IsBoss unchanged", chat)
	}
}

// TestReconcileGroupMembersRemovesStaleMembers is DoD item 2: al
// reconectar, un grupo cuya lista real ya no incluye a alguien lo saca —
// el defecto de fondo del contrato (731 filas viejas en producción).
func TestReconcileGroupMembersRemovesStaleMembers(t *testing.T) {
	s := openTestStore(t)
	group := "555100000004@g.us"
	stayed := "555000000094@s.whatsapp.net"
	left := "555000000095@s.whatsapp.net"
	if err := s.UpsertGroupMember(group, stayed, "Se Queda", 1000); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertGroupMember(group, left, "Se Fue", 1000); err != nil {
		t.Fatal(err)
	}

	if err := s.ReconcileGroupMembers(group, []string{stayed}); err != nil {
		t.Fatal(err)
	}

	members, err := s.ListGroupMembers(group)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].MemberJID != stayed {
		t.Errorf("ListGroupMembers after reconcile = %+v, want only %q left", members, stayed)
	}
}

// TestReconcileGroupMembersEmptyListDoesNotPrune is DoD item 3 — el
// corazón del contrato: una lista vacía (el símbolo de un dato parcial o
// con error, per el contrato) NO poda nada, aunque tenga la oportunidad.
func TestReconcileGroupMembersEmptyListDoesNotPrune(t *testing.T) {
	s := openTestStore(t)
	group := "555100000005@g.us"
	member := "555000000096@s.whatsapp.net"
	if err := s.UpsertGroupMember(group, member, "Sigue Ahi", 1000); err != nil {
		t.Fatal(err)
	}

	if err := s.ReconcileGroupMembers(group, nil); err != nil {
		t.Fatal(err)
	}

	members, err := s.ListGroupMembers(group)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].MemberJID != member {
		t.Errorf("ListGroupMembers after reconcile with an EMPTY list = %+v, want the member untouched (empty list must never prune)", members)
	}
}

// TestReconcileGroupMembersDoesNotTouchOtherGroups is DoD item 4's store
// half: reconciling one group's membership never touches a DIFFERENT
// group's rows, even one with an overlapping member.
func TestReconcileGroupMembersDoesNotTouchOtherGroups(t *testing.T) {
	s := openTestStore(t)
	group := "555100000006@g.us"
	untouched := "555100000007@g.us"
	member := "555000000097@s.whatsapp.net"
	if err := s.UpsertGroupMember(group, member, "En Los Dos", 1000); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertGroupMember(untouched, member, "En Los Dos", 1000); err != nil {
		t.Fatal(err)
	}

	// Reconcile `group` with a list that does NOT include member — should
	// remove it from `group` only.
	if err := s.ReconcileGroupMembers(group, []string{"555000000098@s.whatsapp.net"}); err != nil {
		t.Fatal(err)
	}

	untouchedMembers, err := s.ListGroupMembers(untouched)
	if err != nil {
		t.Fatal(err)
	}
	if len(untouchedMembers) != 1 || untouchedMembers[0].MemberJID != member {
		t.Errorf("ListGroupMembers(%s) = %+v, want the OTHER group's membership untouched", untouched, untouchedMembers)
	}
}

// TestSetContactName covers chats.contact_name (ct-2026-07-19-0102) —
// distinct from Name (TouchChat's WhatsApp display name), round-trips
// through GetChat/scanChat.
func TestSetContactName(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	chat := "111@s.whatsapp.net"
	if err := s.TouchChat(chat, "WhatsApp Display Name", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetContactName(chat, "Juan Perez (agenda)"); err != nil {
		t.Fatal(err)
	}

	c, ok, err := s.GetChat(chat)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("GetChat: not found")
	}
	if c.Name != "WhatsApp Display Name" {
		t.Errorf("c.Name = %q, want unaffected by SetContactName", c.Name)
	}
	if c.ContactName != "Juan Perez (agenda)" {
		t.Errorf("c.ContactName = %q, want %q", c.ContactName, "Juan Perez (agenda)")
	}
}

// TestListAllGroupMembers covers the S1g addition (ct-2026-07-19-1801) —
// unlike ListGroupMembers, this returns every group's members in one query
// (the dashboard's collapsible-groups zone needs all of them at once).
func TestListAllGroupMembers(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.UpsertGroupMember("g1@g.us", "111@s.whatsapp.net", "Alice", 1000); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertGroupMember("g2@g.us", "222@s.whatsapp.net", "Bob", 2000); err != nil {
		t.Fatal(err)
	}

	all, err := s.ListAllGroupMembers()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("ListAllGroupMembers = %d, want 2", len(all))
	}
	byGroup := map[string]string{}
	for _, m := range all {
		byGroup[m.GroupJID] = m.MemberJID
	}
	if byGroup["g1@g.us"] != "111@s.whatsapp.net" || byGroup["g2@g.us"] != "222@s.whatsapp.net" {
		t.Errorf("ListAllGroupMembers = %+v, want both groups represented", all)
	}
}

// TestGroupsOf is T18B (ct-2026-08-05-1243) — the reverse lookup ("which
// groups is this number in") now reads group_members instead of the
// retired chat_groups. A member of two groups reports both; a number in
// no group reports empty, not an error.
func TestGroupsOf(t *testing.T) {
	s := openTestStore(t)

	member := "111@s.whatsapp.net"
	if err := s.UpsertGroupMember("g1@g.us", member, "Alice", 1000); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertGroupMember("g2@g.us", member, "Alice", 2000); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertGroupMember("g1@g.us", "222@s.whatsapp.net", "Bob", 1000); err != nil {
		t.Fatal(err)
	}

	groups, err := s.GroupsOf(member)
	if err != nil {
		t.Fatal(err)
	}
	byGroup := map[string]bool{}
	for _, g := range groups {
		byGroup[g] = true
	}
	if len(groups) != 2 || !byGroup["g1@g.us"] || !byGroup["g2@g.us"] {
		t.Errorf("GroupsOf(%s) = %v, want [g1@g.us g2@g.us]", member, groups)
	}

	none, err := s.GroupsOf("999@s.whatsapp.net")
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Errorf("GroupsOf(no groups) = %v, want empty", none)
	}
}
