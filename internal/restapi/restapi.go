// Package restapi is the SSE nudge (F4-DESIGN.md §"Nodos": "GET
// /api/events", F4b) + the privileged DB-admin/draft-approval endpoints
// (F4c) — direct owner administration from the LAN, distinct from the
// agent-facing MCP surface (mcpserver). Group/profile tools are MCP-only
// (not in this contract's REST scope).
package restapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"time"

	"piumy-gateway/internal/eventbus"
	"piumy-gateway/internal/governor"
	"piumy-gateway/internal/i18n"
	"piumy-gateway/internal/router"
	"piumy-gateway/internal/sessionbackup"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
)

// CAPIConnector is the reconfigurable cAPI injector the dashboard can update
// at runtime. Defined as a local interface to avoid importing capipush.
// Inject (ct-2026-07-22-0422, P8 ping) is the SAME method the normal
// dispatch uses (capipush.Pusher -> Injector.Inject) — the admin ping
// handler calls it directly with a standalone test ciphertext, never
// through capipush's gate, so the real dispatch path is untouched.
type CAPIConnector interface {
	SetConfig(endpoint, terminalID, pinpass string)
	TestHandshake() error
	Inject(terminalID, from, ciphertext string) error
}

// Injector is capipush.Injector's shape, mirrored locally (same "avoid
// importing capipush" reasoning as CAPIConnector above) — just the one
// method a per-agent ping needs (M2, ct-2026-07-22-1301).
type Injector interface {
	Inject(terminalID, from, ciphertext string) error
}

// Resetter kicks a re-sync after D4's "partir de 0" reset (ct-2026-07-22-2100)
// — whatsmeow.Adapter.KickResync satisfies this (same "avoid importing
// whatsmeow" reasoning as MediaFetcher below). Re-runs the SAME triggers a
// real reconnect fires (syncContacts + seedGroups) since the session
// normally stays connected through a reset — no natural reconnect event to
// do it on its own.
type Resetter interface {
	KickResync()
}

// InjectorResolver resolves a registered agent's own cAPI injector by
// agent_id — capipush.Pusher.InjectorFor, the SAME map RegisterInjector/
// OnAgentUpsert already populate (M2, ct-2026-07-22-1301). ok is false for
// an agent_id nothing ever registered — the dashboard's per-agent ping
// reports a clear error rather than silently no-op-ing through
// capipush.LogInjector the way a real dispatch would.
type InjectorResolver interface {
	InjectorFor(agentID string) (Injector, bool)
}

// MediaFetcher triggers the gradual on-demand media backfill for one chat
// (ct-2026-07-21-1358, popup backend A) — implemented by whatsmeow.Adapter.
// Defined as a local interface to avoid importing whatsmeow, same reasoning
// as CAPIConnector above. FetchPendingMedia is fire-and-forget: it starts
// the paced FIFO download in the background and returns immediately, so the
// HTTP handler never blocks on however long the backlog takes.
type MediaFetcher interface {
	FetchPendingMedia(chatJID string)
}

// AvatarRequester triggers a paced, on-demand profile-picture check for one
// jid (T17 Parte 3, ct-2026-08-05-1240) — whatsmeow.Adapter.RequestAvatar
// satisfies this, same "local interface, avoid importing whatsmeow"
// reasoning as MediaFetcher above. Fire-and-forget: enqueues the check and
// returns immediately (avatar_read.go's handleAvatar always serves
// whatever's cached without waiting on it) — never a bulk sweep, only ever
// called for a jid the dashboard is showing right now.
type AvatarRequester interface {
	RequestAvatar(jid string)
}

