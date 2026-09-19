> Fuente de verdad de este manual — vive en el repo y se embebe en el binario (`get_manual` por MCP, ct-2026-07-31-1541). `.claude/skills/piumy-orchestrator/operacion.md` es una copia; editarla a ella no tiene efecto.

# Operación — arrancar, dónde vive todo, cuando falla

## Las cuatro que rompen a quien llega nuevo

Advierte sin que te pregunten.

### 1. La sesión del teléfono no la respalda nada

`whatsmeow.db` guarda el pareo con WhatsApp. **Ningún backup lo toca** — el respaldo automático cubre la base de datos propia (historial, reglas, memoria) y deliberadamente excluye esta.

Si ese archivo se pierde o se corrompe: hay que **parear de nuevo con el QR**. El historial sobrevive; la sesión no.

**Qué hacer:** dilo en el setup, y si el usuario tiene algo que perder, copia ese archivo a mano en algún lado.

### 2. El tablero viene con clave de fábrica

Usuario `admin`, clave `piumy`, sembrada al primer arranque. Cambiarla es el **primer** paso del setup.

**Si el dueño la pierde después, no es un callejón sin salida.** `reset_dashboard_password` es boss-only y ningún agente la ejecuta por él — pero no hace falta: la pantalla de login tiene recuperación por WhatsApp o email (link "¿Olvidaste la contraseña?"), mismo flujo de código en las dos vías. Decíselo si pregunta; no le confirmes que perdió el acceso.

### 3. Sin el identificador del agente principal, los mensajes del dueño se pierden en silencio

Si `PIUMY_DEFAULT_TERMINAL_ID` está vacío, el sistema arranca igual — solo deja un aviso en el log. Y en ese estado **todo mensaje del dueño queda sin despachar, sin error visible**.

Ya pasó una vez y costó horas encontrarlo. Verifícalo siempre en el setup.

### 4. Las dos puertas no se protegen igual

- **La del agente** (`:8091`): si le pones clave, exige clave. Si no le pones, queda abierta.
- **La del tablero y la administración** (`:8092`): **queda abierta a toda la red si no le pones clave**, a propósito. Sin `PIUMY_REST_KEY`, cualquiera en la misma red puede cambiar reglas, marcar dueños o leer todos los mensajes.

El login del tablero es aparte y no cubre esto. Pon la clave.

## Dónde se instala, en Windows

Es lo primero que te van a preguntar, y lo primero que necesitas para revisar cualquier cosa.

| Qué | Dónde |
|---|---|
| El programa | `%LOCALAPPDATA%\Piumy\Piumy.exe` |
| Su configuración (claves, rutas) | `%LOCALAPPDATA%\Piumy\piumy-config.json` |
| Sus datos: base, sesión, respaldos, medios | `%LOCALAPPDATA%\Piumy\secrets\` |
| Cómo lo abre el usuario | Menú inicio → `Piumy` |
| El tablero | `http://127.0.0.1:8092/dashboard/` |

**El instalador se descarga de `https://github.com/chamilonster/Piumy/releases/latest`** — un solo `.exe`, doble clic, no pregunta nada más. Solo Windows por ahora.

**Piumy es una aplicación de bandeja: al abrirla no aparece ninguna ventana**, solo un ícono al lado del reloj. Si el usuario dice "le di y no pasó nada", eso es lo esperado — lo que hay que comprobar es si el tablero responde, no si vio algo.

**Y ojo con esto, porque es la trampa que más tiempo cuesta:** la instalación tiene sus **propias claves**, distintas de cualquier otra instalación y distintas de las de desarrollo. Se leen de `piumy-config.json`, o mejor de `secrets\agent-connect.json`, que el programa reescribe en cada arranque. Una clave copiada de otro lado devuelve `401` siempre, sin más explicación.

## Dónde vive cada cosa

Todo relativo a donde corre el programa, salvo que se den rutas absolutas.

| Qué | Dónde |
|---|---|
| Historial, reglas, memoria, borradores, cola de salida | `PIUMY_DB_PATH` — **la única obligatoria**, sin default |
| Sesión de WhatsApp (el pareo) | `PIUMY_WA_DB_PATH`, default `whatsmeow.db` |
| Quién entra y a quién se rutea | `PIUMY_ROUTER_PATH`, default `router.json` |
| Estado visible (ánimo, QR) | `PIUMY_STATUS_PATH`, default `status.json` |
| Fotos, audios y archivos recibidos | `PIUMY_MEDIA_DIR`, default `media/` |
| Respaldos cifrados de la base | `PIUMY_BACKUP_DIR` — **inerte sin `PIUMY_BACKUP_KEY`** |
| Registro de lo que pasa | `logs/piumy.log`, junto a (no adentro de) `secrets/` — rota por tamaño, 4 archivos × 5 MB |

**Este archivo es lo que le pedís a un usuario cuando algo no le llegó.** Vive aparte de `secrets/` a propósito: comprimir la carpeta para mandártela no puede llevarse de paso `PIUMY_MCP_KEY`/`PIUMY_REST_KEY`/la sesión de WhatsApp. En una instalación estándar de Windows: `%LOCALAPPDATA%\Piumy\logs\piumy.log`. El programa corre sin consola (compilado `-H=windowsgui`, queda en la bandeja del sistema) — no hace falta relanzarlo desde una terminal para ver qué pasó, el archivo ya lo tiene, y relanzar además pierde el historial del momento exacto de la falla.

