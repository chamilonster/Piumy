// Package config carga la configuración de piumy-gateway desde el entorno
// (PIUMY_*) — cero hardcode, cero valores de medio camino. Load lee
// SIEMPRE el entorno, sin excepción; ApplyFileDefaults (T11,
// ct-2026-08-05-1214, filedefaults.go) es un paso ANTERIOR y opcional que
// rellena el entorno desde piumy-config.json (o lo migra de un
// run-piumy.bat viejo) para lo que no esté ya seteado — la variable de
// entorno sigue ganando siempre, Load no sabe ni le importa de dónde salió.
//
// Extendido en F1b con lo que router/governor/state/netinfo necesitan para
// construirse — ver docs/F1B-INFRA-ROUTING.md para qué NO se portó de
// Piumy (whatsmeow, dashboard, adaptador de display/batería) y por qué.
// Extendido de nuevo en F1c con mcpguard/sessionbackup/bridge/autoreply —
// ver docs/F1C-GUARD-BACKUP-BRIDGE-AUTOREPLY.md (incluye la reinterpretación
// de sessionbackup: respalda el store.db propio, no una sesión whatsmeow).
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// Account is PIUMY_ACCOUNT, already trimmed (S1/S2, ct-2026-09-20-1100/
	// 1134) — empty means the default, single-account install (today's only
	// case before S1). This is the ONE place that reads the raw env var for
	// display/identity purposes; DataDir() (datadir.go) and the port
	// defaults below also read PIUMY_ACCOUNT directly, each for its OWN
	// reason (folder layout, port collision) — a third consumer (the tray,
	// S2) reads it from HERE instead, cableado desde la fuente, not a
	// fourth raw os.Getenv.
	Account string

	// DBPath is piumy-gateway's own store.db (chats/messages/rules, F1a) —
	// the conversation history itself. Defaults under DataDir()/secrets
	// (T169, ct-2026-09-19-1433) — no longer PIUMY_DB_PATH-required with no
	// default at all, since that "required" error was the ONE data path
	// that never leaked into the working directory by accident; now none of
	// them do, by construction, so the special case is gone.
	DBPath string
	MCPKey string

	// DefaultTerminalID is the terminal identity capipush falls back to
	// when a chat's route defines none (AGENT-BEHAVIOR.md: "puerto como
	// fallback si la ruta no lo define") — renamed from OpenWAPort (ST-E,
	// ct-2026-07-11-1444): the old name was misleading, this was never an
	// open-wa-specific concept, just reused its env var.
	DefaultTerminalID string

	// CleverAPIEndpoint/TerminalID/Pinpass wire capipush.CleverInjector — the
	// real transport into a CleverCoder terminal via its external-agent-api
	// (ct-2026-07-10-2307). All three empty means CleverInjector isn't
	// selected (main.go falls back to LogInjector). Single-terminal MVP: one
	// set of creds for every dispatch, whatever the chat's route says.
	CleverAPIEndpoint   string
	CleverAPITerminalID string
	CleverAPIPinpass    string

	// WADBPath is whatsmeow's own session store (device identity/crypto
	// keys/pairing state) — separate from DBPath (piumy-gateway's own
	// chats/messages/rules store, F1a). F5.x connector rewrite
	// (ct-2026-07-10-0420): whatsmeow (Go, CGO_ENABLED=0), the sole
	// messaging client (open-wa was deleted, ST-E, ct-2026-07-11-1444) —
	// see internal/whatsmeow. Defaults under DataDir()/secrets, T169.
	WADBPath string
	// WADeviceName is what shows up in WhatsApp's own "Linked Devices" list
	// (store.DeviceProps.Os in internal/whatsmeow) — anti-ban knob (boss
	// catch, 2026-07-10): the library's own default ("whatsmeow") outs the
	// client as unofficial. Configurable, never hardcoded beyond the
	// default in internal/whatsmeow itself.
	WADeviceName string

	// RouterPath is router.json's location (whitelist + routes). Defaults
	// under DataDir()/secrets, T169 (ct-2026-09-19-1433).
	RouterPath string

	// RateLimitPerMin / RateLimitPerDay are the governor's send caps —
	// startup defaults (dashboard-editable at runtime via KV-override once
	// restapi/dashboard land in F4).
	RateLimitPerMin int
	RateLimitPerDay int

	// DispatchDelayMin/Max, ReadDelayMin/Max, ActionDelayMin/Max bound the
	// governor's randomized human-pacing delays (anti-ban: never instant).
	DispatchDelayMin time.Duration
	DispatchDelayMax time.Duration
	ReadDelayMin     time.Duration
	ReadDelayMax     time.Duration
	ActionDelayMin   time.Duration
	ActionDelayMax   time.Duration

	// ChunkMaxLen / ChunkDelayMin/Max (T101, ct-2026-08-29-1651) bound how a
	// long outbound reply gets split into WhatsApp-sized pieces, and the
	// randomized respiro BETWEEN pieces of the SAME message. Boss verbatim:
	// "partirla en pedazos pero que no se envien como metralla, pequeño
	// delay". ChunkDelayMin/Max is deliberately its OWN, shorter window than
	// DispatchDelayMin/Max above — that one paces between DIFFERENT
	// messages, this one paces between parts of the same one. ChunkMaxLen's
	// default (4000) is OpenClaw's reference value, not a dogma.
	ChunkMaxLen   int
	ChunkDelayMin time.Duration
	ChunkDelayMax time.Duration

	// AvatarRecheckMin/Max bound the randomized per-jid window before a
	// cached profile picture is worth asking WhatsApp about again (T17
	// Parte 3, ct-2026-08-05-1240) — days-scale, unlike the second-scale
	// delays above.
	AvatarRecheckMin time.Duration
	AvatarRecheckMax time.Duration

	// ReconnectBaseDelay/MaxDelay/StableAfter (T99, ct-2026-08-29-1607) —
	// the WhatsApp reconnect breaker's own knobs. Base/MaxDelay bound the
	// exponential backoff between reconnect attempts (jitter ±20% is an
	// internal constant, not configurable — Citrino: "es cómo randomizar,
	// no una perilla de producto"). MaxDelay is 5 MINUTES by design, not
	// the outbox's 1 hour: this breaker never gives up like the old Piumy
	// one did (boss: "la reconexion debe ser automatica, no martillante
	// constante"), so the ceiling is the permanent worst-case wait to
	// notice the network came back, not "how long before giving up" — an
	// hour of that would itself read as dead, not automatic. StableAfter
	// is how long a connection must survive before ReconnectBaseDelay's
	// escalation resets — see internal/whatsmeow/reconnect.go's own doc for
	// why a reset-on-first-handshake (whatsmeow's own default behavior) is
	// exactly the bug this exists to fix.
	ReconnectBaseDelay   time.Duration
	ReconnectMaxDelay    time.Duration
	ReconnectStableAfter time.Duration

	// StatusPath / SwampedAt configure state.NewManager (status.json mood
	// contract). SwampedAt is the queue depth at which the resting mood
	// switches from "few" to "swamped". StatusPath defaults under
	// DataDir()/secrets (T169) — main.go derives agent-connect.json's
	// directory and logs/'s location from THIS path, not a separate
	// hardcoded one, so both follow automatically.
	StatusPath string
	SwampedAt  int

	// Hostname overrides the OS hostname netinfo.Gather reports; WifiIface
	// names the interface it queries for SSID. Both empty is a valid
	// default (netinfo degrades gracefully).
	Hostname  string
	WifiIface string

	// MCPGuardRatePerMin/EmitRatePerMin/BlockThreshold/BlockCooldown
	// configure mcpguard.Guard (MCP-inbound anti-flood — distinct from
	// RateLimitPerMin/Day above, which pace the outbound send governor).
	MCPGuardRatePerMin     int
	MCPGuardEmitRatePerMin int
	MCPGuardBlockThreshold int
	MCPGuardBlockCooldown  time.Duration

	// BackupKey/Dir/Keep/Interval configure sessionbackup.Backuper. Empty
	// BackupKey disables backups (fail-safe-off). SessionDBPath isn't a
	// separate field here — it's cfg.DBPath (piumy-gateway's own store.db,
	// see the package doc comment). BackupDir defaults under
	// DataDir()/secrets, T169 — encrypted, but still not code.
	BackupKey      string
	BackupDir      string
	BackupKeep     int
	BackupInterval time.Duration

	// BridgePlugin selects the auto-reply AI bridge ("direct-api" | "none").
	// DeepSeek* configure the direct-api plugin; BridgeBudget is its hard
	// per-process call cap (anti-runaway-cost).
	BridgePlugin     string
	DeepSeekKey      string
	DeepSeekEndpoint string
	DeepSeekModel    string
	BridgeBudget     int

	// AutoReplyInterval/Delay configure autoreply.Worker: how often it
	// sweeps pending auto-mode chats, and how it paces successive
	// Bridge.Draft calls within a sweep (courtesy pacing against a paid API).
	AutoReplyInterval time.Duration
	AutoReplyDelay    time.Duration

	// MediaDir is where incoming media will be saved (original + low-q
	// JPEG), F4-DESIGN §6 — kept for when media inbound is picked back up
	// (deferred, ct-2026-07-11-1433; the previous consumer, internal/openwa's
	// handleMedia, was deleted with that package, ST-E ct-2026-07-11-1444).
	// Defaults under DataDir()/secrets, T169 — real conversation photos,
	// same sensitivity class as the DB itself.
	MediaDir string
	// MediaLowQJPEGQuality is the low-q JPEG's quality (0-100, jpeg.Options)
	// — the central quality/tokens knob of §6's incentive (boss verbatim:
	// "las imágenes las quiero comprimidas en baja calidad"). Found
	// hardcoded in the F4d audit; every other F4d knob already lived here.
	MediaLowQJPEGQuality int

	// Metering (F4-DESIGN §8) — calibration knobs for the usage estimate:
	// est ≈ out_chars/4·OutCharWeight + in_chars/4·InCharWeight +
	// img·ImageCost + audio·AudioCost + msg·MessageCost. Blended 70/30 with
	// real reported tokens when CleverCoder's seam has reported any (store.BlendUsage).
	MeteringOutCharWeight float64
	MeteringInCharWeight  float64
	MeteringImageCost     float64
	MeteringAudioCost     float64
	MeteringMessageCost   float64
	// MeteringDailyQuota is the GLOBAL (all chats) daily usage ceiling
	// capipush checks before dispatching — single-account for now (post-MVP:
	// per-account). Zero/negative disables the quota check entirely.
	MeteringDailyQuota float64

	// MCPAddr/RESTAddr are where main.go's two HTTP servers listen (F5).
	// RESTKey empty = open (dev/LAN only), same fail-OPEN-when-unset
	// convention as the rest of restapi — unlike MCPKey, which is
	// fail-closed (mcpserver.RequireBearerToken).
	MCPAddr  string
	RESTAddr string
	RESTKey  string
	// PolicyPath is the editable decision-policy file both mcpserver and
	// autoreply read from — empty falls back to each package's own
	// embedded default.
	PolicyPath string

	// MaxRedispatch / GateStaleAfter: post-incident containment knobs
	// (ct-2026-07-11-074123) — capipush.Config.MaxRedispatch caps how many
	// times one still-unhandled message gets auto-re-dispatched;
	// GateStaleAfter is how long mcpserver.Gate lets a bound-but-stuck
	// dispatch sit before reclaiming it. Both were hardcoded/unbounded
	// before; an agent bug that never calls mark_handled used to flood
	// indefinitely, and a crashed agent could wedge a terminal for up to
	// an hour with nothing logged.
	//
	// S4b (ct-2026-07-30-1255) recalibrated both defaults together (the
	// "tres relojes" — sweep interval, redispatch cap, stale reclaim — have
	// to make sense next to each other): MaxRedispatch 3→7 (Citrino's
	// 6-step Fibonacci backoff table needs 7 attempts to use all 6 gaps end
	// to end — capipush.redispatchBackoff); GateStaleAfter 1h→15m (an hour
	// was "last-resort net", not "recovers fast" — too long for messaging).
	//
	// T105 (ct-2026-08-29-2129) took GateStaleAfter further, 15m→5m — the
	// boss's own call on the product, not a one-machine tweak ("20 minutos
	// no es ni cañón"): 15m was still a whole terminal blocked for as long
	// as an agent that got stuck. Still env-overridable, and still
	// live-settings-overridable (see below) — this only changes what a
	// clean install starts with.
	//
	// Both are ALSO live-settings-overridable now (store.
	// SettingCapipushMaxRedispatch/SettingCapipushDispatchStaleAfter) — these
	// env vars are only the code-level fallback.
	MaxRedispatch  int
	GateStaleAfter time.Duration

	// DispatchDebounce / MaxDispatchDebounce (ct-2026-07-13-2243): capipush
	// waits for DispatchDebounce silence before dispatching a chat's burst —
	// classic debounce, resets with each new message. Anti-ban jitter (±25%)
	// is added per-sweep (boss verbatim: "ventanas de tiempo variables").
	// MaxDispatchDebounce is the hard ceiling — oldest message older than
	// this → dispatch immediately regardless of recent activity.
	DispatchDebounce    time.Duration
	MaxDispatchDebounce time.Duration

	// SMTPHost/Port/User/Pass/From configure the email channel of password
	// recovery (S1e-2, ct-2026-07-19-1716) — the boss's own outbound mail
	// relay, sent via net/smtp (stdlib, STARTTLS on the default port 587;
	// implicit-TLS 465 relays aren't supported — net/smtp itself doesn't
	// speak that handshake). Empty SMTPHost means email recovery isn't
	// configured: POST /api/auth/recover{method:"email"} silently no-ops,
	// same generic response either way (no state leak, same rule S1e-1
	// already applies to a boss with no is_boss chat configured).
	SMTPHost string
	SMTPPort string
	SMTPUser string
	SMTPPass string
	SMTPFrom string
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// envPath resolves a data-file/dir env var (T169, ct-2026-09-19-1433): the
// raw value VERBATIM if set — never joined, never modified, so the boss's
// own installer-written paths keep working byte-for-byte — otherwise def
// joined onto secretsDir. Every *Path/*Dir field in Config that names an
// actual data file goes through this, never through env()'s bare relative
// default — that bare-relative shape is exactly the bug T169 exists to
// close (CLAUDE.md's own invariant now: no default relative path for a
// data file, ever again).
func envPath(k, secretsDir, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return filepath.Join(secretsDir, def)
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func envDuration(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}

func envFloat(k string, def float64) float64 {
	if v := os.Getenv(k); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

// Load reads every PIUMY_* var from the environment. Every data-file path
// (DBPath included, since T169 ct-2026-09-19-1433) defaults under
// DataDir()/secrets when its own env var isn't set — an explicit env var
// always wins verbatim (envPath), unchanged, so the boss's own installer-
// written paths keep working exactly as they do today. The rest of the
// config is only validated once the package that consumes it actually
// reads it.
func Load() (Config, error) {
	dataDir, err := DataDir()
	if err != nil {
		return Config{}, err
	}
	secretsDir := filepath.Join(dataDir, "secrets")

	// account: read ONCE here, trimmed — DataDir() (called just above) has
	// already validated it via accountSlug if it's non-empty, so trimming
	// again is safe and can't newly fail. Every other PIUMY_ACCOUNT
	// consumer in this package (DataDir/dataDirFor) reads the env var
	// directly for its OWN reason; this is the one read whose RESULT other
	// packages (the tray, S2) are meant to consume, via cfg.Account.
	account := strings.TrimSpace(os.Getenv("PIUMY_ACCOUNT"))

	// mcpAddrDefault/restAddrDefault: with PIUMY_ACCOUNT set, the fixed
	// :8091/:8092 default would make a second account's instance fail to
	// bind against the first's (S1, ct-2026-09-20-1100) — ":0" lets the OS
	// pick a free port instead. An explicit PIUMY_MCP_ADDR/PIUMY_REST_ADDR
	// still wins either way (env() below), so a pinned port keeps working
	// exactly like today for whoever sets one on purpose.
	mcpAddrDefault, restAddrDefault := ":8091", ":8092"
	if account != "" {
		mcpAddrDefault, restAddrDefault = ":0", ":0"
	}

	cfg := Config{
		Account:           account,
		DBPath:            envPath("PIUMY_DB_PATH", secretsDir, "piumy.db"),
		MCPKey:            os.Getenv("PIUMY_MCP_KEY"),
		DefaultTerminalID: os.Getenv("PIUMY_DEFAULT_TERMINAL_ID"),

		CleverAPIEndpoint:   os.Getenv("PIUMY_CAPI_ENDPOINT"),
		CleverAPITerminalID: os.Getenv("PIUMY_CAPI_TERMINAL_ID"),
		CleverAPIPinpass:    os.Getenv("PIUMY_CAPI_PINPASS"),

		WADBPath:     envPath("PIUMY_WA_DB_PATH", secretsDir, "whatsmeow.db"),
		WADeviceName: os.Getenv("PIUMY_WA_DEVICE_NAME"),

		RouterPath: envPath("PIUMY_ROUTER_PATH", secretsDir, "router.json"),

		RateLimitPerMin: envInt("PIUMY_RATE_LIMIT_PER_MIN", 10),
		RateLimitPerDay: envInt("PIUMY_RATE_LIMIT_PER_DAY", 500),

		DispatchDelayMin: envDuration("PIUMY_DELAY_DISPATCH_MIN", 1*time.Second),
		DispatchDelayMax: envDuration("PIUMY_DELAY_DISPATCH_MAX", 5*time.Second),
		ReadDelayMin:     envDuration("PIUMY_DELAY_READ_MIN", 2*time.Second),
		ReadDelayMax:     envDuration("PIUMY_DELAY_READ_MAX", 8*time.Second),
		ActionDelayMin:   envDuration("PIUMY_DELAY_ACTION_MIN", 1*time.Second),
		ActionDelayMax:   envDuration("PIUMY_DELAY_ACTION_MAX", 4*time.Second),

		ChunkMaxLen:   envInt("PIUMY_CHUNK_MAX_LEN", 4000),
		ChunkDelayMin: envDuration("PIUMY_DELAY_CHUNK_MIN", 400*time.Millisecond),
		ChunkDelayMax: envDuration("PIUMY_DELAY_CHUNK_MAX", 1500*time.Millisecond),

		// AvatarRecheckMin/Max: T17 Parte 3 (ct-2026-08-05-1240) — days-scale,
		// deliberately NOT a fixed interval (Citrino's correction on the
		// first draft: a flat "7 days" is a pattern; see
		// whatsmeow.avatarRecheckWindow's own doc for the full reasoning).
		AvatarRecheckMin: envDuration("PIUMY_AVATAR_RECHECK_MIN", 3*24*time.Hour),
		AvatarRecheckMax: envDuration("PIUMY_AVATAR_RECHECK_MAX", 9*24*time.Hour),

		// ReconnectBaseDelay/MaxDelay/StableAfter: T99 (ct-2026-08-29-1607).
		ReconnectBaseDelay:   envDuration("PIUMY_RECONNECT_BASE_DELAY", 5*time.Second),
		ReconnectMaxDelay:    envDuration("PIUMY_RECONNECT_MAX_DELAY", 5*time.Minute),
		ReconnectStableAfter: envDuration("PIUMY_RECONNECT_STABLE_AFTER", 60*time.Second),

		StatusPath: envPath("PIUMY_STATUS_PATH", secretsDir, "status.json"),
		SwampedAt:  envInt("PIUMY_SWAMPED_AT", 8),

		Hostname:  env("PIUMY_HOSTNAME", ""),
		WifiIface: env("PIUMY_WIFI_IFACE", "wlan0"),

		MCPGuardRatePerMin:     envInt("PIUMY_MCPGUARD_RATE_PER_MIN", 120),
		MCPGuardEmitRatePerMin: envInt("PIUMY_MCPGUARD_EMIT_RATE_PER_MIN", 20),
		MCPGuardBlockThreshold: envInt("PIUMY_MCPGUARD_BLOCK_THRESHOLD", 5),
		MCPGuardBlockCooldown:  envDuration("PIUMY_MCPGUARD_BLOCK_COOLDOWN", 5*time.Minute),

		BackupKey:      os.Getenv("PIUMY_BACKUP_KEY"),
		BackupDir:      envPath("PIUMY_BACKUP_DIR", secretsDir, "backups"),
		BackupKeep:     envInt("PIUMY_BACKUP_KEEP", 5),
		BackupInterval: envDuration("PIUMY_BACKUP_INTERVAL", 24*time.Hour),

		BridgePlugin:     env("PIUMY_BRIDGE", "none"),
		DeepSeekKey:      os.Getenv("PIUMY_DEEPSEEK_KEY"),
		DeepSeekEndpoint: env("PIUMY_DEEPSEEK_ENDPOINT", "https://api.deepseek.com"),
		DeepSeekModel:    env("PIUMY_DEEPSEEK_MODEL", "deepseek-chat"),
		BridgeBudget:     envInt("PIUMY_BRIDGE_BUDGET", 100),

		AutoReplyInterval: envDuration("PIUMY_AUTOREPLY_INTERVAL", 5*time.Minute),
		AutoReplyDelay:    envDuration("PIUMY_AUTOREPLY_DELAY", 3*time.Second),

		MediaDir:             envPath("PIUMY_MEDIA_DIR", secretsDir, "media"),
		MediaLowQJPEGQuality: envInt("PIUMY_MEDIA_LOWQ_QUALITY", 40),

		MeteringOutCharWeight: envFloat("PIUMY_METERING_OUT_CHAR_WEIGHT", 1.0),
		MeteringInCharWeight:  envFloat("PIUMY_METERING_IN_CHAR_WEIGHT", 0.25),
		MeteringImageCost:     envFloat("PIUMY_METERING_IMAGE_COST", 800),
		MeteringAudioCost:     envFloat("PIUMY_METERING_AUDIO_COST", 400),
		MeteringMessageCost:   envFloat("PIUMY_METERING_MESSAGE_COST", 5),
		MeteringDailyQuota:    envFloat("PIUMY_METERING_DAILY_QUOTA", 0),

		MCPAddr:    env("PIUMY_MCP_ADDR", mcpAddrDefault),
		RESTAddr:   env("PIUMY_REST_ADDR", restAddrDefault),
		RESTKey:    os.Getenv("PIUMY_REST_KEY"),
		PolicyPath: os.Getenv("PIUMY_POLICY_PATH"),

		MaxRedispatch:  envInt("PIUMY_MAX_REDISPATCH", 7),
		GateStaleAfter: envDuration("PIUMY_GATE_STALE_AFTER", 5*time.Minute),

		DispatchDebounce:    envDuration("PIUMY_DISPATCH_DEBOUNCE", 60*time.Second),
		MaxDispatchDebounce: envDuration("PIUMY_MAX_DISPATCH_DEBOUNCE", 5*time.Minute),

		SMTPHost: os.Getenv("PIUMY_SMTP_HOST"),
		SMTPPort: env("PIUMY_SMTP_PORT", "587"),
		SMTPUser: os.Getenv("PIUMY_SMTP_USER"),
		SMTPPass: os.Getenv("PIUMY_SMTP_PASS"),
		SMTPFrom: os.Getenv("PIUMY_SMTP_FROM"),
	}
	return cfg, nil
}
