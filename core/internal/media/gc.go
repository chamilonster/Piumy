// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
package media

import (
	"os"
	"sort"

	"pimywa/internal/store"
)

// mediaStore is the subset of store.Store GC needs — narrow so tests can fake
// it without a real SQLite file. Chat/media listing has no pagination cursor
// today; a large limit is used since GC is a periodic background pass, not a
// hot path.
type mediaStore interface {
	ListChats(limit int) ([]store.Chat, error)
	ListMedia(chatJID string, limit int) ([]store.Media, error)
	DeleteMedia(chatJID, msgID string) error
}

const listAllLimit = 100000

// GC deletes the oldest media files (store.Media.TS) until the total size of
// what remains is at or under maxBytes. Size-only policy (2026-07-01) —
// text/metadata in the messages table is NEVER touched here,
// only media rows and the files they point at. maxBytes <= 0 disables GC.
func GC(st mediaStore, maxBytes int64) (deletedCount int, freedBytes int64, err error) {
	if maxBytes <= 0 {
		return 0, 0, nil
	}

	all, err := collectAllMedia(st)
	if err != nil {
		return 0, 0, err
	}
	sort.Slice(all, func(i, j int) bool { return all[i].TS < all[j].TS }) // oldest first

	var total int64
	for _, m := range all {
		total += m.Size
	}

	for _, m := range all {
		if total <= maxBytes {
			break
		}
		if rmErr := os.Remove(m.Path); rmErr != nil && !os.IsNotExist(rmErr) {
			// A locked/missing file shouldn't halt the whole pass — the DB row
			// still gets cleaned up below so GC doesn't retry it forever.
			continue
		}
		if dbErr := st.DeleteMedia(m.ChatJID, m.MsgID); dbErr != nil {
			return deletedCount, freedBytes, dbErr
		}
		total -= m.Size
		deletedCount++
		freedBytes += m.Size
	}
	return deletedCount, freedBytes, nil
}

func collectAllMedia(st mediaStore) ([]store.Media, error) {
	chats, err := st.ListChats(listAllLimit)
	if err != nil {
		return nil, err
	}
	var all []store.Media
	for _, c := range chats {
		items, err := st.ListMedia(c.JID, listAllLimit)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
	}
	return all, nil
}
