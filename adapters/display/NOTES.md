# adapters/display — módulo e-paper

Rescatado 1:1 de Piumy (`C:\proyectos\Piumy\coderoot\adapters\display\`),
padre `ct-2026-07-19-1511`. Python autocontenido — no lo importa ningún
paquete Go, no lo toca `go build`.

- S2a (`ct-2026-07-19-1843`): renderer + backend de archivo.
- S2b (`ct-2026-07-19-1853`): service loop + contrato `status.json`/`face.json`.
- S2c (`ct-2026-07-19-1919`): driver Waveshare 2.13" V4 (hardware real) +
  estandarización de los env vars a `PIUMY_*`.
- S3 (pendiente, fuera de este módulo): prueba real en la Pi Zero 2 con el
  panel físico conectado.

## Qué hay

- `render.py` — el renderer puro (PIL, sin dependencia de hardware):
  `render_image(status, anim_step=0)`, el catálogo de 19 caritas
  (`KAOMOJI_CATALOG` + `idle` vía el motor de gaze + `qr` fullscreen), y el
  self-check ejecutable como script.
- `backend.py` — factory `get_backend()` que lee `PIUMY_DISPLAY`
  (`file` | `epaper-waveshare` | `none`) y devuelve el backend elegido.
- `file/backend.py` — `FileBackend`, escribe el PNG renderizado a disco
  (dev/CI sin hardware).
- `epaper/backend.py` — `EPaperWaveshareBackend` (S2c), el driver de
  hardware real. Ver la sección dedicada más abajo.
- `fonts/` — `DejaVuSans.ttf` + `DejaVuSans-Bold.ttf` bundleadas (copia
  bytes-idéntica a Piumy) para que la cara se vea igual en cualquier PC y
  en el Pi, sin depender de qué fuentes tenga instaladas el sistema.
- `requirements.txt` — `Pillow>=10`, `qrcode[pil]>=7`. `spidev`/`gpiod` NO
  van acá (van por `apt` en la Pi — ver más abajo; en un PC sin esos
  paquetes el módulo igual importa, ver "defensivo").
- `service.py` — el loop vivo (S2b): polea `status.json` por mtime,
  decide refresh full vs. parcial (flash solo al entrar/salir de
  `qr`/`error`/`sleeping`, y solo si `PIUMY_EPAPER_FULL_REFRESH=1`),
  avanza la animación idle con cadencia dinámica FAST→SLOW ("sobre de
  atención": rápida tras un evento real, se relaja sola si no pasa
  nada), y escribe el sidecar `face.json` (`{face, mood, ts}`) con
  `pick_variant`/`variant_repr` de `render.py` — nunca duplica el
  catálogo. Graceful shutdown por SIGTERM/SIGINT.

## El contrato con el gateway (status.json / mood)

El gateway (`internal/state/state.go`) **ya** escribe `mood` en el
`status.json` que persiste (`PIUMY_STATUS_PATH`, wireado en `main.go`) —
el campo `Mood` no tiene `omitempty`, así que siempre está presente, y
`state.ValidMoods` ya contiene los 19 moods exactos del catálogo de
`render.py` (fue cherry-pickeado de Piumy con el mismo contrato desde el
principio — ver el comentario en `state.go`). **No hizo falta ningún
cambio Go** — `service.py` lee el mood real apenas se apunte
`PIUMY_STATUS_PATH` (Python) al mismo archivo que `PIUMY_STATUS_PATH`
(Go) — mismo nombre de variable ahora, ver la siguiente sección.

`face.json` (el sidecar que escribe `service.py` con el kaomoji actual)
**no se cablea de vuelta al gateway**: decisión tomada en S2b y todavía
vigente — el dashboard ya mapea mood→kaomoji en JS (S1a), así que exponer
`face` en `/api/status` no suma nada visible hoy. Se puede retomar el día
que el dashboard necesite mostrar la variante EXACTA que dibuja el
e-paper (no solo el mood) en vez de su propio mapeo.

## Env vars — estandarizadas a `PIUMY_*` (S2c)

S2a/S2b habían portado el módulo con el prefijo `PIMYWA_*` heredado de
Piumy (`pimywa`), distinto del resto del gateway (`PIUMY_*`). S2c lo
estandariza: todo el módulo usa `PIUMY_*` ahora, y el path de
`status.json` se renombró literalmente a **`PIUMY_STATUS_PATH`** (antes
`PIMYWA_STATUS`) — mismo nombre exacto que usa el gateway Go
(`internal/config/config.go`), así que un solo `EnvironmentFile` de
systemd con `PIUMY_STATUS_PATH=...` alimenta a los dos procesos sin
duplicar la variable. El resto de los nombres solo cambiaron de prefijo
(mismo sufijo que Piumy).

**Ojo — los DEFAULTS siguen sin alinear** (esto no era parte del pedido,
solo el nombre): el gateway Go default es `status.json` (relativo, cwd —
`config.go`); este módulo default es `/opt/pimywa/data/status.json`
(convención de Piumy/systemd). Si no se setea `PIUMY_STATUS_PATH`
explícitamente en ambos lados (mismo `EnvironmentFile`), cada proceso cae
a un path distinto y el service nunca ve el mood real — sigue siendo un
paso de configuración de despliegue, la estandarización del nombre solo
evita tener que setear DOS variables distintas.

| Variable | Default | Usa |
|---|---|---|
| `PIUMY_DISPLAY` | `file` | `backend.py` — backend: `file`\|`epaper-waveshare`\|`none` |
| `PIUMY_DISPLAY_OUT` | `display.png` | `file/backend.py` — path del PNG (backend file) |
| `PIUMY_STATUS_PATH` | `/opt/pimywa/data/status.json` | `service.py` — path de `status.json` (alinear con el `PIUMY_STATUS_PATH` del gateway Go) |
| `PIUMY_FACE_FILE` | `/run/pimywa/face.json` | `service.py` — sidecar del kaomoji actual |
| `PIUMY_POLL_INTERVAL` | `2.0` | `service.py` — intervalo de chequeo de mtime, s |
| `PIUMY_ANIM_FAST_SEC` | `25.0` | `service.py` — cadencia idle recién after un evento |
| `PIUMY_ANIM_SLOW_SEC` | `60.0` | `service.py` — cadencia idle en reposo |
| `PIUMY_ANIM_RAMP_SEC` | `180.0` | `service.py` — segundos de calma para FAST→SLOW |
| `PIUMY_FACE_MIN_SEC` | `3.0` | `service.py` — piso del piggyback de un data-refresh |
| `PIUMY_LOW_BATT` | `15` | `service.py`/`render.py` — umbral de batería baja, % |
| `PIUMY_BATTERY_SAVER` | `0` (off) | `service.py` — 4x más lento bajo `PIUMY_LOW_BATT` |
| `PIUMY_EPAPER_FULL_REFRESH` | `0` (off) | `service.py` — permite flash en transiciones grandes |
| `PIUMY_SLEEP_ON_EXIT` | `1` | `service.py` — dormir el panel al salir |
| `PIUMY_LOG_LEVEL` | `INFO` | `service.py` |
| `PIUMY_EPAPER_RST_PIN` | `17` | `epaper/backend.py` — BCM GPIO de RESET |
| `PIUMY_EPAPER_DC_PIN` | `25` | `epaper/backend.py` — BCM GPIO de Data/Command |
| `PIUMY_EPAPER_BUSY_PIN` | `24` | `epaper/backend.py` — BCM GPIO de BUSY |
| `PIUMY_EPAPER_PWR_PIN` | `18` | `epaper/backend.py` — BCM GPIO de power enable (`""` = sin pin) |
| `PIUMY_EPAPER_GPIOCHIP` | `gpiochip0` | `epaper/backend.py` — chip GPIO o path `/dev/` |
| `PIUMY_EPAPER_SPI_DEV` | `/dev/spidev0.0` | `epaper/backend.py` — device SPI |
| `PIUMY_EPAPER_SPI_SPEED` | `4000000` | `epaper/backend.py` — velocidad de clock SPI, Hz |
| `PIUMY_EPAPER_GHOST_PARTIALS` | `0` (off) | `epaper/backend.py` — full refresh cada N parciales |

## Driver Waveshare 2.13" V4 (S2c, `epaper/`)

**Modelo:** Waveshare 2.13" V4 (`epd2in13_V4`), panel 122×250 px portrait
(1 bpp). Implementado a mano con `spidev` (SPI) + `gpiod` v2 (GPIO) —
**sin** la librería `waveshare_epd`; la secuencia de comandos del
protocolo está reproducida fielmente desde la referencia MIT del vendor
(`waveshareteam/e-Paper`,
`RaspberryPi_JetsonNano/python/lib/waveshare_epd/epd2in13_V4.py`).

**Política de refresco** (estilo pwnagotchi, sin flash constante): boot +
transiciones grandes (`qr`/`error`/`sleeping`, si `PIUMY_EPAPER_FULL_REFRESH=1`)
hacen `init()+Clear+displayPartBaseImage()` — un flash deliberado; todo lo
demás usa `displayPartial()` — sin flash. Anti-ghosting opcional
(`PIUMY_EPAPER_GHOST_PARTIALS`, default `0` = off): fuerza un refresh
completo cada N parciales si el fantasma se acumula en la práctica.

**Defensivo (lo clave del subcontrato):** los imports de hardware
(`spidev`, `gpiod`) están DENTRO del `__init__` de `_PanelController`,
nunca a nivel de módulo — así el archivo se puede `import` en cualquier
PC sin romper nada. `EPaperWaveshareBackend._try_init()` envuelve la
construcción en `try/except`: si falta `spidev`/`gpiod` (`ImportError`) o
falla cualquier otra cosa del init de hardware, loguea un `warning` y el
backend queda en modo no-op (`show()`/`close()` no hacen nada) — **nunca
crashea, nunca hace crash-loop**. Verificado en esta PC (Windows, sin
`spidev`/`gpiod` instalados): `get_backend("epaper-waveshare")` importa
OK y degrada a no-op con el warning esperado en el log.

**El test real (hardware de verdad) es en la Pi Zero 2 — S3, aparte.**
Acá solo se confirma que el código está portado, el factory lo
selecciona, y el degrade sin hardware funciona.

### Pinout GPIO (Waveshare 2.13" HAT defaults, Pi Zero 2)

| Señal | BCM GPIO | Pin del HAT | Env var |
|-------|----------|--------------|---------|
| RST | 17 | 11 | `PIUMY_EPAPER_RST_PIN` |
| DC | 25 | 22 | `PIUMY_EPAPER_DC_PIN` |
| BUSY | 24 | 18 | `PIUMY_EPAPER_BUSY_PIN` |
| PWR | 18 | 12 | `PIUMY_EPAPER_PWR_PIN` |
| MOSI | 10 | 19 | (driver SPI del kernel) |
| SCLK | 11 | 23 | (driver SPI del kernel) |
| CS0 | 8 | 24 | `PIUMY_EPAPER_SPI_DEV` |

### Instalación en la Pi

1. **Habilitar SPI** — `/boot/firmware/config.txt`: `dtparam=spi=on`, reiniciar.
2. **Paquetes apt** (sistema, no pip):
   ```bash
   sudo apt update && sudo apt install -y \
       python3-pip python3-pil python3-gpiod python3-spidev fonts-dejavu-core
   ```
   `fonts-dejavu-core` es un respaldo — el módulo ya bundlea su propia
   copia en `fonts/` y la prueba primero.
3. **Paquetes pip** (`requirements.txt` — Pillow + qrcode únicamente):
   ```bash
   python3 -m venv /opt/pimywa/venv
   /opt/pimywa/venv/bin/pip install -r adapters/display/requirements.txt
   ```
4. **Systemd** (ejemplo, env `PIUMY_*` ya estandarizados):
   ```ini
   [Unit]
   Description=Piumy gateway display service
   After=network.target

   [Service]
   Type=simple
   User=pi
   EnvironmentFile=/opt/pimywa/.env
   Environment=PIUMY_DISPLAY=epaper-waveshare
   Environment=PIUMY_STATUS_PATH=/opt/pimywa/data/status.json
   ExecStart=/opt/pimywa/venv/bin/python /opt/pimywa/adapters/display/service.py
   Restart=on-failure
   RestartSec=5

   [Install]
   WantedBy=multi-user.target
   ```
   El mismo `EnvironmentFile` (o al menos el mismo valor de
   `PIUMY_STATUS_PATH`) debe usarlo el binario Go del gateway.

### Pendiente de verificar en la Pi real (heredado de Piumy, sin cambios)

- **Dirección de rotación de imagen**: el renderer produce 250×122
  landscape; `image_to_buffer()` rota 90° CCW a 122×250 portrait. Si la
  cara aparece de costado o al revés, ajustar el ángulo en
  `epaper/backend.py::_PanelController.image_to_buffer()`.
- **Polaridad del pin BUSY**: el driver espera hasta que BUSY esté en
  LOW. Si el init se cuelga, revisar la línea física.
- **Ghosting de refresh parcial**: ajustable vía
  `PIUMY_EPAPER_GHOST_PARTIALS` — empezar con 30-50.
- **Pin PWR**: si el board no tiene PWR enable, `PIUMY_EPAPER_PWR_PIN=` (vacío).

## Cómo correrlo

```bash
pip install -r adapters/display/requirements.txt
cd adapters/display
python render.py <outdir>
```

Genera en `<outdir>` un PNG por cada uno de los 19 moods + 3 estados de
batería + las 12 frames de una vuelta completa del loop de gaze + un check
de "no placeholder" — más un assert de seguridad de glyphs (ningún
carácter no verificado en el set `_VERIFIED_GLYPHS`) y un assert de hitbox
(ningún glyph flotante se superpone con otro ni con el chrome). Si el
script termina con `All renders OK.` y sin excepciones, el rescate quedó
fiel.

Para escribir directamente vía el backend (en vez de `render.py` standalone):

```python
from render import render_image
from backend import get_backend

be = get_backend("file")          # o vía env PIUMY_DISPLAY=file
img = render_image({"mood": "idle", "wifi": 3, ...})
be.show(img, full=True)
be.close()
```

Para correr el service loop contra el `status.json` real del gateway,
apuntando al backend de archivo:

```bash
export PIUMY_STATUS_PATH=/ruta/al/status.json    # el mismo PIUMY_STATUS_PATH del gateway Go
export PIUMY_DISPLAY=file
export PIUMY_DISPLAY_OUT=display.png
export PIUMY_FACE_FILE=face.json
python adapters/display/service.py
```

Queda corriendo hasta SIGTERM/SIGINT; cada cambio real de `mood` en
`status.json` (o cada tick de animación idle) reescribe `display.png` +
`face.json`.

En la Pi, con el panel conectado, alcanza con `PIUMY_DISPLAY=epaper-waveshare`
en vez de `file` — mismo `service.py`, sin ningún otro cambio.

## Decisión propia (no portado)

Piumy trae, además, `file/render.py`, `file/faces.py`, `file/display.png`
y `file/requirements.txt`. Son un renderer standalone más viejo que el
`render.py` compartido de arriba dejó huérfano — no lo llama
`backend.py::get_backend()` (que solo usa `file/backend.py`) ni ningún otro
punto del módulo. No se portaron para no resucitar un segundo renderer
duplicado y sin llamador real en el gateway. Si en algún momento se
necesita ese fallback standalone, está en el árbol de Piumy para
rescatarlo aparte.

## Pendiente (fuera de S2a/S2b/S2c)

- **S3**: prueba real en la Pi Zero 2 con el panel Waveshare físico
  conectado — rotación de imagen, polaridad de BUSY, ghosting real.

Ver `docs/MANUAL.md` (sección `adapters/display`) para el resumen a nivel
proyecto.
