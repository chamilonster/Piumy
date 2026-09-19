// The ongoing half of S13 (ct-2026-07-30-1835, "el número gana siempre"):
// store.ReconcileIdentities handles the merge mechanics; this file is the
// only piece that actually HAS both a live resolver (ResolvePN) and the
// store, so it's the natural place to run it periodically — see
// docs/S13-INFORME-UNIFICAR-IDENTIDAD.md for why a periodic sweep was
// chosen over a read-time resolver wired into every query.
package whatsmeow

import (
	"context"
	"log"
	"time"

	"piumy-gateway/internal/store"
)

// identityReconcileSweepInterval is how often reconcileIdentitiesSweepLoop
// checks in — identity reconciliation isn't latency-sensitive the way
// dispatch is, so this is deliberately generous.
const identityReconcileSweepInterval = 1 * time.Hour

// reconcileIdentitiesSweepLoop periodically merges @lid chats into their
// resolved number counterpart. Gated by store.SettingIdentityAutoReconcile,
// default false: shipping this loop does NOT, by itself, merge or delete
// anything — the regla dura for this subcontrato is "nada destructivo sin
// OK explícito del boss y con backup verificado", and ReconcileIdentities
// deletes @lid rows. Wired to start from boot (Start, same convention as
// syncLoop/mediaBgWorkerLoop) so the only thing left once the boss approves
// is flipping the setting — plus the separate, explicit one-time backfill
// for the 557 pairs already on the books — not a deploy.
func (a *Adapter) reconcileIdentitiesSweepLoop(ctx context.Context) {
	if a.store == nil {
		return
	}
	ticker := time.NewTicker(identityReconcileSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.reconcileIdentitiesOnce(ctx)
		}
	}
}

// reconcileIdentitiesOnce runs a single pass — split out from the loop so
// it's testable without waiting on a real ticker. The returned error from
// ReconcileIdentities is infra-level only (e.g. can't read the chats
// table) — a single bad pair never reaches here as an error (S13 C-3,
// ct-2026-07-31-0136): it comes back as a "failed" outcome instead, logged
// with its reason, while every other pair still got processed.
func (a *Adapter) reconcileIdentitiesOnce(ctx context.Context) {
	if !a.store.SettingBool(store.SettingIdentityAutoReconcile, false) {
		return
	}
	outcomes, err := a.store.ReconcileIdentities(func(lidJID string) string {
		pn, err := a.ResolvePN(ctx, lidJID)
		if err != nil {
			return ""
		}
		return pn
	})
	if err != nil {
		log.Printf("whatsmeow: identity reconcile sweep: %v", err)
		return
	}
	if len(outcomes) == 0 {
		return
	}
	merged, renamed, failed, deduped := 0, 0, 0, 0
	for _, o := range outcomes {
		deduped += o.Deduped
		switch o.Action {
		case "merged":
			merged++
		case "renamed":
			renamed++
		case "failed":
			failed++
			log.Printf("whatsmeow: identity reconcile sweep: FAILED lid=%s number=%s: %s", o.LIDJID, o.NumberJID, o.Reason)
		}
	}
	log.Printf("whatsmeow: identity reconcile sweep: merged=%d renamed=%d deduped=%d failed=%d", merged, renamed, deduped, failed)
}
