package store

// BackupCounts returns the dashboard's "Backup" badge data (S1b,
// ct-2026-07-19-1823; recategorizado por categoría en Tramo B,
// ct-2026-07-22-0436 — el mix mensajes+miembros+contactos original no
// distinguía chats de grupos ni contactos de números) — cuánto del backfill
// anti-ban (contact sync, scrape de miembros, HistorySync) llegó a la DB.
// Cuatro COUNT(*) baratos, sin caché: misma razón que siempre, escala de
// dashboard (miles de filas, no millones).
func (s *Store) BackupCounts() (chats, groups, contacts, numbers int, err error) {
	// chats: chats p2p (no @g.us, no status@broadcast) con al menos un
	// mensaje REAL guardado (realMessageSQL — protocol noise sin texto/tipo
	// no cuenta, ct-2026-07-29).
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM chats
		WHERE jid NOT LIKE '%@g.us' AND jid != '` + StatusBroadcastJID + `'
		  AND EXISTS (SELECT 1 FROM messages WHERE messages.chat_jid = chats.jid AND ` + realMessageSQL + `)`).Scan(&chats); err != nil {
		return
	}
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM chats WHERE jid LIKE '%@g.us'`).Scan(&groups); err != nil {
		return
	}
	// contact_name is only ever set by the agenda-name backfill
	// (syncContacts/SetContactName) — this counts contacts actually
	// scraped, not every chats row (that would double-count groups/1:1s
	// with no agenda name known).
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM chats WHERE contact_name != ''`).Scan(&contacts); err != nil {
		return
	}
	// numbers: participantes de grupo que NO son también un contacto de
	// agenda conocido — mismo criterio que is_contact en chatOut (read.go),
	// sin la resolución @lid (eso necesita LIDResolver+context, solo
	// disponible en restapi; una comparación de member_jid crudo alcanza
	// para un conteo resumen).
	if err = s.db.QueryRow(`SELECT COUNT(DISTINCT member_jid) FROM group_members
		WHERE member_jid NOT IN (SELECT jid FROM chats WHERE contact_name != '')`).Scan(&numbers); err != nil {
		return
	}
	return
}
