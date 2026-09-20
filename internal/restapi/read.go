// Read-side endpoints for the dashboard (ct-2026-07-10-2312): status,
// chats, and the QR pairing code — all GET, all just projections of
// state/store, no mutation. Admin (POST) endpoints stay in admin.go.
package restapi

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"piumy-gateway/internal/capipush"
	"piumy-gateway/internal/config"
	"piumy-gateway/internal/store"
)

// dashboardChatLimit bounds GET /api/chats — bumped from the pre-S1g
// default (ListChats(0) fell back to 20) because the collapsible-groups
// list (S1g, ct-2026-07-19-1801) needs the boss's real "600+" scale to
// classify correctly, not just the 20 most-recently-touched rows. Still a
// real cap, not a literal unlimited query — 5000 comfortably covers the
// documented ~717-member scale with headroom.
const dashboardChatLimit = 5000

func (d Deps) registerReadRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/status", d.auth(d.handleStatus))
	mux.HandleFunc("GET /api/chats", d.auth(d.handleChats))
	mux.HandleFunc("GET /api/messages", d.auth(d.handleMessages))
	mux.HandleFunc("GET /api/qr", d.auth(d.handleQR))
	mux.HandleFunc("GET /api/media", d.auth(d.handleMedia))
	mux.HandleFunc("GET /api/avatar", d.auth(d.handleAvatar))
	mux.HandleFunc("GET /api/agents", d.auth(d.handleAgents))
	mux.HandleFunc("GET /api/agents/chats", d.auth(d.handleAgentChats))
}

// handleStatus is the dashboard's one status projection. S1b
// (ct-2026-07-19-1823) added the governor/backup/cifrado/antenna fields —
// real data for the badges S1a deliberately left off rather than fabricate
// (see docs/MANUAL.md's S1a note). Each new field's source (Governor/Store/
// Backup) is independently nil-safe, same convention as the rest of
// restapi: an unwired dependency degrades that one field, never 503s the
// whole endpoint (unlike State, which the endpoint has always required).
func (d Deps) handleStatus(w http.ResponseWriter, r *http.Request) {
	if d.State == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "state not available"})
		return
	}
	snap := d.State.Snapshot()

	var governorRatePerMin int
	var governorKilled bool
	if d.Governor != nil {
		governorRatePerMin = d.Governor.Max()
		governorKilled = d.Governor.Killed()
	}

	var backupChats, backupGroups, backupContacts, backupNumbers int
	var antennaConfigured bool
	if d.Store != nil {
		backupChats, backupGroups, backupContacts, backupNumbers, _ = d.Store.BackupCounts()
		endpoint, _ := d.Store.KVGet(store.SettingCAPIEndpoint)
		antennaConfigured = endpoint != ""
	}

	backupEncrypted := d.Backup != nil && d.Backup.Enabled()

	// factoryPassword: T9 (ct-2026-08-05-1137) — T8 makes a silent install
	// fall back to "piumy" (public: it's in the installer and the
	// open-source code), so the dashboard has to say so instead of staying
	// quiet about it. Comparison is server-side only (isFactoryPassword,
	// auth.go) — the frontend gets a bool, never a password.
	var factoryPassword bool
	if d.Store != nil {
		factoryPassword, _ = isFactoryPassword(d.Store)
	}

	// defaultTerminalConfigured: T25 (hallazgo 2, ct-2026-08-05-1833) —
	// main.go ya resuelve PIUMY_DEFAULT_TERMINAL_ID con un respaldo (la
	// antena principal ya configurada, si la variable de entorno viene
	// vacía) ANTES de armar Deps.PrincipalTerminalID — así que esto es lo
	// mismo que ese campo usa en todos lados (get_instructions,
	// PrincipalAgent), no un valor nuevo. false solo cuando NI la variable
	// de entorno NI la antena dan un terminal — recién ahí los mensajes del
	// dueño no tienen a dónde ir, y el boss lo tiene que ver acá, no
	// solamente en el log de arranque que nadie mira.
	defaultTerminalConfigured := d.PrincipalTerminalID != ""

	// historyLoaded/historyTotal: "X de Y" chats with at least one message
	// on file (ct-2026-07-24-2004 — replaces the removed ON_DEMAND worker's
	// progress line; see store.HistorySummary's own doc for why). Moves
	// forward as real messages arrive (live or passive push), never
	// backward — the boss's own redefined goal.
	var historyLoaded, historyTotal int
	if d.Store != nil {
		historyLoaded, historyTotal, _ = d.Store.HistorySummary()
	}

	// historySync*: the LIVE passive full-sync visibility signal (items 1+2,
	// ct-2026-07-23-0047) — DISTINCT from history_loaded/total above, which
	// is a persisted snapshot of every chat, not a per-push counter. This is
	// what tells the boss the post-re-pareo push is actually flowing right
	// now.
	var historySyncActive bool
	var historySyncMessages, historySyncChats int
	var historySyncLastChunkAgoSec int
	if d.HistorySyncProgress != nil {
		active, messages, chats, ago := d.HistorySyncProgress.HistorySyncStatus()
		historySyncActive = active
		historySyncMessages = messages
		historySyncChats = chats
		historySyncLastChunkAgoSec = int(ago.Seconds())
	}

	// qr_*: P2 (ct-2026-07-24-0015) — the live QR-pairing state for the
	// frontend's countdown + expired + refresh. Read from QRStatus (adapter)
	// directly, NOT from state.Status (which only has stale ShowQR/QRData
	// written by main.go's QR reader). Zero values when no round is active
	// (including when paired — nothing to show).
	var qrCode string
	var qrExpiresAt int64
	var qrExpired bool
	if d.QRStatus != nil {
		code, expiresAt, expired := d.QRStatus.QRState()
		qrCode = code
		if !expiresAt.IsZero() {
			qrExpiresAt = expiresAt.Unix()
		}
		qrExpired = expired
	}

	// account/account_color (S3, ct-2026-09-20-1202): config.ColorForAccount
	// is the SAME function main's trayicon_recolor.go feeds the tray icon
	// with — this just reports its Hex, never derives its own. Empty
	// d.Account -> both come back "" (Hex is already "" for that case, see
	// AccountColor's own doc), the dashboard shows nothing new.
	accountColor := config.ColorForAccount(d.Account)

	writeJSON(w, http.StatusOK, map[string]any{
		"name":       snap.OwnName,
		"own_number": snap.OwnJID,
		"connected":  snap.WAConnected,
		"show_qr":    snap.ShowQR,
		"agents":     snap.Agents,
		"sent":       snap.Sent,
		"muted":      snap.Muted,
		"mood":       snap.Mood,
		"queue":      snap.Queue,

		"antenna_configured":          antennaConfigured,
		"factory_password":            factoryPassword,
		"default_terminal_configured": defaultTerminalConfigured,

		"governor_rate_per_min": governorRatePerMin,
		"governor_killed":       governorKilled,

		"backup_chats":     backupChats,
		"backup_groups":    backupGroups,
		"backup_contacts":  backupContacts,
		"backup_numbers":   backupNumbers,
		"backup_encrypted": backupEncrypted,

		"history_loaded": historyLoaded,
		"history_total":  historyTotal,

		"history_sync_active":             historySyncActive,
		"history_sync_messages":           historySyncMessages,
		"history_sync_chats":              historySyncChats,
		"history_sync_last_chunk_ago_sec": historySyncLastChunkAgoSec,

		"qr_code":       qrCode,
		"qr_expires_at": qrExpiresAt,
		"qr_expired":    qrExpired,

		"account":       d.Account,
		"account_color": accountColor.Hex,
	})
}

