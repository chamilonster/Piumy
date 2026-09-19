package whatsmeow

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"

	"piumy-gateway/internal/governor"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
)

// seedFakeDevice inserts a device row directly (bypassing PutDevice, which
// panics on a synthetic device — Account.* fields are nil until a real
// pairing handshake sets them) so New() finds an existing, paired-looking
// session without a real WhatsApp account. Byte lengths match
// whatsmeow_device's own CHECK constraints (00-latest-schema.sql).
func seedFakeDevice(t *testing.T, dbPath string) {
	t.Helper()
	seedFakeDeviceWithJID(t, dbPath, "5550000000000.0:0@s.whatsapp.net")
}

// seedFakeDeviceWithJID is seedFakeDevice's own insert, parameterized on
// jid (T45, ct-2026-08-10-1424): seedFakeDevice's hardcoded ":0" device
// never exercises the real bug — whatsmeow's own JID.String() omits a
// zero device, so every existing recordOwnIdentity test got a clean jid
// "by accident" and never could have caught this. A regression test needs
// a NONZERO device (the real cut observed on the owner's installation was
// ":15") to actually reproduce it.
func seedFakeDeviceWithJID(t *testing.T, dbPath, jid string) {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	container := sqlstore.NewWithDB(db, "sqlite", waLog.Stdout("whatsmeow", "WARN", true))
	if err := container.Upgrade(context.Background()); err != nil {
		t.Fatal(err)
	}
	b32, b64, dummy := make([]byte, 32), make([]byte, 64), []byte{1, 2, 3, 4}
	_, err = db.Exec(`INSERT INTO whatsmeow_device
		(jid, lid, registration_id, noise_key, identity_key,
		 signed_pre_key, signed_pre_key_id, signed_pre_key_sig,
		 adv_key, adv_details, adv_account_sig, adv_account_sig_key, adv_device_sig,
		 platform, business_name, push_name, facebook_uuid, lid_migration_ts)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		jid, nil, 1, b32, b32,
		b32, 1, b64,
		dummy, dummy, b64, b32, b64,
		"", "", "", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
}

// TestNewCreatesMissingParentDirectory is T169's own regression
// (ct-2026-09-19-1433): cfg.DBPath now usually resolves under
// config.DataDir(), which may not exist yet on a genuinely fresh install —
// sqlite never creates a missing parent, only the file.
func TestNewCreatesMissingParentDirectory(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "does", "not", "exist", "yet", "wm.db")
	a, err := New(context.Background(), Config{DBPath: dbPath})
	if err != nil {
		t.Fatalf("New with a missing parent directory: %v", err)
	}
	// Releases the SQLite file — otherwise it stays locked and t.TempDir()'s
	// own cleanup fails on Windows (same reasoning as
	// TestStartDoesNotBlockWhenUnpaired above).
	a.Stop()
}

// TestStartDoesNotBlockWhenUnpaired is a regression test for the audit
// finding (ct-2026-07-10-0420 review): Start used to run the whole QR
// pairing loop synchronously, so corepipeline.Controller.Start (which
// calls gw.Start(ctx) and only launches the HTTP servers + pipeline loops
// AFTER it returns) would hang the entire process at boot on a fresh
// session until someone scanned — and worse, ctx cancellation (Ctrl+C)
// was never observed because main() itself was stuck inside ctrl.Start(),
// never reaching <-ctx.Done(). Start must return immediately regardless
// of whether pairing ever succeeds.
func TestStartDoesNotBlockWhenUnpaired(t *testing.T) {
	a, err := New(context.Background(), Config{DBPath: filepath.Join(t.TempDir(), "wm.db")})
	if err != nil {
		t.Fatal(err)
	}
	if a.client.Store.ID != nil {
		t.Fatal("a fresh device store should be unpaired (ID == nil) — test setup is wrong")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The pairLoop goroutine spawned by Start below keeps the session DB
	// open in the background (real Connect() attempt) — release it before
	// t.TempDir()'s own cleanup tries to remove the file, or that cleanup
	// fails on Windows (locked file).
	t.Cleanup(a.Stop)

	done := make(chan error, 1)
	go func() { done <- a.Start(ctx) }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Start(unpaired) returned an error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start blocked instead of returning immediately for an unpaired session — pairing must run in a goroutine (pairLoop), not inline")
	}
}

// TestStopAfterCancelDuringPairingDoesNotHang mirrors the audit's explicit
// ask: a Ctrl+C (ctx cancellation) while the QR is still unscanned must
// tear down cleanly, not hang. Stop() is what Controller.Stop calls after
// cancelling the same ctx passed to Start — see corepipeline.Controller.
func TestStopAfterCancelDuringPairingDoesNotHang(t *testing.T) {
	a, err := New(context.Background(), Config{DBPath: filepath.Join(t.TempDir(), "wm.db")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cancel() // the Ctrl+C moment: ctx.Done() before anyone scanned

	done := make(chan struct{})
	go func() {
		a.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop hung after ctx was cancelled mid-pairing")
	}
}

// TestResolvePNSurvivesNilStoreAndLIDs covers the nil-deref fix
// (ct-2026-07-23-0047 index 6): ResolvePN must not panic when called on an
// adapter that's alive but disconnected — nil client (zero-value Adapter)
// and nil LIDs (real adapter post-New, never connected) both hit the guard.
func TestResolvePNSurvivesNilStoreAndLIDs(t *testing.T) {
	a := &Adapter{}
	pn, err := a.ResolvePN(context.Background(), "1@lid")
	if pn != "" || err != nil {
		t.Fatalf("ResolvePN(nil client) = (%q, %v), want (\"\", nil)", pn, err)
	}

	a2, err := New(context.Background(), Config{DBPath: filepath.Join(t.TempDir(), "wm.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a2.Stop)
	// a2.client.Store.LIDs is nil — never connected after New. Crashed before fix.
	pn, err = a2.ResolvePN(context.Background(), "1@lid")
	if pn != "" || err != nil {
		t.Fatalf("ResolvePN(post-New, never connected) = (%q, %v), want (\"\", nil)", pn, err)
	}
}

func TestKillSwitchActiveNilSafeWhenNeitherWired(t *testing.T) {
	a := &Adapter{}
	if a.killSwitchActive() {
		t.Error("killSwitchActive() = true with neither governor nor state wired, want false")
	}
}

func TestKillSwitchActiveGovernorKilled(t *testing.T) {
	gov := governor.NewLimiter(10, time.Minute)
	gov.SetKill(true)
	a := &Adapter{governor: gov}
	if !a.killSwitchActive() {
		t.Error("killSwitchActive() = false with governor.Killed()=true, want true")
	}
}

func TestKillSwitchActiveStateMuted(t *testing.T) {
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
	if err := sm.SetMuted(true); err != nil {
		t.Fatal(err)
	}
	a := &Adapter{state: sm}
	if !a.killSwitchActive() {
		t.Error("killSwitchActive() = false with state.Muted=true, want true")
	}
}

// TestNewLogsNoExistingSession is a regression test for T25
// (ct-2026-08-05-1833): the smoke found a real upgrade where WhatsApp never
// connected and NOT ONE whatsmeow log line appeared — impossible to tell
// "not being called" from "failing before it could log". New() must always
// say, unambiguously, whether it found a previous session.
func TestNewLogsNoExistingSession(t *testing.T) {
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })

	a, err := New(context.Background(), Config{DBPath: filepath.Join(t.TempDir(), "wm.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Stop)

	if !strings.Contains(buf.String(), "sin sesión previa") {
		t.Errorf("New() log = %q, want it to say no previous session was found", buf.String())
	}
}

// TestNewLogsExistingSessionFound is TestNewLogsNoExistingSession's mirror —
// New() must say a previous session WAS found, with the jid, when one
// exists in the db (a real 0.1.1->0.1.3 upgrade's whatsmeow.db, this test's
// synthetic stand-in).
func TestNewLogsExistingSessionFound(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wm.db")
	seedFakeDevice(t, dbPath)

	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })

	a, err := New(context.Background(), Config{DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Stop)

	if a.client.Store.ID == nil {
		t.Fatal("test setup is wrong — seeded device should have a non-nil Store.ID")
	}
	if !strings.Contains(buf.String(), "sesión previa encontrada") || !strings.Contains(buf.String(), "5550000000000") {
		t.Errorf("New() log = %q, want it to name the found session's jid", buf.String())
	}
}

// TestRecordOwnIdentityWithEmptyPushNameLeavesNameBlank is T17's Part 1
// reproduction (ct-2026-08-05-1240): the boss's screenshot showed the
// header's número populated ("55500000042") but the name blank ("(sin
// nombre)") — WHILE THE GATEWAY WAS RUNNING, not just at boot. Citrino's own
// hypothesis (state.NewManager starts empty, the file is never re-read) was
// flagged as suspect by her: OwnJID/OwnName are written TOGETHER by the
// exact same recordOwnIdentity call (inbound.go), so a "blank at boot"
// window would leave BOTH blank, not just the name — that theory alone
// can't explain the screenshot.
//
// This reproduces the REAL mechanism instead: whatsmeow's Store.PushName is
// populated ONLY by a PushNameSetting appstate mutation (library's
// appstate.go:367) — never by anything queryable on demand (confirmed
// against the vendored library: GetUserInfo's own types.UserInfo has no
// name field at all, only Status/PictureID/Devices). A device seeded here
// exactly like TestNewLogsExistingSessionFound (paired: Store.ID != nil)
// but with push_name="" in the devices row (seedFakeDevice's own literal,
// matching a real device whose account-level push-name mutation was never
// replayed to this session) proves: recordOwnIdentity sets OwnJID
// unconditionally from Store.ID, but OwnName stays "" — exactly the
// screenshot's number-without-name — and NOTHING re-tries it: the only
// other trigger is the live *events.PushNameSetting event (inbound.go's
// handleEvent), which fires only if/when WhatsApp delivers that mutation
// again, not on a timer, not on every reconnect.
func TestRecordOwnIdentityWithEmptyPushNameLeavesNameBlank(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wm.db")
	seedFakeDevice(t, dbPath)
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)

	a, err := New(context.Background(), Config{DBPath: dbPath, State: sm})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Stop)

	if a.client.Store.ID == nil {
		t.Fatal("test setup is wrong — seeded device should be paired (Store.ID != nil)")
	}
	if a.client.Store.PushName != "" {
		t.Fatalf("test setup is wrong — seedFakeDevice wrote push_name=\"\", got Store.PushName = %q", a.client.Store.PushName)
	}

	a.recordOwnIdentity() // what handleConnected calls on every *events.Connected

	snap := sm.Snapshot()
	if snap.OwnJID == "" {
		t.Error("OwnJID = \"\" after recordOwnIdentity with a paired device, want it set (this is the half of the bug that DOES work — matches the boss's screenshot showing a real número)")
	}
	if snap.OwnName != "" {
		t.Errorf("OwnName = %q, want \"\" — reproduces the bug (name stays blank when Store.PushName is empty); if this now fails, the fix below made it recover on its own, update this test's expectation to match", snap.OwnName)
	}
}

// TestRecordOwnIdentityPreservesKnownNameWhenPushNameEmpty is T17's Part 1
// FIX regression: once OwnName is known (captured by an earlier successful
// connect, or seeded from a previous run's status.json — state.NewManager's
// own T17 fix), a LATER reconnect with an empty Store.PushName must not
// blank it back out. Before this fix, recordOwnIdentity wrote OwnName
// unconditionally on every call — the exact mechanism behind the boss's
// screenshot ("(sin nombre)" showing for the WHOLE runtime, not a brief
// window): whatever DID get captured once was wiped on the very next
// reconnect where Store.PushName came back empty again.
func TestRecordOwnIdentityPreservesKnownNameWhenPushNameEmpty(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wm.db")
	seedFakeDevice(t, dbPath)
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
	// Simulates a name already known — either an earlier successful capture
	// this same run, or NewManager's own disk-seed from a prior run.
	if err := sm.Update(func(s *state.Status) { s.OwnName = "Bigot Mckuco, Clever.cat" }); err != nil {
		t.Fatal(err)
	}

	a, err := New(context.Background(), Config{DBPath: dbPath, State: sm})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Stop)
	if a.client.Store.PushName != "" {
		t.Fatalf("test setup is wrong — want Store.PushName empty, got %q", a.client.Store.PushName)
	}

	a.recordOwnIdentity()

	if got := sm.Snapshot().OwnName; got != "Bigot Mckuco, Clever.cat" {
		t.Errorf("OwnName = %q after recordOwnIdentity with an empty Store.PushName, want the previously known name preserved", got)
	}
}

// TestRecordOwnIdentityStripsDeviceSuffixBeforeMarkingOwner is T45's
// missing test (Citrino froze the integration over this): the real bug
// lived in the SEAM between markOwner's two calls, TouchChat(jid) then
// MarkOwnerIfUntouched(jid) — TouchChat normalizes and creates the chat
// clean, but MarkOwnerIfUntouched is a plain `UPDATE ... WHERE jid = ?`
// with no normalization of its own; called with the SAME still-suffixed
// jid variable, it matches zero rows, silently. T45's original 5 tests
// exercised TouchChat and AddMessage in isolation — none went through
// markOwner (or recordOwnIdentity, its only real caller) end-to-end, so
// none could have caught this. seedFakeDevice's own ":0" device would not
// have reproduced it either (whatsmeow's JID.String() omits a zero
// device) — this uses seedFakeDeviceWithJID with a nonzero one, matching
// the real ":15" cut measured on the owner's installation.
func TestRecordOwnIdentityStripsDeviceSuffixBeforeMarkingOwner(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wm.db")
	seedFakeDeviceWithJID(t, dbPath, "55500000099.0:15@s.whatsapp.net")
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	a, err := New(context.Background(), Config{DBPath: dbPath, State: sm, Store: st})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Stop)
	if a.client.Store.ID == nil {
		t.Fatal("test setup is wrong — seeded device should be paired (Store.ID != nil)")
	}
	if a.client.Store.ID.Device == 0 {
		t.Fatal("test setup is wrong — want a NONZERO device (the real cut was :15), got 0 — this would not reproduce the bug")
	}

	a.recordOwnIdentity()

	if _, ok, err := st.GetChat("55500000099:15@s.whatsapp.net"); err != nil || ok {
		t.Errorf("GetChat(jid con sufijo) ok=%v err=%v, want ok=false — no debe quedar una fila bajo el jid crudo", ok, err)
	}
	c, ok, err := st.GetChat("55500000099@s.whatsapp.net")
	if err != nil || !ok {
		t.Fatalf("GetChat(jid limpio) ok=%v err=%v, want ok=true", ok, err)
	}
	if !c.IsBoss {
		t.Error("IsBoss = false — la regresión que Citrino frenó: TouchChat normalizaba el chat pero MarkOwnerIfUntouched seguía viendo el jid crudo y no marcaba nada")
	}
	if got := sm.Snapshot().OwnJID; got != "55500000099@s.whatsapp.net" {
		t.Errorf("state.Status.OwnJID = %q, want el jid limpio también ahí — recordOwnIdentity normaliza en el origen, antes de repartir el valor a state y al store", got)
	}
}
