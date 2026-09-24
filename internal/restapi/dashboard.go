// The dashboard itself (ct-2026-07-10-2312): the static web/ page
// (internal/dashboard, embedded at compile time) plus the one piece it
// can't be pure-static about, the QR pairing image.
package restapi

import (
	"html"
	"io/fs"
	"net/http"
	"net/netip"
	"strings"

	"rsc.io/qr"

	"piumy-gateway/internal/config"
	"piumy-gateway/internal/dashboard"
)

// The static shell (index.html/style.css/app.js) is served WITHOUT the
// auth gate (ct-2026-07-19-1616): it carries zero secrets — it's the same
// bytes compiled into the binary either way — and its own JS is what shows
// the login overlay, so it has to be reachable before a session exists. (S5: a
// named account's index.html also carries its label — only for a request from
// this machine, see indexWithAccount.)
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
			// A named account serves its own index.html (indexWithAccount); the
			// default account, and every other file, go through fileServer untouched.
			if d.Account != "" && r.URL.Path == "" { // "" = "/dashboard/" itself, the index
				page, err := d.indexWithAccount(r)
				if err == nil {
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					w.Write(page)
					return
				}
			}
			fileServer.ServeHTTP(w, r)
		})))
	mux.HandleFunc("GET /api/qr/image", d.auth(d.handleQRImage))
}

// indexWithAccount is index.html with the account's identity already written
// in (S5, ct-2026-09-23-2038): the window title and the login screen carry the
// account BEFORE anyone signs in: /api/status, where it used to arrive, needs
// a session, so until then the window said a bare "Piumy Gateway" and the login
// didn't say which account it was for. The values go in as attributes of
// <body> (app.js paints from them) and are HTML-escaped: the label can be a
// WhatsApp display name, anyone's text. Read per request, not once: the label
// changes when the session gets a name.
//
// This page needs no session and its port listens on every interface, while
// the label carries the owner's WhatsApp name and number tail — personal data.
// Only a request from this very machine (the app window) gets them; anyone else
// on the network gets the account id. The full label reaches them after signing
// in, through /api/status.
func (d Deps) indexWithAccount(r *http.Request) ([]byte, error) {
	page, err := fs.ReadFile(dashboard.WebFS(), "index.html")
	if err != nil {
		return nil, err
	}
	var ownName, ownJID string
	if d.State != nil && isLoopback(r.RemoteAddr) {
		snap := d.State.Snapshot()
		ownName, ownJID = snap.OwnName, snap.OwnJID
	}
	label := html.EscapeString(config.AccountLabel(d.Account, ownName, ownJID))
	color := html.EscapeString(config.ColorForAccount(d.Account).Hex)
	out := strings.Replace(string(page), "<title>Piumy Gateway</title>", "<title>Piumy Gateway — "+label+"</title>", 1)
	out = strings.Replace(out, "<body>", `<body data-account="`+label+`" data-account-color="`+color+`">`, 1)
	return []byte(out), nil
}

// isLoopback reports whether a request's RemoteAddr ("host:port") is this
// machine itself.
func isLoopback(remoteAddr string) bool {
	ap, err := netip.ParseAddrPort(remoteAddr)
	return err == nil && ap.Addr().IsLoopback()
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
