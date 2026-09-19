# F4 — Diseño maestro: mcpserver + cAPI + gates + tools + media + metering

Diseño del arquitecto (Citrino), decidido con el boss en sesión (los verbatim viven en el
contrato `F4`). F4 es la fase que define **cómo se comporta el agente**. A diferencia de
F0–F3, **es mayormente construcción nueva, no cherry-pick.**

## Hallazgo del scrapeo (corrige el plan)

- **Las 23 tools MCP existen** en Piumy (`mcpserver/server.go`, mcp-go + `AddTool` +
  Bearer auth fail-closed + flood-guard middleware) → **se migran**.
- **cAPI / AES-GCM NO existe** en Piumy. Piumy despacha con **SSE nudge**
  (`restapi /api/events` + eventbus) **+ pull por MCP** — el modelo "opción 1".
- **cAPI (opción 2: inyección mensaje+header + lock/unlock) es NUEVO.** Y la inyección
  física al terminal es un **seam a CleverCoder** (CleverCoder ya inyecta prompts a
  terminales — `.prompt`/spawn). piumy-gateway construye el lado *productor* (cifra + arma
  el header/nonce + direcciona por `terminal_id`); CleverCoder inyecta.

## Nodos (los mínimos — regla del boss)

Nuevos/activados en F4, sin inventar paquetes de más:

| Nodo | Rol | Migra / Nuevo |
|---|---|---|
| `mcpserver` | 23 tools + **el gate state machine** (lock/unlock/noting) + gating por nivel (middleware) + tools nuevas | migra base + mucho nuevo |
| `capi` | Productor cAPI: cifra (AES-256-GCM) el `{mensaje + header}` y lo direcciona a `terminal_id` (puerto fallback) | **nuevo** |
| `capipush` | Toma `PendingDedicated`, aplica cuota/coalescing/backpressure, entrega vía `capi` | **nuevo** (lógica; reusa store) |
| `media` | Guardar media entrante por ruta + compresión low-q JPEG (`image/jpeg` stdlib, cero dep) | nuevo (Piumy tenía uno acoplado a whatsmeow) |
| `restapi` | SSE nudge (`/api/events`) + endpoints privilegiados (REST, NO MCP): SetChatRules/SetIsBoss/ApproveDraft | migra el nudge + gate privilegiado |

**Metering** NO es un nodo nuevo: es una **tabla `usage` en `store`** + contadores inline en
`capipush`/`send_message`. **Voz/tokens/inyección-CleverCoder** son **seams**, no nodos.

## 1. Modelo de despacho (cAPI, opción 2)

```
inbound → store → PendingDedicated → capipush → capi(cifra + terminal_id) → [SEAM CleverCoder] → terminal del agente
                                        │
                                        ├─ cuota de cuenta (metering) — sobre cuota: NO despacha, queda en cola
                                        ├─ coalescing por chat (burst → 1 inyección)
                                        └─ backpressure por estado swamped
```
- **Header inyectado:** `{ nonce(hex), level(boss|caution|danger), chat_jid }`. Mínimo — el peso (rules/memory/context) se lee por MCP.
- **Solo `dedicated`** se inyecta. `auto` lo maneja `autoreply`. Nuevos/desconocidos esperan.
- **Fallback nudge+pull:** si `swamped`, se deja de empujar; el agente drena `get_pending` por MCP (opción 1) a su ritmo. Nunca se ahoga el terminal.

## 2. Gate state machine (en `mcpserver`, middleware — el gate DURO)

Por dispatch, atado al `nonce` (no a un flag global — evita que dos mensajes intercalados se pisen):

