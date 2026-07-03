# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Camilo Brossard
"""Waveshare 2.13" V4 (epd2in13_V4) e-paper backend for Piumy.

Refresh policy (pwnagotchi-style — NO flash on small changes)
-------------------------------------------------------------
  Boot   : init() + Clear(0xFF) + displayPartBaseImage(white)  — ONE flash only.
  Normal : show(img, full=False) -> displayPartial(buf)         — zero flash.
  Big    : show(img, full=True)  -> init() + Clear + displayPartBaseImage(buf)
                                                                — deliberate flash.
  Anti-ghosting: after PIMYWA_EPAPER_GHOST_PARTIALS partials (default 0 =
           off), auto-trigger one full refresh to clear accumulated
           ghosting. OFF by default again (2026-07-02, after seeing it
           live: "el e-paper se pinta genial con parciales" -- the periodic
           flash was unwanted, not the ghosting itself). Opt back in via env
           if real ghosting shows up in practice.

The service decides full vs partial; this backend just executes the command.
render_image() is NOT called here — the service passes a PIL Image directly.

Hardware: spidev (SPI) + gpiod v2 (GPIO). Both imports are guarded so this
module is safe to import on a PC. If hardware is absent the backend degrades
to a no-op and logs a clear warning — it never raises or crash-loops.

Config env vars (zero hardcode):
    PIMYWA_EPAPER_RST_PIN          BCM GPIO pin for RESET         (default: 17)
    PIMYWA_EPAPER_DC_PIN           BCM GPIO pin for Data/Command  (default: 25)
    PIMYWA_EPAPER_BUSY_PIN         BCM GPIO pin for BUSY          (default: 24)
    PIMYWA_EPAPER_PWR_PIN          BCM GPIO pin for power enable  (default: 18)
                                   Set to empty string to skip PWR pin.
    PIMYWA_EPAPER_GPIOCHIP         GPIO chip name or /dev/ path   (default: gpiochip0)
    PIMYWA_EPAPER_SPI_DEV          SPI device path                (default: /dev/spidev0.0)
    PIMYWA_EPAPER_SPI_SPEED        SPI clock speed Hz             (default: 4000000)
    PIMYWA_EPAPER_GHOST_PARTIALS   Full refresh every N partials  (default: 0 = off)

Driver reference (MIT License):
    waveshareteam/e-Paper
    RaspberryPi_JetsonNano/python/lib/waveshare_epd/epd2in13_V4.py
    Commit 2023-06-25 — command sequences reproduced faithfully.
"""
import logging
import os
import sys
import time

logger = logging.getLogger(__name__)

# ── Configuration (env-driven, zero hardcode) ─────────────────────────────────
_RST_PIN        = int(os.getenv("PIMYWA_EPAPER_RST_PIN",  "17"))
_DC_PIN         = int(os.getenv("PIMYWA_EPAPER_DC_PIN",   "25"))
_BUSY_PIN       = int(os.getenv("PIMYWA_EPAPER_BUSY_PIN", "24"))
_PWR_PIN_S      = os.getenv("PIMYWA_EPAPER_PWR_PIN", "18")
_PWR_PIN        = int(_PWR_PIN_S) if _PWR_PIN_S.strip() else None
_GPIOCHIP       = os.getenv("PIMYWA_EPAPER_GPIOCHIP", "gpiochip0")
_SPI_DEV        = os.getenv("PIMYWA_EPAPER_SPI_DEV",  "/dev/spidev0.0")
_SPI_SPEED      = int(os.getenv("PIMYWA_EPAPER_SPI_SPEED", "4000000"))
_GHOST_PARTIALS = int(os.getenv("PIMYWA_EPAPER_GHOST_PARTIALS", "0"))

# Panel physical dimensions (portrait: 122 px wide x 250 px tall).
_PANEL_W = 122
_PANEL_H = 250