// chatOut is GET /api/chats' per-chat shape — the dashboard's own fields
// (ct-2026-07-10-2312 §1), not store.Chat verbatim (that would leak
// dashboard-irrelevant columns like claimed_by/description).
//
// Type/GroupJID (S1g, ct-2026-07-19-1801) split one flat list into the
// dashboard's two zones: Type "p2p" (a real chats row with ≥1 message,
// rendered in the existing table, unchanged) or "group" (a chats row whose
// jid is @g.us — GroupJID empty, it doesn't belong to another group) or
// "group_member" (a synthetic row, NOT a chats row at all — projected from
// group_members, GroupJID set to which group it's under; every other field
// besides JID/Name/LastTS is the Go zero value, nothing else applies to a
// person who's never actually chatted). One flat array, not a nested
// groups-containing-members structure — grouping/collapsing is a rendering
// concern, left to app.js; classification (what's noise, what's a group,
// who's a member of what) is the one place that logic should live, here.
type chatOut struct {
	JID    string `json:"jid"`
	Name   string `json:"name"`
	Level  string `json:"level"`
	Mode   string `json:"mode"`
	IsBoss bool   `json:"is_boss"`
	// IsApprover (Aprobador P1, ct-2026-07-31-0610): the approver pin —
	// orthogonal to IsBoss/ConfigLevel, see store.Chat.IsApprover's doc.
	// The dashboard renders it as its own marker, separate from the boss
	// star; the Nivel selector (ConfigLevel below) is untouched.
	IsApprover       bool   `json:"is_approver"`
	ConfirmationMode string `json:"confirmation_mode"`
	// ConfigLevel: the unified 5-value config level (boss/auto/confirm/
	// unattended/ignored) derived from is_boss/status/active/
	// confirmation_mode — store.ConfigLevel's doc has the full mapping.
	// Read-only projection for the interface; IsBoss/ConfirmationMode/Status
	// above stay exposed unchanged, this doesn't replace them.
	ConfigLevel string `json:"config_level"`
	// Status is the raw triage status (whitelist|blacklist|new|ignored|
	// agent_exclusive:<id>) — the dashboard only cares whether it equals
	// "ignored" (ct-2026-07-10-2312 rework, F2v2), exposed as-is rather
	// than a derived bool so nothing is hidden from an admin who inspects
	// the API directly.
	Status string `json:"status"`
	LastTS int64  `json:"last_ts"`
	Rules  string `json:"rules"`
	// RulesSource (ct-2026-07-31, "las reglas por defecto no se ven"): which
	// tier of the hierarchy is actually in effect when Rules is empty —
	// "particular" | "tipo:grupo" | "origen:nuevo" | "origen:contacto" |
	// "general" | "" (no rule at any level, the agent won't act). Mirrors
	// store.EffectiveRules' own branching so the dashboard can say WHICH
	// rule governs a chat instead of just "no reglas propias" — that phrase
	// used to read as "nothing applies here", when a type/origin/default
	// tier could be governing it the whole time. Computed here (not a
	// second implementation in JS) so the hierarchy lives in exactly one
	// place; see rulesSourceFor's doc for why it's not just a call to
	// EffectiveRules per row. Empty for a "group_member" pseudo-row (not a
	// dispatchable chat, no resolution applies).
	RulesSource string `json:"rules_source"`
	Memory      string `json:"memory"`
	// ContactName: the phone address book's own name for this number/group
	// (store.Chat.ContactName's doc) — distinct from Name, the WhatsApp
	// display name.
	ContactName string `json:"contact_name,omitempty"`
	// LastText: the chat's most recent message text, for a preview line in
	// the dashboard list — empty for a chat with no messages yet (e.g. a
	// group_member pseudo-row, which never has a real chats/messages row).
	LastText string `json:"last_text,omitempty"`
	Context  string `json:"context"`
	Type     string `json:"type"`
	GroupJID string `json:"group_jid,omitempty"`
	// HasMessages: whether at least one real message (inbound or outbound)
	// exists for this JID — store.ChatJIDsWithMessages' criterion (see its
	// doc). The dashboard's Chats tab (S1h-fix) uses this to show ONLY real
	// conversations, not every address-book contact/group_member the boss
	// has never actually messaged — those live in the Contactos tab instead.
	// Always false for a "group_member" row (the loop below only emits one
	// when it's NOT already in withMessages).
	HasMessages bool `json:"has_messages"`
	// HistoryState: gradual on-demand history backfill progress
	// (ct-2026-07-21-1306) — pending/downloading/loaded, store.Chat's own
	// field verbatim. Empty for a "group_member" pseudo-row (not a real
	// chats row, see store.ConfigLevel's doc for the same convention) — the
	// visor (a later subcontrato) reads this to show "descargando"/"todo
	// cargado" per chat.
	HistoryState string `json:"history_state,omitempty"`
	// ResolvedNumber: the real phone-number JID a @lid row resolves to via
	// LIDResolver (ct-2026-07-21-1809) — set ONLY for a row whose own JID is
	// a @lid AND the resolution succeeded. The frontend shows this instead
	// of the raw @lid "number" (which reads as a nonsense phone number).
	// Empty for every other row — never touches JID itself, which stays the
	// real store key (no re-keying, see resolveCanonical's doc).
	//
	// NO omitempty (ct-2026-07-29, render crash regression): app.js's
	// jidNumber(c.resolved_number) is called unconditionally (falls back to
	// jidNumber(c.jid) via ||, relying on jidNumber("") === "" being falsy —
	// see contactRow's own doc). omitempty dropped the JSON key entirely for
	// every non-@lid row, so c.resolved_number was `undefined` in JS —
	// jidNumber(undefined) throws, killing render() mid-loop (and with it
	// renderGroups/renderContacts, called at render()'s tail). The field
	// must always serialize, "" included, for that fallback to hold.
	ResolvedNumber string `json:"resolved_number"`
	// IsContact: true when this person is a real phone-book contact —
	// contact_name != "" (Tramo B, ct-2026-07-22-0436 P5). For "p2p"/"group"
	// rows that's this chat's own ContactName; for a "group_member" pseudo-
	// row it's whether the member's canonical identity has a contact_name
	// somewhere in chats (chatByCanon lookup below). Lets the Contactos tab
	// split "Contactos" (agenda real) from "Números" (solo participantes).
	IsContact bool `json:"is_contact"`
	// Origin (T18, ct-2026-08-05-1243): store.Chat.Origin verbatim —
	// inbound_spoke (a real conversation happened, either direction) /
	// group_discovered (only ever seen as a group member) / synced_contact
	// (an empty phone-book entry). Already computed on every ListChats call
	// (enrichChat) whether or not this field exists — exposing it here costs
	// nothing new. The Chats tab uses this (not has_messages) to decide what
	// counts as a real conversation; see app.js's isRealConversation.
	// Empty for a "group_member" pseudo-row (not a real chats row, ChatOrigin
	// never runs on it — same convention as HistoryState/RulesSource above).
	Origin string `json:"origin,omitempty"`
}

