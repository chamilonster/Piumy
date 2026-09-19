package corepipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"piumy-gateway/internal/governor"
	"piumy-gateway/internal/router"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
)

func TestProcessOutboxSendsMarksSentAndRecordsSentRow(t *testing.T) {
	st, _, _, fgw, p := newTestPipeline(t)
	if err := st.Enqueue("111@c.us", "hola", 1); err != nil {
		t.Fatal(err)
	}

	p.processOutbox(context.Background())

	if calls := fgw.sentCalls(); len(calls) != 1 || calls[0].toJID != "111@c.us" || calls[0].text != "hola" {
		t.Fatalf("fakeGateway.sent = %+v, want exactly one call to 111@c.us", calls)
	}
	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("PendingOutbox after a successful send = %+v, want empty (MarkSent)", pending)
	}
	msgs, err := st.GetMessages("111@c.us", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || !msgs[0].FromMe || msgs[0].ID != "fake-1" {
		t.Fatalf("got messages=%+v, want the sent row recorded with the fake's real MsgID", msgs)
	}
}

// TestProcessOutboxRecordsOriginTerminalIDOnSentRow is T39's own regression
// (ct-2026-08-08-1619, send_to_boss): origin_terminal_id must travel from
// the outbox row (EnqueueFromAgent) onto the resulting messages row
// (sentMessageRow) — the data a later reply-routing feature needs, not read
// anywhere yet but must not get lost in transit.
func TestProcessOutboxRecordsOriginTerminalIDOnSentRow(t *testing.T) {
	st, _, _, _, p := newTestPipeline(t)
	if err := st.EnqueueFromAgent("555000002@s.whatsapp.net", "[Agente Uno] hola boss", 1, "term-555002"); err != nil {
		t.Fatal(err)
	}

	p.processOutbox(context.Background())

	msgs, err := st.GetMessages("555000002@s.whatsapp.net", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].OriginTerminalID != "term-555002" {
		t.Fatalf("got messages=%+v, want one message with origin_terminal_id=term-555002", msgs)
	}
}

// TestProcessOutboxMetersUsageOnSuccessfulSend is the ST-D regression
// (ct-2026-07-11-074139): processOutbox is the ONE real-send choke point
// (send_message/draft-approved/autoreply-auto-send/a plain dashboard
// Enqueue ALL just queue into the same outbox table — nothing sends
// directly) — usage.AddUsage now fires exactly here, after a confirmed
// send, so "usage" means "what actually left via WhatsApp", not "what an
// agent attempted". Uses plain Enqueue (no model — the human/dashboard
// case) precisely because it's the one origin that never had any metering
// anywhere before this fix, proving the fix isn't piggy-backing on
// something else.
func TestProcessOutboxMetersUsageOnSuccessfulSend(t *testing.T) {
	st, _, _, _, p := newTestPipeline(t)
	if err := st.Enqueue("111@c.us", "hola mundo", 1); err != nil {
		t.Fatal(err)
	}

	p.processOutbox(context.Background())

	u, err := st.UsageForDay("111@c.us", store.Today())
	if err != nil {
		t.Fatal(err)
	}
	if u.OutChars != len("hola mundo") || u.Messages != 1 {
		t.Errorf("usage after a successful outbox send = %+v, want out_chars=%d messages=1", u, len("hola mundo"))
	}
}

// fiveParagraphText is a T101 (ct-2026-08-29-1651) fixture: 5 paragraphs,
// each long enough that no two pack into the same chunk once ChunkMaxLen is
// set to chunkTestMaxLen below — splitIntoChunks then returns exactly 5
// chunks, one per paragraph, which every test below relies on to reason
// about "chunk N of 5" precisely.
const chunkTestMaxLen = 22

var fiveParagraphs = []string{
	"uno dos tres cuatro",
	"cinco seis siete ocho",
	"nueve diez once doce",
	"trece catorce quince",
	"dieciseis diecisiete",
}

func fiveParagraphText() string {
	return strings.Join(fiveParagraphs, "\n\n")
}

