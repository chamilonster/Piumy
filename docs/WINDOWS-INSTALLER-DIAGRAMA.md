# Instalador de Windows (ct-2026-07-31-1643)

> **T21 (ct-2026-08-05-1308)** reescribió por completo cómo se leen/reusan
> las claves de una instalación existente y MOVIÓ ese paso de
> `ssPostInstall` a `PrepareToInstall` (corre antes de copiar archivos,
> para poder abortar limpio) — el diagrama de "ssPostInstall" de abajo
> quedó desactualizado en ese punto (y ya venía desactualizado desde T2/T6
> en el número de claves). Ver **`docs/T21-DIAGRAMA-INSTALADOR-KEY-SAFETY.md`**
> para el flujo real y actual de generación/reuso de claves.

Boss verbatim: *"mi idea principal es que todo se encapsule junto con el
gateway... quisiera compilar esto para que sea llegar a instalar para
cualquier clever codeur en el futuro."* Primer instalador: solo Windows,
Inno Setup, `installer/windows/piumy.iss`. Repo público, se sube SOLO el
`.exe` compilado — no el código fuente (subida no es tarea de Tourmaline).

## El wizard, de punta a punta

```mermaid
flowchart TD
    A["Bienvenida\ncarita del dashboard (wizard-welcome.bmp)\ntexto verbatim del boss"] --> B["Descargo (EULA)\naceptar/rechazar, eula.txt"]
    B --> C["Carpeta de destino\n{localappdata}\\Piumy, sin admin"]
    C --> D["Clave del tablero + correo de recuperación\nmisma página, un solo tema"]
    D --> E["Tareas adicionales\narrancar con Windows (opcional)"]
    E --> F["Instalando\ncopia Piumy.exe"]
    F --> G["ssPostInstall\ngenera 3 claves + escribe launcher + lanza + abre navegador"]
```

## La página de la clave (D) — 3 campos, un tema

```mermaid
flowchart LR
    D1["Clave:\nmin 4 caracteres"] --> V["NextButtonClick\nvalida longitud + coinciden"]
    D2["Repite la clave:"] --> V
    D3["Correo de recuperación de contraseña:\nOPCIONAL, sin validar acá"] -.-> G
    V -->|"ok"| G["ssPostInstall"]
```

## ssPostInstall (G) — qué genera y qué persiste dónde

```mermaid
flowchart TD
    G["ssPostInstall"] --> K["GenerateRandomHex ×3\nMCP 16B · REST 24B · BACKUP 32B"]
    K --> L["run-piumy.bat PERMANENTE\n9 variables + las 3 claves\nNUNCA la clave del tablero ni el correo"]
    G --> M["Entorno del proceso instalador\n(heredado por Piumy.exe, no en disco)"]
    M --> M1["PIUMY_DASHBOARD_PASSWORD"]
    M --> M2["PIUMY_DASHBOARD_RECOVERY_EMAIL"]
    L --> N["Exec Piumy.exe (SW_HIDE)"]
    M1 --> N
    M2 --> N
    N --> O["auth.go: passHash()\nseed-only si SettingDashPassHash vacío"]
    N --> P["admin.go: SeedRecoveryEmailFromEnv()\nllamada eager en main.go, seed-only si KV vacío"]
    N --> Q["Sleep 3s → abre navegador\nhttp://127.0.0.1:8092/dashboard/"]
```

## GenerateRandomHex — por qué CoCreateGuid y no CryptoAPI

`CryptAcquireContextW`+`CryptGenRandom` (advapi32.dll) reventaban con
**Access Violation en el mismo offset del binario**, probado con dos formas
de pasar el buffer (`array of Byte` tipado, después `AnsiString`) — el
offset idéntico entre builds con código distinto adentro de la función
señala que el problema es la marshaling nativa de Pascal Script para esa
forma de llamada externa, no el tipo del lado del script. Reproducido en
vivo, diagnosticado con un build de diagnóstico (crypto stubbeada) que
instaló de punta a punta sin error. Fix: `CoCreateGuid` (ole32.dll) — un
solo parámetro `var TGUID`, tipo explícitamente soportado por Pascal
Script. Aleatoriedad sigue siendo real (motor criptográfico de Windows, no
un timestamp). Verificado sin GUI: instalador headless propio (mismo
código, `/VERYSILENT`, dos carpetas) — 6 claves generadas, las 6 distintas,
ninguna vacía.

## Desinstalación

```mermaid
flowchart TD
    U["Desinstalar"] --> W{"¿Existe secrets\\?"}
    W -->|"no"| X["Termina, nada que preguntar"]
    W -->|"sí"| Y["MsgBox: ¿borrar también los datos?\nexplica qué hay adentro"]
    Y -->|"Sí"| Z["DelTree secrets\\"]
    Y -->|"No / default"| AA["Desinstala el programa,\nsecrets\\ queda intacto"]
```

## Criterio de listo

- `go build ./... && go vet ./... && go test ./...` verde (el gateway
  ganó `restapi.SeedRecoveryEmailFromEnv` + su wiring en `main.go`).
- Instalador compila limpio con ISCC.
- Aleatoriedad verificada sin UI (harness headless, 2 corridas, 6 claves
  distintas, ninguna vacía) — la restricción de mouse/teclado de la sesión
  impidió el click-through real; queda pendiente para cuando se levante o
  lo pruebe el boss.
- `docs/MANUAL.md` actualizado (sección S1e-2, siembra desde el
  instalador).
