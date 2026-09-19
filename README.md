# piumy-gateway

The engine behind [Piumy](https://piumy.app): your WhatsApp, answered by your own AI agents over MCP — routed, stored, and under your rules.

Everything runs on your machine. There are no servers in between, because there are none.

## Getting it

You do not need this repository to use Piumy. **[Download the installer](https://github.com/chamilonster/Piumy/releases/latest)** — one `.exe`, one double click, Windows only for now.

This repository is the source: for reading it, auditing it, or building it yourself.

## What it is

A single Go binary that sits between WhatsApp and your agents:

- **WhatsApp** — [whatsmeow](https://github.com/tulir/whatsmeow) embedded in-process. No browser, no Node, no external client. `CGO_ENABLED=0`.
- **Agents** — an MCP server they connect to, plus a dispatch channel that pushes incoming messages to them.
- **Storage** — SQLite (pure Go), holding history, per-chat rules, memory and the outbox.
- **A dashboard** on localhost to see and steer all of it.

An agent cannot act on its own: every action must belong to a real incoming message it is currently handling. That gate lives in the code, not in a prompt.

## Building

```bash
go build ./...
go test ./...
```

The Windows installer is built from `installer/windows/` with Inno Setup.

## Layout

| Path | What |
|---|---|
| `internal/whatsmeow` | the WhatsApp client adapter |
| `internal/corepipeline` | what happens to a message from arrival to answer |
| `internal/mcpserver` | the tools agents call, and the gate that limits them |
| `internal/capipush` | dispatching messages out to agents |
| `internal/store` | SQLite: chats, messages, rules, memory, drafts |
| `internal/dashboard`, `internal/restapi` | the local dashboard and its API |
| `docs/MANUAL.md` | the map: every package and its exported surface |

Agent-facing manuals live in `internal/mcpserver/manuals/` and are embedded in the binary — an agent reads them over MCP with `get_manual`, so they cannot drift from the build.

## Contributing

Two rules carry more weight than the rest:

- **No personal data in the repository — not even test data.** No phone numbers, WhatsApp identifiers, emails or real names; no chat, contact or history dumps. When a test needs an identifier, use the `555` prefix (`555000001@s.whatsapp.net`). A pre-commit hook enforces this: enable it with `git config core.hooksPath .githooks`.
- **The hard limits belong in the code**, never in a prompt or a manual. A rule an agent can be talked out of is not a rule.

## License

GNU Affero General Public License v3.0 — see [LICENSE](LICENSE).
