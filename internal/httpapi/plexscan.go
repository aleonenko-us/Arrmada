package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/tristenlammi/arrmada/internal/plexscan"
)

// The library-updates cards (Plex's, and Jellyfin's in jellyfin.go): whether Arrmada tells
// the server to scan after changes, how each library folder maps onto the server's, and
// the last scan. Paths only — the token or key and the server address never appear here.

func (a *api) plexScanReady(w http.ResponseWriter) bool {
	return a.scannerReady(w, a.deps.PlexScan, "Plex")
}

func (a *api) scannerReady(w http.ResponseWriter, sc *plexscan.Scanner, server string) bool {
	if sc == nil {
		a.writeError(w, http.StatusServiceUnavailable, server+" scanning isn't available")
		return false
	}
	return true
}

// handlePlexScanView answers GET /insights/plex/scan.
func (a *api) handlePlexScanView(w http.ResponseWriter, r *http.Request) {
	if !a.plexScanReady(w) {
		return
	}
	a.scanView(w, r, a.deps.PlexScan)
}

func (a *api) scanView(w http.ResponseWriter, r *http.Request, sc *plexscan.Scanner) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	a.writeJSON(w, http.StatusOK, sc.View(ctx))
}

// handlePlexScanSave answers PUT /insights/plex/scan {enabled, path_map}.
func (a *api) handlePlexScanSave(w http.ResponseWriter, r *http.Request) {
	if !a.plexScanReady(w) {
		return
	}
	var req struct {
		Enabled *bool               `json:"enabled"`  // absent = unchanged
		PathMap *[]plexscan.PathMap `json:"path_map"` // absent = unchanged
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	a.scanSave(w, r, a.deps.PlexScan, req.Enabled, req.PathMap)
}

func (a *api) scanSave(w http.ResponseWriter, r *http.Request, sc *plexscan.Scanner, enabledReq *bool, mapReq *[]plexscan.PathMap) {
	enabled := sc.Enabled(r.Context())
	if enabledReq != nil {
		enabled = *enabledReq
	}
	maps := sc.PathMaps(r.Context())
	if mapReq != nil {
		maps = *mapReq
	}
	if len(maps) > plexscan.MaxPathMaps*2 {
		a.writeError(w, http.StatusBadRequest, "too many path mappings")
		return
	}
	if err := sc.Save(r.Context(), enabled, maps); err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	a.writeJSON(w, http.StatusOK, sc.View(ctx))
}

// handlePlexScanTest answers POST /insights/plex/scan/test {kind, run}: where kind's
// library folder resolves on Plex, and with run, a scan of it now.
func (a *api) handlePlexScanTest(w http.ResponseWriter, r *http.Request) {
	if !a.plexScanReady(w) {
		return
	}
	a.scanTest(w, r, a.deps.PlexScan)
}

func (a *api) scanTest(w http.ResponseWriter, r *http.Request, sc *plexscan.Scanner) {
	var req struct {
		Kind string `json:"kind"`
		Run  bool   `json:"run"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rv, err := sc.ScanNow(ctx, req.Kind, req.Run)
	if err != nil {
		status := http.StatusBadGateway // the server didn't answer, or refused
		if errors.Is(err, plexscan.ErrUnavailable) {
			status = http.StatusBadRequest
		}
		a.writeJSON(w, status, map[string]any{"status": "error", "message": err.Error(), "root": rv})
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"root": rv, "last_scan": sc.LastScan()})
}
