// Privileged DB-admin + draft-approval endpoints (F4-DESIGN §3, F4c) —
// same store methods the boss-only MCP tools call (mcpserver/admin_tools.go),
// exposed here for the owner to administer directly from the LAN without
// going through an agent at all.
package restapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"piumy-gateway/internal/capiconn"
	"piumy-gateway/internal/eventbus"
	"piumy-gateway/internal/i18n"
	"piumy-gateway/internal/mediautil"
	"piumy-gateway/internal/router"
	"piumy-gateway/internal/store"
)

// publishDraftChanged nudges the dashboard's SSE auto-refresh whenever a
// draft is resolved from the dashboard itself (T16, ct-2026-08-05-123257) —
// JID-less like wa_connected/history_batch: the dashboard just refetches
// the whole pending-drafts list. Mirrors mcpserver's own helper of the same
// name (duplicated on purpose — the two packages don't share internals).
func publishDraftChanged(bus *eventbus.Bus) {
	bus.Publish(eventbus.Event{Type: "draft", TS: time.Now().Unix()})
}

// markHandledForDispatch (T108, ct-2026-09-01-1413) mirrors mcpserver's own
// helper of the same name (duplicated on purpose, same reasoning as
// publishDraftChanged above — the two packages don't share internals):
// scopes the close to sender via MarkHandledBeforeForSender when chatJID is
// a GROUP and sender is known; otherwise falls back to the whole-chat
// MarkHandledBefore. handleApproveDraft is the one restapi caller — a group
// chat is born confirmation_mode="always" (chat.go's TouchChat), so the
// dashboard's "aprobar" button is the NORMAL approval path there, not an
// edge case.
func markHandledForDispatch(st *store.Store, chatJID, sender string, ts int64) error {
	if sender != "" && store.IsGroupJID(chatJID) {
		return st.MarkHandledBeforeForSender(chatJID, sender, ts)
	}
	return st.MarkHandledBefore(chatJID, ts)
}

func (d Deps) registerAdminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/admin/chat-rules", d.auth(d.handleSetChatRules))
	mux.HandleFunc("POST /api/admin/is-boss", d.auth(d.handleSetIsBoss))
	mux.HandleFunc("POST /api/admin/approver", d.auth(d.handleSetIsApprover))
	mux.HandleFunc("POST /api/admin/type-rules", d.auth(d.handleSetTypeRules))
	// GET (ct-2026-07-31, "las reglas por defecto no se ven"): existía solo
	// como POST — sin lectura no había forma de mostrar en el tablero lo que
	// ya estaba guardado. Mismo patrón que rules-default-new-number/
	// rules-default-contact (abajo). default-rules (GET y POST) se fue en
	// T79 (ct-2026-08-27-2034) junto con el tier entero.
	mux.HandleFunc("GET /api/admin/type-rules", d.auth(d.handleGetTypeRules))
	mux.HandleFunc("POST /api/admin/confirmation-mode", d.auth(d.handleSetConfirmationMode))
	mux.HandleFunc("POST /api/admin/config-level", d.auth(d.handleSetConfigLevel))
	mux.HandleFunc("POST /api/admin/approve-draft", d.auth(d.handleApproveDraft))
	mux.HandleFunc("POST /api/admin/discard-draft", d.auth(d.handleDiscardDraft))
	// T15 (ct-2026-08-05-123241): reject asks for another attempt (reason
	// travels back via capipush's redispatch, up to store.MaxDraftRounds
	// rounds); edit replaces a pending draft's text without approving it.
	mux.HandleFunc("POST /api/admin/reject-draft", d.auth(d.handleRejectDraft))
	mux.HandleFunc("POST /api/admin/edit-draft", d.auth(d.handleEditDraft))
	mux.HandleFunc("POST /api/admin/kill", d.auth(d.handleSetKillSwitch))

	// Dashboard-driven additions (ct-2026-07-10-2312): the config zone's
	// remaining editable fields (mode/memory/context) + adding a number to
	// the router's whitelist.
	mux.HandleFunc("POST /api/admin/mode", d.auth(d.handleSetMode))
	mux.HandleFunc("POST /api/admin/memory", d.auth(d.handleSetMemory))
	mux.HandleFunc("POST /api/admin/context", d.auth(d.handleSetContext))
	mux.HandleFunc("POST /api/admin/whitelist-add", d.auth(d.handleWhitelistAdd))
	mux.HandleFunc("POST /api/admin/ignore", d.auth(d.handleSetIgnored))

	// Tramo C (ct-2026-07-22-1235): editar el nombre de contacto desde el
	// dashboard (Store.SetContactName ya existía, sin endpoint) + exponer las
	// confirmaciones pendientes (Store.PendingDrafts, ídem — approve/discard
	// ya tenían endpoint arriba, sin nada que los llamara todavía).
	mux.HandleFunc("POST /api/admin/contact-name", d.auth(d.handleSetContactName))
	mux.HandleFunc("GET /api/admin/pending-drafts", d.auth(d.handleGetPendingDrafts))

	// M3 (ct-2026-07-22-1301): manual per-number agent assignment, from the
	// pestaña Agentes.
	mux.HandleFunc("POST /api/admin/agent-assign", d.auth(d.handleAssignChatToAgent))

	// Agentes paso 1 (ct-2026-07-29): create/update/delete an agent from the
	// dashboard — register_agent/set_agent_capi (MCP) already covered
	// secondaries; these are the REST-side equivalent, PLUS the principal
	// (never had a write path outside the antena modal). One endpoint
	// pattern for either kind of agent — see handleUpdateAgent's doc.
	mux.HandleFunc("POST /api/admin/agent-create", d.auth(d.handleCreateAgent))
	mux.HandleFunc("POST /api/admin/agent-update", d.auth(d.handleUpdateAgent))
	mux.HandleFunc("POST /api/admin/agent-delete", d.auth(d.handleDeleteAgent))
	// T76 (ct-2026-08-27-1752): rol swap desde la ficha — un secundario pasa
	// a principal, el que era principal conserva sus datos como secundario.
	mux.HandleFunc("POST /api/admin/agent-promote", d.auth(d.handlePromoteAgent))

	// M5 (ct-2026-07-22-1903): defaults de atención por origen — 2 pares
	// (modo + reglas) en la cabecera, uno para números desconocidos y otro
	// para contactos de la agenda.
	mux.HandleFunc("GET /api/admin/config-level-default-new", d.auth(d.handleGetConfigLevelDefaultNew))
	mux.HandleFunc("POST /api/admin/config-level-default-new", d.auth(d.handleSetConfigLevelDefaultNew))
	mux.HandleFunc("GET /api/admin/config-level-default-contact", d.auth(d.handleGetConfigLevelDefaultContact))
	mux.HandleFunc("POST /api/admin/config-level-default-contact", d.auth(d.handleSetConfigLevelDefaultContact))
	mux.HandleFunc("GET /api/admin/rules-default-new-number", d.auth(d.handleGetRulesDefaultNewNumber))
	mux.HandleFunc("POST /api/admin/rules-default-new-number", d.auth(d.handleSetRulesDefaultNewNumber))
	mux.HandleFunc("GET /api/admin/rules-default-contact", d.auth(d.handleGetRulesDefaultContact))
	mux.HandleFunc("POST /api/admin/rules-default-contact", d.auth(d.handleSetRulesDefaultContact))

	// T71 (ct-2026-08-27-1410): tercer par junto a modo/reglas por origen —
	// agente por defecto para ese tipo de chat (ruteo, ver
	// store.EffectiveAgentDefault).
	mux.HandleFunc("GET /api/admin/agent-default-new-number", d.auth(d.handleGetAgentDefault(store.SettingAgentDefaultNewNumber)))
	mux.HandleFunc("POST /api/admin/agent-default-new-number", d.auth(d.handleSetAgentDefault(store.SettingAgentDefaultNewNumber)))
	mux.HandleFunc("GET /api/admin/agent-default-contact", d.auth(d.handleGetAgentDefault(store.SettingAgentDefaultContact)))
	mux.HandleFunc("POST /api/admin/agent-default-contact", d.auth(d.handleSetAgentDefault(store.SettingAgentDefaultContact)))
	// T72 (ct-2026-08-27-1625): dos tipos más en la misma jerarquía — grupos
	// y el chat del dueño.
	mux.HandleFunc("GET /api/admin/agent-default-group", d.auth(d.handleGetAgentDefault(store.SettingAgentDefaultGroup)))
	mux.HandleFunc("POST /api/admin/agent-default-group", d.auth(d.handleSetAgentDefault(store.SettingAgentDefaultGroup)))
	mux.HandleFunc("GET /api/admin/agent-default-boss", d.auth(d.handleGetAgentDefault(store.SettingAgentDefaultBoss)))
	mux.HandleFunc("POST /api/admin/agent-default-boss", d.auth(d.handleSetAgentDefault(store.SettingAgentDefaultBoss)))

	// T13 (ct-2026-08-05-123147): pestaña Rules — el campo identity ("asistente
	// de qué"), mismo patrón CRUD que los 4 de arriba.
	mux.HandleFunc("GET /api/admin/identity", d.auth(d.handleGetIdentity))
	mux.HandleFunc("POST /api/admin/identity", d.auth(d.handleSetIdentity))

	// D4 (ct-2026-07-22-2100): "partir de 0" — reset selectivo, checkpointed
	// con Citrino (lista exacta de tablas + excepción de usage, anti-ban).
	mux.HandleFunc("POST /api/admin/reset", d.auth(d.handleReset))

	// M3 (ct-2026-07-22-2342): "Desconectar" — unlinks the current WhatsApp
	// session so the boss can re-pair a fresh QR (M1's full-sync flags only
	// apply to a NEW pairing).
	mux.HandleFunc("POST /api/admin/disconnect", d.auth(d.handleDisconnect))

	// Fix 2 (ct-2026-07-23-0047): "Reconectar" — kicks off a new QR
	// pairing flow after a Logout, without restarting the process. The
	// dashboard's "Ver QR / Reconectar" button calls this.
	mux.HandleFunc("POST /api/admin/reconnect", d.auth(d.handleReconnect))

	// cAPI connector — editable from the dashboard at runtime.
	mux.HandleFunc("GET /api/admin/capi-connector", d.auth(d.handleGetCAPIConnector))
	mux.HandleFunc("POST /api/admin/capi-connector", d.auth(d.handleSetCAPIConnector))
	mux.HandleFunc("POST /api/admin/capi-connector/test", d.auth(d.handleTestCAPIConnector))
	mux.HandleFunc("POST /api/admin/capi-connector-line", d.auth(d.handleSetCAPIConnectorLine))
	mux.HandleFunc("POST /api/admin/capi-ping", d.auth(d.handleCAPIPing))

	// Recovery email (ct-2026-07-19-1716, S1e-2) — the boss's own address
	// for the email channel of password recovery (recover.go).
	mux.HandleFunc("GET /api/admin/recovery-email", d.auth(d.handleGetRecoveryEmail))
	mux.HandleFunc("POST /api/admin/recovery-email", d.auth(d.handleSetRecoveryEmail))

	// Dispatch debounce (T90, ct-2026-08-28-1350) — the entry-side wait
	// before capipush hands a burst to the agent, movable from the tablero
	// without a restart.
	mux.HandleFunc("GET /api/admin/dispatch-debounce", d.auth(d.handleGetDispatchDebounce))
	mux.HandleFunc("POST /api/admin/dispatch-debounce", d.auth(d.handleSetDispatchDebounce))

	// Estado/"About" de WhatsApp (T92, ct-2026-08-28 — el mismo
	// GroupProfile.SetProfileStatus que set_profile_status (MCP) ya usa,
	// solo un segundo entry point; T96, ct-2026-08-28-1743 — la mitad que
	// faltó, leerlo: para pre-llenar el campo y mostrarlo bajo el nombre.
	// Ya no es write-only — mismo patrón que dispatch-debounce arriba,
	// GET+POST.
	mux.HandleFunc("GET /api/admin/profile-status", d.auth(d.handleGetProfileStatus))
	mux.HandleFunc("POST /api/admin/profile-status", d.auth(d.handleSetProfileStatus))

	// Foto de perfil de WhatsApp (T112c, ct-2026-09-01-145755) — mismo
	// GroupProfile.SetProfilePhoto que set_profile_photo (MCP, T111) ya usa,
	// solo un segundo entry point. No hay GET: el avatar propio ya se lee
	// por GET /api/avatar?jid=... (T17), no hace falta un segundo camino de
	// lectura para el mismo dato.
	mux.HandleFunc("POST /api/admin/profile-photo", d.auth(d.handleSetProfilePhoto))

	// On-demand media FIFO backfill (ct-2026-07-21-1358, popup backend A) —
	// the popup calls this on opening a chat; handler lives in
	// media_fetch.go (same file-per-resource split as GET /api/media's own
	// handler, media_read.go).
	mux.HandleFunc("POST /api/media/fetch", d.auth(d.handleFetchMedia))
}

