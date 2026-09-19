// send_message, draft, and silent_act — the three ways ready ->
// {sent, drafted, silenced} (F4-DESIGN §4). send_message/draft share every
// check up to the point where they diverge (enqueue vs. draft), factored
// into validateSend. silent_act (S11, ct-2026-07-30-1619) has no content to
// validate — it consumes the SAME gate turn via the SAME gate.Consume call,
// so a chat the agent deliberately doesn't answer releases the terminal
// exactly as fast as one it does.
package mcpserver

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"piumy-gateway/internal/mediautil"
	"piumy-gateway/internal/store"
)

// maxSendsPerDispatch caps how many send_message/draft calls one dispatch
// may deliver to its OWN chat before validateSend finally refuses with
// "already consumed" — T167 (ct-2026-09-17-1255, boss verbatim: "quiero que
// lo quiten o aumenten a 4 mensajes"; his own number, taken as-is, not a more
// cautious version of it). Lets one reply land as several pieces (text +
// sticker, two short messages as a person would write) without a fresh
// dispatch. The single place this number lives — change it here, nowhere
// else, if it ever needs to be a different number.
//
// Deliberately NOT the gate's one-shot Consume/InFlight lifecycle (see
// gate.go's RecordSend doc): Consume still retires the dispatch, and
// InFlight still frees the terminal for OTHER chats, on the very FIRST send
// — a would-be shared counter across send_message/draft/silent_act was tried
// and rejected (Tourmaline's own repro to Citrino, T167): silent_act is
// nearly always a turn's ONLY call, and counting it here would have left
// InFlight true for up to dispatchStaleAfter after every silent-only turn,
// reintroducing the exact backlog S4b exists to prevent. silent_act stays
// completely outside this cap — one call, always final, as before.
const maxSendsPerDispatch = 4

