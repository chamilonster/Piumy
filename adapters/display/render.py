# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Camilo Brossard
"""Piumy display renderer — kaomoji face + margin-grid data panel.

Public API
----------
render_image(status, anim_step=0) -> PIL.Image (mode "1", 250x122 landscape)
pick_variant(mood, status, anim_step) -> dict  (face variant for this step)
variant_repr(v) -> str  (literal kaomoji string for a pick_variant() result --
    service.py uses this to mirror the SAME face string
    into the face.json sidecar for the dashboard, so the dashboard shows the
    real e-paper face without duplicating KAOMOJI_CATALOG/the eye engine.)

v2 redesign (approved via design-refs/ mockups +
real-Pi renders — mockup_battery.py, mockup_floating.py, kaotest.py +
kaotest_pi.png, gazetest.py + gazetest_pi.png). On top of the v1 layout
(commits 95839af + 77f74ed):

  FACE — real pwnagotchi kaomoji as TEXT ("asi es pwnagotchi,
    es lo que siempre te he pedido"; "temas animalito NO"). Replaces the old
    vector _draw_face entirely. Drawn with the BUNDLED DejaVuSans.ttf (see
    fonts/, and _FONTS_R/_FONTS_B below) so it renders identically
    on a dev PC and on the Pi — glyph coverage verified on the real Pi with
    this exact font (kaotest_pi.png, gazetest_pi.png). Gaze/aliveness is a
    glyph SWAP (◐‿◐ / ●‿● / ◑‿◑ / ◓‿◓ / ◒‿◒ — no image rotation), same trick
    real pwnagotchi uses. See KAOMOJI_CATALOG + _VERIFIED_GLYPHS.
  LAYOUT — margin standard + invisible grid ("imagina lineas que no se
    ven y hay que respetarlas"). M=5 safe margin, G=6 gutter, MID=100 middle-
    column guide — ALL placement routes through these, no loose coordinates.
    Ported from design-refs/mockup_battery.py:
      Left column (x=M)   — "Piumy" (big brand, 2026-07-02 follow-up:
                               reverted from "PIMYWA") / hostname, stacked.
      Middle column (x=MID) — wifi bars + SSID (top), IP below, left-aligned
                               to x=MID so it falls directly under the wifi
                               icon (2026-07-02 follow-ups — was
                               CPU/RAM, removed from the panel; IP moved
                               here from the left column, then re-aligned
                               under wifi after a centred first pass).
      Top-right corner    — battery (pinned to W-M) + time-remaining (left
                               of it) + voltage (below it).
      Footer (bottom) — connected-agents robot "xN" + QUEUE + SENT, grouped
                               at the LEFT (x=M, gutter G). STATUS word
                               (online/offline/paused/muted) centred in
                               whatever empty space is left on the RIGHT
                               (2026-07-02, second follow-up: brought
                               back after being removed for reading
                               redundant with the speech line -- no longer
                               redundant now that the speech line itself
                               is gone).
      Face (left, cx~116 cy~68) -- no speech line under it anymore.
      Sky (right of the face) — floating emoticons, multi-source + capped
                               (see milestone F below).
  BATTERY — ~20% longer icon, solid black when full, drains from the + tip
    toward the − base (fill anchored at −, grows toward +). Charging draws a
    bolt inside the outline instead. Unknown/no battery draws a centred "?".
    Real voltage (mV) and an adaptive, self-calibrating time-remaining
    estimate (adapters/power/timeremain.py) ride along in status.json.
  CPU/RAM — REMOVED from the panel (2026-07-02 follow-up); the core
    still writes cpu/ram into status.json for other consumers (e.g. the
    dashboard), this module just stops drawing them.
  FLOATING EMOTICONS — extends the v1 hitbox/rejection-sampling system to
    multiple sources: battery-low/charging > mail (queue) > pending ("?") >
    ambient sparkle, by priority, capped at 3 total. See _glyph_kinds.

Contract
--------
status dict fields (status.schema.json):
  mood, speech, queue, last_msg, wa_connected, show_qr, qr_data,
  battery, voltage, charging, time_remaining, cpu, ram, wifi (0..4 or null),
  ip, hostname, ssid, uptime, own_jid, sent, muted, agent_connected, agents,
  reconnect_paused, updated_at.

TEXT RULE (relaxed for the face only): the kaomoji face
uses unicode by design — see _VERIFIED_GLYPHS, the closed set of characters
already proven tofu-free on the real Pi with the bundled font. Every OTHER
string drawn with d.text() stays ASCII-only; free-form core-provided fields
(ssid, hostname) are passed through _ascii() so a non-ASCII value can never
break that rule.

NO-PLACEHOLDER RULE ("no quiero ni un placeholder"): battery,
voltage, charging's time-remaining, sent, ssid, uptime, cpu and ram are drawn
ONLY when the core provides a real value — a missing/None/non-int value
means "don't show it", never a fabricated default.
"""
import os
import random

from PIL import Image, ImageDraw, ImageFont

# ── Canvas ────────────────────────────────────────────────────────────────────
W, H = 250, 122
BLACK, WHITE = 0, 1

# ── Layout — margin standard + invisible grid ───────────────────────────────
# "hay que respetar los margenes... imagina lineas que no se ven y hay
# que respetarlas, con ellas alineamos y se ve coherente sin usarlas". EVERY
# placement in render_image() routes through these named guides — no loose
# coordinates. Ported from design-refs/mockup_battery.py.
M = 5                  # safe margin, all 4 edges
G = 6                  # gutter between elements
MID = 100               # middle-column guide (wifi/ssid/ip)
# Face centre. 2026-07-02 follow-ups: first "quiero que crezca mucho
# mas" (34->52, cx pushed to 116 to avoid clipping) -- then, once deployed,
# "la carita... justo al medio y demaciado enorme, va hacia la izquierda
# los emujos a la derecha": 52/116 read as dead-centre and left no real
# room for the floating emoticons on the right. Backed off to 40/90 -- a
# real jump from the original 34 (not "enorme"), and cx=90 (not 116) reads
# as LEFT again, freeing ~75px on the right for the sky instead of ~25px.
# Both numbers measured (not guessed): at 40 the widest KAOMOJI_CATALOG
# entry's real ink bbox is 168px wide, 39px tall -- 90 is comfortably past
# the 89px minimum that avoids clipping the LEFT edge. cy=65 centres the
# face vertically in the band between the top chrome (~y34) and the footer
# (~y101).
_CX, _CY = 90, 65
PAD = 3                 # hitbox padding between floating glyphs / reserved zones

# ── Font discovery (cross-platform, bundled-first) ────────────────────────────
# "bundle DejaVuSans.ttf inside the display adapter...
# load it explicitly. Do NOT rely on system font discovery for the face" —
# the bundled copy (fonts/, freely-redistributable DejaVu) is tried FIRST so
# a dev PC (which may lack DejaVu -> tofu) and the Pi render byte-identical
# faces. System paths remain as a defensive fallback only.
_FONT_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "fonts")

