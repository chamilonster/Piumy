package restapi

import (
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"testing"

	"piumy-gateway/internal/store"
)

// mockSMTP captures the single SendMail-shaped call recover.go makes, so
// tests never touch a real network — the contract's own DoD: "SMTP mockeado".
type mockSMTP struct {
	calls []mockSMTPCall
}

type mockSMTPCall struct {
	addr string
	from string
	to   []string
	msg  string
}

func (m *mockSMTP) send(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
	m.calls = append(m.calls, mockSMTPCall{addr: addr, from: from, to: to, msg: string(msg)})
	return nil
}

func smtpConfig() SMTPConfig {
	return SMTPConfig{Host: "smtp.example.com", Port: "587", User: "bot@example.com", Pass: "s3cr3t", From: "bot@example.com"}
}

func TestRecoverEmailGeneratesCodeAndSendsViaSMTP(t *testing.T) {
	resetRecoveryState(t)
	st := newTestStore(t)
	if err := st.KVSet(store.SettingDashRecoveryEmail, "boss@example.com"); err != nil {
		t.Fatal(err)
	}
	mock := &mockSMTP{}
	srv := httptest.NewServer(NewMux(Deps{Store: st, SMTP: smtpConfig(), SMTPSend: mock.send}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/auth/recover", map[string]any{"method": "email"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(mock.calls) != 1 {
		t.Fatalf("SMTPSend called %d times, want 1", len(mock.calls))
	}
	call := mock.calls[0]
	if len(call.to) != 1 || call.to[0] != "boss@example.com" {
		t.Errorf("to = %v, want [boss@example.com]", call.to)
	}
	if call.from != "bot@example.com" {
		t.Errorf("from = %q, want bot@example.com", call.from)
	}
	extractCode(t, call.msg) // fails the test if no 6-digit code is in the body
}

func TestRecoverEmailNoOpWithoutSMTPConfigured(t *testing.T) {
	resetRecoveryState(t)
	st := newTestStore(t)
	if err := st.KVSet(store.SettingDashRecoveryEmail, "boss@example.com"); err != nil {
		t.Fatal(err)
	}
	mock := &mockSMTP{}
	// SMTP.Host left empty — not configured.
	srv := httptest.NewServer(NewMux(Deps{Store: st, SMTPSend: mock.send}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/auth/recover", map[string]any{"method": "email"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (same generic response even when unconfigured)", resp.StatusCode)
	}
	if len(mock.calls) != 0 {
		t.Errorf("SMTPSend called %d times, want 0 — SMTP isn't configured", len(mock.calls))
	}
}

func TestRecoverEmailNoOpWithoutRecoveryEmailConfigured(t *testing.T) {
	resetRecoveryState(t)
	st := newTestStore(t)
	// No SettingDashRecoveryEmail set.
	mock := &mockSMTP{}
	srv := httptest.NewServer(NewMux(Deps{Store: st, SMTP: smtpConfig(), SMTPSend: mock.send}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/auth/recover", map[string]any{"method": "email"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (same generic response even when no recovery email is set)", resp.StatusCode)
	}
	if len(mock.calls) != 0 {
		t.Errorf("SMTPSend called %d times, want 0 — no recovery email configured", len(mock.calls))
	}
}

func TestRecoverEmailVerifyReusesS1e1Flow(t *testing.T) {
	resetRecoveryState(t)
	st := newTestStore(t)
	if err := st.KVSet(store.SettingDashRecoveryEmail, "boss@example.com"); err != nil {
		t.Fatal(err)
	}
	mock := &mockSMTP{}
	srv := httptest.NewServer(NewMux(Deps{Store: st, SMTP: smtpConfig(), SMTPSend: mock.send}))
	defer srv.Close()

	postJSON(t, srv.URL+"/api/auth/recover", map[string]any{"method": "email"}).Body.Close()
	if len(mock.calls) != 1 {
		t.Fatalf("setup: SMTPSend called %d times, want 1", len(mock.calls))
	}
	code := extractCode(t, mock.calls[0].msg)

	resp := postJSON(t, srv.URL+"/api/auth/recover/verify", map[string]any{"code": code, "new_password": "via-email-pass"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify status = %d, want 200", resp.StatusCode)
	}

	client := clientWithJar(t)
	newLogin := loginAs(t, client, srv.URL, "admin", "via-email-pass")
	defer newLogin.Body.Close()
	if newLogin.StatusCode != http.StatusOK {
		t.Errorf("login with recovered password = %d, want 200", newLogin.StatusCode)
	}
}

func TestRecoverCooldownSharedAcrossMethods(t *testing.T) {
	resetRecoveryState(t)
	st := newTestStore(t)
	sm := newTestState(t, "55500000047@s.whatsapp.net")
	if err := st.KVSet(store.SettingDashRecoveryEmail, "boss@example.com"); err != nil {
		t.Fatal(err)
	}
	mock := &mockSMTP{}
	srv := httptest.NewServer(NewMux(Deps{Store: st, State: sm, SMTP: smtpConfig(), SMTPSend: mock.send}))
	defer srv.Close()

	postJSON(t, srv.URL+"/api/auth/recover", map[string]any{"method": "whatsapp"}).Body.Close()
	postJSON(t, srv.URL+"/api/auth/recover", map[string]any{"method": "email"}).Body.Close()

	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Errorf("PendingOutbox = %d, want 1 (whatsapp fired first, email must hit the same cooldown)", len(pending))
	}
	if len(mock.calls) != 0 {
		t.Errorf("SMTPSend called %d times, want 0 — the active code came from the whatsapp request", len(mock.calls))
	}
}

func TestRecoverUnsupportedMethod(t *testing.T) {
	resetRecoveryState(t)
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/auth/recover", map[string]any{"method": "carrier-pigeon"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an unsupported method", resp.StatusCode)
	}
}
