---
name: piumy-connect
description: Use when you are an AI agent that needs to connect to a Piumy gateway — every time you connect, not just the first (your antenna port can differ from last session), or if a call starts failing with 401 (reread agent-connect.json and retry once before assuming the key changed). Te deja conectado y recibiendo despachos; no te dice cómo contestar (eso es piumy-operator) ni cómo configurar el sistema (eso es piumy-orchestrator).
---

> Fuente de verdad de este manual — vive en el repo y se embebe en el binario (`get_manual` por MCP, ct-2026-08-05-0225). `.claude/skills/piumy/SKILL.md` es una copia; editarla a ella no tiene efecto.

# Piumy — conectarse

Este manual te deja **conectado y recibiendo lo que te llega.** No es el manual de cómo contestar (`piumy-operator`) ni el de cómo configurar el sistema (`piumy-orchestrator`) — léelos aparte cuando termines esto.

Piumy despacha por **cAPI** — el protocolo de agentes externos de CleverCoder (antena, handshake, pinpass, el túnel cifrado por terminal). Ese protocolo no es de Piumy: es de CleverCoder, y **la skill `capi-protocol` es su fuente de verdad** — léela para el mecanismo completo (handshake, derivación de la key, formato del mensaje). Aquí abajo solo están las herramientas *de Piumy* que arman/leen esa conexión — no se redefine el protocolo.

Sigue los seis pasos en orden. No inventes un atajo: cada uno existe porque el anterior no alcanza solo.

**Corre este procedimiento en cada conexión — al abrir tu terminal, no solo la primera vez.** Cada sesión puede tocarte un puerto de antena distinto al de la anterior. Volver a correr los seis pasos es barato e idempotente: `register_agent` sobrescribe tu propia fila, nunca falla por "ya existe". Si en cambio dejas la fila vieja registrada, no da ningún error — simplemente dejas de recibir despachos, sin aviso.

## El procedimiento

### 1. ¿Piumy está instalado?

**Dónde se instala Piumy, en Windows:**

| Qué | Dónde |
|---|---|
| El programa | `%LOCALAPPDATA%\Piumy\Piumy.exe` |
| Su configuración | `%LOCALAPPDATA%\Piumy\piumy-config.json` |
| Sus datos y claves | `%LOCALAPPDATA%\Piumy\secrets\` |
| Acceso directo | Menú inicio → `Piumy` |

Si esa carpeta no existe, no hay nada corriendo. **El instalador se descarga de `https://github.com/chamilonster/Piumy/releases/latest`** — un único `.exe`, se instala con doble clic y no pide nada más. Hoy solo existe instalador para Windows.

Si el programa está pero no responde, arráncalo desde el menú inicio o ejecutando `%LOCALAPPDATA%\Piumy\Piumy.exe`. Es una aplicación de bandeja: al arrancar no abre ventana, se queda como ícono al lado del reloj.

### 2. ¿Está corriendo?

Los dos puertos que declara `agent-connect.json` (`mcp_url`, `rest_url` — paso 3) tienen que responder. Si no, lánzalo desde la carpeta de instalación (el acceso directo, o `Piumy.exe` a mano).

### 3. Lee `agent-connect.json`

Vive en la carpeta de datos de la instalación, junto a `status.json` — en una instalación estándar de Windows, `%LOCALAPPDATA%\Piumy\secrets\agent-connect.json`. El archivo se reescribe entero en cada arranque del gateway — pero la clave que trae adentro normalmente **no cambia**: el instalador la genera una sola vez, y reinstalar sobre una instalación existente la conserva (aborta antes que pisarla con una nueva). No es una promesa de que nunca vaya a cambiar — ante un 401 con una clave que te venía andando, releé este archivo y reintenta una vez antes de suponer otra cosa; no hace falta reabrir el cliente.

```json
{
  "mcp_url": "http://127.0.0.1:8091/mcp",
  "rest_url": "http://127.0.0.1:8092",
  "mcp_key": "...",
  "rest_key": "..."
}
```

De aquí salen las dos URLs y las dos claves de acceso. **Nunca escribas una clave dentro de esta skill.** Un campo vacío (`""`) significa que esa clave no está configurada — no la inventes, sigue sin ella (`mcp_key`/`rest_key` vacíos son válidos en dev/LAN).

### 4. Enciende tu propia antena

`capi_power(action:"on", confirm:true)` → `capi_credentials()`.

Devuelve una línea cruda: `ip:puerto chat_id:<guid> pin:<pinpass>` — el formato exacto que define `capi-protocol` (léela si algo de esta línea no te cierra). Es tuya — la usas sin editar en el paso 5, dos veces si te toca ser secundario.

### 5. ¿Hay líder?

`GET <rest_url>/api/admin/capi-connector`, header `X-API-Key: <rest_key>`. Mira el campo `terminal_id`: vacío = no hay.

**No hay líder → eres el líder.** `POST <rest_url>/api/admin/capi-connector-line`, mismo header, body:

```json
{"line": "<la línea cruda del paso 4, tal cual>"}
```

