package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/jobs"
)

// Activity → Needs you lists every stuck download from the attention feed
// (GET /api/v1/attention) and offers the action that fits each: retry a failing import,
// try another release for a stalled or errored download, move a misfiled TV download to
// the TV category. Held imports keep Review's own actions (/api/v1/reviews/...).

// handleRetryImport drops a failing import's back-off so the next import sweep tries the
// download again at once — once the cause (a folder Arrmada can't write, a full disk) is
// fixed. 404 when the download isn't failing any more.
//
//	POST /api/v1/imports/{hash}/retry
func (a *api) handleRetryImport(w http.ResponseWriter, r *http.Request) {
	hash, ok := a.torrentHash(w, r)
	if !ok {
		return
	}
	failing := false
	for _, f := range a.importFailures() {
		if strings.EqualFold(f.Hash, hash) {
			failing = true
			// The movie importer keys its back-off by the hash as the client spells it.
			if a.deps.Library != nil {
				a.deps.Library.RetryNow(f.Hash)
			}
		}
	}
	if !failing {
		a.writeError(w, http.StatusNotFound, "that import isn't failing any more")
		return
	}
	if a.deps.Automation != nil {
		a.deps.Automation.RetrySeriesImport(hash)
	}
	if a.deps.Attention != nil {
		a.deps.Attention.Kick()
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"status": "retrying"})
}

// handleFailOver tries another release for a stalled or errored download now, as a job:
// the replacement is grabbed first and the stuck torrent removed only then; with nothing
// better found it stays as it is.
//
//	POST /api/v1/queue/{hash}/failover
func (a *api) handleFailOver(w http.ResponseWriter, r *http.Request) {
	hash, ok := a.torrentHash(w, r)
	if !ok {
		return
	}
	if a.deps.Automation == nil {
		a.writeError(w, http.StatusServiceUnavailable, "automation isn't running")
		return
	}
	hash = strings.ToLower(hash)
	spec := jobs.Spec{Kind: "download.failover", Target: "torrent:" + hash, Class: jobs.ClassIndexerSearch, Timeout: 5 * time.Minute,
		Fn: interactive(func(ctx context.Context, p *jobs.Progress) (any, error) {
			out, err := a.deps.Automation.FailOverNow(ctx, hash)
			if err != nil {
				return out, err
			}
			p.SetMessage(failOverMessage(out))
			a.queueChanged(hash)
			return out, nil
		})}
	id, existing, ok := a.submitOr503(w, r, spec)
	if !ok {
		return
	}
	a.accepted(w, id, existing, map[string]any{"status": "searching"})
}

// failOverMessage is a finished fail-over in words.
func failOverMessage(out automation.FailOverResult) string {
	switch {
	case out.Replacement != "":
		return "Grabbed " + out.Replacement + " instead — the stuck download was removed and blocklisted."
	case out.Outage:
		return "Couldn't search the indexers just now — the download was left as it is."
	}
	return "No other release of " + out.Title + " was found — the download was left as it is."
}

// handleSetCategory files a finished download under one of Arrmada's own categories, so
// the import sweep for that kind picks it up: Needs you's "Move to TV" for a TV download
// sitting where the series import never looks. Only a download the series sweep found
// misfiled (a TV release matching a show in the library) can be moved, and only into
// Arrmada's categories.
//
//	POST /api/v1/queue/{hash}/category  {category}
func (a *api) handleSetCategory(w http.ResponseWriter, r *http.Request) {
	hash, ok := a.torrentHash(w, r)
	if !ok {
		return
	}
	var req struct {
		Category string `json:"category"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	cats := download.FixedCategories(a.deps.Config.DownloadCategory)
	if req.Category != cats.TV {
		a.writeError(w, http.StatusBadRequest, "a download can only be moved into Arrmada's TV category ("+cats.TV+")")
		return
	}
	misfiled := false
	if a.deps.Automation != nil {
		for _, it := range a.deps.Automation.WrongCategoryDownloads() {
			if strings.EqualFold(it.Hash, hash) {
				misfiled = true
			}
		}
	}
	if !misfiled {
		a.writeError(w, http.StatusConflict, "only a TV download sitting in the wrong category can be moved — this one isn't (any more)")
		return
	}
	if err := a.deps.Downloads.SetCategory(r.Context(), hash, req.Category); err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, download.ErrNoCategories) {
			status = http.StatusConflict
		}
		a.writeError(w, status, err.Error())
		return
	}
	a.deps.Log.Info("download moved to another category", "hash", hash, "category", req.Category)
	a.queueChanged(hash)
	a.writeJSON(w, http.StatusOK, map[string]any{"status": "moved", "category": req.Category})
}

// queueChanged tells open pages (every tab, not only the one that acted) that torrents
// changed after an action, without waiting for the progress loop's next read, which sees
// only phase changes. No hashes: something changed (Resume all), refetch the lists.
func (a *api) queueChanged(hashes ...string) {
	if a.deps.Bus == nil {
		return
	}
	if hashes == nil {
		hashes = []string{}
	}
	a.deps.Bus.Publish(download.TopicQueueChanged, download.QueueDelta{Added: []string{}, Removed: []string{}, Changed: hashes})
}

// hashesOf is a pause or resume target as queue.changed hashes: none for "all".
func hashesOf(hash string) []string {
	if hash == "all" {
		return nil
	}
	return []string{strings.ToLower(hash)}
}
