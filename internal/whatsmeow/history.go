// HistorySync (ct-2026-07-19-0148, backup Sub 3) — WhatsApp pushes recent
// message history on link (and occasionally afterward); this persists it
// straight to the store, as backup, never dispatching it to the agent.
// PASSIVE: we never request a HistorySync ourselves, only process what
// arrives, so it needs no anti-ban pacing (unlike sync.go's contact/group
// backfill, which DOES hit the server per contact and must stay slow). No
// Piumy reference exists for this — whatsmeow's own documented pattern
// (client.go:940-948) is the guide: ParseWebMessage converts each history
// message into the SAME *events.Message shape live messages have, so
// persisting one is the same shape of work as a live inbound message, minus
// dispatching it anywhere.
//
// ct-2026-07-24-2004: the gradual ON_DEMAND history worker (historyworker.go,
// ct-2026-07-21-1306) was removed — 26 real ON_DEMAND requests against
// chats WITH existing messages all came back
// COMPLETE_AND_NO_MORE_MESSAGE_REMAIN_ON_PRIMARY with zero messages.
// WhatsApp only ever mirrors a partial slice of the phone's history to a
// companion device, by design (Meta's own FAQ, confirmed in parallel) — not
// a rescue mechanism for the phone's full past. The boss's own redefinition
// of the goal (verbatim, 2026-07-29): depth is built FORWARD, accumulating
// what arrives live — not rescued from the past. See
// docs/HISTORY-SYNC-REGRESION-2026-07-24.md for the full read. This file is
// now the ONLY history path: passive push in, store, done.
package whatsmeow

import (
	"log"
	"time"

	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"piumy-gateway/internal/eventbus"
	"piumy-gateway/internal/store"
)

// handleHistorySync walks every conversation/message WhatsApp pushed and
// persists each one. Best-effort per message: one bad JID or unparseable
// message logs and skips, never aborts the whole sync. Every chunk feeds
// recordPassiveHistoryActivity (items 1+2, ct-2026-07-23-0047) — the only
// progress signal the boss gets during the post-re-pareo push.
func (a *Adapter) handleHistorySync(evt *events.HistorySync) {
	var chatJIDs []string
	msgCount := 0
	for _, conv := range evt.Data.GetConversations() {
		chatJID, err := types.ParseJID(conv.GetID())
		if err != nil {
			log.Printf("whatsmeow: history sync: parse chat JID %q: %v", conv.GetID(), err)
			continue
		}
		msgs := conv.GetMessages()
		for _, histMsg := range msgs {
			msgEvt, err := a.client.ParseWebMessage(chatJID, histMsg.GetMessage())
			if err != nil {
				log.Printf("whatsmeow: history sync: parse message in %s: %v", chatJID, err)
				continue
			}
			a.persistHistoryMessage(msgEvt)
		}
		chatJIDs = append(chatJIDs, chatJID.String())
		msgCount += len(msgs)
	}
	// len(chatJIDs)>0 skips degenerate chunks (e.g. a PUSH_NAME sync carries
	// pushnames, no conversations at all) — nothing real to log or count as
	// activity in THIS function's own terms (messages/chats persisted).
	if len(chatJIDs) > 0 {
		// Progress field (evt.Data.GetProgress()) is deliberately NOT
		// trusted as the primary signal — undocumented in the vendored
		// .proto (no comment on the field), and the web research behind
		// this contract found other WhatsApp bridges don't rely on it
		// either. Logged here as best-effort color only.
		log.Printf("whatsmeow: history sync: chunk synctype=%s conversations=%d messages=%d progress=%d",
			evt.Data.GetSyncType(), len(chatJIDs), msgCount, evt.Data.GetProgress())
		a.recordPassiveHistoryActivity(chatJIDs, msgCount)
		a.nudgeHistorySync()
	}
	// A PUSH_NAME chunk correctly has zero conversations for THIS function
	// (it carries evt.Data.GetPushnames() instead — [{JID, Pushname}] pairs,
	// not messages) — there is genuinely nothing here for persistHistoryMessage
	// to do with it. But whatsmeow itself already wrote those push names into
	// its own local Store.Contacts before dispatching this event (the
	// library's handleHistoricalPushNames, user.go, runs on receipt of the
	// raw sync — this event fires after). That makes the chunk arriving HERE
	// a genuine signal, not noise (ct-2026-07-31, "no llegan contactos en
	// una instalación nueva"): it means Store.Contacts just changed, so this
	// is exactly the moment to re-run syncContacts and pick it up — instead
	// of waiting for the next 6h tick, which is what silently starved a
	// fresh install of any contact name for 20+ minutes. Do not "clean this
	// up" into a plain skip; the empty branch is not the point, the trigger
	// below is.
	if evt.Data.GetSyncType() == waHistorySync.HistorySync_PUSH_NAME {
		a.scheduleContactsSync()
	}
}

