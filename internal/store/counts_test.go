package store

import "testing"

func TestBackupCounts(t *testing.T) {
	s := openTestStore(t)

	if err := s.AddMessage(Message{ChatJID: "a@c.us", ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: "a@c.us", ID: "m2", FromMe: true, Text: "chau", TS: 2}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertGroupMember("g@g.us", "m1@c.us", "M1", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertGroupMember("g@g.us", "m2@c.us", "M2", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchChat("a@c.us", "A", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetContactName("a@c.us", "Agenda A"); err != nil {
		t.Fatal(err)
	}
	// A chat with no agenda name scraped yet must not count as a contact.
	if err := s.TouchChat("b@c.us", "B", 1); err != nil {
		t.Fatal(err)
	}

	chats, groups, contacts, numbers, err := s.BackupCounts()
	if err != nil {
		t.Fatal(err)
	}
	if chats != 1 {
		t.Errorf("chats = %d, want 1 (only a@c.us has a message and isn't a group)", chats)
	}
	if groups != 0 {
		t.Errorf("groups = %d, want 0 (g@g.us never got its own chats row in this test)", groups)
	}
	if contacts != 1 {
		t.Errorf("contacts = %d, want 1 (only chats with a scraped agenda name)", contacts)
	}
	if numbers != 2 {
		t.Errorf("numbers = %d, want 2 (m1@c.us and m2@c.us are group members, neither is a contact)", numbers)
	}
}
