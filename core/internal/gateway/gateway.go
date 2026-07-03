// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
//
// Package gateway: WhatsApp gateway via whatsmeow, with runtime start/stop control.
//
// Responsibilities:
//   - Session persistence in a dedicated SQLite file (pure-Go modernc.org/sqlite,
//     CGO_ENABLED=0 safe). The device store is opened with sql.Open("sqlite", dsn)
//     and wrapped via sqlstore.NewWithDB(db, "sqlite3", nil) so whatsmeow sees the
//     sqlite3 SQL dialect without needing a CGO driver.
//   - QR / login flow: on QR event set mood "qr" / ShowQR / QRData; on
//     Connected set WAConnected, OwnJID, resting mood; on LoggedOut clear session.
//   - Incoming messages (events.Message): resolve via router, skip if not allowed,
//     store via store.AddMessage + TouchChat, update Queue + LastMsg, then
//     React("vip") for VIP contacts or React("new_msg") for everyone else.
//   - Outbox drain goroutine: polls store.PendingOutbox, applies governor, sends
//     with human pacing (random delay + composing presence) and marks sent.
//   - Reconnect with exponential backoff; pauses after MaxFails consecutive
//     failures (ReconnectPaused=true, mood "alert") — anti-ban rule.
//   - Runtime Start/Stop via Controller: the dashboard can link/unlink WhatsApp
//     without restarting the service. A built-in QR exposure cap (QRTimeout,
//     default 180 s) auto-stops the gateway if the device is not linked in time.
//
// Only started when PIMYWA_GATEWAY=whatsmeow at boot, or via POST /api/gateway.
package gateway

import (
	"context"
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	waStore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"pimywa/internal/eventbus"
	"pimywa/internal/governor"
	"pimywa/internal/media"
	"pimywa/internal/router"
	"pimywa/internal/state"
	"pimywa/internal/store"
)

// Config holds all tunable knobs for the gateway (zero hardcode).
type Config struct {
	// SessionDB is the path to the SQLite file for the WhatsApp device/session store.
	// Default: /opt/pimywa/data/wa.db  (env PIMYWA_SESSION_DB)
	SessionDB string

	// DeviceName is the label shown in WhatsApp > Linked devices. Set to a normal
	// value so it does NOT reveal the underlying library (anti-ban). Empty → "Piumy".
	DeviceName string

	// QRTimeout is how long the gateway exposes a QR code before auto-stopping.
	// The cap is armed only when the device is not yet linked (Store.ID == nil).
	// 0 disables the cap. Default: 180s (env PIMYWA_QR_TIMEOUT).
	QRTimeout time.Duration

	// MaxFails is the number of consecutive reconnect failures that will trigger
	// ReconnectPaused=true and stop retrying. Default: 5.
	MaxFails int

	// OutboxPoll is how often the outbox drain loop checks for pending messages.
	// Default: 5s.
	OutboxPoll time.Duration

	// OutboxMaxRetry is how many failed send attempts an outbox item gets
	// before it's dead-lettered (excluded from the send loop for good, never
	// deleted). Anti-ban: without this, a systematically failing send retries
	// forever every OutboxPoll tick — a resend loop. Default: 5.
	OutboxMaxRetry int

	// MinSendDelay / MaxSendDelay bound the randomized human-pacing delay before
	// each outbound message. Default: 1s / 5s.
	MinSendDelay time.Duration
	MaxSendDelay time.Duration

	// ReadDelayMin / ReadDelayMax bound the randomized delay before the gateway
	// marks an inbound message as read (WhatsApp read receipt) — anti-ban:
	// never instant, even reads. Default: 2s / 8s.
	ReadDelayMin time.Duration
	ReadDelayMax time.Duration

	// ActionDelayMin / ActionDelayMax bound the randomized delay before any
	// other WhatsApp-server-facing action (contact/group sync fetches, etc).
	// Default: 1s / 4s.
	ActionDelayMin time.Duration
	ActionDelayMax time.Duration

	// MediaDir is where downloaded images/videos/stickers are written to disk
	// (never SQLite). Default: /opt/pimywa/data/media (env PIMYWA_MEDIA_DIR).
	MediaDir string

	// MediaMaxMB is the total size cap (megabytes) for MediaDir. Size-only GC
	// (2026-07-01): oldest files deleted first once exceeded;
	// text/metadata in the messages table is never touched by it. <=0 disables
	// GC entirely. Default: 512 (env PIMYWA_MEDIA_MAX_MB, via config.go).
	MediaMaxMB int
}

// mediaGCInterval is how often the size-cap sweep runs — a background
// housekeeping cadence, not a per-message anti-ban delay, so (like
// dashboard's session GC) it isn't an env knob nobody asked for.
const mediaGCInterval = 30 * time.Minute

func defaultConfig(cfg Config) Config {
	if cfg.MediaDir == "" {
		cfg.MediaDir = "/opt/pimywa/data/media"
	}
	if cfg.MediaMaxMB <= 0 {
		// GC is mandatory (3rd commandment: media must never fill the SD), so
		// an unwired/zero value defaults to enabled, not disabled.
		cfg.MediaMaxMB = 512
	}
	if cfg.MaxFails <= 0 {
		cfg.MaxFails = 5
	}
	if cfg.OutboxPoll <= 0 {
		cfg.OutboxPoll = 5 * time.Second
	}
	if cfg.OutboxMaxRetry <= 0 {
		cfg.OutboxMaxRetry = 5
	}
	if cfg.MinSendDelay <= 0 {
		cfg.MinSendDelay = 1 * time.Second
	}
	if cfg.MaxSendDelay <= 0 || cfg.MaxSendDelay < cfg.MinSendDelay {
		cfg.MaxSendDelay = 5 * time.Second
	}
	if cfg.ReadDelayMin <= 0 {
		cfg.ReadDelayMin = 2 * time.Second
	}
	if cfg.ReadDelayMax <= 0 || cfg.ReadDelayMax < cfg.ReadDelayMin {
		cfg.ReadDelayMax = 8 * time.Second
	}
	if cfg.ActionDelayMin <= 0 {
		cfg.ActionDelayMin = 1 * time.Second
	}
	if cfg.ActionDelayMax <= 0 || cfg.ActionDelayMax < cfg.ActionDelayMin {
		cfg.ActionDelayMax = 4 * time.Second
	}
	return cfg
}

