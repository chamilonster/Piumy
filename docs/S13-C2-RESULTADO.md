# S13 Partes C-2/C-3 — resultado de la fusión de identidad `@lid`/número

No salió limpio de una. Este documento cubre las dos corridas: la que
abortó (C-2, ct-2026-07-30-2238) y la que terminó limpia después del
arreglo (C-3, ct-2026-07-31-0136).

Línea base (C-1, `docs/S13-C1-BASELINE.md`): chats 1797, messages 1827,
outbox 88, media 415, drafts 3, usage 11 filas (suma out_chars=22349
in_chars=40970 images=0 audio=0 messages=669 tokens_real=0). Pares
`@lid`/número detectados entonces: 559 (400 con contenido).

## Corrida 1 (C-2, 2026-07-30 21:18:28) — abortó a mitad de camino

`identity_auto_reconcile` se activó con una escritura `kv` directa (no
existe endpoint REST/MCP para esto — confirmado por grep en
`internal/restapi`/`internal/mcpserver`). El sweep lee el setting en cada
vuelta del ticker (no solo al boot), pero el ticker mismo es fijo en 1h
desde el arranque y no dispara inmediato — el primer tick útil llegó a las
21:18:28, casi 4h después del boot del binario entonces vivo (15:18:26).

Procesó **422 fusiones** (`chats` 1797→1375) y **abortó** con:

```
UNIQUE constraint failed: messages.chat_jid, messages.id
```

**Causa raíz, confirmada leyendo los datos, no supuesta:** el propio chat
Note-to-Self del gateway (par número/`@lid`) tenía dos mensajes (`3EB0087A501850A16379A3`,
`3EB0C6CE4662068D76DB84`, ambos `from_me=1`, mismo `ts` en los dos lados)
grabados con el MISMO `id` bajo ambas identidades — WhatsApp entrega el eco
de un mensaje que uno se manda a sí mismo por las dos formas de dirección,
y el gateway los guardó dos veces. El `UPDATE messages SET chat_jid=...`
del rekey chocaba contra la PK `(chat_jid, id)`.

Antes de este error, `ReconcileIdentities` abortaba el barrido ENTERO al
primer fallo — un defecto de diseño, no del dato: dejaba 138 pares sin
tocar y sin ninguna señal salvo el error en el log.

Invariantes de esa corrida: `outbox` 88, `media` 415, `drafts` 3
idénticos; `messages` 1827→1828 (+1, tráfico orgánico real, el gateway
seguía vivo); `usage` 11→10 filas con la suma intacta. `is_boss` ya
mostraba 2 (el par del boss se había fusionado antes de llegar al par que
rompió todo).

## Arreglo (C-3)

Dos cambios en `internal/store/reconcile.go` (commit `228a405`,
mergeado a `master` por Citrino, fast-forward):

1. **`dedupeBeforeRekey`** — antes del `UPDATE...SET chat_jid`, en la
   MISMA transacción, borra del lado `@lid` las filas de `messages`/`media`
   (las dos únicas tablas del rekey con PK compuesta que incluye
   `chat_jid`; `outbox`/`drafts` usan PK autoincrement propia, sin riesgo)
   cuyo `id`/`msg_id` ya exista del lado número. Cuenta y reporta cuántas
   por par (`ReconcileOutcome.Deduped`) — nada silencioso. Se descartaron a
   propósito `UPDATE OR IGNORE` (pierde la fila colisionante en silencio) y
   saltar Note-to-Self por JID (tapa el caso, la clase de bug sigue viva).
2. **Un par malo no frena el barrido** — cada par corre en su propia
   transacción; una falla hace rollback SOLO de ese par y queda registrada
   como `ReconcileOutcome{Action:"failed", Reason}`. `ReconcileIdentities`
   cambió de firma: `([]ReconcileOutcome, error)` en vez de
   `(merged, renamed int, err error)`. El `error` de retorno queda
   reservado para fallas de infraestructura real (ni poder leer `chats`).

Test de regresión (`TestReconcileIdentitiesDedupesEchoedMessageBeforeRekey`
+ el equivalente para `media`): reproduce el caso real exacto (mismo
id/ts/from_me bajo las dos identidades, más un mensaje distinto que sí
debe sobrevivir) y confirma fusión sin error, cero huérfanos, conteo final
correcto (el duplicado se dedupea, no se suma).

**Deuda conocida, palabras de Citrino:** el camino "un par falla y el
barrido sigue" no tiene test sintético (fabricar un fallo artificial que no
representa nada real se descartó a propósito) y, como esta corrida real
terminó con `failed=0`, **ese camino tampoco se ejerció en producción**.
Sigue sin estar probado ni en test ni en producción — no se sabrá si
funciona hasta que un día falle de verdad.

