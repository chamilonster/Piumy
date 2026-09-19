# F5 — main.go wire + smoke local (cierra el MVP)

Última fase del MVP. Dos entregables: **(1)** `main.go` que cablea todos los
paquetes en un proceso vivo (hoy es el stub F0: solo `config.Load` + `store.Open`),
y **(2)** el **smoke round-trip** con open-wa real corriendo en la PC.

> Diseño preparado por Citrino (leader) mientras F4d está en vuelo. Las piezas
> que dependen de F4d (media dir, quota hook, token seam) están marcadas
> **[F4d]** — se confirman las firmas exactas cuando F4d aterrice y se auditó.
> Todo lo demás (F0–F4c) es superficie estable y no debería driftear.

Regla del proyecto: diagrama → `dflux_resolve` → codear → `ponytail-review` →
`go build/vet/test` verde → `MANUAL.md`.

---

## 1. Orden de composición del wire (`main.go`)

Orden dirigido por dependencias. Constructores verificados contra `MANUAL.md`
y el código actual (master `1ebee1f`).

```
ctx, stop := signal.NotifyContext(ctx.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

cfg  := config.Load()                          // + campos nuevos F5, ver §3
s    := store.Open(cfg.DBPath); defer s.Close()

rt   := router.NewManager(cfg.RouterPath)
gov  := governor.NewLimiter(cfg.RateLimitPerMin, time.Minute)
       gov.SetDailyMax(cfg.RateLimitPerDay)     // cap diario
sm   := state.NewManager(cfg.StatusPath, cfg.SwampedAt)
bus  := eventbus.New()

// gateway (seam F2) — único implementador real: open-wa (F3)
gw   := openwa.New(openwa.Config{
          Endpoint:    cfg.OpenWAEndpoint,
          APIKey:      cfg.OpenWAAPIKey,
          WebhookAddr: cfg.OpenWAWebhookAddr,
          // MediaDir: cfg.MediaDir,            // [F4d] si el adapter ganó el campo
        })

pipe := corepipeline.New(gw, s, rt, gov, sm, corepipeline.Config{
          DispatchDelayMin: cfg.DispatchDelayMin, DispatchDelayMax: cfg.DispatchDelayMax,
          ReadDelayMin: cfg.ReadDelayMin, ReadDelayMax: cfg.ReadDelayMax,
          // OutboxPoll/MaxRetry/Composing: defaults del paquete
        })
pipe.SetBus(bus)
ctrl := corepipeline.NewController(gw, pipe)     // implementa mcpserver.ReadMarker

// despacho cAPI (opción 2)
producer := capi.New(cfg.CAPIKey)
gate     := mcpserver.NewGate()
var inj capipush.Injector = capipush.LogInjector{}   // SEAM CleverCoder — ver §4
pusher   := capipush.New(s, rt, gate, producer, inj, capipush.Config{
              PortFallback: cfg.OpenWAPort,
              // + quota hook [F4d]
            })

// MCP server (23+ tools + gate + gating por nivel)
guard  := mcpguard.New(mcpguard.Config{ /* de cfg.MCPGuard* */ })
mcpSrv := mcpserver.New(ctx, mcpserver.Deps{
            Store: s, State: sm, Router: rt,
            ReadMarker: ctrl,                    // Controller.MarkRead
            PolicyPath: cfg.PolicyPath,          // [F5 config nueva]
            Guard: guard, Gate: gate,            // MISMO gate que pusher
            OpenWA: gw,                          // las 6 tools grupo/perfil
            ClaimTTLDefault: 5*time.Minute,
            MCPAuthConfigured: cfg.MCPKey != "",
          })

// modo auto (bridge→DeepSeek) — INERTE sin PIUMY_BRIDGE=direct-api (NoneBridge)
br := bridge.New(bridge.Config{ /* de cfg.Bridge*/ })
w  := &autoreply.Worker{Store: s, Bridge: br,
        Policy: autoreply.PolicyText(cfg.PolicyPath),
        ModelName: "auto", Interval: cfg.AutoReplyInterval, Delay: cfg.AutoReplyDelay}

// backup cifrado del propio store.db — INERTE sin PIUMY_BACKUP_KEY
bk := sessionbackup.New(sessionbackup.Config{ /* de cfg.Backup* */ })
```

