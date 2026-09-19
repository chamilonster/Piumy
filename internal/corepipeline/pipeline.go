// Package corepipeline: the messaging-vendor-agnostic core. Owns the two
// loops any messaging client needs regardless of vendor — inbound handling
// and outbox draining — plus the Controller facade that starts/stops them
// together with a gateway.Gateway. Never imports open-wa; only the
// gateway.Gateway seam. Rewritten clean from Piumy's gateway.go (which
// mixed whatsmeow + this logic in one type) — see
// docs/F2-PIPELINE.md for what was ported and what wasn't.
package corepipeline

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"piumy-gateway/internal/eventbus"
	"piumy-gateway/internal/gateway"
	"piumy-gateway/internal/governor"
	"piumy-gateway/internal/router"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
)

// Config holds the pipeline's own tunables — deliberately separate from
// internal/config: corepipeline has no dependency on that package, the
// caller (main.go, F5) wires config.Config's values into this struct field
// by field. Zero fields fall back to defaults in New, same pattern as
// governor/mcpguard/sessionbackup.
type Config struct {
	// OutboxPoll is how often the outbox drain loop checks for due items.
	// Default 5s.
	OutboxPoll time.Duration
	// OutboxMaxRetry is how many failed send attempts an item gets before
	// it's dead-lettered (anti-ban: never resend forever). Default 5.
	OutboxMaxRetry int

	// DispatchDelayMin/Max bound the randomized human-pacing delay before
	// each outbound send. Default 1s/5s.
	DispatchDelayMin time.Duration
	DispatchDelayMax time.Duration
	// ReadDelayMin/Max bound the randomized delay before sending a read
	// receipt (anti-ban: never instant, even reads). Default 2s/8s.
	ReadDelayMin time.Duration
	ReadDelayMax time.Duration

	// ComposingMin/Max bound the brief "typing" window between showing the
	// composing indicator and actually sending (feel more human). Default
	// 500ms/1.5s — same as Piumy's hardcoded value, made configurable here
	// so tests can shrink it instead of paying it in wall-clock time.
	ComposingMin time.Duration
	ComposingMax time.Duration

	// ChunkMaxLen / ChunkDelayMin/Max (T101, ct-2026-08-29-1651): a reply
	// longer than ChunkMaxLen splits into several WhatsApp messages
	// (splitIntoChunks), with a randomized respiro in [ChunkDelayMin,
	// ChunkDelayMax] BETWEEN pieces of the SAME reply — its own, shorter
	// window than DispatchDelayMin/Max above, which paces between
	// DIFFERENT messages. Default 4000/400ms/1.5s.
	ChunkMaxLen   int
	ChunkDelayMin time.Duration
	ChunkDelayMax time.Duration
}

func defaultConfig(cfg Config) Config {
	if cfg.OutboxPoll <= 0 {
		cfg.OutboxPoll = 5 * time.Second
	}
	if cfg.OutboxMaxRetry <= 0 {
		cfg.OutboxMaxRetry = 5
	}
	if cfg.DispatchDelayMin <= 0 {
		cfg.DispatchDelayMin = 1 * time.Second
	}
	if cfg.DispatchDelayMax <= 0 || cfg.DispatchDelayMax < cfg.DispatchDelayMin {
		cfg.DispatchDelayMax = 5 * time.Second
	}
	if cfg.ReadDelayMin <= 0 {
		cfg.ReadDelayMin = 2 * time.Second
	}
	if cfg.ReadDelayMax <= 0 || cfg.ReadDelayMax < cfg.ReadDelayMin {
		cfg.ReadDelayMax = 8 * time.Second
	}
	if cfg.ComposingMin <= 0 {
		cfg.ComposingMin = 500 * time.Millisecond
	}
	if cfg.ComposingMax <= 0 || cfg.ComposingMax < cfg.ComposingMin {
		cfg.ComposingMax = 1500 * time.Millisecond
	}
	if cfg.ChunkMaxLen <= 0 {
		cfg.ChunkMaxLen = 4000
	}
	if cfg.ChunkDelayMin <= 0 {
		cfg.ChunkDelayMin = 400 * time.Millisecond
	}
	if cfg.ChunkDelayMax <= 0 || cfg.ChunkDelayMax < cfg.ChunkDelayMin {
		cfg.ChunkDelayMax = 1500 * time.Millisecond
	}
	return cfg
}

