# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Camilo Brossard
"""Adaptive, self-calibrating battery time-remaining estimator
(milestone D).

The requirement: "un servicio muy liviano que calcule el tiempo restante, que
se actualice en tiempo real, el algoritmo se reconstruye para entender la
bateria: al principio un calculo, despues datos reales".

Two-phase estimate
-------------------
1. THEORETICAL (cold start / not enough real data yet): nominal capacity
   (mAh) / estimated draw (mA) -> full-charge hours, scaled by the current
   SoC%. Both are env-configurable (no hardcode) since neither is known
   precisely for a given Pi + panel + battery combo.
2. LEARNED (once real discharge samples exist): the actual dSoC/dt observed
   over a rolling window, blended into a persisted exponential moving
   average (EMA) so it improves across service restarts. This REPLACES the
   theoretical draw once available -- same idea as described above.

Charging vs discharging (revised 2026-07-02, real production bug: the device
was charging at 4.12V/92%-raw and the panel showed battery=44%, stuck on
the linearized discharge estimate). ORIGINAL approach detected charging from
a per-sample voltage DELTA (>=5mV rise between two consecutive polls) --
fragile: it missed the real charge above (voltage climbed 3.04V->4.12V, but
apparently never by >=5mV between two PARTICULAR consecutive polls) and,
separately, could also false-trigger on small noise recovery bumps.

REVISED: charging is detected from the DIVERGENCE between the raw curve pct
and the (still-decaying) linearized_soc -- see _detect_charging. During
discharge under load, raw pct reads AT OR BELOW linearized_soc (VCELL sags
under load, see cw2015.py's KNOWN LIMITATION); during a real charge, raw
voltage climbs and pct pulls AHEAD of the decaying linearized estimate.
That asymmetry (raw >> linearized = charging; raw <= linearized = normal
discharge sag) is robust to noisy individual samples because it accumulates
over the whole decaying baseline, not one delta. The original
"pinned at 100%" rule (spec 3a) stays as a secondary signal for the edge
case where pct is already capped and can't diverge further. While charging,
discharge learning is paused (you cannot learn a discharge rate off a
rising curve) and linearized_soc SNAPS UP to the raw pct every poll (follows
the voltage up in real time) -- see update()'s charging branch.

Linearized battery level (2026-07-02 follow-up: "quiero que la
descarga se vea PAREJA/lineal"). WHY this exists: a 1S LiPo's open-circuit
voltage is famously non-linear (steep knee near full, a long flat plateau
through the middle, steep knee again near empty -- see cw2015.py's own
module doc) -- so a percent DERIVED FROM VOLTAGE always looks jumpy: it
barely moves for a long time on the plateau, then suddenly drops. Reporting
that raw curve percent as "the battery bar" reads as broken/lying, even
though it is technically accurate at each instant.

The fix does NOT touch the voltage or the curve (cw2015.py's _LIPO_CURVE
and _VCELL_MIN_PLAUSIBLE_MV stay exactly as calibrated) -- voltage_mv keeps
shipping raw and real, shown separately. Instead, the value reported AS
"battery" (the number the bar draws) is a BLEND:

  - linearized_soc: a wall-clock COUNTDOWN, not a voltage readout. At the
    start of each discharge cycle (cold start, or right after a charge->
    discharge transition) it is anchored to the current curve-based
    estimate ("auto-corrige cada ciclo" -- each cycle starts from a
    REAL observed reading, so estimation error never accumulates across
    cycles). Between anchors it just counts down by REAL ELAPSED TIME
    against runtime_total_minutes (a full 100->0 discharge at the learned
    rate, or the theoretical capacity/draw estimate before one exists) --
    time passing at a steady rate is what actually produces a visually
    EVEN countdown, independent of the curve's plateau/knee shape.
  - The reported value is (1-weight)*raw_curve_pct + weight*linearized_soc,
    where weight ramps 0 -> 1 as real discharge samples accumulate
    (PIMYWA_BATTERY_LINEARIZE_SAMPLES). Zero learned data -> weight=0 ->
    reports the curve percent exactly, same as before this tweak. This is
    NOT a placeholder -- once there is real discharge history, the blended
    number is a MORE honest "how much runtime is actually left" than a
    voltage that sags under load and recovers at rest (see cw2015.py's
    KNOWN LIMITATION note), which is its own kind of misleading jumpiness.

Persistence: the learned rate (and the sample count that drives the
linearize weight) is written to PIMYWA_BATTERY_LEARN_FILE only at the
named recompute milestones (50/30/20/15/10/5% SoC, spec 3c) --
deliberately NOT on every poll, keeping this "muy liviano" (near-zero writes
to the durable SD card; contrast with status.json/battery.json, which live
in tmpfs and can be rewritten freely).

Discharge/charge trace log (item A). The requirement:
"cada un minuto por ejemplo tomar el voltaje. Entonces el voltaje se nos
vuelve trazable en la linea de tiempo." A CSV row (ts, voltage_mv, raw_pct,
linearized_pct, charging) is appended every PIMYWA_BATTERY_LOG_SEC -- this is
a SEPARATE, much coarser cadence than the learn-rate milestone persistence
above (this log wants a full discharge CURVE over time for the dashboard/
plot, not just the current rate), so it lives in its own file. It survives
restarts (durable SD, like battery_learn.json -- a power-cycle mid-discharge
must not lose the trace) and is a plain append (not tmp+rename): rewriting
an hours-long file on every 60s sample would be O(n^2) disk I/O for no
benefit, and the worst case from a power cut mid-write is one truncated
trailing row, not a corrupted file -- readers just skip a malformed last
line. Size-capped with single-generation rotation (3rd commandment: never
let this grow unbounded on the SD card).

Env knobs (all optional, zero hardcode)
----------------------------------------
PIMYWA_BATTERY_CAPACITY_MAH    Nominal pack capacity, mAh      (default: 1200)
PIMYWA_BATTERY_DRAW_MA         Estimated system draw, mA       (default: 180)
PIMYWA_BATTERY_LEARN_FILE      Persisted learned-rate JSON     (default: /opt/pimywa/data/battery_learn.json)
PIMYWA_BATTERY_LEARN_WINDOW_MIN  Discharge-rate sample window, minutes (default: 15)
PIMYWA_BATTERY_LEARN_ALPHA     EMA smoothing factor (0..1)     (default: 0.3)
PIMYWA_BATTERY_LINEARIZE_SAMPLES  Discharge samples to fully trust the
                                linearized (vs. raw curve) reading (default: 20)
PIMYWA_BATTERY_CHARGE_MARGIN_PCT  Divergence (raw pct - linearized_soc) that
                                means "really charging", percentage points (default: 10)
PIMYWA_BATTERY_LOG_FILE        Discharge/charge trace CSV     (default: /opt/pimywa/data/battery_log.csv)
PIMYWA_BATTERY_LOG_SEC         Trace sample interval, seconds (default: 60)
PIMYWA_BATTERY_LOG_ENABLED     Enable the trace log, 1|0      (default: 1)
PIMYWA_BATTERY_LOG_MAX_BYTES   Rotate the trace log past this size, bytes (default: 2097152 = 2 MiB)
"""
import json
import logging
import os

