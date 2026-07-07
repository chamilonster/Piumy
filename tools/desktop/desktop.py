# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Camilo Brossard
"""Piumy Desktop -- floating carita companion (M1 carita + M2 dashboard + M3 Pi source).

A frameless, always-on-top, draggable Tkinter widget that shows the e-paper
panel -- rendered by the SAME `adapters/display/render.py` the real Pi uses,
so this is pixel-for-pixel the real carita, not a re-implementation. Two data
sources, same shape (sources.py): LocalSource (a sandboxed local copy of the
Go core: no WhatsApp, no hardware) and PiSource (the REAL Pi, read-only --
REST poll + SSH journald), toggled via the "Source: Local/Pi" menu entry. A
collapsible live log sits below the panel. "Open Dashboard" opens the LOCAL
sandbox's own dashboard, auto-logged-in, in a pywebview window (dashboard.py,
run as its own process -- see `_open_dashboard`); it only applies to
Source: Local. See DESIGN.md for the full plan.
"""
import json
import os
import queue
import subprocess
import sys
import tempfile
import time
import tkinter as tk

from PIL import Image, ImageTk

from sources import LocalSource, PiSource

# -- resolve the shared renderer (adapters/display/render.py) ---------------
# Dev mode: import in place from the repo -- one source of truth, no copy to
# drift. Frozen (PyInstaller onefile): build.ps1 bundles a COPY of render.py
# + fonts/ at the archive root (--add-data "render.py;." / "fonts;fonts"),
# extracted to sys._MEIPASS at runtime.


def _display_adapter_dir() -> str:
    if getattr(sys, "frozen", False):
        return sys._MEIPASS
    return os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "adapters", "display")


sys.path.insert(0, _display_adapter_dir())
from render import render_image  # noqa: E402 -- single source of truth for the face

SCALE = 1  # nearest-neighbor upscale of the 250x122 1-bit panel (owner feedback
           # 2026-07-05: the DESIGN.md default of 4x covered other windows with
           # no easy way to move it out of the way -- native size reads as a
           # small companion instead)
W, H = 250, 122
LOG_MAX_LINES = 2000
LOG_DRAIN_MS = 300  # independent of the animation cadence -- see _drain_log_tick

# Idle-animation cadence -- ported from adapters/display/service.py's
# _anim_interval (same "sobre de atencion" lerp: fast right after a mood
# change, ramping down to slow when quiet). Ported, not imported: the e-paper
# service couples this constant to its own env knobs and a partial-refresh
# backend this emulator doesn't have -- a plain repaint-every-tick loop is
# simpler standalone (Tk repaints are cheap; e-paper flashes are not).
_ANIM_FAST_SEC = 25.0
_ANIM_SLOW_SEC = 60.0
_ANIM_RAMP_SEC = 180.0


def _anim_interval(elapsed: float) -> float:
    """Lerp FAST -> SLOW over _ANIM_RAMP_SEC of quiet; see service.py."""
    if _ANIM_RAMP_SEC <= 0:
        return _ANIM_SLOW_SEC
    t = max(0.0, min(1.0, elapsed / _ANIM_RAMP_SEC))
    return _ANIM_FAST_SEC + (_ANIM_SLOW_SEC - _ANIM_FAST_SEC) * t


def _state_path() -> str:
    """Where the last window position is remembered -- %LOCALAPPDATA% so a
    packaged .exe run from anywhere still has a writable spot."""
    base = os.getenv("LOCALAPPDATA") or tempfile.gettempdir()
    d = os.path.join(base, "Piumy")
    os.makedirs(d, exist_ok=True)
    return os.path.join(d, "desktop_state.json")


