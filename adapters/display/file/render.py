#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Camilo Brossard
"""Piumy -- display adapter, ``file`` backend, standalone renderer.

Reads status.json and renders the vector face card to a PNG.
Can be run directly on any PC without hardware.

When the shared renderer (``adapters/display/render.py``) is importable it
is used in preference -- giving the full vector face, QR-code support and
a single source of truth for the card layout.  The local fallback activates
automatically when the shared module or its optional deps are absent.

Env config (zero hardcode):
    PIMYWA_STATUS       path to status.json   (default: auto-discovered)
    PIMYWA_DISPLAY_OUT  output PNG            (default: display.png)
"""
import json
import os
import sys

# -- Try to use the shared renderer (one directory up) -------------------------
_DISPLAY_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
if _DISPLAY_DIR not in sys.path:
    sys.path.insert(0, _DISPLAY_DIR)

try:
    from render import render_image as _shared_render_image
    _HAS_SHARED = True
except ImportError:
    _HAS_SHARED = False

# -- PIL + local faces (always available) --------------------------------------
from PIL import Image, ImageDraw, ImageFont

_FILE_DIR = os.path.dirname(os.path.abspath(__file__))
if _FILE_DIR not in sys.path:
    sys.path.insert(0, _FILE_DIR)

from faces import face_for  # noqa: E402 (local: file/faces.py)

W, H = 250, 122

_FONT_CANDIDATES = (
    "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
    "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf",
    "C:/Windows/Fonts/DejaVuSans.ttf",
    "C:/Windows/Fonts/segoeui.ttf",
    "C:/Windows/Fonts/consola.ttf",
)


def load_font(size: int):
    for p in _FONT_CANDIDATES:
        if os.path.exists(p):
            try:
                return ImageFont.truetype(p, size)
            except Exception:
                pass
    return ImageFont.load_default()


def read_status(path: str) -> dict:
    try:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    except FileNotFoundError:
        return {"mood": "error", "speech": f"no status.json ({path})"}
    except json.JSONDecodeError:
        return {"mood": "error", "speech": "invalid status.json"}
    except OSError as exc:
        return {"mood": "error", "speech": str(exc)}


def _truncate(draw, text, font, max_w):
    if draw.textlength(text, font=font) <= max_w:
        return text
    while text and draw.textlength(text + "...", font=font) > max_w:
        text = text[:-1]
    return text + "..."


def _render_local(status: dict) -> Image.Image:
    """Minimal local renderer (no vector face, no qrcode dependency).

    Used only when the shared render module is unavailable.
    """
    img = Image.new("1", (W, H), 1)
    d = ImageDraw.Draw(img)
    small = load_font(12)
    big   = load_font(34)

    mood = status.get("mood", "idle")

    d.text((2, 1), "Piumy", font=small, fill=0)
    bat = status.get("battery")
    if isinstance(bat, int):
        s = f"{bat}%"
        d.text((W - d.textlength(s, font=small) - 2, 1), s, font=small, fill=0)
    d.line((0, 16, W, 16), fill=0)

    face = face_for(mood)
    d.text(((W - d.textlength(face, font=big)) / 2, 42), face, font=big, fill=0)

    foot = mood
    msg  = (status.get("speech") or status.get("last_msg") or "").replace("\n", " ")
    if msg:
        foot = f"{foot} - {msg}"
    d.line((0, H - 16, W, H - 16), fill=0)
    d.text((2, H - 14), _truncate(d, foot, small, W - 4), font=small, fill=0)

    return img


def render(status: dict, out: str) -> str:
    """Render *status* to *out* (PNG path); return the path."""
    if _HAS_SHARED:
        img = _shared_render_image(status, anim_step=0)
    else:
        img = _render_local(status)
    img.save(out)
    return out


def default_status_path() -> str:
    here = os.path.dirname(__file__)
    for p in (
        os.path.join(here, "..", "..", "..", "core", "status.json"),
        "status.json",
    ):
        if os.path.exists(p):
            return p
    return "status.json"


def main():
    path = os.getenv("PIMYWA_STATUS") or default_status_path()
    out  = os.getenv("PIMYWA_DISPLAY_OUT", "display.png")
    status = read_status(path)
    render(status, out)
    print(f"face rendered: {out}  (mood={status.get('mood')}, status={path})")


if __name__ == "__main__":
    main()