## Arrancar y parear

1. Arranca → si no hay sesión, saca el **QR**: en la terminal y en el tablero.
2. Se escanea desde WhatsApp → Dispositivos vinculados.
3. Ya pareado, no vuelve a pedirlo.

En el tablero hay **Ver QR / Reconectar** y **Desconectar** (cierra la sesión — después hay que parear de nuevo).

**Ojo con cuándo usar cada uno.** Una caída de red (el WiFi que se corta, el router que reinicia) ya no necesita el botón: el gateway reconecta solo, con backoff creciente (ver "Los frenos que se activan solos", abajo) — la respuesta correcta ahí es esperar, no tocar nada. **Ver QR / Reconectar** sigue siendo el camino para lo que la reconexión automática no resuelve: la sesión quedó inválida de verdad (logout desde el teléfono, dispositivo removido) y hay que volver a parear.

**Relanzar:** matar el proceso que escucha el puerto y volver a levantarlo con las mismas variables de entorno. En Windows también se cierra desde la bandeja.

## Conectar un agente

Piumy despacha por **cAPI** (CleverCoder, antena + handshake + túnel cifrado por
terminal) — el protocolo en sí es de CleverCoder, no de Piumy: la skill
`capi-protocol` es su fuente de verdad, léela ahí, no aquí. Lo que sí es de
Piumy: **decisión T28 (ct-2026-08-05-2242, el boss) — el despacho viaja en
texto plano dentro de ese túnel.** Piumy tuvo una segunda capa de cifrado
propia (una clave más, un binario más) y se sacó — el túnel de cAPI ya
protege el hop, y protegerse de CleverCoder no tenía sentido: CleverCoder es
del dueño, en su propia máquina. No es un flag que alguien dejó apagado —
está afuera, no hay interruptor que lo vuelva a prender.

La causa número uno de "el agente está conectado pero no ve nada" ya no es
un cableado de descifrado que falta — es la antena apagada, o el conector
mal registrado. Ver `piumy-connect` (el manual de conexión) para el
procedimiento paso a paso.

Si armas a mano un `curl` con texto que escribió una persona (un mensaje,
una regla) contra el MCP o la REST, nunca lo pases inline en la línea de
comandos — se rompe en cualquier idioma con caracteres fuera de ASCII, no
solo en español. `piumy-connect` tiene el porqué y la forma segura
(`--data-binary @archivo`, guardado en UTF-8).

## Cuando algo falla

Mira en este orden:

1. **¿Está conectado a WhatsApp?** Mientras no lo está, no sale nada. El tablero lo muestra — si fue una caída de red, dale unos minutos antes de tocar nada (reconecta solo); si la sesión quedó inválida, no vuelve sin re-parear (Ver QR / Reconectar).
2. **¿Está puesto el freno general?** Se activa solo cuando WhatsApp desconecta o marca la cuenta, y **no se suelta solo**.
3. **¿El chat está atendido?** Un chat sin atender guarda los mensajes y no contesta. No está roto.
4. **¿Está esperando aprobación?** Si el chat pide confirmación, lo que escribió el agente está en la lista de pendientes, no en camino.
5. **¿Hay agente escuchando?** Sin identificador del principal, los mensajes del dueño no llegan a nadie.
6. **¿Se está frenando solo?** Con mucha cola, el sistema baja el ritmo a propósito.

## Los frenos que se activan solos

Nada de esto es una falla — es el sistema cuidándose de que WhatsApp marque la cuenta:

- **Tope por minuto y por día** de mensajes salientes. El del día sobrevive a un reinicio, no se resetea.
- **Demoras variables** antes de responder, leer o escribir. Nunca instantáneo, nunca un número fijo: contra WhatsApp, los tiempos redondos delatan.
- **Baja el ritmo con la cola llena.**
- **Reintentos espaciados** cuando un envío falla, cada vez más lejos. Después de varios intentos el mensaje queda apartado — **nunca se borra**, queda para revisar.
- **Un turno colgado se libera solo** a los 5 minutos por defecto (configurable, `PIUMY_GATE_STALE_AFTER`), si un agente lo tomó y nunca lo cerró.
- **La reconexión a WhatsApp espera cada vez más entre intentos** cuando la red se cae — arranca en 5s, se duplica en cada fallo hasta un techo de 5min, con variación aleatoria de ±20% para no caer en un ritmo redondo. Nunca se rinde: sigue reintentando solo, sin que nadie tenga que tocar nada. El contador se resetea recién cuando la conexión se sostiene un minuto entero — así una caída en ciclo (conecta y se cae enseguida) no vuelve a martillar cada pocos segundos.
- **Una respuesta larga sale partida en varios mensajes**, con un respiro corto y aleatorio entre uno y el siguiente — no un globo único de miles de caracteres, que ninguna persona escribe así y se ve desde afuera. Si el usuario pregunta por qué llegaron 3-4 mensajes seguidos en vez de uno: es exactamente esto, a propósito.

**No los desarmes para "que ande más rápido".** Son lo que evita que la cuenta termine bloqueada.