// dispatchDelay / readDelay / actionDelay expose the three named anti-ban
// delay windows (see governor.DelayWindow) — dispatch before sending, read
// before marking a message read, action before any other WhatsApp-server-
// facing call (contact/group sync, etc). Each reads a KV-override first,
// falling back to cfg's startup value (0753 — a dashboard edit applies live,
// no restart; the floor/ceiling clamp lives in restapi's write path, not
// here — these always trust whatever's stored).
func (g *Gateway) dispatchDelay() governor.DelayWindow {
	return governor.DelayWindow{
		Min: g.msgSt.SettingDuration(store.SettingDispatchDelayMin, g.cfg.MinSendDelay),
		Max: g.msgSt.SettingDuration(store.SettingDispatchDelayMax, g.cfg.MaxSendDelay),
	}
}

func (g *Gateway) readDelay() governor.DelayWindow {
	return governor.DelayWindow{
		Min: g.msgSt.SettingDuration(store.SettingReadDelayMin, g.cfg.ReadDelayMin),
		Max: g.msgSt.SettingDuration(store.SettingReadDelayMax, g.cfg.ReadDelayMax),
	}
}

func (g *Gateway) actionDelay() governor.DelayWindow {
	return governor.DelayWindow{
		Min: g.msgSt.SettingDuration(store.SettingActionDelayMin, g.cfg.ActionDelayMin),
		Max: g.msgSt.SettingDuration(store.SettingActionDelayMax, g.cfg.ActionDelayMax),
	}
}

// Gateway is the live WhatsApp connection.
type Gateway struct {
	cfg     Config
	msgSt   *store.Store
	stateMg *state.Manager
	rt      *router.Manager
	gov     *governor.Limiter
	client  *whatsmeow.Client
	dl      media.Downloader

	// onConnectedHook is invoked once when WhatsApp reports a successful connection.
	// Set by Controller to cancel the QR exposure timer on a successful link.
	onConnectedHook func()

	// disconnCh receives a signal when the server closes the WebSocket.
	// (client.Disconnect() does NOT emit events.Disconnected, so this channel
	// is only triggered by server-side/network disconnects.)
	disconnCh chan struct{}

	// runMu guards runCtx: the context passed to Start(), stashed so event
	// handlers that fire after a successful connect (delayed MarkRead, future
	// sync fetches) can spawn cancellable background work without threading
	// ctx through every whatsmeow event callback.
	runMu  sync.Mutex
	runCtx context.Context

	// bus is the low-latency SSE notifier (entregable D) — optional,
	// wired via Controller.SetBus. atomic.Pointer instead of a dedicated
	// mutex: it's a single pointer read on every inbound message (the
	// hottest path in the gateway) and a single write at startup, which is
	// exactly what atomic.Pointer is for — no lock contention on the read
	// side. A nil bus (never wired) is handled by eventbus.Bus itself
	// (nil-receiver-safe Publish), so onMessage doesn't need its own
	// nil-check either.
	bus atomic.Pointer[eventbus.Bus]
}

// SetBus registers the event bus onMessage publishes to. Safe to
// call at any time, including before Start() — mirrors SetPostLinkHook's
// contract (entregable A) on Controller.
func (g *Gateway) SetBus(b *eventbus.Bus) {
	g.bus.Store(b)
}

// context returns the context passed to the current/last Start() call, or
// context.Background() if Start() hasn't run yet.
func (g *Gateway) context() context.Context {
	g.runMu.Lock()
	defer g.runMu.Unlock()
	if g.runCtx != nil {
		return g.runCtx
	}
	return context.Background()
}

// New creates a Gateway: opens the session SQLite DB with modernc.org/sqlite
// (pure Go, CGO_ENABLED=0), wraps it in a whatsmeow sqlstore Container using
// dialect "sqlite3" (for SQL syntax), upgrades the schema, fetches/creates the
// first device, and builds the whatsmeow Client. Does NOT connect to WhatsApp.
func New(cfg Config, msgSt *store.Store, sm *state.Manager, rt *router.Manager, gov *governor.Limiter) (*Gateway, error) {
	cfg = defaultConfig(cfg)

	// Ensure parent directory exists.
	if dir := filepath.Dir(cfg.SessionDB); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	// Open with modernc.org/sqlite (driver "sqlite") — pure Go, no CGO.
	// modernc uses `_pragma=name(val)` DSN syntax (NOT mattn's `_foreign_keys=on`),
	// and applies each _pragma to EVERY pooled connection — which is required so
	// whatsmeow sees foreign_keys ON on its own connection (it errors otherwise).
	// foreign_keys: required by whatsmeow's sqlstore.
	// journal_mode=WAL + synchronous=NORMAL: power-loss resilience (3rd commandment)
	// — a sudden cut never corrupts the WhatsApp session (losing it = re-scan QR = ban risk).
	dsn := "file:" + cfg.SessionDB +
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	container := sqlstore.NewWithDB(db, "sqlite3", nil)
	// NewWithDB does not call Upgrade automatically — must call it ourselves.
	if err := container.Upgrade(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}

	device, err := container.GetFirstDevice(context.Background())
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	// Anti-ban: set the device label shown in WhatsApp > Linked devices BEFORE
	// pairing. The default reveals "whatsmeow" (an unofficial-client fingerprint
	// that invites a ban). DeviceProps is global and applied at pairing time, so
	// changing it requires re-linking the device -- verbatim: "para el futuro
	// sí, en el mío déjamelo como está" -- the
	// DEFAULT below only takes effect on a FUTURE pairing (a fresh link, or a
	// re-link after this device is forgotten); an ALREADY-linked session's
	// device name was already committed to WhatsApp's own servers at ITS
	// pairing time and does not change just because this default changed —
	// this line runs on every startup, but only feeds a NEW pairing, never
	// renames an existing one. The owner's current device intentionally keeps
	// showing "Pimywa" until it's re-linked.
	deviceName := cfg.DeviceName
	if deviceName == "" {
		deviceName = "Piumy"
	}
	waStore.DeviceProps.Os = proto.String(deviceName)

	// nil logger → whatsmeow uses a no-op logger (avoids importing waLog).
	client := whatsmeow.NewClient(device, nil)

	g := &Gateway{
		cfg:       cfg,
		msgSt:     msgSt,
		stateMg:   sm,
		rt:        rt,
		gov:       gov,
		client:    client,
		dl:        media.Downloader{Client: client, Dir: cfg.MediaDir},
		disconnCh: make(chan struct{}, 1),
	}

	client.AddEventHandler(g.handleEvent)
	return g, nil
}

