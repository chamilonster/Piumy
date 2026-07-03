// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
//
// Package router: whitelist + per-number rules. For a given chat it resolves
// whether it is allowed and which mode / bridge plugin / model applies.
// Loaded from router.json (zero hardcode). Anti-ban: default whitelist-only.
package router

import (
	"encoding/json"
	"os"
	"sync"
)

type Route struct {
	Match  string `json:"match"`  // exact JID or "*" (catch-all)
	Mode   string `json:"mode"`   // auto | advanced
	Plugin string `json:"plugin"` // AI bridge plugin: mcp-agent | direct-api | none
	Model  string `json:"model"`  // model for this chat
	VIP    bool   `json:"vip"`    // treat this contact as VIP (triggers vip mood on message)
}

type Config struct {
	Whitelist   []string `json:"whitelist"`    // allowed JIDs
	AllowAll    bool     `json:"allow_all"`    // if true, answer anyone (ban risk)
	DefaultMode string   `json:"default_mode"` // default mode (default: advanced)
	Routes      []Route  `json:"routes"`
}

// Load reads router.json. If it does not exist, it returns a safe default
// (whitelist-only, advanced mode).
func Load(path string) Config {
	c := Config{DefaultMode: "advanced"}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &c)
	}
	if c.DefaultMode == "" {
		c.DefaultMode = "advanced"
	}
	return c
}

type Decision struct {
	Allowed bool
	Mode    string
	Plugin  string
	Model   string
}

// Resolve decides what to do with a chat. An exact Match wins over "*".
func (c Config) Resolve(jid string) Decision {
	allowed := c.AllowAll
	for _, w := range c.Whitelist {
		if w == jid {
			allowed = true
			break
		}
	}
	d := Decision{Allowed: allowed, Mode: c.DefaultMode}
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

// ── Manager ───────────────────────────────────────────────────────────────────

// Manager wraps Config under a sync.RWMutex for safe concurrent access and
// supports runtime mutation with automatic persistence to disk. Gateway and
// REST API callers share the same Manager instance so whitelist changes take
// effect immediately for new inbound messages without a restart.
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
// to router.json on disk. Changes to Whitelist / AllowAll take effect for all
// inbound messages processed after Update returns.
// fn must not call any Manager method — it runs inside the lock.
func (m *Manager) Update(fn func(*Config)) error {
	m.mu.Lock()
	fn(&m.cfg)
	data, err := json.MarshalIndent(m.cfg, "", "  ")
	m.mu.Unlock()
	if err != nil {
		return err
	}
	return os.WriteFile(m.path, data, 0o644)
}