logger = logging.getLogger(__name__)

_CAPACITY_MAH = float(os.getenv("PIMYWA_BATTERY_CAPACITY_MAH", "1200"))
_DRAW_MA      = float(os.getenv("PIMYWA_BATTERY_DRAW_MA", "180"))
_LEARN_FILE   = os.getenv("PIMYWA_BATTERY_LEARN_FILE", "/opt/pimywa/data/battery_learn.json")
_WINDOW_MIN   = float(os.getenv("PIMYWA_BATTERY_LEARN_WINDOW_MIN", "15"))
_ALPHA        = float(os.getenv("PIMYWA_BATTERY_LEARN_ALPHA", "0.3"))
_LINEARIZE_SAMPLES = float(os.getenv("PIMYWA_BATTERY_LINEARIZE_SAMPLES", "20"))
_CHARGE_MARGIN_PCT = float(os.getenv("PIMYWA_BATTERY_CHARGE_MARGIN_PCT", "10"))
_LOG_FILE      = os.getenv("PIMYWA_BATTERY_LOG_FILE", "/opt/pimywa/data/battery_log.csv")
_LOG_SEC       = float(os.getenv("PIMYWA_BATTERY_LOG_SEC", "60"))
_LOG_ENABLED   = os.getenv("PIMYWA_BATTERY_LOG_ENABLED", "1") not in ("0", "false", "no")
_LOG_MAX_BYTES = int(os.getenv("PIMYWA_BATTERY_LOG_MAX_BYTES", str(2 * 1024 * 1024)))

# Boss-specified recompute points (spec 3c) -- a fixed calibration schedule,
# not a tunable knob (ascending order so the first m with pct<=m is the
# tightest bracket pct just crossed into).
_MILESTONES = (5, 10, 15, 20, 30, 50)

# How long SoC must sit pinned at 100% before it counts as "must be
# charging" (spec 3a: "si se mantiene siempre en 100% se subentiende modo
# carga") -- a couple of poll cycles, not a single sample (avoids a false
# positive from one lucky reading right at the top of the curve).
_PINNED_100_SECONDS = 120


def _theoretical_minutes(pct: int) -> float:
    """capacity/draw -> full-charge minutes, scaled by current SoC. Used
    until enough real samples exist to trust the learned rate, and as the
    always-available fallback."""
    if _DRAW_MA <= 0:
        return 0.0
    minutes_full = _CAPACITY_MAH / _DRAW_MA * 60.0
    return minutes_full * (pct / 100.0)


def _clamp_pct(x: float) -> float:
    return max(0.0, min(100.0, x))


