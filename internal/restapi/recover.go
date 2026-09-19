// Password recovery via WhatsApp (ct-2026-07-19-1652, S1e-1) and email
// (ct-2026-07-19-1716, S1e-2) — two delivery channels, ONE code/verify
// flow: generation, in-memory bcrypt hash, TTL, attempt cap, and
// RotateDashSessionSecret on success are all channel-agnostic (S1d
// dependency: reuses the same bcrypt hash a normal password change uses,
// so a completed recovery is indistinguishable from a normal change to
// every other part of the system). Only tryStartRecovery's deliver
// callback differs per method.
package restapi

import (
	"crypto/rand"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"piumy-gateway/internal/i18n"
	"piumy-gateway/internal/store"
)

const (
	recoveryCodeTTL     = 10 * time.Minute
	recoveryMaxAttempts = 10
)

// recoveryCode is deliberately process-memory-only, never persisted (the
// contract's own security bullet: "hasheado o en memoria con TTL, no en
// claro persistente") — a lost gateway process just means the boss
// re-requests a code, no worse than the 10-minute TTL already implies.
type recoveryCode struct {
	hash     string
	expiry   time.Time
	used     bool
	attempts int
}

// ponytail: package-level singleton, not a Deps field — this state is
// inherently one-per-process (single admin account, no multi-tenancy), and
// a Deps field would need new main.go wiring for zero benefit. Guarded by
// recoveryMu because two /api/auth/recover(/verify) requests can race.
var (
	recoveryMu     sync.Mutex
	recoveryActive *recoveryCode
)

func (d Deps) registerRecoverRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/recover", d.handleRecover)
	mux.HandleFunc("POST /api/auth/recover/verify", d.handleRecoverVerify)
}

// handleRecover ALWAYS responds identically regardless of what actually
// happened internally (contract: "no reveles si hay sesión/estado") — no
// store, no recipients/email configured, cooldown active, send failure, all
// look the same from the outside. Not behind d.auth: reachable with no
// session by definition (that's the whole point of a recovery flow).
func (d Deps) handleRecover(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Method string `json:"method"`
	}
	if !decode(w, r, &body) {
		return
	}
	switch body.Method {
	case "whatsapp":
		if jids := d.recoveryRecipients(); len(jids) > 0 {
			d.tryStartRecovery(func(code string) { d.deliverRecoveryWhatsApp(jids, code) })
		}
	case "email":
		if to := d.recoveryEmailAddress(); to != "" && d.smtpConfigured() {
			d.tryStartRecovery(func(code string) { d.deliverRecoveryEmail(to, code) })
		}
	default:
		// Untranslated on purpose (T153 etapa 3b, ct-2026-09-16-1828): the UI
		// only ever POSTs "whatsapp" or "email" (two hardcoded buttons) — this
		// branch is an API-contract violation reachable only by calling the
		// endpoint directly, same class as "jid is required" elsewhere. And
		// even if it somehow reached the browser, requestRecoveryCode()'s
		// .catch() shows the SAME t("auth.code_sent_generic") as success, by
		// design (never reveal whether a recovery method is configured) — see
		// error_literals_test.go's exception list.
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported recovery method"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "si corresponde, te enviamos un código"})
}

// tryStartRecovery generates a code and hands it to deliver, unless one is
// already active (the contract's cooldown: "un código activo por vez") —
// checked BEFORE generating, so a deliverability check that already failed
// (handleRecover's caller-side jids/to guard) never burns the cooldown
// window on a code nobody will receive.
func (d Deps) tryStartRecovery(deliver func(code string)) {
	recoveryMu.Lock()
	if recoveryActive != nil && !recoveryActive.used && time.Now().Before(recoveryActive.expiry) {
		recoveryMu.Unlock()
		return
	}
	recoveryMu.Unlock()

	code, err := generateRecoveryCode()
	if err != nil {
		log.Printf("restapi: recover: generate code: %v", err)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("restapi: recover: hash code: %v", err)
		return
	}

	recoveryMu.Lock()
	recoveryActive = &recoveryCode{hash: string(hash), expiry: time.Now().Add(recoveryCodeTTL)}
	recoveryMu.Unlock()

	deliver(code)
}

// deliverRecoveryWhatsApp enqueues the code to every recipient — store.Enqueue
// only queues the row, corepipeline.processOutbox is the one drain loop that
// actually sends, and it already respects the governor/kill-switch/human-pacing
// anti-ban gates. Nothing here bypasses that; the code just rides the same
// queue send_message does.
func (d Deps) deliverRecoveryWhatsApp(jids []string, code string) {
	text := i18n.T(effectiveLang(d.Store), "server.recovery_code", "code", code)
	now := time.Now().Unix()
	for _, jid := range jids {
		if err := d.Store.Enqueue(jid, text, now); err != nil {
			log.Printf("restapi: recover: enqueue to %s: %v", jid, err)
		}
	}
}

