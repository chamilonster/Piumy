// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
//
// Package config: core configuration via environment variables (zero hardcode).
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Name       string // display name (e-paper / dashboard)
	StatusPath string // status.json path (contract with the display)
	DBPath     string // switchboard SQLite database
	RouterPath string // router.json (whitelist + routes)
	MCPAddr    string // MCP server address (streamable HTTP)
	APIAddr    string // REST API address (quick access: curl/scripts/dashboard)
	APIKey     string // REST API key (X-API-Key header or ?key=); empty = open (dev only)
	MCPKey     string // MCP bearer token (Authorization: Bearer <token>); empty = open (dev only), own key, separate from APIKey (0133)
	// Gateway selects the WhatsApp backend: "whatsmeow" | "none" (default).
	// "none" means no WhatsApp connection — safe for dev/off-Pi builds.
	Gateway   string // env PIMYWA_GATEWAY
	SessionDB string // path to the WhatsApp session SQLite file; env PIMYWA_SESSION_DB

	// DeviceName is the label shown in WhatsApp > Linked devices. MUST NOT reveal
	// the underlying library ("whatsmeow") — that fingerprints an unofficial
	// client and invites a ban. Use a normal-looking name. env PIMYWA_WA_DEVICE,
	// default "Piumy" (set to e.g. "Chrome (Windows)" for more camouflage).
	// This default only affects a FUTURE pairing — an already-linked
	// device's name was committed to WhatsApp at ITS pairing time and is
	// untouched by this default changing (see gateway.go).
	DeviceName string

	// Mood thresholds.
	// SwampedAt is the queue depth at which the resting mood switches from "few"
	// to "swamped". Values 1..SwampedAt → "few"; >SwampedAt → "swamped".
	// env PIMYWA_SWAMPED_AT, default 8.
	SwampedAt int

	// AgentIdle is how long the MCP server waits with no tool calls before it
	// clears AgentConnected. env PIMYWA_AGENT_IDLE, default 120s.
	AgentIdle time.Duration

	// QRTimeout is how long the gateway exposes a QR code before auto-stopping.
	// The cap is armed only when the device is not yet linked (no stored session).
	// 0 = no cap (infinite exposure). env PIMYWA_QR_TIMEOUT, default 180s.
	QRTimeout time.Duration

	// DispatchDelayMin / DispatchDelayMax bound the randomized human-pacing
	// delay the gateway waits before each outbound WhatsApp send (anti-ban:
	// never instant). env PIMYWA_DELAY_DISPATCH_MIN / PIMYWA_DELAY_DISPATCH_MAX,
	// default 1s / 5s — same as the gateway's built-in fallback. A non-positive
	// or missing value falls back to the default (envDuration never returns 0).
	DispatchDelayMin time.Duration
	DispatchDelayMax time.Duration

	// ReadDelayMin / ReadDelayMax bound the randomized delay before the
	// gateway marks an inbound message as read (WhatsApp read receipt) — must
	// be slow, even reads, so the account doesn't look like a scraper.
	// env PIMYWA_DELAY_READ_MIN / PIMYWA_DELAY_READ_MAX, default 2s / 8s.
	ReadDelayMin time.Duration
	ReadDelayMax time.Duration

	// ActionDelayMin / ActionDelayMax bound the randomized delay before any
	// other WhatsApp-server-facing action (contact/group sync fetches, etc.).
	// env PIMYWA_DELAY_ACTION_MIN / PIMYWA_DELAY_ACTION_MAX, default 1s / 4s.
	ActionDelayMin time.Duration
	ActionDelayMax time.Duration

	// MediaDir is where downloaded images/videos/stickers are written to disk
	// (never SQLite). env PIMYWA_MEDIA_DIR, default /opt/pimywa/data/media.
	MediaDir string

	// MediaMaxMB is the total size cap (megabytes) for the media directory —
	// the GC policy is size-only, oldest files deleted first; text/metadata in
	// the messages table is never touched. env PIMYWA_MEDIA_MAX_MB, default 512.
	MediaMaxMB int

	// OutboxMaxRetry is how many failed send attempts an outbox item gets
	// before it's dead-lettered (anti-ban: never resend in a loop forever).
	// env PIMYWA_OUTBOX_MAX_RETRY, default 5.
	OutboxMaxRetry int

	// RateLimitPerMin / RateLimitPerDay are the governor's send caps (0753 —
	// dashboard-editable at runtime via KV-override; these are only the
	// startup defaults, previously hardcoded straight into main.go).
	// env PIMYWA_RATE_LIMIT_PER_MIN, default 10 (preserves the prior
	// hardcoded behavior). env PIMYWA_RATE_LIMIT_PER_DAY, default 500 — a
	// new safety net, nothing enforced this before.
	RateLimitPerMin int
	RateLimitPerDay int

	// MCP anti-flood — a limiter DISTINCT from RateLimitPerMin/Day above:
	// those pace WhatsApp-outbound sends, these pace MCP-inbound tool calls
	// from a connected agent, so a "dumb" or buggy AI hammering the MCP
	// server can't overwhelm it. Per-client (MCP session ID),
	// dashboard-editable via KV-override same as everything else here.
	// env PIMYWA_MCPGUARD_RATE_PER_MIN, default 120 (general tool calls).
	MCPGuardRatePerMin int
	// env PIMYWA_MCPGUARD_EMIT_RATE_PER_MIN, default 20 — stricter, only for
	// send_message/escalate (the tools that cause real-world side effects).
	MCPGuardEmitRatePerMin int
	// env PIMYWA_MCPGUARD_BLOCK_THRESHOLD, default 5 — consecutive throttled
	// calls that trip the circuit breaker (full, temporary block).
	MCPGuardBlockThreshold int
	// env PIMYWA_MCPGUARD_BLOCK_COOLDOWN, default 5m — how long a tripped
	// client is fully blocked.
	MCPGuardBlockCooldown time.Duration

	// ClaimTTLDefault is claim_chat's default lock duration when the caller
	// omits ttl_sec. env PIMYWA_CLAIM_TTL_DEFAULT, default 5m. Env-only,
	// deliberately no KV-override/dashboard knob yet — a single agent has
	// nothing to tune here (YAGNI); the hard ceiling (30m,
	// mcpserver.claimTTLCeiling) is a Go const, not configurable at all,
	// for the same reason.
	ClaimTTLDefault time.Duration

	// BatteryFile is the power adapter's battery.json sidecar (single-writer
	// fix) — the core is the ONLY writer of status.json, so the power
	// adapter writes its reading here instead and the core merges it in on
	// its own heartbeat.
	// env PIMYWA_BATTERY_FILE, default /run/pimywa/battery.json (tmpfs,
	// same volume as status.json — 3rd commandment, zero SD wear).
	BatteryFile string
	// BatteryMaxAge is how old a battery.json reading can be before the
	// core treats it as "no reading" (power adapter stopped, or backend
	// none) rather than showing a stale value forever.
	// env PIMYWA_BATTERY_MAX_AGE, default 120s (4x the power adapter's own
	// 30s default poll interval — tolerates a missed poll or two without
	// flickering the battery icon on/off).
	BatteryMaxAge time.Duration
	// StatusHeartbeat is how often the core re-writes status.json even with
	// no event (merges in a fresh battery.json reading, keeps updated_at
	// from going stale during quiet periods). Cheap: status.json lives in
	// tmpfs (RAM), so this costs zero SD wear regardless of interval.
	// env PIMYWA_STATUS_HEARTBEAT, default 15s.
	StatusHeartbeat time.Duration

	// BatteryLogFile is the discharge/charge trace CSV the power adapter
	// appends to (adapters/power/timeremain.py owns the write side). SAME
	// env var name and default as the Python
	// side on purpose: one value in the deploy's env file configures both
	// processes consistently. The core only READS this file (GET
	// /api/battery/log) — it never writes it, same single-writer discipline
	// as battery.json. env PIMYWA_BATTERY_LOG_FILE, default
	// /opt/pimywa/data/battery_log.csv (durable SD, survives restarts).
	BatteryLogFile string

	// FaceFile is the display adapter's face.json sidecar — same
	// single-writer pattern as BatteryFile: the display
	// service writes the live kaomoji face here, the core merges it into
	// status.json on its own heartbeat. env PIMYWA_FACE_FILE, default
	// /run/pimywa/face.json (tmpfs, same volume as status.json/battery.json).
	FaceFile string
	// FaceMaxAge is how old a face.json reading can be before the core
	// treats it as "no reading" (display adapter stopped) rather than
	// showing a stale face forever. env PIMYWA_FACE_MAX_AGE, default 120s
	// (same as BatteryMaxAge).
	FaceMaxAge time.Duration

	// BackupKey is the passphrase that derives the session backup's
	// encryption key (via scrypt). Empty = backups DISABLED — the session
	// (WhatsApp credentials) is never written to disk unencrypted, full
	// stop. env PIMYWA_BACKUP_KEY, default "" (off).
	BackupKey string
	// BackupDir is where encrypted session backups are written. env
	// PIMYWA_BACKUP_DIR, default /opt/pimywa/data/backups. NOTE: this
	// default lives on the SAME SD card as the session — protects against
	// DB corruption, NOT against SD death. Real disaster recovery needs an
	// off-SD path, wired at deploy time (out of scope here).
	BackupDir string
	// BackupKeep is how many of the newest backups to retain — older ones
	// are deleted on rotation. env PIMYWA_BACKUP_KEEP, default 5.
	BackupKeep int
	// BackupInterval is how often the periodic backup ticker runs, in
	// addition to the "backup now" REST trigger and the post-link hook.
	// env PIMYWA_BACKUP_INTERVAL, default 24h.
	BackupInterval time.Duration

	// DecisionPolicyPath is the editable decision-policy file (the owner edits
	// it live, no recompile). If it doesn't exist, mcpserver falls back to
	// its embedded default. env PIMYWA_DECISION_POLICY,
	// default /opt/pimywa/decision-policy.md.
	DecisionPolicyPath string

	// Bridge selects the AI bridge plugin for the auto-reply worker (gap #1):
	// "direct-api" (DeepSeek) | "none" (default — auto mode drafts nothing
	// until a plugin is chosen). env PIMYWA_BRIDGE.
	Bridge string

	// DeepSeekKey is the DeepSeek API key. Secret — env only, never
	// hardcoded, never logged, never in the agent's MCP context.
	// env PIMYWA_DEEPSEEK_KEY.
	DeepSeekKey string

	// DeepSeekEndpoint overrides the DeepSeek API base URL.
	// env PIMYWA_DEEPSEEK_ENDPOINT, default https://api.deepseek.com.
	DeepSeekEndpoint string

	// DeepSeekModel is the DeepSeek model name.
	// env PIMYWA_DEEPSEEK_MODEL, default "deepseek-chat".
	DeepSeekModel string

	// BridgeBudget is the hard cap on bridge API calls — once reached, the
	// bridge stops calling the API entirely (anti-runaway-cost, not a soft
	// warning). env PIMYWA_BRIDGE_BUDGET, default 100.
	BridgeBudget int

	// AutoReplyInterval is how often the auto-reply worker sweeps pending
	// auto-mode chats. env PIMYWA_AUTOREPLY_INTERVAL, default 5m.
	AutoReplyInterval time.Duration

	// AutoReplyDelay paces successive Bridge.Draft calls within a sweep —
	// courtesy pacing against a paid API, not a WhatsApp anti-ban delay.
	// env PIMYWA_AUTOREPLY_DELAY, default 3s.
	AutoReplyDelay time.Duration

	// Hostname is an override for the mDNS/footer hostname shown in status.json.
	// When empty the OS hostname + ".local" is used.
	// env PIMYWA_HOSTNAME.
	Hostname string

	// WifiIface is the network interface queried for the current SSID (`iw
	// dev <iface> link`) — board/dongle-specific, never hardcoded.
	// env PIMYWA_WIFI_IFACE, default "wlan0".
	WifiIface string

	// Dashboard (lightweight LAN web UI served by the same binary).
	// DashEnabled: env PIMYWA_DASH; "on"/"yes"/"true"/"1" → enabled (default on).
	// DashAddr:     env PIMYWA_DASH_ADDR; default ":80".
	// DashUser:     env PIMYWA_DASH_USER; default "admin".
	// DashPass:     env PIMYWA_DASH_PASS (plaintext; hashed with bcrypt in memory at startup).
	// DashPassHash: env PIMYWA_DASH_PASS_HASH (pre-computed bcrypt hash; overrides DashPass).
	// If neither DashPass nor DashPassHash is set, a random password is generated at startup
	// and logged once via log.Printf (visible in journalctl -u pimywa-core).
	DashEnabled  bool
	DashAddr     string
	DashUser     string
	DashPass     string
	DashPassHash string
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func envBool(k string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(k)))
	if v == "" {
		return def
	}
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func envDuration(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}

