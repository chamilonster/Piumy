package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/mdp/qrterminal/v3"

	"piumy-gateway/internal/agentconnect"
	"piumy-gateway/internal/capipush"
	"piumy-gateway/internal/config"
	"piumy-gateway/internal/corepipeline"
	"piumy-gateway/internal/eventbus"
	"piumy-gateway/internal/governor"
	"piumy-gateway/internal/gwlog"
	"piumy-gateway/internal/i18n"
	"piumy-gateway/internal/mcpguard"
	"piumy-gateway/internal/mcpserver"
	"piumy-gateway/internal/restapi"
	"piumy-gateway/internal/router"
	"piumy-gateway/internal/sessionbackup"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
	"piumy-gateway/internal/whatsmeow"
)

// kvOrEnv returns the KV-stored override if set, otherwise the env-derived
// default — the same layering every other dashboard-editable setting uses.
func kvOrEnv(s *store.Store, key, envVal string) string {
	if v, err := s.KVGet(key); err == nil && v != "" {
		return v
	}
	return envVal
}

// resolveDefaultTerminalID applies T25's decision (hallazgo 2,
// ct-2026-08-05-1833): PIUMY_DEFAULT_TERMINAL_ID empty doesn't mean "no
// terminal to dispatch the owner's messages to" when the principal antenna
// is already configured — antennaTerminalID IS where capipush already
// sends everything else. The env var always wins if set; the antenna is
// the fallback, never an override of an explicit setting.
func resolveDefaultTerminalID(envValue, antennaTerminalID string) string {
	if envValue != "" {
		return envValue
	}
	return antennaTerminalID
}

// restoreKillSwitch re-applies the anti-ban emergency stop from its last
// persisted state (T19, ct-2026-08-05-1249) — governor.SetKill/
// state.Muted live only in memory, so a restart (power cut, a Windows
// update, a crash — not hypothetical, the boss's own machine hibernated
// and dropped the gateway) silently released the brake. If it was killed
// for a real reason, the gateway would come back sending exactly when it
// shouldn't. Caller MUST run this before ctrl.Start() — the one call that
// can actually make the pipeline send anything — so the brake is back on
// before there's anything to brake against; reading the setting AFTER
// sends could already start is the exact bug this closes. Applies BOTH
// halves together (governor.SetKill + state.SetMuted), same as
// set_kill_switch itself (mcpserver/restapi) always does — restoring only
// one would silently diverge from what was actually set.
func restoreKillSwitch(s *store.Store, gov *governor.Limiter, sm *state.Manager) (restored bool, err error) {
	if !s.SettingBool(store.SettingKillSwitch, false) {
		return false, nil
	}
	gov.SetKill(true)
	return true, sm.SetMuted(true)
}

// pusherInjectorResolver adapts *capipush.Pusher.InjectorFor to
// restapi.InjectorResolver (M2, ct-2026-07-22-1301) — capipush.Injector and
// restapi.Injector are the same one-method shape but distinct named types
// (each package avoids importing the other, same reasoning as restapi's own
// CAPIConnector/Injector local interfaces), so Go needs this one glue hop;
// main.go is the wiring layer, the right place for it.
type pusherInjectorResolver struct{ p *capipush.Pusher }

func (r pusherInjectorResolver) InjectorFor(agentID string) (restapi.Injector, bool) {
	return r.p.InjectorFor(agentID)
}

// todayStartLocal returns the Unix timestamp of local midnight for now —
// the same day boundary governor.Limiter's own daily-cap rollover uses
// (time.Now().Format("2006-01-02"), local time). Seeding the daily count
// against any other boundary (e.g. UTC midnight, like store.Today()) could
// under/over-count near the day change.
func todayStartLocal(now time.Time) int64 {
	y, m, d := now.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, now.Location()).Unix()
}

