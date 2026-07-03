# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Camilo Brossard
"""CW2015 fuel-gauge backend for Piumy.

The CW2015 (used on common Pi-Zero "UPS-Lite" HATs) is a dedicated gas-gauge
IC that CAN compute its own state-of-charge (REG_SOC, 0x04) -- but that
on-chip estimate depends on a battery-profile table (BATINFO) programmed
into the chip for the EXACT cell it's paired with. Cheap/generic "UPS-Lite"
boards commonly ship with a default or mismatched profile, so REG_SOC can
read unrealistically flat or jump around on a battery it was never
characterized for.

The reasoning behind this (2026-07-02): a single-cell LiPo's
OCV discharge curve is NOT linear -- it sags fast from 4.20V down to
~4.0V in the first ~10-15% of capacity, then sits on a long, flat plateau
through roughly 3.7-3.9V for most of the discharge, then drops fast again
near empty. A cheap/uncalibrated gauge (or a naive linear voltage-to-percent
mapping) gets this badly wrong in exactly this way.

So instead of trusting REG_SOC, this reads the raw cell voltage (REG_VCELL,
0x02/0x03) and maps it through an explicit 1S-LiPo discharge curve
(_LIPO_CURVE, voltage -> percent, linearly interpolated between points) --
same idea as most hobbyist LiPo fuel-gauge projects use for exactly this
"generic gauge, unknown/cheap cell" situation.

Register scaling (VCELL) follows the widely-used CW2015 driver convention
(the same "linshuqin329/UPS-Lite"-style reference code cloned across many
Pi UPS-HAT projects): a 14-bit ADC value in a 16-bit register (MSB at 0x02,
LSB at 0x03), LSB = 305 uV. This is implemented from that documented
convention, NOT verified against real silicon here (no hardware on this dev
machine) -- same honesty as before: the contract scopes real I2C
verification to the Pi at deploy time, treat _VCELL_STEP_UV as the first
thing to double-check if voltage readings look wrong there (compare
against a multimeter on the battery terminals).

KNOWN LIMITATION (2026-07-02) — VCELL sags under load:
reading raw voltage directly (instead of trusting the chip's own
coulomb-counting-compensated SOC, which we deliberately don't trust here —
see above) means the reported percent will read LOWER than the true SOC
while the Pi/panel are actively drawing current, recovering once the load
drops and the cell's voltage relaxes back toward its open-circuit value.
This is the known tradeoff of a direct-voltage method vs. a real fuel
gauge's coulomb counting. NOT worked around here (YAGNI — don't add
speculative smoothing before knowing it's actually needed): if the percent
visibly jitters/jumps under real load on the Pi, the fix is a lightweight
EMA (exponential moving average) over the last few read_soc() calls in
CW2015Backend, not a change to the curve itself. Treat that as the next
calibration knob to reach for, alongside _LIPO_CURVE's generic mV anchors
and _VCELL_STEP_UV above -- all three get tuned together against the real
battery, not before.

_CW2015Sensor takes *bus* as a duck-typed dependency (only needs
read_byte_data(addr, reg)) specifically so the unit test (test_cw2015.py)
can inject a fake bus with zero real I2C hardware or the smbus2 package —
CW2015Backend is what actually constructs a real smbus2.SMBus, and only
when this module is actually used (lazy import in _try_init, same pattern
as EPaperWaveshareBackend importing spidev/gpiod there).
"""
import logging
import os

from backend import BasePowerBackend

logger = logging.getLogger(__name__)

# ── Configuration (env-driven, zero hardcode) ──────────────────────────────
_I2C_BUS  = int(os.getenv("PIMYWA_POWER_I2C_BUS", "1"))
_I2C_ADDR = int(os.getenv("PIMYWA_POWER_I2C_ADDR", "0x62"), 0)

_REG_VCELL = 0x02  # cell voltage: MSB then LSB, 14-bit ADC value, LSB=305uV
_REG_SOC   = 0x04  # on-chip SOC estimate -- NOT used for the percent we
                    # report (see module doc); kept only as a reference in
                    # case a future contract wants to cross-check against it.
_REG_MODE  = 0x0A  # power mode -- bits [7:6] are SLEEP; see _wake()'s doc.
_VCELL_STEP_UV = 305  # microvolts per ADC count

# Follow-up (real-Pi diagnosis, 2026-07-02): the battery was
# NEVER actually disconnected -- the CW2015 on this UPS-Lite boots with
# MODE(0x0A) SLEEP bits set (probed on the real Pi: MODE=0xC0), and a
# sleeping chip reads VCELL=0 regardless of the real cell voltage. Confirmed
# by hand on the Pi (2026-07-02): writing MODE=0x00 immediately turned
# VCELL=0 into a real 4067mV/88% reading. So "VCELL below the plausible
# floor" doesn't ALWAYS mean disconnected/dead -- it can just mean asleep --
# hence the wake-and-reread in _CW2015Sensor.read_soc() below, not only the
# one-shot wake at init.