Despliegue: rebuild sobre el mismo binario
(`worktrees/citrino/piumy-gateway-dash.exe`, mismos flags —
`CGO_ENABLED=0 GOOS=windows GOARCH=amd64 -ldflags="-H windowsgui"`,
confirmados con `go version -m` contra el binario anterior) + `rl.bat`.
Nuevo boot: 2026-07-30 21:49:24 (PID 7992). El relanzamiento reinicia el
ticker — se perdió el tick de las 22:18, el próximo natural pasó a ser
~22:49:24.

## Corrida 2 (C-3, 2026-07-30 22:49:25) — limpia

```
whatsmeow: identity reconcile sweep: merged=159 renamed=17 deduped=2 failed=0
```

Estado final (`piumy.db`, solo lectura):

| tabla | C-1 (línea base) | tras C-2 (abortada) | tras C-3 (limpia) |
|---|---|---|---|
| chats | 1797 | 1375 | 1234 |
| messages | 1827 | 1828 | 1826 |
| outbox | 88 | 88 | 88 |
| media | 415 | 415 | 415 |
| drafts | 3 | 3 | 3 |
| usage (filas) | 11 | 10 | 9 |
| usage (suma out/in/img/audio/msg/tokens) | 22349/40970/0/0/669/0 | igual | igual |

- `chats` bajó de 1375 a 1234 (−141), no exactamente −159 (los `merged` del
  log): en la hora de espera entre el relanzamiento y el tick el gateway
  siguió vivo y sumó ~18 chats nuevos orgánicos, que compensan parte de la
  baja. Consistente con un sistema en producción, no un desvío.
- `messages` bajó de 1828 a 1826 (−2), **exactamente** el `deduped=2` del
  log — los dos mensajes eco de Note-to-Self, correctamente eliminados una
  sola vez, no una pérdida.
- `usage`: 10→9 filas, suma bit-a-bit idéntica a la línea base — otra
  colisión de PK `(chat_jid, day)` resuelta por `mergeUsageRows`, como
  siempre.
- **Pares `@lid`/número restantes: 0.** `failed=0` — ningún par quedó sin
  procesar ni con error.
- **`is_boss`: 2 filas**, como se esperaba — los dos números del boss
  (el principal y el de Note-to-Self). Los dos siguen siendo boss,
  decisión explícita del boss, no tocada.

## Muestreo de política — 5 pares, confirmado leyendo el contenido real

| par | resultado |
|---|---|
| Boss (`@lid` ↔ número) | `@lid` ya no existe. `rules` = "ser claro y consiso" (la del número — ganó, se descartaron las reglas ricas del `@lid`, tal cual decidido). 262 mensajes = 214+48 de C-1. |
| Note-to-Self (`@lid` ↔ número) | `@lid` ya no existe. `rules` = "ser claro y consiso " (la del número). 17 mensajes = 14 (número) + 5 (`@lid`) − 2 (deduped) — cuadra exacto. |
| Contacto Uno (`@lid` ↔ número) | `@lid` ya no existe (no llegó a procesarse en C-2, sí en C-3). 24 mensajes = 21+3 de C-1 — cuadra exacto. `rules` quedó la del número (por construcción de `mergeChat`, que nunca toca `rules` del lado número — verificado también por el test de regresión). |
| Contacto Dos (`@lid` ↔ número) | par sin config en ningún lado — fusionado limpio, 0 mensajes en ambos, caso simple. |
| Contacto Tres (`@lid` ↔ número) | par sin config — fusionado, 1 mensaje conservado. |

## Criterio de listo — checklist

- [x] Build/vet/test verde (`go build ./...`, `go vet ./...`, `go test ./...`).
- [x] Arreglo puesto y desplegado (commit `228a405`, rebuild + relanzamiento).
- [x] Tick natural esperado, no forzado.
- [x] 139 pares restantes de C-2 procesados (0 pares sin fusionar al final).
- [x] Invariantes verificadas contra la línea base, con la salvedad de
      `messages` (puede subir por tráfico real, nunca bajar por pérdida —
      acá bajó 2 y está explicado exacto por el dedupe, no por pérdida).
- [x] Muestreo de 5 pares, incluido Contacto Uno, confirmado leyendo contenido real.
- [x] `is_boss` = 2, los dos números del boss.
- [ ] Deuda conocida: el camino "un par falla" de C-3 sigue sin ejercerse
      (ni en test ni en producción) — anotado arriba, no resuelto acá.

Footer: `Contract: ct-2026-07-30-0308-reparación-del-canal-agente-gateway-hall`
