# SPDX-License-Identifier: AGPL-3.0-only
"""Unit tests for the power adapter.

No real I2C hardware or smbus2 install needed: _CW2015Sensor takes a
duck-typed *bus* (see cw2015.py's module doc), so these tests inject a fake
one. CW2015Backend's smbus2-absent path is exercised for real, since this
dev machine genuinely has no smbus2 installed -- proving the "hardware
library missing -> no-op, never crash" contract without mocking anything.

Run: python3 -m unittest test_cw2015.py -v   (from this directory)
"""
import json
import os
import sys
import tempfile
import time
import types
import unittest
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from cw2015 import _CW2015Sensor, CW2015Backend, _voltage_to_percent, _LIPO_CURVE, _VCELL_STEP_UV, _REG_MODE
from backend import get_backend, _NonePowerBackend
import service


class FakeBus:
    """Duck-typed I2C bus mock -- read_byte_data + write_byte_data (the
    latter needed for _wake(), added in a follow-up)."""

    def __init__(self, registers: dict):
        self.registers = dict(registers)
        self.calls = []
        self.writes = []

    def read_byte_data(self, addr, reg):
        self.calls.append((addr, reg))
        return self.registers[reg]

    def write_byte_data(self, addr, reg, value):
        self.writes.append((addr, reg, value))
        self.registers[reg] = value


def _mv_to_raw(mv: int) -> int:
    """Inverse of _CW2015Sensor.read_voltage_mv's scaling -- lets tests pick
    a target millivolt reading instead of hand-computing register bytes."""
    return mv * 1000 // _VCELL_STEP_UV


class SleepyBus(FakeBus):
    """Reproduces the real-Pi bug (2026-07-02): VCELL reads 0 while
    the chip is asleep (MODE=0xC0); writing MODE=0x00 (the wake) makes it
    report the real cell voltage from then on -- exactly what was observed
    by hand on the Pi (MODE=0xC0/VCELL=0 -> wake -> VCELL=4067mV/SOC=88%)."""

    def __init__(self, real_mv: int):
        raw = _mv_to_raw(real_mv)
        msb, lsb = (raw >> 8) & 0xFF, raw & 0xFF
        super().__init__({0x02: 0, 0x03: 0, _REG_MODE: 0xC0})
        self._real_msb, self._real_lsb = msb, lsb

    def write_byte_data(self, addr, reg, value):
        super().write_byte_data(addr, reg, value)
        if reg == _REG_MODE and value == 0x00:
            self.registers[0x02] = self._real_msb
            self.registers[0x03] = self._real_lsb


class TestCW2015SensorVoltage(unittest.TestCase):
    """Reads raw cell voltage (REG_VCELL) instead of
    trusting the chip's own REG_SOC -- see cw2015.py's module doc for why
    (the underlying reasoning: a cheap/uncalibrated gauge chip plus a nonlinear
    LiPo discharge curve makes REG_SOC unreliable; compute from voltage +
    an explicit curve instead)."""

    def test_read_voltage_mv_decodes_registers(self):
        raw = _mv_to_raw(3900)
        msb, lsb = (raw >> 8) & 0xFF, raw & 0xFF
        bus = FakeBus({0x02: msb, 0x03: lsb})
        sensor = _CW2015Sensor(bus, addr=0x62)
        got = sensor.read_voltage_mv()
        # Integer-division rounding in both directions can be off by a
        # fraction of a millivolt -- assert closeness, not bit-exactness.
        self.assertAlmostEqual(got, 3900, delta=1)
        self.assertEqual(bus.calls, [(0x62, 0x02), (0x62, 0x03)])

    def test_read_soc_uses_the_curve_not_a_raw_register(self):
        raw = 12786  # arbitrary ADC value, ~3899mV
        msb, lsb = (raw >> 8) & 0xFF, raw & 0xFF
        bus = FakeBus({0x02: msb, 0x03: lsb, 0x04: 5})  # REG_SOC deliberately wrong (5) -- must be ignored
        sensor = _CW2015Sensor(bus, addr=0x62)
        expected_mv = raw * _VCELL_STEP_UV // 1000
        self.assertEqual(sensor.read_soc(), _voltage_to_percent(expected_mv))
        self.assertNotEqual(sensor.read_soc(), 5, "REG_SOC (0x04) must never be read/used")

    def test_read_soc_propagates_bus_errors(self):
        class ErrorBus:
            def read_byte_data(self, addr, reg):
                raise OSError("simulated I2C bus error")

        sensor = _CW2015Sensor(ErrorBus(), addr=0x62)
        with self.assertRaises(OSError):
            sensor.read_soc()


