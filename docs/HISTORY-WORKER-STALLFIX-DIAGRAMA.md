# Worker de historial — fix del atasco (ct-2026-07-22-0114)

> **HISTÓRICO — el worker on-demand que describe este doc fue BORRADO
> (ct-2026-07-24-2004).** Ver `docs/HISTORY-SYNC-REGRESION-2026-07-24.md`
> para el porqué. Queda como registro de diseño, no como referencia de
> código vigente.

Contrato: excluir el self-chat + guard de no-atasco generalizado. La
comprobación empírica de ct-2120 confirmó el criterio "loaded"
(`historyPageIsFinal`) contra tráfico real, pero destapó 2 bugs de atasco
— ver contrato para el detalle empírico (self-chat clavado 15+ min, 2
páginas idénticas 1s aparte).

## 1. Causa raíz

`NextHistoryChat` prioriza `downloading` sobre `pending` (a propósito —
termina un chat antes de empezar otro, ct-2120 frente 2). Pero un chat
`downloading` que **nunca contesta** el ON_DEMAND (el self-chat, o
cualquier chat cuyo teléfono se durmió a mitad) queda eligible para
siempre — el worker lo re-elige cada `historyPageDelay` (5-15s) sin límite,
bloqueando el avance a los demás chats. La única señal de "contestó" hoy es
`updateHistoryState` (async, vía `handleHistorySync`) — pero nada trackea
"pedí y no me contestaron", así que no hay forma de distinguir "recién pedí,
puede llegar en cualquier momento" de "esto está mudo, hace rato".

## 2. Diseño: pedido → espera con timeout → reintento con tope

```mermaid
flowchart TD
    Start["requestNextHistoryPage\n(loop interno, igual que ct-2120)"]
    Next["store.NextHistoryChat(ownChatJID)\nexcluye self-chat + loaded + skipped"]
    Start --> Next
    Next -->|nada pendiente| RetNone1["return \"\""]
    Next -->|chat elegido: jid| Anchor["store.OldestMessage(jid)"]

    Anchor -->|sin ancla, 0 mensajes| MarkLoaded["SetHistoryState(jid, loaded)"]
    MarkLoaded --> Next

    Anchor -->|con ancla| Status["store.HistoryRequestStatus(jid)\n-> requestedAt, attempts"]

    Status -->|requestedAt>0 Y no venció el timeout| Wait["return \"\"\n(ya hay un pedido en vuelo, esperar)"]
    Status -->|attempts ya excede el tope aleatorio| GiveUp["SetHistoryState(jid, skipped)\n(chat mudo, no bloquea más)"]
    GiveUp --> Next
    Status -->|nunca pedido, o venció el timeout bajo el tope| Send["SetHistoryState(jid, downloading)\nMarkHistoryRequested(jid, now) -> attempts++\nBuildHistorySyncRequest + SendPeerMessage"]
    Send --> RetJID["return jid"]
```

```mermaid
sequenceDiagram
    participant Loop as historyWorkerLoop
    participant Req as requestNextHistoryPage
    participant Phone as teléfono del dueño (ON_DEMAND)
    participant Evt as handleHistorySync (async)

    Loop->>Req: tick (historyPageDelay corto si mismo chat, historySyncDelay largo si no)
    Req->>Req: HistoryRequestStatus(jid) -- sin pedido en vuelo
    Req->>Phone: SendPeerMessage (anchor=oldest) + MarkHistoryRequested
    Note over Req: attempts=1, requestedAt=now

    alt responde rápido (caso normal, ~1s)
        Phone-->>Evt: HistorySync ON_DEMAND
        Evt->>Evt: updateHistoryState -> ClearHistoryRequestPending(jid)
        Note over Evt: attempts=0, requestedAt=0 -- listo para la\nsiguiente página del MISMO chat sin esperar el timeout
    else no responde (self-chat, o se durmió)
        Loop->>Req: tick siguiente (mismo jid, historyPageDelay corto)
        Req->>Req: HistoryRequestStatus -- requestedAt reciente, NO venció el timeout
        Req-->>Loop: return "" (no re-pide, solo espera)
        Note over Req: se repite hasta que venza historyResponseTimeout (30-60s, random)
        Loop->>Req: tick tras vencer el timeout
        Req->>Req: attempts=1, no excede el tope -> reintenta
        Req->>Phone: SendPeerMessage (mismo anchor) + MarkHistoryRequested
        Note over Req: attempts=2
        Note over Req: ... se repite hasta exceder el tope aleatorio (3-6)
        Req->>Req: SetHistoryState(jid, skipped)
        Note over Req: el worker sigue con los demás chats
    end
```

