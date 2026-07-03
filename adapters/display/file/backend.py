# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Camilo Brossard
"""File backend: writes the rendered face to a PNG (dev / CI / off-Pi).

The service calls render_image() and passes the PIL Image directly;
this backend just saves it to disk.  The ``full`` parameter is accepted
for interface compatibility but ignored (file writes are always "full").

Output path: ``PIMYWA_DISPLAY_OUT`` env (default: ``display.png``).
"""
import logging
import os

logger = logging.getLogger(__name__)


class FileBackend:
    """Writes a 1-bit PIL Image to a PNG file on disk.

    Env:
        PIMYWA_DISPLAY_OUT  Output path (default: display.png)
    """

    def show(self, image, full: bool = False) -> None:  # noqa: ARG002
        out = os.getenv("PIMYWA_DISPLAY_OUT", "display.png")
        image.save(out)
        logger.info("PNG written -> %s", out)

    def close(self) -> None:
        pass
