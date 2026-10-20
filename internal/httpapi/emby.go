package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/tristenlammi/arrmada/internal/connstatus"
	"github.com/tristenlammi/arrmada/internal/embysvc"
	"github.com/tristenlammi/arrmada/internal/plexscan"
)

// The Emby connection's routes (its card on Settings → Connections). The API key goes
// up on PUT and never comes back: answers say key_set only, and a Test's detail never
// carries it.

const embyHealthKey = "emby.connection"

func (a *api) embyReady(w http.ResponseWriter) bool {
	if a.deps.Emby == nil {
		a.writeError(w, http.StatusServiceUnavailable, "Emby isn't available")
		return false
	}
	return true
}

// handleEmbyConfig answers GET /api/v1/emby.
func (a *api) handleEmbyConfig(w http.ResponseWriter, r *http.Request) {
	if !a.embyReady(w) {
		return
	}
	a.writeJSON(w, http.StatusOK, a.deps.Emby.Config(r.Context()))
}

// handleEmbySave answers PUT /api/v1/emby {url, public_url, api_key?}: a missing
// or blank key keeps the saved one. Answers with the saved connection.
func (a *api) handleEmbySave(w http.ResponseWriter, r *http.Request) {
	if !a.embyReady(w) {
		return
	}
	var req struct {
		URL       string `json:"url"`
		PublicURL string `json:"public_url"`
		APIKey    string `json:"api_key"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := a.deps.Emby.Save(ctx, req.URL, req.PublicURL, req.APIKey); err != nil {
		if errors.Is(err, embysvc.ErrInvalid) {
			a.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.deps.Log.Warn("emby: couldn't save the connection", "err", err)
		a.writeError(w, http.StatusInternalServerError, "could not save the Emby connection")
		return
	}
	a.recheckHealth(r, embyHealthKey) // a fixed key clears its warning now
	a.writeJSON(w, http.StatusOK, a.deps.Emby.Config(r.Context()))
}

// handleEmbyDisconnect answers DELETE /api/v1/emby (admin): the address, the key
// and the server's identity are forgotten; how Arrmada used it stays for a reconnect.
// Answers with the (now empty) connection.
func (a *api) handleEmbyDisconnect(w http.ResponseWriter, r *http.Request) {
	if !a.embyReady(w) {
		return
	}
	if err := a.deps.Emby.Disconnect(r.Context()); err != nil {
		a.deps.Log.Warn("emby: couldn't disconnect", "err", err)
		a.writeError(w, http.StatusInternalServerError, "could not disconnect Emby")
		return
	}
	a.deps.Log.Info("emby: disconnected")
	a.recheckHealth(r, embyHealthKey) // an unconfigured server has nothing to warn about
	a.writeJSON(w, http.StatusOK, a.deps.Emby.Config(r.Context()))
}

// handleEmbyTest answers POST /api/v1/emby/test {url?, api_key?}: the typed values
// (blank = the saved ones) tested without saving. Always 200: ok says how it went.
func (a *api) handleEmbyTest(w http.ResponseWriter, r *http.Request) {
	if !a.embyReady(w) {
		return
	}
	var req struct {
		URL    string `json:"url"`
		APIKey string `json:"api_key"`
	}
	if !a.decodeJSON(w, r, &req) { // {} tests the saved connection
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	res := a.deps.Emby.Test(ctx, req.URL, req.APIKey)
	res.Detail = connstatus.Redact(res.Detail)
	a.writeJSON(w, http.StatusOK, res)
}

// GET/PUT /api/v1/emby/scan and POST /api/v1/emby/scan/test: Emby's library
// updates, the same shapes as Plex's (/insights/plex/scan). PUT takes {enabled, path_map};
// scan_on_import is accepted for enabled.
func (a *api) handleEmbyScanView(w http.ResponseWriter, r *http.Request) {
	if !a.scannerReady(w, a.deps.EmbyScan, "Emby") {
		return
	}
	a.scanView(w, r, a.deps.EmbyScan)
}

func (a *api) handleEmbyScanSave(w http.ResponseWriter, r *http.Request) {
	if !a.scannerReady(w, a.deps.EmbyScan, "Emby") {
		return
	}
	var req struct {
		Enabled      *bool               `json:"enabled"`
		ScanOnImport *bool               `json:"scan_on_import"`
		PathMap      *[]plexscan.PathMap `json:"path_map"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if req.Enabled == nil {
		req.Enabled = req.ScanOnImport
	}
	a.scanSave(w, r, a.deps.EmbyScan, req.Enabled, req.PathMap)
}

func (a *api) handleEmbyScanTest(w http.ResponseWriter, r *http.Request) {
	if !a.scannerReady(w, a.deps.EmbyScan, "Emby") {
		return
	}
	a.scanTest(w, r, a.deps.EmbyScan)
}
