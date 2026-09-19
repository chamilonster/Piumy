// The token-report seam (F4-DESIGN §8/§9): CleverCoder reports real token
// usage per dispatch here. Nothing calls this yet — until it does,
// store.BlendUsage's own fallback (no real tokens reported -> pure
// estimate) IS the "runs without the real report" behavior; no separate
// stub component is needed on top of that.
package restapi

import (
	"net/http"

	"piumy-gateway/internal/store"
)

func (d Deps) registerMeteringRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/metering/tokens", d.auth(d.handleReportTokens))
}

func (d Deps) handleReportTokens(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		ChatJID string  `json:"chat_jid"`
		Day     string  `json:"day"`
		Tokens  float64 `json:"tokens"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.ChatJID == "" || body.Tokens <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "chat_jid and a positive tokens are required"})
		return
	}
	day := body.Day
	if day == "" {
		day = store.Today()
	}
	if err := d.Store.AddUsage(body.ChatJID, day, store.UsageDelta{TokensReal: body.Tokens}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "tokens recorded"})
}