# Bug found in production on the real Pi (2026-07-02): the
# CW2015 read VCELL=0 (0.000V) with intermittent I2C I/O errors -- on a
# UPS-Lite the chip is powered from VBAT, so 0V/garbage means the battery is
# disconnected or dead, or the read itself is junk. A real LiPo NEVER reads
# near 0V (even a fully depleted, protection-tripped cell sits around
# 3.0-3.3V -- see _LIPO_CURVE's own 3400mV floor). Reporting 0% here would
# be exactly the placeholder this project avoids ("no quiero ni un
# placeholder") -- 0% looks like a real, if extreme, reading, when it's
# actually "no signal at all". Anything at or below this floor is treated
# as an INVALID reading (read_soc returns None -> unknown), not a real low
# battery. A genuinely near-empty LiPo (~3.0-3.3V) reads comfortably above
# this floor and still gets its correct low-but-nonzero percent from the
# curve.
_VCELL_MIN_PLAUSIBLE_MV = 2500

# 1S LiPo open-circuit-voltage discharge curve: (millivolts, percent),
# HIGHEST voltage first. Deliberately NOT linear -- shaped to match the
# well-known LiPo/Li-ion discharge behavior described above: a STEEP
# knee near full charge (a small amount of used capacity swings the
# voltage a lot, e.g. 4200->4100mV for the top 10%), a FLAT plateau
# through the middle (a large amount of capacity drains for only a small
# voltage change), and a steep knee again near empty. Exact millivolt
# anchors are from commonly-published generic 1S LiPo charts, not this
# specific pack's datasheet -- like _VCELL_STEP_UV above, treat these as
# a reasonable starting shape to sanity-check/recalibrate against the real
# battery on the Pi, not a guaranteed-precise reading.
#
# Recalibrated 2026-07-02 from a real full discharge on this pack:
# it ran all the way down to ~3.04V (so the old 3400mV=0% floor was pessimistic
# -- it was calling the battery empty with real capacity left). Floor extended
# to 3000mV=0% (the true loaded cutoff he observed), and the top knee anchored
# to his own datapoint (~10 min from full to 3.98V -> 3980mV pinned at 80%).
# Still a hand-calibration from sparse points, NOT a logged curve: the precise
# per-cell shape needs the discharge LOG (contract item F) running through a
# full cycle. This is just less wrong than before, using his real endpoints.
_LIPO_CURVE = (
    (4200, 100), (4100, 90), (3980, 80), (3910, 70), (3860, 60),
    (3820, 50), (3790, 40), (3760, 30), (3720, 20), (3650, 10),
    (3400, 4), (3000, 0),
)


def _voltage_to_percent(mv: int) -> int:
    """Map a cell voltage (millivolts) to a percent via _LIPO_CURVE, linearly
    interpolating between the table's points. Clamps outside the table's
    range (>=4200mV -> 100, <=3270mV -> 0) rather than extrapolating --
    a curve is only meaningful within the range it was built from."""
    if mv >= _LIPO_CURVE[0][0]:
        return 100
    if mv <= _LIPO_CURVE[-1][0]:
        return 0
    for (v_hi, p_hi), (v_lo, p_lo) in zip(_LIPO_CURVE, _LIPO_CURVE[1:]):
        if v_lo <= mv <= v_hi:
            # Linear interpolation within this segment of the curve.
            span = v_hi - v_lo
            frac = (mv - v_lo) / span if span else 0.0
            return round(p_lo + frac * (p_hi - p_lo))
    return 0  # unreachable given the range checks above; defensive only


