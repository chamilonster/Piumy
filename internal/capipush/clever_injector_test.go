package capipush

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCleverInjector_HandshakeEncryptPost drives the full happy path against
// a fake CleverCoder server: handshake -> key derivation -> AES-256-GCM
// encrypt -> POST /message. The server independently re-derives the key and
// opens the ciphertext, so a bug in deriveKey or the seal step fails here,
// not silently.
func TestCleverInjector_HandshakeEncryptPost(t *testing.T) {
	const terminalID = "term-guid-1"
	const pinpass = "s3cr3t-pinpass-base64=="
	const issuedToken = "0123456789abcdef0123456789abcdef"
	var gotEnvelope cleverEnvelope

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/handshake":
			var req struct {
				TerminalID string `json:"terminal_id"`
				Pinpass    string `json:"pinpass"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode handshake: %v", err)
			}
			if req.TerminalID != terminalID || req.Pinpass != pinpass {
				t.Errorf("handshake got terminal_id=%q pinpass=%q, want %q/%q", req.TerminalID, req.Pinpass, terminalID, pinpass)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": issuedToken})
		case "/message":
			var req struct {
				Token      string `json:"token"`
				Nonce      string `json:"nonce"`
				Ciphertext string `json:"ciphertext"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode message: %v", err)
			}
			if req.Token != issuedToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			key := deriveKey(pinpass, req.Token)
			nonce, err := base64.StdEncoding.DecodeString(req.Nonce)
			if err != nil {
				t.Fatalf("decode nonce: %v", err)
			}
			sealed, err := base64.StdEncoding.DecodeString(req.Ciphertext)
			if err != nil {
				t.Fatalf("decode ciphertext: %v", err)
			}
			block, err := aes.NewCipher(key)
			if err != nil {
				t.Fatalf("aes.NewCipher: %v", err)
			}
			gcm, err := cipher.NewGCM(block)
			if err != nil {
				t.Fatalf("cipher.NewGCM: %v", err)
			}
			plain, err := gcm.Open(nil, nonce, sealed, nil)
			if err != nil {
				t.Fatalf("server couldn't open what the client sealed: %v", err)
			}
			if err := json.Unmarshal(plain, &gotEnvelope); err != nil {
				t.Fatalf("unmarshal envelope: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	inj := NewCleverInjector(srv.URL, terminalID, pinpass)
	const opaquePayload = "opaque-dispatch-payload"
	const from = "55500000044, boss"
	if err := inj.Inject("some-piumy-routing-terminal-id", from, opaquePayload); err != nil {
		t.Fatalf("Inject: %v", err)
	}

	if gotEnvelope.Message != opaquePayload {
		t.Errorf("envelope.Message = %q, want the payload carried verbatim", gotEnvelope.Message)
	}
	// ct-2026-07-18-1851-B: SenderApp is fixed ("piumy|whatsapp"), From is the
	// caller-supplied dynamic numero/is_boss string — Inject has no opinion
	// on its content, just carries it through.
	if gotEnvelope.From != from {
		t.Errorf("envelope.From = %q, want the from Inject was called with (%q)", gotEnvelope.From, from)
	}
	if gotEnvelope.Header.SenderApp != "piumy|whatsapp" {
		t.Errorf("envelope.Header.SenderApp = %q, want piumy|whatsapp", gotEnvelope.Header.SenderApp)
	}
	if gotEnvelope.Header.Topic != "dispatch" {
		t.Errorf("envelope.Header.Topic = %q, want dispatch", gotEnvelope.Header.Topic)
	}
}

// TestCleverInjector_SetConfigResetsSession verifies that SetConfig clears the
// live session so the next Inject re-handshakes with the new credentials
// instead of carrying a stale token from the old config.
func TestCleverInjector_SetConfigResetsSession(t *testing.T) {
	inj := NewCleverInjector("http://old:8787", "old-term", "old-pin")
	inj.token = "old-token"
	inj.key = []byte("0123456789abcdef0123456789abcdef")

	inj.SetConfig("http://new:8787", "new-term", "new-pin")

	if inj.Endpoint != "http://new:8787" || inj.TerminalID != "new-term" || inj.Pinpass != "new-pin" {
		t.Errorf("credentials not updated: %q / %q / %q", inj.Endpoint, inj.TerminalID, inj.Pinpass)
	}
	if inj.token != "" || inj.key != nil {
		t.Errorf("session not cleared after SetConfig: token=%q key=%v", inj.token, inj.key)
	}
}

// TestCleverInjector_ReHandshakeOn401 covers protocol §5: a 401 on /message
// (a token the server no longer recognizes) must trigger exactly one
// re-handshake, then a retry that succeeds with the fresh token.
func TestCleverInjector_ReHandshakeOn401(t *testing.T) {
	const terminalID = "term-guid-2"
	const pinpass = "another-pinpass=="
	const staleToken = "stale0000000000000000000000000000"
	const freshToken = "fresh0000000000000000000000000000"
	handshakes := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/handshake":
			handshakes++
			_ = json.NewEncoder(w).Encode(map[string]string{"token": freshToken})
		case "/message":
			var req struct {
				Token string `json:"token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Token != freshToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	inj := NewCleverInjector(srv.URL, terminalID, pinpass)
	// Pre-seed a stale session, as if this injector handshook a while ago and
	// the antenna no longer recognizes the token (antenna cycled, or the
	// session simply expired) — the first /message attempt must 401.
	inj.token = staleToken
	inj.key = deriveKey(pinpass, staleToken)

	if err := inj.Inject("piumy-routing-terminal-id", "55500000044, boss", "ciphertext"); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if handshakes != 1 {
		t.Errorf("handshake calls = %d, want exactly 1 (only triggered by the 401)", handshakes)
	}
	if inj.token != freshToken {
		t.Errorf("injector session token = %q, want it to have adopted the fresh token %q", inj.token, freshToken)
	}
}

// handshakeErrorServer builds a fake CleverCoder that always fails the
// handshake with the given code (T32, ct-2026-08-06-1109 — protocol §2,
// ct-2026-08-06-0221's three codes). "" simulates an older CleverCoder
// (pre v1.6.68.191) with no "error" field at all.
func handshakeErrorServer(t *testing.T, code string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/handshake" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNotFound)
		if code != "" {
			_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
		}
	}))
}

// TestCleverInjector_TerminalGoneMarksDead is T32's PERMANENT case: the id
// will never resolve, so Inject must both report it (via errTerminalGone,
// errors.Is-checkable by capipush.dispatch) and discard the credential —
// Configured() flips to false so no future sweep even tries again.
func TestCleverInjector_TerminalGoneMarksDead(t *testing.T) {
	srv := handshakeErrorServer(t, "terminal_gone")
	defer srv.Close()

	inj := NewCleverInjector(srv.URL, "some-old-guid", "pin")
	if !inj.Configured() {
		t.Fatal("setup: injector should start Configured (has an endpoint)")
	}

	err := inj.Inject("routing-id", "55500000045, boss", "payload")
	if !errors.Is(err, errTerminalGone) {
		t.Fatalf("Inject error = %v, want errors.Is(err, errTerminalGone)", err)
	}
	if inj.Configured() {
		t.Error("Configured() = true after terminal_gone, want false — the credential must be discarded")
	}
}

// TestCleverInjector_AntennaOffStaysConfigured and
// TestCleverInjector_PositionEmptyStaysConfigured are T32's TRANSIENT cases:
// the id IS known, nothing's there right now — must NOT discard the
// credential, so the next sweep tries again on its own (capipush's existing
// unbounded-retry-on-delivery-failure behavior, untouched by this contract).
func TestCleverInjector_AntennaOffStaysConfigured(t *testing.T) {
	testHandshakeCodeStaysConfigured(t, "antenna_off")
}

func TestCleverInjector_PositionEmptyStaysConfigured(t *testing.T) {
	testHandshakeCodeStaysConfigured(t, "position_empty")
}

// TestCleverInjector_UnknownCodeStaysConfigured covers compat requirement #2
// (T32's contract, point "Compatibilidad hacia atrás"): a code this side
// doesn't recognize — an older CleverCoder's collapsed terminal_not_listening,
// or a future code not in the table yet — must be treated as transient, not
// break anything. Same assertion as the empty-body (very old server) case
// below: neither discards the credential.
func TestCleverInjector_UnknownCodeStaysConfigured(t *testing.T) {
	testHandshakeCodeStaysConfigured(t, "terminal_not_listening")
}

// TestCleverInjector_NoErrorBodyStaysConfigured is the oldest-server case:
// a 404 with no JSON body at all (pre-dates the "error" field existing).
// Must behave exactly as it did before this contract — generic error,
// credential untouched.
func TestCleverInjector_NoErrorBodyStaysConfigured(t *testing.T) {
	testHandshakeCodeStaysConfigured(t, "")
}

func testHandshakeCodeStaysConfigured(t *testing.T, code string) {
	t.Helper()
	srv := handshakeErrorServer(t, code)
	defer srv.Close()

	inj := NewCleverInjector(srv.URL, "some-id", "pin")
	err := inj.Inject("routing-id", "55500000045, boss", "payload")
	if err == nil {
		t.Fatal("Inject = nil error, want a handshake failure")
	}
	if errors.Is(err, errTerminalGone) {
		t.Errorf("Inject error = %v, want NOT errTerminalGone (code %q is transient)", err, code)
	}
	if !inj.Configured() {
		t.Errorf("Configured() = false after a transient handshake failure (code %q), want true — must keep retrying on its own", code)
	}
}

// TestCleverInjector_SetConfigRevivesDeadCredential confirms the only way
// out T32 gives a dead injector: a fresh SetConfig (re-running
// set_capi_connector/register_agent with new credentials) clears dead, same
// as it already clears the stale token/key.
func TestCleverInjector_SetConfigRevivesDeadCredential(t *testing.T) {
	inj := NewCleverInjector("http://old:8787", "old-term", "old-pin")
	inj.markDead()
	if inj.Configured() {
		t.Fatal("setup: injector should be dead (not Configured) before SetConfig")
	}

	inj.SetConfig("http://new:8787", "new-term", "new-pin")

	if !inj.Configured() {
		t.Error("Configured() = false after SetConfig, want true — a fresh credential revives it")
	}
}
