// ST-E (ct-2026-07-11-1444): group_tools.go never had its own test file —
// every existing test only exercised the "GroupProfile nil" refusal path
// (see admin_tools_test.go). fakeGroupProfile lets these tests drive the
// tools' OWN logic (JSON shape, the data-URL decode step, error
// propagation) without a live WhatsApp session.
package mcpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"go.mau.fi/whatsmeow/types"

	"piumy-gateway/internal/governor"
	"piumy-gateway/internal/router"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
)

type fakeGroupProfile struct {
	err error // if set, every method returns this instead of succeeding

	lastCreateGroupName         string
	lastCreateGroupParticipants []string
	// createGroupJID/createGroupParticipants (T135, ct-2026-09-03-1546):
	// optional overrides for CreateGroup's returned *types.GroupInfo — see
	// CreateGroup's own doc.
	createGroupJID          types.JID
	createGroupParticipants []types.GroupParticipant
	lastPromoteGroup        string
	lastPromoteParticipants []string
	promoteErr              error // independent of err — see PromoteParticipants's own doc
	// promoteFailCount/promoteFailErr (T145, ct-2026-09-07): the first N
	// calls fail with promoteFailErr, then calls succeed — simulates the
	// live hypothesis A shape (a 403 fresh off group creation that clears
	// on retry), independent of promoteErr (which fails EVERY call,
	// unconditionally — the pre-existing "it never recovers" shape).
	// promoteCalls counts every PromoteParticipants call this fake has
	// seen, so a test can assert exactly how many attempts happened.
	promoteFailCount int
	promoteFailErr   error
	promoteCalls     int
	// promoteResult (T136, ct-2026-09-03-1627): optional override for
	// PromoteParticipants's returned participants — lets a test simulate
	// WhatsApp's per-participant Error code (a call that succeeds at the
	// transport level but rejects one target, e.g. promoting someone who
	// isn't actually in the group) without an outer Go error, which
	// promoteErr already covers for a transport failure.
	promoteResult           []types.GroupParticipant
	lastAddParticipantGroup string
	lastAddParticipantID    string
	lastSetGroupPhotoGroup  string
	lastSetGroupPhotoBytes  []byte
	lastSetGroupDescGroup   string
	lastSetGroupDescText    string
	lastSetProfileStatus    string
	// setProfilePhotoCalled / lastSetProfilePhotoBytes (T111, ct-2026-09-01-1442):
	// lastSetProfilePhotoBytes alone can't tell "never called" apart from
	// "called with nil to remove the photo" — both are a nil slice.
	setProfilePhotoCalled    bool
	lastSetProfilePhotoBytes []byte
	getProfileStatus         string // value GetProfileStatus returns (T103)
	getProfileStatusErr      error  // error GetProfileStatus returns (T103) — independent of err above
	// blockUntilCtxDone (Citrino's audit, T104/T103) simulates the real
	// whatsmeow.GetUserInfo call over a dead connection: it never returns on
	// its own, only when the ctx it was given is cancelled — proving the
	// call SITE enforces a timeout, not just that the mock happens to be fast.
	blockUntilCtxDone bool

	mu                    sync.Mutex
	getProfileStatusCalls int
}

func (f *fakeGroupProfile) CreateGroup(ctx context.Context, name string, participantJIDs []string) (*types.GroupInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.lastCreateGroupName = name
	f.lastCreateGroupParticipants = participantJIDs
	// createGroupJID/createGroupParticipants (T135, ct-2026-09-03-1546):
	// optional overrides — the zero value (empty JID, nil Participants)
	// reproduces this fake's exact pre-T135 return shape, so every
	// existing test that never sets them is untouched.
	return &types.GroupInfo{JID: f.createGroupJID, GroupName: types.GroupName{Name: name}, Participants: f.createGroupParticipants}, nil
}

func (f *fakeGroupProfile) AddParticipant(ctx context.Context, groupJID, participantJID string) ([]types.GroupParticipant, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.lastAddParticipantGroup = groupJID
	f.lastAddParticipantID = participantJID
	return []types.GroupParticipant{{}}, nil
}

// PromoteParticipants (T135, ct-2026-09-03-1546) — promoteErr is
// INDEPENDENT of f.err on purpose: a test needs to simulate "the group was
// created fine, but promoting the owner failed", which f.err alone can't
// express (it would also fail CreateGroup itself).
func (f *fakeGroupProfile) PromoteParticipants(ctx context.Context, groupJID string, participantJIDs []string) ([]types.GroupParticipant, error) {
	f.lastPromoteGroup = groupJID
	f.lastPromoteParticipants = participantJIDs
	f.promoteCalls++
	if f.promoteFailCount > 0 && f.promoteCalls <= f.promoteFailCount {
		return nil, f.promoteFailErr
	}
	if f.promoteErr != nil {
		return nil, f.promoteErr
	}
	return f.promoteResult, nil
}

func (f *fakeGroupProfile) SetGroupPhoto(ctx context.Context, groupJID string, jpeg []byte) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.lastSetGroupPhotoGroup = groupJID
	f.lastSetGroupPhotoBytes = jpeg
	return "photo-id", nil
}

func (f *fakeGroupProfile) SetProfilePhoto(ctx context.Context, jpeg []byte) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.setProfilePhotoCalled = true
	f.lastSetProfilePhotoBytes = jpeg
	if jpeg == nil {
		return "remove", nil
	}
	return "photo-id", nil
}

func (f *fakeGroupProfile) SetGroupDescription(ctx context.Context, groupJID, description string) error {
	if f.err != nil {
		return f.err
	}
	f.lastSetGroupDescGroup = groupJID
	f.lastSetGroupDescText = description
	return nil
}

func (f *fakeGroupProfile) SetProfileStatus(ctx context.Context, status string) error {
	if f.err != nil {
		return f.err
	}
	f.lastSetProfileStatus = status
	return nil
}

func (f *fakeGroupProfile) GetProfileStatus(ctx context.Context) (string, error) {
	f.mu.Lock()
	f.getProfileStatusCalls++
	f.mu.Unlock()
	if f.blockUntilCtxDone {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if f.getProfileStatusErr != nil {
		return "", f.getProfileStatusErr
	}
	return f.getProfileStatus, nil
}

func (f *fakeGroupProfile) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.getProfileStatusCalls
}

// serverWithGroupProfile builds a boss-dispatched server wired to fgp —
// same shape as helpers_test.go's newTestServer/bossDispatchContext, just
// with GroupProfile set (those helpers don't expose that field).
func serverWithGroupProfile(t *testing.T, fgp *fakeGroupProfile) (context.Context, *server.MCPServer) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	rtMgr := router.NewManager(filepath.Join(dir, "router.json"))
	sm := state.NewManager(filepath.Join(dir, "status.json"), 8)
	gate := NewGate()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	srv := New(ctx, Deps{Store: st, State: sm, Router: rtMgr, AgentIdle: time.Minute, Gate: gate, GroupProfile: fgp})
	chat := "55500000066@c.us"
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	return termCtx, srv
}

