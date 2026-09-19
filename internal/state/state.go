// Package state: writes status.json atomically — a status/mood contract for
// an optional external display consumer. Ported from Piumy (leaf, no
// internal deps); the battery/face sidecar merge was dropped (see
// docs/F1B-INFRA-ROUTING.md) since piumy-gateway has no display/power
// adapter to write those sidecars in the first place.
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ValidMoods is the full set of valid face moods (contract with the display).
var ValidMoods = map[string]bool{
	"idle":       true,
	"zero":       true,
	"new_msg":    true,
	"few":        true,
	"swamped":    true,
	"reading":    true,
	"switching":  true,
	"thinking":   true,
	"working":    true,
	"responding": true,
	"done":       true,
	"ai_online":  true,
	"vip":        true,
	"muted":      true,
	"sleeping":   true,
	"alert":      true,
	"error":      true,
	"qr":         true,
}

// moodTier classifies a mood into its precedence tier:
//
//	3 (system) — error/qr/sleeping/muted/alert. Set directly via
//	             UpdateMood/SetMood/SetMuted, which ARE the ground-truth
//	             authority for these and are never gated. React() and
//	             SetResting() ARE gated: while a tier-3 mood is active they
//	             refuse to override it (no-op) — a transient event must
//	             never mask "connection is down" or "muted". "alert" (T98,
//	             ct-2026-08-29-0621) is StreamReplaced's own signal
//	             ("otro proceso te tomó la sesión de WhatsApp") — same
//	             precedence as the rest of this tier, so a transient event
//	             can't quietly bury it either. "paused" (the old "me rendí,
//	             actué solo" circuit breaker) lived here too until T106
//	             (ct-2026-08-29-2234) — T99 made reconnection never give up,
//	             so that state stopped having a case where it could occur.
//	2 (queue)  — swamped/few/zero, i.e. what RestingMood computes.
//	1 (other)  — every transient event (reading, switching, responding,
//	             thinking, done, new_msg, ai_online, vip) plus the startup
//	             "idle" baseline. Deliberately NOT blocked by tier 2: the
//	             caller almost always has a nonzero queue when it triggers
//	             these, so "queue > transient events" would suppress the
//	             entire feature — see React's doc comment for the precedent
//	             this follows (revert-to, not block-by).
func moodTier(mood string) int {
	switch mood {
	case "error", "qr", "sleeping", "muted", "alert":
		return 3
	case "swamped", "few", "zero":
		return 2
	default:
		return 1
	}
}

