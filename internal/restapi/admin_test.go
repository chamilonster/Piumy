package restapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"piumy-gateway/internal/eventbus"
	"piumy-gateway/internal/governor"
	"piumy-gateway/internal/router"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestSetChatRulesEndpoint(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/chat-rules", map[string]any{"chat_id": "1@c.us", "rules": "sé amable"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	rules, err := st.EffectiveRules("1@c.us")
	if err != nil || rules != "sé amable" {
		t.Errorf("EffectiveRules = %q, err=%v, want %q", rules, err, "sé amable")
	}
}

// TestGetSetTypeRulesRoundTrip covers the GET side added in ct-2026-07-31
// ("las reglas por defecto no se ven") — type-rules was POST-only before,
// no way to read back what was saved. Group-only since T79
// (ct-2026-08-27-2034) removed the individual tier and chat_type with it.
func TestGetSetTypeRulesRoundTrip(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var before map[string]string
	getJSON(t, srv.URL+"/api/admin/type-rules", &before)
	if before["rules"] != "" {
		t.Errorf("rules before setting = %q, want empty", before["rules"])
	}

	resp := postJSON(t, srv.URL+"/api/admin/type-rules", map[string]any{"rules": "regla de grupos"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST status = %d, want 200", resp.StatusCode)
	}

	var after map[string]string
	getResp := getJSON(t, srv.URL+"/api/admin/type-rules", &after)
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", getResp.StatusCode)
	}
	if after["rules"] != "regla de grupos" {
		t.Errorf("rules after setting = %q, want %q", after["rules"], "regla de grupos")
	}
}

// TestGetSetIdentityRoundTrip (T13, ct-2026-08-05-123147) — same
// plain-string CRUD pattern as TestGetSetTypeRulesRoundTrip above.
func TestGetSetIdentityRoundTrip(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var before map[string]string
	getJSON(t, srv.URL+"/api/admin/identity", &before)
	if before["identity"] != "" {
		t.Errorf("identity before setting = %q, want empty", before["identity"])
	}

	resp := postJSON(t, srv.URL+"/api/admin/identity", map[string]any{"identity": "asistente de una veterinaria"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST status = %d, want 200", resp.StatusCode)
	}

	var after map[string]string
	getResp := getJSON(t, srv.URL+"/api/admin/identity", &after)
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", getResp.StatusCode)
	}
	if after["identity"] != "asistente de una veterinaria" {
		t.Errorf("identity after setting = %q, want %q", after["identity"], "asistente de una veterinaria")
	}
}

func TestSetContactNameEndpoint(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/contact-name", map[string]any{"chat_id": "1@c.us", "name": "Juan"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	c, _, err := st.GetChat("1@c.us")
	if err != nil || c.ContactName != "Juan" {
		t.Errorf("ContactName = %q, err=%v, want %q", c.ContactName, err, "Juan")
	}
}

func TestGetPendingDraftsEndpoint(t *testing.T) {
	st := newTestStore(t)
	if err := st.AddDraft("1@c.us", "hola, ¿cómo estás?", "test-model", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/admin/pending-drafts")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var drafts []store.Draft
	if err := json.NewDecoder(resp.Body).Decode(&drafts); err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 || drafts[0].ChatJID != "1@c.us" {
		t.Errorf("drafts = %+v, want one pending draft for 1@c.us", drafts)
	}
}

// ── M3 manual number assignment (ct-2026-07-22-1301) ────────────────────────

func TestAssignChatToAgentEndpoint(t *testing.T) {
	st := newTestStore(t)
	if err := st.UpsertAgent(store.Agent{AgentID: "term-a", Role: "secondary"}); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("1@s.whatsapp.net", "Juan", 1); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/agent-assign", map[string]any{"chat_id": "1@s.whatsapp.net", "agent_id": "term-a"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	c, _, err := st.GetChat("1@s.whatsapp.net")
	if err != nil || c.Status != "agent_exclusive:term-a" {
		t.Errorf("Status = %q, err=%v, want agent_exclusive:term-a", c.Status, err)
	}
}

func TestAssignChatToAgentEndpointUnassign(t *testing.T) {
	st := newTestStore(t)
	if err := st.UpsertAgent(store.Agent{AgentID: "term-a", Role: "secondary"}); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("1@s.whatsapp.net", "Juan", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatus("1@s.whatsapp.net", "agent_exclusive:term-a"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/agent-assign", map[string]any{"chat_id": "1@s.whatsapp.net", "agent_id": ""})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	c, _, err := st.GetChat("1@s.whatsapp.net")
	if err != nil || c.Status != "new" {
		t.Errorf("Status = %q, err=%v, want new", c.Status, err)
	}
}

// TestAssignChatToAgentEndpointAllowsPrincipal (T70, ct-2026-08-27-1404):
// exclusive assignment to the principal used to be rejected as redundant
// with the default-fallback — but it isn't the same thing, a chat assigned
// to the principal won't be taken by another agent. With only the principal
// registered, rejecting it left the dueño unable to assign anything.
func TestAssignChatToAgentEndpointAllowsPrincipal(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("1@s.whatsapp.net", "Juan", 1); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st, PrincipalTerminalID: "principal-term"}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/agent-assign", map[string]any{"chat_id": "1@s.whatsapp.net", "agent_id": "principal-term"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	c, _, err := st.GetChat("1@s.whatsapp.net")
	if err != nil || c.Status != "agent_exclusive:principal-term" {
		t.Errorf("Status = %q, err=%v, want agent_exclusive:principal-term", c.Status, err)
	}
}

func TestAssignChatToAgentEndpointRejectsUnknownAgent(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("1@s.whatsapp.net", "Juan", 1); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/agent-assign", map[string]any{"chat_id": "1@s.whatsapp.net", "agent_id": "nunca-registrado"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (agent_id desconocido)", resp.StatusCode)
	}
}

// ── Agentes paso 1: create/update/delete (ct-2026-07-29) ────────────────────

