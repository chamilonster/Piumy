// On-demand parallel media fetch (ct-2026-07-21-1437 parte 3) — the boss's
// hybrid design's "abrir chat" half: unlike mediabgworker.go's background
// worker (parte 2, one item at a time, paced, always running), this fires
// when the popup opens a chat and drains THAT chat's media_pending backlog
// with a small concurrent pool — fast, no pacing between items, mimicking a
// browser loading a page's images. Coordinated with the background worker
// via claimMediaDownload/downloadMediaPending (media.go) so the two never
// download the same row twice.
//
// Was ct-2026-07-21-1358's placeholder (FIFO, one at a time, permanently
// erroring — the raw download reference didn't exist anywhere yet). Parte 1
// closed that gap (media_pending); this rewrite is what actually uses it.
package whatsmeow

import (
	"context"
	"errors"
	"log"
	"sync"

	"piumy-gateway/internal/store"
)

// onDemandMediaConcurrency bounds how many downloads run at once for one
// chat's on-demand fetch — fast enough to feel instant when the popup
// opens a chat, small enough to stay a normal "loading images" burst, not a
// scrape. Not config: a fixed small number, no reason for an owner to tune it.
const onDemandMediaConcurrency = 4

// FetchPendingMedia satisfies restapi.MediaFetcher — starts the parallel
// on-demand fetch in the background and returns immediately, so the HTTP
// handler that triggers it never blocks on however long the chat's backlog
// takes.
func (a *Adapter) FetchPendingMedia(chatJID string) {
	go a.fetchPendingMediaParallel(a.context(), chatJID)
}

// fetchPendingMediaParallel drains chatJID's own media_pending backlog
// (store.MediaPendingForChat, oldest first, same store.MaxMediaPendingAttempts
// cap the background worker uses — retrying in parallel doesn't make a
// stale directPath valid again) through a bounded pool of
// onDemandMediaConcurrency goroutines. No actionDelay pacing here — the
// point of parte 3 is speed/priority when the boss is actively looking at
// a chat; the kill switch is still respected, checked once before starting
// and once per item since a large backlog could take a moment to drain.
func (a *Adapter) fetchPendingMediaParallel(ctx context.Context, chatJID string) {
	if a.store == nil {
		return
	}
	pending, err := a.store.MediaPendingForChat(chatJID, store.MaxMediaPendingAttempts)
	if err != nil {
		log.Printf("whatsmeow: on-demand media fetch: pending %s: %v", chatJID, err)
		return
	}

	sem := make(chan struct{}, onDemandMediaConcurrency)
	var wg sync.WaitGroup
	for _, p := range pending {
		if ctx.Err() != nil || a.killSwitchActive() {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(p store.MediaPending) {
			defer wg.Done()
			defer func() { <-sem }()
			a.fetchOnePendingMedia(ctx, p)
		}(p)
	}
	wg.Wait()
}

// fetchOnePendingMedia downloads one row for the on-demand pool —
// increments attempts on a real failure, same bookkeeping
// downloadNextPendingMedia (mediabgworker.go) does, INCLUDING its
// isPermanentMediaDownloadError fast-fail (ct-2026-07-29 fix: this on-demand
// path never checked it before, so a 403/410 opened from the popup burned
// the same 3-real-attempts-to-give-up bug the background worker had), except
// a errMediaDownloadInFlight collision (the background worker already has
// this row) is never counted as a failure.
func (a *Adapter) fetchOnePendingMedia(ctx context.Context, p store.MediaPending) {
	err := a.downloadMediaPending(ctx, p)
	if err == nil {
		return
	}
	if errors.Is(err, errMediaDownloadInFlight) {
		return
	}
	if incErr := a.applyMediaDownloadFailure(p.ChatJID, p.MsgID, err); incErr != nil {
		log.Printf("whatsmeow: on-demand media fetch: record failure %s/%s: %v", p.ChatJID, p.MsgID, incErr)
	}
	if isPermanentMediaDownloadError(err) {
		log.Printf("whatsmeow: on-demand media fetch: giving up on %s/%s after 1 attempt (permanent): %v", p.ChatJID, p.MsgID, err)
		return
	}
	log.Printf("whatsmeow: on-demand media fetch: %v", err)
}
