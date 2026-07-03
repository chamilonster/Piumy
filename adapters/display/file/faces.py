# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Camilo Brossard
"""pwnagotchi-style faces for Piumy.

Homage to pwnagotchi (https://github.com/evilsocket/pwnagotchi) by @evilsocket.
Expressions inspired by its e-ink faces — original code.
"""

FACES = {
    "idle":       "(◕‿‿◕)",
    "thinking":   "(⇀_↼)",
    "responding": "(•‿‿•)",
    "sleeping":   "(⇀‿‿↼)",
    "working":    "(☼‿‿☼)",
    "alert":      "(°▃▃°)",
    "error":      "(☓‿‿☓)",
    "qr":         "(⌐■_■)",
}
DEFAULT = "(-__-)"


def face_for(state: str) -> str:
    """Return the face for a state; DEFAULT if the state is unknown."""
    return FACES.get(state, DEFAULT)


if __name__ == "__main__":
    # minimal self-check
    assert face_for("idle") == FACES["idle"]
    assert face_for("nope") == DEFAULT
    for s, f in FACES.items():
        print(f"{s:11} {f}")
