// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
//
// Package restapi: a small REST facade over the switchboard, served by the same
// binary as the MCP server. It exists for quick access (curl, scripts, the
// dashboard) without setting up an MCP client. Same store, same state.
//
// Auth: if APIKey is set, every /api/* request must send it via the X-API-Key
// header or the ?key= query param. Empty APIKey = open (dev only). Bind to LAN.
package restapi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"pimywa/internal/eventbus"
	"pimywa/internal/governor"
	"pimywa/internal/mcpguard"
	"pimywa/internal/router"
	"pimywa/internal/sessionbackup"
	"pimywa/internal/state"
	"pimywa/internal/store"
)

// GatewayController is the runtime-control interface for the WhatsApp gateway.
// Implemented by *gateway.Controller; defined here to avoid an import cycle.
type GatewayController interface {
	Start() error
	Stop()
	Running() bool
	// Resume clears the reconnect-paused state and restarts the connection loop.
	// Equivalent to Start() for a controller that stopped after MaxFails errors.
	Resume() error
}

type Deps struct {
	Store     *store.Store
	State     *state.Manager
	Gov       *governor.Limiter  // may be nil (killswitch no-ops when nil)
	GWCtrl    GatewayController  // may be nil (gateway endpoints 503 when nil)
	RouterMgr *router.Manager    // may be nil (router endpoints 503 when nil)
	APIKey    string

	// Settings defaults/floors/ceilings (0753) for GET/POST /api/settings —
	// all populated from cfg at wiring time in main.go. Delay windows can
	// only be LOOSENED, never tightened below these mins (anti-ban floor);
	// rate limits can only be TIGHTENED, never loosened past these
	// ceilings (spam/ban risk) — mirror-image guardrails, deliberately.
	DispatchDelayMinDefault, DispatchDelayMaxDefault time.Duration
	ReadDelayMinDefault, ReadDelayMaxDefault         time.Duration
	ActionDelayMinDefault, ActionDelayMaxDefault     time.Duration

	MediaMaxMBDefault int // = cfg.MediaMaxMB (512) — GET fallback when unset
	MediaMaxMBFloor   int // sane minimum so the dashboard can never disable GC outright (3rd commandment)

	RateLimitPerMinDefault, RateLimitPerMinCeiling int
	RateLimitPerDayDefault, RateLimitPerDayCeiling int

	// Backup is the session backup engine — may be nil (backup endpoints
	// 503 when nil). Restore is deliberately NOT exposed here at all: it's
	// a CLI-only operation (pimywa restore-session), never reachable over
	// the LAN — see main.go's runRestoreSession.
	Backup *sessionbackup.Backuper

	// Guard is the MCP anti-flood limiter — may be nil (the /api/mcp-guard
	// endpoints 503 when nil; the MCP server itself never runs without SOME
	// guard, see mcpserver.New's fallback).
	Guard *mcpguard.Guard

	// Bus is the low-latency event notifier for GET /api/events — may be
	// nil (the endpoint 503s rather than panicking; eventbus.Bus itself is
	// also nil-safe, so a nil Bus doesn't need special-casing anywhere else
	// this gets passed).
	Bus *eventbus.Bus

	// BatteryLogFile is the discharge/charge trace CSV path — read-only
	// here, written by adapters/power/timeremain.py. Empty = GET
	// /api/battery/log returns an empty array
	// (no error) rather than 503 — a missing/unconfigured trace log is a
	// normal degraded state (e.g. PIMYWA_BATTERY_LOG_ENABLED=0), not a
	// broken deploy.
	BatteryLogFile string
}