_FONTS_R: tuple = (
    os.path.join(_FONT_DIR, "DejaVuSans.ttf"),
    "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
    "C:/Windows/Fonts/DejaVuSans.ttf",
    "C:/Windows/Fonts/segoeui.ttf",
    "C:/Windows/Fonts/arial.ttf",
)
_FONTS_B: tuple = (
    os.path.join(_FONT_DIR, "DejaVuSans-Bold.ttf"),
    "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf",
    "C:/Windows/Fonts/DejaVuSans-Bold.ttf",
    "C:/Windows/Fonts/segoeuib.ttf",
    "C:/Windows/Fonts/arialbd.ttf",
)
# _FONTS_I (DejaVuSans-Oblique) is unused now that the speech/chatter line
# is gone (2026-07-02, second follow-up) -- fonts/DejaVuSans-Oblique.ttf
# stays bundled (harmless, part of the DejaVu family) in case italic text
# comes back later.

_font_cache: dict = {}


def _font(size: int, family: tuple = _FONTS_R) -> ImageFont.FreeTypeFont:
    """Load (and cache) a TrueType font; falls back to PIL built-in default."""
    key = (size, id(family))
    if key not in _font_cache:
        for p in family:
            if os.path.exists(p):
                try:
                    _font_cache[key] = ImageFont.truetype(p, size)
                    return _font_cache[key]
                except Exception:
                    pass
        _font_cache[key] = ImageFont.load_default()
    return _font_cache[key]


def _tw(d: ImageDraw.ImageDraw, s: str, f) -> float:
    """Text width helper."""
    return d.textlength(s, font=f)


def _truncate(d: ImageDraw.ImageDraw, text: str, f, max_w: float) -> str:
    """Truncate *text* to fit within *max_w* px; appends '...' (ASCII)."""
    if _tw(d, text, f) <= max_w:
        return text
    while text and _tw(d, text + "...", f) > max_w:
        text = text[:-1]
    return text + "..."


# ── Vector primitives (verbatim from v1) ────────────────────────────────────

def _draw_wifi(d: ImageDraw.ImageDraw, x: int, y: int, level: int) -> None:
    """4 signal bars filled to *level* (0..4).  x,y = bottom-left corner.
    level == 0: all bars are outlines + a diagonal cross (no-wifi marker)."""
    for i in range(4):
        # bh caps at 2+3*2=8 (was 3+3*3=12): the tallest bar used to reach
        # y-12, which sat ABOVE the canvas top and got clipped by the panel
        # wall after the 90deg rotation ("la ultima barrita choca con la
        # pared", 2026-07-02). Shorter bars keep the whole icon on-screen.
        bh = 2 + i * 2
        box = [x + i * 5, y - bh, x + i * 5 + 3, y]
        if i < level:
            d.rectangle(box, fill=BLACK)
        else:
            d.rectangle(box, outline=BLACK)
    if level == 0:
        # Diagonal cross over the bars (from idle_proof.py)
        d.line([x - 1, y + 1, x + 17, y - 12], fill=BLACK, width=2)


def _draw_envelope(d: ImageDraw.ImageDraw,
                   x1: int, y1: int, x2: int, y2: int) -> None:
    """Vector envelope icon for the QUEUE row (replaces ✉ glyph).
    Rectangle + two diagonal lines from corners to mid-top (flap)."""
    d.rectangle([x1, y1, x2, y2], outline=BLACK)
    mx = (x1 + x2) / 2
    fold_y = y1 + (y2 - y1) * 0.55
    d.line([x1, y1, mx, fold_y], fill=BLACK)
    d.line([x2, y1, mx, fold_y], fill=BLACK)


def _draw_check(d: ImageDraw.ImageDraw, x: int, y: int) -> None:
    """Small vector checkmark icon for the SENT counter. x,y = top-left-ish
    anchor."""
    d.line([x, y + 5, x + 3, y + 8], fill=BLACK, width=2)
    d.line([x + 3, y + 8, x + 8, y], fill=BLACK, width=2)


# ── Field formatting helpers ────────────────────────────────────────────────

def _ascii(s: str) -> str:
    """Strip non-ASCII so free-form core fields (SSID, hostname) can never
    break the TEXT RULE -- d.text() must stay ASCII-only outside the face."""
    return s.encode("ascii", "ignore").decode("ascii") if s else s


def _uptime_text(seconds) -> str:
    """Format uptime seconds as 'up 2d 4h' (no-placeholder: missing/invalid
    -> ''), per the core's contract (uptime = seconds since process start)."""
    if not isinstance(seconds, int) or seconds < 0:
        return ""
    days, rem = divmod(seconds, 86400)
    hours, rem = divmod(rem, 3600)
    minutes, _ = divmod(rem, 60)
    if days:
        return f"up {days}d {hours}h"
    if hours:
        return f"up {hours}h {minutes}m"
    if minutes:
        return f"up {minutes}m"
    return f"up {seconds}s"


def _time_remaining_text(minutes, charging: bool) -> str:
    """'3h20'/'22m' style (no-placeholder: missing/invalid -> ''). While
    charging this is "the estimate IF unplugged" (spec 3c), not a live
    countdown -- prefixed '~' so it never reads as one."""
    if not isinstance(minutes, int) or minutes < 0:
        return ""
    h, m = divmod(minutes, 60)
    txt = f"{h}h{m:02d}" if h else f"{m}m"
    return f"~{txt}" if charging else txt


def _voltage_text(mv) -> str:
    """'3.90V' style (no-placeholder: missing/invalid -> '')."""
    if not isinstance(mv, int) or mv <= 0:
        return ""
    return f"{mv / 1000:.2f}V"


def _agents_count(status: dict) -> int:
    """Connected-agent count: the real int from the core, falling back to
    the older agent_connected bool (1/0) for a core that hasn't upgraded."""
    n = status.get("agents")
    if isinstance(n, int):
        return max(0, n)
    return 1 if status.get("agent_connected") else 0


# ── Chrome primitives (agents indicator, battery shape, CPU/RAM bars) ──────

def _draw_agents(d: ImageDraw.ImageDraw, x: int, y: int, n: int, font) -> None:
    """Connected-agents indicator: tiny robot head + 'xN'. n==0 draws a
    hollow head with an honest 'x0' -- no-placeholder rule, never hide a
    real zero behind blank chrome."""
    d.rectangle([x, y, x + 10, y + 8], outline=BLACK)      # head
    d.line([x + 5, y, x + 5, y - 3], fill=BLACK)            # antenna
    d.ellipse([x + 4, y - 5, x + 6, y - 3], fill=BLACK)     # antenna tip
    if n > 0:
        d.ellipse([x + 2, y + 3, x + 4, y + 5], fill=BLACK)  # eye
        d.ellipse([x + 6, y + 3, x + 8, y + 5], fill=BLACK)  # eye
    d.text((x + 13, y - 1), f"x{n}", font=font, fill=BLACK)