// resolveCanonical returns jid's real-number identity via LIDResolver when
// jid is a @lid whose resolution succeeds, else jid unchanged — a READ-ONLY
// label lookup (ct-2026-07-21-1809), not the identidad-unificada reconcile
// the boss cancelled (see LIDResolver's own doc): no store write, no
// re-keying, `chats`/`messages` stay exactly as they are. nil LIDResolver or
// an unresolved lookup is a no-op, same fallback discipline capipush's own
// use of this seam already has.
func (d Deps) resolveCanonical(ctx context.Context, jid string) string {
	if d.LIDResolver == nil || !store.IsLIDJID(jid) {
		return jid
	}
	if pn, err := d.LIDResolver.ResolvePN(ctx, jid); err == nil && pn != "" {
		return pn
	}
	return jid
}

// siblingJIDs returns every chats-row JID that resolves to the SAME
// canonical identity as jid (jid itself always included, first) — the
// person's real number PLUS every @lid alias WhatsApp may have delivered
// messages/media under. Same equivalence rule handleChats' p2p dedup
// already uses (resolveCanonical) — shared, not reimplemented: if the
// equivalence rule ever changes, it changes in exactly one place.
//
// ct-2026-07-29: handleMessages used to query only the DISPLAYED jid (the
// dedup's winning real-number row) and came up empty — the conversation's
// actual messages lived under the @lid sibling the dedup drops from
// GET /api/chats. Same gap existed in handleFetchMedia's on-demand media
// burst. Both now go through this.
//
// nil Store degrades to []string{jid} (no sibling lookup) — same
// unwired-dependency convention as resolveCanonical's nil LIDResolver.
func (d Deps) siblingJIDs(ctx context.Context, jid string) ([]string, error) {
	if d.Store == nil {
		return []string{jid}, nil
	}
	chats, err := d.Store.ListChats(dashboardChatLimit)
	if err != nil {
		return nil, err
	}
	canon := d.resolveCanonical(ctx, jid)
	out := []string{jid}
	for _, c := range chats {
		if c.JID != jid && d.resolveCanonical(ctx, c.JID) == canon {
			out = append(out, c.JID)
		}
	}
	return out, nil
}