// Handler builds the REST mux. Endpoints mirror the MCP tools 1:1.
func Handler(d Deps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	// GET /api/status
	mux.HandleFunc("GET /api/status", d.auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, d.State.Snapshot())
	}))

	// GET /api/battery/log?limit= — the discharge/charge trace. Current
	// battery/voltage/charging/time_remaining are already in GET
	// /api/status (state.Status); this is just the historical
	// trace for the dashboard's chart. limit defaults to 500 rows (recent
	// window, not the whole multi-hour file) to keep the response small on a
	// Pi Zero 2 W.
	mux.HandleFunc("GET /api/battery/log", d.auth(func(w http.ResponseWriter, r *http.Request) {
		if d.BatteryLogFile == "" {
			writeJSON(w, http.StatusOK, []batteryLogRow{})
			return
		}
		rows, err := readBatteryLog(d.BatteryLogFile, qint(r, "limit", 500))
		respond(w, rows, err)
	}))

	// GET /api/chats?limit=
	mux.HandleFunc("GET /api/chats", d.auth(func(w http.ResponseWriter, r *http.Request) {
		chats, err := d.Store.ListChats(qint(r, "limit", 20))
		respond(w, chats, err)
	}))

	// GET /api/messages?chat=<jid>&limit=
	mux.HandleFunc("GET /api/messages", d.auth(func(w http.ResponseWriter, r *http.Request) {
		chat := r.URL.Query().Get("chat")
		if chat == "" {
			writeJSON(w, http.StatusBadRequest, errMsg("chat required"))
			return
		}
		msgs, err := d.Store.GetMessages(chat, qint(r, "limit", 20))
		respond(w, msgs, err)
	}))

	// GET /api/queue?limit=  (advanced-mode messages awaiting an agent)
	mux.HandleFunc("GET /api/queue", d.auth(func(w http.ResponseWriter, r *http.Request) {
		msgs, err := d.Store.PendingAdvanced(qint(r, "limit", 20))
		respond(w, msgs, err)
	}))

	// POST /api/send {"to":"...","message":"..."}  (queued; gateway dispatches)
	mux.HandleFunc("POST /api/send", d.auth(func(w http.ResponseWriter, r *http.Request) {
		var b struct{ To, Message string }
		if !decode(w, r, &b) {
			return
		}
		if b.To == "" || b.Message == "" {
			writeJSON(w, http.StatusBadRequest, errMsg("to and message required"))
			return
		}
		err := d.Store.Enqueue(b.To, b.Message, time.Now().Unix())
		respond(w, map[string]string{"status": "queued"}, err)
	}))

	// POST /api/mode {"chat":"...","mode":"auto|advanced"}
	mux.HandleFunc("POST /api/mode", d.auth(func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Chat, Mode string }
		if !decode(w, r, &b) {
			return
		}
		if b.Chat == "" || (b.Mode != "auto" && b.Mode != "advanced") {
			writeJSON(w, http.StatusBadRequest, errMsg("chat and mode(auto|advanced) required"))
			return
		}
		err := d.Store.SetMode(b.Chat, b.Mode)
		respond(w, map[string]string{"status": "ok", "mode": b.Mode}, err)
	}))

	// POST /api/escalate {"chat":"..."}
	mux.HandleFunc("POST /api/escalate", d.auth(func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Chat string }
		if !decode(w, r, &b) {
			return
		}
		if b.Chat == "" {
			writeJSON(w, http.StatusBadRequest, errMsg("chat required"))
			return
		}
		err := d.Store.SetMode(b.Chat, "advanced")
		respond(w, map[string]string{"status": "escalated"}, err)
	}))

	// POST /api/handled {"chat":"...","id":"..."}
	mux.HandleFunc("POST /api/handled", d.auth(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Chat string
			ID   string
		}
		if !decode(w, r, &b) {
			return
		}
		if b.Chat == "" || b.ID == "" {
			writeJSON(w, http.StatusBadRequest, errMsg("chat and id required"))
			return
		}
		err := d.Store.MarkHandled(b.Chat, b.ID)
		respond(w, map[string]string{"status": "ok"}, err)
	}))

	// GET /api/qr.svg — renders current qr_data as an SVG QR code.
	// Returns 404 {"error":"no QR data"} when no QR is available (not in QR mode).
	// Uses github.com/skip2/go-qrcode (pure Go, CGO_ENABLED=0 safe).
	mux.HandleFunc("GET /api/qr.svg", d.auth(func(w http.ResponseWriter, r *http.Request) {
		snap := d.State.Snapshot()
		if snap.QRData == "" {
			writeJSON(w, http.StatusNotFound, errMsg("no QR data"))
			return
		}
		svg, err := makeQRSVG(snap.QRData)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errMsg("QR generation failed"))
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "no-cache, no-store")
		_, _ = w.Write(svg)
	}))

	// GET /api/killswitch — returns {"on": bool} (current kill-switch state).
	mux.HandleFunc("GET /api/killswitch", d.auth(func(w http.ResponseWriter, r *http.Request) {
		on := false
		if d.Gov != nil {
			on = d.Gov.Killed()
		}
		writeJSON(w, http.StatusOK, map[string]bool{"on": on})
	}))

	// POST /api/killswitch {"on": bool} — enable or disable the send kill
	// switch. Also mirrors the flag into state.Status.Muted so the FACE can
	// show it — before this, the switch lived only in
	// the governor (in-memory) and REST; the display had no way to see it.
	mux.HandleFunc("POST /api/killswitch", d.auth(func(w http.ResponseWriter, r *http.Request) {
		var b struct{ On bool }
		if !decode(w, r, &b) {
			return
		}
		if d.Gov != nil {
			d.Gov.SetKill(b.On)
		}
		if d.State != nil {
			_ = d.State.SetMuted(b.On)
		}
		writeJSON(w, http.StatusOK, map[string]bool{"on": b.On})
	}))

	// GET /api/gateway — returns current gateway running state and connection info.
	mux.HandleFunc("GET /api/gateway", d.auth(func(w http.ResponseWriter, r *http.Request) {
		snap := d.State.Snapshot()
		running := false
		if d.GWCtrl != nil {
			running = d.GWCtrl.Running()
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"running":      running,
			"wa_connected": snap.WAConnected,
			"mood":         snap.Mood,
			"own_jid":      snap.OwnJID,
		})
	}))

	// POST /api/gateway {"action":"link"|"disconnect"}
	// "link"       → Start(): launches the connect/QR loop; arms 3-min exposure cap.
	// "disconnect" → Stop(): cancels the loop, resets state to idle.
	mux.HandleFunc("POST /api/gateway", d.auth(func(w http.ResponseWriter, r *http.Request) {
		if d.GWCtrl == nil {
			writeJSON(w, http.StatusServiceUnavailable, errMsg("gateway not available"))
			return
		}
		var b struct{ Action string }
		if !decode(w, r, &b) {
			return
		}
		switch b.Action {
		case "link":
			if err := d.GWCtrl.Start(); err != nil {
				writeJSON(w, http.StatusInternalServerError, errMsg(err.Error()))
				return
			}
		case "disconnect":
			d.GWCtrl.Stop() // synchronous: waits for clean disconnect before responding
		default:
			writeJSON(w, http.StatusBadRequest, errMsg("action must be link or disconnect"))
			return
		}
		snap := d.State.Snapshot()
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":           true,
			"running":      d.GWCtrl.Running(),
			"wa_connected": snap.WAConnected,
			"mood":         snap.Mood,
			"own_jid":      snap.OwnJID,
		})
	}))

	// POST /api/reconnect — clear reconnect-paused state and restart the gateway.
	// Called by the dashboard "Resume connection" button when ReconnectPaused=true.
	mux.HandleFunc("POST /api/reconnect", d.auth(func(w http.ResponseWriter, r *http.Request) {
		if d.GWCtrl == nil {
			writeJSON(w, http.StatusServiceUnavailable, errMsg("gateway not available"))
			return
		}
		if err := d.GWCtrl.Resume(); err != nil {
			writeJSON(w, http.StatusInternalServerError, errMsg(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))

	// GET /api/router — returns the current router config (whitelist, allow_all, etc.)
	mux.HandleFunc("GET /api/router", d.auth(func(w http.ResponseWriter, r *http.Request) {
		if d.RouterMgr == nil {
			writeJSON(w, http.StatusServiceUnavailable, errMsg("router not available"))
			return
		}
		writeJSON(w, http.StatusOK, d.RouterMgr.Snapshot())
	}))

	// POST /api/router/whitelist {"jid":"...","action":"add"|"remove"}
	// Mutates the in-memory whitelist and persists it to router.json immediately.
	// Gateway uses the same Manager so new inbound messages see the change at once.
	mux.HandleFunc("POST /api/router/whitelist", d.auth(func(w http.ResponseWriter, r *http.Request) {
		if d.RouterMgr == nil {
			writeJSON(w, http.StatusServiceUnavailable, errMsg("router not available"))
			return
		}
		var b struct {
			JID    string `json:"jid"`
			Action string `json:"action"` // "add" | "remove"
		}
		if !decode(w, r, &b) {
			return
		}
		if b.JID == "" || (b.Action != "add" && b.Action != "remove") {
			writeJSON(w, http.StatusBadRequest, errMsg("jid and action (add|remove) required"))
			return
		}
		if err := d.RouterMgr.Update(func(c *router.Config) {
			switch b.Action {
			case "add":
				for _, existing := range c.Whitelist {
					if existing == b.JID {
						return // already present — idempotent
					}
				}
				c.Whitelist = append(c.Whitelist, b.JID)
			case "remove":
				newWL := make([]string, 0, len(c.Whitelist))
				for _, existing := range c.Whitelist {
					if existing != b.JID {
						newWL = append(newWL, existing)
					}
				}
				c.Whitelist = newWL
			}
		}); err != nil {
			writeJSON(w, http.StatusInternalServerError, errMsg(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, d.RouterMgr.Snapshot())
	}))

	// POST /api/router/allowall {"on":bool}
	// Enables or disables the "allow all" flag (skips whitelist check entirely).
	// Risky: any number can send messages. Persisted to router.json immediately.
	mux.HandleFunc("POST /api/router/allowall", d.auth(func(w http.ResponseWriter, r *http.Request) {
		if d.RouterMgr == nil {
			writeJSON(w, http.StatusServiceUnavailable, errMsg("router not available"))
			return
		}
		var b struct{ On bool }
		if !decode(w, r, &b) {
			return
		}
		if err := d.RouterMgr.Update(func(c *router.Config) {
			c.AllowAll = b.On
		}); err != nil {
			writeJSON(w, http.StatusInternalServerError, errMsg(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, d.RouterMgr.Snapshot())
	}))

	// POST /api/chats/boss {"jid":"...","boss":bool}
	// Marks/unmarks a chat as the trusted owner (chats.is_boss). Privileged
	// only — gated by the same d.auth() every endpoint here uses (API key,
	// or the dashboard session when mounted there). Deliberately NOT
	// reachable from MCP: an agent must never set a contact's is_boss flag
	// itself (2026-07-01).
	mux.HandleFunc("POST /api/chats/boss", d.auth(func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			writeJSON(w, http.StatusServiceUnavailable, errMsg("store not available"))
			return
		}
		var b struct {
			JID  string `json:"jid"`
			Boss bool   `json:"boss"`
		}
		if !decode(w, r, &b) {
			return
		}
		if b.JID == "" {
			writeJSON(w, http.StatusBadRequest, errMsg("jid required"))
			return
		}
		if err := d.Store.SetIsBoss(b.JID, b.Boss); err != nil {
			writeJSON(w, http.StatusInternalServerError, errMsg(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))

	// POST /api/chats/rules {"jid":"...","rules":"..."}
	// Sets a chat's rules — behavior instructions for how the AI should treat
	// this specific chat ("like a skill"). Privileged only —
	// same gate as /api/chats/boss. Deliberately NOT reachable from MCP: an
	// agent must never rewrite the rules it's judged against (0647).
	mux.HandleFunc("POST /api/chats/rules", d.auth(func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			writeJSON(w, http.StatusServiceUnavailable, errMsg("store not available"))
			return
		}
		var b struct {
			JID   string `json:"jid"`
			Rules string `json:"rules"`
		}
		if !decode(w, r, &b) {
			return
		}
		if b.JID == "" {
			writeJSON(w, http.StatusBadRequest, errMsg("jid required"))
			return
		}
		if err := d.Store.SetChatRules(b.JID, b.Rules); err != nil {
			writeJSON(w, http.StatusInternalServerError, errMsg(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))

	// GET /api/rules — the by-type and global-default rules tiers (1959),
	// for the owner/dashboard to see what's currently set at each level. A
	// chat's own particular rules stay on GET/POST /api/chats/rules.
	mux.HandleFunc("GET /api/rules", d.auth(func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			writeJSON(w, http.StatusServiceUnavailable, errMsg("store not available"))
			return
		}
		individual, err := d.Store.KVGet(store.SettingRulesTypeIndividual)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errMsg(err.Error()))
			return
		}
		group, err := d.Store.KVGet(store.SettingRulesTypeGroup)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errMsg(err.Error()))
			return
		}
		def, err := d.Store.KVGet(store.SettingRulesDefault)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errMsg(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"individual": individual,
			"group":      group,
			"default":    def,
		})
	}))

	// POST /api/rules/type {"type":"individual|group","rules":"..."}
	// Sets the "by type" rules tier (1959) — a chat with no rules of its own
	// inherits these by whether it's a WhatsApp group. Privileged only —
	// same gate as /api/chats/rules. Deliberately NOT reachable from MCP:
	// only the owner assigns rules, at any tier.
	mux.HandleFunc("POST /api/rules/type", d.auth(func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			writeJSON(w, http.StatusServiceUnavailable, errMsg("store not available"))
			return
		}
		var b struct {
			Type  string `json:"type"`
			Rules string `json:"rules"`
		}
		if !decode(w, r, &b) {
			return
		}
		if err := d.Store.SetTypeRules(b.Type, b.Rules); err != nil {
			writeJSON(w, http.StatusBadRequest, errMsg(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))

	// POST /api/rules/default {"rules":"..."}
	// Sets the global default rules tier (1959) — the catch-all for a chat
	// with no particular or by-type rules. Same trust gate as the others.
	mux.HandleFunc("POST /api/rules/default", d.auth(func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			writeJSON(w, http.StatusServiceUnavailable, errMsg("store not available"))
			return
		}
		var b struct {
			Rules string `json:"rules"`
		}
		if !decode(w, r, &b) {
			return
		}
		if err := d.Store.SetDefaultRules(b.Rules); err != nil {
			writeJSON(w, http.StatusInternalServerError, errMsg(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))

	// POST /api/chats/description {"jid":"...","description":"..."}
	// Sets a chat's description — a group's WhatsApp topic, or "a note of
	// your own about the chat" (0130). Privileged only —
	// same gate as /api/chats/rules. group_invite_link has NO REST setter:
	// it only ever comes from WhatsApp, via the gateway.
	mux.HandleFunc("POST /api/chats/description", d.auth(func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			writeJSON(w, http.StatusServiceUnavailable, errMsg("store not available"))
			return
		}
		var b struct {
			JID         string `json:"jid"`
			Description string `json:"description"`
		}
		if !decode(w, r, &b) {
			return
		}
		if b.JID == "" {
			writeJSON(w, http.StatusBadRequest, errMsg("jid required"))
			return
		}
		if err := d.Store.SetChatDescription(b.JID, b.Description); err != nil {
			writeJSON(w, http.StatusInternalServerError, errMsg(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))

	// POST /api/chats/confirmation {"jid":"...","mode":"none|required","confirmer":"..." (optional)}
	// Sets a chat's confirmation baseline and default confirmer (0748/0810)
	// — defaulted by type in TouchChat (group→required, 1-1→none), but the
	// owner can override it per chat here. The bridge's read of that chat's
	// rules can still flip the baseline for a specific reply. Privileged
	// only — same gate as /api/chats/boss. Deliberately NOT reachable from
	// MCP: an agent must not be able to change its own confirmation baseline.
	mux.HandleFunc("POST /api/chats/confirmation", d.auth(func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			writeJSON(w, http.StatusServiceUnavailable, errMsg("store not available"))
			return
		}
		var b struct {
			JID       string `json:"jid"`
			Mode      string `json:"mode"`
			Confirmer string `json:"confirmer"`
		}
		if !decode(w, r, &b) {
			return
		}
		if b.JID == "" {
			writeJSON(w, http.StatusBadRequest, errMsg("jid required"))
			return
		}
		if b.Mode != "none" && b.Mode != "required" {
			writeJSON(w, http.StatusBadRequest, errMsg("mode must be none|required"))
			return
		}
		if err := d.Store.SetConfirmationMode(b.JID, b.Mode); err != nil {
			writeJSON(w, http.StatusInternalServerError, errMsg(err.Error()))
			return
		}
		if err := d.Store.SetConfirmer(b.JID, b.Confirmer); err != nil {
			writeJSON(w, http.StatusInternalServerError, errMsg(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))

	// POST /api/drafts/approve {"id":int,"text":"..." (optional edit)}
	// Moves an auto-reply draft into the outbox (sent with the existing
	// anti-ban pacing). Privileged only — same gate as /api/chats/boss.
	// Deliberately NOT reachable from MCP: the owner approves, never an agent.
	mux.HandleFunc("POST /api/drafts/approve", d.auth(func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			writeJSON(w, http.StatusServiceUnavailable, errMsg("store not available"))
			return
		}
		var b struct {
			ID   int64  `json:"id"`
			Text string `json:"text"`
		}
		if !decode(w, r, &b) {
			return
		}
		ok, err := d.Store.ApproveDraft(b.ID, b.Text, time.Now().Unix())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errMsg(err.Error()))
			return
		}
		if !ok {
			writeJSON(w, http.StatusNotFound, errMsg("draft not found or not pending"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))

	// POST /api/drafts/discard {"id":int}
	// Marks a draft discarded — never sent. Privileged only.
	mux.HandleFunc("POST /api/drafts/discard", d.auth(func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			writeJSON(w, http.StatusServiceUnavailable, errMsg("store not available"))
			return
		}
		var b struct {
			ID int64 `json:"id"`
		}
		if !decode(w, r, &b) {
			return
		}
		ok, err := d.Store.DiscardDraft(b.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errMsg(err.Error()))
			return
		}
		if !ok {
			writeJSON(w, http.StatusNotFound, errMsg("draft not found or not pending"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))

	// GET/POST /api/settings — runtime-editable dashboard config (0753):
	// media download skip (video/photo × group/chat), media retention (MB),
	// and delay windows. Rate limits are reported/accepted here too but
	// live in the governor, not KV, for their effective values (see below).
	// Same d.auth() gate as every other settings-style endpoint here
	// (/api/router, /api/killswitch) — not a privileged/owner-only gate.
	mux.HandleFunc("GET /api/settings", d.auth(func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			writeJSON(w, http.StatusServiceUnavailable, errMsg("store not available"))
			return
		}
		writeJSON(w, http.StatusOK, d.currentSettings())
	}))

	mux.HandleFunc("POST /api/settings", d.auth(func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			writeJSON(w, http.StatusServiceUnavailable, errMsg("store not available"))
			return
		}
		var b settingsPayload
		if !decode(w, r, &b) {
			return
		}

		_ = d.Store.SetSettingBool(store.SettingMediaSkipVideoGroup, b.MediaSkipVideoGroup)
		_ = d.Store.SetSettingBool(store.SettingMediaSkipVideoChat, b.MediaSkipVideoChat)
		_ = d.Store.SetSettingBool(store.SettingMediaSkipPhotoGroup, b.MediaSkipPhotoGroup)
		_ = d.Store.SetSettingBool(store.SettingMediaSkipPhotoChat, b.MediaSkipPhotoChat)

		// Media retention: floored only (3rd commandment — GC must stay
		// meaningfully active, the dashboard can never disable it outright).
		mediaMaxMB := b.MediaMaxMB
		if mediaMaxMB < d.MediaMaxMBFloor {
			mediaMaxMB = d.MediaMaxMBFloor
		}
		_ = d.Store.SetSettingInt(store.SettingMediaMaxMB, mediaMaxMB)

		// Delay windows: floored at the shipped default (never faster/more
		// aggressive than what shipped — "it must be slowly,
		// even read time delays"), max re-clamped to >= min after flooring.
		dispatchMin, dispatchMax := clampDelayWindow(b.DispatchDelayMinSec, b.DispatchDelayMaxSec, d.DispatchDelayMinDefault)
		readMin, readMax := clampDelayWindow(b.ReadDelayMinSec, b.ReadDelayMaxSec, d.ReadDelayMinDefault)
		actionMin, actionMax := clampDelayWindow(b.ActionDelayMinSec, b.ActionDelayMaxSec, d.ActionDelayMinDefault)
		_ = d.Store.SetSettingDuration(store.SettingDispatchDelayMin, dispatchMin)
		_ = d.Store.SetSettingDuration(store.SettingDispatchDelayMax, dispatchMax)
		_ = d.Store.SetSettingDuration(store.SettingReadDelayMin, readMin)
		_ = d.Store.SetSettingDuration(store.SettingReadDelayMax, readMax)
		_ = d.Store.SetSettingDuration(store.SettingActionDelayMin, actionMin)
		_ = d.Store.SetSettingDuration(store.SettingActionDelayMax, actionMax)

		// Rate limits: ceilinged (spam/ban risk — the opposite guardrail
		// from delays). per-min also floored at 1: 0/negative would be a
		// silent de-facto kill switch, and there's already a real one for
		// that (POST /api/killswitch) — this is just a sanity minimum, not
		// an anti-ban rule. per-day floored at 0 (0 = daily cap disabled,
		// a valid choice — negative isn't).
		perMin := b.RateLimitPerMin
		if perMin > d.RateLimitPerMinCeiling {
			perMin = d.RateLimitPerMinCeiling
		}
		if perMin < 1 {
			perMin = 1
		}
		perDay := b.RateLimitPerDay
		if perDay > d.RateLimitPerDayCeiling {
			perDay = d.RateLimitPerDayCeiling
		}
		if perDay < 0 {
			perDay = 0
		}
		_ = d.Store.SetSettingInt(store.SettingRateLimitPerMin, perMin)
		_ = d.Store.SetSettingInt(store.SettingRateLimitPerDay, perDay)
		if d.Gov != nil {
			d.Gov.SetMax(perMin)
			d.Gov.SetDailyMax(perDay)
		}

		writeJSON(w, http.StatusOK, d.currentSettings())
	}))

	// GET /api/backup — session backup status: enabled/off, last
	// backup metadata, how many are retained. Never anything secret.
	mux.HandleFunc("GET /api/backup", d.auth(func(w http.ResponseWriter, r *http.Request) {
		if d.Backup == nil {
			writeJSON(w, http.StatusServiceUnavailable, errMsg("backup not available"))
			return
		}
		writeJSON(w, http.StatusOK, d.Backup.Status())
	}))

	// POST /api/backup — "backup now". No body. Restore is
	// deliberately NOT here — CLI-only, see main.go's restore-session
	// subcommand (never expose "replace the session" over the LAN).
	mux.HandleFunc("POST /api/backup", d.auth(func(w http.ResponseWriter, r *http.Request) {
		if d.Backup == nil {
			writeJSON(w, http.StatusServiceUnavailable, errMsg("backup not available"))
			return
		}
		res, err := d.Backup.BackupNow(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errMsg(err.Error()))
			return
		}
		if res.Path == "" {
			writeJSON(w, http.StatusServiceUnavailable, errMsg("backups are disabled (no PIMYWA_BACKUP_KEY configured)"))
			return
		}
		writeJSON(w, http.StatusOK, res)
	}))

	// GET /api/mcp-guard — anti-flood status:
	// effective config plus every currently-tracked MCP client (session ID,
	// throttle/block state). Session IDs are opaque per-connection
	// identifiers, NOT the auth secret — safe to show:
	// "log del SessionID (nunca el token)".
	mux.HandleFunc("GET /api/mcp-guard", d.auth(func(w http.ResponseWriter, r *http.Request) {
		if d.Guard == nil {
			writeJSON(w, http.StatusServiceUnavailable, errMsg("mcp guard not available"))
			return
		}
		writeJSON(w, http.StatusOK, d.Guard.Status())
	}))

	// POST /api/mcp-guard — edit the anti-flood thresholds at runtime
	// (dashboard-editable, same KV-override mechanism as /api/settings).
	// Floored/ceilinged so the dashboard can never disable the protection
	// outright (floor) nor make it impractically strict (ceiling) —
	// mirrors the guardrail pattern already used for delays/rate limits.
	mux.HandleFunc("POST /api/mcp-guard", d.auth(func(w http.ResponseWriter, r *http.Request) {
		if d.Guard == nil || d.Store == nil {
			writeJSON(w, http.StatusServiceUnavailable, errMsg("mcp guard not available"))
			return
		}
		var b mcpGuardPayload
		if !decode(w, r, &b) {
			return
		}
		rate := clampInt(b.RatePerMin, 10, 600)
		emitRate := clampInt(b.EmitRatePerMin, 2, 100)
		threshold := clampInt(b.BlockThreshold, 1, 20)
		cooldown := clampDuration(time.Duration(b.BlockCooldownSec*float64(time.Second)), 30*time.Second, time.Hour)

		_ = d.Store.SetSettingInt(store.SettingMCPGuardRatePerMin, rate)
		_ = d.Store.SetSettingInt(store.SettingMCPGuardEmitRatePerMin, emitRate)
		_ = d.Store.SetSettingInt(store.SettingMCPGuardBlockThreshold, threshold)
		_ = d.Store.SetSettingDuration(store.SettingMCPGuardBlockCooldown, cooldown)

		d.Guard.SetRatePerMin(rate)
		d.Guard.SetEmitRatePerMin(emitRate)
		d.Guard.SetBlockThreshold(threshold)
		d.Guard.SetBlockCooldown(cooldown)

		writeJSON(w, http.StatusOK, d.Guard.Status())
	}))

	// GET /api/events — low-latency SSE notifications, an alternative to
	// polling get_pending. Same d.auth() gate as
	// everything else (X-API-Key header or ?key=) — note ?key= puts the key
	// in the URL (could land in a proxy's access log); acceptable on a LAN,
	// and it only matters for a browser hitting this directly, since the
	// dashboard itself goes through its own cookie session, not this REST
	// key at all.
	mux.HandleFunc("GET /api/events", d.auth(func(w http.ResponseWriter, r *http.Request) {
		if d.Bus == nil {
			writeJSON(w, http.StatusServiceUnavailable, errMsg("events not available"))
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			// Never actually happens on net/http's real server, but a fake
			// ResponseWriter (a test, or some future middleware) might not
			// implement it — fail loud rather than silently never flushing.
			writeJSON(w, http.StatusInternalServerError, errMsg("streaming not supported"))
			return
		}

		ch, unsubscribe := d.Bus.Subscribe()
		// MUST run on every exit path (client disconnect, write error, or
		// this handler returning for any other reason) — otherwise a
		// dropped SSE connection leaks its channel/goroutine forever, which
		// matters on a long-running Pi service serving many short-lived
		// connections over time.
		defer unsubscribe()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		// Heartbeat: an idle stream (no new messages) would otherwise sit
		// silent long enough for a proxy/load balancer to decide it's dead
		// and kill it, and gives a client no way to tell "still alive" from
		// "silently hung" — cheap insurance even on a LAN-only deployment.
		heartbeat := time.NewTicker(20 * time.Second)
		defer heartbeat.Stop()

		for {
			select {
			case <-r.Context().Done():
				// The client disconnected (or the request otherwise ended) —
				// net/http cancels this on its own; nothing else to do here
				// beyond the deferred unsubscribe above.
				return
			case <-heartbeat.C:
				if _, err := w.Write([]byte(": keep-alive\n\n")); err != nil {
					return
				}
				flusher.Flush()
			case e, ok := <-ch:
				if !ok {
					return // bus/subscription closed out from under us
				}
				b, err := json.Marshal(e)
				if err != nil {
					continue // never expected (Event is a plain struct); skip rather than kill the stream
				}
				if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}))

	return mux
}

// mcpGuardPayload is the wire format for POST /api/mcp-guard. Cooldown is
// seconds (float64), same convention as settingsPayload's delay fields —
// simpler for a plain <input type="number"> than a Go duration string.
type mcpGuardPayload struct {
	RatePerMin       int     `json:"rate_per_min"`
	EmitRatePerMin   int     `json:"emit_rate_per_min"`
	BlockThreshold   int     `json:"block_threshold"`
	BlockCooldownSec float64 `json:"block_cooldown_sec"`
}

func clampInt(n, floor, ceiling int) int {
	if n < floor {
		return floor
	}
	if n > ceiling {
		return ceiling
	}
	return n
}

func clampDuration(d, floor, ceiling time.Duration) time.Duration {
	if d < floor {
		return floor
	}
	if d > ceiling {
		return ceiling
	}
	return d
}

// settingsPayload is the wire format for GET/POST /api/settings. Delays are
// seconds (float64), not Go duration strings — simpler for a plain HTML
// <input type="number"> on the dashboard than round-tripping "1.5s" text.
type settingsPayload struct {
	MediaSkipVideoGroup bool `json:"media_skip_video_group"`
	MediaSkipVideoChat  bool `json:"media_skip_video_chat"`
	MediaSkipPhotoGroup bool `json:"media_skip_photo_group"`
	MediaSkipPhotoChat  bool `json:"media_skip_photo_chat"`
	MediaMaxMB          int  `json:"media_max_mb"`

	DispatchDelayMinSec float64 `json:"dispatch_delay_min_sec"`
	DispatchDelayMaxSec float64 `json:"dispatch_delay_max_sec"`
	ReadDelayMinSec     float64 `json:"read_delay_min_sec"`
	ReadDelayMaxSec     float64 `json:"read_delay_max_sec"`
	ActionDelayMinSec   float64 `json:"action_delay_min_sec"`
	ActionDelayMaxSec   float64 `json:"action_delay_max_sec"`

	RateLimitPerMin int `json:"rate_limit_per_min"`
	RateLimitPerDay int `json:"rate_limit_per_day"`
}

// settingsResponse is what GET returns and POST echoes back: the effective
// (post-clamp) payload plus the floors/ceilings the dashboard shows as hints.
type settingsResponse struct {
	settingsPayload
	Floors   settingsFloors   `json:"floors"`
	Ceilings settingsCeilings `json:"ceilings"`
}

type settingsFloors struct {
	DispatchDelaySec float64 `json:"dispatch_delay_sec"`
	ReadDelaySec     float64 `json:"read_delay_sec"`
	ActionDelaySec   float64 `json:"action_delay_sec"`
	MediaMaxMB       int     `json:"media_max_mb"`
}

type settingsCeilings struct {
	RateLimitPerMin int `json:"rate_limit_per_min"`
	RateLimitPerDay int `json:"rate_limit_per_day"`
}

// currentSettings reads the effective settings straight from the store/
// governor — the single source both GET and POST's response use, so the
// two can never drift apart.
func (d Deps) currentSettings() settingsResponse {
	perMin, perDay := d.RateLimitPerMinDefault, d.RateLimitPerDayDefault
	if d.Gov != nil {
		perMin, perDay = d.Gov.Max(), d.Gov.DailyMax()
	}
	return settingsResponse{
		settingsPayload: settingsPayload{
			MediaSkipVideoGroup: d.Store.SettingBool(store.SettingMediaSkipVideoGroup, false),
			MediaSkipVideoChat:  d.Store.SettingBool(store.SettingMediaSkipVideoChat, false),
			MediaSkipPhotoGroup: d.Store.SettingBool(store.SettingMediaSkipPhotoGroup, false),
			MediaSkipPhotoChat:  d.Store.SettingBool(store.SettingMediaSkipPhotoChat, false),
			MediaMaxMB:          d.Store.SettingInt(store.SettingMediaMaxMB, d.MediaMaxMBDefault),

			DispatchDelayMinSec: d.Store.SettingDuration(store.SettingDispatchDelayMin, d.DispatchDelayMinDefault).Seconds(),
			DispatchDelayMaxSec: d.Store.SettingDuration(store.SettingDispatchDelayMax, d.DispatchDelayMaxDefault).Seconds(),
			ReadDelayMinSec:     d.Store.SettingDuration(store.SettingReadDelayMin, d.ReadDelayMinDefault).Seconds(),
			ReadDelayMaxSec:     d.Store.SettingDuration(store.SettingReadDelayMax, d.ReadDelayMaxDefault).Seconds(),
			ActionDelayMinSec:   d.Store.SettingDuration(store.SettingActionDelayMin, d.ActionDelayMinDefault).Seconds(),
			ActionDelayMaxSec:   d.Store.SettingDuration(store.SettingActionDelayMax, d.ActionDelayMaxDefault).Seconds(),

			RateLimitPerMin: perMin,
			RateLimitPerDay: perDay,
		},
		Floors: settingsFloors{
			DispatchDelaySec: d.DispatchDelayMinDefault.Seconds(),
			ReadDelaySec:     d.ReadDelayMinDefault.Seconds(),
			ActionDelaySec:   d.ActionDelayMinDefault.Seconds(),
			MediaMaxMB:       d.MediaMaxMBFloor,
		},
		Ceilings: settingsCeilings{
			RateLimitPerMin: d.RateLimitPerMinCeiling,
			RateLimitPerDay: d.RateLimitPerDayCeiling,
		},
	}
}

// clampDelayWindow floors both min and max at floor (0753 — never faster
// than what shipped as default), then re-clamps max to be >= min so the
// floor can never invert the window.
func clampDelayWindow(minSec, maxSec float64, floor time.Duration) (min, max time.Duration) {
	min = time.Duration(minSec * float64(time.Second))
	max = time.Duration(maxSec * float64(time.Second))
	if min < floor {
		min = floor
	}
	if max < floor {
		max = floor
	}
	if max < min {
		max = min
	}
	return min, max
}

// makeQRSVG generates an SVG QR code for the given data string.
// It uses the Bitmap() method of go-qrcode to get the raw module matrix and
// renders each dark module as an SVG <rect> element. Pure Go, no CGO.
func makeQRSVG(data string) ([]byte, error) {
	q, err := qrcode.New(data, qrcode.Medium)
	if err != nil {
		return nil, err
	}
	bm := q.Bitmap() // [][]bool, includes quiet zone
	n := len(bm)
	const mod = 8 // pixels per module
	sz := n * mod

	var buf bytes.Buffer
	fmt.Fprintf(&buf, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d">`,
		sz, sz, sz, sz)
	buf.WriteString(`<rect width="100%" height="100%" fill="white"/>`)
	for y, row := range bm {
		for x, dark := range row {
			if dark {
				fmt.Fprintf(&buf, `<rect x="%d" y="%d" width="%d" height="%d" fill="black"/>`,
					x*mod, y*mod, mod, mod)
			}
		}
	}
	buf.WriteString(`</svg>`)
	return buf.Bytes(), nil
}

func (d Deps) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.APIKey != "" {
			key := r.Header.Get("X-API-Key")
			if key == "" {
				key = r.URL.Query().Get("key")
			}
			if key != d.APIKey {
				writeJSON(w, http.StatusUnauthorized, errMsg("unauthorized"))
				return
			}
		}
		h(w, r)
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, errMsg("invalid JSON body"))
		return false
	}
	return true
}

func qint(r *http.Request, k string, def int) int {
	if v := r.URL.Query().Get(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func respond(w http.ResponseWriter, v any, err error) {
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errMsg(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// batteryLogRow is one line of the discharge/charge trace CSV the power
// adapter writes — field order/meaning mirrors
// adapters/power/timeremain.py's _LOG_HEADER exactly:
// ts,voltage_mv,raw_pct,linearized_pct,charging.
type batteryLogRow struct {
	TS            int64   `json:"ts"`
	VoltageMV     *int    `json:"voltage_mv"`
	RawPct        int     `json:"raw_pct"`
	LinearizedPct float64 `json:"linearized_pct"`
	Charging      bool    `json:"charging"`
}

// readBatteryLog reads the CURRENT (non-rotated) trace file and returns the
// last `limit` rows, oldest first. Deliberately does not also read the .1
// rotation — the dashboard chart wants a recent window, not the full
// history, and reading a second file on every poll for data nobody asked to
// see is wasted I/O on a Pi Zero 2 W's SD card.
func readBatteryLog(path string, limit int) ([]batteryLogRow, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []batteryLogRow{}, nil // no trace yet (fresh install, or log disabled) -- not an error
		}
		return nil, err
	}
	defer f.Close()

	var rows []batteryLogRow
	sc := bufio.NewScanner(f)
	header := true
	for sc.Scan() {
		if header {
			header = false
			continue
		}
		if row, ok := parseBatteryLogLine(sc.Text()); ok {
			rows = append(rows, row)
		}
		// A malformed row (e.g. one truncated by a power cut mid-write — see
		// the Python side's module doc) is silently skipped, not fatal: a
		// trace missing one sample is still useful, erroring the whole
		// endpoint over one bad line is not.
	}
	if rows == nil {
		rows = []batteryLogRow{}
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[len(rows)-limit:]
	}
	return rows, nil
}

func parseBatteryLogLine(line string) (batteryLogRow, bool) {
	parts := strings.Split(line, ",")
	if len(parts) != 5 {
		return batteryLogRow{}, false
	}
	ts, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return batteryLogRow{}, false
	}
	var voltageMV *int
	if parts[1] != "" {
		v, err := strconv.Atoi(parts[1])
		if err != nil {
			return batteryLogRow{}, false
		}
		voltageMV = &v
	}
	rawPct, err := strconv.Atoi(parts[2])
	if err != nil {
		return batteryLogRow{}, false
	}
	linPct, err := strconv.ParseFloat(parts[3], 64)
	if err != nil {
		return batteryLogRow{}, false
	}
	return batteryLogRow{
		TS: ts, VoltageMV: voltageMV, RawPct: rawPct,
		LinearizedPct: linPct, Charging: parts[4] == "1",
	}, true
}

func errMsg(s string) map[string]string { return map[string]string{"error": s} }