// TestProcessOutboxShortMessageUnchanged is the DoD's explicit case: a
// short reply (the ordinary, common case) must not touch any of the new
// chunking machinery — one Send, no chunk delay, no chunks_sent bookkeeping.
func TestProcessOutboxShortMessageUnchanged(t *testing.T) {
	st, _, _, fgw, p := newTestPipeline(t)
	p.cfg.ChunkMaxLen = chunkTestMaxLen // small limit — "hola" is still well under it
	if err := st.Enqueue("111@c.us", "hola", 1); err != nil {
		t.Fatal(err)
	}

	p.processOutbox(context.Background())

	if calls := fgw.sentCalls(); len(calls) != 1 || calls[0].text != "hola" {
		t.Fatalf("fakeGateway.sent = %+v, want exactly one call with the unsplit text", calls)
	}
	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("PendingOutbox = %+v, want empty (MarkSent, single send)", pending)
	}
}

// TestProcessOutboxSplitsLongMessageIntoMultipleSends is the DoD's other
// explicit case: a long reply goes out as several WhatsApp messages, each
// stored as its own row (each with a real, distinct MsgID from the
// gateway), and the outbox item is marked sent only once every piece went
// out.
func TestProcessOutboxSplitsLongMessageIntoMultipleSends(t *testing.T) {
	st, _, _, fgw, p := newTestPipeline(t)
	p.cfg.ChunkMaxLen = chunkTestMaxLen
	if err := st.Enqueue("111@c.us", fiveParagraphText(), 1); err != nil {
		t.Fatal(err)
	}

	p.processOutbox(context.Background())

	calls := fgw.sentCalls()
	if len(calls) != 5 {
		t.Fatalf("fakeGateway.sent = %+v, want exactly 5 calls (one per paragraph)", calls)
	}
	for i, want := range fiveParagraphs {
		if calls[i].text != want {
			t.Errorf("chunk %d text = %q, want %q", i, calls[i].text, want)
		}
	}

	msgs, err := st.GetMessages("111@c.us", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 5 {
		t.Fatalf("got %d stored messages, want 5 — one row per chunk, each with its own real MsgID", len(msgs))
	}
	seen := map[string]bool{}
	for _, m := range msgs {
		if seen[m.ID] {
			t.Errorf("duplicate stored MsgID %q — each chunk must get its own row", m.ID)
		}
		seen[m.ID] = true
	}

	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("PendingOutbox after all chunks sent = %+v, want empty (MarkSent)", pending)
	}
}

// TestProcessOutboxResumesFromLastSuccessfulChunkAfterFailure is Citrino's
// own flagged concern for T101 (ct-2026-08-29-1651), point 1: if chunk 3 of
// 5 fails, chunks 1-2 (already delivered to the contact) must NEVER be
// re-sent on retry — that would show the contact repeated text, worse than
// the single giant balloon this contract exists to fix.
func TestProcessOutboxResumesFromLastSuccessfulChunkAfterFailure(t *testing.T) {
	st, _, _, fgw, p := newTestPipeline(t)
	p.cfg.ChunkMaxLen = chunkTestMaxLen
	if err := st.Enqueue("111@c.us", fiveParagraphText(), 1); err != nil {
		t.Fatal(err)
	}
	fgw.setSendFailAt(3) // the 3rd Send call (chunk index 2) fails

	p.processOutbox(context.Background())

	sentBefore := fgw.sentCalls()
	if len(sentBefore) != 2 {
		t.Fatalf("got %d successful sends before the failure, want exactly 2 (chunks 1-2)", len(sentBefore))
	}

	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("PendingOutbox after a mid-sequence failure = %+v, want 1 item still pending", pending)
	}
	if pending[0].ChunksSent != 2 {
		t.Fatalf("ChunksSent = %d, want 2 (progress from the two successful chunks persisted)", pending[0].ChunksSent)
	}
	if pending[0].RetryCount != 1 {
		t.Errorf("RetryCount = %d, want 1 (the failed chunk counted as a normal outbox retry)", pending[0].RetryCount)
	}

	// Force the backoff deadline into the past — same technique
	// TestOutboxDrainRetryBackoffAndDeadLetter uses — and let the gateway
	// succeed from now on.
	if err := st.SetOutboxRetry(pending[0].Seq, pending[0].RetryCount, 0, pending[0].LastError); err != nil {
		t.Fatal(err)
	}
	fgw.setSendFailAt(0)

	p.processOutbox(context.Background())

	sentAfter := fgw.sentCalls()
	if len(sentAfter) != 5 {
		t.Fatalf("got %d total successful sends after resuming, want 5 (2 before + 3 resumed — chunks 1-2 must NOT repeat)", len(sentAfter))
	}
	for i, want := range fiveParagraphs {
		if sentAfter[i].text != want {
			t.Errorf("final send order, position %d = %q, want %q — chunks 1-2 must not have been re-sent or reordered", i, sentAfter[i].text, want)
		}
	}

	pendingAfter, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pendingAfter) != 0 {
		t.Errorf("PendingOutbox after full resume = %+v, want empty (MarkSent)", pendingAfter)
	}

	msgs, err := st.GetMessages("111@c.us", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 5 {
		t.Errorf("got %d stored messages, want 5 — no duplicate rows for the resumed chunks", len(msgs))
	}
}