func (d Deps) handleSetChatRules(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		ChatID string `json:"chat_id"`
		Rules  string `json:"rules"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := d.Store.SetChatRules(body.ChatID, body.Rules); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "rules set"})
}

// handleSetContactName is the REST equivalent of Store.SetContactName — the
// dashboard's "guardar/modificar contacto" (Tramo C, ct-2026-07-22-1235).
// Same trust level as chat-rules/memory/context: any dashboard session can
// call it, no separate boss-only gate (contact_name was already agent- and
// admin-writable via the backup backfill, this just adds a manual override).
func (d Deps) handleSetContactName(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		ChatID string `json:"chat_id"`
		Name   string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := d.Store.SetContactName(body.ChatID, body.Name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "contact name set"})
}

// handleGetProfileStatus is T96's read half (ct-2026-08-28-1743, "la mitad
// que faltó de T92") — reads the current status LIVE from WhatsApp
// (ProfileStatus.GetProfileStatus, whatsmeow.Adapter -> GetUserInfo).
// Backs both the hero's status line and #configmodal's pre-filled edit
// field (same GET, two consumers — no second call). A read failure (no
// connection, timeout) is reported as a real error response here; it's the
// DASHBOARD's job (app.js's own .catch) to treat that exactly like an
// empty status — this field is decorative ("un adorno, no un dato
// crítico", Citrino), never worth a broken hero or a crude error banner.
func (d Deps) handleGetProfileStatus(w http.ResponseWriter, r *http.Request) {
	if d.ProfileStatus == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "whatsapp not available"})
		return
	}
	status, err := d.ProfileStatus.GetProfileStatus(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}

// handleSetProfileStatus is the REST equivalent of the boss-only MCP tool
// set_profile_status (T92, ct-2026-08-28) — the dashboard's "Estado
// (WhatsApp)" field in #configmodal. Same underlying call
// (GroupProfile.SetProfileStatus, whatsmeow.Adapter.SetProfileStatus →
// SetStatusMessage) as the MCP tool, just a second entry point — no second
// implementation to drift out of sync with the first.
func (d Deps) handleSetProfileStatus(w http.ResponseWriter, r *http.Request) {
	if d.ProfileStatus == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "whatsapp not available"})
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := d.ProfileStatus.SetProfileStatus(r.Context(), body.Status); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "profile status set"})
}

// maxProfilePhotoBodyBytes bounds the POST body BEFORE anything is read —
// unlike checkPhotoSize (whatsmeow_catalog.go), which only runs after the
// body was already fully buffered, base64-decoded, and image-decoded
// (Citrino's finding, T112c amend): a request this size never gets that
// far. The dashboard's file picker has no format filter, so this is an
// accident, not an attack — the owner picking a video instead of a photo
// mid-way through handling WhatsApp traffic is enough to take the whole
// gateway down on memory alone. 24 MiB of ORIGINAL file, times ~4/3 for
// base64 (Citrino's own math) ≈ 32 MiB of body — generous for any real
// camera photo, decisive against a video or an unreasonable RAW.
const maxProfilePhotoBodyBytes = 32 << 20 // 32 MiB

// handleSetProfilePhoto is the REST equivalent of the boss-only MCP tool
// set_profile_photo (T111, ct-2026-09-01-1442) — the dashboard's hero
// "Editar" modal (T112c). Same decode-and-convert path as the MCP tool
// (mediautil.DecodeDataURL -> mediautil.EnsureJPEG) so a non-JPEG upload
// from the browser's file picker gets the same automatic conversion, and
// the same underlying call (GroupProfile.SetProfilePhoto,
// whatsmeow.Adapter.SetProfilePhoto) — no second implementation to drift
// out of sync with the first.
func (d Deps) handleSetProfilePhoto(w http.ResponseWriter, r *http.Request) {
	if d.ProfilePhoto == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "whatsapp not available"})
		return
	}
	// http.MaxBytesReader BEFORE the JSON decoder touches the body — not
	// the shared decode() helper, which would report this the same as any
	// malformed JSON. An oversized upload gets its own message naming what
	// actually happened, not a generic parse error.
	r.Body = http.MaxBytesReader(w, r.Body, maxProfilePhotoBodyBytes)
	var body struct {
		DataURL string `json:"data_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": i18n.T(effectiveLang(d.Store), "server.image_too_large")})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if body.DataURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "data_url is required"})
		return
	}
	raw, _, err := mediautil.DecodeDataURL(body.DataURL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(effectiveLang(d.Store), "server.invalid_data_url", "detail", err.Error())})
		return
	}
	jpegBytes, err := mediautil.EnsureJPEG(raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(effectiveLang(d.Store), "server.invalid_data_url", "detail", err.Error())})
		return
	}
	if _, err := d.ProfilePhoto.SetProfilePhoto(r.Context(), jpegBytes); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	invalidateOwnAvatarCache(d)
	writeJSON(w, http.StatusOK, map[string]string{"status": "profile photo set"})
}