func main() {
	// T21 (ct-2026-08-05-1308): claims the mutex the Windows installer
	// checks for before it will reinstall/uninstall over a running Piumy —
	// see appmutex_windows.go. No-op on other platforms.
	acquireAppMutex()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// T11 (ct-2026-08-05-1214): fills in whatever PIUMY_* env vars aren't
	// already set from piumy-config.json (or migrates one from a legacy
	// run-piumy.bat) — BEFORE config.Load() reads the environment. Env
	// var wins always; this never overrides one that's already set.
	if err := config.ApplyFileDefaults(); err != nil {
		log.Printf("config: archivo de configuración: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// T53 (ct-2026-08-10-1849): el binario se compila -H=windowsgui (sin
	// consola) y el instalador lo lanza directo — sin esto, todo lo que
	// log.Printf escribe a partir de acá se evapora. Va lo antes posible,
	// apenas cfg.StatusPath existe.
	//
	// En "logs/", hermano de secrets/ y NO adentro (Citrino, antes de
	// publicar): este archivo existe para pedírselo a un usuario cuando algo
	// no le llega. La forma natural de mandarlo es comprimir la carpeta que
	// lo contiene — y secrets/ tiene PIUMY_MCP_KEY, PIUMY_REST_KEY y la
	// sesión de WhatsApp. Un log que se pide es un log que sale de la
	// máquina: no puede vivir junto a las credenciales.
	if err := gwlog.Setup(filepath.Join(filepath.Dir(filepath.Dir(cfg.StatusPath)), "logs")); err != nil {
		log.Printf("gwlog: no se pudo abrir el archivo de log: %v", err)
	}

	// T169 (ct-2026-09-19-1433): data paths now default under DataDir()
	// instead of the working directory — the one real risk in that change
	// is an operator with existing data who starts WITHOUT the explicit
	// PIUMY_*_PATH vars this time, and Piumy quietly opens a brand-new empty
	// store somewhere else, reading as "lost everything". This only NAMES
	// what it found and where this run is actually looking — never migrates,
	// the decision stays with whoever reads the log.
	for _, w := range config.WarnLegacyData(cfg) {
		log.Printf("config: %s", w)
	}

	// T59 (ct-2026-08-10-2116): dos Piumy corriendo a la vez pisan la misma
	// sesión de WhatsApp (whatsmeow.db) — pasó de verdad, dos veces el mismo
	// día, una terminó con WhatsApp desconectado. Va DESPUÉS de gwlog.Setup
	// (a propósito: el motivo de salida tiene que quedar en el log, no
	// evaporarse como el resto de log.Printf en el binario -H=windowsgui) y
	// ANTES de tocar el store o whatsmeow — si es la segunda instancia, sale
	// sin haber abierto ni tocado la sesión en absoluto. Distinto del mutex
	// de acquireAppMutex de arriba (ver singleinstance_windows.go): ese es
	// best-effort para el instalador, este es autoritativo.
	//
	// S1 (ct-2026-09-20-1100): el candado escala por el directorio de datos
	// EFECTIVO, no por el nombre de cuenta — dos cuentas distintas resuelven
	// a dos directorios distintos y arrancan las dos; dos procesos apuntando
	// al mismo directorio siguen chocando igual que hoy. config.DataDir() es
	// pura (solo lee entorno) — llamarla de nuevo acá, en vez de guardar un
	// campo nuevo en Config solo para este único consumidor, es más directo.
	dataDir, err := config.DataDir()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if !acquireSingleInstance(dataDir) {
		log.Println("piumy-gateway: ya hay una instancia corriendo — esta instancia sale ahora, sin tocar la sesión de WhatsApp")
		return
	}

	s, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer s.Close()

	if err := restapi.SeedRecoveryEmailFromEnv(s); err != nil {
		log.Printf("restapi: correo de recuperación: siembra falló: %v", err)
	}

	// T13 (ct-2026-08-05-123147): sin reglas efectivas la IA nunca actúa
	// (EffectiveRules' gate duro) — sin esto, una instalación limpia nace
	// muda. Solo siembra las claves que nunca se escribieron; ver
	// SeedFactoryRulesIfUnset.
	if seeded, err := s.SeedFactoryRulesIfUnset(); err != nil {
		log.Printf("store: reglas de fábrica: siembra falló: %v", err)
	} else if len(seeded) > 0 {
		log.Printf("store: reglas de fábrica sembradas (nunca se habían escrito): %v", seeded)
	}

	rt := router.NewManager(cfg.RouterPath)
	gov := governor.NewLimiter(cfg.RateLimitPerMin, time.Minute)
	gov.SetDailyMax(cfg.RateLimitPerDay)
	// H1 hardening (ct-2026-07-10-0540): without this, the anti-ban daily
	// cap resets to 0 on every restart — a crash-loop or routine redeploy
	// would silently blow through RateLimitPerDay. Seed it with what's
	// actually gone out since local midnight (governor.checkDaily rolls
	// over on time.Now()'s LOCAL calendar day, not UTC like store.Today()
	// — must count against the same boundary the rollover check uses).
	if sentToday, err := s.CountOutboundSince(todayStartLocal(time.Now())); err != nil {
		log.Printf("governor: count outbound since midnight: %v", err)
	} else {
		gov.SeedDailyCount(sentToday)
	}
	sm := state.NewManager(cfg.StatusPath, cfg.SwampedAt)
	// T19 (ct-2026-08-05-1249): restore the emergency stop, if it was on,
	// as early as physically possible — gov/sm both exist now, and nothing
	// this early can send anything yet (whatsmeow.New below only
	// constructs; ctrl.Start(), the call that actually connects and starts
	// the outbox drain, is still ~250 lines away). Every line between here
	// and ctrl.Start() runs with the brake already correctly applied.
	if restored, err := restoreKillSwitch(s, gov, sm); err != nil {
		log.Printf("governor: restaurar freno de emergencia: %v", err)
	} else if restored {
		log.Println("governor: freno de emergencia restaurado desde el último apagado — el gateway arranca SIN mandar hasta que se desactive explícitamente")
	}
	// mcpLn/restLn: bind NOW, not when Serve() is finally called near the
	// end of main (S1, ct-2026-09-20-1100) — cfg.MCPAddr/RESTAddr can be
	// ":0" (PIUMY_ACCOUNT set, no explicit port), and every consumer of the
	// REAL bound address (agent-connect.json right below, the boot log, the
	// tray's dashboardURL) needs to know the actual port, not ":0" itself.
	// Binding early and Serve()-ing later is fine: the socket just queues
	// connections in its backlog until Serve starts accepting them.
	mcpLn, err := net.Listen("tcp", cfg.MCPAddr)
	if err != nil {
		log.Fatalf("mcp http: listen %s: %v", cfg.MCPAddr, err)
	}
	restLn, err := net.Listen("tcp", cfg.RESTAddr)
	if err != nil {
		log.Fatalf("rest http: listen %s: %v", cfg.RESTAddr, err)
	}
	mcpAddr := mcpLn.Addr().String()
	restAddr := restLn.Addr().String()
	// restPort alone (not restAddr whole) is what dashboardURL below needs:
	// a wildcard bind's Addr() comes back as "[::]:54321"/"0.0.0.0:54321" —
	// concatenating THAT after "http://localhost" would build a malformed
	// URL. cfg.RESTAddr never had this problem (always bare ":port", empty
	// host) until S1 made a real port possible; SplitHostPort is the same
	// extraction agentconnect.localURL already does for the same reason.
	_, restPort, err := net.SplitHostPort(restAddr)
	if err != nil {
		log.Fatalf("rest http: %s: %v", restAddr, err)
	}

	// agent-connect.json: written next to status.json (same data dir,
	// derived from cfg.StatusPath — not a separate hardcoded path) so an
	// agent on any machine can discover mcp_url/rest_url/mcp_key/rest_key
	// without parsing the Windows installer's run-piumy.bat. Uses the REAL
	// bound addresses (mcpAddr/restAddr), not cfg.MCPAddr/RESTAddr — those
	// can be ":0" and an agent can't dial that.
	if err := agentconnect.Write(agentconnect.Params{
		DataDir: filepath.Dir(cfg.StatusPath),
		MCPAddr: mcpAddr, RESTAddr: restAddr,
		MCPKey: cfg.MCPKey, RESTKey: cfg.RESTKey,
	}); err != nil {
		log.Printf("agentconnect: write agent-connect.json: %v", err)
	}
	bus := eventbus.New()

	// gateway (seam F2) — whatsmeow (F5.x connector rewrite,
	// ct-2026-07-10-0420): pure-Go WhatsApp multi-device client. The sole
	// gateway.Gateway implementation (internal/openwa was deleted, ST-E,
	// ct-2026-07-11-1444 — whatsmeow fully replaced it, group/profile
	// admin included). Validated end to end by a standalone smoke
	// (ct-2026-07-10-0338) before this wiring: connects, GetJoinedGroups
	// lists real groups, receives real messages, CGO_ENABLED=0 static.
	gw, err := whatsmeow.New(ctx, whatsmeow.Config{
		DBPath:     cfg.WADBPath,
		DeviceName: cfg.WADeviceName,
		Store:      s,
		Router:     rt,
		// Bus/State/Governor: the "silent death" hardening (H6,
		// ct-2026-07-10-0540) — LoggedOut/TemporaryBan/etc otherwise vanish
		// into a raw library log line only. Governor is the same *Limiter
		// wired into mcpserver/restapi below (H2/H3's kill switch).
		Bus:             bus,
		State:           sm,
		Governor:        gov,
		MediaDir:        cfg.MediaDir,
		LowQJPEGQuality: cfg.MediaLowQJPEGQuality,
		// ActionDelayMin/Max: anti-ban pacing for the contact backfill
		// (ct-2026-07-19-0115, backup Sub 2a) — same PIUMY_DELAY_ACTION_*
		// config dispatch/read delays already use.
		ActionDelayMin: cfg.ActionDelayMin,
		ActionDelayMax: cfg.ActionDelayMax,
		// AvatarRecheckMin/Max: T17 Parte 3 (ct-2026-08-05-1240) — see
		// whatsmeow.avatarRecheckWindow's own doc.
		AvatarRecheckMin: cfg.AvatarRecheckMin,
		AvatarRecheckMax: cfg.AvatarRecheckMax,
		// ReconnectBaseDelay/MaxDelay/StableAfter: T99 (ct-2026-08-29-1607) —
		// see internal/whatsmeow/reconnect.go's own doc.
		ReconnectBaseDelay:   cfg.ReconnectBaseDelay,
		ReconnectMaxDelay:    cfg.ReconnectMaxDelay,
		ReconnectStableAfter: cfg.ReconnectStableAfter,
	})
	if err != nil {
		log.Fatalf("whatsmeow: %v", err)
	}

	pipe := corepipeline.New(gw, s, rt, gov, sm, corepipeline.Config{
		DispatchDelayMin: cfg.DispatchDelayMin, DispatchDelayMax: cfg.DispatchDelayMax,
		ReadDelayMin: cfg.ReadDelayMin, ReadDelayMax: cfg.ReadDelayMax,
		ChunkMaxLen: cfg.ChunkMaxLen, ChunkDelayMin: cfg.ChunkDelayMin, ChunkDelayMax: cfg.ChunkDelayMax,
	})
	pipe.SetBus(bus)
	ctrl := corepipeline.NewController(gw, pipe)

	// despacho cAPI — capipush y mcpserver COMPARTEN este *Gate: capipush
	// registra dispatches, mcpserver los consume (get_instructions/unlock/...).
	gate := mcpserver.NewGate()
	gate.SetStaleAfter(cfg.GateStaleAfter)
	// injector: precedencia única y explícita (ct-2026-07-10-2307).
	// 1. PIUMY_SMOKE_DISPATCH_PATH set -> FileInjector (smoke parte 2a,
	//    ct-2026-07-10-1814): expone el despacho a un agente de prueba
	//    externo, sin CleverCoder real.
	// 2. si no, CleverInjector — SIEMPRE el mismo puntero vivo, tenga o no
	//    endpoint todavía (S6, ct-2026-07-30-031048: antes, si el endpoint
	//    era vacío al boot, se registraba un LogInjector separado y
	//    cleverInj quedaba huérfano — set_capi_connector's SetConfig
	//    reconfiguraba ESE objeto, pero dispatch() seguía usando el
	//    LogInjector original para siempre, porque RegisterInjector se
	//    niega a tocar el slot del principal — "aplica en caliente" era
	//    mentira en ese caso concreto, sólo un restart lo arreglaba). Ahora
	//    cleverInj ES el injector de PortFallback desde el arranque, así
	//    que SetConfig SIEMPRE llega al objeto real que dispatch() usa —
	//    CleverInjector.Configured() (capipush) le avisa a dispatch() que
	//    trate un endpoint vacío igual que LogInjector, sin intentar un
	//    Inject() real contra "".
	// PortFallback (below) is is_boss's ONLY route — dispatch() ignores
	// router.json entirely for LevelBoss chats (ct-2026-07-13-0302). Empty
	// means every owner message dies with "no terminal_id ... no port
	// fallback configured" the moment one is due, buried in a per-sweep
	// log line instead of surfacing at boot (real incident, ct-2026-07-15:
	// cost hours to trace). Loud at startup instead.
	var injector capipush.Injector = capipush.LogInjector{}
	var cleverInj *capipush.CleverInjector
	if smokeDispatchPath := os.Getenv("PIUMY_SMOKE_DISPATCH_PATH"); smokeDispatchPath != "" {
		log.Printf("capipush: SMOKE MODE — dispatches escritos a %s, no a CleverCoder", smokeDispatchPath)
		injector = capipush.FileInjector{Path: smokeDispatchPath}
	} else {
		endpoint := kvOrEnv(s, store.SettingCAPIEndpoint, cfg.CleverAPIEndpoint)
		terminalID := kvOrEnv(s, store.SettingCAPITerminalID, cfg.CleverAPITerminalID)
		pinpass := kvOrEnv(s, store.SettingCAPIPinpass, cfg.CleverAPIPinpass)
		cleverInj = capipush.NewCleverInjector(endpoint, terminalID, pinpass)
		injector = cleverInj
		if endpoint != "" {
			log.Printf("capipush: CleverInjector -> %s (terminal %s)", endpoint, terminalID)
		}
		// T25 (hallazgo 2, ct-2026-08-05-1833): PIUMY_DEFAULT_TERMINAL_ID
		// vacío no significa "no sé a quién despachar" cuando la antena
		// principal YA está configurada — terminalID (arriba) es
		// exactamente el destino al que capipush ya despacha. Antes esto
		// era un cable de medio camino: el log de arranque imprimía la
		// advertencia de "vacío" en la línea de arriba de "CleverInjector ->
		// ... (terminal X)", diciendo dos cosas contradictorias seguidas.
		if resolved := resolveDefaultTerminalID(cfg.DefaultTerminalID, terminalID); resolved != cfg.DefaultTerminalID {
			log.Printf("capipush: PIUMY_DEFAULT_TERMINAL_ID vacío — usando el terminal de la antena principal (%s) como respaldo", resolved)
			cfg.DefaultTerminalID = resolved
		}
	}
	// Recién acá, con el respaldo de la antena ya aplicado, una advertencia
	// real significa lo que dice: ni la variable de entorno ni la antena
	// principal dan un terminal — los mensajes del dueño no tienen a dónde ir.
	if cfg.DefaultTerminalID == "" {
		log.Printf("capipush: WARNING — PIUMY_DEFAULT_TERMINAL_ID vacío: los mensajes del dueño (is_boss) no van a poder despacharse hasta que se setee")
	}
	pusher := capipush.New(s, rt, gate, injector, capipush.Config{
		PortFallback: cfg.DefaultTerminalID,
		Weights: store.UsageWeights{
			OutCharWeight: cfg.MeteringOutCharWeight,
			InCharWeight:  cfg.MeteringInCharWeight,
			ImageCost:     cfg.MeteringImageCost,
			AudioCost:     cfg.MeteringAudioCost,
			MessageCost:   cfg.MeteringMessageCost,
		},
		DailyQuota:    cfg.MeteringDailyQuota,
		MaxRedispatch: cfg.MaxRedispatch,
		// S4b (ct-2026-07-30-1255): same PIUMY_GATE_STALE_AFTER value main.go
		// already passes to gate.SetStaleAfter (line below) — capipush's own
		// sweep re-applies it live from settings every sweep, this is just
		// the code-level fallback threaded through Config so withDefaults
		// has something coherent if this field were ever left zero.
		DispatchStaleAfter: cfg.GateStaleAfter,
		// ct-2026-07-13-2131: suppress read receipts when kill or mute is active.
		HaltedFn: func() bool { return gov.Killed() || sm.Snapshot().Muted },
		// ct-2026-07-13-2243: debounce — wait for silence before dispatching.
		DispatchDebounce:    cfg.DispatchDebounce,
		MaxDispatchDebounce: cfg.MaxDispatchDebounce,
	})
	// Cargar agentes secundarios persistidos y registrar sus injectores en
	// el mapa del pusher. El principal ya está registrado en New() vía injector.
	if agents, err := s.ListAgents(); err != nil {
		log.Printf("capipush: load secondary agents: %v", err)
	} else {
		for _, a := range agents {
			inj := capipush.NewCleverInjector(a.Endpoint, a.AntennaTerminalID, a.Pinpass)
			pusher.RegisterInjector(a.AgentID, inj)
			log.Printf("capipush: secondary agent registered %s -> %s", a.AgentID, a.Endpoint)
		}
	}
	// ct-2026-07-13-2131: read receipts on dispatch — gw satisfies
	// capipush.ReadReceipter (MarkRead signature matches).
	pusher.SetReceipter(gw)
	// ct-2026-07-18-1416: gw satisfies capipush.LIDResolver too (its
	// ResolvePN method, whatsmeow/inbound.go — survives the F1/F2 revert,
	// ct-2026-07-18-171940, as the one piece plaintextPayload still needs).
	pusher.SetLIDResolver(gw)
	// S3 (ct-2026-07-30-030948): the backpressure gate's own signal for the
	// agent (get_status embeds Status) — sm is state.Manager, unrelated to
	// its OWN independent "swamped" mood threshold (cfg.SwampedAt above).
	pusher.SetState(sm)

	// onAgentUpsert/onAgentDelete (ct-2026-07-29, agentes paso 1): the ONE
	// hot-reload effect a secondary agent's credentials changing (or the
	// agent disappearing) has on the live pusher — shared verbatim by
	// mcpserver.Deps (register_agent/set_agent_capi, M1) and restapi.Deps
	// (POST /api/admin/agent-create|update|delete, new). Two entry points
	// into the same store write + the same in-memory effect; the closure
	// itself isn't duplicated.
	onAgentUpsert := func(agentID, endpoint, terminalID, pinpass string) {
		pusher.RegisterInjector(agentID, capipush.NewCleverInjector(endpoint, terminalID, pinpass))
	}
	onAgentDelete := func(agentID string) {
		pusher.UnregisterInjector(agentID)
	}

	// trayLangChanged (T153 etapa 3c, ct-2026-09-16-1854): POST
	// /api/admin/language's only way to reach the tray menu, built once
	// inside systray.Run below. Buffered 1, non-blocking send — Opciones'
	// language change is a rare human click, not a hot path. The only way
	// to lose an update is TWO changes arriving before the tray drains the
	// first one; the tray still ends up on the first of the two, one step
	// behind Opciones until the next change — never stuck on the ORIGINAL
	// language, and never a blocked HTTP handler.
	trayLangChanged := make(chan i18n.Lang, 1)
	onLanguageChanged := func(lang i18n.Lang) {
		select {
		case trayLangChanged <- lang:
		default:
		}
	}

	// sendToBossAntenna (T77, ct-2026-08-27-1753) backs send_to_boss's
	// optional ephemeral-antenna attach: builds a real injector from the
	// caller-supplied credentials, pings it for real (bounded — never
	// blocks the send), and registers it as termID's reply target with a
	// TTL regardless of the ping result (a known-down destination still
	// gets a real, useful notice on a later cited reply, via the SAME
	// channel-down machinery a permanent agent's outage already uses —
	// see capipush.RegisterEphemeralInjector's own doc). Mirrors
	// onAgentUpsert's shape (build+register), the only new piece is the
	// bounded ping deciding the header.
	//
	// SSRF guard (background security review, ct-2026-08-27): endpoint is
	// caller-supplied by ANY MCP-key-holding agent — without this check, a
	// hostile URL gets pinged (and, on a 200, registered as a live dispatch
	// target for EphemeralAgentTTL) straight from the gateway's own network
	// position. Same invariant as the principal's own endpoint ("never a
	// public address" — store.IsAllowedPrincipalEndpoint, agents.go), same
	// function, checked BEFORE any injector is built — a rejected endpoint
	// never gets pinged and never gets registered, not even on a "fail
	// closed to ❌" basis.
	sendToBossAntenna := func(termID, endpoint, antennaTerminalID, pinpass string) bool {
		if allowed, host, err := store.IsAllowedPrincipalEndpoint(endpoint); !allowed {
			log.Printf("send_to_boss: antenna endpoint rejected for %s: %q (%v)", termID, host, err)
			return false
		}
		inj := capipush.NewCleverInjector(endpoint, antennaTerminalID, pinpass)
		pingErr := capipush.PingWithTimeout(inj, capipush.SendToBossPingTimeout)
		pusher.RegisterEphemeralInjector(termID, inj, capipush.EphemeralAgentTTL)
		return pingErr == nil
	}

	// pingAgent (T97, ct-2026-08-29) backs agentTracker's sweep: before
	// clearing AgentConnected for a terminal with no recent MCP calls,
	// probe its REAL registered injector (pusher.InjectorFor, same map
	// RegisterInjector/OnAgentUpsert already populate) rather than assume
	// silence means gone.
	//
	// SILENT on purpose (Citrino's own correction after auditing the first
	// draft, which used PingWithTimeout — that injects a REAL visible
	// message via Inject/postMessage; correct for T77's send_to_boss,
	// where the boss WANTS to see the test ping while configuring an
	// antenna, wrong here — an agent that's alive but just not calling
	// piumy tools would get one of these into its own context roughly
	// every idleAfter, ~240/day at the default 120s — the exact contract
	// this fixes exists to stop bothering that same agent, not to bother
	// it more quietly disguised as a fix). TestHandshake (clever_injector.go)
	// already IS the silent probe piumy needed — negotiates a handshake,
	// discards the session, no postMessage — the SAME method the
	// dashboard's "probar conexión" button already calls
	// (restapi.CAPIConnector). No new Probe() method needed.
	//
	// The type assertion is the "no puedo verificar" fallback Citrino
	// asked for: LogInjector/FileInjector don't implement TestHandshake,
	// so an unconfigured/fallback injector returns false here exactly like
	// "no injector at all" does. Bounded to the same 4s as T77's own ping
	// (SendToBossPingTimeout) — same goroutine+timeout shape as
	// PingWithTimeout, just wrapping TestHandshake instead of Inject;
	// PingWithTimeout itself stays untouched, T77 still needs it visible.
	pingAgent := func(terminalID string) bool {
		inj, ok := pusher.InjectorFor(terminalID)
		if !ok {
			return false
		}
		prober, ok := inj.(interface{ TestHandshake() error })
		if !ok {
			return false
		}
		done := make(chan error, 1)
		go func() { done <- prober.TestHandshake() }()
		select {
		case err := <-done:
			return err == nil
		case <-time.After(capipush.SendToBossPingTimeout):
			return false
		}
	}

	// MCP server (23+ tools + gate + gating por nivel).
	guard := mcpguard.New(mcpguard.Config{
		RatePerMin:     cfg.MCPGuardRatePerMin,
		EmitRatePerMin: cfg.MCPGuardEmitRatePerMin,
		BlockThreshold: cfg.MCPGuardBlockThreshold,
		BlockCooldown:  cfg.MCPGuardBlockCooldown,
	})
	mcpSrv := mcpserver.New(ctx, mcpserver.Deps{
		Store: s, State: sm, Router: rt,
		ReadMarker: ctrl,
		// MediaDir (T122): same directory the adapter downloads inbound
		// media into and restapi's own Deps.MediaDir already resets —
		// send_message saves an outbound photo here before enqueueing.
		MediaDir:   cfg.MediaDir,
		PolicyPath: cfg.PolicyPath,
		Guard:      guard,
		Gate:       gate,
		Governor:   gov,
		// Gateway: send_message's H6 hardening (ct-2026-07-10-0540) refuses
		// outright while disconnected instead of silently enqueueing.
		Gateway: gw,
		// GroupProfile: gw (*whatsmeow.Adapter) satisfies mcpserver's
		// GroupProfile interface directly — the 5 group/profile boss-only
		// tools call CreateGroup/AddParticipant/SetGroupPhoto/
		// SetGroupDescription/SetProfileStatus (ST-E, ct-2026-07-11-1444).
		GroupProfile:        gw,
		ClaimTTLDefault:     5 * time.Minute,
		MCPAuthConfigured:   cfg.MCPKey != "",
		PrincipalTerminalID: cfg.DefaultTerminalID,
		// Connector: set_capi_connector (ct-2026-07-18-1638) reconfigures the
		// same live injector restapi's Connector field already updates.
		Connector: cleverInj,
		// OnAgentUpsert wires a new/updated CleverInjector into the pusher
		// in caliente whenever register_agent or set_agent_capi succeeds.
		OnAgentUpsert: onAgentUpsert,
		// OnAgentDelete (ct-2026-07-29, agentes paso 3): the SAME closure
		// restapi.Deps uses below — delete_agent (MCP) unregisters the live
		// injector exactly like POST /api/admin/agent-delete already does.
		OnAgentDelete: onAgentDelete,
		// SendToBossAntenna (T77): send_to_boss's optional ephemeral-antenna
		// attach, see the closure's own doc above.
		SendToBossAntenna: sendToBossAntenna,
		// PingAgent (T97): agentTracker's pre-idle verification, see the
		// closure's own doc above.
		PingAgent: pingAgent,
		// Bus (T16, ct-2026-08-05-123257): a "draft" event nudges the
		// dashboard's SSE auto-refresh when the boss resolves a draft via
		// MCP (e.g. "aprobá los pendientes") — same bus corepipeline
		// publishes "message"/etc. on.
		Bus: bus,
	})

	// ponytail: internal/autoreply + internal/bridge quedan sin cablear acá
	// a propósito (T5, ct-2026-08-05-0311) — con 'auto' entrando ahora al
	// mismo despacho cAPI que 'dedicated' (store.PendingDedicated), dejar
	// este worker corriendo también duplicaría la respuesta a un mismo
	// mensaje (uno por el bridge, otro por el agente). Ninguno de los dos
	// paquetes se borró — si el auto-reply por IA propia vuelve, es
	// decisión del boss, y esto es lo único que hay que recablear acá.

	// backup cifrado del propio store.db — INERTE sin PIUMY_BACKUP_KEY.
	bk := sessionbackup.New(sessionbackup.Config{
		SessionDBPath: cfg.DBPath,
		Key:           cfg.BackupKey,
		Dir:           cfg.BackupDir,
		Keep:          cfg.BackupKeep,
		Interval:      cfg.BackupInterval,
	})

	// First-time login: whatsmeow.Adapter.QRChannel emits pairing codes
	// until the owner scans one (or the session is already paired, in
	// which case it emits nothing and closes immediately). Must be called
	// before ctrl.Start() (which calls gw.Start(), the one that actually
	// drives the QR/Connect loop — see internal/whatsmeow.Adapter.Start).
	if qrChan, err := gw.QRChannel(ctx); err != nil {
		log.Printf("whatsmeow: QR channel: %v", err)
	} else if qrChan != nil {
		go func() {
			for code := range qrChan {
				log.Println("whatsmeow: scan this QR (WhatsApp -> Linked Devices):")
				qrterminal.GenerateHalfBlock(code, qrterminal.L, os.Stdout)
				// GET /api/qr (ct-2026-07-10-2312) reads this — the dashboard's
				// own QR display, alongside the terminal ASCII one above.
				// Mood="qr" (ct-2026-07-19-1735, S1f) drives the dashboard's
				// carita + gates the admin panel behind the pairing screen
				// until whatsmeow.Adapter.clearErrorState resets it on connect.
				_ = sm.Update(func(st *state.Status) {
					st.ShowQR = true
					st.QRData = code
					st.Mood = "qr"
				})
			}
		}()
	}

	if err := ctrl.Start(); err != nil {
		log.Fatalf("corepipeline: start: %v", err)
	}
	// pusher/backup both touch the store and both return on ctx.Done() —
	// join them (same discipline as corepipeline.Run's own WaitGroup for
	// its loops) so none is mid-store-op when the deferred s.Close() runs.
	// Without this, a sweep/backup in flight at shutdown races Close
	// ("database is closed"); harmless-but-logged under the MVP config
	// (backup off), a real race under load.
	var bg sync.WaitGroup
	for _, run := range []func(context.Context){pusher.Run, bk.RunPeriodic} {
		bg.Add(1)
		go func(r func(context.Context)) { defer bg.Done(); r(ctx) }(run)
	}

	mcpTransport := server.NewStreamableHTTPServer(mcpSrv,
		server.WithHTTPContextFunc(mcpserver.ExtractTerminalID),
		server.WithEndpointPath("/mcp"))
	mcpHTTP := &http.Server{Handler: mcpserver.RequireBearerToken(cfg.MCPKey, mcpTransport)}
	restHTTP := &http.Server{Handler: restapi.NewMux(restapi.Deps{
		Bus: bus, Store: s, Governor: gov, State: sm, Router: rt, APIKey: cfg.RESTKey, Connector: cleverInj, Backup: bk,
		// MediaFetcher: on-demand media FIFO backfill (ct-2026-07-21-1358) —
		// gw (whatsmeow.Adapter) implements restapi.MediaFetcher.
		MediaFetcher: gw,
		// LIDResolver: Contactos tab @lid/número dedup (ct-2026-07-21-1809) —
		// gw also implements restapi.LIDResolver (same ResolvePN capipush uses).
		LIDResolver: gw,
		// Avatars: paced on-demand profile-picture check (T17 Parte 3,
		// ct-2026-08-05-1240) — gw also implements restapi.AvatarRequester.
		Avatars: gw,
		// OnLanguageChanged (T153 etapa 3c, ct-2026-09-16-1854): the tray
		// menu's only way to hear about a language change from Opciones.
		OnLanguageChanged: onLanguageChanged,
		SMTP:              restapi.SMTPConfig{Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser, Pass: cfg.SMTPPass, From: cfg.SMTPFrom},
		// PrincipalTerminalID: same identity mcpserver.Deps already uses
		// below (M1, ct-2026-07-22-1301) — GET /api/agents synthesizes the
		// principal's row from it + the KV capi-connector settings.
		PrincipalTerminalID: cfg.DefaultTerminalID,
		// DefaultDispatchDebounce/DefaultMaxDispatchDebounce (T90,
		// ct-2026-08-28-1350) — the SAME values already threaded into
		// pusher's capipush.Config below, reused as the tablero's
		// "not yet touched from here" fallback (see restapi.Deps' own doc).
		DefaultDispatchDebounce:    cfg.DispatchDebounce,
		DefaultMaxDispatchDebounce: cfg.MaxDispatchDebounce,
		// Injectors: pusher's own injectors map (RegisterInjector/
		// OnAgentUpsert below already keep it current) — the per-agent ping
		// (M2, ct-2026-07-22-1301) resolves THAT agent's injector through
		// this instead of the antena modal's single Connector.
		Injectors: pusherInjectorResolver{pusher},
		// OnAgentUpsert/OnAgentDelete (ct-2026-07-29, agentes paso 1): the
		// SAME closures mcpserver.Deps uses above — POST /api/admin/
		// agent-create|update|delete hot-reload the live pusher exactly
		// like register_agent/set_agent_capi already do.
		OnAgentUpsert: onAgentUpsert,
		OnAgentDelete: onAgentDelete,
		// Resetter/MediaDir: D4's "partir de 0" reset (ct-2026-07-22-2100) —
		// gw (whatsmeow.Adapter) implements restapi.Resetter (KickResync);
		// MediaDir is the same directory the adapter itself downloads into
		// (cfg.MediaDir, PIUMY_MEDIA_DIR).
		Resetter: gw,
		MediaDir: cfg.MediaDir,
		// Disconnecter: the dashboard's "Desconectar" button (M3,
		// ct-2026-07-22-2342) — gw also implements restapi.Disconnecter
		// (Logout).
		Disconnecter: gw,
		// HistorySyncProgress: the post-re-pareo push visibility signal
		// (items 1+2, ct-2026-07-23-0047) — gw also implements
		// restapi.HistorySyncProgress (HistorySyncStatus).
		HistorySyncProgress: gw,
		// Reconnecter: the dashboard's "Ver QR / Reconectar" button (Fix 2,
		// ct-2026-07-23-0047) — gw also implements restapi.Reconnecter
		// (Reconnect).
		Reconnecter: gw,
		QRStatus:    gw,
		// ProfileStatus: the dashboard's "Estado (WhatsApp)" field (T92) —
		// gw also implements restapi.ProfileStatusSetter (SetProfileStatus),
		// the SAME method set_profile_status (MCP, below) already calls.
		ProfileStatus: gw,
		// ProfilePhoto: the dashboard's hero "Editar" modal (T112c) — gw
		// also implements restapi.ProfilePhoto (SetProfilePhoto), the SAME
		// method set_profile_photo (MCP, T111, below) already calls.
		ProfilePhoto: gw,
	})}

	go func() {
		if err := mcpHTTP.Serve(mcpLn); err != nil && err != http.ErrServerClosed {
			log.Printf("mcp http: %v", err)
		}
	}()
	go func() {
		if err := restHTTP.Serve(restLn); err != nil && err != http.ErrServerClosed {
			log.Printf("rest http: %v", err)
		}
	}()

	log.Printf("piumy-gateway up — mcp=%s rest=%s", mcpAddr, restAddr)
	// Windows: shows a tray icon (F3, ct-2026-07-10-2312) and blocks until
	// "Salir" or Ctrl+C; every other platform: unchanged, just waits for
	// ctx.Done() (see tray_windows.go / tray_other.go).
	//
	// T153 etapa 3c (ct-2026-09-16-1854): the startup value, resolved once,
	// same rule as restapi.effectiveLang/capipush's Pusher.lang() (KVGet
	// the manual override, i18n.Resolve falls back to the OS locale).
	// trayLangChanged (above) is what keeps the menu in sync after that —
	// Opciones' language change doesn't wait for a restart.
	trayRaw, _ := s.KVGet(store.SettingLanguage)
	runTrayOrWait(ctx, stop, "http://localhost:"+restPort+"/dashboard", i18n.Resolve(trayRaw), trayLangChanged)
	log.Println("piumy-gateway shutting down")

	// Orden de apagado: dejar de aceptar tráfico nuevo -> drenar el
	// pipeline -> recién ahí el store se cierra (defer). ctrl.Stop() espera
	// a que el goroutine del pipeline retorne antes de volver, así ninguna
	// goroutine del pipeline toca el store después de Close().
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := mcpHTTP.Shutdown(shutdownCtx); err != nil {
		log.Printf("mcp http shutdown: %v", err)
	}
	if err := restHTTP.Shutdown(shutdownCtx); err != nil {
		log.Printf("rest http shutdown: %v", err)
	}
	ctrl.Stop()
	bg.Wait() // pusher/backup fully returned -> safe for defer s.Close()
	log.Println("piumy-gateway stopped")
}
