# El core del core — cómo el agente usa la DB, y dónde vive el gate duro

Diseño de referencia (decidido por Citrino como arquitecto). Cada punto marcado
**[CONFIRMAR]** solo necesita el OK del boss al llegar a la fase que lo usa (F3/F4);
no es una pregunta en blanco, es un diseño listo.

## Principio

El agente **no recibe** las rules/memoria/contexto metidas en el mensaje. Recibe el
mensaje + un **semáforo** mínimo, y **lee lo que necesita de la DB por MCP**. Así el
mensaje cAPI queda chico y la fuente de verdad es siempre la DB.

## Flujo del agente al recibir un mensaje (por cAPI)

1. Llega el mensaje al terminal con el **header semáforo**: `{ level, rule_ref }`.
2. **La skill obliga, ANTES de redactar:** leer la rule por MCP (`get_chat` → campo
   `rules`, ya resuelto como *EffectiveRules*) según el `rule_ref`/`level`.
3. Si hace falta, leer `memory` y `context` del chat por MCP (`get_chat`).
4. Redactar la respuesta **respetando la rule**.
5. Enviar con `send_message` — que aplica el **gate duro** (abajo).
6. Opcional: actualizar lo aprendido con `set_chat_memory` / `set_chat_context`.

La skill es la **guía** (ayuda a que el agente lea y aplique). El **gate duro** es la
garantía, y vive en el código (§ Gate duro), nunca en la skill.

## Cada atributo del chat (tabla `chats`)

| Atributo | Qué es | Agente | Cómo se usa |
|---|---|---|---|
| `rules` | Instrucciones de cómo tratar a ese contacto (como una mini-skill). | Lee, y desde T31 (ct-2026-08-06-0244) también **escribe** (`set_chat_rules`, sin restricción de código — cualquier chat, cualquier nivel; decisión explícita del boss, "ya es responsabilidad del usuario") | La skill las lee y las obedece. El código impide enviar si no hay rules. La skill `piumy-operator` trae la recomendación de arquitectura (agentes separados) para usar `set_chat_rules` con criterio — no es un gate, es guía. |
| `memory` | Hechos **permanentes** del contacto ("es cliente", "se llama Ana", "debe $X"). | Lee y **escribe** (`set_chat_memory`) | Conocimiento previo estable. |
| `context` | Situación **del momento** ("espera el presupuesto que le mandé ayer"). | Lee y **escribe** (`set_chat_context`) | Continuidad de la conversación actual. |
| `is_boss` | Es el dueño → capacidades plenas. | **Solo lee** | Gatea qué puede hacer el agente. El agente nunca se auto-declara boss. |
| `confirmation_mode` | Si el agente manda directo o **borra y espera confirmación**. | Lee | Ver [CONFIRMAR] abajo. |
| `mode`, `status` | Routing/triage. | — | Los maneja el core, no el agente. |

**Regla dura de escritura:** el agente puede escribir `memory`, `context`, y desde
T31 `rules` de cualquier chat (`set_chat_rules`, sin gate). `is_boss`, reglas por
tipo/globales, aprobar drafts **siguen sin** exponerse por MCP (solo REST
privilegiado) — el agente nunca cambia quién manda ni las reglas del sistema
entero, pero desde T31 sí puede reescribir las reglas particulares de un chat
(las propias, o las de otro) — el boss lo decidió así, explícitamente, después
de que dos versiones con condición fueran rechazadas. Ver el `set_chat_rules`
de `internal/mcpserver/admin_tools.go` y la skill `piumy-operator` para la
recomendación de arquitectura que reemplaza al gate de código.

## Semáforo del header [CONFIRMAR]

`{ level: boss | caution | danger, rule_ref: "<id de la rule>" }`. Dato mínimo; el
contenido se lee por MCP.

- **`boss`** — el dueño. Capacidades plenas (tools potentes).
- **`caution`** — gente de confianza (amigos, contactos conocidos). La skill lee la rule
  y aplica; margen más flexible.
- **`danger`** — clientes, desconocidos, chats nuevos. La skill **debe** leer la rule y
  aplicarla estricto **antes** de responder. Máxima cautela.

El nivel sale del router/estado del chat (is_boss, status, si es nuevo). Un solo mensaje
(no dos inyecciones — evita el problema de orden de la cola async de la antena).

## memory vs context [CONFIRMAR]

- **memory = permanente** (hechos que sobreviven a la conversación).
- **context = del momento** (la situación actual, más volátil).
Ambos escribibles por el agente. Si el boss los piensa distinto, se ajusta acá.

## confirmation_mode [CONFIRMAR]

Default **por tipo** (como Piumy): 1-a-1 → `none` (responde solo), grupo → `required`
(borra y confirma primero). Las **rules pueden override**. El agente lo **lee** del chat
(`get_chat`) y actúa: `required` → deja un draft y NO envía; `none` → envía. **No** viaja
en el semáforo — el agente lo lee de la DB.

## Gate duro (en el CÓDIGO — `send_message`, no en la skill)

`send_message` valida ANTES de encolar (6 checks, migrados de Piumy):
1. No `Muted`/kill switch.
2. `policy_version` vigente.
3. `to` es un JID completo.
4. **El chat tiene `EffectiveRules` no vacías** → *"sin rules no se actúa"*. Esta es la
   garantía: por más que la skill falle o un mensaje traiga un prompt-injection, el código
   no deja salir un mensaje a un chat sin rules.
5. No `claimed_by` otro modelo (claim-lock anti doble-atención).
6. Grupo no `ignored`; pasa el whitelist del router — **salvo el chat del dueño**
   (`is_boss=1`, T30, `ct-2026-08-06-0159`), exento del whitelist en las dos
   direcciones. Es su propia cuenta: bloquearlo no protege de nada, y es el
   único de los cuatro gates de is_boss que no lo eximía — `initiateAuthorized`
   (enviar sin dispatch atado), `store.PendingDedicated` (entrada a la cola) y
   `capipush.dispatch` (ruteo: "is_boss ⟹ principal") ya lo hacían. La
   excepción es del dueño, de nadie más — un chat con rules pero sin
   `is_boss` sigue bloqueado si no está en la lista.

La misma excepción aplica en la **entrada** (`corepipeline.handleInbound`, el
gate que decide si un mensaje entrante se guarda): antes de T30, un
`router.json` sin el número del dueño hacía que sus propios mensajes
entrantes se descartaran en silencio — sin guardarse, sin log, sin nada que
lo mostrara. Peor que el lado de salida, que al menos devuelve un error de
tool visible.

La skill puede reforzar; el código es la última línea. (Mandamiento de Piumy: *"system
prompt anti-injection = capa secundaria, no la defensa principal"*.)

## media [CONFIRMAR]

Post-MVP. El adaptador open-wa arranca solo con texto; fotos/audio (open-wa las da como
URL/base64, distinto de whatsmeow) quedan para una fase posterior.