// validateSend runs the F4b gate check + the policy_version gate + Piumy's
// 6 base checks shared by send_message and draft. Returns the resolved
// chat on success ("" error), or a user-facing error message to return
// immediately. draft used to skip the policy_version check (send_message
// had its own copy) — found in F4c audit: draft's own description claims
// "same guardrails as send_message", which wasn't true. Folded in here so
// both tools enforce it identically, in one place.
//
// active/bound (T147, ct-2026-09-07): read ONCE by the caller, before
// calling this — never gate.Active(termID) again internally. The handler
// reuses this SAME snapshot for markTS, the mark-handled scope, and the
// Consume identity below. Two independent reads of gate.Active() within one
// handler (this function's own internal read used to be the first of two —
// send_message/draft each took a SECOND one later) is exactly the TOCTOU
// T147 was born from: nothing ties what got VALIDATED here to what's still
// there by the time the handler finishes.
func validateSend(ctx context.Context, d Deps, active ActiveDispatch, bound bool, to, model, policyVersion string) (store.Chat, string) {
	termID := terminalIDFromContext(ctx)
	isPrincipal := d.PrincipalTerminalID != "" && termID == d.PrincipalTerminalID
	// T64 (ct-2026-08-11-1627, boss verbatim: "pero que el agente escriba
	// primero es un peduido que llevo mas de 5 dias pidiendolo... y ustedes
	// lo returcen y lo vuelven a quitar") — any registered agent may INITIATE
	// a conversation, no dispatch required first. This is the third time this
	// exact request landed: 2026-07-13 opened it, 2026-07-18's "candado
	// versión segura" narrowed it back down to principal+is_boss/active, and
	// this closes it for good — not a wider narrow version, no version at
	// all. isPrincipal/initiateAuthorized (the old exemption) are gone
	// entirely, not extended to everyone: there is nothing left to be
	// exempt FROM.
	//
	// What stays, because it's a DIFFERENT gate the boss explicitly said
	// this doesn't touch ("el gate por nivel de despacho... no es lo que el
	// dueño está pidiendo abrir"): an agent that DOES have an active
	// dispatch is still bound by it — Ready must be true (ST-A, below), and
	// a caution/danger dispatch still can't be redirected to a chat other
	// than the one it was actually dispatched for (anti-leakage). Only the
	// "no dispatch at all" case changes, from DENY to allowed.
	if bound {
		// T150 (ct-2026-09-07-1839): Consume() leaves the dispatch bound in
		// byTerminal forever (marked done, Ready=false) — "it stays gated
		// until a NEW get_instructions binds it" (gate.go's own Consume doc).
		// The Ready lock below is about THAT dispatch's own chat: reusing a
		// consumed (or never-unlocked) dispatch to act on the chat it was
		// actually for stays rejected. A DIFFERENT chat is a different
		// question — T64's "any registered agent may INITIATE, no dispatch
		// required" must hold even with a stale dispatch sitting on this
		// terminal from something else entirely; before this fix a terminal
		// that ever consumed ONE dispatch was locked out of send_message to
		// ANY chat until a fresh dispatch registered (the principal's own
		// live incident: consumed one dispatch, then couldn't write to an
		// unrelated group where the owner was waiting).
		sameChat := to == active.ChatJID
		if !active.Ready {
			if sameChat {
				// T147 (ct-2026-09-07): "locked" alone used to mean either
				// "never touched" OR "already consumed" — indistinguishable,
				// and the second one is exactly what a stale caller hits
				// after this SAME identity-checked Consume fix below. Say
				// which.
				if active.Done {
					// T167 (ct-2026-09-17-1255): Consume already retired
					// this dispatch on its FIRST send (unchanged — see
					// gate.go's RecordSend doc), but the boss's own case
					// (message, then a sticker) needs MORE than one send to
					// land. Up to maxSendsPerDispatch total to THIS dispatch's
					// own chat fall through instead of refusing; the (N+1)th
					// still gets the same "already consumed" it always did.
					if active.SendCount >= maxSendsPerDispatch {
						return store.Chat{}, "locked: this dispatch was already consumed — call get_instructions for a new one"
					}
				} else {
					return store.Chat{}, "locked: call get_instructions -> unlock -> remember/skip before send_message"
				}
			}
			// to != active.ChatJID: falls through to the checks below,
			// exactly like the bound=false path — a stale dispatch
			// elsewhere on this terminal has no say over an unrelated chat.
		} else if active.Level != LevelBoss && !sameChat {
			// ST-A security fix (ct-2026-07-11-0740): Ready is required for
			// EVERY level, boss included — RegisterDispatch starts boss
			// dispatches gateReady (skips the checkpoint by design) and
			// Consume marks any dispatch gateDone (Ready=false) regardless
			// of level, so a boss dispatch that already sent once can no
			// longer send again to ITS OWN chat without a fresh dispatch
			// (enforced by the sameChat branch above). This anti-leakage
			// redirect check only applies while the dispatch is genuinely
			// still active (Ready) — a caution/danger dispatch can't abuse
			// its current live turn to message a third chat, but once that
			// turn is over, it's no longer "in the middle of" anything.
			return store.Chat{}, "refused: this dispatch is unlocked for " + active.ChatJID + ", not " + to + " — get_instructions for the right chat first"
		}
	}
	if d.State != nil && d.State.Snapshot().Muted {
		return store.Chat{}, "muted: message not sent"
	}
	// ct-2026-07-13-1822: principal puede omitir policy_version (es opcional para él).
	// No-principal: siempre requerido (gate caution/danger intacto).
	if !isPrincipal || policyVersion != "" {
		if _, current := decisionPolicy(d.PolicyPath); policyVersion != current {
			return store.Chat{}, "stale/missing policy_version — call get_decision_policy first"
		}
	}
	if !strings.Contains(to, "@") {
		return store.Chat{}, "to must be a full JID (e.g. 55500000001@c.us), not a bare number — copy it from list_chats/get_queue/resolve_chat"
	}
	c, ok, err := d.Store.GetChat(to)
	if err != nil {
		return store.Chat{}, err.Error()
	}
	if !ok {
		// T64 (ct-2026-08-11-1627, boss verbatim: "hazte cargo de estos 3
		// numertos, la ia se registra por mcp y atiende a esos numeros") — a
		// number with no chat row yet used to bounce here before the agent
		// ever got a chance to write to it. TouchChat is the SAME upsert
		// whitelist-add already reuses for this exact purpose (read.go) — no
		// new path. Creating the row grants nothing by itself: it still needs
		// to clear EffectiveRules below like any other chat, so this only
		// removes "no row = give up immediately", not the rules law.
		if err := d.Store.TouchChat(to, "", time.Now().Unix()); err != nil {
			return store.Chat{}, err.Error()
		}
		c, ok, err = d.Store.GetChat(to)
		if err != nil {
			return store.Chat{}, err.Error()
		}
		if !ok {
			return store.Chat{}, "error: no rules on this chat"
		}
	}
	if c.ClaimedBy != "" && c.ClaimedBy != model {
		return store.Chat{}, "refusing to send: " + to + " is claimed by another agent (" + c.ClaimedBy + ") until " + time.Unix(c.ClaimedUntil, 0).UTC().Format(time.RFC3339) + " — wait, or claim_chat it yourself once expired"
	}
	effRules, err := d.Store.EffectiveRules(to)
	if err != nil {
		return store.Chat{}, err.Error()
	}
	if effRules == "" {
		return store.Chat{}, "error: no rules on this chat"
	}
	// T65 (ct-2026-08-11-1642, boss verbatim: "el blacklist es el sistema
	// de ignorar, ahi pueden sumar los condados que no deben ser por
	// defecto") — everyone is allowed by default; blacklist/ignored is the
	// ONE brake, and it now applies to every chat, not just groups (the
	// isGroupJID condition this used to have was the bug: a 1:1 chat the
	// owner ignored still got sent to). The router whitelist gate that used
	// to sit here is gone entirely, not narrowed — the owner asked for it
	// removed twice before and got a softer version of the same lock both
	// times; this is the third ask, taken literally.
	//
	// store.ChatIsOff, not a local status=="ignored"||status=="blacklist"
	// check (T67, ct-2026-08-11-172135): pending.go's dispatch queries had
	// their OWN copy of this exact condition, minus 'blacklist' — a chat
	// blacklisted here still dispatched there. One shared definition is
	// what keeps that from happening again.
	if store.ChatIsOff(c.Status) {
		return store.Chat{}, "refusing to send: " + to + " is " + c.Status + " — the owner must change its status first"
	}
	return c, ""
}