// TestProcessOutboxMetersAutoreplyOriginatedSend covers the autoreply path
// specifically: EnqueueWithModel is exactly what autoreply.Worker.draftFor
// calls for its auto-send branch (NeedsConfirmation == false) — same outbox
// row shape as every other origin, so the SAME processOutbox metering
// covers it with no autoreply-side change needed.
func TestProcessOutboxMetersAutoreplyOriginatedSend(t *testing.T) {
	st, _, _, _, p := newTestPipeline(t)
	if err := st.EnqueueWithModel("222@c.us", "dale, te ayudo", 1, "auto"); err != nil {
		t.Fatal(err)
	}

	p.processOutbox(context.Background())

	u, err := st.UsageForDay("222@c.us", store.Today())
	if err != nil {
		t.Fatal(err)
	}
	if u.OutChars != len("dale, te ayudo") || u.Messages != 1 {
		t.Errorf("usage after an autoreply-originated send = %+v, want out_chars=%d messages=1", u, len("dale, te ayudo"))
	}
}

// TestProcessOutboxMetersApprovedDraft covers the approve_draft path: a
// draft holds NO usage on its own (see mcpserver's draft tool — a
// discarded draft must never count), only ApproveDraft's EnqueueWithModel
// followed by a real send should.
func TestProcessOutboxMetersApprovedDraft(t *testing.T) {
	st, _, _, _, p := newTestPipeline(t)
	if err := st.AddDraft("333@c.us", "aprobado por el dueño", "auto", 1); err != nil {
		t.Fatal(err)
	}
	drafts, err := st.PendingDrafts(10)
	if err != nil || len(drafts) != 1 {
		t.Fatalf("setup: PendingDrafts = %+v, err=%v", drafts, err)
	}
	if _, _, _, ok, err := st.ApproveDraft(drafts[0].ID, "", 2); err != nil || !ok {
		t.Fatalf("setup: ApproveDraft ok=%v err=%v", ok, err)
	}

	// Before the real send, approving alone must not have metered anything.
	preSend, err := st.UsageForDay("333@c.us", store.Today())
	if err != nil {
		t.Fatal(err)
	}
	if preSend.Messages != 0 {
		t.Fatalf("usage right after ApproveDraft, before any real send = %+v, want zero", preSend)
	}

	p.processOutbox(context.Background())

	u, err := st.UsageForDay("333@c.us", store.Today())
	if err != nil {
		t.Fatal(err)
	}
	if u.OutChars != len("aprobado por el dueño") || u.Messages != 1 {
		t.Errorf("usage after the approved draft actually sent = %+v, want out_chars=%d messages=1", u, len("aprobado por el dueño"))
	}
}