// Start runs the gateway until ctx is cancelled. It launches the outbox drain
// goroutine and then enters the reconnect loop. It returns when ctx is done.
func (g *Gateway) Start(ctx context.Context) {
	g.runMu.Lock()
	g.runCtx = ctx
	g.runMu.Unlock()

	go g.drainOutbox(ctx)
	go g.gcMediaLoop(ctx)
	go g.syncLoop(ctx)
	g.reconnectLoop(ctx)
	g.client.Disconnect()
	log.Println("gateway: disconnected, stopped")
}

// gcMediaLoop periodically enforces the media size cap (3rd commandment:
// media must never fill the SD — text/metadata in messages is never
// touched). No-op if MediaMaxMB <= 0 at startup (disabling GC outright is a
// boot-time decision, not dashboard-runtime — see store.SettingMediaMaxMB
// for the retention SIZE, which IS runtime-editable, read fresh every tick).
func (g *Gateway) gcMediaLoop(ctx context.Context) {
	if g.cfg.MediaMaxMB <= 0 {
		return
	}
	ticker := time.NewTicker(mediaGCInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			maxMB := g.msgSt.SettingInt(store.SettingMediaMaxMB, g.cfg.MediaMaxMB)
			maxBytes := int64(maxMB) * 1024 * 1024
			deleted, freed, err := media.GC(g.msgSt, maxBytes)
			if err != nil {
				log.Printf("gateway: media GC: %v", err)
				continue
			}
			if deleted > 0 {
				log.Printf("gateway: media GC deleted %d files, freed %d bytes", deleted, freed)
			}
		}
	}
}

