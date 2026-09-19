// GET /api/media (dashboard popup celular con media) — serves the raw bytes
// of a downloaded media file. Same binary-response shape as dashboard.go's
// handleQRImage (Content-Type + written bytes, not JSON), gated by the same
// d.auth() (session-or-?key=) since an <img>/<audio> src can't send the
// X-API-Key header.
package restapi

import (
	"net/http"
	"os"
)

// handleMedia serves a message's media bytes. ?full=1 serves the original,
// uncompressed file (store.Media.FullPath, same file get_media_full hands
// the agent); without it, store.Media.Path — already the low-q copy for
// images, or the original as-is when there's no low-q variant (see
// store.Media's doc). A missing DB row OR a missing file on disk (the known
// disk<->DB drift from the backfill) both degrade to a clean 404, never a
// 500/panic.
func (d Deps) handleMedia(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	chatJID := r.URL.Query().Get("chat")
	msgID := r.URL.Query().Get("msg")
	if chatJID == "" || msgID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "chat and msg are required"})
		return
	}
	m, ok, err := d.Store.GetMedia(chatJID, msgID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "media not found"})
		return
	}
	path := m.Path
	if r.URL.Query().Get("full") == "1" {
		path = m.FullPath
	}
	f, err := os.Open(path)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "media file not found on disk"})
		return
	}
	defer f.Close()
	if m.Mime != "" {
		w.Header().Set("Content-Type", m.Mime)
	}
	info, err := f.Stat()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}