def _draw_battery_shape(d: ImageDraw.ImageDraw, x: int, y: int, pct, charging: bool) -> None:
    """Battery SHAPE: ~20% longer than the v1 icon (L=28 vs 22), solid black
    when full. DRAINS FROM THE + TIP TOWARD THE - BASE (black
    recedes from the + nub as charge drops) -- fill is anchored at the -
    (left) end and grows toward the + (right) nub as pct rises, so a low
    charge visibly leaves the nub-side white. charging=True draws a bolt
    instead (the "pinned at 100%" inference itself lives in the power
    service -- see timeremain.py -- this just trusts the resulting bool).
    pct not an int (unknown/no battery) draws a centred "?" -- never a fake
    level (no-placeholder)."""
    L, TH = 28, 12
    d.rectangle([x, y, x + L, y + TH], outline=BLACK)
    d.rectangle([x + L, y + 3, x + L + 2, y + TH - 3], fill=BLACK)  # + nub (right)
    if charging:
        cx = x + L // 2
        d.line([cx + 3, y + 2, cx - 2, y + 6], fill=BLACK, width=2)
        d.line([cx - 2, y + 6, cx + 2, y + 6], fill=BLACK, width=2)
        d.line([cx + 2, y + 6, cx - 3, y + 10], fill=BLACK, width=2)
        return
    if not isinstance(pct, int):
        qf = _font(11, _FONTS_B)
        qw = _tw(d, "?", qf)
        d.text((x + L / 2 - qw / 2, y - 1), "?", font=qf, fill=BLACK)
        return
    inner = L - 2
    bw = round(inner * max(0, min(100, pct)) / 100)
    if bw > 0:
        d.rectangle([x + 1, y + 1, x + 1 + bw, y + TH - 1], fill=BLACK)


# ── Floating emoticons: multi-source, priority-capped, hitbox ──────────────
# Milestone F extends the v1 single-source
# rejection-sampling system: battery-low/charging > mail (queue) > pending
# ("?") > ambient sparkle, by priority, capped at 3 total ("calcular
# un maximo para no sobrecargar"). Real signals only -- idle with nothing
# real draws just the ambient sparkle, same as v1.

_LOW_BATT_PCT = 15  # icon/emoticon threshold -- matches the power service's
                    # own PIMYWA_LOW_BATT default (adapters/power/service.py)


def _glyph_bbox(kind: str, cx: int, cy: int, s: int):
    if kind == "env":
        return (cx - s, cy - int(s * 0.7), cx + s, cy + int(s * 0.7))
    if kind == "batt_low":
        # Battery box + "!" to its right -- widen the bbox to cover both.
        return (cx - s, cy - int(s * 0.6), cx + int(s * 1.9), cy + int(s * 0.6))
    return (cx - s, cy - s, cx + s, cy + s)  # q, batt_chg, spark


def _draw_glyph(d: ImageDraw.ImageDraw, kind: str, cx: int, cy: int, s: int) -> None:
    if kind == "env":
        x1, y1, x2, y2 = cx - s, cy - int(s * 0.7), cx + s, cy + int(s * 0.7)
        d.rectangle([x1, y1, x2, y2], outline=BLACK)
        mx = (x1 + x2) / 2
        fold = y1 + (y2 - y1) * 0.5
        d.line([x1, y1, mx, fold], fill=BLACK)
        d.line([x2, y1, mx, fold], fill=BLACK)
    elif kind == "q":
        f = _font(max(10, int(s * 2.4)), _FONTS_B)
        w = _tw(d, "?", f)
        d.text((cx - w / 2, cy - s - 1), "?", font=f, fill=BLACK)
    elif kind == "batt_low":
        # Small nearly-empty battery + "!" -- joins the sky only when the
        # real reading is <= _LOW_BATT_PCT (see _glyph_kinds).
        x0, y0, x1, y1 = cx - s, cy - int(s * .6), cx + s, cy + int(s * .6)
        d.rectangle([x0, y0, x1, y1], outline=BLACK)
        d.rectangle([x1, cy - 2, x1 + 1, cy + 2], fill=BLACK)        # + nub
        d.rectangle([x0 + 1, y0 + 1, x0 + 2, y1 - 1], fill=BLACK)    # sliver of charge left
        f = _font(int(s * 1.8), _FONTS_B)
        d.text((x1 + 3, cy - s), "!", font=f, fill=BLACK)
    elif kind == "batt_chg":
        # Tiny lightning bolt -- distinct silhouette from batt_low, no outline.
        d.line([cx + s * .4, cy - s, cx - s * .3, cy + s * .15], fill=BLACK, width=2)
        d.line([cx - s * .3, cy + s * .15, cx + s * .15, cy + s * .15], fill=BLACK, width=2)
        d.line([cx + s * .15, cy + s * .15, cx - s * .4, cy + s], fill=BLACK, width=2)
    else:  # spark -- ambient liveness when nothing real needs showing
        d.line([cx - s, cy, cx + s, cy], fill=BLACK, width=1)
        d.line([cx, cy - s, cx, cy + s], fill=BLACK, width=1)
        d.line([cx - s + 1, cy - s + 1, cx + s - 1, cy + s - 1], fill=BLACK, width=1)
        d.line([cx - s + 1, cy + s - 1, cx + s - 1, cy - s + 1], fill=BLACK, width=1)


def _rects_overlap(a, b) -> bool:
    return not (a[2] < b[0] or a[0] > b[2] or a[3] < b[1] or a[1] > b[3])


def _glyph_kinds(status: dict, seed: int) -> list:
    """Priority-ordered, capped-at-3 list of (kind, size) for this frame:
    battery-low/charging > mail > pending > ambient sparkle. Real signals
    only (no-placeholder rule)."""
    rnd = random.Random(seed)
    out: list = []
    if status.get("charging"):
        out.append(("batt_chg", 7))
    elif isinstance(status.get("battery"), int) and status["battery"] <= _LOW_BATT_PCT:
        out.append(("batt_low", 7))

    queue = status.get("queue")
    queue = queue if isinstance(queue, int) else 0
    n_mail = min(3 - len(out), max(0, queue))
    for _ in range(n_mail):
        kind = "env" if rnd.random() < 0.72 else "q"
        out.append((kind, rnd.choice([5, 6, 8])))

    if not out:
        out.append(("spark", rnd.choice([3, 4])))
    return out


def _place_glyphs(seed: int, kinds: list, reserved: list):
    """Place *kinds* ([(kind, size), ...], already priority-capped by
    _glyph_kinds) via rejection sampling: each candidate position is
    rejected if it overlaps (with PAD) an already-placed glyph or any
    reserved chrome zone -- HITBOX rule, nothing ever overlaps. Deterministic
    per seed (=anim_step) so the service's frame-identical skip and this
    module's self-check stay stable."""
    rnd = random.Random(seed)
    sky = (108, 36, 244, 100)   # candidate zone, right of the face
    placed: list = []
    out: list = []
    for kind, s in kinds:
        for _ in range(60):
            cx = rnd.randint(sky[0] + s, sky[2] - s)
            cy = rnd.randint(sky[1] + s, sky[3] - s)
            bb = _glyph_bbox(kind, cx, cy, s)
            hb = (bb[0] - PAD, bb[1] - PAD, bb[2] + PAD, bb[3] + PAD)
            if any(_rects_overlap(hb, r) for r in placed + reserved):
                continue
            placed.append(bb)
            out.append((kind, cx, cy, s))
            break
    return out


