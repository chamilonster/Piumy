package corepipeline

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"piumy-gateway/internal/gateway"
)

// fakeGateway implements gateway.Gateway for tests — with it, the whole
// pipeline is testable with no open-wa involved.
type fakeGateway struct {
	mu        sync.Mutex
	inbound   chan gateway.Inbound
	connected bool
	sent      []sentCall
	sendErr   error
	stopped   bool
	// sendDelay makes Send block before returning — used to deterministically
	// widen the window where processOutbox is "in flight" mid-Send, for
	// tests that need to provoke a shutdown race on purpose (see
	// TestControllerStopWaitsForInFlightOutboxSend).
	sendDelay time.Duration
	// sendStarted, if non-nil, gets a non-blocking signal the INSTANT Send
	// is entered — before sendDelay's sleep. A test that needs to know
	// "Send is now in flight" waits on this instead of a fixed sleep
	// (ct-2026-07-18-1507: the fixed-sleep version was the actual source
	// of TestControllerStopWaitsForInFlightOutboxSend's flakiness — under
	// scheduler load the ticker+dispatch+composing delay chain could still
	// be short of reaching Send by the time the sleep elapsed, so Stop()
	// legitimately had nothing to wait for and returned "too fast").
	sendStarted chan struct{}

	// sendFailAt (T101, ct-2026-08-29-1651), 1-indexed: the sendCallCount-th
	// call to Send fails with errFakeSend — every other call (before and
	// after) succeeds normally. 0 = never fails this way. Lets a test
	// simulate "chunk 3 of 5 fails" without a blanket setSendErr, which
	// would fail every call.
	sendFailAt    int
	sendCallCount int

	markRead    []markReadCall
	markReadErr error

	// sentMedia (T122, ct-2026-09-02-2045): every SendMedia call — shares
	// sendErr/sendCallCount with Send so a test can fail either path with
	// setSendErr without a second, parallel error knob.
	sentMedia []sentMediaCall
}

type sentCall struct {
	toJID, text string
}

type sentMediaCall struct {
	toJID, kind, mime, caption string
	dataLen                    int
	seconds                    int
}

type markReadCall struct {
	chatJID string
	sender  string
	msgIDs  []string
}

func newFakeGateway() *fakeGateway {
	return &fakeGateway{inbound: make(chan gateway.Inbound, 10), connected: true}
}

func (f *fakeGateway) Start(ctx context.Context) error { return nil }

func (f *fakeGateway) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.stopped {
		f.stopped = true
		close(f.inbound)
	}
}

func (f *fakeGateway) Connected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected
}

func (f *fakeGateway) setConnected(c bool) {
	f.mu.Lock()
	f.connected = c
	f.mu.Unlock()
}

func (f *fakeGateway) setSendErr(err error) {
	f.mu.Lock()
	f.sendErr = err
	f.mu.Unlock()
}

// setSendFailAt makes the n-th call (1-indexed) to Send fail with
// errFakeSend; every other call succeeds. n<=0 disables it.
func (f *fakeGateway) setSendFailAt(n int) {
	f.mu.Lock()
	f.sendFailAt = n
	f.mu.Unlock()
}

func (f *fakeGateway) setSendDelay(d time.Duration) {
	f.mu.Lock()
	f.sendDelay = d
	f.mu.Unlock()
}

func (f *fakeGateway) setSendStarted(ch chan struct{}) {
	f.mu.Lock()
	f.sendStarted = ch
	f.mu.Unlock()
}

func (f *fakeGateway) Inbound() <-chan gateway.Inbound { return f.inbound }

func (f *fakeGateway) Send(ctx context.Context, toJID, text string) (gateway.SendResult, error) {
	f.mu.Lock()
	delay := f.sendDelay
	started := f.sendStarted
	f.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if delay > 0 {
		time.Sleep(delay) // outside the lock: Connected()/setConnected must not block on this
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendCallCount++
	if f.sendErr != nil {
		return gateway.SendResult{}, f.sendErr
	}
	if f.sendFailAt > 0 && f.sendCallCount == f.sendFailAt {
		return gateway.SendResult{}, errFakeSend
	}
	f.sent = append(f.sent, sentCall{toJID, text})
	return gateway.SendResult{MsgID: fmt.Sprintf("fake-%d", len(f.sent)), TS: time.Now().Unix()}, nil
}

// SendMedia mirrors Send's shape (same sendErr/sendFailAt/sendCallCount
// bookkeeping) so tests that already know how to fail/pace Send get the
// same control surface for media, free. Takes gateway.OutboundMedia (T123,
// ct-2026-09-02-2121 — SendMedia moved from 6 positional params to this
// struct the first time a second media kind needed its own field).
func (f *fakeGateway) SendMedia(ctx context.Context, toJID string, media gateway.OutboundMedia) (gateway.SendResult, error) {
	f.mu.Lock()
	delay := f.sendDelay
	started := f.sendStarted
	f.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if delay > 0 {
		time.Sleep(delay)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendCallCount++
	if f.sendErr != nil {
		return gateway.SendResult{}, f.sendErr
	}
	if f.sendFailAt > 0 && f.sendCallCount == f.sendFailAt {
		return gateway.SendResult{}, errFakeSend
	}
	f.sentMedia = append(f.sentMedia, sentMediaCall{toJID, media.Kind, media.Mime, media.Caption, len(media.Data), media.Seconds})
	return gateway.SendResult{MsgID: fmt.Sprintf("fake-media-%d", len(f.sentMedia)), TS: time.Now().Unix()}, nil
}

func (f *fakeGateway) sentMediaCalls() []sentMediaCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sentMediaCall, len(f.sentMedia))
	copy(out, f.sentMedia)
	return out
}

func (f *fakeGateway) sentCalls() []sentCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sentCall, len(f.sent))
	copy(out, f.sent)
	return out
}

func (f *fakeGateway) SetTyping(ctx context.Context, toJID string, on bool) error { return nil }

func (f *fakeGateway) MarkRead(ctx context.Context, chatJID, senderJID string, msgIDs []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markRead = append(f.markRead, markReadCall{chatJID, senderJID, msgIDs})
	if f.markReadErr != nil {
		return f.markReadErr
	}
	return nil
}

// setMarkReadErr (T91): simulates gw.MarkRead failing — "websocket not
// connected", the bug's own log evidence — without needing a real
// disconnect. The call is still recorded in markRead either way, so a test
// can assert an attempt happened even though it failed.
func (f *fakeGateway) setMarkReadErr(err error) {
	f.mu.Lock()
	f.markReadErr = err
	f.mu.Unlock()
}

func (f *fakeGateway) markReadCalls() []markReadCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]markReadCall, len(f.markRead))
	copy(out, f.markRead)
	return out
}

func (f *fakeGateway) MarkDelivered(ctx context.Context, chatJID string, msgIDs []string) error {
	return nil
}

func (f *fakeGateway) QRChannel(ctx context.Context) (<-chan string, error) {
	return nil, nil
}

var errFakeSend = errors.New("fake send error")
