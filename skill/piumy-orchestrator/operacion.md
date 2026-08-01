# Operación — arrancar, dónde vive todo, cuando falla

## Las cuatro que rompen a quien llega nuevo

Advierte sin que te pregunten.

### 1. La sesión del teléfono no la respalda nada

`whatsmeow.db` guarda el pareo con WhatsApp. **Ningún backup lo toca** — el respaldo automático cubre la base de datos propia (historial, reglas, memoria) y deliberadamente excluye esta.

Si ese archivo se pierde o se corrompe: hay que **parear de nuevo con el QR**. El historial sobrevive; la sesión no.

**Qué hacer:** dilo en el setup, y si el usuario tiene algo que perder, copia ese archivo a mano en algún lado.

### 2. El tablero viene con clave de fábrica

Usuario `admin`, clave `piumy`, sembrada al primer arranque. Cambiarla es el **primer** paso del setup.

### 3. Sin el identificador del agente principal, los mensajes del dueño se pierden en silencio

Si `PIUMY_DEFAULT_TERMINAL_ID` está vacío, el sistema arranca igual — solo deja un aviso en el log. Y en ese estado **todo mensaje del dueño queda sin despachar, sin error visible**.

Ya pasó una vez y costó horas encontrarlo. Verifícalo siempre en el setup.

### 4. Las dos puertas no se protegen igual

- **La del agente** (`:8091`): si le pones clave, exige clave. Si no le pones, queda abierta.
- **La del tablero y la administración** (`:8092`): **queda abierta a toda la red si no le pones clave**, a propósito. Sin `PIUMY_REST_KEY`, cualquiera en la misma red puede cambiar reglas, marcar dueños o leer todos los mensajes.

El login del tablero es aparte y no cubre esto. Pon la clave.

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
| Registro de lo que pasa | no hay archivo: sale por pantalla |

**En Windows el programa corre sin consola** (queda en la bandeja del sistema). Si se lanza con doble clic, esa salida **no va a ningún lado**: para ver qué pasa hay que lanzarlo desde una terminal que la capture, o redirigirla a un archivo.

## Arrancar y parear

1. Arranca → si no hay sesión, saca el **QR**: en la terminal y en el tablero.
2. Se escanea desde WhatsApp → Dispositivos vinculados.
3. Ya pareado, no vuelve a pedirlo.

En el tablero hay **Ver QR / Reconectar** (repara sin reiniciar el programa) y **Desconectar** (cierra la sesión — después hay que parear de nuevo).

**Relanzar:** matar el proceso que escucha el puerto y volver a levantarlo con las mismas variables de entorno. En Windows también se cierra desde la bandeja.

## Conectar un agente

Un agente **no puede leer los mensajes que le llegan si no tiene su propio puente**: los despachos van cifrados y la clave vive **solo del lado del agente**, nunca en el gateway.

Sin ese puente cableado en la configuración del agente, no descifra ni un mensaje. Es la causa número uno de "el agente está conectado pero no ve nada".

## Cuando algo falla

Mira en este orden:

1. **¿Está conectado a WhatsApp?** Si la sesión se cayó, no sale nada. El tablero lo muestra.
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
- **Un turno colgado se libera solo** a los 15 minutos, si un agente lo tomó y nunca lo cerró.

**No los desarmes para "que ande más rápido".** Son lo que evita que la cuenta termine bloqueada.
