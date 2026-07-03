#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Camilo Brossard
"""Piumy display service — living face animator.

Polls status.json by mtime for real changes AND drives an idle animation
ticker so the face is never frozen (pwnagotchi-style "always alive").

Refresh policy
--------------
Boot                     : the BACKEND (not this service) does ONE deliberate
                          flash before the loop below ever runs — see
                          epaper/backend.py's EPaperWaveshareBackend._try_init,
                          which needs it once to anchor the partial-refresh
                          comparator to a known-blank baseline. That is setup,
                          not "during operation", so it is UNCONDITIONAL —
                          PIMYWA_EPAPER_FULL_REFRESH does not touch it.
First content frame     : render + show(full=PIMYWA_EPAPER_FULL_REFRESH).
Mood CHANGE (big)       : render + show(full=PIMYWA_EPAPER_FULL_REFRESH) —
                          entering/leaving qr/error/sleeping.
Mood CHANGE (normal)    : render + show(full=False) — partial, no flash.
Idle animation tick     : advance anim_step, render next variant,
                          show(full=False) if image changed — partial, no flash.
Same mood, same image   : SKIP — no pointless partial update.

PIMYWA_EPAPER_FULL_REFRESH (default "0"/off, 2026-07-02: "el reset con
multiples pestañeos debe ser opcional, desactivalo, el e-paper se pinta
genial con parciales") gates every full=True the LOOP would otherwise
request (first content frame, big-mood transitions) — off means the whole
running service only ever does partial (flash-free) updates after that one
backend boot flash. Set to 1 to restore the old always-flash-on-big-
transitions behavior.

"Sobre de atencion": the idle animation tick interval
is DYNAMIC, not fixed — it lerps from PIMYWA_ANIM_FAST_SEC right after a
real status.json change (an interaction: a new message, an agent tool call,
...) up to PIMYWA_ANIM_SLOW_SEC once PIMYWA_ANIM_RAMP_SEC has passed with
nothing happening. The panel looks alert right after something occurs and
settles back to a slow, low-power idle blink when quiet — see
_anim_interval(). This replaces the old fixed PIMYWA_IDLE_ANIM_SEC knob.

Battery / sleeping mode : if battery < PIMYWA_LOW_BATT or mood == "sleeping",
                          the (already-dynamic) animation interval is
                          multiplied by 4 on top (very slow);
                          real status changes still trigger immediate updates.

Graceful shutdown on SIGTERM / SIGINT.  On exit the panel is put to sleep
unless PIMYWA_SLEEP_ON_EXIT=0.

Live face mirror. The actual requirement: "en la pagina web
agrega las caritas que estan apareciendo" -- the dashboard shows the SAME
kaomoji as the e-paper. Rather than reimplement KAOMOJI_CATALOG/the eye
engine in JS (a real duplication of render.py's whole face-selection logic),
this service writes the literal face string it ALREADY computed for this
frame -- via render.variant_repr(pick_variant(...)), the exact functions
render_image() itself calls -- to a small face.json sidecar (tmpfs, same
single-writer pattern as the power adapter's battery.json: this service
never touches status.json directly, the CORE reads this sidecar back in on
its own heartbeat and merges it in). One source of truth (render.py), zero
duplicated catalog data.

Env knobs (all optional, zero hardcode)
----------------------------------------
PIMYWA_STATUS           Path to status.json          (default: /opt/pimywa/data/status.json)
PIMYWA_DISPLAY          Backend name                 (default: file)
PIMYWA_DISPLAY_OUT      PNG path (file backend)      (default: display.png)
PIMYWA_POLL_INTERVAL    Mtime-check interval, s      (default: 2.0)
PIMYWA_ANIM_FAST_SEC    Idle-tick interval right after interaction, s (default: 2.0)
PIMYWA_ANIM_SLOW_SEC    Idle-tick interval once quiet, s              (default: 18.0)
PIMYWA_ANIM_RAMP_SEC    Seconds of quiet to ramp FAST -> SLOW         (default: 60.0)
PIMYWA_LOW_BATT         Low-battery threshold, %     (default: 15)
PIMYWA_EPAPER_FULL_REFRESH  Allow full-refresh flashes during operation, 1|0 (default: 0)
PIMYWA_SLEEP_ON_EXIT    Sleep panel on exit: 1|0     (default: 1)
PIMYWA_FACE_FILE        Path to the face.json sidecar (default: /run/pimywa/face.json)
PIMYWA_LOG_LEVEL        Logging level                (default: INFO)
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
from render import render_image, pick_variant, variant_repr  # noqa: E402

# ── Env config ────────────────────────────────────────────────────────────────
_STATUS_PATH    = os.getenv("PIMYWA_STATUS",        "/opt/pimywa/data/status.json")
_FACE_FILE      = os.getenv("PIMYWA_FACE_FILE",     "/run/pimywa/face.json")
_POLL_INTERVAL  = float(os.getenv("PIMYWA_POLL_INTERVAL", "2.0"))
# Idle face cadence (2026-07-02: "3 segundos es derroche de bateria, no
# battery saving; cada 25-30s e ir aumentandolo de manera esporadica"). The
# face advances every ~25s right after a real event, ramping to ~60s the longer
# it sits idle. Event REACTIONS stay instant (a mood change renders immediately);
# only the idle cycling within a mood is slow. Decoupled from the core's ~15s
# status heartbeat below so routine data updates never reset this cadence.
_ANIM_FAST_SEC  = float(os.getenv("PIMYWA_ANIM_FAST_SEC", "25.0"))
_ANIM_SLOW_SEC  = float(os.getenv("PIMYWA_ANIM_SLOW_SEC", "60.0"))
_ANIM_RAMP_SEC  = float(os.getenv("PIMYWA_ANIM_RAMP_SEC", "180.0"))
# Piggyback throttle (2026-07-02: "si se detecta cambio de bateria o de
# internet, o cualquier otro refresco / movimiento MCP, aprovechar ese cambio
# para apurar la carita... dar un minimo de 3 segundos en cada cambio"). Any
# VISIBLE status refresh (battery/wifi/agent-connect/MCP activity) also advances
# the face -- but never faster than this, so a burst of quick MCP calls can't
# flip it every poll. The slow idle tick (FAST->SLOW above) is the floor when
# nothing is happening.
_FACE_MIN_SEC   = float(os.getenv("PIMYWA_FACE_MIN_SEC", "3.0"))
_LOW_BATT       = int(os.getenv("PIMYWA_LOW_BATT",   "15"))
# Battery-saver OFF by default (2026-07-02: "el sistema de battery saving
# que quede en el dashboard, pero desactivado"). When off, low battery does NOT
# throttle the face animation -- the e-paper keeps its small slow movement so it
# never looks hung. Enable it (dashboard/env) to slow the face 4x under _LOW_BATT.
_BATTERY_SAVER  = os.getenv("PIMYWA_BATTERY_SAVER", "0") not in ("0", "false", "no")
_FULL_REFRESH   = os.getenv("PIMYWA_EPAPER_FULL_REFRESH", "0") not in ("0", "false", "no")
_SLEEP_ON_EXIT  = os.getenv("PIMYWA_SLEEP_ON_EXIT", "1") not in ("0", "false", "no")

# ── Big-transition moods (trigger full refresh on enter/leave) ────────────────
_BIG_MOODS = frozenset({"qr", "error", "sleeping"})

# Resting/idle moods — calm states, NOT events. Arriving at one of these does
# NOT reset the "sobre de atencion" to fast (2026-07-02: "volver a idle no
# acelera, idle desespera"); only an actual event mood (new_msg, agent connect,
# reading, responding, ...) re-anchors the cadence to react fast.
_RESTING_MOODS = frozenset({"idle", "zero", "few", "swamped"})

logging.basicConfig(
    level=os.getenv("PIMYWA_LOG_LEVEL", "INFO"),
    format="%(asctime)s %(levelname)s [%(name)s] %(message)s",
)
logger = logging.getLogger("pimywa.display")


# ── Status reader ─────────────────────────────────────────────────────────────

def _anim_interval(elapsed: float) -> float:
    """Lerp FAST -> SLOW over PIMYWA_ANIM_RAMP_SEC of quiet ("sobre de
    atencion"): elapsed==0 -> FAST, elapsed>=RAMP -> SLOW,
    monotonically increasing in between."""
    if _ANIM_RAMP_SEC <= 0:
        return _ANIM_SLOW_SEC
    t = max(0.0, min(1.0, elapsed / _ANIM_RAMP_SEC))
    return _ANIM_FAST_SEC + (_ANIM_SLOW_SEC - _ANIM_FAST_SEC) * t


def _write_face_file(path: str, face, mood: str) -> None:
    """Atomically write the face.json sidecar (tmpfs -- cheap to rewrite on
    every render, same discipline as status.json/battery.json). face is None
    for mood "qr" (pick_variant's special case -- no kaomoji face is drawn,
    the panel shows the QR code instead; no-placeholder, never a fabricated
    face string)."""
    tmp = path + ".tmp"
    try:
        d = os.path.dirname(path)
        if d:
            os.makedirs(d, exist_ok=True)
        with open(tmp, "w", encoding="utf-8") as fh:
            json.dump({"face": face, "mood": mood, "ts": int(time.time())}, fh)
        os.replace(tmp, path)
    except OSError as exc:
        logger.warning("could not write %s: %s", path, exc)


def _read_status(path: str) -> dict:
    """Read and parse status.json.  Returns an error-state dict on failure."""
    try:
        with open(path, encoding="utf-8") as fh:
            return json.load(fh)
    except FileNotFoundError:
        return {"mood": "error", "speech": f"no status.json: {path}"}
    except json.JSONDecodeError:
        return {"mood": "error", "speech": "invalid status.json"}
    except OSError as exc:
        return {"mood": "error", "speech": str(exc)}


# ── Service loop ──────────────────────────────────────────────────────────────

def run() -> None:
    """Main service loop.  Blocks until SIGTERM or SIGINT."""
    backend_name = os.getenv("PIMYWA_DISPLAY", "file")
    backend = _backend_mod.get_backend(backend_name)

    logger.info(
        "Display service started -- backend=%s  status=%s  "
        "anim=%s..%ss/ramp=%ss  low_batt=%s%%  full_refresh=%s",
        backend_name, _STATUS_PATH, _ANIM_FAST_SEC, _ANIM_SLOW_SEC, _ANIM_RAMP_SEC, _LOW_BATT, _FULL_REFRESH,
    )

    _running = True

    def _stop(sig, _frame) -> None:
        nonlocal _running
        logger.info("Signal %s received -- shutting down", sig)
        _running = False

    signal.signal(signal.SIGTERM, _stop)
    signal.signal(signal.SIGINT, _stop)

    # State
    last_mtime:       float | None = None
    last_mood:        str          = ""
    last_image_bytes: bytes        = b""
    anim_step:        int          = 0
    last_anim_tick:   float        = 0.0
    is_first_frame:   bool         = True
    current_status:   dict         = {}
    last_interaction: float        = time.monotonic()   # boot counts as one

    try:
        while _running:
            now = time.monotonic()

            # ── Check for status.json change by mtime ─────────────────────────
            status_changed = False
            try:
                mtime: float | None = os.path.getmtime(_STATUS_PATH)
            except OSError:
                mtime = None

            if mtime != last_mtime:
                current_status = _read_status(_STATUS_PATH)
                last_mtime = mtime
                status_changed = True
                # NOTE: last_interaction is NOT reset here — the core rewrites
                # status.json every ~15s (heartbeat: uptime/battery), and letting
                # that speed the idle face back up made it flip every ~3s
                # ("derroche de bateria"). Only a real event (mood change, below)
                # re-anchors the idle cadence.

            mood    = current_status.get("mood", "idle")
            battery = current_status.get("battery")

            # Low-power mode: only when the battery saver is ON (off by default,
            # per owner request) AND battery is low; 'sleeping' is an intentional
            # rest state and always slows regardless.
            low_power = (
                _BATTERY_SAVER and isinstance(battery, int) and battery < _LOW_BATT
            ) or mood == "sleeping"

            # "Sobre de atencion": dynamic FAST->SLOW idle-tick interval,
            # still 4x slower on top in low-power mode.
            anim_interval = _anim_interval(now - last_interaction) * (4.0 if low_power else 1.0)
            anim_due      = (now - last_anim_tick) >= anim_interval

            # ── Decide what to render ─────────────────────────────────────────
            should_render = False
            use_full      = False
            next_step     = anim_step
            reanchor      = False   # did the FACE itself change? -> re-anchor the
                                    # idle timer. A routine data-only render (e.g.
                                    # battery % ticked) must NOT reset it, or the
                                    # face cadence collapses back to the heartbeat.
            data_change   = False   # a same-mood status change: might be a VISIBLE
                                    # data refresh (battery/wifi) -> piggyback a face
                                    # advance onto it (see render section below).

            if status_changed or is_first_frame:
                should_render = True
                mood_changed  = mood != last_mood

                if is_first_frame:
                    # NOT the backend's own boot flash (that already ran,
                    # unconditionally, before this loop started) -- this is
                    # the first REAL CONTENT frame, drawn from the backend's
                    # known-blank baseline. A partial update from "blank" is
                    # perfectly safe, so it only flashes if the owner opted
                    # back into full-refresh flashes.
                    use_full = _FULL_REFRESH
                elif mood_changed and (
                    mood in _BIG_MOODS or last_mood in _BIG_MOODS
                ):
                    use_full = _FULL_REFRESH               # big transition flash, opt-in
                else:
                    use_full = False                       # partial for minor changes

                if mood_changed:
                    next_step = 0                          # reset animation cycle
                    reanchor = True
                    if mood not in _RESTING_MOODS:
                        last_interaction = now             # an EVENT re-anchors the
                                                           # cadence to react fast;
                                                           # arriving at idle/resting
                                                           # stays calm (does NOT
                                                           # accelerate)
                elif not is_first_frame:
                    data_change = True                     # same mood, but the data
                                                           # (battery/wifi/...) may have
                                                           # visibly changed -> piggyback

            elif anim_due:
                # Idle animation tick — mood unchanged, just advance the face.
                # NOT gated on `not low_power` anymore (bug: that FROZE the face
                # on one variant at low battery instead of just slowing it, since
                # status-change re-renders don't advance anim_step). low_power
                # already 4x's anim_interval above, so the face still animates,
                # just slowly, on low battery (2026-07-02: "no hay animacion,
                # aparece siempre la misma").
                should_render = True
                use_full      = False
                next_step     = anim_step + 1
                reanchor      = True

            # ── Render + push ─────────────────────────────────────────────────
            if should_render:
                try:
                    img = render_image(current_status, anim_step=next_step)
                except Exception as exc:
                    logger.error("render_image() failed: %s", exc)
                    img = None

                # Piggyback (2026-07-02): a same-mood data refresh
                # (battery/wifi/agent-connect/MCP activity...) that VISIBLY changed
                # the panel also advances the face -- free movement on a refresh
                # that's happening anyway -- but never faster than _FACE_MIN_SEC, so
                # a burst of quick MCP calls can't flip it every poll. Returning to
                # idle is a MOOD change (handled above), not a data_change, so it
                # never accelerates ("volver a idle no acelera, idle desespera").
                if (data_change and img is not None
                        and img.tobytes() != last_image_bytes
                        and (now - last_anim_tick) >= _FACE_MIN_SEC):
                    next_step = anim_step + 1
                    reanchor  = True
                    try:
                        img = render_image(current_status, anim_step=next_step)
                    except Exception as exc:
                        logger.error("render_image() failed: %s", exc)

                # Mirror the exact face this frame drew into face.json --
                # pick_variant is deterministic in
                # (mood, status, anim_step), so calling it again here
                # reproduces the SAME variant render_image() just used
                # internally, without render_image needing to return it.
                try:
                    face_variant = pick_variant(mood, current_status, next_step)
                    face_str = variant_repr(face_variant) if face_variant else None
                    _write_face_file(_FACE_FILE, face_str, mood)
                except Exception as exc:
                    logger.error("face.json mirror failed: %s", exc)

                if img is not None:
                    img_bytes = img.tobytes()
                    # Skip if the 1-bit image is pixel-for-pixel identical
                    if img_bytes != last_image_bytes or is_first_frame:
                        try:
                            backend.show(img, full=use_full)
                        except Exception as exc:
                            logger.error("backend.show() failed: %s", exc)
                        last_image_bytes = img_bytes
                        logger.debug(
                            "Frame rendered -- mood=%s  step=%d  full=%s",
                            mood, next_step, use_full,
                        )
                    else:
                        logger.debug(
                            "Frame skipped (identical image) -- mood=%s  step=%d",
                            mood, next_step,
                        )

                # Commit state after successful render
                anim_step      = next_step
                last_mood      = mood
                if reanchor:                 # only a real face change re-anchors the
                    last_anim_tick = now     # idle timer — data-only renders don't,
                                             # so the ~25s cadence survives heartbeats.
                is_first_frame = False

            time.sleep(_POLL_INTERVAL)

    finally:
        logger.info("Display service stopping")
        if _SLEEP_ON_EXIT:
            try:
                backend.close()
                logger.info("Backend closed")
            except Exception as exc:
                logger.warning("backend.close() failed: %s", exc)
        logger.info("Display service stopped")


def _self_check() -> None:
    """Cheap invariant check for _anim_interval: FAST at
    elapsed=0, SLOW at elapsed>=RAMP, monotonically increasing in between.
    Runs at every startup -- negligible cost, catches a broken lerp before
    it ships a wrong animation cadence."""
    assert _anim_interval(0) == _ANIM_FAST_SEC
    assert _anim_interval(_ANIM_RAMP_SEC) == _ANIM_SLOW_SEC
    assert _anim_interval(_ANIM_RAMP_SEC * 10) == _ANIM_SLOW_SEC  # clamped, not extrapolated
    prev = _anim_interval(0)
    for frac in (0.1, 0.25, 0.5, 0.75, 0.9, 1.0):
        cur = _anim_interval(_ANIM_RAMP_SEC * frac)
        assert cur >= prev, f"_anim_interval not monotonic at {frac}: {cur} < {prev}"
        prev = cur


if __name__ == "__main__":
    _self_check()
    run()