class _PanelController:
    """Low-level controller for the Waveshare 2.13" V4 panel.

    Implements the epd2in13_V4 register sequence verbatim using spidev for
    SPI transfers and gpiod (libgpiod v2) for GPIO control.

    Instantiate only inside try/except — the constructor imports hardware
    libraries that are unavailable on non-Pi hosts.
    """

    def __init__(self) -> None:
        # Hardware imports are intentionally inside __init__ so the module is
        # safe to import on a PC.  EPaperWaveshareBackend._try_init() wraps
        # this in a try/except and degrades to no-op on ImportError.
        import spidev                           # noqa: PLC0415
        import gpiod                            # noqa: PLC0415
        from gpiod.line import Direction, Value # noqa: PLC0415

        self._Value = Value

        # ── SPI setup ─────────────────────────────────────────────────────
        basename = os.path.basename(_SPI_DEV).replace("spidev", "")
        spi_bus, spi_cs = (int(p) for p in basename.split("."))
        self._spi = spidev.SpiDev()
        self._spi.open(spi_bus, spi_cs)
        self._spi.max_speed_hz = _SPI_SPEED
        self._spi.mode = 0b00

        # ── GPIO setup (gpiod v2) ─────────────────────────────────────────
        gpio_cfg: dict = {
            _RST_PIN:  gpiod.LineSettings(direction=Direction.OUTPUT,
                                          output_value=Value.INACTIVE),
            _DC_PIN:   gpiod.LineSettings(direction=Direction.OUTPUT,
                                          output_value=Value.INACTIVE),
            _BUSY_PIN: gpiod.LineSettings(direction=Direction.INPUT),
        }
        if _PWR_PIN is not None:
            gpio_cfg[_PWR_PIN] = gpiod.LineSettings(
                direction=Direction.OUTPUT, output_value=Value.INACTIVE
            )

        chip_path = _GPIOCHIP if _GPIOCHIP.startswith("/") else f"/dev/{_GPIOCHIP}"
        self._gpio = gpiod.request_lines(
            chip_path, consumer="pimywa-epaper", config=gpio_cfg,
        )

        if _PWR_PIN is not None:
            self._gpio.set_value(_PWR_PIN, Value.ACTIVE)

    # ── GPIO helpers ──────────────────────────────────────────────────────────

    def _high(self, pin: int) -> None:
        self._gpio.set_value(pin, self._Value.ACTIVE)

    def _low(self, pin: int) -> None:
        self._gpio.set_value(pin, self._Value.INACTIVE)

    def _wait_busy(self) -> None:
        """Block until BUSY pin goes LOW (panel ready to accept commands)."""
        while self._gpio.get_value(_BUSY_PIN) == self._Value.ACTIVE:
            time.sleep(0.010)

    # ── SPI helpers ───────────────────────────────────────────────────────────

    def _cmd(self, cmd: int) -> None:
        self._low(_DC_PIN)
        self._spi.writebytes([cmd])

    def _dat(self, data) -> None:
        self._high(_DC_PIN)
        if isinstance(data, int):
            self._spi.writebytes([data])
        elif isinstance(data, (bytes, bytearray)):
            self._spi.writebytes2(data)
        else:
            self._spi.writebytes2(bytearray(data))

    # ── Panel commands ────────────────────────────────────────────────────────

    def _reset(self) -> None:
        self._high(_RST_PIN); time.sleep(0.020)
        self._low(_RST_PIN);  time.sleep(0.002)
        self._high(_RST_PIN); time.sleep(0.020)

    def _set_window(self, x0: int, y0: int, x1: int, y1: int) -> None:
        self._cmd(0x44)
        self._dat([(x0 >> 3) & 0xFF, (x1 >> 3) & 0xFF])
        self._cmd(0x45)
        self._dat([y0 & 0xFF, (y0 >> 8) & 0xFF, y1 & 0xFF, (y1 >> 8) & 0xFF])

    def _set_cursor(self, x: int, y: int) -> None:
        self._cmd(0x4E); self._dat(x & 0xFF)
        self._cmd(0x4F); self._dat([y & 0xFF, (y >> 8) & 0xFF])

    def _turn_on_full(self) -> None:
        self._cmd(0x22); self._dat(0xF7)
        self._cmd(0x20); self._wait_busy()

    def _turn_on_partial(self) -> None:
        self._cmd(0x22); self._dat(0xFF)
        self._cmd(0x20); self._wait_busy()

    # ── Public interface ──────────────────────────────────────────────────────

    def init(self) -> None:
        """Full hardware init (epd2in13_V4 startup sequence)."""
        self._reset()
        self._wait_busy()
        self._cmd(0x12)                        # Software reset
        self._wait_busy()
        self._cmd(0x01); self._dat([0xF9, 0x00, 0x00])  # Driver output control
        self._cmd(0x11); self._dat(0x03)       # Data entry mode: X inc, Y inc
        self._set_window(0, 0, _PANEL_W - 1, _PANEL_H - 1)
        self._set_cursor(0, 0)
        self._cmd(0x3C); self._dat(0x05)       # Border waveform control
        self._cmd(0x21); self._dat([0x00, 0x80])  # Display update control
        self._cmd(0x18); self._dat(0x80)       # Built-in temperature sensor
        self._wait_busy()

    def image_to_buffer(self, img) -> bytearray:
        """Convert a PIL Image to the panel's raw byte buffer.

        Accepts 250x122 landscape (rotated 90 CCW) or 122x250 portrait as-is.
        Returns bytearray of ceil(122/8)*250 = 4000 bytes, 1 bpp MSB-first.
        """
        from PIL import Image as _Image
        w, h = img.size
        if w == _PANEL_H and h == _PANEL_W:        # landscape 250x122
            img = img.rotate(90, expand=True)       # CCW -> portrait 122x250
        if img.size != (_PANEL_W, _PANEL_H):
            logger.warning(
                "Panel expects %dx%d; got %dx%d — sending blank white buffer",
                _PANEL_W, _PANEL_H, *img.size,
            )
            return bytearray([0xFF] * ((_PANEL_W + 7) // 8 * _PANEL_H))
        return bytearray(img.convert("1").tobytes("raw"))

    def clear(self, color: int = 0xFF) -> None:
        """Fill the display with *color* (0xFF = white, 0x00 = black)."""
        lw = (_PANEL_W + 7) // 8
        self._cmd(0x24)
        self._dat(bytearray([color] * (_PANEL_H * lw)))
        self._turn_on_full()

    def display_part_base(self, buf: bytearray) -> None:
        """Full refresh — sets both RAM planes to *buf* (the partial baseline).

        Must be called at boot and after any deliberate full refresh to
        re-anchor the partial-refresh comparator to the new image.
        """
        self._cmd(0x24); self._dat(buf)   # current plane
        self._cmd(0x26); self._dat(buf)   # previous plane (partial baseline)
        self._turn_on_full()

    def display_partial(self, buf: bytearray) -> None:
        """Fast partial refresh — no full-panel flash; only changed pixels update.

        The comparator uses the image last written via display_part_base().
        Ghosting accumulates over time; see PIMYWA_EPAPER_GHOST_PARTIALS.
        """
        # Brief reset pulse to enter partial mode (driver reference pattern)
        self._low(_RST_PIN);  time.sleep(0.001)
        self._high(_RST_PIN)
        self._cmd(0x3C); self._dat(0x80)              # Border waveform (partial)
        self._cmd(0x01); self._dat([0xF9, 0x00, 0x00])  # Driver output
        self._cmd(0x11); self._dat(0x03)              # Data entry mode
        self._set_window(0, 0, _PANEL_W - 1, _PANEL_H - 1)
        self._set_cursor(0, 0)
        self._cmd(0x24); self._dat(buf)               # Write current RAM
        self._turn_on_partial()

    def sleep(self) -> None:
        """Enter deep sleep (minimum power). Hardware reset required to wake."""
        self._cmd(0x10); self._dat(0x01)
        time.sleep(2.0)

    def close(self) -> None:
        """Release SPI and GPIO resources (idempotent)."""
        try:
            if _PWR_PIN is not None:
                self._gpio.set_value(_PWR_PIN, self._Value.INACTIVE)
            self._gpio.release()
        except Exception:
            pass
        try:
            self._spi.close()
        except Exception:
            pass


# ── Public backend class ──────────────────────────────────────────────────────

class EPaperWaveshareBackend:
    """Waveshare 2.13" V4 display backend for Piumy.

    Refresh policy
    --------------
    Boot  : init() + Clear(0xFF) + displayPartBaseImage(white) — one flash only.
    Normal: show(img, full=False)  -> displayPartial()          — no flash.
    Big   : show(img, full=True)   -> init()+Clear+displayPartBaseImage() — flash.

    Anti-ghosting (optional):
    If PIMYWA_EPAPER_GHOST_PARTIALS > 0, a full refresh runs automatically
    every N partial refreshes to clear accumulated ghost pixels.

    The backend does NOT call render_image(); the caller passes a PIL Image.
    If hardware is absent the backend silently degrades to a no-op.
    """

    def __init__(self) -> None:
        self._hw: _PanelController = None   # type: ignore[assignment]
        self._available: bool = False
        self._partial_count: int = 0
        self._try_init()

    def _try_init(self) -> None:
        try:
            self._hw = _PanelController()
            self._hw.init()
            self._hw.clear(0xFF)            # white screen

            # Set partial-refresh baseline to all-white so the first
            # partial can diff against a known clean state.
            lw  = (_PANEL_W + 7) // 8
            white_buf = bytearray([0xFF] * (_PANEL_H * lw))
            self._hw.display_part_base(white_buf)   # ONE boot flash

            self._available = True
            self._partial_count = 0
            logger.info(
                'Waveshare 2.13" V4 ready — panel %dx%d (portrait), '
                "SPI %s @ %d Hz, chip %s",
                _PANEL_W, _PANEL_H, _SPI_DEV, _SPI_SPEED, _GPIOCHIP,
            )
        except ImportError as exc:
            logger.warning(
                "spidev or gpiod not importable (%s) — "
                "epaper backend is a no-op. "
                "On the Pi: sudo apt install python3-spidev python3-gpiod",
                exc,
            )
        except Exception as exc:
            logger.warning("e-paper hardware init failed (%s) — backend is a no-op.", exc)
            if self._hw is not None:
                try:
                    self._hw.close()
                except Exception:
                    pass
                self._hw = None  # type: ignore[assignment]

    def show(self, image, full: bool = False) -> None:
        """Push *image* (PIL Image, 250x122 landscape) to the panel.

        full=False  — partial refresh (fast, no flash, preferred for animation).
        full=True   — full refresh (init+clear+base): deliberate flash for big
                      transitions (boot handled in __init__, not here).

        Anti-ghosting: if PIMYWA_EPAPER_GHOST_PARTIALS > 0 and the partial
        counter reaches that threshold, a full refresh fires automatically and
        resets the counter.
        """
        if not self._available or self._hw is None:
            return
        try:
            buf = self._hw.image_to_buffer(image)

            if full:
                self._hw.init()
                self._hw.clear(0xFF)
                self._hw.display_part_base(buf)
                self._partial_count = 0
                logger.debug("e-paper: full refresh (deliberate)")
            else:
                # Anti-ghosting check
                if _GHOST_PARTIALS > 0 and self._partial_count >= _GHOST_PARTIALS:
                    self._hw.init()
                    self._hw.clear(0xFF)
                    self._hw.display_part_base(buf)
                    self._partial_count = 0
                    logger.debug(
                        "e-paper: anti-ghost full refresh after %d partials",
                        _GHOST_PARTIALS,
                    )
                else:
                    self._hw.display_partial(buf)
                    self._partial_count += 1
                    logger.debug(
                        "e-paper: partial refresh #%d", self._partial_count
                    )

        except Exception as exc:
            logger.error("e-paper show() failed: %s", exc)

    def close(self) -> None:
        """Sleep the panel and release all hardware resources."""
        if self._hw is None:
            return
        try:
            self._hw.sleep()
            logger.info("e-paper panel in deep sleep")
        except Exception as exc:
            logger.warning("e-paper sleep() failed: %s", exc)
        finally:
            try:
                self._hw.close()
            except Exception:
                pass
            self._hw = None  # type: ignore[assignment]
            self._available = False
