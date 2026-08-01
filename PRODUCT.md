# Product

Piumy — your WhatsApp, answered by your own AI agents.

## Platform

web (marketing site at piumy.app, served from `web/` via GitHub Pages) — the
product itself is a Windows desktop application.

## Stack

Static HTML/CSS/JS, no framework, no build step. Single file per page
(`web/index.html`, `web/download/index.html`). Self-contained: no CDN, no
external fonts, no trackers.

## Users

People who already work with AI agents — CleverCoder users first — and who
handle real conversations on WhatsApp: their own contacts, their team, their
customers. Technically comfortable enough to install a program, not
technically interested enough to edit config files or invent security keys.

Second audience: makers who found the earlier Raspberry Pi edition and want to
know what happened to it.

## Product Purpose

Hand your WhatsApp to the AI agents you already use, without handing it to
anyone else. Piumy receives, stores and routes; the agent answers. You decide,
conversation by conversation, how much freedom it has — from answering on its
own to nothing leaving without your approval.

## Positioning

Not a chatbot service. Not a SaaS with an account. It is a plugin for
[clevercoder.app](https://clevercoder.app) that runs entirely on the user's own
machine — no servers in the middle, because there are none.

Closest reference point people arrive with: "a WhatsApp bot". The distinction
that matters: a bot is a script you configure; Piumy is a switchboard your own
agent operates, under written rules, with a supervision dial.

## Operating Context

Read on a laptop, in a browser, by someone deciding whether to install a
program that will read all their WhatsApp. Trust is the conversion problem, not
features. Many visitors arrive from CleverCoder already trusting the author;
others arrive cold from GitHub or Reddit.

Bilingual audience: Spanish and English, roughly even. Language must resolve
itself from the browser — asking is friction.

## Capabilities and Constraints

- Windows: available now, one installer, version 0.1.1.
- macOS and Linux: engine already builds for both (pure Go, `CGO_ENABLED=0`);
  packaging is what's missing.
- Raspberry Pi port: announced, not shipped. Must read as *coming*, never as
  *available*.
- Free, AGPL-3.0, donation-funded (Lemon Squeezy checkout, pay what you want).
- **Honest risk that must appear before install:** Piumy connects to WhatsApp
  through an unofficial path. WhatsApp can limit or permanently ban the linked
  account. This cannot be softened or buried.

## Brand Commitments

- **Simple, clear, minimal.** (Boss, verbatim: "la marca es simple y claro,
  minimalista".)
- **The existing warmth stays.** (Boss: "se mantiene".) First person, plain
  speech, the live kaomoji face, the pwnagotchi homage. Not corporate.
- Terminal/phosphor visual world: dark, green accent, monospace for anything
  that is literally code or state.
- No account, no tracking, no cloud — stated as fact, not as a selling promise.

## Evidence on Hand

- Live product installed and running; installer verified end to end.
- The orchestrator manual (`skill/piumy-orchestrator/`) — the same catalogue of
  scenarios the site should teach: close circle, team, requests and tasks,
  marketing, sales, delicate conversations, someone else approves, many write
  one filters.
- Existing site (`web/index.html`) — incumbent visual world, to be preserved in
  spirit and drastically simplified in substance.

## Product Principles

- **General first, particular second — and the AI does the configuring.**
  (Boss, verbatim: "describe lo general y lo particular la ia lo configura".)
  The page explains what Piumy is, then what it can do case by case, and makes
  one thing unmistakable: the user never fills in settings. The agent asks what
  they want to achieve and sets it up. That is the product, not a convenience.
- Say what it is before saying why it's good.
- Never promise a platform that isn't shipped.
- The risk goes above the download button, not in a footnote.
- Nothing on the page should need a second reading.

## Accessibility & Inclusion

Readable at 100% zoom without squinting; contrast that survives the dark
palette; keyboard-reachable controls; text that reflows on a phone. Language
switch must be manual-overridable, never locked to browser locale.

---

## Assumptions (inferred, not interviewed)

The boss said "dale continua" instead of answering the full interview, then
confirmed two points directly ("se mantiene" / "simple y claro, minimalista").
Everything else here is inferred from the product, the shipped manuals and the
existing site. Flagged so it can be corrected:

- **Mode: Persuade.** Assumed — it is a landing page whose job is a download.
- **Anti-reference: generic SaaS landing.** Assumed from "sacale todo el slop"
  and "minimalista", not stated.
- **Audience split Spanish/English.** Inferred from a Chilean author with an
  English-language site and an international repo.