// messagesAcrossJIDs fetches up to limit messages merged across every jid in
// jids, newest first (store.GetMessages' own order) — the multi-identity
// case siblingJIDs exists for (see its doc). Each store.Message keeps its
// own ChatJID (already set by GetMessages), so a caller building a
// /api/media URL still points at the jid the message ACTUALLY lives under,
// not the one the request was made with.
func (d Deps) messagesAcrossJIDs(jids []string, limit int) ([]store.Message, error) {
	if len(jids) == 1 {
		return d.Store.GetMessages(jids[0], limit)
	}
	var all []store.Message
	for _, jid := range jids {
		msgs, err := d.Store.GetMessages(jid, limit)
		if err != nil {
			return nil, err
		}
		all = append(all, msgs...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].TS > all[j].TS })
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

// resolveSenderNames maps each distinct non-FromMe Sender in msgs to a
// display name (D3, ct-2026-07-22-2100 — the popup-as-chat's per-bubble
// sender label). Only meaningful for a GROUP chat — a 1:1 chat's every
// inbound message shares the same sender (the chat itself), nothing to
// resolve. Scoped to chatJID's own group_members (ListGroupMembers, ONE
// group — not ListAllGroupMembers' full-table scan) since a group
// message's sender can only be one of THIS group's members. Same name
// hierarchy handleChats' P4b already established for a group_member row:
// contact_name (agenda real) > WhatsApp name > the group scrape's own
// member_name > "" (frontend falls back to jidNumber(sender)).
func (d Deps) resolveSenderNames(ctx context.Context, chatJID string, msgs []store.Message) map[string]string {
	out := map[string]string{}
	if !store.IsGroupJID(chatJID) {
		return out
	}
	members, err := d.Store.ListGroupMembers(chatJID)
	if err != nil {
		return out
	}
	memberNameByJID := map[string]string{}
	for _, m := range members {
		memberNameByJID[m.MemberJID] = m.MemberName
	}
	for _, msg := range msgs {
		if msg.FromMe || msg.Sender == "" {
			continue
		}
		if _, done := out[msg.Sender]; done {
			continue
		}
		name := memberNameByJID[msg.Sender]
		canon := d.resolveCanonical(ctx, msg.Sender)
		if c, ok, err := d.Store.GetChat(canon); err == nil && ok {
			if c.ContactName != "" {
				name = c.ContactName
			} else if c.Name != "" {
				name = c.Name
			}
		}
		out[msg.Sender] = name
	}
	return out
}

// rulesSourceFor labels which tier of the hierarchy governs c when c.Rules
// is empty — same branching as store.EffectiveRules (chat.go), kept as a
// SEPARATE function on purpose rather than calling EffectiveRules once per
// row: EffectiveRules re-fetches the chat and re-reads KV on every call,
// which would mean up to dashboardChatLimit extra DB round-trips per
// GET /api/chats. Here the 3 KV values are read ONCE by the caller and
// passed in; only the branching is shared conceptually, not the I/O. If the
// hierarchy in EffectiveRules ever changes, this must change with it — the
// two are meant to compute the same answer for the same chat.
//
// T79 (ct-2026-08-27-2034) removed the two params this used to take,
// general and the old rules_type_individual trap this doc used to flag —
// both dead, both resolved by removal rather than left open.
func rulesSourceFor(c store.Chat, isGroup bool, typeGroup, originNew, originContact string) string {
	if c.Rules != "" {
		return "particular"
	}
	if isGroup {
		if typeGroup != "" {
			return "tipo:grupo"
		}
		return ""
	}
	isContact := c.ContactName != ""
	originRules := originNew
	if isContact {
		originRules = originContact
	}
	if originRules != "" {
		if isContact {
			return "origen:contacto"
		}
		return "origen:nuevo"
	}
	return ""
}