# ── FACE — kaomoji as TEXT ───────────────────────────────────────────────────
# Replaces the v1 vector _draw_face entirely ("asi es
# pwnagotchi, es lo que siempre te he pedido"; "temas animalito NO"). Every
# face below is drawn with d.text() using the BUNDLED DejaVu font (see
# _FONTS_R above) -- glyph coverage was verified on the real Pi with this
# exact font file (design-refs/kaotest_pi.png, gazetest_pi.png).
# _VERIFIED_GLYPHS is the closed set of non-ASCII characters actually proven
# there; the self-check asserts every face string only uses glyphs from that
# set (plain ASCII never needs proving -- guaranteed by any font).
_VERIFIED_GLYPHS = frozenset("◕‿⚆⇀↼ᵔ◡☼⌐■☓♥◐◑◓◒●")

_FACE_FONT_SIZE = 40  # 2026-07-02: 26->34 (first follow-up) -> 52
                       # ("crezca mucho mas", second follow-up) -> 40 (third:
                       # 52 read as "demaciado enorme" once deployed AND
                       # pushed cx too far right -- see _CX/_CY's comment).
                       # 40 is still a real jump from the original 26/34,
                       # just not the maximal one.

# ── Eye engine: rotatable sprite gaze ────────────────────────────────────────
# "tenemos tres tipos de ojo distintos rotando, y creo que
# deberian ser AGRUPADOS... van mirando a distintos lados en grupo." Three
# eye TYPES, each able to look in up to 8 directions (45deg steps):
#   halfmoon (50/50)  -- ◐◑◓◒ are 4 NATIVE glyphs (cardinal directions); the
#     4 diagonal in-betweens are the SAME base glyph (◐) rotated as a
#     bitmap sprite (design-refs/gazetest.py, proven tofu-free on the real
#     Pi -- gazetest_pi.png).
#   quarter (25/75)   -- the preferred type ("los mas lindos y kawaii de
#     todos... lo que hace a pwnagotchi tan exitoso"). A single glyph (◕),
#     ALL 8 directions via sprite rotation (no native alternates exist).
#   point             -- a single glyph (⚆), same rotation approach.
# RECIPROCITY (design rule): both eyes in one frame are always the SAME type +
# SAME direction -- see _draw_gaze_face, which only ever draws one
# (type, angle) pair for both eye slots, never mixed.
_HALFMOON_NATIVE = {0: "◑", 90: "◓", 180: "◐", 270: "◒"}
_HALFMOON_BASE = "◐"
_HALFMOON_BASE_ANGLE = 180


def _halfmoon_glyph_angle(angle: int):
    """Native glyph for the 4 cardinal directions; the SAME base glyph
    rotated for the 4 diagonal in-betweens (contract: "para 8, rotas el
    sprite a 45 grados")."""
    if angle in _HALFMOON_NATIVE:
        return _HALFMOON_NATIVE[angle], 0
    return _HALFMOON_BASE, (angle - _HALFMOON_BASE_ANGLE) % 360


_EYE_TYPES: dict = {
    "halfmoon": {a: _halfmoon_glyph_angle(a) for a in range(0, 360, 45)},
    "quarter":  {a: ("◕", a) for a in range(0, 360, 45)},   # the preferred type
    "point":    {a: ("⚆", a) for a in range(0, 360, 45)},
}

_sprite_cache: dict = {}


def _eye_sprite(glyph: str, font, size: int, angle: int):
    """Render *glyph* centred in a size x size 1-bit canvas and rotate it by
    *angle* degrees (proven approach: design-refs/gazetest.py -- verified
    clean/no-tofu on the real Pi, gazetest_pi.png). Cached: the (glyph,
    size, angle) space is small (3 types x 8 angles) and re-rotating on
    every frame would be wasted work on a Pi Zero."""
    key = (glyph, size, angle)
    cached = _sprite_cache.get(key)
    if cached is not None:
        return cached
    canvas = Image.new("1", (size, size), WHITE)
    d = ImageDraw.Draw(canvas)
    bbox = d.textbbox((0, 0), glyph, font=font)
    w, h = bbox[2] - bbox[0], bbox[3] - bbox[1]
    x = (size - w) / 2 - bbox[0]
    y = (size - h) / 2 - bbox[1]
    d.text((x, y), glyph, font=font, fill=BLACK)
    if angle:
        canvas = canvas.rotate(angle, expand=False, fillcolor=WHITE)
    _sprite_cache[key] = canvas
    return canvas


# The actual requirement (2026-07-02, refining the "agrupar por tipo" rule
# above): "tres direcciones random de tipo uno, blink, tres direcciones
# random de tipo dos, blink, tres direcciones random de tipo tres, blink. y
# asi va el loop." 12-frame cycle; the blink is the TRANSITION between
# groups. Quarter (◕, the preferred type) goes first, per "dale preferencia
# a los ◕".
_GAZE_SEQUENCE = (
    "quarter", "quarter", "quarter", "blink",
    "halfmoon", "halfmoon", "halfmoon", "blink",
    "point", "point", "point", "blink",
)
_BLINK_VARIANT = {"face": "(--‿--)", "speech": "*blink*"}  # "-_-" -> "--_--", keep the smile


def _gaze_variant(anim_step: int) -> dict:
    """One frame of the grouped gaze loop. "Random" but DETERMINISTIC per
    anim_step (seed=anim_step -- same trick _place_glyphs already uses for
    the floating emoticons) so frame-skip-on-identical-image and the
    self-check both stay stable, while still looking varied lap to lap."""
    slot = anim_step % len(_GAZE_SEQUENCE)
    kind = _GAZE_SEQUENCE[slot]
    if kind == "blink":
        return dict(_BLINK_VARIANT)
    angle = random.Random(anim_step).choice(tuple(_EYE_TYPES[kind].keys()))
    return {"eye_type": kind, "angle": angle, "mouth": "‿", "speech": "..."}