// invalidateOwnAvatarCache (T116, ct-2026-09-01-2040) forces the NEXT real
// avatar check for the host's own jid, instead of waiting out whatever's
// left of its recheck window (avatar.go's defaultOwnAvatarRecheckMin/Max —
// short, but not zero). Without this, a photo change made FROM the
// dashboard itself would still serve the stale cached file until that
// window naturally elapses: the hero already repaints with the bytes the
// browser just uploaded (T112c), which hides the symptom on THIS screen,
// but the server-side cache stays wrong until the next real check. Best-
// effort — a failure here doesn't undo the photo change that already
// succeeded, it only means the cache clears itself on the regular schedule
// instead of immediately.
func invalidateOwnAvatarCache(d Deps) {
	if d.State == nil || d.Store == nil {
		return
	}
	ownJID := d.State.Snapshot().OwnJID
	if ownJID == "" {
		return
	}
	existing, _, err := d.Store.GetAvatar(ownJID)
	if err != nil {
		log.Printf("restapi: invalidate own avatar cache: read %s: %v", ownJID, err)
		return
	}
	existing.JID = ownJID
	existing.NextCheckAt = 0
	if err := d.Store.UpsertAvatar(existing); err != nil {
		log.Printf("restapi: invalidate own avatar cache: write %s: %v", ownJID, err)
	}
}

func (d Deps) handleSetIsBoss(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		ChatID string `json:"chat_id"`
		IsBoss bool   `json:"is_boss"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := d.Store.SetIsBoss(body.ChatID, body.IsBoss); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "is_boss set"})
}

// handleSetIsApprover (Aprobador P1, ct-2026-07-31-0610) — same shape as
// handleSetIsBoss: the privileged dashboard path, guarded only by the
// dashboard's own session (d.auth), same as every other admin endpoint here.
func (d Deps) handleSetIsApprover(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		ChatID     string `json:"chat_id"`
		IsApprover bool   `json:"is_approver"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := d.Store.SetIsApprover(body.ChatID, body.IsApprover); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "is_approver set"})
}

// handleGetTypeRules reads back the "by type" rules tier — group-only since
// T79 (ct-2026-08-27-2034) removed rules_type_individual (dead since M5,
// see rulesSourceFor's old doc, read.go — the trap it flagged is resolved
// now, not left open) and the global default tier entirely.
func (d Deps) handleGetTypeRules(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	rules, err := d.Store.KVGet(store.SettingRulesTypeGroup)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"rules": rules})
}

func (d Deps) handleSetTypeRules(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		Rules string `json:"rules"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := d.Store.SetTypeRules(body.Rules); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "type rules set"})
}

// validConfirmationMode mirrors the enum the MCP tool's schema already
// enforces (mcpserver/admin_tools.go's set_confirmation_mode,
// mcp.Enum("none","discretion","always")) — found missing here in the F4c
// audit: without it, this endpoint would happily persist "required" (the
// deprecated legacy value, chat.go) or any typo, which send_message's gate
// silently never matches (the same class of bug the HIGH finding fixed).
func validConfirmationMode(mode string) bool {
	switch mode {
	case "none", "discretion", "always":
		return true
	}
	return false
}

func (d Deps) handleSetConfirmationMode(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		ChatID string `json:"chat_id"`
		Mode   string `json:"mode"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !validConfirmationMode(body.Mode) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mode must be none|discretion|always"})
		return
	}
	if err := d.Store.SetConfirmationMode(body.ChatID, body.Mode); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "confirmation_mode set"})
}

// validConfigLevel mirrors the set_config_level MCP tool's schema enum
// (mcpserver/admin_tools.go, mcp.Enum("boss","auto","confirm","unattended",
// "ignored")) — same reasoning as validConfirmationMode/validMode above:
// reject a typo outright rather than let store.SetConfigLevel's own error
// be the only thing catching it.
func validConfigLevel(level string) bool {
	switch level {
	case "boss", "auto", "confirm", "unattended", "ignored":
		return true
	}
	return false
}

