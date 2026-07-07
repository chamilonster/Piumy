# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Camilo Brossard
"""Piumy Desktop -- status/log data sources.

One small interface, two implementations (see DESIGN.md):

    start()                 # bring the source up
    status() -> dict        # latest status dict (for render_image)
    on_log(line_cb)          # register a callback for each new log line
    stop()                   # bring the source down
    is_alive() -> bool       # still producing fresh data?

LocalSource (this file) is the only one M1 builds: a sandboxed local copy of
the Go core, gateway/dashboard disabled, driving render_image from a real
status.json. PiSource (REST poll + SSH journald against the real Pi) is M3 --
out of scope here; desktop.py only shows it as a greyed menu stub.
"""
import os
import queue
import secrets
import socket
import subprocess
import sys
import tempfile
import threading
import time

# Go's log package writes to stderr; merged into stdout so ordering is
# preserved in one stream (DESIGN.md: "the rich real log: moods, sends, MCP,
# gateway" -- all through one reader).
_NO_WINDOW = getattr(subprocess, "CREATE_NO_WINDOW", 0)


def _free_port() -> int:
    """Ask the OS for an unused localhost port (stdlib bind-to-0 trick)."""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _exe_dir() -> str:
    """Directory to look for pimywa.exe in: PyInstaller's extraction dir when
    frozen (--add-data "pimywa.exe;." puts it at the bundle root), the script's
    own directory in dev mode (build.ps1 drops it right here)."""
    return getattr(sys, "_MEIPASS", os.path.dirname(os.path.abspath(__file__)))


