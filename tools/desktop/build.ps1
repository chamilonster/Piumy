# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Camilo Brossard
#
# Builds Piumy.exe: builds the sandboxed core for windows/amd64 (native on a
# Windows dev box -- GOOS/GOARCH are set explicitly anyway so this also works
# from a cross-compiling host), then bundles it + the shared display
# renderer into a single PyInstaller executable.
#
# Requires: Go toolchain on PATH; Python with pillow + pyinstaller installed
# (pip install -r requirements.txt pyinstaller).

$ErrorActionPreference = "Stop"

$Desktop     = $PSScriptRoot                                        # coderoot/tools/desktop
$CoderootDir = Split-Path -Parent (Split-Path -Parent $Desktop)     # coderoot
$CoreDir     = Join-Path $CoderootDir "core"
$DisplayDir  = Join-Path $CoderootDir "adapters/display"

Write-Host "== Building sandboxed core (windows/amd64) =="
Push-Location $CoreDir
try {
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    go build -o (Join-Path $Desktop "pimywa.exe") .
} finally {
    Pop-Location
}

Write-Host "== Packaging Piumy.exe (PyInstaller) =="
Push-Location $Desktop
try {
    # render.py is the single source of truth for the face (adapters/display/)
    # -- bundled as a plain copy at the archive root, same layout desktop.py's
    # _display_adapter_dir() expects when frozen (sys._MEIPASS). fonts/ rides
    # alongside it for the same reason render.py itself needs it un-frozen.
    # No faces.py: render.py is self-contained (PIL + os + random only).
    # via the `py` launcher + `-m PyInstaller`, not a bare `python`/`pyinstaller`
    # -- this box has more than one Python install and PATH order picks
    # whichever one happens to resolve first, which may not be the one
    # `pip install -r requirements.txt pyinstaller` targeted.
    # render.py is bundled as a raw DATA file (--add-data), not analyzed as
    # code -- PyInstaller's static import scan never sees ITS imports, so
    # PIL.ImageDraw/ImageFont (only imported inside render.py, not desktop.py)
    # need to be hinted explicitly or the frozen exe ImportErrors on launch.
    py -m PyInstaller --onefile --windowed --noconfirm `
        --name Piumy `
        --hidden-import PIL.ImageDraw `
        --hidden-import PIL.ImageFont `
        --add-data "$(Join-Path $DisplayDir 'render.py');." `
        --add-data "$(Join-Path $DisplayDir 'fonts');fonts" `
        --add-data "pimywa.exe;." `
        desktop.py
} finally {
    Pop-Location
}

Write-Host "== Done: $(Join-Path $Desktop 'dist\Piumy.exe') =="
