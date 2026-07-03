# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Camilo Brossard
"""Display backend interface and factory for Piumy.

The service renders a PIL Image via render_image() and passes it to the
backend's show() method.  Backends handle only output (write file / drive
e-paper / no-op); they do NOT call render_image() themselves.

Usage::

    from backend import get_backend

    be = get_backend()              # reads PIMYWA_DISPLAY env
    img = render_image(status)      # from render module
    be.show(img, full=False)        # partial or full refresh
    be.close()                      # release resources

Supported backend names (env ``PIMYWA_DISPLAY``):
    file              -- writes a PNG (dev / CI / off-Pi); default
    epaper-waveshare  -- Waveshare 2.13" V4 panel via SPI
    none              -- no-op (headless operation)
"""
import logging
import os
import sys

_HERE = os.path.dirname(os.path.abspath(__file__))
if _HERE not in sys.path:
    sys.path.insert(0, _HERE)

logger = logging.getLogger(__name__)


class BaseBackend:
    """Minimal interface all display backends must implement."""

    def show(self, image, full: bool = False) -> None:
        """Push *image* (PIL Image, 250x122 landscape) to the output.

        full=False  Partial refresh (no flash) — used for idle animation ticks
                    and minor content changes.
        full=True   Full refresh (flash) — used for big mood transitions
                    (entering/leaving qr, error, sleeping) and boot.
        """
        raise NotImplementedError

    def close(self) -> None:
        """Release hardware resources (idempotent)."""


def get_backend(name: str = None) -> BaseBackend:
    """Return a backend instance selected by *name* (or ``PIMYWA_DISPLAY`` env).

    Falls back to ``none`` for unrecognised names — logs a warning, never raises.
    """
    if name is None:
        name = os.getenv("PIMYWA_DISPLAY", "file")
    key = name.lower().strip().replace("-", "_")

    if key == "file":
        from file.backend import FileBackend    # type: ignore[import]
        return FileBackend()

    if key in ("epaper_waveshare", "epaper"):
        from epaper.backend import EPaperWaveshareBackend  # type: ignore[import]
        return EPaperWaveshareBackend()

    if key == "none":
        return _NoneBackend()

    logger.warning("Unknown PIMYWA_DISPLAY=%r -- falling back to none", name)
    return _NoneBackend()


class _NoneBackend(BaseBackend):
    """No-op backend for headless operation."""

    def show(self, image, full: bool = False) -> None:
        pass

    def close(self) -> None:
        pass
