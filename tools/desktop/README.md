# Piumy Desktop (M1 carita · M2 dashboard button · M3 Pi source)

A floating, always-on-top, draggable widget that shows the e-paper panel on
your desktop — the SAME `adapters/display/render.py` the real Pi uses. Two
data sources, toggled from the right-click menu ("Source: Local/Pi"):

- **Local** — a sandboxed local copy of the Go core (no WhatsApp, no
  hardware). "Open Dashboard" opens that sandbox's own web dashboard,
  auto-logged-in, in its own window.
- **Pi** — the REAL Pi, **read-only**: `GET /api/status` polled over REST,
  the live log streamed from `journalctl` over SSH. Degrades gracefully (one
  `[Pi unreachable]` log line, panel frozen on the last frame, no crash) if
  the Pi is off or unreachable.

A **system tray icon** keeps the app reachable once the carita is hidden:
show/hide, open the dashboard, quit — the tray icon's face is cropped from a
real `render_image()` frame, not a separate drawing.

See [`DESIGN.md`](DESIGN.md) for the full plan. All three milestones done —
the companion is feature-complete.

## Run from source

```powershell
pip install -r requirements.txt
cd ..\..\core; go build -o ..\tools\desktop\pimywa.exe .; cd ..\tools\desktop
python desktop.py
```

Right-click the panel for the menu (toggle the log, stop/start the sandboxed
core, Local/Pi toggle, Open Dashboard, quit). Left-drag anywhere on the panel
to move it — position is remembered across restarts
(`%LOCALAPPDATA%\Piumy\desktop_state.json`). The tray icon (bottom-right of
the taskbar, may be under the "^" overflow arrow the first time Windows sees
it) has its own menu: show/hide the carita, open the dashboard, quit — this
is the only way back once the carita itself is hidden, so it always has its
own Quit too.

## Build the standalone `.exe`

```powershell
pip install -r requirements.txt pyinstaller
.\build.ps1
```

Produces `dist\Piumy.exe` — bundles `pimywa.exe` (the sandboxed core),
`render.py` and `fonts/` (the shared face renderer), no separate Python or Go
install needed on the target machine.

## Files

- `desktop.py` — the Tkinter widget: frameless window, panel repaint loop
  (reusing `render_image`), collapsible log (drained on its own fast timer,
  independent of the slow idle-animation cadence), right-click menu
  (Local/Pi toggle, Open Dashboard, start/stop, quit), and the system tray
  icon (`pystray`, its own thread -- no main-thread restriction on Windows,
  unlike pywebview).
- `sources.py` — `LocalSource` (sandboxed core: `PIMYWA_GATEWAY=none`,
  dashboard on a loopback high port with a random password, own temp dir +
  free ports) and `PiSource` (the real Pi, read-only: REST poll +
  `journalctl` over SSH). Same shape: `status()` / `on_log()` / `stop()` /
  `is_alive()`.
- `dashboard.py` — pywebview launcher: opens the dashboard URL and
  auto-submits the login form. Runs as its **own process** (spawned by
  `desktop.py --dashboard <url> <user> <pass>`), not a thread — pywebview's
  Windows backend requires `webview.start()` on a process's main thread,
  which Tkinter's mainloop already occupies in the main app. Local source
  only (the Pi's own dashboard is out of scope for this button).
- `build.ps1` — builds the core + packages `Piumy.exe`.

## PiSource configuration

Zero hardcode: `PIMYWA_PI_HOST` (default `192.168.1.79`), `PIMYWA_PI_REST_PORT`
(default `8080`), `PIMYWA_PI_SSH_USER`, `PIMYWA_PI_SSH_KEY` (default
`~/.ssh/pimywa_pi`) all override their defaults via env. The SSH username
otherwise comes from the local secrets file (`C:\proyectos\Piumy\pipass.txt`,
line 1) — never committed. SSH auth is key-only (`BatchMode=yes`); the
secrets file's line-2 password is legacy and never read by this tool.