func TestCreateAgentEndpoint(t *testing.T) {
	st := newTestStore(t)
	var upserted []string
	srv := httptest.NewServer(NewMux(Deps{Store: st, OnAgentUpsert: func(agentID, endpoint, terminalID, pinpass string) {
		upserted = []string{agentID, endpoint, terminalID, pinpass}
	}}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/agent-create", map[string]any{
		"agent_id": "term-a", "name": "Vendedor", "endpoint": "http://192.168.1.10:8787",
		"antenna_terminal_id": "ant-guid", "pinpass": "s3cr3t==",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	a, ok, err := st.GetAgent("term-a")
	if err != nil || !ok {
		t.Fatalf("GetAgent: ok=%v err=%v", ok, err)
	}
	if a.Name != "Vendedor" || a.Endpoint != "http://192.168.1.10:8787" || a.AntennaTerminalID != "ant-guid" || a.Pinpass != "s3cr3t==" || a.Role != "secondary" {
		t.Errorf("GetAgent(term-a) = %+v, want the posted fields with role=secondary", a)
	}
	want := []string{"term-a", "http://192.168.1.10:8787", "ant-guid", "s3cr3t=="}
	if len(upserted) != 4 || upserted[0] != want[0] || upserted[1] != want[1] || upserted[2] != want[2] || upserted[3] != want[3] {
		t.Errorf("OnAgentUpsert args = %v, want %v — must hot-register the new injector", upserted, want)
	}
}

// TestCreateAgentEndpointDuplicateTakesOver is the regression test for T155
// (ct-2026-09-11-1613-t155, boss verbatim: "jamas que se vuelva a negar el
// registro por el puerto chocante... el problema es doble" — denying the
// alta leaves the live agent out AND the dead one in, eating dispatches).
// An agent-create with a colliding agent_id must take the id's place, not
// bounce with 400.
func TestCreateAgentEndpointDuplicateTakesOver(t *testing.T) {
	st := newTestStore(t)
	if err := st.UpsertAgent(store.Agent{AgentID: "term-a", Endpoint: "http://old", Role: "secondary"}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/agent-create", map[string]any{
		"agent_id": "term-a", "endpoint": "http://new", "antenna_terminal_id": "ant-guid", "pinpass": "x",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the one that arrives wins", resp.StatusCode)
	}
	var got map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["status"] != "updated" {
		t.Errorf("response status = %q, want %q — take-over, not a fresh alta", got["status"], "updated")
	}
	a, ok, _ := st.GetAgent("term-a")
	if !ok || a.Endpoint != "http://new" {
		t.Errorf("GetAgent(term-a).Endpoint = %q, ok=%v, want %q, ok=true — the newest registration must take the id's place", a.Endpoint, ok, "http://new")
	}
}

// TestCreateAgentEndpointPrincipalIDUpdatesPrincipal is the regression test
// for T155: an agent-create posted with the principal's own id must not be
// denied either — it routes through store.SetPrincipalAgent, the same path
// handleUpdateAgent's principal branch uses, and must never create a
// secondary row shadowing the principal (T88's ghost).
func TestCreateAgentEndpointPrincipalIDUpdatesPrincipal(t *testing.T) {
	st := newTestStore(t)
	conn := &mockConnector{}
	srv := httptest.NewServer(NewMux(Deps{Store: st, PrincipalTerminalID: "principal-term", Connector: conn}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/agent-create", map[string]any{
		"agent_id": "principal-term", "name": "Boss", "endpoint": "http://192.168.1.10:8787",
		"antenna_terminal_id": "ant-guid", "pinpass": "x",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the principal's id is never denied either", resp.StatusCode)
	}
	var got map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["status"] != "updated" || got["role"] != "principal" {
		t.Errorf("response = %v, want status=updated role=principal", got)
	}
	principal, ok, err := st.PrincipalAgent("principal-term")
	if err != nil || !ok || principal.Endpoint != "http://192.168.1.10:8787" {
		t.Errorf("PrincipalAgent(principal-term) = %+v, ok=%v, err=%v, want the posted endpoint", principal, ok, err)
	}
	if conn.lastEndpoint != "http://192.168.1.10:8787" {
		t.Errorf("Connector.SetConfig not called with the posted endpoint: %q", conn.lastEndpoint)
	}
	if _, ok, _ := st.GetAgent("principal-term"); ok {
		t.Error("must NOT create a secondary row for the principal's id — storage stays split")
	}
}

func TestCreateAgentEndpointRequiresFields(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/agent-create", map[string]any{"agent_id": "term-a"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (missing endpoint/antenna_terminal_id/pinpass)", resp.StatusCode)
	}
}

func TestUpdateAgentEndpointSecondaryPartialUpdate(t *testing.T) {
	st := newTestStore(t)
	if err := st.UpsertAgent(store.Agent{
		AgentID: "term-a", Name: "Viejo Nombre", Endpoint: "http://old",
		AntennaTerminalID: "ant-old", Pinpass: "pin-old", Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}
	var upserted []string
	srv := httptest.NewServer(NewMux(Deps{Store: st, OnAgentUpsert: func(agentID, endpoint, terminalID, pinpass string) {
		upserted = []string{agentID, endpoint, terminalID, pinpass}
	}}))
	defer srv.Close()

	// Only "name" posted — endpoint/antenna_terminal_id/pinpass omitted must
	// stay exactly as they were (set_agent_capi's own "omit to keep current").
	resp := postJSON(t, srv.URL+"/api/admin/agent-update", map[string]any{"agent_id": "term-a", "name": "Nuevo Nombre"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	a, _, err := st.GetAgent("term-a")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "Nuevo Nombre" {
		t.Errorf("Name = %q, want Nuevo Nombre", a.Name)
	}
	if a.Endpoint != "http://old" || a.AntennaTerminalID != "ant-old" || a.Pinpass != "pin-old" {
		t.Errorf("unposted fields changed: %+v, want endpoint/antenna_terminal_id/pinpass untouched", a)
	}
	if len(upserted) != 4 || upserted[1] != "http://old" {
		t.Errorf("OnAgentUpsert args = %v, want the merged (unchanged) endpoint http://old", upserted)
	}
}

func TestUpdateAgentEndpointUnknownSecondary(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/agent-update", map[string]any{"agent_id": "nunca-registrado", "name": "X"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (create it first)", resp.StatusCode)
	}
}

// TestUpdateAgentEndpointPrincipal covers the unified-API, no-storage-
// migration design (Citrino/boss, ct-2026-07-29): editing the principal
// through the SAME agent-update endpoint writes to the SAME KV keys/hot-
// reload path GET/POST /api/admin/capi-connector already use, plus the
// name — the one field the principal never had anywhere before.
func TestUpdateAgentEndpointPrincipal(t *testing.T) {
	st := newTestStore(t)
	conn := &mockConnector{}
	srv := httptest.NewServer(NewMux(Deps{Store: st, PrincipalTerminalID: "principal-term", Connector: conn}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/agent-update", map[string]any{
		"agent_id": "principal-term", "name": "Boss", "endpoint": "http://127.0.0.1:8787",
		"antenna_terminal_id": "ant-guid", "pinpass": "s3cr3t==",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ep, _ := st.KVGet(store.SettingCAPIEndpoint); ep != "http://127.0.0.1:8787" {
		t.Errorf("KV capi_endpoint = %q, want the posted endpoint — same KV /api/admin/capi-connector uses", ep)
	}
	if name, _ := st.KVGet(store.SettingPrincipalName); name != "Boss" {
		t.Errorf("KV principal_name = %q, want Boss", name)
	}
	if conn.lastEndpoint != "http://127.0.0.1:8787" || conn.lastTerminalID != "ant-guid" || conn.lastPinpass != "s3cr3t==" {
		t.Errorf("Connector.SetConfig not called with correct args: %q/%q/%q", conn.lastEndpoint, conn.lastTerminalID, conn.lastPinpass)
	}
	// Never creates an `agents` table row for the principal — API-shape
	// unification only, no storage migration.
	if _, ok, _ := st.GetAgent("principal-term"); ok {
		t.Error("the principal must NOT get a row in the agents table — storage stays split, only the REST endpoint is unified")
	}
}

// TestUpdateAgentEndpointPrincipalAllowsLANEndpoint is the regression test
// for ct-2026-07-29 (boss caught the first cut of this gate same day: it
// required literal 127.0.0.1, which breaks the product's actual target
// deploy — a Raspberry Pi with the principal agent on a different LAN
// machine). A private-network endpoint must be accepted through the same
// unified agent-update path the principal shares with secondaries.
func TestUpdateAgentEndpointPrincipalAllowsLANEndpoint(t *testing.T) {
	st := newTestStore(t)
	conn := &mockConnector{}
	srv := httptest.NewServer(NewMux(Deps{Store: st, PrincipalTerminalID: "principal-term", Connector: conn}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/agent-update", map[string]any{
		"agent_id": "principal-term", "endpoint": "http://192.168.1.10:8787",
		"antenna_terminal_id": "ant-guid", "pinpass": "s3cr3t==",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (LAN endpoint is allowed — Raspberry Pi deploy)", resp.StatusCode)
	}
	if ep, _ := st.KVGet(store.SettingCAPIEndpoint); ep != "http://192.168.1.10:8787" {
		t.Errorf("KV capi_endpoint = %q, want the posted LAN endpoint", ep)
	}
	if conn.lastEndpoint != "http://192.168.1.10:8787" {
		t.Errorf("Connector.SetConfig endpoint = %q, want the posted LAN endpoint", conn.lastEndpoint)
	}
}

// TestUpdateAgentEndpointPrincipalRejectsPublicEndpoint is the regression
// test for ct-2026-07-29 (agentes paso 3): the real invariant is "never a
// public address", not "always this machine" — only the dashboard's
// readonly input enforced anything before this, cosmetically. A direct API
// call with a public IP must be rejected (400), and must not touch KV or
// the live Connector.
func TestUpdateAgentEndpointPrincipalRejectsPublicEndpoint(t *testing.T) {
	st := newTestStore(t)
	conn := &mockConnector{}
	srv := httptest.NewServer(NewMux(Deps{Store: st, PrincipalTerminalID: "principal-term", Connector: conn}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/agent-update", map[string]any{
		"agent_id": "principal-term", "endpoint": "http://8.8.8.8:8787",
		"antenna_terminal_id": "ant-guid", "pinpass": "s3cr3t==",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (public endpoint)", resp.StatusCode)
	}
	if ep, _ := st.KVGet(store.SettingCAPIEndpoint); ep != "" {
		t.Errorf("KV capi_endpoint = %q, want untouched (empty)", ep)
	}
	if conn.lastEndpoint != "" {
		t.Errorf("Connector.SetConfig must not have been called, got endpoint=%q", conn.lastEndpoint)
	}
}

func TestDeleteAgentEndpoint(t *testing.T) {
	st := newTestStore(t)
	if err := st.UpsertAgent(store.Agent{AgentID: "term-a", Role: "secondary"}); err != nil {
		t.Fatal(err)
	}
	var deleted string
	srv := httptest.NewServer(NewMux(Deps{Store: st, OnAgentDelete: func(agentID string) { deleted = agentID }}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/agent-delete", map[string]any{"agent_id": "term-a"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if _, ok, _ := st.GetAgent("term-a"); ok {
		t.Error("agent must be gone from the store")
	}
	if deleted != "term-a" {
		t.Errorf("OnAgentDelete called with %q, want term-a — the live injector must be unregistered", deleted)
	}
}

// TestDeleteAgentEndpointAllowsPrincipal (T76, ct-2026-08-27-1752): the
// principal can now be deleted — boss verbatim, no gate to soften it: "a
// mí no me interesa [los problemas]... yo lo quiero, como yo lo digo".
// "Deleted" means ClearPrincipalAgent (the principal has no `agents` row —
// T70 already hit this) + resetting the live injector, not an error.
func TestDeleteAgentEndpointAllowsPrincipal(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetPrincipalAgent("Boss", "http://192.168.1.10:8787", "principal-term", "s3cr3t=="); err != nil {
		t.Fatal(err)
	}
	conn := &mockConnector{}
	srv := httptest.NewServer(NewMux(Deps{Store: st, PrincipalTerminalID: "principal-term", Connector: conn}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/agent-delete", map[string]any{"agent_id": "principal-term"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — deleting the principal must be allowed", resp.StatusCode)
	}
	got, ok, err := st.PrincipalAgent("principal-term")
	if err != nil || !ok || got.Endpoint != "" || got.Name != "" {
		t.Errorf("PrincipalAgent after delete = %+v, ok=%v, err=%v, want empty (fresh-install state)", got, ok, err)
	}
	if conn.lastEndpoint != "" || conn.lastTerminalID != "" || conn.lastPinpass != "" {
		t.Errorf("live injector after delete = endpoint=%q terminal=%q pinpass=%q, want all empty — the connector must be reset, not left with stale credentials", conn.lastEndpoint, conn.lastTerminalID, conn.lastPinpass)
	}
}

// TestDeleteAgentEndpointPrincipalWithNoOtherAgentsDoesNotExplode (T76):
// deleting the ONLY agent (the principal, nothing else registered) must
// not error or panic — the gateway just falls back to PortFallback with
// nothing configured, an accepted outcome per the contract, not a state to
// prevent.
func TestDeleteAgentEndpointPrincipalWithNoOtherAgentsDoesNotExplode(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetPrincipalAgent("Único", "http://192.168.1.10:8787", "principal-term", "s3cr3t=="); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st, PrincipalTerminalID: "principal-term"}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/agent-delete", map[string]any{"agent_id": "principal-term"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 even with zero agents left afterward", resp.StatusCode)
	}
	agents, err := st.ListAgents()
	if err != nil || len(agents) != 0 {
		t.Errorf("ListAgents after deleting the only agent = %v, err=%v, want empty", agents, err)
	}
}

// TestPromoteAgentEndpoint (T76, ct-2026-08-27-1752) — the dashboard side
// of "cambiar de rol": a secondary becomes principal, the old principal's
// data survives at the promoted agent's own vacated agent_id, and the live
// injectors get hot-reloaded (Connector.SetConfig for the new principal,
// OnAgentUpsert for the demoted one) — no restart needed.
func TestPromoteAgentEndpoint(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetPrincipalAgent("Vieja", "http://192.168.1.10:8787", "principal-term", "pin-old"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAgent(store.Agent{AgentID: "term-secundario", Name: "Nueva", Endpoint: "http://192.168.1.20:8787", AntennaTerminalID: "ant-new", Pinpass: "pin-new", Role: "secondary"}); err != nil {
		t.Fatal(err)
	}
	conn := &mockConnector{}
	var upsertedID, upsertedEndpoint string
	srv := httptest.NewServer(NewMux(Deps{
		Store: st, PrincipalTerminalID: "principal-term", Connector: conn,
		OnAgentUpsert: func(agentID, endpoint, terminalID, pinpass string) { upsertedID, upsertedEndpoint = agentID, endpoint },
	}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/agent-promote", map[string]any{"agent_id": "term-secundario"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	newPrincipal, ok, err := st.PrincipalAgent("principal-term")
	if err != nil || !ok || newPrincipal.Name != "Nueva" || newPrincipal.AntennaTerminalID != "ant-new" {
		t.Errorf("principal after promote = %+v, ok=%v, err=%v, want the promoted secondary's data", newPrincipal, ok, err)
	}
	demoted, ok, err := st.GetAgent("term-secundario")
	if err != nil || !ok || demoted.Name != "Vieja" || demoted.Endpoint != "http://192.168.1.10:8787" {
		t.Errorf("demoted agent = %+v, ok=%v, err=%v, want the old principal's data intact", demoted, ok, err)
	}
	if conn.lastEndpoint != "http://192.168.1.20:8787" || conn.lastTerminalID != "ant-new" {
		t.Errorf("live injector for the principal slot = endpoint=%q terminal=%q, want the promoted secondary's own credentials (hot-reload without restart)", conn.lastEndpoint, conn.lastTerminalID)
	}
	if upsertedID != "term-secundario" || upsertedEndpoint != "http://192.168.1.10:8787" {
		t.Errorf("OnAgentUpsert called with id=%q endpoint=%q, want term-secundario / the old principal's endpoint — the demoted agent needs its OWN live injector", upsertedID, upsertedEndpoint)
	}
}

func TestPromoteAgentEndpointRejectsUnknownAgent(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetPrincipalAgent("Vieja", "http://192.168.1.10:8787", "principal-term", "pin-old"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st, PrincipalTerminalID: "principal-term"}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/agent-promote", map[string]any{"agent_id": "nunca-registrado"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (agent_id desconocido)", resp.StatusCode)
	}
}

// TestPromoteAgentEndpointEmptyPrincipalLeavesNoGhost (T88,
// ct-2026-08-28-0626, boss: "hay 2 citrinos en opciones pero existe solo
// uno") — end-to-end through the HTTP handler, not just store.
// PromoteToPrincipal directly: promoting into an EMPTY principal slot must
// leave GET /api/agents with exactly ONE entry (the new principal), never
// two, and must never fire OnAgentUpsert for a row that was never written.
func TestPromoteAgentEndpointEmptyPrincipalLeavesNoGhost(t *testing.T) {
	st := newTestStore(t)
	// El principal arranca vacío a propósito — NO SetCAPIConnector, NO
	// SettingPrincipalName: el estado real que el dueño tenía (Selenita
	// borrada, nada configurado) cuando promovió a Citrino.
	if err := st.UpsertAgent(store.Agent{
		AgentID: "term-citrino", Name: "Citrino", Endpoint: "http://192.168.1.20:8787",
		AntennaTerminalID: "ant-citrino", Pinpass: "pin-citrino", Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}
	// Sin Connector: el hot-reload del principal (SetConfig) ya lo cubre
	// TestPromoteAgentEndpoint, es nil-safe acá — este test es sobre el
	// fantasma, no sobre eso.
	upsertCalled := false
	srv := httptest.NewServer(NewMux(Deps{
		Store: st, PrincipalTerminalID: "principal-term",
		OnAgentUpsert: func(agentID, endpoint, terminalID, pinpass string) { upsertCalled = true },
	}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/agent-promote", map[string]any{"agent_id": "term-citrino"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if upsertCalled {
		t.Error("OnAgentUpsert fired for the vacated slot, want it skipped — nothing was written there to register")
	}

	var out []agentOut
	getResp := getJSON(t, srv.URL+"/api/agents", &out)
	defer getResp.Body.Close()
	if len(out) != 1 {
		t.Fatalf("GET /api/agents after promoting an empty principal = %+v, want exactly 1 entry (no ghost)", out)
	}
	if out[0].AgentID != "principal-term" || out[0].Name != "Citrino" {
		t.Errorf("the one remaining entry = %+v, want the promoted agent as principal", out[0])
	}
}

// TestDeleteAgentEndpointUnassignsOrphanedChats is the regression test for
// ct-2026-07-29 (boss: "ningún chat queda apuntando a un agente que ya no
// existe"): every chat exclusively assigned to a deleted agent must revert
// to "new" — not a dangling agent_exclusive that only happens to be safe
// via dispatch's own fallback guard.
func TestDeleteAgentEndpointUnassignsOrphanedChats(t *testing.T) {
	st := newTestStore(t)
	if err := st.UpsertAgent(store.Agent{AgentID: "term-a", Role: "secondary"}); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("1@s.whatsapp.net", "Juan", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatus("1@s.whatsapp.net", store.AgentExclusiveStatus("term-a")); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("2@s.whatsapp.net", "Ana", 2); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatus("2@s.whatsapp.net", store.AgentExclusiveStatus("term-a")); err != nil {
		t.Fatal(err)
	}
	// A chat assigned to a DIFFERENT agent must not be touched.
	if err := st.UpsertAgent(store.Agent{AgentID: "term-b", Role: "secondary"}); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("3@s.whatsapp.net", "Otro", 3); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatus("3@s.whatsapp.net", store.AgentExclusiveStatus("term-b")); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/agent-delete", map[string]any{"agent_id": "term-a"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["chats_unassigned"] != float64(2) {
		t.Errorf("chats_unassigned = %v, want 2", body["chats_unassigned"])
	}
	c1, _, _ := st.GetChat("1@s.whatsapp.net")
	c2, _, _ := st.GetChat("2@s.whatsapp.net")
	c3, _, _ := st.GetChat("3@s.whatsapp.net")
	if c1.Status != "new" || c2.Status != "new" {
		t.Errorf("term-a's chats after delete: 1=%q 2=%q, want both new", c1.Status, c2.Status)
	}
	if c3.Status != "agent_exclusive:term-b" {
		t.Errorf("term-b's chat after deleting term-a: %q, want untouched (agent_exclusive:term-b)", c3.Status)
	}
}

func TestDeleteAgentEndpointUnknownAgent(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/agent-delete", map[string]any{"agent_id": "nunca-registrado"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (unknown agent_id)", resp.StatusCode)
	}
}

// ── M5 defaults por origen (ct-2026-07-22-1903) ─────────────────────────────

func TestConfigLevelDefaultEndpoints(t *testing.T) {
	for _, tc := range []struct {
		path string
		key  string
	}{
		{"config-level-default-new", "config_level_default_new"},
		{"config-level-default-contact", "config_level_default_contact"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			st := newTestStore(t)
			srv := httptest.NewServer(NewMux(Deps{Store: st}))
			defer srv.Close()

			resp := postJSON(t, srv.URL+"/api/admin/"+tc.path, map[string]any{"level": "confirm"})
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("POST status = %d, want 200", resp.StatusCode)
			}
			v, _ := st.KVGet(tc.key)
			if v != "confirm" {
				t.Errorf("KV %s = %q, want confirm", tc.key, v)
			}

			var body map[string]any
			getResp := getJSON(t, srv.URL+"/api/admin/"+tc.path, &body)
			defer getResp.Body.Close()
			if body["level"] != "confirm" {
				t.Errorf("GET level = %v, want confirm", body["level"])
			}
		})
	}
}

// TestConfigLevelDefaultEndpointsDefaultUnattended (ct-2026-07-22-2100,
// safety default): GET returns "unattended" before anything was ever
// POSTed — the boss's own "no atiende hasta que configure explícitamente".
func TestConfigLevelDefaultEndpointsDefaultUnattended(t *testing.T) {
	for _, path := range []string{"config-level-default-new", "config-level-default-contact"} {
		t.Run(path, func(t *testing.T) {
			st := newTestStore(t)
			srv := httptest.NewServer(NewMux(Deps{Store: st}))
			defer srv.Close()

			var body map[string]any
			resp := getJSON(t, srv.URL+"/api/admin/"+path, &body)
			defer resp.Body.Close()
			if body["level"] != "unattended" {
				t.Errorf("level = %v, want unattended before anything is configured", body["level"])
			}
		})
	}
}

func TestConfigLevelDefaultEndpointsRejectBoss(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/config-level-default-new", map[string]any{"level": "boss"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 — 'boss' must never be a valid origin default", resp.StatusCode)
	}
}

func TestRulesDefaultOriginEndpoints(t *testing.T) {
	for _, tc := range []struct {
		path string
		key  string
	}{
		{"rules-default-new-number", "rules_default_new_number"},
		{"rules-default-contact", "rules_default_contact"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			st := newTestStore(t)
			srv := httptest.NewServer(NewMux(Deps{Store: st}))
			defer srv.Close()

			resp := postJSON(t, srv.URL+"/api/admin/"+tc.path, map[string]any{"rules": "sé breve"})
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("POST status = %d, want 200", resp.StatusCode)
			}
			v, _ := st.KVGet(tc.key)
			if v != "sé breve" {
				t.Errorf("KV %s = %q, want %q", tc.key, v, "sé breve")
			}

			var body map[string]any
			getResp := getJSON(t, srv.URL+"/api/admin/"+tc.path, &body)
			defer getResp.Body.Close()
			if body["rules"] != "sé breve" {
				t.Errorf("GET rules = %v, want %q", body["rules"], "sé breve")
			}
		})
	}
}

