# T153 etapa 3d — el email de recuperación y el mapa de superficies (ct-2026-09-16-1916)

Diagrama hecho a mano, no vía `dflux_resolve` — mismo gap conocido de Go en
el codemap (ver etapas 3/3a/3b/3c).

## Parte 1 — el email, mismo molde que el WhatsApp de 3a

```mermaid
flowchart LR
    A["deliverRecoveryEmail(to, code)"] --> B["lang := effectiveLang(d.Store)"]
    B --> C["subject := i18n.T(lang, server.recovery_email_subject)"]
    B --> D["body := i18n.T(lang, server.recovery_email_body, code, code)"]
    C --> E["smtp.SendMail"]
    D --> E
```

Mismo criterio que "bajá la espera o subí el techo" (3b): el español ya
tenía voseo (`ignorá`, `si no lo pediste vos`) — queda VERBATIM, sin tocar
una letra. El inglés es la traducción nueva. Plantilla entera con
`{code}` como hueco, nunca `"Tu código es: " + code` concatenado.

## Parte 2 — el mapa de superficies

El error que costó dos huecos: enumerar POR ÁREA (bandeja, email) en vez
de por MECANISMO de salida.

```mermaid
flowchart TD
    A["¿Qué áreas tiene el tablero?"] -->|"lista de memoria,\ndeja huecos"| B["se escapa lo que\nnadie pensó nombrar\n(bandeja, email)"]
    C["¿Quién importa net/smtp,\nfyne.io/systray? ¿quién\nescribe al ResponseWriter?\n¿quién encola al outbox?"] -->|"búsqueda por\ndependencia, finita"| D["encuentra TODOS los\nsinks reales, incluidos\nlos que nadie nombró"]
```

**El barrido real, uno por uno:**

```mermaid
flowchart TD
    A["grep de imports:\nnet/smtp, fyne.io/systray,\nqrterminal, os.Stdout, fmt.Print"] --> B["net/smtp → recover.go\n(email, 3d)"]
    A --> C["fyne.io/systray → tray_windows.go\n(bandeja, 3c)"]
    A --> D["qrterminal → main.go\n(QR ascii en consola)"]
    A --> E["fmt.Print* → secrets/*/main.go\n(scripts sueltos, gitignored)"]
    F["grep de call sites:\n.Enqueue(, .EnqueueWithModel("] --> G["7 sitios — 3 literales fijos\n(ya traducidos, 3a/3d),\n4 pasan contenido DINÁMICO\n(draft/agente, dato no interfaz)"]
    H["grep de call sites:\nwriteJSON("] --> I["campo error: 43 literales\n(3b) — campo status:\nverificado que app.js NUNCA\nlo pinta crudo, solo lo compara"]
```

## Los tres verificados "no aplica" — con evidencia, no supuesto

```mermaid
flowchart TD
    A["qrterminal.GenerateHalfBlock(...,\nos.Stdout)"] -->|"el binario shippeado\ncorre -H windowsgui"| B["sin consola adjunta,\neste Write no tiene\nadónde ir en producción"]
    C["eventbus.Event{type,jid,ts}"] -->|"grep textContent = data./\n= d. en TODO app.js"| D["el único uso de\ndata.status es un ===\n(rama), nunca se pinta;\nEvent.type es tag fijo,\nnunca texto de mensaje"]
    E["http.Error( en todo el árbol"] -->|"0 resultados"| F["no existe ese sink hoy —\nsi aparece, es sink nuevo,\nagregar fila a la tabla"]
```

## El entregable: tabla en docs/MANUAL.md, no un test

Un test no puede fallar por un sink que todavía no existe (nadie puede
escribir hoy una aserción sobre un `import` que se agregará el mes que
viene). La tabla en `docs/MANUAL.md` ("Superficies de texto — mapa de
sinks") es lo que hace que el PRÓXIMO sink encuentre un lugar donde
sumarse, en vez de escaparse una tercera vez.

## Lo que se reportó y NO se tocó

`installer/windows/*.iss` (Inno Setup) — texto visible en español, pero
otra tecnología y otro ciclo de instalación. Ya verificado por Citrino que
`internal/installer/` no existe (mismo patrón que casi esconde la
bandeja) — está en `installer/windows/`. Escalado al dueño, no es parte
de T153.

## Verificación

`go build ./...` (Windows), `CGO_ENABLED=0 GOOS=linux go build ./...`,
`CGO_ENABLED=0 GOOS=darwin go build ./...`, `go vet ./...`,
`go test ./...` — los cinco verdes.