// nudgeHistorySync tells the dashboard "the chat list may have changed, go
// reload it" — DELIBERATELY a distinct Event.Type ("history_batch") from
// "message" (corepipeline's own event, the one that means new LIVE inbound
// work arrived). History must never look like a new message to whatever's
// listening on the eventbus (this file's own doc, same reasoning) — a
// future agent-facing subscriber that only knows to react to "message"
// stays untouched by this. restapi's SSE handler forwards any Type to the
// browser unchanged, no special-casing needed there.
//
// Publishes UNTHROTTLED, once per chunk (ct-2026-07-24-0527, Citrino's own
// architecture call): coalescing a real burst — a re-pareo push landed 4
// chunks in ~7s in ct-2026-07-29's capture, never one-per-message — lives
// in the dashboard's own debounced loaders, not here. That's more robust
// than a backend throttle: it also coalesces THIS event arriving close
// together with an unrelated one (a live "message" mid-sync, say), which a
// backend-side throttle scoped to just this one event source could never
// see.
func (a *Adapter) nudgeHistorySync() {
	if a.bus != nil {
		a.bus.Publish(eventbus.Event{Type: "history_batch", TS: time.Now().Unix()})
	}
}

// recordPassiveHistoryActivity updates the live sync-progress signal both
// freshPairingSyncWindow (its auto-extend) and HistorySyncStatus
// (GET /api/status's visibility fields) read — see Adapter.historySyncStats'
// doc. Called once per HistorySync event with at least one real
// conversation.
func (a *Adapter) recordPassiveHistoryActivity(chatJIDs []string, messages int) {
	a.historySyncMu.Lock()
	defer a.historySyncMu.Unlock()
	if a.historySyncStats.ChatsSeen == nil {
		a.historySyncStats.ChatsSeen = make(map[string]struct{})
	}
	for _, jid := range chatJIDs {
		a.historySyncStats.ChatsSeen[jid] = struct{}{}
	}
	a.historySyncStats.Messages += messages
	a.historySyncStats.LastChunkAt = time.Now()
}

// defaultHistoryFreshPairGraceWindow bounds how long after a FRESH pairing
// (never a routine reconnect — pairLoop in adapter.go is the only caller
// that stamps pairedAt) the dashboard's "still syncing" signal
// (HistorySyncStatus) stays true by default, before a real passive chunk
// arrives to extend it. No hard number exists for how long WhatsApp's own
// push takes (web research: "minutes to hours"); 30 min is a conservative
// default, KV-overridable (store.SettingHistoryFreshPairGraceWindow) if a
// real re-pareo shows it needs to be longer.
//
// ct-2026-07-24-2004: this window used to also gate the (now removed)
// ON_DEMAND history worker off while the passive push got first crack at
// every chat (M2, ct-2026-07-22-2342) — with that worker gone, this is
// purely the dashboard visibility signal's own window now.
//
// Items 1+2 (ct-2026-07-23-0047, Citrino-approved design): the window
// AUTO-EXTENDS while passive chunks keep arriving — anchored at
// max(pairedAt, last passive chunk seen), not just pairedAt — so a push
// that genuinely takes longer than the default doesn't report "done"
// mid-delivery. historyFreshPairAbsoluteCeiling is the defensive
// counterweight: without SOME hard ceiling, continued activity (or a bug)
// could report "still syncing" forever.
const defaultHistoryFreshPairGraceWindow = 30 * time.Minute