// ProfileStatus reads and writes the host number's own status/"About" text
// — write side POST /api/admin/profile-status (T92, ct-2026-08-28) —
// whatsmeow.Adapter.SetProfileStatus satisfies this, the SAME method the
// boss-only MCP tool set_profile_status already calls
// (mcpserver/group_tools.go) — one place doing the write, two entry points.
// Read side GET /api/admin/profile-status (T96, ct-2026-08-28-1743, "la
// mitad que faltó de T92" — mostrar el estado bajo el nombre, y pre-llenar
// el campo de edición en vez de dejarlo vacío sobre un valor existente).
// GetProfileStatus reads it live from WhatsApp each call — no cache, the
// same reasoning MANUAL.md's T96 entry documents in full: this is a
// decorative field ("un adorno, no un dato crítico", Citrino), not
// something worth a passive-listener/cache design like OwnName's.
//
// NOT the display name in either direction: whatsmeow has no API for that
// (group_tools.go's own doc comment), so there is no NameSetter/NameGetter
// here to mirror it — an editable-looking field for something that can't
// actually be changed is worse than no field at all (T92's own finding).
// Defined locally to avoid importing whatsmeow, same reasoning as
// Disconnecter/MediaFetcher above.
type ProfileStatus interface {
	SetProfileStatus(ctx context.Context, status string) error
	GetProfileStatus(ctx context.Context) (string, error)
}

// ProfilePhoto sets the host number's own WhatsApp profile photo — write
// side POST /api/admin/profile-photo (T112c, ct-2026-09-01-145755) —
// whatsmeow.Adapter.SetProfilePhoto satisfies this, the SAME method the
// boss-only MCP tool set_profile_photo already calls (T111,
// mcpserver/group_tools.go) — one place doing the write, two entry points,
// same reasoning as ProfileStatus above. A separate interface, not a third
// method bolted onto ProfileStatus: the two write to unrelated WhatsApp
// fields and nothing here needs both at once.
type ProfilePhoto interface {
	SetProfilePhoto(ctx context.Context, jpeg []byte) (string, error)
}

// Disconnecter unlinks the current WhatsApp session (POST
// /api/admin/disconnect, M3, ct-2026-07-22-2342, the dashboard's
// "Desconectar" button) — whatsmeow.Adapter.Logout satisfies this. Distinct
// from process shutdown (Stop): Logout deletes the local device store, so a
// FRESH QR pairing is required afterward — M1's full-sync flags only apply
// to a fresh pairing, the whole point of the boss's re-pareo. Defined
// locally to avoid importing whatsmeow, same reasoning as CAPIConnector/
// MediaFetcher above.
type Disconnecter interface {
	Logout(ctx context.Context) error
}

// Reconnecter kicks off a new QR pairing flow after a Logout — the "Ver
// QR / Reconectar" button's POST /api/admin/reconnect (Fix 2,
// ct-2026-07-23-0047). whatsmeow.Adapter.Reconnect satisfies this. Defined
// locally to avoid importing whatsmeow, same reasoning as Disconnecter/
// HistorySyncProgress above.
type Reconnecter interface {
	Reconnect(ctx context.Context) error
}

// QRStatus exposes the live QR-pairing state for GET /api/status (P2,
// ct-2026-07-24-0015): current code string, its expiry time, and whether
// the last round ended without pairing (qr_expired). whatsmeow.Adapter.
// QRState satisfies this. nil -> fields report zero values, same
// degrade-one-piece convention as every other optional Deps field.
type QRStatus interface {
	QRState() (code string, expiresAt time.Time, expired bool)
}

// HistorySyncProgress exposes the live passive full-sync visibility signal
// (items 1+2, ct-2026-07-23-0047, Citrino-approved design) — GET
// /api/status reads this so the boss can SEE the post-re-pareo history push
// actually flowing, instead of waiting "minutes to hours" blind (M2's own
// finding: the on-demand worker's history_state progress doesn't move for
// passive chunks, so without this there is NO progress signal at all
// during exactly the window that matters most). whatsmeow.Adapter.
// HistorySyncStatus satisfies this. active also doubles as the SAME signal
// M2's gate (freshPairingSyncWindow) reads — one function, two consumers,
// so the dashboard's "receiving history" line and the gate that pauses the
// on-demand worker can never disagree. Defined locally to avoid importing
// whatsmeow, same reasoning as Resetter/MediaFetcher/Disconnecter above.
type HistorySyncProgress interface {
	HistorySyncStatus() (active bool, messages, chats int, lastActivityAgo time.Duration)
}

