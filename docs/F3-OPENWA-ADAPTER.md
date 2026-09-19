# F3 — Diseño: adaptador open-wa (`internal/openwa`)

Diseño del arquitecto (Citrino). F3 implementa la interfaz **`gateway.Gateway`** (definida en
F2) contra **open-wa** real. open-wa (`@open-wa/wa-automate`) corre como **proceso Node
aparte**; la integración es **REST out + webhook in**. El core no cambia — solo aparece la
primera implementación real del seam. Contrato verificado contra la doc oficial de open-wa
(context7, `/open-wa/wa-automate-nodejs`).

> Regla F2 que sigue rigiendo: el core (corepipeline) NUNCA importa open-wa. Todo lo
> open-wa-específico vive acá. Este es el único paquete que conoce el formato de open-wa.

## Contrato de open-wa (EASY API, verificado)

**Server:** `npx @open-wa/wa-automate -p <PORT> -k <API_KEY>` levanta la EASY API (REST).
El webhook se registra con `registerWebhook(url, events)` (o `-w <url>`), apuntando a
nuestro server; `events = ["onMessage","onStateChanged"]`.

**Outbound (REST → open-wa):** `POST <endpoint>` con header `api_key: <API_KEY>` y body
`{ "method": "sendText", "args": { "to": "<jid>", "content": "<text>" } }`.
Respuesta: el `MessageId` (string) o boolean. Métodos que usamos: `sendText`,
`simulateTyping` (typing on/off), `sendSeen`.

**Inbound (webhook ← open-wa):** open-wa hace `POST` al webhook con el envelope
`EventPayload`:
```json
{ "event": "onMessage", "data": { /* Message */ }, "id": "...", "sessionId": "...", "ts": 123 }
```
- `event == "onMessage"` → `data` es un **Message**. `onMessage` **ya excluye `fromMe`**
  (a diferencia de `onAnyMessage`) — igual, filtrar `data.fromMe` por las dudas.
- `event == "onStateChanged"` → actualizar `Connected` (CONNECTED vs el resto).

**Message (campos que usamos):** `from`/`chatId` (ChatId), `sender`/`author`, `id`
(MessageId, ej. `false_447...@c.us_HEX`), `body` (texto para type chat; open-wa ya
desenvuelve los contenedores — no hace falta el `unwrapMessage` de whatsmeow), `type`, `t`
(timestamp), `notifyName`, `fromMe`, `isGroupMsg`, `caption`/`mimetype` (media, post-MVP).

**JID format (open-wa):** `\d+@c.us` (1-1), `\d+-\d+@g.us` (grupo), `\d+@lid`. **piumy-gateway
adopta el formato de open-wa en todo el sistema** (router.json keyed por `@c.us`, no por el
`@s.whatsapp.net` de whatsmeow). `store.isGroupJID` (`@g.us`) no cambia.

**Receipts:** `sendSeen(chatId)` marca **todo el chat** como visto (ack:3) — es **per-chat,
no per-mensaje**. Nuestro `MarkRead(chatJID, msgIDs)` colapsa a `sendSeen(chatJID)` (los
msgIDs solo determinan el chat). `MarkDelivered` → **no-op** (WhatsApp Web lo hace solo;
open-wa no tiene ack de entrega por mensaje).

## El adaptador (`internal/openwa`) — implementa `gateway.Gateway`

```go
type Adapter struct {
    endpoint string        // PIUMY_OPENWA_ENDPOINT (base URL de la EASY API)
    apiKey   string        // PIUMY_OPENWA_APIKEY (header api_key)
    listen   string        // PIUMY_OPENWA_WEBHOOK_ADDR (donde ESCUCHA nuestro webhook)
    http     *http.Client
    inbound  chan gateway.Inbound
    srv      *http.Server  // el server del webhook
    connected atomic.Bool
}
```
- **`Start(ctx)`**: levanta `srv` (HTTP server que recibe el webhook de open-wa en `listen`);
  registra el webhook en open-wa (`registerWebhook` vía REST, o asumir `-w` ya seteado —
  ver DoD). Idempotente.
- **`Inbound() <-chan gateway.Inbound`**: el canal que alimenta el handler del webhook.
- **webhook handler**: parse `EventPayload` → si `onMessage` y `!data.fromMe`, mapear
  Message → `gateway.Inbound{ChatJID: from, SenderJID: sender/author, MsgID: id, Text: body,
  Type: type, TS: t, PushName: notifyName}` → push al canal (no bloqueante: si el pipeline
  no drena, dropear con log o buffer acotado — decidir en impl). Si `onStateChanged` →
  `connected.Store(...)`.
