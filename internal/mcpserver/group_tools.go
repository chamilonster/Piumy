// Group/profile tools (F4-DESIGN §5) — call d.GroupProfile directly, not
// through gateway.Gateway (see Deps.GroupProfile's doc comment for why).
// d.GroupProfile nil (not wired, or a future adapter that doesn't support
// group/profile admin) means every tool here refuses cleanly.
//
// T148 (ct-2026-09-07-1644, boss verbatim, direct: "quiero que quites ese
// candado, si le pido a un agente que cree un grupo, quiero que lo haga"):
// no longer boss-only — see levelgate.go's own bossOnlyTools doc for why.
// Any registered agent may use every tool below, dispatch or not: none of
// these 7 were ever in chatScopedArg (levelgate.go) either — leaving
// bossOnlyTools was their ONLY dispatch requirement, a side effect of
// living in that map for authority reasons, not a deliberate scope gate
// (unlike the chat-scoped tools, e.g. set_chat_memory, which DO still
// require a dispatch — "sin despacho no hay chat sobre el cual actuar",
// T148's own contract, untouched). Flagged to Citrino, not held back on:
// none of these touch the outbox or the send-rate governor
// (internal/corepipeline), the one thing the boss asked to keep protected.
//
// T149 (ct-2026-09-07-1730, boss verbatim, straight from that flag: "esos
// frenos de en masa deven ir como frenos, no como candados"): with the
// authority locks gone, nothing paced a burst of these 7 the way the
// outbox paces sends — groupActionPacer below closes that, a BRAKE that
// only ever waits, never refuses. See its own doc for the mechanism.
//
// ST-E (ct-2026-07-11-1444): cabled to whatsmeow (*whatsmeow.Adapter
// satisfies GroupProfile), replacing the deleted internal/openwa. Two
// boss decisions from that migration: set_profile_name became
// set_profile_status (whatsmeow has no API for the display name, only the
// "About" status text — SetStatusMessage); set_profile_pic is gone
// entirely (whatsmeow has no API for the own profile picture).
package mcpserver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.mau.fi/whatsmeow/types"

	"piumy-gateway/internal/governor"
	"piumy-gateway/internal/mediautil"
	"piumy-gateway/internal/store"
)

// GroupProfile is the subset of *whatsmeow.Adapter this file needs — group
// admin isn't part of the gateway.Gateway seam (F2): that interface is for
// Send/SetTyping/MarkRead, the cross-vendor messaging concept every
// adapter shares. Groups aren't — inflating Gateway for this one
// implementer would couple every future adapter to a group-admin shape
// only WhatsApp has. Defined here (not imported from whatsmeow) so a fake
// can drive this file's own logic (JSON shape, data-URL decode, error
// paths) in tests without a live WhatsApp session.
type GroupProfile interface {
	CreateGroup(ctx context.Context, name string, participantJIDs []string) (*types.GroupInfo, error)
	AddParticipant(ctx context.Context, groupJID, participantJID string) ([]types.GroupParticipant, error)
	// PromoteParticipants grants group-admin rights (T135, ct-2026-09-03-
	// 1546) — see create_group's own doc for why it's called there.
	PromoteParticipants(ctx context.Context, groupJID string, participantJIDs []string) ([]types.GroupParticipant, error)
	SetGroupPhoto(ctx context.Context, groupJID string, jpeg []byte) (string, error)
	// SetProfilePhoto changes the host number's OWN profile photo (T111,
	// ct-2026-09-01-1442) — the own JID is resolved inside the adapter, the
	// caller never supplies one. nil jpeg removes the photo.
	SetProfilePhoto(ctx context.Context, jpeg []byte) (string, error)
	SetGroupDescription(ctx context.Context, groupJID, description string) error
	SetProfileStatus(ctx context.Context, status string) error
	// GetProfileStatus reads the host number's own "About" text (T96 built
	// this on the adapter; T103, ct-2026-08-29-1759, exposes it to MCP —
	// only the REST dashboard consumed it before). "" with a nil error is
	// the normal, common case: no status set.
	GetProfileStatus(ctx context.Context) (string, error)
}

const groupProfileNotAvailable = "not available: no group/profile client wired"