// handleChats classifies every row into the S1g zones instead of returning
// store.Chat verbatim:
//   - a group (@g.us) is always "group".
//   - every other chat is "p2p" — WITH or WITHOUT a real message (S1g-fix,
//     ct-2026-07-19-1905: the boss saw only is_boss + groups in the list and
//     wants every non-group contact visible; S1g's own noise filter
//     (store.ChatJIDsWithMessages/is_boss gate) is reverted here). The
//     frontend's default "recientes" sort (by last_ts descending) already
//     puts messaged chats first and ts=0 contacts last — no extra field
//     needed to satisfy "activos primero, sin actividad después".
//   - every group_members row becomes a "group_member" pseudo-entry UNLESS
//     its member_jid already qualifies as "p2p" — "gana el 1:1" (contract):
//     someone who both is in a group AND actually messaged 1:1 shows once,
//     in the p2p zone, never duplicated under the group too. A member in
//     several groups gets one group_member row per group (Citrino's call,
//     flagged: showing under every group they're actually in is more
//     complete/honest than picking an arbitrary "first" group).
//
// @lid dedup (ct-2026-07-21-1809, boss: "contactos duplicados con números
// que no corresponden"): a person can have TWO independent `chats` rows —
// one keyed by their real @s.whatsapp.net and one by WhatsApp's internal
// @lid (both classify as "p2p", @lid isn't a group JID) — and/or show up a
// third time as a group_member whose member_jid is the @lid form, which
// "gana el 1:1" never caught because it only ever compared raw JIDs. Both
// are fixed the same way: resolveCanonical maps a @lid to the real number
// ONLY for comparison/display, never for storage.
//
// P4b/P5 (Tramo B, ct-2026-07-22-0436): a group_member row's Name/IsContact
// come from chatByCanon — the chats row for that person's canonical
// identity, when one exists — instead of the raw group-scrape member_name
// (almost always empty) and a raw jid. Same is_contact criterion
// (ContactName != "") also stamps every p2p/group row, so the frontend can
// split the Contactos tab into "Contactos" (agenda real) vs "Números"
// (solo participantes) without re-deriving it client-side.
//
// Group membership alone is NOT a 1:1 conversation or a contact
// (ct-2026-07-29, boss: "no mezclen contactos con miembros de grupos" — his
// real 1:1s are ~4, not 408): a p2p row with no agenda name AND
// store.ChatOrigin == "group_discovered" is skipped entirely here — the
// person still shows once, under their group, via the group_member loop
// below. StatusBroadcastJID (WhatsApp's shared Status/Stories pseudo-chat)
// is skipped outright, same reason — never a conversation/contact/group.
//
// T18/T18B (ct-2026-08-05-1243): this used to be a SEPARATE computation
// (isGroupMemberCanon, built from ListAllGroupMembers/group_members) from
// the one store.ChatOrigin did (chat_groups, a DIFFERENT table with no
// production writer at all — group_discovered could never actually fire).
// T18B retired chat_groups and rewired ChatOrigin onto group_members —
// same table this exclusion already used, so the two answer the same
// question from the same source now. Consolidated onto ChatOrigin here too.

// manuallyDecided (T66b, ct-2026-08-11-1827) reports whether the owner
// explicitly decided something about c — as opposed to c sitting at
// whatever TouchChat/the group scrape defaulted it to. A group-only
// contact (no agenda name, no real message) is filtered out of the main
// list as agenda noise, and that filter stays — but a chat the owner
// actually acted on is a decision, never noise, even for someone he only
// knows from a group.
//
// c.Status is checked directly (via store.AgentExclusiveID, not a
// hand-parsed prefix) rather than relying on ConfigLevelSource alone:
// SetStatus — the write path behind assign_chat_to_agent — deliberately
// does NOT flip ConfigLevelSource to "manual" (see its own doc), so an
// assignment alone would go unnoticed by that check.
func manuallyDecided(c store.Chat) bool {
	if c.IsBoss || c.IsApprover || c.ConfigLevelSource == "manual" {
		return true
	}
	_, ok := store.AgentExclusiveID(c.Status)
	return ok
}