**Hay líder → comprueba que esté vivo.** `POST <rest_url>/api/admin/capi-connector/test`, mismo header. Body vacío.

- Responde `{"ok":true,...}` → entras como secundario. `register_agent` por MCP (`mcp_url`, header `Authorization: Bearer <mcp_key>` + `X-Piumy-Terminal-Id: <tu chat_id del paso 4>`) con:
  ```json
  {"endpoint": "http://<ip:puerto del paso 4>",
   "antenna_terminal_id": "<tu chat_id del paso 4>",
   "pinpass": "<tu pin del paso 4>"}
  ```
  `X-Piumy-Terminal-Id` y `antenna_terminal_id` son el mismo valor — es lo que hace que un despacho futuro te encuentre a ti y no a otro agente.
- Responde `{"ok":false,...}` → el líder está muerto. Tomas su lugar: mismo `capi-connector-line` del caso "no hay líder", con TU línea del paso 4.

### La configuración MCP de Piumy — la que va por defecto

Esta es la entrada que tienes que dejar en tu cliente MCP. En una instalación estándar de Windows los dos primeros valores son siempre los mismos; los dos últimos son tuyos:

```json
{"mcpServers": {"piumy-gateway": {
  "type": "http",
  "url": "http://127.0.0.1:8091/mcp",
  "headers": {
    "Authorization": "Bearer <mcp_key de agent-connect.json>",
    "X-Piumy-Terminal-Id": "<tu chat_id del paso 4>"
  }}}}
```

**Los dos errores que dejan esto sin funcionar, y no se ven hasta que fallas:**

- **La clave.** Sale de `agent-connect.json` de **esta** instalación (paso 3). Normalmente es estable entre arranques — el instalador la genera una sola vez y reinstalar la conserva —, pero no está prometido que nunca cambie. Una clave copiada de otra instalación, de un ejemplo, o de una configuración vieja devuelve **HTTP 401**, siempre. No hay clave "de desarrollo" que sirva en una instalación real. Ante un 401 con una clave que te venía andando, releé `agent-connect.json` y reintenta una vez antes de suponer otra cosa.
- **El identificador.** Va tu `chat_id` del paso 4, **entero y tal cual** (`capi-<proyecto>-<agente>-<verificador>`). No pongas tu nombre, ni `principal`, ni un valor inventado: el despacho se entrega por ese campo, y con un valor que el gateway no reconoce no te llega nada — sin error visible.

Si estás dentro de CleverCoder, no edites el `.mcp.json` a mano (se regenera): usa `my_mcp_add` con `name:"piumy-gateway"` y este mismo objeto como `payload`. **Toma efecto al reabrir tu terminal**, no antes.

### 6. Verifica de verdad

`POST <rest_url>/api/admin/capi-ping`, header `X-API-Key: <rest_key>`. Body vacío si eres el líder; `{"agent_id": "<tu chat_id del paso 4>"}` si te registraste como secundario en el paso 5 — es la misma identidad que usaste ahí.

**La prueba es recibir el ping en tu propio terminal, no la respuesta HTTP del ping.** Que el POST haya devuelto `{"ok":true,...}` solo dice que el gateway logró inyectártelo — no que te haya llegado. Si no ves nada, la antena no está prendida, o el conector quedó mal registrado — repasa el paso 4/5, o `capi-protocol` si el problema es del handshake mismo (401, 404).

**Si el ping te llegaba antes y dejó de llegarte, sin que cambiaras nada de tu lado:** no es un problema nuevo, es que te reconectaste con un puerto de antena distinto al que quedó registrado. Rehaz el paso 4 y el 5 — re-registrarte es idempotente, no hay nada que comparar primero.

## Lo que tienes que tener claro, además de los pasos

### Conectado no es habilitado: sin un despacho activo no puedes MIRAR casi nada

Apenas te conectas, la mayoría de las herramientas de chat te van a responder esto:

```
refused: no active dispatch for this terminal (default DENY) — call get_instructions first
```

**Eso no es una falla de tu conexión: es el sistema funcionando.** `get_chat`, `get_messages`, `get_media` y el resto de las herramientas de la tabla "Lo que SÍ tocas" (`piumy-operator`) exigen un despacho real antes de dejarte mirar un chat puntual — 28 de las 55 herramientas del gateway funcionan así.

**Eso no quiere decir que no puedas hablar primero.** `send_message`/`draft` sí dejan iniciar sin ningún despacho cuando tenés motivo — ver el flujo 17 del manual del operador, "Iniciar tú, sin despacho" (el dueño te encargó un número, por ejemplo). Y `send_to_boss` existe justo para avisarle algo al dueño sin que te haya escrito nada. Vas a ciegas (sin despacho tampoco podés mirar el chat antes con `get_chat`/`get_messages`), pero podés escribir.

Cómo distinguir una cosa de la otra sin perder tiempo:

