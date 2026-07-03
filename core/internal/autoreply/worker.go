// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
//
// Package autoreply: the auto-reply worker (gap #1). Periodically drafts
// replies for pending chats in "auto" mode via a bridge.Bridge, subject to
// the governing rule (0800/1959): the AI NEVER acts on a chat with no EFFECTIVE
// rules (particular → by-type → global default → nothing, see
// store.EffectiveRules) — recipient gets nothing, not even a held draft.
// Whether a reply sends straight away or holds for confirmation is by chat
// type (0810): a WhatsApp group's baseline is to confirm, a 1-1 chat's is
// not to — the bridge's own read of that chat's rules can flip away from
// the baseline in either direction. This package only orchestrates store +
// bridge — it never touches whatsmeow/the gateway directly.
package autoreply

import (
	"context"
	_ "embed"
	"log"
	"os"
	"strings"
	"time"

	"pimywa/internal/bridge"
	"pimywa/internal/store"
)

//go:embed decision-policy.md
var defaultPolicy string

// PolicyText returns the current decision policy: the external file at path
// if it exists (same live-editable file mcpserver reads, decided
// 2026-07-01), else this package's own embedded default. Read fresh on
// every call — no caching, so an edit applies immediately.
func PolicyText(path string) string {
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			return string(data)
		}
	}
	return defaultPolicy
}

// Worker periodically sweeps pending auto-mode chats, asks a bridge.Bridge
// whether/what to draft, and stores results as pending drafts.
type Worker struct {
	Store  *store.Store
	Bridge bridge.Bridge
	// Policy returns the current decision-policy text — a func, not a fixed
	// string, so each sweep sees a live edit (matches PolicyText's design).
	Policy func() string
	// ModelName is recorded on drafts.model — whichever model Bridge
	// actually is (e.g. "deepseek-chat"); irrelevant when Bridge is
	// bridge.NoneBridge, since that never returns shouldReply=true.
	ModelName string
	// Interval is how often to sweep. Default 5 minutes.
	Interval time.Duration
	// Delay paces successive Bridge.Draft calls within a sweep — not an
	// anti-ban WhatsApp concern (this never touches WhatsApp), just courtesy
	// pacing against a paid third-party API. Default 3 seconds.
	Delay time.Duration
}

func (w *Worker) interval() time.Duration {
	if w.Interval <= 0 {
		return 5 * time.Minute
	}
	return w.Interval
}

func (w *Worker) delay() time.Duration {
	if w.Delay <= 0 {
		return 3 * time.Second
	}
	return w.Delay
}

// Run sweeps periodically until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sweep(ctx)
		}
	}
}

func (w *Worker) sweep(ctx context.Context) {
	pending, err := w.Store.PendingChats(50, time.Now().Unix())
	if err != nil {
		log.Printf("autoreply: pending chats: %v", err)
		return
	}
	policy := w.Policy()
	for _, p := range pending {
		if ctx.Err() != nil {
			return
		}
		if !eligible(p) {
			continue
		}
		w.draftFor(ctx, p, policy)

		select {
		case <-ctx.Done():
			return
		case <-time.After(w.delay()):
		}
	}
}

// eligible is the gate: auto-mode, explicitly active (anti-ban: silent until
// marked active), non-blacklisted. WhatsApp groups are chats too (0800) but
// start out status "ignored" until the owner activates one — that extra
// condition only applies to groups. Rules eligibility (0800/1959: the AI
// never acts without EFFECTIVE rules — particular → type → default) is
// checked in draftFor via EffectiveRules, NOT here (1959, changes 0800):
// PendingChats' query can't cheaply resolve the type/default tiers — they
// live in KV, not a joinable chats column — so eligible() only filters what
// it can see cheaply from the query it already ran.
func eligible(p store.Pending) bool {
	if p.Mode != "auto" || !p.Active || p.Status == "blacklist" {
		return false
	}
	return !isGroupJID(p.JID) || p.Status != "ignored"
}

func isGroupJID(jid string) bool {
	return strings.HasSuffix(jid, "@g.us")
}

// draftFor asks the bridge to draft a reply for p. The rules law (see
// eligible's doc) is enforced HERE, first thing: no effective rules, no
// draft, no bridge call at all.
func (w *Worker) draftFor(ctx context.Context, p store.Pending, policy string) {
	rules, err := w.Store.EffectiveRules(p.JID)
	if err != nil {
		log.Printf("autoreply: effective rules %s: %v", p.JID, err)
		return
	}
	if rules == "" {
		return
	}

	msgs, err := w.Store.GetMessages(p.JID, 20)
	if err != nil {
		log.Printf("autoreply: get messages %s: %v", p.JID, err)
		return
	}
	// Chat's persisted memory/context (0647) plus its confirmation baseline
	// by type (0810) go into the bridge's system prompt, alongside the
	// EFFECTIVE rules resolved above. A lookup miss fails SAFE
	// (DefaultConfirm=true — hold until we know better).
	info := bridge.ChatInfo{Rules: rules, DefaultConfirm: true}
	var chatConfirmer string
	if c, ok, err := w.Store.GetChat(p.JID); err != nil {
		log.Printf("autoreply: get chat %s: %v", p.JID, err)
	} else if ok {
		info.Memory = c.Memory
		info.Context = c.Context
		info.DefaultConfirm = c.ConfirmationMode == "required"
		chatConfirmer = c.Confirmer
	}
	decision, err := w.Bridge.Draft(ctx, msgs, policy, info)
	if err != nil {
		if err != bridge.ErrBudgetExhausted {
			log.Printf("autoreply: draft %s: %v", p.JID, err)
		}
		return
	}
	if !decision.ShouldReply || decision.Draft == "" {
		return
	}

	// The bridge's NeedsConfirmation is already the final verdict — it
	// factored in the chat's baseline and its rules, nothing left to combine
	// here (0810).
	if decision.NeedsConfirmation {
		confirmer := decision.Confirmer
		if confirmer == "" {
			confirmer = chatConfirmer
		}
		if err := w.Store.AddDraftWithConfirmer(p.JID, decision.Draft, w.ModelName, confirmer, time.Now().Unix()); err != nil {
			log.Printf("autoreply: add draft %s: %v", p.JID, err)
		}
		return
	}

	if err := w.Store.EnqueueWithModel(p.JID, decision.Draft, time.Now().Unix(), w.ModelName); err != nil {
		log.Printf("autoreply: enqueue %s: %v", p.JID, err)
	}
}
