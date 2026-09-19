// POST /api/media/fetch (dashboard popup celular con media, ct-2026-07-21-1358,
// popup backend A tarea 1) — triggers the on-demand media FIFO backfill for
// one chat: the popup calls this when the boss opens a chat, so its
// pending photos/audios/stickers/videos start downloading gradually
// instead of all at once. Fire-and-forget: Deps.MediaFetcher.FetchPendingMedia
// starts the paced work in the background (whatsmeow.Adapter's own
// goroutine) and returns immediately — this handler never blocks on
// however long the chat's backlog takes.
package restapi

import "net/http"

func (d Deps) handleFetchMedia(w http.ResponseWriter, r *http.Request) {
	if d.MediaFetcher == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "media fetcher not available"})
		return
	}
	var body struct {
		ChatID string `json:"chat_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.ChatID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "chat_id is required"})
		return
	}
	// siblingJIDs (ct-2026-07-29): pending media for this conversation can
	// be queued under a deduped-away @lid sibling, not the requested
	// (dedup-winning) jid — same gap handleMessages had. nil Store
	// degrades to just body.ChatID, same as siblingJIDs' own doc.
	siblings, err := d.siblingJIDs(r.Context(), body.ChatID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	for _, jid := range siblings {
		d.MediaFetcher.FetchPendingMedia(jid)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "fetch started"})
}
