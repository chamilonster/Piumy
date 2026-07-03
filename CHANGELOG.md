# Changelog

Pimywa doesn't follow semver yet (pre-1.0). Entries are most recent first.

## Post-deployment fix — CW2015 VCELL=0 no longer reports a fake 0% (2026-07-02)

Found while verifying on the real Pi: the CW2015
read `VCELL=0x0000` with intermittent I2C I/O errors (battery disconnected/
dead, or a bad read -- the chip is powered from VBAT on a UPS-Lite, so 0V
means no signal, not "empty"). `battery.json` ended up with `{"battery": 0}`,
which the panel correctly-but-wrongly drew as a real, if extreme, 0% --
exactly the placeholder this project avoids, since a LiPo never actually
reads near 0V (even a depleted, protection-tripped cell sits ~3.0-3.3V).

Fix (`adapters/power/cw2015.py`): `_CW2015Sensor.read_soc()` now treats any
VCELL reading at or below `_VCELL_MIN_PLAUSIBLE_MV` (2500mV) as an invalid
read and returns `None` (unknown) instead of computing a percent from it. A
genuinely near-empty cell (~3.0-3.3V) stays well above that floor and still
gets its correct low-but-nonzero (or a real, curve-clamped 0%) reading --
only implausible voltages (disconnected battery, garbage I2C data) get
swept into "unknown". 5 new tests (21 total, up from 16): VCELL=0 and
below-floor both -> `None`; a value between the plausible floor and the
curve's own 0%-anchor is a real (not `None`) zero; a value above the
curve's anchor is a real nonzero low percent; the fix is verified through
`CW2015Backend.read_soc()` end-to-end, not just the low-level sensor.

## E-paper layout redesign: proportional grid + state-driven side frames

Approved via a rendered mockup (same process as the tuned-vector faces) before
touching `render.py` -- see the contract's mockup-first requirement
("quirurgico y con pinzas... medi antes de mover").

- **Proportional grid, calculated (not eyeballed):** replaced the earlier leftover
  margins (the 2 rectangular frames were removed there but nothing else moved)
  with an explicit grid -- `_FRAME_H`/`_FRAME_MARGIN`/`_CONTENT_TOP`/
  `_CONTENT_BOTTOM`/`_CONTENT_L`/`_CONTENT_R`/`_DIVIDER_X`/`_RIGHT_X`, each
  documented with the reasoning behind it.
- **State-driven vertical side frames (new):** landscape TOP/BOTTOM strips
  become the physical PORTRAIT panel's LEFT/RIGHT vertical frames after the
  backend's 90deg CCW rotation (verified both by rotation math and
  empirically). TOP/portrait-LEFT = agent activity (tick density: calm/
  light/busy, or a distinct zigzag for system-tier moods like error/paused/
  muted). BOTTOM/portrait-RIGHT = battery (proportional segmented fill, a
  hollow/outline texture if unknown -- same no-placeholder rule as
  everywhere else). Both reuse `mood`/`queue`/`battery` the core already
  sends -- zero new status.json fields.
- **Battery icon removed** from the top-right entirely
  ("mueve la bateria") -- the bottom side frame is now the only battery
  indicator; a second icon would have just duplicated it. Freed space went
  to the wifi/IP block.
- **QUEUE + SENT grouped** on one row in the right column
  ("deberian estar juntos") -- were previously split across the column
  (QUEUE) and the now-removed footer (SENT). New `_draw_check` icon for
  SENT, matching QUEUE's envelope icon language.
- **Identity (brand + hostname) and connectivity (wifi + IP) grouped** into
  top-left/top-right blocks respectively -- were previously scattered across
  3 separate corners with no coherent order.
- A 3rd line is intentionally RESERVED (not drawn) in the connectivity block
  for a future WiFi network name (SSID) -- a follow-up request that's
  new content (needs a status.json field + core-side SSID read), explicitly
  out of this layout-only contract's scope, but the grid already has room
  for it with zero future reshuffling.
