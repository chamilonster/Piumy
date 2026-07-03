// SPDX-License-Identifier: AGPL-3.0-only
package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pimywa/internal/store"
)

func TestNoneBridgeIsNoOp(t *testing.T) {
	dec, err := NoneBridge{}.Draft(context.Background(), nil, "some policy", ChatInfo{})
	if err != nil || dec.ShouldReply || dec.Draft != "" {
		t.Errorf("NoneBridge.Draft = (%+v, %v), want (zero Decision, nil)", dec, err)
	}
}

func TestBudgetHardCap(t *testing.T) {
	b := NewBudget(2)
	if !b.Allow() || !b.Allow() {
		t.Fatal("first two Allow() calls should succeed with max=2")
	}
	if b.Allow() {
		t.Fatal("third Allow() should fail — budget exhausted")
	}
	if b.Spent() != 2 {
		t.Errorf("Spent() = %d, want 2 (exhausted attempt doesn't count)", b.Spent())
	}
}

func TestNewDefaultsToNone(t *testing.T) {
	switch New(Config{Plugin: ""}).(type) {
	case NoneBridge:
	default:
		t.Error("New with empty/unrecognized Plugin should default to NoneBridge")
	}
	switch New(Config{Plugin: "typo"}).(type) {
	case NoneBridge:
	default:
		t.Error("New with an unrecognized Plugin should fail safe to NoneBridge, not error/crash")
	}
	switch New(Config{Plugin: "direct-api"}).(type) {
	case *DeepSeekBridge:
	default:
		t.Error("New with direct-api should build a *DeepSeekBridge")
	}
}