class LocalSource:
    """Runs the Go core as a sandboxed subprocess: no WhatsApp (PIMYWA_GATEWAY
    =none), dashboard on a loopback-only high port with a random password
    (M2's "Open Dashboard" webview), all state files redirected into a
    private temp dir so nothing touches a real deploy.
    """

    def __init__(self, exe_path: str | None = None):
        self.exe_path = exe_path or os.path.join(_exe_dir(), "pimywa.exe")
        self.sandbox_dir = tempfile.mkdtemp(prefix="piumy_sandbox_")
        self.status_path = os.path.join(self.sandbox_dir, "status.json")
        self.api_port = _free_port()
        self.mcp_port = _free_port()
        self.dash_port = _free_port()
        self.dash_user = "admin"
        # Random, session-local -- nobody types this; the M2 dashboard button
        # knows it (LocalSource generated it) and auto-fills the login form.
        self.dash_pass = secrets.token_urlsafe(18)
        self.dashboard_url = f"http://127.0.0.1:{self.dash_port}/"

        self.proc: subprocess.Popen | None = None
        self._log_cb = None
        self._log_thread: threading.Thread | None = None
        self._last_mtime: float | None = None
        self._last_status: dict = {"mood": "idle"}

    # -- lifecycle -------------------------------------------------------
    def start(self) -> None:
        """Launch (or relaunch) the sandboxed core. Raises FileNotFoundError
        if pimywa.exe hasn't been built yet (see build.ps1) -- callers should
        catch this and keep running with no live core rather than crash."""
        if not os.path.exists(self.exe_path):
            raise FileNotFoundError(
                f"pimywa.exe not found at {self.exe_path} -- run build.ps1 first"
            )

        env = dict(os.environ)
        env.update({
            "PIMYWA_STATUS": self.status_path,
            "PIMYWA_DB": os.path.join(self.sandbox_dir, "pimywa.db"),
            "PIMYWA_ROUTER": os.path.join(self.sandbox_dir, "router.json"),
            "PIMYWA_SESSION_DB": os.path.join(self.sandbox_dir, "wa.db"),
            "PIMYWA_MEDIA_DIR": os.path.join(self.sandbox_dir, "media"),
            "PIMYWA_BACKUP_DIR": os.path.join(self.sandbox_dir, "backups"),
            "PIMYWA_DECISION_POLICY": os.path.join(self.sandbox_dir, "decision-policy.md"),
            "PIMYWA_BATTERY_FILE": os.path.join(self.sandbox_dir, "battery.json"),
            "PIMYWA_FACE_FILE": os.path.join(self.sandbox_dir, "face.json"),
            "PIMYWA_GATEWAY": "none",
            # Dashboard ON, loopback-only high port (M2: the default ":80"
            # needs admin on Windows and this box only needs it reachable to
            # the local webview, never the LAN).
            "PIMYWA_DASH": "1",
            "PIMYWA_DASH_ADDR": f"127.0.0.1:{self.dash_port}",
            "PIMYWA_DASH_USER": self.dash_user,
            "PIMYWA_DASH_PASS": self.dash_pass,
            "PIMYWA_API_ADDR": f"127.0.0.1:{self.api_port}",
            "PIMYWA_MCP_ADDR": f"127.0.0.1:{self.mcp_port}",
        })
        self.proc = subprocess.Popen(
            [self.exe_path, "serve"],
            env=env,
            cwd=self.sandbox_dir,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            bufsize=1,
            creationflags=_NO_WINDOW,
        )
        self._last_mtime = None
        self._last_status = {"mood": "idle"}
        self._log_thread = threading.Thread(target=self._pump_log, daemon=True)
        self._log_thread.start()

    def on_log(self, cb) -> None:
        """Register the callback invoked (from a background thread) with each
        new log line. Persists across start()/stop()/start() restarts."""
        self._log_cb = cb

    def _pump_log(self) -> None:
        proc = self.proc
        if proc is None or proc.stdout is None:
            return
        for line in proc.stdout:
            if self._log_cb:
                self._log_cb(line.rstrip("\n"))
        # ponytail: no distinct "process crashed vs stopped cleanly" line here
        # -- is_alive() + the caller's own tick loop already surfaces that by
        # noticing the process died; add an exit-code line if that's ever not
        # enough signal on its own.

    def stop(self) -> None:
        """Hard-stop the sandboxed core (TerminateProcess on Windows -- no
        graceful SIGTERM path from a Python parent; acceptable here since
        PIMYWA_GATEWAY=none means there's no WhatsApp session to protect).
        """
        if self.proc is not None and self.proc.poll() is None:
            self.proc.terminate()
            try:
                self.proc.wait(timeout=3)
            except subprocess.TimeoutExpired:
                self.proc.kill()

    def is_alive(self) -> bool:
        return self.proc is not None and self.proc.poll() is None

    # -- status -----------------------------------------------------------
    def status(self) -> dict:
        """Read status.json by mtime (only re-parses on real change); returns
        the last good dict when the file is missing/mid-write/unreadable --
        never raises, never a fabricated field, just stale-but-honest data."""
        try:
            mtime = os.path.getmtime(self.status_path)
        except OSError:
            return self._last_status
        if mtime != self._last_mtime:
            try:
                import json
                with open(self.status_path, encoding="utf-8") as fh:
                    self._last_status = json.load(fh)
                self._last_mtime = mtime
            except (OSError, ValueError):
                pass  # transient tmp+rename race -- keep the last good status
        return self._last_status


def _self_check() -> None:
    """Smoke-test LocalSource end to end: start the sandboxed core, wait for
    a real status.json, confirm a mood shows up, stop it cleanly. This IS the
    M1 "Verify" step from the contract, scripted so it can be re-run any time
    without the Tk UI."""
    src = LocalSource()
    print(f"sandbox: {src.sandbox_dir}")
    lines = []
    src.on_log(lines.append)
    src.start()
    try:
        deadline = time.monotonic() + 10
        mood = None
        while time.monotonic() < deadline:
            s = src.status()
            if "mood" in s and os.path.exists(src.status_path):
                mood = s["mood"]
                break
            time.sleep(0.2)
        assert mood is not None, "status.json never appeared"
        assert src.is_alive(), "core exited unexpectedly"
        print(f"OK -- mood={mood!r}, {len(lines)} log lines captured")
    finally:
        src.stop()
        time.sleep(0.3)
        assert not src.is_alive(), "core did not stop"
    print("self-check OK")


if __name__ == "__main__":
    _self_check()