// serverWithGroupProfileAndStore is serverWithGroupProfile plus the
// *store.Store handle (T135, ct-2026-09-03-1546) — a separate constructor,
// not a signature change, so every existing serverWithGroupProfile call
// site stays untouched. T135's own tests need st to seed is_boss and to
// verify what create_group actually persisted.
func serverWithGroupProfileAndStore(t *testing.T, fgp *fakeGroupProfile) (*store.Store, context.Context, *server.MCPServer) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	rtMgr := router.NewManager(filepath.Join(dir, "router.json"))
	sm := state.NewManager(filepath.Join(dir, "status.json"), 8)
	gate := NewGate()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	srv := New(ctx, Deps{Store: st, State: sm, Router: rtMgr, AgentIdle: time.Minute, Gate: gate, GroupProfile: fgp})
	chat := "55500000066@c.us"
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	return st, termCtx, srv
}

// fastPromoteRetries (T145, ct-2026-09-07) overrides promoteRetryWindow to
// a near-zero window for the duration of the test — group_tools.go's own
// retry loop runs for real (not bypassed, not mocked), just without a test
// actually waiting seconds per attempt. Restored on cleanup so other tests
// never see the override.
func fastPromoteRetries(t *testing.T) {
	t.Helper()
	orig := promoteRetryWindow
	promoteRetryWindow = governor.DelayWindow{Min: time.Microsecond, Max: 2 * time.Microsecond}
	t.Cleanup(func() { promoteRetryWindow = orig })
}

// fastGroupActionSpacing (T149, ct-2026-09-07-1730) overrides
// groupActionSpacing to a small-but-measurable window for the duration of
// the test — group_tools.go's own pacer runs for real (not bypassed, not
// mocked), just fast enough that a test proving it waits doesn't actually
// burn seconds. Restored on cleanup, same pattern as fastPromoteRetries.
func fastGroupActionSpacing(t *testing.T) {
	t.Helper()
	orig := groupActionSpacing
	groupActionSpacing = governor.DelayWindow{Min: 30 * time.Millisecond, Max: 60 * time.Millisecond}
	t.Cleanup(func() { groupActionSpacing = orig })
}

func TestCreateGroupCallsGroupProfileAndReturnsJSON(t *testing.T) {
	fgp := &fakeGroupProfile{}
	ctx, srv := serverWithGroupProfile(t, fgp)

	out := callTool(t, ctx, srv, "create_group", map[string]any{
		"name": "Asado del viernes", "participants": []string{"111@c.us", "222@c.us"},
	})
	if fgp.lastCreateGroupName != "Asado del viernes" {
		t.Errorf("CreateGroup name = %q, want %q", fgp.lastCreateGroupName, "Asado del viernes")
	}
	if len(fgp.lastCreateGroupParticipants) != 2 {
		t.Errorf("CreateGroup participants = %v, want 2 entries", fgp.lastCreateGroupParticipants)
	}
	if !strings.Contains(out, "Asado del viernes") {
		t.Errorf("create_group result = %s, want the group name serialized", out)
	}
}

// ── T135 (ct-2026-09-03-1546) — el dueño queda administrador del grupo ─────

