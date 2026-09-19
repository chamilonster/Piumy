// Contact backfill (ct-2026-07-19-0115, backup Sub 2a) — cherry-picked from
// Piumy's gateway/sync.go:24-62 (the syncLoop + syncContactsAndGroups'
// contact half), adapted: this gateway's group_members/contact_name schema
// (Sub 1) didn't exist in Piumy, and groups are a separate sub (2b), not
// touched here. Boss verbatim: "al conectar un numero, lenta pero
// progresivamente, guardar en la DB toda, absolutamente toda la info de
// todos los numeros... Progresivo, lento, antiban." Every WhatsApp-server-
// facing step here waits actionDelay — must be slow, even for a passive sync.
package whatsmeow

import (
	"context"
	"log"
	"time"

	"go.mau.fi/whatsmeow/types"

	"piumy-gateway/internal/governor"
	"piumy-gateway/internal/store"
)

// syncInterval is how often the periodic contact re-sweep runs. The one-shot
// initial sweep is triggered separately, from handleConnected. Kept as a
// safety net (ct-2026-07-31): with scheduleContactsSync reacting to the real
// event now, this clock is no longer the primary mechanism — do not lower
// it to compensate for a timing bug; if the event-driven path works, the
// clock is redundant, and if it doesn't, a shorter clock wouldn't fix that.
const syncInterval = 6 * time.Hour

// contactsSyncDebounceDefault coalesces a burst of "contact data just
// landed" signals into a single syncContacts run (ct-2026-07-31, "no llegan
// contactos en una instalación nueva" — see scheduleContactsSync's doc).
// WhatsApp delivers these in short bursts, not one at a time — history.go's
// own doc records a real re-pareo push landing 4 HistorySync chunks in
// ~7s — and syncContacts walks every known contact with a real per-contact
// anti-ban delay, so firing it once per chunk/event would be wasteful and
// slow for no benefit. 10s: comfortably longer than that observed burst
// window, short enough that a fresh pairing's contacts show up in seconds
// instead of the old 6h wait. Overridable via Adapter.contactsSyncDebounce
// (zero = use this default) — a test-only seam, same shape as
// actionDelayMin/Max being set directly on a test Adapter; NOT KV-backed
// like actionDelay, on purpose: this is an internal coalescing detail with
// no anti-ban/operational reason for the boss to ever tune it.
const contactsSyncDebounceDefault = 10 * time.Second

// scheduleContactsSync arms (or re-arms) a debounced syncContacts run —
// the fix for ct-2026-07-31's diagnosis ("no llegan contactos en una
// instalación nueva"): is_contact/chats.name were reaching ~0 on a fresh
// pairing because the ONLY code that ever reads whatsmeow's local
// Store.Contacts into piumy.db (syncContacts) ran exactly once, synchronously,
// at connect — racing ahead of whatsmeow's own async delivery of that data
// (app-state sync for the contact list, or a PUSH_NAME HistorySync chunk),
// which the library's own research (history.go) documents as taking
// "minutes to hours". Nothing re-ran syncContacts until the next 6h tick.
// This reacts to the EVENT that signals the data actually arrived, instead
// of waiting on a clock that has no relationship to when WhatsApp actually
// sends it. Callers: handleHistorySync (history.go, PUSH_NAME chunks) and
// the *events.AppStateSyncComplete case for the contact-list collection
// (inbound.go).
func (a *Adapter) scheduleContactsSync() {
	if a.store == nil {
		return
	}
	debounce := a.contactsSyncDebounce
	if debounce <= 0 {
		debounce = contactsSyncDebounceDefault
	}
	a.contactsSyncMu.Lock()
	defer a.contactsSyncMu.Unlock()
	if a.contactsSyncTimer != nil {
		a.contactsSyncTimer.Stop()
	}
	a.contactsSyncTimer = time.AfterFunc(debounce, func() {
		a.syncContacts(a.context())
	})
}

// actionDelay is the anti-ban pacing window for the backfill — same
// governor.DelayWindow mechanism corepipeline uses for dispatch/read
// pacing, a live KV-override checked first (dashboard edit applies live, no
// restart), falling back to Config.ActionDelayMin/Max.
func (a *Adapter) actionDelay() governor.DelayWindow {
	return governor.DelayWindow{
		Min: a.store.SettingDuration(store.SettingActionDelayMin, a.actionDelayMin),
		Max: a.store.SettingDuration(store.SettingActionDelayMax, a.actionDelayMax),
	}
}

// syncLoop runs the periodic contact re-sweep. Safe to re-run: whatsmeow's
// contact snapshot is always the full current set, so re-sweeping is
// inherently incremental (unseen contacts get added, already-seen ones are
// a harmless idempotent upsert — TouchChat/SetContactName both are).
func (a *Adapter) syncLoop(ctx context.Context) {
	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if a.client.IsConnected() {
				a.syncContacts(ctx)
			}
		}
	}
}

// syncContacts is the slow, paced contact backfill — every known contact
// becomes an (possibly empty) chat row, ts=0 never lowering an existing
// chat's last_ts (TouchChat's own MAX()).
func (a *Adapter) syncContacts(ctx context.Context) {
	if a.store == nil {
		return
	}
	contacts, err := a.client.Store.Contacts.GetAllContacts(ctx)
	if err != nil {
		log.Printf("whatsmeow: sync contacts: %v", err)
		return
	}
	a.backfillContacts(ctx, contacts)
}

// backfillContacts is syncContacts' paced per-contact loop, split out so
// it's testable with a synthetic contacts map — no live whatsmeow client
// needed. Assumes a.store is non-nil (syncContacts' only production caller
// already guards it).
func (a *Adapter) backfillContacts(ctx context.Context, contacts map[types.JID]types.ContactInfo) {
	for jid, info := range contacts {
		if ctx.Err() != nil {
			return
		}
		a.actionDelay().Sleep(ctx)

		if err := a.store.TouchChat(jid.String(), info.PushName, 0); err != nil {
			log.Printf("whatsmeow: sync touch contact %s: %v", jid, err)
		}

		// Agenda name (boss verbatim: "respaldar el nombre de contacto si
		// esta en el telefono") — FullName first, FirstName as fallback;
		// NEVER PushName (the contact's own self-chosen name — that's what
		// TouchChat just wrote to chats.name above). An empty agenda name
		// is never written: SetContactName would happily overwrite a name
		// already known with "", so the guard lives here, at the source.
		name := info.FullName
		if name == "" {
			name = info.FirstName
		}
		if name == "" {
			continue
		}
		if err := a.store.SetContactName(jid.String(), name); err != nil {
			log.Printf("whatsmeow: sync contact name %s: %v", jid, err)
		}
	}
}
