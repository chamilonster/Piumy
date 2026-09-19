package store

// resetTables lists exactly what ResetMessagingData wipes — messaging/
// operational data coupled to the chats being reset (D4, ct-2026-07-22-2100,
// the boss's "partir de 0" smoke-test reset). Checkpointed with Citrino
// before implementing, table by table:
//   - chats/messages/group_members/media/drafts: the contract's own
//     explicit list.
//   - outbox: the approved-and-queued send backlog — references chats.jid
//     (to_jid), left behind it would try to send to a chat that no longer
//     exists.
//   - media_pending: the media-download FIFO queue, keyed by
//     (chat_jid, msg_id) of messages that are about to disappear too.
//
// chat_groups is GONE from this list (T18B, ct-2026-08-05-1243) — the
// table itself was retired, no longer part of the schema at all
// (GroupsOf/ChatOrigin's "group_discovered" both read group_members now).
//
// Deliberately NOT here:
//   - kv: antenna/dashboard password/all settings (M5 defaults included) —
//     the boss's own configuration, "partir de 0" means the DATA, not the
//     setup.
//   - agents: registered secondaries — same reasoning, operational config.
//   - usage: anti-ban, non-negotiable (Citrino's ratified rule). It feeds
//     capipush.Pusher.sweepOnce's DailyQuota gate via TotalUsageToday — the
//     literal governor.Limiter (per-minute rate) never reads usage at all,
//     but DailyQuota IS a volume cap gating dispatch on "how much went out
//     today", the same anti-ban SPIRIT the rule names. Zeroing it would let
//     that quota permit a fresh burst right after reset — the exact risk
//     Citrino flagged (boss activates a chat in auto right after resetting,
//     governor/quota has no memory of what already went out today).
var resetTables = []string{
	"chats", "messages", "group_members", "media",
	"drafts", "outbox", "media_pending",
}

// ResetMessagingData wipes resetTables in ONE transaction — a failure
// partway rolls back everything rather than leaving some tables emptied
// and others not. Table names are a fixed Go literal (resetTables above),
// never external input, so building the DELETE statement by concatenation
// carries no injection risk here.
func (s *Store) ResetMessagingData() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op once Commit succeeds
	for _, table := range resetTables {
		if _, err := tx.Exec("DELETE FROM " + table); err != nil {
			return err
		}
	}
	return tx.Commit()
}
