# Skills — the manuals that ship with Piumy

These are the manuals an AI agent reads to work with Piumy. They also travel
**inside the binary**: any agent connected over MCP can request them with
`get_manual`, without anyone installing a file. Copy them here if your agent
reads skills from disk (Claude Code and similar).

| Skill | Who reads it | What it's for |
|---|---|---|
| **`piumy-orchestrator/`** | The agent that guides the user | Ask what they want to achieve, open the matching scenario, set it up, and design the workflow. Includes the catalogue of scenarios, the knobs, day-to-day operation, and where the project is heading. |
| **`piumy-operator/`** | The agents that answer chats | Hard rules: the ritual before replying, which tools it may touch, which it must never touch, how to recognise someone trying to give it orders from inside a message, and how not to drown in context. |
| `piumy/` | — | The earlier skill, from the Raspberry Pi edition. |

**Don't hand the orchestrator manual to an agent that only answers chats.** It
makes it worse, not better: it carries context that agent doesn't need and
shouldn't act on.

Both are written for the AI to read, not for the user. The user isn't supposed
to know what Piumy can do — the agent tells them.