- Face `cx`/`cy` recalculated for the new grid; `_draw_face`'s internal
  geometry (eyes/mouth/live engine) untouched, per the contract's explicit
  "no toques la cara" constraint.
- Old `_draw_battery` (the icon primitive) removed as dead code -- nothing
  calls it anymore.
- Verified via the render self-check (17 faces + 20 idle variants + the
  no-placeholder case) plus manual zoom inspection of several states
  (idle/swamped/error/no-placeholder) for pixel-level overlap checks.

## E-paper: tuned-vector faces, event moods, SENT counter, muted, real battery

### D1 — Display: tuned-vector faces (2026-07-01, `54879f6`)
- Restyled `_draw_face`'s eye/mouth primitives for pwnagotchi tenderness while
  keeping the existing "alive" engine (movable pupils, blink, per-mood variant
  cycling): a white-socket + big movable pupil + catchlight eye, and a
  double-bump smile -- both drawn as vectors, never font glyphs. Added `wink`,
  `cross` (derp), and `peek` (half-lid) eye variants for the idle pool.
- Removed the double border frame. Brackets `( )`
  left untouched.
- New moods `reading`, `switching`, `done`, `muted`; `ai_online`/`vip` gained a
  `sparkle` accessory; `alert` now uses two bang marks (`bang2`) to read
  distinctly from `new_msg`'s single one. Idle pool grew from 8 to ~20 named
  personality variants.
- Footer centre now shows `SENT n` (was uptime, now removed as dead code);
  `own_jid` (present in the schema but never drawn before) shows in the
  previously-empty space under QUEUE.
- Battery honesty: the icon is only drawn when `battery` is a real int -- the
  old `100` fallback (a lie about charge level) is gone. Same rule applied to
  the new `sent` field.