// LIDResolver resolves a @lid JID to its phone-number JID — the exact same
// seam capipush.LIDResolver already uses (whatsmeow.Adapter.ResolvePN),
// reused here READ-ONLY for the Contactos tab's display dedup
// (ct-2026-07-21-1809: @lid and the same person's real number both showing
// up as separate "contacts"). This is deliberately NOT the identidad-
// unificada reconcile machinery the boss cancelled (ct-2026-07-18-171940,
// store.ReconcileIdentities/POST /api/admin/reconcile-identities) — no chat
// row is merged, renamed, or re-keyed; handleChats only uses the resolution
// to decide what to SHOW. nil (unwired, e.g. tests) just skips it — every
// chat displays exactly as stored, same as today.
type LIDResolver interface {
	ResolvePN(ctx context.Context, lidJID string) (string, error)
}

// SMTPConfig is the boss's outbound mail relay for email-channel password
// recovery (recover.go, ct-2026-07-19-1716, S1e-2). Empty Host means email
// recovery isn't configured.
type SMTPConfig struct {
	Host string
	Port string
	User string
	Pass string
	From string
}

// Deps bundles what restapi needs.
type Deps struct {
	Bus   *eventbus.Bus // nil -> /api/events 503s rather than panicking
	Store *store.Store  // nil -> the admin endpoints 503

	// Governor/State: the anti-ban kill switch (H2+H3 hardening,
	// ct-2026-07-10-0540) — POST /api/admin/kill flips both together. Same
	// nil-safe convention as Store: unset just skips that half.
	Governor *governor.Limiter
	State    *state.Manager

	// Router backs POST /api/admin/whitelist-add (ct-2026-07-10-2312) — the
	// dashboard's own "agregar número" — same Manager corepipeline/capipush
	// already share, so a change is live immediately, no restart. nil ->
	// that one endpoint 503s.
	Router *router.Manager

	// Connector is the live cAPI injector — the dashboard reconfigures it at
	// runtime via POST /api/admin/capi-connector. nil -> those 3 endpoints 503.
	Connector CAPIConnector

	// Injectors resolves a SECONDARY agent's own injector by agent_id for
	// the per-agent ping (POST /api/admin/capi-ping with agent_id, M2,
	// ct-2026-07-22-1301) — capipush.Pusher itself satisfies this. nil ->
	// a ping WITH agent_id reports "no disponible" instead of a 500; a
	// ping withOUT agent_id is unaffected, it still goes through Connector
	// exactly as before Injectors existed.
	Injectors InjectorResolver

	// OnAgentUpsert hot-registers a secondary agent's cAPI injector after
	// POST /api/admin/agent-create or /api/admin/agent-update writes its
	// row (ct-2026-07-29, agentes paso 1) — the REST-side twin of
	// mcpserver.Deps.OnAgentUpsert (register_agent/set_agent_capi already
	// had this; the dashboard's own write path didn't). Both are wired in
	// main.go to the same effect (pusher.RegisterInjector) — two entry
	// points, one outcome. nil -> the row is written but its injector
	// doesn't take effect until restart.
	OnAgentUpsert func(agentID, endpoint, terminalID, pinpass string)

	// OnLanguageChanged hot-refreshes the tray menu's text after POST
	// /api/admin/language (T153 etapa 3c, ct-2026-09-16-1854) — same shape
	// as OnAgentUpsert/OnAgentDelete below, a callback main.go wires to its
	// own in-memory state. nil -> the language change still takes effect
	// (dashboard, future notices) but the tray keeps its old text until
	// Piumy restarts.
	OnLanguageChanged func(i18n.Lang)

	// OnAgentDelete removes a secondary agent's live injector after POST
	// /api/admin/agent-delete (ct-2026-07-29) — without this, a deleted
	// agent's OLD credentials keep dispatching from memory even though its
	// `agents` row is gone (boss: "un borrado que deja las credenciales
	// vivas es un borrado que miente"). nil -> the row is deleted but the
	// injector stays live until restart.
	OnAgentDelete func(agentID string)

	// MediaFetcher triggers the on-demand media FIFO backfill (POST
	// /api/media/fetch, ct-2026-07-21-1358). nil -> that endpoint 503s.
	MediaFetcher MediaFetcher

	// Avatars triggers the paced on-demand avatar check (GET /api/avatar,
	// T17 Parte 3, ct-2026-08-05-1240). nil -> the endpoint still serves
	// whatever's already cached, it just never nudges a fresh check.
	Avatars AvatarRequester

	// LIDResolver backs GET /api/chats' @lid/número dedup (ct-2026-07-21-1809).
	// nil -> every chat displays exactly as stored (today's behavior).
	LIDResolver LIDResolver

	// Backup backs GET /api/status's "encrypted" badge (S1b,
	// ct-2026-07-19-1823) — reuses Backuper.Enabled() (already == cfg.BackupKey
	// != "") rather than duplicating that check as a separate bool. nil ->
	// reported as not encrypted (same as an unconfigured PIUMY_BACKUP_KEY).
	Backup *sessionbackup.Backuper

	// DefaultDispatchDebounce/DefaultMaxDispatchDebounce (T90,
	// ct-2026-08-28-1350) are cfg.DispatchDebounce/cfg.MaxDispatchDebounce —
	// the SAME values already threaded into capipush.Config, reused here
	// as the fallback GET /api/admin/dispatch-debounce reports before the
	// dashboard ever sets a live override (store.SettingCapipushDispatch
	// Debounce/MaxDispatchDebounce win once set — see capipush.isDebounced).
	// Zero here just means "read the code-level default at zero value" —
	// this pair is never itself the source of truth for what capipush
	// actually uses, only what to SHOW as "not yet touched from here".
	DefaultDispatchDebounce    time.Duration
	DefaultMaxDispatchDebounce time.Duration

	// PrincipalTerminalID (ct-2026-07-22-1301, M1) is cfg.DefaultTerminalID —
	// the SAME identity mcpserver.Deps.PrincipalTerminalID already gates
	// register_agent/set_agent_capi on. The `agents` table only ever holds
	// secondaries (both those tools explicitly reject this terminal_id);
	// GET /api/agents uses it to synthesize the principal's own row from the
	// KV capi-connector settings (SettingCAPIEndpoint/TerminalID/Pinpass)
	// instead of a real `agents` row. Empty -> principal omitted from the
	// list rather than shown with a blank identity.
	PrincipalTerminalID string

	// APIKey gates every /api/* request via the X-API-Key header or a
	// ?key= query param. Empty = open (dev only, LAN-bound deployment) —
	// same fail-OPEN-when-unset convention Piumy's restapi already used
	// (unlike MCP's Bearer auth, which is fail-closed): this is direct
	// owner admin from the LAN, not an agent-facing surface.
	APIKey string

	// SMTP is the boss's mail relay for email-channel recovery (recover.go).
	// SMTPSend is the actual network call, defaulting to smtp.SendMail
	// (stdlib) when nil — overridable in tests without a real TCP server.
	SMTP     SMTPConfig
	SMTPSend func(addr string, a smtp.Auth, from string, to []string, msg []byte) error

	// Resetter kicks the re-sync D4's "partir de 0" reset needs (POST
	// /api/admin/reset, ct-2026-07-22-2100) — see the Resetter interface's
	// doc. nil -> the reset still wipes the DB/media dir, just skips the kick
	// (same degrade-one-piece convention as every other optional Deps field).
	Resetter Resetter

	// Disconnecter unlinks WhatsApp (POST /api/admin/disconnect, M3,
	// ct-2026-07-22-2342) — see the Disconnecter interface's doc. nil ->
	// that endpoint 503s.
	Disconnecter Disconnecter

	// Reconnecter kicks off a new QR pairing after a Logout (POST
	// /api/admin/reconnect, Fix 2, ct-2026-07-23-0047) — see the
	// Reconnecter interface's doc. nil -> that endpoint 503s.
	Reconnecter Reconnecter

	// QRStatus backs GET /api/status's qr_code/qr_expires_at/qr_expired
	// fields (P2, ct-2026-07-24-0015) — see the QRStatus interface's doc.
	// nil -> those fields report zero values rather than 503ing.
	QRStatus QRStatus

	// HistorySyncProgress backs GET /api/status's history_sync_* fields
	// (items 1+2, ct-2026-07-23-0047) — see the HistorySyncProgress
	// interface's doc. nil -> those fields report the zero value (inactive,
	// nothing received) rather than 503ing the whole status endpoint, same
	// degrade-one-piece convention as every other optional Deps field.
	HistorySyncProgress HistorySyncProgress

	// ProfileStatus reads/writes the host number's own status/"About" text
	// (GET+POST /api/admin/profile-status, T92+T96) — see the ProfileStatus
	// interface's doc. nil -> both endpoints 503.
	ProfileStatus ProfileStatus

	// ProfilePhoto sets the host number's own WhatsApp profile photo (POST
	// /api/admin/profile-photo, T112c) — see the ProfilePhoto interface's
	// doc. nil -> the endpoint 503s.
	ProfilePhoto ProfilePhoto

	// MediaDir is where downloaded media files live on disk (cfg.MediaDir,
	// PIUMY_MEDIA_DIR) — D4's reset clears its CONTENTS (not the directory
	// itself) so orphaned files don't accumulate across repeated smoke-test
	// resets, since the media DB rows referencing them are wiped too. Empty
	// -> that step is skipped (no directory configured to clear).
	MediaDir string
}