// Pipeline runs the inbound and outbox loops against a gateway.Gateway.
// Agnostic on purpose: store/router/governor/state/eventbus are all
// already-migrated packages (F1), nothing here knows about open-wa.
type Pipeline struct {
	gw    gateway.Gateway
	store *store.Store
	rt    *router.Manager
	gov   *governor.Limiter
	state *state.Manager
	cfg   Config

	// bus is optional (SetBus) — atomic.Pointer so it's safe to wire at any
	// time, including before Run(), without a dedicated lock on the hottest
	// path (every inbound message reads it once).
	bus atomic.Pointer[eventbus.Bus]

	// runCtx is stashed so MarkRead (triggered later by an MCP tool call,
	// F4) can spawn cancellable background work without threading ctx
	// through every caller.
	runMu  sync.Mutex
	runCtx context.Context
}

// New builds a Pipeline. gw, st, rt, gov, and sm must all be non-nil.
func New(gw gateway.Gateway, st *store.Store, rt *router.Manager, gov *governor.Limiter, sm *state.Manager, cfg Config) *Pipeline {
	return &Pipeline{
		gw:    gw,
		store: st,
		rt:    rt,
		gov:   gov,
		state: sm,
		cfg:   defaultConfig(cfg),
	}
}

// SetBus registers the event bus handleInbound publishes to. Safe to call
// at any time, including before Run().
func (p *Pipeline) SetBus(b *eventbus.Bus) {
	p.bus.Store(b)
}

func (p *Pipeline) context() context.Context {
	p.runMu.Lock()
	defer p.runMu.Unlock()
	if p.runCtx != nil {
		return p.runCtx
	}
	return context.Background()
}

func (p *Pipeline) dispatchDelay() governor.DelayWindow {
	return governor.DelayWindow{
		Min: p.store.SettingDuration(store.SettingDispatchDelayMin, p.cfg.DispatchDelayMin),
		Max: p.store.SettingDuration(store.SettingDispatchDelayMax, p.cfg.DispatchDelayMax),
	}
}

func (p *Pipeline) readDelay() governor.DelayWindow {
	return governor.DelayWindow{
		Min: p.store.SettingDuration(store.SettingReadDelayMin, p.cfg.ReadDelayMin),
		Max: p.store.SettingDuration(store.SettingReadDelayMax, p.cfg.ReadDelayMax),
	}
}

// chunkDelay is the respiro BETWEEN pieces of the SAME long reply (T101,
// ct-2026-08-29-1651) — its own window, deliberately separate from and
// shorter than dispatchDelay above (which paces between DIFFERENT
// messages).
func (p *Pipeline) chunkDelay() governor.DelayWindow {
	return governor.DelayWindow{
		Min: p.store.SettingDuration(store.SettingChunkDelayMin, p.cfg.ChunkDelayMin),
		Max: p.store.SettingDuration(store.SettingChunkDelayMax, p.cfg.ChunkDelayMax),
	}
}

// chunkMaxLen is the length at which an outbound reply gets split — live
// dashboard-editable like every other governor knob, falling back to
// cfg.ChunkMaxLen (env PIUMY_CHUNK_MAX_LEN, default 4000).
func (p *Pipeline) chunkMaxLen() int {
	return p.store.SettingInt(store.SettingChunkMaxLen, p.cfg.ChunkMaxLen)
}

// Run launches the inbound loop and the outbox drain loop, and blocks until
// ctx is cancelled AND both loops have actually returned. Waiting for
// outboxLoop here (not just inboundLoop) matters: Controller.Stop() returns
// as soon as Run returns, and a caller is free to close the store right
// after Stop() — if outboxLoop were still mid-processOutbox (e.g. blocked
// on a real gw.Send when ctx was cancelled), its trailing store writes
// (MarkSent/AddMessage) would race a closed store ("database is closed").
func (p *Pipeline) Run(ctx context.Context) {
	p.runMu.Lock()
	p.runCtx = ctx
	p.runMu.Unlock()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.outboxLoop(ctx)
	}()
	p.inboundLoop(ctx)
	wg.Wait()
}

