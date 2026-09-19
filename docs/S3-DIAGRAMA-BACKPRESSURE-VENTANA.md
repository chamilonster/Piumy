# S3 — Backpressure SwampedAt: el freno que no se soltaba (ct-2026-07-30-030948)

Contrato madre: `ct-2026-07-30-0308-reparación-del-canal-agente-gateway-hall`.
Decisión de diseño ya tomada (opción D) — no reabierta, solo implementada.

## 1. Los tres defectos combinados (contrato)

1. **Contaba deuda histórica, no presión actual** — 76 de 82 mensajes en el
   smoke eran de febrero/marzo, en un chat que nadie iba a contestar.
2. **El agente no se enteraba** — nada le decía que estaba en backpressure.
3. **Umbral clavado en código**, contaba mensajes de TODOS los chats
   (incluido el boss).

## 2. El diseño nuevo

```mermaid
flowchart TD
    Sweep["sweepOnce (cada 5s)"]
    Sweep --> Count["store.CountRecentPendingNonBoss(now - SwampedWindow)\nis_boss=0 AND ts >= ventana"]
    Count --> Cmp{"count >= SwampedAt?\n(ambos leídos de settings,\nfallback en Config)"}
    Cmp -->|"no"| All["Todos los chats pasan\n(comportamiento normal)"]
    Cmp -->|"sí"| Log["logTransition: 1 línea al ENTRAR/SALIR\n(no una por sweep — S1)"]
    Log --> Signal["state.Status.Backpressure = true\nBackpressureReason = \"N >= M, drená a tu ritmo\"\n(señal para el AGENTE, get_status la expone)"]
    Signal --> Loop["Por cada chat due:"]
    Loop --> IsBoss{"c.IsBoss?"}
    IsBoss -->|"sí"| Dispatch["dispatch() normal\n(el chat del boss NUNCA se frena)"]
    IsBoss -->|"no"| Skip["continue — se salta este sweep,\nqueda en PendingDedicated para el próximo"]
```

## 3. Por qué el chat del boss no cuenta ni se frena

`store/pending.go:96`'s `PendingDedicated` ya trata `is_boss=1` como bypass
incondicional del gate `active/status`. `CountRecentPendingNonBoss`
(store, nuevo) reusa el MISMO criterio (`is_boss = 0` en el WHERE) — Citrino
fue explícito: "reusá ese criterio, no inventes otro". El loop de
`sweepOnce` aplica el mismo criterio del otro lado: cuando `swamped`, cada
chat se chequea con `GetChat(chatJID).IsBoss` antes de saltarlo — costo
extra solo mientras el gate está activo (el camino normal, sin swamped, no
paga nada nuevo).

## 4. Settings, no hardcode

`store.SettingCapipushSwampedAt` / `SettingCapipushSwampedWindow` (KV,
`SettingInt`/`SettingDuration` ya genéricos, sin store method nuevo) —
leídos EN VIVO en cada `sweepOnce`, `p.cfg.SwampedAt`/`SwampedWindow` quedan
como fallback de código (igual patrón que `dispatchDelay()`/`readDelay()`
ya usan para `SettingDispatchDelayMin` y compañía).

## 5. Trampa de nombres que casi vuelve a pasar (flagueado, no tocado)

`internal/state` YA tenía su PROPIO `swampedAt` (default 8, mismo número) y
un mood `"swamped"` — pero es un indicador COSMÉTICO de profundidad de cola
(`RestingMood`, la carita del dashboard), sin ventana temporal ni exclusión
de boss, totalmente desconectado del freno real de `capipush`. Coincidencia
de nombre y número, NO el mismo mecanismo — confirmado leyendo
`state.go:129-186` antes de tocar nada. La señal nueva usa campos propios
(`Status.Backpressure`/`BackpressureReason`), no reutiliza `Mood`.

## 6. La señal al agente: `state.Manager`, no un nuevo tool

`get_status` ya embebe `state.Status` completo (`server.go:305-309`) — al
agregar `Backpressure`/`BackpressureReason` ahí, `get_status` los expone
gratis, sin tocar el handler. `capipush` gana un seam opcional
`SetState(sm)` (mismo patrón que `SetReceipter`/`SetLIDResolver`, nil-safe,
cableado una vez en `main.go`) — `internal/state` es un paquete hoja (cero
deps internas), así que no hay ciclo de imports. Alternativa descartada:
que `mcpserver.get_status` consulte `*capipush.Pusher` directamente —
imposible, `capipush` ya importa `mcpserver` (ciclo).

## Judgment calls

1. **`CountRecentPendingNonBoss` es una query nueva**, no una reutilización
   de `CountPendingDedicated` con parámetros — los criterios `is_boss`
   están INVERTIDOS entre las dos (`PendingDedicated`: is_boss=1 SIEMPRE
   pasa; el conteo de backpressure: is_boss=0 SIEMPRE), fusionarlas en una
   sola función con flags habría sido menos legible que dos queries cortas.
2. **El chequeo `GetChat` extra en el loop de `sweepOnce`** solo corre
   mientras `swamped==true` — es una consulta SQLite local por PRIMARY KEY,
   costo despreciable, y evita restructurar `dueChats()`/`dispatch()` para
   cargar `is_boss` de antemano (que ya lo hace `dispatch()` internamente,
   de todos modos).
3. **Fail-open en el conteo si `CountRecentPendingNonBoss` da error** —
   mismo criterio que el chequeo de `DailyQuota` ya usaba (loguea y sigue,
   no bloquea todo por un error transitorio de DB).
4. **`SwampedWindow` default 10 minutos** — juicio propio, no especificado
   en el contrato; documentado en el código, ajustable por settings sin
   rebuild.