// TestCreateGroupPromotesOwnerToAdmin is the actual bug: the dueño's own
// chats row (IsBoss) has to drive a PromoteParticipants call for a group
// he's a participant of — WhatsApp itself never makes him admin by default.
func TestCreateGroupPromotesOwnerToAdmin(t *testing.T) {
	fgp := &fakeGroupProfile{
		createGroupJID: types.NewJID("555001", "g.us"),
		createGroupParticipants: []types.GroupParticipant{
			{JID: types.NewJID("555000000099", "s.whatsapp.net")},
			{JID: types.NewJID("555000000088", "s.whatsapp.net")},
		},
	}
	st, ctx, srv := serverWithGroupProfileAndStore(t, fgp)
	dueño := "555000000099@s.whatsapp.net"
	if err := st.TouchChat(dueño, "Dueño", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIsBoss(dueño, true); err != nil {
		t.Fatal(err)
	}

	callTool(t, ctx, srv, "create_group", map[string]any{
		"name": "Piumy", "participants": []string{dueño, "555000000088@s.whatsapp.net"},
	})

	if fgp.lastPromoteGroup != "555001@g.us" {
		t.Errorf("PromoteParticipants group = %q, want %q", fgp.lastPromoteGroup, "555001@g.us")
	}
	if len(fgp.lastPromoteParticipants) != 1 || fgp.lastPromoteParticipants[0] != dueño {
		t.Errorf("PromoteParticipants participants = %v, want exactly [%q]", fgp.lastPromoteParticipants, dueño)
	}
}

// TestCreateGroupNeverAttemptsPromotionForAnUnresolvedLIDBoss is T145's
// (ct-2026-09-07-1456) own cheap, no-live-group-needed check for its
// hypothesis B (canonicalParticipantJID falling back to an unresolved
// @lid). GetChat runs a raw WHERE jid=?, no LID resolution of its own
// (store/chat.go) — so if WhatsApp's CreateGroup response ever returns the
// boss's participant with an empty PhoneNumber (an @lid whose PN the
// server hasn't resolved yet), canonicalParticipantJID falls back to that
// @lid, the store lookup by @lid never matches the boss's PN-keyed chats
// row (T107: chats.jid is always the phone number, never @lid), and
// PromoteParticipants is never even attempted — SILENTLY, no warning at
// all. That is the opposite of what was captured live: a warning WAS
// produced ("could not promote the owner to group admin: info query
// returned status 403: forbidden"), which only happens when toPromote
// wasn't empty — proving the real incident's candidate string DID match
// the boss's PN-keyed row, i.e. canonicalParticipantJID returned the PN,
// not the @lid. Hypothesis B (the identity-form theory) is refuted by this
// reproduction; see T145's report to Citrino for the full chain.
func TestCreateGroupNeverAttemptsPromotionForAnUnresolvedLIDBoss(t *testing.T) {
	fgp := &fakeGroupProfile{
		createGroupJID: types.NewJID("555001", "g.us"),
		createGroupParticipants: []types.GroupParticipant{
			// PhoneNumber left zero-value: WhatsApp hasn't resolved this
			// participant's PN yet, same shape parseParticipant leaves an
			// @lid participant in when the server's response carries no
			// phone_number attribute.
			{JID: types.NewJID("555000000000001", "lid")},
		},
	}
	st, ctx, srv := serverWithGroupProfileAndStore(t, fgp)
	dueño := "555000000099@s.whatsapp.net" // the boss's real, PN-keyed row
	if err := st.TouchChat(dueño, "Dueño", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIsBoss(dueño, true); err != nil {
		t.Fatal(err)
	}

	out := callTool(t, ctx, srv, "create_group", map[string]any{
		"name": "Piumy", "participants": []string{"555000000000001@lid"},
	})

	if fgp.lastPromoteGroup != "" || fgp.lastPromoteParticipants != nil {
		t.Errorf("PromoteParticipants was called (group=%q participants=%v), want never called — an unresolved @lid never matches the boss's PN-keyed chats row", fgp.lastPromoteGroup, fgp.lastPromoteParticipants)
	}
	if strings.Contains(out, "could not promote") {
		t.Errorf("create_group result = %s, want no promotion warning at all — the miss is silent, not a WhatsApp error", out)
	}
}

// TestCreateGroupDoesNotPromoteNonBossParticipants is the DoD's own
// "never promote a stranger" check: with NO participant marked is_boss,
// PromoteParticipants must never be called at all.
func TestCreateGroupDoesNotPromoteNonBossParticipants(t *testing.T) {
	fgp := &fakeGroupProfile{
		createGroupJID: types.NewJID("555001", "g.us"),
		createGroupParticipants: []types.GroupParticipant{
			{JID: types.NewJID("555000000088", "s.whatsapp.net")},
			{JID: types.NewJID("555000000077", "s.whatsapp.net")},
		},
	}
	st, ctx, srv := serverWithGroupProfileAndStore(t, fgp)
	// Both participants exist as known, non-boss contacts.
	if err := st.TouchChat("555000000088@s.whatsapp.net", "Otro", 1); err != nil {
		t.Fatal(err)
	}

	callTool(t, ctx, srv, "create_group", map[string]any{
		"name": "Grupo", "participants": []string{"555000000088@s.whatsapp.net", "555000000077@s.whatsapp.net"},
	})

	if fgp.lastPromoteGroup != "" || fgp.lastPromoteParticipants != nil {
		t.Errorf("PromoteParticipants was called (group=%q participants=%v), want never called — nobody is is_boss", fgp.lastPromoteGroup, fgp.lastPromoteParticipants)
	}
}

// TestCreateGroupPromotionFailureStillReturnsTheGroup is the DoD's own
// non-negotiable: creating and promoting are separate network operations,
// and the second one failing must never lose the group. The response still
// carries the group (name recognizable in the JSON), plus a warning.
// TestCreateGroupPromotionFailureStillReturnsTheGroup is T135's own
// non-negotiable rule, unchanged by T145's retry: a promotion that fails
// on EVERY attempt still returns the group as a success — never the other
// way around.
func TestCreateGroupPromotionFailureStillReturnsTheGroup(t *testing.T) {
	fastPromoteRetries(t)
	fgp := &fakeGroupProfile{
		createGroupJID: types.NewJID("555001", "g.us"),
		createGroupParticipants: []types.GroupParticipant{
			{JID: types.NewJID("555000000099", "s.whatsapp.net")},
		},
		promoteErr: errors.New("network hiccup promoting"),
	}
	st, ctx, srv := serverWithGroupProfileAndStore(t, fgp)
	dueño := "555000000099@s.whatsapp.net"
	if err := st.TouchChat(dueño, "Dueño", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIsBoss(dueño, true); err != nil {
		t.Fatal(err)
	}

	out := callTool(t, ctx, srv, "create_group", map[string]any{
		"name": "Piumy", "participants": []string{dueño},
	})

	if strings.Contains(out, `"isError":true`) {
		t.Fatalf("create_group with a promotion failure = %s, want the group still returned as success", out)
	}
	if !strings.Contains(out, "Piumy") {
		t.Errorf("create_group result = %s, want the group itself still in the response", out)
	}
	if !strings.Contains(out, "network hiccup promoting") {
		t.Errorf("create_group result = %s, want the promotion failure reported as a warning", out)
	}
	if fgp.promoteCalls != maxPromoteAttempts {
		t.Errorf("PromoteParticipants calls = %d, want %d (a failure that never clears must exhaust every retry, then stop)", fgp.promoteCalls, maxPromoteAttempts)
	}
}

// TestCreateGroupPromotionRetriesAndSucceedsOnTransientFailure is T145's
// own regression — hypothesis A, confirmed live: WhatsApp 403s an admin op
// fresh off group creation, and a LATER retry succeeds. A transient
// failure that clears by the second attempt must promote the owner
// without any warning at all — the DoD's own "sin intervención manual".
func TestCreateGroupPromotionRetriesAndSucceedsOnTransientFailure(t *testing.T) {
	fastPromoteRetries(t)
	fgp := &fakeGroupProfile{
		createGroupJID: types.NewJID("555001", "g.us"),
		createGroupParticipants: []types.GroupParticipant{
			{JID: types.NewJID("555000000099", "s.whatsapp.net")},
		},
		promoteFailCount: 1, // fails once (the live shape), succeeds after
		promoteFailErr:   errors.New("info query returned status 403: forbidden"),
	}
	st, ctx, srv := serverWithGroupProfileAndStore(t, fgp)
	dueño := "555000000099@s.whatsapp.net"
	if err := st.TouchChat(dueño, "Dueño", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIsBoss(dueño, true); err != nil {
		t.Fatal(err)
	}

	out := callTool(t, ctx, srv, "create_group", map[string]any{
		"name": "Piumy", "participants": []string{dueño},
	})

	if strings.Contains(out, `"isError":true`) {
		t.Fatalf("create_group with a transient promotion failure = %s, want success", out)
	}
	if strings.Contains(out, "warnings") || strings.Contains(out, "403") {
		t.Errorf("create_group result = %s, want NO warning at all — the retry recovered, the owner never needs to know it took a second try", out)
	}
	if fgp.promoteCalls != 2 {
		t.Errorf("PromoteParticipants calls = %d, want 2 (fails once, succeeds on the retry)", fgp.promoteCalls)
	}
}

// TestPromoteRetryWindowIsRandomizedNotFixed is the contract's own
// non-negotiable anti-ban criterion, checked structurally: a fixed or
// round wait between retries against WhatsApp is exactly what the
// project's anti-ban discipline forbids (governor.DelayWindow itself is
// already covered by internal/governor's own tests for the actual
// randomization — this only guards that group_tools.go's window is a
// real, non-degenerate range, not a single fixed value in disguise).
func TestPromoteRetryWindowIsRandomizedNotFixed(t *testing.T) {
	if promoteRetryWindow.Min <= 0 || promoteRetryWindow.Max <= promoteRetryWindow.Min {
		t.Errorf("promoteRetryWindow = %+v, want a real [Min, Max) range (Min > 0, Max > Min) — a degenerate window samples the same fixed wait every time", promoteRetryWindow)
	}
}

// TestCreateGroupSeedsNameAndMembersWithoutWaitingForAMessage covers
// Citrino's own addition: the name and membership WhatsApp's CreateGroup
// response already carries must be saved immediately — not left for the
// connect-time scrape or a first group message to fill in later.
func TestCreateGroupSeedsNameAndMembersWithoutWaitingForAMessage(t *testing.T) {
	fgp := &fakeGroupProfile{
		createGroupJID: types.NewJID("555001", "g.us"),
		createGroupParticipants: []types.GroupParticipant{
			{JID: types.NewJID("555000000099", "s.whatsapp.net"), DisplayName: ""},
			{JID: types.NewJID("555000000088", "s.whatsapp.net"), DisplayName: ""},
		},
	}
	st, ctx, srv := serverWithGroupProfileAndStore(t, fgp)

	callTool(t, ctx, srv, "create_group", map[string]any{
		"name": "Piumy", "participants": []string{"555000000099@s.whatsapp.net", "555000000088@s.whatsapp.net"},
	})

	chat, ok, err := st.GetChat("555001@g.us")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || chat.Name != "Piumy" {
		t.Errorf("chats row after create_group = ok=%v name=%q, want ok=true name=%q", ok, chat.Name, "Piumy")
	}
	members, err := st.ListGroupMembers("555001@g.us")
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Errorf("group_members after create_group = %+v, want 2 rows, without waiting for a scrape or a message", members)
	}
}

// TestCreateGroupPromotionNormalizesDeviceSuffix is the same trap T118/
// T125/T132 already found, one seam over: the participant JID WhatsApp
// returns can carry a device suffix, and GetChat runs a raw WHERE jid=?
// with no normalization of its own — an unnormalized cross-reference here
// would silently miss the dueño's row and never promote him.
func TestCreateGroupPromotionNormalizesDeviceSuffix(t *testing.T) {
	fgp := &fakeGroupProfile{
		createGroupJID: types.NewJID("555001", "g.us"),
		createGroupParticipants: []types.GroupParticipant{
			// WhatsApp's real device-suffix shape: user:device@server.
			{JID: types.NewJID("555000000099:5", "s.whatsapp.net")},
		},
	}
	st, ctx, srv := serverWithGroupProfileAndStore(t, fgp)
	dueño := "555000000099@s.whatsapp.net" // stored suffix-free, as chats always are
	if err := st.TouchChat(dueño, "Dueño", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIsBoss(dueño, true); err != nil {
		t.Fatal(err)
	}

	callTool(t, ctx, srv, "create_group", map[string]any{
		"name": "Piumy", "participants": []string{dueño},
	})

	if len(fgp.lastPromoteParticipants) != 1 || fgp.lastPromoteParticipants[0] != dueño {
		t.Errorf("PromoteParticipants participants = %v, want exactly [%q] (the cross-reference must normalize)", fgp.lastPromoteParticipants, dueño)
	}
}

func TestAddParticipantCallsGroupProfile(t *testing.T) {
	fgp := &fakeGroupProfile{}
	ctx, srv := serverWithGroupProfile(t, fgp)

	callTool(t, ctx, srv, "add_participant", map[string]any{
		"group_id": "g1@g.us", "participant_id": "333@c.us",
	})
	if fgp.lastAddParticipantGroup != "g1@g.us" || fgp.lastAddParticipantID != "333@c.us" {
		t.Errorf("AddParticipant got group=%q participant=%q, want g1@g.us / 333@c.us", fgp.lastAddParticipantGroup, fgp.lastAddParticipantID)
	}
}

// ── T136 (ct-2026-09-03-1627) — promote_group_admin ────────────────────

// TestPromoteGroupAdminCallsGroupProfile is the basic case the owner
// asked for: name a group and a participant, that participant becomes
// admin.
func TestPromoteGroupAdminCallsGroupProfile(t *testing.T) {
	fgp := &fakeGroupProfile{
		promoteResult: []types.GroupParticipant{
			{JID: types.NewJID("555000000099", "s.whatsapp.net"), IsAdmin: true},
		},
	}
	ctx, srv := serverWithGroupProfile(t, fgp)

	out := callTool(t, ctx, srv, "promote_group_admin", map[string]any{
		"group_id": "555001@g.us", "participant_id": "555000000099@s.whatsapp.net",
	})

	if fgp.lastPromoteGroup != "555001@g.us" {
		t.Errorf("PromoteParticipants group = %q, want 555001@g.us", fgp.lastPromoteGroup)
	}
	if len(fgp.lastPromoteParticipants) != 1 || fgp.lastPromoteParticipants[0] != "555000000099@s.whatsapp.net" {
		t.Errorf("PromoteParticipants participants = %v, want exactly [555000000099@s.whatsapp.net]", fgp.lastPromoteParticipants)
	}
	if text := decodeToolText(t, out); !strings.Contains(text, `"IsAdmin": true`) {
		t.Errorf("promote_group_admin result = %s, want IsAdmin:true reflected back", text)
	}
}

// TestPromoteGroupAdminAcceptsNonBossParticipant is the use case, not an
// exception (T136's own answer to the question T135 left open): unlike
// create_group's automatic promotion, this tool has NO is_boss filter —
// the owner naming a teammate who isn't the account owner is the most
// obvious call, and it must go through untouched.
func TestPromoteGroupAdminAcceptsNonBossParticipant(t *testing.T) {
	fgp := &fakeGroupProfile{}
	st, ctx, srv := serverWithGroupProfileAndStore(t, fgp)
	notBoss := "555000000077@s.whatsapp.net"
	if err := st.TouchChat(notBoss, "Empleado", 1); err != nil {
		t.Fatal(err)
	}
	// IsBoss is never set here — this participant is explicitly NOT boss.

	out := callTool(t, ctx, srv, "promote_group_admin", map[string]any{
		"group_id": "555001@g.us", "participant_id": notBoss,
	})

	if strings.Contains(out, `"isError":true`) {
		t.Fatalf("promote_group_admin on a non-boss participant = %s, want success", out)
	}
	if len(fgp.lastPromoteParticipants) != 1 || fgp.lastPromoteParticipants[0] != notBoss {
		t.Errorf("PromoteParticipants participants = %v, want exactly [%q]", fgp.lastPromoteParticipants, notBoss)
	}
}

// TestPromoteGroupAdminBlockedForNonOwnerDispatch: the gate, not the
// handler, is the only lock this tool has (T136's own contract) — a
// caution-level dispatch must never reach GroupProfile at all.
// TestPromoteGroupAdminWorksFromANonBossDispatch is T148's own regression
// (ct-2026-09-07-1644, boss verbatim, direct: "quiero que quites ese
// candado, si le pido a un agente que cree un grupo, quiero que lo haga").
// promote_group_admin left bossOnlyTools — a caution-level dispatch (not
// the owner, not boss) must be able to use it now, the same as every other
// group/profile tool. Replaces the old
// TestPromoteGroupAdminBlockedForNonOwnerDispatch, which proved the exact
// restriction this contract removes.
func TestPromoteGroupAdminWorksFromANonBossDispatch(t *testing.T) {
	fgp := &fakeGroupProfile{}
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	rtMgr := router.NewManager(filepath.Join(dir, "router.json"))
	sm := state.NewManager(filepath.Join(dir, "status.json"), 8)
	gate := NewGate()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv := New(ctx, Deps{Store: st, State: sm, Router: rtMgr, AgentIdle: time.Minute, Gate: gate, GroupProfile: fgp})

	termID := "term-non-owner"
	if err := gate.RegisterDispatch("nonce-non-owner", "555000000012@c.us", LevelCaution, termID, 0, ""); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, termID)

	out := callTool(t, termCtx, srv, "promote_group_admin", map[string]any{
		"group_id": "555001@g.us", "participant_id": "555000000099@s.whatsapp.net",
	})

	if strings.Contains(out, "boss-only") || strings.Contains(out, `"isError":true`) {
		t.Errorf("promote_group_admin from a caution dispatch = %s, want it to succeed — T148 removed the boss-only restriction", out)
	}
	if fgp.lastPromoteGroup != "555001@g.us" {
		t.Errorf("PromoteParticipants group = %q, want the group_id to have gone through", fgp.lastPromoteGroup)
	}
}

// TestPromoteGroupAdminNormalizesDeviceSuffix is the same gap T118/T125/
// T132/T135 already found, one seam over: a participant JID read off
// get_chat_groups can carry a device suffix WhatsApp's own group-admin
// call never expects.
func TestPromoteGroupAdminNormalizesDeviceSuffix(t *testing.T) {
	fgp := &fakeGroupProfile{}
	ctx, srv := serverWithGroupProfile(t, fgp)

	callTool(t, ctx, srv, "promote_group_admin", map[string]any{
		"group_id": "555001@g.us", "participant_id": "555000000099:5@s.whatsapp.net",
	})

	want := "555000000099@s.whatsapp.net"
	if len(fgp.lastPromoteParticipants) != 1 || fgp.lastPromoteParticipants[0] != want {
		t.Errorf("PromoteParticipants participants = %v, want exactly [%q] (device suffix stripped)", fgp.lastPromoteParticipants, want)
	}
}

// TestPromoteGroupAdminParticipantNotInGroupReturnsLegibleError: whatsmeow
// reports this as a per-participant Error code on an otherwise successful
// call, never as an outer Go error — the tool must still surface it as a
// failure, not a silent "success" with nothing changed.
func TestPromoteGroupAdminParticipantNotInGroupReturnsLegibleError(t *testing.T) {
	fgp := &fakeGroupProfile{
		promoteResult: []types.GroupParticipant{
			{JID: types.NewJID("555000000099", "s.whatsapp.net"), Error: 404},
		},
	}
	ctx, srv := serverWithGroupProfile(t, fgp)

	out := callTool(t, ctx, srv, "promote_group_admin", map[string]any{
		"group_id": "555001@g.us", "participant_id": "555000000099@s.whatsapp.net",
	})

	if !strings.Contains(out, `"isError":true`) {
		t.Fatalf("promote_group_admin for a non-participant = %s, want an error result, not a panic or a silent success", out)
	}
	if !strings.Contains(out, "404") {
		t.Errorf("promote_group_admin error = %s, want the WhatsApp error code surfaced", out)
	}
}

// testImageDataURL builds a real, tiny data: URL for a solid-color image in
// the given format ("jpeg", "png", "gif") — T111 (ct-2026-09-01-1442)
// callers need actual decodable images, not arbitrary bytes: EnsureJPEG
// decides by the REAL detected format, so a fake ".../jpeg;base64,..." over
// non-image bytes would now fail to decode instead of passing through.
func testImageDataURL(t *testing.T, format string) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			img.Set(x, y, color.RGBA{50, 100, 150, 255})
		}
	}
	var buf bytes.Buffer
	var mime string
	switch format {
	case "jpeg":
		if err := jpeg.Encode(&buf, img, nil); err != nil {
			t.Fatal(err)
		}
		mime = "image/jpeg"
	case "png":
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
		mime = "image/png"
	case "gif":
		if err := gif.Encode(&buf, img, nil); err != nil {
			t.Fatal(err)
		}
		mime = "image/gif"
	default:
		t.Fatalf("testImageDataURL: unknown format %q", format)
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// decodedFormatOf is the test's own oracle for "is this really a JPEG now" —
// same criterion EnsureJPEG itself uses (the real decoded format, not a
// trusted mime string).
func decodedFormatOf(t *testing.T, data []byte) string {
	t.Helper()
	_, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decoding tool output as an image: %v", err)
	}
	return format
}

// TestSetGroupIconDecodesDataURLBeforeCallingGroupProfile is the ST-E
// angle Amatista flagged: set_group_icon receives data_url, but
// SetGroupPhoto wants raw jpeg bytes — the decode has to happen in
// group_tools.go itself.
func TestSetGroupIconDecodesDataURLBeforeCallingGroupProfile(t *testing.T) {
	fgp := &fakeGroupProfile{}
	ctx, srv := serverWithGroupProfile(t, fgp)

	out := callTool(t, ctx, srv, "set_group_icon", map[string]any{
		"group_id": "g1@g.us", "data_url": testImageDataURL(t, "jpeg"),
	})
	if !strings.Contains(out, "group icon set") {
		t.Errorf("set_group_icon = %s, want success", out)
	}
	if fgp.lastSetGroupPhotoGroup != "g1@g.us" {
		t.Errorf("SetGroupPhoto group = %q, want g1@g.us", fgp.lastSetGroupPhotoGroup)
	}
	if len(fgp.lastSetGroupPhotoBytes) == 0 {
		t.Error("SetGroupPhoto bytes is empty, want the decoded image payload")
	}
}

// TestSetGroupIconConvertsPNGToJPEG is T111's (ct-2026-09-01-1442) own
// extension of the case above: a PNG data_url must reach GroupProfile as a
// real JPEG, not the raw PNG bytes — "el mismo conversor sirve para las
// dos [foto de perfil e ícono de grupo] y debe compartirse".
func TestSetGroupIconConvertsPNGToJPEG(t *testing.T) {
	fgp := &fakeGroupProfile{}
	ctx, srv := serverWithGroupProfile(t, fgp)

	out := callTool(t, ctx, srv, "set_group_icon", map[string]any{
		"group_id": "g1@g.us", "data_url": testImageDataURL(t, "png"),
	})
	if !strings.Contains(out, "group icon set") {
		t.Errorf("set_group_icon (png) = %s, want success", out)
	}
	if format := decodedFormatOf(t, fgp.lastSetGroupPhotoBytes); format != "jpeg" {
		t.Errorf("bytes reaching GroupProfile decode as %q, want jpeg", format)
	}
}

// TestSetGroupIconRejectsUndecodableImage: a data_url whose payload decodes
// (as base64) but isn't a real image must be rejected before ever reaching
// GroupProfile — same "never call the client with garbage" invariant as the
// malformed-data_url case.
func TestSetGroupIconRejectsUndecodableImage(t *testing.T) {
	fgp := &fakeGroupProfile{}
	ctx, srv := serverWithGroupProfile(t, fgp)

	out := callTool(t, ctx, srv, "set_group_icon", map[string]any{
		"group_id": "g1@g.us", "data_url": "data:image/jpeg;base64,aGVsbG8=", // "hello", not an image
	})
	if !strings.Contains(out, "invalid data_url") {
		t.Errorf("set_group_icon with undecodable image bytes = %s, want an invalid data_url error", out)
	}
	if fgp.lastSetGroupPhotoGroup != "" {
		t.Error("SetGroupPhoto was called despite the payload not decoding as an image")
	}
}

// TestSetGroupIconRejectsMalformedDataURLWithoutCallingGroupProfile: a
// decode failure must be caught in group_tools.go, before ever reaching
// the client — SetGroupPhoto must not be called with garbage.
func TestSetGroupIconRejectsMalformedDataURLWithoutCallingGroupProfile(t *testing.T) {
	fgp := &fakeGroupProfile{}
	ctx, srv := serverWithGroupProfile(t, fgp)

	out := callTool(t, ctx, srv, "set_group_icon", map[string]any{
		"group_id": "g1@g.us", "data_url": "not-a-data-url",
	})
	if !strings.Contains(out, "invalid data_url") {
		t.Errorf("set_group_icon with a malformed data_url = %s, want an invalid data_url error", out)
	}
	if fgp.lastSetGroupPhotoGroup != "" {
		t.Error("SetGroupPhoto was called despite the data_url failing to decode")
	}
}

func TestSetGroupDescriptionCallsGroupProfile(t *testing.T) {
	fgp := &fakeGroupProfile{}
	ctx, srv := serverWithGroupProfile(t, fgp)

	out := callTool(t, ctx, srv, "set_group_description", map[string]any{
		"group_id": "g1@g.us", "description": "grupo de prueba",
	})
	if !strings.Contains(out, "group description set") {
		t.Errorf("set_group_description = %s, want success", out)
	}
	if fgp.lastSetGroupDescGroup != "g1@g.us" || fgp.lastSetGroupDescText != "grupo de prueba" {
		t.Errorf("SetGroupDescription got group=%q text=%q, want g1@g.us / grupo de prueba", fgp.lastSetGroupDescGroup, fgp.lastSetGroupDescText)
	}
}

// TestSetProfileStatusCallsSetStatusMessage is the ST-E rename regression
// (ct-2026-07-11-1444): set_profile_name became set_profile_status and
// wraps whatsmeow's SetStatusMessage (the "About" text), never a display
// name — there is no whatsmeow API for that.
func TestSetProfileStatusCallsSetStatusMessage(t *testing.T) {
	fgp := &fakeGroupProfile{}
	ctx, srv := serverWithGroupProfile(t, fgp)

	out := callTool(t, ctx, srv, "set_profile_status", map[string]any{"status": "disponible"})
	if !strings.Contains(out, "profile status set") {
		t.Errorf("set_profile_status = %s, want success", out)
	}
	if fgp.lastSetProfileStatus != "disponible" {
		t.Errorf("SetProfileStatus got %q, want %q", fgp.lastSetProfileStatus, "disponible")
	}
}

// ── T111 (ct-2026-09-01-1442) — set_profile_photo ───────────────────────────

// TestSetProfilePhotoDecodesAndConvertsToJPEG: a PNG data_url reaches
// GroupProfile as a real JPEG — the account's own profile photo, same
// conversion set_group_icon gets.
func TestSetProfilePhotoDecodesAndConvertsToJPEG(t *testing.T) {
	fgp := &fakeGroupProfile{}
	ctx, srv := serverWithGroupProfile(t, fgp)

	out := callTool(t, ctx, srv, "set_profile_photo", map[string]any{
		"data_url": testImageDataURL(t, "png"),
	})
	if !strings.Contains(out, "profile photo set") {
		t.Errorf("set_profile_photo = %s, want success", out)
	}
	if !fgp.setProfilePhotoCalled {
		t.Fatal("SetProfilePhoto was not called")
	}
	if format := decodedFormatOf(t, fgp.lastSetProfilePhotoBytes); format != "jpeg" {
		t.Errorf("bytes reaching GroupProfile decode as %q, want jpeg", format)
	}
}

// TestSetProfilePhotoRemoveClearsPhoto: remove=true removes the photo —
// GroupProfile.SetProfilePhoto gets nil bytes, whatsmeow's own "delete"
// signal, regardless of whatever data_url (if any) came along with it.
func TestSetProfilePhotoRemoveClearsPhoto(t *testing.T) {
	fgp := &fakeGroupProfile{}
	ctx, srv := serverWithGroupProfile(t, fgp)

	out := callTool(t, ctx, srv, "set_profile_photo", map[string]any{"remove": true})
	if !strings.Contains(out, "profile photo removed") {
		t.Errorf("set_profile_photo(remove=true) = %s, want success", out)
	}
	if !fgp.setProfilePhotoCalled {
		t.Fatal("SetProfilePhoto was not called")
	}
	if fgp.lastSetProfilePhotoBytes != nil {
		t.Errorf("SetProfilePhoto bytes = %v, want nil (whatsmeow's own remove signal)", fgp.lastSetProfilePhotoBytes)
	}
}

// TestSetProfilePhotoRemoveIgnoresDataURL: remove=true wins even if a
// data_url was also passed — an agent that means "clear it" should not
// have to worry about whether it also forgot to omit data_url.
func TestSetProfilePhotoRemoveIgnoresDataURL(t *testing.T) {
	fgp := &fakeGroupProfile{}
	ctx, srv := serverWithGroupProfile(t, fgp)

	callTool(t, ctx, srv, "set_profile_photo", map[string]any{
		"remove": true, "data_url": testImageDataURL(t, "jpeg"),
	})
	if fgp.lastSetProfilePhotoBytes != nil {
		t.Errorf("SetProfilePhoto bytes = %v, want nil — remove=true must win over a data_url also present", fgp.lastSetProfilePhotoBytes)
	}
}

// TestSetProfilePhotoRequiresDataURLOrRemove: neither remove=true nor a
// usable data_url — an explicit error, not a silent no-op or a call to
// GroupProfile with nothing.
func TestSetProfilePhotoRequiresDataURLOrRemove(t *testing.T) {
	fgp := &fakeGroupProfile{}
	ctx, srv := serverWithGroupProfile(t, fgp)

	out := callTool(t, ctx, srv, "set_profile_photo", map[string]any{})
	if !strings.Contains(out, "data_url") {
		t.Errorf("set_profile_photo with neither data_url nor remove = %s, want an error naming data_url/remove", out)
	}
	if fgp.setProfilePhotoCalled {
		t.Error("SetProfilePhoto was called despite no data_url and no remove")
	}
}

// TestSetProfilePhotoRejectsUndecodableImage: same "never call the client
// with garbage" invariant as set_group_icon.
func TestSetProfilePhotoRejectsUndecodableImage(t *testing.T) {
	fgp := &fakeGroupProfile{}
	ctx, srv := serverWithGroupProfile(t, fgp)

	out := callTool(t, ctx, srv, "set_profile_photo", map[string]any{
		"data_url": "data:image/jpeg;base64,aGVsbG8=", // "hello", not an image
	})
	if !strings.Contains(out, "invalid data_url") {
		t.Errorf("set_profile_photo with undecodable image bytes = %s, want an invalid data_url error", out)
	}
	if fgp.setProfilePhotoCalled {
		t.Error("SetProfilePhoto was called despite the payload not decoding as an image")
	}
}

// serverWithGroupProfileNoDispatch is serverWithGroupProfile without the
// boss dispatch — get_status is one of the 27-of-55 tools that answers
// with no dispatch bound at all (levelgate.go), and T103's profile_status
// field has to work in exactly that unrestricted, no-candado state.
func serverWithGroupProfileNoDispatch(t *testing.T, fgp GroupProfile) (context.Context, *server.MCPServer) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv := New(ctx, Deps{
		Store: st, State: state.NewManager(filepath.Join(dir, "status.json"), 8),
		Router: router.NewManager(filepath.Join(dir, "router.json")), AgentIdle: time.Minute,
		Gate: NewGate(), GroupProfile: fgp,
	})
	return ctx, srv
}

