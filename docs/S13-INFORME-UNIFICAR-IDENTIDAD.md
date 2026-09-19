# S13 — Informe: fusionar filas vs. unificar en lectura (ct-2026-07-30-1835)

Contrato madre: `ct-2026-07-30-0308-reparación-del-canal-agente-gateway-hall`.
**Primera etapa, sin código** — este documento es el criterio de listo de esa
etapa. Cero líneas de implementación hasta que Citrino/el boss aprueben el
enfoque.

## Recomendación

**Fusionar filas (opción "merge"), reactivando y corrigiendo el
`store.ReconcileIdentities` que ya existe en el historial git — no
reconstruirlo de cero.** Con un agregado sobre el diseño original: engancharlo
para que corra automáticamente cada vez que se aprende un par nuevo, no solo
como backfill único de los 557 pares de hoy.

Las cuatro cosas que el contrato pide que queden ciertas se satisfacen
**permanentemente y sin ambigüedad** con la fusión; con unificar en lectura,
tres de las cuatro necesitan trabajo adicional real (no son gratis), y una
(el `is_boss` disperso) **no se resuelve con lectura en absoluto** — ver
más abajo.

## Por qué NO investigar y asumir — lo que se verificó antes de concluir

Antes de recomendar, se investigó (dos líneas de trabajo en paralelo):
1. **Arqueología git** del intento anterior (F1/F2, cancelado
   `ct-2026-07-18-171940`) — commits exactos, diseño técnico completo de
   `ReconcileIdentities`/`resolveNumberJID`, y la razón real de la
   cancelación.
2. **Relevamiento del código actual** — costo real, archivo por archivo, de
   qué necesitaría tocar "unificar en lectura" para las 4 exigencias del
   contrato.

## Hallazgo clave #1: la cancelación de F1/F2 fue prioridad, no un problema técnico

Verificado en los commits reales (`b87d9e2` F1, `53ed18c` F2, reverts
`e67b834`/`bf6d5af`, todos 2026-07-18): el propio mensaje de F2 dice
explícito **"la fusión de datos reales queda SIN EJECUTAR hasta checkpoint
con el boss"** — el código de fusión **nunca corrió contra datos reales**
antes de cancelarse. El contrato de cancelación registra únicamente "Boss
canceló la unificación de identidad" — ningún fallo técnico documentado en
commits, comentarios de código, ni el contrato mismo. Fue una decisión de
timing/prioridad, revocada limpiamente, no una fusión que se probó y salió
mal.

