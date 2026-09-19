# S7b — Completar el fix de identidad en la ingesta (ct-2026-07-30-0332)

Contrato madre: `ct-2026-07-30-0308-reparación-del-canal-agente-gateway-hall`.
Reemplaza al S7 original (revocado — estaba planteado como pregunta abierta;
la decisión ya la había tomado el boss).

## 1. El hueco (confirmado)

`ResolvePN` (`whatsmeow/inbound.go`) resuelve @lid→número y la usan el
despacho (`capipush.go`) y la lectura (`restapi/read.go`). La INGESTA
(`inbound.go`, `handleMessage`) no: escribe `chatJID := evt.Info.Chat.String()`
crudo. Consecuencia medida (herramienta read-only, `secrets/investigate-lid-dup/`):
**557 pares duplicados** hoy en la base del boss (398 con nombre/actividad
real, 159 ruido) sobre 1795 chats totales (617 son `@lid`).

## 2. Investigación previa a codear (contrato lo exige)

- **Sender vs Chat**: no necesitan tratamiento independiente más allá de la
  resolución en sí. `messages.sender` es puramente informativo —
  `corepipeline.handleInbound` resuelve TODO (router/rules) por `ChatJID`,
  nunca por `Sender`. Solo `chat_jid` es la clave de identidad.
- **Salientes (`from_me`)**: `outbox.go`'s `sentMessageRow` escribe
  `ChatJID = item.ToJID`, un valor que el agente ya trae resuelto (de
  `get_chat`/dispatch/`list_chats`) — no resuelve nada por su cuenta. Arreglar
  la ingesta alcanza; el saliente hereda la identidad correcta gratis.
- **Commit de referencia `4763063`** (rama vieja `identidad-unificada`, NO
  mergeada): tiene `resolveNumberJID`, aprovechable en su mecanismo de
  resolución — pero es hermano del mismo commit que trae `ReconcileIdentities`
  (F2, el merge de filas) y la jerarquía de nombre (F1) — **ambos cancelados
  por el boss** (`project_identidad_unificada_cancelada`, memoria). Se rescata
  SOLO la resolución de escritura hacia adelante; nunca el merge ni F1.

## 3. El bug que Citrino encontró releyendo whatsmeow (antes de aprobar el rebate)

`SenderAlt` (el campo que whatsmeow ya resuelve gratis, por mensaje, sin
tocar la DB) **nunca se llena cuando el mensaje es `IsFromMe`** —
`message.go`'s `parseMessageSource` solo lo llena en la rama de un inbound
real de otra persona. En la rama `IsFromMe` (línea ~126-144), `Sender` es LA
CUENTA DEL GATEWAY y `Chat` es el destinatario real — el campo que sí se
llena ahí es `RecipientAlt`, no `SenderAlt`. Resolver `Chat` con `SenderAlt`
en un mensaje propio sincronizado desde otro dispositivo escribiría el
número de la CUENTA DEL GATEWAY en la fila de OTRA persona — corrupción de
datos, no solo un chat mal nombrado.

```mermaid
flowchart TD
    Evt["events.Message"]
    Evt --> Grp{"IsGroup?"}
    Grp -->|sí| RawG["Chat sin tocar (@g.us nunca es @lid)"]
    Grp -->|no| Addr{"AddressingMode == LID?"}
    Addr -->|no| RawA["Chat sin tocar (ya viene por número)"]
    Addr -->|sí| FromMe{"IsFromMe?"}
    FromMe -->|sí| RA["alt = RecipientAlt\n(Chat = destinatario, Sender = YO)"]
    FromMe -->|no| SA["alt = SenderAlt\n(Chat == Sender, la otra persona)"]
    RA --> Check{"alt resuelto\n(server=pn, no vacío)?"}
    SA --> Check
    Check -->|sí, gratis| Resolved["Chat = alt.ToNonAD()"]
    Check -->|no| DB["GetPNForLID(Chat)\n(fallback — cacheado en RAM por whatsmeow)"]
    DB -->|resuelto| Resolved
    DB -->|"\"\" (sin mapeo todavía)"| Fallback["Chat = @lid crudo\n(nunca se pierde el mensaje)"]
```

## 4. Por qué no hace falta cache propia (lo que pedía medir el contrato)

`GetPNForLID` es una llamada a whatsmeow en el camino caliente — pero:
1. **La mayoría de los mensajes ya trae el alt resuelto en el propio stanza**
   (`SenderAlt`/`RecipientAlt`, poblado por whatsmeow desde `sender_pn`/
   `sender_lid`/`peer_recipient_pn`, confirmado en `message.go`) — cero
   llamada a DB en ese caso, y de yapa whatsmeow persiste el mapeo solo
   (`StoreLIDPNMapping`).
2. **El fallback ya está cacheado en RAM** por whatsmeow mismo
   (`sqlstore.CachedLIDMap.getLIDMapping` — chequea un mapa en memoria antes
   de tocar `whatsmeow.db`; `cacheFilled` evita la DB del todo una vez
   cargado el mapa completo una vez).

Agregar una capa de cache propia sería una tercera capa sobre dos que ya
existen — descartado (YAGNI).

## 5. Fuera de scope (explícito, no se toca)

- Los 557 pares duplicados existentes — no se migran ni se fusionan. El
  boss decide eso aparte, con OK explícito (Citrino se lo reporta por
  separado).
- Grupos (`@g.us`) — nunca pasan por `AddressingMode == LID`, el gate los
  deja intactos sin necesidad de un chequeo extra.
- `messages.sender` / mensajes salientes — no tocados (ver investigación).
- `ReconcileIdentities` / jerarquía de nombre (F1/F2) — siguen muertos.

## Judgment calls

1. **Un solo helper (`resolveChatJID`)**, no una función separada para el
   caso `from_me` — la rama `if info.IsFromMe { alt = info.RecipientAlt }`
   es la única diferencia real; todo lo demás (gate de grupo, gate de
   addressing mode, fallback a DB, fallback a JID crudo) es idéntico.
2. **El chequeo `alt.Server == types.DefaultUserServer`** se mantiene (igual
   que el commit de referencia) — defensivo, costo cero, evita confiar en
   un alt que por algún motivo no sea un JID de número real.
3. **Test dedicado para `from_me`** (pedido explícito de Citrino): fija
   `RecipientAlt` a un número Y `SenderAlt` a un número DISTINTO a
   propósito — si el código alguna vez usara el campo equivocado, el test
   lo detecta comparando contra el valor incorrecto, no solo confirmando
   que "algo" se resolvió.