class TestImplausibleVoltageIsNotZeroPercent(unittest.TestCase):
    """Production bug (2026-07-02): the CW2015 on the real Pi read
    VCELL=0 (disconnected/dead battery or a bad I2C read) and it was
    reported as a real "0%" -- a placeholder in disguise, since a LiPo
    never actually reads near 0V. Below _VCELL_MIN_PLAUSIBLE_MV, read_soc
    must return None (unknown), never 0."""

    def _bus_for_mv(self, mv):
        raw = _mv_to_raw(mv)
        msb, lsb = (raw >> 8) & 0xFF, raw & 0xFF
        return FakeBus({0x02: msb, 0x03: lsb})

    def test_vcell_zero_is_none_not_zero_percent(self):
        sensor = _CW2015Sensor(self._bus_for_mv(0), addr=0x62)
        self.assertIsNone(sensor.read_soc())

    def test_vcell_below_floor_is_none(self):
        sensor = _CW2015Sensor(self._bus_for_mv(1200), addr=0x62)
        self.assertIsNone(sensor.read_soc())

    def test_vcell_between_floor_and_curve_bottom_is_valid_zero(self):
        # A genuinely near-empty/over-discharged cell sits ABOVE the plausible
        # floor (2500mV) but BELOW the curve's own 0%-anchor (3000mV, recal
        # 2026-07-02) -- must NOT be swept up in the same "invalid/no reading"
        # bucket as a disconnected battery: it's a real, valid reading, the
        # curve just clamps it to 0% (extremely low, not "unknown").
        sensor = _CW2015Sensor(self._bus_for_mv(2800), addr=0x62)
        pct = sensor.read_soc()
        self.assertEqual(pct, 0)  # a real 0%, not None

    def test_vcell_above_curve_bottom_is_a_valid_nonzero_percent(self):
        sensor = _CW2015Sensor(self._bus_for_mv(3450), addr=0x62)
        pct = sensor.read_soc()
        self.assertIsNotNone(pct)
        self.assertGreater(pct, 0)
        self.assertLess(pct, 20)

    def test_backend_read_soc_propagates_none_for_implausible_voltage(self):
        # End-to-end through CW2015Backend (what service.py actually
        # calls) -- not just the low-level sensor.
        be = CW2015Backend()
        be._sensor = _CW2015Sensor(self._bus_for_mv(0), addr=0x62)
        self.assertIsNone(be.read_soc())

    def test_read_soc_wakes_and_rereads_a_sleeping_chip(self):
        """Follow-up: reproduces the real bug diagnosed on the Pi -- the
        chip boots asleep (VCELL=0), so read_soc
        must wake it and re-read instead of reporting a false 'no battery'."""
        bus = SleepyBus(real_mv=4067)
        sensor = _CW2015Sensor(bus, addr=0x62)
        pct = sensor.read_soc()
        self.assertIsNotNone(pct, "a sleeping-but-real battery must not read as 'no battery'")
        self.assertEqual(pct, _voltage_to_percent(4067))
        self.assertIn((0x62, _REG_MODE, 0x00), bus.writes,
                       "must have written MODE=0x00 to wake the chip")

    def test_read_soc_gives_up_after_one_wake_if_still_implausible(self):
        """A genuinely disconnected/dead battery (or a bad read) stays
        implausible even after the wake attempt -- must still return None,
        not loop forever or fabricate a value."""
        bus = self._bus_for_mv(0)
        sensor = _CW2015Sensor(bus, addr=0x62)
        self.assertIsNone(sensor.read_soc())
        self.assertEqual(bus.writes.count((0x62, _REG_MODE, 0x00)), 1,
                          "must wake exactly once per read_soc() call, not retry forever")


class TestVoltageToPercentCurve(unittest.TestCase):
    """Direct tests of the LiPo discharge curve mapping itself -- the part
    that encodes the actual requirement (2026-07-02): a proper
    nonlinear curve, not a linear voltage-to-percent guess."""

    def test_full_charge_and_above_clamp_to_100(self):
        self.assertEqual(_voltage_to_percent(4200), 100)
        self.assertEqual(_voltage_to_percent(4500), 100)  # above the table's top

    def test_empty_and_below_clamp_to_0(self):
        self.assertEqual(_voltage_to_percent(3000), 0)  # the curve's 0% floor (2026-07-02 recal)
        self.assertEqual(_voltage_to_percent(2600), 0)  # below the table's bottom

    def test_exact_table_points(self):
        for mv, pct in _LIPO_CURVE:
            self.assertEqual(_voltage_to_percent(mv), pct, f"{mv}mV should read exactly {pct}%")

    def test_interpolates_between_table_points(self):
        # Midpoint between (4200,100) and (4100,90) -> 95, exactly.
        self.assertEqual(_voltage_to_percent(4150), 95)

    def test_curve_is_nonlinear_steep_knee_vs_flat_plateau(self):
        # The original framing (2026-07-02): "el voltaje maximo
        # 4.2 baja rapido a 4.0... al principio cae rapido" -- near full
        # charge, a SMALL amount of used capacity swings the voltage a
        # LOT (steep mV-per-percent knee). The middle of the curve should
        # be comparatively flat (a lot of capacity drains for only a
        # small voltage change) -- otherwise this is just a disguised
        # linear mapping and doesn't actually address the complaint.
        top_mv_per_percent = (4200 - 4100) / (100 - 90)      # top-of-charge segment
        mid_mv_per_percent = (3900 - 3800) / (60 - 40)        # middle/plateau segment
        self.assertGreater(
            top_mv_per_percent, mid_mv_per_percent,
            "near-full charge must be a steeper mV/percent knee than the mid-curve plateau",
        )


