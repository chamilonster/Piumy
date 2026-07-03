# Display adapter — installation notes and env-var reference

## Environment variables

All Piumy display knobs are env-driven (zero hardcode).  Set them in
`/opt/pimywa/.env` (loaded by the systemd `EnvironmentFile=` directive) or
export them directly for development.

### Service / animator (`service.py`)

| Variable | Default | Description |
|---|---|---|
| `PIMYWA_STATUS` | `/opt/pimywa/data/status.json` | Path to the status file the core writes |
| `PIMYWA_DISPLAY` | `file` | Backend: `file` \| `epaper-waveshare` \| `none` |
| `PIMYWA_DISPLAY_OUT` | `display.png` | Output PNG path (file backend only) |
| `PIMYWA_POLL_INTERVAL` | `2.0` | Seconds between status.json mtime checks |
| `PIMYWA_ANIM_FAST_SEC` | `2.0` | Idle-tick interval right after a real status change ("sobre de atencion") |
| `PIMYWA_ANIM_SLOW_SEC` | `18.0` | Idle-tick interval once quiet for `PIMYWA_ANIM_RAMP_SEC` |
| `PIMYWA_ANIM_RAMP_SEC` | `60.0` | Seconds of quiet to lerp FAST -> SLOW |
| `PIMYWA_LOW_BATT` | `15` | Battery % below which the (already-dynamic) idle animation interval is 4x slower |
| `PIMYWA_SLEEP_ON_EXIT` | `1` | Put the e-paper panel to sleep on service exit (`0` to skip) |
| `PIMYWA_LOG_LEVEL` | `INFO` | Python logging level (`DEBUG` \| `INFO` \| `WARNING` \| `ERROR`) |

### Refresh policy (no-flash on small changes)

- **Boot**: `init()` + `Clear(0xFF)` + `displayPartBaseImage(white)` — one flash only.
- **Normal idle animation tick**: `displayPartial()` — zero flash.
- **Mood change (minor)**: `displayPartial()` — zero flash.
- **Mood change (big transition)**: `init()` + `Clear` + `displayPartBaseImage()` — deliberate flash.
  Big moods: `qr`, `error`, `sleeping` — entering or leaving these triggers a full refresh.
- **Identical image**: skipped entirely — no pointless partial update.
- **Anti-ghosting** (`PIMYWA_EPAPER_GHOST_PARTIALS > 0`, default `40`): automatic full refresh every N partials — matters more now that "sobre de atencion" can drive partials as fast as every ~2s during a burst.

### E-paper hardware (`epaper/backend.py`)

| Variable | Default | Description |
|---|---|---|
| `PIMYWA_EPAPER_RST_PIN` | `17` | BCM GPIO pin for RESET |
| `PIMYWA_EPAPER_DC_PIN` | `25` | BCM GPIO pin for Data/Command |
| `PIMYWA_EPAPER_BUSY_PIN` | `24` | BCM GPIO pin for BUSY |
| `PIMYWA_EPAPER_PWR_PIN` | `18` | BCM GPIO pin for power enable (set to empty string to skip) |
| `PIMYWA_EPAPER_GPIOCHIP` | `gpiochip0` | GPIO chip name or `/dev/` path |
| `PIMYWA_EPAPER_SPI_DEV` | `/dev/spidev0.0` | SPI device path |
| `PIMYWA_EPAPER_SPI_SPEED` | `4000000` | SPI clock speed in Hz |
| `PIMYWA_EPAPER_GHOST_PARTIALS` | `40` | Anti-ghosting: full refresh every N partials; `0` = disabled |

---

## Face catalog

`render.py` contains `FACES_CATALOG` — 13 moods from `status.schema.json`
(all moods except `qr` which renders a full-canvas QR code):