// Status is the core <-> display contract.
type Status struct {
	Mood        string `json:"mood"`
	Speech      string `json:"speech,omitempty"`
	Queue       int    `json:"queue"`
	LastMsg     string `json:"last_msg,omitempty"`
	WAConnected bool   `json:"wa_connected"`
	ShowQR      bool   `json:"show_qr"`
	QRData      string `json:"qr_data,omitempty"`
	// CPU/RAM are read straight from /proc by internal/sysinfo — nil when
	// unavailable (non-Linux dev host).
	CPU      *int   `json:"cpu,omitempty"`
	RAM      *int   `json:"ram,omitempty"`
	Wifi     *int   `json:"wifi,omitempty"`
	IP       string `json:"ip,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	SSID     string `json:"ssid,omitempty"`
	OwnJID   string `json:"own_jid,omitempty"`
	// OwnName is the linked account's own WhatsApp display name
	// (whatsmeow's Store.PushName) — set once on connect, alongside OwnJID.
	OwnName        string `json:"own_name,omitempty"`
	AgentConnected bool   `json:"agent_connected"`
	// Agents is the count of currently active MCP connections (extends the
	// older AgentConnected bool, kept alongside it for backward compat).
	Agents int `json:"agents"`
	// Uptime is seconds since this core process started (not OS uptime).
	Uptime int `json:"uptime"`
	// Muted mirrors the governor's kill switch into the core<->display
	// contract.
	Muted bool `json:"muted"`
	// Sent is the all-time count of successfully sent outbound messages.
	// Derived from store.CountOutboundSince(0), never a separately
	// incremented counter (that would drift on a retry/crash mismatch
	// between the increment and the actual send).
	Sent      int    `json:"sent"`
	UpdatedAt string `json:"updated_at"`

	// Backpressure / BackpressureReason (S3, ct-2026-07-30-030948) mirror
	// capipush's own anti-flood gate — an explicit signal for the AGENT
	// (get_status embeds Status), which can't read the gateway's log file.
	// NOT the same thing as Mood's "swamped" tier: that's a cosmetic
	// queue-depth face with its OWN independent threshold (Manager.swampedAt,
	// RestingMood) — coincidentally the same default number, unrelated
	// mechanism. True while capipush is pausing dispatch to every non-boss
	// chat (the boss's own chat is never paused); BackpressureReason names
	// the count/threshold that triggered it, "" while not swamped.
	Backpressure       bool   `json:"backpressure"`
	BackpressureReason string `json:"backpressure_reason,omitempty"`
}

// Write persists the status atomically (tmp + rename) and stamps UpdatedAt.
func Write(path string, s Status) error {
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Manager keeps the state in memory and persists it on every change.
type Manager struct {
	path      string
	mu        sync.Mutex
	cur       Status
	swampedAt int    // queue depth at which resting mood becomes "swamped"
	reactGen  uint64 // generation counter — cancels stale React reverts
}

// NewManager creates a Manager. swampedAt is the queue depth threshold for
// the "swamped" resting mood (queue > swampedAt → swamped; 1..swampedAt → few).
// A value <= 0 falls back to the default of 8.
//
// T17 (ct-2026-08-05-1240, Part 1): seeds OwnName/OwnJID from an existing
// status.json at path, when one is readable — before this, every restart
// started both blank no matter what was already on disk, so the header
// showed no name/number for the whole window between process start and the
// first *events.Connected — on a slow reconnect, or one that never lands,
// that window is the entire runtime, not a brief flicker.
//
// Deliberately narrow: only these two identity fields are carried over, NOT
// the whole Status. Everything else (Mood, WAConnected, Queue, Muted,
// Backpressure, CPU/RAM, ...) is live/session state that must start fresh
// at boot — seeding a stale Mood="responding" or WAConnected=true from a
// crashed run would lie about the CURRENT process, which is a worse bug
// than the blank name this fixes. A missing file, or one that fails to
// parse (first run ever, or a hand-edited/corrupt file), leaves both blank,
// same as always — this is a best-effort warm start, never a requirement.
//
// What's seeded can be stale (a real reconnect self-corrects the moment it
// lands) — see recordOwnIdentity's own doc (internal/whatsmeow/inbound.go)
// for the other half of this fix: it no longer blanks out a seeded/known
// OwnName just because a given connect's Store.PushName came back empty.
func NewManager(path string, swampedAt int) *Manager {
	if swampedAt <= 0 {
		swampedAt = 8
	}
	cur := Status{Mood: "idle"}
	if data, err := os.ReadFile(path); err == nil {
		var seeded Status
		if json.Unmarshal(data, &seeded) == nil {
			cur.OwnName, cur.OwnJID = seeded.OwnName, seeded.OwnJID
		}
	}
	return &Manager{
		path:      path,
		cur:       cur,
		swampedAt: swampedAt,
	}
}

// Update applies a mutation and atomically rewrites status.json.
// Does NOT cancel pending React reverts — safe for non-mood fields
// (e.g. IP, Hostname, LastMsg, Queue).
func (m *Manager) Update(mut func(*Status)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	mut(&m.cur)
	return Write(m.path, m.cur)
}

// UpdateMood applies a mutation, bumps reactGen (cancelling any pending React
// revert), and persists atomically. Use whenever the Mood field is being set.
func (m *Manager) UpdateMood(mut func(*Status)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reactGen++
	mut(&m.cur)
	return Write(m.path, m.cur)
}

// SetMood changes only the face mood and cancels any pending React revert.
func (m *Manager) SetMood(mood string) error {
	return m.UpdateMood(func(s *Status) { s.Mood = mood })
}

// RestingMood returns the appropriate resting mood for a given queue depth:
//
//	0            → "zero"
//	1..swampedAt → "few"
//	>swampedAt   → "swamped"
//
// Safe to call outside the lock (swampedAt is set once at construction).
func (m *Manager) RestingMood(queue int) string {
	if queue == 0 {
		return "zero"
	}
	if queue <= m.swampedAt {
		return "few"
	}
	return "swamped"
}

// restingSpeech returns a default chatter line for a given resting mood.
func restingSpeech(mood string) string {
	switch mood {
	case "zero":
		return "all clear"
	case "few":
		return "messages waiting"
	case "swamped":
		return "inbox on fire"
	default:
		return ""
	}
}

// SetResting sets mood to RestingMood(current Queue) with a matching default
// speech and cancels any pending React revert. No-op while a tier-3 system
// mood is active (see moodTier) — a queue-depth refresh must never mask
// "connection is down"/"muted"/etc.
func (m *Manager) SetResting() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if moodTier(m.cur.Mood) >= 3 {
		return nil
	}
	m.reactGen++
	m.cur.Mood = m.RestingMood(m.cur.Queue)
	m.cur.Speech = restingSpeech(m.cur.Mood)
	return Write(m.path, m.cur)
}

// React sets a transient mood+speech, cancels previous React reverts (via a
// generation counter), and after ttl reverts to the resting mood derived from
// the then-current Queue — unless a newer mood-setting call has occurred.
//
// No-op while a tier-3 system mood is active (see moodTier) — an event
// notification must never flash over "the connection is down" or "muted".
// Deliberately NOT gated by tier-2 queue moods: the caller almost always has
// a nonzero queue when it triggers these events, so blocking on queue would
// suppress the whole feature — see moodTier's doc comment. The system-mood
// guard itself only needs to run at the START: reverting a NEWER system mood
// back to this event is already impossible because UpdateMood/SetMood/
// SetMuted all bump reactGen too, so the revert goroutine below sees a
// generation mismatch and backs off on its own.
//
// Concurrent calls are safe: only the latest generation ever reverts.
func (m *Manager) React(mood, speech string, ttl time.Duration) error {
	m.mu.Lock()
	if moodTier(m.cur.Mood) >= 3 {
		m.mu.Unlock()
		return nil
	}
	m.reactGen++
	myGen := m.reactGen
	m.cur.Mood = mood
	m.cur.Speech = speech
	err := Write(m.path, m.cur)
	m.mu.Unlock()

	if err != nil {
		return err
	}

	go func() {
		time.Sleep(ttl)
		m.mu.Lock()
		defer m.mu.Unlock()
		// A newer React or UpdateMood call has taken over — do not revert.
		if m.reactGen != myGen {
			return
		}
		m.cur.Mood = m.RestingMood(m.cur.Queue)
		m.cur.Speech = ""
		_ = Write(m.path, m.cur)
	}()

	return nil
}

// SetMuted sets the mute flag and, unlike React/SetResting, is itself a
// tier-3 SYSTEM authority — mirrors how error/qr/alert get set directly via
// UpdateMood, never gated. Muting forces mood="muted" so no transient event
// can mask it; unmuting reverts to the current resting mood (whatever the
// queue says right now).
func (m *Manager) SetMuted(muted bool) error {
	return m.UpdateMood(func(s *Status) {
		s.Muted = muted
		if muted {
			s.Mood = "muted"
			s.Speech = "muted -- not replying"
		} else {
			s.Mood = m.RestingMood(s.Queue)
			s.Speech = restingSpeech(s.Mood)
		}
	})
}

// Snapshot returns a copy of the current state.
func (m *Manager) Snapshot() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cur
}