type profileStatusFields struct {
	ProfileStatus          string `json:"profile_status"`
	ProfileStatusAvailable bool   `json:"profile_status_available"`
}

func getStatusProfileStatus(t *testing.T, ctx context.Context, srv *server.MCPServer) profileStatusFields {
	t.Helper()
	out := callTool(t, ctx, srv, "get_status", nil)
	var f profileStatusFields
	if err := json.Unmarshal([]byte(decodeToolText(t, out)), &f); err != nil {
		t.Fatalf("decoding get_status profile_status fields: %v\nraw: %s", err, out)
	}
	return f
}

// TestGetStatusReportsProfileStatus is T103's core case (ct-2026-08-29-1759):
// GetProfileStatus already existed on the adapter (T96) but only the REST
// dashboard consumed it — an agent could SET the account's WhatsApp status
// but never READ it back.
func TestGetStatusReportsProfileStatus(t *testing.T) {
	fgp := &fakeGroupProfile{getProfileStatus: "disponible"}
	ctx, srv := serverWithGroupProfileNoDispatch(t, fgp)

	f := getStatusProfileStatus(t, ctx, srv)
	if f.ProfileStatus != "disponible" || !f.ProfileStatusAvailable {
		t.Errorf("get_status profile_status/available = %q/%v, want %q/true", f.ProfileStatus, f.ProfileStatusAvailable, "disponible")
	}
}

