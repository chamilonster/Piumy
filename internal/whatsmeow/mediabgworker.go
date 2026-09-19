// Background FIFO media backfill (ct-2026-07-21-1437 parte 2) — the boss's
// hybrid design's default half: unlike mediaworker.go's on-demand, per-chat
// fetch (parte 3, triggered by opening a chat), this walks EVERY chat's
// media_pending backlog as ONE global chronological queue
// (store.NextMediaPending), one item per pace window, gradually filling in
// every chat's media over time. Same actionDelay()/DelayWindow pacing
// mechanism sync.go's contact backfill already uses against WhatsApp's own
// servers (unlike historyworker.go's requests, which go to the owner's
// phone) — never bursty, respects the kill switch.
package whatsmeow

import (
	"context"
	"errors"
	"log"

	"piumy-gateway/internal/store"
)

// mediaBgWorkerLoop runs the gradual background backfill: one real download
// attempt per wait, waiting a random duration in actionDelay()'s window
// between attempts (same reasoning as backfillContacts/mediaworker.go's own
// on-demand fetch — variation, not a fixed tick). No-op while disconnected
// or while the kill switch/mute is active, checked every iteration since the
// backlog can be long enough for the switch to flip mid-drain.
func (a *Adapter) mediaBgWorkerLoop(ctx context.Context) {
	if a.store == nil || a.mediaDir == "" {
		return
	}
	for {
		a.actionDelay().Sleep(ctx)
		if ctx.Err() != nil {
			return
		}
		if !a.client.IsConnected() || a.killSwitchActive() {
			continue
		}
		a.downloadNextPendingMedia(ctx)
	}
}

// downloadNextPendingMedia advances the backfill by exactly ONE item — the
// globally oldest media_pending row under store.MaxMediaPendingAttempts —
// split out so it's testable without a live client's timing. A failed
// attempt increments the row's attempts counter (IncrementMediaPendingAttempts)
// instead of leaving it untouched — once it reaches
// store.MaxMediaPendingAttempts, NextMediaPending stops returning it, so it
// no longer blocks the rows behind it (Citrino catch, ct-2026-07-21-1437
// parte 2: a stale directPath never recovers, so it must eventually stop
// being retried).
func (a *Adapter) downloadNextPendingMedia(ctx context.Context) {
	if a.store == nil {
		return
	}
	p, ok, err := a.store.NextMediaPending(store.MaxMediaPendingAttempts)
	if err != nil {
		log.Printf("whatsmeow: media worker: next pending: %v", err)
		return
	}
	if !ok {
		return // backlog empty
	}
	if err := a.downloadMediaPending(ctx, p); err != nil {
		if errors.Is(err, errMediaDownloadInFlight) {
			return
		}
		if incErr := a.applyMediaDownloadFailure(p.ChatJID, p.MsgID, err); incErr != nil {
			log.Printf("whatsmeow: media worker: record failure %s/%s: %v", p.ChatJID, p.MsgID, incErr)
		}
		// ct-2026-07-24-0041: 403/410 = media permanentemente expirada
		// (WhatsApp's CDN no la sirve mas) — applyMediaDownloadFailure ya
		// saltó derecho al cap arriba (1 intento real, no 3).
		if isPermanentMediaDownloadError(err) {
			log.Printf("whatsmeow: media worker: giving up on %s/%s after 1 attempt (permanent): %v", p.ChatJID, p.MsgID, err)
			return
		}
		attempts := p.Attempts + 1
		if attempts >= store.MaxMediaPendingAttempts {
			log.Printf("whatsmeow: media worker: giving up on %s/%s after %d attempts, skipping from queue: %v", p.ChatJID, p.MsgID, attempts, err)
		} else {
			log.Printf("whatsmeow: media worker: %v (attempt %d/%d)", err, attempts, store.MaxMediaPendingAttempts)
		}
	}
}
