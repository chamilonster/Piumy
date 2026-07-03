// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
//
// Piumy switchboard — core (Go). It does not reply: it routes and stores.
// It exposes MCP so an external agent can read the queue, act, and respond.
//
// Subcommands:
//
//	pimywa serve          start the MCP server (streamable HTTP)
//	pimywa state <mood>   write status.json (face) — handy for the display
//	pimywa seed           insert demo data to test without WhatsApp
//	pimywa restore-session [--force] <file>
//	                       decrypt a session backup and replace the live
//	                       session DB — refuses if `serve` looks like it's
//	                       still running; --force overrides.
//	pimywa auth setup [--env-file <path>]
//	                       generate + persist PIMYWA_MCP_KEY if not already
//	                       set — the MCP endpoint
//	                       (:8081) refuses ALL requests without one.
//	pimywa auth rotate [--env-file <path>]
//	                       always generate a NEW PIMYWA_MCP_KEY, replacing
//	                       any existing one (invalidates old clients).
//
// Env config: PIMYWA_STATUS, PIMYWA_DB, PIMYWA_ROUTER, PIMYWA_MCP_ADDR,
//
//	PIMYWA_MCP_KEY, PIMYWA_SWAMPED_AT, PIMYWA_AGENT_IDLE, PIMYWA_HOSTNAME,
//	PIMYWA_DASH, PIMYWA_DASH_ADDR, PIMYWA_DASH_USER, PIMYWA_DASH_PASS,
//	PIMYWA_DASH_PASS_HASH, PIMYWA_BACKUP_KEY, PIMYWA_BACKUP_DIR,
//	PIMYWA_BACKUP_KEEP, PIMYWA_BACKUP_INTERVAL.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"pimywa/internal/autoreply"
	"pimywa/internal/bridge"
	"pimywa/internal/config"
	"pimywa/internal/dashboard"
	"pimywa/internal/eventbus"
	"pimywa/internal/gateway"
	"pimywa/internal/governor"
	"pimywa/internal/mcpguard"
	"pimywa/internal/mcpserver"
	"pimywa/internal/netinfo"
	"pimywa/internal/restapi"
	"pimywa/internal/router"
	"pimywa/internal/sessionbackup"
	"pimywa/internal/state"
	"pimywa/internal/store"
	"pimywa/internal/sysinfo"
)

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve":
		runServe()
	case "state":
		runState(os.Args[2:])
	case "seed":
		runSeed()
	case "restore-session":
		runRestoreSession(os.Args[2:])
	case "auth":
		runAuth(os.Args[2:])
	default:
		fmt.Fprintln(os.Stderr, "usage: pimywa [serve | state <mood> | seed | restore-session [--force] <file> | auth <setup|rotate> [--env-file <path>]]")
		os.Exit(2)
	}
}

// runRestoreSession decrypts a session backup and replaces the live session
// DB (entregable A). MANUAL/deliberate only — never wired to REST,
// never automatic. Refuses if the session DB's lock file names a live
// `pimywa serve` process (see sessionbackup.CheckNotServing) unless --force
// is passed.
func runRestoreSession(args []string) {
	force := false
	var backupFile string
	for _, a := range args {
		if a == "--force" {
			force = true
			continue
		}
		backupFile = a
	}
	if backupFile == "" {
		fmt.Fprintln(os.Stderr, "usage: pimywa restore-session [--force] <backup-file>")
		os.Exit(2)
	}

	cfg := config.Load()
	if cfg.BackupKey == "" {
		fmt.Fprintln(os.Stderr, "error: PIMYWA_BACKUP_KEY is not set — cannot decrypt the backup")
		os.Exit(1)
	}
	if err := sessionbackup.CheckNotServing(cfg.SessionDB, force); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if err := sessionbackup.Restore(backupFile, cfg.SessionDB, cfg.BackupKey); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("session restored: %s -> %s\n", backupFile, cfg.SessionDB)
}

// defaultEnvFile matches deploy/install.sh's ENV_FILE convention — the same
// file every systemd unit loads via EnvironmentFile=. NOT a config.go
// setting: config.Load() only reads already-exported env vars (systemd's
// EnvironmentFile= does the loading before this binary ever runs) — this
// CLI is the one thing that must know the file's path directly, to edit it.
const defaultEnvFile = "/opt/pimywa/pimywa.env"

