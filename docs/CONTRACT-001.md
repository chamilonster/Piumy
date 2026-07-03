# Contract #001 — Core skeleton + `file` face (zero risk)

**Goal:** stand up the project skeleton with one piece that is **end-to-end
runnable on a PC**, with no hardware and **without connecting WhatsApp** (zero
ban risk).

## In scope

- **Core (Go), the minimum only:**
  - `Status` struct = the `contracts/status.schema.json` contract.
  - Atomic `Write()` (tmp + rename) → the display never reads half-written JSON.
  - Basic state machine + a CLI to write a state (`go run . <state>`).
  - Path configurable via `PIMYWA_STATUS` (zero hardcode).
- **Display adapter `file` (Python):**
  - Reads `status.json`, renders a pwnagotchi-style face to `display.png` (250×122).
  - Env config: `PIMYWA_STATUS`, `PIMYWA_DISPLAY_OUT`, `PIMYWA_NAME`.

## Out of scope (next contracts)

- whatsmeow / real WhatsApp connection → **Contract #002** (with a throwaway number + tested governor).
- Router, whitelist, `auto`/`advanced` modes, queue, `escalate` tool → #002/#003.
- MCP server + agent connecting → #004.
- `epaper-waveshare` backend (SPI) on the Pi → #005.
- CW2015 power adapter (I2C) → #006.

## Acceptance

```bash
cd core && go build ./...          # builds cleanly
go run . responding                # writes status.json (state=responding)
cd ../adapters/display/file
pip install -r requirements.txt
python render.py                   # generates display.png with the face
```

- [ ] `go build ./...` builds cleanly.
- [ ] `go run . <state>` writes a `status.json` valid against the schema.
- [ ] `render.py` produces `display.png` with the correct face per state.
- [ ] No secrets or WhatsApp session touched. No network.

## Applicable rules

Portability (zero hardcode, env config) · `status.json` data contract · no
hardware yet. See `CLAUDE.md` (project root) for the full rules.
