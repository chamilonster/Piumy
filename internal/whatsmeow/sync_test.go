package whatsmeow

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"

	"piumy-gateway/internal/store"
)

// TestBackfillContactsSetsAgendaNameAndSkipsEmpty covers ct-2026-07-19-0115's
// core rule: chats.name gets PushName (the contact's own self-chosen name,
// via TouchChat), chats.contact_name gets the PHONE AGENDA name
// (FullName, falling back to FirstName) — never PushName — and an empty
// agenda name is never written (SetContactName not called at all), so a
// name already known from an earlier sweep can't be erased.
func TestBackfillContactsSetsAgendaNameAndSkipsEmpty(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	a := &Adapter{store: st, actionDelayMin: time.Millisecond, actionDelayMax: 2 * time.Millisecond}

	withFullName := types.NewJID("111", "s.whatsapp.net")
	withFirstNameOnly := types.NewJID("222", "s.whatsapp.net")
	withNoAgendaName := types.NewJID("333", "s.whatsapp.net")

	contacts := map[types.JID]types.ContactInfo{
		withFullName:      {Found: true, FullName: "Juan Perez", FirstName: "Juan", PushName: "Juanito"},
		withFirstNameOnly: {Found: true, FirstName: "Maria", PushName: "Mari"},
		withNoAgendaName:  {Found: true, PushName: "SoloPushName"},
	}

	a.backfillContacts(context.Background(), contacts)

	c1, ok, err := st.GetChat(withFullName.String())
	if err != nil || !ok {
		t.Fatalf("GetChat(withFullName) = ok=%v err=%v", ok, err)
	}
	if c1.Name != "Juanito" {
		t.Errorf("c1.Name = %q, want the PushName \"Juanito\"", c1.Name)
	}
	if c1.ContactName != "Juan Perez" {
		t.Errorf("c1.ContactName = %q, want the agenda FullName \"Juan Perez\"", c1.ContactName)
	}

	c2, ok, err := st.GetChat(withFirstNameOnly.String())
	if err != nil || !ok {
		t.Fatalf("GetChat(withFirstNameOnly) = ok=%v err=%v", ok, err)
	}
	if c2.ContactName != "Maria" {
		t.Errorf("c2.ContactName = %q, want the FirstName fallback \"Maria\"", c2.ContactName)
	}

	c3, ok, err := st.GetChat(withNoAgendaName.String())
	if err != nil || !ok {
		t.Fatalf("GetChat(withNoAgendaName) = ok=%v err=%v", ok, err)
	}
	if c3.Name != "SoloPushName" {
		t.Errorf("c3.Name = %q, want the PushName \"SoloPushName\"", c3.Name)
	}
	if c3.ContactName != "" {
		t.Errorf("c3.ContactName = %q, want empty — no agenda name known, must not be written", c3.ContactName)
	}

	// A later re-sweep that finds no agenda name for a contact that DID
	// have one must not erase it (SetContactName is only ever called with
	// a non-empty name — the guard lives in backfillContacts itself).
	a.backfillContacts(context.Background(), map[types.JID]types.ContactInfo{
		withFullName: {Found: true, PushName: "Juanito"}, // FullName/FirstName now blank
	})
	c1Again, _, err := st.GetChat(withFullName.String())
	if err != nil {
		t.Fatal(err)
	}
	if c1Again.ContactName != "Juan Perez" {
		t.Errorf("after re-sweep with blank agenda name, ContactName = %q, want it to keep \"Juan Perez\"", c1Again.ContactName)
	}
}

// TestBackfillContactsPacesWithActionDelay verifies the anti-ban core of
// the sub: each contact incurs a real actionDelay().Sleep BEFORE its store
// write — not a batched/instant loop. A small-but-measurable window keeps
// the test fast while still proving pacing actually happens.
func TestBackfillContactsPacesWithActionDelay(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	const delay = 20 * time.Millisecond
	a := &Adapter{store: st, actionDelayMin: delay, actionDelayMax: delay}

	contacts := map[types.JID]types.ContactInfo{
		types.NewJID("1", "s.whatsapp.net"): {Found: true, PushName: "A"},
		types.NewJID("2", "s.whatsapp.net"): {Found: true, PushName: "B"},
		types.NewJID("3", "s.whatsapp.net"): {Found: true, PushName: "C"},
	}

	start := time.Now()
	a.backfillContacts(context.Background(), contacts)
	elapsed := time.Since(start)

	// 3 contacts * 20ms each, minus scheduler-jitter tolerance.
	if want := 2 * delay; elapsed < want {
		t.Errorf("backfillContacts(3 contacts) took %v, want at least %v (each contact must sleep actionDelay)", elapsed, want)
	}
}