# mood -> list of {"face": kaomoji, "speech": default chatter}, 2-4+
# variants each so gaze/blink cycling (by anim_step, see pick_variant) keeps
# the panel "alive" -- same idea as real pwnagotchi swapping the whole face
# string. EDIT THIS TABLE to retune the map; each entry gets rendered on
# the Pi and reviewed face-by-face before it ships.
KAOMOJI_CATALOG: dict = {
    # idle is NOT a key here anymore: it's the RESTING
    # mood users actually see most, and it has its own grouped
    # gaze-loop sequence -- see _GAZE_SEQUENCE / _gaze_variant, which
    # pick_variant calls directly for mood=="idle" instead of indexing a
    # flat list here.
    "zero": [
        {"face": "(●‿●)", "speech": "inbox zero"},     # (●‿●)
        {"face": "(◕‿◕)", "speech": "all clear!"},     # (◕‿◕)
        {"face": "(^‿^)",          "speech": "nothing here"},   # (^‿^)
    ],
    "new_msg": [
        {"face": "(◕‿◕)!", "speech": "ooh! a message"},  # (◕‿◕)!
        {"face": "(●o●)!",       "speech": "incoming!"},       # (●o●)!
    ],
    # 'few' (some queue) — 7 kawaii/directional variants so the resting face
    # cycles lively instead of sticking on one plain look. 2026-07-02:
    # "esta carita no es kawaii... si hubiese una iteracion de siete caritas,
    # esta [(o_o)] es la septima, me pareceria bien" -> (o_o) kept only as the
    # 7th, the rest are the ◕/directional kawaii set (all _VERIFIED_GLYPHS).
    "few": [
        {"face": "(◕‿◕)",  "speech": "a few to handle"},
        {"face": "(◐‿◐)",  "speech": "let me see..."},   # look-left
        {"face": "(◑‿◑)",  "speech": "over here?"},      # look-right
        {"face": "(⚆‿⚆)",  "speech": "on it"},           # curious
        {"face": "(◓‿◓)",  "speech": "hmm..."},          # look-up
        {"face": "(--‿--)",         "speech": "*blink*"},         # blink (2 hyphens min)
        {"face": "(o_o)",           "speech": "a few to handle"}, # the "7th"
    ],
    "swamped": [
        {"face": "(◑_◑)~",    "speech": "swamped!"},        # (◑_◑)~
        {"face": "(◐_◑)~",    "speech": "so many..."},      # (◐_◑)~
        {"face": "(x_x)",              "speech": "help!"},
    ],
    "reading": [
        {"face": "(◐_◐)",     "speech": "reading..."},      # (◐_◐)
        {"face": "(◑_◑)",     "speech": "reading..."},      # (◑_◑)
    ],
    "switching": [
        {"face": "(◓_◓)",     "speech": "next chat..."},    # (◓_◓)
        {"face": "(◒_◒)",     "speech": "next chat..."},    # (◒_◒)
    ],
    "thinking": [
        {"face": "(-.-)",              "speech": "thinking..."},
        {"face": "(o.O)",              "speech": "hmm..."},
    ],
    "working": [
        {"face": "(--‿--)",       "speech": "on it"},           # 2 hyphens min
        {"face": "(●_●)",     "speech": "processing..."},   # (●_●)
    ],
    "responding": [
        {"face": "(●‿●)>", "speech": "typing..."},     # (●‿●)>
        {"face": "(◕‿◕)>", "speech": "replying..."},   # (◕‿◕)>
    ],
    "done": [
        {"face": "(^‿^)v",         "speech": "done!"},           # (^‿^)v
        {"face": "(◕‿◕)v", "speech": "all set!"},      # (◕‿◕)v
    ],
    "ai_online": [
        {"face": "(⌐■_■)", "speech": "brain online!"}, # (⌐■_■)
        {"face": "(☼‿☼)",  "speech": "AI ready!"},     # (☼‿☼)
    ],
    "vip": [
        {"face": "(♥‿♥)", "speech": "the owner!"},     # (♥‿♥)
        {"face": "(♥o♥)",      "speech": "VIP!"},           # (♥o♥)
        {"face": "(ᵔ◡◡ᵔ)", "speech": "excited!"}, # (ᵔ◡◡ᵔ)
    ],
    "muted": [
        {"face": "(-_-)",              "speech": "muted -- not replying"},
    ],
    "sleeping": [
        {"face": "(⇀‿↼)", "speech": "zzz..."},         # (⇀‿↼)
        {"face": "(-_-)",              "speech": "..."},
    ],
    "paused": [
        {"face": "(◒_◒)",     "speech": "paused -- check link"},  # (◒_◒)
    ],
    "alert": [
        {"face": "(☓o☓)!",    "speech": "alert!"},          # (☓o☓)!
        {"face": "(☓_☓)!",    "speech": "look!"},           # (☓_☓)!
    ],
    "error": [
        {"face": "(☓_☓)",     "speech": "disconnected"},    # (☓_☓)
        {"face": "(x_x)",              "speech": "connection lost"},
        {"face": "(☓‿☓)", "speech": "offline"},        # (☓‿☓)
    ],
    # qr is handled separately by _render_qr(); no entry needed here.
}

_NOWIFI_FACE: dict = {"face": "(⚆_⚆)", "speech": "no wifi -- searching"}  # (⚆_⚆)
_NOWIFI_MOODS = frozenset({"idle", "zero", "few"})


# ── Public: pick_variant ──────────────────────────────────────────────────────

_FALLBACK_VARIANTS = [{"face": "(◕‿◕)", "speech": "..."}]  # unknown-mood fallback only


def pick_variant(mood: str, status: dict, anim_step: int) -> dict:
    """Return a face variant dict for this mood + anim_step -- either
    {"face": kaomoji_str, "speech": str} (literal) or
    {"eye_type": str, "angle": int, "mouth": str, "speech": str} (a
    rotatable-sprite gaze frame, mood=="idle" only -- see _gaze_variant).

    Rules:
      * mood "qr"         → returns {} (special path: caller uses _render_qr).
      * wifi==0 + neutral → returns _NOWIFI_FACE (searching look).
      * mood "idle"       → _gaze_variant(anim_step) (grouped gaze loop --
                             see _GAZE_SEQUENCE).
      * else              → cycles through KAOMOJI_CATALOG[mood] by anim_step.
      * status["speech"]  → overrides variant's default speech when non-empty.
    """
    if mood == "qr":
        return {}

    wifi_level = status.get("wifi")
    if wifi_level == 0 and mood in _NOWIFI_MOODS:
        v = dict(_NOWIFI_FACE)
        v["speech"] = status.get("speech") or v["speech"]
        return v

    if mood == "idle":
        v = _gaze_variant(anim_step)
    else:
        variants = KAOMOJI_CATALOG.get(mood, _FALLBACK_VARIANTS)
        v = dict(variants[anim_step % len(variants)])   # copy — don't mutate catalog

    override = status.get("speech", "")
    if override:
        v["speech"] = override

    return v


def _draw_kaomoji_face(d: ImageDraw.ImageDraw, cx: int, cy: int, face: str, font):
    """Draw *face* (a kaomoji string) LEFT-ALIGNED to the left margin, vertically
    centred at cy. Returns the drawn bbox (x0,y0,x1,y1) so the caller reserves
    that exact zone for the floating glyphs (hitbox rule).

    Left-aligned (not centred at cx) as of 2026-07-02: the owner saw dead space
    between the left edge and the face and wanted it pushed left, leaving REAL
    room on the right for the floating emoticons. cx is kept in the signature
    for the caller but no longer sets the x — every face now starts at the same
    left edge; wider faces just extend further right into the (variable) sky."""
    bbox = d.textbbox((0, 0), face, font=font)
    h = bbox[3] - bbox[1]
    x = (M + 1) - bbox[0]           # visible left edge hugs the margin
    y = cy - h / 2 - bbox[1]
    d.text((x, y), face, font=font, fill=BLACK)
    return (x + bbox[0], y + bbox[1], x + bbox[2], y + bbox[3])


