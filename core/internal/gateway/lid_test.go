// SPDX-License-Identifier: AGPL-3.0-only
package gateway

import (
	"context"
	"errors"
	"testing"

	"go.mau.fi/whatsmeow/types"
)

var (
	lidJID = types.JID{User: "248506891690190", Server: types.HiddenUserServer}
	pnJID  = types.JID{User: "56955147132", Server: types.DefaultUserServer}
)

// TestResolvePNPassesThroughNonLID covers the common case (the vast majority
// of JIDs) without needing a live whatsmeow client — resolvePN must not even
// touch g.client for a non-LID JID.
func TestResolvePNPassesThroughNonLID(t *testing.T) {
	g := &Gateway{} // client is nil — would panic if resolvePN touched it
	got := g.resolvePN(context.Background(), pnJID)
	if got != pnJID {
		t.Errorf("resolvePN(non-LID) = %v, want unchanged %v", got, pnJID)
	}
}

// TestPNFromLIDLookup covers the fallback decision the bug fix hinges on:
// mapping found → use it; not found or error → fall back to the LID as-is
// (never block/crash on an unmapped contact).
func TestPNFromLIDLookup(t *testing.T) {
	t.Run("mapping found", func(t *testing.T) {
		if got := pnFromLIDLookup(lidJID, pnJID, nil); got != pnJID {
			t.Errorf("got %v, want the resolved PN %v", got, pnJID)
		}
	})
	t.Run("not found (empty result, no error)", func(t *testing.T) {
		if got := pnFromLIDLookup(lidJID, types.JID{}, nil); got != lidJID {
			t.Errorf("got %v, want the original LID %v (no mapping learned yet)", got, lidJID)
		}
	})
	t.Run("lookup error", func(t *testing.T) {
		if got := pnFromLIDLookup(lidJID, types.JID{}, errors.New("db error")); got != lidJID {
			t.Errorf("got %v, want the original LID %v (fail open, not closed)", got, lidJID)
		}
	})
}
