# Piumy Desktop (M1 — floating carita, LOCAL source)

A floating, always-on-top, draggable widget that shows the e-paper panel on
your desktop — the SAME `adapters/display/render.py` the real Pi uses, fed by
a sandboxed local copy of the Go core (no WhatsApp, no hardware). See
[`DESIGN.md`](DESIGN.md) for the full plan.

M1 only: LOCAL source. "Open Dashboard" (M2) and "Source: Local/Pi" (M3) are
greyed stubs — separate subcontracts.

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
  (`PIMYWA_GATEWAY=none`, `PIMYWA_DASH=0`, own temp dir + free ports),
  exposes `status()` / `on_log()` / `stop()` / `is_alive()`.
- `build.ps1` — builds the core + packages `Piumy.exe`.
