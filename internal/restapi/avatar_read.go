// GET /api/avatar (T17 Parte 3, ct-2026-08-05-1240) — serves a chat's
// cached profile-picture bytes, same binary-response shape as handleMedia
// (media_read.go), gated by the same d.auth() (session-or-?key=) since an
// <img> src can't send the X-API-Key header.
package restapi

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
)

// handleAvatar serves jid's cached avatar bytes if present, and nudges a
// paced background re-check via d.Avatars — never blocks on the network:
// a miss or a stale-but-present cache both return immediately, a fresher
// photo (if any) only lands on a LATER request once the background fetch
// completes. No cached row at all -> 404, so the frontend falls back to
// initials (T17's own "sin foto, sin hueco" requirement) instead of
// waiting on a check that hasn't even started pacing yet.
func (d Deps) handleAvatar(w http.ResponseWriter, r *http.Request) {
	jid := r.URL.Query().Get("jid")
	if jid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "jid is required"})
		return
	}
	if d.Avatars != nil {
		d.Avatars.RequestAvatar(jid)
	}
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	a, ok, err := d.Store.GetAvatar(jid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !ok || a.Path == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no avatar cached"})
		return
	}
	f, err := os.Open(a.Path)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "avatar file not found on disk"})
		return
	}
	defer f.Close()
	if ct := mime.TypeByExtension(filepath.Ext(a.Path)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	info, err := f.Stat()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// no-cache (2026-09-02): ServeContent manda Last-Modified pero NINGUN
	// Cache-Control, y sin el el navegador cachea por heuristica sobre esa
	// fecha. La URL de un avatar no cambia nunca (?jid=...), asi que el
	// navegador se queda con la foto vieja aunque el gateway ya tenga la
	// nueva en disco — que es exactamente lo que reporto el dueno: el
	// endpoint devolvia 80.060 bytes de foto nueva y la pantalla seguia
	// mostrando la anterior. must-revalidate NO significa "descargar
	// siempre": con el Last-Modified que ServeContent ya manda, el navegador
	// pregunta y recibe un 304 vacio mientras la foto no cambie.
	// Mismo defecto que el de los estaticos del tablero (dashboard.go), en
	// otra ruta que no se toco entonces.
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}