class _CW2015Sensor:
    """Register-level CW2015 reader over a generic SMBus-compatible *bus*."""

    def __init__(self, bus, addr: int = _I2C_ADDR) -> None:
        self.bus = bus
        self.addr = addr

    def read_voltage_mv(self) -> int:
        """Return the cell's open-circuit-ish voltage in millivolts, per
        the register convention in this module's doc comment."""
        msb = self.bus.read_byte_data(self.addr, _REG_VCELL)
        lsb = self.bus.read_byte_data(self.addr, _REG_VCELL + 1)
        raw = (msb << 8) | lsb
        return raw * _VCELL_STEP_UV // 1000

    def _wake(self) -> None:
        """Clear the CW2015's SLEEP bits (REG_MODE=0x00) -- this UPS-Lite
        boots asleep, which reads VCELL=0 no matter the real cell voltage
        (verified on the real Pi, 2026-07-02: MODE was 0xC0 at boot; writing
        0x00 immediately produced a real 4067mV/88% reading). Idempotent and
        cheap -- safe to call defensively, not just once at construction."""
        self.bus.write_byte_data(self.addr, _REG_MODE, 0x00)

    def read_soc(self):
        """Return state-of-charge as an integer percent, computed from the
        raw cell voltage via the LiPo discharge curve (see module doc for
        why this is preferred over the chip's own REG_SOC) — clamped to
        [0, 100] by _voltage_to_percent itself. Returns None (NOT 0) if the
        voltage is STILL implausible for a real LiPo cell after one wake +
        reread attempt (see _VCELL_MIN_PLAUSIBLE_MV's doc comment) — a
        disconnected battery, a garbage I2C read, or the chip having gone
        back to sleep (e.g. after a power cut) must never masquerade as "0%
        charge"."""
        mv = self.read_voltage_mv()
        if mv < _VCELL_MIN_PLAUSIBLE_MV:
            logger.warning(
                "CW2015 VCELL=%dmV is below the plausible LiPo floor (%dmV) -- "
                "the chip may just be asleep, trying one wake + reread",
                mv, _VCELL_MIN_PLAUSIBLE_MV,
            )
            self._wake()
            mv = self.read_voltage_mv()
            if mv < _VCELL_MIN_PLAUSIBLE_MV:
                logger.warning(
                    "CW2015 VCELL=%dmV still below the plausible LiPo floor "
                    "(%dmV) after a wake attempt -- treating as no reading "
                    "(battery disconnected/dead or a bad I2C read), not 0%%",
                    mv, _VCELL_MIN_PLAUSIBLE_MV,
                )
                return None
        return _voltage_to_percent(mv)


class CW2015Backend(BasePowerBackend):
    """CW2015 power backend — degrades to "no reading" (None) if smbus2 or
    the hardware itself is unavailable; never raises, never crash-loops
    (same contract as EPaperWaveshareBackend)."""

    def read_voltage_mv(self):
        """Return the real cell voltage in millivolts (milestone C:
        "exponer voltaje real"), or None on the same terms as
        read_soc() -- no sensor, a transient I2C error, or a reading below
        the plausible-LiPo floor (see _CW2015Sensor.read_soc's doc) all mean
        "no reading right now", never a fabricated value."""
        if self._sensor is None:
            return None
        try:
            mv = self._sensor.read_voltage_mv()
        except Exception as exc:
            logger.warning("CW2015 voltage read failed: %s", exc)
            return None
        return mv if mv >= _VCELL_MIN_PLAUSIBLE_MV else None

    def __init__(self) -> None:
        self._bus = None
        self._sensor: _CW2015Sensor = None  # type: ignore[assignment]
        self._try_init()

    def _try_init(self) -> None:
        try:
            import smbus2  # noqa: PLC0415 (guarded — see module doc)
            self._bus = smbus2.SMBus(_I2C_BUS)
            self._sensor = _CW2015Sensor(self._bus, _I2C_ADDR)
            try:
                self._sensor._wake()
            except Exception as exc:
                # Not fatal -- read_soc()'s own defensive wake-and-reread
                # (see _CW2015Sensor.read_soc) will try again on the first
                # implausible reading, so a transient failure here just
                # means the FIRST poll might come back empty instead of
                # this one-time head start.
                logger.warning("CW2015 wake-on-init failed (%s) -- will retry on first low reading", exc)
            logger.info(
                "CW2015 fuel gauge ready -- i2c bus %d, addr 0x%02x",
                _I2C_BUS, _I2C_ADDR,
            )
        except ImportError as exc:
            logger.warning(
                "smbus2 not importable (%s) -- power backend is a no-op. "
                "On the Pi: pip3 install smbus2 (or apt python3-smbus2).",
                exc,
            )
        except Exception as exc:
            logger.warning("CW2015 I2C init failed (%s) -- power backend is a no-op.", exc)
            self._sensor = None
            if self._bus is not None:
                try:
                    self._bus.close()
                except Exception:
                    pass
                self._bus = None

    def read_soc(self):
        if self._sensor is None:
            return None
        try:
            return self._sensor.read_soc()
        except Exception as exc:
            # A transient I2C error (bus noise, device busy) is common and
            # NOT fatal -- log and report "no reading this cycle" so the
            # service can keep the display's last-known value rather than
            # flapping the battery icon on/off every failed poll.
            logger.warning("CW2015 read failed: %s", exc)
            return None

    def close(self) -> None:
        if self._bus is not None:
            try:
                self._bus.close()
            except Exception:
                pass
            self._bus = None
        self._sensor = None
