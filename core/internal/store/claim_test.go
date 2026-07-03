// SPDX-License-Identifier: AGPL-3.0-only
package store

import (
	"path/filepath"
	"testing"
	"time"
)

func newClaimTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	jid := "56911112222@s.whatsapp.net"
	if err := st.TouchChat(jid, "Test", 1); err != nil {
		t.Fatal(err)
	}
	return st, jid
}

// TestClaimChatUnclaimedSucceeds covers the base case: an unclaimed chat can
// always be claimed.
func TestClaimChatUnclaimedSucceeds(t *testing.T) {
	st, jid := newClaimTestStore(t)
	ok, err := st.ClaimChat(jid, "claude-opus-4-8", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("claiming an unclaimed chat: want true")
	}
	c, _, err := st.GetChat(jid)
	if err != nil {
		t.Fatal(err)
	}
	if c.ClaimedBy != "claude-opus-4-8" || c.ClaimedUntil == 0 {
		t.Errorf("GetChat after claim = %+v, want claimed_by set and claimed_until > 0", c)
	}
}

// TestClaimChatBlocksDifferentModel covers the exclusivity guarantee: a
// second, different model cannot claim a chat another model already holds.
func TestClaimChatBlocksDifferentModel(t *testing.T) {
	st, jid := newClaimTestStore(t)
	if ok, err := st.ClaimChat(jid, "claude-opus-4-8", time.Minute); err != nil || !ok {
		t.Fatalf("1st claim: ok=%v err=%v, want true/nil", ok, err)
	}
	ok, err := st.ClaimChat(jid, "deepseek-chat", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("a different model claiming an already-held chat: want false")
	}
}

// TestClaimChatSameModelRenews covers idempotent renewal: re-claiming your
// own claim always succeeds and extends the TTL.
func TestClaimChatSameModelRenews(t *testing.T) {
	st, jid := newClaimTestStore(t)
	if ok, _ := st.ClaimChat(jid, "claude-opus-4-8", time.Second); !ok {
		t.Fatal("1st claim: want true")
	}
	ok, err := st.ClaimChat(jid, "claude-opus-4-8", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("re-claiming your own claim: want true (renew)")
	}
	c, _, _ := st.GetChat(jid)
	if c.ClaimedUntil < time.Now().Add(59*time.Minute).Unix() {
		t.Errorf("claimed_until after renew = %d, want ~1h out (renewed, not left at the original 1s)", c.ClaimedUntil)
	}
}

// TestClaimChatExpiredIsClaimable covers TTL expiry: once a claim's TTL has
// passed, ANY model — including a different one — can claim the chat.
func TestClaimChatExpiredIsClaimable(t *testing.T) {
	st, jid := newClaimTestStore(t)
	// A negative TTL claims "in the past" — simulates an already-expired
	// claim without a real sleep.
	if ok, err := st.ClaimChat(jid, "claude-opus-4-8", -time.Minute); err != nil || !ok {
		t.Fatalf("expired-on-arrival claim: ok=%v err=%v, want true/nil", ok, err)
	}
	ok, err := st.ClaimChat(jid, "deepseek-chat", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("claiming a chat whose prior claim already expired: want true")
	}
}

// TestGetChatReadsBackExpiredClaimAsUnclaimed covers effectiveClaim's whole
// purpose: a caller never compares timestamps itself — GetChat already
// reports an expired claim as no claim at all.
func TestGetChatReadsBackExpiredClaimAsUnclaimed(t *testing.T) {
	st, jid := newClaimTestStore(t)
	if ok, err := st.ClaimChat(jid, "claude-opus-4-8", -time.Minute); err != nil || !ok {
		t.Fatalf("expired-on-arrival claim: ok=%v err=%v", ok, err)
	}
	c, _, err := st.GetChat(jid)
	if err != nil {
		t.Fatal(err)
	}
	if c.ClaimedBy != "" || c.ClaimedUntil != 0 {
		t.Errorf("GetChat after expiry = claimed_by=%q claimed_until=%d, want both zero", c.ClaimedBy, c.ClaimedUntil)
	}
}

// TestReleaseChatOwnerOnly covers lock semantics: only the current holder's
// release actually clears the claim; a foreign release is a harmless no-op
// that must never steal/clear someone else's active claim.
func TestReleaseChatOwnerOnly(t *testing.T) {
	st, jid := newClaimTestStore(t)
	if ok, _ := st.ClaimChat(jid, "claude-opus-4-8", time.Minute); !ok {
		t.Fatal("claim: want true")
	}

	if err := st.ReleaseChat(jid, "deepseek-chat"); err != nil {
		t.Fatal(err)
	}
	c, _, _ := st.GetChat(jid)
	if c.ClaimedBy != "claude-opus-4-8" {
		t.Errorf("after a FOREIGN release: claimed_by = %q, want still claude-opus-4-8 (untouched)", c.ClaimedBy)
	}

	if err := st.ReleaseChat(jid, "claude-opus-4-8"); err != nil {
		t.Fatal(err)
	}
	c, _, _ = st.GetChat(jid)
	if c.ClaimedBy != "" {
		t.Errorf("after the OWNER's release: claimed_by = %q, want empty", c.ClaimedBy)
	}

	// A different model can now claim it — release truly cleared it, not
	// just changed to look empty via expiry.
	if ok, err := st.ClaimChat(jid, "deepseek-chat", time.Minute); err != nil || !ok {
		t.Fatalf("claim after release: ok=%v err=%v, want true/nil", ok, err)
	}
}

// TestClaimChatNonexistentChat covers that claiming a chat that was never
// touched affects 0 rows (the MCP tool layer is responsible for turning
// this into a clear "chat not found" instead of a misleading "claimed by
// someone else" — this test just pins the store-level contract it relies on).
func TestClaimChatNonexistentChat(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ok, err := st.ClaimChat("56900000000@s.whatsapp.net", "claude-opus-4-8", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("claiming a chat that was never TouchChat'd: want false (0 rows matched)")
	}
}

// TestPendingChatsReflectsClaim covers get_pending's visibility requirement:
// PendingChats surfaces the same effective claim state as
// GetChat, additive to its existing fields.
func TestPendingChatsReflectsClaim(t *testing.T) {
	st, jid := newClaimTestStore(t)
	if err := st.AddMessage(Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 100}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.ClaimChat(jid, "claude-opus-4-8", time.Minute); !ok {
		t.Fatal("claim: want true")
	}

	pending, err := st.PendingChats(20, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ClaimedBy != "claude-opus-4-8" {
		t.Fatalf("PendingChats = %+v, want exactly one entry with claimed_by=claude-opus-4-8", pending)
	}
}

// TestPendingChatsUnclaimedIsUnaffected covers the "solo agent" guarantee
// directly at the store layer: a chat nobody ever claimed reports the exact
// same empty/zero claim fields PendingChats always returned before this
// feature existed.
func TestPendingChatsUnclaimedIsUnaffected(t *testing.T) {
	st, jid := newClaimTestStore(t)
	if err := st.AddMessage(Message{ChatJID: jid, ID: "m1", FromMe: false, Text: "hola", TS: 100}); err != nil {
		t.Fatal(err)
	}
	pending, err := st.PendingChats(20, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ClaimedBy != "" || pending[0].ClaimedUntil != 0 {
		t.Fatalf("PendingChats with no claim ever made = %+v, want claimed_by/claimed_until empty/zero", pending)
	}
}