// historyFreshPairAbsoluteCeiling caps how long the window can EVER stay
// open after a fresh pairing, measured from pairedAt — regardless of how
// recently a passive chunk arrived. ponytail: 6h, wide enough for any real
// re-pareo's passive push (the research behind this contract found
// "minutes to hours", never days), tight enough to guarantee the dashboard
// signal eventually reports "done" even in a pathological case.
const historyFreshPairAbsoluteCeiling = 6 * time.Hour

// historySyncAnchor returns pairedAt (the fresh-pairing stamp) alongside
// the more recent of pairedAt and the last passive chunk seen — the shared
// "how long has it actually been since something happened" both
// freshPairingSyncWindow and HistorySyncStatus read, so the two signals
// can't drift apart from computing it two different ways.
func (a *Adapter) historySyncAnchor() (anchor, pairedAt time.Time) {
	a.pairedAtMu.Lock()
	pairedAt = a.pairedAt
	a.pairedAtMu.Unlock()
	a.historySyncMu.Lock()
	lastChunkAt := a.historySyncStats.LastChunkAt
	a.historySyncMu.Unlock()
	anchor = pairedAt
	if lastChunkAt.After(anchor) {
		anchor = lastChunkAt
	}
	return anchor, pairedAt
}

// freshPairingSyncWindow reports whether we're still inside the grace
// window following a FRESH pairing. pairedAt's zero value — the common
// case, every routine reconnect to an already-paired session — makes this
// always false; only pairLoop's one-time stamp after a NEW QR pairing ever
// makes it true. historyFreshPairAbsoluteCeiling wins even if chunks are
// still actively arriving — see its own doc.
func (a *Adapter) freshPairingSyncWindow() bool {
	anchor, pairedAt := a.historySyncAnchor()
	if pairedAt.IsZero() {
		return false
	}
	if time.Since(pairedAt) > historyFreshPairAbsoluteCeiling {
		return false
	}
	window := defaultHistoryFreshPairGraceWindow
	if a.store != nil {
		window = a.store.SettingDuration(store.SettingHistoryFreshPairGraceWindow, window)
	}
	return time.Since(anchor) < window
}

// HistorySyncStatus reports the live passive-sync visibility signal (item 1,
// ct-2026-07-23-0047) — restapi.HistorySyncProgress reads this for
// GET /api/status. active reuses freshPairingSyncWindow() itself (zero
// duplicated logic): being inside the fresh-pairing window IS the "still
// receiving" signal. lastActivityAgo is zero if pairing never happened this
// process (nothing to report); otherwise time since the more recent of
// pairing or the last passive chunk, even before the first chunk has
// arrived.
func (a *Adapter) HistorySyncStatus() (active bool, messages, chats int, lastActivityAgo time.Duration) {
	active = a.freshPairingSyncWindow()
	anchor, _ := a.historySyncAnchor()
	a.historySyncMu.Lock()
	messages = a.historySyncStats.Messages
	chats = len(a.historySyncStats.ChatsSeen)
	a.historySyncMu.Unlock()
	if !anchor.IsZero() {
		lastActivityAgo = time.Since(anchor)
	}
	return active, messages, chats, lastActivityAgo
}