// TestAgentDefaultOriginEndpoints (T71, ct-2026-08-27-1410): the third pair
// next to modo/reglas por origen — agente por defecto, mismo shape GET/POST
// que rules-default-*, pero validando agent_id como agent-assign (T70): la
// principal es válida sin fila en `agents`.
func TestAgentDefaultOriginEndpoints(t *testing.T) {
	for _, tc := range []struct {
		path string
		key  string
	}{
		{"agent-default-new-number", "agent_default_new_number"},
		{"agent-default-contact", "agent_default_contact"},
		{"agent-default-group", "agent_default_group"},
		{"agent-default-boss", "agent_default_boss"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			st := newTestStore(t)
			if err := st.UpsertAgent(store.Agent{AgentID: "term-a", Role: "secondary"}); err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(NewMux(Deps{Store: st}))
			defer srv.Close()

			resp := postJSON(t, srv.URL+"/api/admin/"+tc.path, map[string]any{"agent_id": "term-a"})
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("POST status = %d, want 200", resp.StatusCode)
			}
			v, _ := st.KVGet(tc.key)
			if v != "term-a" {
				t.Errorf("KV %s = %q, want term-a", tc.key, v)
			}

			var body map[string]any
			getResp := getJSON(t, srv.URL+"/api/admin/"+tc.path, &body)
			defer getResp.Body.Close()
			if body["agent_id"] != "term-a" {
				t.Errorf("GET agent_id = %v, want term-a", body["agent_id"])
			}
		})
	}
}