- `get_status` **sí responde sin despacho** — no es la única (27 de las 55 herramientas no lo piden, entre ellas `send_message`/`draft`/`silent_act`/`send_to_boss`), pero es la que sirve para diagnosticar tu conexión: si te contesta, estás bien conectado — mira `agent_connected` y `agents`. Si te contesta y las de chat te rechazan, no toques nada más: solo estás esperando trabajo (o iniciá vos, si tenés motivo — flujo 17).
- Si `get_status` también falla, entonces sí es la conexión — vuelve al paso 3 (clave) y al paso 5 (identificador).

**Un despacho no se puede fabricar.** Nace de un mensaje real entrante de WhatsApp. El `capi-ping` del paso 6 comprueba el camino pero **no habilita**: su nonce no se registra en el gate, a propósito, para que un agente no pueda autoautorizarse. Si necesitas probar que puedes responder a un mensaje concreto (no iniciar vos), pídele al dueño que le escriba al número — no busques la vuelta.

### Ignorado o en blacklist: el dueño apagó ese chat a propósito

Hay un **segundo** motivo, independiente del anterior, por el que `send_message` puede rechazarte con despacho activo y todo:

```
refusing to send: <jid> is ignored — the owner must change its status first
```

(o `is blacklist`, en vez de `is ignored` — desde T65, ct-2026-08-11-1642, frenan igual, para cualquier chat, no solo grupos.)

**No es tu conexión ni tu despacho: es que el dueño marcó ese chat para que nadie le conteste.** Si ves este rechazo, es la respuesta — no lo rodees pidiéndole a otro agente que lo intente, no reintentes de otra forma. Misma ley que el resto de este manual: el rechazo no se sortea.

**Lo que ya NO existe, a propósito:** hasta T65 había una whitelist del router que bloqueaba el envío a cualquier número no agregado explícitamente — el dueño la pidió sacar, tres veces, hasta que se sacó de verdad (verbatim: *"yo quierp todo en witelist... para algo esta ignorar, eso ya apaga el chat"*). Hoy todo chat está permitido por defecto; `ignorado`/`blacklist` es el único freno de envío que queda, y también el único filtro de entrada — antes un mensaje de un número no habilitado ni siquiera se guardaba; ahora se guarda y se ve, siempre.

**Sobre el riesgo de baneo — información, no un candado.** Escribirle primero a números que nunca te escribieron, o mandar muchos mensajes seguidos a desconocidos, es el patrón que WhatsApp banea por mensajería masiva. Nada en el código te lo va a impedir — es responsabilidad del dueño, tal como él la reclamó (verbatim: *"si te doy una lista de numeros es mi responsabilidad"*). Como agente: tenlo en cuenta antes de escribirle primero a alguien sin despacho previo, no porque el sistema te vaya a frenar, sino porque el costo de un baneo lo paga la cuenta del dueño.

### Un rechazo del gate es la respuesta, no un obstáculo

Si `register_agent` te rechaza, si `capi-connector-line` te rechaza, si `capi-connector/test` no responde — esa es la respuesta. No lo rodees por otra vía, no le pidas a otro agente que lo haga por ti, no reintentes con datos distintos "a ver si pasa". Es la misma ley del manual del operador: el rechazo no se sortea.

### La API de administración es del dueño

`/api/admin/*` no es una puerta de servicio genérica: es la administración de **esta instalación en particular**, protegida por el `rest_key` de **esta instalación en particular**, leído de **su** `agent-connect.json`. Usarla para conectarte a tu propio gateway es el propósito; usarla para cualquier otra cosa no lo es.

### Armar un cuerpo con texto humano: nunca por la línea de comandos

Los `curl -d '...'` de este manual llevan solo ids, claves o literales fijos — son seguros tal cual. **En cuanto el cuerpo lleva texto que escribió una persona** (un mensaje, una regla, cualquier campo en lenguaje humano) pasarlo inline en la línea de comandos puede llegar roto, o perderse entero. No es un problema de acentos en español: en portugués o alemán se rompe igual, y en chino o árabe el texto desaparece — la terminal reemplaza cada caracter que no sabe convertir por `?`.

La causa: la terminal recodifica el argumento con su codificación local **antes** de que el proceso lo reciba, y esa codificación local casi nunca es UTF-8. Pasa igual mandando un JSON-RPC al MCP (`mcp_url`) que un body a la REST (`rest_url`) — no es un problema de un endpoint puntual, es de cómo se le pasa texto a cualquier proceso por argv. La solución no depende del idioma: nunca pases texto humano como argumento, siempre como archivo.

```bash
# ROMPE — el texto viaja por argv, la terminal lo recodifica
curl -d '{"text":"..."}' <url>

# ANDA — el texto viaja como bytes de archivo, no pasa por argv
curl --data-binary @cuerpo.json <url>   # cuerpo.json guardado en UTF-8
```

Probado (T29, `ct-2026-08-06-0140`): el mismo texto en español/portugués/alemán + chino + árabe, mandado de las dos formas contra un servidor de prueba — inline llegó con los acentos rotos y el chino/árabe reemplazados por `?`; por archivo llegó byte a byte idéntico, en las cinco lenguas.