// deliverRecoveryEmail sends the code via the boss's own SMTP relay
// (d.SMTP). d.SMTPSend defaults to smtp.SendMail (stdlib, net/smtp) when
// nil — overridable in tests without a real TCP server.
func (d Deps) deliverRecoveryEmail(to, code string) {
	lang := effectiveLang(d.Store)
	subject := i18n.T(lang, "server.recovery_email_subject")
	body := i18n.T(lang, "server.recovery_email_body", "code", code)
	msg := []byte("From: " + d.SMTP.From + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"\r\n" + body)

	var auth smtp.Auth
	if d.SMTP.User != "" {
		auth = smtp.PlainAuth("", d.SMTP.User, d.SMTP.Pass, d.SMTP.Host)
	}
	send := d.SMTPSend
	if send == nil {
		send = smtp.SendMail
	}
	addr := d.SMTP.Host + ":" + d.SMTP.Port
	if err := send(addr, auth, d.SMTP.From, []string{to}, msg); err != nil {
		log.Printf("restapi: recover: send email: %v", err)
	}
}

// recoveryRecipients is the gateway's own number (state.OwnJID) plus every
// chat marked is_boss (store.BossJIDs) — deduped, and normalized to a bare
// JID: OwnJID comes from whatsmeow's own linked-device identity
// (client.Store.ID.String()) which — unlike every JID already stored in
// chats.jid — can carry a ":<device>" suffix (this gateway links as a
// companion device, never device 0), so it needs the same stripping
// inbound.go's LID resolution already does via ToNonAD before it's usable
// as a send target.
func (d Deps) recoveryRecipients() []string {
	if d.Store == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(jid string) {
		jid = bareJID(jid)
		if jid == "" || seen[jid] {
			return
		}
		seen[jid] = true
		out = append(out, jid)
	}
	if d.State != nil {
		add(d.State.Snapshot().OwnJID)
	}
	if bossJIDs, err := d.Store.BossJIDs(); err != nil {
		log.Printf("restapi: recover: BossJIDs: %v", err)
	} else {
		for _, jid := range bossJIDs {
			add(jid)
		}
	}
	return out
}

// recoveryEmailAddress returns the boss-configured recovery email, or "" if
// unset or the store isn't available — handleRecover treats that exactly
// like recoveryRecipients returning nothing: no code generated, same
// generic response regardless.
func (d Deps) recoveryEmailAddress() string {
	if d.Store == nil {
		return ""
	}
	email, _ := d.Store.KVGet(store.SettingDashRecoveryEmail)
	return email
}

// smtpConfigured is true only once the boss has provided a mail relay.
func (d Deps) smtpConfigured() bool {
	return d.SMTP.Host != ""
}

// bareJID strips a whatsmeow ":<device>" suffix, if present, leaving the
// same non-AD form every other JID in this codebase already uses (chats.jid,
// IsBoss, ...). No dependency on whatsmeow/types needed for this one string op.
func bareJID(jid string) string {
	user, server, ok := strings.Cut(jid, "@")
	if !ok {
		return jid
	}
	if i := strings.IndexByte(user, ':'); i != -1 {
		user = user[:i]
	}
	return user + "@" + server
}

func generateRecoveryCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// handleRecoverVerify — also not behind d.auth: the caller has no session
// yet, that's the scenario. current_password is deliberately NOT required
// here (unlike POST /api/admin/password) — the whole point of recovery is
// not knowing it; the WhatsApp-delivered code IS the proof of ownership.
func (d Deps) handleRecoverVerify(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		Code        string `json:"code"`
		NewPassword string `json:"new_password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !consumeRecoveryCode(body.Code) {
		// "invalid or expired code" stays a bare literal on purpose (same
		// reasoning as "invalid credentials" in auth.go): recover_submit()'s
		// .catch() in app.js unconditionally shows t("auth.invalid_or_expired_code")
		// for ANY failure of this endpoint — this text, translated or not,
		// never reaches the screen. See error_literals_test.go's exception list.
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired code"})
		return
	}
	if len(body.NewPassword) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(effectiveLang(d.Store), "server.new_password_required")})
		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := d.Store.KVSet(store.SettingDashPassHash, string(newHash)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := d.Store.RotateDashSessionSecret(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "password reset"})
}

// consumeRecoveryCode validates code against the active recovery code —
// present, not expired, not already used, under the attempt cap — and
// marks it used on a match so it can never be replayed. A WRONG guess still
// counts against the cap (and burns the code outright once the cap is hit,
// forcing a fresh /api/auth/recover request) so the endpoint can't be
// brute-forced through all 10^6 codes inside the 10-minute window.
func consumeRecoveryCode(code string) bool {
	recoveryMu.Lock()
	defer recoveryMu.Unlock()
	c := recoveryActive
	if c == nil || c.used || time.Now().After(c.expiry) {
		return false
	}
	c.attempts++
	if c.attempts > recoveryMaxAttempts {
		c.used = true
		return false
	}
	if bcrypt.CompareHashAndPassword([]byte(c.hash), []byte(code)) != nil {
		return false
	}
	c.used = true
	return true
}