func TestAgentDefaultOriginEndpointsAllowPrincipal(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st, PrincipalTerminalID: "principal-term"}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/agent-default-new-number", map[string]any{"agent_id": "principal-term"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the principal has no `agents` row but is a valid default", resp.StatusCode)
	}
}

func TestAgentDefaultOriginEndpointsRejectUnknownAgent(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/agent-default-contact", map[string]any{"agent_id": "nunca-registrado"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (agent_id desconocido)", resp.StatusCode)
	}
}

func TestSetConfirmationModeEndpoint(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/confirmation-mode", map[string]any{"chat_id": "1@c.us", "mode": "always"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	c, _, err := st.GetChat("1@c.us")
	if err != nil || c.ConfirmationMode != "always" {
		t.Errorf("ConfirmationMode = %q, err=%v, want always", c.ConfirmationMode, err)
	}
}

// TestSetConfirmationModeEndpointRejectsInvalidMode is the regression test
// for the Low finding in the F4c audit: this endpoint used to pass mode
// through unvalidated, so a typo (or the deprecated legacy "required")
// would persist a value send_message's gate never matches.
func TestSetConfirmationModeEndpointRejectsInvalidMode(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	for _, bad := range []string{"required", "typo", ""} {
		resp := postJSON(t, srv.URL+"/api/admin/confirmation-mode", map[string]any{"chat_id": "1@c.us", "mode": bad})
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("mode=%q status = %d, want 400", bad, resp.StatusCode)
		}
	}
}

// TestSetConfigLevelEndpoint covers the config_level translation-layer
// endpoint end-to-end: POST /api/admin/config-level persists the 4
// underlying fields via store.SetConfigLevel, same as the MCP tool.
func TestSetConfigLevelEndpoint(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/config-level", map[string]any{"chat_id": "1@c.us", "level": "confirm"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	c, _, err := st.GetChat("1@c.us")
	if err != nil {
		t.Fatal(err)
	}
	if c.ConfirmationMode != "always" || !c.Active || c.IsBoss {
		t.Errorf("chat after config-level=confirm = %+v, want active=true confirmation_mode=always is_boss=false", c)
	}
	if store.ConfigLevel(c) != "confirm" {
		t.Errorf("ConfigLevel after config-level=confirm = %q, want confirm", store.ConfigLevel(c))
	}
}

// TestSetConfigLevelEndpointRejectsInvalidLevel is the endpoint's own
// enum-validation regression, same class of fix as
// TestSetConfirmationModeEndpointRejectsInvalidMode.
func TestSetConfigLevelEndpointRejectsInvalidLevel(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	for _, bad := range []string{"noreply", "typo", ""} {
		resp := postJSON(t, srv.URL+"/api/admin/config-level", map[string]any{"chat_id": "1@c.us", "level": bad})
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("level=%q status = %d, want 400", bad, resp.StatusCode)
		}
	}
}

func TestSetIsBossEndpoint(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/is-boss", map[string]any{"chat_id": "1@c.us", "is_boss": true})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	c, _, err := st.GetChat("1@c.us")
	if err != nil || !c.IsBoss {
		t.Errorf("GetChat.IsBoss = %v, err=%v, want true", c.IsBoss, err)
	}
}

// TestSetIsApproverEndpoint (Aprobador P1, ct-2026-07-31-0610) — same shape
// as TestSetIsBossEndpoint: the privileged dashboard path, orthogonal to
// is_boss (setting the pin must not also flip IsBoss).
func TestSetIsApproverEndpoint(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/approver", map[string]any{"chat_id": "1@c.us", "is_approver": true})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	c, _, err := st.GetChat("1@c.us")
	if err != nil || !c.IsApprover {
		t.Errorf("GetChat.IsApprover = %v, err=%v, want true", c.IsApprover, err)
	}
	if c.IsBoss {
		t.Error("GetChat.IsBoss = true after setting the approver pin, want false — orthogonal")
	}
}

func TestAdminEndpointsRequireAPIKeyWhenSet(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st, APIKey: "secret"}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/chat-rules", map[string]any{"chat_id": "1@c.us", "rules": "x"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status without key = %d, want 401", resp.StatusCode)
	}
}

func TestApproveAndDiscardDraftEndpoints(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("1@c.us", "C", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.AddDraft("1@c.us", "hola", "m", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.AddDraft("1@c.us", "chau", "m", 2); err != nil {
		t.Fatal(err)
	}
	drafts, err := st.PendingDrafts(10)
	if err != nil || len(drafts) != 2 {
		t.Fatalf("setup: PendingDrafts = %+v, err=%v", drafts, err)
	}

	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/approve-draft", map[string]any{"id": drafts[0].ID})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve status = %d, want 200", resp.StatusCode)
	}
	pending, err := st.PendingOutbox(10)
	if err != nil || len(pending) != 1 {
		t.Errorf("PendingOutbox after approve = %+v, err=%v, want 1", pending, err)
	}

	resp2 := postJSON(t, srv.URL+"/api/admin/discard-draft", map[string]any{"id": drafts[1].ID})
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("discard status = %d, want 200", resp2.StatusCode)
	}

	remaining, err := st.PendingDrafts(10)
	if err != nil || len(remaining) != 0 {
		t.Errorf("PendingDrafts after resolving both = %+v, err=%v, want empty", remaining, err)
	}
}

func TestApproveDraftNotFound(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/approve-draft", map[string]any{"id": 9999})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status for a nonexistent draft = %d, want 404", resp.StatusCode)
	}
}

// TestRejectDraftEndpointReopensMessage is T15 (ct-2026-08-05-123241) —
// the REST equivalent of mcpserver.TestRejectDraftReopensMessageForRedispatch:
// reason recorded, triggering message reopened for the next capipush sweep.
func TestRejectDraftEndpointReopensMessage(t *testing.T) {
	st := newTestStore(t)
	chat := "1@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMode(chat, "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetActive(chat, true); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "m1", FromMe: false, Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddDraftWithConfirmer(chat, "borrador", "m", "", "", 1, 2); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkHandledBefore(chat, 1); err != nil {
		t.Fatal(err)
	}
	drafts, err := st.PendingDrafts(10)
	if err != nil || len(drafts) != 1 {
		t.Fatalf("setup: PendingDrafts = %+v, err=%v, want 1", drafts, err)
	}

	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/reject-draft", map[string]any{"id": drafts[0].ID, "reason": "sé más breve"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reject status = %d, want 200", resp.StatusCode)
	}

	pending, err := st.PendingDedicated(10)
	if err != nil || len(pending) != 1 || pending[0].ID != "m1" {
		t.Fatalf("PendingDedicated after reject = %+v, err=%v, want m1 pending again", pending, err)
	}
	reason, _, ok, err := st.PendingRejectionNote(chat)
	if err != nil || !ok || reason != "sé más breve" {
		t.Errorf("PendingRejectionNote after reject = reason=%q ok=%v err=%v, want the rejection reason", reason, ok, err)
	}
}

func TestRejectDraftEndpointNotFound(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/reject-draft", map[string]any{"id": 9999, "reason": "x"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status for a nonexistent draft = %d, want 404", resp.StatusCode)
	}
}

// TestEditDraftEndpoint is T15's "editar sin aprobar" — text changes,
// status stays pending.
func TestEditDraftEndpoint(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("1@c.us", "C", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.AddDraft("1@c.us", "borrador original", "m", 1); err != nil {
		t.Fatal(err)
	}
	drafts, err := st.PendingDrafts(10)
	if err != nil || len(drafts) != 1 {
		t.Fatalf("setup: PendingDrafts = %+v, err=%v, want 1", drafts, err)
	}

	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/edit-draft", map[string]any{"id": drafts[0].ID, "text": "texto corregido"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("edit status = %d, want 200", resp.StatusCode)
	}

	after, err := st.PendingDrafts(10)
	if err != nil || len(after) != 1 || after[0].Text != "texto corregido" || after[0].Status != "pending" {
		t.Errorf("PendingDrafts after edit = %+v, err=%v, want text updated and status still pending", after, err)
	}
}