Esto importa: no estamos resucitando algo que falló en producción — estamos
retomando algo que quedó pausado antes de su primera ejecución real, con el
propio boss ahora pidiendo explícitamente retomarlo ("el numero gana
siempre").

## Hallazgo clave #2: el diseño viejo YA no sirve tal cual — necesita dos correcciones antes de reusarse

`store.ReconcileIdentities` (código completo recuperado de
`git show 53ed18c:internal/store/reconcile.go`) es transaccional,
toca las tablas correctas (`chats`/`messages`/`outbox`/`media`/`drafts`,
con `mergeUsageRows` para el caso de colisión de PK en `usage`), y tenía 4
tests pasando (`TestReconcileIdentitiesMergesIntoExistingGhost`,
`RenamesWhenNoGhostExists`, `SkipsUnresolvedLIDs`, `IsIdempotent`). Pero:

1. **Su política de empate contradice la nueva regla del boss.** El
   diseño viejo (aprobado por Citrino+boss en su momento): en un empate de
   contenido (ambas filas tienen reglas/memoria/contexto no vacíos), **gana
   el `@lid`** ("es el que tiene el historial real de mensajes", decía el
   comentario). La regla nueva es absoluta: **"el número gana siempre
   (prioridad)"**, sin excepción de empate. Hay que reescribir
   `mergeChatFields` — no reusar tal cual.
2. **Su gate de resolución de identidad (`resolveNumberJID`,
   `whatsmeow.go`) es el MISMO gate defectuoso que S7c encontró y arregló
   12 días después**, en este mismo repo: `if info.AddressingMode !=
   types.AddressingModeLID { return info.Chat }` — el chequeo por
   `AddressingMode` que un mensaje real del boss (verificado en vivo,
   S7c) llegó con `addressing_mode=""` y necesitaba resolverse igual. El
   fix real vive en `resolveChatJID` (`inbound.go`): `Chat.Server !=
   types.HiddenUserServer`. Cualquier resurrección de F2 tiene que usar
   ESE gate, no el viejo.

Dos gaps menores del diseño original, a decidir ahora:
- `chats.status` quedó sin política de merge (comentario propio del código
  viejo lo marcaba como sin resolver). Propuesta: mismo criterio absoluto,
  gana el número.
- `chat_groups.member_jid` quedó deliberadamente fuera de alcance (un
  miembro de grupo bajo `@lid` no se re-clave). Sigue pareciendo
  razonablemente fuera de alcance de este subcontrato (afecta membresía de
  grupos, no identidad de chat 1:1) — señalado, no soy yo quien decide si
  entra.

## Hallazgo clave #3: unificar en lectura cuesta más de lo que parece, y no resuelve `is_boss` en absoluto

Costo real medido contra el código actual, no estimado:

- **`store` no puede resolver `@lid→número` por sí mismo.** El paquete
  `store` no importa `whatsmeow` (capa limpia, una sola dirección) —
  `ResolvePN` vive en el adaptador vivo (mapa en memoria/disco de
  whatsmeow, `client.Store.LIDs.GetPNForLID`), no en `piumy.db`. Unificar
  en lectura DENTRO de `store` necesitaría, o bien un `ATTACH DATABASE`
  cruzando a `whatsmeow.db` (rompe la capa limpia, acopla `store` al
  esquema interno de whatsmeow), o bien una tabla de mapeo NUEVA que
  `store` posea, poblada por el adaptador — un cambio de esquema real, no
  gratis.
- **El gate de despacho de `capipush` tiene HOY un bug de invisibilidad
  independiente del display**, descubierto en este relevamiento:
  `PendingDedicated` lee `active`/`mode`/`is_boss` de la fila que
  literalmente coincide con el `chat_jid` del MENSAJE — un mensaje bajo el
  `@lid` de un chat donde solo la fila del NÚMERO está configurada
  `active`/`dedicated` **no aparece nunca en `PendingDedicated`, invisible
  al despacho, hoy mismo**, sin relación con qué se muestra en el
  dashboard. Arreglar esto en lectura significa tocar el gate de despacho
  — el componente más delicado que calibré todo este contrato madre
  (S1→S4c→S6→S8) — para una preocupación cross-cutting nueva, justo
  después de estabilizarlo. Riesgo real, no cosmético.
- **`mcpserver` no tiene NINGÚN seam de resolución de identidad hoy**
  (`capipush`/`restapi` cada uno tiene su propia interfaz `LIDResolver`;
  `mcpserver`, cero). Cada una de las ~15-20 tools que reciben `chat_id`
  (`get_chat`, `send_message`, `set_chat_active`, `mark_handled`,
  `claim_chat`, etc.) vería una forma u otra según cuál llegue, y hoy
  obtendría configuración DISTINTA según cuál — habría que agregar el seam
  Y cablearlo en cada tool.
- **`is_boss`/`BossJIDs()` NO se resuelve con lectura, ni siquiera con todo
  lo anterior arreglado.** `store.BossJIDs()` (`SELECT jid FROM chats
  WHERE is_boss=1`) es SQL crudo — seguiría devolviendo 3 JIDs sin importar
  cuánta canonicalización se agregue en otros lados, a menos que ESA
  función puntual también se reescriba a mano para conocer el
  emparejamiento. Es exactamente el requisito #4 del contrato (el que
  desbloquea avisar al boss designado) — y unificar en lectura no lo cierra
  gratis, necesita el mismo trabajo puntual que la fusión haría de una vez.
- **No es realmente "una sola vez".** Pares nuevos van a seguir
  apareciendo (cualquier contacto nuevo que primero aparece en un grupo
  como `@lid` y después escribe 1:1 revelando su número). La lógica de
  "resolver en cada lectura" tendría que vivir PARA SIEMPRE en cada
  call-site nuevo que toque identidad de chat — un impuesto permanente y
  un tipo de bug nuevo y recurrente ("¿esta ruta se olvidó de resolver?"),
  en vez de resolver la duplicación en el origen, una vez por par.
