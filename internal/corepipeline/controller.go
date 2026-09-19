package corepipeline

import (
	"context"
	"sync"

	"piumy-gateway/internal/eventbus"
	"piumy-gateway/internal/gateway"
	"piumy-gateway/internal/store"
)

// Controller orchestrates a gateway.Gateway + Pipeline lifecycle. Much
// simpler than Piumy's gateway.Controller: no QR/pairing/reconnect state —
// open-wa manages its own session outside this process (F3).
type Controller struct {
	gw   gateway.Gateway
	pipe *Pipeline

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	doneCh  chan struct{}
}

// NewController wires gw and pipe together. Does not start anything; call
// Start().
func NewController(gw gateway.Gateway, pipe *Pipeline) *Controller {
	return &Controller{gw: gw, pipe: pipe}
}

// Start connects the gateway and launches the pipeline's loops in a
// background goroutine. Idempotent — no-op if already running. If
// gw.Start fails, Start returns that error without launching the pipeline.
func (c *Controller) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := c.gw.Start(ctx); err != nil {
		cancel()
		return err
	}

	doneCh := make(chan struct{})
	c.cancel = cancel
	c.doneCh = doneCh
	c.running = true

	go func() {
		defer func() {
			c.mu.Lock()
			c.running = false
			c.mu.Unlock()
			close(doneCh)
		}()
		c.pipe.Run(ctx)
	}()

	return nil
}

// Stop cancels the pipeline's loops, waits for them to exit, then
// disconnects the gateway. Idempotent — safe to call when already stopped.
func (c *Controller) Stop() {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return
	}
	c.running = false
	cancel := c.cancel
	c.cancel = nil
	doneCh := c.doneCh
	c.mu.Unlock()

	cancel()
	<-doneCh
	c.gw.Stop()
}

// Resume is Start() under another name — equivalent and idempotent, for
// callers that think in terms of "resuming" a previously stopped gateway.
func (c *Controller) Resume() error {
	return c.Start()
}

// Status reports the gateway's current connection state.
func (c *Controller) Status() gateway.Status {
	return gateway.Status{Connected: c.gw.Connected()}
}

// SetBus registers the event bus the pipeline publishes to. Safe to call at
// any time, including before Start().
func (c *Controller) SetBus(b *eventbus.Bus) {
	c.pipe.SetBus(b)
}

// MarkRead schedules honest read receipts for msgs — no-op if the pipeline
// isn't currently running.
func (c *Controller) MarkRead(chatJID string, msgs []store.Message) {
	c.mu.Lock()
	running := c.running
	c.mu.Unlock()
	if !running {
		return
	}
	c.pipe.MarkRead(chatJID, msgs)
}