// fakeDeepSeekServer returns an httptest.Server that mimics the DeepSeek
// chat-completions response shape and counts requests it received.
func fakeDeepSeekServer(t *testing.T, decision draftDecision) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		content, err := json.Marshal(decision)
		if err != nil {
			t.Fatal(err)
		}
		resp := deepseekResponse{}
		resp.Choices = []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}{{Message: struct {
			Content string `json:"content"`
		}{Content: string(content)}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// TestDeepSeekBridgeParsesResponse covers the DoD's "cliente parsea la
// respuesta" — a mocked DeepSeek response round-trips into (draft, shouldReply).
func TestDeepSeekBridgeParsesResponse(t *testing.T) {
	srv, calls := fakeDeepSeekServer(t, draftDecision{ShouldReply: true, Draft: "claro, te ayudo"})

	d := &DeepSeekBridge{APIKey: "test-key", Endpoint: srv.URL, Budget: NewBudget(10)}
	dec, err := d.Draft(context.Background(), []store.Message{
		{FromMe: false, Text: "hola, necesito ayuda"},
	}, "responde con cortesía", ChatInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if !dec.ShouldReply || dec.Draft != "claro, te ayudo" {
		t.Errorf("Draft = %+v, want ShouldReply=true Draft=\"claro, te ayudo\"", dec)
	}
	if *calls != 1 {
		t.Errorf("server got %d calls, want 1", *calls)
	}
}

// TestDeepSeekBridgeBudgetCutsBeforeCalling covers the DoD's "el tope de
// gasto corta antes de llamar" — an exhausted budget must not reach the
// server at all.
func TestDeepSeekBridgeBudgetCutsBeforeCalling(t *testing.T) {
	srv, calls := fakeDeepSeekServer(t, draftDecision{ShouldReply: true, Draft: "should never be seen"})

	budget := NewBudget(1)
	budget.Allow() // exhaust it before the bridge ever gets a chance

	d := &DeepSeekBridge{APIKey: "test-key", Endpoint: srv.URL, Budget: budget}
	dec, err := d.Draft(context.Background(), []store.Message{{FromMe: false, Text: "hola"}}, "policy", ChatInfo{})
	if err != ErrBudgetExhausted {
		t.Errorf("err = %v, want ErrBudgetExhausted", err)
	}
	if dec.ShouldReply {
		t.Error("ShouldReply = true after budget exhaustion, want false")
	}
	if *calls != 0 {
		t.Errorf("server got %d calls, want 0 — budget must cut BEFORE the HTTP call", *calls)
	}
}

// TestDeepSeekBridgeKeyNeverLogged covers the DoD's "la key no aparece en
// logs": drive a real request (success and a server-error path) through
// Draft with the standard logger redirected to a buffer, and confirm the API
// key string never appears in it.
func TestDeepSeekBridgeKeyNeverLogged(t *testing.T) {
	const secretKey = "sk-super-secret-do-not-log-12345"

	var logBuf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(orig)

	okServer, _ := fakeDeepSeekServer(t, draftDecision{ShouldReply: false, Draft: ""})
	d := &DeepSeekBridge{APIKey: secretKey, Endpoint: okServer.URL, Budget: NewBudget(10)}
	if _, err := d.Draft(context.Background(), nil, "policy", ChatInfo{}); err != nil {
		t.Fatal(err)
	}

	errServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer errServer.Close()
	d2 := &DeepSeekBridge{APIKey: secretKey, Endpoint: errServer.URL, Budget: NewBudget(10)}
	_, _ = d2.Draft(context.Background(), nil, "policy", ChatInfo{}) // error expected, ignored

	if strings.Contains(logBuf.String(), secretKey) {
		t.Fatalf("API key leaked into logs: %s", logBuf.String())
	}
}

// TestDeepSeekBridgeIncludesChatInfo covers the DoD's "el Draft incluye
// memory+context+rules del chat" (0647): a request captured at the fake
// server must have all three in the system prompt sent to DeepSeek.
func TestDeepSeekBridgeIncludesChatInfo(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		resp := deepseekResponse{}
		resp.Choices = []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}{{Message: struct {
			Content string `json:"content"`
		}{Content: `{"should_reply":false,"draft":""}`}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	d := &DeepSeekBridge{APIKey: "test-key", Endpoint: srv.URL, Budget: NewBudget(10)}
	info := ChatInfo{
		Memory:  "le gusta el café",
		Context: "cliente frecuente hace 2 años",
		Rules:   "tratarlo siempre de usted",
	}
	if _, err := d.Draft(context.Background(), []store.Message{{FromMe: false, Text: "hola"}}, "responde con cortesía", info); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{info.Memory, info.Context, info.Rules} {
		if !strings.Contains(string(capturedBody), want) {
			t.Errorf("request body = %s, want it to contain %q", capturedBody, want)
		}
	}
}

// TestDeepSeekBridgeParsesConfirmation covers the DoD's confirmation half
// (0748/0800): needs_confirmation/confirmer from the model's response
// round-trip into Decision, same as should_reply/draft already do.
func TestDeepSeekBridgeParsesConfirmation(t *testing.T) {
	confirmFalse := false
	srv, _ := fakeDeepSeekServer(t, draftDecision{
		ShouldReply:       true,
		Draft:             "reviso el stock y te confirmo",
		NeedsConfirmation: &confirmFalse,
		Confirmer:         "56911112222@s.whatsapp.net",
	})

	d := &DeepSeekBridge{APIKey: "test-key", Endpoint: srv.URL, Budget: NewBudget(10)}
	dec, err := d.Draft(context.Background(), []store.Message{{FromMe: false, Text: "hay stock?"}}, "policy", ChatInfo{Rules: "si involucra stock, confirmar con el bodeguero 56911112222"})
	if err != nil {
		t.Fatal(err)
	}
	if dec.NeedsConfirmation || dec.Confirmer != "56911112222@s.whatsapp.net" {
		t.Errorf("Draft = %+v, want NeedsConfirmation=false (explicit) Confirmer=56911112222@s.whatsapp.net", dec)
	}
}

// TestDeepSeekBridgeConfirmationFailsSafe covers the DoD's fail-safe default
// (0810): a response that OMITS needs_confirmation entirely must fall back to
// THIS CHAT'S OWN baseline (ChatInfo.DefaultConfirm), never to a blind
// true/false guess independent of it.
func TestDeepSeekBridgeConfirmationFailsSafe(t *testing.T) {
	newOmittingServer := func(t *testing.T) *httptest.Server {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := deepseekResponse{}
			resp.Choices = []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{{Message: struct {
				Content string `json:"content"`
			}{Content: `{"should_reply":true,"draft":"ok"}`}}} // needs_confirmation omitted entirely
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		}))
		t.Cleanup(srv.Close)
		return srv
	}

	t.Run("group baseline (DefaultConfirm=true) falls back to true", func(t *testing.T) {
		d := &DeepSeekBridge{APIKey: "test-key", Endpoint: newOmittingServer(t).URL, Budget: NewBudget(10)}
		dec, err := d.Draft(context.Background(), nil, "policy", ChatInfo{DefaultConfirm: true})
		if err != nil {
			t.Fatal(err)
		}
		if !dec.NeedsConfirmation {
			t.Error("NeedsConfirmation = false, want true (falls back to the chat's own baseline)")
		}
	})

	t.Run("1-1 baseline (DefaultConfirm=false) falls back to false", func(t *testing.T) {
		d := &DeepSeekBridge{APIKey: "test-key", Endpoint: newOmittingServer(t).URL, Budget: NewBudget(10)}
		dec, err := d.Draft(context.Background(), nil, "policy", ChatInfo{DefaultConfirm: false})
		if err != nil {
			t.Fatal(err)
		}
		if dec.NeedsConfirmation {
			t.Error("NeedsConfirmation = true, want false (falls back to the chat's own baseline)")
		}
	})
}