// NewMux builds the HTTP mux serving the nudge + privileged admin +
// metering endpoints.
func NewMux(d Deps) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/events", d.auth(d.handleEvents))
	d.registerAdminRoutes(mux)
	d.registerMeteringRoutes(mux)
	d.registerReadRoutes(mux)
	d.registerDashboardRoutes(mux)
	d.registerI18nRoutes(mux)
	d.registerAuthRoutes(mux)
	d.registerRecoverRoutes(mux)
	return mux
}

// auth gates a route behind EITHER credential (ct-2026-07-19-1616 adds the
// second): a valid X-API-Key/?key= (programmatic — MCP/curl, unchanged
// behavior) OR a valid signed session cookie from the dashboard login
// (auth.go, human via browser). APIKey=="" still means fully open — that
// dev/LAN-only convention predates this ticket and isn't what it's scoped
// to change; closing it would also break exactly the curl-without-a-key
// workflows this ticket was told not to break.
func (d Deps) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.APIKey != "" {
			key := r.Header.Get("X-API-Key")
			if key == "" {
				key = r.URL.Query().Get("key")
			}
			if key == d.APIKey {
				h(w, r)
				return
			}
			if d.validSession(r) {
				h(w, r)
				return
			}
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": i18n.T(effectiveLang(d.Store), "server.unauthorized")})
			return
		}
		h(w, r)
	}
}

