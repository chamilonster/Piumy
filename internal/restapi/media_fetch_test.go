package restapi

import (
	"net/http/httptest"
	"testing"
)

// fakeMediaFetcher records every jid FetchPendingMedia was called with —
// a real MediaFetcher (whatsmeow.Adapter) needs a live client, out of scope
// for a restapi-only test.
type fakeMediaFetcher struct {
	calls []string
}

func (f *fakeMediaFetcher) FetchPendingMedia(chatJID string) {
	f.calls = append(f.calls, chatJID)
}

// TestFetchMediaEndpointTriggersAllSiblings is the regression test for
// ct-2026-07-29: the popup opens a chat by its DISPLAYED (dedup-winning
// real-number) jid, but pending media can be queued under a deduped-away
// @lid sibling — same gap TestMessagesEndpointMergesLIDSibling covers for
// GET /api/messages. POSTing with the real number must still kick off the
// fetch for its @lid sibling too.
func TestFetchMediaEndpointTriggersAllSiblings(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("111@lid", "Dana (lid)", 100); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat("222@s.whatsapp.net", "Dana", 50); err != nil {
		t.Fatal(err)
	}
	resolver := fakeLIDResolver{"111@lid": "222@s.whatsapp.net"}
	fetcher := &fakeMediaFetcher{}
	srv := httptest.NewServer(NewMux(Deps{Store: st, LIDResolver: resolver, MediaFetcher: fetcher}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/media/fetch", map[string]any{"chat_id": "222@s.whatsapp.net"})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	want := map[string]bool{"222@s.whatsapp.net": true, "111@lid": true}
	if len(fetcher.calls) != 2 {
		t.Fatalf("FetchPendingMedia calls = %v, want exactly 222@s.whatsapp.net and 111@lid", fetcher.calls)
	}
	for _, c := range fetcher.calls {
		if !want[c] {
			t.Errorf("unexpected FetchPendingMedia call for %q", c)
		}
	}
}

// TestFetchMediaEndpointNoSiblingIsUnaffected covers the common case (no
// @lid involved at all) — must still call FetchPendingMedia exactly once,
// with the given jid, same as before this fix.
func TestFetchMediaEndpointNoSiblingIsUnaffected(t *testing.T) {
	st := newTestStore(t)
	if err := st.TouchChat("1@c.us", "Ana", 100); err != nil {
		t.Fatal(err)
	}
	fetcher := &fakeMediaFetcher{}
	srv := httptest.NewServer(NewMux(Deps{Store: st, MediaFetcher: fetcher}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/media/fetch", map[string]any{"chat_id": "1@c.us"})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(fetcher.calls) != 1 || fetcher.calls[0] != "1@c.us" {
		t.Errorf("FetchPendingMedia calls = %v, want exactly [1@c.us]", fetcher.calls)
	}
}