// handleSetConfigLevel is the REST equivalent of the set_config_level MCP
// tool — the translation layer over is_boss/active/confirmation_mode/status
// (store.SetConfigLevel), same write path, direct owner admin from the LAN.
func (d Deps) handleSetConfigLevel(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		ChatID string `json:"chat_id"`
		Level  string `json:"level"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !validConfigLevel(body.Level) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "level must be boss|auto|confirm|unattended|ignored"})
		return
	}
	if err := d.Store.SetConfigLevel(body.ChatID, body.Level); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "config_level set"})
}

func (d Deps) handleApproveDraft(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		ID           int64  `json:"id"`
		TextOverride string `json:"text_override"`
	}
	if !decode(w, r, &body) {
		return
	}
	now := time.Now().Unix()
	chatJID, burstMaxTS, sender, ok, err := d.Store.ApproveDraft(body.ID, body.TextOverride, now)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": i18n.T(effectiveLang(d.Store), "server.draft_not_found_resolved")})
		return
	}
	// ct-2026-07-13-2243: mark only the burst that was dispatched.
	// burstMaxTS==0 means pre-ct-2243 draft → fall back to now.
	markTS := burstMaxTS
	if markTS == 0 {
		markTS = now
	}
	// T108 (ct-2026-09-01-1413): scoped to the draft's own sender in a group
	// — see markHandledForDispatch's own doc.
	if err := markHandledForDispatch(d.Store, chatJID, sender, markTS); err != nil {
		log.Printf("restapi: approve_draft: mark_handled_before %s: %v", chatJID, err)
	}
	publishDraftChanged(d.Bus)
	writeJSON(w, http.StatusOK, map[string]string{"status": "approved, moved to outbox"})
}

func (d Deps) handleDiscardDraft(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		ID int64 `json:"id"`
	}
	if !decode(w, r, &body) {
		return
	}
	ok, err := d.Store.DiscardDraft(body.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": i18n.T(effectiveLang(d.Store), "server.draft_not_found_resolved")})
		return
	}
	publishDraftChanged(d.Bus)
	writeJSON(w, http.StatusOK, map[string]string{"status": "discarded"})
}

// handleRejectDraft is T15 (ct-2026-08-05-123241) — unlike discard, this
// asks for another attempt: the reason is recorded on the draft
// (store.RejectDraft) and, unless the rejected round already hit
// store.MaxDraftRounds, the triggering messages are reopened
// (store.MarkPendingBefore) so the next capipush sweep redispatches the
// chat with the reason attached (capipush.dispatchPayload reads it back).
func (d Deps) handleRejectDraft(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		ID     int64  `json:"id"`
		Reason string `json:"reason"`
	}
	if !decode(w, r, &body) {
		return
	}
	chatJID, burstMaxTS, round, ok, err := d.Store.RejectDraft(body.ID, body.Reason)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": i18n.T(effectiveLang(d.Store), "server.draft_not_found_resolved")})
		return
	}
	publishDraftChanged(d.Bus)
	if round >= store.MaxDraftRounds {
		writeJSON(w, http.StatusOK, map[string]string{"status": "rejected — round cap reached, no automatic redispatch"})
		return
	}
	if err := d.Store.MarkPendingBefore(chatJID, burstMaxTS); err != nil {
		log.Printf("restapi: reject_draft: mark_pending_before %s: %v", chatJID, err)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "rejected — redispatched for another attempt"})
}

// handleEditDraft is T15 (ct-2026-08-05-123241) — "editar sin aprobar":
// replaces a pending draft's text in place, no status change, still needs
// approve-draft afterward.
func (d Deps) handleEditDraft(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		ID   int64  `json:"id"`
		Text string `json:"text"`
	}
	if !decode(w, r, &body) {
		return
	}
	ok, err := d.Store.EditDraft(body.ID, body.Text)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": i18n.T(effectiveLang(d.Store), "server.draft_not_found_pending")})
		return
	}
	publishDraftChanged(d.Bus)
	writeJSON(w, http.StatusOK, map[string]string{"status": "edited"})
}

// dashboardPendingDraftsLimit bounds GET /api/admin/pending-drafts — same
// "real cap, not literal unlimited" convention as dashboardChatLimit
// (read.go). A footer list has no reason to show more than this many at
// once; PendingDrafts itself already orders oldest-first (FIFO).
const dashboardPendingDraftsLimit = 100

// handleGetPendingDrafts is the REST equivalent of Store.PendingDrafts — the
// dashboard footer's "confirmaciones pendientes" (Tramo C, ct-2026-07-22-1235).
// Store.ApproveDraft/DiscardDraft already existed above with no caller; this
// is the missing read side that makes them reachable from the dashboard too.
func (d Deps) handleGetPendingDrafts(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	drafts, err := d.Store.PendingDrafts(dashboardPendingDraftsLimit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, drafts)
}

// handleSetKillSwitch is the REST equivalent of the MCP set_kill_switch
// tool (mcpserver/admin_tools.go) — same H2+H3 hardening
// (ct-2026-07-10-0540): flips governor.SetKill and state.SetMuted
// together, since they used to be two divergent flags with no single
// caller for either. Direct owner admin from the LAN, no agent involved.
//
// T19 (ct-2026-08-05-1249): also persists to store.SettingKillSwitch,
// BEFORE applying the live effect — if the process dies between the two,
// the recorded intent ("this was killed") survives even though the live
// flags didn't get set this round; main.go's restoreKillSwitch reads it
// back on the next boot. Persisted best-effort (logged, never blocks the
// emergency stop itself on a DB hiccup) — the live governor/state calls
// below are what actually stop sends RIGHT NOW.
func (d Deps) handleSetKillSwitch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kill bool `json:"kill"`
	}
	if !decode(w, r, &body) {
		return
	}
	if d.Store != nil {
		if err := d.Store.SetSettingBool(store.SettingKillSwitch, body.Kill); err != nil {
			log.Printf("restapi: persist kill switch: %v", err)
		}
	}
	if d.Governor != nil {
		d.Governor.SetKill(body.Kill)
	}
	if d.State != nil {
		if err := d.State.SetMuted(body.Kill); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "kill switch set", "kill": body.Kill})
}

// validMode mirrors the two modes router.json/store.Chat actually use —
// same reasoning as validConfirmationMode above: reject a typo outright
// instead of silently persisting a mode nothing downstream matches.
func validMode(mode string) bool {
	switch mode {
	case "dedicated", "auto":
		return true
	}
	return false
}

func (d Deps) handleSetMode(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		ChatID string `json:"chat_id"`
		Mode   string `json:"mode"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !validMode(body.Mode) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mode must be dedicated|auto"})
		return
	}
	if err := d.Store.SetMode(body.ChatID, body.Mode); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "mode set"})
}

func (d Deps) handleSetMemory(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		ChatID string `json:"chat_id"`
		Memory string `json:"memory"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := d.Store.SetChatMemory(body.ChatID, body.Memory); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "memory set"})
}

func (d Deps) handleSetContext(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		ChatID  string `json:"chat_id"`
		Context string `json:"context"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := d.Store.SetChatContext(body.ChatID, body.Context); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "context set"})
}

