package restapi

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
)

// resetRecoveryState guards every test in this file against cross-test
// pollution — recoveryActive is a package-level singleton (recover.go).
func resetRecoveryState(t *testing.T) {
	t.Helper()
	recoveryMu.Lock()
	recoveryActive = nil
	recoveryMu.Unlock()
	t.Cleanup(func() {
		recoveryMu.Lock()
		recoveryActive = nil
		recoveryMu.Unlock()
	})
}

var codePattern = regexp.MustCompile(`\b(\d{6})\b`)

func extractCode(t *testing.T, text string) string {
	t.Helper()
	m := codePattern.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("no 6-digit code found in %q", text)
	}
	return m[1]
}

func newTestState(t *testing.T, ownJID string) *state.Manager {
	t.Helper()
	sm := state.NewManager(t.TempDir()+"/status.json", 8)
	if err := sm.Update(func(s *state.Status) { s.OwnJID = ownJID }); err != nil {
		t.Fatal(err)
	}
	return sm
}

func TestRecoverGeneratesCodeAndEnqueuesToSelfAndBoss(t *testing.T) {
	resetRecoveryState(t)
	st := newTestStore(t)
	if err := st.SetIsBoss("55500000020@s.whatsapp.net", true); err != nil {
		t.Fatal(err)
	}
	// Own JID carries a device suffix, same shape whatsmeow's linked-device
	// identity actually has — must be stripped before landing in outbox.
	sm := newTestState(t, "55500000047:5@s.whatsapp.net")
	srv := httptest.NewServer(NewMux(Deps{Store: st, State: sm}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/auth/recover", map[string]any{"method": "whatsapp"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 {
		t.Fatalf("PendingOutbox = %d rows, want 2 (self + boss)", len(pending))
	}
	byJID := map[string]string{}
	for _, o := range pending {
		byJID[o.ToJID] = o.Text
	}
	if _, ok := byJID["55500000047@s.whatsapp.net"]; !ok {
		t.Errorf("no message enqueued to bare own JID (device suffix must be stripped): %+v", byJID)
	}
	if _, ok := byJID["55500000020@s.whatsapp.net"]; !ok {
		t.Errorf("no message enqueued to the is_boss chat: %+v", byJID)
	}
	code1 := extractCode(t, byJID["55500000047@s.whatsapp.net"])
	code2 := extractCode(t, byJID["55500000020@s.whatsapp.net"])
	if code1 != code2 {
		t.Errorf("self and boss got different codes: %q vs %q, want the same code", code1, code2)
	}
}

func TestRecoverCooldownSkipsSecondCode(t *testing.T) {
	resetRecoveryState(t)
	st := newTestStore(t)
	sm := newTestState(t, "55500000047@s.whatsapp.net")
	srv := httptest.NewServer(NewMux(Deps{Store: st, State: sm}))
	defer srv.Close()

	postJSON(t, srv.URL+"/api/auth/recover", map[string]any{"method": "whatsapp"}).Body.Close()
	postJSON(t, srv.URL+"/api/auth/recover", map[string]any{"method": "whatsapp"}).Body.Close()

	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("PendingOutbox = %d rows after 2 recover calls, want 1 (cooldown must skip the second)", len(pending))
	}
}

func TestRecoverAlwaysSameResponse(t *testing.T) {
	resetRecoveryState(t)
	st := newTestStore(t)
	// No State, no boss chats — nothing to send to, but the response must
	// look identical to the success case (no state leak).
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/auth/recover", map[string]any{"method": "whatsapp"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status with no recipients = %d, want 200 (same as success)", resp.StatusCode)
	}

	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("PendingOutbox = %d, want 0 — nothing to send to", len(pending))
	}
}