// TestProcessOutboxDoesNotMeterOnFailedSend: a failed send (still queued
// for retry, never actually left) must not count as output.
func TestProcessOutboxDoesNotMeterOnFailedSend(t *testing.T) {
	st, _, _, fgw, p := newTestPipeline(t)
	fgw.setSendErr(errFakeSend)
	if err := st.Enqueue("444@c.us", "hola", 1); err != nil {
		t.Fatal(err)
	}

	p.processOutbox(context.Background())

	u, err := st.UsageForDay("444@c.us", store.Today())
	if err != nil {
		t.Fatal(err)
	}
	if u.Messages != 0 {
		t.Errorf("usage after a FAILED send = %+v, want zero — it never actually left", u)
	}
}

func TestProcessOutboxNoOpWhenDisconnected(t *testing.T) {
	st, _, _, fgw, p := newTestPipeline(t)
	fgw.setConnected(false)
	if err := st.Enqueue("111@c.us", "hola", 1); err != nil {
		t.Fatal(err)
	}

	p.processOutbox(context.Background())

	if calls := fgw.sentCalls(); len(calls) != 0 {
		t.Fatalf("fakeGateway.sent = %+v, want none while disconnected", calls)
	}
	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("got %d pending, want the item still queued (never attempted)", len(pending))
	}
}

func TestProcessOutboxKillSwitchSkipsEverything(t *testing.T) {
	st, _, gov, fgw, p := newTestPipeline(t)
	gov.SetKill(true)
	if err := st.Enqueue("111@c.us", "hola", 1); err != nil {
		t.Fatal(err)
	}

	p.processOutbox(context.Background())

	if calls := fgw.sentCalls(); len(calls) != 0 {
		t.Fatalf("fakeGateway.sent = %+v, want none — kill switch is active", calls)
	}
}

// TestProcessOutboxMutedAloneSkipsEverything is the H2+H3 hardening
// regression (ct-2026-07-10-0540): state.Muted alone — without
// gov.Killed() — must also halt the outbox drain. Before this fix the kill
// switch tool/endpoint flipped both flags but processOutbox only ever
// checked the governor's, so a divergence between the two silently kept
// sending. newTestPipeline doesn't expose the state.Manager it builds
// internally, so this test wires its own pipeline directly (same pieces,
// same testConfig) instead of extending that helper's return tuple across
// its 16 other call sites for one test.
func TestProcessOutboxMutedAloneSkipsEverything(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	rt := router.NewManager(filepath.Join(t.TempDir(), "router.json"))
	gov := governor.NewLimiter(100, time.Minute)
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
	fgw := newFakeGateway()
	p := New(fgw, st, rt, gov, sm, testConfig())

	if err := sm.SetMuted(true); err != nil {
		t.Fatal(err)
	}
	if err := st.Enqueue("111@c.us", "hola", 1); err != nil {
		t.Fatal(err)
	}

	p.processOutbox(context.Background())

	if calls := fgw.sentCalls(); len(calls) != 0 {
		t.Fatalf("fakeGateway.sent = %+v, want none — state.Muted alone must halt the drain", calls)
	}
}

