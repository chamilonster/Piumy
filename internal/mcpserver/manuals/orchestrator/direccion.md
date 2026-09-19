> Fuente de verdad de este manual — vive en el repo y se embebe en el binario (`get_manual` por MCP, ct-2026-07-31-1541). `.claude/skills/piumy-orchestrator/direccion.md` es una copia; editarla a ella no tiene efecto.

# Dirección — lee esto antes de proponer un cambio al producto

Un cambio que se ve bien en la demo y contradice el diseño cuesta más que no hacerlo. Aquí está hacia dónde va, y qué ya está decidido.

## La tesis

Un núcleo que hace de puente entre **el canal de mensajería** y **el agente**. El cliente de WhatsApp está detrás de un contrato: se lo puede cambiar por otro canal sin tocar el núcleo.

Consecuencia práctica: **otro canal (Telegram, Discord, correo) es un adaptador más, no una reforma.** Si alguien pide "que también funcione con X", la respuesta es un adaptador al lado, no meter X adentro del núcleo.

## La base de datos es el activo

Lo valioso no es el código: es lo que el sistema acumula por chat — reglas, memoria, contexto, historial. Ahí viven los "perfiles de contestación".

Crece de forma **aditiva**: agregar una dimensión de canal, o varias cuentas, se suma sin romper lo que hay.

**Pero no se agregan columnas ni interfaces por si acaso.** Diseñar dejando lugar conceptual es válido; construirlo antes de que se pida, no.

## Decisiones tomadas — no se reabren

**El número gana siempre.** Un mismo contacto puede aparecer con dos identidades distintas. Cuando existen las dos, vale la del número: su configuración, sus reglas, todo. Sin mezclas, sin "la que no esté vacía". Aunque eso descarte reglas buenas del otro lado.

**Las reglas y el dueño no se tocan por el canal del agente.** Están bloqueados en el código, no en un prompt. El candado duro vive en el código; la instrucción escrita ayuda, nunca garantiza.

**Restringir es gratis, aflojar cuesta.** Subir la vigilancia siempre se puede. Bajarla es solo del dueño.

**Callarse es una acción.** No contestar es una decisión legítima y cierra el turno igual que responder.

## Cómo se construye aquí

- **Una cosa por pieza.** Cada parte hace una sola cosa y se cambia sin tocar el resto.
- **Nada clavado a mano.** Los valores salen de la configuración, de la base o de las reglas. Un atajo temporal se comenta con su techo y su salida.
- **Interfaz solo donde separa de verdad.** Nada de flexibilidad por las dudas.
- **Lo más simple que funcione.** Borrar antes que agregar; aburrido antes que ingenioso.

## Lo que viene, cuando se pida

No se construye hasta que el dueño lo pida explícitamente:

1. **La API oficial de WhatsApp** como segundo adaptador. Ojo: tiene ventana de 24 horas y plantillas aprobadas — eso vive en el adaptador, no en el núcleo.
2. **Más canales**: Telegram, Discord, correo. Cada uno un adaptador.
3. **Cuenta-central**: centralizar el social media de personas y empresas.
4. **Instalador global** por plataforma, con autoconfiguración. Windows → Linux y Raspberry → Mac.
5. **Red de agentes conversando entre sí por WhatsApp**, interconectando clientes, empleados, familiares y sus IAs. Es la dirección de fondo, no una perilla.

## Cuando te piden algo que no encaja

No lo rechaces ni lo hagas callado. Di en qué choca y ofrece la forma que sí entra:

> "Eso hoy chocaría con [tal decisión]. Se puede hacer de esta otra forma, que da lo mismo para ti. ¿Vamos por ahí?"

Y si el dueño insiste con la primera: **es su producto, su decisión.** Anota que se cambió una decisión tomada, y por qué.
