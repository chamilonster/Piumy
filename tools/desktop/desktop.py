# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Camilo Brossard
"""Piumy Desktop -- floating carita companion (M1: LOCAL source only).

A frameless, always-on-top, draggable Tkinter widget that shows the e-paper
panel -- rendered by the SAME `adapters/display/render.py` the real Pi uses,
so this is pixel-for-pixel the real carita, not a re-implementation -- fed by
a sandboxed local copy of the Go core (sources.LocalSource: no WhatsApp, no
hardware). A collapsible live log sits below it. See DESIGN.md for the full
plan; M2 (dashboard webview) and M3 (Pi source) are separate subcontracts --
their menu entries exist here only as greyed stubs.
"""
import json
import os
import queue
import sys
import tempfile
import time
import tkinter as tk

from PIL import Image, ImageTk

from sources import LocalSource

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

        self._build_ui()
        self._restore_position()
        self._start_source()
        self._tick()

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
        self.menu.add_command(label="Open Dashboard", state="disabled")  # M2
        self.menu.add_command(label="Source: Local", state="disabled")   # M3
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

    # -- animation / repaint ----------------------------------------------------
    def _tick(self):
        self._drain_log()
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
            # Core died (crash or "Stop core") -- leave the last frame on
            # screen (no re-render below) instead of animating stale data.
            self._append_log("[core exited -- panel frozen]")
            self._frozen_logged = True

        interval = _anim_interval(time.monotonic() - self._last_interaction)
        self.root.after(int(interval * 1000), self._tick)

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


if __name__ == "__main__":
    _self_check()
    App().run()
