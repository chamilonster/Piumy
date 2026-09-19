// profileStatusCache bounds and caches get_status's read of the WhatsApp
// account's own "About" text (T103/T104, ct-2026-08-29-1759/1818 — added
// after Citrino's audit, not in the original cut). GetProfileStatus wraps a
// LIVE network call to WhatsApp (whatsmeow.GetUserInfo) with no timeout of
// its own — and get_status is the exact tool the manual tells an agent to
// call to diagnose a dead connection (connect/SKILL.md: "si get_status
// también falla, entonces sí es la conexión"). A get_status that hangs on a
// dead WhatsApp link defeats its own diagnostic purpose — the same shape of
// bug T104 exists to close, just one layer down. get_status must never hang
// because of this, and the account's status text changes rarely while
// get_status is the most-called tool in the system — caching avoids hitting
// WhatsApp on every single call.
package mcpserver

import (
	"context"
	"sync"
	"time"
)

const (
	// profileStatusTimeout bounds the live read — get_status answers within
	// this no matter what GroupProfile does.
	profileStatusTimeout = 2 * time.Second
	// profileStatusTTL is how long a successful read is reused before the
	// next get_status call reads again.
	profileStatusTTL = 30 * time.Second
)

// profileStatusCache holds the last successfully read status. Safe for
// concurrent use — get_status can be called from many terminals at once.
type profileStatusCache struct {
	mu        sync.Mutex
	value     string
	fetchedAt time.Time
}

// get returns the cached status if still fresh, or reads a new one bounded
// by profileStatusTimeout. ok=false means the value is NOT trustworthy right
// now (the read failed, timed out, or was never attempted) — distinct from
// a legitimate "" (no status set, ok=true). A failed/timed-out read does
// NOT get cached: the next call tries again rather than reusing a stale
// error.
func (c *profileStatusCache) get(ctx context.Context, gp GroupProfile) (status string, ok bool) {
	c.mu.Lock()
	if !c.fetchedAt.IsZero() && time.Since(c.fetchedAt) < profileStatusTTL {
		v := c.value
		c.mu.Unlock()
		return v, true
	}
	c.mu.Unlock()

	tctx, cancel := context.WithTimeout(ctx, profileStatusTimeout)
	defer cancel()
	v, err := gp.GetProfileStatus(tctx)
	if err != nil {
		return "", false
	}

	c.mu.Lock()
	c.value = v
	c.fetchedAt = time.Now()
	c.mu.Unlock()
	return v, true
}
