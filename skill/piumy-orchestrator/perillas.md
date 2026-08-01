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

**Cómo se marca:** desde el tablero. Un agente no puede marcarlo, ni siquiera el principal — está bloqueado en el código, a propósito. Es la llave maestra.

## Quién aprueba

> Ya funciona: se habilita desde el tablero con el botón **Aprueba MSG** en la fila del chat, que pide confirmación explicando qué implica.
>
> ⏳ **Todavía no:** rechazar con motivo y corregir-y-enviar. Hoy quien aprueba solo puede dar el visto o descartar — si descarta, el que escribió no se entera de por qué. Dilo así al usuario cuando le armes el escenario.

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

Lo que define cómo habla el agente. Tres niveles, y el más específico gana:

1. **Del chat** — para ese contacto
2. **Del tipo** — todos los grupos, todos los 1 a 1
3. **Generales** — el default

**Se escriben desde el tablero, nunca por un agente.** Está bloqueado en el código: ni el agente principal puede tocarlas. Si el usuario quiere cambiar cómo habla el agente con alguien, se las sacas conversando y las dejas puestas tú, desde el tablero.

## El freno de mano

Hay un corte general que detiene **todo** lo que sale. Se activa solo si WhatsApp desconecta la sesión o marca la cuenta — y **no se suelta solo**: alguien tiene que soltarlo a mano, a propósito.

Si el usuario dice "no está contestando nada", esto es lo primero a mirar.
