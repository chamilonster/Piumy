> Fuente de verdad de este manual — vive en el repo y se embebe en el binario (`get_manual` por MCP, ct-2026-07-31-1541). `.claude/skills/piumy-orchestrator/perillas.md` es una copia; editarla a ella no tiene efecto.

# Perillas — niveles, quién manda, quién aprueba

## Dos ejes distintos. No los mezcles.

**Eje 1 — cuánta autonomía tiene el agente en ese chat** (el nivel):

| Nivel | Qué pasa |
|---|---|
| **Dueño** | Es el chat del dueño. Le habla directo al agente principal y puede pedir cualquier cosa. |
| **Automático** | El agente contesta solo. |
| **Con confirmación** | El agente escribe, pero queda como borrador esperando visto bueno. |
| **Sin atender** | Los mensajes entran y quedan guardados. Nadie contesta. |
| **Ignorado** | Ni se mira. |

**Eje 2 — quién lo atiende:** el modo automático interno, o un agente al que se le despacha el chat. Es ruteo, no confianza. Un chat puede estar en "con confirmación" y atendido por cualquiera de los dos.

Confundirlos es el error más común: subir la autonomía cuando lo que se quería era cambiar quién atiende.

## Quién manda

**El dueño** es un chat marcado como tal. Es el único que puede aflojar controles: sacar la confirmación de un chat, ponerlo en automático, nombrar aprobadores.

**Puede haber más de un chat marcado como dueño** — la misma persona con dos números, por ejemplo. En ese caso hay que decidir cuál de los dos administra las aprobaciones; no lo resuelve solo.

**Cómo se marca:** el número con el que se parea WhatsApp queda marcado dueño solo, sin que nadie toque nada. Cualquier OTRO número personal del dueño se marca desde el tablero — a mano, a propósito: no hay forma de adivinar cuál es. Un agente no puede marcarlo, ni siquiera el principal — está bloqueado en el código, a propósito, en los dos caminos. Es la llave maestra.

## Quién aprueba

> Ya funciona: se habilita desde el tablero con el botón **Aprueba MSG** en la fila del chat, que pide confirmación explicando qué implica.
>
> Ya funciona también: **rechazar con motivo** y **corregirlo y mandarlo**. Al rechazar, el motivo vuelve al agente que escribió y se le pide otro intento — hasta 3 rondas por chat. Descartar sigue siendo el camino final: corta el borrador y no explica nada de vuelta.

**El aprobador** puede darle salida a lo que otro escribió. Nada más.

| Puede | No puede |
|---|---|
| Aprobar un borrador | Cambiar las reglas de un chat |
| Rechazarlo con motivo | Sacarle la confirmación a un chat |
| Corregirlo y mandarlo | Poner un chat en automático |
| | Nombrar aprobadores (ni a sí mismo) |

**Puede ser una persona** (su chat de WhatsApp marcado como aprobador) **o una IA** (un agente registrado que revisa la cola de borradores).

Esa separación es todo el punto: varias personas dan el visto bueno, una sola cambia las reglas del juego. Quien puede apagar la necesidad de aprobar dejó de ser aprobador y pasó a ser dueño.

**Cómo se pone el pin:** desde el tablero, o el dueño pidiéndoselo al agente él mismo. De ninguna otra forma.

## El circuito de aprobación

```
el agente escribe en un chat con confirmación
  → queda un borrador, NO sale
  → aparece en la lista de pendientes (tablero, y para quien apruebe)
  → quien aprueba elige:
       aprobar             → sale tal cual
       rechazar con motivo → no sale; el motivo vuelve a quien lo escribió
       corregir y enviar   → sale con el texto cambiado
```

**Por qué el motivo importa:** sin él, el que redactó no se entera de qué estuvo mal y lo repite. Con él, el rechazo enseña.

**Aclara esto al usuario:** que un mensaje quede esperando **no es una falla**. Es exactamente lo que pidió cuando puso ese chat en confirmación.

## Restringir es gratis, aflojar cuesta

Regla de diseño que atraviesa todo el sistema: **subir la vigilancia siempre se puede** — cualquiera puede poner un chat en confirmación, incluido el propio agente si detecta que la conversación se puso delicada.

**Bajarla, no.** Sacar la confirmación o poner algo en automático es solo del dueño, hablándole él mismo al agente.

Si algo te rechaza, fíjate de qué lado estás: casi siempre es esto.

## Las reglas del chat

Lo que define cómo habla el agente. Dos niveles, y el más específico gana:

1. **Del chat** — para ese contacto
2. **Del tipo** — grupos (todos), y por origen para 1 a 1 (contacto guardado / número nuevo, quien ya está guardado gana)

Sin reglas propias ni de tipo, el agente no actúa en ese chat — no hay un tercer nivel al que caer.

**Las del tipo se escriben solo desde el tablero.** Está bloqueado en el código: ni el agente principal puede tocarlas.

**Las del chat no**: cualquier agente puede cambiarlas, a cualquier nivel, sin restricción. Es a propósito — decisión del dueño de Piumy, textual: *"ya es responsabilidad del usuario"*. Si el usuario da por hecho que nadie más toca cómo habla el agente con un contacto suyo, decíselo: un agente conectado sí puede.

## El freno de mano

Hay un corte general que detiene **todo** lo que sale. Se activa solo si WhatsApp desconecta la sesión o marca la cuenta — y **no se suelta solo**: alguien tiene que soltarlo a mano, a propósito.

Si el usuario dice "no está contestando nada", esto es lo primero a mirar.

## Leer el estado de WhatsApp: libre. Escribirlo: del dueño

El mismo dato, dos caminos distintos — otro caso de "restringir es gratis, aflojar cuesta" (arriba). Cualquier agente conectado puede leer el "Estado" (About) propio de la cuenta: viaja como `profile_status` dentro de `get_status`, sin candado — leerlo no cambia nada ni toca a terceros. Cambiarlo (`set_profile_status`) sigue siendo boss-only, igual que el resto de las tools que tocan WhatsApp hacia afuera de forma irreversible.

Antes de T103 (ct-2026-08-29-1759) esta asimetría era peor: un agente podía ESCRIBIR el estado y no tenía forma de LEER el que había — la única vía era pedirle al dueño que abriera el tablero y mirara. Corregido: el dato que ya existía en el adapter (T96) se expuso también por MCP.

## La foto de perfil y el ícono de grupo

`set_profile_photo` (T111, ct-2026-09-01-1442) cambia la foto de perfil de la CUENTA — la ven todos los contactos, al instante. Boss-only, igual que `set_group_icon` (el ícono de un grupo) y el resto de lo que toca WhatsApp hacia afuera sin vuelta atrás.

**No hace falta mandar un JPEG.** Las dos tools convierten sola cualquier imagen común (PNG, GIF) a JPEG antes de subirla — el dueño no tiene que convertirla a mano. Solo lo que no se puede decodificar como imagen (un archivo que no es una imagen, un formato exótico) da error.

**Para quitar la foto de perfil**, `set_profile_photo` con `remove: true` — no un `data_url` vacío. Un string vacío que llegó mal armado no puede borrarle la foto a nadie por accidente; hace falta decirlo explícito.

**Si WhatsApp la rechaza**, el mensaje dice la verdad, incluida la parte que no se sabe: puede ser el formato o el tamaño de la imagen — el servidor no distingue entre los dos motivos al rechazar, así que tampoco se puede afirmar cuál fue. La pista que sí se puede dar: probar con una imagen más chica.
