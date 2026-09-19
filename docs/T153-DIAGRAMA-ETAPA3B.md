# T153 etapa 3b — los errores del servidor que llegan a pantalla (ct-2026-09-16-1828)

Diagrama hecho a mano, no vía `dflux_resolve` — mismo gap conocido de Go en
el codemap (ver etapas 3/3a).

## El corte A/B, medido sobre los 43

```mermaid
flowchart TD
    A["43 literales \"error\": \"...\" en internal/restapi/\n(medidos, no ~27 como se creía)"] --> B{"¿qué hace el\nusuario al leerlo?"}
    B -->|"hay una acción\n(achicar la imagen, corregir\nel mail, reintentar)"| C["Clase A\ni18n.T(lang, \"server.xxx\")\n14 literales"]
    B -->|"la única acción es\navisarle a un desarrollador"| D["Clase B\nqueda literal, declarado\ncon motivo — 29 literales"]
    C --> E["esCatalog/enCatalog"]
    D --> F["errorLiteralExceptions\n(error_literals_test.go)"]
```

6 de los 14 de Clase A ya estaban en español (medidos por Citrino, sin
discusión); los otros 8 ya estaban en inglés — ahí el español es la
traducción NUEVA, igual que en catalog.go.

## La trampa: texto que el front YA traduce por su cuenta

Tres de los 43 casen en Clase A por el criterio ("hay una acción: reintentar
el login/código") pero NUNCA llegan a pantalla — el catch de login/recover
en app.js ignora el campo `error` por completo:

```mermaid
flowchart TD
    A["auth.go: \"invalid credentials\"\nrecover.go: \"invalid or expired code\"\nrecover.go: \"unsupported recovery method\""] --> B["post() arma un Error\ncon data.error o el path→status"]
    B --> C{"¿el .catch() de\nese formulario LEE\ne.message?"}
    C -->|"login/recover: NO\nsubmitLogin(), recover_submit(),\nrequestRecoveryCode()"| D["texto fijo propio —\nt(\"auth.invalid_credentials\")\nt(\"auth.invalid_or_expired_code\")\nt(\"auth.code_sent_generic\")\n(éxito Y error, mismo texto)"]
    C -->|"todo el resto del tablero:\nSÍ — t(\"error.prefix\") + e.message"| E["el server.* SÍ se ve"]
    D -.->|"crear server.* acá\nduplicaría el mismo\nconcepto en dos catálogos"| F["queda literal,\ndeclarado con motivo\n(Clase B por razón distinta)"]
```

Verificado leyendo app.js línea por línea (`submitLogin`, `recover_submit`,
`requestRecoveryCode`), no asumido. Los otros ~40 call sites SÍ pasan por
`t("error.prefix") + e.message` (o son `<img>/<video> src`, ver abajo) —
ahí un `server.*` sin traducir SÍ se vería.

## Dos formas más de "nunca llega a pantalla como texto"

```mermaid
flowchart LR
    A["\"no QR pending\"\n\"media not found\" / \"...on disk\"\n\"no avatar cached\" / \"...on disk\""] -->|"servidas como bytes,\nno como JSON leído por JS"| B["<img>/<video> src=\"/api/...\"\nfallo = imagen rota,\nnunca un t() en pantalla"]
    C["\"agent_id, endpoint, antenna_terminal_id\nand pinpass are required\""] -->|"app.js valida ANTES\nde postear (createBtn.onclick)"| D["solo se llega llamando\nel endpoint directo"]
```

## Un literal mixto: prefijo traducible + err.Error() crudo

`admin.go`: `"invalid data_url: " + err.Error()` — parte prosa (Clase A),
parte error técnico de Go pegado atrás (fuera de alcance, mismo problema que
los ~95 `err.Error()` que el contrato padre ya excluyó). Resuelto como
plantilla completa con hueco, no fragmentos pegados (regla del catálogo
desde 2b-i):

```mermaid
flowchart LR
    A["\"invalid data_url: \" + err.Error()"] --> B["server.invalid_data_url =\n\"invalid data_url: {detail}\""]
    B --> C["i18n.T(lang, \"server.invalid_data_url\",\n\"detail\", err.Error())"]
    C -.->|"el {detail} sigue siendo\nGo crudo, sin traducir —\nmismo carve-out del padre"| D["no es un problema de idioma,\nqueda para después de publicar"]
```

## El hallazgo lateral: nil Store en auth()

Ningún handler de arriba tenía este problema (todos chequean `d.Store ==
nil` antes de llegar al literal) — pero `auth()` es el ÚNICO gate que corre
ANTES de cualquier chequeo de handler, y `TestEventsRequiresAPIKeyWhenSet`
lo ejercita con `Deps{Store: nil}`:

```mermaid
flowchart TD
    A["auth() sin key/sesión válida"] --> B["i18n.T(effectiveLang(d.Store), \"server.unauthorized\")"]
    B --> C{"d.Store == nil?"}
    C -->|"sí (único caso real:\nTestEventsRequiresAPIKeyWhenSet)"| D["antes: KVGet sobre\nreceiver nil → PANIC"]
    C -->|"no"| E["camino normal"]
    D -.->|"fix"| F["effectiveLang(nil) → i18n.Detect()\ndirecto, sin tocar la DB"]
```

Encontrado corriendo la suite ANTES de dar por bueno el cableado (no
asumido) — build/vet pasan igual con o sin el guard, solo el test explota.

## El test que sostiene la clasificación — probado en rojo

`error_literals_test.go` barre TODO `.go` de `internal/restapi/` (no
`_test.go`) por PATRÓN (`"error":\s*"..."`), no por lista de call sites —
mismo diagnóstico que 2b-v y 3a: enumerar por lista deja huecos.

```mermaid
flowchart TD
    A["cada \"error\": \"literal\" encontrado"] --> B{"¿está en\nerrorLiteralExceptions?"}
    B -->|no| C["FALLA — literal sin clasificar"]
    B -->|sí| D{"¿tiene motivo\nno vacío?"}
    D -->|no| E["FALLA — TestErrorLiteralExceptionsHaveReasons"]
```

Probado en rojo dos veces antes de cerrar:
1. Literal nuevo sin clasificar insertado en `dashboard.go` (revertido
   después) → `TestErrorLiteralsAreClassified` cayó con el mensaje esperado.
2. Motivo vaciado a mano en `errorLiteralExceptions["no QR pending"]`
   (revertido después) → `TestErrorLiteralExceptionsHaveReasons` cayó.

`err.Error()`, `i18n.T(...)` y la variable `status` quedan afuera del
regex por construcción (ninguno abre con comilla justo después de
`"error":`) — no son literales, el contrato lo pide así.

## Verificación

`go build ./... && go vet ./... && go test ./...` verde completo, incluidos
`TestErrorLiteralsAreClassified`, `TestErrorLiteralExceptionsHaveReasons` y
`TestServerKeysMatchGoCallSites` (internal/i18n) — este último ya cubre los
14 call sites de Clase A sin cambios propios, porque camina TODO el árbol
`.go`, no solo `capipush`.
