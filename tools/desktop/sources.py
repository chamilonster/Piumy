# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Camilo Brossard
"""Piumy Desktop -- status/log data sources.

One small interface, two implementations (see DESIGN.md):

    start()                 # bring the source up
    status() -> dict        # latest status dict (for render_image)
    on_log(line_cb)          # register a callback for each new log line
    stop()                   # bring the source down
    is_alive() -> bool       # still producing fresh data?

LocalSource: a sandboxed local copy of the Go core, driving render_image
from a real status.json. PiSource (M3): the same shape against a REAL Pi,
READ-ONLY -- REST poll for status, SSH journald for the log; never a command
that changes the Pi. Both degrade (is_alive() False + one log line) rather
than crash when their backend goes away.
"""
import json
import os
import queue
import secrets
import socket
import subprocess
import sys
import tempfile
import threading
import time
import urllib.request

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
                with open(self.status_path, encoding="utf-8") as fh:
                    self._last_status = json.load(fh)
                self._last_mtime = mtime
            except (OSError, ValueError):
                pass  # transient tmp+rename race -- keep the last good status
        return self._last_status


# -- PiSource (M3) --------------------------------------------------------------

_PI_CREDS_FILE = r"C:\proyectos\Piumy\pipass.txt"  # local secrets, never in git
_DEFAULT_PI_HOST = "192.168.1.79"
_DEFAULT_PI_REST_PORT = 8080
_DEFAULT_SSH_KEY = os.path.expanduser("~/.ssh/pimywa_pi")


def _read_pi_ssh_user() -> str | None:
    """Line 1 of the local secrets file is the SSH username -- read at
    runtime, never hardcoded/committed. Only the username is used here: SSH
    auth goes through the key (line 3 of that file notes key-auth is the
    live deploy's actual setup, password is a legacy fallback), so the
    line-2 password never needs to be read or passed around at all."""
    try:
        with open(_PI_CREDS_FILE, encoding="utf-8") as fh:
            first = fh.readline().strip()
        return first or None
    except OSError:
        return None


class PiSource:
    """Monitors a REAL Pi, READ-ONLY: REST poll for status (GET /api/status)
    ~1/s, SSH journald for the live log. Never issues a control/write
    command against the Pi. Degrades instead of crashing when the Pi is
    unreachable: is_alive() goes False, one `[Pi unreachable]` log line (not
    a repeat per poll), the caller's own tick loop freezes the panel on the
    last frame -- same contract LocalSource already honors.

    Zero hardcode: PIMYWA_PI_HOST / _REST_PORT / _SSH_USER / _SSH_KEY env
    vars override every default; the SSH username otherwise comes from the
    local secrets file.
    """

    def __init__(self):
        self.host = os.getenv("PIMYWA_PI_HOST", _DEFAULT_PI_HOST)
        self.rest_port = int(os.getenv("PIMYWA_PI_REST_PORT", str(_DEFAULT_PI_REST_PORT)))
        self.ssh_user = os.getenv("PIMYWA_PI_SSH_USER") or _read_pi_ssh_user() or "pi"
        self.ssh_key = os.getenv("PIMYWA_PI_SSH_KEY", _DEFAULT_SSH_KEY)

        self._log_cb = None
        self._stop_evt = threading.Event()
        self._poll_thread: threading.Thread | None = None
        self._ssh_thread: threading.Thread | None = None
        self._ssh_proc: subprocess.Popen | None = None
        self._last_status: dict = {"mood": "idle"}
        self._alive = False
        self._unreachable_logged = False

    def on_log(self, cb) -> None:
        self._log_cb = cb

    def start(self) -> None:
        self._stop_evt.clear()
        self._unreachable_logged = False
        self._poll_thread = threading.Thread(target=self._poll_loop, daemon=True)
        self._poll_thread.start()
        self._ssh_thread = threading.Thread(target=self._ssh_loop, daemon=True)
        self._ssh_thread.start()

    def stop(self) -> None:
        self._stop_evt.set()
        if self._ssh_proc is not None and self._ssh_proc.poll() is None:
            self._ssh_proc.terminate()
        self._alive = False

    def is_alive(self) -> bool:
        """True while the REST poll is currently succeeding -- the caller's
        tick loop uses this to decide whether to repaint or freeze."""
        return self._alive

    def status(self) -> dict:
        return self._last_status

    def _poll_loop(self) -> None:
        url = f"http://{self.host}:{self.rest_port}/api/status"
        while not self._stop_evt.is_set():
            try:
                with urllib.request.urlopen(url, timeout=2) as resp:
                    self._last_status = json.loads(resp.read().decode("utf-8"))
                self._alive = True
                self._unreachable_logged = False
            except Exception:
                self._alive = False
                if not self._unreachable_logged and self._log_cb:
                    self._log_cb("[Pi unreachable]")
                    self._unreachable_logged = True  # one line, not a spam loop
            self._stop_evt.wait(1.0)

    def _ssh_loop(self) -> None:
        # journald only -- /api/events is a nudge stream, not a log (DESIGN.md).
        args = [
            "ssh", "-i", self.ssh_key,
            "-o", "BatchMode=yes",           # never prompt -- key auth or fail
            "-o", "ConnectTimeout=5",
            "-o", "StrictHostKeyChecking=accept-new",
            f"{self.ssh_user}@{self.host}",
            "journalctl -u pimywa-core -u pimywa-display -f -o cat",
        ]
        try:
            self._ssh_proc = subprocess.Popen(
                args, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                text=True, bufsize=1, creationflags=_NO_WINDOW,
            )
        except OSError as exc:
            if self._log_cb:
                self._log_cb(f"[Pi SSH log unavailable: {exc}]")
            return
        if self._ssh_proc.stdout is None:
            return
        for line in self._ssh_proc.stdout:
            if self._stop_evt.is_set():
                break
            if self._log_cb:
                self._log_cb(line.rstrip("\n"))


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


