// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
//
// Package governor: anti-ban. A token bucket to throttle the send rate
// (no instant replies, no bursts) plus a kill switch to cut everything off.
package governor

import (
	"context"
	"math/rand"
	"sync"
	"time"
)

// Limiter is a simple token bucket (max events per window) plus an
// independent daily cap (0753) — Allow denies if EITHER is exhausted.
type Limiter struct {
	mu     sync.Mutex
	max    float64
	window time.Duration
	tokens float64
	last   time.Time
	kill   bool

	// dailyMax <= 0 disables the daily cap (unset). dailyCount/dailyDate
	// track today's sends; dailyDate ("2006-01-02", local time) resets
	// dailyCount to 0 the first time Allow() is called on a new calendar
	// day — simpler than a rolling 24h window, and good enough for a soft
	// anti-ban guardrail. dailyCount is NOT self-persisting across a
	// restart (this package has no DB access) — the caller MUST call
	// SeedDailyCount once at startup, reconstructed from message history
	// (3rd commandment: a power cut must never silently reset the daily
	// anti-ban cap to 0 and let the bot blow past it). The per-minute token
	// bucket, in contrast, is fine starting over — it's short enough to
	// recover on its own within a minute.
	dailyMax   int
	dailyCount int
	dailyDate  string
}

func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{
		max:    float64(max),
		window: window,
		tokens: float64(max),
		last:   time.Now(),
	}
}

func (l *Limiter) refill() {
	now := time.Now()
	elapsed := now.Sub(l.last).Seconds()
	rate := l.max / l.window.Seconds()
	l.tokens = min(l.max, l.tokens+elapsed*rate)
	l.last = now
}

// SetMax changes the per-window rate limit at runtime (0753 — dashboard-
// editable, ceiling-clamped by the caller before this is reached).
func (l *Limiter) SetMax(max int) {
	l.mu.Lock()
	l.max = float64(max)
	l.mu.Unlock()
}

// SetDailyMax changes the daily cap at runtime. <= 0 disables it.
func (l *Limiter) SetDailyMax(max int) {
	l.mu.Lock()
	l.dailyMax = max
	l.mu.Unlock()
}

// SeedDailyCount sets today's already-sent count directly — call ONCE at
// startup, before the limiter serves any Allow() calls, with a count the
// caller reconstructed from message history (3rd commandment: the daily cap
// must survive a restart, not reset to 0 and let the bot exceed it after a
// power cut). Also stamps dailyDate to today, so the very next Allow() call
// doesn't mistake this seed for a stale day and immediately reset it.
func (l *Limiter) SeedDailyCount(n int) {
	l.mu.Lock()
	l.dailyCount = n
	l.dailyDate = time.Now().Format("2006-01-02")
	l.mu.Unlock()
}

// Max returns the current per-window rate limit.
func (l *Limiter) Max() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return int(l.max)
}

// DailyMax returns the current daily cap (0 = disabled).
func (l *Limiter) DailyMax() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.dailyMax
}

// checkDaily resets dailyCount when the calendar day has changed since the
// last call. Caller must hold l.mu.
func (l *Limiter) checkDaily() {
	today := time.Now().Format("2006-01-02")
	if today != l.dailyDate {
		l.dailyDate = today
		l.dailyCount = 0
	}
}

// Allow consumes a token if one is available, the daily cap (if set) isn't
// exhausted, and the kill switch is off.
func (l *Limiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.kill {
		return false
	}
	l.checkDaily()
	if l.dailyMax > 0 && l.dailyCount >= l.dailyMax {
		return false
	}
	l.refill()
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	l.dailyCount++
	return true
}

// SetKill enables/disables the total send cutoff.
func (l *Limiter) SetKill(k bool) {
	l.mu.Lock()
	l.kill = k
	l.mu.Unlock()
}

func (l *Limiter) Killed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.kill
}

// DelayWindow bounds a randomized human-pacing delay for one class of
// WhatsApp-server-facing action (dispatch, read, or generic server
// interaction) — anti-ban: never instant, even reads. Generalizes what used
// to be a single private randDelay in gateway.go into one reusable helper
// with named windows per action class.
type DelayWindow struct {
	Min time.Duration
	Max time.Duration
}

// NewDelayWindow builds a window, falling back to (defMin, defMax) when min
// is non-positive or max is non-positive/inverted — the same normalization
// gateway.defaultConfig already did inline for the dispatch window.
func NewDelayWindow(min, max, defMin, defMax time.Duration) DelayWindow {
	if min <= 0 {
		min = defMin
	}
	if max <= 0 || max < min {
		max = defMax
	}
	return DelayWindow{Min: min, Max: max}
}

// Random returns a uniformly random duration in [Min, Max].
func (w DelayWindow) Random() time.Duration {
	if w.Max <= w.Min {
		return w.Min
	}
	//nolint:gosec // non-crypto random is intentional for human pacing
	return w.Min + time.Duration(rand.Int63n(int64(w.Max-w.Min)))
}

// Sleep waits a random duration from the window, returning early if ctx is
// cancelled (so shutdown isn't blocked by a pending humanized delay).
func (w DelayWindow) Sleep(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(w.Random()):
	}
}
