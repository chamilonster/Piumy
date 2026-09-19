package store

import (
	"database/sql"
	"fmt"
)

// ReconcileOutcome is what happened to one @lid chat during a
// ReconcileIdentities pass — one entry per lidJID that had a resolvable
// number (an unresolved lid, resolve()=="", is skipped entirely and never
// appears here, same as before S13 C-3). Deduped counts messages+media rows
// dropped because WhatsApp had already delivered the same message under
// both the @lid and the number chat_jid (observed on Note-to-Self: the
// echo of a self-sent message arrives through both addressing forms with
// the same id) — reported explicitly per the boss's own rule: a silent
// dedupe is indistinguishable from a loss.
type ReconcileOutcome struct {
	LIDJID    string
	NumberJID string
	Action    string // "merged", "renamed", or "failed"
	Deduped   int
	Reason    string // set only when Action == "failed"
}

// ReconcileIdentities merges @lid chats into their resolved phone-number
// counterpart (S13, ct-2026-07-30-1835 — reactivates F2's original design,
// ct-2026-07-11-0030, cancelled ct-2026-07-18-171940 for priority/timing,
// never for a technical failure: its own merge code never ran against real
// data before the boss cancelled it). resolve maps a @lid JID string to its
// number JID string ("" = not resolved yet, e.g. no message via that LID has
// arrived to populate whatsmeow's own LID map — skip it, idempotent: a later
// run picks it up once resolve has an answer). store never imports
// whatsmeow directly; the caller supplies resolve (whatsmeow.Adapter.ResolvePN
// in production).
//
// Merge policy (boss verbatim, 2026-07-30, in response to Citrino's explicit
// repregunta about whether config is included: "el numero gana siempre
// (prioridad)"): the number's row wins EVERYTHING config-related, no
// blending. This is DELIBERATELY simpler than F2's original policy (which OR'd
// is_boss, took the more-restrictive confirmation_mode, and let @lid win a
// content tie on rules/memory/context) — that policy is now wrong, not just
// dated: the boss was asked directly whether the old @lid rules ("Eres el
// asistente personal de Camilo...") should survive over the number's thinner
// ones, and said no, explicitly, aware of the consequence. See mergeChat's
// own doc for exactly what does and doesn't move.
//
// If no ghost exists at the number JID, the @lid chat is simply renamed —
// nothing to merge, nothing lost.
//
// One bad pair does not stop the sweep (S13 C-3, ct-2026-07-31-0136): each
// lidJID gets its own transaction; a failure rolls back THAT pair only and
// is recorded in the returned outcome, and the loop continues to the next
// lidJID. Before C-3 a single failing pair (the Note-to-Self message-id
// collision, see dedupeBeforeRekey) aborted the entire sweep, silently
// leaving every other pair unprocessed until someone noticed by chance —
// a design defect, not a data problem. The returned error is reserved for
// an infra-level failure (can't even read the chats table); per-pair
// failures never propagate there.
func (s *Store) ReconcileIdentities(resolve func(lidJID string) string) ([]ReconcileOutcome, error) {
	rows, err := s.db.Query(`SELECT jid FROM chats`)
	if err != nil {
		return nil, err
	}
	var lidJIDs []string
	for rows.Next() {
		var jid string
		if err := rows.Scan(&jid); err != nil {
			rows.Close()
			return nil, err
		}
		if IsLIDJID(jid) {
			lidJIDs = append(lidJIDs, jid)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	var outcomes []ReconcileOutcome
	for _, lidJID := range lidJIDs {
		numberJID := resolve(lidJID)
		if numberJID == "" {
			continue
		}
		_, ok, err := s.GetChat(numberJID)
		if err != nil {
			outcomes = append(outcomes, ReconcileOutcome{LIDJID: lidJID, NumberJID: numberJID, Action: "failed", Reason: err.Error()})
			continue
		}
		if !ok {
			deduped, err := s.rekeyChat(lidJID, numberJID)
			if err != nil {
				outcomes = append(outcomes, ReconcileOutcome{LIDJID: lidJID, NumberJID: numberJID, Action: "failed", Deduped: deduped, Reason: err.Error()})
				continue
			}
			outcomes = append(outcomes, ReconcileOutcome{LIDJID: lidJID, NumberJID: numberJID, Action: "renamed", Deduped: deduped})
			continue
		}
		deduped, err := s.mergeChat(lidJID, numberJID)
		if err != nil {
			outcomes = append(outcomes, ReconcileOutcome{LIDJID: lidJID, NumberJID: numberJID, Action: "failed", Deduped: deduped, Reason: err.Error()})
			continue
		}
		outcomes = append(outcomes, ReconcileOutcome{LIDJID: lidJID, NumberJID: numberJID, Action: "merged", Deduped: deduped})
	}
	return outcomes, nil
}

// dedupeBeforeRekey deletes rows from table under oldJID whose keyCol value
// already exists under newJID, INSIDE the caller's transaction — before the
// rekeying UPDATE runs. Without this, the UPDATE...SET chat_jid=newJID
// collides on (chat_jid, keyCol)'s primary key the moment the same id
// exists on both sides (S13 C-3, ct-2026-07-31-0136: WhatsApp echoes a
// self-sent Note-to-Self message back through BOTH the number and the @lid
// addressing forms, so the gateway had stored it twice under the same
// WhatsApp message id — same id, same ts, same from_me, genuinely the same
// message, not two). Deleting the @lid side's copy is correct, not lossy:
// the content already lives under newJID. Two rejected alternatives, both
// data-losing or bug-hiding: `UPDATE OR IGNORE` would silently strand the
// colliding row under the @lid, which then gets deleted with the rest of
// the chat — real, silent data loss; skipping Note-to-Self by JID would
// hide the bug for THIS chat while leaving the same collision class alive
// for the next chat that happens to have an echo. Returns how many rows
// were dropped as redundant duplicates — the caller reports this per pair,
// per the boss's rule that a silent dedupe reads the same as a loss.
func dedupeBeforeRekey(tx *sql.Tx, table, keyCol, oldJID, newJID string) (int, error) {
	q := fmt.Sprintf(`DELETE FROM %s WHERE chat_jid = ? AND %s IN (SELECT %s FROM %s WHERE chat_jid = ?)`,
		table, keyCol, keyCol, table)
	res, err := tx.Exec(q, oldJID, newJID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// rekeyReferencingRows moves every row that references a chat by JID —
// messages/media (deduped first, see dedupeBeforeRekey — both have a PK
// composite with chat_jid), outbox/drafts (plain UPDATE, no PK-collision
// risk: outbox keys on `seq` alone and drafts on `id` alone, chat_jid is
// just a column on both, never part of the primary key) — plus usage
// (merged via mergeUsageRows: a plain per-row insert when newJID has no
// usage yet — the rename case — and a real sum when it does — the merge
// case; one code path covers both, untouched by C-3). group_members.member_jid
// is deliberately NOT touched (chat_groups, this comment's original
// reference, was retired in T18B, ct-2026-08-05-1243 — group_members is
// the one table this exclusion actually applies to now, same reasoning):
// group-membership identity is a separate concern from 1:1 conversation
// identity, out of this reconciliation's scope (flagged, not decided, in
// docs/S13-INFORME-UNIFICAR-IDENTIDAD.md). Returns how many messages+media
// rows were deduped away (see dedupeBeforeRekey).
func rekeyReferencingRows(tx *sql.Tx, oldJID, newJID string) (int, error) {
	deduped := 0
	for _, t := range []struct{ table, keyCol string }{
		{"messages", "id"},
		{"media", "msg_id"},
	} {
		n, err := dedupeBeforeRekey(tx, t.table, t.keyCol, oldJID, newJID)
		if err != nil {
			return deduped, err
		}
		deduped += n
	}
	for _, q := range []string{
		`UPDATE messages SET chat_jid=? WHERE chat_jid=?`,
		`UPDATE outbox SET to_jid=? WHERE to_jid=?`,
		`UPDATE media SET chat_jid=? WHERE chat_jid=?`,
		`UPDATE drafts SET chat_jid=? WHERE chat_jid=?`,
	} {
		if _, err := tx.Exec(q, newJID, oldJID); err != nil {
			return deduped, err
		}
	}
	return deduped, mergeUsageRows(tx, oldJID, newJID)
}

// rekeyChat renames a chat (no ghost exists at newJID) and every row that
// references it. Returns how many messages+media rows were deduped away
// (see dedupeBeforeRekey) — normally 0 here, since a rename target has no
// pre-existing chat row to have accumulated rows under, but the dedupe runs
// unconditionally anyway: orphaned rows under newJID with no chats row
// (shouldn't happen, but rekeyReferencingRows doesn't assume the DB is
// always in the state it expects) are exactly the case a defensive dedupe
// protects against for free.
func (s *Store) rekeyChat(oldJID, newJID string) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	deduped, err := rekeyReferencingRows(tx, oldJID, newJID)
	if err != nil {
		return deduped, err
	}
	if _, err := tx.Exec(`UPDATE chats SET jid=? WHERE jid=?`, newJID, oldJID); err != nil {
		return deduped, err
	}
	return deduped, tx.Commit()
}

// mergeChat merges lidJID into the existing chat at numberJID — the
// number's row wins EVERYTHING config-related (S13: "el número gana
// siempre... y punto", no exceptions for a content tie). Touches NOTHING
// on the number's row except:
//   - Name/ContactName: ONLY as a fallback when the number's OWN value is
//     empty — a display gap (nothing to show), not a config choice. If the
//     number already has a name, the @lid's is discarded, same as any
//     other config field.
//   - LastTS: MAX of the two — a fact about recency, not a policy the boss
//     sets.
//
// is_boss/active/status/rules/memory/context/confirmation_mode/confirmer/
// config_level_source are left EXACTLY as the number's row already had
// them — no OR, no "more restrictive", no content-wins tiebreak. The @lid
// row's own values in those columns are discarded when it's deleted below;
// that discarding IS the boss's decision, not an oversight. Returns how
// many messages+media rows were deduped away (see dedupeBeforeRekey).
func (s *Store) mergeChat(lidJID, numberJID string) (int, error) {
	lidChat, _, err := s.GetChat(lidJID)
	if err != nil {
		return 0, err
	}
	numberChat, _, err := s.GetChat(numberJID)
	if err != nil {
		return 0, err
	}

	name := numberChat.Name
	if name == "" {
		name = lidChat.Name
	}
	contactName := numberChat.ContactName
	if contactName == "" {
		contactName = lidChat.ContactName
	}
	lastTS := numberChat.LastTS
	if lidChat.LastTS > lastTS {
		lastTS = lidChat.LastTS
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	deduped, err := rekeyReferencingRows(tx, lidJID, numberJID)
	if err != nil {
		return deduped, err
	}
	if _, err := tx.Exec(`UPDATE chats SET name=?, contact_name=?, last_ts=? WHERE jid=?`,
		name, contactName, lastTS, numberJID); err != nil {
		return deduped, err
	}
	if _, err := tx.Exec(`DELETE FROM chats WHERE jid=?`, lidJID); err != nil {
		return deduped, err
	}
	return deduped, tx.Commit()
}

// mergeUsageRows sums oldJID's per-day metering into newJID's (both chats
// can independently have metered the same calendar day — a blind UPDATE
// would violate usage's (chat_jid, day) primary key), then drops oldJID's
// rows.
func mergeUsageRows(tx *sql.Tx, oldJID, newJID string) error {
	rows, err := tx.Query(`SELECT day, out_chars, in_chars, images, audio, messages, tokens_real FROM usage WHERE chat_jid=?`, oldJID)
	if err != nil {
		return err
	}
	type dayUsage struct {
		day                                        string
		outChars, inChars, images, audio, messages int
		tokensReal                                 float64
	}
	var toMerge []dayUsage
	for rows.Next() {
		var d dayUsage
		if err := rows.Scan(&d.day, &d.outChars, &d.inChars, &d.images, &d.audio, &d.messages, &d.tokensReal); err != nil {
			rows.Close()
			return err
		}
		toMerge = append(toMerge, d)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	for _, d := range toMerge {
		if _, err := tx.Exec(`
			INSERT INTO usage (chat_jid, day, out_chars, in_chars, images, audio, messages, tokens_real)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(chat_jid, day) DO UPDATE SET
				out_chars = out_chars + excluded.out_chars,
				in_chars = in_chars + excluded.in_chars,
				images = images + excluded.images,
				audio = audio + excluded.audio,
				messages = messages + excluded.messages,
				tokens_real = tokens_real + excluded.tokens_real`,
			newJID, d.day, d.outChars, d.inChars, d.images, d.audio, d.messages, d.tokensReal,
		); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`DELETE FROM usage WHERE chat_jid=?`, oldJID)
	return err
}