// TestGetStatusProfileStatusCachesAcrossCalls is Citrino's third audit
// point: get_status is the most-called tool in the system, but the
// account's own status text changes rarely — hitting WhatsApp on every
// single get_status call is needless traffic. Two calls close together
// must only read GroupProfile once.
func TestGetStatusProfileStatusCachesAcrossCalls(t *testing.T) {
	fgp := &fakeGroupProfile{getProfileStatus: "disponible"}
	ctx, srv := serverWithGroupProfileNoDispatch(t, fgp)

	getStatusProfileStatus(t, ctx, srv)
	getStatusProfileStatus(t, ctx, srv)

	if calls := fgp.callCount(); calls != 1 {
		t.Errorf("GetProfileStatus called %d times across 2 get_status calls within the TTL, want 1 (cached)", calls)
	}
}

// TestGetStatusProfileStatusEmptyIsNotAnError is the DoD's explicit case:
// no status set is the normal, common case, "" with no error — and
// available=true, since the read itself succeeded (Citrino's audit: "" must
// not mean two different things — a legit empty status vs. a failed read).
func TestGetStatusProfileStatusEmptyIsNotAnError(t *testing.T) {
	fgp := &fakeGroupProfile{getProfileStatus: ""}
	ctx, srv := serverWithGroupProfileNoDispatch(t, fgp)

	out := callTool(t, ctx, srv, "get_status", nil)
	if strings.Contains(out, `"isError":true`) {
		t.Errorf("get_status with no profile status set returned an error: %s", out)
	}
	f := getStatusProfileStatus(t, ctx, srv)
	if f.ProfileStatus != "" || !f.ProfileStatusAvailable {
		t.Errorf("get_status profile_status/available = %q/%v, want empty/true (legit empty status, read succeeded)", f.ProfileStatus, f.ProfileStatusAvailable)
	}
}