// markHandledForDispatch closes chatJID's pending messages up to ts —
// scoped to sender via MarkHandledBeforeForSender when chatJID is a GROUP
// and sender is known (T108, ct-2026-09-01-1413); otherwise (a 1:1 chat, or
// a group dispatch/draft with no sender recorded — never happens via the
// live gate/dueChats path, but never assumed) falls back to the whole-chat
// MarkHandledBefore, same as every caller had before this contract. A group
// burst is per-speaker since T108 (capipush.dueChats) — closing the whole
// chat instead would re-open the exact defect this contract exists to fix:
// answering one participant marking the rest handled without anyone having
// answered them.
func markHandledForDispatch(d Deps, chatJID, sender string, ts int64) error {
	if sender != "" && store.IsGroupJID(chatJID) {
		return d.Store.MarkHandledBeforeForSender(chatJID, sender, ts)
	}
	return d.Store.MarkHandledBefore(chatJID, ts)
}

// markDispatchChatIfDifferent also closes the ACTIVE DISPATCH's own chat
// when it differs from `to` (T33, ct-2026-08-06-1526 — a real case the boss
// hit live: ordered over WhatsApp to write a third number, change its
// rules, and note memory/context; his own message came back a second time).
// send_message/draft only ever marked `to` handled — when the dispatch
// being answered is a DIFFERENT chat than the one just written to, that
// dispatch's own inbound message was never marked, so the sweep found it
// still pending once the terminal freed up and re-dispatched it. Not
// introduced by T31 (ct-2026-08-06-0244): the principal-terminal and
// boss-dispatch bypasses in levelGateMiddleware already let send_message/
// draft target a chat other than the active dispatch's before that — T31
// made it the everyday case (the owner routinely asking to act on a third
// chat) instead of a rare one. silent_act never had this bug: it has no
// `to` at all, always targets active.ChatJID.
//
// Same bound as silent_act's own MarkHandledBefore call — active.BurstMaxTS,
// never `now` — a message that arrived in the dispatch's chat AFTER the
// burst must stay pending (T33's own "no marques de más" requirement); this
// only closes what was actually dispatched. No-op when there's no bound
// dispatch, or when it's the same chat as `to` (today's normal case,
// already marked by the caller — this must never double-write it).
func markDispatchChatIfDifferent(d Deps, active ActiveDispatch, bound bool, to string) {
	if !bound || active.ChatJID == to {
		return
	}
	if err := markHandledForDispatch(d, active.ChatJID, active.Sender, active.BurstMaxTS); err != nil {
		log.Printf("mcpserver: mark_handled_before (dispatch chat) %s: %v", active.ChatJID, err)
	}
}

