# Patrones de datos personales, compartidos por pre-commit y commit-msg
# (T114, ct-2026-09-01-1905) — una sola fuente de verdad para "qué es un
# dato personal" en vez de mantener los mismos regex duplicados en los dos
# hooks. `. este/archivo.sh` desde sh; no ejecutable por sí solo.
#
# Ver constitution.md §2b para el porqué y la historia (un archivo con 559
# personas quedó versionado una vez).
#
# 555 SIEMPRE pasa (número inventado, la convención del proyecto) — cada
# patrón lo excluye con un negative lookahead. Un ejemplo/test escrito con
# el prefijo 555 nunca dispara ninguno de los cuatro.
#
# jid_pat: número de WhatsApp armado como JID completo, CUALQUIER dominio
# real que el proyecto usa (antes solo lid/s.whatsapp.net — T114 sumó
# c.us/g.us, que es donde vivía el propio caso sin arreglar de este
# contrato) y CUALQUIER país (nunca tuvo filtro de país, a diferencia de
# cl_pat). Umbral 7, no 8 — medido: con 8 el caso real de 7 dígitos que
# motivó este contrato pasaba invisible; con 7, cero ruido sobre el árbol
# completo (un solo hit, el caso real); con 6 aparece ruido de ids de
# grupo (@g.us); con 5, 19 falsos positivos. 7 es el punto medido, no
# adivinado.
jid_pat='(^|[^0-9])(?!555)[0-9]{7,}@(lid|s\.whatsapp\.net|c\.us|g\.us)'

# code_jid_pat: un JID armado EN CÓDIGO GO, no como literal con arroba —
# types.NewJID("<digitos>", "...") / types.JID{User: "<digitos>"}. Ninguna
# búsqueda de "numero@dominio" ve esto (la arroba no está, la arma el
# runtime) — es el vector que más datos dejó pasar según R21. Genérico,
# cualquier país, mismo umbral 7 medido arriba (cero ruido sobre el árbol
# completo con ese umbral).
code_jid_pat='(NewJID\(|JID\{User:[[:space:]]*)"(?!555)[0-9]{7,}"'

# intl_pat: un número en notación internacional (+<código país><número>),
# CUALQUIER país — el signo + es la señal: nada más en este proyecto
# escribe un "+" pegado a 7+ dígitos, así que no hace falta una lista de
# códigos de país (frágil, nunca completa) para reconocerlo. Medido: cero
# ruido sobre el árbol completo.
intl_pat='(^|[^0-9+])\+(?!555)[0-9]{1,3} ?[0-9]{6,12}(?![0-9])'

# cl_pat: el patrón original (T105-era), SIN el signo +, formato local
# chileno suelto en texto (569 + 8 dígitos, sin arroba ni +). Se mantiene
# TAL CUAL — no se generaliza a "cualquier país" porque un número sin +
# y sin dominio es indistinguible de un timestamp/id interno sin saber
# el código de país: medido contra el árbol completo con un patrón
# genérico de 7+ dígitos sueltos, 31 falsos positivos (timestamps Unix,
# ids con año embebido). El caso real que motivó "no solo Chile" (un JID
# armado en código con prefijo argentino) ya lo cubre code_jid_pat arriba,
# sin necesitar reconocer el prefijo — por eso este patrón sigue
# específico: ampliarlo sin el signo + no cierra un hueco real medido,
# solo agrega ruido medido.
cl_pat='(^|[^0-9])\+?56 ?9(?!55) ?[0-9]{8}([^0-9]|$)'

personal_data_pat="$jid_pat|$code_jid_pat|$intl_pat|$cl_pat"