// TestGetStatusProfileStatusEmptyWithoutGroupProfile: get_status must never
// fail outright just because GroupProfile isn't wired — same nil-safe
// degrade as DefaultMode when Router is nil. available=false: there was no
// way to read anything, distinct from a legit empty status.
func TestGetStatusProfileStatusEmptyWithoutGroupProfile(t *testing.T) {
	_, srv, ctx, _ := newTestServer(t)

	out := callTool(t, ctx, srv, "get_status", nil)
	if strings.Contains(out, `"isError":true`) {
		t.Errorf("get_status with no GroupProfile wired returned an error: %s", out)
	}
	f := getStatusProfileStatus(t, ctx, srv)
	if f.ProfileStatus != "" || f.ProfileStatusAvailable {
		t.Errorf("get_status profile_status/available = %q/%v, want empty/false when GroupProfile is nil", f.ProfileStatus, f.ProfileStatusAvailable)
	}
}

// TestGetStatusProfileStatusEmptyOnAdapterError: a real read failure (no
// connection, timeout) must degrade to "", not break the whole tool —
// same "decorative, not critical" call the REST handler already makes
// (admin.go's handleGetProfileStatus doc). available=false distinguishes
// this from the legit-empty case above.
func TestGetStatusProfileStatusEmptyOnAdapterError(t *testing.T) {
	fgp := &fakeGroupProfile{getProfileStatusErr: errors.New("not connected")}
	ctx, srv := serverWithGroupProfileNoDispatch(t, fgp)

	out := callTool(t, ctx, srv, "get_status", nil)
	if strings.Contains(out, `"isError":true`) {
		t.Errorf("get_status errored out because GetProfileStatus failed: %s", out)
	}
	f := getStatusProfileStatus(t, ctx, srv)
	if f.ProfileStatus != "" || f.ProfileStatusAvailable {
		t.Errorf("get_status profile_status/available = %q/%v, want empty/false on a read failure", f.ProfileStatus, f.ProfileStatusAvailable)
	}
}

