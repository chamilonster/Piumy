package restapi

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"piumy-gateway/internal/eventbus"
)

func TestEventsRequiresAPIKeyWhenSet(t *testing.T) {
	srv := httptest.NewServer(NewMux(Deps{Bus: eventbus.New(), APIKey: "secret"}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status without key = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestEventsUnavailableWithoutBus(t *testing.T) {
	srv := httptest.NewServer(NewMux(Deps{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status with nil Bus = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}
}

func TestEventsStreamsPublishedEvent(t *testing.T) {
	bus := eventbus.New()
	srv := httptest.NewServer(NewMux(Deps{Bus: bus}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events?key=", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// Give the handler a moment to Subscribe before publishing, otherwise
	// the event could fire before anyone's listening.
	time.Sleep(50 * time.Millisecond)
	bus.Publish(eventbus.Event{Type: "message", JID: "55500000002@c.us", TS: 1})

	reader := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(2 * time.Second)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("reading SSE stream: %v", err)
		}
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, "55500000002@c.us") {
			return // found it
		}
		if time.Now().After(deadline) {
			t.Fatal("never saw the published event on the SSE stream")
		}
	}
}
