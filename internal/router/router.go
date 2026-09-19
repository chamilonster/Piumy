// Package router: per-number rules. For a given chat it resolves which mode
// / bridge plugin / model / terminal_id applies, and whether the JID is a
// VIP contact (Whitelist entries and vip:true routes both mark VIPs — this
// only affects the tray icon's mood on message, it does not gate anything).
// Loaded from router.json (zero hardcode).
//
// T64 (ct-2026-08-11-1627) removed the whitelist GATE — everyone is allowed
// by default, store.ChatIsOff (ignored/blacklist) is the one remaining
// brake, enforced in mcpserver/send.go, not here. Whitelist/IsVIP survive
// only as the VIP-marking mechanism; nothing in this package decides who
// may be messaged anymore.
package router

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type Route struct {
	Match  string `json:"match"`  // exact JID or "*" (catch-all)
	Mode   string `json:"mode"`   // auto | dedicated
	Plugin string `json:"plugin"` // AI bridge plugin: clevercoder | mcp-agent | direct-api | none
	Model  string `json:"model"`  // model for this chat
	VIP    bool   `json:"vip"`    // treat this contact as VIP (triggers vip mood on message)
	// TerminalID identifies which agent terminal this route dispatches to
	// over cAPI. Empty means the caller falls back to the route's port (see
	// AGENT-BEHAVIOR.md: "identidad del agente: terminal_id por ruta;
	// puerto como fallback si la ruta no lo define").
	TerminalID string `json:"terminal_id,omitempty"`
}

// normalizeMode maps the legacy mode value 'advanced' to its current name
// 'dedicated'. Kept as a read-alias so a router.json the owner still
// hand-wrote with 'advanced' keeps working — nothing downstream ever sees
// the legacy value. Empty/other values pass through untouched.
func normalizeMode(m string) string {
	if m == "advanced" {
		return "dedicated"
	}
	return m
}

type Config struct {
	// Whitelist no longer gates anything (T64, ct-2026-08-11-1627 — the
	// whitelist gate was removed in T65). Its one live use left is IsVIP:
	// an entry here still marks that JID a VIP for the tray icon's mood on
	// message. Field/JSON key kept as-is, not renamed, so existing
	// router.json files don't lose the owner's current VIPs.
	Whitelist   []string `json:"whitelist"`
	DefaultMode string   `json:"default_mode"` // default mode (default: dedicated)
	Routes      []Route  `json:"routes"`
}

// Load reads router.json. If it does not exist, it returns a safe default
// (dedicated mode).
func Load(path string) Config {
	c := Config{DefaultMode: "dedicated"}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &c)
	}
	if c.DefaultMode == "" {
		c.DefaultMode = "dedicated"
	}
	// Read-alias: a hand-written router.json may still carry the pre-rename
	// value 'advanced'. Normalize DefaultMode and every route so nothing
	// downstream sees the legacy name (the DB rows are migrated separately
	// on the store side).
	c.DefaultMode = normalizeMode(c.DefaultMode)
	for i := range c.Routes {
		c.Routes[i].Mode = normalizeMode(c.Routes[i].Mode)
	}
	return c
}

type Decision struct {
	Mode       string
	Plugin     string
	Model      string
	TerminalID string
}

// Resolve decides what to do with a chat. An exact Match wins over "*".
func (c Config) Resolve(jid string) Decision {
	d := Decision{Mode: c.DefaultMode}
	for _, r := range c.Routes {
		if r.Match == "*" || r.Match == jid {
			if r.Mode != "" {
				d.Mode = r.Mode
			}
			if r.Plugin != "" {
				d.Plugin = r.Plugin
			}
			if r.Model != "" {
				d.Model = r.Model
			}
			if r.TerminalID != "" {
				d.TerminalID = r.TerminalID
			}
			if r.Match == jid {
				break // exact match wins, stop before the catch-all
			}
		}
	}
	return d
}

// IsVIP returns true if the JID is a VIP contact: either it appears in the
// whitelist (exact match) or it has a route entry with vip:true. The wildcard
// catch-all route ("*") with vip:true makes everyone a VIP.
func (c Config) IsVIP(jid string) bool {
	for _, w := range c.Whitelist {
		if w == jid {
			return true
		}
	}
	for _, r := range c.Routes {
		if r.VIP && (r.Match == jid || r.Match == "*") {
			return true
		}
	}
	return false
}

// ── Manager ───────────────────────────────────────────────────────────────

// Manager wraps Config under a sync.RWMutex for safe concurrent access and
// supports runtime mutation with automatic persistence to disk. The pipeline
// and REST API callers share the same Manager instance so route/VIP changes
// take effect immediately, without a restart.
type Manager struct {
	mu   sync.RWMutex
	cfg  Config
	path string
}

// NewManager loads the config from path via Load and returns a Manager ready
// for concurrent use.
func NewManager(path string) *Manager {
	return &Manager{cfg: Load(path), path: path}
}

// Resolve decides what to do with a chat. Safe for concurrent read callers.
func (m *Manager) Resolve(jid string) Decision {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.Resolve(jid)
}

// IsVIP returns true if the JID is a VIP contact. Safe for concurrent read callers.
func (m *Manager) IsVIP(jid string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.IsVIP(jid)
}

// Snapshot returns a shallow copy of the current Config. Safe for concurrent callers.
func (m *Manager) Snapshot() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// Update calls fn under the write lock to mutate the Config, then persists it
// to router.json on disk. Changes (routes, whitelist/VIP membership, default
// mode) take effect for all messages resolved after Update returns.
// fn must not call any Manager method — it runs inside the lock.
func (m *Manager) Update(fn func(*Config)) error {
	m.mu.Lock()
	fn(&m.cfg)
	data, err := json.MarshalIndent(m.cfg, "", "  ")
	m.mu.Unlock()
	if err != nil {
		return err
	}
	// MkdirAll first (T169, ct-2026-09-19-1433): m.path now usually resolves
	// under config.DataDir(), which may not exist yet the first time an
	// operator ever changes a route — same pattern every other writer in
	// this codebase already applies to its own directory.
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(m.path, data, 0o644)
}
