package restapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"piumy-gateway/internal/governor"
	"piumy-gateway/internal/sessionbackup"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
)

// fakeLIDResolver is a tiny stand-in for whatsmeow.Adapter.ResolvePN — a
// real lookup needs a live client/session store, out of scope for a
// restapi-only test. A missing key resolves to "" (unresolved), same
// fallback ResolvePN itself returns.
type fakeLIDResolver map[string]string

func (f fakeLIDResolver) ResolvePN(_ context.Context, lidJID string) (string, error) {
	return f[lidJID], nil
}

func getJSON(t *testing.T, url string, out any) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("unmarshal %s: %v (body=%s)", url, err, body)
	}
	return resp
}

func TestStatusEndpoint(t *testing.T) {
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
	if err := sm.Update(func(s *state.Status) {
		s.OwnName = "Cuenta De Prueba"
		s.OwnJID = "55500000044@s.whatsapp.net"
		s.WAConnected = true
		s.Agents = 1
		s.Sent = 42
		s.Muted = false
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{State: sm}))
	defer srv.Close()

	var out struct {
		Name      string `json:"name"`
		OwnNumber string `json:"own_number"`
		Connected bool   `json:"connected"`
		Agents    int    `json:"agents"`
		Sent      int    `json:"sent"`
		Muted     bool   `json:"muted"`
	}
	resp := getJSON(t, srv.URL+"/api/status", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if out.Name != "Cuenta De Prueba" || out.OwnNumber != "55500000044@s.whatsapp.net" || !out.Connected || out.Agents != 1 || out.Sent != 42 || out.Muted {
		t.Errorf("GET /api/status = %+v, want it to reflect the state snapshot", out)
	}
}

// statusS1bFields is the S1b (ct-2026-07-19-1823) slice of GET /api/status —
// governor/backup/cifrado/antenna, real data for the badges S1a left off.
type statusS1bFields struct {
	AntennaConfigured         bool `json:"antenna_configured"`
	DefaultTerminalConfigured bool `json:"default_terminal_configured"`
	GovernorRatePerMin        int  `json:"governor_rate_per_min"`
	GovernorKilled            bool `json:"governor_killed"`
	BackupChats               int  `json:"backup_chats"`
	BackupGroups              int  `json:"backup_groups"`
	BackupContacts            int  `json:"backup_contacts"`
	BackupNumbers             int  `json:"backup_numbers"`
	BackupEncrypted           bool `json:"backup_encrypted"`
}

func TestStatusEndpointS1bFieldsNilSafeDefaults(t *testing.T) {
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
	srv := httptest.NewServer(NewMux(Deps{State: sm}))
	defer srv.Close()

	var out statusS1bFields
	resp := getJSON(t, srv.URL+"/api/status", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if out.AntennaConfigured || out.DefaultTerminalConfigured || out.GovernorRatePerMin != 0 || out.GovernorKilled || out.BackupChats != 0 || out.BackupGroups != 0 || out.BackupContacts != 0 || out.BackupNumbers != 0 || out.BackupEncrypted {
		t.Errorf("S1b fields with nothing wired = %+v, want all zero/false", out)
	}
}

// TestStatusEndpointDefaultTerminalConfigured is T25 (hallazgo 2,
// ct-2026-08-05-1833): the dashboard alarm for "neither the env var nor the
// antenna give a terminal to dispatch the owner's messages to" — main.go
// resolves the effective value into Deps.PrincipalTerminalID BEFORE this
// endpoint ever sees it (resolveDefaultTerminalID, main_test.go), so this
// only has to prove the endpoint reflects PrincipalTerminalID honestly.
func TestStatusEndpointDefaultTerminalConfigured(t *testing.T) {
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
	srv := httptest.NewServer(NewMux(Deps{State: sm, PrincipalTerminalID: "term-principal"}))
	defer srv.Close()

	var out statusS1bFields
	getJSON(t, srv.URL+"/api/status", &out)
	if !out.DefaultTerminalConfigured {
		t.Error("default_terminal_configured = false with PrincipalTerminalID set, want true")
	}
}

func TestStatusEndpointGovernorFields(t *testing.T) {
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
	gov := governor.NewLimiter(10, time.Minute)
	srv := httptest.NewServer(NewMux(Deps{State: sm, Governor: gov}))
	defer srv.Close()

	var out statusS1bFields
	getJSON(t, srv.URL+"/api/status", &out)
	if out.GovernorRatePerMin != 10 || out.GovernorKilled {
		t.Errorf("governor fields = %+v, want rate=10 killed=false", out)
	}

	gov.SetKill(true)
	getJSON(t, srv.URL+"/api/status", &out)
	if !out.GovernorKilled {
		t.Error("governor_killed = false after SetKill(true), want true")
	}
}

func TestStatusEndpointBackupCountsAndAntenna(t *testing.T) {
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
	st := newTestStore(t)
	if err := st.AddMessage(store.Message{ChatJID: "a@c.us", ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertGroupMember("g@g.us", "m@c.us", "M", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("a@c.us", "A", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetContactName("a@c.us", "Agenda A"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCAPIConnector("http://127.0.0.1:8787", "term-1", "pin"); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(NewMux(Deps{State: sm, Store: st}))
	defer srv.Close()

	var out statusS1bFields
	getJSON(t, srv.URL+"/api/status", &out)
	if out.BackupChats != 1 || out.BackupGroups != 0 || out.BackupContacts != 1 || out.BackupNumbers != 1 {
		t.Errorf("backup counts = %+v, want chats=1 groups=0 (g@g.us never got a chats row) contacts=1 numbers=1", out)
	}
	if !out.AntennaConfigured {
		t.Error("antenna_configured = false after SetCAPIConnector, want true")
	}
}

// TestStatusEndpointHistorySummary covers the "X de Y" chats-with-messages
// line (ct-2026-07-24-2004 — replaced the removed ON_DEMAND worker's
// history_state-based progress), store.HistorySummary verbatim.
func TestStatusEndpointHistorySummary(t *testing.T) {
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
	st := newTestStore(t)
	if err := st.TouchChat("a@c.us", "A", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("b@c.us", "B", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("c@c.us", "C", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: "a@c.us", ID: "m1", Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(NewMux(Deps{State: sm, Store: st}))
	defer srv.Close()

	var out struct {
		HistoryLoaded int `json:"history_loaded"`
		HistoryTotal  int `json:"history_total"`
	}
	getJSON(t, srv.URL+"/api/status", &out)
	if out.HistoryLoaded != 1 || out.HistoryTotal != 3 {
		t.Errorf("history summary = %+v, want loaded=1 total=3", out)
	}
}

func TestStatusEndpointBackupEncrypted(t *testing.T) {
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)

	unconfigured := httptest.NewServer(NewMux(Deps{State: sm, Backup: sessionbackup.New(sessionbackup.Config{})}))
	defer unconfigured.Close()
	var out statusS1bFields
	getJSON(t, unconfigured.URL+"/api/status", &out)
	if out.BackupEncrypted {
		t.Error("backup_encrypted = true with no PIUMY_BACKUP_KEY, want false")
	}

	configured := httptest.NewServer(NewMux(Deps{State: sm, Backup: sessionbackup.New(sessionbackup.Config{Key: "s3cr3t"})}))
	defer configured.Close()
	getJSON(t, configured.URL+"/api/status", &out)
	if !out.BackupEncrypted {
		t.Error("backup_encrypted = false with PIUMY_BACKUP_KEY set, want true")
	}
}

func TestChatsEndpoint(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetIsBoss("boss@c.us", true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatRules("boss@c.us", "sé breve"); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("boss@c.us", "El Boss", 1); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var out []struct {
		JID   string `json:"jid"`
		Name  string `json:"name"`
		Level string `json:"level"`
		Rules string `json:"rules"`
	}
	resp := getJSON(t, srv.URL+"/api/chats", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(out) != 1 || out[0].JID != "boss@c.us" || out[0].Level != "boss" || out[0].Rules != "sé breve" {
		t.Errorf("GET /api/chats = %+v, want one boss-level chat with its rules", out)
	}
}

// TestChatsEndpointExposesApproverPin (Aprobador P1, ct-2026-07-31-0610,
// "lo que tiene que quedar cierto" #4: "el pin se ve en la fila del
// chat") — the dashboard's pin toggle reads is_approver off this endpoint;
// this is the data contract it depends on, distinct from and unaffected by
// Level (a non-boss approver's Level is "approver", not "boss").
func TestChatsEndpointExposesApproverPin(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetIsApprover("secretaria@c.us", true); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("secretaria@c.us", "Secretaria", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("plain@c.us", "Nadie especial", 1); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var out []struct {
		JID        string `json:"jid"`
		Level      string `json:"level"`
		IsApprover bool   `json:"is_approver"`
	}
	getJSON(t, srv.URL+"/api/chats", &out)
	byJID := map[string]bool{}
	levelByJID := map[string]string{}
	for _, c := range out {
		byJID[c.JID] = c.IsApprover
		levelByJID[c.JID] = c.Level
	}
	if !byJID["secretaria@c.us"] {
		t.Error("is_approver = false for the pinned chat, want true")
	}
	if levelByJID["secretaria@c.us"] != "approver" {
		t.Errorf("level for the pinned (non-boss) chat = %q, want %q", levelByJID["secretaria@c.us"], "approver")
	}
	if byJID["plain@c.us"] {
		t.Error("is_approver = true for an unpinned chat, want false")
	}
}

// TestChatsEndpointClassifiesTypes is the S1g regression (ct-2026-07-19-1801),
// updated by S1g-fix (ct-2026-07-19-1905 — the boss only saw is_boss +
// groups, wants every non-group contact visible): a real 1:1 (has a
// message) is "p2p", an @g.us row is always "group", a group_members row
// becomes a synthetic "group_member" pseudo-entry, and a contact backfilled
// with zero messages (syncContacts/whitelist-add shape — TouchChat with no
// AddMessage ever) is ALSO "p2p" now — S1g's noise filter is gone.
func TestChatsEndpointClassifiesTypes(t *testing.T) {
	st := newTestStore(t)

	// Real 1:1 — has an actual message.
	if err := st.TouchChat("real1to1@s.whatsapp.net", "Ana", 100); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: "real1to1@s.whatsapp.net", ID: "m1", FromMe: false, Text: "hola", TS: 100}); err != nil {
		t.Fatal(err)
	}

	// A group, with 2 members, neither of whom ever 1:1-messaged.
	if err := st.TouchChat("group1@g.us", "Familia", 200); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertGroupMember("group1@g.us", "member1@s.whatsapp.net", "Beto", 200); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertGroupMember("group1@g.us", "member2@s.whatsapp.net", "Cami", 200); err != nil {
		t.Fatal(err)
	}

	// A contact backfilled by syncContacts (ts=0, never messaged, not
	// is_boss) — S1g would have dropped this entirely; S1g-fix shows it as
	// p2p (with a zero last_ts, sorted last by the frontend's default sort).
	if err := st.TouchChat("noactivity@s.whatsapp.net", "Desconocido", 0); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var out []chatOut
	resp := getJSON(t, srv.URL+"/api/chats", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	byJID := map[string]chatOut{}
	for _, c := range out {
		byJID[c.JID] = c
	}

	if c, ok := byJID["real1to1@s.whatsapp.net"]; !ok || c.Type != "p2p" {
		t.Errorf("real1to1 = %+v, ok=%v, want type=p2p", c, ok)
	}
	if c, ok := byJID["group1@g.us"]; !ok || c.Type != "group" {
		t.Errorf("group1 = %+v, ok=%v, want type=group", c, ok)
	}
	if c, ok := byJID["member1@s.whatsapp.net"]; !ok || c.Type != "group_member" || c.GroupJID != "group1@g.us" {
		t.Errorf("member1 = %+v, ok=%v, want type=group_member group_jid=group1@g.us", c, ok)
	}
	if c, ok := byJID["member2@s.whatsapp.net"]; !ok || c.Type != "group_member" || c.GroupJID != "group1@g.us" {
		t.Errorf("member2 = %+v, ok=%v, want type=group_member group_jid=group1@g.us", c, ok)
	}
	if c, ok := byJID["noactivity@s.whatsapp.net"]; !ok || c.Type != "p2p" {
		t.Errorf("noactivity = %+v, ok=%v, want type=p2p (S1g-fix: zero-message non-boss contacts show now)", c, ok)
	}
}

// TestChatsEndpointP2PWinsOverGroupMember covers the contract's explicit
// tie-break: "si un contacto es miembro de grupo Y ADEMÁS te escribió 1:1
// → va en Conversaciones 1:1 (gana el 1:1). No lo escondas bajo el grupo."
func TestChatsEndpointP2PWinsOverGroupMember(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("group1@g.us", "Familia", 200); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertGroupMember("group1@g.us", "both@s.whatsapp.net", "Dana", 200); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("both@s.whatsapp.net", "Dana", 300); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: "both@s.whatsapp.net", ID: "m1", FromMe: false, Text: "hola", TS: 300}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var out []chatOut
	resp := getJSON(t, srv.URL+"/api/chats", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var matches []chatOut
	for _, c := range out {
		if c.JID == "both@s.whatsapp.net" {
			matches = append(matches, c)
		}
	}
	if len(matches) != 1 || matches[0].Type != "p2p" {
		t.Errorf("both@s.whatsapp.net entries = %+v, want exactly one with type=p2p", matches)
	}
}

// TestChatsEndpointDedupesLIDAgainstRealNumberRow is the regression test
// for ct-2026-07-21-1809 (boss: "contactos duplicados con números que no
// corresponden"): the same person can have TWO independent chats rows — one
// keyed by their real @s.whatsapp.net, one by WhatsApp's internal @lid —
// both classify as "p2p" (IsGroupJID only excludes @g.us). Once the @lid
// resolves to a real-number row that already exists, only the real-number
// row must survive in the output.
func TestChatsEndpointDedupesLIDAgainstRealNumberRow(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("111@lid", "Dana (lid)", 100); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("222@s.whatsapp.net", "Dana", 200); err != nil {
		t.Fatal(err)
	}
	resolver := fakeLIDResolver{"111@lid": "222@s.whatsapp.net"}
	srv := httptest.NewServer(NewMux(Deps{Store: st, LIDResolver: resolver}))
	defer srv.Close()

	var out []chatOut
	resp := getJSON(t, srv.URL+"/api/chats", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var p2p []chatOut
	for _, c := range out {
		if c.Type == "p2p" {
			p2p = append(p2p, c)
		}
	}
	if len(p2p) != 1 || p2p[0].JID != "222@s.whatsapp.net" {
		t.Errorf("p2p rows = %+v, want exactly one: 222@s.whatsapp.net", p2p)
	}
}

// TestChatsEndpointDedupedLIDRowKeepsHasMessages is the regression test for
// ct-2026-07-29 (boss: "veo un solo chat de 408" — dashboard-preview1 fue el
// caso, medido: 405 de 408 chats con mensajes reales son @lid). The dedup
// above keeps only the real-number row for display, but WhatsApp
// overwhelmingly delivers messages into the @lid jid, not the real-number
// one — the winning row must NOT silently report has_messages=false just
// because ITS OWN jid never received a message.
func TestChatsEndpointDedupedLIDRowKeepsHasMessages(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("111@lid", "Dana (lid)", 100); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: "111@lid", ID: "m1", FromMe: false, Text: "hola", TS: 100}); err != nil {
		t.Fatal(err)
	}
	// Real-number row itself never got a message directly — only TouchChat
	// (e.g. a contacts backfill), same as the boss's real data.
	if err := st.TouchChat("222@s.whatsapp.net", "Dana", 50); err != nil {
		t.Fatal(err)
	}
	resolver := fakeLIDResolver{"111@lid": "222@s.whatsapp.net"}
	srv := httptest.NewServer(NewMux(Deps{Store: st, LIDResolver: resolver}))
	defer srv.Close()

	var out []chatOut
	resp := getJSON(t, srv.URL+"/api/chats", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var p2p []chatOut
	for _, c := range out {
		if c.Type == "p2p" {
			p2p = append(p2p, c)
		}
	}
	if len(p2p) != 1 || p2p[0].JID != "222@s.whatsapp.net" {
		t.Fatalf("p2p rows = %+v, want exactly one: 222@s.whatsapp.net", p2p)
	}
	if !p2p[0].HasMessages {
		t.Errorf("222@s.whatsapp.net has_messages = false, want true (its @lid sibling 111@lid holds the real conversation)")
	}
	if p2p[0].LastText != "hola" {
		t.Errorf("222@s.whatsapp.net last_text = %q, want %q (from the deduped @lid sibling)", p2p[0].LastText, "hola")
	}
}

// TestChatsEndpointShowsResolvedNumberForLIDOnlyRow covers the other half:
// when a person is ONLY known via @lid (no separate real-number row exists
// yet), the row must stay — but with ResolvedNumber set to the real number,
// so the frontend shows it instead of the @lid's raw digits (which read
// like a nonsense phone number).
func TestChatsEndpointShowsResolvedNumberForLIDOnlyRow(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("111@lid", "Dana", 100); err != nil {
		t.Fatal(err)
	}
	resolver := fakeLIDResolver{"111@lid": "222@s.whatsapp.net"}
	srv := httptest.NewServer(NewMux(Deps{Store: st, LIDResolver: resolver}))
	defer srv.Close()

	var out []chatOut
	resp := getJSON(t, srv.URL+"/api/chats", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(out) != 1 || out[0].JID != "111@lid" || out[0].ResolvedNumber != "222@s.whatsapp.net" {
		t.Errorf("GET /api/chats = %+v, want the @lid row kept with ResolvedNumber=222@s.whatsapp.net", out)
	}
}

// TestChatsEndpointGroupMemberLIDWinsOverP2P extends
// TestChatsEndpointP2PWinsOverGroupMember across the @lid/número boundary:
// "gana el 1:1" must also catch the case where the group_member row is keyed
// by @lid but the person's real 1:1 chat is keyed by the resolved number (or
// vice versa) — comparing raw JIDs alone misses this, since they never
// match textually.
func TestChatsEndpointGroupMemberLIDWinsOverP2P(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("group1@g.us", "Familia", 200); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertGroupMember("group1@g.us", "111@lid", "Dana", 200); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("222@s.whatsapp.net", "Dana", 300); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: "222@s.whatsapp.net", ID: "m1", FromMe: false, Text: "hola", TS: 300}); err != nil {
		t.Fatal(err)
	}
	resolver := fakeLIDResolver{"111@lid": "222@s.whatsapp.net"}
	srv := httptest.NewServer(NewMux(Deps{Store: st, LIDResolver: resolver}))
	defer srv.Close()

	var out []chatOut
	resp := getJSON(t, srv.URL+"/api/chats", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	for _, c := range out {
		if c.Type == "group_member" {
			t.Errorf("got a group_member row = %+v, want none — Dana already shows as p2p (222@s.whatsapp.net)", c)
		}
	}
}

// TestChatsEndpointHidesGroupMemberOnlyPhantomP2P is the regression test for
// ct-2026-07-29 (boss: "no mezclen contactos con miembros de grupos" — his
// real 1:1s are ~4, not 408). A group member whose ONLY trace in `chats` is
// a noise message (empty text/type — see realMessageSQL's doc) must never
// show as a "p2p" conversation nor leak into Contactos' "Números": they
// belong ONLY under their group (the group_member row).
func TestChatsEndpointHidesGroupMemberOnlyPhantomP2P(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("group1@g.us", "Familia", 200); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertGroupMember("group1@g.us", "111@lid", "Dana", 200); err != nil {
		t.Fatal(err)
	}
	// The ONLY reason 111@lid has a `chats` row at all: a noise message
	// (empty text/type, e.g. a receipt/reaction WhatsApp delivered for her
	// inside the group) — no real content, no agenda name.
	if err := st.AddMessage(store.Message{ChatJID: "111@lid", ID: "noise1", FromMe: true, Text: "", TS: 200}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var out []chatOut
	resp := getJSON(t, srv.URL+"/api/chats", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	for _, c := range out {
		if c.Type == "p2p" && c.JID == "111@lid" {
			t.Errorf("got 111@lid as p2p = %+v, want none — group membership alone isn't a conversation or a contact", c)
		}
	}
	var groupMember *chatOut
	for i := range out {
		if out[i].Type == "group_member" && out[i].JID == "111@lid" {
			groupMember = &out[i]
		}
	}
	if groupMember == nil {
		t.Fatal("111@lid never appears as group_member — Dana disappeared entirely instead of showing under her group")
	}
}

// TestChatsEndpointGroupMemberWithRealMessageStillShowsAsP2P is the sibling
// case: a group member who ALSO has a genuine 1:1 conversation (real text)
// must still show as a real p2p chat — the phantom-row exclusion is scoped
// to "no real signal at all", not "is a group member" alone.
func TestChatsEndpointGroupMemberWithRealMessageStillShowsAsP2P(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("group1@g.us", "Familia", 200); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertGroupMember("group1@g.us", "111@lid", "Dana", 200); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: "111@lid", ID: "m1", FromMe: false, Text: "hola", TS: 200}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var out []chatOut
	resp := getJSON(t, srv.URL+"/api/chats", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	for _, c := range out {
		if c.Type == "p2p" && c.JID == "111@lid" {
			if !c.HasMessages {
				t.Errorf("111@lid p2p has_messages = false, want true — she has a real message")
			}
			return
		}
	}
	t.Errorf("111@lid never appears as p2p, want it to (real conversation) — out=%+v", out)
}

// TestChatsEndpointGroupMemberNoDecisionStaysBare (T66b, ct-2026-08-11-1827)
// confirms the owner's own filter is untouched: a group-only contact with
// no agenda name and NO decision from the owner (never assigned, never
// pinned boss/approver) still shows as a bare group_member — same shape as
// before T66b. The person isn't hidden (that's TestChatsEndpointHidesGroupMemberOnlyPhantomP2P's
// job to confirm from the p2p side); the point here is Status/IsBoss stay
// at their zero value, because nothing was ever decided.
func TestChatsEndpointGroupMemberNoDecisionStaysBare(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("group1@g.us", "Familia", 200); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertGroupMember("group1@g.us", "111@c.us", "Dana", 200); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("111@c.us", "", 200); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var out []chatOut
	resp := getJSON(t, srv.URL+"/api/chats", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var member *chatOut
	for i := range out {
		if out[i].Type == "group_member" && out[i].JID == "111@c.us" {
			member = &out[i]
		}
	}
	if member == nil {
		t.Fatal("111@c.us never appears as group_member")
	}
	if member.Status != "" || member.IsBoss || member.IsApprover || member.Level != "" || member.Mode != "" {
		t.Errorf("group_member with no owner decision = %+v, want Status/IsBoss/IsApprover/Level/Mode all zero-value", member)
	}
}

// TestChatsEndpointGroupMemberAssignedToAgentShowsStatus is T66b's own
// defect (ct-2026-08-11-1827, Peridot R14/T66 + Citrino's own pliegue):
// the SAME group-only contact, once the owner assigns her to an agent
// (handleAssignChatToAgent's own write path, store.SetStatus with
// AgentExclusiveStatus — deliberately NOT going through TouchChat/
// MarkConfigManual, matching production exactly), must show that decision
// instead of looking like an unremarkable member. Status is the field the
// dashboard's own agentExclusiveID(status) JS helper reads to render the
// assignment — this is what makes it show there, no frontend change needed.
func TestChatsEndpointGroupMemberAssignedToAgentShowsStatus(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("group1@g.us", "Familia", 200); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertGroupMember("group1@g.us", "111@c.us", "Dana", 200); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("111@c.us", "", 200); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatus("111@c.us", store.AgentExclusiveStatus("agent-x")); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var out []chatOut
	resp := getJSON(t, srv.URL+"/api/chats", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var member *chatOut
	for i := range out {
		if out[i].Type == "group_member" && out[i].JID == "111@c.us" {
			member = &out[i]
		}
	}
	if member == nil {
		t.Fatal("111@c.us never appears as group_member")
	}
	if member.Status != "agent_exclusive:agent-x" {
		t.Errorf("assigned group_member Status = %q, want agent_exclusive:agent-x", member.Status)
	}
	if member.Level == "" {
		t.Error("assigned group_member Level = \"\", want a real dispatch level, not the zero value")
	}
	if member.Mode == "" {
		t.Error("assigned group_member Mode = \"\", want a real mode, not the zero value")
	}
}

// TestChatsEndpointGroupMemberBossPinShowsStatus covers the other two
// "tocado a mano" triggers the contract named — is_boss/is_approver — not
// just the agent-assignment path, so the carve-out doesn't accidentally
// only cover one of the three.
func TestChatsEndpointGroupMemberBossPinShowsStatus(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("group1@g.us", "Familia", 200); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertGroupMember("group1@g.us", "111@c.us", "Dana", 200); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("111@c.us", "", 200); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIsBoss("111@c.us", true); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var out []chatOut
	resp := getJSON(t, srv.URL+"/api/chats", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var member *chatOut
	for i := range out {
		if out[i].Type == "group_member" && out[i].JID == "111@c.us" {
			member = &out[i]
		}
	}
	if member == nil {
		t.Fatal("111@c.us never appears as group_member")
	}
	if !member.IsBoss {
		t.Errorf("group_member pinned is_boss = %+v, want IsBoss=true to travel", member)
	}
}

// TestChatsEndpointExcludesStatusBroadcast covers StatusBroadcastJID: never
// a real chat, even with a message that has a real media type.
func TestChatsEndpointExcludesStatusBroadcast(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat(store.StatusBroadcastJID, "Alguien viendo un estado", 100); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: store.StatusBroadcastJID, ID: "s1", FromMe: false, Type: "media", TS: 100}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var out []chatOut
	resp := getJSON(t, srv.URL+"/api/chats", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	for _, c := range out {
		if c.JID == store.StatusBroadcastJID {
			t.Errorf("got status@broadcast in /api/chats = %+v, want it excluded entirely", c)
		}
	}
}

// TestChatsEndpointContactNameAndLastText covers the S1-dashboard-3-tabs
// backend piece: contact_name (store.Chat.ContactName, backfilled from the
// phone's address book) and last_text (the chat's most recent message,
// reusing enrichChat's existing LastMessage call) both travel to
// GET /api/chats — previously dropped by chatOut.
func TestChatsEndpointContactNameAndLastText(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("a@s.whatsapp.net", "WA Display Name", 100); err != nil {
		t.Fatal(err)
	}
	if err := st.SetContactName("a@s.whatsapp.net", "Agenda Name"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: "a@s.whatsapp.net", ID: "m1", FromMe: false, Text: "hola qué tal", TS: 100}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var out []chatOut
	resp := getJSON(t, srv.URL+"/api/chats", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(out) != 1 || out[0].ContactName != "Agenda Name" || out[0].LastText != "hola qué tal" {
		t.Errorf("GET /api/chats = %+v, want contact_name=Agenda Name last_text='hola qué tal'", out)
	}
}

// TestChatsEndpointNameCarriesPushNameWithNoAgendaEntry is T17's Part 2
// verification (ct-2026-08-05-1240): "averiguá si [el push name] ya se está
// guardando; si se guarda, usalo cuando no haya nombre de agenda." It
// already was — corepipeline.handleInbound TouchChat(jid, msg.PushName, ts)
// on every inbound message (pipeline_test.go's own TestHandleInboundNewChat
// covers the store side) — this closes the loop at the API boundary GET
// /api/chats actually serves: with NO contact_name (never in the phone's
// address book / backup hasn't run), Name still carries WhatsApp's own push
// name, not "". The dashboard's own precedence
// (app.js#renderRow: c.contact_name || c.name || jidNumber(c.jid)) is
// unchanged code, already correct — this test is the missing API-level
// confirmation that Name is never silently dropped before reaching it.
func TestChatsEndpointNameCarriesPushNameWithNoAgendaEntry(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("b@s.whatsapp.net", "Su Nombre De WhatsApp", 100); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: "b@s.whatsapp.net", ID: "m1", FromMe: false, Text: "hola", TS: 100}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var out []chatOut
	resp := getJSON(t, srv.URL+"/api/chats", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(out) != 1 || out[0].Name != "Su Nombre De WhatsApp" || out[0].ContactName != "" {
		t.Errorf("GET /api/chats = %+v, want name='Su Nombre De WhatsApp' contact_name=''", out)
	}
}

// TestChatsEndpointGroupMemberNameCrossRef covers P4b (Tramo B,
// ct-2026-07-22-0436): a group_member row's Name comes from its own chats
// row (contact_name over name) when one is known for that member_jid,
// instead of the raw group-scrape member_name — which the contract notes is
// almost always empty ("grupos anónimos"). Falls back to the raw
// member_name when no chats row exists to cross-reference at all.
func TestChatsEndpointGroupMemberNameCrossRef(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("group1@g.us", "Familia", 200); err != nil {
		t.Fatal(err)
	}
	// Known as an agenda contact — group scrape got no name for them.
	if err := st.UpsertGroupMember("group1@g.us", "agenda@s.whatsapp.net", "", 200); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("agenda@s.whatsapp.net", "WA Name", 50); err != nil {
		t.Fatal(err)
	}
	if err := st.SetContactName("agenda@s.whatsapp.net", "Agenda Beto"); err != nil {
		t.Fatal(err)
	}
	// Known only by WhatsApp display name, not in the agenda.
	if err := st.UpsertGroupMember("group1@g.us", "waonly@s.whatsapp.net", "", 200); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("waonly@s.whatsapp.net", "WA Only", 50); err != nil {
		t.Fatal(err)
	}
	// Never seen outside this group — no chats row to cross-reference.
	if err := st.UpsertGroupMember("group1@g.us", "unknown@s.whatsapp.net", "Raw Scrape Name", 200); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var out []chatOut
	resp := getJSON(t, srv.URL+"/api/chats", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	members := map[string]chatOut{}
	for _, c := range out {
		if c.Type == "group_member" {
			members[c.JID] = c
		}
	}
	if m, ok := members["agenda@s.whatsapp.net"]; !ok || m.Name != "Agenda Beto" || !m.IsContact {
		t.Errorf("agenda member = %+v, ok=%v, want name=Agenda Beto is_contact=true", m, ok)
	}
	if m, ok := members["waonly@s.whatsapp.net"]; !ok || m.Name != "WA Only" || m.IsContact {
		t.Errorf("wa-only member = %+v, ok=%v, want name=WA Only is_contact=false", m, ok)
	}
	if m, ok := members["unknown@s.whatsapp.net"]; !ok || m.Name != "Raw Scrape Name" || m.IsContact {
		t.Errorf("unknown member = %+v, ok=%v, want name=Raw Scrape Name (kept, no chats row) is_contact=false", m, ok)
	}
}

// TestChatsEndpointIsContactFlag covers P5 (Tramo B, ct-2026-07-22-0436): a
// p2p/group row's IsContact is exactly contact_name != "" — the flag the
// Contactos tab uses to split "Contactos" from "Números".
func TestChatsEndpointIsContactFlag(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("contact@s.whatsapp.net", "WA Name", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetContactName("contact@s.whatsapp.net", "Agenda Name"); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("plain@s.whatsapp.net", "WA Only", 1); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var out []chatOut
	resp := getJSON(t, srv.URL+"/api/chats", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	byJID := map[string]chatOut{}
	for _, c := range out {
		byJID[c.JID] = c
	}
	if c, ok := byJID["contact@s.whatsapp.net"]; !ok || !c.IsContact {
		t.Errorf("contact = %+v, ok=%v, want is_contact=true", c, ok)
	}
	if c, ok := byJID["plain@s.whatsapp.net"]; !ok || c.IsContact {
		t.Errorf("plain = %+v, ok=%v, want is_contact=false", c, ok)
	}
}

// TestChatsEndpointRulesSource covers rules_source (ct-2026-07-31, boss:
// "las reglas por defecto no se ven") — every branch rulesSourceFor takes,
// matching store.EffectiveRules' own hierarchy (chat_test.go tests that one
// directly; this one tests the label travels correctly on GET /api/chats).
// T79 (ct-2026-08-27-2034) removed the "general" branch — a group with no
// type rule, or an individual with nothing at its own origin tier, now
// resolves to "" instead of falling further. Each of those still needs its
// OWN store: SetTypeRules/the origin KV values are global (not per-chat),
// so a chat that must NOT see a sibling's tier value has to live where
// that value was never written.
func TestChatsEndpointRulesSource(t *testing.T) {
	st := newTestStore(t)

	// Particular: has its own rules — wins regardless of any tier below.
	if err := st.TouchChat("own@s.whatsapp.net", "Own", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatRules("own@s.whatsapp.net", "mis propias reglas"); err != nil {
		t.Fatal(err)
	}

	// Group with a type-group rule set.
	if err := st.TouchChat("grouptyped@g.us", "Grupo con tipo", 2); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTypeRules("regla de grupos"); err != nil {
		t.Fatal(err)
	}

	// New (non-contact) individual — falls to the origin "nuevo" tier.
	if err := st.TouchChat("newnumber@s.whatsapp.net", "Nuevo", 4); err != nil {
		t.Fatal(err)
	}
	if err := st.KVSet(store.SettingRulesDefaultNewNumber, "regla de nuevos"); err != nil {
		t.Fatal(err)
	}

	// Contact individual — falls to the origin "contacto" tier (wins over
	// "nuevo" even though both KV values are set).
	if err := st.TouchChat("contact@s.whatsapp.net", "Contacto", 5); err != nil {
		t.Fatal(err)
	}
	if err := st.SetContactName("contact@s.whatsapp.net", "Agenda"); err != nil {
		t.Fatal(err)
	}
	if err := st.KVSet(store.SettingRulesDefaultContact, "regla de contactos"); err != nil {
		t.Fatal(err)
	}

	// Group with NO type-group rule — falls to "" (T79: no tier left below).
	// Own store: st's SetTypeRules call above is global, a group there
	// would always see it.
	st2 := newTestStore(t)
	if err := st2.TouchChat("groupbare@g.us", "Grupo sin tipo", 6); err != nil {
		t.Fatal(err)
	}

	// Individual with nothing at ANY level — own store, same reasoning.
	st3 := newTestStore(t)
	if err := st3.TouchChat("nada@s.whatsapp.net", "Nada", 7); err != nil {
		t.Fatal(err)
	}

	// checkSource fetches GET /api/chats from st and asserts jid's
	// rules_source — shared across the 3 stores this test needs.
	checkSource := func(st *store.Store, jid, want string) {
		t.Helper()
		srv := httptest.NewServer(NewMux(Deps{Store: st}))
		defer srv.Close()
		var out []chatOut
		resp := getJSON(t, srv.URL+"/api/chats", &out)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		for _, c := range out {
			if c.JID == jid {
				if c.RulesSource != want {
					t.Errorf("%s: rules_source = %q, want %q", jid, c.RulesSource, want)
				}
				return
			}
		}
		t.Fatalf("%s not found in GET /api/chats", jid)
	}

	checkSource(st, "own@s.whatsapp.net", "particular")
	checkSource(st, "grouptyped@g.us", "tipo:grupo")
	checkSource(st, "newnumber@s.whatsapp.net", "origen:nuevo")
	checkSource(st, "contact@s.whatsapp.net", "origen:contacto")
	checkSource(st2, "groupbare@g.us", "")
	checkSource(st3, "nada@s.whatsapp.net", "")
}

// TestChatsEndpointHasMessages covers the S1h-fix dashboard-filter backend
// piece: has_messages (store.ChatJIDsWithMessages — already computed in
// handleChats for the p2p/group_member dedup) now travels on every chatOut
// row, so the frontend's Chats tab can show ONLY real conversations (boss
// verbatim: "veo todos mis contactos en conversaciones") instead of the
// full address-book/group-member noise.
func TestChatsEndpointHasMessages(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("messaged@s.whatsapp.net", "Ana", 100); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: "messaged@s.whatsapp.net", ID: "m1", FromMe: false, Text: "hola", TS: 100}); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("noactivity@s.whatsapp.net", "Desconocido", 0); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var out []chatOut
	resp := getJSON(t, srv.URL+"/api/chats", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	byJID := map[string]chatOut{}
	for _, c := range out {
		byJID[c.JID] = c
	}
	if c, ok := byJID["messaged@s.whatsapp.net"]; !ok || !c.HasMessages {
		t.Errorf("messaged = %+v, ok=%v, want has_messages=true", c, ok)
	}
	if c, ok := byJID["noactivity@s.whatsapp.net"]; !ok || c.HasMessages {
		t.Errorf("noactivity = %+v, ok=%v, want has_messages=false", c, ok)
	}
}

// TestChatsEndpointExposesOrigin is T18 (ct-2026-08-05-1243): chatOut.Origin
// (store.ChatOrigin verbatim) now travels on GET /api/chats — previously
// computed on every ListChats call and silently discarded. A chat the boss
// started with no reply must read inbound_spoke (T18's own fix — see
// store.TestChatOriginRealMessageEitherDirection for the store-level
// coverage of that), not just a chat where the contact spoke first.
func TestChatsEndpointExposesOrigin(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("inbound@c.us", "Ana", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: "inbound@c.us", ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("boss-started@c.us", "Juan", 2); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: "boss-started@c.us", ID: "m2", FromMe: true, Text: "tenes stock?", TS: 2}); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("empty@c.us", "Nadie", 0); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var out []chatOut
	resp := getJSON(t, srv.URL+"/api/chats", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	byJID := map[string]chatOut{}
	for _, c := range out {
		byJID[c.JID] = c
	}
	if c, ok := byJID["inbound@c.us"]; !ok || c.Origin != "inbound_spoke" {
		t.Errorf("inbound@c.us origin = %+v, ok=%v, want inbound_spoke", c, ok)
	}
	if c, ok := byJID["boss-started@c.us"]; !ok || c.Origin != "inbound_spoke" {
		t.Errorf("boss-started@c.us origin = %+v, ok=%v, want inbound_spoke (T18: outbound-only still counts)", c, ok)
	}
	if c, ok := byJID["empty@c.us"]; !ok || c.Origin != "synced_contact" {
		t.Errorf("empty@c.us origin = %+v, ok=%v, want synced_contact", c, ok)
	}
}

// TestChatsEndpointConfigLevel covers config_level's unified 5-value
// projection (store.ConfigLevel) traveling on GET /api/chats — a boss chat
// reads "boss" regardless of its other fields, an ordinary fresh 1:1 chat
// (active=true, confirmation_mode=none) reads "auto".
func TestChatsEndpointConfigLevel(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetIsBoss("boss@c.us", true); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("boss@c.us", "El Boss", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("plain@s.whatsapp.net", "Plain", 2); err != nil {
		t.Fatal(err)
	}
	// TouchChat alone leaves active=false (anti-ban default, chat.go's own
	// doc) — config_level "auto" needs active=true too, so mark it active
	// explicitly (same as an owner running set_config_level("auto")).
	if err := st.SetActive("plain@s.whatsapp.net", true); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var out []chatOut
	resp := getJSON(t, srv.URL+"/api/chats", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	byJID := map[string]chatOut{}
	for _, c := range out {
		byJID[c.JID] = c
	}
	if c, ok := byJID["boss@c.us"]; !ok || c.ConfigLevel != "boss" {
		t.Errorf("boss@c.us = %+v, ok=%v, want config_level=boss", c, ok)
	}
	if c, ok := byJID["plain@s.whatsapp.net"]; !ok || c.ConfigLevel != "auto" {
		t.Errorf("plain@s.whatsapp.net = %+v, ok=%v, want config_level=auto", c, ok)
	}
}

func TestQREndpoint(t *testing.T) {
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
	if err := sm.Update(func(s *state.Status) {
		s.ShowQR = true
		s.QRData = "2@abc,def..."
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{State: sm}))
	defer srv.Close()

	var out struct {
		ShowQR bool   `json:"show_qr"`
		QRData string `json:"qr_data"`
	}
	resp := getJSON(t, srv.URL+"/api/qr", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !out.ShowQR || out.QRData != "2@abc,def..." {
		t.Errorf("GET /api/qr = %+v, want the pairing code from state", out)
	}
}

func TestMessagesEndpoint(t *testing.T) {
	st := newTestStore(t)
	if err := st.AddMessage(store.Message{ChatJID: "1@c.us", ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: "1@c.us", ID: "m2", FromMe: true, Text: "qué tal", TS: 2}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var out []struct {
		FromMe bool   `json:"from_me"`
		Text   string `json:"text"`
		TS     int64  `json:"ts"`
	}
	resp := getJSON(t, srv.URL+"/api/messages?chat=1@c.us", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(out) != 2 || out[0].Text != "hola" || out[1].Text != "qué tal" || out[1].FromMe != true {
		t.Errorf("GET /api/messages = %+v, want [hola(in), qué tal(out)] oldest-first", out)
	}
}

// TestMessagesEndpointMedia covers ct-2026-07-21-1358's popup backend A,
// tarea 2: a media message exposes {mime, kind, downloaded, url}, a plain
// text message exposes no "media" key at all, and the url only appears
// once the file is actually downloaded (media table row present).
func TestMessagesEndpointMedia(t *testing.T) {
	st := newTestStore(t)
	chat := "1@c.us"
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "m1", FromMe: false, Text: "hola", TS: 1, Type: "text"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "m2", FromMe: false, TS: 2, Type: "image/jpeg"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "m3", FromMe: false, TS: 3, Type: "audio/ogg"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMedia(store.Media{MsgID: "m3", ChatJID: chat, Path: "a.ogg", FullPath: "a.ogg", Mime: "audio/ogg", TS: 3}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var out []messageOut
	resp := getJSON(t, srv.URL+"/api/messages?chat="+chat, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(out) != 3 {
		t.Fatalf("len(out) = %d, want 3", len(out))
	}
	byID := map[string]messageOut{}
	for _, m := range out {
		byID[m.ID] = m
	}

	if m := byID["m1"]; m.Media != nil {
		t.Errorf("text message m1 = %+v, want Media=nil", m)
	}
	if m := byID["m2"]; m.Media == nil || m.Media.Kind != "photo" || m.Media.Downloaded || m.Media.URL != "" {
		t.Errorf("undownloaded photo m2 = %+v, want kind=photo downloaded=false url=empty", m.Media)
	}
	if m := byID["m3"]; m.Media == nil || m.Media.Kind != "audio" || !m.Media.Downloaded || m.Media.URL == "" {
		t.Errorf("downloaded audio m3 = %+v, want kind=audio downloaded=true url set", m.Media)
	}
	if m := byID["m3"]; m.Media != nil && m.Media.URL != "/api/media?chat=1%40c.us&msg=m3" {
		t.Errorf("m3.Media.URL = %q, want the query-escaped /api/media url", m.Media.URL)
	}
	if m := byID["m2"]; m.Media != nil && m.Media.Failed {
		t.Errorf("m2.Media.Failed = true, want false — still queued, nothing gave up on it")
	}
}

// TestMessagesEndpointMediaFailed is the regression test for ct-2026-07-29
// (boss: "los audios dicen descargando... un adjunto que falló con 403 no
// puede decir 'descargando' para siempre"): an attachment whose
// media_pending row exhausted store.MaxMediaPendingAttempts must expose
// failed=true (and downloaded/url stay false/empty) — distinct from a
// message with no media_pending row at all (still queued, might arrive).
func TestMessagesEndpointMediaFailed(t *testing.T) {
	st := newTestStore(t)
	chat := "1@c.us"
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "gaveup", FromMe: false, TS: 1, Type: "audio/ogg"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMediaPending(store.MediaPending{ChatJID: chat, MsgID: "gaveup", Mime: "audio/ogg", Kind: "audio", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.FailMediaPendingPermanently(chat, "gaveup"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "stilltrying", FromMe: false, TS: 2, Type: "image/jpeg"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMediaPending(store.MediaPending{ChatJID: chat, MsgID: "stilltrying", Mime: "image/jpeg", Kind: "photo", TS: 2}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var out []messageOut
	resp := getJSON(t, srv.URL+"/api/messages?chat="+chat, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	byID := map[string]messageOut{}
	for _, m := range out {
		byID[m.ID] = m
	}
	if m := byID["gaveup"]; m.Media == nil || !m.Media.Failed || m.Media.Downloaded || m.Media.URL != "" {
		t.Errorf("gaveup.Media = %+v, want failed=true downloaded=false url=empty", m.Media)
	}
	if m := byID["stilltrying"]; m.Media == nil || m.Media.Failed || m.Media.Downloaded {
		t.Errorf("stilltrying.Media = %+v, want failed=false downloaded=false (still queued)", m.Media)
	}
}

// TestMessagesEndpointMergesLIDSibling is the regression test for
// ct-2026-07-29: GET /api/chats' p2p dedup shows the real-number row and
// drops its @lid sibling from the output entirely — but WhatsApp very often
// stores the actual messages under that dropped @lid (measured on the real
// DB: 405 of 408). Opening the chat by its DISPLAYED (real-number) jid — the
// only jid the dashboard ever knows about — must still return the
// conversation, not an empty list.
func TestMessagesEndpointMergesLIDSibling(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("111@lid", "Dana (lid)", 100); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: "111@lid", ID: "m1", FromMe: false, Text: "hola", TS: 100}); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("222@s.whatsapp.net", "Dana", 50); err != nil {
		t.Fatal(err)
	}
	resolver := fakeLIDResolver{"111@lid": "222@s.whatsapp.net"}
	srv := httptest.NewServer(NewMux(Deps{Store: st, LIDResolver: resolver}))
	defer srv.Close()

	// The dashboard opens the chat with the DISPLAYED jid — the real
	// number (see TestChatsEndpointDedupesLIDAgainstRealNumberRow).
	var out []messageOut
	resp := getJSON(t, srv.URL+"/api/messages?chat=222@s.whatsapp.net", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(out) != 1 || out[0].Text != "hola" {
		t.Fatalf("GET /api/messages?chat=222@s.whatsapp.net = %+v, want [hola] (from the @lid sibling)", out)
	}
}

// TestMessagesEndpointMediaAcrossLIDSibling covers the same gap for media: a
// downloaded photo stored under the @lid sibling must still surface as
// downloaded=true with a /api/media URL pointing at the jid it's ACTUALLY
// stored under (111@lid) — GET /api/media looks the row up by exact
// (chat, msg), not the requested display jid (222@...).
func TestMessagesEndpointMediaAcrossLIDSibling(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("111@lid", "Dana (lid)", 100); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: "111@lid", ID: "m1", FromMe: false, TS: 100, Type: "image/jpeg"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMedia(store.Media{MsgID: "m1", ChatJID: "111@lid", Path: "a.jpg", FullPath: "a.jpg", Mime: "image/jpeg", TS: 100}); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("222@s.whatsapp.net", "Dana", 50); err != nil {
		t.Fatal(err)
	}
	resolver := fakeLIDResolver{"111@lid": "222@s.whatsapp.net"}
	srv := httptest.NewServer(NewMux(Deps{Store: st, LIDResolver: resolver}))
	defer srv.Close()

	var out []messageOut
	resp := getJSON(t, srv.URL+"/api/messages?chat=222@s.whatsapp.net", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(out) != 1 || out[0].Media == nil || !out[0].Media.Downloaded {
		t.Fatalf("GET /api/messages = %+v, want 1 downloaded media message", out)
	}
	wantURL := "/api/media?chat=111%40lid&msg=m1"
	if out[0].Media.URL != wantURL {
		t.Errorf("Media.URL = %q, want %q (the jid the message actually lives under)", out[0].Media.URL, wantURL)
	}
}

// TestMessagesEndpointReplyAndForwarded covers ct-2026-07-21-1610 (S6a
// backend): quoted_preview/forwarded must be exposed for a reply/forwarded
// message, and quoted_preview must be OMITTED (not just empty-string) for a
// plain message — the popup only needs the key when there's something to
// show.
func TestMessagesEndpointReplyAndForwarded(t *testing.T) {
	st := newTestStore(t)
	chat := "1@c.us"
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "m1", Text: "normal", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{
		ChatJID: chat, ID: "m2", Text: "respuesta", TS: 2,
		QuotedID: "Q1", QuotedPreview: "original", Forwarded: true,
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/messages?chat=" + chat)
	if err != nil {
		t.Fatal(err)
	}
	rawBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out []messageOut
	if err := json.Unmarshal(rawBody, &out); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, rawBody)
	}
	byID := map[string]messageOut{}
	for _, m := range out {
		byID[m.ID] = m
	}
	if m := byID["m2"]; m.QuotedPreview != "original" || !m.Forwarded {
		t.Errorf("m2 = %+v, want QuotedPreview=original Forwarded=true", m)
	}
	if m := byID["m1"]; m.QuotedPreview != "" || m.Forwarded {
		t.Errorf("m1 = %+v, want empty QuotedPreview and Forwarded=false", m)
	}
	if bytes.Contains(rawBody, []byte(`"quoted_preview":""`)) {
		t.Error(`response body has an explicit "quoted_preview":"" — want the key omitted for a plain message`)
	}
}

// TestMessagesEndpointResolvesGroupSenderName (D3, ct-2026-07-22-2100): a
// group message's sender resolves via the same hierarchy as GET /api/chats'
// group_member rows — contact_name > WhatsApp name > the group scrape's own
// member_name. A 1:1 chat or an outbound (from_me) message never gets
// sender/sender_name (omitempty, nothing to resolve).
func TestMessagesEndpointResolvesGroupSenderName(t *testing.T) {
	st := newTestStore(t)
	group := "12345@g.us"
	memberOnly := "1@s.whatsapp.net"    // only known via the group scrape
	memberContact := "2@s.whatsapp.net" // ALSO a real agenda contact

	if err := st.UpsertGroupMember(group, memberOnly, "Scrape Name", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertGroupMember(group, memberContact, "", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat(memberContact, "WhatsApp Name", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetContactName(memberContact, "Agenda Real"); err != nil {
		t.Fatal(err)
	}

	if err := st.AddMessage(store.Message{ChatJID: group, ID: "g1", Sender: memberOnly, Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: group, ID: "g2", Sender: memberContact, Text: "hola2", TS: 2}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: group, ID: "g3", FromMe: true, Sender: memberContact, Text: "nuestra", TS: 3}); err != nil {
		t.Fatal(err)
	}

	oneOnOne := "3@s.whatsapp.net"
	if err := st.AddMessage(store.Message{ChatJID: oneOnOne, ID: "p1", Sender: oneOnOne, Text: "hola p2p", TS: 1}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var groupOut []messageOut
	resp := getJSON(t, srv.URL+"/api/messages?chat="+group, &groupOut)
	defer resp.Body.Close()
	byID := map[string]messageOut{}
	for _, m := range groupOut {
		byID[m.ID] = m
	}
	if m := byID["g1"]; m.Sender != memberOnly || m.SenderName != "Scrape Name" {
		t.Errorf("g1 = %+v, want sender=%q sender_name=Scrape Name", m, memberOnly)
	}
	if m := byID["g2"]; m.Sender != memberContact || m.SenderName != "Agenda Real" {
		t.Errorf("g2 = %+v, want sender=%q sender_name=Agenda Real (contact_name wins)", m, memberContact)
	}
	if m := byID["g3"]; m.Sender != "" || m.SenderName != "" {
		t.Errorf("g3 (from_me) = %+v, want empty sender/sender_name", m)
	}

	var oneOnOneOut []messageOut
	resp2 := getJSON(t, srv.URL+"/api/messages?chat="+oneOnOne, &oneOnOneOut)
	defer resp2.Body.Close()
	if len(oneOnOneOut) != 1 || oneOnOneOut[0].Sender != "" || oneOnOneOut[0].SenderName != "" {
		t.Errorf("1:1 message = %+v, want empty sender/sender_name (not a group)", oneOnOneOut)
	}
}

func TestMessagesEndpointRequiresChat(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/messages")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status without chat= = %d, want 400", resp.StatusCode)
	}
}

// TestAgentsEndpoint (M1, ct-2026-07-22-1301): principal synthesized from
// KV capi-connector settings + PrincipalTerminalID, followed by every
// registered secondary — never the pinpass in clear.
func TestAgentsEndpoint(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetCAPIConnector("http://127.0.0.1:8787", "principal-antenna", "s3cr3t=="); err != nil {
		t.Fatal(err)
	}
	// SettingPrincipalName (ct-2026-07-29, agentes paso 1) — the principal's
	// own name, written via POST /api/admin/agent-update.
	if err := st.KVSet(store.SettingPrincipalName, "Boss"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAgent(store.Agent{
		AgentID: "secondary-term", Name: "Sonnet", Endpoint: "http://192.168.1.10:8787",
		AntennaTerminalID: "ant-term", Pinpass: "p1n==", Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st, PrincipalTerminalID: "principal-term"}))
	defer srv.Close()

	var out []agentOut
	resp := getJSON(t, srv.URL+"/api/agents", &out)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2 (principal + secondary): %+v", len(out), out)
	}
	principal, secondary := out[0], out[1]
	if principal.AgentID != "principal-term" || principal.Name != "Boss" || principal.Role != "principal" ||
		principal.Endpoint != "http://127.0.0.1:8787" || principal.AntennaTerminalID != "principal-antenna" || !principal.PinpassSet {
		t.Errorf("principal = %+v, want synthesized from KV capi-connector settings + principal_name", principal)
	}
	if secondary.AgentID != "secondary-term" || secondary.Name != "Sonnet" || secondary.Role != "secondary" || !secondary.PinpassSet {
		t.Errorf("secondary = %+v, want the upserted agent", secondary)
	}
}

func TestAgentsEndpointOmitsPrincipalWhenUnset(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var out []agentOut
	resp := getJSON(t, srv.URL+"/api/agents", &out)
	defer resp.Body.Close()
	if len(out) != 0 {
		t.Errorf("out = %+v, want empty (no PrincipalTerminalID, no agents)", out)
	}
}

// TestAgentChatsEndpoint (M3, ct-2026-07-22-1301): only chats assigned to
// agent_id come back.
func TestAgentChatsEndpoint(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("1@s.whatsapp.net", "Juan", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("2@s.whatsapp.net", "Otro", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatus("1@s.whatsapp.net", store.AgentExclusiveStatus("term-a")); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var out []agentChatOut
	resp := getJSON(t, srv.URL+"/api/agents/chats?agent_id=term-a", &out)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(out) != 1 || out[0].JID != "1@s.whatsapp.net" {
		t.Errorf("out = %+v, want exactly [1@s.whatsapp.net]", out)
	}
}

func TestAgentChatsEndpointRequiresAgentID(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/agents/chats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status without agent_id = %d, want 400", resp.StatusCode)
	}
}