class App:
    def __init__(self):
        self.source = LocalSource()
        self._log_q: queue.Queue = queue.Queue()
        self.source.on_log(self._log_q.put)

        self.root = tk.Tk()
        self.root.overrideredirect(True)          # frameless
        self.root.attributes("-topmost", True)    # always-on-top
        self.root.attributes("-alpha", 0.96)       # reads as "floating"
        self.root.configure(bg="white")

        self._anim_step = 0
        self._last_mood = ""
        self._last_interaction = time.monotonic()
        self._frozen_logged = False
        self._photo = None  # keep a live reference -- Tk drops GC'd images
        self._tick_id = None  # pending self.root.after() for _tick -- see _toggle_source

        self._build_ui()
        self._restore_position()
        self._start_source()
        self._tick()
        self._drain_log_tick()

    # -- UI ------------------------------------------------------------------
    def _build_ui(self):
        self.panel = tk.Label(self.root, bd=0, bg="white", cursor="fleur")
        self.panel.pack()
        self.panel.bind("<ButtonPress-1>", self._drag_start)
        self.panel.bind("<B1-Motion>", self._drag_move)
        self.panel.bind("<Button-3>", self._show_menu)

        # Collapsible log -- hidden by default ("just the cute floating
        # face"; expand when you want it).
        self.log_frame = tk.Frame(self.root, bg="black")
        self.log = tk.Text(
            self.log_frame, height=12, width=70, bg="black", fg="#00ff66",
            insertbackground="#00ff66", font=("Consolas", 9), state="disabled", bd=0,
        )
        self.log.pack(fill="both", expand=True)
        self._log_visible = False

        self.menu = tk.Menu(self.root, tearoff=0)
        self.menu.add_command(label="Open Dashboard", command=self._open_dashboard)
        self._source_index = self.menu.index("end") + 1
        self.menu.add_command(label="Source: Local", command=self._toggle_source)
        self.menu.add_separator()
        self.menu.add_command(label="Toggle log", command=self._toggle_log)
        self._start_stop_index = self.menu.index("end") + 1
        self.menu.add_command(label="Stop core", command=self._toggle_core)
        self.menu.add_separator()
        self.menu.add_command(label="Quit", command=self._quit)

        # Redundant, always-reachable close path -- owner feedback 2026-07-05:
        # a frameless/topmost window with only a maybe-finicky right-click
        # menu as the sole way out is a trap. Escape works regardless of the
        # menu's grab/focus behavior.
        self.root.bind("<Escape>", lambda e: self._quit())
        self.root.focus_force()

    def _drag_start(self, event):
        self.root.focus_force()  # keep Escape-to-quit reachable even if focus drifted
        self._drag_x, self._drag_y = event.x, event.y

    def _drag_move(self, event):
        x = self.root.winfo_pointerx() - self._drag_x
        y = self.root.winfo_pointery() - self._drag_y
        self.root.geometry(f"+{x}+{y}")

    def _show_menu(self, event):
        # overrideredirect windows don't get Windows focus on click like a
        # normal titled window -- without forcing it first, tk_popup's grab
        # can misbehave on Windows (menu not receiving clicks, or the window
        # left stuck with no visible way to reach Quit -- owner feedback
        # 2026-07-05: "no vi manera de cerrar eso"). grab_release in finally
        # so a menu dismissed by Escape/click-outside doesn't leave a stale
        # grab blocking the NEXT right-click.
        self.root.focus_force()
        try:
            self.menu.tk_popup(event.x_root, event.y_root)
        finally:
            self.menu.grab_release()

    def _toggle_log(self):
        self._log_visible = not self._log_visible
        if self._log_visible:
            self.log_frame.pack(fill="both", expand=True)
        else:
            self.log_frame.pack_forget()

    def _toggle_core(self):
        if self.source.is_alive():
            self.source.stop()
            self.menu.entryconfigure(self._start_stop_index, label="Start core")
        else:
            self._start_source()
            self._frozen_logged = False

    def _start_source(self):
        try:
            self.source.start()
            label = "Stop core"
        except FileNotFoundError as exc:
            self._append_log(f"[{exc}]")
            label = "Start core"
        self.menu.entryconfigure(self._start_stop_index, label=label)

    def _toggle_source(self):
        # Swap the data source in place: stop the current one, bring up the
        # other, re-hook the log queue (a fresh source instance needs its own
        # on_log registration), keep the same tick loop/panel/log running.
        self.source.stop()
        if isinstance(self.source, LocalSource):
            self.source = PiSource()
            label = "Source: Pi"
        else:
            self.source = LocalSource()
            label = "Source: Local"
        self.source.on_log(self._log_q.put)
        self._frozen_logged = False
        self._start_source()
        self.menu.entryconfigure(self._source_index, label=label)
        # Force an immediate refresh instead of waiting for whatever slow
        # idle-animation interval was already pending (up to 60s, see
        # _anim_interval) -- a source swap should show up right away, not on
        # the next scheduled tick. Cancel that pending call first: _tick()
        # re-schedules itself, so calling it directly without cancelling
        # would leave TWO chains running in parallel from here on (each
        # further toggle adding another) -- after_cancel on an already-fired
        # id is a safe no-op, so this is correct even on the very first tick.
        if self._tick_id is not None:
            self.root.after_cancel(self._tick_id)
        self._tick()

    def _open_dashboard(self):
        # dashboard.py's webview.start() must run on a process's main thread
        # (pywebview raises on Windows otherwise) -- Tkinter's mainloop
        # already owns this one, so the dashboard runs as its OWN process,
        # not a thread. Frozen: re-exec this same Piumy.exe with a hidden
        # mode flag (see the bottom of this file); dev: re-exec via the
        # current Python interpreter + this script's path.
        # LOCAL only -- PiSource has no sandboxed dashboard to open (the real
        # Pi's own dashboard is out of scope for this button).
        if not isinstance(self.source, LocalSource):
            self._append_log("[Open Dashboard: only available on Source: Local]")
            return
        if not self.source.is_alive():
            self._append_log("[Open Dashboard: core is not running]")
            return
        if getattr(sys, "frozen", False):
            args = [sys.executable]
        else:
            args = [sys.executable, os.path.abspath(__file__)]
        args += ["--dashboard", self.source.dashboard_url, self.source.dash_user, self.source.dash_pass]
        subprocess.Popen(args)

    # -- position persistence -------------------------------------------------
    def _restore_position(self):
        try:
            with open(_state_path(), encoding="utf-8") as fh:
                pos = json.load(fh)
            self.root.geometry(f"+{int(pos['x'])}+{int(pos['y'])}")
        except (OSError, ValueError, KeyError):
            self.root.geometry("+100+100")

    def _save_position(self):
        try:
            with open(_state_path(), "w", encoding="utf-8") as fh:
                json.dump({"x": self.root.winfo_x(), "y": self.root.winfo_y()}, fh)
        except OSError:
            pass

    # -- log -------------------------------------------------------------------
    def _append_log(self, line: str):
        self.log.configure(state="normal")
        self.log.insert("end", line + "\n")
        n = int(self.log.index("end-1c").split(".")[0])
        if n > LOG_MAX_LINES:
            self.log.delete("1.0", f"{n - LOG_MAX_LINES}.0")
        self.log.see("end")
        self.log.configure(state="disabled")

    def _drain_log(self):
        try:
            while True:
                self._append_log(self._log_q.get_nowait())
        except queue.Empty:
            pass

    def _drain_log_tick(self):
        # Independent fast timer, decoupled from _tick()'s slow idle-animation
        # cadence (which can legitimately be up to _ANIM_SLOW_SEC between
        # calls) -- a background source thread's log line (e.g. PiSource's
        # "[Pi unreachable]") must show up promptly, not whenever the panel
        # next happens to repaint.
        self._drain_log()
        self.root.after(LOG_DRAIN_MS, self._drain_log_tick)

    # -- animation / repaint ----------------------------------------------------
    def _tick(self):
        if self.source.is_alive():
            status = self.source.status()
            mood = status.get("mood", "idle")
            if mood != self._last_mood:
                self._anim_step = 0
                self._last_interaction = time.monotonic()
                self._last_mood = mood
            else:
                self._anim_step += 1
            self._repaint(status)
            self._frozen_logged = False
        elif not self._frozen_logged:
            # Source went away (LocalSource crashed/stopped, or PiSource lost
            # the Pi -- it already logged its own "[Pi unreachable]" line) --
            # leave the last frame on screen instead of animating stale data.
            self._append_log("[source unavailable -- panel frozen]")
            self._frozen_logged = True

        interval = _anim_interval(time.monotonic() - self._last_interaction)
        self._tick_id = self.root.after(int(interval * 1000), self._tick)

    def _repaint(self, status: dict):
        img = render_image(status, anim_step=self._anim_step)
        img = img.convert("RGB").resize((W * SCALE, H * SCALE), Image.NEAREST)
        self._photo = ImageTk.PhotoImage(img)
        self.panel.configure(image=self._photo)

    # -- shutdown -----------------------------------------------------------------
    def _quit(self):
        self._save_position()
        self.source.stop()
        self.root.destroy()

    def run(self):
        self.root.protocol("WM_DELETE_WINDOW", self._quit)  # safety net (no titlebar close button)
        self.root.mainloop()