def _draw_gaze_face(d: ImageDraw.ImageDraw, img: Image.Image, cy: int,
                     eye_type: str, angle: int, mouth: str, font):
    """Compose "(<eye><mouth><eye>)" where BOTH eye slots use the SAME
    rotatable sprite (RECIPROCITY -- design rule: never mixed types/directions
    between the two eyes). Left-aligned to the margin, same convention as
    _draw_kaomoji_face. Returns the drawn bbox for hitbox reservation.

    The parens and mouth are plain d.text() glyphs; only the two eye slots
    get a bitmap paste (img.paste, hence needing the Image, not just the
    ImageDraw) -- everything else about the layout (left-align, vertical
    centring, bbox math) mirrors _draw_kaomoji_face exactly."""
    glyph, sprite_angle = _EYE_TYPES[eye_type][angle % 360]
    face_str = f"({glyph}{mouth}{glyph})"
    bbox = d.textbbox((0, 0), face_str, font=font)
    h = bbox[3] - bbox[1]
    x0 = (M + 1) - bbox[0]
    y0 = cy - h / 2 - bbox[1]

    eye_bb = d.textbbox((0, 0), glyph, font=font)
    eye_size = max(eye_bb[2] - eye_bb[0], eye_bb[3] - eye_bb[1])

    cursor = x0
    for i, ch in enumerate(face_str):
        ch_w = d.textlength(ch, font=font)
        if i in (1, 3) and sprite_angle:
            sprite = _eye_sprite(glyph, font, eye_size, sprite_angle)
            paste_x = int(round(cursor + (ch_w - eye_size) / 2))
            paste_y = int(round(y0 + bbox[1] + (h - eye_size) / 2))
            img.paste(sprite, (paste_x, paste_y))
        else:
            d.text((cursor, y0), ch, font=font, fill=BLACK)
        cursor += ch_w
    return (x0 + bbox[0], y0 + bbox[1], x0 + bbox[2], y0 + bbox[3])


def _draw_face_variant(d: ImageDraw.ImageDraw, img: Image.Image, cy: int, variant: dict, font):
    """Dispatch a pick_variant() result to the right drawer -- a structured
    gaze frame (eye_type/angle/mouth) or a literal kaomoji string."""
    if "eye_type" in variant:
        return _draw_gaze_face(d, img, cy, variant["eye_type"], variant["angle"],
                                variant.get("mouth", "‿"), font)
    return _draw_kaomoji_face(d, _CX, cy, variant.get("face", "(o__o)"), font)


def variant_repr(v: dict) -> str:
    """Literal-string equivalent of a pick_variant()-shaped dict -- works for
    both {"face": ...} entries and structured gaze entries. Public API,
    used by the self-check (glyph-safety, hitbox bbox
    measurement) AND by service.py to mirror the live face into face.json for
    the dashboard -- one source of truth either way, this function doesn't
    need to know the two variant shapes apart. v={} (mood "qr", see
    pick_variant) is the caller's responsibility to skip -- this raises on it,
    deliberately, rather than silently returning a placeholder string. The
    rendered silhouette dimensions match this string closely enough for a
    reserved-zone approximation, even though the eyes actually paint as
    rotated sprites, not this literal text."""
    if "face" in v:
        return v["face"]
    glyph, _ = _EYE_TYPES[v["eye_type"]][v["angle"] % 360]
    return f"({glyph}{v.get('mouth', '‿')}{glyph})"


# ── Status field helpers ──────────────────────────────────────────────────────

def _status_text(status: dict) -> str:
    """WhatsApp/system connection word -- online/offline/paused/muted. Back
    in the footer (2026-07-02, second follow-up) after being removed
    for reading redundant with the speech line under the face --
    now that the speech line itself is gone, there is no more redundancy,
    and it moved to the footer's RIGHT side (see render_image) rather than
    where it used to live."""
    if status.get("reconnect_paused"):
        return "paused"
    if status.get("muted"):
        return "muted"
    if status.get("wa_connected"):
        return "online"
    return "offline"


def _queue_text(status: dict, mood: str) -> str:
    """Format queue integer as display string."""
    if mood == "error":
        return "-"
    q = status.get("queue")
    if q is None:
        return "0"
    return str(int(q))


def _ip_display(status: dict) -> str:
    """Return the IP string or an ASCII placeholder when offline/no-wifi."""
    ip = status.get("ip", "")
    wifi = status.get("wifi")
    if not ip or wifi == 0:
        return "-.-.-.-"    # ASCII placeholder (no em-dashes)
    return ip


# ── QR renderer ───────────────────────────────────────────────────────────────

def _render_qr(qr_data: str) -> Image.Image:
    """Render *qr_data* as a QR code, maximized on the 250x122 canvas.

    The panel is 122x250 portrait (this landscape canvas is rotated by the
    backend), so a square QR is limited by the 122 short dimension. We use the
    minimum quiet zone (border=1) and scale the QR to fill those 122 px exactly
    with NEAREST so the modules stay crisp 1-bit (no anti-alias gray that hurts
    scanning). The leftover band carries a short "scan to link" label so it is
    not plain white margin.
    """
    import qrcode  # optional dep; only needed for mood "qr"

    qr = qrcode.QRCode(
        version=None,
        error_correction=qrcode.constants.ERROR_CORRECT_L,
        box_size=4,
        border=1,  # minimal quiet zone (1 module) — reclaims panel space
    )
    qr.add_data(qr_data)
    qr.make(fit=True)

    qr_pil = qr.make_image().get_image().convert("1")
    qr_w, qr_h = qr_pil.size

    # Scale to fill the short dimension (H) exactly — up or down, NEAREST.
    target = H
    new_size = (round(qr_w * target / qr_h), target)
    qr_pil = qr_pil.resize(new_size, Image.NEAREST)
    qr_w, qr_h = qr_pil.size

    canvas = Image.new("1", (W, H), WHITE)
    # QR flush to the right; label fills the leftover left band (becomes the
    # top/bottom band once the backend rotates the canvas to portrait).
    canvas.paste(qr_pil, (W - qr_w, 0))

    band_w = W - qr_w
    if band_w >= 40:
        from PIL import ImageDraw
        lbl = Image.new("1", (H, band_w), WHITE)  # drawn sideways, then rotated
        d = ImageDraw.Draw(lbl)
        f1 = _font(13, _FONTS_B)
        f2 = _font(10, _FONTS_R)
        d.text((6, 6), "SCAN TO LINK", font=f1, fill=BLACK)
        d.text((6, 24), "WA > link device", font=f2, fill=BLACK)
        d.text((6, 40), "pimywa", font=f2, fill=BLACK)
        # Pre-rotate 270 CW so the backend's 90 CCW panel rotation lands it upright.
        canvas.paste(lbl.rotate(270, expand=True), (0, 0))
    return canvas


# ── Main renderer ─────────────────────────────────────────────────────────────

