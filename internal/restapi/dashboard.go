// The dashboard itself (ct-2026-07-10-2312): the static web/ page
// (internal/dashboard, embedded at compile time) plus the one piece it
// can't be pure-static about, the QR pairing image.
package restapi

import (
	"net/http"

	"rsc.io/qr"

	"piumy-gateway/internal/dashboard"
)

// The static shell (index.html/style.css/app.js) is served WITHOUT the
// auth gate (ct-2026-07-19-1616): it carries zero secrets — it's the same
// bytes compiled into the binary either way — and its own JS is what shows
// the login overlay, so it has to be reachable before a session exists.
// Every actual piece of data the shell fetches (GET /api/status, /api/chats,
// /api/qr/image, ...) still goes through auth() (session-or-key) unchanged.
func (d Deps) registerDashboardRoutes(mux *http.ServeMux) {
	fileServer := http.FileServer(http.FS(dashboard.WebFS()))
	mux.HandleFunc("GET /dashboard", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dashboard/", http.StatusMovedPermanently)
	})
	// no-cache en los estaticos (2026-09-01): van embebidos con go:embed,
	// que no tiene mtime, asi que FileServer no manda Last-Modified ni ETag
	// y el navegador cachea por heuristica. Sin esto, actualizar el gateway
	// deja al usuario viendo el CSS/JS anterior y el cambio parece no haberse
	// hecho. Son bytes locales y chicos: revalidar siempre no cuesta nada.
	mux.Handle("GET /dashboard/", http.StripPrefix("/dashboard/",
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache, must-revalidate")
			fileServer.ServeHTTP(w, r)
		})))
	mux.HandleFunc("GET /api/qr/image", d.auth(d.handleQRImage))
}

// handleQRImage renders the current pairing code as a PNG — same QRData
// string qrterminal already prints as ASCII to the console (main.go), just
// a different renderer for the dashboard's <img>. rsc.io/qr was already a
// transitive dependency (via qrterminal itself); this only promotes it to a
// direct import, no new third-party dependency added.
func (d Deps) handleQRImage(w http.ResponseWriter, r *http.Request) {
	if d.State == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "state not available"})
		return
	}
	snap := d.State.Snapshot()
	if !snap.ShowQR || snap.QRData == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no QR pending"})
		return
	}
	code, err := qr.Encode(snap.QRData, qr.L)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Write(code.PNG())
}