| Mood | Eye type | Mouth | Acc | Variants |
|---|---|---|---|---|
| `idle` | open (movable pupils) | smile / flat / smirk / yawn | zzz | **8** |
| `zero` | line (content) | smile | — | 3 |
| `new_msg` | wide | o | bang | 3 |
| `few` | round / open | flat / smile | — | 3 |
| `swamped` | dizzy | wavy / o | sweat | 3 |
| `thinking` | up | flat | gear | 3 |
| `working` | line | flat | gear | 3 |
| `responding` | round / open | talk | — | 3 |
| `ai_online` | star | grin | — | 3 |
| `vip` | star / wide | grin | — | 3 |
| `sleeping` | line | flat | zzz | 2 |
| `alert` | wide | o | bang | 2 |
| `error` | x | frown | — | 3 |

The `idle` variants implement the full "alive" cycle from `idle_proof.py`:
look center → look left → look right → blink → look up → bored → look down-left → yawn.

**No-wifi override**: when `wifi == 0` and mood is `idle`/`zero`/`few`, the face
shows wide eyes + flat mouth ("no wifi -- searching"), matching `idle_proof.py`'s
NOWIFI frame.

---

## Pi installation

### 1. Enable SPI

```
# /boot/firmware/config.txt
dtparam=spi=on
```

Reboot after enabling.

### 2. Apt packages

```bash
sudo apt update && sudo apt install -y \
    python3-pip python3-pil python3-gpiod python3-spidev fonts-dejavu-core
```

`fonts-dejavu-core` installs DejaVuSans.ttf at
`/usr/share/fonts/truetype/dejavu/` — the first path the renderer tries.

### 3. Python packages

```bash
python3 -m venv /opt/pimywa/venv
/opt/pimywa/venv/bin/pip install -r /opt/pimywa/adapters/display/requirements.txt
```

### 4. Systemd unit (example)

```ini
[Unit]
Description=Pimywa display service
After=network.target

[Service]
Type=simple
User=pi
EnvironmentFile=/opt/pimywa/.env
Environment=PIMYWA_DISPLAY=epaper-waveshare
Environment=PIMYWA_STATUS=/opt/pimywa/data/status.json
ExecStart=/opt/pimywa/venv/bin/python /opt/pimywa/adapters/display/service.py
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

### 5. GPIO pin mapping (Waveshare 2.13" HAT defaults)

| Signal | BCM GPIO | HAT pin | Env var |
|--------|----------|---------|---------|
| RST    | 17       | 11      | `PIMYWA_EPAPER_RST_PIN` |
| DC     | 25       | 22      | `PIMYWA_EPAPER_DC_PIN` |
| BUSY   | 24       | 18      | `PIMYWA_EPAPER_BUSY_PIN` |
| PWR    | 18       | 12      | `PIMYWA_EPAPER_PWR_PIN` |
| MOSI   | 10       | 19      | (kernel SPI driver) |
| SCLK   | 11       | 23      | (kernel SPI driver) |
| CS0    | 8        | 24      | `PIMYWA_EPAPER_SPI_DEV` |

### 6. Waveshare driver

No `waveshare_epd` Python package is needed — the epd2in13_V4 register
protocol is implemented directly in `epaper/backend.py` using `spidev` + `gpiod`.

Reference: `waveshareteam/e-Paper`,
`RaspberryPi_JetsonNano/python/lib/waveshare_epd/epd2in13_V4.py` (MIT).

---

## Items that need on-Pi testing

- **Image rotation direction**: the renderer produces a 250x122 landscape
  image which `image_to_buffer()` rotates 90 degrees CCW to portrait 122x250.
  If the face appears sideways or upside-down, change the rotation angle in
  `epaper/backend.py::_PanelController.image_to_buffer()`.

- **BUSY pin polarity**: the driver polls until BUSY is LOW.  Some panel
  revisions may invert this — if init hangs, check the physical BUSY line.

- **Partial-refresh ghosting**: tunable via `PIMYWA_EPAPER_GHOST_PARTIALS`.
  Start with 30-50 partials (~9-15 minutes at the default 18 s idle interval).

- **PWR pin**: driven HIGH on init and LOW on close.  If your board has no
  PWR enable pin, set `PIMYWA_EPAPER_PWR_PIN=` (empty).

- **`anim_step` and QR**: the QR code changes only when `qr_data` changes;
  `anim_step` is irrelevant for that path.