def _load_learned(path: str) -> dict:
    try:
        with open(path, encoding="utf-8") as fh:
            return json.load(fh)
    except (OSError, ValueError):
        return {}


def _save_learned(path: str, data: dict) -> None:
    """Atomic tmp+rename (3rd commandment) -- this file lives on the durable
    SD card (survives restarts, unlike tmpfs status.json/battery.json), so a
    half-written file from a power cut would corrupt the learned rate."""
    tmp = path + ".tmp"
    try:
        d = os.path.dirname(path)
        if d:
            os.makedirs(d, exist_ok=True)
        with open(tmp, "w", encoding="utf-8") as fh:
            json.dump(data, fh)
        os.replace(tmp, path)
    except OSError as exc:
        logger.warning("could not persist %s: %s", path, exc)


_LOG_HEADER = "ts,voltage_mv,raw_pct,linearized_pct,charging\n"


def _rotate_log_if_needed(path: str, max_bytes: int) -> None:
    """Single-generation rotation (current -> .1, dropping any older .1) --
    enough to cap SD growth (3rd commandment) while keeping recent history
    for the dashboard trace; a multi-generation scheme would be overkill for
    a diagnostic log nobody archives long-term."""
    try:
        if os.path.getsize(path) < max_bytes:
            return
    except OSError:
        return  # file does not exist yet -- nothing to rotate
    try:
        os.replace(path, path + ".1")
    except OSError as exc:
        logger.warning("could not rotate %s: %s", path, exc)


def _append_log(path: str, max_bytes: int, ts: float, voltage_mv,
                 raw_pct: int, linearized_pct: float, charging: bool) -> None:
    """Append one trace row -- see module doc's "Discharge/charge trace log"
    section for why this is a plain append (not tmp+rename) unlike
    _save_learned above."""
    _rotate_log_if_needed(path, max_bytes)
    try:
        d = os.path.dirname(path)
        if d:
            os.makedirs(d, exist_ok=True)
        is_new = not os.path.exists(path)
        with open(path, "a", encoding="utf-8") as fh:
            if is_new:
                fh.write(_LOG_HEADER)
            v = "" if voltage_mv is None else voltage_mv
            fh.write(f"{int(ts)},{v},{raw_pct},{round(linearized_pct, 1)},{int(charging)}\n")
    except OSError as exc:
        logger.warning("could not append %s: %s", path, exc)