def render_image(status: dict, anim_step: int = 0) -> Image.Image:
    """Render *status* → 250x122 1-bit PIL Image (mode "1", white background).

    Landscape orientation — backends that need portrait must rotate.
    *anim_step* selects which variant from KAOMOJI_CATALOG to draw AND seeds
    the floating-glyph placement (see module docstring); the service
    increments it on each idle animation tick so the panel "lives".

    For mood "qr" with a non-empty qr_data field the full canvas is used for
    the QR code; the card chrome is not drawn.
    """
    mood: str = status.get("mood", "idle")

    # ── QR special path ───────────────────────────────────────────────────────
    if mood == "qr" and status.get("qr_data"):
        return _render_qr(status["qr_data"])

    # ── Canvas + fonts ────────────────────────────────────────────────────────
    img = Image.new("1", (W, H), WHITE)
    d   = ImageDraw.Draw(img)

    f_brand  = _font(18, _FONTS_B)               # "Piumy" brand (bold, big -- 2026-07-02 follow-up)
    f_net    = _font(9,  _FONTS_R)               # hostname / IP / SSID
    f_sm     = _font(8,  _FONTS_R)               # small chrome (battery %)
    f_val    = _font(11, _FONTS_R)               # counter values (QUEUE/SENT)
    f_agent  = _font(9,  _FONTS_B)               # agents "xN"
    f_face   = _font(_FACE_FONT_SIZE, _FONTS_R)  # kaomoji face
    f_status = _font(12, _FONTS_B)               # footer status word ("online esta muy pequeno")

    # ── Left identity column (x=M, stacked): brand / host ────────────────────
    # IP moved to the middle column (below wifi+SSID) and the brand text
    # reverted to "Piumy" (2026-07-02 follow-up: "quiero que volvamos
    # al texto original, que era Piumy... que se vea en grande").
    d.text((M, 2), "Piumy", font=f_brand, fill=BLACK)
    # Fallback only fires when status.json has no hostname yet (early boot,
    # or the core's netinfo.Gather couldn't read os.Hostname()) -- the core
    # itself already reads the REAL system hostname (the Pi's hostname was
    # renamed to "piumy" via hostnamectl), so this fallback just needs to
    # match that naming, not duplicate it.
    hostname = _ascii(status.get("hostname") or "piumy.local")
    d.text((M, 22), hostname, font=f_net, fill=BLACK)

    # ── Middle column: wifi + SSID (top), IP below (was CPU/RAM) ─────────────
    # CPU/RAM removed from the panel entirely ("saca el uso del CPU y
    # la RAM") -- _draw_pct_bar has no other caller, deleted along with it.
    # The IP now sits in the vacated row, left-aligned to x=MID -- same x as
    # _draw_wifi below -- so it falls exactly UNDER the wifi icon (follow-up:
    # "la ip no esta justamente debajo del logo de wifi"; an
    # earlier centred version didn't line up with it).
    wifi_lvl = status.get("wifi")
    wifi_lvl = wifi_lvl if isinstance(wifi_lvl, int) else 4
    _draw_wifi(d, MID, 12, wifi_lvl)
    ssid = _ascii(status.get("ssid") or "")
    if ssid:
        d.text((MID + 20, 3), _truncate(d, ssid, f_sm, W - M - (MID + 20)), font=f_sm, fill=BLACK)
    ip_str = _truncate(d, _ip_display(status), f_net, W - M - MID)
    d.text((MID, 18), ip_str, font=f_net, fill=BLACK)

    # ── Top-right corner: battery + time-remaining + voltage ─────────────────
    BW = 30
    bxr = W - M - BW
    charging = bool(status.get("charging"))
    _draw_battery_shape(d, bxr, 3, status.get("battery"), charging)
    time_txt = _time_remaining_text(status.get("time_remaining"), charging)
    if time_txt:
        d.text((bxr - G - _tw(d, time_txt, f_sm), 4), time_txt, font=f_sm, fill=BLACK)
    volt_txt = _voltage_text(status.get("voltage"))
    if volt_txt:
        d.text((W - M - _tw(d, volt_txt, f_sm), 18), volt_txt, font=f_sm, fill=BLACK)

    # ── Face (kaomoji, left) -- no speech line under it ───────────────────────
    # 2026-07-02, second follow-up: "esa carita quiero que crezca mucho
    # mas... porque ella no va a tener el texto debajo" -- the chatter line
    # freed the whole band below the face for the face itself. pick_variant
    # still returns a "speech" value (status.json keeps carrying it for
    # other consumers, e.g. the dashboard) -- this module just stops
    # drawing it.
    variant = pick_variant(mood, status, anim_step)
    if mood == "qr" and not variant:
        variant = pick_variant("idle", status, anim_step)
    face_bbox = _draw_face_variant(d, img, _CY, variant, f_face)

    # ── Footer (bottom): agents|queue|sent LEFT, STATUS word RIGHT ──────────
    # Status word is back (2026-07-02, second follow-up) -- no longer
    # redundant now that the speech line is gone. It does NOT go back where
    # it used to live: the 3 counters keep the LEFT (x=M, gutter G between
    # groups); status is centred in whatever empty space is left on the
    # RIGHT, never overlapping them ("centrado en el espacio que
    # queda").
    fy = H - M - 8
    fx = M
    n_agents = _agents_count(status)
    _draw_agents(d, fx, fy + 1, n_agents, f_agent)
    fx += 13 + _tw(d, f"x{n_agents}", f_agent) + G
    _draw_envelope(d, fx, fy + 1, fx + 8, fy + 8)
    q_text = _queue_text(status, mood)
    d.text((fx + 11, fy - 1), q_text, font=f_val, fill=BLACK)
    fx += 11 + _tw(d, q_text, f_val) + G
    _draw_check(d, fx, fy - 1)
    # SENT: same no-placeholder rule as battery -- the check icon always
    # shows (it's chrome, not data), the number only when real.
    sent = status.get("sent")
    if isinstance(sent, int):
        d.text((fx + 11, fy - 1), str(sent), font=f_val, fill=BLACK)
        fx += 11 + _tw(d, str(sent), f_val)
    else:
        fx += 11
    fx += G  # gutter before the leftover right-side space

    stx = _status_text(status)
    stx_w = _tw(d, stx, f_status)
    right_space_center = (fx + (W - M)) / 2
    d.text((right_space_center - stx_w / 2, fy - 2), stx, font=f_status, fill=BLACK)

    # ── Floating emoticons (multi-source, hitbox / rejection sampling) ───────
    reserved = [
        (0, 0, W, 34),                                                       # top chrome band
        (face_bbox[0] - PAD, face_bbox[1] - PAD, face_bbox[2] + PAD, face_bbox[3] + PAD),  # face (no speech below anymore)
        (0, fy - 4, W, H),                                                   # footer (spans full width now: counters left, status right)
    ]
    for kind, gx, gy, gs in _place_glyphs(anim_step, _glyph_kinds(status, anim_step), reserved):
        _draw_glyph(d, kind, gx, gy, gs)

    # own_jid is deliberately NOT drawn on the panel: the phone number is
    # private and the device gets photographed/shared pwnagotchi-style, so the
    # JID must never leak on-screen (owner request 2026-07-02). It stays in
    # status.json for the dashboard; it just never reaches the e-paper.

    return img


# ── Self-check / CLI ──────────────────────────────────────────────────────────

