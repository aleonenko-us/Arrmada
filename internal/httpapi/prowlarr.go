package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/jobs"
)

const (
	keyProwlarrURL = indexer.SettingProwlarrURL
	keyProwlarrKey = indexer.SettingProwlarrKey
)

// prowlarrUIPort is the host port Prowlarr's web UI is published on. install.sh picks the
// first free one of 9696-9698 and compose passes it in as ARRMADA_PROWLARR_HOST_PORT; the
// link used to hard-code 9696 and so could open someone's older Prowlarr instead.
func prowlarrUIPort() string {
	if p := strings.TrimSpace(os.Getenv("ARRMADA_PROWLARR_HOST_PORT")); p != "" {
		return p
	}
	return "9696"
}

// handleProwlarrInfo returns the Prowlarr connection for the panel: the URL (saved
// override, else the configured/bundled default), whether an API key is stored (so the
// user needn't re-enter it), how the last sync went, and where Prowlarr's own web UI is —
// ui_url when one was saved for a Prowlarr on another machine, otherwise this host on
// ui_port.
func (a *api) handleProwlarrInfo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := map[string]any{
		"url":     a.deps.Settings.Get(ctx, keyProwlarrURL, a.deps.Config.ProwlarrURL),
		"has_key": a.deps.Settings.Get(ctx, keyProwlarrKey, "") != "",
		"ui_port": prowlarrUIPort(),
		"ui_url":  a.deps.Settings.Get(ctx, indexer.SettingProwlarrUIURL, ""),
	}
	if ls, ok := indexer.ReadProwlarrLastSync(ctx, a.deps.Settings); ok {
		out["last_sync"] = ls
	}
	a.writeJSON(w, http.StatusOK, out)
}

// handleProwlarrSettings saves the address of Prowlarr's web UI, for a Prowlarr that
// isn't on this host. Blank clears it. Only an http(s) address is kept: it becomes a link.
func (a *api) handleProwlarrSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UIURL string `json:"ui_url"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	ui := strings.TrimRight(strings.TrimSpace(req.UIURL), "/")
	if ui != "" {
		u, err := url.Parse(ui)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			a.writeError(w, http.StatusBadRequest, "Prowlarr's address must be an http:// or https:// link, like http://192.168.1.10:9696")
			return
		}
	}
	if err := a.deps.Settings.Set(r.Context(), indexer.SettingProwlarrUIURL, ui); err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not save Prowlarr's address")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"ui_url": ui})
}

// handleProwlarrSync pulls indexers from Prowlarr and mirrors them into Arrmada.
// Body {url, api_key} are optional — a blank URL falls back to the configured
// default, and a blank key reuses the stored one. Successful values are saved.
// add_flaresolverr_proxy adds Arrmada's FlareSolverr to a Prowlarr that isn't the
// bundled one (the bundled one always gets it).
func (a *api) handleProwlarrSync(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL                  string `json:"url"`
		APIKey               string `json:"api_key"`
		AddFlareSolverrProxy bool   `json:"add_flaresolverr_proxy"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	url := req.URL
	if url == "" {
		url = a.deps.Settings.Get(ctx, keyProwlarrURL, a.deps.Config.ProwlarrURL)
	}
	key := req.APIKey
	if key == "" {
		key = a.deps.Settings.Get(ctx, keyProwlarrKey, "")
	}
	// The FlareSolverr URL Prowlarr is pointed at is the one in Settings (env as fallback).
	flare := ""
	if a.deps.APIKeys != nil {
		flare = a.deps.APIKeys.Value(ctx, "flaresolverr")
	}
	res, err := a.deps.Indexers.SyncProwlarr(ctx, indexer.ProwlarrSync{
		URL: url, APIKey: key, FlareSolverrURL: flare, AddFlareSolverrProxy: req.AddFlareSolverrProxy,
	})
	if ctx.Err() == nil {
		// The panel's "Last synced" line and the health check read this, whoever synced.
		a.deps.Indexers.RecordProwlarrSync(ctx, a.deps.Settings, indexer.ProwlarrTriggerManual, res, err)
	}
	if err != nil {
		a.writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	// Remember what worked for next time.
	_ = a.deps.Settings.Set(ctx, keyProwlarrURL, url)
	_ = a.deps.Settings.Set(ctx, keyProwlarrKey, key)
	// Each new row's capabilities come from its own feed (one request per indexer through
	// the per-host throttle), so they're read in the background rather than holding up
	// the answer; the page shows them on its next read.
	if ids := res.AddedIDs; len(ids) > 0 {
		_, _, _ = a.submit(r, jobs.Spec{Kind: "indexer.caps", Target: "prowlarr", Class: jobs.ClassIndexerSearch, Timeout: 10 * time.Minute,
			Fn: func(ctx context.Context, _ *jobs.Progress) (any, error) {
				return nil, a.deps.Indexers.RefreshCaps(ctx, ids)
			}})
	}
	a.writeJSON(w, http.StatusOK, res)
}
