# Changelog

Todos los cambios notables de piumy-gateway. Formato basado en
[Keep a Changelog](https://keepachangelog.com/es/). Lo más reciente arriba.
Se actualiza en cada deploy/build junto con el relanzamiento del gateway.

---

## 0.12.1 — 2026-09-23

### Added
- **Ahora puedes tener más de un Piumy abierto a la vez, cada uno con su
  propio número de WhatsApp.** En el ícono de la bandeja hay un ítem nuevo,
  **"Abrir otro Piumy"**. Cada cuenta nueva tiene su propia carpeta, su propia
  sesión de WhatsApp y su propio tablero: no se mezclan chats ni mensajes.
- **Cada cuenta se reconoce a simple vista.** El ícono de la bandeja cambia de
  color, y el nombre de la cuenta aparece en la bandeja, en el título de la
  ventana y en el tablero — con el mismo color en los tres. Cuando vinculas
  WhatsApp, el nombre pasa a ser el de tu WhatsApp seguido de los últimos 4
  dígitos del número (así dos cuentas con el mismo nombre no se confunden);
  hasta entonces se llaman "cuenta-2", "cuenta-3"…
- **La pantalla de acceso ya dice de qué cuenta es**, sin tener que entrar
  primero. Desde otra computadora de tu red, esa pantalla solo dice
  "cuenta-2": tu nombre y tu número no salen sin iniciar sesión.
- **Accesos directos listos.** Al abrir otra cuenta, Piumy deja un acceso
  directo en el Escritorio y en el Menú Inicio, con el ícono del color de esa
  cuenta, para volver a abrirla. Si tu Piumy ya arranca con Windows, la cuenta
  nueva también. Cuando vinculas WhatsApp, los accesos directos toman el mismo
  nombre que la bandeja.
- **La cuenta nueva abre con la misma clave del tablero** que la cuenta desde
  la que la abriste, no con la de fábrica (admin / piumy). No hay una segunda
  clave que recordar.
- **El tablero de una cuenta nueva se abre solo** mientras no tenga WhatsApp
  vinculado. El código QR no aparece solo: se genera cuando pulsas "Conectar
  QR".
- Quien arranca Piumy a mano puede elegir la cuenta con `--account nombre`.

### Fixed
- **Dos tableros abiertos a la vez ya no se cierran la sesión entre sí.**
  Antes, entrar al tablero de una cuenta te sacaba del de la otra.
- **El buscador de chats ya no se rellena con "admin" en Chrome.** El
  navegador tomaba la barra de búsqueda por el campo de usuario de la clave
  guardada.

### Sin cambios
- **Tu instalación de siempre queda igual**: sigue siendo la cuenta
  principal, con los mismos datos, la misma clave, el mismo ícono y la misma
  sesión de WhatsApp. Nada de lo nuevo se nota hasta que abres otra cuenta.

### Para quien arma el instalador
- `build-all.sh` ahora también arma el setup de Windows
  (`dist/Piumy-Setup-<versión>.exe`) si encuentra Inno Setup 6; si no lo
  encuentra, termina con un error que dice dónde buscó. En Linux y Mac avisa
  que el setup solo se arma en Windows y sigue.

---

## 0.12.0 — 2026-09-19

### Changed
- **Piumy ahora guarda sus datos en su propia carpeta del sistema**, no en
  el directorio desde donde se lo arranque. Antes, correrlo parado en otro
  lado dejaba ahí la base de conversaciones, la sesión de WhatsApp y las
  imágenes.
- **Tu instalación no cambia de lugar ni de comportamiento** — sigue
  usando exactamente los mismos archivos de siempre.

### Fixed
- **Arreglado de paso:** si la carpeta de datos no existía, Piumy fallaba
  al arrancar en vez de crearla. Solo pasaba en una instalación hecha a
  mano, sin instalador.

---

## 0.11.0 — 2026-09-17

### Added
- **La IA ya puede mandar varias piezas seguidas en una misma respuesta** —
  por ejemplo un mensaje y después un sticker, o una foto, o dos frases
  cortas como escribe una persona. Antes solo podía mandar una y tenía que
  esperar a que le contestaran.
- **Hasta 4 piezas por respuesta.** Sigue sin poder insistirle a alguien que
  no contestó — eso no cambió, y es a propósito.

---

## 0.10.1 — 2026-09-16

### Fixed
- **El buscador de chats mostraba un código interno** ("[placeholder.search_chats]")
  al abrir el tablero, en vez de "Buscar chats" — en los dos idiomas. Se
  arreglaba solo con cambiar de pestaña y volver a Chats, pero apenas se
  entraba quedaba así. Lo introdujo la traducción de Piumy (0.10.0) y
  estuvo unas horas en la máquina del dueño — corregido ahora.
- **Un par de textos no cambiaban de idioma al instante**: el botón de
  Configuración, el lápiz para editar el estado, y el aviso de ritmo de
  envío dentro de Opciones si esa pantalla ya estaba abierta al cambiar el
  idioma. Ahora se actualizan todos apenas se cambia el idioma, sin
  importar qué pantalla esté abierta en ese momento.

---

## 0.10.0 — 2026-09-16

### Added
- **Piumy ahora habla inglés y español.** Detecta el idioma del sistema
  operativo solo, y se puede cambiar a mano desde Opciones — la elección
  manual siempre gana sobre la detección automática. Cubre todo lo que
  Piumy le escribe a una persona, no solo el tablero: la pantalla de
  Opciones, la bandeja del sistema (el ícono junto al reloj de Windows),
  los avisos automáticos que salen por WhatsApp (por ejemplo, cuando el
  agente queda sin conexión), los mensajes de error que aparecen en
  pantalla, y el correo de recuperación de contraseña. Cambiar el idioma
  en Opciones se aplica al instante — tablero y bandeja incluidos — sin
  reiniciar Piumy ni recargar la página.

### Fixed
- **Los errores del servidor podían aparecer a medias en el otro idioma**
  — con Piumy en español, un error se leía igual "Error: store not
  available", mitad en inglés. Ahora los errores que le sirven a quien
  usa el tablero están en su idioma; los que son fallas internas (sirven
  para encontrar el problema, no para que un usuario los lea) quedan tal
  cual a propósito.

---

## 0.9.19 — 2026-09-11

### Fixed
- **Las notas de voz ya se reproducían bien; el manual del operador seguía
  advirtiendo lo contrario** (T154, ct-2026-09-11-1606-t154-sacar-del-flujo-19-la-advertencia-d).
  La causa real era una versión vieja de CleverCoder en la máquina, no un
  problema de Piumy — medido con el binario actual y la ruta real que usa
  el operador, la nota llega siempre en el formato que WhatsApp móvil
  reproduce. El manual ya no asusta al agente con una falla que no existe.
- **Agregar un agente desde el tablero podía rebotar con "ya existe"**
  (T155, ct-2026-09-11-1613-t155-el-alta-de-un-agente-nunca-se-niega). Ahora
  el que llega toma el lugar del que estaba registrado antes, y el tablero
  muestra cuál de las dos cosas pasó — alta nueva o reemplazo — en vez de
  negarse.
- **Un agente que reabría su terminal con otro puerto de antena dejaba de
  recibir despachos, sin ningún aviso** (T156, ct-2026-09-11-1625-t156-el-agente-re-registra-su-puerto-en).
  La guía de conexión estaba escrita como un procedimiento de una sola vez,
  así que nadie se la volvía a leer al reconectarse. Ahora dice explícito
  que se corre en cada conexión, y cómo reconocer el síntoma cuando el
  puerto registrado quedó viejo.

---

## 0.9.18 — 2026-09-07

### Added
- **Cuando alguien responde citando un mensaje, el agente ahora sabe a
  cuál** (T151, ct-2026-09-07-1901). Pedido del dueño. El aviso que le
  llega al agente incluye el identificador del mensaje citado y un
  extracto de una línea de su texto — o una marca como "[imagen]" si lo
  citado era una foto o un audio.
  Va el extracto y no solo el identificador porque, con el número suelto,
  la IA tendría que pedir el mensaje aparte para saber de qué le hablan.
  El gateway ya guardaba esa relación: la usa para que una respuesta
  citada vuelva al agente que escribió el original. Eso no se tocó.
  **El mensaje citado se busca siempre dentro del mismo chat**, así que
  un extracto nunca puede venir de otra conversación.

---

## 0.9.17 — 2026-09-07

### Fixed
- **Un agente dejaba de poder trabajar justo después de responder** (T150,
  ct-2026-09-07-1839). Al terminar de atender un mensaje, el sistema
  bloqueaba también las herramientas que nunca necesitaron permiso —
  incluida la de diagnóstico. Un agente quedaba ciego exactamente cuando
  el dueño le preguntaba si el gateway estaba trabado, y no podía ni
  mirar si el canal andaba. Le pasó a un agente y después al principal,
  que no pudo avisar en un grupo donde el dueño estaba esperando.
  Lo encontró el agente de temascal.cl: notó que el manual promete que la
  herramienta de diagnóstico responde siempre, y el código no lo cumplía.
  Ahora se comprueba primero si la herramienta necesita permiso; si no lo
  necesita, pasa sin importar en qué estado quedó el turno anterior.
  **Lo que sí protege sigue protegiendo:** las herramientas reservadas al
  dueño siguen rechazadas, y reutilizar un turno ya cerrado para actuar
  sobre su mismo chat también.

---

## 0.9.16 — 2026-09-07

### Added
- **Freno de ritmo para las acciones que se ven desde afuera** (T149,
  ct-2026-09-07-1730). Crear grupos, agregar participantes, dar
  administración, poner ícono o descripción, y cambiar la foto o el
  estado de la cuenta ahora se espacian solas si alguien las dispara
  seguidas. Pedido del dueño, textual: *"esos frenos de en masa deben ir
  como frenos, no como candados"*.
  **El freno nunca rechaza**: espera y deja pasar. Y una acción suelta no
  espera nada — está pensado contra la ráfaga, no contra el uso normal.
  La espera es al azar entre 8 y 25 segundos, nunca un intervalo fijo,
  que es justo lo que hace que un número parezca un robot.
- **Los manuales explican ahora cómo se hace bien**, no solo qué está
  prohibido: que el número es uno solo y si cae por spam se quedan sin
  canal todos los agentes, y que si dos acciones seguidas tardan no está
  roto — se está espaciando a propósito, no hay que reintentar.

---

## 0.9.15 — 2026-09-07

### Fixed
- **Al crear un grupo, el dueño vuelve a quedar administrador solo**
  (T145, ct-2026-09-07-1456). Fallaba con un rechazo de WhatsApp y había
  que corregirlo a mano. La causa resultó ser de tiempo: WhatsApp
  devuelve el grupo ya creado, pero todavía no acepta consultas sobre él
  durante unos segundos. Ahora se reintenta hasta tres veces, con una
  espera al azar entre 2 y 7 segundos — nunca un intervalo fijo, que es
  justo lo que hace que un número parezca un robot. Si aun así falla, el
  grupo queda creado igual y se avisa.

---

## 0.9.14 — 2026-09-07

### Changed
- **Un agente ya puede trabajar sin pedir permiso** (T148,
  ct-2026-09-07-1644). Pedido del dueño, textual: *"si le pido a un
  agente que cree un grupo, quiero que lo haga"* y *"resulta ser que
  piumy estaba lleno de candados estúpidos"*. Salieron **doce** de trece
  candados: crear grupos, agregar participantes, dar administración,
  poner ícono y descripción de grupo, cambiar la foto y el estado de la
  cuenta, recablear la antena del principal, recuperar la clave del
  tablero, repartir chats entre agentes, borrar agentes y actualizar
  credenciales de otro agente. Ninguno protegía nada: solo obligaban a
  dar un rodeo.
- **Queda un solo candado: el freno de emergencia anti-ban**
  (`set_kill_switch`). El dueño acotó qué se cuida — *"lo único que hay
  que cuidar realmente es no cagarla con whatsapp espamear su ip"*— y ese
  freno es exactamente esa protección. Un agente que puede apagar su
  propio freno lo deja sin efecto.
- **El ritmo de envío no se tocó y no dependía de esto.** El control
  anti-ban vive en la cola de salida, no en los permisos: todo mensaje
  pasa por él, lo mande quien lo mande. Verificado que ninguna de las doce
  herramientas abiertas puede saltearlo.
- **Los manuales se actualizaron en el mismo cambio.** Un texto que
  sigue diciendo "solo el dueño" hace invisible lo que se acaba de abrir.

---

## 0.9.13 — 2026-09-07

### Fixed
- **El gateway dejaba de entregar mensajes del dueño, en silencio** (T147,
  ct-2026-09-07-1526). Si le escribías mientras el agente estaba
  terminando de responder otra cosa, tu mensaje nuevo quedaba marcado
  como atendido sin que nadie lo hubiera leído: no llegaba, no volvía a
  aparecer, y no aparecía ningún error. El reporte original era otro —
  "el canal se traba 15 minutos"— que resultó ser el síntoma menor y
  visible; la pérdida de mensajes es lo que estaba debajo y no se veía.
  Pasaba con solo escribir dos veces seguidas en un grupo.
- **Los avisos de error del canal decían la causa que suponían, no la
  que sabían** (T147). Cuando un turno no se podía abrir, el sistema
  afirmaba "probablemente fue reemplazado por otro más nuevo" sin haberlo
  verificado — y en la investigación de este mismo problema esa frase
  resultó falsa y costó tres rondas de diagnóstico. Ahora el sistema
  recuerda por qué cada turno salió de circulación —atendido, reemplazado,
  vencido o cancelado— y lo dice tal cual. Si no lo sabe, lo dice también.

---

## 0.9.12 — 2026-09-07

### Added
- **Actualizar un agente pegando su link de antena de una línea** (T141,
  ct-2026-09-07-1155). En la tarjeta de cada agente del tablero ahora hay
  un campo para pegar la línea completa de la antena: llena solo el
  endpoint, el terminal y el PIN, y con apretar Guardar queda. Antes había
  que borrar el agente y crearlo de nuevo, porque el pegado de una línea
  existía únicamente al dar de alta. El nombre no se pisa si el agente ya
  tenía uno.
- **Ahora se puede dar de alta a otro agente, no solo a uno mismo** (T142,
  ct-2026-09-07-1222). Pedido del dueño: *"quiten ese candado, no tiene
  logica"*. Hasta acá cada agente solo podía registrarse a sí mismo, y ni
  siquiera el principal podía anotar a otro — pero el tablero ya permitía
  crear cualquier agente sin esa restricción, así que el candado no
  protegía nada: obligaba a dar un rodeo. `register_agent` acepta ahora un
  `agent_id` opcional; si se omite, se comporta igual que siempre.

### Fixed
- **El tablero ya no borra lo que estás escribiendo en la tarjeta de un
  agente** (T141). El refresco automático de cada 15 segundos reconstruía
  las tarjetas enteras, llevándose lo recién pegado. Ahora la tarjeta que
  estás editando se queda quieta y el resto del tablero se sigue
  actualizando solo — el "cero F5" queda intacto.
- **Los manuales que leen los agentes afirmaban una regla que ya no
  existe** (T143, ct-2026-09-07-1236). Decían que darse de alta era "solo
  para vos mismo". Un agente lo leía, lo creía, y no usaba la capacidad
  recién abierta. Corregido en el manual del operador, junto con la firma
  de la herramienta.

---

## 0.9.11 — 2026-09-07

### Added
- **Ahora se puede dar administración de un grupo a alguien** (T136,
  ct-2026-09-03-1627). Antes solo se podía al crear el grupo; si el grupo
  ya existía y quedaste como participante común, no había forma de
  arreglarlo desde acá. La herramienta es solo para el dueño.
- **Al crear un grupo, el dueño queda administrador**, y el grupo se
  guarda con su nombre y su lista de miembros (T135,
  ct-2026-09-03-1546). Antes se creaba el grupo y se tiraba a la basura
  el nombre y los miembros que WhatsApp ya había devuelto: el chat
  quedaba sin nombre y sin nadie adentro. Si la promoción o el guardado
  fallan, el grupo queda creado igual — se avisa, no se pierde.
- **El gateway se entera de los cambios mientras corre** (T138,
  ct-2026-09-03-1722). Un grupo renombrado, una foto nueva, alguien que
  entra a un grupo, un grupo nuevo: antes nada de eso se veía hasta el
  siguiente reinicio. Ahora llega en el momento. La foto no se baja de
  golpe — se marca para revisar y se busca cuando le toca, respetando el
  ritmo que evita que WhatsApp se moleste.
- **La onda de la nota de voz sale del audio de verdad** (T128,
  ct-2026-09-03-0133). La de la versión anterior era un dibujo
  provisorio, siempre igual, sin relación con lo que se escuchaba.
  Ahora se mide del audio real y se estira de punta a punta para que se
  noten los altos y los bajos. Además, si CleverCoder ya midió la onda
  del audio original, esa gana sobre la calculada acá.

### Fixed
- **Los tildes azules en los grupos** (T126, ct-2026-09-02-2246). El
  acuse de lectura de un grupo se mandaba al grupo y no a la persona que
  había escrito, así que nunca se teñían.
- **Un agente podía quedar mudo sin un solo error a la vista** (T129,
  ct-2026-09-03-0200). Si el nombre del agente y el de su antena no
  coincidían, los despachos se guardaban bajo un nombre y el agente
  preguntaba por el otro: nadie le contestaba nunca. Ahora se lo
  reconoce por cualquiera de los dos.
- **Lo mismo, para el agente principal** (T133, ct-2026-09-03-0634). Era
  el mismo problema pero peor: el principal atiende todo chat sin
  asignar, así que lo afectaba a él y a todos los chats sueltos. Venía
  funcionando de casualidad, porque nadie había tocado esos dos valores.
- **Cuando el dueño habla en un grupo, el agente lo escucha como el
  dueño** (T132, ct-2026-09-03-0624). Verbatim del dueño: *"distinto es
  que el boss hable en un grupo, el agente del grupo recibe ese mensaje
  como boss"*. Antes el nivel salía solo del chat, y en un grupo eso no
  puede acertar: el grupo no es nadie en particular. Ahora sale de quien
  escribió. En un chat de a dos no cambia nada.
- **El formulario de "+ Nuevo agente" ya no se cierra solo** (T140,
  ct-2026-09-05-1607). El tablero se refresca solo cada 15 segundos, y
  ese refresco borraba el formulario mientras se estaba llenando. Ahora
  el formulario se queda quieto y el resto del tablero se sigue
  refrescando. Además, pegar la antena alcanza: los campos sueltos
  quedaron escondidos detrás de "Editar campos manualmente", y se
  reabren solos si falta un dato.
- **La lista de miembros de un grupo ya no crece para siempre** (T139,
  ct-2026-09-03-1900). Nunca hubo forma de sacar a nadie de esa lista:
  solo se sumaba. Quien se iba de un grupo seguía figurando adentro.
  Ahora sale quien sale — y sale solo de la lista del grupo: su chat y
  sus marcas quedan intactos.
- **Las notas de voz salen mejor preparadas para el teléfono** (T136,
  ct-2026-09-03-1627). Se ordena el envoltorio del audio antes de
  mandarlo, siguiendo lo que WhatsApp espera. Son tres de las cuatro
  cosas que hacen falta para que se reproduzca en el celular; **la
  cuarta es del lado de CleverCoder** y todavía no está. Los bytes del
  audio no se tocan, y si el archivo no se entiende se manda igual, tal
  cual.
- **Corregido el conteo de herramientas en la documentación** (T137,
  ct-2026-09-03-1710): decía 55 y son 57. Importaba porque el número
  estaba justo en el párrafo que le explica a un agente por qué le
  rechazan una herramienta.

---

## 0.9.10 — 2026-09-02

### Added
- **Las notas de voz que manda el agente ahora dibujan la onda del audio**,
  en vez de mostrarse como una línea recta.

### Fixed
- **El aviso ahora dice el nombre del chat, no un número interno** (T124,
  ct-2026-09-02-2210). En un grupo, el "de:" del aviso mostraba el ID
  interno de WhatsApp — un número de 18 dígitos sin significado para
  nadie — en vez del nombre del grupo. Ahora muestra el nombre.
- **Se corrigió de raíz un problema que hacía perder avisos** (T125,
  ct-2026-09-02-2221). Algunos números de WhatsApp llegan con una
  coletilla técnica del aparato pegada al final; si no se le sacaba, el
  sistema podía tratar el mismo chat como si fueran dos distintos, y
  algún aviso se perdía en el camino. Ahora se le saca siempre, en
  cualquier lugar donde se guarda un chat — no solo donde ya se sabía que
  hacía falta.

---

## 0.9.9 — 2026-09-02

### Added
- **El agente ya puede mandar fotos y notas de voz por WhatsApp** (T122,
  ct-2026-09-02-2045; T123, ct-2026-09-02-2121). Pasan por el mismo
  control anti-baneo que un mensaje de texto — no salen instantáneas, y
  cuentan contra el mismo tope diario. Si el chat pide confirmación antes
  de contestar, el borrador que queda esperando aprobación muestra la
  foto (o deja escuchar la nota de voz) antes de que se apruebe — no hay
  que aprobar a ciegas.

---

## 0.9.8 — 2026-09-02

### Added
- **Ya se puede elegir qué agente atiende cada grupo de WhatsApp, desde el
  tablero** (T119, ct-2026-09-02-1700). El control para asignar agente ya
  existía para los chats 1:1 pero nunca se había agregado al header de un
  grupo — ahora está ahí, igual de simple.

### Changed
- **Un grupo ya no se puede marcar como Jefe** (T121, ct-2026-09-02-1722).
  Boss es un número, identifica a una persona — un grupo no lo es. La
  opción se sacó del selector del tablero y, aparte, el sistema la
  rechaza aunque alguien intente ponerla directo por la API.

### Fixed
- **Poner un grupo en "Confirmar" ahora lo prende de verdad** (T120,
  ct-2026-09-02-1712). Antes, el tablero guardaba la elección sin error
  pero el grupo se quedaba "Ignorado" por dentro — no llegaba ni un
  mensaje suyo a la cola aunque el dueño hubiera elegido atenderlo.

---

## 0.8.2 — 2026-09-01

### Added
- **Ya se puede cambiar la foto de perfil de WhatsApp de la cuenta** (T111 +
  T112c), desde el tablero o desde un agente. Si mandas un PNG, el programa
  lo convierte solo — y el ícono de grupo, que también rechazaba PNG, quedó
  igual de flexible. También se puede quitar la foto.

- **El estado de WhatsApp se edita desde el tablero** (T112c), junto a la
  foto y con el número debajo. El nombre de la cuenta se muestra grande pero
  no se puede cambiar: WhatsApp no ofrece esa operación a ningún programa,
  se cambia desde el teléfono.

### Changed
- **Una sola barra de título** (T112d). El tablero tenía dos apiladas: la
  del sistema y una decorativa propia. Quedó la del sistema.

- **La carita es más chica y dice "piumy" al lado** (T112b), alineadas a la
  izquierda. El indicador de conexión se mudó junto al nombre, separado del
  indicador de ánimo — son dos cosas distintas.

### Fixed
- **En modo privacidad, un nombre que es un número ahora se tapa** (T110).
  Cuando un contacto no está guardado, WhatsApp entrega su número como si
  fuera el nombre, y ese quedaba a la vista mientras el número de abajo sí
  aparecía tapado. Se revisaron los siete lugares donde eso pasaba, incluida
  la ventana de editar, que mostraba el identificador completo.

- **En un grupo, responderle a una persona ya no da por atendidas a las
  demás** (T108). Antes, contestarle a alguien marcaba como respondidos los
  mensajes de todos los demás participantes y el agente se quedaba sin turno
  para contestarles: alguien podía preguntar algo y quedarse sin respuesta
  mientras el sistema creía haberlo atendido.

- **El gateway reconoce quién habla dentro de un grupo** (T107). Antes era
  imposible: WhatsApp identifica a las personas de una forma dentro de los
  grupos y de otra fuera, y el sistema solo conocía la de fuera.

---

## 0.8.1 — 2026-09-01

### Fixed
- **El chat se abre más grande y el botón de cerrar quedó adentro del
  marco** (T109, ct-2026-09-01-1426). Pedido del dueño: el marco pasó de
  360x640 a 460x760, la X está dentro y es casi el doble de grande.

- **La ventana de editar reglas dejó de ser diminuta.** Pasó de 440 a 720
  de ancho y las cajas de texto de 90 a 220 de alto: ahí se edita prosa
  —reglas, memoria y contexto de un chat— y cada párrafo entraba en tres
  renglones.

- **Las barras de desplazamiento usan el color del tema** en todo el
  tablero, no solo en la vista de chat. Antes aparecía la barra blanca del
  sistema sobre el fondo oscuro.

- **El tablero deja de mostrarse desactualizado después de actualizar.**
  Sus archivos se servían sin avisarle al navegador que habían cambiado, así
  que se podía instalar una versión nueva y seguir viendo la pantalla
  anterior. Vale para cualquier cambio futuro del tablero, no solo para
  estos.

---

## 0.8.0 — 2026-08-30

### Added
- **Un agente puede saber con qué identidad lo ve el gateway y si es el
  principal** (T104+T103, ct-2026-08-29-1818 + ct-2026-08-29-1759). El
  sistema detecta y avisa cuando esa identidad viene cruzada — la falla que
  dejaba el canal tomado 20 minutos por mensaje, con chats de terceros
  esperando detrás. El error de despacho ajeno nombra la causa real en vez
  de sonar a permiso denegado, y el estado de WhatsApp de la cuenta se
  puede leer sin la contraseña del tablero.

- **Una respuesta larga sale partida en varios mensajes** (T101,
  ct-2026-08-29-1651), con una pausa al azar entre uno y otro y el
  "escribiendo..." antes de cada pedazo. Si falla un pedazo a la mitad, el
  reintento no repite los que ya salieron.

### Fixed
- **Si el dueño contesta desde su teléfono, el agente ya no contesta
  encima** (T100, ct-2026-08-29-1649). El contacto deja de recibir dos
  respuestas. Además esos mensajes se guardan: el tablero muestra la
  conversación completa.

- **Los manuales dejan de enseñar cuatro cosas que el código ya no hacía**
  (T105, ct-2026-08-29-2129), y un turno colgado se libera en 5 minutos en
  vez de 15, para toda instalación y no solo la que tenga la variable
  puesta. El texto del tablero sobre la red local deja de empezar con
  "nunca".

### Removed
- **Se va un estado que no podía ocurrir nunca** (T106,
  ct-2026-08-29-2234) y que ocupaba la prioridad más alta en la pantalla
  del display.

### Nota de mantenimiento
- Los commits volvieron a quedar enlazados a su contrato — faltaba el
  gancho en los worktrees, y el repositorio que el sistema lee estaba
  once commits atrás.

## 0.7.1 — 2026-08-29

### Added
- **Si se corta la conexión con WhatsApp, Piumy reintenta solo** (T99,
  ct-2026-08-29-1607), espaciando cada vez más entre intentos y sin usar
  cifras fijas — nunca se queda parado esperando que el dueño haga algo.

  El arreglo de fondo fue el contador de intentos: antes se reiniciaba con
  cualquier conexión, aunque durara apenas dos segundos, así que un canal
  que entraba y se caía en ciclo reintentaba prácticamente sin pausa.
  Ahora el contador solo se reinicia cuando la conexión se sostiene un
  rato — recién ahí Piumy vuelve a considerarse recuperado.

- **El tablero ahora muestra tres situaciones que antes no se veían**
  (T98, ct-2026-08-29-0621): la conexión que se degrada, el rechazo del
  servidor, y sobre todo cuando otro proceso le toma la sesión de
  WhatsApp — este último ahora avisa fuerte, no se esconde entre líneas
  de registro.

### Fixed
- **Un caso donde el gateway podía quedar mudo, sin volver a reaccionar
  nunca, se encontró y se arregló antes de que le pasara al dueño.** Bajo
  ciertas condiciones, la conexión con WhatsApp podía degradarse sin
  llegar a cortarse del todo — y quedarse así, indefinidamente, sin que
  nada la reactivara. Ya no puede pasar.

## 0.7.0 — 2026-08-29

### Fixed
- **El tablero daba por desconectado a un agente que seguía vivo, solo
  porque hacía rato que no llamaba a ninguna herramienta** (T97,
  ct-2026-08-29-0218). Antes de marcarlo así, el gateway ahora pregunta
  primero — y la pregunta es silenciosa: no le cuesta nada al agente, no le
  aparece nada en su contexto. Si contesta, sigue conectado; si no, recién
  ahí se lo marca. El tablero deja de mentir sobre quién sigue trabajando.

### Added
- **El estado de WhatsApp ("About") ahora se ve debajo del nombre, en el
  tablero** (T96, ct-2026-08-28-1743). Completa un pedido que había quedado
  a medias: se podía editar el estado, pero no verlo.

  De paso se arregla un daño silencioso: abrir la ventana de configuración
  y guardar sin tocar ese campo borraba el estado que el dueño ya había
  puesto, porque el campo arrancaba siempre vacío. Ahora se pre-llena con
  el valor actual, y guardar un estado nuevo se refleja en el tablero al
  instante, sin recargar la página.

  Un estado en blanco —algo normal en WhatsApp— simplemente no muestra
  ningún renglón, en vez de un aviso que parecería un error.

## 0.6.9 — 2026-08-29

### Fixed
- **El error dejaba creer que faltaba un permiso cuando el turno solo había
  vencido** (T87, ct-2026-08-28-0454). Un agente recibía un mensaje, el
  gateway se reiniciaba, y al intentar trabajar sobre ese mensaje le decían
  *"refused: no active dispatch (default DENY)"*. Eso se lee como permiso
  denegado; la causa real era otra, y opuesta: el turno dejó de existir, el
  mensaje no se perdió, y va a volver a despacharse.

  La diferencia importa porque las respuestas correctas son contrarias: ante
  un permiso denegado hay que parar; ante esto, esperar. Un agente que leyera
  el mensaje viejo le reportaría al dueño un problema que no existe.

  Ahora se distinguen. **El rechazo del caso legítimo —un identificador
  inventado— queda exactamente igual**: esto no afloja ningún control.

  Los turnos siguen sin guardarse en disco, a propósito: uno a medio camino
  que sobreviviera a un reinicio quedaría viciado. Es más limpio que el
  mensaje vuelva de cero.

- **Una condición de carrera en la lectura del tiempo de expiración** (T87),
  encontrada por Tourmaline en su propia revisión antes de entregar. El valor
  se leía fuera del candado mientras otro proceso lo modifica en cada ciclo:
  el tipo de defecto que aparece de forma intermitente meses después y no hay
  manera de reproducir.

## 0.6.8 — 2026-08-28

### Fixed
- **Un acuse de lectura fallido ya no se pierde para siempre** (T91,
  ct-2026-08-28-1354). Reporte del dueño: *"tengo texto que se inyectó pero no
  le veo los tikes azules"*, y después *"incluso hay texto entremedio sin
  tikes azules"*. Ese "entremedio" era la pista: no fallaban todos, fallaban
  los que caían en una ventana mala.

  Evidencia del log real: el despacho salía bien y el marcado de leído del
  mismo segundo fallaba porque el enlace con WhatsApp estaba caído en ese
  instante. El error se anotaba y ahí moría — sin reintento. Esos tildes se
  perdían.

  Ahora esos mensajes se marcan cuando la conexión vuelve. **Sin cola nueva**:
  el propio registro de "sin acuse" ya existía en la base, así que el reintento
  sobrevive a un reinicio por construcción.

  **Y sin ráfaga**: los chats se procesan de a uno, con una espera aleatoria
  entre cada uno. Marcar cuarenta de golpe al reconectar es exactamente el
  patrón que WhatsApp castiga. Se descartó reusar el marcado existente
  justamente porque lanza una tarea por llamada, y todas habrían despertado
  dentro de la misma ventana.

## 0.6.7 — 2026-08-28

### Changed
- **En modo privacidad, un número que ocupa el lugar del nombre se tapa
  parcialmente** (T94, ct-2026-08-28-1715). Pedido del dueño: con todo tapado,
  los chats sin nombre se veían idénticos y no podía seguir de cuál hablaba
  mientras mostraba su trabajo.

  Se muestran los tres primeros dígitos —el código de país, que no identifica
  a nadie— y una etiqueta corta. **Nunca los últimos**, que es justamente lo
  que se usa para reconocer a una persona.

  La primera versión usaba un hash del número. Se descartó al probarla: con
  500 números del mismo prefijo apareció una colisión, o sea dos personas
  distintas con el mismo tapado. Un hash no puede garantizar lo contrario.
  Ahora cada número recibe un índice correlativo, que nunca se repite. A
  cambio, las etiquetas no sobreviven a recargar la página.

### Notas
- **El nombre autoproclamado ya se mostraba, y no hacía falta cambiar nada.**
  Se verificó que los tres caminos que registran un chat con un mensaje real
  ya guardan ese nombre, con un test que lo prueba desde hace tiempo.

  El caso que se veía era otro: un chat que **el dueño inició** hacia un número
  nuevo. Ahí WhatsApp todavía no entregó cómo se llama esa persona —no hay
  forma de pedírselo— así que el título cae al número hasta que conteste o
  hasta que WhatsApp lo sincronice. Se resuelve solo.

## 0.6.6 — 2026-08-28

### Added
- **El estado de WhatsApp se puede cambiar desde el tablero** (T92,
  ct-2026-08-28-1505). Ya se podía por MCP; ahora también desde Config, por el
  mismo camino — una sola implementación con dos entradas, no dos copias.

### Notas
- **El nombre de la cuenta no se puede cambiar, y no es una decisión de este
  proyecto.** La librería que habla con WhatsApp no expone escritura del
  nombre de perfil, solo del estado. Se verificó contra tres versiones
  distintas antes de afirmarlo: el campo de nombre existe pero es un espejo de
  solo lectura, que se actualiza cuando se cambia desde el teléfono.

  La pantalla no ofrece ningún campo para intentarlo —uno que parezca editable
  y no lo sea es peor que no tenerlo— y una nota indica dónde se cambia de
  verdad.

- **Todavía no se muestra el estado vigente bajo el nombre**, que era la otra
  mitad del pedido. Verificar la lectura necesita una sesión real de WhatsApp,
  que no se toca desde el desarrollo. Queda contratado aparte.

## 0.6.5 — 2026-08-28

### Fixed
- **El modal de Configuración se cortaba abajo y dejaba campos inalcanzables**
  (T95, ct-2026-08-28-171540). Reporte del dueño: *"el popup de opciones debe
  tener su propio scrollbar"*. El contenido fue creciendo con cada agregado y
  nadie miró el alto total: en una pantalla normal, el campo de la espera
  —entregado en 0.6.3, y pedido justamente porque los mensajes tardaban—
  quedaba fuera de la ventana, sin forma de llegar a él.

  Ahora el modal nunca supera el alto de la pantalla y su contenido scrollea,
  con la barra de título fija arriba. Aplica a los nueve modales del tablero.

  **Esto no contradice la regla de "nada de scroll interno"** que rige para la
  lista de chats: esa lista vive dentro de la página y puede crecer libre. Un
  modal flota sobre la pantalla y no puede crecer más que ella — o scrollea
  adentro, o es inalcanzable. Se usó un selector que no alcanza al contenedor
  de la tabla, y se verificó que su encabezado pegajoso sigue funcionando.

- **La explicación del límite de espera no se entendía** (T95). El dueño:
  *"ni yo entiendo"*. Decía "fuerza el despacho aunque el chat siga activo",
  que solo tiene sentido si ya se sabe que la espera se reinicia con cada
  mensaje nuevo. Reescrita desde el problema que resuelve: si alguien escribe
  sin parar, sin este límite el agente podría no recibir nada nunca.

## 0.6.4 — 2026-08-28

### Changed
- **El modo privacidad tapa solo los números, no los nombres** (T93,
  ct-2026-08-28-150547). El dueño: *"te pedí borrar los numeros, no los
  nombres"*.

  Su pedido original hablaba de números de teléfono; el alcance se había
  ampliado a nombres y avatares razonando la intención, sin que él lo
  aprobara. Vuelven a verse nombres de contacto y de grupo, avatares, el
  nombre propio de la cabecera y el alias de WhatsApp. Se sigue tapando todo
  número, incluido el propio.

  El aviso del botón ahora dice explícitamente que tapa **solo** números —
  con los nombres visibles, es más fácil suponer de menos que de más.

### Fixed
- **Un chat sin nombre habría mostrado su número en texto plano** (T93).
  Varias listas pintan "el nombre del contacto, o si no tiene, su número". Al
  dejar de enmascarar nombres, ese segundo caso destapaba un número real
  disfrazado de nombre — justo lo que el modo debe seguir ocultando.
  Encontrado antes de publicarse, con la excepción correcta para los
  identificadores de grupo, que no son teléfonos.

## 0.6.3 — 2026-08-28

### Added
- **La espera antes de pasarle los mensajes al agente se mueve desde el
  tablero, y se aplica en caliente** (T90, ct-2026-08-28-1350). Reporte del
  dueño: *"se estan demorandodo mucho en entrar los menjes, falta esa perilla
  tambien"*.

  La espera existe a propósito —agrupa los mensajes seguidos para que el
  agente reciba una conversación y no cuatro despachos sueltos— pero venía en
  **60 segundos**, que en un chat en vivo es una eternidad, y solo se podía
  cambiar con una variable de arranque.

  Ahora se cambia desde Config y **el despacho siguiente ya usa el valor
  nuevo, sin reiniciar**. Eso era el requisito: el punto de una perilla es
  probar, sentir y ajustar.

  **Cero es una elección válida**: despacho apenas llega, un despacho por
  mensaje. También se puede mover el techo, con la validación de que no quede
  por debajo de la espera normal.

### Fixed
- **Poner la espera en cero habría hecho caer el gateway** (T90). El retardo
  aleatorio se calcula sobre una fracción de la espera, y con cero esa cuenta
  entra en pánico. Encontrado al implementar la opción, antes de que existiera
  forma de activarla.

## 0.6.2 — 2026-08-28

### Fixed
- **El desplegable de asignación decía el nombre de una persona y guardaba un
  puesto** (T89, ct-2026-08-28-0628). Elegir al agente principal nunca ató el
  chat a ese agente: guardaba el identificador del PUESTO, que no cambia
  aunque el puesto lo ocupe otro. La etiqueta no lo decía, así que el dueño
  pidió una opción que ya tenía. Ahora se lee **"Principal — <nombre>"**.

- **Promover un agente ya no deja los chats asignados apuntando a la nada**
  (T89). Al promover, la asignación seguía nombrando el identificador viejo,
  que después de la promoción ya no atiende a nadie: el chat quedaba retenido
  en silencio, o caía a un destino que el dueño nunca eligió. Ahora esas
  asignaciones se migran al puesto en el mismo movimiento.

  Verificado a través de dos promociones consecutivas: el chat siguió al
  puesto sin que nadie lo tocara.

## 0.6.1 — 2026-08-28

### Fixed
- **Promover un agente ya no deja una ficha fantasma** (T88,
  ct-2026-08-28-0626). Reporte del dueño: *"hay 2 citrinos en opciones pero
  existe solo uno"*.

  Promover es un intercambio: los datos del que baja se guardan en la fila que
  el que sube acaba de dejar libre. Eso protege sus credenciales. Pero si el
  principal que bajaba **no tenía nada** —el caso real: se había borrado el
  agente anterior— el intercambio escribía una fila vacía, y quedaba un agente
  sin nombre ni antena apareciendo en todos los desplegables.

  Ahora, cuando el que baja no tiene endpoint ni antena, la fila se borra en
  vez de escribirse: no hay nada que preservar. Tener nombre no lo salva —un
  agente sin forma de recibir un despacho no es un agente— y hay un test que
  lo fija.

## 0.6.0 — 2026-08-28

### Added
- **Modo privacidad: un ojito que tapa los datos personales del tablero**
  (T85, ct-2026-08-28-0059). Pedido del dueño: *"asi puedo mostrar mi trabajo
  en stream sin exponer a nadie"*.

  Tapa números, nombres de contacto y de grupo, avatares y el número propio de
  la barra. Cada contacto recibe una etiqueta corta y **estable** —el mismo
  chat se ve igual toda la sesión— así se pueden seguir distinguiendo las
  filas sin revelar quién es cada una.

  **El estado se lee antes de que exista un solo pedido de datos en vuelo.**
  Ese era el requisito que decidía si la función servía o no: si el tablero
  pinta y después tapa, en un stream el número ya quedó grabado en ese cuadro
  — y peor, quien lo usa cree estar protegido. Acá no hay ventana por
  construcción, no por promesa, y sobrevive a recargar la página.

  **Alcance más amplio que "los números":** un nombre completo o una foto de
  perfil exponen igual. El alias de WhatsApp (*pushname*) se suprime entero en
  vez de taparse — un alias tapado sigue siendo la pista de que hay uno.

  **Lo que NO tapa, a propósito:** la vista previa de los mensajes, que es
  justamente lo que se quiere mostrar. Si alguien escribió un número dentro de
  un mensaje, eso queda visible — el control lo advierte.

### Fixed
- **Con el modo recién activado, dos pestañas seguían mostrando datos reales
  hasta 15 segundos** (T85). Agentes y Borradores no estaban en la cadena de
  repintado inmediato: se refrescaban solas por un temporizador. Con el botón
  ya en verde, el nombre real seguía en pantalla. Encontrado probándolo en
  vivo, no leyendo el código.

## 0.5.6 — 2026-08-28

### Fixed
- **La ficha del agente principal ahora lista sus números asignados** (T86,
  ct-2026-08-28-0451). El bloque entero —listar, quitar y buscar para
  asignar— se saltaba al principal por una condición que venía de cuando el
  principal **no podía** ser destino de una asignación. La 0.2.1 lo permitió;
  esta parte del tablero se quedó describiendo el mundo anterior.

  Sin cambios de servidor: consultar los chats de un agente y asignarle uno ya
  eran agnósticos desde entonces. Era una condición sobrante del lado de la
  pantalla.

- **La zona de credenciales de la ficha deja de desbordarse** (T86). Reporte
  del dueño: *"esta zona no tiene bien los margenes"*. Eran dos cosas: el
  identificador de terminal chocaba contra el borde derecho —ahora se corta
  con puntos suspensivos, y sigue siendo copiable entero porque es un campo
  real, no texto recortado— y "Borrar agente" vivía fuera del contenedor que
  las otras dos filas de botones usaban, así que le faltaba su margen. Ese
  hueco era el "espaciado irregular" que se veía.

## 0.5.5 — 2026-08-27

### Fixed
- **El diálogo de borrar un agente ahora cambia de estado cuando termina**
  (T82, ct-2026-08-27-2256). Reporte del dueño: *"borré a selenita, pero es
  extraño, la ventana no cierra"*. Después de borrar seguía mostrando los dos
  botones originales, y los dos habían quedado sin sentido: "Borrar agente"
  apuntaba a algo que ya no existía, y "Cancelar" ofrecía cancelar lo que ya
  había pasado. El dueño quedó sin saber qué apretar.

  Ahora, al completarse: el botón destructivo desaparece —no queda gris, que
  sigue invitando a apretarlo— y "Cancelar" pasa a "Cerrar". El resultado
  ("✓ Borrado. N chats desasignados") queda visible hasta que el usuario
  salga: ese número dice si quedaron chats sueltos, y cerrar la ventana sola
  se lo llevaría puesto antes de que alcance a leerlo.

  **Si la acción falla, nada cambia**: los dos botones siguen vivos, porque
  ahí reintentar y cancelar sí significan algo.

  Los otros tres diálogos de confirmación se revisaron y **no comparten el
  defecto** —ya se cierran solos al completarse— así que no se tocaron.

## 0.5.4 — 2026-08-27

### Fixed
- **El botón "Aprueba MSG" ya no se corta en pantalla de teléfono** (T81,
  ct-2026-08-27-2251). La celda metía indicador, selector y botón en una sola
  fila sin permitir que envolviera; a ~390px el botón quedaba partido.
  Preexistente, no introducido por el cambio de anchos de 0.5.1.

### Changed
- **El botón "Aprueba MSG" solo aparece en los niveles "Boss" y "Respuesta
  automática"** (T81, ampliación del dueño: *"ese boton deberia verse solo
  para numeros boss y auto, y nada mas"*). Antes se dibujaba en todas las
  filas sin ninguna condición.

  **Con una excepción deliberada:** un chat que YA está marcado como
  aprobador sigue mostrando el botón sea cual sea su nivel. Esconderlo ahí
  dejaría un permiso activo sin forma de quitarlo desde donde se puso — un
  aprobador invisible es peor que un botón de más.

## 0.5.3 — 2026-08-27

### Fixed
- **Los estados de WhatsApp dejan de entrar a la cola de despacho** (T84,
  ct-2026-08-27-2314). Las historias que publican los contactos llegaban como
  `status@broadcast` y el gateway las trataba como una conversación más: se
  encolaban, reintentaban cada 5 segundos, tocaban el tope de redespacho y
  volvían a intentar. Para siempre.

  Medido sobre los tres logs rotados de una instalación real:

      44.387 líneas con status@broadcast
      44.359 de ellas, el bucle del tope de redespacho

  Era el origen del "gateway dando vueltas 34 horas" que se venía reportando
  sin identificar, y de los ~14 MB de log en dos días que enterraban cualquier
  línea útil. Con un agente conectado, además, esos despachos le llegaban y le
  ocupaban el turno del canal.

  **El defecto era peor de lo medido:** el mismo bucle inflaba
  `CountRecentPendingNonBoss`, el contador de contrapresión — así que podía
  estar frenando conversaciones reales por una lectura de presión que nunca
  fue cierta.

  Un estado no es una conversación: nadie escribió a nadie, es contenido
  publicado, no hay a quién responder.

  **Recibir y archivar no cambia.** El corte está en las cuatro consultas de
  la cola, no en la ingesta — un estado trae contenido real y descartarlo al
  entrar habría roto el archivado.

  **Cortado por el JID exacto, nunca por el sufijo:** una lista de difusión
  propia es `<id>@broadcast` y sigue siendo un destino legítimo. Hay un test
  que lo protege de una futura barrida bien intencionada.

## 0.5.2 — 2026-08-27

### Fixed
- **Asignar el chat del dueño a un agente desde el tablero ahora funciona**
  (T83, ct-2026-08-27-2257). Se podía elegir el agente, la elección se
  guardaba y se veía en la tabla — y el despacho la ignoraba por completo. Los
  mensajes del dueño seguían yendo al principal.

  La rama que 0.2.2 introdujo para el chat del dueño consultaba únicamente el
  "agente por defecto para BOSS" y nunca la asignación puntual. Ahora vale la
  misma regla que en cualquier otro chat: **lo específico pisa a lo general**.
  Primero lo asignado a mano, después el default de tipo, después el principal.

  Las rutas automáticas de `router.json` siguen sin aplicar al chat del dueño,
  y es deliberado: una asignación a mano es una decisión explícita sobre ese
  chat; una ruta es ruteo automático y general.

  El defecto se descubrió porque el dueño lo usó: asignó su chat, no le llegó
  nada, y tuvo que reportarlo. Antes se le había confirmado por escrito que esa
  asignación tenía prioridad, sin verificar que su propio chat era la
  excepción — la confirmación equivocada es parte del defecto, no un detalle
  aparte.

## 0.5.1 — 2026-08-27

### Changed
- **El tablero deja de estar clavado en 900px de ancho** (T80,
  ct-2026-08-27-2205). Reporte del dueño: *"está muy angosto"*, con la tabla
  de Conversaciones cortando el texto de Reglas a media palabra.

  La causa no era la tabla: `--maxw` fijaba 900px sin importar la pantalla,
  así que en un monitor grande se desperdiciaba más de la mitad mientras las
  cuatro columnas se peleaban por lo que sobraba afuera. Ese número alcanzaba
  cuando la tabla tenía tres columnas.

  Sube a 1280. El corte al modo tarjeta pasa de 640px a 900px — el rango
  640-900 era justamente donde peor se veía, tabla apretada sin espacio; ahora
  ahí ya son tarjetas apiladas, que es el modo que ya existía y funcionaba.

  **Sin reintroducir ninguna de las dos cosas que el dueño ya había
  rechazado**: nada de scroll interno con altura fija (*"la lista de chats no
  baja es un iframe pequeñito"*) ni de `min-width` que empuje scroll
  horizontal. El problema se resolvió quitando la necesidad de ancho, no
  agregando una barra para compensarla.

  La pestaña Reglas recibe su propio límite de lectura: ensanchar el tablero
  no debía estirar sus textarea de prosa a la línea completa.

## 0.5.0 — 2026-08-27

Salto de menor: la regla general deja de existir. Quien actualice va a ver
una casilla menos en Reglas y una jerarquía de un escalón más corta.

### Removed
- **La regla general, y todo su cableado** (T79, ct-2026-08-27-2034).
  Decisión del dueño: *"regla general ya no va por que no tiene a donde ir"*.

  El razonamiento, corroborado en el código antes de tocarlo: al resolver las
  reglas de un chat hay tres caminos posibles después de las reglas propias
  —grupo, persona en la agenda, persona desconocida— y **los tres ya tienen
  su propia casilla**. La partición es exhaustiva: un chat es grupo o no lo
  es; si no lo es, está en la agenda o no. No queda un cuarto caso al que la
  general pudiera servir. Solo se alcanzaba vaciando una casilla de tipo, y
  en ese escenario heredar un texto genérico que nadie eligió para ese caso
  es peor que quedar sin reglas, que es un resultado explícito y predecible.

  Se fue de todas partes, no solo de la pantalla: la constante, la lectura en
  `EffectiveRules`, `SetDefaultRules`, los endpoints
  `GET|POST /api/admin/default-rules`, el control del tablero y la tool MCP
  `set_default_rules`. Un valor que sigue vivo en la base pero ya no se
  ofrece es una regla fantasma, y eso es peor que una regla inútil.

- **`rules_type_individual`**, muerta desde M5 (ct-2026-07-22-1903) cuando el
  eje contacto/número-nuevo la reemplazó. Su propio comentario ya admitía que
  no se leía. `SetTypeRules` queda aceptando solo grupos.

### Notas
- **El texto guardado no se borra de la base.** Deja de leerse y de
  ofrecerse; la fila queda huérfana e ignorada. Recuperable a mano si alguien
  se arrepiente — borrarla no aportaba nada y quitaba la marcha atrás.
- Los tests que fijaban el fallback a la general **se dieron vuelta**, no se
  borraron: ahora documentan que la cadena termina en la casilla de tipo.

## 0.4.3 — 2026-08-27

### Security
- **La dirección de metadata de nube deja de ser alcanzable** (T78,
  ct-2026-08-27-1952). `169.254.169.254` es donde AWS, GCP y Azure entregan
  las credenciales de la instancia sin pedir autenticación — el premio
  clásico de un SSRF. Caía dentro del rango link-local, que el validador
  permitía entero.

  Se bloquea **esa dirección sola**, no el rango: el resto de link-local es
  lo que habilita el descubrimiento local del caso Raspberry Pi, y cerrarlo
  entero sí quitaría una capacidad real. En una instalación de escritorio
  esta dirección no existe, así que el cambio no altera nada en uso normal.

  Hay un segundo test, deliberado, que confirma que una link-local corriente
  sigue pasando: está ahí para que un endurecimiento futuro bien intencionado
  no rompa el caso Pi sin darse cuenta.

  Cierra el cabo que el propio fix de 0.4.1 había dejado anotado.

## 0.4.2 — 2026-08-27

### Fixed
- **Las skills que leen los agentes estaban en el mundo anterior a que se
  sacara el candado** (T74, ct-2026-08-27-1725). Cada manual vive dos veces:
  la fuente que se embebe en el binario, y una copia en `.claude/skills/`.
  Se habían separado 23 líneas, y lo que le faltaba a la copia era
  exactamente lo que se había liberado: el flujo entero que explica que un
  agente puede escribir primero sin esperar despacho.

  El dueño pidió sacar ese candado tres veces. Salió del código, y siguió
  vivo en el texto que los agentes leían.

- **La fuente apuntaba a una carpeta que no existe** (`piumy-connect`; la
  real es `piumy`). La nota que era la única defensa contra la deriva
  señalaba a la nada.

### Added
- **Un test compara las 7 fuentes contra sus copias en cada `go test`**
  (T74). El diseño original decidió explícitamente no tener sincronización
  —"la nota alcanza y no se rompe sola"— y esa apuesta se perdió. Ahora la
  deriva falla ruidosamente, con la ruta exacta y cómo arreglarla, en vez de
  depender de que alguien se acuerde. Verificado en los dos sentidos: verde
  sincronizado, rojo al desincronizar a propósito.

- **Dónde recuperar la contraseña, dicho en el manual.** Las skills listaban
  `reset_dashboard_password` como bloqueada y ahí terminaban, sin mencionar
  que existe recuperación por WhatsApp y por email. Un texto incompleto le
  hizo reportar al leader que una contraseña perdida no se podía recuperar
  — era falso.

### Changed
- **El manual del operador tiene menos restricción y más oficio** (T74). Tres
  secciones que repetían casi lo mismo sobre no rodear un rechazo se
  fundieron en una. Y ahora distingue lo que antes mezclaba: un rechazo de
  **permisos** es la respuesta y no se rodea; una falla **técnica** (timeout,
  canal caído, destino sin terminal abierto) es un problema a resolver y sí
  se reintenta por otro camino. 465 → 439 líneas.

## 0.4.1 — 2026-08-27

### Security
- **La antena que un agente adjunta a `send_to_boss` ahora se valida antes
  de pingearla.** En 0.4.0, el `endpoint` lo elegía quien llamaba: un agente
  con la clave MCP podía apuntarlo a cualquier dirección alcanzable desde la
  red del gateway y hacer que Piumy hiciera el pedido HTTP por él — y si esa
  dirección respondía, quedaba registrada como destino de despacho vivo
  durante 24 horas.

  Se reusa el mismo validador que el endpoint del principal ya usaba
  (`IsAllowedPrincipalEndpoint`): un endpoint rechazado no se pinguea, no se
  registra, y el mensaje sale marcado ❌.

  Detectado por un review de seguridad automático el mismo día que se
  publicó 0.4.0. No contradice el modelo permisivo del proyecto: ese modelo
  cubre lo que un agente puede hacer DENTRO de Piumy, no usar a Piumy de
  intermediario hacia el resto de la red.

  **Pendiente, preexistente:** el rango link-local sigue permitido porque
  habilita el descubrimiento local (caso Raspberry Pi), y ahí vive también
  la IP de metadata de las nubes. Afecta igual al endpoint del principal
  desde antes de este cambio. Anotado para decidir aparte.

## 0.4.0 — 2026-08-27

Salto de menor: cualquier agente puede escribirle al dueño sin darse de alta,
y el dueño puede contestarle a ESE agente sin que el principal se entere.

### Added
- **Antena efímera en `send_to_boss`** (T77, ct-2026-08-27-1753). La
  herramienta ya no exige `register_agent` previo — esa exigencia era lo que
  el dueño reportaba como "no funciona": el envío entregaba bien, el rechazo
  era la única barrera.

  Un agente puede adjuntar su antena (endpoint / antenna_terminal_id /
  pinpass, los tres o ninguno) en la misma llamada. Piumy **la pinguea de
  verdad antes de mandar** y marca el mensaje según el resultado:

      [Nombre] 📡 ✅   respondé citándolo y te llega
      [Nombre] 📡 ❌   sin vuelta

  Verbatim del dueño sobre por qué el ping: *"no llega ese ticket, no es de
  papel, tiene que ser validado con un PING antes"*. El ✅ significa "probé
  esta antena recién y contestó", nunca "el agente dijo que tiene una".

  **Adjuntar la antena es opcional y el ping nunca bloquea**: sin antena, o
  con una que no responde, el mensaje sale igual marcado ❌. El dueño sabe de
  entrada si gastar una respuesta o no — antes lo descubría después de
  contestar al vacío.

  Si el ping entra, esa antena queda registrada como destino de respuesta por
  tiempo limitado. **No es un agente**: no aparece en `list_agents`, no toca
  al principal, y expira sola.

### Fixed
- **El enrutado por cita quedó demostrado a un terminal que no es el
  principal** (T77). Existía desde T43/T44 pero nunca se había probado de
  verdad: la única reproducción disponible citaba un mensaje cuyo origen ya
  era el principal, o sea el mismo destino al que habría caído igual. Ahora
  está verificado de punta a punta —antena HTTP real, sweep corriendo, un
  despacho de nivel boss aterrizando en un terminal efímero— y con test.

## 0.3.1 — 2026-08-27

### Changed
- **La zona para pegar las credenciales de una antena ahora es un campo
  propio y visible**, el primero de "+ Nuevo agente" (T75,
  ct-2026-08-27-1751). En 0.2.3 el pegado se detectaba sobre los campos que
  ya existían, sin un lugar que lo anunciara: el dueño lo probó, no supo
  dónde pegar y terminó pegando en Endpoint. Una función invisible es una
  función que no está.

  El pegado sobre los 5 campos de abajo **se queda** — quien ya lo aprendió
  así lo sigue teniendo. Es aditivo. El parseo no cambió: los mismos 8 casos.

  El campo nuevo escucha `input`/`change` además de `paste`, así funciona
  también con un pegado del mouse o texto tipeado — y de paso permitió
  cerrar en vivo la verificación que en 0.2.3 quedó abierta, porque Chrome
  no dispara `paste` ante teclas automatizadas.

## 0.3.0 — 2026-08-27

Salto de menor: el agente principal deja de ser una posición fija. Se puede
cambiar de rol y se puede borrar — dos candados que decidían por el dueño en
su propia cuenta.

### Added
- **Un secundario puede pasar a principal, y el principal a secundario**
  (T76, ct-2026-08-27-1752). Desde el tablero, un botón en la ficha del
  agente; y por MCP con `promote_to_principal()`, que es **self-service**: el
  agente que llama se promueve a sí mismo, nunca a un tercero. Pedido del
  dueño: "yo puedo rápidamente hacer que el principal me hable a mi WhatsApp
  y después venga otro y se cambia principal... se estén intercambiando el
  principal entre ellos". Es un intercambio, no un reemplazo: el que baja
  conserva nombre, endpoint y credenciales.

  `store.PromoteToPrincipal` cruza las dos representaciones que conviven —el
  principal vive en KV, un secundario en la tabla `agents`— sin colisionar:
  `PrincipalTerminalID` queda como slot estable (lo usan ~25 lugares) y lo
  que cambia es el contenido detrás; el degradado ocupa el `agent_id` que el
  promovido acaba de vacar, nunca uno inventado.

### Changed
- **Se puede borrar al agente principal** (T76). `delete_agent` y
  `POST /api/admin/agent-delete` ya no lo rechazan. Como el principal no
  tiene fila propia, borrarlo es volver al estado sin configurar de una
  instalación nueva. **Quedarse sin ningún agente es un resultado aceptado,
  no un estado a impedir**: el gateway sigue recibiendo y simplemente no
  tiene a quién despachar. Sin confirmación extra ni validación de "tiene que
  quedar al menos uno" — decisión explícita del dueño, verbatim: "Me da lo
  mismo que piensen que van a haber problemas. Yo lo quiero, como yo lo digo."

  El test que fijaba el rechazo se dio vuelta, no se borró.

## 0.2.3 — 2026-08-27

### Added
- **Pegar las credenciales de una antena llena el formulario de agente nuevo**
  (T73, ct-2026-08-27-1713). Pedido del dueño: "me gustaria un auto detect
  para pegar ese string completo y que detecte el nombre del agente tambien".
  Se pega el string que produce `capi_credentials` en cualquier campo de
  "+ Nuevo agente" y se completan los cinco: endpoint, terminal id, pin,
  nombre e ID. No dispara Crear — el dueño revisa y edita antes.

  El nombre se deriva del chat_id parseando **desde el final**: el nombre del
  proyecto puede llevar guiones (`piumy-gateway`), así que contar segmentos
  desde el principio rompe. Si el último segmento es numérico es el formato
  sin identidad y no hay nombre que sacar. Un pegado que no sea credencial
  pasa de largo sin tocar nada, y un pegado parcial no borra lo ya escrito.

  Sin build step ni framework, fiel a app.js: la lógica pura vive en
  `agentpaste.js` (dos `<script>` planos) para poder probarla con `node` sin
  simular el navegador. 8 casos cubiertos, incluidos pin terminado en "=" y
  "==", espacios de más, y el formato sin identidad.

## 0.2.2 — 2026-08-27

Sigue el hilo de 0.2.1: repartir chats entre agentes, ahora completo. Se
suman los dos tipos que faltaban (grupos y el chat del dueño) y la vista
inversa — asignar parado en el agente, no solo parado en el tipo.

### Added
- **Tipos de chat asignables desde la ficha del agente, y dos tipos nuevos:
  grupos y boss** (T72, ct-2026-08-27-1625). Pedido del dueño: "quiero que
  sumemos a que en la parte de agentes, se le asignen tambien los mensajes
  nuevos, de contracto, de grupos o al boss". `EffectiveAgentDefault` pasa
  de dos tipos a cuatro (boss -> grupo -> contacto/número nuevo), mismo
  patrón que `EffectiveRules` ya usaba. En la ficha de cada agente, un
  checkbox por tipo; en Reglas, el selector que faltaba en Grupos y una fila
  nueva Boss. Las dos vistas escriben la MISMA clave — una sola fuente de
  verdad, sin tabla ni clave paralela.

### Changed
- **El chat del dueño entra a la cadena de ruteo, y SOLO por su tipo** (T72).
  Hasta acá `dispatch` lo saltaba entero (`level != LevelBoss`) y lo mandaba
  siempre a `PortFallback`. Ahora consulta el default de tipo "boss" — y nada
  más: `agent_exclusive` y `router.json` siguen sin aplicarle nunca, la
  invariante "is_boss => principal" (ct-2026-07-13-0302) sigue siendo la
  regla, con un escape explícito puesto desde el tablero en vez de cero.
  Sin configurar nada sigue cayendo al principal, tal como el dueño lo pidió:
  "el boss se asigna automaticamente al principal". Fijado por
  `TestBossUnconfiguredDefaultStaysOnPortFallback`,
  `TestBossIgnoresRouterJSONEvenWithTypeDefault` y
  `TestBossIgnoresAgentExclusive`.

### Fixed
- **Una viñeta del MANUAL ("Reglas invisibles") había perdido su encabezado**
  en el merge de T71 y quedó colgando como si fuera parte de otro bloque
  (T72). La auditoría de ese merge no lo detectó por revisar código y no
  prosa — queda anotado como límite conocido de esa revisión.

## 0.2.1 — 2026-08-27

Parche de un solo tema: el dueño no podía asignar números a un agente, y
sus propios mensajes le llegaban al agente con reglas encima. Las dos
cosas eran candados viejos que sobrevivieron a la decisión que los
derogó.

### Fixed
- **El desplegable de asignación escondía al principal, y con un solo
  agente registrado dejaba al dueño sin poder asignar nada** (T70,
  ct-2026-08-27-1404). Reporte del dueño: "la asignacion de agentes a los
  numeros está roto, no se puede ver la lista". No fallaba: se filtraba
  hasta quedar vacío. El gate estaba en TRES capas, no en una — el filtro
  del `<select>` en `app.js`, un rechazo 400 en `handleAssignChatToAgent`,
  y un `GetAgent` que rechazaba al principal por no tener fila en `agents`
  (se sintetiza desde KV). Asignar en EXCLUSIVA no es lo mismo que caer
  ahí por defecto: un chat asignado no lo toma otro agente. Sale de las
  tres. El test que fijaba el rechazo se dio vuelta, no se borró.

- **Los mensajes del dueño llegaban al agente con el bloque `rules.md`
  encima** (T71, ct-2026-08-27-1410). No era un pedido nuevo: el dueño
  lo había pedido el 2026-08-06 —"si soy boss tiene que decir is boss, y
  si no, el preámbulo son las reglas"— y esa frase es una ALTERNATIVA que
  se implementó como una SUMA. Ahora un despacho boss-level lleva el texto,
  la línea `is_boss: true` y nada más; `EffectiveRules` ni se calcula para
  descartarlo. Un chat no-boss queda exactamente igual que antes.

### Added
- **Agente por defecto por origen del chat** (T71, ct-2026-08-27-1410).
  Selector nuevo junto al de modo en "Mensajes nuevos" y "Contactos del
  celular": un chat sin asignación propia se despacha al agente elegido
  ahí. Cuelga de la misma jerarquía KV que ya usa `EffectiveRules`
  (`store.EffectiveAgentDefault`, contacto pisa a número nuevo), y entra
  como precedencia 2.5 en `capipush.dispatch` — debajo de `agent_exclusive`
  y de `router.json`, encima de `PortFallback`. Ruteo, no permiso: sin
  gates nuevos.

## 0.2.0 — 2026-08-11

Salto de menor, no de parche: la whitelist del router dejó de existir en
los tres lugares donde frenaba — quien actualice va a ver su gateway
comportarse distinto. En una línea: los desconocidos que te escriben
ahora llegan y se ven; un agente puede escribir primero; apagar un chat
lo apaga de verdad; el tablero abre sin ventana negra.

### Fixed
- **El dato de T66b viajaba en `/api/chats` pero la pantalla seguía sin
  pintarlo — el defecto real, para el dueño, seguía igual de vivo** (T66c,
  ct-2026-08-11-1843, autocrítica de Citrino sobre su propio contrato de
  T66b: pidió "que el `Status` viaje en la API" cuando el problema real
  era "asigno un número y no veo nada"). El popup de miembros de un grupo
  (y la lista inline de la pestaña Grupos) seguían mostrando nombre +
  número liso, sin importar qué decisión hubiera tomado el dueño. Ahora
  un miembro con una decisión encima lo dice, con el MISMO lenguaje visual
  que ya usa la tabla de Conversaciones (T56/Aprobador P1): ★ ámbar si es
  el dueño, el ícono de aprobador si aprueba mensajes, "→ nombre del
  agente" si está asignado — pero de solo lectura, nada de esto abre un
  control nuevo; asignar/aprobar sigue viviendo únicamente en esa tabla.
  "Fundir, no agregar": ninguna columna, panel o segunda tabla nueva — se
  reusó `contactRow`, el mismo componente que ya pintaba esa fila.
  Verificado a ojo, con navegador (el único contrato del día donde eso, y
  no un test, era el entregable real).
- **Asignarle un agente a alguien conocido solo por un grupo (sin agenda)
  no se veía en ningún lado del tablero** (T66b, ct-2026-08-11-1827,
  hallazgo de Peridot R14/T66 + un pliegue verificado por Citrino). El
  filtro que saca a esos contactos de la lista principal es un pedido
  explícito del dueño y sigue intacto — pero la fila que le quedaba a esa
  persona (bajo su grupo) no llevaba estado, nivel, ni ningún dato de la
  asignación: no había forma de verificarla ni revertirla desde el
  tablero, y sus 1:1 reales son ~4 contra cientos de conocidos-por-grupo,
  así que este "caso borde" era en realidad el caso común. Ahora, una vez
  que el dueño decidió algo a mano sobre ese contacto — lo asignó a un
  agente, lo marcó boss/aprobador — esa fila viaja completa. Verificado
  contra `/api/chats` directo, sin navegador.

### Removed
- **Cualquier agente registrado puede iniciar una conversación, sin
  despacho previo** (T64, ct-2026-08-11-1627). Tercer pedido del dueño en
  el mismo sentido — verbatim: *"pero que el agente escriba primero es un
  peduido que llevo mas de 5 dias pidiendolo... y ustedes lo returcen y lo
  vuelven a quitar"*. ct-2026-07-13-0538 lo abrió, ct-2026-07-18-1438's
  "candado versión segura" lo volvió a acotar a solo el agente principal, y
  solo a un chat marcado `is_boss`/`active` — esta vez se sacó entero, no
  se entregó una versión más ancha del mismo candado. `isPrincipal`/
  `initiateAuthorized` ya no existen en `send.go`. Lo único que se
  mantiene: un dispatch YA vinculado sigue exigiendo `Ready` (ST-A) y no
  puede redirigirse a otro chat (anti-leakage caution/danger) — eso es un
  gate distinto (de nivel, no de iniciación) que el dueño pidió no tocar.
  Rules/claim/`ignorado`/`blacklist` se aplican igual, con o sin dispatch.
- **`router.Decision.Allowed` y `router.Config.AllowAll` — sin
  consumidores, se van** (T64, ct-2026-08-11-1627, punto 3 revisado sobre
  la marcha). El plan original de este punto era exponer un control
  `allow_all` en el tablero; antes de construirlo se encontró que el campo
  ya no gatea nada desde T65 — la whitelist dejó de ser un gate y con eso
  el `.Allowed` que alimentaba también quedó muerto. Construir una perilla
  para un ajuste que no cambia ningún comportamiento habría sido justo la
  abstracción especulativa que el proyecto prohíbe, y encima mentirosa —
  se optó por borrar en vez de exponer. Se va también `router_allow_all`
  de `get_status` (informaba una perilla inexistente — un agente podía
  tomar decisiones con ese dato). `Config.Whitelist` **no se borra** — es
  el único consumidor vivo de `IsVIP` — pero ya no habilita nada, solo
  marca VIPs para el ánimo del ícono de bandeja; la clave JSON no se
  renombró para no perderle al dueño los VIPs ya guardados.
- **La whitelist del router ya no filtra nada — todo chat está permitido
  por defecto** (T65, ct-2026-08-11-1642, pedido explícito del dueño, la
  tercera vez que lo pidió: *"yo quierp todo en witelist... para algo esta
  ignorar, eso ya apaga el chat"*). Antes había tres frenos separados
  usando la whitelist: uno en la ENTRADA (un mensaje de un número no
  habilitado ni siquiera se guardaba — nunca aparecía en el tablero, esa
  era la causa de que el dueño no viera a esos números), uno en la SALIDA
  (`send_message` rechazaba "not in the whitelist"), y uno en la descarga
  de fotos/audios. Los tres se sacaron enteros, no se suavizaron.

### Added
- **Escribirle a un número que nunca tuvo ficha ya no rechaza — la crea**
  (T64, ct-2026-08-11-1627, boss verbatim: *"hazte cargo de estos 3
  numertos, la ia se registra por mcp y atiende a esos numeros"*).
  `send_message`/`draft` usan `store.TouchChat` (el mismo upsert que
  `whitelist-add` ya usaba) cuando el chat no existe, en vez de rendirse
  con "no rules on this chat" antes de darle una oportunidad al agente. La
  fila recién creada sigue las mismas leyes que cualquier otra — esto solo
  saca el "no existe fila = me rindo", no la ley de rules. Verificado en
  vivo contra un binario real (transporte MCP HTTP real, sin conexión de
  WhatsApp real): un agente secundario sin despacho le escribió a un
  número 555 nunca tocado, la fila se creó (visible en `GET /api/chats`) y
  el envío avanzó hasta el único freno que le quedaba — el gateway
  desconectado (H6, sin sesión de WhatsApp en la instancia de prueba).

### Changed
- **`ignorado`/`blacklist` frenan el envío en CUALQUIER chat, no solo
  grupos** (T65, mismo pedido de arriba). Antes la condición era
  "grupo Y marcado ignorado" — un chat 1 a 1 que el dueño ignoraba o
  ponía en la lista negra igual recibía mensajes, exactamente el bug que
  el dueño reportó ("para algo esta ignorar, eso ya apaga el chat" — no
  apagaba). Es el único freno de envío que queda, ahora parejo para
  cualquier chat.
- **El riesgo de baneo por mensajería masiva pasa a ser una advertencia
  del manual, no un bloqueo de código** — decisión del dueño, verbatim:
  *"si te doy una lista de numeros es mi responsabilidad"*.

### Fixed
- **Un chat en la lista negra igual se despachaba a un agente** (T67,
  ct-2026-08-11-172135, hallazgo propio de T65, reportado ese mismo día y
  cerrado hoy). La cola de despacho excluía chats "ignorados" pero nunca
  los "en blacklist" — como marcar un chat como blacklist no lo desactiva,
  seguía entrando: el agente lo recibía, gastaba razonamiento en
  procesarlo, y recién al querer contestar se topaba con el rechazo. Ahora
  hay una sola definición de "chat apagado" (`store.ChatIsOff`), consultada
  por igual desde el despacho, el envío, y lo que informa `resolve_chat`
  (que también decía un permiso viejo de la whitelist ya removida — un
  agente podía leer "no permitido" para un chat al que sí podía escribirle).
  Verificado matando el proceso de verdad: un chat blacklisteado con
  mensaje sin atender nunca apareció en el despacho real, tras varios
  ciclos de sweep.
- **Abrir el tablero desde la bandeja mostraba una ventana negra en vez de la
  web** (T62, ct-2026-08-11-1527, reporte del dueño: "es como que en vez de
  abrir la web, usan un terminal para hacerlo"). Causa raíz: los dos intentos
  de abrir Edge/Chrome buscaban el ejecutable por el PATH del proceso, y
  Windows nunca pone los navegadores ahí — fallaban SIEMPRE, en silencio, y
  todo caía al último recurso, que era literalmente una consola (`cmd /c
  start`). La "ventana de aplicación" que el código decía abrir nunca se
  había abierto ni una sola vez desde que existe. Ahora la ruta real de
  Edge/Chrome se resuelve por el registro de Windows (`App Paths`, la misma
  fuente que usa el propio Explorador), y el último recurso —cuando no hay
  ninguno de los dos— pasó de `cmd` a `rundll32 url.dll,FileProtocolHandler`,
  que abre el navegador por defecto sin crear ninguna ventana de consola.
- **El log se llenaba de una sola línea repetida** (T61, hallazgo de Peridot en
  R11). La verificación de la contraseña del tablero anunciaba "usando el hash
  ya existente" en **cada request autenticado**, y el tablero consulta cada
  pocos segundos: en el log real de la instalación eran ~200 de 522 líneas —
  el 40% del archivo diciendo que todo está normal. Enterraba exactamente lo
  que el log vino a mostrar. Ahora solo se anuncia el caso que pasa una vez:
  cuando se siembra una contraseña nueva.

---

## 0.1.21 — 2026-08-10

### Fixed
- **Ahora solo puede correr una instancia de Piumy a la vez** (T59,
  ct-2026-08-10-2116). Nada impedía lanzarlo dos veces sobre la misma sesión
  de WhatsApp — pasó de verdad, dos veces el mismo día, y una terminó con
  WhatsApp desconectado. Ahora la segunda instancia sale sola apenas arranca
  (sin llegar a tocar la sesión) y deja constancia en el log de por qué. Sin
  diálogos — esto se lanza desde la bandeja y desde el instalador, no
  siempre hay alguien mirando. Sobrevive a un cierre sucio (proceso matado,
  corte de luz): verificado matando el proceso de verdad, no solo leyendo el
  código — el mecanismo es un mutex del sistema operativo, que el propio
  Windows libera apenas el proceso muere, sea como sea que haya muerto, así
  que nunca queda una instancia "fantasma" bloqueando el próximo arranque.
  Separado del mutex que ya existía para el instalador (no se tocó — sigue
  best-effort, sigue funcionando igual).

---

## 0.1.20 — 2026-08-10

### Fixed
- **El manual del operador no avisaba lo que más confunde al empezar** (T58,
  hallazgo de Bernstein ejecutando los flujos). Casi ninguna herramienta
  funciona sin un despacho activo — ni siquiera las de solo lectura, como
  consultar un chat o sus mensajes. Ese aviso existía, pero en el manual de
  conexión, que no es el que lee un operador. Un agente recién conectado veía
  todo rechazado y buscaba qué había configurado mal, cuando lo esperable era
  esperar trabajo.
- **Dos descripciones del propio código se contradecían sobre los medios.** Una
  presentaba `get_media` como un listado; la otra decía que sirve una copia de
  baja calidad. Ninguna de las dos lo contaba bien: devuelve los medios
  recientes **en versión liviana**, y `get_media_full` trae el original de uno
  solo, que se cobra aparte. La diferencia no es listar contra descargar, es
  calidad y costo. Corregidas la descripción de la herramienta y el manual.
### Added
- **El tablero ahora puede asignar un chat a un agente** (T56,
  ct-2026-08-10-201641). El endpoint `/api/admin/agent-assign` ya existía y
  funcionaba, pero la pantalla nunca lo llamaba — el dueño no tenía forma de
  repartir qué números atiende cada agente desde el tablero, solo por MCP.
  Ahora la tabla de chats tiene una columna "Agente" con un selector: elegir
  un agente asigna el chat en exclusiva; "Sin asignar" limpia la asignación
  y el chat vuelve al principal. Sin recargar la página — mismo patrón que
  ya usa el selector de Nivel. La lista de agentes sale de la misma fuente
  que ya alimenta el resto del tablero.

---

## 0.1.19 — 2026-08-10

### Fixed
- **El instalador ahora verifica que el programa se haya actualizado de
  verdad** (T54, ct-2026-08-10-1934). Instalando con Piumy corriendo, el
  instalador podía cerrar el proceso, completar la configuración, y
  reportarse como exitoso sin haber reemplazado el programa — la versión
  anterior seguía corriendo, sin ningún aviso. Ahora, después de instalar,
  se compara el programa que quedó contra el que el instalador traía; si
  no coinciden, avisa claramente que probablemente algo lo tenía abierto
  o un antivirus lo bloqueó, y pide cerrar Piumy y reintentar — en vez de
  quedarse callado.

---

## 0.1.18 — 2026-08-10

### Added
- **El gateway ahora deja rastro de lo que hace** (T53, ct-2026-08-10-1849).
  El binario se compila sin consola y el instalador lo lanza directo, así que
  todo lo que registraba —"sin antena", "canal caído", "reintento diferido",
  "despacho OK"— se evaporaba: esas líneas existían en el código y no había
  forma de leerlas nunca. Ahora se escriben en `logs/piumy.log`, junto a la
  instalación, con rotación por tamaño (5 MB, 3 archivos viejos) para que no
  crezca sin techo. Sin interruptor: si hubiera que encenderlo, nadie lo
  tendría encendido justo cuando pasa el problema.

  Va en `logs/` y **no** junto a la base de datos y las claves, a propósito:
  este archivo existe para pedírselo a alguien cuando algo no le llega, y la
  forma natural de mandarlo es comprimir la carpeta que lo contiene. No
  contiene texto de conversaciones — solo identificadores y estados.

---

## 0.1.17 — 2026-08-10

### Fixed
- **El chat fantasma con sufijo de dispositivo ya no genera filas muertas en
  el outbox** (T52, ct-2026-08-10-1837). `BossJIDs()` normalizaba y
  deduplicaba antes de retornar — con el fantasma (`numero:15@s.whatsapp.net`,
  `is_boss=1`) y la fila real del mismo número, cada `send_to_boss` y cada
  recovery encolaban una fila inentregable. Dos piezas: `BossJIDs()` ahora
  quita el sufijo y colapsa destinos idénticos (el duplicado visible es peor
  que la fila muerta silenciosa); `Enqueue`/`EnqueueFromAgent` normalizan el
  JID en el INSERT — defensa simétrica a T45 en la entrada.
- **Actualizar Piumy ya no borra las variables de `piumy-config.json` que
  el instalador no conoce** (T51, ct-2026-08-10-1826). Cada actualización
  reescribía ese archivo entero desde su plantilla fija — cualquier
  variable agregada a mano (`PIUMY_DEFAULT_TERMINAL_ID` es el caso real
  que lo hizo visible) desaparecía sin aviso, y el gateway seguía
  arrancando igual, solo que sin esa configuración. Ahora, si el archivo
  ya existe, el instalador lo completa en vez de reemplazarlo: agrega
  solo lo que le falte y conserva todo lo demás tal cual estaba.

---

## 0.1.16 — 2026-08-10

### Fixed
- **El sufijo de dispositivo de WhatsApp ya no crea chats duplicados** (T45,
  ct-2026-08-10-1424). Medido contra la instalación real: de 200 chats,
  exactamente uno tenía el formato `numero:NN@s.whatsapp.net` — la propia
  cuenta del dueño, duplicada junto a la fila legítima sin sufijo, las dos
  marcadas como dueño, y la del sufijo sin poder recibir nunca nada. Ahora
  ese sufijo se quita antes de que cualquier chat o mensaje se guarde —
  `@lid` y los grupos (`@g.us`) no se tocan, nunca lo llevan.

---

## 0.1.15 — 2026-08-08

### Fixed
- **Los flujos que 0.1.14 agregó al manual del operador tenían las llamadas
  mal escritas** (T50, hallazgos de la revisión independiente de Peridot sobre
  el trabajo de Citrino). El manual pasó a documentar las llamadas concretas,
  pero varias no coincidían con el código: `send_message` y `draft` se
  mostraban con `chat_id`/`text` cuando en realidad son `to`/`message` y
  además exigen `model` y `policy_version`; `claim_chat`/`release_chat`
  omitían `model`; `mark_handled` usaba un nombre de parámetro inexistente;
  `get_media` se presentaba como si bajara un medio cuando lista los
  recientes; y `escalate` aparecía recibiendo un motivo que la herramienta no
  acepta ni registra. Un agente que siguiera esos flujos al pie de la letra
  fallaba. Todas las llamadas del manual se verificaron una por una contra las
  firmas reales del código.
- **El manual decía que aprobar un borrador exige ser el dueño.** Exige un
  despacho de nivel dueño **o aprobador** — y aprobar es justamente lo único
  que concede el pin de aprobador. Un aprobador que leyera el manual creía que
  no podía hacer lo único para lo que existe. Queda documentado también que
  `approve_draft` acepta un texto de reemplazo: corregir y aprobar en un paso.
- **El manual prometía que una respuesta citada no se pierde si el agente está
  desconectado.** No es así cuando el agente no tiene antena: el aviso al dueño
  cierra esa respuesta y el agente no la ve al volver. Solo sobrevive el caso
  del corte breve con la antena configurada. El manual ahora dice cuál es cuál,
  y que no conviene dejar un hilo abierto antes de desconectarse.
- El ritual no mencionaba `get_decision_policy`, que es obligatorio antes de
  enviar para todo agente que no sea el principal. Ahora es el paso 4.
- Las herramientas de gestión del plantel de agentes (`list_agents`,
  `set_agent_capi`, `assign_chat_to_agent`, `delete_agent`) no figuraban en
  ninguna de las dos listas; ahora están donde corresponde, del lado de lo que
  un operador no toca.

---

## 0.1.14 — 2026-08-08

### Changed
- **El manual del operador ahora enseña los flujos, no solo las herramientas**
  (T48, ct-2026-08-08-2338, pedido del dueño: "la skill debe especificar flujos
  de ejemplo, todos los flujos"). Se agregó un catálogo de los 16 flujos que un
  agente operador puede vivir, cada uno con las llamadas concretas en orden:
  desde el despacho normal hasta el borrador rechazado que vuelve con el motivo,
  el aviso al dueño sin despacho, y la respuesta citada que regresa al agente que
  la escribió.

### Fixed
- **Tres cosas que el manual del operador decía mal.** (1) El primer paso del
  ritual estaba escrito como `get_instructions(chat_id)`, pero el parámetro real
  es el `nonce` del despacho — un agente que seguía el manual al pie de la letra
  erraba la primera llamada. (2) El manual declaraba que rechazar un borrador con
  motivo y corregirlo "todavía no existe", cuando `reject_draft` y `edit_draft`
  están construidas desde T15: un texto viejo mantenía muerta una función viva.
  (3) El ritual se presentaba sin excepciones, pero un despacho del dueño está
  exento del gate; ahora está dicho, para que ese comportamiento no se lea como
  una falla.
- **Los dos huecos que quedaban en el aviso "agente sin conexión"** (T47,
  ct-2026-08-08-233459, encontrados por Citrino leyendo el código, uno de
  ellos reproducido con un test antes de escribir el contrato).
  - Un agente con antena configurada pero la máquina apagada o en otra red
    reintentaba para siempre, en silencio — el aviso solo salía cuando no
    había antena en absoluto. Ahora, pasados 60 segundos de canal caído,
    el dueño recibe el aviso igual, y el mensaje sigue esperando al
    agente (no se cierra) — un corte corto sigue sin generar ruido.
  - Un burst con un mensaje normal seguido de un reply a un agente sin
    conexión perdía el mensaje normal: el aviso cerraba el burst entero.
    Ahora solo se cierra lo que efectivamente era la respuesta a ese
    agente; el resto sigue pendiente y llega a destino en el siguiente
    ciclo, como corresponde.

---

## 0.1.13 — 2026-08-08

### Fixed
- **Un reply ya no cae al principal en silencio cuando el agente citado no
  tiene antena viva** (T44, ct-2026-08-08-2251, corrección de una decisión
  de T43). Pedido del dueño, verbatim: "siempre que el boss responda a un
  mensaje de agente is boss le llega a ese terminal, y si el mensaje no
  llega, entonces que diga 'agente sin conexion'". Antes, si el agente
  citado no tenía conexión en ese momento, la respuesta se la llevaba el
  agente principal sin ningún aviso — parecía que había funcionado, y no.
  Ahora el destino de un reply es siempre el agente citado; si no se le
  puede entregar, el dueño recibe el mensaje automático `agente sin
  conexión` en ese mismo chat, y no queda nada reintentando en círculo.

---

## 0.1.12 — 2026-08-08

### Added
- **Responder citando un mensaje ahora enruta al agente que lo escribió**
  (T43, ct-2026-08-08-2043, pedido del dueño: "si yo le respondo a un
  mensaje de un agente, se le responda a ese terminal... en un chat puedo
  tener diferentes destinos, dependiendo a quién le respondo"). Antes, un
  mensaje del dueño siempre iba al agente principal, sin excepción — ni
  siquiera citando algo que había escrito otro agente por `send_to_boss`.
  Ahora, si el mensaje citado lo mandó un agente registrado y ese agente
  sigue con antena viva, la respuesta llega a ESE terminal — si no, cae
  al comportamiento de siempre (al principal), sin quedar nunca trabada.

---

## 0.1.11 — 2026-08-08

### Added
- **`send_to_boss`: un agente le puede escribir al dueño por WhatsApp sin
  tener un despacho activo** (T39, ct-2026-08-08-1619, pedido del dueño:
  "que tal una herramienta: send to boss en el mcp, que pueda usarlo
  cualqueira que tenga el mcp?" — con la enmienda que define el diseño:
  "pero que el agente se identifique"). Antes, un terminal sin despacho no
  podía hacer nada — un agente en medio de una tarea ("avisame cuando
  termines") no tenía forma de llegar al dueño. La herramienta no acepta
  destino (siempre va a los chats marcados dueño) ni identidad declarada
  (se resuelve del registro, cruzando la conexión — un terminal_id no
  registrado recibe error explícito y no envía nada); el mensaje sale
  firmado `[nombre] texto` y pasa por la misma cola con freno anti-ban de
  siempre, nunca directo.

---

## 0.1.10 — 2026-08-08

### Fixed
- **La señal de "no se pudo descifrar" quedaba solo en un log que nadie ve
  en producción** (T35, ct-2026-08-08-1258). `handleRetryReceipt` detecta
  correctamente el retry receipt de WhatsApp (la única señal observable de
  que un mensaje nuestro llegó ilegible al destinatario, ver 0.1.9 más
  abajo), pero antes solo hacía `log.Printf` — sin `log.SetOutput` a
  archivo y compilado `-H windowsgui` (sin consola), esa línea se tiraba.
  Ahora la señal se persiste (`messages.decrypt_retry_at`, columna nueva)
  y se puede consultar desde `GET /api/messages`, no solo verse por
  casualidad en una captura de pantalla del contacto. **(T36,
  ct-2026-08-08-1312)** Ese primer arreglo tenía un hueco que le pegaba
  justo al caso real que originó todo: para un chat bajo LID, el mensaje se
  guarda con el chat_jid ya resuelto a número (`resolveChatJID`), pero la
  marca buscaba por el `@lid` crudo del receipt — no encontraba la fila y,
  como cero filas no es error, fallaba en silencio, otra vez. `handleMessage`
  y `handleRetryReceipt` ahora resuelven el chat_jid con la misma función
  (`resolveChatJID`, bajada a `types.MessageSource` para servir a los dos),
  así el guardado y la marca no pueden volver a divergir.

### Added
- **La versión de piumy-gateway ahora se ve desde el tray** (T37,
  ct-2026-08-08-1433, pedido del dueño: "quiero que el tray diga la version
  de piumy"). Piumy corre sin ventana (`-H windowsgui`) — el ícono del tray
  es lo único visible del proceso, y hasta ahora no había forma de saber qué
  versión estaba corriendo sin abrir el dashboard. El menú del ícono ahora
  trae, como primer ítem y deshabilitado, `Piumy Gateway <versión>` —
  leído de `internal/version.Version`, la fuente única (nunca escrito a
  mano). El tooltip/título del ícono quedan sin tocar, a pedido explícito
  del dueño ("en el tray en el menú, no al pasar el mouse").

---

## 0.1.9 — 2026-08-08

### Fixed
- **Los mensajes salientes podían llegar imposibles de descifrar**
  (ct-2026-08-07/08, caso real: un contacto real recibió un mensaje del
  gateway y nunca le llegó el texto — WhatsApp le mostró "Esperando
  mensaje" en su lugar, y nadie del lado del gateway se enteró hasta que
  ella mandó una captura de pantalla un día después). Causa confirmada
  leyendo la sesión real en modo solo-lectura: la cuenta **tiene** un LID
  asignado pero `lid_migration_ts` está en **0** — con la versión de
  whatsmeow que corría hasta ahora (`v0.0.0-20260709092057`), ese flag en
  0 hacía que los mensajes directos se siguieran direccionando por
  **número de teléfono** en vez del LID, aunque el destinatario ya
  operara bajo esa identidad. El dispositivo del otro lado recibe el
  sobre cifrado bajo una identidad para la que no tiene la sesión Signal
  correcta y no puede abrirlo — el gateway lo registra como enviado
  igual, sin ningún error visible. Arreglado actualizando whatsmeow a
  `v0.0.0-20260806224404` — el commit `4f8f64e0dd21` ("send: always use
  LID for DMs") saca esa condición y resuelve el LID del destinatario
  siempre, sin depender de `lid_migration_ts`. Sin este flag corregido,
  **cualquier mensaje saliente puede estar llegando ilegible**, no solo
  el caso puntual detectado.
- **Voseo en los manuales de `internal/mcpserver/manuals/`** (ct-2026-08-07).
  Los sirve `get_manual` a cualquier agente de terceros que se conecte —
  publicados en GitHub, es producto. A diferencia del instalador (que le
  habla a una persona y pasó a usted), estos textos le hablan a un agente
  IA: se mantuvo el **tuteo** que ya predominaba, solo se sacaron las
  formas rioplatenses (`tenés`→`tienes`, `podés`→`puedes`,
  `querés`→`quieres`, `fijate`→`fíjate`, `mirá`→`mira`, `acá`→`aquí`,
  `vos`→`tú`, y variantes: `sos`→`eres`, `armás`→`armas`,
  `seguís`→`sigues`, imperativos como `Encendé`/`Leé`/`Verificá` a su
  forma tuteante). Tocados los 6 archivos con voseo real: `connect/
  SKILL.md`, `operator/SKILL.md` y 4 de los 5 de `orchestrator/`
  (`orchestrator/perillas.md` ya estaba limpio). Contenido intacto —
  solo registro; el `dale`/`is_boss` del ejemplo de ataque inyectado
  (bloque de código, `operator/SKILL.md`) no se tocó, es texto ilustrando
  qué escribiría un atacante, no el manual hablándole al agente.
  `internal/dashboard/web/` relevado aparte: sin problema real, lo que
  parecía voseo eran comentarios de código (`acá`) y el texto de usuario
  ya está en tuteo neutro, no rioplatense.
- **`newTestWmeowClient` sin `SetMaxOpenConns(1)` en su base `:memory:`**
  (`internal/whatsmeow/media_test.go`, hallado investigando un test
  intermitente que resultó no venir de acá). Con el pool default
  (ilimitado), cada conexión nueva a una SQLite `:memory:` es una base
  vacía distinta — no explicaba el síntoma que se investigaba (ese
  camino nunca toca `cli.Store`), pero era una trampa latente para el
  próximo test que sí lo tocara concurrentemente. Alineado con
  `store.Open` (`schema.go`), que ya usa el mismo `SetMaxOpenConns(1)`
  por el mismo motivo.
- **`TestAvatarWorkerLoopPacesRequestsWithVariableGaps` intermitente bajo
  carga** (`internal/whatsmeow/avatar_test.go`, ct-2026-08-07 — este era
  el test real detrás de la investigación anterior; el de media nunca
  falló). Causa raíz confirmada por Citrino con el `--- FAIL` real bajo
  40 corridas de la suite completa en paralelo: una separación medida en
  29.4966ms contra un piso de 30ms — 0.5ms/1.7% corto. El código de
  producción está sano (el worker durmió lo que tenía que dormir); lo
  que erraba era la MEDICIÓN: el test observa cada dequeue por *polling*
  de `len(a.avatarQueue)` cada 1ms, no por señal síncrona, y ese lag no
  cancela simétricamente entre dos lecturas consecutivas — bajo carga,
  sumado al scheduling de goroutines y la resolución del reloj de
  Windows, el margen crece. Agregado `pacingMeasurementSlop` (2ms) solo
  contra el piso `actionDelayMin`, documentado en el propio test como
  error de instrumento y no permiso para que el pacing afloje. **Sin
  tocar** la validación de que las separaciones son variables (nunca la
  misma dos veces) — esa sigue exactamente igual de estricta. Verificado
  con los números reales de Citrino (29.4966ms pasa con el margen nuevo;
  un pacing genuinamente roto, 5ms o menos, sigue fallando) y con el
  mismo método de reproducción bajo carga: 10 corridas completas de la
  suite en paralelo (57-149s cada una en vez de los ~7s normales) más
  intentos dedicados del test — sin una sola falla del test corregido.
- **`TestControllerStopWaitsForInFlightOutboxSend` intermitente bajo carga**
  (`internal/corepipeline/controller_test.go`, ct-2026-08-07 — reproducido
  por Citrino con dos síntomas distintos: `outbox Send was never entered`
  y `sql: database is closed`). **No es una carrera de producción** —
  `Controller.Stop()`/`Pipeline.Run()` siguen uniendo `outboxLoop`
  correctamente, reverificado bajo la misma carga. El bug estaba en el
  propio test: a diferencia de los demás tests de este archivo, no tenía
  un `defer c.Stop()` incondicional — si el primer `t.Fatal` (el timeout
  de `sendStarted`, que bajo carga extrema puede tardar minutos en
  cumplirse) disparaba, el `Goexit` saltaba directo por encima del
  `c.Stop()` explícito de más abajo, dejando el Controller corriendo sin
  parar mientras el `defer st.Close()` ya registrado cerraba la base — de
  ahí el `database is closed` repetido, un `processOutbox` huérfano
  reintentando cada 5ms contra una base cerrada. Arreglado con
  `sync.OnceFunc`: el `defer` de limpieza y el `Stop()` explícito del
  medio del test son ahora la MISMA llamada — cualquiera de los dos
  caminos que dispare primero, el otro espera a que termine de verdad
  (contrato propio de `sync.Once`), en vez de pisarlo con un no-op
  idempotente. Reproducido de forma confiable con 15 corridas paralelas de
  `go test ./...` (15/15 fallaban antes del fix) y verificado sin fallas
  con el mismo método: 109 corridas dedicadas + las 15 corridas completas
  de la suite, todas en verde, bajo la carga que antes lo hacía fallar
  siempre.
- **`TestFloodGuardThrottlesGeneralCalls` intermitente bajo carga**
  (`internal/mcpserver/floodguard_test.go`, ct-2026-08-07 — visto fallar
  por timeout, 30.8s, bajo carga pesada). **No es un bug del flood guard**
  — su token bucket (`internal/mcpguard`) hace exactamente lo que tiene
  que hacer: si pasan 30 segundos reales entre dos llamadas con
  `RatePerMin: 2`, rellena un token, permite la siguiente y NO la
  bloquea — es la defensa real funcionando, no rota. El test asumía que
  sus 3 llamadas quedarían pegadas en el tiempo real, algo que ninguna
  cantidad de reintentos locales puede garantizar bajo contención extrema
  del scheduler del SO (a diferencia del de avatares, acá el retraso es
  externo al test, no una imprecisión de su propia medición). Agregado
  `Guard.SetClock` (`internal/mcpguard/mcpguard.go`) — inyecta la fuente
  de tiempo de `Check`, test-only, producción sigue con `time.Now` por
  default. El test ahora fija un reloj congelado: las 3 llamadas quedan
  en el MISMO instante desde el punto de vista del bucket, sin depender
  en absoluto del tiempo real transcurrido — a diferencia de los dos
  flakies anteriores, esto no reduce la probabilidad de fallo bajo carga,
  la elimina por completo (verificado con una prueba sintética: el mismo
  bucket con reloj congelado ignora un `time.Sleep` real de 200ms entre
  llamadas, y sigue rellenando correctamente si el reloj inyectado se
  adelanta de verdad). **No logré reproducir esta falla específica hoy**
  pese a ~100 corridas bajo dos tandas de carga creciente (hasta 25
  suites completas en paralelo) — a diferencia de los casos anteriores,
  esto no fue necesario para confiar en el arreglo: la aritmética del
  token bucket es exacta, no probabilística, y confirmé el mismo
  mecanismo de raíz en un test hermano (`TestCircuitBreakerBlocksAfterThreshold`,
  `internal/mcpguard`) que sí falló bajo carga pesada en esta sesión.
- **`TestCircuitBreakerBlocksAfterThreshold` — mismo arreglo** (`internal/
  mcpguard/mcpguard_test.go`, ct-2026-08-07). Gemelo exacto del flaky de
  `TestFloodGuardThrottlesGeneralCalls` de arriba — mismo `Guard.Check`
  llamado varias veces seguidas sin control de reloj, y este SÍ se vio
  fallar bajo carga real en esta sesión. Mismo `Guard.SetClock`, mismo
  reloj congelado.
  **Cierre de la serie de tests intermitentes** (T33, T19, T14, avatares,
  corepipeline, flood guard, circuit breaker): de acá en más, se arregla
  lo que falla en una corrida normal de la suite. Lo que solo aparece
  forzando 20-25 corridas de `go test ./...` en paralelo (peleando por
  CPU entre sí) no es un test roto — es un entorno que nadie usa; bajo
  carga suficiente, cualquier test que toque tiempo real puede fallar, y
  perseguir eso no tiene fondo. `TestControllerStopWaitsForInFlightOutboxSend`
  (con su propio timeout de 2s, no la causa ya arreglada),
  `TestControllerRunsPipelineEndToEnd` y `TestPrivilegedToolsRefuseNonBoss`
  cayeron solo bajo esa carga extrema (25 suites en paralelo) — quedan
  anotados, no perseguidos, hasta que alguno moleste en una corrida normal.

### Added
- **Rastro de mensajes ilegibles — `types.ReceiptTypeRetry`** (ct-2026-08-07,
  caso real: un contacto real recibió un mensaje del gateway que le llegó
  ilegible — WhatsApp le mostró "Esperando mensaje" en vez del texto — y
  el gateway nunca se enteró; se supo un día después, por una captura de
  pantalla). WhatsApp ya manda esta señal (`types.ReceiptTypeRetry`,
  documentado por la propia librería: *"the message was delivered to the
  device, but decrypting the message failed"*) y whatsmeow ya la
  despachaba como `*events.Receipt` — el switch de `handleEvent`
  (`inbound.go`) nunca tenía un caso para `*events.Receipt`, así que se
  perdía en silencio. Ahora deja un log claro (`whatsmeow: mensaje a
  %s (id %s) llegó al dispositivo pero no se pudo descifrar...`) filtrado
  a **solo** el tipo retry — `*events.Receipt` llega para todo tipo de
  acuse (entregado, leído, reproducido), y logueoar los demás habría
  vuelto a esconder la señal real en ruido. **Solo observa — no reintenta,
  no reenvía, no cambia el direccionamiento del envío.** Esa decisión
  (actualizar whatsmeow, rama `whatsmeow-update-prep` aparte) es de
  Citrino/el boss. No se persiste contra la base: el schema de `messages`
  no tiene una columna para esto hoy y agregar una es decisión de Citrino,
  no tomada acá.

---

## 0.1.8 — 2026-08-07

### Fixed
- **`onExit` del tray no cerraba nada** (ct-2026-08-07). El segundo callback
  de `systray.Run` en `tray_windows.go` estaba vacío — `WM_CLOSE`/
  `WM_ENDSESSION` (apagado de Windows, o un cierre externo sin `/F`) llegan
  hasta `fyne.io/systray` y de ahí no pasaban a ningún lado: el proceso de
  Piumy podía seguir corriendo después de que el ícono de bandeja
  desapareciera. Ahora `onExit` llama a `stop()`, el mismo `CancelFunc` que
  ya usan "Salir" y `ctx.Done()` (idempotente, sin caminos nuevos).
  Verificado con una instancia de prueba propia (nunca la de producción):
  `taskkill` sin `/F` → cierre ordenado completo (`"piumy-gateway shutting
  down"` seguido de `"piumy-gateway stopped"`) en ~600ms.
- **Voseo en los textos de error del instalador** (ct-2026-08-07). Los
  mensajes que `installer/windows/piumy.iss` le muestra al usuario final
  (`Result`/`Log`/`MsgBox` de claves existentes ilegibles, instancia
  corriendo sin poder cerrarse, clave repetida que no coincide, fallo al
  crear `piumy-config.json`, desinstalación) estaban en voseo rioplatense
  (`Cerrá`, `volvé`, `borrá`, `querés`, `Cerrala`, `Ábrelo`...) — Piumy se
  publica a terceros de cualquier país hispanohablante. Pasados a
  tratamiento de **usted**, mismo contenido, sin reescribir nada. La
  página de la clave del tablero (`CreateInputQueryPage`) tenía el mismo
  problema pero en tuteo (`Elige`, `vas a entrar`, `tu WhatsApp`,
  `puedes`, `Repite`) — una pasada anterior las había sacado del voseo
  sin llegar a usted; corregido también. `WizardForm.WelcomeLabel1/2` NO
  se tocaron: el comentario que las precede las marca como texto verbatim
  del boss.

---

## 0.1.7 — 2026-08-07

### Fixed
- **El instalador no cerraba el Piumy corriendo** (`ct-2026-08-07-0415-t34`,
  bug reportado por el boss con captura). Dos fallas, no una: en modo
  interactivo pedía al usuario cerrar la app a mano — y Piumy es de bandeja,
  sin ventana, así que ni sabía cómo. En modo desatendido (`/VERYSILENT
  /SUPPRESSMSGBOXES`) el diálogo de `AppMutex` (suprimido, respuesta por
  defecto Cancelar) disparaba un `EAbort` — Inno terminaba con exit code 1,
  pero **sin instalar nada**: el peor de los dos mundos, ya publicado en
  0.1.6. Auditando el registro apareció el alcance real: la última
  instalación que el instalador completó de verdad acá fue **0.1.3** — desde
  entonces cada actualización de binario fue manual (los `Piumy.exe.bak-pre-*`
  en la carpeta real lo confirman).
  `CloseApplications=yes` (RestartManager) no alcanza contra una app de
  bandeja sin ventana principal — confirmado con un decoy `-H windowsgui`
  que solo toma el mutex y duerme, sin código de producto real de por medio:
  con RestartManager solo, el decoy queda corriendo y el diálogo igual
  aparece. El cierre es explícito (`taskkill /F` por nombre de imagen,
  verificado releyendo `tasklist` — no el exit code de `taskkill`, que varía
  entre versiones de Windows) y corre en `InitializeSetup`, no en
  `PrepareToInstall`: un log real (`/LOG=`) mostró que el propio diálogo de
  Inno dispara y aborta ANTES de que `PrepareToInstall` llegue a
  ejecutarse — `InitializeSetup` es el primer código del script, corre antes
  que cualquier chequeo interno de Inno. Si no logra cerrar la instancia
  corriendo, el instalador ahora aborta con exit code distinto de cero —
  nunca más "0 sin instalar nada". Piumy se relanza solo al terminar (ya lo
  hacía, sin cambios).
  Probado contra un decoy propio, nunca contra la instalación real: modo
  silencioso — cierra el proceso viejo, instala, `DisplayVersion` en el
  registro pasa de la versión vieja a la nueva (no queda pegado), relanza,
  exit 0; modo interactivo — ningún diálogo aparece (confirmado por título
  de ventana, va directo al wizard normal); camino de falla — exit code
  distinto de cero confirmado forzando el chequeo de verificación.
- **T33 — actuar sobre otro chat dejaba el mensaje del dueño sin cerrar**
  (`ct-2026-08-06-1526`, caso real en vivo: el boss ordenó por WhatsApp
  escribirle a un tercer número, cambiarle las reglas y anotarle
  memoria/contexto — su propio mensaje le llegó dos veces).
  `send_message`/`draft` marcaban `MarkHandledBefore(to, ...)` — solo el
  chat DESTINO. Cuando el despacho que se está atendiendo es un chat
  distinto del destino, ese despacho nunca se marcaba, quedaba pendiente
  y el sweep lo re-despachaba con un nonce nuevo. `silent_act` nunca tuvo
  este bug (no tiene `to`, siempre marca el chat del despacho).
  - **No es un bug nuevo de T31** — corrección registrada en
    `docs/T33-DIAGRAMA-CERRAR-DESPACHO-OTRO-CHAT.md`: los bypasses de
    terminal-principal y despacho-boss en `levelGateMiddleware` ya
    permitían apuntar a otro chat antes de T31; T31 solo volvió eso el
    caso cotidiano, no lo creó.
  - Recorrido completo antes de codear (grep exhaustivo de
    `gate.Consume`/`MarkHandledBefore`, 4 call sites en todo
    `internal/mcpserver`): `approve_draft` verificado sano (marca el
    chat del borrador, nunca toca el turno propio, a propósito).
    Ninguna otra tool (`set_chat_rules`, `set_chat_memory`,
    `set_chat_context`, `set_chat_status`, `set_chat_active`,
    `set_mode`, `escalate`, `claim_chat`, `release_chat`,
    `mark_handled`, `resolve_chat`) toca el turno — nunca lo tocó. Si el
    turno entero de un agente es una de estas, el despacho queda
    atado-sin-consumir hasta `DispatchStaleAfter` (15 min) — y el
    **terminal entero** queda bloqueado ese tiempo, no solo ese chat.
    Decisión (con Citrino): no cerrar automático ahí — arreglado por
    skill, no por código.
  - Fix: `markDispatchChatIfDifferent` (`send.go`), un helper compartido
    entre los 3 call sites reales — cierra también el chat del despacho
    activo cuando difiere del destino, usando `active.BurstMaxTS` (nunca
    `now`, no marca de más). No-op en el caso de siempre (mismo chat).
  - Skill del operador (`internal/mcpserver/manuals/operator/SKILL.md`)
    corregida: tres afirmaciones que el código no respaldaba (una
    contradecía a otra línea del mismo documento — mismo patrón que el
    cifrado de T28), más la instrucción explícita del costo real de no
    cerrar un turno puramente administrativo.
  - Tests: `TestSendMessageToAnotherChatAlsoClosesDispatchChat`/
    `TestDraftToAnotherChatAlsoClosesDispatchChat` (confirmados que
    fallan sin el fix, antes de darlos por buenos),
    `TestSendMessageToAnotherChatDoesNotMarkDispatchMessagesAfterBurst`,
    `TestSendMessageSameChatDoesNotDoubleMark`.
- **T19 — el freno de emergencia ya sobrevive un reinicio**
  (`ct-2026-08-05-1249`, descubierto probando la instancia con sesión
  real — T10). `governor.SetKill`/`state.Muted` vivían solo en memoria:
  un reinicio (corte de luz, un update, un crash — no hipotético, le
  pasó al PC del boss: hibernó y el gateway se cayó) soltaba el freno en
  silencio. Si en ese momento estaba frenado por una razón real, el
  gateway volvía mandando exactamente cuando menos había que hacerlo.
  - `set_kill_switch` (MCP y `POST /api/admin/kill`) ahora también
    persiste a `store.SettingKillSwitch`, ANTES de aplicar el efecto en
    vivo — best-effort, logueado, nunca bloquea el freno en sí por un
    problema de disco.
  - `main.go`'s `restoreKillSwitch` (nuevo) relee esa marca al arrancar y
    aplica **las dos mitades juntas** (`governor.SetKill(true)` +
    `state.SetMuted(true)`) — llamado justo después de que existen
    `gov`/`sm`, muchísimo antes de `ctrl.Start()` (el único call que
    puede hacer que el pipeline mande algo). El orden es la parte que
    importaba: si arranca mandando y recién después lee el ajuste, ya
    salió lo que no debía.
  - El tablero ya mostraba el freno puesto en vivo (badge "⛔ kill" +
    carita en mood `muted`) — con el freno restaurado correctamente
    desde el arranque, esos mismos indicadores ahora también reflejan un
    freno que sobrevivió un reinicio, sin UI nueva.
  - Verificado con el binario real: `store` sembrado con el freno puesto,
    reinicio, `GET /api/status` confirmado devolviendo
    `governor_killed: true` / `muted: true` desde el primer poll.
  - Tests: `main_test.go` (`TestRestoreKillSwitch*`, la restauración en
    sí) + `corepipeline/outbox_test.go`'s
    `TestKillSwitchSurvivesRestartAndReallyBlocksSending` — la prueba
    explícitamente pedida de que un freno restaurado bloquea un envío
    REAL, no solo que las banderas queden en `true`. Extendidos también
    `TestSetKillSwitchFlipsGovernorAndState` (mcpserver) y
    `TestSetKillSwitchEndpoint` (restapi) para cubrir la persistencia.
- **T18** (`ct-2026-08-05-1243`, el boss: su bandeja mezclaba ~1351 chats —
  gente que le escribió junto a números que solo aparecieron en un grupo)
  — `store.ChatOrigin` solo miraba mensajes ENTRANTES
  (`from_me=0`): un chat que el dueño inició y nadie contestó caía en
  `group_discovered`/`synced_contact` en vez de `inbound_spoke`, aunque
  sea una conversación real. Reproducido con 3 casos concretos antes de
  codear. Fix: reusa `realMessageSQL` (misma constante que
  `ChatJIDsWithMessages` ya usa) sin filtro de dirección — de paso quedó
  excluido el ruido de protocolo (recibos/reacciones sin texto ni tipo)
  que la consulta original tampoco filtraba. El valor `"inbound_spoke"` se
  mantuvo sin renombrar (viaja por `list_chats`/`get_chat`/
  `decision-policy.md`) — se ajustaron esas 3 descripciones para decir lo
  que el criterio significa de verdad, y remiten a `last_speaker` para
  "me toca responder".
  - `chatOut.Origin` expuesto en `GET /api/chats` — ya se calculaba en
    cada `ListChats` (`enrichChat`) y se descontaba antes de esto.
  - La pestaña Chats del tablero filtra por `origin === "inbound_spoke"`
    (`app.js#isRealConversation`) en vez de `has_messages`.
  - **Hallazgo sin resolver, flagueado a Citrino:** `chat_groups` (la
    tabla que lee `group_discovered`) no tiene ningún escritor en código
    de producción — solo tests. El sync real de grupos escribe
    `group_members` (tabla hermana) a propósito, nunca `chat_groups`.
    `group_discovered` no dispara hoy en producción, y `get_chat_groups`
    (MCP) siempre devuelve vacío. La implementación paralela por
    `group_members` que oculta números-solo-de-grupo en `handleChats`
    quedó SIN TOCAR — es la única de las dos que funciona hoy; borrarla
    antes de resolver `chat_groups` habría sido peor que dejarla.
  - `docs/T18-DIAGRAMA-SEPARAR-POR-ORIGEN.md` con el detalle completo;
    `docs/MANUAL.md` documenta el hallazgo de `chat_groups` y distingue
    los tres conceptos de "origen" que conviven en el código (pedido de
    Citrino).
- **T17 Parte 1** (`ct-2026-08-05-1240`, el boss mandó capturas: la
  cabecera decía "(sin nombre)" sobre un número real) — `recordOwnIdentity`
  pisaba `state.OwnName` SIN CONDICIÓN en cada reconexión; cuando
  `client.Store.PushName` volvía vacío (confirmado contra la librería
  whatsmeow: esa mutación de appstate puede no repetirse nunca para una
  cuenta ya asentada, y no hay ninguna llamada consultable para pedirlo de
  nuevo), borraba cualquier nombre ya conocido — no un parpadeo al
  arrancar, el estado permanente. Reproducido a nivel código antes de
  tocar nada (`seedFakeDevice`, sin pareo real). Fix: `recordOwnIdentity`
  solo pisa `OwnName` cuando el valor nuevo no es vacío (mismo criterio
  que `TouchChat` ya aplica a `chats.name`); `state.NewManager` siembra
  `OwnName`/`OwnJID` (solo esos dos campos, nunca el resto del `Status`)
  desde un `status.json` existente al arrancar.
  - La hipótesis original ("arranca vacío, el archivo no se relee") no
    explicaba por completo lo que vio el boss — `OwnJID`/`OwnName` los
    escribe la misma llamada, así que un arranque en blanco dejaría los
    dos vacíos, no solo el nombre. La causa real es la de arriba.

### Added
- **Sello de versión en `get_manual`** (ct-2026-08-07, motivado por el
  rescate de contenido solo-en-copias: hasta ahora no había forma de saber
  si un manual leído era el de la versión que corre). Cada manual servido
  por `get_manual` termina con `<!-- piumy-skill-version: X.Y.Z -->`,
  agregado por `manualWithVersionStamp` al responder — nunca escrito en el
  `.md` embebido, lee `version.Version` en vivo en cada llamada, así nunca
  puede desincronizarse del binario que lo sirve.
- **T17 Parte 3 — avatares** (`ct-2026-08-05-1240`, sub-cambio partido
  aparte con OK de Citrino). Funcionalidad nueva y delicada: pedir fotos
  de perfil es actividad hacia WhatsApp, cuenta para el anti-ban igual
  que cualquier acción server-facing — 719 números/591 contactos hacen
  que un barrido masivo sea exactamente el patrón que tira una cuenta.
  - **Nunca un sweep.** `whatsmeow.Adapter.RequestAvatar(jid)` solo se
    llama desde `restapi` para un chat que el tablero está mostrando
    ahora mismo (cabecera o una fila visible de la lista) — nunca sobre
    todos los números conocidos.
  - **Bajo demanda, pero con cola paceada.** Sin la cola, "bajo demanda"
    con varios chats visibles a la vez sería una ráfaga disfrazada —
    `avatarWorkerLoop` drena un jid a la vez, espaciado con el mismo
    `governor.DelayWindow` que ya pacea el backfill de contactos/media
    (`actionDelay()`).
  - **El chequeo "¿cambió?" es gratis del lado del protocolo.**
    `GetProfilePictureInfo` con el `ExistingID` cacheado devuelve
    `(nil, nil)` sin transferir bytes cuando la foto no cambió —
    verificado contra la librería whatsmeow vendorizada. El riesgo
    anti-ban es la frecuencia de PREGUNTAR, no el peso de bajar.
  - **Ventana de re-chequeo aleatoria, nunca fija.** Corrección de
    Citrino sobre el primer borrador (que proponía "cada 7 días" liso):
    un intervalo fijo es un patrón, y los patrones son lo que se
    detecta. `avatarRecheckWindow()` sortea (mismo mecanismo
    `governor.DelayWindow.Random()`) una ventana de 3-9 días por jid, en
    cada chequeo — nunca el mismo offset dos veces para el mismo número.
    Override en caliente vía `store.SettingAvatarRecheckMin/Max`
    (`PIUMY_AVATAR_RECHECK_MIN/MAX`).
  - **Cache en disco** (`store.Avatar`, tabla `avatars` — deliberadamente
    fuera de `resetTables`, es un cache de WhatsApp, no historial del
    boss). Confirmado-sin-foto borra el archivo cacheado (la persona
    pudo haberla sacado); cualquier otro error bumpea el próximo chequeo
    sin tocar el cache existente.
  - **`GET /api/avatar?jid=`** sirve los bytes cacheados (mismo patrón
    binario que `GET /api/media`) y dispara el chequeo paceado de paso,
    sin esperarlo — sin cache, 404 inmediato.
  - **Sin foto → iniciales**, nunca un cuadrado vacío ni un ícono roto
    (`app.js#buildAvatar`, cabecera + lista de chats).
  - **Evidencia real del pacing** (pedido explícito, "es la parte que
    puede costar una cuenta"): `TestAvatarWorkerLoopPacesRequestsWithVariableGaps`
    drena la cola con el loop real y mide separaciones reales entre
    pedidos — nunca por debajo del mínimo configurado, nunca repetidas.
  - `docs/T17-DIAGRAMA-NOMBRE-Y-AVATAR.md` actualizado con el detalle
    completo de esta parte.

### Changed
- **T32** (`ct-2026-08-06-1109`, acuerdo con el líder de CleverCoder,
  implementado del otro lado en v1.6.68.191) — el handshake de cAPI ya no
  colapsa tres motivos de "no entrega" en un único error genérico.
  CleverCoder ahora distingue: `antenna_off`/`position_empty`
  (TRANSITORIO — el id es válido, no hay nadie ahí ahora mismo) vs.
  `terminal_gone` (PERMANENTE — no corresponde a nada, nunca va a
  existir). El despacho actúa distinto según cuál:
  - `antenna_off`/`position_empty` → vuelve a la cola, igual que antes de
    este cambio (reintento cada sweep, no consume el presupuesto de
    redespacho, no da de baja el registro del agente).
  - `terminal_gone` → `CleverInjector` descarta su propia credencial
    (`markDead`, nuevo — `Configured()` pasa a `false`) y `dispatch()` deja
    de reintentar contra ese agente, con una línea de log propia
    explicando el motivo (no la genérica "canal caído", que implica una
    recuperación que acá nunca llega). Solo una credencial nueva
    (`SetConfig`, vía `set_capi_connector`/`register_agent`) lo revive.
  - Un código que esta versión no reconoce (un CleverCoder viejo con el
    `terminal_not_listening` de antes, o uno nuevo del futuro) se trata
    como transitorio y queda en el log — nunca rompe ni descarta nada.
  - **Compatibilidad hacia atrás intacta**: contra un CleverCoder anterior
    a la 191 (sin el campo `error` en el body, o con un código que esta
    versión no conoce) el comportamiento es EXACTAMENTE el de antes de
    este cambio.
  - Gratis, mismo protocolo: el `chat_id` que arma CleverCoder ahora es
    estable y calculado; sin party, la forma sin agente nombra una
    POSICIÓN (el N-ésimo terminal del proyecto), no un terminal puntual —
    documentado en `store.Agent.AntennaTerminalID`, porque explica por qué
    `position_empty` es normal y no una credencial mal configurada.
  - `docs/T32-DIAGRAMA-MOTIVOS-NO-ENTREGA.md` con el detalle completo.
- **T14 — el buscador va dentro de las pestañas** (`ct-2026-08-05-1232`,
  el boss: *"y el buscador lo quiero dentro de las pestañas no fuera
  (antes)"*). El `<input id="search">` vivía suelto arriba de todo, antes
  del bloque de pestañas — buscando una sola cosa (conversaciones) pero
  posicionado como si buscara en todo, cuando en realidad solo actuaba
  sobre 3 de las 6 pestañas (Agentes/Reglas/Drafts nunca filtraron nada).
  - **Reposicionar, no duplicar.** El mismo `<input>` (uno solo, nunca
    copiado por pestaña) se movió a DESPUÉS de `.tabs-head` y ANTES de
    los `.tab-panel` — queda visualmente agrupado con el contenido de la
    pestaña activa, no separado arriba. `state.filterText`/
    `matchesNeedle`/`foldAccents` ya eran los únicos helpers que Chats,
    Grupos y Contactos compartían — no había lógica repetida que
    consolidar, solo un input mal ubicado prometiendo más de lo que hacía.
  - **`SEARCHABLE_TABS`** (`app.js`, nuevo) — el único lugar que decide
    qué pestañas buscan y con qué placeholder. El mismo handler que
    cambia de pestaña muestra/oculta el input entero para las que no
    filtran (Agentes/Reglas/Drafts) en vez de dejarlo presente y muerto.
  - **El filtro NO se arrastra al cambiar de pestaña** — se limpia
    (`state.filterText` + el valor del input) en cada cambio. Cada
    pestaña busca un universo distinto; una lista vacía sin ninguna pista
    de que hay un filtro viejo activo se lee como "está roto", no como
    "no hay resultados acá" — ahorrar un re-tipeo no compensa esa
    confusión.
  - Alternativa descartada: reparentar el `<input>` por JS dentro de cada
    `.tab-panel` en cada cambio de pestaña, en vez de dejarlo fijo entre
    `.tabs-head` y los paneles — más código por el mismo resultado
    visual, ya que las 3 pestañas que buscan comparten el mismo layout
    genérico.
  - `node --check` sobre `app.js`; smoke manual (binario compilado,
    `GET /dashboard/` verificado sirviendo el HTML/JS con la nueva
    estructura) — sin navegador disponible en este entorno para el pase
    visual completo.

### Verified
- **T17 Parte 2** (`ct-2026-08-05-1240`) — investigado antes de tocar
  código: el push name YA se guardaba (`TouchChat` en cada mensaje
  entrante) y la precedencia agenda → push name → número YA existía en
  el frontend (`app.js#renderRow`, desde S1h). No era un bug — cerraba
  solo un hueco de confirmación a nivel API
  (`TestChatsEndpointNameCarriesPushNameWithNoAgendaEntry`). Los números
  pelados que vio el boss son chats sin ningún mensaje entrante real —ahí
  no hay push name que mostrar—, y T18/T18B (mismo día) ya los saca de la
  pestaña Chats.

### Removed
- **T18B** (`ct-2026-08-05-1243`, sub-cambio sobre el hallazgo de T18,
  decisión del boss vía Citrino) — se retira `chat_groups` entera:
  `store.AddGroupMember`/`RemoveGroupMember` y la tabla misma. Nunca tuvo
  escritor en código de producción (solo tests) — dos tablas para la
  misma relación con solo una viva es la deuda que veníamos pagando todo
  el día; `group_members` (la que el sync real de grupos escribe) queda
  como fuente única.
  - `ChatOrigin`/`GroupsOf`/`get_chat_groups` (MCP) leen `group_members`
    ahora — `group_discovered` dispara de verdad por primera vez, y
    `get_chat_groups` devuelve datos reales en vez de vacío siempre.
  - La implementación paralela de `handleChats` (`isGroupMemberCanon`,
    dejada sin tocar en T18 por estar bloqueada en esto) se consolidó
    sobre `store.ChatOrigin` — cierra el punto 4 pendiente de T18.
  - **Verificado antes de borrar, no dado por hecho**: sin datos que
    migrar (confirmado, cero escritor real); `group_members` cubre
    grupos donde el gateway no es admin (verificado contra la librería
    whatsmeow vendorizada — sin filtro por rol en ningún lado);
    `group_members` solo se refresca al conectar/reconectar o con un
    `KickResync` explícito, nunca en vivo por evento — documentado como
    ventana de frescura conocida, no resuelto (no era lo pedido), no
    peor que antes (que nunca funcionaba en absoluto).
  - `resetTables` de 8 a 7 nombres. `reconcile.go`'s nota de alcance
    (fusión @lid↔número) actualizada a `group_members`.
  - `docs/T18B-DIAGRAMA-RETIRAR-CHAT-GROUPS.md` con el detalle completo;
    `docs/T18-DIAGRAMA-SEPARAR-POR-ORIGEN.md` marcado resuelto sin
    reescribirse.

---

## 0.1.6 — 2026-08-07

### Added
- **Preámbulo en todo despacho** (`ct-2026-08-05-0155`, boss verbatim: *"si soy
  boss tiene que decir is boss, y si no, el preámbulo son las reglas. Todo
  mensaje con su preámbulo"*). `dispatchPayload` tenía un `if !isBoss` que
  omitía el bloque entero justo para el dueño: su mensaje llegaba con el texto
  y el nonce, sin reglas y sin decir con quién se hablaba — el agente tenía que
  deducirlo. Comprobado en vivo. Ahora la línea de identidad va siempre
  (`is_boss` / `is_approver` / nivel) y las reglas efectivas se adjuntan para
  cualquier nivel, incluido el dueño, cuyas reglas propias se descartaban.
  `get_instructions` expone los mismos dos campos para el agente que se
  reconecta a mitad de camino.
- **Versión única de verdad** (`VERSION` en la raíz). Convivían tres números y
  ninguno coincidía: el código decía `0.1.0`, el instalador `0.1.4`, y lo
  publicado era `0.1.5`. `build-all.sh` inyecta por ldflags y genera el define
  del instalador; `server.go` usa la constante; **`get_status` expone el campo
  `version`** — es la única tool que responde sin despacho activo, así que un
  cliente puede preguntar contra qué versión habla apenas se conecta. `go:embed`
  no lee fuera del paquete y los symlinks no son portables en Windows:
  `internal/version` guarda una copia que el build resincroniza, con test que
  falla si divergen.

### Security
- **Datos personales fuera del repositorio.** Auditando antes del primer push
  público apareció `docs/S13-C1-PARES.csv` con **559 personas — nombre y
  teléfono**, más el JID del boss, nombres de contactos en comentarios y un
  grupo real en tests. Evidencia sacada del repo, ~124 identificadores
  sustituidos por prefijo `555` en 35 archivos, docs reescritos. Freno duro en
  `.githooks/pre-commit` (activar con `git config core.hooksPath .githooks`),
  reglas en `constitution.md §2b`. Boss verbatim: *"no deberia haber ni siquiera
  numeros de prueba… el server debe venir limpio, igual que un server nomral"*.
  Las cabeceras de copyright conservan la autoría: ahí el nombre es del autor.

### Docs
- **Manuales embebidos**: dónde se instala Piumy con rutas concretas, de dónde
  se descarga, la configuración MCP por defecto, y que **conectado no es
  habilitado** — sin despacho activo toda tool de acción responde `default DENY`,
  `get_status` es la puerta permitida, y un despacho no se fabrica.

### Published
- **Código fuente público**: `github.com/chamilonster/piumy-gateway`, AGPL v3,
  historia limpia (el historial de desarrollo contiene datos reales de pruebas).
  Repo separado del sitio a propósito: `piumy.app` se publica desde `master` de
  `chamilonster/Piumy` y un push encima lo habría tumbado.
- **Instaladores 0.1.5 y 0.1.6** como releases en `chamilonster/Piumy`.

### Known issue
- **El instalador no cierra el Piumy corriendo** (`ct-2026-08-07-0415-t34`, en
  vuelo). `AppMutex` detecta la instancia viva pero falta `CloseApplications`:
  muestra un diálogo pidiéndole al usuario que lo cierre a mano — y Piumy es app
  de bandeja, sin ventana. Peor: en modo desatendido **devuelve código 0 sin
  instalar nada**. Afecta a 0.1.5 y 0.1.6. Sale arreglado en 0.1.7.

---

## Histórico sin versión asignada

Entradas anteriores a que el proyecto empezara a publicar releases. Salieron
en alguna versión entre 0.1.1 y 0.1.6, pero fijar cuál exactamente requeriría
el commit-corte de cada una — no adivinado, queda sin asignar.

### Changed
- **T31** (`ct-2026-08-06-0244`, decisión del boss, revierte S10 —
  `ct-2026-07-30-1349` — para un solo botón) — `set_chat_rules` se
  desbloquea por MCP, **sin condiciones**. Reemplaza dos versiones de este
  contrato que el boss rechazó (una llave — despacho del chat del dueño —,
  después dos — esa más ser el agente principal). Su argumento, verbatim:
  *"No pongas condiciones, que la skill recomiende nada mas, me cargan que
  metan tantas limitaciones y frenos miedosos... ya es responsabilidad del
  usuario."* Respaldado con la evidencia del propio día: el whitelist que
  lo bloqueaba a él mismo (T30), las reglas vacías que dejaban el sistema
  mudo (T5), el cifrado obligatorio (T28) — frenos agregados por
  precaución, los tres deshechos después.
  - El handler llama `store.SetChatRules` directo — sin chequeo de nivel,
    sin `chatScopedArg` (cualquier `chat_id`, no solo el del despacho
    propio), sin requerir un despacho atado. Ausente de `bossOnlyTools`,
    `chatScopedArg` y `selfGatedTools` — cero gate, en ningún lado.
  - `set_type_rules`/`set_default_rules`/`set_is_boss` **sin cambios** —
    siguen MCP-BLOCKED incondicional, exactamente como S10 los dejó. El
    boss pidió destrabar las reglas de un chat, no las de alcance amplio.
  - La arquitectura que el boss sí quiere (agentes separados: uno con esta
    capacidad, otro que atienda desconocidos sin tenerla) queda como
    **recomendación** en la skill `piumy-operator` — tono de consejo de
    diseño, no de gate ni de advertencia de seguridad.
  - `docs/T31-DIAGRAMA-DESBLOQUEAR-SET-CHAT-RULES.md` con el detalle
    completo; `docs/S10-DIAGRAMA-GATE-DURO-CONFIG.md` queda intacto con
    una nota apuntando acá (misma disciplina que T28 con los docs de T2).

### Added
- **T16** (`ct-2026-08-05-123257`, pestaña Drafts — depende de T15) — el
  footer fijo "Confirmaciones pendientes" pasa a ser la 6ª pestaña del
  tablero, con el contador **sobre el botón** (`#draftbadge`, visible sin
  entrar a la pestaña, pedido literal del boss vía Citrino). Adentro:
  leer el mensaje, editar (`✎`), rechazar con motivo obligatorio (`↩`,
  modal propio, sin `window.prompt`), aprobar (`✓`) y descartar (`✕`) —
  los dos últimos ya existían, `editar`/`rechazar` llaman el backend de
  T15 (`edit-draft`/`reject-draft`). Una fila muestra "— ronda N" cuando
  el draft es un redraft (T15's `round`, ya viajaba en el JSON, nadie lo
  mostraba).
  - **El contador se actualiza solo** — pedido explícito de Citrino: "un
    borrador que aparece y no se ve hasta recargar es una respuesta que
    salió tarde." Nuevo `Event.Type` `"draft"` (`eventbus`), publicado
    desde los 6 puntos donde un draft se crea o se resuelve
    (`mcpserver/send.go`+`admin_tools.go`, `restapi/admin.go`) vía
    `publishDraftChanged` (un helper por paquete, no comparten
    internals). `app.js` cuelga `REFRESH_ON.draft` del mismo ciclo de
    auto-refresco SSE que ya existía (`docs/DASHBOARD-AUTO-REFRESH-2026-07-24.md`)
    — sin polling nuevo, el de 15s queda como red de seguridad.
    `mcpserver.Deps` gana `Bus *eventbus.Bus` (nuevo — `restapi.Deps` ya
    lo tenía) para que un draft resuelto por MCP (el boss diciendo
    "aprobá los pendientes") nudgee al tablero igual que uno resuelto
    desde la propia UI.
  - Verificado contra un binario real en scratchpad (DB sembrada a mano,
    nunca la instalación real): HTML/JS servidos contienen los elementos
    nuevos, `node --check` sobre el `app.js` servido, `edit`/`reject`
    (ronda normal y en el tope)/`approve`/`discard` mutan la lista como
    se espera, y una llamada real a `discard-draft` hizo aparecer
    `{"type":"draft"}` en el stream SSE (`curl -N /api/events`) —
    confirma el camino completo. Sin extensión de Chrome disponible en
    esta sesión para el click-through visual, dicho explícitamente.
  - `docs/T16-DIAGRAMA-PESTANA-DRAFTS.md` con el flujo completo.
- **T15** (`ct-2026-08-05-123241`, backend de borradores) — rechazar un
  borrador con motivo, tope de tres rondas, y editar sin aprobar.
  - `reject_draft`/`POST /api/admin/reject-draft` — a diferencia de
    `discard_draft` (final), pide otro intento: la razón queda grabada EN
    el draft (`drafts.reject_reason`, no un canal aparte — pedido explícito
    de Citrino: "el motivo tiene que viajar con el mensaje, no aparte") y,
    si la ronda rechazada es menor a `store.MaxDraftRounds` (3), el mensaje
    que la disparó vuelve a `PendingDedicated` (`store.MarkPendingBefore`,
    inverso de `MarkHandledBefore`) para que el próximo sweep de `capipush`
    redespache. `capipush.dispatchPayload` antepone el motivo + el borrador
    anterior al payload — el agente lo ve junto con el mensaje original, no
    tiene que ir a buscarlo.
  - Tope de rondas (`drafts.round`, calculado por `nextDraftRound` en cada
    `AddDraftWithConfirmer`): un redraft que responde a un rechazo continúa
    la cadena (ronda+1); cualquier otro caso (aprobado, descartado, chat sin
    drafts previos) arranca en ronda 1. Rechazar la ronda 3 registra el
    motivo pero NO redespacha — el ciclo automático para ahí, el dueño
    resuelve con `edit_draft`/`discard_draft`.
  - `edit_draft`/`POST /api/admin/edit-draft` — reemplaza el texto de un
    draft pendiente sin aprobarlo ni cambiar su status.
  - Ambas tools MCP, misma familia que `discard_draft`: nunca envían →
    restringen → siempre permitidas, ningún nivel de dispatch requerido
    (`selfGatedTools`, sin chequeo propio en el handler).
  - `docs/MANUAL.md`/`AGENT-BEHAVIOR.md` no tocado en el segundo (T15 no es
    parte del gate duro de envío) — solo `MANUAL.md`, con el detalle de
    arriba en las secciones `store`/`mcpserver`/`capipush`/`restapi`.

### Fixed
- **T30** (`ct-2026-08-06-0159`, Citrino, decisión del boss: *"el criterio
  de salida tiene que alinearse con el de entrada"*) — el chat del dueño
  (`is_boss=1`) queda exento del whitelist anti-ban del router, en las dos
  direcciones. Origen: Citrino recibió el mensaje de vuelta del boss y el
  gateway rechazó el envío por whitelist vacía — el chat tenía rules,
  había pasado el ritual entero, y aun así no se pudo contestar. Era el
  único de los cuatro gates de is_boss que no lo eximía:
  `initiateAuthorized` (enviar sin dispatch atado), `store.PendingDedicated`
  (T5, entrada a la cola) y `capipush.dispatch` ("is_boss ⟹ principal, sin
  configuración de router.json") ya lo hacían.
  - `mcpserver.validateSend` (`send.go`) — salta `d.Router.Resolve(to).Allowed`
    cuando `c.IsBoss`. Todo lo demás (muted, JID, claim, rules,
    grupo-no-ignorado, policy_version) se sigue aplicando igual.
  - **Hallazgo de yapa, recorriendo el camino completo — más grave que el
    reportado:** `corepipeline.handleInbound` (la entrada, no la salida)
    aplicaba el mismo whitelist sin excepción — un `router.json` sin el
    número del dueño descartaba sus propios mensajes ENTRANTES en
    silencio: sin guardar, sin publicar al eventbus, sin log. Peor que la
    salida, que al menos devuelve un error de tool visible. Nueva función
    `isBossChat(jid)` (`pipeline.go`) — mismo criterio, mismo "sin
    beneficio de la duda" que `initiateAuthorized` si el chat nunca se
    tocó.
  - El governor (`internal/whatsmeow`, el anti-ban de pacing/rate-limit
    real) — sin tocar, a propósito.
  - `AGENT-BEHAVIOR.md` (check 6 del gate duro) y `docs/MANUAL.md`
    actualizados — la excepción quedó escrita como decisión tomada
    (mismo criterio que T28), no como propiedad categórica que el
    próximo reconstruye. Documentado también el falso amigo: `chat.Status`
    usa los mismos literales `"whitelist"`/`"blacklist"` para un concepto
    de UI sin relación con el whitelist del router.
  - Tests nuevos: `TestSendMessageWhitelistBypassedForBossChat`
    (`mcpserver`) y `TestHandleInboundBypassesRouterGateForBossChat`
    (`corepipeline`) — el test existente que aserta el bloqueo para un
    chat NO-boss (`TestSendMessageWhitelistGateStillApplies`) queda
    intacto, su jid nunca tuvo `is_boss`.

### Removed
- **T28** (`ct-2026-08-05-2242`, decisión del boss, revierte T2 —
  `ct-2026-08-05-0205`) — el despacho ya NO lleva una segunda capa de
  cifrado propia. Boss verbatim: *"Clever coder es mío, y lo programo
  yo... Lo único que hace es buscar actualizaciones del mismo programa.
  Entonces, no hay nada de qué protegerse."* El canal cAPI (CleverCoder)
  ya es un túnel cifrado por handshake — la capa que T2 agregó adentro
  solo protegía el contenido de CleverCoder mismo, y CleverCoder es del
  dueño, en su propia máquina. **Sin flag, sin interruptor** (corrección
  sobre T27, que había propuesto dejarlo apagado por default: un flag
  apagado es justo el mecanismo por el que esto vuelve — alguien lo
  prende "porque estaba ahí"). Es la tercera vez que el boss pide esto —
  eso cambió el problema: el tramo incluyó auditar POR QUÉ volvía.
  - **Borrado, no deshabilitado:** `internal/capi` (Producer/Encrypt/Decrypt,
    AES-256-GCM) y `cmd/agentclient` (decrypt_dispatch) — paquetes
    enteros. `config.CAPIKey`/`PIUMY_CAPI_KEY`,
    `config.CAPIPlaintext`/`PIUMY_CAPI_PLAINTEXT`,
    `capipush.Config.Plaintext`, `restapi.Deps.Plaintext`/`CAPIProducer`,
    `agentconnect.Info/Params.CAPIKey`/`AgentClientPath` — ninguno
    sobrevive como campo vacío, todos dejaron de existir.
    `capipush.plaintextPayload` renombrada a `dispatchPayload` (ya no hay
    un segundo modo del que distinguirse). El instalador ya no genera ni
    preserva una 4ta clave ni empaqueta `agentclient.exe` — versión
    0.1.3 → 0.1.4. `build-all.sh` ya no compila el binario del agente.
  - **La causa real de que volviera tres veces:** cuatro lugares
    afirmaban el cifrado como propiedad estructural, en prosa categórica
    ("el cifrado nunca es opcional", "no es opcional") — el manual de
    conexión (`piumy-connect`), el del orquestador (`operacion.md`), el
    mapa del proyecto (`docs/MANUAL.md`) y un comentario del instalador.
    Los cuatro reescritos para decir que la decisión SE TOMÓ, no para
    describirla como regla del sistema — el próximo que los lea no tiene
    nada que "arreglar" ahí.
  - **Agregado del boss, mismo tramo:** los tres manuales de Piumy
    (`connect`/`operator`/`orchestrator`) enlazan a la skill
    `capi-protocol` (CleverCoder) para el protocolo real (handshake,
    pinpass, el túnel cifrado) en vez de redescribirlo — una sola fuente,
    el protocolo no es nuestro.
  - **Qué NO se tocó:** el AES-256-GCM propio de `CleverInjector`
    (`postMessage`/`deriveKey`) — es el túnel real de cAPI, el que
    justifica la decisión. `capiconn` (conectarse a la antena, concepto
    distinto). El cifrado de `sessionbackup`/`PIUMY_BACKUP_KEY` — feature
    aparte. Los docs históricos de diseño (F4/F4B/F5/S1/S4C/S5,
    `T21-DIAGRAMA-INSTALADOR-KEY-SAFETY.md`) — quedan como registro de la
    decisión ORIGINAL, no se reescriben (misma razón que la corrección de
    T25: la traza de qué se decidió vale más que un archivo que la
    borra). `.claude/skills/piumy-connect/SKILL.md` (copia local, no
    trackeada) queda con el texto viejo hasta que CleverCoder la
    resincronice desde la fuente ya corregida.
  - Detalle completo, con diagrama, en
    `docs/T28-DIAGRAMA-CAPI-SIN-CIFRADO.md`.

### Added
- **T29** (`ct-2026-08-06-0140`, Citrino) — manual de conexión
  (`piumy-connect`) y del orquestador (`operacion.md`) advierten cómo
  armar un `curl` cuyo cuerpo lleve texto humano: nunca inline en la línea
  de comandos, siempre por archivo UTF-8 + `--data-binary @archivo`. Boss
  verbatim: *"eso del armado debería ser automático o expresado con skill
  para que no falle en ningún idioma."* Origen: Citrino armó un JSON
  pasando texto por la línea de comandos y le llegó al boss con acentos
  rotos; mandado desde archivo llegó perfecto. No es un bug de
  piumy-gateway — `encoding/json`/`net/http` manejan UTF-8 bien siempre —
  la corrupción pasa antes, en la terminal del que llama: recodifica el
  argumento con su codepage local (casi nunca UTF-8) antes de que el
  proceso lo reciba. No es específico del español: en portugués o alemán
  se rompe una tilde, en chino o árabe el texto entero se pierde
  (reemplazado por `?`). Probado con las dos formas, lado a lado, en
  español/portugués/alemán + chino + árabe.
- **T25, hallazgo 2** (`ct-2026-08-05-1833`, decisión de Citrino tras el
  smoke) — `PIUMY_DEFAULT_TERMINAL_ID` vacío ya no deja a is_boss sin
  destino cuando la antena principal está configurada: el propio log de
  arranque mostraba la advertencia de "vacío" seguida, en la línea
  siguiente, del terminal real al que capipush ya despachaba todo lo
  demás — un cable de medio camino, no una configuración faltante.
  `resolveDefaultTerminalID` (`main.go`, función pura, testeada) usa ese
  mismo `terminalID` (KV `capi_terminal_id` o env) como respaldo — la
  variable de entorno sigue ganando si está puesta, y el WARNING real
  ahora solo dispara cuando NI el env NI la antena dan un terminal. Para
  ese último caso, alarma nueva en el tablero
  (`default_terminal_configured`, `GET /api/status` — mismo patrón que
  `factory_password`/`antenna_configured`, sin caché). Verificado en vivo
  (binario real, no la instalación del boss): configurar la antena
  MIENTRAS el proceso corre no aplica el respaldo solo (`PortFallback` es
  inmutable post-arranque, ya documentado en `capipush.go` — no es un
  hueco nuevo); reiniciar con la antena ya puesta sí lo aplica, log y
  alarma lo confirman. De paso: **corrección sobre el hallazgo 3** de esta
  misma investigación — no era un falso positivo. Citrino comparó el hash
  guardado del boss contra bcrypt de "piumy" de forma directa: da
  verdadero, T9 funcionó exactamente como debía. La prueba de login que
  parecía contradecirlo tenía un campo mal armado (`user` en vez de
  `username`). El test agregado en su momento
  (`TestFactoryPasswordAlarmIgnoresEnvSeedWhenOwnerAlreadyChangedIt`) sigue
  siendo válido, cubre un caso real, pero no era el causante. Detalle
  completo en `docs/T25-DIAGRAMA-SMOKE-WHATSAPP-SILENCIO.md`.
- **T25** (`ct-2026-08-05-1833`, primer smoke real sobre la instalación del
  boss) — WhatsApp no arrancó tras la actualización y no había NI UNA línea
  de whatsmeow en el log de arranque, ni éxito ni error. Descartadas con
  test aislado (no sobre la instalación real): el wiring
  `piumy-config.json`→env→`cfg.WADBPath` (sano), una ruta con espacio en el
  nombre (sana), un cambio de versión de la librería whatsmeow entre 0.1.1
  y master (idéntica). El hueco real que SÍ encontré: `Start()`/`New()`
  (`internal/whatsmeow/adapter.go`) nunca logueaban nada en el camino
  "sesión existente encontrada → Connect()" — indistinguible de "no se
  llamó" (el propio diagnóstico de Citrino). Dos `log.Printf` nuevos cierran
  la ambigüedad para el próximo smoke: `New()` dice si encontró una sesión
  previa (con el jid), `Start()` dice que va a conectar antes de intentarlo.
  Tests: `TestNewLogsNoExistingSession`/`TestNewLogsExistingSessionFound`.
  Hallazgo 3 (alarma de contraseña de fábrica en falso positivo): descartada
  la sospecha específica de Citrino (env `PIUMY_DASHBOARD_PASSWORD` de una
  instalación silenciosa interfiriendo) con test —
  `TestFactoryPasswordAlarmIgnoresEnvSeedWhenOwnerAlreadyChangedIt` prueba
  que la alarma y el login usan la MISMA comparación (`passHash`), no
  pueden discrepar por esta vía. Hallazgo 2 (`PIUMY_DEFAULT_TERMINAL_ID`
  vacío): NO codeado, dos propuestas a discutir con Citrino antes de tocar
  código (alarma en el tablero vs. auto-siembra desde el primer contacto,
  mismo patrón que T12). Detalle completo en
  `docs/T25-DIAGRAMA-SMOKE-WHATSAPP-SILENCIO.md`.
- S1a — dashboard: estilo terminal piumy.app + carita viva (`ct-2026-07-19-1517`,
  padre `ct-2026-07-19-1511` "release Piumy v1"): `internal/dashboard/web/` con
  la paleta del mockup aprobado, layout hero+cards, carita conectada al mood
  real (`GET /api/status` ahora expone `mood`/`queue`, ya existían en
  `state.Status`), buscador de conversaciones y tabla responsive en mobile.
  Cero endpoint de negocio tocado, cero funcionalidad existente rota.
- S1c — dashboard: antena por UI, pegar la línea completa (`ct-2026-07-19-1556`,
  padre `ct-2026-07-19-1511`): `POST /api/admin/capi-connector-line {line}`
  (`internal/restapi/admin.go`) parsea el string tal cual lo imprime
  `capi_credentials` y re-cablea en un paso — reusa `capiconn.ParseConnectorString`
  (factorizado de `internal/mcpserver` a `internal/capiconn` para no duplicar el
  parseo entre la tool MCP `set_capi_connector` y este endpoint) +
  `store.SetCAPIConnector`, mismo write path que el endpoint estructurado
  existente. Fuerza `http://127.0.0.1:<puerto>` siempre (la IP de LAN del string
  se descarta). Modal "Antena ⚡" del dashboard reescrito para calzar el mockup:
  un textarea + botón "Conectar", reemplazando el form de 3 campos sueltos +
  "Probar handshake" anterior.
- S1d — dashboard: auth (login admin/piumy + sesión + cambiar contraseña)
  (`ct-2026-07-19-1616`, padre `ct-2026-07-19-1511`) — SEGURIDAD. Archivo nuevo
  `internal/restapi/auth.go`: `POST /api/auth/login {username, password}`
  (usuario fijo `admin`, bcrypt contra `store.SettingDashPassHash` — se siembra
  con `"piumy"` si no hay hash guardado; mismo error para user/pass malos, sin
  enumeración) + cookie de sesión firmada HMAC (HttpOnly, SameSite=Strict) +
  `POST /api/admin/password` (valida la pass actual, guarda la nueva, rota el
  secreto de firma — invalida toda sesión existente, forzando re-login).
  `restapi.auth()` ahora acepta X-API-Key (sin cambios, sigue andando para
  MCP/curl) O una sesión válida — `APIKey==""` sigue 100% abierto, intacto
  (no se rompe el acceso programático que ya usaba Citrino). `/dashboard/`
  pasa a ser público (el shell no tiene secretos; es su JS el que muestra el
  login). `reset_dashboard_password` (MCP) también rota el secreto de sesión
  ahora — un reset de emergencia cierra sesiones de navegador existentes.
  Modal Config (cambiar contraseña) reintroducido, sacado a propósito en S1a.
  Verificado con playwright: login → dashboard → cambiar pass → sesión muere →
  pass vieja falla → pass nueva entra.
- S1e-1 — dashboard: recuperación de contraseña por WhatsApp (`ct-2026-07-19-1652`,
  padre `ct-2026-07-19-1511`) — SOLO WhatsApp, la vía correo es S1e-2 aparte.
  Archivo nuevo `internal/restapi/recover.go`: `POST /api/auth/recover
  {method:"whatsapp"}` genera un código de 6 dígitos (crypto/rand, hasheado con
  bcrypt, en memoria del proceso con TTL de 10 min — nunca persistido en claro)
  y lo encola (`store.Enqueue`, la MISMA cola que `send_message` — respeta
  governor/kill-switch/pacing, cero atajo al anti-ban) al self number
  (`state.OwnJID`, pelado del sufijo `:device` de whatsmeow con el helper nuevo
  `bareJID`) + a todos los chats `is_boss` (`store.BossJIDs`, método nuevo).
  Responde SIEMPRE el mismo mensaje, pase lo que pase adentro — sin estado que
  filtrar. Cooldown: un código activo por vez. `POST /api/auth/recover/verify
  {code, new_password}` valida (no vencido, no usado, bajo el tope de 10
  intentos — se quema si se excede) y resetea vía el mismo camino de S1d
  (`SetDashPassHash` + `RotateDashSessionSecret`, cierra toda sesión). Frontend:
  link "Recuperar por WhatsApp" en el login + overlay nuevo `#recovermodal`
  (código + nueva/repetir), cero CSS nuevo (todo ya portado en S1a/S1d).
  Verificado con playwright contra un harness descartable con una ruta
  debug-only (nunca en producción) que simula la entrega leyendo el outbox.
- S1e-2 — dashboard: recuperación de contraseña por correo/SMTP
  (`ct-2026-07-19-1716`, padre `ct-2026-07-19-1511`) — cierra S1e (recuperación
  completa). Reusa TODO el flujo de S1e-1 (código, hash en memoria, TTL, tope
  de intentos, cooldown, verify, rotar secreto); solo agrega el canal.
  `Deps.SMTP` (env `PIUMY_SMTP_HOST/PORT/USER/PASS/FROM`, default puerto 587
  STARTTLS) + `Deps.SMTPSend` (seam mockeable en tests, cae a
  `net/smtp.SendMail` real). `tryStartRecovery` refactorizado a
  `deliver(code)` callback — `handleRecover` ahora switchea
  `method:"whatsapp"|"email"`, cooldown compartido entre canales (el código
  en memoria es solo el hash, no hay plaintext que reenviar por el otro
  canal). `GET/POST /api/admin/recovery-email` — nuevo KV
  `dashboard_recovery_email`, validación liviana (solo `@` + sin espacios).
  `handleRecoverVerify` sin cambios (no le importa qué canal entregó el
  código). Frontend: overlay de recover ahora elige vía (WhatsApp/correo, dos
  botones) en vez de auto-enviar por WhatsApp; campo de correo en el modal
  Config. Cero CSS nuevo. Verificado con playwright contra un harness
  descartable con SMTP mockeado.
- S1f — dashboard: el panel admin se activa recién tras vincular WhatsApp
  (`ct-2026-07-19-1735`, padre `ct-2026-07-19-1511`) — casi todo frontend,
  reusa `GET /api/status` (`connected`/`show_qr`), `GET /api/qr/image` y el
  overlay de QR existente. Las 3 cards del panel admin se envolvieron en
  `#adminpanel` (oculto por default); `applyLinkGate` (`app.js`) lo
  muestra/oculta según `s.connected`, y auto-abre/cierra `#qroverlay` en la
  dirección contraria — cambia CUÁNDO se muestra el overlay, no cómo. Carita
  chica estática agregada al overlay de QR (mismo patrón `.login-head` de
  S1d). Transición en vivo vía SSE: `clearErrorState`/`handleDisconnect`
  (`internal/whatsmeow/inbound.go`) publican `wa_connected`/
  `wa_disconnected` — `wa_connected` es evento nuevo, sin él la transición
  hubiera tardado hasta 15s (el poll) en vez de ser instantánea.
  Correcciones de backend mínimas encontradas auditando el contrato:
  `MOOD_FACES` no tenía entrada `"qr"` (ni la tuvo nunca el `KAOMOJI_CATALOG`
  original de Piumy — ahí es un path de render sin cara) y `main.go` nunca
  seteaba `Mood="qr"` al emitir un QR — ambas agregadas, con el reset
  correspondiente en `clearErrorState` para no quedar pegado en "qr" tras el
  primer vínculo. Cero CSS nuevo salvo una utilidad genérica `.hidden`.
  Verificado con playwright contra un harness descartable arrancando sin
  vincular, con 2 rutas debug-only que simulan escanear/desconectar.
- S1g — dashboard: organizar la lista, 1:1 arriba + grupos colapsables al
  fondo (`ct-2026-07-19-1801`, padre `ct-2026-07-19-1511`) — pedido fresco
  del boss (dictado confuso, lectura de Citrino marcada como corregible).
  Resuelve que el backfill de contactos y el scraping de miembros de grupo
  (~717 en la escala real) no se mezclen con los 1:1 reales en una lista de
  600+. `GET /api/chats` ahora clasifica cada fila con `type:
  "p2p"|"group"|"group_member"` (+ `group_jid` en `group_member`): p2p
  exige un mensaje real guardado (`store.ChatJIDsWithMessages`, nuevo) o
  `is_boss` (carve-out agregado para no esconder ese flag crítico); un
  contacto sin mensaje y sin ser grupo/boss queda excluido del todo (ruido);
  cada `group_members` row se proyecta como `group_member` salvo que su
  número ya haya ganado como p2p ("gana el 1:1", contrato literal); un
  miembro en varios grupos aparece bajo cada uno. `dashboardChatLimit=5000`
  reemplaza el límite efectivo de 20 que traía `ListChats(0)` — con 600+
  chats reales ese límite hacía la clasificación casi inútil.
  `store.ListAllGroupMembers` (una sola query, no N+1). Frontend: la tabla
  1:1 existente queda intacta; grupos se renderizan aparte (`#groupzone`)
  con cabecera colapsable + contador, colapsado por default y persistido
  por grupo en localStorage; la búsqueda atraviesa grupos colapsados
  (auto-expande el que tenga un match). Verificado con playwright: 1:1
  arriba sin ruido, grupos colapsados con contador correcto, toggle
  persiste tras reload, búsqueda expande el grupo correcto.
- S1b — dashboard: cablear el estado real (governor/backup/cifrado)
  (`ct-2026-07-19-1823`, padre `ct-2026-07-19-1511`) — **CIERRA EL
  DASHBOARD**. Los badges que S1a dejó honestamente sin dato falso ahora
  llevan dato real. `GET /api/status` suma `antenna_configured` (¿hay un
  endpoint del connector guardado? — reemplaza el criterio viejo del badge
  Antena, `agents > 0`, que en realidad medía otra cosa),
  `governor_rate_per_min`/`governor_killed` (`Governor.Max()`/`Killed()`,
  ya existían), `backup_messages`/`backup_group_members`/`backup_contacts`
  (`store.BackupCounts`, nuevo — 3 `COUNT(*)` livianos sin caché) y
  `backup_encrypted` (`Deps.Backup.Enabled()` — reusa
  `sessionbackup.Backuper` en vez de duplicar el chequeo de
  `PIUMY_BACKUP_KEY` como un bool aparte). Cada campo nil-safe por su
  cuenta — sin esa dependencia wireada, ese campo solo cae a su zero
  value, nunca 503 el endpoint entero. Frontend: 3 badges nuevos en la
  `.status-bar` (Governor/Backup/Cifrado), cero CSS nuevo. Verificado con
  playwright: los 5 badges con dato real de una, más una ruta debug-only
  que dispara el kill switch para confirmar que "Governor" pasa a
  "⛔ kill" en vivo, sin recargar.
- S2a — e-paper: portar el renderer de la carita (`ct-2026-07-19-1843`,
  padre `ct-2026-07-19-1511`) — arranca la Parte 2 del release (módulo
  e-paper). RESCATE, no reescritura: `adapters/display/` nuevo (Python,
  autocontenido, no toca el binario Go) con copia byte-a-byte fiel de
  Piumy (`adapters/display/`) — `render.py` (`KAOMOJI_CATALOG` 19 moods +
  motor de gaze de 3 tipos de ojo + QR fullscreen), `backend.py` (factory
  `get_backend()`), `file/` (backend de archivo, dev/CI sin hardware),
  `fonts/` (DejaVuSans + Bold bundleadas) y `requirements.txt`
  (Pillow + qrcode[pil]). Verificación: `python render.py <outdir>`
  genera las 19 caritas + QR — comparado con un render idéntico corrido
  contra la fuente Piumy, `diff -q` no encontró ninguna diferencia entre
  los 35 PNG de ambos lados. Decisión propia: NO se portaron
  `file/render.py`/`file/faces.py`/`file/display.png`/
  `file/requirements.txt` — código huérfano de una versión anterior a que
  existiera el `render.py` compartido, no referenciado por
  `backend.py::get_backend()` ni por nada más en el módulo; portarlo
  hubiera resucitado un renderer duplicado y confuso sin ningún llamador
  real. `NOTES.md` nuevo documenta cómo correr el self-check. Pendiente
  para S2b/S2c: `service.py` (loop) y `epaper/` (driver Waveshare) —
  fuera de este subcontrato.
- S2b — e-paper: service loop + contrato status.json/face.json
  (`ct-2026-07-19-1853`, padre `ct-2026-07-19-1511`) — rescate de
  `service.py` (copia byte-a-byte fiel): polea `status.json` por mtime,
  decide refresh full vs. parcial (flash solo al entrar/salir de
  `qr`/`error`/`sleeping`, opt-in vía `PIMYWA_EPAPER_FULL_REFRESH`),
  cadencia de animación dinámica FAST→SLOW ("sobre de atención"), y
  escribe el sidecar `face.json` con `pick_variant`/`variant_repr` de
  `render.py` (S2a) — cero catálogo duplicado. Verificación del contrato
  con el gateway: `internal/state/state.go` **ya** escribe `mood` en
  `status.json` (campo sin `omitempty`, siempre presente) vía
  `PIUMY_STATUS_PATH`, y `state.ValidMoods` ya tiene los 19 moods exactos
  del catálogo — **cero cambio Go necesario**, el gate estaba cherry-pickeado
  desde el principio. Probado en PC con el backend de archivo: cambiar el
  mood a mano en un `status.json` de prueba actualiza `face.json` + el PNG
  con la cara correcta en el siguiente poll (verificado con mood `idle`
  → `vip`, `(♥o♥)` correcto). Dos decisiones propias (marcadas en
  `NOTES.md`): (1) `face.json` NO se cablea de vuelta al gateway —
  opcional per contrato, el dashboard ya mapea mood→kaomoji en JS (S1a),
  sin consumidor real hoy para el string exacto de variante; (2) el
  módulo mantiene el prefijo de env vars `PIMYWA_*` heredado de Piumy
  (vs. `PIUMY_*` del resto del gateway) — no chocan en runtime (procesos
  separados) pero hay que alinear `PIMYWA_STATUS`/`PIUMY_STATUS_PATH`
  explícitamente al desplegar; queda para que Citrino decida si
  estandarizar. `.gitignore` suma `__pycache__/`/`*.pyc`. Pendiente para
  S2c: `epaper/` (driver Waveshare).
- S1g-fix — dashboard: MOSTRAR los contactos no-boss (`ct-2026-07-19-1905`,
  padre `ct-2026-07-19-1511`) — feedback del boss en vivo: la lista solo
  mostraba `is_boss` + grupos, faltaban los números no-boss. `handleChats`
  (`internal/restapi/read.go`) ya no excluye contactos no-grupo sin
  mensaje/sin `is_boss` — todo chat no-grupo es `"p2p"` ahora, con o sin
  mensaje. Cero cambio en el frontend: el sort default `recientes` (por
  `last_ts` descendente) ya ordena los sin-mensaje al final, y `timeAgo(0)`
  ya mostraba "sin mensajes". El segundo ítem del mismo subcontrato
  (scroll horizontal de la tabla) queda **pausado a pedido de Citrino** —
  viene un rediseño mayor a 3 tabs que probablemente cambia esta tabla,
  se retoma con el subcontrato nuevo.
- S2c — e-paper: driver Waveshare 2.13" V4 + estandarizar env a `PIUMY_*`
  (`ct-2026-07-19-1919`, padre `ct-2026-07-19-1511`) — rescate de
  `epaper/backend.py` de Piumy: `EPaperWaveshareBackend`/
  `_PanelController`, driver `epd2in13_V4` a mano con `spidev`+`gpiod` v2
  (sin la lib `waveshare_epd`), misma política de refresco pwnagotchi-style
  del resto del módulo. Defensivo (el punto clave del subcontrato): los
  imports de hardware están dentro del `__init__` de `_PanelController`,
  nunca a nivel de módulo, y `_try_init()` los envuelve en `try/except` —
  sin `spidev`/`gpiod` degrada a no-op con `warning`, nunca crashea.
  Verificado en esta PC (Windows, sin esas libs instaladas):
  `get_backend("epaper-waveshare")` importa y degrada OK, sin excepción.
  El test con panel físico real queda para S3 (Pi Zero 2), aparte. De
  paso, estandaricé los env vars de todo el módulo (`backend.py`,
  `service.py`, `file/backend.py`, `epaper/backend.py`) de `PIMYWA_*`
  (heredado de Piumy) a `PIUMY_*` — lo que S2b había dejado marcado
  pendiente. El path de `status.json` se renombró literalmente a
  `PIUMY_STATUS_PATH` (antes `PIMYWA_STATUS`), igual que el env del
  gateway Go, así un solo `EnvironmentFile` alimenta a los dos procesos
  (los DEFAULTS siguen sin alinear — sigue haciendo falta setear la
  variable al desplegar). `NOTES.md` suma la tabla completa de env vars +
  pinout GPIO de la Pi Zero 2 + pasos de instalación (apt/pip/systemd).
- **0.1.2 — instalador: el canal del agente queda listo de fábrica**
  (padre `ct-2026-08-05-0155`, tramos T1-T4). Hasta 0.1.1, un despacho
  cAPI real llegaba cifrado y ningún agente podía leerlo — la clave que
  lo descifra vive deliberadamente solo del lado del agente
  (`cmd/agentclient`), pero nada en el instalador la generaba, compilaba
  ni publicaba.
  - **T1** (`ct-2026-08-05-015542`) — `internal/agentconnect`: el gateway
    escribe `agent-connect.json` al arrancar, junto a `status.json`
    (mismo data dir, derivado de la config) — `mcp_url`/`rest_url`/
    `mcp_key`/`rest_key`, para que un agente descubra cómo hablarle sin
    parsear `run-piumy.bat`.
  - **T2** (`ct-2026-08-05-0205`) — `installer/windows/piumy.iss` genera
    la 4ta clave (`PIUMY_CAPI_KEY`) y empaqueta `agentclient.exe`
    (compilado desde `cmd/agentclient`) al lado de `Piumy.exe`;
    `agent-connect.json` suma `capi_key`/`agentclient_path`. Cifrado
    intacto, nunca opcional.
  - **T3** (`ct-2026-08-05-0225`) — `get_manual(role:"connect")`: la
    skill que le dice a un agente, paso a paso, cómo conectarse
    (`internal/mcpserver/manuals/connect/SKILL.md`).
  - **T4** (`ct-2026-08-05-0256`, este) — Inno Setup instalado en la
    máquina de build (`winget install JRSoftware.InnoSetup`, no estaba —
    T2 tuvo que simular con el `.bat` a mano). Bump de versión a 0.1.2
    (el contenido del paquete cambió: 4ta clave + `agentclient.exe`).
    `build-all.sh` → `dist/` (6 targets del gateway + `agentclient-
    windows-amd64.exe`, nuevo desde T2) → `ISCC piumy.iss` →
    `dist/Piumy-Setup-0.1.2.exe` (21.483.614 bytes). Verificado que
    empaqueta `agentclient.exe` sin ejecutar el instalador: el log del
    propio compilador lista `Compressing:
    ...\agentclient-windows-amd64.exe` como parte de este build exacto,
    y el tamaño del instalador creció 4.010.503 bytes (~3,82 MiB) sobre
    el 0.1.1 (que no lo incluía) — consistente con ese binario de 8,67 MB
    comprimido lzma2. Intenté además una inspección de terceros
    independiente del log de compilación (7-Zip, `innoextract`) — ninguna
    lee el formato de Inno Setup 6.7.3 todavía (7-Zip no trae códec Inno;
    `innoextract` 1.9 solo llega a Inno 6.0.5), documentado para quien lo
    retome. **El instalador no se ejecutó** — el boss tiene 0.1.1
    corriendo con su sesión de WhatsApp pareada y su base real;
    reinstalar es decisión suya.
  - **T6** (`ct-2026-08-05-0315`, BUG bloqueante encontrado por Citrino
    en el instalador de T4 antes de que se llegara a distribuir) —
    `CurStepChanged/ssPostInstall` generaba las 4 claves **siempre**, sin
    mirar si ya había una instalación: reinstalar sobre una instalación
    existente rotaba `PIUMY_BACKUP_KEY` (backups cifrados viejos
    ilegibles para siempre) y `PIUMY_MCP_KEY`/`PIUMY_REST_KEY` (todo lo
    ya cableado contra las viejas dejaba de andar). Le pasaba a
    cualquiera que actualizara, no solo al boss (que tiene 3 backups
    reales cifrados con la clave actual). Fix: antes de generar, si
    `{app}\run-piumy.bat` ya existe, se lee (`LoadStringsFromFile`) y
    cada clave presente se reusa tal cual (`KeyOrGenerate` +
    `ExistingBatKey`, nuevas en `piumy.iss`) — solo se genera la que
    falta (el caso real de actualizar una 0.1.1: las 3 viejas se
    conservan, `PIUMY_CAPI_KEY` se crea porque no estaba).
    `ponytail:` documentado en el propio `.iss` — parsear el `.bat` es
    la única fuente que tiene una 0.1.1 (`agent-connect.json` no existía
    ahí todavía), camino de upgrade: mover las 4 claves a un archivo de
    config propio. La contraseña del tablero se investigó aparte —
    **ya estaba bien** (`passHash` en `internal/restapi/auth.go` es
    seed-only, confirmado con el test existente
    `TestPassHashIgnoresEnvSeedWhenHashAlreadyExists`), no se tocó nada
    ahí. Verificado con un instalador de prueba aislado (mismas
    funciones `[Code]` copiadas y diffeadas contra `piumy.iss`, corrido
    con `/DIR` sobre carpetas propias, nunca sobre la instalación del
    boss) en tres escenarios: instalación completa con 4 claves
    conocidas → las 4 salen intactas; launcher estilo 0.1.1 (3 claves,
    sin `PIUMY_CAPI_KEY`) → las 3 se conservan y la 4ta aparece nueva
    (64 caracteres hex); instalación limpia → las 4 nuevas, con los
    largos correctos (16/24/32/32 bytes). `Piumy-Setup-0.1.2.exe`
    recompilado con el fix adentro — la versión no se mueve, 0.1.2
    todavía no se había distribuido.
  - **T7** (`ct-2026-08-05-1125`) — instalador real (no un espejo) contra
    una carpeta de prueba propia, previo a publicar en el release público
    de `chamilonster/Piumy`. Primer intento: bloqueante real con
    `/VERYSILENT` — la página de la clave corre su validación igual
    (`Values[]` vacíos) y aborta sin escribir nada; resuelto por T8.
    Retomado después: instalación real con puertos propios preexportados
    (`PIUMY_MCP_ADDR`/`PIUMY_REST_ADDR`, heredados por el `Exec()` del
    instalador) → `netstat` confirmó `LISTENING` real, `agent-connect.json`
    con los 6 campos coincidiendo con `run-piumy.bat`. Sembré un mensaje
    boss directo en el `piumy.db` recién creado, relancé el mismo
    `Piumy.exe` instalado con las mismas claves + variables de captura de
    despacho → `capipush: despacho OK ... nonce=3622` real, descifrado con
    el `agentclient.exe` INSTALADO (no una copia) y la `capi_key` de su
    propio `agent-connect.json` — nonce coincidente, texto exacto. Cierra
    el circuito completo antes de publicar.
  - **T8** (`ct-2026-08-05-1133`) — el defecto real detrás de los dos
    síntomas: la página de la clave solo contemplaba "un humano
    instalando por primera vez". El boss lo vio en pantalla al
    reinstalar (le pedía una clave que `passHash()` —seed-only, T6—
    después descarta) y T7 lo encontró desde el otro lado (sin camino
    desatendido). Un solo arreglo para los dos:
    - **Instalación existente → la página no aparece.** `ShouldSkipPage`
      nuevo, misma detección de T6 (`run-piumy.bat` ya presente). No hay
      clave que pedir, la que hay se conserva.
    - **Modo silencioso → la clave entra por `/DASHBOARDPASSWORD=`**
      (`{param:...}`, mecanismo nativo de Inno) en vez de validar una
      página que nadie ve. `/RECOVERYEMAIL=` opcional, mismo criterio.
    - **Silencioso, instalación limpia, sin parámetro → NO aborta**
      (corrección del boss sobre el diseño original, verbatim: "que por
      defecto la clave del instalador sea piumy si se hace instalacion
      silenciosa"): cae al mismo default de fábrica que ya usa
      `auth.go` (`dashboardDefaultPassword = "piumy"`) — no inventa una
      clave nueva, reusa la que YA existía como fallback del lado del
      gateway. Lo deja bien visible en el log de instalación (`Log`, no
      un `MsgBox` que en silencioso nadie lee) para quien despliegue
      muchas máquinas desatendidas. Precedencia verificada: parámetro
      primero, `"piumy"` solo como último recurso, nunca al revés.
    - **Parámetro presente pero inválido (< 4 caracteres) → sí aborta**
      (`RaiseException`, no `MsgBox`) — ahí no es "no me dieron nada",
      es "me dieron algo mal". Verificado con un arnés de prueba aparte
      (mismas funciones, borrado): `RaiseException` da exit code 1
      distinguible de éxito, y el mensaje SÍ queda en el log aun con
      `/SUPPRESSMSGBOXES`.
    - Instalación interactiva limpia: **sin cambios** — la rama
      `WizardSilent` es nueva, la validación original queda intacta
      debajo, sin tocar.
    Verificado con el instalador REAL (`Piumy-Setup-0.1.2.exe`, no un
    espejo), 4 casos, en carpetas propias, nunca sobre la instalación
    del boss: (1) silencioso+limpio+sin parámetro → instala con
    `"piumy"`, log con la línea de aviso, `agent-connect.json` con los 6
    campos; (2) silencioso+limpio+parámetro válido → instala con esa
    clave, sin la línea de aviso; (3) silencioso+limpio+parámetro
    inválido → exit code 1, nada instalado; (4) silencioso+actualización
    (launcher estilo 0.1.1 + `secrets/` con un archivo real adentro) →
    página saltada (sin línea de aviso), las 3 claves viejas intactas,
    la 4ta nueva, el archivo de `secrets/` sin tocar. `Piumy-Setup-
    0.1.2.exe` recompilado con el fix — versión sin mover, seguía sin
    publicarse.
  - **T10** (`ct-2026-08-05-1203`, pedido directo del boss: "compruebe si
    funciona el instalador y el dashbord" — T7 probó el instalador y el
    gateway, nadie había mirado el tablero) — instalación real,
    silenciosa, en carpeta propia, dejada VIVA (no una corrida y
    apagado) para revisión externa. Verificado por HTTP desde este lado
    (sin navegador — esa herramienta la tiene Citrino/el boss): `GET
    /dashboard/` → 200 HTML real; login con clave incorrecta → 401;
    login con la correcta → 200 + cookie de sesión; `GET /api/status`
    sin autenticar → 401, autenticado → 200. **Hallazgo real, no de la
    prueba**: al revisar el tablero recién instalado, el boss usó el
    único CTA visible (el botón de QR) y vinculó su WhatsApp REAL a la
    instalación de prueba — detectado por el log (`whatsmeow: connected`,
    2 grupos reales) al recompilar para T9, nunca provocado a propósito.
    Bajada la instancia de inmediato, sin borrar `whatsmeow.db` (borrar
    el archivo NO desvincula el dispositivo del lado de WhatsApp — deja
    un dispositivo fantasma; el boss lo desvincula desde el teléfono).
    Contraorden del boss después ("es un telefono de pruebas... hay
    reglas"): la instancia se relanzó y queda VIVA a propósito —
    `POST /api/admin/kill {"kill":true}` activado y verificado en dos
    capas de código real (`corepipeline/outbox.go` + `whatsmeow/
    adapter.go`, no un flag decorativo), para servir de banco de pruebas
    con sesión/grupos/contactos reales sin poder mandar nada. **Hallazgo
    de producto anotado, no corregido acá**: nada en el dashboard avisa
    antes de vincular un WhatsApp real a una instalación de prueba.
  - **T9** (`ct-2026-08-05-1137`, boss verbatim: "si la contrase[ñ]a es
    piumy, mostrar un mensaje que diga, cambiar contraseña por defecto
    como alarma en el dashboard, y que lo lleve a la zona de opciones
    dode se cambia la contraseña") — T8 deja una instalación silenciosa
    sin parámetro en `piumy`, una clave pública (está en el instalador y
    en el código abierto). `isFactoryPassword(st)` (`auth.go`) reusa
    `passHash` (una sola fuente para el hash actual — dos formas de
    llegar al mismo default es cómo se desincronizan, criterio de
    Citrino en T8) + `bcrypt.CompareHashAndPassword` contra
    `dashboardDefaultPassword`, 100% server-side — nunca viaja una
    contraseña, solo un booleano nuevo (`factory_password`) en `GET
    /api/status`. Frontend: `#factorypwalert`, una barra FUERA de
    `#adminpanel` a propósito (visible aunque WhatsApp no esté
    vinculado — la ventana de mayor riesgo es la instalación recién
    hecha). `openConfigModal` factorizada del handler de `configbtn` —
    el botón de la alarma abre el MISMO modal de config existente,
    enfocado en la contraseña, sin pantalla nueva. Desaparece sola: el
    `toggle("hidden", ...)` corre en cada poll de `loadStatus` (15s) y
    en el re-login forzado que ya dispara un cambio de contraseña
    (`RotateDashSessionSecret` mata la sesión, `showLogin()` +
    `submitLogin()` vuelven a pedir el status) — sin lógica de
    desaparición aparte. Tests nuevos: factory=true en un store fresco,
    false tras cambiar la clave, false con `PIUMY_DASHBOARD_PASSWORD`
    seedeado (instalación silenciosa CON parámetro). Verificado además
    por HTTP contra una instancia real aislada (sin parear WhatsApp —
    la alarma no lo necesita): login con "piumy" → `factory_password:
    true`; cambio de clave → sesión vieja muerta, clave vieja
    rechazada, clave nueva acepta con `factory_password:false`.
- **T12** (`ct-2026-08-05-1231`, padre `ct-2026-08-05-0155`, boss verbatim:
  "un problema, el selfnumber no se auto define como boss automaticamente")
  — el número con el que se vincula WhatsApp ahora se marca dueño solo, sin
  que nadie toque el tablero. `whatsmeow.recordOwnIdentity` (ya existía,
  corre en cada `*events.Connected`) ahora también llama `markOwner(jid,
  name)`: `TouchChat` (garantiza la fila con los defaults de un chat
  individual normal — evita el default crudo `confirmation_mode='always'`
  de una fila insertada desde cero, que habría dejado cada respuesta al
  dueño esperando su propia aprobación) + `store.MarkOwnerIfUntouched(jid)`.
  Columna nueva `chats.is_boss_touched` (propia, independiente de
  `config_level_source`: rastrea SOLO si `is_boss` ya fue decidido, para
  que un cambio de `active`/`confirmation_mode` ajeno no lo cuente como
  "decidido") — `SetIsBoss` la marca en 1 en cada llamada (cualquier
  dirección), así que si el dueño se desmarca a mano, una reconexión
  posterior NUNCA se lo vuelve a poner. Migración atómica propia
  (`migrateIsBossTouched`, backfillea todas las filas preexistentes a 1 en
  la misma transacción — mismo patrón que `migrateConfirmationMode`): un
  `DEFAULT 1` plano en el `ALTER` habría envenenado también las filas
  NUEVAS (`TouchChat` nunca nombra la columna), dejando el auto-marcado
  permanentemente inerte. Ningún otro número/chat se toca — solo
  `state.OwnJID`, tal como pide el contrato ("no inventes heurísticas").
  Tests: instalación limpia marca dueño, reconexión tras desmarcado manual
  lo deja desmarcado, ningún otro chat cambia (`internal/store/chat_test.go`,
  `internal/whatsmeow/inbound_test.go`).
- **T21** (`ct-2026-08-05-1308`, CRITICAL, bloqueaba la publicación —
  revisión independiente de Amatista R2) — el instalador de Windows ya no
  puede pisar claves que no pudo leer con certeza. `ExistingBatKey`/
  `KeyOrGenerate` (T6) exigían un match exacto de `set VAR=`; si fallaba
  (el caso real: el `.bat` abierto en el Bloc de notas y guardado como
  "Unicode"), generaba una clave nueva en silencio y pisaba el launcher
  viejo — los backups cifrados con la clave anterior quedaban ilegibles
  para siempre. Regla fijada por Citrino: "ante la duda, no se pisa nada".
  - `ResolveLauncherKeys` reescrito: lee bytes crudos (`LoadStringFromFile`
    como `AnsiString`, nunca `String` — evita que una conversión Unicode
    implícita se coma la marca de orden de bytes antes de poder
    detectarla), aborta si detecta UTF-16 LE/BE o UTF-8+BOM, parser
    tolerante propio (`SET` mayúscula o minúscula, comillas, espacios
    alrededor del `=`, última coincidencia gana en vez de la primera).
    Exige las 3 claves base (MCP/REST/BACKUP); si falta cualquiera de esas
    tres, aborta sin completar lo que falta — CapiKey ausente es la única
    excepción legítima conocida (launcher 0.1.1) y ahí sí se genera.
  - **Rediseño, no parche, tras probarlo en vivo:** la primera versión
    llamaba `RaiseException` desde `ssPostInstall` — colgaba para siempre
    en cualquier aborto real (el log mostraba "Installation process
    succeeded" ANTES de la excepción: los archivos ya estaban copiados, y
    sin `/SUPPRESSMSGBOXES` funcionando de verdad — roto por un bug de
    mangling de paths de Git Bash en las pruebas — el diálogo de error
    interno de Inno esperaba un click que nunca llega). Fix real: la
    resolución de claves se movió a `PrepareToInstall` — el hook de Inno
    que corre ANTES de copiar un solo archivo; devolver un string no vacío
    ahí cancela la instalación entera, exit code distinto de cero, sin
    dejar nada a medio escribir.
  - `PIUMY_REST_ADDR` (agregado durante las pruebas, ver abajo): si el
    launcher anterior lo tenía a mano, se preserva en el nuevo — antes se
    perdía en silencio en cada reinstalación.
  - Secundarios del mismo archivo: desinstalación con default "No" en vez
    de "Sí" (`MB_DEFBUTTON2`); `AppMutex=PiumyGatewaySingleInstanceMutex`
    en `[Setup]`, creado por el binario mismo (`appmutex_windows.go`,
    `CreateMutexW`, apenas entra a `main()`) — sin esto, reinstalar/
    desinstalar con Piumy corriendo dejaba el ejecutable viejo corriendo
    emparejado con el launcher nuevo.
  - **Agregado en el camino** (Citrino, detectado al ver el efecto real
    durante las pruebas silenciosas de este mismo cambio: le abrían
    pestañas del tablero al boss): `LaunchFirstRunAndOpenDashboard` abría
    `http://127.0.0.1:8092/dashboard/` con el puerto escrito a mano — con
    otro `PIUMY_REST_ADDR` abría el tablero de otra instalación. Fix: usa
    el puerto real (preservado o default) y **no abre nada en modo
    silencioso** (`WizardSilent` corta el `ShellExecAsOriginalUser`; el
    `Exec` que arranca la app sigue igual).
  - Probado en vivo contra el instalador real compilado
    (`//VERYSILENT //SUPPRESSMSGBOXES`): UTF-16/truncado/bloqueado
    abortan con exit code distinto de cero y CERO archivos copiados;
    upgrade normal 0.1.1 (3 claves) y 0.1.2 (4 claves) intacto; duplicados
    resuelven a la última línea; puerto custom preservado y confirmado
    escuchando ahí; reinstalar/desinstalar con la app corriendo aborta
    (exit 1) sin tocar el binario. Detalle completo en
    `docs/T21-DIAGRAMA-INSTALADOR-KEY-SAFETY.md`.
  - `go build ./... && go vet ./... && go test ./...` verde. Instalador
    recompilado (`dist/Piumy-Setup-0.1.2.exe`, ISCC 6.7.3).
- **T20** (`ct-2026-08-05-1301`, revisión independiente de Amatista R1,
  sobre el merge de T5) — el contador de saturación de `capipush`
  (`store.CountRecentPendingNonBoss`) quedó ciego a los chats en modo
  `auto`. T5 (ct-2026-08-05-0311) ensanchó `PendingDedicated`/
  `CountPendingDedicated` a `mode IN ('dedicated','auto')` para que esos
  chats DESPACHEN, pero `CountRecentPendingNonBoss` — una TERCERA query
  con el mismo filtro, no nombrada en el pedido original de T5 — se quedó
  en `mode = 'dedicated'`. Efecto: una avalancha de chats `auto` nunca
  tripeaba el freno de backpressure, aunque sí seguía despachando sin
  frenarse. No abría la salida (los candados de `send.go` intactos,
  verificado a fondo por Amatista) — el semáforo mentía por omisión.
  Fix: `mode IN ('dedicated', 'auto')`, en lockstep con las otras dos.
  Auditoría pedida por Citrino ("qué otras consultas filtran por mode y
  por qué cada una está bien así"): exactamente 3 queries SQL en
  `internal/store` filtran por `mode` (las 3 en `pending.go` — ninguna
  cuarta) + un filtro en Go, no SQL, en `internal/autoreply/worker.go`
  (`p.Mode != "auto"`, correcto por diseño: ese worker es el bridge
  legacy específico de chats `auto`, opuesto en propósito al despacho
  dedicated+auto de `capipush`). Detalle completo en el reporte a
  Citrino. Test: un chat `auto` pendiente por sí solo cruza `SwampedAt` y
  frena a otro chat (`internal/capipush/capipush_test.go`,
  `TestBackpressureCountsAutoModeChats`) + test directo del contador
  (`internal/store/pending_test.go`,
  `TestCountRecentPendingNonBossIncludesAutoMode`).
  `go build ./... && go vet ./... && go test ./...` verde.
- **T22** (`ct-2026-08-05-1455`, bloqueaba la publicación — tercera
  revisión de Amatista R3, sobre T21) — dos huecos más en el instalador,
  uno en el parser, uno en la escritura.
  - **Comillas alrededor del valor:** `ParseSetLine` sacaba las comillas
    del valor (`set VAR="x"` → reusaba `x` sin comillas). cmd NO las saca
    ahí — solo cuando envuelven TODO `VAR=valor` (`set "VAR=x"`).
    Verificado ejecutando cmd de verdad (Amatista, re-verificado acá
    independiente). El caso real: alguien edita el `.bat` con
    `set PIUMY_BACKUP_KEY="miclave"` — la app cifra con `"miclave"`
    (comillas incluidas), el parser viejo reusaba `miclave` sin comillas
    (una clave DISTINTA) sin abortar, porque "se encontraba". Fix,
    dirección de Citrino: replicar cmd exacto, no interpretar — se borró
    el despojo de comillas del valor.
  - **Escritura del launcher sin verificar:** `SaveStringToFile` no
    chequeaba su resultado — un antivirus bloqueando la creación del
    `.bat` (actor real y frecuente) dejaba la instalación "exitosa" con
    la app arrancando igual, las 4 claves solo en el entorno del proceso.
    Un backup generado en esa corrida quedaba cifrado con una clave que
    muere al cerrar — la misma pérdida de R2 (T21), entrando por la
    escritura. Fix: se chequea el resultado; si falla, la app NO arranca
    y se avisa.
  - **Encontrado probando el fix de arriba:** mi primer intento avisaba
    con un `MsgBox()` incondicional — colgó el instalador para siempre en
    modo silencioso (verificado en vivo con un arnés aislado). A
    diferencia del diálogo interno de Inno por una excepción no atrapada
    (T21, sí cubierto por `/SUPPRESSMSGBOXES`), un `MsgBox()` propio
    llamado desde `[Code]` NO se auto-responde con esa flag. Mismo patrón
    que `NextButtonClick` (T8) ya usaba: `MsgBox` solo si `not
    WizardSilent`; el `Log` (incondicional) es lo único que sobrevive en
    silencioso.
  - Menores: `GenerateRandomHex` propaga error como string en vez de
    `RaiseException` (corre dentro de `PrepareToInstall` desde T21, nunca
    se había probado desde ahí); URL del tablero con `PIUMY_REST_ADDR` en
    forma `host:puerto` (no `:puerto`) ya no antepone `127.0.0.1` dos
    veces.
  - Probado en vivo: las dos formas de comillas reusan el valor idéntico
    al real; escritura bloqueada no cuelga, no arranca la app, avisa;
    tabla de T21 repasada por spot-check (limpia, UTF-16, truncado,
    `AppMutex`) — sigue verde. Instalador recompilado
    (`dist/Piumy-Setup-0.1.2.exe`, ISCC 6.7.3). Detalle completo en
    `docs/T21-DIAGRAMA-INSTALADOR-KEY-SAFETY.md` (sección T22).
- **T23** (`ct-2026-08-05-1615`, lo último que bloqueaba publicar — cuarta
  revisión de Amatista, R4) — el mismo patrón que T22 arregló en la
  escritura del launcher (`MsgBox()` propio, no cubierto por
  `/SUPPRESSMSGBOXES`, cuelga una instalación desatendida para siempre)
  quedó vivo en otros dos lugares del mismo archivo.
  - **`Exec` falla al arrancar Piumy.exe** (línea 649, 12 líneas antes del
    corte por silencioso): antivirus/SmartScreen bloqueando un ejecutable
    nuevo sin firma es igual o más probable que bloqueando la creación
    del `.bat`. Las claves ya están escritas (estado recuperable), pero
    el `MsgBox` incondicional colgaba la máquina en una instalación
    "zombie" para siempre. Fix: mismo patrón — `Log` siempre, `MsgBox`
    solo si `not WizardSilent`.
  - **Confirmación de borrar `secrets\` al desinstalar** (línea 804):
    distinto de los otros dos — es un Sí/No que decide algo irreversible,
    no un aviso informativo. En silencioso, `UninstallSilent` salta
    directo al mismo default que ya tenía el diálogo interactivo
    (`MB_DEFBUTTON2` = No) y lo registra — un retiro desatendido de
    varias máquinas nunca pierde datos por un click que nadie dio.
  - **Barrido completo de la superficie**, no solo los dos señalados:
    enumerados los 6 `MsgBox`/`RaiseException` reales del archivo — los 3
    restantes (línea 572 `RaiseException` ya verificado por T8; líneas
    582/587 inalcanzables en silencioso por construcción) confirmados
    seguros, ninguno más en el archivo (grepeado `external`/
    `SuppressibleMsgBox`/`TaskDialogMsgBox`/`Confirm(` — nada nuevo).
  - Probado en vivo: `Exec` fallando con arnés aislado (mismo patrón
    exacto, sin cuelgue, log escrito) y desinstalación silenciosa real
    con `secrets\` presente (`unins000.exe //VERYSILENT
    //SUPPRESSMSGBOXES` — exit 0, sin cuelgue, `secrets\` intacta).
    Instalador recompilado (`dist/Piumy-Setup-0.1.2.exe`, ISCC 6.7.3).
    Detalle en `docs/T21-DIAGRAMA-INSTALADOR-KEY-SAFETY.md` (sección T23).
- **T13** (`ct-2026-08-05-123147`, urgente — sin esto una instalación
  limpia nace muda) — pestaña Rules en el tablero + campo `identity` +
  las 4 reglas de fábrica, boss verbatim ("apruebo las 4 reglas").
  - **El defecto:** las 4 reglas (`rules_default`/`rules_type_group`/
    `rules_default_contact`/`rules_default_new_number`) están vacías por
    default y `EffectiveRules`'s gate duro ("sin reglas efectivas, la IA
    nunca actúa") las deja SIN NADA — verificado por API contra la
    instalación real del boss, las 4 vacías. Una instalación limpia recibe
    todo y no contesta nunca.
  - **`store.SeedFactoryRulesIfUnset()`** (`rules_seed.go`) siembra
    `identity` + las 4, con su texto de fábrica EXACTO (aprobado por el
    boss, no parafraseado), SOLO para las claves que nunca se escribieron
    — `store.KVExists(key)`, nuevo, distingue "nunca se escribió" de
    "se escribió vacío a propósito" (`KVGet` sola no puede: devuelve `""`
    para ambos casos). Llamada una vez al arrancar (`main.go`), cubre
    tanto una instalación limpia como una ya corriendo que actualiza (la
    del boss).
  - **`identity`** (nuevo campo, `SettingIdentity`): "asistente de qué"
    (boss verbatim: "una empresa de x cosa, una persona ocupada, etc."),
    arriba de las 4 reglas porque las gobierna a todas. Mismo patrón CRUD
    que las 4 — `GET/POST /api/admin/identity`.
  - **Pestaña Rules**: los 4 campos que vivían sueltos arriba de las
    pestañas del tablero (M5, ct-2026-07-22-1903) se mudaron a una
    pestaña propia junto a Chats/Grupos/Contactos/Agentes, `identity`
    arriba de todos. Guardar confirma con "✓ Guardado." sin recargar la
    página (`flashSaveResult`, mismo patrón que `config_email_save` ya
    usaba). Los selectores de nivel de "Mensajes nuevos"/"Contactos"
    (M5) se mudaron junto con su regla — siguen siendo el mismo par
    modo+reglas, no se separaron.
  - **Agregado de Citrino** (nota de regla 4 de T12): el manual del
    orquestador (`internal/mcpserver/manuals/orchestrator/SKILL.md`)
    decía en el paso 3 del setup que había que marcar al dueño a mano
    "sin esto sus propios mensajes no llegan a ningún agente y el sistema
    no avisa" — ya no es cierto para el número que parea WhatsApp (T12).
    Corregido: el que parea queda marcado solo; el paso 3 ahora es
    específicamente para UN SEGUNDO número personal del dueño, que sigue
    siendo manual a propósito (`perillas.md` también actualizado con la
    misma aclaración).
  - Probado: instalación limpia siembra las 5 con su texto de fábrica
    (verificado por API contra una instancia real, verbatim exacto);
    campo vaciado a propósito + reinicio → sigue vacío, no se resiembra;
    campo escrito por el dueño + reinicio → intacto; campo nunca tocado +
    reinicio → sigue con el de fábrica. HTML servido validado con un
    parser real (`html.parser`, sin mismatches de tags) — el navegador
    Chrome no estaba disponible en la sesión para el click-through visual.
  - `go build ./... && go vet ./... && go test ./...` verde.
- **T11** (`ct-2026-08-05-1214`, último de los que bloqueaban publicar —
  boss verbatim: "se nota que se ejecuta un bat o algo, se puede hacer eso
  silencioso?") — la pantalla negra que veía el boss al arrancar Piumy
  desde el menú inicio. Diagnóstico por eliminación (ya venía en el
  contrato, verificado): `Piumy.exe` es subsystem GUI (no abre consola
  sola), los accesos directos apuntaban a un `.vbs` que corría
  `run-piumy.bat` con ventana oculta — en Windows 11 el host de consola
  por defecto no siempre respeta ese pedido. La causa de fondo no era el
  parpadeo: había un script de shell en el camino de arranque, solo para
  setear variables de entorno — y un `.bat` de Windows no viaja a
  Linux/Mac/Raspberry.
  - **`piumy-config.json`**, archivo de configuración nuevo de ENTRADA
    (el gateway lo LEE al arrancar) — distinto a propósito de
    `agent-connect.json` (SALIDA: el gateway lo escribe para que un
    agente lo lea). `config.ApplyFileDefaults()` (`internal/config/
    filedefaults.go`) corre en `main.go` ANTES de `config.Load()`:
    rellena los `PIUMY_*` que NO estén ya seteados — la variable de
    entorno gana SIEMPRE, así que dev/`rl.bat`/tests no cambian en nada.
  - **Migración sin pérdida de claves**: si `piumy-config.json` no existe
    pero sí un `run-piumy.bat` viejo, se migra — mismo parser tolerante
    que T21/T22 escribieron para el instalador (mayús/minús, comillas sin
    despojar del valor, última coincidencia gana), portado a Go. Exige
    las 3 claves base (MCP/REST/BACKUP) o aborta sin escribir nada; `CapiKey`
    ausente se genera con `crypto/rand`.
  - **El instalador** (`piumy.iss`, `ResolveKeys`, renombrada de
    `ResolveLauncherKeys`) ahora prueba 3 fuentes en orden:
    `piumy-config.json` (ya actualizó a T11) → `run-piumy.bat` (vieja, sin
    actualizar) → generar las 4 (limpia) — las dos fuentes existentes
    comparten `FinalizeKeys` para la validación. `CurStepChanged` escribe
    `piumy-config.json` con el mismo patrón de escritura-verificada de
    T22/T23. Los accesos directos (`[Icons]`) apuntan a `Piumy.exe`
    DIRECTO — el `.vbs` deja de generarse, ya no tiene propósito. Un
    `.bat` viejo se deja intacto, sin volver a escribirlo (convenience de
    arranque manual, fuera del camino normal). Versión del paquete
    0.1.2 → 0.1.3 (cambia qué instala/cómo arranca, mismo criterio que
    T4/T2).
  - **Deuda pagada**: T6 había dejado un `ponytail:` anotando "el camino
    de upgrade es mover las 4 claves a un archivo de config propio" —
    borrado de `piumy.iss`, ya no es una nota, es lo que hay.
  - Probado en vivo contra el instalador real: instalación limpia (sin
    `.bat`/`.vbs`, acceso directo real leído con `WScript.Shell` apunta a
    `Piumy.exe`, la app responde por REST usando solo el archivo);
    migración desde un `.bat` de 4 claves (exactas, `.bat` viejo intacto);
    `piumy-config.json` ya existente gana sobre un `.bat` con otro valor
    (nunca se re-migra); BOM/truncado en `piumy-config.json` abortan
    (exit 7, cero archivos); escritura bloqueada no cuelga ni arranca la
    app; `PIUMY_DB_PATH` como variable de entorno real gana sobre el
    archivo. 10 tests de Go (`internal/config/filedefaults_test.go`).
    Detalle completo en `docs/T11-DIAGRAMA-CONFIG-FILE-NO-BAT.md`.
  - `go build ./... && go vet ./... && go test ./...` verde.
- **T24** (`ct-2026-08-05-1740`, CRÍTICO — bloqueaba publicar) — el smoke
  en la máquina real del boss (instalador 0.1.3 sobre su 0.1.1) pidió la
  clave del tablero de nuevo en una actualización real. Canceló a tiempo,
  cero archivos tocados — el aborto limpio de T21/T22/T23 funcionó — pero
  el síntoma en sí era un defecto nuevo.
  - **La causa**: `ExistingInstallDetected` confía en la carpeta de
    instalación que Inno resuelve solo — vía el AppId en el registro si lo
    reconoce, o el directorio por defecto si no. Si esa entrada de
    registro falta o quedó desactualizada (cualquier motivo, no hace falta
    saber cuál) la carpeta cae al default de siempre, y si la instalación
    real del usuario NO está ahí, todo lo que depende de esa carpeta falla
    en silencio: pide la clave de nuevo Y genera 4 claves nuevas sobre una
    instalación que ya tenía las suyas. Reproducido en aislamiento: sin
    registro + `run-piumy.bat` real en otra carpeta → exactamente ese
    desastre.
  - **El fix**: el acceso directo del menú inicio (`[Icons]`, creado
    SIEMPRE, sin `Tasks:`) sobrevive a que el registro se pierda — para
    haber arrancado la app alguna vez de verdad, tuvo que apuntar a la
    carpeta REAL. Leerlo con `WScript.Shell` en `InitializeWizard`
    (`PreviousInstallDirFromShortcut`, mismo mecanismo que T11 ya usó para
    VERIFICAR el `.lnk`, ahora para LEERLO) y, si la carpeta que Inno
    precargó no tiene instalación pero la del acceso directo sí, forzar
    `WizardForm.DirEdit.Text` a esa — de ahí en más `ExistingInstallDetected`/
    `ResolveKeys`/`PrepareToInstall` ven la carpeta correcta sin tocarlos.
  - Encontrado haciendo el fix: la constante de directorio no se puede
    expandir todavía dentro de `InitializeWizard` (error interno, probado
    en carne propia) — hay que leer `WizardForm.DirEdit.Text` directo, no
    la constante, en ese punto del ciclo de vida.
  - Probado en vivo (instalador de prueba aislado, tres escenarios):
    registro-reconoce-correctamente sigue igual; sin registro con la
    instalación real en otra carpeta, ahora reusa las 4 claves sin pedir
    nada (antes: pedía la clave y generaba 4 nuevas); instalación limpia
    real sigue generando 4 claves, sin falso positivo. Instalador
    recompilado (`dist/Piumy-Setup-0.1.3.exe`, ISCC 6.7.3) desde el
    archivo real. Detalle completo en
    `docs/T21-DIAGRAMA-INSTALADOR-KEY-SAFETY.md` (sección T24).
  - **Agregado (boss verbatim: "tiene que quedar comentado, para cuando se
    compile para mac y linux se resuelva distinto")**: comentario en
    `piumy.iss`, junto a `PreviousInstallDirFromShortcut`, deja explícito
    que el mecanismo (`WScript.Shell`, un `.lnk`) es Windows puro — lo que
    cruza a otra plataforma es el PROBLEMA (encontrar una instalación
    previa cuando el mecanismo del sistema no la reporta), no este
    mecanismo concreto.

---

## 2026-07-19 — Deploy: backup completo de WhatsApp

### Added
- Schema para backup completo de WhatsApp (`ct-2026-07-19-0102`, backup Sub 1 — SOLO
  schema, sin poblar): tabla `group_members` (miembros de grupo con nombre, para el
  scraping del boss) + columna `chats.contact_name` (nombre de agenda del teléfono,
  distinto de `chats.name`). `media.ts` verificado, ya existía.
- Backfill anti-ban de CONTACTOS (`ct-2026-07-19-0115`, backup Sub 2a — cherry-pick
  de Piumy `gateway/sync.go`, solo contactos, grupos van en el Sub 2b): al conectar
  y cada 6h, recorre todos los contactos conocidos por whatsmeow, paceado con
  `governor.DelayWindow` (mismo mecanismo anti-ban que dispatch/read) antes de cada
  contacto — nunca un volcado masivo. Guarda el nombre de AGENDA del teléfono
  (`info.FullName`/`FirstName`, nunca `PushName`) en `chats.contact_name`
  (`SetContactName`, Sub 1), sin pisar un nombre ya conocido con uno vacío.
- Scraping de MIEMBROS de grupos (`ct-2026-07-19-0138`, backup Sub 2b — extiende
  `seedGroups`): por cada grupo ya conocido al conectar, guarda sus participantes
  (número + nombre si viene) en `group_members` (`UpsertGroupMember`, Sub 1) y su
  topic en `chats.description`. Store-only, sin delay anti-ban — los participantes
  ya vienen con `GetJoinedGroups`, sin llamada extra al servidor.
- HistorySync — persistir el historial reciente que WhatsApp empuja al vincular
  (`ct-2026-07-19-0148`, backup Sub 3 — patrón oficial de whatsmeow
  `ParseWebMessage`, sin referencia de Piumy): pasivo, sin delay anti-ban.
  Escribe directo al store (texto + metadata, `Type`), nunca al canal de inbound
  — el historial no se despacha al agente. A diferencia del mensaje en vivo:
  guarda también los mensajes PROPIOS viejos (`IsFromMe` no se filtra) y NUNCA
  baja media histórica (evita flood anti-ban con miles de mensajes) — media
  histórica queda diferida a un sub futuro.

---

## 2026-07-18 (tarde) — Deploy: rediseño del formato + tool de re-cableo

### Changed
- Formato del dispatch cAPI rediseñado (`ct-2026-07-18-1851`, commit `5ae18df`): `numero` y
  `nivel` (boss/caution/danger, el nivel real del sistema) suben al header del envelope que
  CleverCoder renderiza — `app: piumy|whatsapp` / `de: <numero>, <nivel>`. El body queda solo con
  el texto (+ bloque rules.md para no-boss). Nonce recortado a 4 hex como firma `NC:<hex>`, único
  entre dispatches activos (`Gate.NonceActive`). Reemplaza `piumy:whatsapp:(numero)(is_boss)(Mensaje:…)`.

### Added
- Tool MCP `set_capi_connector` (boss-only, `ct-2026-07-18-185047`, commit `ff1a875`): re-cablea la
  antena cAPI en 1 paso desde el string pegado (`ip:puerto chat_id pin`), forzando `127.0.0.1`,
  reusando el write-path del POST admin (`store.SetCAPIConnector`).

---

## 2026-07-18 — Deploy: formato compacto + reversión de la unificación

### Added
- Formato de dispatch compacto en modo plaintext: `piumy:whatsapp:(numero)(is_boss)(Mensaje:…)`
  + nonce, reemplaza el envelope JSON `{nonce,chat_jid,level,messages}`. (ct-2026-07-18-1416)
- Candado `initiateAuthorized`: el agente solo inicia conversación a chats `is_boss` o
  `active`+rules. Cierra el vector de iniciación no autorizada. (ct-2026-07-18-1438)

### Removed
- Revertida la "identidad unificada" (F1 nombres + F2 resolución `@lid→número` + reconcile):
  el boss la canceló ("separados por número está bien"). Los chats siguen keyed por `@lid`.
  Se conservan `store.IsLIDJID` + `whatsmeow.Adapter.ResolvePN` (los usa el formato compacto).

### Fixed
- `TestControllerStopWaitsForInFlightOutboxSend`: sincronización determinística del test (no un
  fix de `Stop()`).

---

## Hitos previos (resumen)

### 2026-07-14 — Media inbound
- Bajada y persistencia de media inbound (fotos/audios/stickers) en el adapter whatsmeow +
  marcador de media en el dispatch.

### 2026-07-11 — Pivote a whatsmeow (ST-E) + hardening del gateway
- **Pivote de arquitectura:** de open-wa (cliente Node externo) a **whatsmeow** (librería Go
  embebida, `CGO_ENABLED=0`, QR en el mismo proceso). Se borró `internal/openwa`. Un solo binario.
- Gate boss no residual (exige `Ready` para `LevelBoss`), flood-guard por mensaje, metering real +
  governor anti-ban honesto, modo/estado del owner persistente.

### 2026-07-10 — Smoke real + hardening post-auditoría + dashboard
- Flujo entrante WhatsApp → pipeline → despacho y camino de vuelta (agente → `send_message` →
  WhatsApp) validados en sesión real. 5 fixes graves (anti-ban + seguridad + liveness).
  Dashboard del gateway (webview + tray Windows). Injector real cAPI (CleverInjector).

### 2026-07-09 — MVP F0→F5
- F0 esqueleto Go → F1 store/infra/routing/guard → F2 interfaz `Gateway` + corepipeline →
  F3 adaptador → F4 mcpserver (23 tools MCP, Bearer, gate por nivel, cAPI, media, metering,
  confirmation_mode, DB-admin boss-only) → F5 wiring en `main.go` + MCP sobre HTTP + config.

---