if __name__ == "__main__":
    import sys
    out_dir = sys.argv[1] if len(sys.argv) > 1 else "."
    os.makedirs(out_dir, exist_ok=True)

    # Glyph-safety: every kaomoji face string -- literal catalog entries AND
    # every (eye_type, angle) the sprite engine can produce -- uses ONLY
    # plain ASCII or a character already proven tofu-free on the real Pi
    # with the bundled font (_VERIFIED_GLYPHS). Catches a typo'd/unverified
    # glyph before it ever reaches a render. _all_reprs is also reused below
    # for the hitbox bbox measurement, so both checks stay in sync with the
    # actual catalog + eye-engine content.
    _all_reprs = [v["face"] for variants in KAOMOJI_CATALOG.values() for v in variants]
    for _eye_type, _angles in _EYE_TYPES.items():
        for _angle in _angles:
            _all_reprs.append(variant_repr({"eye_type": _eye_type, "angle": _angle, "mouth": "‿"}))
    _all_reprs.append(_BLINK_VARIANT["face"])
    _all_reprs.append(_NOWIFI_FACE["face"])

    for _repr_str in _all_reprs:
        for ch in _repr_str:
            assert ch.isascii() or ch in _VERIFIED_GLYPHS, \
                f"{_repr_str!r} uses unverified glyph {ch!r} (U+{ord(ch):04X})"
    print(f"  glyph-safety OK -- {len(_all_reprs)} face representations, all verified/ASCII")

    moods = list(KAOMOJI_CATALOG.keys()) + ["idle", "qr"]
    for mood in moods:
        st: dict = {
            "mood": mood, "wa_connected": True, "agent_connected": True, "agents": 2,
            "queue": 3, "battery": 72, "voltage": 3900, "charging": False,
            "time_remaining": 200, "cpu": 14, "ram": 38,
            "wifi": 3, "ip": "192.168.1.99", "hostname": "piumy.local",
            "ssid": "Brossard_5G", "uptime": 187260,
        }
        if mood == "qr":
            st["qr_data"] = "https://example.com/link"
        img = render_image(st, anim_step=0)
        assert img.size == (W, H), f"Size mismatch for {mood}: {img.size}"
        assert img.mode == "1", f"Mode mismatch for {mood}: {img.mode}"
        out = os.path.join(out_dir, f"face_{mood}.png")
        img.save(out)
        print(f"  {mood:12} -> {out}")

    # Battery states (longer icon, drain direction, charging bolt,
    # unknown "?", plus the matching floating emoticon).
    for label, extra in [
        ("charging", {"battery": 100, "charging": True, "time_remaining": 40}),
        ("low", {"battery": 8, "charging": False, "time_remaining": 12}),
        ("unknown", {"battery": None, "voltage": None, "charging": False, "time_remaining": None}),
    ]:
        st = {"mood": "idle", "wa_connected": True, "queue": 1, "wifi": 3,
              "ip": "10.0.0.1", "hostname": "piumy.local", "ssid": "Home",
              "uptime": 42, "agents": 1, "cpu": 20, "ram": 30}
        st.update(extra)
        img = render_image(st, anim_step=0)
        out = os.path.join(out_dir, f"battery_{label}.png")
        img.save(out)
        print(f"  battery:{label:9} -> {out}")

    # Render one full lap of the grouped gaze loop ("tres direcciones random
    # de tipo uno, blink, ... y asi va el loop"): 3 dirs of type1 + blink +
    # 3 dirs of type2 + blink + 3 dirs of type3 + blink = 12 frames. This
    # filmstrip is used to review every frame before deploy (also varies
    # queue, to exercise the floating-glyph placement across frames).
    for step in range(len(_GAZE_SEQUENCE)):
        st = {"mood": "idle", "wa_connected": True, "queue": step % 4,
              "battery": 85, "wifi": 4, "ip": "10.0.0.1",
              "hostname": "piumy.local", "ssid": "Home", "uptime": 42,
              "agents": 1, "cpu": 10 + step, "ram": 30}
        out = os.path.join(out_dir, f"idle_step{step:02d}.png")
        render_image(st, anim_step=step).save(out)
    print(f"  idle gaze loop -> {len(_GAZE_SEQUENCE)} frames (one full lap)")
    assert len(_GAZE_SEQUENCE) == 12, \
        "3 directions + blink, x3 eye types = 12-frame loop"
    assert _GAZE_SEQUENCE.count("blink") == 3, "one blink transition per type group"
    assert _GAZE_SEQUENCE[0] == "quarter", "preferencia a los ◕ -- quarter leads the loop"
    # Reciprocity + determinism: the SAME anim_step always yields the SAME
    # (type, angle) for both eyes (never mixed), and repeats exactly on
    # re-render -- required for the service's frame-identical skip logic.
    for _step in range(len(_GAZE_SEQUENCE) * 2):
        _v1 = _gaze_variant(_step)
        _v2 = _gaze_variant(_step)
        assert _v1 == _v2, f"anim_step={_step} gaze pick is not deterministic: {_v1} vs {_v2}"

    # No-placeholder check ("no quiero ni un placeholder"):
    # every optional field missing must render fine (no crash) and simply
    # not appear, never a fabricated default.
    st_bare = {"mood": "idle", "wa_connected": True, "queue": 0,
               "ip": "10.0.0.1", "hostname": "piumy.local",
               "own_jid": "56999999999:12@s.whatsapp.net"}
    img = render_image(st_bare, anim_step=0)
    assert img.size == (W, H) and img.mode == "1", \
        "bare status (every optional field missing) failed to render"
    out = os.path.join(out_dir, "face_no_placeholder.png")
    img.save(out)
    print(f"  no-placeholder (missing optional fields, own_jid NOT shown) -> {out}")

    # HITBOX invariant, multi-source (milestone F): no two floating
    # glyphs, nor a glyph and a reserved chrome zone, ever overlap -- across
    # a spread of seeds/queue depths/battery states. The face reserved zone
    # is derived from the ACTUAL worst-case bbox across every representation
    # in _all_reprs (literal catalog entries + every eye-engine glyph, same
    # font/size render_image uses) rather than a guessed constant, so this
    # stays accurate if the catalog or eye types change. The face is
    # LEFT-ALIGNED to the margin now (not centred at _CX -- 2026-07-02:
    # "va hacia la izquierda"), so the reserved x-range starts at M and
    # extends by the widest representation's width, not +-half around _CX.
    _face_font_chk = _font(_FACE_FONT_SIZE, _FONTS_R)
    _dummy = ImageDraw.Draw(Image.new("1", (1, 1)))
    _fy0 = _fy1 = 0.0
    _fmaxw = 0.0
    for _repr_str in _all_reprs:
        _bb = _dummy.textbbox((0, 0), _repr_str, font=_face_font_chk)
        _w, _h = _bb[2] - _bb[0], _bb[3] - _bb[1]
        _fmaxw = max(_fmaxw, _w)
        _fy0 = min(_fy0, -_h / 2); _fy1 = max(_fy1, _h / 2)
    _reserved = [
        (0, 0, W, 34),
        (M - PAD, _CY + _fy0 - PAD, M + 1 + _fmaxw + PAD, _CY + _fy1 + PAD),
        (0, H - M - 12, W, H),
    ]
    _battery_states = [
        {"battery": 72, "charging": False},
        {"battery": 8, "charging": False},
        {"battery": 100, "charging": True},
        {"battery": None, "charging": False},
    ]
    for seed in range(30):
        for queue in (0, 1, 2, 3, 4, 9):
            for bstate in _battery_states:
                st = {"queue": queue, **bstate}
                placed: list = []
                for kind, gx, gy, gs in _place_glyphs(seed, _glyph_kinds(st, seed), _reserved):
                    bb = _glyph_bbox(kind, gx, gy, gs)
                    hb = (bb[0] - PAD, bb[1] - PAD, bb[2] + PAD, bb[3] + PAD)
                    assert not any(_rects_overlap(hb, r) for r in placed + _reserved), \
                        f"glyph overlap at seed={seed} queue={queue} battery={bstate}"
                    placed.append(bb)
    print("  hitbox invariant OK (no glyph overlap across seeds/queue/battery states)")

    print("All renders OK.")
