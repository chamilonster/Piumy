// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
//
// Package mcpguard: anti-flood protection for the MCP surface. The
// requirement, plainly: "el sistema debe ser capaz de
// bloquear a la IA si se pone a hacer flood... algunas IAs son bastante
// tontas y podrían usar el MCP." This is a DIFFERENT limiter from
// internal/governor (that one paces WhatsApp-OUTBOUND sends; this one paces
// MCP-INBOUND tool calls from a connected agent) — kept in its own package
// on purpose, by design, so the two never get conflated
// or accidentally share config.
//
// Deliberately mcp-go-agnostic: this package knows nothing about
// mcp.CallToolRequest or server.ClientSessionFromContext — the adapter that
// bridges it into an actual ToolHandlerMiddleware lives in mcpserver (the
// package that owns the MCP protocol seam), the same way whatsmeow specifics
// never leave the gateway package. Guard.Check takes a plain string key and
// a bool, and is fully unit-testable with no mcp-go dependency.
package mcpguard

import (
	"fmt"
	"log"
	"sort"
	"sync"
	"time"
)

// clientStaleAfter bounds how long an idle client's tracking entry survives
// before opportunistic cleanup reclaims it. A Pi Zero has 512MB total RAM,
// and every MCP reconnect can hand a client a fresh session ID — nothing
// here should accumulate forever just because clients come and go.
const clientStaleAfter = 1 * time.Hour

// unknownKey is the shared bucket used when the caller can't identify the
// connecting client (e.g. ClientSessionFromContext returned nil). This is
// the deliberate fail-safe middle ground: not a full block (a
// caller that genuinely can't be identified still gets served) and not
// unlimited passage either (it shares one bucket, so it's still capped).
const unknownKey = "unknown"

// Config are the tunables, all dashboard-editable at runtime (KV-override,
// same mechanism as 0753) — see restapi's /api/mcp-guard. Zero values fall
// back to defaults in New, mirroring sessionbackup.New/governor's own
// pattern of "a Config field left at its zero value just means 'use the
// shipped default'", so callers never have to remember every field.
type Config struct {
	RatePerMin     int           // general tool-call cap per client, per minute
	EmitRatePerMin int           // stricter cap for send_message/escalate specifically
	BlockThreshold int           // consecutive throttled calls that trip the circuit breaker
	BlockCooldown  time.Duration // how long a tripped client is fully blocked
}

// Verdict is Check's answer: either the call proceeds, or Reason explains
// why not (returned to the agent as a tool error — plain text, so a
// non-malicious-but-buggy agent can read it and self-correct, e.g. "oh, I
// need to slow down").
type Verdict struct {
	Allowed bool
	Reason  string
}

// clientEntry tracks one connected client's flood state. Two independent
// token buckets — general vs. emit — because send_message/escalate are the
// tools that can do real-world harm (an actual WhatsApp message sent, a
// chat escalated); a buggy agent hammering ONLY those should trip the
// stricter limit even while comfortably under the general call-rate cap.
type clientEntry struct {
	general      bucket
	emit         bucket
	throttleHits int
	blockedUntil time.Time
	lastSeen     time.Time
}

// bucket is a minimal token bucket — deliberately not governor.Limiter:
// this package tracks one bucket PER CLIENT (a map of them), while governor
// is a singleton with its own daily cap and kill switch that have no
// meaning here. Re-deriving ~10 lines is cheaper than coupling two
// packages deliberately kept separate.
type bucket struct {
	tokens float64
	last   time.Time
}