class TimeRemainingEstimator:
    """Call update(pct, voltage_mv, now) once per poll; returns
    {"minutes": int|None, "charging": bool, "battery_pct": int|None}.
    pct=None (no battery reading) -> all None/False, no-placeholder.
    battery_pct is the value the power service should write as "battery" in
    battery.json (see module doc's "Linearized battery level" section) --
    voltage_mv itself is never touched, stays raw."""

    def __init__(self, learn_file: str = _LEARN_FILE, log_file: str = _LOG_FILE,
                 log_enabled: bool = _LOG_ENABLED, log_interval_sec: float = _LOG_SEC,
                 log_max_bytes: int = _LOG_MAX_BYTES):
        self._learn_file = learn_file
        state = _load_learned(learn_file)
        self._rate_pct_per_min = state.get("rate_pct_per_min")
        self._last_milestone_hit = state.get("last_milestone_hit")
        self._discharge_sample_count = state.get("discharge_sample_count", 0)
        self._samples: list = []            # [(ts, soc)], discharging-only, within the learn window
        self._pinned_100_since = None
        # Auto-calibrated extremes for THIS cell (item
        # B): "a partir del voltaje del que esta cargando,
        # baja al cien, del cien al cero. el ultimo voltaje registrado
        # despues se convierte en el cero" -- max_voltage_mv is the highest
        # voltage ever observed (any sample, charging or not -- a real
        # charge is the only way voltage climbs, so its ceiling IS the
        # "acaba de cargar" 100% point); last_plausible_voltage_mv is the
        # most recent voltage seen WHILE DISCHARGING (excludes charging
        # samples on purpose -- a rising voltage mid-charge is not "about to
        # die"), so whatever is on disk when the device eventually loses
        # power from a real depletion is close to the true death voltage.
        self._max_voltage_mv = state.get("max_voltage_mv")
        self._last_plausible_voltage_mv = state.get("last_plausible_voltage_mv")
        self._log_file = log_file
        self._log_enabled = log_enabled
        self._log_interval_sec = log_interval_sec
        self._log_max_bytes = log_max_bytes
        self._last_log_time = None
        # Linear countdown state (2026-07-02 follow-up) -- see module doc.
        # Deliberately tracked as a PERCENT (not minutes): runtime_total
        # changes as the learned rate improves, and minutes/runtime_total
        # would silently go stale if reinterpreted through a NEW
        # runtime_total after the rate moves -- percent has no such unit
        # mismatch, decaying it directly by (elapsed / current runtime_total)
        # each tick is always self-consistent.
        self._linearized_soc = None
        self._last_update_time = None
        self._was_charging = False

    def _detect_charging(self, pct: int, now: float) -> bool:
        """Robust charging signal (2026-07-02 production fix): DIVERGENCE
        between the raw curve pct and the (still-decaying) linearized_soc,
        not a fragile per-sample voltage delta -- see module doc for the
        real bug this replaces. self._linearized_soc is None before the
        first anchor (nothing to diverge from yet) -> not charging."""
        diverging = (
            self._linearized_soc is not None
            and pct - self._linearized_soc >= _CHARGE_MARGIN_PCT
        )
        if pct == 100:
            if self._pinned_100_since is None:
                self._pinned_100_since = now
            pinned_awhile = (now - self._pinned_100_since) >= _PINNED_100_SECONDS
        else:
            self._pinned_100_since = None
            pinned_awhile = False
        return diverging or pinned_awhile

    def _estimate_minutes(self, pct: int) -> float:
        """Instant snapshot from the current curve reading -- the OLD (pre-
        linearization) estimate, still used as the anchor value and as the
        low-confidence end of the blend."""
        if self._rate_pct_per_min and self._rate_pct_per_min > 0:
            return pct / self._rate_pct_per_min
        return _theoretical_minutes(pct)

    def _runtime_total_minutes(self) -> float:
        """Full 100->0 discharge time under the current model -- "capacidad/
        ritmo aprendido" (spec): the learned rate once one exists, else the
        theoretical capacity/draw estimate. Denominator for linearized_soc."""
        if self._rate_pct_per_min and self._rate_pct_per_min > 0:
            return 100.0 / self._rate_pct_per_min
        return _theoretical_minutes(100)

    def _maybe_persist(self, pct: int) -> None:
        """Refresh the persisted learned rate (+ sample count, so the
        linearize-confidence weight also survives a restart) only at the
        milestone SoC points (spec 3c) -- avoids an SD write on every
        single poll. Also carries the auto-calibrated voltage extremes
        (item B) piggybacking on the same write: the tightest bracket (5%)
        is the closest checkpoint to a real depletion, so whatever is on
        disk is close to the true death voltage even though this is not a
        write-on-every-sample log."""
        hit = next((m for m in _MILESTONES if pct <= m), None)
        if hit is None or hit == self._last_milestone_hit:
            return
        self._last_milestone_hit = hit
        _save_learned(self._learn_file, {
            "rate_pct_per_min": self._rate_pct_per_min,
            "last_milestone_hit": hit,
            "discharge_sample_count": self._discharge_sample_count,
            "max_voltage_mv": self._max_voltage_mv,
            "last_plausible_voltage_mv": self._last_plausible_voltage_mv,
        })

    def personalized_pct(self, voltage_mv) -> "float | None":
        """Map a voltage to a percent using THIS cell's own observed
        extremes (item B) instead of the generic _LIPO_CURVE table -- None
        until both extremes are known (no-placeholder: a single sample is
        not enough range to calibrate against, and a degenerate/zero span
        would divide by zero). Used by the voltage<->time mapping (item C)."""
        if voltage_mv is None or self._max_voltage_mv is None or self._last_plausible_voltage_mv is None:
            return None
        span = self._max_voltage_mv - self._last_plausible_voltage_mv
        if span <= 0:
            return None
        return _clamp_pct((voltage_mv - self._last_plausible_voltage_mv) / span * 100.0)

    def minutes_remaining_from_voltage(self, voltage_mv) -> "float | None":
        """Voltage -> time remaining (item C), from THIS cell's calibrated
        extremes (item B) + the learned discharge rate -- independent of an
        in-flight update() call, e.g. the dashboard rendering a voltage read
        from the trace log. None (no-placeholder) until BOTH the extremes
        and a real learned rate exist -- falling back to the theoretical
        capacity/draw estimate here would silently contradict the whole
        point of a per-cell calibrated model with a generic guess."""
        pct = self.personalized_pct(voltage_mv)
        if pct is None or not self._rate_pct_per_min or self._rate_pct_per_min <= 0:
            return None
        return pct / self._rate_pct_per_min

    def expected_voltage_for_minutes_remaining(self, minutes_remaining: float) -> "int | None":
        """Inverse of minutes_remaining_from_voltage (item C, "viceversa:
        sabemos cuanto tiempo ha pasado viendo el voltaje") -- the voltage
        the calibrated model expects at a given time-remaining. Same
        preconditions/None rule as the forward mapping."""
        if self._max_voltage_mv is None or self._last_plausible_voltage_mv is None:
            return None
        if not self._rate_pct_per_min or self._rate_pct_per_min <= 0:
            return None
        expected_pct = _clamp_pct(minutes_remaining * self._rate_pct_per_min)
        span = self._max_voltage_mv - self._last_plausible_voltage_mv
        return round(self._last_plausible_voltage_mv + expected_pct / 100.0 * span)

    def _maybe_log(self, now: float, voltage_mv, raw_pct: int, charging: bool) -> None:
        """Append one trace row, throttled to log_interval_sec independent of
        the caller's poll interval (contract item A: "cada un minuto") --
        runs on BOTH charge and discharge samples (unlike rate learning,
        which pauses while charging) so the trace shows the whole timeline,
        including the charge periods that explain a pct jump."""
        if not self._log_enabled:
            return
        if self._last_log_time is not None and (now - self._last_log_time) < self._log_interval_sec:
            return
        self._last_log_time = now
        _append_log(self._log_file, self._log_max_bytes, now, voltage_mv,
                    raw_pct, self._linearized_soc, charging)

    def update(self, pct, voltage_mv, now: float) -> dict:
        # voltage_mv is not used for charge DETECTION (see module doc /
        # _detect_charging), but IS used below for the trace log (item A)
        # and the auto-calibrated extremes (item B).
        if pct is None:
            return {"minutes": None, "charging": False, "battery_pct": None}

        charging = self._detect_charging(pct, now)

        # Auto-calibrated extremes (item B) -- track on EVERY sample
        # (cheap comparisons), independent of the milestone-gated persist
        # below. See __init__'s docstring for why max is tracked across any
        # sample but last_plausible only while discharging.
        if voltage_mv is not None:
            if self._max_voltage_mv is None or voltage_mv > self._max_voltage_mv:
                self._max_voltage_mv = voltage_mv
            if not charging:
                self._last_plausible_voltage_mv = voltage_mv

        # Re-anchor the linear countdown at the START of each discharge
        # cycle (cold start, or a charge->discharge transition) -- "auto-
        # corrige cada ciclo": each cycle starts the countdown from a
        # REAL curve-based reading, so estimation error can never accumulate
        # across cycles the way a pure free-running clock would.
        if self._linearized_soc is None or (self._was_charging and not charging):
            self._linearized_soc = float(pct)
            self._last_update_time = now
        self._was_charging = charging

        if charging:
            # Discharge learning pauses while charging -- you cannot learn a
            # discharge rate off a rising curve. Dropping the sample window
            # (rather than just not appending) means a subsequent discharge
            # starts clean, not blended with pre-charge data. linearized_soc
            # SNAPS UP to the raw pct every poll while charging (2026-07-02
            # production fix: it used to just sit frozen at wherever the
            # last discharge cycle left it, so a real charge to 92% still
            # reported the stale 44% -- see module doc) -- the battery bar
            # follows the rising voltage in real time, same value as the
            # (also-passed-through) battery_pct below.
            self._samples.clear()
            self._linearized_soc = float(pct)
            self._last_update_time = now
            self._maybe_log(now, voltage_mv, pct, charging)
            return {
                "minutes": round(self._estimate_minutes(pct)),
                "charging": True,
                "battery_pct": pct,
            }

        # Advance the countdown by REAL ELAPSED TIME against the CURRENT
        # (pre-this-sample) runtime estimate -- this is what makes it
        # "linear/pareja" instead of inheriting the LiPo curve's plateau-
        # then-knee shape. Using the rate as of the START of this tick (not
        # the one about to be relearned two lines down) keeps the decay
        # amount consistent with the linearized_soc value it's adjusting.
        runtime_total_before = self._runtime_total_minutes()
        elapsed_min = max(0.0, (now - self._last_update_time) / 60.0) if self._last_update_time is not None else 0.0
        decay_pct = (elapsed_min / runtime_total_before * 100.0) if runtime_total_before > 0 else 0.0
        self._linearized_soc = _clamp_pct(self._linearized_soc - decay_pct)
        self._last_update_time = now

        self._samples.append((now, pct))
        cutoff = now - _WINDOW_MIN * 60
        self._samples = [(t, s) for t, s in self._samples if t >= cutoff]
        self._discharge_sample_count += 1

        if len(self._samples) >= 2:
            t0, s0 = self._samples[0]
            t1, s1 = self._samples[-1]
            dt_min = (t1 - t0) / 60.0
            if dt_min > 0 and s1 < s0:  # real discharge, not noise/flat
                observed_rate = (s0 - s1) / dt_min
                self._rate_pct_per_min = (
                    observed_rate if self._rate_pct_per_min is None
                    else _ALPHA * observed_rate + (1 - _ALPHA) * self._rate_pct_per_min
                )

        self._maybe_persist(pct)

        # BLEND (spec: "sin datos aprendidos -> curva de voltaje... a medida
        # que hay datos reales, transicionas -- peso creciente -- al SoC
        # linealizado-por-tiempo"): weight 0 at cold start (matches the OLD
        # curve-only behavior exactly), ramping to 1 as real discharge
        # samples accumulate.
        weight = min(1.0, self._discharge_sample_count / _LINEARIZE_SAMPLES) if _LINEARIZE_SAMPLES > 0 else 1.0
        blended_pct = (1 - weight) * pct + weight * self._linearized_soc

        # runtime_total AFTER the (possible) rate update above -- freshest
        # estimate for the DISPLAYED minutes, decoupled from the decay step.
        runtime_total_after = self._runtime_total_minutes()
        snapshot_minutes = self._estimate_minutes(pct)
        linearized_minutes = self._linearized_soc / 100.0 * runtime_total_after
        blended_minutes = (1 - weight) * snapshot_minutes + weight * linearized_minutes

        self._maybe_log(now, voltage_mv, pct, charging)

        return {
            "minutes": round(blended_minutes),
            "charging": False,
            "battery_pct": round(blended_pct),
        }


