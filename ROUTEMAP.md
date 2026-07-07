# Piumy — routemap 🦉

> Portable, distributed WhatsApp assistant. A tiny ARM board **routes and stores**
> (the *switchboard*); the brain (an AI agent) lives outside and connects over **MCP**.
> AGPL-3.0.

Status legend: ✅ done & deployed · 🚧 in progress · 🗓️ planned (MVP) · 🔭 post-MVP

## Core platform (Go switchboard)
- ✅ WhatsApp gateway (whatsmeow) — connect, session, receive/send
- ✅ SQLite store — messages, contacts, per-chat state (mode, memory, context, rules)
- ✅ Router — whitelist + per-number rules + mode (`auto` / `advanced`)
- ✅ Governor (anti-ban) — rate limits, human pacing, kill switch (mute), reconnect backoff
- ✅ MCP server — chats/messages/queue + tools (`list_chats`, `get_messages`, `send_message`, `escalate`, `set_mode`, …)
- ✅ `status.json` state machine (idle/thinking/responding/sleeping/muted/paused/alert/error/qr)
- ✅ Outbox retry/backoff + dead-letter; queue claim/lock; agent judgment / decision policy
- ✅ **MCP auth** — bearer token, fail-closed by default; `pimywa auth setup`/`rotate` installer
- ✅ QR delivery without the panel — `GET /api/qr.svg`
- ✅ `resume_connection` — `POST /api/reconnect` clears the paused state and restarts the gateway
- ✅ Low-latency agent notification — SSE (`GET /api/events`)
- ✅ Encrypted WhatsApp session backup (AES-256-GCM) + `pimywa restore-session`
- ✅ Media download — images, video, stickers, audio (voice notes)

## E-paper face (Python display adapter)
- ✅ Real pwnagotchi **kaomoji** faces (bundled DejaVu, tofu-free on the Pi)
- ✅ **Eye engine** — 3 eye types × rotated directions, 12-frame gaze loop
- ✅ Margin-grid layout — identity, wifi+SSID, battery, footer counters, connected agents
- ✅ Attention-envelope animation — piggybacks on any visible refresh (battery/wifi/agent/MCP), min 3s, calm idle
- ✅ Optional full-refresh (off by default — flash-free partial updates)

## Battery intelligence (Python power adapter, CW2015)
- ✅ Real cell voltage (VCELL over I2C) + chip wake-on-init
- ✅ Self-calibrating LiPo curve (learns this cell's real 100%/0% endpoints)
- ✅ Linearized level (even discharge) + adaptive time-remaining
- ✅ Robust charging detection (divergence, not fragile per-sample delta)
- ✅ Per-minute discharge **log** (voltage↔time traceable)
- 🔭 Low-battery safe shutdown (UPS-Lite)

## Dashboard (Go, embedded, LAN, login)
- ✅ Status · **Battery** view with SVG chart (raw vs linearized + charging bands) · **live kaomoji face**
- ✅ WhatsApp link/disconnect · anti-ban mute · settings · rate limits · router/whitelist · rules-by-tier · MCP anti-flood
- ✅ Dark CSS theme
- ✅ MCP tool to reset the dashboard password (fail-closed, gated by MCP auth)

## Desktop companion (Windows, `tools/desktop/`)
- ✅ **M1** — floating carita: frameless always-on-top Tkinter widget, reuses `render.py` (pixel-identical to the e-paper), sandboxed local core, live log, packaged `Piumy.exe` (PyInstaller)
- 🗓️ M2 — "Open Dashboard" button (pywebview, auto-login)
- 🗓️ M3 — Pi source (REST + SSH journald) + Local/Pi toggle

## Client / brain side
- 🗓️ **Skill** — a Claude Code skill that operates Piumy via the MCP tools (zero install)
- 🔭 WireGuard tunnel — optional hidden-port secure access

## Hardware & ops
- ✅ Raspberry Pi Zero 2 W — switchboard + e-paper, systemd services, hostname `piumy.local`
- ✅ Portable ARM64 via generic Linux interfaces (spidev / libgpiod / i2c-dev), zero hardcode
- ✅ `install.sh` (idempotent); power-loss resilience (WAL, tmpfs status, watchdog)
- 🗓️ `install.sh` "no-plugins" flags (choose adapters per board)

## MVP finish line
- 🗓️ Client skill
- 🗓️ Public GitHub launch: README + face screenshots + button/battery diagram + dashboard photo + this routemap

## Post-MVP backlog
- 🚧 Auto-reply worker — drafts pending `auto`-mode replies via a pluggable bridge (`direct-api`/DeepSeek); never auto-sends, a draft waits for approval
- 🔭 Groups as a first-class mode (chat/member sync + message storage already work; routing/whitelist today treats a group JID like any other, no group-specific behavior yet)
- 🔭 `router.json` hot-reload
- 🔭 Accelerometer (I2C) as a "picked-it-up" interaction source
- 🔭 Learned per-contact emoji icons around the face
- 🔭 WhatsApp official cloud-api gateway adapter (commercial)
- 🔭 Managed hosting / support / dual-license monetization
