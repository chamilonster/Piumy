// SPDX-License-Identifier: AGPL-3.0-only
package restapi

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pimywa/internal/eventbus"
)

// TestEventsEndpointNilUnavailable covers the DoD's degrade-gracefully
// requirement: with no Bus wired, the endpoint 503s rather than hanging or
// panicking (same pattern as backup/mcp-guard's nil-dependency cases).
func TestEventsEndpointNilUnavailable(t *testing.T) {
	h := Handler(Deps{APIKey: "secret"})
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	req.Header.Set("X-API-Key", "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET /api/events with nil Bus: status = %d, want 503", rec.Code)
	}
}

// TestEventsEndpointRequiresAuth covers the same d.auth() gate every other
// endpoint here uses.
func TestEventsEndpointRequiresAuth(t *testing.T) {
	h := Handler(Deps{APIKey: "secret", Bus: eventbus.New()})
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/events without API key: status = %d, want 401", rec.Code)
	}
}

// TestEventsEndpointStreamsPublishedEvent drives a real SSE round-trip:
// subscribes via the actual HTTP handler (httptest.NewServer, a real
// listener — httptest.ResponseRecorder doesn't support streaming/flushing),
// publishes on the bus, and reads the "data: ..." line back off the wire.
func TestEventsEndpointStreamsPublishedEvent(t *testing.T) {
	bus := eventbus.New()
	h := Handler(Deps{APIKey: "secret", Bus: bus})
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events?key=secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	// Give the handler a moment to reach its Subscribe() call before
	// publishing — otherwise the event could fire before anyone's listening.
	time.Sleep(50 * time.Millisecond)
	bus.Publish(eventbus.Event{Type: "message", JID: "56911112222@s.whatsapp.net", TS: 123})

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			payload := strings.TrimPrefix(line, "data: ")
			if !strings.Contains(payload, `"jid":"56911112222@s.whatsapp.net"`) || !strings.Contains(payload, `"type":"message"`) {
				t.Errorf("event payload = %s, want type=message jid=56911112222@s.whatsapp.net", payload)
			}
			return
		}
	}
	t.Fatal("stream closed before a data: line arrived")
}

// TestEventsEndpointUnsubscribesOnDisconnect covers the leak guard directly
// ("un subscriber colgado no puede acumularse
// para siempre en un servicio de larga vida"): once the client disconnects,
// the handler's deferred unsubscribe must actually run, proven by the bus's
// subscriber count dropping back to 0.
func TestEventsEndpointUnsubscribesOnDisconnect(t *testing.T) {
	bus := eventbus.New()
	h := Handler(Deps{APIKey: "secret", Bus: bus})
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events?key=secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // let the handler reach Subscribe()
	if got := bus.Subscribers(); got != 1 {
		t.Fatalf("Subscribers() while connected = %d, want 1", got)
	}

	cancel() // client disconnects
	resp.Body.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if bus.Subscribers() == 0 {
			return // unsubscribe ran — success
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("Subscribers() never dropped to 0 after disconnect (leaked) — still %d", bus.Subscribers())
}
