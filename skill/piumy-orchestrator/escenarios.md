# Escenarios — qué quiere lograr, y qué dejar puesto

Cada uno: qué es, qué preguntar, qué queda configurado. Se combinan.

---

## Círculo cercano

Amigos y familia. Cada uno con su trato y su memoria.

**Pregunta:** "¿Quieres que conteste solo, o que te avise antes?" · "¿Hay alguno con el que tenga que hablar distinto?"

**Deja puesto:** cada chat en automático, con sus reglas propias (`set_chat_rules` desde el tablero). La memoria del chat se llena sola con lo que el agente aprende.

**Cuidado:** las reglas de un chat pisan a las del tipo, y las del tipo pisan a las generales. Escribe lo particular donde va, no en las generales.

---

## Canal con empleados

Comunicaciones internas, avisos, coordinación.

**Pregunta:** "¿Es un grupo o los escribes uno por uno?" · "¿Quieres que responda solo, o que te muestre antes lo que va a decir?"

**Deja puesto:** los grupos arrancan **pidiendo confirmación** por defecto — eso es a propósito, un error en un grupo lo ven todos. Si el usuario quiere soltarlo, que sea explícito.

Reglas por tipo (`set_type_rules` para grupos) en vez de repetirlas chat por chat.

---

## Pedidos y tareas

Alguien pide información o encarga algo; queda despachado a quien corresponde.

**Pregunta:** "¿Todos los pedidos los atiende lo mismo, o según el tema va a alguien distinto?"

**Deja puesto:** un chat puede quedar asignado a un agente específico (`assign_chat_to_agent`), o quedar en el reparto general. Si hay temas que van a personas distintas, cada agente atiende lo suyo y el resto queda fuera de su vista.

Lo que no se resuelve solo: el agente escala (`escalate`) y aparece en pendientes.

---

## Marketing

Chats que entran desde la publicidad. Todos desconocidos, y llegan de golpe.

**Pregunta:** "El que te escribe por primera vez, ¿lo atendemos, lo dejamos esperando, o lo ignoramos?"

**Deja puesto:** la política del desconocido es la decisión central acá. Un contacto nuevo arranca sin atención hasta que alguien lo active — eso protege, pero si nadie mira, se pierden.

**Cuidado:** cuando llega mucho junto, el sistema **frena solo** el ritmo de despacho. No está roto: se está cuidando de que WhatsApp lo marque como spam. No lo fuerces.

---

## Ventas

Atender, cotizar, seguir. Que no se caiga ninguno.

**Pregunta:** "¿Quieres que conteste solo las consultas y te avise cuando hay que cerrar?"

**Deja puesto:** el chat en automático, y confirmación puesta para el momento de cerrar — o el chat entero en confirmación si el usuario prefiere leer todo. Es la combinación más pedida: rápido en lo de arriba, con freno en lo que importa.

---

## Lo delicado

Mensajes que necesitan mirada quirúrgica. Nada sale sin visto bueno.

**Pregunta:** "¿Qué chats son los que no pueden salir mal?"

**Deja puesto:** esos chats en **con confirmación**. Todo lo que el agente escriba queda como borrador esperando. Nadie puede sacarles esa condición salvo el dueño.

---

## Secretaria que aprueba

> Ya se puede armar: se habilita desde el tablero, botón **Aprueba MSG** en la fila de esa persona.
>
> ⏳ **Todavía no:** rechazar con motivo. Si descarta un borrador, el agente que lo escribió no se entera de por qué y puede repetir el error. Avísalo al armar el escenario.

Otra persona da el visto bueno sin poder cambiar las reglas.

**Pregunta:** "¿Quién más te ayuda?" · "¿Quieres que pueda aprobar lo que sale, o solo leer?"

**Deja puesto:** su chat marcado como **aprobador**. Con eso puede aprobar, rechazar con motivo y corregir antes de mandar. **No** puede cambiar reglas, ni sacar la confirmación de un chat, ni nombrar otros aprobadores. Esa separación es el punto.

El pin de aprobador lo pone el dueño — desde el tablero, o pidiéndoselo él mismo al agente.

---

## Muchos escriben, uno filtra

Modelos baratos redactando, uno caro aprobando o corrigiendo.

**Pregunta:** "¿Cuántos agentes quieres contestando?" · "¿Qué modelo quieres que revise?"

**Deja puesto:**
- Los que redactan: registrados como agentes, cada uno con sus chats asignados, chats en **con confirmación** (así todo lo que escriben queda como borrador).
- El revisor: agente aparte, marcado como aprobador, que trabaja sobre la cola de borradores.

**Dile esto al usuario, que es lo que hace que funcione:** cuando el revisor rechaza, el motivo vuelve al que escribió. No es solo un filtro — el que redacta aprende del rechazo y el siguiente sale mejor.

---

## Cuando pide algo que no está en la lista

Ubícalo primero: ¿cambia **cómo habla** el agente (reglas), **quién puede qué** (dueño y aprobadores), o **lo que el sistema hace** (producto)? Los tres se resuelven distinto — está en el índice de la skill.