### D2 — Core: muted, event-driven moods, mood precedence, SENT counter (2026-07-01/02)
- `state.Status` gained `Muted bool` and `Sent int`. The anti-ban kill switch
  (previously governor-only, invisible to the display) now mirrors into
  `Muted` via `Manager.SetMuted`, which also forces/reverts the `muted` mood.
  `send_message` rejects outright (never enqueues) while muted. Dashboard UI
  relabeled "Kill Switch" -> "Mute" ("Kill viene de matar... mudo esta
  bien").
- Mood precedence made explicit (`state.moodTier`): system (error/qr/sleeping/
  muted/paused) > queue (swamped/few/zero) > everything else (transient
  events + idle). `React`/`SetResting` are now guarded -- a transient event or
  a queue refresh can never mask a system mood. Deliberately NOT gated by the
  queue tier: an agent almost always has a nonzero queue when it calls the
  tools that trigger these events, so blocking on it would silence the whole
  feature -- "queue > events" describes what an event reverts TO, not
  something that blocks it.
- New system mood `paused` replaces the old, misleading `alert` on the
  reconnect-pause path (`gateway.go`): `reconnect_paused` is a ban-risk signal
  (the governor backing off 12-24h after repeated failures), not peaceful
  sleep -- collapsing it into `sleeping` would have hidden exactly what the
  anti-ban rules want visible. `alert` itself stays a transient (tier-events)
  mood for a genuine "unexpected" notification.
- Event-driven moods wired to MCP tool calls (~4s decay): `get_messages` ->
  `reading` (was `working`), `list_chats`/`get_chat`/`resolve_chat` ->
  `switching` (new), `mark_handled` -> `done` (new). `send_message` ->
  `responding`, `escalate` -> `thinking`, agent connect -> `ai_online`, and
  inbound arrival -> `new_msg` already existed and were left as-is (ttl
  standardized to 4s). `get_queue` intentionally left on the generic
  `working` mood -- not named in the event table, no new mapping
  invented for it.
- SENT counter: derived from `store.CountOutboundSince(0)` (never a
  separately incremented counter -- that would drift on a retry/crash
  mismatch), populated once at boot and again after each confirmed send.
- `contracts/status.schema.json` updated: `mood` enum gained
  reading/switching/done/muted/paused; new `muted`/`sent` fields; `battery`'s
  description updated for the single-writer architecture below.

### D3 — Power adapter: CW2015 over generic i2c-dev, single-writer fix (2026-07-01/02)
- New `adapters/power/` package (`backend.py`, `cw2015.py`, `service.py`),
  mirroring `adapters/display/epaper/backend.py`'s pattern exactly: hardware
  import (`smbus2` -- a generic i2c-dev binding, not a CW2015 vendor SDK)
  guarded inside `__init__`, degrading to a no-op (never a crash-loop) if the
  library or hardware is absent. Backend selectable via `PIMYWA_POWER_BACKEND`
  (`cw2015-i2c` | `none`).
- Architecture correction during review: the service no longer writes
  `status.json` directly (an earlier version did a read-modify-write, which
  made it a second, racing writer that could clobber a core-set mood like
  `paused`/`error`). It now writes ONLY its own sidecar,
  `battery.json` (`{"battery": N, "ts": <unix>}`, atomic tmp+rename); the
  core -- the sole `status.json` writer -- reads it back in on a short
  heartbeat (`PIMYWA_STATUS_HEARTBEAT`, default 15s) via
  `state.ReadBatteryFile`, which also treats a missing/stale reading
  (`PIMYWA_BATTERY_MAX_AGE`, default 120s) as "no battery" rather than a
  placeholder. The heartbeat doubles as keeping `updated_at` fresh during
  quiet periods (cheap: tmpfs, zero SD wear).
- Battery percent is computed from the raw cell voltage (`REG_VCELL`) through
  an explicit 1S-LiPo discharge curve, NOT read directly from the chip's own
  `REG_SOC` estimate (2026-07-02: cheap/uncalibrated
  gauge chips plus a genuinely nonlinear discharge curve -- steep near full
  charge, e.g. 4.2V sags to ~4.0V for only a small fraction of used capacity,
  flat through the middle, steep again near empty -- make the on-chip SOC
  unreliable on a generic "UPS-Lite" board). `_LIPO_CURVE`'s exact millivolt
  anchors are from commonly-published generic-cell charts, not this specific
  pack's datasheet; same "needs real-hardware calibration" caveat as the
  register scaling below.
- Low-battery safe shutdown is explicitly out of scope (separate future
  contract, per the parent 0220's own note).
- `deploy/install.sh`: installs `smbus2` (apt with pip fallback, same pattern
  as `qrcode`), new `--no-power` flag (default: install, mirrors
  `--no-display`), copies `adapters/power/` to `$INSTALL_DIR/power`, new env
  defaults (`PIMYWA_POWER_*`, `PIMYWA_BATTERY_FILE`, `PIMYWA_BATTERY_MAX_AGE`,
  `PIMYWA_STATUS_HEARTBEAT`), and the new `pimywa-power.service` unit
  (mirrors `pimywa-display.service`).
- Unit tests (Python stdlib `unittest`, no new test framework): voltage
  register decode, the LiPo curve mapping itself (exact table points,
  interpolation, clamping, and a direct check that the near-full knee is
  steeper than the mid-curve plateau -- pins the actual complaint,
  not just implementation details), bus-error propagation, `smbus2`-absent
  degradation (exercised for real -- this dev machine has no `smbus2`
  installed), backend factory fallback, and the atomic `battery.json` write.
  `internal/state`
  gained a focused Go test suite covering the precedence guard, the
  queue-does-not-block-events guarantee, `paused` surviving a stale revert,
  `SetMuted`'s contract, and `ReadBatteryFile`'s honesty rule.

## Platform closure (backup, anti-flood, rules UI, queue claim/lock, SSE notifications)

### D — Low-latency agent notification via SSE (2026-07-01)
- New `internal/eventbus` package: a tiny, standalone in-process pub/sub. `Publish`
  never blocks (per-subscriber buffered channel + non-blocking send — a full/stuck
  subscriber drops its own event, nobody else is affected, and the publisher — the
  WhatsApp message-receiving path — never waits). Nil-safe throughout.
- `gateway.onMessage` publishes a `{type:"message", jid, ts}` nudge after every
  successfully stored inbound message, regardless of chat mode — no message content
  on the wire; the agent still calls `get_pending`/`get_chat`/`get_messages` (all
  gated exactly as before) to see what actually changed. Wired via
  `Controller.SetBus` (no constructor signature change, same pattern as entregable
  A's `SetPostLinkHook`).
- New `GET /api/events` (SSE): same privileged auth as every other REST endpoint,
  20s heartbeat against idle proxy/LB timeouts, and a deferred unsubscribe on every
  exit path so a dropped connection can't leak a channel/goroutine on a long-running
  service.
- Deliberately no replay/`Last-Event-ID`/delivery guarantee: SSE here is a latency
  optimization over polling, not a new source of truth — `get_pending`/`get_queue`
  remain authoritative, and a missed event is caught on the agent's next poll.

### C — Queue claim/lock (2026-07-01, `55188a0`)
- Added a transient, TTL-based per-chat lock (`chats.claimed_by`/`claimed_until`) so
  multiple connected MCP agents/models don't both work the same chat — "avoid
  double-attention". Identified by `model` (the same identity `send_message`
  already requires), not the MCP session ID (which dies on reconnect).
- New MCP tools `claim_chat`/`release_chat`; `send_message` refuses only when a
  *different* model holds an unexpired claim — a solo agent that never calls
  `claim_chat` is completely unaffected.
- `get_chat`/`get_pending` surface the claim state (already expiry-resolved —
  callers never compare timestamps). `get_queue` and the pre-existing
  `agent_exclusive:<id>` triage label were left untouched on purpose (different,
  unrelated mechanism — see `validChatStatus`'s doc comment).
- `PIMYWA_CLAIM_TTL_DEFAULT` (default 5m), hard ceiling 30m (Go const, not
  configurable — no dashboard/KV knob yet, YAGNI for a single agent).

### B — Dashboard rules-by-tier editor (2026-07-01, `b3561e8`)
- Dashboard section to view/edit the individual/group/global-default rules tiers
  introduced in `5b272a2` (1959). No new Go code — the REST endpoints already
  existed (`GET /api/rules`, `POST /api/rules/type`, `POST /api/rules/default`).

### E — MCP anti-flood limiter (2026-07-01, `c5e950f`)
- New `internal/mcpguard` package: per-client (MCP session ID) rate limiting on
  every MCP tool call, distinct from `internal/governor` (which paces
  WhatsApp-outbound sends, not MCP-inbound calls). Two tiers — a general
  call-rate cap and a stricter one for `send_message`/`escalate` — plus a
  circuit breaker that temporarily blocks a client after repeated throttling.
- Wired via mcp-go's native `ToolHandlerMiddleware`, applied uniformly to every
  registered tool regardless of registration order.
- `PIMYWA_MCPGUARD_RATE_PER_MIN`/`_EMIT_RATE_PER_MIN`/`_BLOCK_THRESHOLD`/
  `_BLOCK_COOLDOWN`, KV-override + `GET/POST /api/mcp-guard` + dashboard section.

### A — Encrypted session backup (2026-07-01, `e0b006a`)
- New `internal/sessionbackup` package: hot, WAL-safe snapshots of the WhatsApp
  session DB (`VACUUM INTO` via a separate `mode=ro` connection) encrypted with
  AES-256-GCM (scrypt-derived key from `PIMYWA_BACKUP_KEY`), rotated, and
  restorable via a CLI-only `pimywa restore-session` subcommand (never exposed
  over REST — no "replace the whole session" button on the LAN).
- Restore refuses while the service is confirmed running (PID lockfile,
  `--force` to override) — guards against a live gateway and a CLI restore
  racing each other on the same session file.
- `PIMYWA_BACKUP_KEY` (empty = disabled, session never written unencrypted),
  `PIMYWA_BACKUP_DIR`, `PIMYWA_BACKUP_KEEP`, `PIMYWA_BACKUP_INTERVAL`.