// persistHistoryMessage is handleHistorySync's per-message write — split
// out so it's testable with a synthetic *events.Message (same approach as
// sync.go's backfillContacts), no real HistorySync protobuf needed.
//
// Deliberately DIFFERENT from handleMessage (the live path) in two ways:
//   - NO IsFromMe filter: history carries the owner's own old messages
//     too, and the backup wants them (boss: "guardar... absolutamente
//     toda la info"). handleMessage filters FromMe because a live
//     self-send is already captured via the outbox on its way out — that
//     path never applies to history.
//   - NO media download: a single HistorySync can carry thousands of
//     messages; downloading all their media would hammer the server
//     (anti-ban flood). Text/Type still reflect the media (mime/caption,
//     same as handleMessage) and captureMediaPending stores the download
//     reference (ct-2026-07-21-1437 parte 1) — only the actual file bytes
//     are deferred, to the media background worker (mediabgworker.go).
//
// Writes DIRECT to the store (AddMessage/TouchChat), never through
// a.inbound — history must never reach the agent as if it were new.
// AddMessage's own INSERT OR IGNORE dedups, so a HistorySync landing
// twice (WhatsApp can push more than one) is a harmless no-op re-write.
func (a *Adapter) persistHistoryMessage(evt *events.Message) {
	if a.store == nil {
		return
	}
	text := evt.Message.GetConversation()
	if text == "" {
		text = evt.Message.GetExtendedTextMessage().GetText()
	}
	msgType := evt.Info.Type
	// S7c (ct-2026-07-30-0524): same @lid-vs-número gap as the live path
	// (inbound.go's handleMessage) — resolveChatJID's JID-identity gate
	// works here too even though ParseWebMessage never sets AddressingMode/
	// SenderAlt/RecipientAlt (only GetPNForLID's fallback ever fires for
	// history, see resolveChatJID's own doc).
	chatJID := a.resolveChatJID(evt.Info.MessageSource).String()
	msgID := string(evt.Info.ID)
	m, isMedia := detectMedia(evt)
	if isMedia {
		msgType = m.mime
		if text == "" {
			text = m.caption
		}
		a.captureMediaPending(chatJID, msgID, evt.Info.Timestamp.Unix(), m)
	} else if text == "" {
		// S5 (ct-2026-07-30-031027), extended to the history path per
		// Citrino's explicit call (the live-path-only fix already cost a
		// whole subcontract once today, S7b/S7c, when the identical
		// @lid-vs-número gap was left unfixed here): the exact same
		// waE2E.Message oneof variants (protocol messages, reactions, poll
		// votes...) that produce type:"text" text:"" on the live path
		// arrive through ParseWebMessage too — UnwrapRaw runs inside it
		// (client.go), so evt.Message has the identical shape either way.
		// The backfill is MORE exposed, not less: one HistorySync chunk can
		// carry thousands of messages. Dropped here, never written to the
		// store at all.
		log.Printf("whatsmeow: history sync: mensaje sin texto ni media descartado (no es contenido de chat real) chat=%s id=%s campo=%s", chatJID, msgID, firstSetFieldName(evt.Message))
		return
	} else if msgType == "" {
		// S9 (ct-2026-07-30-031143): ParseWebMessage (whatsmeow's own
		// history-message constructor) never sets Info.Type at all — unlike
		// a live stanza, whose wire-level "type" attribute reads "text" for
		// a genuine text message (see inbound.go's handleMessage). Without
		// this, the exact SAME real message ends up type:"" via history and
		// type:"text" via the live path — two identical messages treated
		// differently by anything that filters on type. text is non-empty
		// here (the branch above already returned on empty), so this IS a
		// real text message; default its type the same way the live path
		// would have recorded it.
		msgType = "text"
	}
	reply := detectReply(evt.Message)
	if err := a.store.AddMessage(store.Message{
		ChatJID:       chatJID,
		ID:            msgID,
		FromMe:        evt.Info.IsFromMe,
		Sender:        evt.Info.Sender.String(),
		Text:          text,
		TS:            evt.Info.Timestamp.Unix(),
		Type:          msgType,
		QuotedID:      reply.quotedID,
		QuotedPreview: reply.quotedPreview,
		Forwarded:     reply.forwarded,
	}); err != nil {
		log.Printf("whatsmeow: history persist message %s/%s: %v", evt.Info.Chat, evt.Info.ID, err)
	}
	if err := a.store.TouchChat(chatJID, evt.Info.PushName, evt.Info.Timestamp.Unix()); err != nil {
		log.Printf("whatsmeow: history touch chat %s: %v", evt.Info.Chat, err)
	}
}