// TestGetStatusProfileStatusTimesOutOnSlowGroupProfile is Citrino's audit
// finding on T103/T104 (ct-2026-08-29-1759/1818): GetProfileStatus wraps a
// LIVE WhatsApp network call with no timeout — and get_status is the exact
// tool the manual tells an agent to call to diagnose a dead connection
// (connect/SKILL.md: "si get_status también falla, entonces sí es la
// conexión"). A get_status that hangs on a dead WhatsApp link defeats its
// own diagnostic purpose — the same failure shape T104 exists to close.
// get_status must ALWAYS answer, bounded, even when GroupProfile never
// returns at all.
func TestGetStatusProfileStatusTimesOutOnSlowGroupProfile(t *testing.T) {
	fgp := &fakeGroupProfile{blockUntilCtxDone: true}
	ctx, srv := serverWithGroupProfileNoDispatch(t, fgp)

	req := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "get_status", "arguments": map[string]any{}},
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	done := make(chan any, 1)
	go func() { done <- srv.HandleMessage(ctx, raw) }()

	select {
	case resp := <-done:
		if elapsed := time.Since(start); elapsed > profileStatusTimeout+2*time.Second {
			t.Errorf("get_status took %s to answer, want it bounded near the %s profile-status timeout", elapsed, profileStatusTimeout)
		}
		rawOut, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(rawOut), `"isError":true`) {
			t.Errorf("get_status errored out on a slow GroupProfile instead of degrading: %s", rawOut)
		}
		f := profileStatusFields{}
		if err := json.Unmarshal([]byte(decodeToolText(t, string(rawOut))), &f); err != nil {
			t.Fatalf("decoding get_status profile_status fields: %v\nraw: %s", err, rawOut)
		}
		if f.ProfileStatusAvailable {
			t.Error("profile_status_available = true for a read that never completed")
		}
	case <-time.After(profileStatusTimeout + 3*time.Second):
		t.Fatal("get_status hung well past the profile-status timeout — get_status must NEVER hang on a dead WhatsApp connection")
	}
}

