# S1 — Multi-cuenta: aislamiento por cuenta (ct-2026-09-20-1100)

Contrato madre: `ct-2026-07-19-0636-multi-cuenta-db-por-número-ui-selector-c`.
Solo el motor — sin distintivo visual, sin bandeja, sin selector de dashboard
(esos son peldaños posteriores).

## El problema — cuatro puntos donde dos instancias chocan hoy

```mermaid
flowchart TD
    ENV["PIUMY_ACCOUNT=trabajo\n(variable nueva, única)"]

    subgraph P1["1. DataDir()"]
        D1["hoy: una sola carpeta\npara toda la máquina"]
    end
    subgraph P2["2. singleInstanceMutexName"]
        D2["hoy: constante global\npor MÁQUINA, no por sesión"]
    end
    subgraph P3["3. MCPAddr/RESTAddr"]
        D3["hoy: :8091/:8092 fijos\nsegunda instancia no bindea"]
    end
    subgraph P4["4. piumy-config.json"]
        D4["al lado del binario —\nrutas explícitas ganan sobre DataDir()"]
    end

    D1 -->|"sin aislar"| X["dos Piumy comparten\nwhatsmeow.db"]
    D2 -->|"candado más grueso\nque su propósito"| Y["la 2da instancia\nno arranca NUNCA,\naunque sea otra cuenta"]
    D3 --> Z["la 2da instancia\nmuere: bind falla"]
    D4 -->|"invisible, sin síntoma"| X
```

Los primeros tres son visibles apenas se corre `go run` desde `coderoot`. El
cuarto (punto 4) es el que Citrino midió como el que no se ve: la instalación
real del boss tiene `piumy-config.json` con rutas explícitas al lado del
`.exe`, y una ruta explícita le gana a `DataDir()` — sin tocarlo, la cuenta
nueva volvería a caer en la sesión de WhatsApp de la instalación default, sin
ningún síntoma hasta que las dos cuentas se pisen de verdad.

## El fix — una sola entrada, se deriva todo

```mermaid
flowchart TD
    ACC["PIUMY_ACCOUNT"] --> SLUG["accountSlug(account)\nvacío / '..' / separadores / ':' → error de arranque"]
    SLUG --> DD["dataDirFor(goos, localAppData, home, account)\n<base del SO>/accounts/<slug>"]
    PDD["PIUMY_DATA_DIR"] -->|"gana SIEMPRE, verbatim,\nsin tocar (como hoy)"| DD_FINAL["DataDir() efectivo"]
    DD --> DD_FINAL

    DD_FINAL --> MUTEX["acquireSingleInstance(dataDir)\nsha256(lower(clean(dataDir)))\n→ nombre de mutex por CARPETA,\nno por máquina ni por nombre de cuenta"]

    ACC -->|"seteado, y el env de\npuerto NO seteado a mano"| PORTS["MCPAddr/RESTAddr default: ':0'\n(el SO elige puerto libre)"]

    DD_FINAL --> FILEDEF["ApplyFileDefaultsIn:\ncon PIUMY_ACCOUNT, las variables\nde RUTA de piumy-config.json\nNO se aplican — las de CLAVE\n(MCP_KEY/REST_KEY) sí"]

    LISTEN["net.Listen(tcp, cfg.MCPAddr/RESTAddr)"] --> REALADDR["ln.Addr() — dirección REAL"]
    REALADDR --> AGENTCONNECT["agentconnect.Write\n(movido a DESPUÉS del bind)"]
    REALADDR --> LOG["log de arranque"]
    REALADDR --> TRAY["dashboardURL del tray"]
```

## Qué NO se toca

- `appMutexName`/`appmutex_windows.go` — del instalador, global, tiene que
  matchear `AppMutex` del `.iss` verbatim. Prohibido en el contrato.
- El fail-OPEN de `acquireSingleInstance` ante cualquier error de la API de
  Windows, y el uso de un mutex de SO (no un lock file/PID) — el kernel lo
  libera solo cuando el proceso muere, sea como sea.
- `agent-connect.json` como archivo de descubrimiento — ya asume que el
  puerto puede cambiar entre sesiones, no hace falta un registro nuevo.
- `PIUMY_DATA_DIR` sigue ganando por encima de todo, exactamente como hoy —
  ni siquiera se le agrega `accounts/<slug>` encima si está seteada.
- Sin `PIUMY_ACCOUNT`, cero cambio de comportamiento: misma carpeta, mismos
  puertos fijos, mismo `agent-connect.json` de siempre.

## Criterio de listo

- Sin `PIUMY_ACCOUNT`: `DataDir()`/puertos/`agent-connect.json` idénticos a
  hoy — probado con Y sin `piumy-config.json` al lado del binario.
- Dos instancias a la vez (`PIUMY_ACCOUNT=a`/`b`): las dos vivas, carpeta y
  `whatsmeow.db` propios, `agent-connect.json` con el puerto real bindeado.
- Mismo directorio, dos procesos: el segundo sale con el mensaje de hoy, sin
  tocar la sesión.
- Nombre de cuenta hostil (vacío, `..`, separadores) → error de arranque
  claro, nada escrito fuera de la carpeta.
- `go build ./... && go vet ./... && go test ./...` verde + cross-compiles.
- `MANUAL.md` actualizado.