func (p *Pipeline) inboundLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-p.gw.Inbound():
			if !ok {
				return
			}
			p.handleInbound(msg)
		}
	}
}

func (p *Pipeline) outboxLoop(ctx context.Context) {
	ticker := time.NewTicker(p.cfg.OutboxPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.processOutbox(ctx)
			// T91: reuses this SAME ticker rather than a new one — a lost
			// read receipt isn't urgent enough to justify its own loop, and
			// piggybacking means one fewer goroutine/interval to reason
			// about. retryReadReceipts paces itself internally regardless.
			p.retryReadReceipts(ctx)
		}
	}
}

// handleInbound: store (dedup'd by store.AddMessage) → eventbus nudge (only
// on a successful store) → TouchChat → recompute queue depth →
// state.Update(Queue,LastMsg) → a transient mood (vip/new_msg). Deliberately
// does NOT mark anything read (a read receipt must reflect real agent
// attention, not mere receipt — see Pipeline.MarkRead) and does NOT touch
// media (shim/no-op, post-MVP — Inbound.Type is stored on the message row
// above and nothing else happens with it here).
//
// msg.FromMe (T100, ct-2026-08-29-1649) is a different kind of event
// entirely — the OWNER answered a real contact from another device (the
// phone), not a new message needing the agent's attention — routed to
// handleOwnerReply instead of the rest of this function.
func (p *Pipeline) handleInbound(msg gateway.Inbound) {
	if msg.FromMe {
		p.handleOwnerReply(msg)
		return
	}
	m := store.Message{
		ChatJID:       msg.ChatJID,
		ID:            msg.MsgID,
		FromMe:        false,
		Sender:        msg.SenderJID,
		Text:          msg.Text,
		TS:            msg.TS,
		Type:          msg.Type,
		QuotedID:      msg.QuotedID,
		QuotedPreview: msg.QuotedPreview,
		Forwarded:     msg.Forwarded,
	}
	if err := p.store.AddMessage(m); err != nil {
		log.Printf("corepipeline: store message: %v", err)
	} else if b := p.bus.Load(); b != nil {
		b.Publish(eventbus.Event{Type: "message", JID: msg.ChatJID, TS: msg.TS})
	}

	// msg.PushName is the SENDER's own display name — correct as the chat's
	// name for a 1:1, but for a group it would overwrite the group's real
	// name (seeded by whatsmeow's seedGroups/GetJoinedGroups) with whoever
	// happens to send the next message (ct-2026-07-10-1758, boss escalation:
	// a real WhatsApp group's own name was clobbered to a member's pushname
	// during the smoke test). "" makes TouchChat a no-op on name (chat.go's
	// upsert only overwrites when the new value is non-empty).
	chatName := msg.PushName
	if store.IsGroupJID(msg.ChatJID) {
		chatName = ""
	}
	if err := p.store.TouchChat(msg.ChatJID, chatName, msg.TS); err != nil {
		log.Printf("corepipeline: touch chat: %v", err)
	}

	// Mirror the router's mode decision onto the chat. PendingDedicated (cAPI
	// push) and the auto-reply sweep both filter by chats.mode, so a fresh
	// inbound chat must adopt dec.Mode — otherwise it stays at the schema
	// default 'auto' (AddMessage created it above) and nothing ever dispatches
	// it. The router is the source of truth for mode; chats.mode is a
	// queryable cache of it.
	// ST-B fix (ct-2026-07-11-0741): SyncRouterMode, not SetMode — re-applied
	// every inbound, but now a no-op once the owner/agent has set the mode
	// explicitly (mode_source='manual', set by SetMode). Before this, a
	// manual set_mode/escalate call got silently reverted by the very next
	// inbound message from that chat.
	if err := p.store.SyncRouterMode(msg.ChatJID, p.rt.Resolve(msg.ChatJID).Mode); err != nil {
		log.Printf("corepipeline: set mode chat=%s: %v", msg.ChatJID, err)
	}

	qCount, err := p.store.CountPendingDedicated()
	if err != nil {
		log.Printf("corepipeline: count queue: %v", err)
	}

	preview := msg.Text
	if len(preview) > 80 {
		preview = preview[:80] + "…"
	}
	_ = p.state.Update(func(s *state.Status) {
		s.Queue = qCount
		if preview != "" {
			s.LastMsg = preview
		}
	})

	if p.rt.IsVIP(msg.ChatJID) {
		_ = p.state.React("vip", "the owner!", 6*time.Second)
	} else {
		_ = p.state.React("new_msg", "ooh! a message", 4*time.Second)
	}
}