// handleWhitelistAdd is the dashboard's "agregar número": appends jid to
// router.json's whitelist (idempotent — a jid already present is left
// alone) via the same Manager.Update corepipeline/capipush read from. Since
// T64 (ct-2026-08-11-1627) whitelist membership no longer gates anything —
// every number is allowed by default — so this only marks the jid VIP
// (router.IsVIP, the tray icon's mood on message). Also TouchChat's it
// (found live in browser testing, ct-2026-07-10-2312:
// without this, GET /api/chats never lists a freshly-whitelisted number —
// whitelisting alone doesn't create a chat row, only an actual inbound
// message does — so "Agregar" would silently do nothing visible until the
// contact writes first). TouchChat is the same idempotent upsert
// corepipeline itself uses; an empty name never overwrites a real one.
func (d Deps) handleWhitelistAdd(w http.ResponseWriter, r *http.Request) {
	if d.Router == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "router not available"})
		return
	}
	var body struct {
		JID string `json:"jid"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.JID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "jid is required"})
		return
	}
	if err := d.Router.Update(func(c *router.Config) {
		for _, existing := range c.Whitelist {
			if existing == body.JID {
				return
			}
		}
		c.Whitelist = append(c.Whitelist, body.JID)
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if d.Store != nil {
		if err := d.Store.TouchChat(body.JID, "", time.Now().Unix()); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "whitelist updated"})
}

// handleSetIgnored is the dashboard's IGNORADO toggle (ct-2026-07-10-2312
// rework, F2v2) — reuses the EXISTING store.SetStatus/"ignored" triage
// value (grouped-chat default since TouchChat, already load-bearing in
// send.go's group-ignored gate) rather than a new column: "ignored" was
// already real backend, not invented for the UI. Un-ignoring resets to
// "new" (TouchChat's own baseline) — there's no history of what it was
// before, and "new" is the same safe default a never-seen chat starts at.
func (d Deps) handleSetIgnored(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		ChatID  string `json:"chat_id"`
		Ignored bool   `json:"ignored"`
	}
	if !decode(w, r, &body) {
		return
	}
	status := "new"
	if body.Ignored {
		status = "ignored"
	}
	if err := d.Store.SetStatus(body.ChatID, status); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// M5 (ct-2026-07-22-1903): ignoring/un-ignoring is an explicit owner
	// decision about this chat's mode — freeze it against a later
	// applyOriginDefaultIfUnset (contact sync) silently reviving it.
	if err := d.Store.MarkConfigManual(body.ChatID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ignored flag set"})
}

// handleGetCAPIConnector returns the current cAPI connector config.
// NEVER returns the pinpass in clear — only whether it's set.
func (d Deps) handleGetCAPIConnector(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	endpoint, _ := d.Store.KVGet(store.SettingCAPIEndpoint)
	terminalID, _ := d.Store.KVGet(store.SettingCAPITerminalID)
	pinpass, _ := d.Store.KVGet(store.SettingCAPIPinpass)
	writeJSON(w, http.StatusOK, map[string]any{
		"endpoint":    endpoint,
		"terminal_id": terminalID,
		"pinpass_set": pinpass != "",
	})
}

// handleSetCAPIConnector persists the 3 cAPI fields and reconfigures the live
// injector in-hot — no restart needed.
func (d Deps) handleSetCAPIConnector(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		Endpoint   string `json:"endpoint"`
		TerminalID string `json:"terminal_id"`
		Pinpass    string `json:"pinpass"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := d.Store.SetCAPIConnector(body.Endpoint, body.TerminalID, body.Pinpass); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if d.Connector != nil {
		d.Connector.SetConfig(body.Endpoint, body.TerminalID, body.Pinpass)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleSetCAPIConnectorLine is the antena modal's "pegá la línea completa"
// flow (ct-2026-07-19-1556): the boss pastes capi_credentials' raw output
// and this parses + re-cablea in one step. S6 (ct-2026-07-30-031048): now
// routes through Store.SetPrincipalAgent (same write path as the
// set_capi_connector MCP tool and POST /api/admin/agent-update's principal
// branch) instead of the raw, unvalidated SetCAPIConnector — before this,
// the endpoint was forced to http://127.0.0.1:<port> (any LAN IP in the
// pasted line discarded) BEFORE isAllowedPrincipalEndpoint ever got a say,
// blocking the Raspberry Pi case (gateway on the Pi, agent on another
// machine of the same LAN) that function's own fix exists to enable. The IP
// now survives; SetPrincipalAgent decides whether it's allowed. name isn't
// part of the pasted line, so the current one is kept unchanged.
func (d Deps) handleSetCAPIConnectorLine(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		Line string `json:"line"`
	}
	if !decode(w, r, &body) {
		return
	}
	ip, port, terminalID, pinpass, err := capiconn.ParseConnectorString(body.Line)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	endpoint := "http://" + ip + ":" + port
	current, _, err := d.Store.PrincipalAgent(d.PrincipalTerminalID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := d.Store.SetPrincipalAgent(current.Name, endpoint, terminalID, pinpass); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrPrincipalEndpointPublic) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	if d.Connector != nil {
		d.Connector.SetConfig(endpoint, terminalID, pinpass)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "endpoint": endpoint, "terminal_id": terminalID})
}

// handleTestCAPIConnector does a probe handshake against the current config
// and returns a human-readable result (never errors out the HTTP request —
// the test result IS the response body).
func (d Deps) handleTestCAPIConnector(w http.ResponseWriter, r *http.Request) {
	if d.Connector == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "result": "connector not available"})
		return
	}
	err := d.Connector.TestHandshake()
	if err == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": "200 ok — handshake exitoso"})
		return
	}
	msg := err.Error()
	var result string
	switch {
	case strings.Contains(msg, "401"):
		result = "401 — pinpass inválido"
	case strings.Contains(msg, "endpoint not configured"):
		result = "sin configurar — guardá endpoint/terminal_id/pinpass primero"
	default:
		result = "error: " + msg
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": false, "result": result})
}

// handleCAPIPing sends a REAL test dispatch to the wired terminal (P8,
// ct-2026-07-22-0422 — corrects ct-2026-07-22-0356's ping, which only did a
// handshake and never actually reached the terminal). Same compact payload
// shape a real dispatch uses (a trailing "NC:<nonce>" line, no rules.md
// block — the ping has no real chat to have rules for) — but the nonce is
// NEVER registered via gate.RegisterDispatch: this is a standalone test
// blob, not a real dispatch, so it can't collide with or interfere with
// the live gate/redispatch machinery. The message text says outright it's
// a test so the agent doesn't chase get_instructions(nonce) on a nonce the
// gate never heard of.
//
// agent_id (M2, ct-2026-07-22-1301, optional): pings THAT agent's own
// injector (Injectors.InjectorFor — same map RegisterInjector/OnAgentUpsert
// already populate, principal included, its PortFallback slot is in there
// too) instead of the antena modal's single Connector. Omitted -> unchanged
// pre-M2 behavior, still Connector — every existing caller (the antena
// modal's own ping button) keeps working exactly as before.
func (d Deps) handleCAPIPing(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AgentID string `json:"agent_id"`
	}
	if !decode(w, r, &body) {
		return
	}

	var inj Injector
	if body.AgentID != "" {
		if d.Injectors == nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "result": "ping no disponible: sin resolver de agentes"})
			return
		}
		found, ok := d.Injectors.InjectorFor(body.AgentID)
		if !ok {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "result": "agente sin conector — registrá sus credenciales primero"})
			return
		}
		inj = found
	} else {
		if d.Connector == nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "result": "ping no disponible: conector sin configurar"})
			return
		}
		inj = d.Connector
	}

	nonce := make([]byte, 4)
	if _, err := rand.Read(nonce); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "result": "error: " + err.Error()})
		return
	}
	pingText := "🏓 PING de prueba desde el dashboard de Piumy — no es un mensaje real, no requiere get_instructions ni respuesta."
	payload := pingText + "\nNC:" + hex.EncodeToString(nonce) + "\n"

	if err := inj.Inject("", "piumy-dashboard", payload); err != nil {
		msg := err.Error()
		var result string
		switch {
		case strings.Contains(msg, "no escucha"):
			result = "el terminal no escucha — antena apagada o tab cerrado"
		case strings.Contains(msg, "401"):
			result = "401 — pinpass inválido"
		case strings.Contains(msg, "endpoint not configured"):
			result = "sin configurar — guardá endpoint/terminal_id/pinpass primero"
		default:
			result = "error: " + msg
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "result": result})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": "200 ok — el terminal recibió el ping"})
}

// handleGetRecoveryEmail returns the boss's configured recovery-email
// address (ct-2026-07-19-1716, S1e-2) — empty string means unset.
func (d Deps) handleGetRecoveryEmail(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	email, err := d.Store.KVGet(store.SettingDashRecoveryEmail)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"email": email})
}

// handleSetRecoveryEmail persists the recovery-email address. Light format
// check only (must contain "@", no whitespace) — same "trust the owner,
// don't over-validate free text" criterion the rest of the dashboard's
// config fields (memory/context/rules) already use; strict RFC validation
// isn't the point here, an obviously-broken address is.
func (d Deps) handleSetRecoveryEmail(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Email != "" && (!strings.Contains(body.Email, "@") || strings.ContainsAny(body.Email, " \t\n")) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(effectiveLang(d.Store), "server.invalid_email")})
		return
	}
	if err := d.Store.KVSet(store.SettingDashRecoveryEmail, body.Email); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "recovery email set"})
}