// TestKillSwitchSurvivesRestartAndReallyBlocksSending is T19's own
// acceptance criterion (ct-2026-08-05-1249, Citrino verbatim: "verificá
// que de verdad no manda: no alcanza con que el estado diga que sí").
// Simulates an actual restart: the kill switch was persisted to the store
// BEFORE this test's gov/sm ever existed (a fresh governor.Limiter/
// state.Manager, exactly like a real process boot has zero prior
// in-memory state) — then applies the SAME two-flag restore main.go's
// restoreKillSwitch does (governor.SetKill + state.SetMuted from the
// persisted flag), and only THEN builds the pipeline and asks it to
// actually drain a real queued message. Proof is fakeGateway.sentCalls()
// being empty — not gov.Killed()/sm.Snapshot().Muted reading true, which
// TestRestoreKillSwitchAppliesBothFlagsTogether (main_test.go) already
// covers on its own and would pass even if processOutbox's own gate were
// broken.
func TestKillSwitchSurvivesRestartAndReallyBlocksSending(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	// The kill switch was set in a PREVIOUS run — all that survives is
	// this persisted flag, same as store.SetSettingBool(store.
	// SettingKillSwitch, true) from set_kill_switch (mcpserver/restapi).
	if err := st.SetSettingBool(store.SettingKillSwitch, true); err != nil {
		t.Fatal(err)
	}

	// "Restart": brand-new governor.Limiter/state.Manager, never told
	// about the kill switch yet — this is the exact in-memory state a real
	// process has the instant after boot, before restoreKillSwitch runs.
	rt := router.NewManager(filepath.Join(t.TempDir(), "router.json"))
	gov := governor.NewLimiter(100, time.Minute)
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)

	// main.go's restoreKillSwitch, inlined (corepipeline can't import
	// package main) — must run BEFORE the pipeline is even built, same
	// ordering main.go itself follows relative to ctrl.Start().
	if st.SettingBool(store.SettingKillSwitch, false) {
		gov.SetKill(true)
		if err := sm.SetMuted(true); err != nil {
			t.Fatal(err)
		}
	}

	fgw := newFakeGateway()
	p := New(fgw, st, rt, gov, sm, testConfig())
	if err := st.Enqueue("111@c.us", "no debería salir", 1); err != nil {
		t.Fatal(err)
	}

	p.processOutbox(context.Background())

	if calls := fgw.sentCalls(); len(calls) != 0 {
		t.Fatalf("fakeGateway.sent = %+v, want none — a kill switch restored from a persisted flag must block sending exactly like a live one", calls)
	}
	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("PendingOutbox = %+v, want the message still queued (never attempted, not lost)", pending)
	}
}

func TestProcessOutboxRateLimitedDefers(t *testing.T) {
	st, _, gov, fgw, p := newTestPipeline(t)
	gov.SetMax(0) // no tokens ever refill above the floor -> Allow() always false
	if err := st.Enqueue("111@c.us", "hola", 1); err != nil {
		t.Fatal(err)
	}

	p.processOutbox(context.Background())

	if calls := fgw.sentCalls(); len(calls) != 0 {
		t.Fatalf("fakeGateway.sent = %+v, want none — rate-limited", calls)
	}
	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("got %d pending, want the item still queued for the next tick", len(pending))
	}
}

func TestProcessOutboxInvalidJIDMarkedSentWithoutSending(t *testing.T) {
	st, _, _, fgw, p := newTestPipeline(t)
	if err := st.Enqueue("", "hola", 1); err != nil {
		t.Fatal(err)
	}

	p.processOutbox(context.Background())

	if calls := fgw.sentCalls(); len(calls) != 0 {
		t.Fatalf("fakeGateway.sent = %+v, want none — empty JID must never be attempted", calls)
	}
	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("PendingOutbox = %+v, want empty — an invalid JID is marked sent (skipped, not retried)", pending)
	}
}

// TestProcessOutboxSendFailureSetsRetry covers processOutbox wiring into
// retryOrDeadLetter end to end: a failed send bumps retry_count and sets a
// backoff deadline, without immediately dead-lettering (well under
// OutboxMaxRetry=3 from testConfig).
func TestProcessOutboxSendFailureSetsRetry(t *testing.T) {
	st, _, _, fgw, p := newTestPipeline(t)
	fgw.setSendErr(errFakeSend)
	if err := st.Enqueue("111@c.us", "hola", 1); err != nil {
		t.Fatal(err)
	}

	p.processOutbox(context.Background())

	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("got %d pending, want the failed item still queued (not dead-lettered yet)", len(pending))
	}
	if pending[0].RetryCount != 1 || pending[0].NextRetryTS <= 0 || pending[0].DeadLetter {
		t.Errorf("got %+v, want retry_count=1, a future next_retry_ts, dead_letter=false", pending[0])
	}
}

// ── T122 (ct-2026-09-02-2045) — media outbox items ──────────────────────