// handleEvents streams eventbus notifications as SSE — a heartbeat every
// 20s keeps a proxy/load balancer from deciding an idle connection is dead.
func (d Deps) handleEvents(w http.ResponseWriter, r *http.Request) {
	if d.Bus == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "events not available"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	ch, unsubscribe := d.Bus.Subscribe()
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// writeEvent is shared by the heartbeat and the real eventbus feed
	// below — same wire shape either way, so the dashboard's onmessage
	// handler needs no special-casing to tell them apart beyond Type.
	writeEvent := func(e eventbus.Event) bool {
		b, err := json.Marshal(e)
		if err != nil {
			return true // skip this one, connection stays open
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			// ct-2026-07-24-0527: a real event, not a bare SSE comment — a
			// comment line never fires EventSource.onmessage (spec
			// behavior), so the old ": keep-alive" was invisible to the
			// browser. As a real Type it still keeps a proxy/load-balancer
			// from timing out the connection, AND doubles as the
			// client-observable "I'm still here" the dashboard's reconnect
			// watchdog needs — a half-open TCP connection (e.g. after a
			// laptop sleep) never fires a socket error, silence is the
			// only signal available client-side.
			if !writeEvent(eventbus.Event{Type: "heartbeat", TS: time.Now().Unix()}) {
				return
			}
		case e, ok := <-ch:
			if !ok {
				return
			}
			if !writeEvent(e) {
				return
			}
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