func TestEditDraftEndpointNotFound(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/edit-draft", map[string]any{"id": 9999, "text": "x"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status for a nonexistent draft = %d, want 404", resp.StatusCode)
	}
}

// TestApproveDiscardRejectEditDraftEndpointsPublishEventbus is T16
// (ct-2026-08-05-123257): the dashboard's own action buttons must nudge
// its SSE auto-refresh too, not just MCP-triggered resolutions — same
// requirement, REST side.
func TestApproveDiscardRejectEditDraftEndpointsPublishEventbus(t *testing.T) {
	st := newTestStore(t)
	chat := "1@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	bus := eventbus.New()
	srv := httptest.NewServer(NewMux(Deps{Store: st, Bus: bus}))
	defer srv.Close()

	ch, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	drain := func() {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for the draft eventbus nudge")
		}
	}

	if err := st.AddDraft(chat, "uno", "m", 1); err != nil {
		t.Fatal(err)
	}
	d, _ := st.PendingDrafts(10)
	postJSON(t, srv.URL+"/api/admin/approve-draft", map[string]any{"id": d[0].ID}).Body.Close()
	drain()

	if err := st.AddDraft(chat, "dos", "m", 2); err != nil {
		t.Fatal(err)
	}
	d, _ = st.PendingDrafts(10)
	postJSON(t, srv.URL+"/api/admin/discard-draft", map[string]any{"id": d[0].ID}).Body.Close()
	drain()

	if err := st.AddDraft(chat, "tres", "m", 3); err != nil {
		t.Fatal(err)
	}
	d, _ = st.PendingDrafts(10)
	postJSON(t, srv.URL+"/api/admin/reject-draft", map[string]any{"id": d[0].ID, "reason": "no"}).Body.Close()
	drain()

	if err := st.AddDraft(chat, "cuatro", "m", 4); err != nil {
		t.Fatal(err)
	}
	d, _ = st.PendingDrafts(10)
	postJSON(t, srv.URL+"/api/admin/edit-draft", map[string]any{"id": d[0].ID, "text": "cuatro corregido"}).Body.Close()
	drain()
}

// TestSetKillSwitchEndpoint is the REST-side half of the H2+H3 hardening
// regression (ct-2026-07-10-0540): POST /api/admin/kill must flip BOTH
// governor.Killed and state.Muted together, same as the MCP tool
// (mcpserver.TestSetKillSwitchFlipsGovernorAndState). T19 (ct-2026-08-05-1249)
// also persists the flag — main.go's restoreKillSwitch reads it back on
// the next boot.
func TestSetKillSwitchEndpoint(t *testing.T) {
	dir := t.TempDir()
	st := newTestStore(t)
	gov := governor.NewLimiter(10, time.Minute)
	sm := state.NewManager(filepath.Join(dir, "status.json"), 8)
	srv := httptest.NewServer(NewMux(Deps{Store: st, Governor: gov, State: sm}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/kill", map[string]any{"kill": true})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("kill(true) status = %d, want 200", resp.StatusCode)
	}
	if !gov.Killed() {
		t.Error("governor.Killed() = false after POST kill=true")
	}
	if !sm.Snapshot().Muted {
		t.Error("state Muted = false after POST kill=true")
	}
	if !st.SettingBool(store.SettingKillSwitch, false) {
		t.Error("store.SettingKillSwitch = false after POST kill=true, want persisted true — a restart would silently release the brake")
	}

	resp2 := postJSON(t, srv.URL+"/api/admin/kill", map[string]any{"kill": false})
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("kill(false) status = %d, want 200", resp2.StatusCode)
	}
	if gov.Killed() {
		t.Error("governor.Killed() = true after POST kill=false")
	}
	if sm.Snapshot().Muted {
		t.Error("state Muted = true after POST kill=false")
	}
	if st.SettingBool(store.SettingKillSwitch, false) {
		t.Error("store.SettingKillSwitch = true after POST kill=false, want persisted false")
	}
}

func TestSetModeEndpoint(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/mode", map[string]any{"chat_id": "1@c.us", "mode": "auto"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	c, ok, err := st.GetChat("1@c.us")
	if err != nil || !ok || c.Mode != "auto" {
		t.Errorf("chat mode = %q ok=%v err=%v, want %q", c.Mode, ok, err, "auto")
	}

	resp2 := postJSON(t, srv.URL+"/api/admin/mode", map[string]any{"chat_id": "1@c.us", "mode": "bogus"})
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("status for invalid mode = %d, want 400", resp2.StatusCode)
	}
}

func TestSetMemoryAndContextEndpoints(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/memory", map[string]any{"chat_id": "1@c.us", "memory": "le gusta el café"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("memory status = %d, want 200", resp.StatusCode)
	}

	resp2 := postJSON(t, srv.URL+"/api/admin/context", map[string]any{"chat_id": "1@c.us", "context": "cliente frecuente"})
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("context status = %d, want 200", resp2.StatusCode)
	}

	c, ok, err := st.GetChat("1@c.us")
	if err != nil || !ok || c.Memory != "le gusta el café" || c.Context != "cliente frecuente" {
		t.Errorf("chat = %+v ok=%v err=%v, want memory/context set", c, ok, err)
	}
}