// canonicalParticipantJID picks the phone-number-form identity for a group
// participant WhatsApp just returned (T135, ct-2026-09-03-1546) — the same
// preference whatsmeow/inbound.go's seedGroups already lives by, one seam
// over: chats.jid is always the canonical phone number, never @lid (T107).
// PhoneNumber is empty only for an @lid participant whose number the
// server didn't resolve; JID is the fallback then, same as everywhere else
// in this codebase degrades rather than drops the participant.
func canonicalParticipantJID(p types.GroupParticipant) types.JID {
	if !p.PhoneNumber.IsEmpty() {
		return p.PhoneNumber
	}
	return p.JID
}

// seedCreatedGroup persists the name and membership WhatsApp's own
// CreateGroup response already carries (T135, ct-2026-09-03-1546) — the
// exact shape whatsmeow/inbound.go's seedGroups uses for the connect-time
// scrape, here for the ONE moment a group is born instead of waiting for a
// reconnect or a first message to learn it. Best-effort: a write failure
// here must never read as "the group wasn't created" — it's returned as a
// warning string, the caller decides what to do with it. No-op (no
// warnings) if d.Store is nil.
func seedCreatedGroup(d Deps, info *types.GroupInfo) []string {
	if d.Store == nil {
		return nil
	}
	var warnings []string
	jid := info.JID.String()
	now := time.Now().Unix()
	if err := d.Store.TouchChat(jid, info.Name, now); err != nil {
		warnings = append(warnings, fmt.Sprintf("could not save the group's name: %v", err))
	}
	for _, p := range info.Participants {
		member := canonicalParticipantJID(p)
		if err := d.Store.UpsertGroupMember(jid, member.String(), p.DisplayName, now); err != nil {
			warnings = append(warnings, fmt.Sprintf("could not save member %s: %v", member, err))
		}
	}
	return warnings
}

// maxPromoteAttempts caps promoteBossParticipants' retry (T145,
// ct-2026-09-07 — hypothesis A confirmed live: WhatsApp can 403 an admin
// op on a group it JUST created, and a manual retry ~1 minute later
// worked). 3 total attempts (1 + 2 retries): enough to plausibly cross
// whatever propagation delay caused the live 403, without hanging
// create_group's own response indefinitely if WhatsApp is genuinely
// refusing (a dead network, a real permissions problem) — those attempts
// would fail identically at attempt 10 as at attempt 1.
const maxPromoteAttempts = 3

// promoteRetryWindow bounds the randomized wait BETWEEN promote attempts —
// anti-ban discipline is non-negotiable even here (project convention,
// governor.DelayWindow — same mechanism/reasoning as
// internal/whatsmeow/avatar.go's own recheck windows): never a fixed or
// round wait, sampled fresh on every retry. No KV override: nobody has
// asked to tune this yet (YAGNI, unlike avatarRecheckWindow's own contacts
// window, which already needed one when a second real case showed up).
var promoteRetryWindow = governor.DelayWindow{Min: 2 * time.Second, Max: 7 * time.Second}