// allow consumes one token if available, refilling at max/window since the
// last call. The first call on a fresh bucket starts full (a brand-new
// client isn't punished for the bucket having "just been created").
func (b *bucket) allow(max float64, window time.Duration, now time.Time) bool {
	if b.last.IsZero() {
		b.tokens = max
	} else {
		elapsed := now.Sub(b.last).Seconds()
		b.tokens = min(max, b.tokens+elapsed*(max/window.Seconds()))
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Guard is the anti-flood limiter. The zero value is not usable — build one
// with New. Safe for concurrent use.
type Guard struct {
	mu sync.Mutex

	ratePerMin     int
	emitRatePerMin int
	blockThreshold int
	blockCooldown  time.Duration

	clients map[string]*clientEntry
}

// New builds a Guard from cfg, applying defaults for any zero field.
// Numbers set 2026-07-01:
// 120/min general, 20/min for send_message/escalate (a real flood is
// hundreds/min — generous enough to never throttle a legitimate burst of
// reads), 5 consecutive throttles trips a 5-minute block. Provisional, like
// every other numeric knob in this project — editable via KV/dashboard
// without a rebuild.
func New(cfg Config) *Guard {
	g := &Guard{
		ratePerMin:     cfg.RatePerMin,
		emitRatePerMin: cfg.EmitRatePerMin,
		blockThreshold: cfg.BlockThreshold,
		blockCooldown:  cfg.BlockCooldown,
		clients:        make(map[string]*clientEntry),
	}
	if g.ratePerMin <= 0 {
		g.ratePerMin = 120
	}
	if g.emitRatePerMin <= 0 {
		g.emitRatePerMin = 20
	}
	if g.blockThreshold <= 0 {
		g.blockThreshold = 5
	}
	if g.blockCooldown <= 0 {
		g.blockCooldown = 5 * time.Minute
	}
	return g
}

// Check is the anti-flood gate for one tool call. clientKey identifies the
// caller (the MCP session ID — falls back to the shared "unknown" bucket
// when empty, see unknownKey); emit marks a call to send_message/escalate,
// which is checked against the stricter emit bucket too.
func (g *Guard) Check(clientKey string, emit bool) Verdict {
	if clientKey == "" {
		clientKey = unknownKey
	}
	now := time.Now()

	g.mu.Lock()
	defer g.mu.Unlock()

	g.sweepLocked(now)

	e := g.clients[clientKey]
	if e == nil {
		e = &clientEntry{}
		g.clients[clientKey] = e
	}
	e.lastSeen = now

	// Circuit breaker: once a client has been throttled blockThreshold times
	// in a row, stop doing per-call token-bucket math for it entirely and
	// just refuse — cheaper under sustained flood, and gives the agent (or
	// whoever operates it) an unambiguous "you are blocked" signal instead
	// of an endless stream of "slow down" it might otherwise keep ignoring.
	if now.Before(e.blockedUntil) {
		return Verdict{Reason: fmt.Sprintf("blocked: too many rate-limited calls, try again after %s", e.blockedUntil.UTC().Format(time.RFC3339))}
	}

	ok := e.general.allow(float64(g.ratePerMin), time.Minute, now)
	if ok && emit {
		ok = e.emit.allow(float64(g.emitRatePerMin), time.Minute, now)
	}
	if ok {
		// A clean call resets the streak — the breaker is meant to catch
		// SUSTAINED flooding, not penalize a client forever over one early
		// burst it already backed off from.
		e.throttleHits = 0
		return Verdict{Allowed: true}
	}

	e.throttleHits++
	if e.throttleHits >= g.blockThreshold {
		e.blockedUntil = now.Add(g.blockCooldown)
		e.throttleHits = 0
		log.Printf("mcpguard: client %s blocked for %s after repeated flooding", clientKey, g.blockCooldown)
		return Verdict{Reason: fmt.Sprintf("blocked: too many rate-limited calls, try again after %s", e.blockedUntil.UTC().Format(time.RFC3339))}
	}
	return Verdict{Reason: "rate limited, slow down"}
}

// sweepLocked drops entries idle for more than clientStaleAfter. Caller
// must hold g.mu. Cheap by construction: the expected client count is a
// handful of connected agents, not thousands, so an O(n) walk on every
// Check call is negligible next to the map lookup it's already doing.
func (g *Guard) sweepLocked(now time.Time) {
	for key, e := range g.clients {
		if now.Sub(e.lastSeen) > clientStaleAfter {
			delete(g.clients, key)
		}
	}
}

// --- runtime-editable config (dashboard/REST, mirrors governor.Limiter's
// SetMax/SetDailyMax pattern) ---

func (g *Guard) SetRatePerMin(n int) {
	g.mu.Lock()
	g.ratePerMin = n
	g.mu.Unlock()
}

func (g *Guard) SetEmitRatePerMin(n int) {
	g.mu.Lock()
	g.emitRatePerMin = n
	g.mu.Unlock()
}

func (g *Guard) SetBlockThreshold(n int) {
	g.mu.Lock()
	g.blockThreshold = n
	g.mu.Unlock()
}

func (g *Guard) SetBlockCooldown(d time.Duration) {
	g.mu.Lock()
	g.blockCooldown = d
	g.mu.Unlock()
}

func (g *Guard) RatePerMin() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.ratePerMin
}

func (g *Guard) EmitRatePerMin() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.emitRatePerMin
}

func (g *Guard) BlockThreshold() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.blockThreshold
}

func (g *Guard) BlockCooldown() time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.blockCooldown
}

// ClientStatus is one tracked client's flood state, for dashboard/REST
// visibility. Key is the MCP session ID — an opaque per-connection
// identifier, NOT the auth secret (PIMYWA_MCP_KEY) — safe to show/log in
// full (by design: "log del SessionID (nunca el token)").
type ClientStatus struct {
	Key          string    `json:"session_id"`
	ThrottleHits int       `json:"throttle_hits"`
	Blocked      bool      `json:"blocked"`
	BlockedUntil time.Time `json:"blocked_until,omitempty"`
}

// Status is the full dashboard/REST snapshot: effective config plus every
// currently-tracked client. Never anything secret — see ClientStatus.
type Status struct {
	RatePerMin       int            `json:"rate_per_min"`
	EmitRatePerMin   int            `json:"emit_rate_per_min"`
	BlockThreshold   int            `json:"block_threshold"`
	BlockCooldownSec float64        `json:"block_cooldown_sec"`
	Clients          []ClientStatus `json:"clients"`
}

func (g *Guard) Status() Status {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	st := Status{
		RatePerMin:       g.ratePerMin,
		EmitRatePerMin:   g.emitRatePerMin,
		BlockThreshold:   g.blockThreshold,
		BlockCooldownSec: g.blockCooldown.Seconds(),
	}
	for key, e := range g.clients {
		cs := ClientStatus{Key: key, ThrottleHits: e.throttleHits, Blocked: now.Before(e.blockedUntil)}
		if cs.Blocked {
			cs.BlockedUntil = e.blockedUntil
		}
		st.Clients = append(st.Clients, cs)
	}
	sort.Slice(st.Clients, func(i, j int) bool { return st.Clients[i].Key < st.Clients[j].Key })
	return st
}