// runAuth implements `pimywa auth setup|rotate` (item
// B — verbatim: "el auth necesita un instalador en el terminal").
// setup is idempotent (a no-op if PIMYWA_MCP_KEY is already set); rotate
// always replaces it, invalidating every currently-configured client.
func runAuth(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: pimywa auth <setup|rotate> [--env-file <path>]")
		os.Exit(2)
	}
	sub := args[0]
	envFile := defaultEnvFile
	for i := 1; i < len(args); i++ {
		if args[i] == "--env-file" && i+1 < len(args) {
			envFile = args[i+1]
			i++
		}
	}

	switch sub {
	case "setup":
		runAuthSetup(envFile)
	case "rotate":
		runAuthRotate(envFile)
	default:
		fmt.Fprintln(os.Stderr, "usage: pimywa auth <setup|rotate> [--env-file <path>]")
		os.Exit(2)
	}
}

func runAuthSetup(envFile string) {
	existing, present, err := readEnvKey(envFile, "PIMYWA_MCP_KEY")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: reading %s: %v\n", envFile, err)
		os.Exit(1)
	}
	if present && existing != "" {
		fmt.Printf("PIMYWA_MCP_KEY is already configured in %s -- nothing to do.\n", envFile)
		fmt.Println("Run `pimywa auth rotate` to generate a new one (this invalidates the current token).")
		return
	}

	token, err := genAuthToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: generating token: %v\n", err)
		os.Exit(1)
	}
	if err := setEnvKey(envFile, "PIMYWA_MCP_KEY", token); err != nil {
		fmt.Fprintf(os.Stderr, "error: writing %s: %v\n", envFile, err)
		os.Exit(1)
	}
	printAuthTokenInstructions(envFile, token, false)
}

func runAuthRotate(envFile string) {
	token, err := genAuthToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: generating token: %v\n", err)
		os.Exit(1)
	}
	if err := setEnvKey(envFile, "PIMYWA_MCP_KEY", token); err != nil {
		fmt.Fprintf(os.Stderr, "error: writing %s: %v\n", envFile, err)
		os.Exit(1)
	}
	printAuthTokenInstructions(envFile, token, true)
}

// genAuthToken returns a random 64-hex-char (32-byte) bearer token — twice
// the entropy of dashboard.GenerateRandomPassword's 24-char human password,
// deliberately: this token gates EVERY MCP tool (including owner-scoped
// ones like reset_dashboard_password), a broader blast radius than one
// dashboard login, and nobody needs to type it by hand (pasted into a
// client config once).
func genAuthToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// readEnvKey returns key's value from a simple KEY=VALUE env file (one
// assignment per line, matching install.sh's own format) — present=false
// means the key has no line at all, distinct from a line present with an
// empty value (both are treated as "needs setup" by the caller, but this
// distinction keeps the function honest about what it actually saw).
func readEnvKey(path, key string) (value string, present bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	prefix := key + "="
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix)), true, nil
		}
	}
	return "", false, nil
}

// setEnvKey sets key=value in a simple KEY=VALUE env file, creating the
// file (and its parent directory) if needed, replacing an existing
// key=... line in place or appending a new one. Atomic tmp+rename (3rd
// commandment: a power cut mid-write must never corrupt a secrets file)
// and 0600 permissions, since this file holds PIMYWA_MCP_KEY alongside
// whatever other secrets (PIMYWA_DASH_PASS, PIMYWA_BACKUP_KEY) already
// live there — never written to git (deploy/pimywa.env.example is the
// checked-in TEMPLATE, this is the real, gitignored file).
func setEnvKey(path, key, value string) error {
	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		lines = strings.Split(string(data), "\n")
	} else if !os.IsNotExist(err) {
		return err
	}
	// Split on a trailing "\n" always yields one extra "" element (any
	// prior write here, or install.sh's own file, ends with a newline) --
	// drop it up front so it never accumulates across repeated writes,
	// whether this call replaces an existing key or appends a new one.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	prefix := key + "="
	found := false
	for i, line := range lines {
		if strings.HasPrefix(line, prefix) {
			lines[i] = prefix + value
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, prefix+value)
	}

	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// printAuthTokenInstructions shows the new token exactly once (it is never