// dispatchSpeaker (T108, ct-2026-09-01-1413) resolves which speaker `to`
// should be scoped to: the active dispatch's own sender, but ONLY when `to`
// IS that dispatch's chat — a caller writing to a DIFFERENT chat (T33) has
// no known speaker there (the agent is acting on its own initiative, not
// answering a particular participant), so it stays unscoped (whole-chat,
// unchanged from before T108).
func dispatchSpeaker(active ActiveDispatch, bound bool, to string) string {
	if bound && to == active.ChatJID {
		return active.Sender
	}
	return ""
}

// saveOutboundImage decodes send_message's optional image_data_url param
// into a saved JPEG file (T122, ct-2026-09-02-2045) — mediautil.
// DecodeDataURL + EnsureJPEG, the SAME conversion set_group_icon/
// set_profile_photo already use (F4d/T111), so "any common format in, JPEG
// out" isn't reimplemented here. path is "" and errMsg non-empty on any
// failure (bad data_url, undecodable image, no MediaDir configured) —
// never a panic, never a silent partial enqueue.
func saveOutboundImage(d Deps, dataURL string) (path, mime, kind string, errMsg string) {
	if d.MediaDir == "" {
		return "", "", "", "cannot send an image: no media directory configured (PIUMY_MEDIA_DIR)"
	}
	raw, _, err := mediautil.DecodeDataURL(dataURL)
	if err != nil {
		return "", "", "", "invalid image_data_url: " + err.Error()
	}
	jpegBytes, err := mediautil.EnsureJPEG(raw)
	if err != nil {
		return "", "", "", "invalid image_data_url: " + err.Error()
	}
	path, err = mediautil.SaveOutboundMedia(d.MediaDir, jpegBytes, ".jpg")
	if err != nil {
		return "", "", "", "could not save image: " + err.Error()
	}
	return path, "image/jpeg", "photo", ""
}

// saveOutboundAudio decodes send_message's optional audio_data_url param
// into a saved OGG/Opus file (T123, ct-2026-09-02-2121) — VALIDATED, never
// converted (converting to Opus needs libopus/CGO, which would break the
// CGO_ENABLED=0 build for all 6 targets — the whole reason this contract
// exists is that CleverCoder's talk() already delivers Opus, so there is
// nothing to convert). A payload that isn't real OGG/Opus (checked by
// mediautil.IsOggOpus's magic bytes, never a caller-declared mime) is
// rejected outright — sending it anyway would reach WhatsApp as a broken
// or unplayable attachment, read as "Piumy is failing" when the actual
// mistake was the caller's format.
//
// waveform, if non-nil, is send_message's own audio_waveform param —
// already validated by the caller (mediautil.WaveformFromInts) — saved
// alongside the audio as a sidecar (T128 iteration 2, ct-2026-09-03-0133).
// Best-effort: a sidecar write failure is logged, never fails the send —
// SendAudio falls back to computing its own waveform either way.
func saveOutboundAudio(d Deps, dataURL string, waveform []byte) (path, mime, kind string, errMsg string) {
	if d.MediaDir == "" {
		return "", "", "", "cannot send audio: no media directory configured (PIUMY_MEDIA_DIR)"
	}
	raw, _, err := mediautil.DecodeDataURL(dataURL)
	if err != nil {
		return "", "", "", "invalid audio_data_url: " + err.Error()
	}
	if !mediautil.IsOggOpus(raw) {
		return "", "", "", "invalid audio_data_url: expected OGG/Opus (WhatsApp's voice-note format) — CleverCoder's talk() tool with entry_id+host_path delivers audio already in this format; talk(save_to=...) does NOT (a plain uncompressed .wav) and is not usable here"
	}
	path, err = mediautil.SaveOutboundMedia(d.MediaDir, raw, ".ogg")
	if err != nil {
		return "", "", "", "could not save audio: " + err.Error()
	}
	if len(waveform) > 0 {
		if err := mediautil.SaveWaveformSidecar(d.MediaDir, raw, waveform); err != nil {
			log.Printf("mcpserver: save waveform sidecar: %v", err)
		}
	}
	return path, "audio/ogg; codecs=opus", "audio", ""
}

