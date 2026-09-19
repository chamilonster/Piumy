package corepipeline

import (
	"context"
	"log"
	"os"
	"time"

	"piumy-gateway/internal/gateway"
	"piumy-gateway/internal/governor"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
)

// killSwitchActive is true if either half of the kill switch is set.
// H2+H3 hardening (ct-2026-07-10-0540): set_kill_switch (mcpserver/restapi)
// flips gov.Killed and state.Muted together, but they're still two
// independent flags — check both rather than trust they never diverge.
func (p *Pipeline) killSwitchActive() bool {
	return p.gov.Killed() || p.state.Snapshot().Muted
}

// processOutbox drains due outbox items through the gateway, respecting the
// anti-ban governor and human pacing. No-op while the gateway reports
// disconnected.
func (p *Pipeline) processOutbox(ctx context.Context) {
	if !p.gw.Connected() {
		return
	}

	pending, err := p.store.DueOutbox(10, time.Now().Unix())
	if err != nil {
		log.Printf("corepipeline: pending outbox: %v", err)
		return
	}

	for _, item := range pending {
		if ctx.Err() != nil {
			return
		}
		if p.killSwitchActive() {
			log.Println("corepipeline: kill switch active, skipping outbox")
			return
		}
		if !p.gov.Allow() {
			log.Println("corepipeline: rate-limited, deferring outbox")
			return
		}

		if !validJID(item.ToJID) {
			log.Printf("corepipeline: invalid JID %q — skipping", item.ToJID)
			if err := p.store.MarkSent(item.Seq); err != nil {
				log.Printf("corepipeline: mark sent (bad JID): %v", err)
			}
			continue
		}

		// Human pacing: wait a randomized delay, then re-check the kill
		// switch (it may have flipped while we were sleeping).
		select {
		case <-ctx.Done():
			return
		case <-time.After(p.dispatchDelay().Random()):
		}
		if p.killSwitchActive() {
			return
		}

		// T122 (ct-2026-09-02-2045): a media item never chunks — a caption
		// doesn't split the way a long text reply does — so it skips
		// splitIntoChunks/sendItemChunks entirely for its own single-send
		// path (sendMediaItem), gated by the SAME governor.Allow()/kill-
		// switch/dispatchDelay checks above. MediaKind == "" (every item
		// before this contract, and every plain-text item since) is the
		// exact branch that already existed.
		if item.MediaKind != "" {
			if stop := p.sendMediaItem(ctx, item); stop {
				return
			}
			continue
		}

		// T101 (ct-2026-08-29-1651): a long reply splits into several
		// WhatsApp-sized pieces (boss verbatim: "partirla en pedazos pero
		// que no se envien como metralla, pequeño delay"). A short reply —
		// the ordinary case — comes back as its own single-element slice
		// and sendItemChunks below takes the exact same path it always did.
		chunks := splitIntoChunks(item.Text, p.chunkMaxLen())
		if stop := p.sendItemChunks(ctx, item, chunks); stop {
			return
		}
	}
}

// sendMediaItem sends ONE outbox item's media (T122, ct-2026-09-02-2045) —
// the same governor-gated typing+composing dance as a single-chunk text
// send (sendItemChunks), but never chunked and via gw.SendMedia instead of
// gw.Send. Reads item.MediaPath off disk — the enqueue side
// (mcpserver/send.go) already saved the file via mediautil.SaveOutboundMedia;
// the DB row only ever held the path.
func (p *Pipeline) sendMediaItem(ctx context.Context, item store.Outbox) (stop bool) {
	data, err := os.ReadFile(item.MediaPath)
	if err != nil {
		log.Printf("corepipeline: read media seq=%d path=%s: %v", item.Seq, item.MediaPath, err)
		p.retryOrDeadLetter(item, err)
		return false
	}

	if err := p.gw.SetTyping(ctx, item.ToJID, true); err != nil {
		log.Printf("corepipeline: set typing: %v", err)
	}

	composing := governor.DelayWindow{Min: p.cfg.ComposingMin, Max: p.cfg.ComposingMax}
	select {
	case <-ctx.Done():
		return true
	case <-time.After(composing.Random()):
	}

	res, sendErr := p.gw.SendMedia(ctx, item.ToJID, gateway.OutboundMedia{
		Kind: item.MediaKind, Data: data, Mime: item.MediaMime, Caption: item.Text,
		// Seconds (T123, ct-2026-09-02-2121): a voice note's duration —
		// travels as item.MediaSeconds, an honest outbox column (never
		// packed into MediaMime, which store.AddMedia/get_media/the
		// dashboard also read as a real mime string).
		Seconds: item.MediaSeconds,
	})

	if err := p.gw.SetTyping(ctx, item.ToJID, false); err != nil {
		log.Printf("corepipeline: clear typing: %v", err)
	}

	if sendErr != nil {
		log.Printf("corepipeline: send media seq=%d: %v", item.Seq, sendErr)
		p.retryOrDeadLetter(item, sendErr)
		return false
	}

	if err := p.store.AddMessage(sentMediaMessageRow(item, res)); err != nil {
		log.Printf("corepipeline: store sent media message seq=%d: %v", item.Seq, err)
	}
	// Same media table a received photo populates (whatsmeow.
	// downloadAndStoreMedia) — get_media/get_media_full and the dashboard's
	// existing message-bubble rendering read from here regardless of
	// direction; no low-q/full split for outbound (one file, one quality),
	// same convention Media.Path/FullPath already use for non-image media.
	if err := p.store.AddMedia(store.Media{
		MsgID: res.MsgID, ChatJID: item.ToJID,
		Path: item.MediaPath, FullPath: item.MediaPath,
		Mime: item.MediaMime, Size: int64(len(data)), TS: res.TS,
	}); err != nil {
		log.Printf("corepipeline: store sent media row seq=%d: %v", item.Seq, err)
	}

	// ST-D (ct-2026-07-11-074139)'s metering choke point, media's own —
	// mirrors sendItemChunks' AddUsage below, Images/Audio instead of
	// OutChars. T123 (ct-2026-09-02-2121): charged on the RIGHT axis —
	// store.UsageDelta has separate Images/Audio counters, and a voice
	// note is not an image send.
	usage := store.UsageDelta{Messages: 1}
	if item.MediaKind == "audio" {
		usage.Audio = 1
	} else {
		usage.Images = 1
	}
	if err := p.store.AddUsage(item.ToJID, store.Today(), usage); err != nil {
		log.Printf("corepipeline: meter output seq=%d: %v", item.Seq, err)
	}

	if err := p.store.MarkSent(item.Seq); err != nil {
		log.Printf("corepipeline: mark sent seq=%d: %v", item.Seq, err)
	}

	if sent, err := p.store.CountOutboundSince(0); err != nil {
		log.Printf("corepipeline: count sent: %v", err)
	} else {
		_ = p.state.Update(func(s *state.Status) { s.Sent = sent })
	}

	log.Printf("corepipeline: sent outbox seq=%d to %s (media: %s)", item.Seq, item.ToJID, item.MediaKind)
	return false
}