// promoteBossParticipants grants group-admin rights to whichever of the
// group's participants are marked is_boss in OUR OWN store (T135,
// ct-2026-09-03-1546 — the dueño, verbatim: "cuando crees un grupo al boss
// siempre se le otorga administracion del grupo"). Never more than one
// PromoteParticipants call PER ATTEMPT, even with several matches — the
// underlying WhatsApp op already batches.
//
// SECURITY: identity comes ONLY from store.Chat.IsBoss, keyed by
// canonicalParticipantJID (StripDeviceSuffix'd — GetChat runs a raw
// WHERE jid=?, the exact gap T118/T125/T132 already found) — never a
// display name, which the participant themselves controls. Best-effort,
// same as seedCreatedGroup: a promotion failure (even after every retry)
// is a warning, never a reason to have created the group for nothing —
// T135's own non-negotiable rule, untouched by this retry.
func promoteBossParticipants(ctx context.Context, d Deps, info *types.GroupInfo) []string {
	if d.Store == nil || d.GroupProfile == nil {
		return nil
	}
	var warnings []string
	var toPromote []string
	for _, p := range info.Participants {
		candidate := store.StripDeviceSuffix(canonicalParticipantJID(p).String())
		chat, ok, err := d.Store.GetChat(candidate)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("could not check boss status for %s: %v", candidate, err))
			continue
		}
		if ok && chat.IsBoss {
			toPromote = append(toPromote, candidate)
		}
	}
	if len(toPromote) == 0 {
		return warnings
	}
	// T145: attempt 1 fires immediately (no wait before the FIRST try — the
	// live incident's own first attempt failed instantly, a pre-wait
	// wouldn't have changed that); a 403 there is exactly the shape
	// hypothesis A predicts (WhatsApp not yet accepting admin ops on a
	// group it just created), so retries wait for that window to pass.
	var err error
	for attempt := 1; attempt <= maxPromoteAttempts; attempt++ {
		if attempt > 1 {
			promoteRetryWindow.Sleep(ctx)
		}
		if _, err = d.GroupProfile.PromoteParticipants(ctx, info.JID.String(), toPromote); err == nil {
			return warnings
		}
	}
	warnings = append(warnings, fmt.Sprintf("could not promote the owner to group admin after %d attempts: %v", maxPromoteAttempts, err))
	return warnings
}

// groupActionSpacing bounds the randomized wait groupActionPacer.pace
// enforces between consecutive group/profile actions (T149,
// ct-2026-09-07-1730). Wider than every message-pacing window in this
// codebase (DispatchDelayMin/Max, 1-5s; ReadDelayMin/Max, 2-8s —
// internal/config/config.go) on purpose, per the contract's own
// instruction to size this wider and justify the number: a human sends
// messages constantly, but creates a group or adds a participant rarely —
// several of THOSE seconds apart reads as far more suspicious to WhatsApp
// than a quick text exchange. No KV override: nobody has asked to tune
// this yet (YAGNI, same call T145 made for promoteRetryWindow above).
var groupActionSpacing = governor.DelayWindow{Min: 8 * time.Second, Max: 25 * time.Second}

// groupActionPacer is the BRAKE T149 asks for, not a candado (boss
// verbatim: "esos frenos de en masa deven ir como frenos, no como
// candados") — pace only ever waits, then lets the caller through; it must
// never return an error or a bool a caller could act on by refusing, or
// it would be exactly the authority lock T148 just removed. ONE instance
// is built per addGroupTools call below, and every one of the seven
// group/profile tools shares it — the single point of application the
// contract requires (rule 4: write the spacing logic once, or the next
// tool this gateway adds will forget it). Its state starts fresh every
// time a server is built: main.go builds exactly one for the whole
// process (one WhatsApp number, one shared budget across every agent —
// "el número es uno solo"); tests build a fresh one per test, so nothing
// needs resetting between runs.
type groupActionPacer struct {
	mu      sync.Mutex
	firedAt time.Time
}

// pace waits only if the LAST group/profile action (any of the seven)
// fired recently enough that this one would land inside the same burst —
// firedAt zero (nothing paced yet) or older than groupActionSpacing's own
// ceiling means immediate, the DoD's "una sola llamada aislada no paga
// nada". Holding the lock across the wait is deliberate: calls landing
// close together queue behind each other and each draws its OWN fresh
// random wait off groupActionSpacing, which is what actually spreads a
// burst out instead of letting every queued call fire the instant the
// first one's wait ends.
func (p *groupActionPacer) pace(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.firedAt.IsZero() && time.Since(p.firedAt) < groupActionSpacing.Max {
		groupActionSpacing.Sleep(ctx)
	}
	p.firedAt = time.Now()
}

