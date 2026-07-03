// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
//
// Package state: the switchboard's face. Writes status.json atomically
// (the contract with the display adapter). See ../../../contracts/status.schema.json
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ValidMoods is the full set of valid face moods (contract with the display).
// reading/switching/done/muted/paused were added later — see
// moodTier for how each one is prioritized against the others.
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
	"paused":     true,
	"alert":      true,
	"error":      true,
	"qr":         true,
}

// moodTier classifies a mood into its precedence tier:
//
//	3 (system) — error/qr/sleeping/muted/paused. Set directly via
//	             UpdateMood/SetMood/SetMuted, which ARE the ground-truth
//	             authority for these and are never gated. React() and
//	             SetResting() ARE gated: while a tier-3 mood is active they
//	             refuse to override it (no-op) — a transient event must
//	             never mask "WhatsApp is down" or "muted".
//	2 (queue)  — swamped/few/zero, i.e. what RestingMood computes.
//	1 (other)  — every transient event (reading, switching, responding,
//	             thinking, done, new_msg, ai_online, vip) plus the startup
//	             "idle" baseline. Deliberately NOT blocked by tier 2: an
//	             agent almost always has a nonzero queue when it calls the
//	             tools that trigger these, so "queue > transient events"
//	             would suppress the entire feature — see React's doc
//	             comment for the precedent this follows (revert-to, not
//	             block-by).
func moodTier(mood string) int {
	switch mood {
	case "error", "qr", "sleeping", "muted", "paused":
		return 3
	case "swamped", "few", "zero":
		return 2
	default:
		return 1
	}
}