func TestWhitelistAddEndpoint(t *testing.T) {
	rt := router.NewManager(filepath.Join(t.TempDir(), "router.json"))
	srv := httptest.NewServer(NewMux(Deps{Router: rt}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/whitelist-add", map[string]any{"jid": "999@s.whatsapp.net"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !rt.Snapshot().IsVIP("999@s.whatsapp.net") {
		t.Error("999@s.whatsapp.net not marked VIP after whitelist-add")
	}

	// Idempotent: adding the same jid again must not duplicate it.
	resp2 := postJSON(t, srv.URL+"/api/admin/whitelist-add", map[string]any{"jid": "999@s.whatsapp.net"})
	defer resp2.Body.Close()
	if n := len(rt.Snapshot().Whitelist); n != 1 {
		t.Errorf("whitelist len = %d after re-adding the same jid, want 1", n)
	}
}

// TestWhitelistAddTouchesChat covers a gap found live in browser testing
// (ct-2026-07-10-2312): whitelisting alone never creates a chat row, so
// GET /api/chats wouldn't list a freshly-added number until it actually
// wrote in. handleWhitelistAdd must TouchChat it too when Store is wired.
func TestWhitelistAddTouchesChat(t *testing.T) {
	st := newTestStore(t)
	rt := router.NewManager(filepath.Join(t.TempDir(), "router.json"))
	srv := httptest.NewServer(NewMux(Deps{Store: st, Router: rt}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/whitelist-add", map[string]any{"jid": "888@s.whatsapp.net"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	_, ok, err := st.GetChat("888@s.whatsapp.net")
	if err != nil || !ok {
		t.Errorf("GetChat after whitelist-add: ok=%v err=%v, want the chat to exist", ok, err)
	}
}

func TestSetIgnoredEndpoint(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("group@g.us", "Grupo", 1); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/ignore", map[string]any{"chat_id": "group@g.us", "ignored": true})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	c, _, err := st.GetChat("group@g.us")
	if err != nil || c.Status != "ignored" {
		t.Errorf("status = %q err=%v, want ignored", c.Status, err)
	}

	resp2 := postJSON(t, srv.URL+"/api/admin/ignore", map[string]any{"chat_id": "group@g.us", "ignored": false})
	defer resp2.Body.Close()
	c2, _, err := st.GetChat("group@g.us")
	if err != nil || c2.Status != "new" {
		t.Errorf("status after un-ignore = %q err=%v, want new", c2.Status, err)
	}
}

// TestSetIgnoredEndpointMarksConfigManual (M5, ct-2026-07-22-1903): ignoring
// an individual chat is an explicit owner decision — it must freeze
// config_level_source so a later contact-name sync never silently revives
// (un-ignores) it via the origin default.
func TestSetIgnoredEndpointMarksConfigManual(t *testing.T) {
	st := newTestStore(t)
	if err := st.KVSet("config_level_default_contact", "auto"); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("1@s.whatsapp.net", "Alguien", 1); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/ignore", map[string]any{"chat_id": "1@s.whatsapp.net", "ignored": true})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// A later contact-name sync must NOT un-ignore this chat via the
	// "contact" origin default (config_level_default_contact=auto would set
	// active=true otherwise).
	if err := st.SetContactName("1@s.whatsapp.net", "Juan Pérez"); err != nil {
		t.Fatal(err)
	}
	c, _, err := st.GetChat("1@s.whatsapp.net")
	if err != nil || c.Status != "ignored" {
		t.Errorf("Status after later contact sync = %q err=%v, want ignored to survive (manual)", c.Status, err)
	}
}

// ── D4 reset "partir de 0" (ct-2026-07-22-2100) ─────────────────────────────

type mockResetter struct{ kicked bool }

func (m *mockResetter) KickResync() { m.kicked = true }

func TestResetEndpointWipesDataPreservesConfig(t *testing.T) {
	st := newTestStore(t)
	jid := "1@s.whatsapp.net"
	if err := st.TouchChat(jid, "Ana", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: jid, ID: "m1", Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.KVSet("some_setting", "value"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAgent(store.Agent{AgentID: "term-a", Role: "secondary"}); err != nil {
		t.Fatal(err)
	}

	resetter := &mockResetter{}
	srv := httptest.NewServer(NewMux(Deps{Store: st, Resetter: resetter}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/reset", map[string]any{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if _, ok, err := st.GetChat(jid); err != nil || ok {
		t.Errorf("GetChat after reset: ok=%v err=%v, want gone", ok, err)
	}
	v, err := st.KVGet("some_setting")
	if err != nil || v != "value" {
		t.Errorf("KV after reset = %q err=%v, want preserved 'value'", v, err)
	}
	if a, ok, err := st.GetAgent("term-a"); err != nil || !ok || a.AgentID != "term-a" {
		t.Errorf("GetAgent after reset: ok=%v err=%v, want preserved", ok, err)
	}
	if !resetter.kicked {
		t.Error("Resetter.KickResync was never called")
	}
}

func TestResetEndpointClearsMediaDirContentsButKeepsDir(t *testing.T) {
	st := newTestStore(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "orphan.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(NewMux(Deps{Store: st, MediaDir: dir}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/reset", map[string]any{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if _, err := os.Stat(dir); err != nil {
		t.Errorf("MediaDir itself must survive the reset: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("MediaDir entries after reset = %v, want empty (contents cleared)", entries)
	}
}

func TestClearDirContentsMissingDirIsNoOp(t *testing.T) {
	if err := clearDirContents(filepath.Join(t.TempDir(), "never-existed")); err != nil {
		t.Errorf("clearDirContents(missing dir) = %v, want nil", err)
	}
}

// ── M3 "Desconectar" (ct-2026-07-22-2342) ───────────────────────────────────

type mockDisconnecter struct {
	called bool
	err    error
}

func (m *mockDisconnecter) Logout(ctx context.Context) error {
	m.called = true
	return m.err
}

func TestDisconnectEndpointCallsLogout(t *testing.T) {
	disc := &mockDisconnecter{}
	srv := httptest.NewServer(NewMux(Deps{Disconnecter: disc}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/disconnect", map[string]any{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !disc.called {
		t.Error("Disconnecter.Logout was never called")
	}
}

func TestDisconnectEndpointUnavailableWithoutDisconnecter(t *testing.T) {
	srv := httptest.NewServer(NewMux(Deps{}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/disconnect", map[string]any{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status with nil Disconnecter = %d, want 503", resp.StatusCode)
	}
}

func TestDisconnectEndpointReportsLogoutError(t *testing.T) {
	disc := &mockDisconnecter{err: errors.New("boom")}
	srv := httptest.NewServer(NewMux(Deps{Disconnecter: disc}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/disconnect", map[string]any{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status on Logout error = %d, want 500", resp.StatusCode)
	}
}

// ── Estado de WhatsApp (T92, ct-2026-08-28) ─────────────────────────────────

type mockProfileStatus struct {
	lastStatus string // last value SetProfileStatus was called with
	setErr     error  // error SetProfileStatus returns

	getStatus string // value GetProfileStatus returns (T96)
	getErr    error  // error GetProfileStatus returns (T96)
}

func (m *mockProfileStatus) SetProfileStatus(ctx context.Context, status string) error {
	m.lastStatus = status
	return m.setErr
}

func (m *mockProfileStatus) GetProfileStatus(ctx context.Context) (string, error) {
	return m.getStatus, m.getErr
}

func TestSetProfileStatusEndpointCallsSetProfileStatus(t *testing.T) {
	ps := &mockProfileStatus{}
	srv := httptest.NewServer(NewMux(Deps{ProfileStatus: ps}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/profile-status", map[string]any{"status": "Ocupado, respondo pronto"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ps.lastStatus != "Ocupado, respondo pronto" {
		t.Errorf("SetProfileStatus called with %q, want %q", ps.lastStatus, "Ocupado, respondo pronto")
	}
}

func TestSetProfileStatusEndpointUnavailableWithoutProfileStatus(t *testing.T) {
	srv := httptest.NewServer(NewMux(Deps{}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/profile-status", map[string]any{"status": "hola"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status with nil ProfileStatus = %d, want 503", resp.StatusCode)
	}
}

func TestSetProfileStatusEndpointReportsError(t *testing.T) {
	ps := &mockProfileStatus{setErr: errors.New("boom")}
	srv := httptest.NewServer(NewMux(Deps{ProfileStatus: ps}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/profile-status", map[string]any{"status": "hola"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status on SetProfileStatus error = %d, want 500", resp.StatusCode)
	}
}

// T96 (ct-2026-08-28-1743) — la mitad que faltó de T92: leer el estado en
// vez de solo escribirlo.

func TestGetProfileStatusEndpointReturnsCurrentStatus(t *testing.T) {
	ps := &mockProfileStatus{getStatus: "Ocupado, respondo pronto"}
	srv := httptest.NewServer(NewMux(Deps{ProfileStatus: ps}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/admin/profile-status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "Ocupado, respondo pronto" {
		t.Errorf("status = %q, want %q", body["status"], "Ocupado, respondo pronto")
	}
}

// TestGetProfileStatusEndpointReturnsEmptyStatusWithoutError cubre el estado
// vacío — legítimo en WhatsApp (la mayoría de las cuentas nunca configura
// uno), no un error: el endpoint debe responder 200 con status="", nunca un
// 500 ni un 404 que el dashboard confundiría con una falla real.
func TestGetProfileStatusEndpointReturnsEmptyStatusWithoutError(t *testing.T) {
	ps := &mockProfileStatus{}
	srv := httptest.NewServer(NewMux(Deps{ProfileStatus: ps}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/admin/profile-status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (un estado vacío no es un error)", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "" {
		t.Errorf("status = %q, want \"\"", body["status"])
	}
}

func TestGetProfileStatusEndpointReportsError(t *testing.T) {
	ps := &mockProfileStatus{getErr: errors.New("boom")}
	srv := httptest.NewServer(NewMux(Deps{ProfileStatus: ps}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/admin/profile-status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status on GetProfileStatus error = %d, want 500", resp.StatusCode)
	}
}

func TestGetProfileStatusEndpointUnavailableWithoutProfileStatus(t *testing.T) {
	srv := httptest.NewServer(NewMux(Deps{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/admin/profile-status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status with nil ProfileStatus = %d, want 503", resp.StatusCode)
	}
}

// ── Foto de perfil de WhatsApp (T112c, ct-2026-09-01-145755) ───────────────

type mockProfilePhoto struct {
	lastJPEG []byte // bytes SetProfilePhoto was called with
	setErr   error  // error SetProfilePhoto returns
}

func (m *mockProfilePhoto) SetProfilePhoto(ctx context.Context, jpeg []byte) (string, error) {
	m.lastJPEG = jpeg
	return "pic-id", m.setErr
}

// testPNGDataURL builds a tiny valid PNG data URL — same shape as
// mcpserver's testImageDataURL(t, "png"), duplicated here because the two
// packages don't share test internals (same reasoning as
// markHandledForDispatch/publishDraftChanged above).
func testPNGDataURL(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			img.Set(x, y, color.RGBA{50, 100, 150, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestSetProfilePhotoEndpointDecodesAndConvertsToJPEG(t *testing.T) {
	pp := &mockProfilePhoto{}
	srv := httptest.NewServer(NewMux(Deps{ProfilePhoto: pp}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/profile-photo", map[string]any{"data_url": testPNGDataURL(t)})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if _, format, err := image.Decode(bytes.NewReader(pp.lastJPEG)); err != nil || format != "jpeg" {
		t.Errorf("bytes reaching ProfilePhoto decode as (%q, %v), want jpeg with no error", format, err)
	}
}

// TestSetProfilePhotoEndpointInvalidatesOwnAvatarCache (T116,
// ct-2026-09-01-2040): a photo change made FROM the dashboard must force
// the NEXT real avatar check for the host's own jid — otherwise the
// server-side cache keeps serving the stale file until the (short, but
// not zero) recheck window naturally elapses, even though the hero already
// repaints with the freshly-uploaded bytes (T112c) and hides the symptom
// on screen. PictureID/Path/FetchedAt of the existing row must survive
// untouched — only NextCheckAt resets.
func TestSetProfilePhotoEndpointInvalidatesOwnAvatarCache(t *testing.T) {
	st := newTestStore(t)
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
	ownJID := "555000001@s.whatsapp.net"
	if err := sm.Update(func(s *state.Status) { s.OwnJID = ownJID }); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(10 * time.Minute).Unix()
	if err := st.UpsertAvatar(store.Avatar{
		JID: ownJID, PictureID: "pic-old", Path: "old.jpg", FetchedAt: 1000, NextCheckAt: future,
	}); err != nil {
		t.Fatal(err)
	}

	pp := &mockProfilePhoto{}
	srv := httptest.NewServer(NewMux(Deps{ProfilePhoto: pp, Store: st, State: sm}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/profile-photo", map[string]any{"data_url": testPNGDataURL(t)})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	got, ok, err := st.GetAvatar(ownJID)
	if err != nil || !ok {
		t.Fatalf("GetAvatar: ok=%v err=%v", ok, err)
	}
	if got.NextCheckAt >= future {
		t.Errorf("NextCheckAt = %d, want reset to force the next real check (was %d)", got.NextCheckAt, future)
	}
	if got.PictureID != "pic-old" || got.Path != "old.jpg" || got.FetchedAt != 1000 {
		t.Errorf("invalidation touched fields it shouldn't have: %+v", got)
	}
}

func TestSetProfilePhotoEndpointRejectsEmptyDataURL(t *testing.T) {
	pp := &mockProfilePhoto{}
	srv := httptest.NewServer(NewMux(Deps{ProfilePhoto: pp}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/profile-photo", map[string]any{"data_url": ""})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status with empty data_url = %d, want 400", resp.StatusCode)
	}
	if pp.lastJPEG != nil {
		t.Error("SetProfilePhoto was called despite an empty data_url")
	}
}

func TestSetProfilePhotoEndpointRejectsUndecodableImage(t *testing.T) {
	pp := &mockProfilePhoto{}
	srv := httptest.NewServer(NewMux(Deps{ProfilePhoto: pp}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/profile-photo", map[string]any{"data_url": "data:image/png;base64,aGVsbG8="})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status with undecodable image = %d, want 400", resp.StatusCode)
	}
	if pp.lastJPEG != nil {
		t.Error("SetProfilePhoto was called despite an undecodable image")
	}
}

func TestSetProfilePhotoEndpointReportsError(t *testing.T) {
	pp := &mockProfilePhoto{setErr: errors.New("boom")}
	srv := httptest.NewServer(NewMux(Deps{ProfilePhoto: pp}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/profile-photo", map[string]any{"data_url": testPNGDataURL(t)})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status on SetProfilePhoto error = %d, want 500", resp.StatusCode)
	}
}

func TestSetProfilePhotoEndpointUnavailableWithoutProfilePhoto(t *testing.T) {
	srv := httptest.NewServer(NewMux(Deps{}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/profile-photo", map[string]any{"data_url": testPNGDataURL(t)})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status with nil ProfilePhoto = %d, want 503", resp.StatusCode)
	}
}

// TestSetProfilePhotoEndpointRejectsOversizedBody (T112c amend, Citrino's
// finding): the file picker in the browser has no format filter — nothing
// stops the owner from choosing a video instead of a photo. Without a body
// cap, the gateway reads and buffers the whole thing before EnsureJPEG ever
// gets a chance to reject it, on a process that's also handling live
// WhatsApp traffic. This must be rejected before SetProfilePhoto is ever
// reached, with its own message — not the generic "invalid JSON body".
func TestSetProfilePhotoEndpointRejectsOversizedBody(t *testing.T) {
	pp := &mockProfilePhoto{}
	srv := httptest.NewServer(NewMux(Deps{ProfilePhoto: pp}))
	defer srv.Close()

	huge := strings.Repeat("A", maxProfilePhotoBodyBytes+1)
	resp := postJSON(t, srv.URL+"/api/admin/profile-photo", map[string]any{"data_url": huge})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status with oversized body = %d, want 413", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err == nil && !strings.Contains(body["error"], "grande") {
		t.Errorf("error = %q, want it to name the image as too large", body["error"])
	}
	if pp.lastJPEG != nil {
		t.Error("SetProfilePhoto was called despite an oversized body")
	}
}

// ── cAPI connector tests ─────────────────────────────────────────────────────

type mockConnector struct {
	lastEndpoint   string
	lastTerminalID string
	lastPinpass    string
	testErr        error

	injectErr   error
	lastFrom    string
	lastPayload string
}

func (m *mockConnector) SetConfig(endpoint, terminalID, pinpass string) {
	m.lastEndpoint, m.lastTerminalID, m.lastPinpass = endpoint, terminalID, pinpass
}

func (m *mockConnector) TestHandshake() error { return m.testErr }

func (m *mockConnector) Inject(_, from, payload string) error {
	m.lastFrom, m.lastPayload = from, payload
	return m.injectErr
}

func TestGetCAPIConnector(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/admin/capi-connector")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["pinpass_set"] != false {
		t.Errorf("pinpass_set = %v, want false when nothing stored", body["pinpass_set"])
	}
}

func TestSetCAPIConnector(t *testing.T) {
	st := newTestStore(t)
	conn := &mockConnector{}
	srv := httptest.NewServer(NewMux(Deps{Store: st, Connector: conn}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/capi-connector", map[string]any{
		"endpoint":    "http://192.168.1.10:8787",
		"terminal_id": "term-guid",
		"pinpass":     "s3cr3t==",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// KV persisted
	ep, _ := st.KVGet("capi_endpoint")
	if ep != "http://192.168.1.10:8787" {
		t.Errorf("KV capi_endpoint = %q, want the posted endpoint", ep)
	}
	// Connector reconfigured in-hot
	if conn.lastEndpoint != "http://192.168.1.10:8787" || conn.lastTerminalID != "term-guid" || conn.lastPinpass != "s3cr3t==" {
		t.Errorf("SetConfig not called with correct args: %q/%q/%q", conn.lastEndpoint, conn.lastTerminalID, conn.lastPinpass)
	}

	// GET after POST must show pinpass_set = true
	getResp, err := http.Get(srv.URL + "/api/admin/capi-connector")
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(getResp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["pinpass_set"] != true {
		t.Errorf("pinpass_set = %v after POST, want true", body["pinpass_set"])
	}
	if _, hasPin := body["pinpass"]; hasPin {
		t.Error("GET must never return the pinpass in clear")
	}
}

func TestTestCAPIConnectorOK(t *testing.T) {
	conn := &mockConnector{testErr: nil}
	srv := httptest.NewServer(NewMux(Deps{Connector: conn}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/capi-connector/test", map[string]any{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true {
		t.Errorf("ok = %v, want true on successful handshake", body["ok"])
	}
}

func TestTestCAPIConnectorFail(t *testing.T) {
	conn := &mockConnector{testErr: errors.New("clever_injector: endpoint not configured")}
	srv := httptest.NewServer(NewMux(Deps{Connector: conn}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/capi-connector/test", map[string]any{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want always-200 (result is the body)", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != false {
		t.Errorf("ok = %v, want false on handshake error", body["ok"])
	}
}

// ── P8 ping (ct-2026-07-22-0422) ─────────────────────────────────────────────

// TestCAPIPingSendsReadableMessage (T28, ct-2026-08-05-2242: dispatches are
// never encrypted — the second AES-256-GCM layer this ping used to exercise
// is gone) verifies the ping delivers a readable message, same compact
// shape a real dispatch uses (a trailing "NC:<nonce>" line).
func TestCAPIPingSendsReadableMessage(t *testing.T) {
	conn := &mockConnector{}
	srv := httptest.NewServer(NewMux(Deps{Connector: conn}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/capi-ping", map[string]any{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true {
		t.Errorf("ok = %v, want true", body["ok"])
	}
	if !strings.Contains(conn.lastPayload, "PING de prueba") {
		t.Errorf("payload = %q, want the readable ping text", conn.lastPayload)
	}
	if !strings.Contains(conn.lastPayload, "NC:") {
		t.Errorf("payload = %q, want a trailing NC:<nonce> line (same shape as a real dispatch)", conn.lastPayload)
	}
}

func TestCAPIPingTerminalNotListening(t *testing.T) {
	conn := &mockConnector{injectErr: errors.New("clever_injector: terminal x no escucha (antena apagada o tab cerrado) — encendé la antena en el terminal destino")}
	srv := httptest.NewServer(NewMux(Deps{Connector: conn}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/capi-ping", map[string]any{})
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != false {
		t.Errorf("ok = %v, want false when the terminal isn't listening", body["ok"])
	}
}

func TestCAPIPingUnavailableWithoutConnector(t *testing.T) {
	srv := httptest.NewServer(NewMux(Deps{}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/capi-ping", map[string]any{})
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != false {
		t.Error("ok should be false when Connector is nil")
	}
}

// ── M2 per-agent ping (ct-2026-07-22-1301) ──────────────────────────────────

// mockInjectorResolver is a tiny stand-in for capipush.Pusher's injectors
// map — same reasoning as mockConnector above.
type mockInjectorResolver struct {
	byAgent map[string]*mockConnector // reused as the Injector (Inject is all that's needed)
}

func (m mockInjectorResolver) InjectorFor(agentID string) (Injector, bool) {
	inj, ok := m.byAgent[agentID]
	return inj, ok
}

func TestCAPIPingWithAgentIDUsesThatAgentsInjector(t *testing.T) {
	principal := &mockConnector{}
	secondary := &mockConnector{}
	resolver := mockInjectorResolver{byAgent: map[string]*mockConnector{"secondary-term": secondary}}
	srv := httptest.NewServer(NewMux(Deps{Connector: principal, Injectors: resolver}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/capi-ping", map[string]any{"agent_id": "secondary-term"})
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true {
		t.Fatalf("ok = %v, want true: %v", body["ok"], body)
	}
	if secondary.lastPayload == "" {
		t.Error("the SECONDARY's injector was never called")
	}
	if principal.lastPayload != "" {
		t.Error("the PRINCIPAL's Connector was called instead of the resolved agent's own injector")
	}
}

func TestCAPIPingWithUnknownAgentIDReportsClearError(t *testing.T) {
	resolver := mockInjectorResolver{byAgent: map[string]*mockConnector{}}
	srv := httptest.NewServer(NewMux(Deps{Injectors: resolver}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/capi-ping", map[string]any{"agent_id": "never-registered"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want always-200 (result is the body)", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != false {
		t.Errorf("ok = %v, want false for an agent_id nothing ever registered", body["ok"])
	}
}

func TestCAPIPingWithAgentIDButNoResolverWiredDoesNotCrash(t *testing.T) {
	srv := httptest.NewServer(NewMux(Deps{})) // Injectors left nil
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/capi-ping", map[string]any{"agent_id": "secondary-term"})
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != false {
		t.Errorf("ok = %v, want false (no crash) when Injectors is nil", body["ok"])
	}
}

func TestSetCAPIConnectorLineValid(t *testing.T) {
	st := newTestStore(t)
	conn := &mockConnector{}
	srv := httptest.NewServer(NewMux(Deps{Store: st, Connector: conn}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/capi-connector-line", map[string]any{
		"line": "192.168.1.83:8787 chat_id:57582399-1400-485c-ab6a-22febe672344 pin:3y+X4bmS0Yau91l/6cJAjw==",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["terminal_id"] != "57582399-1400-485c-ab6a-22febe672344" {
		t.Errorf("terminal_id = %v, want the parsed chat_id", body["terminal_id"])
	}
	// S6 (ct-2026-07-30-031048): the LAN IP survives now — a private
	// address is exactly what isAllowedPrincipalEndpoint (via
	// SetPrincipalAgent) is meant to accept, not discard.
	wantEndpoint := "http://192.168.1.83:8787"
	if body["endpoint"] != wantEndpoint {
		t.Errorf("endpoint = %v, want %q (a private LAN IP must survive)", body["endpoint"], wantEndpoint)
	}
	ep, _ := st.KVGet("capi_endpoint")
	if ep != wantEndpoint {
		t.Errorf("KV capi_endpoint = %q, want %q", ep, wantEndpoint)
	}
	if conn.lastEndpoint != wantEndpoint || conn.lastTerminalID != "57582399-1400-485c-ab6a-22febe672344" || conn.lastPinpass != "3y+X4bmS0Yau91l/6cJAjw==" {
		t.Errorf("SetConfig not called with correct args: %q/%q/%q", conn.lastEndpoint, conn.lastTerminalID, conn.lastPinpass)
	}
}

// TestSetCAPIConnectorLineRejectsPublicIP: isAllowedPrincipalEndpoint (via
// SetPrincipalAgent) is the ONE place that decides what's accepted — a
// public IP pasted in the connector line must still be refused here, same
// as the set_capi_connector MCP tool and POST /api/admin/agent-update.
func TestSetCAPIConnectorLineRejectsPublicIP(t *testing.T) {
	st := newTestStore(t)
	conn := &mockConnector{}
	srv := httptest.NewServer(NewMux(Deps{Store: st, Connector: conn}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/capi-connector-line", map[string]any{
		"line": "203.0.113.7:8787 chat_id:x pin:y",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 on a public IP", resp.StatusCode)
	}
	ep, _ := st.KVGet("capi_endpoint")
	if ep != "" {
		t.Errorf("KV capi_endpoint = %q after a refused public-IP line, want empty (never persisted)", ep)
	}
	if conn.lastEndpoint != "" {
		t.Errorf("Connector.SetConfig was called (%q) for a refused public IP, want it never reached", conn.lastEndpoint)
	}
}

func TestSetCAPIConnectorLineInvalid(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/capi-connector-line", map[string]any{
		"line": "esto no es una línea válida",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 on unparseable line", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["error"] == "" || body["error"] == nil {
		t.Error("error body empty, want a clear parse-error message")
	}
}

func TestAdminEndpointsUnavailableWithoutStore(t *testing.T) {
	srv := httptest.NewServer(NewMux(Deps{}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/chat-rules", map[string]any{"chat_id": "1@c.us", "rules": "x"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status with nil Store = %d, want 503", resp.StatusCode)
	}
}

func TestRecoveryEmailGetSetRoundTrip(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	getEmpty, err := http.Get(srv.URL + "/api/admin/recovery-email")
	if err != nil {
		t.Fatal(err)
	}
	defer getEmpty.Body.Close()
	var bodyEmpty map[string]string
	json.NewDecoder(getEmpty.Body).Decode(&bodyEmpty)
	if bodyEmpty["email"] != "" {
		t.Errorf("email before setting = %q, want empty", bodyEmpty["email"])
	}

	resp := postJSON(t, srv.URL+"/api/admin/recovery-email", map[string]any{"email": "boss@example.com"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	getSet, err := http.Get(srv.URL + "/api/admin/recovery-email")
	if err != nil {
		t.Fatal(err)
	}
	defer getSet.Body.Close()
	var bodySet map[string]string
	json.NewDecoder(getSet.Body).Decode(&bodySet)
	if bodySet["email"] != "boss@example.com" {
		t.Errorf("email after setting = %q, want boss@example.com", bodySet["email"])
	}
}

func TestRecoveryEmailRejectsInvalidFormat(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/recovery-email", map[string]any{"email": "not-an-email"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a malformed email", resp.StatusCode)
	}
}

// TestDispatchDebounceGetSetRoundTrip (T90, ct-2026-08-28-1350) — before the
// dashboard ever touches it, GET reports the boot-time config default
// (DefaultDispatchDebounce/DefaultMaxDispatchDebounce); after a POST, GET
// reports the STORED value instead — "el valor del tablero pisa al de
// entorno", same round-trip shape as TestRecoveryEmailGetSetRoundTrip.
func TestDispatchDebounceGetSetRoundTrip(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{
		Store: st, DefaultDispatchDebounce: 60 * time.Second, DefaultMaxDispatchDebounce: 5 * time.Minute,
	}))
	defer srv.Close()

	var out struct {
		DebounceSeconds    int `json:"debounce_seconds"`
		MaxDebounceSeconds int `json:"max_debounce_seconds"`
	}
	getBefore := getJSON(t, srv.URL+"/api/admin/dispatch-debounce", &out)
	defer getBefore.Body.Close()
	if out.DebounceSeconds != 60 || out.MaxDebounceSeconds != 300 {
		t.Fatalf("GET before any dashboard edit = %+v, want the config defaults (60, 300)", out)
	}

	resp := postJSON(t, srv.URL+"/api/admin/dispatch-debounce", map[string]any{"debounce_seconds": 8, "max_debounce_seconds": 120})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	getAfter := getJSON(t, srv.URL+"/api/admin/dispatch-debounce", &out)
	defer getAfter.Body.Close()
	if out.DebounceSeconds != 8 || out.MaxDebounceSeconds != 120 {
		t.Errorf("GET after set = %+v, want the value just set (8, 120), not the config default", out)
	}
}

// TestDispatchDebounceAcceptsZero: 0 is "despachá apenas llegue", a
// legitimate owner choice — must NOT be rejected as invalid.
func TestDispatchDebounceAcceptsZero(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st, DefaultMaxDispatchDebounce: 5 * time.Minute}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/dispatch-debounce", map[string]any{"debounce_seconds": 0, "max_debounce_seconds": 300})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status for debounce_seconds=0 = %d, want 200 (a legitimate value, not an error)", resp.StatusCode)
	}
}

// TestDispatchDebounceRejectsCeilingBelowFloor: a max smaller than the
// debounce it's meant to cap is a configuration with no sensible meaning —
// rejected with a message that explains why, not a silent clamp.
func TestDispatchDebounceRejectsCeilingBelowFloor(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/dispatch-debounce", map[string]any{"debounce_seconds": 60, "max_debounce_seconds": 10})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (ceiling below floor)", resp.StatusCode)
	}
	var out map[string]string
	json.NewDecoder(resp.Body).Decode(&out)
	if out["error"] == "" {
		t.Error("400 response has no error message explaining why — the DoD asks for one the owner can read")
	}
}

// TestSeedRecoveryEmailFromEnvOnFirstBoot (Windows installer, ct-2026-07-31-
// 1643): same seed-only shape as TestPassHashSeedsFromEnvOnFirstBoot — a
// fresh store with PIUMY_DASHBOARD_RECOVERY_EMAIL set seeds the KV from it.
func TestSeedRecoveryEmailFromEnvOnFirstBoot(t *testing.T) {
	t.Setenv(dashboardRecoveryEmailSeedEnv, "boss@example.com")
	st := newTestStore(t)

	if err := SeedRecoveryEmailFromEnv(st); err != nil {
		t.Fatal(err)
	}
	got, err := st.KVGet(store.SettingDashRecoveryEmail)
	if err != nil {
		t.Fatal(err)
	}
	if got != "boss@example.com" {
		t.Errorf("recovery email = %q, want boss@example.com", got)
	}
}

// TestSeedRecoveryEmailFromEnvIgnoresEnvWhenAlreadySet mirrors
// TestPassHashIgnoresEnvSeedWhenHashAlreadyExists — an existing value
// (the boss's own, set through the dashboard) is never overwritten by the
// installer's one-shot env var on a later boot.
func TestSeedRecoveryEmailFromEnvIgnoresEnvWhenAlreadySet(t *testing.T) {
	st := newTestStore(t)
	if err := st.KVSet(store.SettingDashRecoveryEmail, "already-set@example.com"); err != nil {
		t.Fatal(err)
	}

	t.Setenv(dashboardRecoveryEmailSeedEnv, "should-not-overwrite@example.com")
	if err := SeedRecoveryEmailFromEnv(st); err != nil {
		t.Fatal(err)
	}
	got, err := st.KVGet(store.SettingDashRecoveryEmail)
	if err != nil {
		t.Fatal(err)
	}
	if got != "already-set@example.com" {
		t.Errorf("recovery email = %q, want unchanged already-set@example.com", got)
	}
}

// TestSeedRecoveryEmailFromEnvSkipsMalformed guards the seed path with the
// same light check handleSetRecoveryEmail applies to a human-submitted
// address — a broken value from the installer shouldn't silently become the
// stored "recovery" address.
func TestSeedRecoveryEmailFromEnvSkipsMalformed(t *testing.T) {
	t.Setenv(dashboardRecoveryEmailSeedEnv, "not-an-email")
	st := newTestStore(t)

	if err := SeedRecoveryEmailFromEnv(st); err != nil {
		t.Fatal(err)
	}
	got, err := st.KVGet(store.SettingDashRecoveryEmail)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("recovery email = %q, want empty (malformed seed must be ignored)", got)
	}
}

// TestSeedRecoveryEmailFromEnvNoSeedLeavesEmpty is the default-behavior
// regression guard — an install with no PIUMY_DASHBOARD_RECOVERY_EMAIL at
// all (the field is optional, per the boss) must leave the KV empty, not
// error or fall back to some placeholder.
func TestSeedRecoveryEmailFromEnvNoSeedLeavesEmpty(t *testing.T) {
	st := newTestStore(t)

	if err := SeedRecoveryEmailFromEnv(st); err != nil {
		t.Fatal(err)
	}
	got, err := st.KVGet(store.SettingDashRecoveryEmail)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("recovery email = %q, want empty with no seed and no prior value", got)
	}
}