func addGroupTools(s *server.MCPServer, d Deps, tracker *agentTracker) {
	pacer := &groupActionPacer{}

	// ── create_group ─────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("create_group",
		mcp.WithDescription("Create a new WhatsApp group with the given participants."),
		mcp.WithString("name", mcp.Required()),
		mcp.WithArray("participants", mcp.Required(), mcp.Description("Participant JIDs, e.g. 55500000001@c.us"), mcp.Items(map[string]any{"type": "string"}))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			if d.GroupProfile == nil {
				return mcp.NewToolResultError(groupProfileNotAvailable), nil
			}
			pacer.pace(ctx)
			name, err := r.RequireString("name")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			participants, err := r.RequireStringSlice("participants")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			info, err := d.GroupProfile.CreateGroup(ctx, name, participants)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			// From here on the group EXISTS — every step below is
			// best-effort and reported as a warning, never a failure that
			// could read as "the group wasn't created" (T135,
			// ct-2026-09-03-1546): losing a group over a failed follow-up
			// write would be worse than the problem this contract fixes.
			warnings := seedCreatedGroup(d, info)
			warnings = append(warnings, promoteBossParticipants(ctx, d, info)...)
			if len(warnings) == 0 {
				return jsonResult(info)
			}
			return jsonResult(struct {
				*types.GroupInfo
				Warnings []string `json:"warnings"`
			}{info, warnings})
		})

	// ── add_participant ──────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("add_participant",
		mcp.WithDescription("Add one participant to an existing WhatsApp group."),
		mcp.WithString("group_id", mcp.Required()),
		mcp.WithString("participant_id", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			if d.GroupProfile == nil {
				return mcp.NewToolResultError(groupProfileNotAvailable), nil
			}
			pacer.pace(ctx)
			groupID, err := r.RequireString("group_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			participantID, err := r.RequireString("participant_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			participants, err := d.GroupProfile.AddParticipant(ctx, groupID, participantID)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(participants)
		})

	// ── promote_group_admin ──────────────────────────────────────────────
	// T136 (ct-2026-09-03-1627): the tool T135 deliberately did NOT add —
	// that contract's own automatic promotion stayed restricted to is_boss
	// (the system choosing who gets power needs that restriction) and this
	// one doesn't (the owner choosing, for someone he names, doesn't — see
	// this tool's own description). Reuses Adapter.PromoteParticipants
	// (T135) verbatim; the only thing missing was exposing it.
	s.AddTool(mcp.NewTool("promote_group_admin",
		mcp.WithDescription("Grant group-admin rights to one participant of an existing WhatsApp group. Accepts ANY participant, not just is_boss — unlike create_group's automatic promotion, this is for naming someone specific (a teammate is the common case, not the account owner)."),
		mcp.WithString("group_id", mcp.Required()),
		mcp.WithString("participant_id", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			if d.GroupProfile == nil {
				return mcp.NewToolResultError(groupProfileNotAvailable), nil
			}
			pacer.pace(ctx)
			groupID, err := r.RequireString("group_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			participantID, err := r.RequireString("participant_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			// StripDeviceSuffix (T118/T125/T132/T135's recurring gap): the
			// caller may hand back a JID it read off get_chat_groups, which
			// can carry a ":<device>" suffix WhatsApp itself never expects
			// on a group-membership operation.
			participantID = store.StripDeviceSuffix(participantID)
			participants, err := d.GroupProfile.PromoteParticipants(ctx, groupID, []string{participantID})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			// UpdateGroupParticipants (whatsmeow) reports a per-participant
			// failure — promoting someone who isn't actually in the group,
			// for instance — as a non-zero GroupParticipant.Error on an
			// otherwise successful call, never as the outer error above. A
			// silent "success" here would hide exactly the case this
			// contract calls out: never panic, but never call it a success
			// either.
			for _, p := range participants {
				if p.Error != 0 {
					return mcp.NewToolResultError(fmt.Sprintf("WhatsApp rejected promoting %s (error code %d) — check that this is actually a participant of the group", participantID, p.Error)), nil
				}
			}
			return jsonResult(participants)
		})

	// ── set_group_icon ───────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("set_group_icon",
		mcp.WithDescription("Set a group's icon from a data: URL image string (no media pipeline yet, F4d — supply a data URL directly)."),
		mcp.WithString("group_id", mcp.Required()),
		mcp.WithString("data_url", mcp.Required(), mcp.Description("A data: URL, e.g. data:image/jpeg;base64,..."))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			if d.GroupProfile == nil {
				return mcp.NewToolResultError(groupProfileNotAvailable), nil
			}
			pacer.pace(ctx)
			groupID, err := r.RequireString("group_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			dataURL, err := r.RequireString("data_url")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			raw, _, err := mediautil.DecodeDataURL(dataURL)
			if err != nil {
				return mcp.NewToolResultError("invalid data_url: " + err.Error()), nil
			}
			// T111 (ct-2026-09-01-1442, the boss: "que lo convierta a jpg") —
			// whatsmeow rejects anything that isn't JPEG; a PNG/GIF is
			// converted here so the owner never has to convert one by hand
			// first. Only a payload that can't be decoded as ANY image format
			// errors out — same "invalid data_url" the malformed-URL case
			// already used, one error shape for the whole decode step.
			jpegBytes, err := mediautil.EnsureJPEG(raw)
			if err != nil {
				return mcp.NewToolResultError("invalid data_url: " + err.Error()), nil
			}
			if _, err := d.GroupProfile.SetGroupPhoto(ctx, groupID, jpegBytes); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText("group icon set"), nil
		})

	// ── set_group_description ────────────────────────────────────────────
	s.AddTool(mcp.NewTool("set_group_description",
		mcp.WithDescription("Change a group's description/topic."),
		mcp.WithString("group_id", mcp.Required()),
		mcp.WithString("description", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			if d.GroupProfile == nil {
				return mcp.NewToolResultError(groupProfileNotAvailable), nil
			}
			pacer.pace(ctx)
			groupID, err := r.RequireString("group_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			description, err := r.RequireString("description")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := d.GroupProfile.SetGroupDescription(ctx, groupID, description); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText("group description set"), nil
		})

	// ── set_profile_photo ────────────────────────────────────────────────
	// T111 (ct-2026-09-01-1442): a tool of its own, not a group_id-optional
	// mode of set_group_icon — the two write to different targets (own JID
	// vs. a group's), and an implicit "empty group_id means my own photo"
	// perilla is exactly the kind nobody remembers later (Citrino's own
	// call). remove is an explicit bool, not "an empty data_url means
	// clear it" — this goes out to every contact instantly, unforgeable by
	// a stray empty string.
	s.AddTool(mcp.NewTool("set_profile_photo",
		mcp.WithDescription("Set or remove the host number's own WhatsApp profile photo — seen by every contact instantly. Supply data_url to set it (any common image format — a non-JPEG is converted automatically), or remove=true to clear it. Irreversible outward action."),
		mcp.WithString("data_url", mcp.Description("A data: URL, e.g. data:image/png;base64,... — required unless remove=true")),
		mcp.WithBoolean("remove", mcp.Description("true to remove the current photo instead of setting one — wins over data_url if both are given"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			if d.GroupProfile == nil {
				return mcp.NewToolResultError(groupProfileNotAvailable), nil
			}
			pacer.pace(ctx)
			if r.GetBool("remove", false) {
				if _, err := d.GroupProfile.SetProfilePhoto(ctx, nil); err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText("profile photo removed"), nil
			}
			dataURL := r.GetString("data_url", "")
			if dataURL == "" {
				return mcp.NewToolResultError("data_url is required (or pass remove=true to clear the photo)"), nil
			}
			raw, _, err := mediautil.DecodeDataURL(dataURL)
			if err != nil {
				return mcp.NewToolResultError("invalid data_url: " + err.Error()), nil
			}
			jpegBytes, err := mediautil.EnsureJPEG(raw)
			if err != nil {
				return mcp.NewToolResultError("invalid data_url: " + err.Error()), nil
			}
			if _, err := d.GroupProfile.SetProfilePhoto(ctx, jpegBytes); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText("profile photo set"), nil
		})

	// ── set_profile_status ───────────────────────────────────────────────
	s.AddTool(mcp.NewTool("set_profile_status",
		mcp.WithDescription("Set the host number's own status/\"About\" text — NOT the display name (whatsmeow has no API to change that). Renamed from set_profile_name (ST-E, ct-2026-07-11-1444) to avoid confusing agents/skills about what it actually changes."),
		mcp.WithString("status", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			if d.GroupProfile == nil {
				return mcp.NewToolResultError(groupProfileNotAvailable), nil
			}
			pacer.pace(ctx)
			status, err := r.RequireString("status")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := d.GroupProfile.SetProfileStatus(ctx, status); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText("profile status set"), nil
		})
}