// Status is the core <-> display contract. Field names and JSON tags match
// contracts/status.schema.json exactly.
type Status struct {
	Mood            string `json:"mood"`
	Speech          string `json:"speech,omitempty"`
	Queue           int    `json:"queue"`
	LastMsg         string `json:"last_msg,omitempty"`
	WAConnected     bool   `json:"wa_connected"`
	ShowQR          bool   `json:"show_qr"`
	QRData          string `json:"qr_data,omitempty"`
	Battery         *int   `json:"battery,omitempty"`
	// Voltage/Charging/TimeRemaining ride the same battery.json sidecar
	// merge as Battery (milestones C/D) — see
	// ReadBatteryFile. Voltage is millivolts; TimeRemaining is minutes.
	Voltage       *int `json:"voltage,omitempty"`
	Charging      bool `json:"charging"`
	TimeRemaining *int `json:"time_remaining,omitempty"`
	// CPU/RAM are read straight from /proc by internal/sysinfo (milestone E)
	// — nil when unavailable (non-Linux dev host).
	CPU             *int   `json:"cpu,omitempty"`
	RAM             *int   `json:"ram,omitempty"`
	Wifi            *int   `json:"wifi,omitempty"`
	IP              string `json:"ip,omitempty"`
	Hostname        string `json:"hostname,omitempty"`
	SSID            string `json:"ssid,omitempty"`
	OwnJID          string `json:"own_jid,omitempty"`
	AgentConnected  bool   `json:"agent_connected"`
	// Agents is the count of currently active MCP connections (extends the
	// older AgentConnected bool, kept alongside it for
	// backward compat since the renderer's fallback path still reads it).
	Agents int `json:"agents"`
	// Uptime is seconds since this core process started (not OS uptime).
	Uptime int `json:"uptime"`
	ReconnectPaused bool   `json:"reconnect_paused"`
	// Muted mirrors the governor's kill switch into the core<->display
	// contract — previously the switch lived ONLY in
	// governor.Limiter (in-memory) and REST's /api/killswitch; the display
	// had no way to show it at all. Renamed from the earlier internal
	// "killed" naming per the owner's own words ("no le diria a Kill
	// Switch... mudo esta bien") — "kill" implies destroying something,
	// "muted" is just silence.
	Muted bool `json:"muted"`
	// Sent is the all-time count of successfully sent outbound messages —
	// the "contador de mensajes enviados" (pwnagotchi-style SENT n
	// footer). Derived from store.CountOutboundSince(0), never a separately
	// incremented counter (that would drift on a retry/crash mismatch
	// between the increment and the actual send) — see main.go/gateway.go
	// for where it's (re)computed: once at boot, and again after each
	// confirmed send.
	Sent      int    `json:"sent"`
	// Face is the literal kaomoji string the display adapter's renderer just
	// drew for this mood/animation frame — rides the
	// same sidecar-merge pattern as Battery/Voltage (see ReadFaceFile): the
	// display service is the ONLY writer of face.json, the core merges it
	// in here on its own heartbeat. nil when the display adapter isn't
	// running, hasn't rendered yet, or the current mood is "qr" (no kaomoji
	// face is drawn then) — no-placeholder, never a fabricated face.
	Face      *string `json:"face,omitempty"`
	UpdatedAt string  `json:"updated_at"`
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
// A value ≤ 0 falls back to the default of 8.
func NewManager(path string, swampedAt int) *Manager {
	if swampedAt <= 0 {
		swampedAt = 8
	}
	return &Manager{
		path:      path,
		cur:       Status{Mood: "idle"},
		swampedAt: swampedAt,
	}
}

// Update applies a mutation and atomically rewrites status.json.
// Does NOT cancel pending React reverts — safe for non-mood fields
// (e.g. Battery, IP, Hostname, LastMsg, Queue).
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
// mood is active (see moodTier) — a queue-depth refresh must
// never mask "WhatsApp is down"/"muted"/etc.
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
// No-op while a tier-3 system mood is active (see moodTier)
// — an event notification (the agent read a message, sent a reply, etc.)
// must never flash over "the WhatsApp connection is down" or "muted".
// Deliberately NOT gated by tier-2 queue moods: the agent almost always has
// a nonzero queue when it calls the tools that trigger these events, so
// blocking on queue would suppress the whole feature — see moodTier's doc
// comment. The system-mood guard itself only needs to run at the START:
// reverting a NEWER system mood back to this event is already impossible
// because UpdateMood/SetMood/SetMuted all bump reactGen too, so the revert
// goroutine below sees a generation mismatch and backs off on its own.
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
// tier-3 SYSTEM authority — mirrors how error/qr/paused
// get set directly via UpdateMood, never gated. Muting forces mood="muted"
// so no transient event can mask it; unmuting reverts to the current
// resting mood (whatever the queue says right now).
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

// batteryFile is the shape the power adapter's battery.json sidecar writes
// (entregable D3's single-writer fix): the core is the
// ONLY writer of status.json (a second writer risked a race that could
// clobber e.g. mood="paused"/"error" — see the contract's D3 rework note),
// so the power adapter instead writes just its reading here, and the core
// reads it back in on its own heartbeat (see ReadBatteryFile / main.go).
// VoltageMV/TimeRemainingMin are pointers (milestones
// C/D): the power adapter's estimator may not have a real reading yet (no
// sensor, or not enough samples) — JSON null there must stay nil here, never
// decode to a fake 0.
type batteryFile struct {
	Battery          int   `json:"battery"`
	VoltageMV        *int  `json:"voltage_mv"`
	Charging         bool  `json:"charging"`
	TimeRemainingMin *int  `json:"time_remaining_min"`
	TS               int64 `json:"ts"`
}

// BatteryReading is the merged-in shape ReadBatteryFile hands back to a
// caller (extends the original battery-only reading).
type BatteryReading struct {
	Battery          int
	VoltageMV        *int
	Charging         bool
	TimeRemainingMin *int
}

// ReadBatteryFile reads the power adapter's battery.json sidecar at path
// and returns the reading if present and no older than maxAge. A missing
// file, unparseable content, or a stale timestamp all return ok=false —
// meaning "no real reading right now", never a fabricated value (same
// battery-honesty rule the render side already enforces). Callers should
// clear Status.Battery/Voltage/TimeRemaining to nil when ok is false, not
// leave a very old value standing forever.
func ReadBatteryFile(path string, maxAge time.Duration) (BatteryReading, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BatteryReading{}, false
	}
	var bf batteryFile
	if err := json.Unmarshal(data, &bf); err != nil {
		return BatteryReading{}, false
	}
	if time.Now().Unix()-bf.TS > int64(maxAge.Seconds()) {
		return BatteryReading{}, false
	}
	return BatteryReading{
		Battery:          bf.Battery,
		VoltageMV:        bf.VoltageMV,
		Charging:         bf.Charging,
		TimeRemainingMin: bf.TimeRemainingMin,
	}, true
}

// faceFile is the shape the display adapter's face.json sidecar writes
// — same single-writer rationale as batteryFile above:
// the display service is the only writer, the core reads it back in on its
// own heartbeat. Face is a pointer since "qr" mood legitimately has no
// kaomoji face (null in the JSON, not an empty string).
type faceFile struct {
	Face *string `json:"face"`
	TS   int64   `json:"ts"`
}

// ReadFaceFile reads the display adapter's face.json sidecar at path and
// returns the face string if present and no older than maxAge — same
// staleness/no-placeholder discipline as ReadBatteryFile (a missing file,
// unparseable content, or a stale timestamp all return ok=false; callers
// should clear Status.Face to nil then, not leave a stale face standing).
func ReadFaceFile(path string, maxAge time.Duration) (*string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var ff faceFile
	if err := json.Unmarshal(data, &ff); err != nil {
		return nil, false
	}
	if time.Now().Unix()-ff.TS > int64(maxAge.Seconds()) {
		return nil, false
	}
	return ff.Face, true
}
