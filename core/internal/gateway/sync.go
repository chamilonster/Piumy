// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
//
// Contact/group sync: "the scraped numbers are empty chats" (2026-07-01) —
// every known contact and group becomes a chat row even before
// it has a single message, and group membership is recorded in chat_groups.
// Four modes, by design: one-shot (right after connect),
// continuous + incremental (a slow periodic re-sweep — whatsmeow's
// contact/group snapshot is always the full current set, so re-running the
// same sweep is inherently incremental: unseen entries get added, already-seen
// ones are a harmless idempotent upsert), and reactive (live join/leave/new-
// group events, handled in handleEvent). Every WhatsApp-server-facing step in
// here waits an actionDelay — must be slow, even for a passive sync.
package gateway

import (
	"context"
	"log"
	"time"

	"go.mau.fi/whatsmeow/types"
)

// syncInterval is how often the periodic contact/group re-sweep runs.
const syncInterval = 6 * time.Hour

// syncLoop runs the periodic re-sweep (continuous + incremental). The
// one-shot initial sweep is triggered separately from onConnected.
func (g *Gateway) syncLoop(ctx context.Context) {
	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if g.client.IsConnected() {
				g.syncContactsAndGroups(ctx)
			}
		}
	}
}

// syncContactsAndGroups is the slow, paced backfill: every known contact
// becomes an empty chat row (TouchChat with ts=0 — never lowers an existing
// chat's last_ts thanks to the MAX() in its UPDATE), every joined group
// becomes an empty chat row plus a chat_groups row per participant, and every
// known chat's Archived flag is refreshed from WhatsApp's own chat settings.
func (g *Gateway) syncContactsAndGroups(ctx context.Context) {
	contacts, err := g.client.Store.Contacts.GetAllContacts(ctx)
	if err != nil {
		log.Printf("gateway: sync contacts: %v", err)
	}
	for jid, info := range contacts {
		if ctx.Err() != nil {
			return
		}
		g.actionDelay().Sleep(ctx)
		if err := g.msgSt.TouchChat(jid.String(), info.PushName, 0); err != nil {
			log.Printf("gateway: sync touch contact %s: %v", jid, err)
		}
	}

	groups, err := g.client.GetJoinedGroups(ctx)
	if err != nil {
		log.Printf("gateway: sync groups: %v", err)
	}
	for _, grp := range groups {
		if ctx.Err() != nil {
			return
		}
		g.actionDelay().Sleep(ctx)
		g.syncGroup(grp.JID, grp.Name, grp.Topic, grp.Participants)
		g.syncGroupInviteLink(ctx, grp.JID)
	}

	g.syncArchivedFlags(ctx)
}

// syncGroup registers a group as an (possibly empty) chat, its description
// (topic), and every participant's membership. Pure store writes, no
// WhatsApp-server call — shared by the periodic sweep and the reactive
// JoinedGroup handler, and safe to call with no client set (see sync_test.go).
func (g *Gateway) syncGroup(groupJID types.JID, name, topic string, participants []types.GroupParticipant) {
	jid := groupJID.String()
	if err := g.msgSt.TouchChat(jid, name, 0); err != nil {
		log.Printf("gateway: sync touch group %s: %v", jid, err)
	}
	if err := g.msgSt.SetChatDescription(jid, topic); err != nil {
		log.Printf("gateway: sync group description %s: %v", jid, err)
	}
	for _, p := range participants {
		if err := g.msgSt.AddGroupMember(p.JID.String(), jid); err != nil {
			log.Printf("gateway: sync group member %s/%s: %v", p.JID, jid, err)
		}
	}
}

// syncGroupInviteLink fetches and stores a group's invite link (0130) — a
// SEPARATE step from syncGroup because it's the only one that calls the
// WhatsApp server, so it's kept out of syncGroup's pure-store tests. Only
// fetches if the link isn't already known (never re-fetch every sweep —
// acota llamadas al servidor). Best-effort: GetGroupInviteLink requires
// being a group admin; any error just logs and leaves it empty, it never
// fails the sync. Paced with actionDelay like every other server-facing
// step in this file.
func (g *Gateway) syncGroupInviteLink(ctx context.Context, groupJID types.JID) {
	jid := groupJID.String()
	c, ok, err := g.msgSt.GetChat(jid)
	if err != nil {
		log.Printf("gateway: sync invite link %s: get chat: %v", jid, err)
		return
	}
	if ok && c.GroupInviteLink != "" {
		return
	}
	g.actionDelay().Sleep(ctx)
	if ctx.Err() != nil {
		return
	}
	link, err := g.client.GetGroupInviteLink(ctx, groupJID, false)
	if err != nil {
		log.Printf("gateway: sync invite link %s: %v (best-effort — probably not admin)", jid, err)
		return
	}
	if err := g.msgSt.SetGroupInviteLink(jid, link); err != nil {
		log.Printf("gateway: set invite link %s: %v", jid, err)
	}
}

// syncArchivedFlags refreshes chats.archived (WhatsApp's own attribute,
// distinct from Piumy's Active) for every chat already known to the store.
func (g *Gateway) syncArchivedFlags(ctx context.Context) {
	chats, err := g.msgSt.ListChats(listAllChatsLimit)
	if err != nil {
		log.Printf("gateway: sync archived: list chats: %v", err)
		return
	}
	for _, c := range chats {
		if ctx.Err() != nil {
			return
		}
		jid, err := types.ParseJID(c.JID)
		if err != nil {
			continue
		}
		g.actionDelay().Sleep(ctx)
		settings, err := g.client.Store.ChatSettings.GetChatSettings(ctx, jid)
		if err != nil {
			log.Printf("gateway: sync archived %s: %v", c.JID, err)
			continue
		}
		if err := g.msgSt.SetArchived(c.JID, settings.Archived); err != nil {
			log.Printf("gateway: set archived %s: %v", c.JID, err)
		}
	}
}

// listAllChatsLimit is a "give me everything" limit for ListChats/ListMedia
// calls in periodic background sweeps (sync, GC) — not a hot path, no
// pagination cursor exists yet.
const listAllChatsLimit = 100000