// re-displayable after this — it's now only stored, same "shown once"
// discipline as reset_dashboard_password) with the exact client config
// snippet, and never writes it to a log (only stdout, from an explicit
// interactive CLI invocation).
func printAuthTokenInstructions(envFile, token string, rotated bool) {
	if rotated {
		fmt.Println("PIMYWA_MCP_KEY rotated -- the PREVIOUS token is now invalid.")
	} else {
		fmt.Println("PIMYWA_MCP_KEY generated and saved to", envFile)
	}
	fmt.Println()
	fmt.Println("Configure your MCP client (Claude Code / OpenCode) with:")
	fmt.Println()
	fmt.Printf("  {\"mcpServers\": {\"piumy\": {\"url\": \"http://<host>:8081/mcp\",\n")
	fmt.Printf("    \"headers\": {\"Authorization\": \"Bearer %s\"}}}}\n", token)
	fmt.Println()
	fmt.Println("Shown once -- save it now (e.g. a password manager). Restart the core to apply:")
	fmt.Println("  sudo systemctl restart pimywa-core")
}

func runState(args []string) {
	mood := "idle"
	if len(args) > 0 {
		mood = args[0]
	}
	if !state.ValidMoods[mood] {
		fmt.Fprintf(os.Stderr, "invalid mood: %q\n", mood)
		fmt.Fprintln(os.Stderr, "valid: idle zero new_msg few swamped thinking working responding ai_online vip sleeping alert error qr")
		os.Exit(2)
	}
	cfg := config.Load()
	s := state.Status{Mood: mood, WAConnected: mood != "qr" && mood != "error", ShowQR: mood == "qr"}
	if err := state.Write(cfg.StatusPath, s); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("status.json written to %s (mood=%s)\n", cfg.StatusPath, mood)
}

func runSeed() {
	cfg := config.Load()
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()
	now := time.Now().Unix()
	_ = st.SetMode("56999999999@s.whatsapp.net", "advanced")
	_ = st.AddMessage(store.Message{ChatJID: "56999999999@s.whatsapp.net", ID: "demo1", Sender: "56999999999", Text: "hi, can you send me the catalog?", TS: now, Type: "text"})
	_ = st.AddMessage(store.Message{ChatJID: "56988888888@s.whatsapp.net", ID: "demo2", Sender: "56988888888", Text: "hello", TS: now, Type: "text"})
	fmt.Println("seed OK at", cfg.DBPath)
}

