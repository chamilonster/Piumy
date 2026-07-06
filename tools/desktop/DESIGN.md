# Piumy Desktop — floating panel companion (Windows `.exe`)

A desktop companion that puts the e-paper panel (the "carita") **floating on the
desktop** — frameless, always-on-top, draggable, pwnagotchi-style — with a live
terminal-log below it, and a button that opens the existing web dashboard in an
auto-login webview.

Two jobs, one window:
1. **Emulate the panel faithfully** by reusing `adapters/display/render.py` — the
   single source of truth. No re-implementation of the face/layout. (Proven
   headless on Windows: `render_image(status, step)` → 1-bit 250×122 PIL image.)
2. **Reach the full control UI** without friction — the dashboard, auto-logged-in,
   in an embedded webview.

## Goals
- Faithful 1-bit panel (render.py), live-animated, floating on the desktop.
- Live terminal-log (core stdout locally / journald on the Pi), green-on-black.
- One-click dashboard, no manual login.
- Ship as a single Windows `.exe` (PyInstaller).

## Non-goals (v1)
- No WhatsApp, no hardware, no editing state. Read-only monitor of the Pi.
- No re-implementing the face in JS/canvas (would duplicate render.py).

## UX — the floating widget
- **Tkinter**, frameless (`overrideredirect(True)`), always-on-top (`-topmost`),
  draggable (mouse bind), remembers last position. Optional transparency
  (`-alpha`) so it reads as "floating".
- **Top:** the render.py panel, upscaled nearest-neighbor to keep 1-bit pixels
  crisp (default ~4×). This IS the carita — battery bar, queue envelopes, wifi,
  face, all of it.
- **Below (collapsible):** the terminal-log — monospace, green-on-black,
  autoscroll, capped backlog (~2000 lines). Hidden by default → just the cute
  floating face; expand when you want the log.
- **Right-click menu / tiny toolbar:** Open Dashboard · Toggle log · Source
  (Local/Pi) · Start/Stop local core · Quit.

## Data sources — default LOCAL
The panel is driven by a `status` dict; the log by a stream of text lines. One
small interface, two implementations:

```
Source:
  start()                 # bring the source up
  status() -> dict        # latest status dict (for render_image)
  on_log(line_cb)         # push each new log line to the UI
  stop()
```

### LocalSource (default)
- Launch the Go core as a subprocess **in sandbox**: env
  `PIMYWA_GATEWAY=none`, `PIMYWA_DISPLAY=none`,
  `PIMYWA_STATUS=<tmpdir>/status.json`, REST bound to `127.0.0.1:<port>`.
  (The CORE writes status.json — the display service only reads it — so a
  sandbox core alone is enough. No Pi, no hardware.)
- `status()`: read `status.json` by mtime (like `service.py`) — or
  `GET http://127.0.0.1:<port>/api/status`.
- `on_log()`: capture the subprocess stdout/stderr line-by-line (the rich real
  log: moods, sends, MCP, gateway).
- Needs a Windows core binary `pimywa.exe` (built `GOOS=windows GOARCH=amd64`),
  bundled next to the app.

### PiSource (secondary)
- `status()`: `GET http://<pi>:8080/api/status` (poll ~1 s).
- `on_log()`: SSH `journalctl -u pimywa-core -u pimywa-display -f -o cat`
  (creds from the usual local file; read-only). `/api/events` is NOT a log — it
  is a minimal nudge stream — so the log comes from journald, not REST.

## Panel animation
- `from render import render_image, pick_variant, variant_repr`.
- Re-render on status change + an idle tick advancing `anim_step` with the
  FAST→SLOW cadence (reuse `service.py::_anim_interval` logic — the emulator is
  simpler: no partial-refresh backend, just repaint the image).

## Dashboard button (auto-login webview)
- **pywebview** (WebView2 on Windows) window loading the dashboard URL
  (`http://127.0.0.1:<port>/` local, `http://<pi>/` for Pi).
- The dashboard root requires a session cookie; there is no no-auth mode. So the
  app **auto-logins**: on landing at `/login`, inject JS to fill username +
  password and submit — the cookie then lives in the webview. The user never
  sees a login screen. Local: the app sets the sandbox core's password (knows
  it). Pi: the admin creds from the local secrets file.
- Note: a webview loads the page as a top-level document, so the dashboard's
  `X-Frame-Options: DENY` (which only blocks iframes) does not interfere.

## Packaging
- `pyinstaller --onefile --windowed --add-data "render.py;." --add-data
  "faces.py;." --add-data "fonts;fonts" --add-data "pimywa.exe;." desktop.py`
  → `Piumy.exe`.
- `build.ps1`: cross-build the sandbox core
  (`cd core && GOOS=windows GOARCH=amd64 go build -o ../tools/desktop/pimywa.exe .`)
  then run PyInstaller.

## Files (`coderoot/tools/desktop/`)
- `desktop.py`  — Tkinter floating widget + panel/animation loop + menu.
- `sources.py`  — `LocalSource`, `PiSource`.
- `dashboard.py`— pywebview launcher + auto-login.
- `build.ps1`   — build core + PyInstaller.
- `requirements.txt` — pillow, pywebview (paramiko only if PiSource SSH).
- `README.md`.

## Milestones (dispatch order)
1. **M1 — floating carita + log (LOCAL).** The heart: frameless always-on-top
   widget rendering render.py from a sandbox core, stdout log, PyInstaller `.exe`.
   Pareto: 80% of the value.
2. **M2 — Dashboard button** (pywebview auto-login).
3. **M3 — PiSource** (REST + SSH journald) + Local/Pi toggle.

## Verify (Murphy)
- M1: launch → the carita floats on the desktop and animates; the real core log
  scrolls; kill the core → log shows the exit, panel freezes, app does not crash.
- The panel matches render.py pixel-for-pixel (it *is* render.py).
- `Piumy.exe` runs on a clean Windows box with no Python installed.
