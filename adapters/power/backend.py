# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Camilo Brossard
"""Power (battery) backend interface and factory for Piumy.

The actual requirement, "no quiero ni un
placeholder" — the prior state.Battery was NEVER set by the core (a stub);
render.py's card just drew a fake 100%. This module reads a REAL fuel gauge
and reports either a real percent or nothing at all — never a fabricated
value.

Usage::

    from backend import get_backend

    be = get_backend()              # reads PIMYWA_POWER_BACKEND env
    pct = be.read_soc()             # int 0-100, or None (no reading available)
    be.close()

Supported backend names (env ``PIMYWA_POWER_BACKEND``):
    cw2015-i2c  -- CW2015 fuel gauge over the generic i2c-dev subsystem
    none        -- no-op (default; no battery hardware, or not confirmed present)

Hardware access is via ``smbus2`` — a generic Python binding for the Linux
i2c-dev character device (NOT a CW2015-vendor SDK), same "generic Linux
interface, no vendor libs" rule already applied to spidev (SPI) and gpiod
(GPIO) in adapters/display/epaper/backend.py. The import is guarded so this
module stays importable (and unit-testable) on a machine with no I2C
hardware or smbus2 installed at all.

Low-battery safe shutdown is OUT OF SCOPE for this contract (noted, not
built) — tracked as a separate future contract.
"""
import logging
import os

logger = logging.getLogger(__name__)


class BasePowerBackend:
    """Minimal interface all power backends must implement."""

    def read_soc(self):
        """Return the battery state-of-charge as an int percent (0-100), or
        None if no real reading is available right now (hardware absent,
        read failed, or backend=none). Callers MUST treat None as "don't
        show battery at all" — never substitute a default."""
        raise NotImplementedError

    def read_voltage_mv(self):
        """Return the real cell voltage in millivolts (milestone C), or
        None on the same "no real reading" terms as
        read_soc(). Default no-op — only CW2015Backend overrides this."""
        return None

    def close(self) -> None:
        """Release hardware resources (idempotent)."""


class _NonePowerBackend(BasePowerBackend):
    """No-op backend — no battery hardware, or not confirmed present."""

    def read_soc(self):
        return None

    def close(self) -> None:
        pass


def get_backend(name: str = None) -> BasePowerBackend:
    """Return a power backend selected by *name* (or ``PIMYWA_POWER_BACKEND``
    env). Falls back to ``none`` for unrecognised names or a hardware/import
    failure — logs a warning, never raises (same fail-safe contract as
    adapters/display/backend.get_backend)."""
    if name is None:
        name = os.getenv("PIMYWA_POWER_BACKEND", "none")
    key = name.lower().strip().replace("-", "_")

    if key == "none":
        return _NonePowerBackend()

    if key == "cw2015_i2c":
        from cw2015 import CW2015Backend  # noqa: PLC0415 (local import, see module doc)
        return CW2015Backend()

    logger.warning("Unknown PIMYWA_POWER_BACKEND=%r -- falling back to none", name)
    return _NonePowerBackend()