def _pisource_self_check() -> None:
    """Config resolution + pre-start state -- always runnable, no network.
    A real reachability run (below) covers the actual degrade-or-monitor
    path, whichever the Pi happens to be doing right now."""
    os.environ["PIMYWA_PI_HOST"] = "10.0.0.99"
    os.environ["PIMYWA_PI_SSH_USER"] = "test-user"
    try:
        src = PiSource()
        assert src.host == "10.0.0.99", "env override for host not honored"
        assert src.ssh_user == "test-user", "env override for ssh user not honored"
        assert src.is_alive() is False, "should not be alive before start()"
        assert src.status() == {"mood": "idle"}, "status() should be the safe default before start()"
    finally:
        del os.environ["PIMYWA_PI_HOST"]
        del os.environ["PIMYWA_PI_SSH_USER"]
    print("PiSource config/pre-start self-check OK")


def _pisource_reachability_check() -> None:
    """Runs PiSource for real against whatever PIMYWA_PI_HOST resolves to
    (default: the real Pi) for a few seconds -- exercises the ACTUAL path
    live, whichever one that is right now: a reachable Pi (status/log flow)
    or an unreachable one (the graceful-degradation path this contract
    specifically asks to verify). Never asserts a particular outcome --
    only that whichever path runs behaves honestly (no crash, no fabricated
    status, at most one unreachable log line)."""
    src = PiSource()
    print(f"probing Pi at {src.host}:{src.rest_port} (ssh user={src.ssh_user!r})")
    lines = []
    src.on_log(lines.append)
    src.start()
    time.sleep(6)
    alive = src.is_alive()
    status = src.status()
    src.stop()
    time.sleep(0.5)
    if alive:
        print(f"Pi REACHABLE -- mood={status.get('mood')!r}, {len(lines)} log lines")
    else:
        assert lines.count("[Pi unreachable]") == 1, "expected exactly one unreachable line, not a spam loop"
        assert status == {"mood": "idle"}, "no fabricated status while unreachable"
        print("Pi UNREACHABLE -- degraded cleanly (one log line, no crash, no fabricated status)")
    assert not src.is_alive(), "PiSource did not stop"
    print("PiSource reachability check OK")


if __name__ == "__main__":
    _self_check()
    _pisource_self_check()
    _pisource_reachability_check()