def _self_check() -> None:
    """Cheap invariant check for the ported anim cadence (mirrors
    service.py's own _self_check) -- catches a broken lerp before it ships."""
    assert _anim_interval(0) == _ANIM_FAST_SEC
    assert _anim_interval(_ANIM_RAMP_SEC) == _ANIM_SLOW_SEC
    assert _anim_interval(_ANIM_RAMP_SEC * 10) == _ANIM_SLOW_SEC
    prev = _anim_interval(0)
    for frac in (0.25, 0.5, 0.75, 1.0):
        cur = _anim_interval(_ANIM_RAMP_SEC * frac)
        assert cur >= prev, f"_anim_interval not monotonic at {frac}"
        prev = cur


def _selfcheck_no_tick_leak() -> None:
    """Regression check (Citrino caught this in review): _toggle_source()
    calls _tick() directly for an immediate refresh, since _tick's own
    schedule can legitimately be up to _ANIM_SLOW_SEC away. _tick()
    re-schedules itself via self.root.after() -- calling it directly
    without cancelling the already-pending one first would leave an EXTRA
    parallel chain running per toggle (accumulating: N toggles -> N chains,
    the panel repainting/advancing faster with every one). Not run on every
    launch (spins up a real App -- real subprocess core, real Tk window --
    too slow/visible for routine startup); run manually: `python desktop.py
    --selfcheck-ticks`.
    """
    app = App()
    try:
        before = len(app.root.tk.call("after", "info"))
        for _ in range(5):
            app._toggle_source()
        after = len(app.root.tk.call("after", "info"))
        assert after <= before, (
            f"pending after() count grew ({before} -> {after}) across 5 toggles -- tick chain leaking"
        )
    finally:
        app.source.stop()
        app.root.destroy()
    print("no-tick-leak self-check OK")


if __name__ == "__main__":
    # Hidden re-exec mode (see App._open_dashboard): this same script/exe,
    # spawned as its OWN process so pywebview's start() gets a fresh main
    # thread instead of fighting Tkinter's mainloop for the one in this
    # process. Never reached via a normal double-click/launch.
    if len(sys.argv) == 5 and sys.argv[1] == "--dashboard":
        import dashboard
        dashboard.open_dashboard(sys.argv[2], sys.argv[3], sys.argv[4])
    elif len(sys.argv) == 2 and sys.argv[1] == "--selfcheck-ticks":
        _selfcheck_no_tick_leak()
    else:
        _self_check()
        App().run()