- **Lo que YA existe (`restapi/read.go`: `resolveCanonical`/
  `siblingJIDs`/`messagesAcrossJIDs`, de ct-2026-07-21-1809/ct-2026-07-29)
  resuelve ~80% de los puntos #1 y #3 del contrato SOLO para el
  dashboard** — deduplica la vista y mergea historial para mostrar. Pero
  `resolveSenderNames` usa esa resolución SOLO para el nombre a mostrar,
  nunca para reglas/`is_boss`/`confirmation_mode` — el punto #2
  (configuración efectiva) sigue sin tocar ahí. Mantenerlo (no romperlo)
  y NO intentar generalizarlo a "la solución completa" — sería construir
  un mecanismo paralelo grande para el problema de configuración cuando ya
  existe uno completo, probado, en el historial.

## Qué pasa con los 557 pares en cada opción

- **Fusión:** tras correr (con backup + OK explícito del boss, como exige
  la regla dura), los 557 pares colapsan a su fila del número; las filas
  `@lid` se borran; mensajes/outbox/media/drafts/usage quedan re-clavados
  a la fila del número en una transacción por par (mecanismo ya existente
  y testeado). `is_boss` deja de estar disperso para cada par real — con
  la salvedad del caso del boss, ver la pregunta abierta abajo.
- **Unificar en lectura:** los 557 pares (y los que sigan apareciendo)
  quedan en la base para siempre, duplicados; cada consulta nueva necesita
  saber resolver el par; `BossJIDs()` sigue devolviendo 3 JIDs hasta que
  se le agregue lógica dedicada — el mismo trabajo que la fusión haría de
  una vez, pero repetido cada vez que se lee.

## El dato que pesa — identidades ocultas tipo Instagram

El boss cree que WhatsApp va hacia identidades sin número visible y pidió
"no esperes siempre que haya un número". Pesado explícitamente: **la regla
"el número gana" solo se activa cuando existe un PAR real** (ambas formas
para el mismo contacto). Un contacto que nunca revela número simplemente
tiene UNA fila (`@lid`) — nunca hay par, nunca hay fusión que correr, la
fusión queda dormida para ese contacto, no equivocada. No envejece mal en
el sentido de "algún día decide mal" — como mucho, deja de encontrar
candidatos con el tiempo si los números dejan de aparecer.

Para que la fusión no quede pensada como "un backfill de una sola vez"
sino como algo que sigue sirviendo mientras sigan apareciendo pares
nuevos: **proponer enganchar el mismo `ReconcileIdentities` (corregido) al
momento en que whatsmeow aprende un par `@lid↔número` nuevo** (un solo
punto de gancho, no lectura dispersa) — así el backfill de los 557 de hoy
y el sostenimiento hacia adelante son el MISMO mecanismo, no dos.

## Pregunta abierta — el caso de 3 vías del boss no es un par simple

El contrato cita como evidencia: *"Tres chats con `is_boss = true`, los
tres el mismo boss: número principal, `@lid`, número de Note-to-Self"*.
Esto NO es un par `@lid`/número — son **dos números de teléfono
DISTINTOS** (dígitos distintos, no la misma línea) más un `@lid`. El
número de Note-to-Self es específicamente su chat Note-to-Self (WhatsApp
usa el propio número de la cuenta para ese chat especial). La fusión de
pares `@lid↔número` colapsa el `@lid` contra CUALQUIERA de los dos
números al que esté emparejado — pero **no decide por sí sola cuál de los
DOS números reales es "el"
boss** si ambos son legítimamente suyos (¿dos líneas, o uno es un resabio
viejo?). Esto necesita una decisión aparte del boss antes de que "avisar
al boss designado" (`ct-2026-07-30-1832`) quede realmente desbloqueado —
no algo que la regla "el número gana" resuelva mecánicamente. Señalado
para que se decida explícitamente, no asumido.

## Próximo paso

Si se aprueba fusionar: preparo el plan concreto (corrección de
`mergeChatFields`, gate de identidad actualizado a `resolveChatJID`,
política de `status`, el gancho de "correr al aprender un par nuevo") y lo
traigo ANTES de tocar código real, con el backup verificado, tal como exige
la regla dura del contrato. Cero líneas de implementación hasta esa
aprobación explícita.