- **`Send(ctx, to, text)`**: POST sendText con `api_key` → parsear MessageId → `SendResult{MsgID, TS}`.
- **`SetTyping(ctx, to, on)`**: POST `simulateTyping` (best-effort; error solo se loguea).
- **`MarkRead(ctx, chatJID, msgIDs)`**: POST `sendSeen` con `chatId=chatJID`.
- **`MarkDelivered(...)`**: no-op (return nil).
- **`Connected()`**: `connected.Load()` (seteado por onStateChanged; opcional: ping a
  `/getConnectionState` al arrancar para el estado inicial).
- **`Stop()`**: `srv.Shutdown` + `close(inbound)`. **Cerrar `inbound` SOLO acá** (contrato
  del seam — ver nota de robustez de F2 abajo), nunca ante un disconnect transitorio.

## Validación de JID (el core la relajó en F2)

F2 dejó `validJID` = "no vacío" a propósito (el core es agnóstico). **Acá va la validación
de formato real** contra el patrón open-wa `^\d+(-\d+)?@(c|g)\.us$|^\d+@lid$` — antes de
mandar a `Send`, y al parsear inbound. (El otro punto de validación de formato es el gate
duro de `send_message` en F4.)

## Config nueva (`internal/config`, env-only `PIUMY_*`)
- `PIUMY_OPENWA_ENDPOINT` — base URL de la EASY API de open-wa (ej. `http://localhost:8080`).
- `PIUMY_OPENWA_APIKEY` — el `-k` de open-wa (header `api_key`).
- `PIUMY_OPENWA_WEBHOOK_ADDR` — donde escucha nuestro server del webhook (ej. `:8090`).
- (opcional) `PIUMY_OPENWA_WEBHOOK_URL` — la URL que registramos en open-wa si el adaptador hace el `registerWebhook`.

## Notas del audit de F2 que aterrizan acá (F3)
1. **Filtrar `fromMe`** — el pipeline asume Inbound = incoming. El adaptador NO empuja mensajes propios.
2. **Formato de JID** — validarlo acá (el core lo relajó).
3. **Robustez de `Inbound()`** — cerrar el canal SOLO en `Stop()`. Si open-wa se desconecta,
   marcar `connected=false` (el outbox drain frena solo por `Connected()`), pero **NO** cerrar
   el canal (cerrarlo sin cancelar el ctx dejaría el outboxLoop colgado — ver controller.go de F2).
4. **`-race`** — no corre local (sin gcc). Correrlo en Linux/CI antes de F5 (ver memoria del proyecto).

## Testing (sin open-wa real — lo pide MIGRATION-PLAN F3)
- **Outbound:** `httptest.Server` que finge la EASY API de open-wa → asserta que `Send`
  hace el POST correcto (método sendText, header api_key, body {to,content}) y parsea el MessageId;
  `SetTyping`/`MarkRead` idem (simulateTyping/sendSeen).
- **Inbound:** POST-ear un `EventPayload{event:"onMessage", data:Message}` sintético al handler
  del webhook → asserta el `gateway.Inbound` producido en el canal; `fromMe:true` → NO se emite;
  `onStateChanged` → `Connected()` cambia.
- **End-to-end con corepipeline:** el adaptador real + un pipeline real, POST webhook → se guarda
  en store; encolar outbox → el adaptador POST-ea al httptest de open-wa. (Cierra el loop del diseño.)

## DoD de F3
- `internal/openwa.Adapter` implementa `gateway.Gateway` completo. El core sin cambios.
- Inbound (webhook→Inbound, filtra fromMe, onStateChanged→Connected), outbound (sendText/simulateTyping/sendSeen), MarkDelivered no-op, validación de JID.
- Config `PIUMY_OPENWA_*` sumada, env-only.
- Tests con `httptest` fake de open-wa (outbound + inbound + end-to-end con pipeline). [evidencia]
- Media = shim/no-op (Type se guarda; sin descarga — post-MVP).
- `MANUAL.md` actualizado con el nodo `openwa`. Diagrama + dflux. `go build/vet/test` verde. [evidencia]