// handleGetDispatchDebounce reports the EFFECTIVE values capipush.isDebounced
// is actually using right now (T90, ct-2026-08-28-1350) — the store override
// if the dashboard ever set one, else the boot-time config default
// (DefaultDispatchDebounce/DefaultMaxDispatchDebounce, the same values
// capipush.Config already got at startup). Seconds, not a Duration string —
// the tablero's own unit, no client-side parsing.
func (d Deps) handleGetDispatchDebounce(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	debounce := d.Store.SettingDuration(store.SettingCapipushDispatchDebounce, d.DefaultDispatchDebounce)
	maxDebounce := d.Store.SettingDuration(store.SettingCapipushMaxDispatchDebounce, d.DefaultMaxDispatchDebounce)
	writeJSON(w, http.StatusOK, map[string]int{
		"debounce_seconds":     int(debounce.Seconds()),
		"max_debounce_seconds": int(maxDebounce.Seconds()),
	})
}

// handleSetDispatchDebounce persists a live override for both values — hot,
// no restart: capipush.isDebounced rereads store.SettingCapipushDispatch
// Debounce/MaxDispatchDebounce on every call, so the very next sweep already
// sees it. 0 for debounce is accepted on purpose ("despachá apenas llegue",
// boss's own legitimate choice — isDebounced returns early for it instead of
// treating it as unset). The one guard that IS enforced: the ceiling can't
// sit below the floor — a smaller max than the debounce it's supposed to
// cap is a configuration with no sensible meaning, not a product lock.
func (d Deps) handleSetDispatchDebounce(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		DebounceSeconds    int `json:"debounce_seconds"`
		MaxDebounceSeconds int `json:"max_debounce_seconds"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.DebounceSeconds < 0 || body.MaxDebounceSeconds < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(effectiveLang(d.Store), "server.values_not_negative")})
		return
	}
	if body.MaxDebounceSeconds < body.DebounceSeconds {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(effectiveLang(d.Store), "server.ceiling_below_floor")})
		return
	}
	if err := d.Store.SetSettingDuration(store.SettingCapipushDispatchDebounce, time.Duration(body.DebounceSeconds)*time.Second); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := d.Store.SetSettingDuration(store.SettingCapipushMaxDispatchDebounce, time.Duration(body.MaxDebounceSeconds)*time.Second); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "dispatch debounce set"})
}

// dashboardRecoveryEmailSeedEnv (Windows installer, ct-2026-07-31-1643) —
// same seed-only pattern as auth.go's dashboardPasswordSeedEnv: the
// installer's one-shot env var for the very first boot. SeedRecoveryEmailFromEnv
// only ever writes when the KV is still empty — an existing value (including
// one the boss later cleared on purpose) is never overwritten.
const dashboardRecoveryEmailSeedEnv = "PIUMY_DASHBOARD_RECOVERY_EMAIL"

// SeedRecoveryEmailFromEnv seeds SettingDashRecoveryEmail from the
// installer's env var at startup. Unlike passHash's lazy seed-on-first-login
// (login is unavoidable, so lazy is enough), the recovery email has no
// forcing function — the boss might never open the config modal — so this
// runs once eagerly at boot (main.go) instead of waiting for a handler.
func SeedRecoveryEmailFromEnv(st *store.Store) error {
	existing, err := st.KVGet(store.SettingDashRecoveryEmail)
	if err != nil {
		return err
	}
	if existing != "" {
		log.Println("restapi: correo de recuperación: ya hay uno configurado, no se toca")
		return nil
	}
	seed := os.Getenv(dashboardRecoveryEmailSeedEnv)
	if seed == "" {
		return nil
	}
	if !strings.Contains(seed, "@") || strings.ContainsAny(seed, " \t\n") {
		log.Printf("restapi: correo de recuperación: %s trae un valor con formato inválido, se ignora", dashboardRecoveryEmailSeedEnv)
		return nil
	}
	if err := st.KVSet(store.SettingDashRecoveryEmail, seed); err != nil {
		return err
	}
	log.Println("restapi: correo de recuperación: sembrado desde " + dashboardRecoveryEmailSeedEnv + " (siembra del instalador, primer arranque)")
	return nil
}

// handleAssignChatToAgent is M3's manual number-assignment (ct-2026-07-22-1301)
// — writes/clears chats.status' agent_exclusive:<id> form (store.
// AgentExclusiveStatus, same write path set_chat_status/SetStatus already
// use — chat.go:198). agent_id=="" CLEARS the assignment (falls back to
// "new", same convention as handleSetIgnored's un-ignore — there's no
// history of what it was before). Assigning to the principal IS allowed
// (T70, ct-2026-08-27-1404): exclusive assignment isn't the same as falling
// back to the principal by default — a chat assigned to it won't be taken by
// another agent. An agent_id the store doesn't know about is rejected
// (typo protection) rather than silently writing a status dispatch can
// never resolve.
func (d Deps) handleAssignChatToAgent(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		ChatID  string `json:"chat_id"`
		AgentID string `json:"agent_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.ChatID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "chat_id is required"})
		return
	}

	if body.AgentID == "" {
		if err := d.Store.SetStatus(body.ChatID, "new"); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "unassigned"})
		return
	}

	// The principal never has a row in `agents` (PrincipalAgent's own doc:
	// it's synthesized from KV, not self-registered) — GetAgent alone would
	// reject it as unknown even though it's a valid, listed assignment target.
	if body.AgentID != d.PrincipalTerminalID {
		if _, ok, err := d.Store.GetAgent(body.AgentID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		} else if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(effectiveLang(d.Store), "server.unknown_agent_id")})
			return
		}
	}
	if err := d.Store.SetStatus(body.ChatID, store.AgentExclusiveStatus(body.AgentID)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "assigned"})
}