// reconnectLoop manages connection with exponential backoff (anti-ban rule).
func (g *Gateway) reconnectLoop(ctx context.Context) {
	// Drain any stale disconnect signal left from a previous Start/Stop cycle.
	select {
	case <-g.disconnCh:
	default:
	}

	failures := 0
	backoff := 5 * time.Second
	const maxBackoff = time.Hour

	for {
		if ctx.Err() != nil {
			return
		}

		log.Printf("gateway: connecting (attempt %d)", failures+1)
		if err := g.connect(ctx); err != nil {
			log.Printf("gateway: connect error: %v", err)
			failures++
		} else {
			// Connect() returned nil — WebSocket established.
			// Block here until the server disconnects or ctx is cancelled.
			select {
			case <-ctx.Done():
				return
			case <-g.disconnCh:
				log.Println("gateway: server-side disconnect")
				failures++
			}
		}

		if failures >= g.cfg.MaxFails {
			log.Printf("gateway: %d consecutive failures — pausing reconnect (anti-ban)", failures)
			// mood "paused", NOT "sleeping" or the
			// transient "alert" — this is a ban-risk signal (the governor
			// gave up after MaxFails and is backing off 12-24h), not
			// peaceful low-power sleep. The anti-ban rules explicitly want
			// this state VISIBLE (gap #3, "señal paused/baneado") — folding
			// it into sleeping's zzz would hide exactly what needs seeing.
			_ = g.stateMg.UpdateMood(func(s *state.Status) {
				s.ReconnectPaused = true
				s.WAConnected = false
				s.Mood = "paused"
				s.Speech = "paused -- check link"
			})
			return
		}

		log.Printf("gateway: waiting %v before next connect attempt", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// connect establishes the WebSocket connection. If the device has no stored ID
// (fresh/unlinked), it first opens a QR channel so the state machine can expose
// the QR code to the user (dashboard / status.json).
func (g *Gateway) connect(ctx context.Context) error {
	if g.client.Store.ID == nil {
		// New or unlinked device — QR login required.
		qrChan, err := g.client.GetQRChannel(ctx)
		if err != nil {
			return err
		}
		go g.handleQRChannel(ctx, qrChan)
	}
	return g.client.Connect()
}

// handleQRChannel reads QR codes from the channel and writes them into state.
func (g *Gateway) handleQRChannel(ctx context.Context, ch <-chan whatsmeow.QRChannelItem) {
	for item := range ch {
		switch item.Event {
		case whatsmeow.QRChannelEventCode:
			code := item.Code
			_ = g.stateMg.UpdateMood(func(s *state.Status) {
				s.Mood = "qr"
				s.ShowQR = true
				s.QRData = code
			})
			log.Printf("gateway: QR code ready (timeout=%v)", item.Timeout)

		case whatsmeow.QRChannelSuccess.Event:
			log.Println("gateway: QR pairing successful")
			_ = g.stateMg.Update(func(s *state.Status) {
				s.ShowQR = false
				s.QRData = ""
			})

		case whatsmeow.QRChannelTimeout.Event:
			log.Println("gateway: QR channel timed out")
			_ = g.stateMg.UpdateMood(func(s *state.Status) {
				s.ShowQR = false
				s.QRData = ""
				s.Mood = "error"
				s.Speech = "qr timeout"
			})

		default:
			if item.Error != nil {
				log.Printf("gateway: QR channel error: %v", item.Error)
			} else {
				log.Printf("gateway: QR channel event: %s", item.Event)
			}
		}
	}
}

// handleEvent is the whatsmeow event handler (registered in New).
func (g *Gateway) handleEvent(evt any) {
	switch v := evt.(type) {
	case *events.Connected:
		g.onConnected()

	case *events.LoggedOut:
		g.onLoggedOut(v)

	case *events.Disconnected:
		// Server closed the WebSocket (network error / server-side kick).
		// client.Disconnect() does NOT fire this, so reconnect is safe.
		select {
		case g.disconnCh <- struct{}{}:
		default:
		}

	case *events.Message:
		g.onMessage(v)

	case *events.Receipt:
		g.onReceipt(v)

	case *events.JoinedGroup:
		// Reactive sync: a brand new group doesn't need to wait for the next
		// periodic sweep to become a chat + get its members recorded.
		go g.syncGroup(v.JID, v.Name, v.Topic, v.Participants)
		go g.syncGroupInviteLink(g.context(), v.JID)

	case *events.GroupInfo:
		// Reactive sync: membership changed on an existing group — Join and
		// Leave both handled, no waiting for the next periodic sweep.
		if len(v.Join) > 0 {
			go func(groupJID string, joined []types.JID) {
				for _, u := range joined {
					if err := g.msgSt.AddGroupMember(u.String(), groupJID); err != nil {
						log.Printf("gateway: reactive group member %s/%s: %v", u, groupJID, err)
					}
				}
			}(v.JID.String(), v.Join)
		}
		if len(v.Leave) > 0 {
			go func(groupJID string, left []types.JID) {
				for _, u := range left {
					if err := g.msgSt.RemoveGroupMember(u.String(), groupJID); err != nil {
						log.Printf("gateway: reactive group leave %s/%s: %v", u, groupJID, err)
					}
				}
			}(v.JID.String(), v.Leave)
		}
		// Topic/invite-link CHANGE notifications (0130): WhatsApp pushes the
		// new value directly here — store it as-is, never re-fetch from the
		// server for this (best anti-ban path there is: zero extra calls).
		if v.Topic != nil {
			if err := g.msgSt.SetChatDescription(v.JID.String(), v.Topic.Topic); err != nil {
				log.Printf("gateway: reactive group description %s: %v", v.JID, err)
			}
		}
		if v.NewInviteLink != nil {
			if err := g.msgSt.SetGroupInviteLink(v.JID.String(), *v.NewInviteLink); err != nil {
				log.Printf("gateway: reactive group invite link %s: %v", v.JID, err)
			}
		}
	}
}

func (g *Gateway) onConnected() {
	jid := ""
	if g.client.Store.ID != nil {
		jid = g.client.Store.ID.String()
	}
	// Get queue depth for the correct resting mood.
	qCount, qErr := g.msgSt.CountPendingAdvanced()
	if qErr != nil {
		log.Printf("gateway: count queue on connect: %v", qErr)
	}
	restMood := g.stateMg.RestingMood(qCount)

	_ = g.stateMg.UpdateMood(func(s *state.Status) {
		s.WAConnected = true
		s.ShowQR = false
		s.QRData = ""
		s.OwnJID = jid
		s.ReconnectPaused = false
		s.Queue = qCount
		s.Mood = restMood
		s.Speech = "online"
	})
	log.Printf("gateway: connected, JID=%s", jid)

	// Cancel the QR exposure cap timer — device linked / reconnected successfully.
	if g.onConnectedHook != nil {
		g.onConnectedHook()
	}

	// One-shot initial backfill (contacts/groups → empty chats + chat_groups,
	// archived flags). Paced with actionDelay, runs in the background so
	// onConnected (called synchronously from whatsmeow's event dispatcher)
	// doesn't block on a potentially long, deliberately slow sweep.
	go g.syncContactsAndGroups(g.context())
}

func (g *Gateway) onLoggedOut(v *events.LoggedOut) {
	log.Printf("gateway: logged out (onConnect=%v, reason=%v)", v.OnConnect, v.Reason)
	// Clear persisted session so the next connect asks for a fresh QR.
	if g.client.Store.ID != nil {
		if err := g.client.Store.Delete(context.Background()); err != nil {
			log.Printf("gateway: failed to delete device store: %v", err)
		}
	}
	_ = g.stateMg.UpdateMood(func(s *state.Status) {
		s.WAConnected = false
		s.OwnJID = ""
		s.Mood = "error"
		s.Speech = "disconnected"
	})
}

// onReceipt records WhatsApp delivery/read acks against the stored message
// row. Applies to both directions: a receipt for something Piumy sent
// (confirms an outbound reply actually landed) and a receipt WhatsApp
// generates when marking someone else's inbound message read on our behalf.
func (g *Gateway) onReceipt(evt *events.Receipt) {
	kind := receiptKind(evt.Type)
	if kind == "" {
		return // retry/sender/played/etc — not delivered or read, nothing to record
	}
	chatJID := evt.Chat.String()
	ts := evt.Timestamp.Unix()
	for _, id := range evt.MessageIDs {
		var err error
		if kind == "delivered" {
			err = g.msgSt.SetDelivered(chatJID, string(id), ts)
		} else {
			err = g.msgSt.SetRead(chatJID, string(id), ts)
		}
		if err != nil {
			log.Printf("gateway: receipt (%s) chat=%s id=%s: %v", evt.Type, chatJID, id, err)
		}
	}
}

// receiptKind maps a WhatsApp receipt type to which store update it means:
// "delivered", "read", or "" (not tracked — retry/sender/played/etc receipts
// don't correspond to a messages.delivered_ts/read_ts column).
func receiptKind(t types.ReceiptType) string {
	switch t {
	case types.ReceiptTypeDelivered:
		return "delivered"
	case types.ReceiptTypeRead, types.ReceiptTypeReadSelf:
		return "read"
	default:
		return ""
	}
}

// resolvePN translates a WhatsApp "LID" (hidden/linked identity, server
// "lid") to the real phone-number JID, when whatsmeow has already learned
// the mapping — a local cache/DB lookup, not a network call, so no delay
// window applies here. Contacts sometimes arrive as @lid instead of
// @s.whatsapp.net; router.json's whitelist is keyed by real numbers, so
// without this translation a known contact silently falls through as
// unrecognized (default mode, outbound blocked by the whitelist guardrail).
// Returns jid unchanged if it isn't a LID or no mapping is known yet.
func (g *Gateway) resolvePN(ctx context.Context, jid types.JID) types.JID {
	if jid.Server != types.HiddenUserServer {
		return jid
	}
	pn, err := g.client.Store.LIDs.GetPNForLID(ctx, jid)
	if err != nil {
		log.Printf("gateway: resolve LID %s: %v", jid, err)
	}
	return pnFromLIDLookup(jid, pn, err)
}

// pnFromLIDLookup decides what resolvePN returns given a (possibly
// not-yet-known) LID→PN lookup result — factored out so the fallback logic
// is unit-testable without a live whatsmeow client/session.
func pnFromLIDLookup(original, lookedUp types.JID, err error) types.JID {
	if err != nil || lookedUp.IsEmpty() {
		return original
	}
	return lookedUp
}

func (g *Gateway) onMessage(evt *events.Message) {
	info := evt.Info

	// Skip own messages (the core does not auto-reply).
	if info.IsFromMe {
		return
	}

	// Translate @lid → real number when known (see resolvePN) BEFORE routing
	// or storing anything — router.json's whitelist is keyed by real numbers.
	ctx := g.context()
	chat := g.resolvePN(ctx, info.Chat)
	sender := g.resolvePN(ctx, info.Sender)

	chatJID := chat.String()
	dec := g.rt.Resolve(chatJID)
	if !dec.Allowed {
		return
	}

	// Extract plain text, unwrapping the containers whatsmeow leaves in place
	// (ephemeral / view-once / doc-with-caption / device-sent). Without this,
	// texts arrive empty — e.g. groups with disappearing messages wrap every
	// text in EphemeralMessage. Also picks up media captions.
	text := messageText(evt.Message)

	msgType := info.Type
	if msgType == "" {
		msgType = "text"
	}

	msg := store.Message{
		ChatJID: chatJID,
		ID:      string(info.ID),
		FromMe:  false,
		Sender:  sender.String(),
		Text:    text,
		TS:      info.Timestamp.Unix(),
		Type:    msgType,
	}
	if err := g.msgSt.AddMessage(msg); err != nil {
		log.Printf("gateway: store message: %v", err)
	} else if b := g.bus.Load(); b != nil {
		// Low-latency nudge for a connected agent (entregable D) —
		// only fired on a SUCCESSFUL store (publishing after a failed
		// AddMessage would wake an agent up to find nothing new). No message
		// content here on purpose: this is "go check", not a 2nd source of
		// truth — the agent fetches the real text via get_pending/
		// get_messages, which stay gated exactly as they always were.
		// Fires for EVERY inbound message regardless of chat mode
		// (auto/advanced) — filtering "is this actually pending" already
		// lives in store.PendingChats/PendingAdvanced; duplicating that
		// logic here would be a second place for it to drift out of sync.
		b.Publish(eventbus.Event{Type: "message", JID: chatJID, TS: info.Timestamp.Unix()})
	}

	// Update chat name from PushName (best-effort).
	if err := g.msgSt.TouchChat(chatJID, info.PushName, info.Timestamp.Unix()); err != nil {
		log.Printf("gateway: touch chat: %v", err)
	}

	// Recompute queue depth after storing the new message.
	qCount, qErr := g.msgSt.CountPendingAdvanced()
	if qErr != nil {
		log.Printf("gateway: count queue: %v", qErr)
	}

	// Update Queue and LastMsg atomically without touching the mood (Update,
	// not UpdateMood) so an in-flight React revert is not prematurely cancelled.
	preview := ""
	if text != "" {
		preview = text
		if len(preview) > 80 {
			preview = preview[:80] + "…"
		}
	}
	_ = g.stateMg.Update(func(s *state.Status) {
		s.Queue = qCount
		if preview != "" {
			s.LastMsg = preview
		}
	})

	// Trigger a transient mood appropriate to the sender.
	if g.rt.IsVIP(chatJID) {
		_ = g.stateMg.React("vip", "the owner!", 6*time.Second)
	} else {
		_ = g.stateMg.React("new_msg", "ooh! a message", 4*time.Second)
	}

	// NOTE: no automatic scheduleMarkRead here (removed, project decision
	// 2026-07-01) — the read receipt (blue ticks) must reflect that an agent
	// actually attended the message via MCP/API, not that the gateway merely
	// received it. See Controller.MarkRead / markReadMessages, triggered from
	// mcpserver's get_messages.
	g.scheduleMediaDownload(chatJID, string(info.ID), unwrapMessage(evt.Message), info.Timestamp.Unix())
}

// scheduleMediaDownload downloads an image/video/sticker attached to the
// message (if any) after a randomized action delay, then records it via
// store.AddMedia. No-op for messages without one of those three media types
// (most messages — text is already stored above, unconditionally). Runs in
// its own goroutine, same pattern and rationale as scheduleMarkRead.
//
// Skips video/image per the owner's dashboard toggles (0753), by type ×
// origin (group vs 1-1) — text/metadata is already stored regardless.
// Stickers are NEVER skipped (never skipped: "se tienen que guardar
// también todos los stickers") — only video/image are gated at all.
func (g *Gateway) scheduleMediaDownload(chatJID, msgID string, m *waE2E.Message, ts int64) {
	if g.mediaSkipped(media.Kind(m), isGroupJID(chatJID)) {
		return
	}
	ctx := g.context()
	go func() {
		g.actionDelay().Sleep(ctx)
		if ctx.Err() != nil {
			return
		}
		res, err := g.dl.Download(ctx, chatJID, msgID, m)
		if err != nil {
			log.Printf("gateway: media download chat=%s id=%s: %v", chatJID, msgID, err)
			return
		}
		if res == nil {
			return
		}
		if err := g.msgSt.AddMedia(store.Media{
			MsgID: msgID, ChatJID: chatJID, Path: res.Path, Mime: res.Mime, Size: res.Size, TS: ts,
		}); err != nil {
			log.Printf("gateway: add media chat=%s id=%s: %v", chatJID, msgID, err)
		}
	}()
}

// mediaSkipped reports whether the owner's dashboard settings (0753) say to
// skip downloading this kind of media from this kind of chat. Only "image"
// and "video" are gated; anything else (stickers, "") is never skipped.
func (g *Gateway) mediaSkipped(kind string, isGroup bool) bool {
	switch kind {
	case "video":
		if isGroup {
			return g.msgSt.SettingBool(store.SettingMediaSkipVideoGroup, false)
		}
		return g.msgSt.SettingBool(store.SettingMediaSkipVideoChat, false)
	case "image":
		if isGroup {
			return g.msgSt.SettingBool(store.SettingMediaSkipPhotoGroup, false)
		}
		return g.msgSt.SettingBool(store.SettingMediaSkipPhotoChat, false)
	default:
		return false
	}
}

// isGroupJID reports whether jid is a WhatsApp group chat (per the JID
// suffix convention, @g.us vs @s.whatsapp.net) — mirrors the same-named
// helpers in store/mcpserver/autoreply; each package keeps its own to avoid
// cross-package coupling for one string check.
func isGroupJID(jid string) bool {
	return strings.HasSuffix(jid, "@g.us")
}

// scheduleMarkRead sends the WhatsApp read receipt after a randomized delay
// (anti-ban: must be slow, even reads — a scraper marks things read
// instantly, a human doesn't). Runs in its own goroutine so onMessage, called
// synchronously from whatsmeow's event dispatcher, never blocks on it; tied
// to the gateway's run context so a Stop() doesn't leave it dangling.
func (g *Gateway) scheduleMarkRead(chat, sender types.JID, id types.MessageID, ts time.Time) {
	ctx := g.context()
	go func() {
		g.readDelay().Sleep(ctx)
		if ctx.Err() != nil {
			return
		}
		if err := g.client.MarkRead(ctx, []types.MessageID{id}, ts, chat, sender); err != nil {
			log.Printf("gateway: mark read chat=%s id=%s: %v", chat, id, err)
			return
		}
		// Persist locally too — nothing else sets messages.read_ts for our
		// own MarkRead calls (WhatsApp doesn't echo them back as a Receipt
		// event; those are for the OTHER side's read status on OUR sends).
		if err := g.msgSt.SetRead(chat.String(), string(id), time.Now().Unix()); err != nil {
			log.Printf("gateway: store read receipt chat=%s id=%s: %v", chat, id, err)
		}
	}()
}

// markReadMessages marks every not-yet-read inbound message in msgs as read
// (with the anti-ban read delay) — the entry point an agent's actual
// attention (via mcpserver) triggers, now that reads are no longer
// automatic. Groups nothing — WhatsApp's MarkRead is per (chat, sender), so
// each message schedules its own call; sender usually matches chat in a 1:1
// DM but can differ in a group.
func (g *Gateway) markReadMessages(chatJID string, msgs []store.Message) {
	chat, err := types.ParseJID(chatJID)
	if err != nil {
		log.Printf("gateway: mark read: invalid chat JID %q: %v", chatJID, err)
		return
	}
	for _, m := range msgs {
		if !needsReadReceipt(m) {
			continue
		}
		sender, err := types.ParseJID(m.Sender)
		if err != nil {
			log.Printf("gateway: mark read: invalid sender %q: %v", m.Sender, err)
			continue
		}
		g.scheduleMarkRead(chat, sender, types.MessageID(m.ID), time.Unix(m.TS, 0))
	}
}

// needsReadReceipt reports whether m is an inbound message that hasn't been
// marked read yet — the filter markReadMessages applies before scheduling
// any WhatsApp read receipt (never touch outbound or already-read messages).
func needsReadReceipt(m store.Message) bool {
	return !m.FromMe && m.ReadTS == 0
}

// unwrapMessage descends through the container messages whatsmeow does NOT
// unwrap for us (ephemeral, view-once v1/v2, document-with-caption, device-sent),
// returning the innermost real message. The iteration cap guards against a
// malformed self-referential chain.
func unwrapMessage(m *waE2E.Message) *waE2E.Message {
	for i := 0; i < 8 && m != nil; i++ {
		switch {
		case m.GetEphemeralMessage().GetMessage() != nil:
			m = m.GetEphemeralMessage().GetMessage()
		case m.GetViewOnceMessage().GetMessage() != nil:
			m = m.GetViewOnceMessage().GetMessage()
		case m.GetViewOnceMessageV2().GetMessage() != nil:
			m = m.GetViewOnceMessageV2().GetMessage()
		case m.GetViewOnceMessageV2Extension().GetMessage() != nil:
			m = m.GetViewOnceMessageV2Extension().GetMessage()
		case m.GetDocumentWithCaptionMessage().GetMessage() != nil:
			m = m.GetDocumentWithCaptionMessage().GetMessage()
		case m.GetDeviceSentMessage().GetMessage() != nil:
			m = m.GetDeviceSentMessage().GetMessage()
		default:
			return m
		}
	}
	return m
}

// messageText returns the human-readable text of a message: plain conversation,
// extended (formatted / reply / link) text, or the caption of an image / video /
// document. Returns "" for media without a caption.
func messageText(m *waE2E.Message) string {
	m = unwrapMessage(m)
	if m == nil {
		return ""
	}
	if t := m.GetConversation(); t != "" {
		return t
	}
	if t := m.GetExtendedTextMessage().GetText(); t != "" {
		return t
	}
	if img := m.GetImageMessage(); img != nil {
		return img.GetCaption()
	}
	if vid := m.GetVideoMessage(); vid != nil {
		return vid.GetCaption()
	}
	if doc := m.GetDocumentMessage(); doc != nil {
		return doc.GetCaption()
	}
	return ""
}

// drainOutbox polls store.PendingOutbox and sends each entry via whatsmeow
// with human pacing (random delay + composing presence) while respecting the
// governor anti-ban limiter. Runs until ctx is cancelled.
func (g *Gateway) drainOutbox(ctx context.Context) {
	ticker := time.NewTicker(g.cfg.OutboxPoll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.processOutbox(ctx)
		}
	}
}

func (g *Gateway) processOutbox(ctx context.Context) {
	if !g.client.IsConnected() {
		return
	}

	pending, err := g.msgSt.DueOutbox(10, time.Now().Unix())
	if err != nil {
		log.Printf("gateway: pending outbox: %v", err)
		return
	}

	for _, item := range pending {
		if ctx.Err() != nil {
			return
		}
		if g.gov.Killed() {
			log.Println("gateway: kill switch active, skipping outbox")
			return
		}
		if !g.gov.Allow() {
			log.Println("gateway: rate-limited, deferring outbox")
			return
		}

		jid, err := types.ParseJID(item.ToJID)
		if err != nil {
			log.Printf("gateway: invalid JID %q: %v — skipping", item.ToJID, err)
			if markErr := g.msgSt.MarkSent(item.Seq); markErr != nil {
				log.Printf("gateway: mark sent (bad JID): %v", markErr)
			}
			continue
		}

		// Human pacing: wait a randomized delay, then show composing.
		delay := g.dispatchDelay().Random()
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}

		if g.gov.Killed() {
			return
		}

		// Composing presence (typing indicator).
		if err := g.client.SendChatPresence(ctx, jid, types.ChatPresenceComposing, types.ChatPresenceMediaText); err != nil {
			log.Printf("gateway: composing presence: %v", err)
		}

		// Brief composing window before sending (feel more human).
		select {
		case <-ctx.Done():
			return
		case <-time.After((governor.DelayWindow{Min: 500 * time.Millisecond, Max: 1500 * time.Millisecond}).Random()):
		}

		// Send the message.
		resp, sendErr := g.client.SendMessage(ctx, jid, &waE2E.Message{
			Conversation: proto.String(item.Text),
		})

		// Stop composing indicator regardless of send result.
		if err := g.client.SendChatPresence(ctx, jid, types.ChatPresencePaused, types.ChatPresenceMediaText); err != nil {
			log.Printf("gateway: paused presence: %v", err)
		}

		if sendErr != nil {
			log.Printf("gateway: send message seq=%d: %v", item.Seq, sendErr)
			g.retryOrDeadLetter(item, sendErr)
			continue
		}

		if err := g.msgSt.MarkSent(item.Seq); err != nil {
			log.Printf("gateway: mark sent seq=%d: %v", item.Seq, err)
		}

		// Record the outbound message with its real WhatsApp ID (so delivery/
		// read receipts, which reference that ID, can match it later) and its
		// model attribution (empty for a human-sent REST/dashboard message).
		if err := g.msgSt.AddMessage(sentMessageRow(jid.String(), item, resp)); err != nil {
			log.Printf("gateway: store sent message seq=%d: %v", item.Seq, err)
		}

		// SENT counter: recompute from the messages
		// table (AFTER the AddMessage above, so this send is included) —
		// never a separately incremented counter, see state.Status.Sent's
		// doc comment for why that would drift. Update, not UpdateMood: a
		// counter refresh must never disturb an in-flight React/mood.
		if sent, err := g.msgSt.CountOutboundSince(0); err != nil {
			log.Printf("gateway: count sent: %v", err)
		} else {
			_ = g.stateMg.Update(func(s *state.Status) { s.Sent = sent })
		}

		log.Printf("gateway: sent outbox seq=%d to %s", item.Seq, item.ToJID)
	}
}

// retryOrDeadLetter is the anti-ban core of this: a failed send must NEVER
// just retry again next tick forever (that's a resend loop = ban risk). Bump
// retry_count and back off exponentially; once retry_count reaches
// OutboxMaxRetry, dead-letter the item instead (out of the send loop for
// good, but never deleted — it stays for inspection).
func (g *Gateway) retryOrDeadLetter(item store.Outbox, sendErr error) {
	retryCount := item.RetryCount + 1
	if retryCount >= g.cfg.OutboxMaxRetry {
		if err := g.msgSt.DeadLetterOutbox(item.Seq, sendErr.Error()); err != nil {
			log.Printf("gateway: dead-letter seq=%d: %v", item.Seq, err)
			return
		}
		log.Printf("gateway: outbox seq=%d dead-lettered after %d failures", item.Seq, retryCount)
		return
	}
	nextRetry := time.Now().Add(exponentialBackoff(retryCount)).Unix()
	if err := g.msgSt.SetOutboxRetry(item.Seq, retryCount, nextRetry, sendErr.Error()); err != nil {
		log.Printf("gateway: set outbox retry seq=%d: %v", item.Seq, err)
	}
}

// exponentialBackoff returns 5s * 2^retryCount capped at 1h — the same
// growth pattern reconnectLoop already uses for reconnect attempts, applied
// here to outbox send retries. Anti-ban: minimum is always > 0 (5s), never
// instant, and retryCount is always >= 1 when called.
func exponentialBackoff(retryCount int) time.Duration {
	const base = 5 * time.Second
	const maxBackoff = time.Hour
	backoff := base
	for i := 1; i < retryCount; i++ {
		backoff *= 2
		if backoff > maxBackoff {
			return maxBackoff
		}
	}
	return backoff
}

// sentMessageRow builds the messages row to record for a successfully sent
// outbox item. Uses the real WhatsApp response (ID + timestamp) rather than
// anything guessed at enqueue time, so delivery/read receipts — which
// reference the ID WhatsApp actually assigned — can match it later.
func sentMessageRow(chatJID string, item store.Outbox, resp whatsmeow.SendResponse) store.Message {
	return store.Message{
		ChatJID: chatJID,
		ID:      string(resp.ID),
		FromMe:  true,
		Text:    item.Text,
		TS:      resp.Timestamp.Unix(),
		Type:    "text",
		Model:   item.Model,
	}
}

// ── Controller ────────────────────────────────────────────────────────────────

// Controller manages the gateway lifecycle at runtime: Start/Stop on demand,
// optional QR exposure cap (PIMYWA_QR_TIMEOUT). Created once at boot; reused
// across link/disconnect cycles without restarting the service.
type Controller struct {
	cfg   Config
	msgSt *store.Store
	sm    *state.Manager
	rt    *router.Manager
	gov   *governor.Limiter
	gw    *Gateway

	mu           sync.Mutex
	running      bool
	cancel       context.CancelFunc
	doneCh       chan struct{} // closed by the running goroutine when it exits
	qrTimer      *time.Timer   // non-nil while the QR exposure cap is active
	postLinkHook func()        // optional, set via SetPostLinkHook
}

// NewController creates a Controller: opens the session DB, builds the whatsmeow
// client, and wires the connected hook. Does NOT connect to WhatsApp; call Start().
func NewController(cfg Config, msgSt *store.Store, sm *state.Manager, rt *router.Manager, gov *governor.Limiter) (*Controller, error) {
	gw, err := New(cfg, msgSt, sm, rt, gov)
	if err != nil {
		return nil, err
	}
	c := &Controller{
		cfg:   cfg,
		msgSt: msgSt,
		sm:    sm,
		rt:    rt,
		gov:   gov,
		gw:    gw,
	}
	// Wire the connected hook: cancel the QR timer when the device links,
	// then run any caller-supplied post-link hook (e.g. an
	// immediate session backup after a fresh scan; see SetPostLinkHook).
	// Fires on EVERY successful connect, not just a first-ever link
	// (reconnects included) — same event whatsmeow reports either way.
	gw.onConnectedHook = func() {
		c.mu.Lock()
		if c.qrTimer != nil {
			c.qrTimer.Stop()
			c.qrTimer = nil
		}
		hook := c.postLinkHook
		c.mu.Unlock()
		if hook != nil {
			hook()
		}
	}
	return c, nil
}

// Running reports whether the gateway goroutine is currently active
// (connecting, waiting for QR scan, or connected).
func (c *Controller) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

// SetPostLinkHook registers fn to run every time WhatsApp reports a
// successful connection — fresh link AND every later reconnect
// alike, same as the QR-timer-cancel logic already wired in NewController.
// Called synchronously from the whatsmeow event dispatcher via
// onConnectedHook, so fn must not block; do slow work in its own goroutine.
// Safe to call at any time, including before Start().
func (c *Controller) SetPostLinkHook(fn func()) {
	c.mu.Lock()
	c.postLinkHook = fn
	c.mu.Unlock()
}

// SetBus registers the event bus onMessage publishes to (entregable
// D) — delegates straight to the inner Gateway, no Controller-level state of
// its own needed. Safe to call at any time, including before Start().
func (c *Controller) SetBus(b *eventbus.Bus) {
	c.gw.SetBus(b)
}

// MarkRead schedules honest read receipts (with the anti-ban read delay) for
// inbound messages an agent just retrieved via MCP/API — reads must reflect
// real attention, not the gateway merely receiving something (2026-07-01).
// No-op if the gateway isn't currently connected.
func (c *Controller) MarkRead(chatJID string, msgs []store.Message) {
	c.mu.Lock()
	running := c.running
	c.mu.Unlock()
	if !running {
		return
	}
	c.gw.markReadMessages(chatJID, msgs)
}

// Start launches the WhatsApp connection loop in a background goroutine.
// Idempotent — no-op if already running.
// If the device is not yet linked (no stored session), the QR exposure cap timer
// (Config.QRTimeout) is armed; the gateway auto-stops if not linked in time.
// Linked devices (session already stored) reconnect silently with no timer.
func (c *Controller) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		return nil
	}

	// Clear any previous paused state so the UI does not show a stale warning.
	_ = c.sm.Update(func(s *state.Status) {
		s.ReconnectPaused = false
	})

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan struct{})
	c.cancel = cancel
	c.doneCh = doneCh
	c.running = true

	// Arm the cap only for unlinked devices that need to show a QR.
	needsQR := c.gw.client.Store.ID == nil

	go func() {
		defer func() {
			// Goroutine exiting: update running flag then signal Stop() waiters.
			c.mu.Lock()
			c.running = false
			c.mu.Unlock()
			close(doneCh)
		}()

		c.gw.Start(ctx)

		// Natural exit (max failures / ReconnectPaused): mood already set to
		// "alert" by the reconnect loop, but QR fields may still be populated.
		if ctx.Err() == nil {
			_ = c.sm.Update(func(s *state.Status) {
				s.ShowQR = false
				s.QRData = ""
			})
		}
	}()

	if needsQR && c.cfg.QRTimeout > 0 {
		c.qrTimer = time.AfterFunc(c.cfg.QRTimeout, func() {
			log.Printf("gateway: QR exposure cap reached (%v) — auto-stopping", c.cfg.QRTimeout)
			c.Stop()
		})
	}

	return nil
}

