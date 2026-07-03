#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Camilo Brossard
"""Piumy power service — polls the battery backend, writes battery.json.

The actual requirement: "la bateria nunca la vi bajar... no
quiero ni un placeholder".

Single-writer architecture (design review, 2026-07-01): the Go core is
the ONLY writer of status.json — an earlier version of this service wrote
directly into it (read-modify-write), but that made it a SECOND writer with
a real race against the core's own writes (a power update landing between
the core reading a mood change and writing it back could clobber e.g.
mood="paused"/"error"). Instead, this service writes ONLY its own small
sidecar file (PIMYWA_BATTERY_FILE, default /run/pimywa/battery.json); the
core reads that back in on its own heartbeat and merges it into status.json
itself. This service never touches status.json at all.

A later revision (milestones C/D) extends the sidecar beyond the original
{"battery": N, "ts": <unix>} with the real cell voltage and an adaptive
time-remaining estimate (see timeremain.py) -- every new field follows the
same no-placeholder rule: null/absent when there is no real reading, never
a fabricated value.

Env knobs (all optional, zero hardcode)
----------------------------------------
PIMYWA_BATTERY_FILE      Path to the battery.json sidecar (default: /run/pimywa/battery.json)
PIMYWA_POWER_BACKEND     Backend name                  (default: none)
PIMYWA_POWER_I2C_BUS     I2C bus number (cw2015-i2c)    (default: 1)
PIMYWA_POWER_I2C_ADDR    I2C address, hex ok (cw2015)   (default: 0x62)
PIMYWA_POWER_INTERVAL    Poll interval, seconds         (default: 30.0)
PIMYWA_LOG_LEVEL         Logging level                  (default: INFO)

See timeremain.py's own module doc for the PIMYWA_BATTERY_CAPACITY_MAH /
PIMYWA_BATTERY_DRAW_MA / PIMYWA_BATTERY_LEARN_* knobs behind the adaptive
time-remaining estimate.

Low-battery safe shutdown is OUT OF SCOPE here (parent contract 0220 notes
it as a separate, future contract) -- this service only reports the charge
level, it never acts on it.
"""
import json
import logging
import os
import signal
import sys
import time

_HERE = os.path.dirname(os.path.abspath(__file__))
if _HERE not in sys.path:
    sys.path.insert(0, _HERE)

import backend as _backend_mod  # noqa: E402
from timeremain import TimeRemainingEstimator  # noqa: E402

_BATTERY_FILE = os.getenv("PIMYWA_BATTERY_FILE", "/run/pimywa/battery.json")
_INTERVAL     = float(os.getenv("PIMYWA_POWER_INTERVAL", "30.0"))

logging.basicConfig(
    level=os.getenv("PIMYWA_LOG_LEVEL", "INFO"),
    format="%(asctime)s %(levelname)s [%(name)s] %(message)s",
)
logger = logging.getLogger("pimywa.power")


def _write_battery_file(path: str, pct: int, voltage_mv, charging: bool,
                         time_remaining_min) -> None:
    """Atomically write the sidecar (tmp+rename — same technique as the Go
    core's state.Write, 3rd commandment: a power cut mid-write must never
    leave a half-written file). A FRESH file every time, not a
    read-modify-write -- there's nothing else in it to preserve. voltage_mv/
    time_remaining_min are None when there's no real reading (no-placeholder
    -- the core/renderer must never see a fabricated value)."""
    tmp = path + ".tmp"
    try:
        with open(tmp, "w", encoding="utf-8") as fh:
            json.dump({
                "battery": pct,
                "voltage_mv": voltage_mv,
                "charging": charging,
                "time_remaining_min": time_remaining_min,
                "ts": int(time.time()),
            }, fh)
        os.replace(tmp, path)
    except OSError as exc:
        logger.warning("could not write %s: %s", path, exc)


def run() -> None:
    """Main service loop. Blocks until SIGTERM or SIGINT."""
    backend_name = os.getenv("PIMYWA_POWER_BACKEND", "none")
    backend = _backend_mod.get_backend(backend_name)
    estimator = TimeRemainingEstimator()

    logger.info(
        "Power service started -- backend=%s  battery_file=%s  interval=%ss",
        backend_name, _BATTERY_FILE, _INTERVAL,
    )

    running = True

    def _stop(sig, _frame) -> None:
        nonlocal running
        logger.info("Signal %s received -- shutting down", sig)
        running = False

    signal.signal(signal.SIGTERM, _stop)
    signal.signal(signal.SIGINT, _stop)

    try:
        while running:
            pct = backend.read_soc()  # raw curve-based SoC (cw2015.py's LiPo curve)
            if pct is not None:
                voltage_mv = backend.read_voltage_mv()
                est = estimator.update(pct, voltage_mv, now=time.time())
                # "battery" (the bar) is the LINEARIZED estimate, not the raw
                # curve pct -- see timeremain.py's module doc ("Linearized
                # battery level") for why a voltage-derived percent looks
                # jumpy even when accurate. voltage_mv itself stays raw/real,
                # written separately, untouched by the blend.
                _write_battery_file(_BATTERY_FILE, est["battery_pct"], voltage_mv,
                                     est["charging"], est["minutes"])
                logger.debug(
                    "battery=%s%% (raw curve=%d%%)  voltage=%s  charging=%s  remaining=%smin -- written to %s",
                    est["battery_pct"], pct, voltage_mv, est["charging"], est["minutes"], _BATTERY_FILE,
                )
            # pct is None (backend=none, hardware absent, or a transient
            # read failure): deliberately skip the write -- see the
            # no-placeholder rule in backend.py's module doc. The core's
            # own staleness check (max-age on the "ts" field) is what turns
            # a stopped/absent power service into "no battery shown", not
            # an active write from here.
            time.sleep(_INTERVAL)
    finally:
        logger.info("Power service stopping")
        try:
            backend.close()
        except Exception as exc:
            logger.warning("backend.close() failed: %s", exc)
        logger.info("Power service stopped")


if __name__ == "__main__":
    run()