// handleCreateAgent registers an agent from the dashboard (ct-2026-07-29,
// agentes paso 1) — the REST twin of the MCP register_agent tool
// (agent_tools.go), same required fields, same store.UpsertAgent write path,
// reused rather than duplicated. The alta never gets denied for a colliding
// agent_id (T155, boss verbatim: "jamas que se vuelva a negar el registro
// por el puerto chocante... el problema es doble" — denying leaves the live
// agent out AND leaves the dead one in, eating dispatches): the one that
// arrives wins and takes the id's place, response says "updated" instead of
// "created" so the dashboard can tell alta from take-over. The principal's
// own id routes through store.SetPrincipalAgent — the same path
// handleUpdateAgent's principal branch uses — never UpsertAgent, so it never
// creates a secondary row shadowing the principal.
func (d Deps) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		AgentID           string `json:"agent_id"`
		Name              string `json:"name"`
		Endpoint          string `json:"endpoint"`
		AntennaTerminalID string `json:"antenna_terminal_id"`
		Pinpass           string `json:"pinpass"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.AgentID == "" || body.Endpoint == "" || body.AntennaTerminalID == "" || body.Pinpass == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id, endpoint, antenna_terminal_id and pinpass are required"})
		return
	}
	if d.PrincipalTerminalID != "" && body.AgentID == d.PrincipalTerminalID {
		if err := d.Store.SetPrincipalAgent(body.Name, body.Endpoint, body.AntennaTerminalID, body.Pinpass); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrPrincipalEndpointPublic) {
				status = http.StatusBadRequest
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		if d.Connector != nil {
			d.Connector.SetConfig(body.Endpoint, body.AntennaTerminalID, body.Pinpass)
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "updated", "agent_id": body.AgentID, "role": "principal"})
		return
	}
	_, existed, err := d.Store.GetAgent(body.AgentID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	a := store.Agent{
		AgentID: body.AgentID, Name: body.Name, Endpoint: body.Endpoint,
		AntennaTerminalID: body.AntennaTerminalID, Pinpass: body.Pinpass, Role: "secondary",
	}
	if err := d.Store.UpsertAgent(a); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if d.OnAgentUpsert != nil {
		d.OnAgentUpsert(a.AgentID, a.Endpoint, a.AntennaTerminalID, a.Pinpass)
	}
	status := "created"
	if existed {
		status = "updated"
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": status, "agent_id": a.AgentID})
}

// handleUpdateAgent edits an EXISTING agent — principal or secondary, one
// endpoint either way (ct-2026-07-29, agentes paso 1 — "la configuración
// cAPI es siempre por agente"). Every field but agent_id is optional, same
// "omit to keep current" convention set_agent_capi (MCP) already uses.
//
// The principal branch reads/writes through store.PrincipalAgent/
// SetPrincipalAgent (paso 3 — shared with the set_capi_connector MCP tool,
// so REST and MCP can't drift on how the principal is updated) — this does
// NOT migrate the principal into the `agents` table (rejected design, see
// the boss/Citrino exchange: no storage migration, API-shape unification
// only). SetPrincipalAgent rejects a non-local endpoint outright (paso 3 —
// closes a gap paso 1 left: only the dashboard's readonly input enforced
// "principal endpoint is always local" before, not the backend).
func (d Deps) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		AgentID           string  `json:"agent_id"`
		Name              *string `json:"name"`
		Endpoint          *string `json:"endpoint"`
		AntennaTerminalID *string `json:"antenna_terminal_id"`
		Pinpass           *string `json:"pinpass"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.AgentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id is required"})
		return
	}

	if d.PrincipalTerminalID != "" && body.AgentID == d.PrincipalTerminalID {
		current, _, err := d.Store.PrincipalAgent(d.PrincipalTerminalID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		name, endpoint, terminalID, pinpass := current.Name, current.Endpoint, current.AntennaTerminalID, current.Pinpass
		if body.Name != nil {
			name = *body.Name
		}
		if body.Endpoint != nil {
			endpoint = *body.Endpoint
		}
		if body.AntennaTerminalID != nil {
			terminalID = *body.AntennaTerminalID
		}
		if body.Pinpass != nil {
			pinpass = *body.Pinpass
		}
		if err := d.Store.SetPrincipalAgent(name, endpoint, terminalID, pinpass); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrPrincipalEndpointPublic) {
				status = http.StatusBadRequest
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		if d.Connector != nil {
			d.Connector.SetConfig(endpoint, terminalID, pinpass)
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "updated", "agent_id": body.AgentID})
		return
	}

	existing, ok, err := d.Store.GetAgent(body.AgentID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(effectiveLang(d.Store), "server.unknown_agent_id_create_first")})
		return
	}
	if body.Name != nil {
		existing.Name = *body.Name
	}
	if body.Endpoint != nil {
		existing.Endpoint = *body.Endpoint
	}
	if body.AntennaTerminalID != nil {
		existing.AntennaTerminalID = *body.AntennaTerminalID
	}
	if body.Pinpass != nil {
		existing.Pinpass = *body.Pinpass
	}
	if err := d.Store.UpsertAgent(existing); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if d.OnAgentUpsert != nil {
		d.OnAgentUpsert(existing.AgentID, existing.Endpoint, existing.AntennaTerminalID, existing.Pinpass)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated", "agent_id": existing.AgentID})
}

// handleDeleteAgent removes an agent (ct-2026-07-29, agentes paso 1) — a
// secondary, or, since T76 (ct-2026-08-27-1752, boss verbatim: "no puedo
// borrar a la gente principal... a mí no me interesa [los problemas], yo
// lo quiero, como yo lo digo"), the principal too. Two things happen
// beyond the DB row, both required for the delete to be real rather than
// cosmetic (boss: "ningún chat queda apuntando a un agente que ya no
// existe" / "un borrado que deja las credenciales vivas es un borrado que
// miente"):
//  1. UnassignAllChatsForAgent reverts every chat exclusively assigned to
//     this agent back to "new" — no dangling agent_exclusive left pointing
//     at a dead id. Works the same for the principal (its own agent_id is
//     just a string here, agent_exclusive doesn't care it has no row).
//  2. Secondary: OnAgentDelete (capipush.Pusher.UnregisterInjector) removes
//     the live injector so the old credentials stop dispatching
//     immediately. Principal: it has no `agents` row to delete
//     (PrincipalAgent synthesizes it from KV — T70 already hit this) —
//     "deleted" means store.ClearPrincipalAgent (back to the same
//     unconfigured state a fresh install starts in) plus resetting the
//     live injector (Connector.SetConfig with empty credentials) so it
//     stops looking configured; dispatch's own existing "no antenna" quiet
//     retention takes it from there. No new gate for "don't leave zero
//     agents" — if that happens, the gateway keeps receiving and just has
//     nowhere to dispatch, the accepted outcome, not a state to prevent.
func (d Deps) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		AgentID string `json:"agent_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.AgentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id is required"})
		return
	}
	isPrincipal := d.PrincipalTerminalID != "" && body.AgentID == d.PrincipalTerminalID
	if !isPrincipal {
		if _, ok, err := d.Store.GetAgent(body.AgentID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		} else if !ok {
			// Same concept as handleAssignChatToAgent's "agent_id desconocido"
			// (T153 etapa 3b): this call site's own literal was already the
			// exact English text server.unknown_agent_id needs — reused
			// instead of authoring a near-duplicate (checked against the
			// whole catalog first, same discipline as etapa 2b-iv).
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(effectiveLang(d.Store), "server.unknown_agent_id")})
			return
		}
	}
	unassigned, err := d.Store.UnassignAllChatsForAgent(body.AgentID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if isPrincipal {
		if err := d.Store.ClearPrincipalAgent(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if d.Connector != nil {
			d.Connector.SetConfig("", "", "")
		}
	} else {
		if err := d.Store.DeleteAgent(body.AgentID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if d.OnAgentDelete != nil {
			d.OnAgentDelete(body.AgentID)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "chats_unassigned": unassigned})
}

// handlePromoteAgent is T76's (ct-2026-08-27-1752) "cambiar de rol" from
// the dashboard — boss verbatim: "poder cambiar si tengo un secundario a
// al principal de manera rápida". One call to store.PromoteToPrincipal
// (the single swap implementation — MCP's promote_to_principal tool,
// agent_tools.go, calls the exact same store method), then the two live
// hot-reloads the store call alone can't do: Connector.SetConfig for the
// new principal's injector (same mechanism set_capi_connector already
// uses) and OnAgentUpsert for the demoted one's OWN new injector, at the
// vacated agent_id it now occupies.
func (d Deps) handlePromoteAgent(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		AgentID string `json:"agent_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.AgentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id is required"})
		return
	}
	demotedID, err := d.Store.PromoteToPrincipal(d.PrincipalTerminalID, body.AgentID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	newPrincipal, _, err := d.Store.PrincipalAgent(d.PrincipalTerminalID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if d.Connector != nil {
		d.Connector.SetConfig(newPrincipal.Endpoint, newPrincipal.AntennaTerminalID, newPrincipal.Pinpass)
	}
	if d.OnAgentUpsert != nil {
		if demoted, ok, err := d.Store.GetAgent(demotedID); err == nil && ok {
			d.OnAgentUpsert(demotedID, demoted.Endpoint, demoted.AntennaTerminalID, demoted.Pinpass)
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "promoted", "new_principal": d.PrincipalTerminalID, "demoted_to": demotedID})
}

// validConfigLevelDefault mirrors store.applyConfigLevelDefault's own enum
// — "boss" is deliberately excluded, unlike validConfigLevel: a default is
// never the owner's identity (M5, ct-2026-07-22-1903, boss's own combo box
// only offers sin confirmar/con confirmación/desatendido/ignorado).
func validConfigLevelDefault(level string) bool {
	switch level {
	case "auto", "confirm", "unattended", "ignored":
		return true
	}
	return false
}

// M5 (ct-2026-07-22-1903) — 2 pares (modo + reglas) en la cabecera del
// dashboard: "mensajes nuevos" (números desconocidos) y "contactos de la
// libreta". Calco de handleGetRecoveryEmail/handleSetRecoveryEmail's shape
// — GET simple + POST con validación liviana. El EFECTO de estos 4
// settings (cómo un chat sin config manual hereda el default por origen)
// vive en store.go (TouchChat/SetContactName/applyOriginDefaultIfUnset) —
// estos handlers son solo lectura/escritura del KV, ya consumido por ahí.

func (d Deps) handleGetConfigLevelDefaultNew(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	level, err := d.Store.EffectiveConfigLevelDefault(store.SettingConfigLevelDefaultNew)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"level": level})
}