func addSendTools(s *server.MCPServer, d Deps, gate *Gate, tracker *agentTracker) {
	// ── send_message ─────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("send_message",
		mcp.WithDescription("Queue a message to send over WhatsApp. The gateway dispatches it while respecting the anti-ban governor (it is not sent instantly). 'to' must be a full JID (copy it from list_chats/get_queue/resolve_chat), not a bare phone number. Requires the current policy_version from get_decision_policy — read that FIRST, every time; a stale or missing value is rejected. LAW: rejected with \"error: no rules on this chat\" if the chat has no EFFECTIVE rules (get_chat's rules field) — the agent never acts without rules. A WhatsApp group additionally needs a non-\"ignored\" status. If the chat's confirmation_mode is \"always\", this creates a draft instead of sending (owner approval required) — use the draft tool to do that deliberately in \"discretion\" mode. To send a photo, pass image_data_url — a data: URL, e.g. data:image/png;base64,... (any common image format; converted to JPEG automatically). To send a voice note, pass audio_data_url instead — MUST already be OGG/Opus (WhatsApp's own voice-note format; CleverCoder's talk() tool with entry_id+host_path delivers audio in this format already — talk(save_to=...) gives a plain .wav and will be rejected). Audio is never converted, only validated. message becomes the caption (photo) or is stored with the item but not shown in WhatsApp's own voice-note bubble (audio has no caption slot there) — may be empty either way. Pass at most one of image_data_url/audio_data_url. The media still goes through the outbox/governor/confirmation gate exactly like a text message — it is never sent directly. You may call this (or draft) again in the SAME turn, no new get_instructions needed, to complete one reply with more than one piece — e.g. a text message, then a sticker-like image — up to a few calls total per dispatch; don't use this to insist on a chat that already had the last word and didn't answer (see list_chats/get_chat)."),
		mcp.WithString("to", mcp.Required(), mcp.Description("Destination JID, e.g. 55500000001@c.us")),
		mcp.WithString("message", mcp.Required(), mcp.Description("Text to send, or the photo's caption when image_data_url is given (may be empty)")),
		mcp.WithString("model", mcp.Required(), mcp.Description("Which model is sending this — required so every reply is attributable")),
		mcp.WithString("policy_version", mcp.Description("Hash from get_decision_policy — call that tool first. Required for non-principal terminals; the principal (DefaultTerminalID) may omit it.")),
		mcp.WithString("image_data_url", mcp.Description("Optional: a data: URL to send as a photo, e.g. data:image/jpeg;base64,... (any common image format — converted to JPEG automatically). Omit for a plain text message.")),
		mcp.WithString("audio_data_url", mcp.Description("Optional: a data: URL to send as a WhatsApp voice note — MUST be OGG/Opus already (not converted; CleverCoder's talk() with entry_id+host_path delivers this format). Mutually exclusive with image_data_url.")),
		mcp.WithNumber("audio_seconds", mcp.Description("Optional: the voice note's duration in seconds, if you already know it (never computed here). Omit to send without a shown duration — never guessed.")),
		mcp.WithArray("audio_waveform", mcp.Description("Optional: exactly 64 integers, each 0-100 — the voice note's waveform, already measured from the ORIGINAL uncompressed audio. Better than Piumy's own fallback (a guess from the already-compressed Opus file's packet sizes): use this when you have it. Ignored unless audio_data_url is also given. Malformed input (wrong length, any value outside 0-100) is silently discarded — Piumy computes its own instead, the send never fails because of this."), mcp.Items(map[string]any{"type": "integer"}))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			to, err := r.RequireString("to")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			policyVersion := r.GetString("policy_version", "")
			model, err := r.RequireString("model")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			// T147 (ct-2026-09-07): read ONCE, reused for validation, marking,
			// and Consume's identity below — see validateSend's own doc.
			termID := terminalIDFromContext(ctx)
			active, bound := gate.Active(termID)
			c, errMsg := validateSend(ctx, d, active, bound, to, model, policyVersion)
			if errMsg != "" {
				return mcp.NewToolResultError(errMsg), nil
			}
			// H6 hardening (ct-2026-07-10-0540): refuse outright while the
			// gateway is disconnected — enqueueing anyway just puts the
			// message in the outbox with no ETA, and the agent reads
			// "queued for sending" as success when it may never go out
			// (deauthed/banned session, see internal/whatsmeow's disconnect
			// handling). draft is unaffected: it never sends, connectivity
			// doesn't matter for holding a draft.
			if d.Gateway != nil && !d.Gateway.Connected() {
				return mcp.NewToolResultError("refusing to send: gateway is disconnected — the message would sit in the outbox with no ETA. Wait for reconnect or escalate to the owner."), nil
			}
			msg, err := r.RequireString("message")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			// T122/T123 (ct-2026-09-02-2045 / -2121): decode+validate/convert
			// +save happens HERE, at ingestion, before either branch below —
			// a draft and an enqueued item both just need the resulting
			// path/mime/kind/seconds, never the raw bytes again.
			imageDataURL := r.GetString("image_data_url", "")
			audioDataURL := r.GetString("audio_data_url", "")
			if imageDataURL != "" && audioDataURL != "" {
				return mcp.NewToolResultError("send_message: pass at most one of image_data_url or audio_data_url, not both"), nil
			}
			var mediaPath, mediaMime, mediaKind string
			var mediaSeconds int
			switch {
			case imageDataURL != "":
				var imgErr string
				mediaPath, mediaMime, mediaKind, imgErr = saveOutboundImage(d, imageDataURL)
				if imgErr != "" {
					return mcp.NewToolResultError(imgErr), nil
				}
			case audioDataURL != "":
				var audioErr string
				waveform, _ := mediautil.WaveformFromInts(r.GetIntSlice("audio_waveform", nil))
				mediaPath, mediaMime, mediaKind, audioErr = saveOutboundAudio(d, audioDataURL, waveform)
				if audioErr != "" {
					return mcp.NewToolResultError(audioErr), nil
				}
				mediaSeconds = int(r.GetFloat("audio_seconds", 0))
			}

			now := time.Now().Unix()
			// T147 (ct-2026-09-07): burstMaxTS — never `now`, never an
			// isPrincipal exception. A handler that never opened a dispatch
			// (bound=false, T64's own initiate-without-one path) has no
			// burst to speak of and marks NOTHING as handled below — "no
			// abriste un despacho, no tenés derecho a declarar leídos
			// mensajes que nadie leyó" (Citrino). 0 is AddDraftWithConfirmer's
			// own documented sentinel for "no burst" (draft.go).
			var burstMaxTS int64
			if bound {
				burstMaxTS = active.BurstMaxTS
			}

			// confirmation_mode (F4-DESIGN §4): "always" never sends
			// directly, fail-safe by code — creates a draft for the owner
			// to approve instead. "none"/"discretion" (or unset) send as
			// always.
			if c.ConfirmationMode == "always" {
				speaker := dispatchSpeaker(active, bound, to)
				// T122: a photo held for confirmation carries its media
				// fields too — approve_draft (store.ApproveDraft) reads
				// them back to enqueue as media, not text.
				var draftErr error
				if mediaKind != "" {
					draftErr = d.Store.AddMediaDraftWithConfirmer(to, msg, model, c.Confirmer, speaker, mediaPath, mediaMime, mediaKind, mediaSeconds, burstMaxTS, now)
				} else {
					draftErr = d.Store.AddDraftWithConfirmer(to, msg, model, c.Confirmer, speaker, burstMaxTS, now)
				}
				if draftErr != nil {
					return mcp.NewToolResultError(draftErr.Error()), nil
				}
				// T147: only a handler that actually opened a dispatch has
				// anything to close — closing/marking on a T64 initiate
				// (bound=false) would grab whatever's THERE by accident.
				if bound {
					if err := markHandledForDispatch(d, to, speaker, burstMaxTS); err != nil {
						log.Printf("send_message: draft mark_handled_before %s: %v", to, err)
					}
					markDispatchChatIfDifferent(d, active, bound, to)
					gate.Consume(termID, active.Nonce)
					gate.RecordSend(termID, active.Nonce)
				}
				publishDraftChanged(d.Bus)
				return mcp.NewToolResultText("held for confirmation (confirmation_mode=always) — awaiting owner approval"), nil
			}

			_ = d.State.React("responding", "replying...", 4*time.Second)
			// T122: a photo enqueues via EnqueueMediaWithModel so
			// processOutbox sends it through gw.SendMedia, never chunked
			// text — mediaKind == "" (every plain-text send) takes the
			// exact same EnqueueWithModel call as before.
			var enqueueErr error
			if mediaKind != "" {
				enqueueErr = d.Store.EnqueueMediaWithModel(to, msg, mediaPath, mediaMime, mediaKind, mediaSeconds, now, model)
			} else {
				enqueueErr = d.Store.EnqueueWithModel(to, msg, now, model)
			}
			if enqueueErr != nil {
				return mcp.NewToolResultError(enqueueErr.Error()), nil
			}
			// ST-D (ct-2026-07-11-074139): usage is metered once this message
			// actually leaves via WhatsApp (corepipeline.processOutbox, the
			// single real-send choke point), not here at enqueue time — a
			// message enqueued but never sent (killed, dead-lettered) must
			// not count as output.
			// T147: same "only if this handler actually opened a dispatch"
			// guard as the draft branch above — Consume identifies by
			// nonce now (gate.go), but there is nothing to identify when
			// nothing was ever opened.
			if bound {
				// Consume still retires the dispatch on this very first
				// call (InFlight -> false immediately, unchanged) — but
				// T167 (ct-2026-09-17-1255) lets up to maxSendsPerDispatch
				// MORE send_message/draft calls land on this same chat
				// after that, tracked by RecordSend, not by this state.
				gate.Consume(termID, active.Nonce)
				gate.RecordSend(termID, active.Nonce)
				// ct-2026-07-13-2243: mark handled only up to burstMaxTS —
				// messages that arrived while the agent was composing stay
				// pending and are re-dispatched (with debounce) on the next
				// capipush sweep.
				if err := markHandledForDispatch(d, to, dispatchSpeaker(active, bound, to), burstMaxTS); err != nil {
					log.Printf("send_message: mark_handled_before %s: %v", to, err)
				}
				markDispatchChatIfDifferent(d, active, bound, to)
			}
			refreshQueue(d.State, d.Store)
			return mcp.NewToolResultText("queued for sending"), nil
		})

	// ── draft ────────────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("draft",
		mcp.WithDescription("Create a draft instead of sending directly — available in any confirmation_mode, for when you'd rather have the owner review before it goes out (see the sensitive-content checklist in the /piumy skill). Same guardrails as send_message (rules/claim/ignored-or-blacklist/policy_version), but this never sends: it always waits for approve_draft."),
		mcp.WithString("to", mcp.Required(), mcp.Description("Destination JID, e.g. 55500000001@c.us")),
		mcp.WithString("message", mcp.Required(), mcp.Description("Text to hold as a draft")),
		mcp.WithString("model", mcp.Required(), mcp.Description("Which model is drafting this")),
		mcp.WithString("policy_version", mcp.Description("Hash from get_decision_policy — call that tool first. Required for non-principal terminals; the principal (DefaultTerminalID) may omit it."))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			to, err := r.RequireString("to")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			model, err := r.RequireString("model")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			policyVersion := r.GetString("policy_version", "")
			// T147 (ct-2026-09-07): read ONCE — same discipline as
			// send_message, see validateSend's own doc.
			termID := terminalIDFromContext(ctx)
			active, bound := gate.Active(termID)
			c, errMsg := validateSend(ctx, d, active, bound, to, model, policyVersion)
			if errMsg != "" {
				return mcp.NewToolResultError(errMsg), nil
			}
			msg, err := r.RequireString("message")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			now := time.Now().Unix()
			// T147: never `now`, never an isPrincipal exception — see
			// send_message's own copy of this reasoning.
			var burstMaxTS int64
			if bound {
				burstMaxTS = active.BurstMaxTS
			}
			speaker := dispatchSpeaker(active, bound, to)
			if err := d.Store.AddDraftWithConfirmer(to, msg, model, c.Confirmer, speaker, burstMaxTS, now); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			// T147: only a handler that actually opened a dispatch has
			// anything to close — see send_message's own copy of this guard.
			if bound {
				// ct-2026-07-13-2243: mark handled up to burstMaxTS so the
				// agent's view of the chat is clean, but post-burst messages
				// stay pending.
				if err := markHandledForDispatch(d, to, speaker, burstMaxTS); err != nil {
					log.Printf("draft: mark_handled_before %s: %v", to, err)
				}
				markDispatchChatIfDifferent(d, active, bound, to)
				// ST-D (ct-2026-07-11-074139): NOT metered here — a draft
				// that's discarded (or never resolved) never reaches
				// WhatsApp. Metering happens once (if ever) at the real
				// send in processOutbox.
				gate.Consume(termID, active.Nonce)
				gate.RecordSend(termID, active.Nonce)
			}
			publishDraftChanged(d.Bus)
			return mcp.NewToolResultText("drafted, awaiting owner approval"), nil
		})

	// ── silent_act ───────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("silent_act",
		mcp.WithDescription("Use INSTEAD of send_message when the right move is not replying — the decision policy is explicit that always having the last word is a mistake. Unlike ignoring the dispatch, this is a deliberate action: it releases your turn immediately (the next chat doesn't wait up to 15 minutes for this one to go stale), marks this burst's messages as handled (they won't be re-dispatched), and optionally records why, so the owner can review your judgment instead of having to guess whether you're stuck. Always targets the CURRENT dispatch's chat — no override. Requires the same unlock -> remember/skip checkpoint as send_message."),
		mcp.WithString("reason", mcp.Description("Optional: why you're staying silent — e.g. \"ya tuve la última palabra\", \"no me corresponde\", \"spam\". Free text."))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			termID := terminalIDFromContext(ctx)
			active, bound := gate.Active(termID)
			if !bound {
				return mcp.NewToolResultError(gate.noDispatchMessage(termID, "silent_act", "locked: no active dispatch for this terminal (default DENY) — call get_instructions first")), nil
			}
			if !active.Ready {
				// T147 (ct-2026-09-07): distinguish "already consumed" from
				// "never touched" — see send_message's own copy of this fix.
				if active.Done {
					return mcp.NewToolResultError("locked: this dispatch was already consumed — call get_instructions for a new one"), nil
				}
				return mcp.NewToolResultError("locked: call get_instructions -> unlock -> remember/skip before silent_act"), nil
			}
			reason := r.GetString("reason", "")
			if err := d.Store.SetChatSilence(active.ChatJID, reason, time.Now().Unix()); err != nil {
				log.Printf("silent_act: set silence %s: %v", active.ChatJID, err)
			}
			// Same bound applied by send_message's own MarkHandledBefore call:
			// only the dispatched burst, not messages that arrived afterward.
			// T108: scoped to the dispatch's own speaker in a group — staying
			// silent on one participant must not silently close another's
			// unrelated, still-unanswered message.
			if err := markHandledForDispatch(d, active.ChatJID, active.Sender, active.BurstMaxTS); err != nil {
				log.Printf("silent_act: mark_handled_before %s: %v", active.ChatJID, err)
			}
			// One-shot, same as send_message/draft: releases the terminal so
			// the next dispatch doesn't wait on dispatchStaleAfter. Identified
			// by nonce (T147) — silent_act already required bound above, so
			// active.Nonce is always this dispatch's own.
			gate.Consume(termID, active.Nonce)
			return mcp.NewToolResultText("silence recorded — turn released, messages marked handled"), nil
		})
}