func TestRecoverVerifyResetsPasswordAndRotatesSecret(t *testing.T) {
	resetRecoveryState(t)
	st := newTestStore(t)
	sm := newTestState(t, "55500000047@s.whatsapp.net")
	srv := httptest.NewServer(NewMux(Deps{Store: st, State: sm}))
	defer srv.Close()

	postJSON(t, srv.URL+"/api/auth/recover", map[string]any{"method": "whatsapp"}).Body.Close()
	pending, err := st.PendingOutbox(10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("setup: PendingOutbox = %+v, err=%v, want 1 row", pending, err)
	}
	code := extractCode(t, pending[0].Text)

	beforeSecret, _ := st.KVGet(store.SettingDashSessionSecret)

	resp := postJSON(t, srv.URL+"/api/auth/recover/verify", map[string]any{"code": code, "new_password": "recovered-pass"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify status = %d, want 200", resp.StatusCode)
	}

	afterSecret, _ := st.KVGet(store.SettingDashSessionSecret)
	if beforeSecret == afterSecret {
		t.Error("session secret unchanged after recovery — old browser sessions would survive")
	}

	client := clientWithJar(t)
	oldLogin := loginAs(t, client, srv.URL, "admin", "piumy")
	defer oldLogin.Body.Close()
	if oldLogin.StatusCode != http.StatusUnauthorized {
		t.Errorf("login with old default password after recovery = %d, want 401", oldLogin.StatusCode)
	}
	client2 := clientWithJar(t)
	newLogin := loginAs(t, client2, srv.URL, "admin", "recovered-pass")
	defer newLogin.Body.Close()
	if newLogin.StatusCode != http.StatusOK {
		t.Errorf("login with recovered password = %d, want 200", newLogin.StatusCode)
	}
}

func TestRecoverVerifyWrongCodeFails(t *testing.T) {
	resetRecoveryState(t)
	st := newTestStore(t)
	sm := newTestState(t, "55500000047@s.whatsapp.net")
	srv := httptest.NewServer(NewMux(Deps{Store: st, State: sm}))
	defer srv.Close()

	postJSON(t, srv.URL+"/api/auth/recover", map[string]any{"method": "whatsapp"}).Body.Close()

	resp := postJSON(t, srv.URL+"/api/auth/recover/verify", map[string]any{"code": "000000", "new_password": "whatever-pass"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 on wrong code (or the 1-in-1e6 fluke matched — rerun)", resp.StatusCode)
	}
}

func TestConsumeRecoveryCodeExpired(t *testing.T) {
	resetRecoveryState(t)
	hash := bcryptHash(t, "123456")
	recoveryMu.Lock()
	recoveryActive = &recoveryCode{hash: hash, expiry: time.Now().Add(-time.Minute)}
	recoveryMu.Unlock()

	if consumeRecoveryCode("123456") {
		t.Error("consumeRecoveryCode succeeded on an expired code")
	}
}

func TestConsumeRecoveryCodeSingleUse(t *testing.T) {
	resetRecoveryState(t)
	hash := bcryptHash(t, "123456")
	recoveryMu.Lock()
	recoveryActive = &recoveryCode{hash: hash, expiry: time.Now().Add(recoveryCodeTTL)}
	recoveryMu.Unlock()

	if !consumeRecoveryCode("123456") {
		t.Fatal("first consumeRecoveryCode with the right code failed")
	}
	if consumeRecoveryCode("123456") {
		t.Error("consumeRecoveryCode succeeded twice on the same code — must be single-use")
	}
}

func TestConsumeRecoveryCodeAttemptCapBurnsCode(t *testing.T) {
	resetRecoveryState(t)
	hash := bcryptHash(t, "123456")
	recoveryMu.Lock()
	recoveryActive = &recoveryCode{hash: hash, expiry: time.Now().Add(recoveryCodeTTL)}
	recoveryMu.Unlock()

	for i := 0; i < recoveryMaxAttempts+1; i++ {
		consumeRecoveryCode("wrong-guess")
	}
	if consumeRecoveryCode("123456") {
		t.Error("consumeRecoveryCode succeeded with the right code after exceeding the attempt cap — must be burned")
	}
}

func bcryptHash(t *testing.T, code string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	return string(h)
}