```
locked  ── get_instructions(nonce) ──▶  (devuelve EffectiveRules+memory+context + token AL FONDO)
   │                                              │
   └─────────────── unlock(token) ────────────────┘ ─▶ noting
noting  ── remember(memory?,context?) | skip ──▶ ready      (checkpoint obligatorio; write y modify = 1 upsert)
ready   ── send_message | draft ──▶  (aplica los 6 checks + confirmation_mode)
```
- `get_instructions` pone el token de unlock **al final** del output → obliga a ingerir todo (rules/memory/context) para obtenerlo.
- **Anti-replay:** token atado al nonce del header. **Anti-injection:** el nonce lo genera el server, un mensaje no lo forja.
- `send_message` conserva los 6 checks de Piumy (incl. "sin rules no actúa" = EffectiveRules no vacías).
- La skill `/piumy` orquesta este flujo; **el código lo garantiza** (regla #4).

> **🔒 SECURITY — default fail-CLOSED (F4a lo dejó fail-OPEN; F4b DEBE cerrarlo).**
> En F4a "sin dispatch bound → irrestricto (como boss)". Eso es un **agujero**: un agente
> prompt-inyectado que **saltee `get_instructions`** bypassa el lock/unlock Y el anti-leakage
> por nivel (los 6 checks base de send_message igual sobreviven). Hoy es **inerte** (nada
> llama `RegisterDispatch` salvo tests). **F4b tiene que:** (a) `RegisterDispatch` liga el
> dispatch al `terminal_id` (cAPI ya lo sabe); (b) una sesión/terminal con un dispatch
> pendiente **no puede** actuar como "sin dispatch" — default DENY hasta pasar el gate;
> (c) boss/admin **explícito** (dispatch level=boss o credencial admin aparte), NUNCA
> "ausencia de dispatch". Reescribir los comentarios de gate.go/levelgate.go/server.go que
> hoy afirman que el fail-open es "safe by Bearer auth" — no lo es.

## 3. Política por nivel (defaults sembrados, overridables)

El nivel (del router + `is_boss` + estado del chat) gobierna **tres cosas** — y **qué tools ve el MCP** (gating por nivel, middleware):

| Nivel | Header/lock | Scope de `memory` | confirmation default | Tools disponibles |
|---|---|---|---|---|
| **boss** (`is_boss`) | sin gate | **todas** las memorias | `none` | **DB-admin completo** (rules, is_boss, política) + todo |
| **caution** | gate liviano | chat actual | `discretion` | memory/context + acciones seguras |
| **danger** | gate completo | **solo chat actual** (anti-leakage) | `always` | memory/context + acciones seguras — **nunca** las privilegiadas |

**Las modificaciones duras (rules/is_boss/política/ApproveDraft) solo en sesión boss** — por
código, no por skill. Un agente de cara al público jamás ve esas tools (si no, desarma todos los gates).

## 4. confirmation_mode — 3 modos (enforce en código)

- `none` → `send_message` envía.
- `discretion` → el agente elige `send_message` o `draft` (usa la **checklist de contenido sensible**, §7).
- `always` → `send_message` se convierte en `draft` por código (fail-safe, nada sale sin OK).

## 5. Tools (las 23 migradas + nuevas)

**Migradas (23):** get_status, list_chats, get_messages, get_chat, get_chat_groups,
get_decision_policy, get_drafts, get_media, get_outbox, get_pending, get_queue, claim_chat,
release_chat, send_message, set_chat_active, set_chat_context, set_chat_memory,
set_chat_status, set_mode, mark_handled, escalate, resolve_chat, reset_dashboard_password.

**Nuevas (gateadas por nivel):**
- Gate: `get_instructions`, `unlock`, `remember`, `skip`.
- Grupo/perfil (privilegiadas): `create_group`, `add_participant`, `set_group_icon`,
  `set_group_description`, `set_profile_pic`, `set_profile_name`.
- DB-admin (**solo boss**): `set_chat_rules`, `set_is_boss`, `set_type_rules`,
  `set_default_rules`, `approve_draft`, `set_confirmation_mode`.
- Media: `get_media_full` (calidad sin comprimir on-demand).
- Audio (seam): `send_voice`.

Comandos de la skill `/piumy` **en inglés**.

## 6. Media (por ruta + compresión)

- Entrante: `media` guarda el original por ruta + genera un **JPEG low-q** (`image/jpeg`
  stdlib) que es lo que el agente lee por defecto → menos tokens de interpretar.
- `get_media_full(msg_id)` → el original sin comprimir (para un logo, etc.).
- **Incentivo automático:** low-q por defecto = menos `usage`; pedir full = `IMG_COST` alto = paga más. (§8)

## 7. Checklist de contenido sensible (va a `/piumy`, capa blanda)

Guía el `discretion`. Las 3 primeras **frenan aunque el chat esté en `none`**:
datos privados del boss · datos de un tercero · secrets/credenciales · plata/compromisos
económicos · promesas/compromisos · categorías sensibles (salud/legal) · media saliente.
(El código no gatea contenido semántico; los gates duros son los de chat. Esto hace el juicio consistente.)

## 8. Metering — 2 modalidades + blend 70/30

Tabla `usage` en `store` (por cuenta/chat/día). Unidad ≈ tokens (para que el blend cierre):

```
est ≈ out_chars/4·W_OUT + in_chars/4·W_IN + img·IMG_COST + audio·AUDIO_COST + msg·MSG_COST
usage = hay_tokens? (0.7·tokens_reales + 0.3·est) : est
```
- `W_OUT` el más pesado (output de la IA). Pesos = **perillas de calibración** (config, se afinan contra tokens reales).
- Contadores nativos en `capipush` (dispatch) + `send_message` (output). **Tokens = seam a CleverCoder.**
- **Rate-limit de cuenta:** cuota sobre el **dispatch** (no sobre el inbound — recibir es gratis). Sobre cuota → no despacha, cola + aviso opcional. Reusa el patrón de `governor`/`mcpguard`.

## 9. Seams (piumy-gateway deja el hueco; CleverCoder enchufa después)

- **Inyección al terminal** (cAPI in): CleverCoder inyecta el dispatch cifrado.
- **Tokens:** CleverCoder reporta uso por dispatch → el 70% del blend.
- **Audio (TTS/STT + voces):** CleverCoder motor; piumy-gateway manda/recibe la nota de voz por open-wa (`sendPtt` + media) y expone `send_voice`, pasando `terminal_id/modelo` para la voz.

## Sub-fases (gateadas, auditadas por parte)

- **F4a** — `mcpserver` core: migrar las 23 tools + Bearer auth + flood-guard + **el gate state machine** (locked/instructions/unlock/noting/ready) + gating por nivel (middleware). El corazón.
- **F4b** — `capi` + `capipush`: productor cAPI (AES-GCM, terminal_id/puerto) + PendingDedicated→coalescing→backpressure→dispatch + el SSE nudge de `restapi`. Seam de inyección documentado.
- **F4c** — tools nuevas: grupo/perfil + DB-admin (boss-only) + los endpoints privilegiados REST. Todo gateado por nivel.
- **F4d** — `media` (por ruta + low-q JPEG + `get_media_full`) + **metering** (tabla `usage` + algoritmo nativo + contadores + blend + seams de tokens/audio).
- **`/piumy`** (skill del agente) — diseñada en paralelo: el protocolo (instructions→unlock→remember/skip→responder), la checklist, los comandos en inglés. Entregable aparte del Go.

Disciplina por sub-fase: diagrama → `dflux_resolve` → codear → `ponytail-review` →
`go build/vet/test` verde → **actualizar `MANUAL.md`** con los nodos/botones nuevos.