**Levantar y esperar señal (graceful shutdown):**

```
ctrl.Start()                      // loop inbound + drain outbox
go pusher.Run(ctx)                // sweep de despacho cAPI
go w.Run(ctx)                     // auto-mode (inerte por default)
go bk.RunPeriodic(ctx)            // backup periódico (inerte por default)
mcpHTTP  := mcpServerOverHTTP(...) // §2  -> *http.Server
restHTTP := restServerOverHTTP(...)// §2  -> *http.Server
go mcpHTTP.ListenAndServe()
go restHTTP.ListenAndServe()

<-ctx.Done()
// orden de apagado: dejar de aceptar → drenar → cerrar
mcpHTTP.Shutdown(...); restHTTP.Shutdown(...)
ctrl.Stop()                       // para el pipeline (gw.Stop cierra Inbound())
// s.Close() por defer
```

**Nota de convivencia `capipush` vs `autoreply` (verificado, no colisionan):**
`autoreply` solo barre chats `Mode=="auto"` (`worker.go:123`); `capipush` barre
`PendingDedicated` (modo `dedicated`). Modos **disjuntos** por router → nunca
doble-responden. Ambos corren juntos sin conflicto; el router es el switch.

---

## 2. MCP sobre HTTP — el hueco que F5 cierra

Hoy `ExtractTerminalID` está documentado pero **sin montar sobre transporte
HTTP real** (`terminal.go`, F4b: "eso es F5/smoke"). Verificado contra
context7 `/mark3labs/mcp-go` v0.55.1:

- `server.NewStreamableHTTPServer(mcpSrv, opts...)` es el transporte y es un
  `http.Handler` (tiene `ServeHTTP`).
- `server.WithHTTPContextFunc(mcpserver.ExtractTerminalID)` — mcp-go pasa los
  headers HTTP al handler; acá se lee `X-Piumy-Terminal-Id` al contexto (el
  mismo `HTTPContextFunc` que ya escribió F4b).
- Bearer: el `RequireBearerToken(cfg.MCPKey, http.Handler) http.Handler` que ya
  existe (`auth.go`, fail-closed) envuelve el `StreamableHTTPServer`.

```go
mcpTransport := server.NewStreamableHTTPServer(mcpSrv,
    server.WithHTTPContextFunc(mcpserver.ExtractTerminalID),
    server.WithEndpointPath("/mcp"))
mcpHandler := mcpserver.RequireBearerToken(cfg.MCPKey, mcpTransport) // fail-closed
mcpHTTP := &http.Server{Addr: cfg.MCPAddr, Handler: mcpHandler}
```

REST (nudge SSE + admin privilegiado) monta directo su mux:
```go
restHTTP := &http.Server{Addr: cfg.RESTAddr,
    Handler: restapi.NewMux(restapi.Deps{Bus: bus, Store: s, APIKey: cfg.RESTKey})}
```

> Ojo (behavior note de mcp-go): `WithHTTPContextFunc` se honra pero el contexto
> difiere según se entre por `ServeHTTP` (nuestro caso, envuelto) o `Handle`.
> Verificar en el smoke que el header llega al contexto de la tool.

---

## 3. Config nueva de F5 (cero hardcode)

`config.go` hoy NO tiene dirección de escucha para MCP ni REST. F5 agrega
(mismo patrón `env`/`envInt`):

| Campo | Env | Default sugerido |
|---|---|---|
| `MCPAddr` | `PIUMY_MCP_ADDR` | `:8091` |
| `RESTAddr` | `PIUMY_REST_ADDR` | `:8092` |
| `RESTKey` | `PIUMY_REST_KEY` | `""` (vacío = abierto, dev/LAN — a diferencia del MCP fail-closed) |
| `PolicyPath` | `PIUMY_POLICY_PATH` | `""` (cae al `decision-policy.md` embebido) |
| `MediaDir` **[F4d]** | `PIUMY_MEDIA_DIR` | lo define F4d |