// ownerReplyModel tags a stored message as the owner's own reply (from
// another linked device, not an AI-generated one) — messages.model is
// otherwise "" for inbound and the model name for an agent's outbound reply
// (T100, ct-2026-08-29-1649).
const ownerReplyModel = "boss"

// handleOwnerReply persists an outbound message the OWNER sent to a real
// contact from another device (the phone) and closes that chat's earlier
// pending queue — T100 (ct-2026-08-29-1649). whatsmeow.handleMessage only
// ever reaches here after ruling out an echo of THIS gateway's own device
// and the self-chat (Note-to-Self keeps going through ordinary handleInbound,
// unchanged).
//
// The bound for MarkHandledBefore is msg.TS — the outgoing message's OWN
// timestamp, never time.Now() — same rule the other five MarkHandledBefore
// callers already follow (send.go/admin_tools.go/chat.go): a message that
// arrives to the SAME chat AFTER the owner's reply must stay pending, not
// be silently swallowed by a too-generous bound.
//
// Deliberately skips SyncRouterMode and the vip/new_msg mood reaction that
// handleInbound applies to real inbound — this isn't new work waiting on
// the agent, it's the opposite: the owner already closed it.
func (p *Pipeline) handleOwnerReply(msg gateway.Inbound) {
	m := store.Message{
		ChatJID: msg.ChatJID,
		ID:      msg.MsgID,
		FromMe:  true,
		Text:    msg.Text,
		TS:      msg.TS,
		Type:    msg.Type,
		Model:   ownerReplyModel,
	}
	if err := p.store.AddMessage(m); err != nil {
		log.Printf("corepipeline: store owner reply: %v", err)
	} else if b := p.bus.Load(); b != nil {
		b.Publish(eventbus.Event{Type: "message", JID: msg.ChatJID, TS: msg.TS})
	}

	if err := p.store.MarkHandledBefore(msg.ChatJID, msg.TS); err != nil {
		log.Printf("corepipeline: mark handled before (owner reply) %s: %v", msg.ChatJID, err)
	}

	qCount, err := p.store.CountPendingDedicated()
	if err != nil {
		log.Printf("corepipeline: count queue: %v", err)
		return
	}
	_ = p.state.Update(func(s *state.Status) { s.Queue = qCount })
}

// MarkRead sends read receipts for the not-yet-read messages in msgs,
// paced by the anti-ban read delay, and returns immediately — the delay and
// the actual gw.MarkRead call run in the background so a caller (an MCP
// tool handler, F4) never blocks on it.
//
// msgs can span several senders (T127, ct-2026-09-02-2249): unlike
// capipush's own dispatch burst (single-sender by construction, T108),
// this is called with a chat's raw message list (get_messages) — a group
// mixes participants freely. Grouped by sender so each gets its own
// gw.MarkRead call, addressed correctly; one sender's failure doesn't
// block the others (independent WhatsApp-side receipts).
func (p *Pipeline) MarkRead(chatJID string, msgs []store.Message) {
	bySender := map[string][]string{}
	for _, m := range msgs {
		if needsReadReceipt(m) {
			bySender[m.Sender] = append(bySender[m.Sender], m.ID)
		}
	}
	if len(bySender) == 0 {
		return
	}

	ctx := p.context()
	go func() {
		p.readDelay().Sleep(ctx)
		if ctx.Err() != nil {
			return
		}
		// H2+H3 audit follow-up (M1, escalated into ct-2026-07-10-0540
		// since H6 trips the kill switch on a LoggedOut/TemporaryBan):
		// processOutbox already checks this, but read receipts are their
		// own outbound WhatsApp activity — without this, "kill everything"
		// wasn't actually true, sends stopped but read receipts kept going
		// out right when staying quiet matters most.
		if p.killSwitchActive() {
			return
		}
		for sender, ids := range bySender {
			if err := p.gw.MarkRead(ctx, chatJID, sender, ids); err != nil {
				log.Printf("corepipeline: mark read chat=%s sender=%s: %v", chatJID, sender, err)
				continue
			}
			now := time.Now().Unix()
			for _, id := range ids {
				if err := p.store.SetRead(chatJID, id, now); err != nil {
					log.Printf("corepipeline: store read receipt chat=%s id=%s: %v", chatJID, id, err)
				}
			}
		}
	}()
}