func runServe() {
	startTime := time.Now()
	cfg := config.Load()
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	sm := state.NewManager(cfg.StatusPath, cfg.SwampedAt)
	_ = sm.SetMood("idle")
	// SENT counter: populate once at boot from the
	// message history (store.CountOutboundSince(0) — total sent, ever) so
	// the display shows the real all-time total from the first frame, not
	// 0 until this session's first send. Between sends the count can't
	// change, so this one-time seed plus the update after each confirmed
	// send (gateway.go) is enough — no need to recompute on every
	// status.json write.
	if sent, err := st.CountOutboundSince(0); err != nil {
		log.Printf("state: count sent: %v", err)
	} else {
		_ = sm.Update(func(s *state.Status) { s.Sent = sent })
	}
	rtMgr := router.NewManager(cfg.RouterPath)
	// Rate limits (0753): KV-override wins over the env default so a
	// restart doesn't silently forget a dashboard edit.
	gov := governor.NewLimiter(st.SettingInt(store.SettingRateLimitPerMin, cfg.RateLimitPerMin), time.Minute)
	gov.SetDailyMax(st.SettingInt(store.SettingRateLimitPerDay, cfg.RateLimitPerDay))
	// The daily cap must survive a restart (3rd commandment) — reconstruct
	// today's already-sent count from message history instead of starting
	// over at 0, which would let a crash-looping Pi blow past its daily
	// anti-ban limit. The per-minute bucket is fine starting fresh (it
	// recovers within a minute on its own).
	{
		now := time.Now()
		startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
		sentToday, err := st.CountOutboundSince(startOfDay)
		if err != nil {
			log.Printf("governor: count sent today: %v", err)
		} else {
			gov.SeedDailyCount(sentToday)
		}
	}

	// MCP anti-flood (entregable E) — a limiter DISTINCT from gov
	// above: gov paces WhatsApp-outbound sends, this paces MCP-inbound tool
	// calls from a connected agent ("el sistema debe ser capaz de
	// bloquear a la IA si se pone a hacer flood"). KV-override wins over
	// the env default, same restart-durability reasoning as the rate
	// limits above — no in-memory-only state to lose on a crash-loop.
	guard := mcpguard.New(mcpguard.Config{
		RatePerMin:     st.SettingInt(store.SettingMCPGuardRatePerMin, cfg.MCPGuardRatePerMin),
		EmitRatePerMin: st.SettingInt(store.SettingMCPGuardEmitRatePerMin, cfg.MCPGuardEmitRatePerMin),
		BlockThreshold: st.SettingInt(store.SettingMCPGuardBlockThreshold, cfg.MCPGuardBlockThreshold),
		BlockCooldown:  st.SettingDuration(store.SettingMCPGuardBlockCooldown, cfg.MCPGuardBlockCooldown),
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Session backup (entregable A — SENSITIVE, touches WhatsApp
	// credentials). Empty PIMYWA_BACKUP_KEY disables it (fail-safe-off, logs
	// its own startup warning). MarkServing is UNCONDITIONAL — even with
	// backups off, `pimywa restore-session` must still be able to detect a
	// live `serve` process and refuse (corruption risk exists independent
	// of whether backups are configured).
	backuper := sessionbackup.New(sessionbackup.Config{
		SessionDBPath: cfg.SessionDB,
		Key:           cfg.BackupKey,
		Dir:           cfg.BackupDir,
		Keep:          cfg.BackupKeep,
		Interval:      cfg.BackupInterval,
	})
	if backuper.Enabled() {
		sessionbackup.WarnIfSameVolume(cfg.BackupDir, filepath.Dir(cfg.SessionDB), log.Printf)
	}
	if err := sessionbackup.MarkServing(cfg.SessionDB); err != nil {
		log.Printf("sessionbackup: mark serving: %v", err)
	}
	go backuper.RunPeriodic(ctx)

	// Network info: populate once at startup, refresh every 30 s.
	go func() {
		applyNetInfo := func() {
			info := netinfo.Gather(cfg.Hostname, cfg.WifiIface)
			_ = sm.Update(func(s *state.Status) {
				s.Hostname = info.Hostname
				s.IP = info.IP
				s.Wifi = info.Wifi
				s.SSID = info.SSID
			})
		}
		applyNetInfo()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				applyNetInfo()
			}
		}
	}()

	// status.json heartbeat (D3's single-writer fix):
	// the core is now the ONLY writer of status.json — the power adapter
	// writes just its reading to battery.json (cfg.BatteryFile), and this
	// ticker merges it in. Also doubles as keeping updated_at fresh during
	// quiet periods with no other event (Write() always re-stamps it) —
	// cheap since status.json lives in tmpfs (3rd commandment: zero SD
	// wear regardless of interval). Update (not UpdateMood/SetResting):
	// must never disturb an in-flight React or mood transition, just these
	// fields. (milestones C/E) also merges in
	// voltage/charging/time_remaining from the same sidecar, and CPU/RAM
	// straight from /proc (internal/sysinfo) — cheap enough for this same
	// tick, no separate ticker needed. Also merges in the same
	// merge for face.json (the display adapter's live kaomoji face).
	go func() {
		applyBattery := func() {
			_ = sm.Update(func(s *state.Status) {
				if r, ok := state.ReadBatteryFile(cfg.BatteryFile, cfg.BatteryMaxAge); ok {
					s.Battery = &r.Battery
					s.Voltage = r.VoltageMV
					s.Charging = r.Charging
					s.TimeRemaining = r.TimeRemainingMin
				} else {
					s.Battery = nil
					s.Voltage = nil
					s.Charging = false
					s.TimeRemaining = nil
				}
				if face, ok := state.ReadFaceFile(cfg.FaceFile, cfg.FaceMaxAge); ok {
					s.Face = face
				} else {
					s.Face = nil
				}
				if pct, ok := sysinfo.CPUPercent(); ok {
					s.CPU = &pct
				} else {
					s.CPU = nil
				}
				if pct, ok := sysinfo.RAMPercent(); ok {
					s.RAM = &pct
				} else {
					s.RAM = nil
				}
				s.Uptime = int(time.Since(startTime).Seconds())
			})
		}
		applyBattery()
		ticker := time.NewTicker(cfg.StatusHeartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				applyBattery()
			}
		}
	}()

	// Gateway controller — created unconditionally so the dashboard can always
	// trigger a link even when PIMYWA_GATEWAY=none at boot.
	gwCtrl, err := gateway.NewController(
		gateway.Config{
			SessionDB:      cfg.SessionDB,
			QRTimeout:      cfg.QRTimeout,
			DeviceName:     cfg.DeviceName,
			MinSendDelay:   cfg.DispatchDelayMin,
			MaxSendDelay:   cfg.DispatchDelayMax,
			ReadDelayMin:   cfg.ReadDelayMin,
			ReadDelayMax:   cfg.ReadDelayMax,
			ActionDelayMin: cfg.ActionDelayMin,
			ActionDelayMax: cfg.ActionDelayMax,
			MediaDir:       cfg.MediaDir,
			MediaMaxMB:     cfg.MediaMaxMB,
		},
		st, sm, rtMgr, gov,
	)
	if err != nil {
		log.Fatalf("gateway: init: %v", err)
	}
	// Session backup post-link hook: fires on every successful
	// connect (fresh link AND reconnects). Non-blocking (its own goroutine)
	// since it runs synchronously off whatsmeow's event dispatcher;
	// debounced (BackupIfDue) so a flaky-wifi reconnect storm can't spam
	// scrypt derivations on a Pi Zero for no benefit.
	gwCtrl.SetPostLinkHook(func() {
		go backuper.BackupIfDue(ctx, 10*time.Minute)
	})
	// Low-latency agent notification (entregable D): the gateway
	// publishes a nudge on every stored inbound message; GET /api/events
	// (wired below, into restapi.Deps) fans it out over SSE. Standalone bus,
	// no coupling — see internal/eventbus's package doc for why.
	bus := eventbus.New()
	gwCtrl.SetBus(bus)
	if cfg.Gateway == "whatsmeow" {
		if err := gwCtrl.Start(); err != nil {
			log.Fatalf("gateway: start: %v", err)
		}
		log.Printf("Piumy gateway: whatsmeow — session=%s", cfg.SessionDB)
	} else {
		log.Printf("Piumy gateway: %s (no WhatsApp connection; start via dashboard)", cfg.Gateway)
	}

	// REST API (quick access: curl/scripts/dashboard, no MCP client needed).
	go func() {
		h := restapi.Handler(restapi.Deps{
			Store: st, State: sm, Gov: gov, APIKey: cfg.APIKey, GWCtrl: gwCtrl, RouterMgr: rtMgr, Backup: backuper,

			// Settings floors/ceilings (0753): delays can only be loosened
			// past these defaults, never tightened; rate limits are the
			// mirror-image (ceilinged, never loosened past these numbers).
			DispatchDelayMinDefault: cfg.DispatchDelayMin,
			DispatchDelayMaxDefault: cfg.DispatchDelayMax,
			ReadDelayMinDefault:     cfg.ReadDelayMin,
			ReadDelayMaxDefault:     cfg.ReadDelayMax,
			ActionDelayMinDefault:   cfg.ActionDelayMin,
			ActionDelayMaxDefault:   cfg.ActionDelayMax,

			MediaMaxMBDefault: cfg.MediaMaxMB,
			MediaMaxMBFloor:   16, // never let the dashboard disable GC outright (3rd commandment)

			RateLimitPerMinDefault: cfg.RateLimitPerMin,
			RateLimitPerMinCeiling: 30, // 3x the default — sane ceiling, not a recommendation
			RateLimitPerDayDefault: cfg.RateLimitPerDay,
			RateLimitPerDayCeiling: 2000,

			Guard: guard,
			Bus:   bus,

			BatteryLogFile: cfg.BatteryLogFile,
		})
		log.Printf("Piumy REST API on %s", cfg.APIAddr)
		if err := http.ListenAndServe(cfg.APIAddr, h); err != nil && err != http.ErrServerClosed {
			log.Printf("rest api: %v", err)
		}
	}()

	// Dashboard (lightweight LAN web UI, optional, default enabled).
	if cfg.DashEnabled {
		passHash, err := dashboard.ResolvePassHash(cfg.DashPassHash, cfg.DashPass)
		if err != nil {
			log.Fatalf("dashboard: password setup: %v", err)
		}
		dashCfg := dashboard.Config{
			Addr:     cfg.DashAddr,
			Username: cfg.DashUser,
			PassHash: passHash,
		}
		dashDeps := dashboard.Deps{
			Store:          st,
			State:          sm,
			Gov:            gov,
			GWCtrl:         gwCtrl,
			RouterMgr:      rtMgr,
			BatteryLogFile: cfg.BatteryLogFile,
		}
		go func() {
			h := dashboard.Handler(ctx, dashCfg, dashDeps)
			log.Printf("Piumy dashboard on %s (user=%s)", cfg.DashAddr, cfg.DashUser)
			if err := http.ListenAndServe(cfg.DashAddr, h); err != nil && err != http.ErrServerClosed {
				log.Printf("dashboard: %v", err)
			}
		}()
	}

	// Auto-reply worker (gap #1): drafts replies for pending auto-mode chats
	// via a pluggable bridge. NEVER sends — drafts wait for privileged
	// approval (a separate, later piece). PIMYWA_BRIDGE=none (default) makes
	// the bridge a no-op, so with nothing configured this does nothing.
	autoBridge := bridge.New(bridge.Config{
		Plugin:           cfg.Bridge,
		DeepSeekKey:      cfg.DeepSeekKey,
		DeepSeekEndpoint: cfg.DeepSeekEndpoint,
		DeepSeekModel:    cfg.DeepSeekModel,
		BudgetMax:        cfg.BridgeBudget,
	})
	autoWorker := &autoreply.Worker{
		Store:     st,
		Bridge:    autoBridge,
		Policy:    func() string { return autoreply.PolicyText(cfg.DecisionPolicyPath) },
		ModelName: cfg.DeepSeekModel,
		Interval:  cfg.AutoReplyInterval,
		Delay:     cfg.AutoReplyDelay,
	}
	go autoWorker.Run(ctx)

	mcpSrv := mcpserver.New(ctx, mcpserver.Deps{
		Store:             st,
		State:             sm,
		Router:            rtMgr,
		Gov:               gov,
		AgentIdle:         cfg.AgentIdle,
		ReadMarker:        gwCtrl,
		PolicyPath:        cfg.DecisionPolicyPath,
		Guard:             guard,
		ClaimTTLDefault:   cfg.ClaimTTLDefault,
		MCPAuthConfigured: cfg.MCPKey != "",
	})

	// Auth: bearer token via PIMYWA_MCP_KEY (0133, gap #4) — its own key,
	// separate from REST's PIMYWA_API_KEY, since MCP client configs pass
	// auth via an Authorization header (.mcp.json's "headers"). Empty key
	// keeps the endpoint open (logs a startup warning). A custom
	// *http.Server + mux is required to insert the auth middleware in front
	// of the streamable handler (Start() only auto-builds a mux when none
	// is supplied).
	mcpHTTPServer := &http.Server{}
	httpSrv := server.NewStreamableHTTPServer(mcpSrv, server.WithStreamableHTTPServer(mcpHTTPServer))
	mcpMux := http.NewServeMux()
	mcpMux.Handle("/mcp", mcpserver.RequireBearerToken(cfg.MCPKey, httpSrv))
	mcpHTTPServer.Handler = mcpMux

	go func() {
		<-ctx.Done()
		log.Println("shutting down...")
		_ = sm.SetMood("sleeping")
		gwCtrl.Stop() // disconnect WhatsApp cleanly before server goes down
		// Clean shutdown: clear the serving lock so a later restore-session
		// doesn't have to guess whether this exit was clean. A
		// crash skips this — CheckNotServing's stale-lock path handles that.
		if err := sessionbackup.UnmarkServing(cfg.SessionDB); err != nil {
			log.Printf("sessionbackup: unmark serving: %v", err)
		}
		shctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shctx)
	}()

	log.Printf("Piumy switchboard — MCP (streamable HTTP) on %s · db=%s", cfg.MCPAddr, cfg.DBPath)
	if err := httpSrv.Start(cfg.MCPAddr); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
}
