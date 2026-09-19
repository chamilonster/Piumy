package store

import "database/sql"

// Avatar is one cached profile-picture row (T17 Parte 3, ct-2026-08-05-1240)
// — account-scoped (keyed by jid, not chat_jid/msg_id like media/
// media_pending), so it's NOT in resetTables (reset.go): a "partir de 0"
// wipes the boss's message history, not WhatsApp's own current photo for a
// number that still exists on his phone. PictureID/Path both "" means
// "checked, confirmed no photo set" — distinct from never having checked at
// all (Path=="" but NextCheckAt==0, GetAvatar's ok=false: no row yet).
type Avatar struct {
	JID         string
	PictureID   string // WhatsApp's own picture id — types.ProfilePictureInfo.ID, the ExistingID conditional-check key
	Path        string // "" when confirmed no photo set
	FetchedAt   int64  // unix seconds of the last successful check (not necessarily a change)
	NextCheckAt int64  // unix seconds — before this, GetProfilePictureInfo is not worth asking again
}

// GetAvatar returns jid's cached avatar row. ok=false means no row exists
// yet (never checked) — distinct from a checked-but-no-photo row (ok=true,
// Path=="").
func (s *Store) GetAvatar(jid string) (Avatar, bool, error) {
	var a Avatar
	err := s.db.QueryRow(
		`SELECT jid, picture_id, path, fetched_at, next_check_at FROM avatars WHERE jid = ?`, jid,
	).Scan(&a.JID, &a.PictureID, &a.Path, &a.FetchedAt, &a.NextCheckAt)
	if err == sql.ErrNoRows {
		return Avatar{}, false, nil
	}
	return a, err == nil, err
}

// UpsertAvatar persists a. The caller decides every field — this is one
// plain write path for all three outcomes of a check (new/changed photo,
// confirmed no photo, or a bare next_check_at bump after an unchanged/
// errored check), so the three can't drift into three different SQL
// statements that quietly diverge later.
func (s *Store) UpsertAvatar(a Avatar) error {
	_, err := s.db.Exec(`
		INSERT INTO avatars (jid, picture_id, path, fetched_at, next_check_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(jid) DO UPDATE SET
			picture_id = excluded.picture_id,
			path = excluded.path,
			fetched_at = excluded.fetched_at,
			next_check_at = excluded.next_check_at`,
		a.JID, a.PictureID, a.Path, a.FetchedAt, a.NextCheckAt)
	return err
}