(`OpenWAWebhookAddr` ya default `:8090`; con MCP `:8091` y REST `:8092` no
chocan puertos.)

---

## 4. Seams que quedan como stub en F5 (los enchufa CleverCoder afuera)

El smoke corre **con estos stubs** — piumy-gateway es entregable sin el lado
CleverCoder cableado:

1. **Inyección al terminal** (`capipush.Injector`): `LogInjector` loguea el
   ciphertext en vez de inyectarlo. El round-trip verifica hasta el cAPI
   cifrado; el lado agente (descifrar + llamar MCP) lo hace o bien un terminal
   agente real conectado, o se simula descifrando el log con `capi.Decrypt`.
2. **Reporte de tokens** **[F4d]**: hook stub tipo `LogInjector` — corre sin el
   reporte real de CleverCoder.
3. **TTS/STT + voces**: fuera de piumy-gateway (lo maneja CleverCoder, decisión
   del boss). F5 no lo toca.

Skill del agente `/piumy` (comandos en inglés) = capa blanda; el gate duro vive
en código y ya está (F4a/b/c). F5 no la implementa.

---

## 5. Smoke round-trip (DoD central del MVP)

Correr open-wa (`@open-wa/wa-automate`, `-k <apikey> -p <port> -w <webhook-url>`)
+ piumy-gateway en la PC. Camino completo:

```
WhatsApp → open-wa → webhook POST → openwa.Adapter → corepipeline inbound
  → store (TouchChat + AddMessage) → [modo dedicated] capipush sweep
  → gate.RegisterDispatch → capi.Encrypt → Injector (log/agente)
  → [agente] get_instructions → unlock → remember|skip → send_message
  → store.outbox → corepipeline drain (governor pacing) → open-wa sendText
  → WhatsApp (llega la respuesta)
```

**Unknowns de open-wa a verificar contra el proceso REAL** (el adapter F3 los
asumió/derivó; el smoke los confirma — no inventar):

1. Shape del webhook `EventPayload{event,data}`: nombres exactos de campos en
   `onMessage` (id/from/body/type/t/notifyName) y `onStateChanged`.
2. JIDs realmente emitidos (`@c.us`/`@g.us`/`@lid`) matchean el regex del adapter.
3. Respuesta de `sendText` → parsea a `SendResult{MsgID,TS}`.
4. `sendSeen` per-chat (semántica de `MarkRead`).
5. `simulateTyping` acepta nuestros args (`SetTyping`).
6. Webhook pre-registrado vía `-w <url>` (el adapter NO llama `registerWebhook`
   — confirmar que open-wa efectivamente postea).
7. Entrega de media **[F4d]** (base64 / URL / `AUTO_DECRYPT_SAVE` path) — F4d lo
   verifica; re-confirmar acá con media real.

**Además del round-trip:**
- `go test -race` en **Linux/CI** (sin gcc local — memoria `race-detector-needs-linux`).
- **Revisar con el boss la passphrase del backup** = número de teléfono
  (memoria `sessionbackup-passphrase-phone`): la flagueé débil porque `memory`
  guarda secrets. Traerlo al cerrar F5.

---

## 6. Definition of Done (F5)

- `main.go` cablea todo: pipeline + capipush + mcpserver(HTTP) + restapi +
  backup + autoreply, con graceful shutdown por señal. [evidencia: proceso
  levanta y apaga limpio]
- MCP sobre HTTP con `X-Piumy-Terminal-Id` + Bearer fail-closed. [evidencia:
  una tool gateada responde distinto según terminal_id del header]
- Config nueva (`PIUMY_MCP_ADDR`/`REST_ADDR`/`REST_KEY`/`POLICY_PATH`) — cero
  hardcode.
- Smoke round-trip verde con open-wa real; los 7 unknowns confirmados o
  documentados. [evidencia: log del round-trip end-to-end]
- `go build/vet/test` verde + `go test -race` verde en Linux.
- `MANUAL.md` con el nodo `main` (el wire) + diagrama F5 + `dflux`.
- Passphrase del backup revisada con el boss.
```