// writeTestMedia is a T122 fixture: a real file on disk at the path a media
// outbox item points at — sendMediaItem does os.ReadFile(item.MediaPath),
// so the file must genuinely exist for these tests, same as production.
func writeTestMedia(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestProcessOutboxSendsMediaMarksSentAndRecordsSentRow is the media
// sibling of TestProcessOutboxSendsMarksSentAndRecordsSentRow: a media item
// goes out via gw.SendMedia (never gw.Send/chunking), gets marked sent, and
// leaves both a messages row (Type=mime) and a media row an agent could
// later read back via get_media/get_media_full.
func TestProcessOutboxSendsMediaMarksSentAndRecordsSentRow(t *testing.T) {
	st, _, _, fgw, p := newTestPipeline(t)
	path := writeTestMedia(t, []byte("bytes de una foto"))
	if err := st.EnqueueMediaWithModel("555000010@c.us", "mirá esto", path, "image/jpeg", "photo", 0, 1, "gpt-5"); err != nil {
		t.Fatal(err)
	}

	p.processOutbox(context.Background())

	calls := fgw.sentMediaCalls()
	if len(calls) != 1 || calls[0].toJID != "555000010@c.us" || calls[0].kind != "photo" || calls[0].mime != "image/jpeg" || calls[0].caption != "mirá esto" {
		t.Fatalf("fakeGateway.sentMedia = %+v, want exactly one matching call", calls)
	}
	if len(fgw.sentCalls()) != 0 {
		t.Errorf("fakeGateway.sent (text path) got a call — a media item must never go through gw.Send")
	}
	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("PendingOutbox after a successful media send = %+v, want empty (MarkSent)", pending)
	}
	msgs, err := st.GetMessages("555000010@c.us", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || !msgs[0].FromMe || msgs[0].Type != "image/jpeg" || msgs[0].Text != "mirá esto" {
		t.Fatalf("got messages=%+v, want one FromMe row with Type=image/jpeg", msgs)
	}
	media, ok, err := st.GetMedia("555000010@c.us", msgs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || media.Path != path || media.Mime != "image/jpeg" {
		t.Errorf("GetMedia after a sent photo: ok=%v media=%+v, want a row pointing at %q", ok, media, path)
	}
}

// TestProcessOutboxSendsAudioWithSeconds is T123's own regression
// (ct-2026-09-02-2121): an audio item's MediaSeconds must reach
// gateway.OutboundMedia.Seconds intact — the honest column that replaced
// the rejected mime-suffix hack.
func TestProcessOutboxSendsAudioWithSeconds(t *testing.T) {
	st, _, _, fgw, p := newTestPipeline(t)
	path := writeTestMedia(t, []byte("bytes de un audio"))
	if err := st.EnqueueMediaWithModel("555000014@c.us", "", path, "audio/ogg; codecs=opus", "audio", 12, 1, "gpt-5"); err != nil {
		t.Fatal(err)
	}

	p.processOutbox(context.Background())

	calls := fgw.sentMediaCalls()
	if len(calls) != 1 || calls[0].kind != "audio" || calls[0].mime != "audio/ogg; codecs=opus" || calls[0].seconds != 12 {
		t.Fatalf("fakeGateway.sentMedia = %+v, want exactly one audio call with seconds=12", calls)
	}
	msgs, err := st.GetMessages("555000014@c.us", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Type != "audio/ogg; codecs=opus" {
		t.Fatalf("got messages=%+v, want one row with Type=audio/ogg; codecs=opus", msgs)
	}
}

// TestProcessOutboxMediaRateLimitedDefers mirrors
// TestProcessOutboxRateLimitedDefers for media: the anti-ban governor gates
// a photo exactly like it gates text — this is T122's non-negotiable
// requirement, not incidental.
func TestProcessOutboxMediaRateLimitedDefers(t *testing.T) {
	st, _, gov, fgw, p := newTestPipeline(t)
	gov.SetMax(0)
	path := writeTestMedia(t, []byte("foto"))
	if err := st.EnqueueMediaWithModel("555000011@c.us", "", path, "image/jpeg", "photo", 0, 1, ""); err != nil {
		t.Fatal(err)
	}

	p.processOutbox(context.Background())

	if calls := fgw.sentMediaCalls(); len(calls) != 0 {
		t.Fatalf("fakeGateway.sentMedia = %+v, want none — rate-limited", calls)
	}
	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("got %d pending, want the photo still queued for the next tick", len(pending))
	}
}

// TestProcessOutboxMetersImageUsageOnMediaSend: a sent photo counts as an
// image usage charge, same axis get_media_full already meters.
func TestProcessOutboxMetersImageUsageOnMediaSend(t *testing.T) {
	st, _, _, _, p := newTestPipeline(t)
	path := writeTestMedia(t, []byte("foto"))
	if err := st.EnqueueMediaWithModel("555000012@c.us", "hola", path, "image/jpeg", "photo", 0, 1, ""); err != nil {
		t.Fatal(err)
	}

	p.processOutbox(context.Background())

	u, err := st.UsageForDay("555000012@c.us", store.Today())
	if err != nil {
		t.Fatal(err)
	}
	if u.Images != 1 || u.Messages != 1 || u.Audio != 0 {
		t.Errorf("usage after a successful photo send = %+v, want images=1 messages=1 audio=0", u)
	}
}

// TestProcessOutboxMetersAudioUsageOnMediaSend is T123's own regression
// (ct-2026-09-02-2121): a sent voice note counts as AUDIO usage, not
// Images — store.UsageDelta has a separate counter for exactly this.
func TestProcessOutboxMetersAudioUsageOnMediaSend(t *testing.T) {
	st, _, _, _, p := newTestPipeline(t)
	path := writeTestMedia(t, []byte("audio"))
	if err := st.EnqueueMediaWithModel("555000015@c.us", "", path, "audio/ogg; codecs=opus", "audio", 5, 1, ""); err != nil {
		t.Fatal(err)
	}

	p.processOutbox(context.Background())

	u, err := st.UsageForDay("555000015@c.us", store.Today())
	if err != nil {
		t.Fatal(err)
	}
	if u.Audio != 1 || u.Messages != 1 || u.Images != 0 {
		t.Errorf("usage after a successful audio send = %+v, want audio=1 messages=1 images=0", u)
	}
}

// TestProcessOutboxMediaSendFailureSetsRetry mirrors
// TestProcessOutboxSendFailureSetsRetry for media: sendMediaItem's error
// path must wire into the SAME retryOrDeadLetter anti-ban backoff, not a
// separate/forgotten one.
func TestProcessOutboxMediaSendFailureSetsRetry(t *testing.T) {
	st, _, _, fgw, p := newTestPipeline(t)
	fgw.setSendErr(errFakeSend)
	path := writeTestMedia(t, []byte("foto"))
	if err := st.EnqueueMediaWithModel("555000013@c.us", "hola", path, "image/jpeg", "photo", 0, 1, ""); err != nil {
		t.Fatal(err)
	}

	p.processOutbox(context.Background())

	pending, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("got %d pending, want the failed photo still queued (not dead-lettered yet)", len(pending))
	}
	if pending[0].RetryCount != 1 || pending[0].NextRetryTS <= 0 || pending[0].DeadLetter {
		t.Errorf("got %+v, want retry_count=1, a future next_retry_ts, dead_letter=false", pending[0])
	}
}

// TestRetryOrDeadLetterDeadLettersAtThreshold unit-tests the threshold
// directly (no need to wait out real backoff across multiple ticks): an
// item already one failure short of cfg.OutboxMaxRetry gets dead-lettered
// on its next failure, and dead-lettered items are excluded from
// PendingOutbox's underlying DueOutbox but stay visible for inspection.
func TestRetryOrDeadLetterDeadLettersAtThreshold(t *testing.T) {
	st, _, _, _, p := newTestPipeline(t) // testConfig().OutboxMaxRetry == 3
	if err := st.Enqueue("111@c.us", "hola", 1); err != nil {
		t.Fatal(err)
	}
	pending, err := st.PendingOutbox(1)
	if err != nil || len(pending) != 1 {
		t.Fatalf("setup: PendingOutbox = %+v, err=%v", pending, err)
	}
	item := pending[0]
	item.RetryCount = 2 // one more failure reaches OutboxMaxRetry=3

	p.retryOrDeadLetter(item, errFakeSend)

	due, err := st.DueOutbox(10, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("DueOutbox after dead-lettering = %+v, want empty (excluded from the send loop)", due)
	}
	all, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || !all[0].DeadLetter {
		t.Fatalf("PendingOutbox = %+v, want the item still visible with dead_letter=true", all)
	}
}
