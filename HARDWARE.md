# Hardware & portability

Piumy runs on any ARM64 board with Linux. The core is board-agnostic; everything
board-specific lives in **adapters** and in **config** (zero hardcode).

## Reference boards

| Board | RAM | Role in the project |
|-------|-----|---------------------|
| Raspberry Pi Zero 2 W | 512 MB | switchboard + e-paper (main target, only board for now) |
| Raspberry Pi 3 A+ | 512 MB | spare / portability test target |

The brain (AI agent) connects over MCP from your PC or any MCP client; no dedicated
box for now. (A dedicated board such as an ODROID-C2 is left for another project.)

Golden rule: **the AI never runs on the e-paper board.** That board only routes + draws.

## Interfaces (generic Linux, not vendor-locked)

- **SPI** → e-paper. Via `spidev` (`/dev/spidev*`).
- **GPIO** (panel reset/dc/busy) → via `libgpiod` (`/dev/gpiochip*`), **not** `RPi.GPIO`.
- **I2C** → CW2015 battery gauge. Via `i2c-dev` (`/dev/i2c-*`).

On Raspberry Pi OS, enable in `/boot/firmware/config.txt`:

```
dtparam=spi=on
dtparam=i2c_arm=on
```

(On other boards: the equivalent device-tree / overlay.)

## Peripherals

- **Display:** Waveshare e-paper 2.13" V4 (122×250 physical; rendered as 250×122 and rotated).
- **Battery:** UPS-Lite with CW2015 on I2C bus 1, address `0x62`.

## Physical controls (MVP)

Three human controls, read by a small button adapter (Python via `libgpiod` — never
`RPi.GPIO`), debounced, **pins in config (zero hardcode)**. Each maps to an existing
action, so the core needs little or no change:

- **"Connect" push-button (momentary)** — context-aware from `status.json`: WhatsApp
  not linked → show the WhatsApp **QR** on the e-paper; already linked → show the agent
  pairing **pinpass** (future, see `PARKED-AUTH-PAIRING.md`). Out-of-band by design:
  only physical access to the board can request a pairing code.
- **Kill switch (maintained)** — stops the bot from **sending** replies (the anti-ban
  kill switch, `POST /api/killswitch`); it stays connected and receiving. Read as
  **set-to-position** (up = replying, down = muted), **not** toggle — so a dashboard
  change never inverts the switch's polarity. The switch is **authoritative** (a
  dashboard toggle is reverted on the next read). The anti-ban systems run *always*;
  this only gates responses.
- **Shutdown push-button (long-press ~2–3 s)** — triggers a clean `poweroff` (never
  pull power directly — SD corruption, 3rd commandment). The e-paper shows the
  **sleeping** face = safe to unplug. Also available from the dashboard, and
  automatically on low battery via the power adapter (future, gap #9).

Safe default (anti-ban): if the adapter can't read a control (boot, GPIO error), the
kill defaults to **muted** — never "reply just in case".

### Reference pins & wiring (MVP)

The e-paper uses BCM 8/10/11/17/24/25 and the CW2015 uses BCM 2/3 — the rest is free.
All pins are **config, not hardcoded**; swap for whatever is physically reachable.

| Control | BCM | Header pin | Nearby GND |
|---|---|---|---|
| "Connect" push-button | GPIO5 | 29 | 30 |
| Kill switch | GPIO6 | 31 | 34 |
| Shutdown push-button | GPIO13 | 33 | 39 |

**No external resistors** — each line uses the Pi's internal pull-up (a `libgpiod` bias
flag). Wiring is just `GPIO → button/switch → GND`: idle reads HIGH, pressed/closed
reads LOW (active-low). Optional 0.1 µF across a contact if it bounces. The kill switch
is wired so **open/floating = HIGH = muted** — a cut wire fails safe.

Verify against the actual board: (1) your exact e-paper HAT variant doesn't reuse BCM
5/6/13 (some add their own buttons — check its schematic); (2) physical access to the
header (a stacking/pass-through header, or solder to the pads).

## E-paper face reflects state (pwnagotchi-style)

The face reacts to `status.json` (never a dead screen). Beyond the existing moods, it
must show:

- **muted** (kill engaged) — silenced. This is *also the visual source of truth* for
  the kill state, so a stale physical switch position never lies about what's happening.
- **wants to attend** (pending chats waiting) — wakes up, eager: "chats waiting!".
- **sleeping** (shutdown / deep idle) — doubles as the safe-to-unplug indicator.
- **qr / pinpass** (connect button) — shows the code to scan/type.
- **alert** (anti-ban reconnect pause), **error**, etc.
- **idle / online greeting:** *"pimywa online"*.

All of this rides the `status.json` contract (core writes state → display adapter draws
the face), so the core work is buildable now and the face rendering is dev-testable via
the `file` backend before any panel exists.

## Checklist to port to a new board

1. Install ARM64 Linux + enable SPI/I2C (per the board).
2. In `config`/`.env`: set the SPI device, the `gpiochip`, the pin numbers (reset/dc/busy), and the I2C bus and address.
3. Choose the display adapter backend (`epaper-waveshare`) and power backend (`cw2015-i2c`), or `none`/`file` if there is no hardware.
4. Build the core (`go build`) and run the adapter (Python).
5. Verify with the `file` backend first (generates a PNG) before connecting the real panel.

> No pins or buses are hardcoded: it is all config. If something cannot be
> configured, that is a portability bug.