func (d Deps) handleChats(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	chats, err := d.Store.ListChats(dashboardChatLimit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	withMessages, err := d.Store.ChatJIDsWithMessages()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	members, err := d.Store.ListAllGroupMembers()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// The 3 tiers rulesSourceFor needs, read ONCE here rather than per row
	// (its own doc explains why this isn't just EffectiveRules per chat).
	rulesTypeGroup, err := d.Store.KVGet(store.SettingRulesTypeGroup)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	rulesOriginNew, err := d.Store.KVGet(store.SettingRulesDefaultNewNumber)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	rulesOriginContact, err := d.Store.KVGet(store.SettingRulesDefaultContact)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	ctx := r.Context()
	// hasRealRow[canon]: canon itself already appears as its OWN chats row
	// (a @lid row resolving to it doesn't need to be shown too — same
	// person, prefer the real-number row).
	hasRealRow := map[string]bool{}
	// chatByCanon[canon]: the chats row that represents this canonical
	// identity — used below (P4b/P5, ct-2026-07-22-0436) to give a
	// group_member pseudo-row a real name/is_contact instead of "(sin
	// nombre)" + raw jid. A real (non-@lid) row always wins over a @lid
	// alias for the same person.
	chatByCanon := map[string]store.Chat{}
	// canonMessageChat[canon]: among every chats row sharing this canonical
	// identity, the one that actually HOLDS the messages (its own JID is a
	// withMessages hit) — most recent by LastTS if more than one. Needed
	// because the p2p dedup below (hasRealRow) keeps only the real-number
	// row for DISPLAY when both a @lid and a real-number row exist for the
	// same person, but WhatsApp overwhelmingly delivers into the @lid jid
	// (ct-2026-07-29, boss: "quiero ver en chats todos los chats" — measured
	// 405 of 408 messaged p2p chats are @lid). Without this, the winning
	// real-number row's own withMessages[c.JID] is false — its @lid sibling
	// (holding the real conversation) was dropped from the output entirely
	// — so it silently reports has_messages=false and an empty preview.
	canonMessageChat := map[string]store.Chat{}
	for _, c := range chats {
		canon := d.resolveCanonical(ctx, c.JID)
		if canon == c.JID {
			hasRealRow[c.JID] = true
		}
		if existing, ok := chatByCanon[canon]; !ok || (c.JID == canon && existing.JID != canon) {
			chatByCanon[canon] = c
		}
		if withMessages[c.JID] {
			if existing, ok := canonMessageChat[canon]; !ok || c.LastTS > existing.LastTS {
				canonMessageChat[canon] = c
			}
		}
	}
	out := []chatOut{}
	seenCanon := map[string]bool{}
	for _, c := range chats {
		if c.JID == store.StatusBroadcastJID {
			continue // status/story updates, never a conversation/contact/group — see StatusBroadcastJID's doc
		}
		isGroup := store.IsGroupJID(c.JID)
		t := "p2p"
		if isGroup {
			t = "group"
		}
		canon := d.resolveCanonical(ctx, c.JID)
		if t == "p2p" {
			if canon != c.JID && hasRealRow[canon] {
				continue // duplicate: the real-number row for this same person already covers it
			}
			if seenCanon[canon] {
				continue // defensive: two @lid rows that happen to resolve the same way
			}
			seenCanon[canon] = true
		}
		resolvedNumber := ""
		if t == "p2p" && canon != c.JID {
			resolvedNumber = canon
		}
		// hasMessages/lastTS/lastText: prefer this row's OWN message data,
		// but fall back to canonMessageChat[canon] when a deduped-away
		// sibling (see canonMessageChat's doc) is the one actually holding
		// the conversation — same person, so its last message IS this row's
		// last message for display purposes.
		hasMessages := withMessages[c.JID] || withMessages[canon]
		lastTS, lastText := c.LastTS, c.LastText
		if msgChat, ok := canonMessageChat[canon]; ok {
			hasMessages = true
			if msgChat.LastTS > lastTS {
				lastTS, lastText = msgChat.LastTS, msgChat.LastText
			}
		}
		// T18B: rebased onto c.Origin (single source now — see the doc
		// comment above handleChats). !hasMessages dropped: group_discovered
		// can only happen when ChatOrigin already found no real message —
		// checking both was redundant, not a second safety net.
		if t == "p2p" && c.ContactName == "" && c.Origin == "group_discovered" {
			continue
		}
		out = append(out, chatOut{
			// nil sender (T132, ct-2026-09-03-0624): this is a chat LISTING,
			// no specific speaker in context — the chat's own level, same as
			// before that contract.
			JID: c.JID, Name: c.Name, Level: capipush.LevelFor(c, nil), Mode: c.Mode,
			IsBoss: c.IsBoss, IsApprover: c.IsApprover, ConfirmationMode: c.ConfirmationMode, Status: c.Status,
			ConfigLevel: store.ConfigLevel(c),
			LastTS:      lastTS, Rules: c.Rules, Memory: c.Memory, Context: c.Context,
			ContactName: c.ContactName, LastText: lastText,
			Type: t, HasMessages: hasMessages, Origin: c.Origin,
			HistoryState:   c.HistoryState,
			ResolvedNumber: resolvedNumber,
			IsContact:      c.ContactName != "",
			RulesSource:    rulesSourceFor(c, isGroup, rulesTypeGroup, rulesOriginNew, rulesOriginContact),
		})
	}
	for _, m := range members {
		canon := d.resolveCanonical(ctx, m.MemberJID)
		if withMessages[m.MemberJID] || withMessages[canon] {
			continue
		}
		resolvedNumber := ""
		if canon != m.MemberJID {
			resolvedNumber = canon
		}
		row := chatOut{
			JID: m.MemberJID, Name: m.MemberName, LastTS: m.AddedTS,
			Type: "group_member", GroupJID: m.GroupJID,
			ResolvedNumber: resolvedNumber,
		}
		// P4b: cruzá el miembro contra su chats row (por identidad
		// canónica) — si esa persona tiene contact_name o name conocido
		// ahí, usalo en vez del member_name crudo del scrape de grupo (que
		// casi siempre viene vacío, ver UpsertGroupMember's doc).
		// contact_name (agenda real) gana sobre name (nombre de WhatsApp);
		// si no hay ninguno, se mantiene el member_name existente (o "").
		if info, ok := chatByCanon[canon]; ok {
			if info.ContactName != "" {
				row.Name = info.ContactName
				row.IsContact = true
			} else if info.Name != "" {
				row.Name = info.Name
			}
			// T66b (ct-2026-08-11-1827): a group-only contact the owner
			// decided something about — assigned an agent, pinned boss/
			// approver, or otherwise touched their config level by hand —
			// isn't agenda noise anymore, even with no contact_name. The
			// filter above that hides an unremarkable group-only contact
			// stays untouched (the owner's own request); this only
			// enriches the row THAT filter already lets through as a
			// bare group_member, the same way "gana el 1:1" already
			// enriches a member who turns out to have real messages.
			if manuallyDecided(info) {
				row.Status = info.Status
				row.IsBoss = info.IsBoss
				row.IsApprover = info.IsApprover
				row.Level = capipush.LevelFor(info, nil)
				row.Mode = info.Mode
			}
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
}

// messageMediaOut is messageOut's media sub-shape (ct-2026-07-21-1358, popup
// backend A) — only present when the message's type is a media MIME
// (store.MediaKind). URL is set ONLY once Downloaded is true (points at the
// existing GET /api/media?chat=&msg= that already serves the bytes) — the
// popup has nothing to fetch otherwise, matching the same no-placeholder
// discipline the e-paper renderer uses.
//
// Failed (ct-2026-07-29, boss: "los audios dicen descargando... un adjunto
// que falló con 403 no puede decir 'descargando' para siempre") — true when
// store.MediaPendingFailedMsgIDs says this row exhausted
// store.MaxMediaPendingAttempts: WhatsApp's CDN copy is gone for good (or
// every generic retry failed) and NextMediaPending/MediaPendingForChat will
// never pick it up again. Distinct from plain !Downloaded (still queued,
// genuinely might arrive) — the frontend needs both states to stop showing
// "descargando…" for something that's never coming.
type messageMediaOut struct {
	Mime       string `json:"mime"`
	Kind       string `json:"kind"` // photo|audio|sticker|video|doc — store.MediaKind
	Downloaded bool   `json:"downloaded"`
	Failed     bool   `json:"failed,omitempty"`
	URL        string `json:"url,omitempty"`
}

// messageOut is GET /api/messages' per-message shape — read-only chat log
// for the dashboard's panel (ct-2026-07-10-2312, F2v3 addendum). Sending
// from the dashboard is explicitly post-MVP, not this endpoint's job.
type messageOut struct {
	ID     string           `json:"id"`
	FromMe bool             `json:"from_me"`
	Text   string           `json:"text"`
	TS     int64            `json:"ts"`
	Media  *messageMediaOut `json:"media,omitempty"`
	// QuotedPreview/Forwarded (ct-2026-07-21-1610, S6a): reply/forward
	// metadata captured at ingestion — store.Message's own fields verbatim.
	// QuotedPreview omitted entirely when empty (not a reply); Forwarded only
	// ever appears as true (omitempty on a bool drops the common false case).
	QuotedPreview string `json:"quoted_preview,omitempty"`
	Forwarded     bool   `json:"forwarded,omitempty"`
	// Sender/SenderName (D3, ct-2026-07-22-2100): who sent this message —
	// only set for a GROUP chat's inbound message (store.Message.Sender was
	// already persisted for every message, chat.go/history.go; this is the
	// missing exposure). Sender is the raw JID, SenderName the resolved
	// display name (same hierarchy as GET /api/chats' group_member rows —
	// contact_name > WhatsApp name > the group scrape's own member_name —
	// see resolveSenderNames). Both omitted for a 1:1 chat or an outbound
	// message (from_me — the popup already knows who "us" is).
	Sender     string `json:"sender,omitempty"`
	SenderName string `json:"sender_name,omitempty"`
	// DecryptRetryAt (T35, ct-2026-08-08-1258): unix ts of WhatsApp's last
	// "no se pudo descifrar" retry receipt for this outbound message — 0/
	// omitted when none arrived. store.Message.DecryptRetryAt verbatim; the
	// visual indicator (icon/color) is the boss's call, not this endpoint's.
	DecryptRetryAt int64 `json:"decrypt_retry_at,omitempty"`
}

// handleMessages returns a chat's recent messages, oldest first (a chat log
// reads top-to-bottom-oldest-to-newest; store.GetMessages itself returns
// newest-first, reversed here so the dashboard never has to). Media
// (ct-2026-07-21-1358): one bulk MediaMsgIDs query per sibling jid (same
// "one query, not N" convention as handleChats' withMessages), not a
// per-message GetMedia call.
//
// siblingJIDs (ct-2026-07-29): the requested chat may be the dedup's
// winning real-number row while the actual conversation lives under a
// deduped-away @lid — merge every sibling's messages/media into one
// timeline instead of querying only the jid the request named. Each
// message keeps its OWN ChatJID for the /api/media URL, since a message
// merged in from a sibling isn't stored under the requested jid.
func (d Deps) handleMessages(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	chatJID := r.URL.Query().Get("chat")
	if chatJID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "chat is required"})
		return
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	ctx := r.Context()
	siblings, err := d.siblingJIDs(ctx, chatJID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	msgs, err := d.messagesAcrossJIDs(siblings, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	downloadedIDs := map[string]bool{}
	failedIDs := map[string]bool{}
	for _, jid := range siblings {
		ids, err := d.Store.MediaMsgIDs(jid)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		for id := range ids {
			downloadedIDs[id] = true
		}
		failed, err := d.Store.MediaPendingFailedMsgIDs(jid)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		for id := range failed {
			failedIDs[id] = true
		}
	}
	isGroup := store.IsGroupJID(chatJID)
	senderNames := d.resolveSenderNames(ctx, chatJID, msgs)
	out := make([]messageOut, len(msgs))
	for i, m := range msgs {
		mo := messageOut{
			ID: m.ID, FromMe: m.FromMe, Text: m.Text, TS: m.TS,
			QuotedPreview: m.QuotedPreview, Forwarded: m.Forwarded,
			DecryptRetryAt: m.DecryptRetryAt,
		}
		if isGroup && !m.FromMe && m.Sender != "" {
			mo.Sender = m.Sender
			mo.SenderName = senderNames[m.Sender]
		}
		if kind, ok := store.MediaKind(m.Type); ok {
			downloaded := downloadedIDs[m.ID]
			media := &messageMediaOut{Mime: m.Type, Kind: kind, Downloaded: downloaded, Failed: !downloaded && failedIDs[m.ID]}
			if downloaded {
				media.URL = "/api/media?chat=" + url.QueryEscape(m.ChatJID) + "&msg=" + url.QueryEscape(m.ID)
			}
			mo.Media = media
		}
		out[len(msgs)-1-i] = mo
	}
	writeJSON(w, http.StatusOK, out)
}

// agentOut is GET /api/agents' per-agent shape (M1, ct-2026-07-22-1301) —
// pinpass NEVER included in clear, only whether it's set (same convention
// as GET /api/admin/capi-connector's own handleGetCAPIConnector).
type agentOut struct {
	AgentID           string `json:"agent_id"`
	Name              string `json:"name"`
	Role              string `json:"role"` // "principal" | "secondary"
	Endpoint          string `json:"endpoint"`
	AntennaTerminalID string `json:"antenna_terminal_id"`
	PinpassSet        bool   `json:"pinpass_set"`
}

// handleAgents lists the principal followed by every registered secondary —
// the `agents` table never holds a row for the principal (register_agent/
// set_agent_capi explicitly reject PrincipalTerminalID, see agent_tools.go),
// so its row is SYNTHESIZED via store.PrincipalAgent (ct-2026-07-29, agentes
// paso 3 — factored out so restapi and the list_agents MCP tool read the
// principal the exact same way instead of each re-reading the 4 KV keys
// inline). PrincipalTerminalID unset -> principal omitted rather than shown
// with a blank identity.
func (d Deps) handleAgents(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	out := []agentOut{}
	if principal, ok, err := d.Store.PrincipalAgent(d.PrincipalTerminalID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	} else if ok {
		out = append(out, agentOut{
			AgentID: principal.AgentID, Name: principal.Name, Role: principal.Role,
			Endpoint: principal.Endpoint, AntennaTerminalID: principal.AntennaTerminalID,
			PinpassSet: principal.Pinpass != "",
		})
	}
	agents, err := d.Store.ListAgents()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	for _, a := range agents {
		out = append(out, agentOut{
			AgentID: a.AgentID, Name: a.Name, Role: a.Role,
			Endpoint: a.Endpoint, AntennaTerminalID: a.AntennaTerminalID, PinpassSet: a.Pinpass != "",
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// agentChatOut is GET /api/agents/chats' per-chat shape (M3, ct-2026-07-22-1301)
// — just enough to label a manually-assigned number in the agent card;
// rules/memory/context/level don't apply to this listing, unlike chatOut.
type agentChatOut struct {
	JID         string `json:"jid"`
	Name        string `json:"name"`
	ContactName string `json:"contact_name,omitempty"`
}

// handleAgentChats lists the numbers manually assigned to agent_id
// (store.ChatsForAgent — chats.status == agent_exclusive:<agent_id>) — the
// agent card's "números asignados" section.
func (d Deps) handleAgentChats(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	agentID := r.URL.Query().Get("agent_id")
	if agentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id is required"})
		return
	}
	chats, err := d.Store.ChatsForAgent(agentID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]agentChatOut, len(chats))
	for i, c := range chats {
		out[i] = agentChatOut{JID: c.JID, Name: c.Name, ContactName: c.ContactName}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleQR reports the current pairing QR, if any is showing — main.go's
// QR loop writes show_qr/qr_data into state as codes arrive (whatsmeow's
// pairing round). No POST /api/qr/refresh yet: forcing a fresh round needs
// a new Gateway capability (disconnect-and-retry), flagged to Citrino
// rather than extending that interface unasked.
func (d Deps) handleQR(w http.ResponseWriter, r *http.Request) {
	if d.State == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "state not available"})
		return
	}
	snap := d.State.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"show_qr": snap.ShowQR,
		"qr_data": snap.QRData,
	})
}