## 3. Piezas nuevas (store)

- `chats.history_requested_at` (INTEGER, unix ts, 0 = sin pedido en vuelo) +
  `chats.history_request_attempts` (INTEGER, pedidos consecutivos sin
  respuesta) — **distinto** del belt existente `history_empty_pages`
  (cuenta páginas RECIBIDAS vacías, no pedidos SIN respuesta).
- `HistoryRequestStatus(jid)` — lee ambos.
- `MarkHistoryRequested(jid, at)` — pisa `requested_at`, `attempts++`.
- `ClearHistoryRequestPending(jid)` — vuelve ambos a 0, llamado desde
  `updateHistoryState` SIEMPRE que llega una respuesta (cualquier
  `transferType`/`pageLen`) — es la señal "este chat sí contesta".
- `NextHistoryChat(excludeJID)` — nuevo parámetro (antes sin argumentos);
  excluye también el estado `skipped` además de `loaded`.
- Nuevo estado `skipped` en el enum de 3 valores → 4 (`validHistoryStates`).
  `HistorySummary` cuenta `skipped` junto con `loaded` como "resuelto" (el
  worker no va a volver a intentarlo, aunque no haya bajado nada real).

## 4. Piezas nuevas (whatsmeow)

- `(*Adapter) ownChatJID() string` — `client.Store.ID.ToNonAD().String()`
  (sin sufijo de device, mismo formato que `chats.jid`), `""` nil-safe
  antes de parear.
- `historyResponseTimeout` (`governor.DelayWindow`, 30-60s) — ventana
  aleatoria antes de considerar un pedido sin respuesta.
- `historyRequestMaxAttemptsMin/Max` (3-6) — tope aleatorio de reintentos
  antes de `skipped`, re-rolleado en cada chequeo (mismo patrón que
  `nextHistoryPageSize`, sin necesidad de persistir el tope elegido).

## Judgment calls

1. **`skipped` en vez de reusar `loaded`** — más honesto (el chat NO se
   backfilleó, solo se dejó de intentar); costó 1 valor de enum + 1 rama en
   `HistorySummary`/`NextHistoryChat`. El dashboard (`HISTORY_BADGES` en
   `app.js`) no tiene ícono para `skipped` todavía — degrada a "sin ícono",
   igual que `pending` (no rompe nada), pero no comunica el caso
   distintamente. Fuera de scope de este contrato (backend/worker); lo
   flag para decisión de Citrino/boss si se quiere un ícono propio.
2. **Tope de reintentos re-rolleado en cada chequeo, no persistido por
   chat** — evita una columna nueva solo para el tope; una vez que
   `attempts` supera el máximo (6), el chequeo siempre da `true` sin
   importar el roll — el random solo varía EN QUÉ intento puntual (3 a 6)
   se da por vencido, que es justo el efecto anti-fingerprint buscado.
3. **El timeout NO se persiste como deadline fijo** — se re-calcula
   (`historyResponseTimeout.Random()`) contra `requestedAt` en cada
   chequeo. Mismo razonamiento que el punto 2: simple, sin estado extra,
   el jitter cerca del borde es aceptable (deseado).
4. **dflux_resolve no pudo anclar los símbolos Go existentes** (todo
   devuelve `to-build`/`missing-endpoint`, incluso funciones ya mergeadas a
   master) — `dir_map`/`find` tampoco ven `internal/whatsmeow` ni
   `internal/store` (solo `internal/dashboard` aparece indexado), pese a
   `meta()` reportar stack Go sin grammars faltantes. Reindexé antes de
   correrlo. Parece una limitación/gap de indexado Go en este proyecto, no
   algo a arreglar bajo este contrato — flagueado para Citrino.
