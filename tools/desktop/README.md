# Piumy Desktop (M1 — floating carita, M2 — dashboard button)

A floating, always-on-top, draggable widget that shows the e-paper panel on
your desktop — the SAME `adapters/display/render.py` the real Pi uses, fed by
a sandboxed local copy of the Go core (no WhatsApp, no hardware). Right-click
→ "Open Dashboard" opens that same sandbox's web dashboard, auto-logged-in,
in its own window. See [`DESIGN.md`](DESIGN.md) for the full plan.

M1 + M2, LOCAL source only. "Source: Local/Pi" (M3) is a greyed stub — a
separate subcontract.

## Run from source

```powershell
pip install -r requirements.txt
cd ..\..\core; go build -o ..\tools\desktop\pimywa.exe .; cd ..\tools\desktop
python desktop.py
```

Right-click the panel for the menu (toggle the log, stop/start the sandboxed
core, quit). Left-drag anywhere on the panel to move it — position is
remembered across restarts (`%LOCALAPPDATA%\Piumy\desktop_state.json`).

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
  (reusing `render_image`), collapsible log, right-click menu.
- `sources.py` — `LocalSource`: launches the Go core in a sandbox
  (`PIMYWA_GATEWAY=none`, dashboard on a loopback high port with a random
  password, own temp dir + free ports), exposes `status()` / `on_log()` /
  `stop()` / `is_alive()` / `dashboard_url` / `dash_user` / `dash_pass`.
- `dashboard.py` — pywebview launcher: opens the dashboard URL and
  auto-submits the login form. Runs as its **own process** (spawned by
  `desktop.py --dashboard <url> <user> <pass>`), not a thread — pywebview's
  Windows backend requires `webview.start()` on a process's main thread,
  which Tkinter's mainloop already occupies in the main app.
- `build.ps1` — builds the core + packages `Piumy.exe`.