# ── Self-check ────────────────────────────────────────────────────────────────

def _est(n, **kw) -> "TimeRemainingEstimator":
    """Sandboxed estimator for tests not specifically about persistence --
    both learn_file and log_file point at nonexistent paths (never the real
    _LEARN_FILE/_LOG_FILE defaults), so _save_learned/_append_log fail closed
    (a caught+logged OSError, same graceful-degradation as a real missing SD)
    instead of littering the dev machine with real files."""
    base = f"/nonexistent/does-not-exist{n}"
    return TimeRemainingEstimator(learn_file=base + ".json", log_file=base + ".csv", **kw)


def _self_check() -> None:
    # Theoretical fallback: no learned rate yet -> proportional to SoC.
    est = _est(1)
    r100 = est.update(100, 4200, now=0.0)
    assert r100["charging"] is False
    r50 = _est(2).update(50, 3850, now=0.0)
    assert round(r50["minutes"]) == round(r100["minutes"] / 2), \
        f"theoretical estimate should scale linearly with SoC: {r50} vs {r100}"

    # "Con 0 datos cae de vuelta a la curva de voltaje": zero learned
    # discharge samples -> battery_pct reports the RAW curve reading
    # exactly, unchanged from pre-linearization behavior.
    assert r100["battery_pct"] == 100, r100
    assert r50["battery_pct"] == 50, r50

    # Charging inference: SoC pinned at 100% for >= _PINNED_100_SECONDS.
    est = _est(3)
    r = est.update(100, 4200, now=0.0)
    assert r["charging"] is False, "a single 100% sample must not immediately read as charging"
    r = est.update(100, 4200, now=_PINNED_100_SECONDS + 1)
    assert r["charging"] is True, "SoC pinned at 100% for a while must infer charging"

    # Charging inference: DIVERGENCE (2026-07-02 production fix -- see
    # module doc). A bump right at the edge of the margin is noise/CV-phase
    # flattening, not a real charge -- must NOT trigger (this is exactly
    # what a naive per-sample delta check false-triggered on before). A real
    # jump past the margin must.
    est = _est(4)
    est.update(60, 3850, now=0.0)   # anchors linearized_soc=60
    r = est.update(60 + _CHARGE_MARGIN_PCT - 1, 3860, now=30.0)  # just under the margin
    assert r["charging"] is False, \
        f"a bump just under the divergence margin must not read as charging: {r}"
    r = est.update(78, 4050, now=60.0)  # well past the margin from the anchor
    assert r["charging"] is True, f"a curve pct well above linearized_soc must read as charging: {r}"

    # Discharge learning: a steady real drain converges the estimate away
    # from the theoretical fallback toward the observed rate.
    est = _est(5)
    pct, t = 80, 0.0
    for _ in range(10):
        r = est.update(pct, 3900, now=t)
        pct -= 1
        t += 60.0  # 1%/min real drain
    assert est._rate_pct_per_min is not None, "discharge samples must learn a rate"
    assert 0.5 < est._rate_pct_per_min < 1.5, \
        f"learned rate should converge near the real 1%%/min drain, got {est._rate_pct_per_min}"

    # Reproduces the exact real-Pi bug (2026-07-02): a long
    # discharge decays linearized_soc well below the raw curve, THEN the
    # device plugs in and the raw curve jumps way up (3.04V->4.12V, curve
    # ~92%). Must read as charging and report the RAW reading, not the
    # stale decayed linearized value.
    est = _est(10)
    for k in range(20):
        est.update(50 - k, 3800 - k * 10, now=k * 60.0)
    r = est.update(92, 4120, now=25 * 60.0)
    assert r["charging"] is True and r["battery_pct"] == 92, \
        f"debe reportar el crudo, no el linearized viejo: {r}"

    # Linearized SoC: a synthetic LINEAR discharge (constant rate) must
    # produce a monotonically non-increasing, smooth battery_pct sequence
    # (no bumps back up) once real samples exist.
    est = _est(6)
    pct, t, prev_reported = 90, 0.0, None
    for _ in range(15):
        r = est.update(pct, 3900, now=t)
        if prev_reported is not None:
            assert r["battery_pct"] <= prev_reported, \
                f"linear discharge must never bump battery_pct back up: {prev_reported} -> {r['battery_pct']}"
        prev_reported = r["battery_pct"]
        pct -= 1
        t += 60.0

    # Linearized SoC smooths a NON-linear plateau-then-knee sequence (the
    # actual LiPo shape this tweak exists for) once confidence has ramped up:
    # the raw curve's biggest single-step jump must NOT show up 1:1 in the
    # blended output.
    est = _est(7)
    pct_sequence = [60] * 10 + list(range(59, 49, -1)) + [40]  # plateau, slow drift, sudden knee-drop
    reported, t = [], 0.0
    for pct in pct_sequence:
        r = est.update(pct, 3850, now=t)
        reported.append(r["battery_pct"])
        t += 60.0
    raw_biggest_jump = max(abs(a - b) for a, b in zip(pct_sequence, pct_sequence[1:]))
    reported_biggest_jump = max(abs(a - b) for a, b in zip(reported, reported[1:]))
    assert reported_biggest_jump < raw_biggest_jump, \
        f"blended battery_pct should smooth the raw curve's jump: raw={raw_biggest_jump} reported={reported_biggest_jump}"

    # Defensive: an unusable theoretical model (e.g. misconfigured
    # PIMYWA_BATTERY_DRAW_MA=0) with no learned rate yet must not divide by
    # zero -- falls back to the raw curve pct instead of crashing.
    global _DRAW_MA
    old_draw = _DRAW_MA
    _DRAW_MA = 0.0
    try:
        est = _est(8)
        r = est.update(42, 3850, now=0.0)
        assert r["battery_pct"] == 42, r
    finally:
        _DRAW_MA = old_draw

    # Charging: battery_pct passes the raw curve reading straight through
    # (it genuinely rises with voltage while plugged in) -- divergence past
    # the margin, not a small delta.
    est = _est(9)
    est.update(50, 3850, now=0.0)
    r = est.update(62, 3980, now=30.0)
    assert r["charging"] is True and r["battery_pct"] == 62, r

    # Milestone persistence: only fires when SoC crosses into a NEW bracket.
    import tempfile
    with tempfile.TemporaryDirectory() as d:
        path = os.path.join(d, "learn.json")
        est = TimeRemainingEstimator(learn_file=path, log_file=os.path.join(d, "unused.csv"))
        est.update(96, 3900, now=0.0)     # above all milestones -> no write yet
        assert not os.path.exists(path)
        est.update(48, 3850, now=60.0)    # crosses into the 50% bracket -> persists
        assert os.path.exists(path)
        first_mtime = os.path.getmtime(path)
        rate_at_persist = est._rate_pct_per_min
        est.update(47, 3840, now=120.0)   # still inside the 50% bracket -> no re-write
        assert os.path.getmtime(path) == first_mtime, \
            "must not persist again while still inside the same milestone bracket"
        # Restart: a fresh estimator picks up the rate as of the LAST
        # persist (not est's latest in-memory update, which hasn't been
        # written yet -- persistence is deliberately milestone-gated).
        reloaded = TimeRemainingEstimator(learn_file=path, log_file=os.path.join(d, "unused.csv"))
        assert reloaded._rate_pct_per_min == rate_at_persist

    # ── Discharge trace log (item A) ──────────────────
    with tempfile.TemporaryDirectory() as d:
        log_path = os.path.join(d, "trace.csv")
        # Throttled to log_interval_sec: 5 updates 10s apart with a 30s
        # interval must yield only 2 rows (t=0 anchors, t=30 is the next
        # due sample), not 5.
        est = TimeRemainingEstimator(learn_file=os.path.join(d, "learn_a.json"),
                                      log_file=log_path, log_interval_sec=30.0)
        pct, t = 80, 0.0
        for _ in range(5):
            est.update(pct, 3900, now=t)
            pct -= 1
            t += 10.0
        with open(log_path, encoding="utf-8") as fh:
            lines = fh.read().splitlines()
        assert lines[0] == "ts,voltage_mv,raw_pct,linearized_pct,charging", lines[:1]
        assert len(lines) == 1 + 2, f"expected header + 2 throttled rows, got {lines}"

        # Disabled: no file created at all.
        log_path_off = os.path.join(d, "trace_off.csv")
        est_off = TimeRemainingEstimator(learn_file=os.path.join(d, "learn_b.json"),
                                          log_file=log_path_off, log_enabled=False)
        est_off.update(80, 3900, now=0.0)
        est_off.update(79, 3890, now=60.0)
        assert not os.path.exists(log_path_off), "log_enabled=False must not write anything"

        # Rotation: a tiny max_bytes forces the existing file to roll to .1
        # once the next sample would push it over the cap.
        log_path_rot = os.path.join(d, "trace_rot.csv")
        est_rot = TimeRemainingEstimator(learn_file=os.path.join(d, "learn_c.json"),
                                          log_file=log_path_rot, log_interval_sec=1.0,
                                          log_max_bytes=64)
        pct, t = 80, 0.0
        for _ in range(20):
            est_rot.update(pct, 3900, now=t)
            pct = max(1, pct - 1)
            t += 1.0
        assert os.path.exists(log_path_rot + ".1"), "log must rotate past log_max_bytes"
        assert os.path.getsize(log_path_rot) < 64 + 200, \
            "the CURRENT log file must stay small after rotating, not keep growing"

    # ── Auto-calibrated extremes (item B) ─────────────
    # No extremes yet -> personalized_pct is None (no-placeholder).
    est = _est(11)
    assert est.personalized_pct(3900) is None

    # max_voltage_mv tracks the highest voltage ever seen (any sample);
    # last_plausible_voltage_mv tracks the latest DISCHARGING voltage only.
    est = _est(12)
    est.update(80, 4100, now=0.0)               # discharging: max=4100, last_plausible=4100
    est.update(60, 3900, now=60.0)               # discharging: last_plausible updates to 3900
    r = est.update(95, 4200, now=120.0)          # charging: max updates to 4200, last_plausible UNCHANGED
    assert r["charging"] is True, r
    assert est._max_voltage_mv == 4200, est._max_voltage_mv
    assert est._last_plausible_voltage_mv == 3900, \
        "a charging sample must not overwrite the discharge-side 0% anchor"
    est.update(15, 3500, now=180.0)              # discharging again -> last_plausible tracks down
    assert est._last_plausible_voltage_mv == 3500, est._last_plausible_voltage_mv

    # personalized_pct rescales THIS cell's real span, not the generic
    # _LIPO_CURVE table: with max=4200/last_plausible=3500, the midpoint
    # voltage must read ~50%, not whatever the generic curve says for it.
    assert abs(est.personalized_pct(3850) - 50.0) < 0.5, est.personalized_pct(3850)
    assert est.personalized_pct(4200) == 100.0
    assert est.personalized_pct(3500) == 0.0
    assert est.personalized_pct(3200) == 0.0, "must clamp below the learned floor, not go negative"

    # Extremes persist across restarts, piggybacking on the milestone write.
    with tempfile.TemporaryDirectory() as d:
        path = os.path.join(d, "learn_extremes.json")
        log_path = os.path.join(d, "unused.csv")
        est = TimeRemainingEstimator(learn_file=path, log_file=log_path)
        est.update(96, 4180, now=0.0)     # above all milestones -> no write yet, but extremes tracked in-memory
        est.update(48, 3820, now=60.0)    # crosses into the 50% bracket -> persists (incl. extremes)
        assert os.path.exists(path)
        reloaded = TimeRemainingEstimator(learn_file=path, log_file=log_path)
        assert reloaded._max_voltage_mv == 4180, reloaded._max_voltage_mv
        assert reloaded._last_plausible_voltage_mv == 3820, reloaded._last_plausible_voltage_mv

    # ── Voltage<->time mapping (item C) ────────────────
    # None before extremes + a real learned rate both exist.
    est = _est(13)
    assert est.minutes_remaining_from_voltage(3900) is None
    assert est.expected_voltage_for_minutes_remaining(30.0) is None

    # Drive a steady synthetic 2%/min discharge from 4200mV to calibrate
    # both the extremes and the rate.
    est = _est(14)
    pct, mv, t = 100, 4200, 0.0
    for _ in range(15):
        est.update(pct, mv, now=t)
        pct -= 2
        mv -= 20
        t += 60.0
    assert est._rate_pct_per_min is not None and est._max_voltage_mv == 4200

    # Forward mapping is consistent with personalized_pct/rate. Midpoint of
    # the LEARNED span (not a hardcoded voltage) -- guaranteed inside
    # [last_plausible, max] regardless of the exact synthetic rate learned.
    v = round((est._max_voltage_mv + est._last_plausible_voltage_mv) / 2)
    expected_minutes = est.personalized_pct(v) / est._rate_pct_per_min
    assert abs(est.minutes_remaining_from_voltage(v) - expected_minutes) < 1e-9

    # Round trip: voltage -> minutes -> voltage must land back near the
    # original reading (exact, since both directions use the same linear
    # calibrated span -- the only loss is round() on the returned mV).
    minutes = est.minutes_remaining_from_voltage(v)
    back = est.expected_voltage_for_minutes_remaining(minutes)
    assert abs(back - v) <= 1, f"voltage->minutes->voltage round trip drifted: {v} -> {minutes} -> {back}"

    # Monotonic sanity: less time remaining must map to a lower expected
    # voltage (the model must not invert direction).
    hi = est.expected_voltage_for_minutes_remaining(40.0)
    lo = est.expected_voltage_for_minutes_remaining(5.0)
    assert hi > lo, f"more time remaining should map to a higher expected voltage: {hi} vs {lo}"


if __name__ == "__main__":
    _self_check()
    print("timeremain self-check OK")
