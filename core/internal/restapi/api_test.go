// SPDX-License-Identifier: AGPL-3.0-only
package restapi

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"pimywa/internal/store"
)

// TestSetBossPrivileged covers the DoD directly: is_boss is settable via the
// privileged REST path (gated by the same API-key auth every endpoint here
// uses), and a request without the key is rejected and changes nothing.
func TestSetBossPrivileged(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	jid := "56955147132@s.whatsapp.net"
	if err := st.TouchChat(jid, "Boss", 1); err != nil {
		t.Fatal(err)
	}

	h := Handler(Deps{Store: st, APIKey: "secret"})

	// Without the key: rejected, nothing changes.
	req := httptest.NewRequest(http.MethodPost, "/api/chats/boss", strings.NewReader(`{"jid":"`+jid+`","boss":true}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("without API key: status = %d, want 401", rec.Code)
	}
	if c, _, _ := st.GetChat(jid); c.IsBoss {
		t.Fatal("is_boss was set despite the unauthorized request")
	}

	// With the key: succeeds, is_boss set.
	req = httptest.NewRequest(http.MethodPost, "/api/chats/boss", strings.NewReader(`{"jid":"`+jid+`","boss":true}`))
	req.Header.Set("X-API-Key", "secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("with API key: status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if c, ok, err := st.GetChat(jid); err != nil || !ok || !c.IsBoss {
		t.Fatalf("is_boss not set after privileged call: %+v ok=%v err=%v", c, ok, err)
	}
}

// TestSetChatRulesPrivileged covers the DoD directly: rules is settable via
// the privileged REST path only (same gate as is_boss), and a request
// without the key is rejected and changes nothing.
func TestSetChatRulesPrivileged(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	jid := "56999999999@s.whatsapp.net"
	if err := st.TouchChat(jid, "Contact", 1); err != nil {
		t.Fatal(err)
	}

	h := Handler(Deps{Store: st, APIKey: "secret"})

	// Without the key: rejected, nothing changes.
	req := httptest.NewRequest(http.MethodPost, "/api/chats/rules", strings.NewReader(`{"jid":"`+jid+`","rules":"tratarlo de usted"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("without API key: status = %d, want 401", rec.Code)
	}
	if c, _, _ := st.GetChat(jid); c.Rules != "" {
		t.Fatal("rules was set despite the unauthorized request")
	}

	// With the key: succeeds, rules set.
	req = httptest.NewRequest(http.MethodPost, "/api/chats/rules", strings.NewReader(`{"jid":"`+jid+`","rules":"tratarlo de usted"}`))
	req.Header.Set("X-API-Key", "secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("with API key: status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if c, ok, err := st.GetChat(jid); err != nil || !ok || c.Rules != "tratarlo de usted" {
		t.Fatalf("rules not set after privileged call: %+v ok=%v err=%v", c, ok, err)
	}
}

// TestSetChatDescriptionPrivileged covers the DoD directly (0130):
// description is settable via the privileged REST path only, and a request
// without the key is rejected and changes nothing.
func TestSetChatDescriptionPrivileged(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	jid := "12345-67890@g.us"
	if err := st.TouchChat(jid, "Group", 1); err != nil {
		t.Fatal(err)
	}

	h := Handler(Deps{Store: st, APIKey: "secret"})
	body := `{"jid":"` + jid + `","description":"el grupo de la oficina"}`

	// Without the key: rejected, nothing changes.
	req := httptest.NewRequest(http.MethodPost, "/api/chats/description", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("without API key: status = %d, want 401", rec.Code)
	}
	if c, _, _ := st.GetChat(jid); c.Description != "" {
		t.Fatal("description was set despite the unauthorized request")
	}

	// With the key: succeeds, description set.
	req = httptest.NewRequest(http.MethodPost, "/api/chats/description", strings.NewReader(body))
	req.Header.Set("X-API-Key", "secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("with API key: status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if c, ok, err := st.GetChat(jid); err != nil || !ok || c.Description != "el grupo de la oficina" {
		t.Fatalf("description not set after privileged call: %+v ok=%v err=%v", c, ok, err)
	}
}

// TestSetChatConfirmationPrivileged covers the DoD directly (0748/0810):
// confirmation_mode/confirmer are settable via the privileged REST path
// only, and a request without the key is rejected and changes nothing.
func TestSetChatConfirmationPrivileged(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	jid := "56999999999@s.whatsapp.net"
	if err := st.TouchChat(jid, "Contact", 1); err != nil {
		t.Fatal(err) // 1-1 chat: defaults to ConfirmationMode="none" (0810)
	}

	h := Handler(Deps{Store: st, APIKey: "secret"})
	body := `{"jid":"` + jid + `","mode":"required","confirmer":"56911112222@s.whatsapp.net"}`

	// Without the key: rejected, nothing changes.
	req := httptest.NewRequest(http.MethodPost, "/api/chats/confirmation", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("without API key: status = %d, want 401", rec.Code)
	}
	if c, _, _ := st.GetChat(jid); c.ConfirmationMode != "none" || c.Confirmer != "" {
		t.Fatalf("confirmation was set despite the unauthorized request: %+v", c)
	}

	// With the key: succeeds, mode + confirmer set (the owner overrides this 1-1
	// chat's default away from its type baseline).
	req = httptest.NewRequest(http.MethodPost, "/api/chats/confirmation", strings.NewReader(body))
	req.Header.Set("X-API-Key", "secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("with API key: status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if c, ok, err := st.GetChat(jid); err != nil || !ok || c.ConfirmationMode != "required" || c.Confirmer != "56911112222@s.whatsapp.net" {
		t.Fatalf("confirmation not set after privileged call: %+v ok=%v err=%v", c, ok, err)
	}

	// Invalid mode rejected.
	req = httptest.NewRequest(http.MethodPost, "/api/chats/confirmation", strings.NewReader(`{"jid":"`+jid+`","mode":"banana"}`))
	req.Header.Set("X-API-Key", "secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid mode: status = %d, want 400", rec.Code)
	}
}

// TestApproveDraftPrivileged covers the DoD: approving via the privileged
// REST path moves the draft into the outbox with its model; a request
// without the key is rejected and sends nothing.
func TestApproveDraftPrivileged(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	jid := "56999999999@s.whatsapp.net"
	if err := st.AddDraft(jid, "hola desde el auto-respondedor", "deepseek-chat", 1); err != nil {
		t.Fatal(err)
	}
	drafts, err := st.PendingDrafts(10)
	if err != nil || len(drafts) != 1 {
		t.Fatalf("setup: drafts=%+v err=%v", drafts, err)
	}
	id := drafts[0].ID

	h := Handler(Deps{Store: st, APIKey: "secret"})
	body := `{"id":` + strconv.FormatInt(id, 10) + `}`

	// Without the key: rejected, nothing sent.
	req := httptest.NewRequest(http.MethodPost, "/api/drafts/approve", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("without API key: status = %d, want 401", rec.Code)
	}
	if outbox, _ := st.PendingOutbox(10); len(outbox) != 0 {
		t.Fatal("draft was sent despite the unauthorized request")
	}

	// With the key: succeeds, draft lands in outbox with its model.
	req = httptest.NewRequest(http.MethodPost, "/api/drafts/approve", strings.NewReader(body))
	req.Header.Set("X-API-Key", "secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("with API key: status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	outbox, err := st.PendingOutbox(10)
	if err != nil || len(outbox) != 1 || outbox[0].ToJID != jid || outbox[0].Model != "deepseek-chat" {
		t.Fatalf("outbox after approve = %+v err=%v, want the draft's content", outbox, err)
	}
}

// TestDiscardDraftPrivileged covers "descartar no envía".
func TestDiscardDraftPrivileged(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	jid := "56999999999@s.whatsapp.net"
	if err := st.AddDraft(jid, "no enviar", "deepseek-chat", 1); err != nil {
		t.Fatal(err)
	}
	drafts, _ := st.PendingDrafts(10)
	id := drafts[0].ID

	h := Handler(Deps{Store: st, APIKey: "secret"})
	req := httptest.NewRequest(http.MethodPost, "/api/drafts/discard", strings.NewReader(`{"id":`+strconv.FormatInt(id, 10)+`}`))
	req.Header.Set("X-API-Key", "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if outbox, _ := st.PendingOutbox(10); len(outbox) != 0 {
		t.Fatal("discarded draft was sent — must never reach the outbox")
	}
}