// sendItemChunks sends item as one or more WhatsApp messages (chunks),
// resuming from item.ChunksSent — 0 for a fresh item, > 0 when a PREVIOUS
// tick's mid-sequence failure already persisted how many pieces went out
// (T101, ct-2026-08-29-1651, Citrino's own flagged concern, point 1): a
// later chunk failing must never re-send pieces the contact already
// received, which would read as repeated text — worse than the single
// giant balloon this contract exists to fix. Each chunk is its own
// gw.Send/SetTyping/composing cycle, same as an ordinary single-chunk
// message; only a chunk AFTER the first one in THIS call adds chunkDelay()
// (the short respiro between parts of the SAME reply — dispatchDelay above
// already paced the first one, same as any other message) and a fresh
// governor.Allow() (each chunk is a real WhatsApp send and must count
// against the rate limit like any other).
//
// Returns true if processOutbox must stop draining entirely this tick
// (ctx cancelled, kill switch flipped, or the governor ran out of budget
// mid-sequence) — same signal the top-of-loop checks already give for a
// single-chunk item, just reachable from inside a multi-chunk send too.
func (p *Pipeline) sendItemChunks(ctx context.Context, item store.Outbox, chunks []string) (stop bool) {
	for i := item.ChunksSent; i < len(chunks); i++ {
		if i > item.ChunksSent {
			select {
			case <-ctx.Done():
				return true
			case <-time.After(p.chunkDelay().Random()):
			}
			if p.killSwitchActive() {
				return true
			}
			if !p.gov.Allow() {
				log.Println("corepipeline: rate-limited mid-chunk, deferring outbox")
				return true
			}
		}

		if err := p.gw.SetTyping(ctx, item.ToJID, true); err != nil {
			log.Printf("corepipeline: set typing: %v", err)
		}

		composing := governor.DelayWindow{Min: p.cfg.ComposingMin, Max: p.cfg.ComposingMax}
		select {
		case <-ctx.Done():
			return true
		case <-time.After(composing.Random()):
		}

		res, sendErr := p.gw.Send(ctx, item.ToJID, chunks[i])

		if err := p.gw.SetTyping(ctx, item.ToJID, false); err != nil {
			log.Printf("corepipeline: clear typing: %v", err)
		}

		if sendErr != nil {
			log.Printf("corepipeline: send seq=%d chunk=%d/%d: %v", item.Seq, i+1, len(chunks), sendErr)
			p.retryOrDeadLetter(item, sendErr)
			return false
		}

		// Record the outbound message with the client's real id (so
		// delivery/read receipts, which reference that id, can match it
		// later) and its model attribution.
		if err := p.store.AddMessage(sentMessageRow(item.ToJID, chunks[i], item, res)); err != nil {
			log.Printf("corepipeline: store sent message seq=%d chunk=%d: %v", item.Seq, i+1, err)
		}

		// ST-D (ct-2026-07-11-074139): the ONE metering point for every real
		// WhatsApp send, whatever queued it — send_message/draft (MCP),
		// approve_draft, autoreply's auto-send, and a plain outbox item all
		// converge here via Enqueue/EnqueueWithModel; processOutbox (via
		// this function) is the only caller of gw.Send in the whole
		// codebase (verified). usage now means "what actually went out",
		// not "what an agent attempted" — a held-then-discarded draft was
		// never metered even before this fix and still isn't; that's
		// correct, it never left the outbox. Metered per CHUNK: each is a
		// real, separate WhatsApp message.
		if err := p.store.AddUsage(item.ToJID, store.Today(), store.UsageDelta{OutChars: len(chunks[i]), Messages: 1}); err != nil {
			log.Printf("corepipeline: meter output seq=%d chunk=%d: %v", item.Seq, i+1, err)
		}

		// Persist progress after EVERY successful chunk (including the
		// last) — simpler than special-casing "is this the last one", and
		// makes a crash between chunks resume from the right place too.
		if err := p.store.SetOutboxChunkProgress(item.Seq, i+1); err != nil {
			log.Printf("corepipeline: set chunk progress seq=%d: %v", item.Seq, err)
		}
	}

	if err := p.store.MarkSent(item.Seq); err != nil {
		log.Printf("corepipeline: mark sent seq=%d: %v", item.Seq, err)
	}

	// Sent counter: recomputed from the messages table (never a separately
	// incremented counter — see state.Status.Sent's doc comment for why
	// that drifts on a retry/crash mismatch).
	if sent, err := p.store.CountOutboundSince(0); err != nil {
		log.Printf("corepipeline: count sent: %v", err)
	} else {
		_ = p.state.Update(func(s *state.Status) { s.Sent = sent })
	}

	log.Printf("corepipeline: sent outbox seq=%d to %s (%d chunk(s))", item.Seq, item.ToJID, len(chunks))
	return false
}

