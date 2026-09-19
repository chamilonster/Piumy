package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"strconv"
	"time"
)

func (s *Store) KVGet(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM kv WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (s *Store) KVSet(key, val string) error {
	_, err := s.db.Exec(`INSERT INTO kv (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, val)
	return err
}

// Setting keys: KV-override names shared by the pipeline (readers) and
// restapi (writers), defined once here so neither side can typo a mismatch.
// env = default (config package); dashboard edits persist here; the core
// rereads them live wherever it matters (dispatch/read/action delay, media
// skip, media retention) so a change applies without a restart.
const (
	SettingMediaSkipVideoGroup = "media_skip_video_group"
	SettingMediaSkipVideoChat  = "media_skip_video_chat"
	SettingMediaSkipPhotoGroup = "media_skip_photo_group"
	SettingMediaSkipPhotoChat  = "media_skip_photo_chat"
	SettingMediaMaxMB          = "media_max_mb"

	SettingDispatchDelayMin = "delay_dispatch_min"
	SettingDispatchDelayMax = "delay_dispatch_max"
	SettingReadDelayMin     = "delay_read_min"
	SettingReadDelayMax     = "delay_read_max"
	SettingActionDelayMin   = "delay_action_min"
	SettingActionDelayMax   = "delay_action_max"

	// SettingChunkDelayMin/Max / SettingChunkMaxLen (T101, ct-2026-08-29-1651)
	// — same live-override pattern as the delays above, for the respiro
	// BETWEEN pieces of one split-up long reply (not between messages) and
	// the length at which a reply gets split in the first place.
	SettingChunkDelayMin = "delay_chunk_min"
	SettingChunkDelayMax = "delay_chunk_max"
	SettingChunkMaxLen   = "chunk_max_len"

	// SettingAvatarRecheckMin/Max (T17 Parte 3, ct-2026-08-05-1240) bound
	// the randomized window before a CACHED avatar is worth asking
	// WhatsApp about again — sampled independently per jid (governor.
	// DelayWindow.Random(), same mechanism as the delays above, days-scale
	// instead of seconds-scale). Citrino's explicit correction on the
	// first draft of this contract: a fixed interval (originally proposed
	// as "every 7 days") is a pattern, and patterns are what gets
	// detected — the whole point of a RANGE is that no two numbers ever
	// ask on the same cadence. See whatsmeow.Adapter.avatarRecheckWindow
	// for the code-level fallback defaults when unset.
	SettingAvatarRecheckMin = "avatar_recheck_min"
	SettingAvatarRecheckMax = "avatar_recheck_max"

	// SettingHistoryFreshPairGraceWindow (M2, ct-2026-07-22-2342) overrides
	// how long after a FRESH pairing (never a routine reconnect —
	// whatsmeow's pairLoop is the only caller that stamps pairedAt) the
	// dashboard's "still syncing" signal (HistorySyncStatus) stays true by
	// default, before a real passive chunk extends it. No env layer: this
	// only matters right after a re-pairing, rare enough that a
	// dashboard-editable override (falling back to a Go constant in
	// internal/whatsmeow) is enough.
	SettingHistoryFreshPairGraceWindow = "history_fresh_pair_grace_window"

	SettingRateLimitPerMin = "rate_limit_per_min"
	SettingRateLimitPerDay = "rate_limit_per_day"

	// SettingCapipushSwampedAt / SettingCapipushSwampedWindow (S3,
	// ct-2026-07-30-030948) — the backpressure gate's threshold + recency
	// window, moved out of code per CLAUDE.md's "cero hardcode". SwampedAt
	// is the recent (non-boss) pending count at/above which capipush pauses
	// dispatch to every non-boss chat; SwampedWindow bounds how far back
	// "recent" reaches (old debt never counts). See capipush.Config for the
	// code-level fallback defaults when unset.
	SettingCapipushSwampedAt     = "capipush_swamped_at"
	SettingCapipushSwampedWindow = "capipush_swamped_window"

	// SettingCapipushMaxRedispatch / SettingCapipushDispatchStaleAfter (S4b,
	// ct-2026-07-30-1255) — two of the "three clocks" (sweep interval,
	// redispatch cap, stale-dispatch reclaim) calibrated together. MaxRedispatch
	// is how many times a still-unhandled message gets auto-redispatched
	// (with Fibonacci backoff between attempts, see capipush.redispatchBackoff)
	// before capipush holds it. DispatchStaleAfter is how long a dispatch can
	// sit bound-but-stuck in mcpserver.Gate before the terminal is freed —
	// applied live via Gate.SetStaleAfter each capipush sweep. See
	// capipush.Config for the code-level fallback defaults when unset.
	SettingCapipushMaxRedispatch      = "capipush_max_redispatch"
	SettingCapipushDispatchStaleAfter = "capipush_dispatch_stale_after"

	// SettingCapipushDispatchDebounce / SettingCapipushMaxDispatchDebounce
	// (T90, ct-2026-08-28-1350, boss: "se estan demorando mucho... falta esa
	// perilla tambien, tiempo de espera pre inyeccion") — the ENTRY-side wait
	// before capipush hands a chat's burst to the agent (never the outbound
	// anti-ban delays, PIUMY_DELAY_*, a different knob entirely).
	// DispatchDebounce is the silence window (0 is a legitimate choice —
	// "dispatch as soon as it arrives", capipush.isDebounced returns early
	// rather than treating it as unset); MaxDispatchDebounce is the hard
	// ceiling that forces a burst out even if the chat stays active. See
	// capipush.Config for the code-level fallback defaults when unset here.
	SettingCapipushDispatchDebounce    = "capipush_dispatch_debounce"
	SettingCapipushMaxDispatchDebounce = "capipush_max_dispatch_debounce"

	// SettingActivationSweepWindow (S8, ct-2026-07-30-031126) overrides
	// SetActive's own backlog-sweep reach on a genuine inactive→active
	// transition: messages OLDER than `now - window` get marked handled
	// (never dispatched); anything within the window survives and
	// dispatches normally. Citrino's own catch: sweeping everything up to
	// `now` (the original S8 cut) swallowed the EXACT message that
	// prompted the activation — the boss's real flow ("atendé a este
	// número") is spot a message, THEN activate, seconds to minutes later.
	// See chat.go's SetActive for the code-level fallback default.
	SettingActivationSweepWindow = "activation_sweep_window"

	// SettingIdentityAutoReconcile (S13, ct-2026-07-30-1835) gates
	// whatsmeow.Adapter.ReconcileIdentitiesSweep — default false/off,
	// deliberately: the regla dura is "nada destructivo sin OK explícito
	// del boss y con backup verificado", and ReconcileIdentities deletes
	// @lid rows. The sweep's own goroutine is wired to run from boot (same
	// pattern as every other periodic sweep in this codebase), but reads
	// this setting on every tick and is a pure no-op while it's false — so
	// shipping the sweep does not, by itself, merge or delete anything.
	// Flipping this to true (after the boss's OK, once the one-time
	// backfill for today's 557 pairs has ALSO run separately, with a
	// verified backup) is what makes it live for pairs learned from then on.
	SettingIdentityAutoReconcile = "identity_auto_reconcile"

	// SettingKillSwitch (T19, ct-2026-08-05-1249) persists the anti-ban
	// emergency stop — set_kill_switch (mcpserver/restapi) flips it
	// alongside governor.SetKill/state.SetMuted, and main.go's
	// restoreKillSwitch reads it BEFORE ctrl.Start() (the one call that can
	// actually make the pipeline send) so a restart can never silently
	// release a brake that was on for a real reason. governor.Limiter/
	// state.Manager themselves stay memory-only on purpose — this is the
	// one bit that has to survive a crash/power-cut/update, not a general
	// "persist all governor state" precedent.
	SettingKillSwitch = "kill_switch"

	// MCP anti-flood — own namespace, deliberately separate from the
	// rate-limit keys above (those are the WhatsApp-outbound governor;
	// these are the MCP-inbound flood guard).
	SettingMCPGuardRatePerMin     = "mcpguard_rate_per_min"
	SettingMCPGuardEmitRatePerMin = "mcpguard_emit_rate_per_min"
	SettingMCPGuardBlockThreshold = "mcpguard_block_threshold"
	SettingMCPGuardBlockCooldown  = "mcpguard_block_cooldown"

	// Rules hierarchy: the "by type" tier a GROUP with no chat-particular
	// rules inherits. See EffectiveRules. T79 (ct-2026-08-27-2034) removed
	// its two siblings — SettingRulesTypeIndividual (dead since M5, the
	// contact/new-number origin split replaced it) and SettingRulesDefault
	// (the global catch-all, "no tiene a donde ir" once the partition above
	// it is exhaustive) — both existed only as write-and-never-read KV keys
	// by the time they went. The orphaned rows stay in an existing DB
	// (nothing migrates them out — reversible if ever needed), just nothing
	// in code names either key anymore.
	SettingRulesTypeGroup = "rules_type_group"

	// SettingDashPassHash is the dashboard login's bcrypt hash — written by
	// the owner-scoped MCP tool reset_dashboard_password AND by the
	// dashboard's own POST /api/admin/password (ct-2026-07-19-1616, S1d),
	// read on every login attempt. Empty/unset means no password has ever
	// been set — restapi/auth.go seeds it with bcrypt("piumy") on first use
	// (the documented default, admin/piumy).
	SettingDashPassHash = "dash_pass_hash"

	// SettingDashSessionSecret is the HMAC secret signing the dashboard's
	// session cookie (restapi/auth.go, ct-2026-07-19-1616, S1d) — lazily
	// generated by RotateDashSessionSecret on first use. Rotating it
	// invalidates every previously issued cookie in one write: both
	// POST /api/admin/password and reset_dashboard_password call it after
	// changing the hash, so a password change (planned or emergency) always
	// ends every existing browser session, not just the one that changed it.
	SettingDashSessionSecret = "dash_session_secret"

	// SettingDashRecoveryEmail is the boss's own configured recovery-email
	// address (restapi/recover.go, ct-2026-07-19-1716, S1e-2) —
	// POST /api/auth/recover{method:"email"} sends the code here. Empty
	// means email recovery isn't configured; that method then silently
	// no-ops, same generic response either way (no state leak — same rule
	// S1e-1 already applies when there's no is_boss chat to WhatsApp).
	SettingDashRecoveryEmail = "dashboard_recovery_email"

	// cAPI connector — persisted by the dashboard, applied in-hot without
	// restart. Env vars (PIUMY_CAPI_*) are the startup default; these KV
	// entries are the runtime override (same pattern as rate-limit overrides).
	// ponytail: pinpass stored as plaintext at-rest (MVP, local deployment);
	// encrypted at-rest = post-MVP if needed.
	SettingCAPIEndpoint   = "capi_endpoint"
	SettingCAPITerminalID = "capi_terminal_id"
	SettingCAPIPinpass    = "capi_pinpass"
	// SettingPrincipalName (ct-2026-07-29, agentes paso 1): the principal's
	// own display name — never had a home before (the tab showed
	// "(sin nombre)"). Secondaries already have Agent.Name (agents table,
	// register_agent/set_agent_capi); this is the principal's equivalent,
	// kept in KV like the rest of its cAPI config since it has no `agents`
	// row (unification is API-shape only, not storage — see restapi's
	// handleUpsertAgent doc).
	SettingPrincipalName = "principal_name"

	// M5 (ct-2026-07-22-1903) — defaults de atención por origen: 2 pares
	// (modo + reglas) en la cabecera del dashboard, uno para números
	// desconocidos ("mensajes nuevos") y otro para contactos de la agenda.
	// Boss verbatim: "si te habla un chat nuevo pero es contacto, tiene
	// prioridad ser contacto ya guardado" — contacto gana, ver EffectiveRules
	// (reglas, cableado ya) y el checkpoint de diseño pendiente con Citrino
	// (modo, toca el pipeline del core, todavía sin cablear el efecto).
	SettingConfigLevelDefaultNew     = "config_level_default_new"
	SettingConfigLevelDefaultContact = "config_level_default_contact"
	SettingRulesDefaultNewNumber     = "rules_default_new_number"
	SettingRulesDefaultContact       = "rules_default_contact"

	// SettingAgentDefaultNewNumber/SettingAgentDefaultContact (T71,
	// ct-2026-08-27-1410) — agente por defecto por origen, MISMA jerarquía
	// KV/precedencia que las 2 keys de rules de arriba (EffectiveAgentDefault
	// en chat.go). Ruteo, no reglas: un chat sin agent_exclusive particular
	// que caiga en ese origen se despacha al agente elegido acá.
	SettingAgentDefaultNewNumber = "agent_default_new_number"
	SettingAgentDefaultContact   = "agent_default_contact"

	// SettingAgentDefaultGroup/SettingAgentDefaultBoss (T72, ct-2026-08-27-1625)
	// — 2 tipos más en la misma jerarquía: grupos y el chat del dueño. Boss
	// verbatim: "el agente principal se asigna al boss" — sin configurar,
	// EffectiveAgentDefault devuelve "" para boss y capipush.dispatch cae a
	// PortFallback (el principal), igual que hoy.
	SettingAgentDefaultGroup = "agent_default_group"
	SettingAgentDefaultBoss  = "agent_default_boss"

	// SettingIdentity (T13, ct-2026-08-05-123147, boss verbatim: "en identity
	// va el asistente de que, una empresa de x cosa, una persona ocupada,
	// etc.") — answers "assistant OF WHAT", the one field the other 4 rules
	// tiers all sit under (pestaña Rules shows it first). Same settings
	// tier/CRUD pattern as the 4 rules keys above — own GET/POST endpoint,
	// no wiring into EffectiveRules' resolution chain (that's rules text
	// specifically; identity is a separate, single always-on field).
	SettingIdentity = "identity"

	// SettingLanguage (T153 etapa 1, ct-2026-09-08-1656) is the boss's
	// manual override for the dashboard/tray/avisos language — empty means
	// "never chosen", restapi's effectiveLang then falls back to
	// i18n.Detect() (the OS locale). Manual choice always wins once set.
	SettingLanguage = "language"
)

// SettingBool returns a KV-overridden boolean setting, falling back to def
// when unset — never an error, so callers on a message-handling path (the
// media skip check) never need to special-case a DB hiccup.
func (s *Store) SettingBool(key string, def bool) bool {
	v, err := s.KVGet(key)
	if err != nil || v == "" {
		return def
	}
	return v == "1" || v == "true"
}

// SetSettingBool persists a boolean setting.
func (s *Store) SetSettingBool(key string, b bool) error {
	if b {
		return s.KVSet(key, "1")
	}
	return s.KVSet(key, "0")
}

// SettingDuration is SettingBool for a time.Duration (Go's duration string
// format, e.g. "1s"). Falls back to def if unset or unparseable.
func (s *Store) SettingDuration(key string, def time.Duration) time.Duration {
	v, err := s.KVGet(key)
	if err != nil || v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

// SetSettingDuration persists a duration setting.
func (s *Store) SetSettingDuration(key string, d time.Duration) error {
	return s.KVSet(key, d.String())
}

// SettingInt is SettingBool for an integer count (rate limits, media MB).
// Falls back to def if unset or unparseable.
func (s *Store) SettingInt(key string, def int) int {
	v, err := s.KVGet(key)
	if err != nil || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// SetSettingInt persists an integer setting.
func (s *Store) SetSettingInt(key string, n int) error {
	return s.KVSet(key, strconv.Itoa(n))
}

// SetCAPIConnector persists the 3 cAPI connector settings together — the
// single write path both restapi's POST /api/admin/capi-connector and the
// set_capi_connector MCP tool call, so the re-cableo logic exists once.
func (s *Store) SetCAPIConnector(endpoint, terminalID, pinpass string) error {
	for key, val := range map[string]string{
		SettingCAPIEndpoint:   endpoint,
		SettingCAPITerminalID: terminalID,
		SettingCAPIPinpass:    pinpass,
	} {
		if err := s.KVSet(key, val); err != nil {
			return err
		}
	}
	return nil
}

// RotateDashSessionSecret generates a fresh random HMAC secret and persists
// it as SettingDashSessionSecret — every dashboard session cookie signed
// with the previous secret stops verifying immediately. Used both to seed
// the secret lazily on first use and to invalidate all sessions on a
// password change (planned or emergency reset).
func (s *Store) RotateDashSessionSecret() error {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	return s.KVSet(SettingDashSessionSecret, base64.StdEncoding.EncodeToString(secret))
}