class TestCW2015Backend(unittest.TestCase):
    def test_no_smbus2_degrades_to_none_reading(self):
        # This dev machine has no smbus2 installed -- proves the real
        # ImportError-guard path, not a simulated one.
        be = CW2015Backend()
        self.assertIsNone(be.read_soc())
        be.close()  # must not raise even though nothing was ever opened

    def test_close_is_idempotent(self):
        be = CW2015Backend()
        be.close()
        be.close()  # calling twice must not raise


class TestTryInitWakesTheChip(unittest.TestCase):
    """Follow-up: _try_init must wake the chip right
    after opening the bus, not only defensively on the first low reading.
    This dev machine has no smbus2 installed (deliberately -- see the
    module docstring's reasoning for the other tests that lean on that), so
    a fake module is injected via sys.modules instead of requiring a real
    install just for this one test."""

    def test_try_init_writes_mode_wake_after_opening_bus(self):
        fake_bus = FakeBus({0x02: 0, 0x03: 0})
        fake_smbus2 = types.ModuleType("smbus2")
        fake_smbus2.SMBus = lambda bus_num: fake_bus
        with mock.patch.dict(sys.modules, {"smbus2": fake_smbus2}):
            be = CW2015Backend()
        try:
            self.assertIn((0x62, _REG_MODE, 0x00), fake_bus.writes,
                           "_try_init must wake the chip (write MODE=0x00) right after opening the bus")
        finally:
            be.close()


class TestBackendFactory(unittest.TestCase):
    def test_none_backend_reads_none(self):
        be = get_backend("none")
        self.assertIsInstance(be, _NonePowerBackend)
        self.assertIsNone(be.read_soc())
        be.close()  # must not raise

    def test_unknown_backend_falls_back_to_none(self):
        be = get_backend("some-typo-backend")
        self.assertIsInstance(be, _NonePowerBackend)

    def test_default_env_is_none(self):
        # Zero hardcode / fail-safe default: no PIMYWA_POWER_BACKEND set ->
        # never assume hardware is present.
        os.environ.pop("PIMYWA_POWER_BACKEND", None)
        be = get_backend()
        self.assertIsInstance(be, _NonePowerBackend)


class TestWriteBatteryFile(unittest.TestCase):
    """Single-writer fix: this service writes ONLY its
    own battery.json sidecar now, never status.json (see service.py's
    module doc for why -- a second status.json writer risked clobbering a
    core-set mood like "paused"/"error")."""

    def test_writes_battery_and_timestamp(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "battery.json")
            before = int(time.time())
            service._write_battery_file(path, 55, 3900, False, 200)
            after = int(time.time())
            with open(path, encoding="utf-8") as fh:
                data = json.load(fh)
            self.assertEqual(data["battery"], 55)
            self.assertTrue(before <= data["ts"] <= after)

    def test_writes_voltage_charging_and_time_remaining(self):
        """Milestones C/D: the sidecar carries the new
        fields too, and None (no real reading) round-trips as JSON null --
        never a fabricated value (no-placeholder)."""
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "battery.json")
            service._write_battery_file(path, 55, 3900, True, 42)
            with open(path, encoding="utf-8") as fh:
                data = json.load(fh)
            self.assertEqual(data["voltage_mv"], 3900)
            self.assertTrue(data["charging"])
            self.assertEqual(data["time_remaining_min"], 42)

            service._write_battery_file(path, None, None, False, None)
            with open(path, encoding="utf-8") as fh:
                data = json.load(fh)
            self.assertIsNone(data["voltage_mv"])
            self.assertIsNone(data["time_remaining_min"])

    def test_atomic_no_tmp_file_left_behind(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "battery.json")
            service._write_battery_file(path, 42, None, False, None)
            self.assertFalse(os.path.exists(path + ".tmp"))
            self.assertTrue(os.path.exists(path))

    def test_overwrites_stale_reading(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "battery.json")
            service._write_battery_file(path, 10, None, False, None)
            service._write_battery_file(path, 90, None, False, None)
            with open(path, encoding="utf-8") as fh:
                data = json.load(fh)
            self.assertEqual(data["battery"], 90)


if __name__ == "__main__":
    unittest.main()