// TestSetProfilePicToolNoLongerExists: whatsmeow has no API to change the
// own profile picture — the boss's decision was to remove the tool
// entirely, not leave a permanently-erroring stub. **T111 (ct-2026-09-01-
// 1442) found this premise was wrong**: SetGroupPhoto isn't group-specific
// despite its name — the recipient is just a parameter, and the OWN JID
// makes it a profile photo. The capability exists now, under a NEW name,
// `set_profile_photo` (see below) — `set_profile_pic`, the specific name
// this test checks, still correctly doesn't exist; nothing revived it.
func TestSetProfilePicToolNoLongerExists(t *testing.T) {
	for _, tool := range listTools(t) {
		if tool.Name == "set_profile_pic" {
			t.Error("set_profile_pic is still registered — ST-E removed it (whatsmeow has no API for it)")
		}
	}
}

// ── T149 (ct-2026-09-07-1730) — el freno de ritmo, no un candado ──────────
//
// Boss verbatim, tras cerrar T148: "esos frenos de en masa deven ir como
// frenos, no como candados." Cuatro reglas se verifican acá: nunca rechaza
// (TestGroupActionBrakeNeverRejectsOnlyDelays), espera aleatoria
// (TestGroupActionSpacingIsRandomizedNotFixed), una llamada aislada no paga
// nada (cada subtest de TestEveryGroupProfileToolCallsThePaceBrake mide su
// PRIMERA llamada), y un solo punto de aplicación compartido por las siete
// (TestGroupActionPaceIsSharedAcrossDifferentTools — dos tools DISTINTAS
// seguidas, no la misma dos veces, prueba que comparten una sola instancia).

// TestGroupActionSpacingIsRandomizedNotFixed is the same structural,
// non-negotiable anti-ban check T145's own TestPromoteRetryWindowIsRandomizedNotFixed
// runs for promoteRetryWindow — a fixed or round wait is exactly what this
// project's anti-ban discipline forbids.
func TestGroupActionSpacingIsRandomizedNotFixed(t *testing.T) {
	if groupActionSpacing.Min <= 0 || groupActionSpacing.Max <= groupActionSpacing.Min {
		t.Errorf("groupActionSpacing = %+v, want a real [Min, Max) range (Min > 0, Max > Min) — a degenerate window samples the same fixed wait every time", groupActionSpacing)
	}
}

// TestEveryGroupProfileToolCallsThePaceBrake is the DoD's "una sola llamada
// aislada no paga nada" AND "se espacian solas" checked PER tool, not just
// once: for each of the seven, the first call on a fresh server (nothing
// paced yet) must be near-instant, and a second call of the SAME tool
// landing right behind it must measurably wait. A tool that forgot to call
// the brake would pass the first assertion but fail the second.
func TestEveryGroupProfileToolCallsThePaceBrake(t *testing.T) {
	cases := []struct {
		tool string
		args map[string]any
	}{
		{"create_group", map[string]any{"name": "g", "participants": []string{"555000000001@c.us"}}},
		{"add_participant", map[string]any{"group_id": "g1@g.us", "participant_id": "555000000001@c.us"}},
		{"promote_group_admin", map[string]any{"group_id": "g1@g.us", "participant_id": "555000000001@c.us"}},
		{"set_group_icon", map[string]any{"group_id": "g1@g.us", "data_url": "data:image/jpeg;base64,x"}},
		{"set_group_description", map[string]any{"group_id": "g1@g.us", "description": "d"}},
		{"set_profile_photo", map[string]any{"remove": true}},
		{"set_profile_status", map[string]any{"status": "n"}},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			fastGroupActionSpacing(t)
			fgp := &fakeGroupProfile{}
			ctx, srv := serverWithGroupProfile(t, fgp)

			start := time.Now()
			callTool(t, ctx, srv, c.tool, c.args)
			if elapsed := time.Since(start); elapsed >= groupActionSpacing.Min {
				t.Errorf("%s's FIRST call on a fresh server took %v, want near-instant (rule: an isolated call pays nothing)", c.tool, elapsed)
			}

			start = time.Now()
			callTool(t, ctx, srv, c.tool, c.args)
			if elapsed := time.Since(start); elapsed < groupActionSpacing.Min {
				t.Errorf("%s's SECOND call, right behind the first, took %v, want at least %v — a burst must space itself out", c.tool, elapsed, groupActionSpacing.Min)
			}
		})
	}
}

// TestGroupActionPaceIsSharedAcrossDifferentTools is rule 4's own
// regression: "un solo punto de aplicación... si lo escribís siete veces,
// la octava tool que agreguemos se va a olvidar." create_group followed
// immediately by the DIFFERENT tool add_participant must still measurably
// wait — proving one shared brake backs every one of the seven, not seven
// independent copies that only pace against themselves.
func TestGroupActionPaceIsSharedAcrossDifferentTools(t *testing.T) {
	fastGroupActionSpacing(t)
	fgp := &fakeGroupProfile{}
	ctx, srv := serverWithGroupProfile(t, fgp)

	callTool(t, ctx, srv, "create_group", map[string]any{"name": "g", "participants": []string{}})

	start := time.Now()
	callTool(t, ctx, srv, "add_participant", map[string]any{"group_id": "g1@g.us", "participant_id": "555000000001@c.us"})
	if elapsed := time.Since(start); elapsed < groupActionSpacing.Min {
		t.Errorf("add_participant right after create_group took %v, want at least %v — the brake must be ONE shared instance across every group/profile tool, not one per tool", elapsed, groupActionSpacing.Min)
	}
}

// TestGroupActionBrakeNeverRejectsOnlyDelays is rule 1, the one the boss
// called out by name: "SI EL FRENO ALGUNA VEZ DEVUELVE UN ERROR, dejó de
// ser freno y volvió a ser candado." Four calls back-to-back must all
// succeed — only delayed, checked on every single one, not just the last —
// and the total elapsed time must reflect the three gaps between them
// actually pacing out.
func TestGroupActionBrakeNeverRejectsOnlyDelays(t *testing.T) {
	fastGroupActionSpacing(t)
	fgp := &fakeGroupProfile{}
	ctx, srv := serverWithGroupProfile(t, fgp)

	const calls = 4
	start := time.Now()
	for i := 0; i < calls; i++ {
		out := callTool(t, ctx, srv, "add_participant", map[string]any{"group_id": "g1@g.us", "participant_id": "555000000001@c.us"})
		if strings.Contains(out, `"isError":true`) {
			t.Fatalf("add_participant call %d of a burst = %s, want it to always succeed, only delayed — a brake must never refuse", i+1, out)
		}
	}
	if elapsed := time.Since(start); elapsed < (calls-1)*groupActionSpacing.Min {
		t.Errorf("%d calls back-to-back took %v, want at least %v across the %d gaps between them (a burst must space itself out)", calls, elapsed, (calls-1)*groupActionSpacing.Min, calls-1)
	}
}