// Resume clears the reconnect-paused state and restarts the gateway loop.
// It is equivalent to Start() and is idempotent if the gateway is already
// running. Intended for the dashboard "Resume connection" button that appears
// when ReconnectPaused=true (gateway stopped after MaxFails consecutive errors).
// Start() already clears ReconnectPaused and resets the failure counter.
func (c *Controller) Resume() error {
	return c.Start()
}

// Stop cancels the connection loop, waits for it to exit cleanly, and resets
// state to idle / clears QR. Idempotent — safe to call when already stopped.
func (c *Controller) Stop() {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return
	}
	// Mark stopped immediately so concurrent callers (QR timer + manual) short-circuit.
	c.running = false
	if c.qrTimer != nil {
		c.qrTimer.Stop()
		c.qrTimer = nil
	}
	cancel := c.cancel
	c.cancel = nil
	doneCh := c.doneCh
	c.mu.Unlock()

	cancel() // signal the gateway goroutine to exit
	<-doneCh // wait for clean disconnect

	// Reset UI state to a neutral idle position.
	_ = c.sm.UpdateMood(func(s *state.Status) {
		s.ShowQR = false
		s.QRData = ""
		s.WAConnected = false
		s.OwnJID = ""
		s.Mood = "idle"
		s.Speech = ""
	})
	log.Println("gateway: controller stopped")
}