// retryOrDeadLetter is the anti-ban core: a failed send must NEVER just
// retry again next tick forever (a resend loop). Bump retry_count and back
// off exponentially; once retry_count reaches cfg.OutboxMaxRetry,
// dead-letter the item instead (out of the send loop for good, never
// deleted — stays for inspection).
func (p *Pipeline) retryOrDeadLetter(item store.Outbox, sendErr error) {
	retryCount := item.RetryCount + 1
	if retryCount >= p.cfg.OutboxMaxRetry {
		if err := p.store.DeadLetterOutbox(item.Seq, sendErr.Error()); err != nil {
			log.Printf("corepipeline: dead-letter seq=%d: %v", item.Seq, err)
			return
		}
		log.Printf("corepipeline: outbox seq=%d dead-lettered after %d failures", item.Seq, retryCount)
		return
	}
	nextRetry := time.Now().Add(exponentialBackoff(retryCount)).Unix()
	if err := p.store.SetOutboxRetry(item.Seq, retryCount, nextRetry, sendErr.Error()); err != nil {
		log.Printf("corepipeline: set outbox retry seq=%d: %v", item.Seq, err)
	}
}

// exponentialBackoff returns 5s * 2^(retryCount-1) capped at 1h. Anti-ban:
// always > 0, never instant; retryCount is always >= 1 when called.
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

// sentMessageRow builds the messages row for a successfully sent outbox
// item, using the client's real response (id + timestamp) rather than
// anything guessed at enqueue time — so delivery/read receipts, which
// reference that id, can match it later. text is the piece actually sent
// (T101, ct-2026-08-29-1651: a chunk of item.Text, not necessarily all of
// it — a split reply gets one row per chunk, each with its own real MsgID).
// OriginTerminalID (T39, ct-2026-08-08-1619) travels the same way Model
// already does — set at enqueue (EnqueueFromAgent), carried through the
// outbox row, copied here onto the real sent message.
func sentMessageRow(chatJID, text string, item store.Outbox, res gateway.SendResult) store.Message {
	return store.Message{
		ChatJID:          chatJID,
		ID:               res.MsgID,
		FromMe:           true,
		Text:             text,
		TS:               res.TS,
		Type:             "text",
		Model:            item.Model,
		OriginTerminalID: item.OriginTerminalID,
	}
}

// sentMediaMessageRow is sentMessageRow's media sibling (T122, ct-2026-09-
// 02-2045) — Type holds the mime (image/jpeg), matching how an INBOUND
// media message already stores it there (downloadAndStoreMedia,
// whatsmeow/media.go; see schema.go's migrateMimeInText for why "type
// holds the mime for a media row" is the established convention, not
// invented here). Text is the caption — empty is a valid caption, same as
// a photo sent with no comment.
func sentMediaMessageRow(item store.Outbox, res gateway.SendResult) store.Message {
	return store.Message{
		ChatJID:          item.ToJID,
		ID:               res.MsgID,
		FromMe:           true,
		Text:             item.Text,
		TS:               res.TS,
		Type:             item.MediaMime,
		Model:            item.Model,
		OriginTerminalID: item.OriginTerminalID,
	}
}

// validJID rejects only the one invalid case this agnostic layer can judge
// without knowing any vendor's JID format: empty. Deeper format validation
// belongs to the adapter (F3), not the core.
func validJID(jid string) bool {
	return jid != ""
}