// TestBackfillContactsRespectsCancellation verifies ct-2026-07-19-0115's
// "respeta ... cancelacion": an already-cancelled ctx stops the loop before
// touching the store at all — no partial/racy writes on shutdown.
func TestBackfillContactsRespectsCancellation(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	a := &Adapter{store: st, actionDelayMin: time.Hour, actionDelayMax: time.Hour} // would hang the test if cancellation were ignored

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	jid := types.NewJID("111", "s.whatsapp.net")
	a.backfillContacts(ctx, map[types.JID]types.ContactInfo{
		jid: {Found: true, FullName: "Juan Perez", PushName: "Juanito"},
	})

	if _, ok, err := st.GetChat(jid.String()); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Error("backfillContacts wrote a chat despite an already-cancelled ctx")
	}
}

// TestSyncContactsNilStoreIsNoOp confirms the nil-safe convention (same as
// seedGroups): without a store, syncContacts must return before ever
// touching a.client — safe to call even with neither wired, same as every
// other optional-dependency guard in this package.
func TestSyncContactsNilStoreIsNoOp(t *testing.T) {
	a := &Adapter{}
	a.syncContacts(context.Background())
}

// TestScheduleContactsSyncNilStoreIsNoOp mirrors TestSyncContactsNilStoreIsNoOp
// for the debounced entry point — must not panic, must not arm a timer.
func TestScheduleContactsSyncNilStoreIsNoOp(t *testing.T) {
	a := &Adapter{}
	a.scheduleContactsSync()
	if a.contactsSyncTimer != nil {
		t.Error("scheduleContactsSync armed a timer despite a nil store")
	}
}

// TestScheduleContactsSyncDebouncesBurstAndPicksUpData is the fix for
// ct-2026-07-31 ("no llegan contactos en una instalación nueva"), verified
// offline (no live WhatsApp account — Citrino's explicit condition):
// reproduces the real race with a genuine *whatsmeow.Client backed by an
// in-memory sqlite Store.Contacts, exactly like production, just without a
// live connection. 3 calls to scheduleContactsSync ~10ms apart simulate a
// burst (multiple HistorySync/AppStateSyncComplete signals landing close
// together, as history.go's own doc records really happening — "4 chunks in
// ~7s"). A push name is written directly into Store.Contacts mid-burst
// (whatsmeow itself does this BEFORE dispatching the event we react to —
// see history.go's doc on handleHistoricalPushNames) to prove the eventual
// single syncContacts run picks up data that arrived DURING the debounce
// window, not just at schedule time.
func TestScheduleContactsSyncDebouncesBurstAndPicksUpData(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	client := newTestWmeowClient(t)
	a := &Adapter{
		store:                st,
		client:               client,
		actionDelayMin:       time.Millisecond,
		actionDelayMax:       2 * time.Millisecond,
		contactsSyncDebounce: 30 * time.Millisecond,
	}

	jid := types.NewJID("111", "s.whatsapp.net")

	a.scheduleContactsSync()
	time.Sleep(10 * time.Millisecond)
	a.scheduleContactsSync()
	time.Sleep(10 * time.Millisecond)
	// The data "arrives" mid-burst, same order production sees: whatsmeow
	// writes Store.Contacts, THEN the signal we react to fires.
	if _, _, err := client.Store.Contacts.PutPushName(context.Background(), jid, "Recien Llegado"); err != nil {
		t.Fatal(err)
	}
	a.scheduleContactsSync() // re-arms again — the burst isn't over yet

	// Still well inside the (30ms) window from this last call — must not
	// have synced yet. Proves the burst coalesced instead of firing on the
	// first or second call.
	if _, ok, err := st.GetChat(jid.String()); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("syncContacts ran before the debounce window from the LAST call elapsed")
	}

	time.Sleep(80 * time.Millisecond) // past the 30ms window from the last call

	chat, ok, err := st.GetChat(jid.String())
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("syncContacts never ran after the debounce window — the burst should have coalesced into exactly one run")
	}
	if chat.Name != "Recien Llegado" {
		t.Errorf("chat.Name = %q, want the push name that arrived mid-burst", chat.Name)
	}
}