func Load() Config {
	return Config{
		Name:       env("PIMYWA_NAME", "Piumy"),
		StatusPath: env("PIMYWA_STATUS", "status.json"),
		DBPath:     env("PIMYWA_DB", "pimywa.db"),
		RouterPath: env("PIMYWA_ROUTER", "router.json"),
		MCPAddr:    env("PIMYWA_MCP_ADDR", ":8081"),
		APIAddr:    env("PIMYWA_API_ADDR", ":8080"),
		APIKey:     env("PIMYWA_API_KEY", ""),
		MCPKey:     env("PIMYWA_MCP_KEY", ""),
		Gateway:    env("PIMYWA_GATEWAY", "none"),
		SessionDB:  env("PIMYWA_SESSION_DB", "/opt/pimywa/data/wa.db"),
		DeviceName: env("PIMYWA_WA_DEVICE", "Piumy"),
		SwampedAt:  envInt("PIMYWA_SWAMPED_AT", 8),
		AgentIdle:  envDuration("PIMYWA_AGENT_IDLE", 120*time.Second),
		QRTimeout:  envDuration("PIMYWA_QR_TIMEOUT", 180*time.Second),
		Hostname:   env("PIMYWA_HOSTNAME", ""),
		WifiIface:  env("PIMYWA_WIFI_IFACE", "wlan0"),

		DispatchDelayMin: envDuration("PIMYWA_DELAY_DISPATCH_MIN", 1*time.Second),
		DispatchDelayMax: envDuration("PIMYWA_DELAY_DISPATCH_MAX", 5*time.Second),
		ReadDelayMin:     envDuration("PIMYWA_DELAY_READ_MIN", 2*time.Second),
		ReadDelayMax:     envDuration("PIMYWA_DELAY_READ_MAX", 8*time.Second),
		ActionDelayMin:   envDuration("PIMYWA_DELAY_ACTION_MIN", 1*time.Second),
		ActionDelayMax:   envDuration("PIMYWA_DELAY_ACTION_MAX", 4*time.Second),

		MediaDir:       env("PIMYWA_MEDIA_DIR", "/opt/pimywa/data/media"),
		MediaMaxMB:     envInt("PIMYWA_MEDIA_MAX_MB", 512),
		OutboxMaxRetry: envInt("PIMYWA_OUTBOX_MAX_RETRY", 5),

		RateLimitPerMin: envInt("PIMYWA_RATE_LIMIT_PER_MIN", 10),
		RateLimitPerDay: envInt("PIMYWA_RATE_LIMIT_PER_DAY", 500),

		MCPGuardRatePerMin:     envInt("PIMYWA_MCPGUARD_RATE_PER_MIN", 120),
		MCPGuardEmitRatePerMin: envInt("PIMYWA_MCPGUARD_EMIT_RATE_PER_MIN", 20),
		MCPGuardBlockThreshold: envInt("PIMYWA_MCPGUARD_BLOCK_THRESHOLD", 5),
		MCPGuardBlockCooldown:  envDuration("PIMYWA_MCPGUARD_BLOCK_COOLDOWN", 5*time.Minute),

		ClaimTTLDefault: envDuration("PIMYWA_CLAIM_TTL_DEFAULT", 5*time.Minute),

		BatteryFile:     env("PIMYWA_BATTERY_FILE", "/run/pimywa/battery.json"),
		BatteryMaxAge:   envDuration("PIMYWA_BATTERY_MAX_AGE", 120*time.Second),
		StatusHeartbeat: envDuration("PIMYWA_STATUS_HEARTBEAT", 15*time.Second),
		BatteryLogFile:  env("PIMYWA_BATTERY_LOG_FILE", "/opt/pimywa/data/battery_log.csv"),
		FaceFile:        env("PIMYWA_FACE_FILE", "/run/pimywa/face.json"),
		FaceMaxAge:      envDuration("PIMYWA_FACE_MAX_AGE", 120*time.Second),

		BackupKey:      env("PIMYWA_BACKUP_KEY", ""),
		BackupDir:      env("PIMYWA_BACKUP_DIR", "/opt/pimywa/data/backups"),
		BackupKeep:     envInt("PIMYWA_BACKUP_KEEP", 5),
		BackupInterval: envDuration("PIMYWA_BACKUP_INTERVAL", 24*time.Hour),

		DecisionPolicyPath: env("PIMYWA_DECISION_POLICY", "/opt/pimywa/decision-policy.md"),

		Bridge:            env("PIMYWA_BRIDGE", "none"),
		DeepSeekKey:       env("PIMYWA_DEEPSEEK_KEY", ""),
		DeepSeekEndpoint:  env("PIMYWA_DEEPSEEK_ENDPOINT", "https://api.deepseek.com"),
		DeepSeekModel:     env("PIMYWA_DEEPSEEK_MODEL", "deepseek-chat"),
		BridgeBudget:      envInt("PIMYWA_BRIDGE_BUDGET", 100),
		AutoReplyInterval: envDuration("PIMYWA_AUTOREPLY_INTERVAL", 5*time.Minute),
		AutoReplyDelay:    envDuration("PIMYWA_AUTOREPLY_DELAY", 3*time.Second),

		DashEnabled:  envBool("PIMYWA_DASH", true),
		DashAddr:     env("PIMYWA_DASH_ADDR", ":80"),
		DashUser:     env("PIMYWA_DASH_USER", "admin"),
		DashPass:     env("PIMYWA_DASH_PASS", ""),
		DashPassHash: env("PIMYWA_DASH_PASS_HASH", ""),
	}
}