// needsReadReceipt reports whether m is an inbound message that hasn't been
// marked read yet.
func needsReadReceipt(m store.Message) bool {
	return !m.FromMe && m.ReadTS == 0
}

// readReceiptRetryAge (T91, ct-2026-08-28): how old an unread message has to
// be before retryReadReceipts will touch it — young enough that its FIRST
// mark-read attempt (MarkRead's own readDelay, or capipush's immediate
// post-dispatch call) hasn't necessarily fired yet doesn't belong here; this
// floor keeps the retry pass from racing that first attempt.
const readReceiptRetryAge = 60 * time.Second

// retryReadReceipts recovers read receipts a first attempt lost for good —
// the T91 bug: gw.MarkRead failing (typically "websocket not connected", the
// boss's own log evidence) was logged and dropped, no retry, the blue
// checkmark never came back even once the connection did. store.ReadTS==0 IS
// the durable "still owes a receipt" signal (both MarkRead above and
// capipush's own post-dispatch call only set it on SUCCESS) — no separate
// retry queue needed, this just re-asks the store what's still due.
//
// Anti-ban (boss's own past incident, cited in this contract): a backlog
// recovered all at once right after reconnect is exactly the pattern
// WhatsApp punishes. One chat at a time, SEQUENTIALLY — p.readDelay().Random()
// blocks between each chat before the next one even starts, unlike MarkRead's
// own fire-and-forget goroutine (fine for one chat; concurrent goroutines for
// N chats would all wake within the same few seconds, the exact burst this
// pass exists to avoid). Called from outboxLoop's own ticker (no new one) —
// each tick only pulls a capped batch, so a big backlog drains across several
// ticks too, not one.
func (p *Pipeline) retryReadReceipts(ctx context.Context) {
	if !p.gw.Connected() {
		return
	}
	if p.killSwitchActive() {
		return
	}
	pending, err := p.store.PendingReadReceipts(time.Now().Add(-readReceiptRetryAge).Unix(), 20)
	if err != nil {
		log.Printf("corepipeline: pending read receipts: %v", err)
		return
	}
	if len(pending) == 0 {
		return
	}

	// Grouped by (chat, sender) — T127, ct-2026-09-02-2249: a pending
	// backlog can mix a group's participants same as MarkRead's own list
	// can (see there). Still one gw.MarkRead per key, still sequential.
	type chatSender struct{ chatJID, sender string }
	byChatSender := map[chatSender][]string{}
	for _, m := range pending {
		key := chatSender{m.ChatJID, m.Sender}
		byChatSender[key] = append(byChatSender[key], m.ID)
	}
	for key, ids := range byChatSender {
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(p.readDelay().Random()):
		}
		if p.killSwitchActive() {
			return
		}
		if err := p.gw.MarkRead(ctx, key.chatJID, key.sender, ids); err != nil {
			log.Printf("corepipeline: retry mark read chat=%s sender=%s: %v", key.chatJID, key.sender, err)
			continue
		}
		now := time.Now().Unix()
		for _, id := range ids {
			if err := p.store.SetRead(key.chatJID, id, now); err != nil {
				log.Printf("corepipeline: store retried read receipt chat=%s id=%s: %v", key.chatJID, id, err)
			}
		}
	}
}
