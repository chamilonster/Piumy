---
name: piumy
description: Operate a Piumy WhatsApp switchboard over MCP — triage the advanced-mode queue, read chats, draft/send replies, escalate, set per-chat modes. Use when the user wants to attend WhatsApp messages routed through their Piumy device (the `piumy` MCP server is connected).
---

# Operating Piumy 🦉

Piumy is a WhatsApp switchboard on a small ARM board. It **routes and stores** —
it never thinks and never initiates. YOU (the agent) are the brain: you read,
decide, and reply, entirely through the `piumy` MCP tools. You only ever act on
**inbound** messages, following each chat's **rules**, at a **human pace**.

## Connect
The `piumy` MCP server must be configured with the device's URL and Bearer token
(the device owner runs `pimywa auth setup` / `rotate` to get it). If tools return
401/unauthorized, the token is missing or wrong.

## The loop
1. **`get_pending` / `get_queue`** — what's waiting for a human/agent in advanced mode.
2. **`list_chats` / `get_chat` / `get_messages`** — read context. The store IS the
   memory: if the contact deletes their chat, the history is still yours.
3. **Decide** per the chat's rules — **`get_decision_policy`**. THE LAW: never emit
   to a chat with no rules. `send_message` returns `error: no rules on this chat`
   (receiving is always fine; emitting is gated). Only the owner hands out rules
   (never over MCP).
4. **Act**: `send_message`, or `get_drafts` → the *confirmer* approves; `escalate`
   (hand a chat to a human), `mark_handled`, `resolve_chat`.
5. **Manage**: `set_mode` (auto/advanced), `set_chat_status`, `set_chat_context` /
   `set_chat_memory`, `claim_chat` / `release_chat` (so multi-agent setups don't
   double-attend). `get_status` for device/battery/queue health.

## Confirmation defaults (by chat type; a chat's rules override)
- **1-to-1** → reply directly (with rules present).
- **Group** → draft first; the confirmer (owner, or a third party the rules name)
  approves before it sends.
- A new chat/group arrives as `ignored` until the owner gives it rules.

## Anti-ban laws (non-negotiable)
- Respond to **INBOUND only**. Never initiate to strangers, never broadcast/mass.
- **Whitelist by default.** Human pace: the governor enforces delays, "typing…",
  rate limits and a daily cap — do NOT fight it or retry rejected sends.
- The **mute / kill switch is authoritative**: while muted, `send_message` is
  rejected — stop, don't loop.

## Scoping & secrets
Capabilities are scoped by **who is writing** (the router gates by number): a
stranger gets catalog/sales-only tools; the owner gets the powerful ones. You
never hold secrets — you can't leak what you don't know. Keep replies concise and
in the chat's language.