func (d Deps) handleSetConfigLevelDefaultNew(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		Level string `json:"level"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !validConfigLevelDefault(body.Level) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "level must be auto|confirm|unattended|ignored"})
		return
	}
	if err := d.Store.KVSet(store.SettingConfigLevelDefaultNew, body.Level); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (d Deps) handleGetConfigLevelDefaultContact(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	level, err := d.Store.EffectiveConfigLevelDefault(store.SettingConfigLevelDefaultContact)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"level": level})
}

func (d Deps) handleSetConfigLevelDefaultContact(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		Level string `json:"level"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !validConfigLevelDefault(body.Level) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "level must be auto|confirm|unattended|ignored"})
		return
	}
	if err := d.Store.KVSet(store.SettingConfigLevelDefaultContact, body.Level); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (d Deps) handleGetRulesDefaultNewNumber(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	rules, err := d.Store.KVGet(store.SettingRulesDefaultNewNumber)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"rules": rules})
}

func (d Deps) handleSetRulesDefaultNewNumber(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		Rules string `json:"rules"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := d.Store.KVSet(store.SettingRulesDefaultNewNumber, body.Rules); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (d Deps) handleGetRulesDefaultContact(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	rules, err := d.Store.KVGet(store.SettingRulesDefaultContact)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"rules": rules})
}

func (d Deps) handleSetRulesDefaultContact(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		Rules string `json:"rules"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := d.Store.KVSet(store.SettingRulesDefaultContact, body.Rules); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleGetAgentDefault/handleSetAgentDefault (T71, ct-2026-08-27-1410; sumó
// grupos/boss en T72, ct-2026-08-27-1625) back all 4 agent-default-*
// endpoints — new-number/contact/group/boss, agente por defecto para ese
// tipo, ruteo puro (store.EffectiveAgentDefault, capipush.go's dispatch).
// Closures over the KV key rather than 8 near-identical functions —
// GET/SET-default-rules and GET/SET-config-level-default above don't do
// this (2 origins each, kept separate historically); parameterizing here
// is a fresh choice for a fresh pair, not a rewrite of those — T72 growing
// this to 4 types confirms it was the right call, not 8 copies to keep in
// sync. Same validation handleAssignChatToAgent (T70) already settled: the
// principal is a valid target even though it has no `agents` row, any
// other agent_id must exist, "" clears the default.
func (d Deps) handleGetAgentDefault(key string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
			return
		}
		agentID, err := d.Store.KVGet(key)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"agent_id": agentID})
	}
}

func (d Deps) handleSetAgentDefault(key string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
			return
		}
		var body struct {
			AgentID string `json:"agent_id"`
		}
		if !decode(w, r, &body) {
			return
		}
		if body.AgentID != "" && body.AgentID != d.PrincipalTerminalID {
			if _, ok, err := d.Store.GetAgent(body.AgentID); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			} else if !ok {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(effectiveLang(d.Store), "server.unknown_agent_id")})
				return
			}
		}
		if err := d.Store.KVSet(key, body.AgentID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// handleGetIdentity/handleSetIdentity (T13, ct-2026-08-05-123147): "asistente
// de qué" — the field the 4 rules tiers sit under in the pestaña Rules. Same
// plain-string CRUD pattern as rules-default-contact above; no validation
// beyond store.decode, same as every other free-text rules field.
func (d Deps) handleGetIdentity(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	identity, err := d.Store.KVGet(store.SettingIdentity)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"identity": identity})
}

func (d Deps) handleSetIdentity(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		Identity string `json:"identity"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := d.Store.KVSet(store.SettingIdentity, body.Identity); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReset is the boss's "partir de 0" smoke-test reset (D4,
// ct-2026-07-22-2100 — checkpointed with Citrino before implementing).
// SELECTIVE: Store.ResetMessagingData wipes exactly the 7 agreed tables
// (chats/messages/group_members/media/drafts/outbox/media_pending —
// chat_groups retired, T18B, ct-2026-08-05-1243), PRESERVING kv
// (antenna/password/every setting, M5
// defaults included), agents (registered secondaries), and usage
// (anti-ban, non-negotiable — see ResetMessagingData's doc for why).
// NEVER touches whatsmeow.db — this handler has no reference to that
// store/file at all, structurally can't reach it (a completely separate
// *store.Store the whatsmeow client owns, opened from PIUMY_WA_DB_PATH,
// unrelated to Deps.Store here). Also clears MediaDir's own CONTENTS (not
// the directory) so orphaned files don't accumulate across repeated
// resets, then kicks a re-sync (Resetter) since the session normally
// stays connected through a reset — no natural reconnect event to
// re-trigger syncContacts/seedGroups otherwise.
func (d Deps) handleReset(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	if err := d.Store.ResetMessagingData(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if d.MediaDir != "" {
		if err := clearDirContents(d.MediaDir); err != nil {
			log.Printf("restapi: reset: clear media dir %s: %v", d.MediaDir, err)
		}
	}
	if d.Resetter != nil {
		d.Resetter.KickResync()
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
}

// handleDisconnect is the dashboard's "Desconectar" button (M3,
// ct-2026-07-22-2342) — see the mux registration's doc for the full picture
// (why Logout, not Disconnect; why a restart is still needed after).
func (d Deps) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if d.Disconnecter == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "gateway not available"})
		return
	}
	if err := d.Disconnecter.Logout(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "disconnected"})
}

// handleReconnect is the dashboard's "Ver QR / Reconectar" button (Fix 2,
// ct-2026-07-23-0047) — kick-starts a new QR pairing flow after a Logout
// without restarting the process. Returns "already paired" if the gateway
// is already connected (Store.ID != nil), or "reconnecting" if a fresh
// pairLoop was spawned. If a pairLoop is already running (no-op re-request),
// still returns 200 "reconnecting" — the QR is already being generated.
func (d Deps) handleReconnect(w http.ResponseWriter, r *http.Request) {
	if d.Reconnecter == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "gateway not available"})
		return
	}
	if err := d.Reconnecter.Reconnect(r.Context()); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reconnecting"})
}

// clearDirContents removes every entry INSIDE dir, leaving dir itself in
// place — os.RemoveAll(dir) would also delete the directory, which a
// concurrent writer (the media-download worker) could then fail to
// recreate mid-write. A missing dir is not an error (nothing to clear).
func clearDirContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return false
	}
	return true
}
