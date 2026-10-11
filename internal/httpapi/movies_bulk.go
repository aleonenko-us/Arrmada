package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/movies"
)

// The mass editor's endpoints. One request changes a whole selection: the fields commit
// together or not at all, and the searches it starts join the throttled movie search
// queue like every other movie search.

// Bulk search modes: none, missing only, or missing plus an upgrade search for films whose
// file still fits the profile.
const (
	bulkSearchNone    = "none"
	bulkSearchMissing = "missing"
	bulkSearchAll     = "missing_and_upgrade"
)

// exceedsItem is a film whose file the (new) profile no longer accepts. It is left alone;
// the page offers to download a smaller version (POST /movies/bulk/regrab).
type exceedsItem struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Smaller   bool   `json:"smaller"` // above a ceiling: a smaller release fixes it
}

// bulkItemError is one film the request couldn't act on, and why.
type bulkItemError struct {
	ID  int64  `json:"id"`
	Msg string `json:"msg"`
}

// handleBulkUpdateMovies is PUT /movies/bulk: set monitoring, the quality profile and/or
// the minimum availability on many films in one transaction, then (search) queue what each
// film needs. An unknown profile, a bad availability or a film that's gone rejects the
// whole request and changes nothing.
func (a *api) handleBulkUpdateMovies(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs             []int64 `json:"ids"`
		Monitored       *bool   `json:"monitored"`
		QualityProfile  *string `json:"quality_profile"`
		MinAvailability *string `json:"min_availability"`
		Search          string  `json:"search"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	ids := uniqueIDs(req.IDs)
	if len(ids) == 0 || len(ids) > maxBulkSearch {
		a.writeError(w, http.StatusBadRequest, fmt.Sprintf("ids must list between 1 and %d movies", maxBulkSearch))
		return
	}
	if req.Search == "" {
		req.Search = bulkSearchNone
	}
	switch req.Search {
	case bulkSearchNone, bulkSearchMissing, bulkSearchAll:
	default:
		a.writeError(w, http.StatusBadRequest, "search must be none, missing or missing_and_upgrade")
		return
	}
	if req.Monitored == nil && req.QualityProfile == nil && req.MinAvailability == nil && req.Search == bulkSearchNone {
		a.writeError(w, http.StatusBadRequest, "nothing to change")
		return
	}
	// Validated once, before anything is written.
	if req.QualityProfile != nil && (a.deps.Automation == nil || !a.deps.Automation.KnownProfile(r.Context(), *req.QualityProfile)) {
		a.writeError(w, http.StatusBadRequest, "unknown quality profile")
		return
	}
	if req.MinAvailability != nil && !movies.ValidAvailability(*req.MinAvailability) {
		a.writeError(w, http.StatusBadRequest, "min_availability must be announced, inCinemas or released")
		return
	}
	if req.Search != bulkSearchNone && a.deps.Automation == nil {
		a.writeError(w, http.StatusServiceUnavailable, "searching isn't available")
		return
	}

	ch := movies.BulkChange{Monitored: req.Monitored, QualityProfile: req.QualityProfile, MinAvailability: req.MinAvailability}
	if req.Monitored != nil || req.QualityProfile != nil || req.MinAvailability != nil {
		if err := a.deps.Movies.BulkUpdate(r.Context(), ids, ch); err != nil {
			var missing *movies.MissingIDsError
			if errors.As(err, &missing) {
				a.writeJSON(w, http.StatusNotFound, map[string]any{"status": "error", "message": err.Error(), "missing": missing.IDs})
				return
			}
			a.writeError(w, http.StatusInternalServerError, "could not update the movies — nothing was changed")
			return
		}
	}

	// After the commit: what each film needs now.
	resp := struct {
		Updated        int             `json:"updated"`
		Queued         int             `json:"queued"`
		Duplicates     int             `json:"duplicates"`
		NotMonitored   int             `json:"not_monitored"`
		ExceedsProfile []exceedsItem   `json:"exceeds_profile"`
		Errors         []bulkItemError `json:"errors"`
	}{ExceedsProfile: []exceedsItem{}, Errors: []bulkItemError{}}
	if req.Monitored != nil || req.QualityProfile != nil || req.MinAvailability != nil {
		resp.Updated = len(ids)
	}
	judge := req.QualityProfile != nil || req.Search == bulkSearchAll
	if req.Search == bulkSearchNone && !judge {
		a.writeJSON(w, http.StatusOK, resp)
		return
	}
	var batch *automation.UpgradeBatch
	for _, id := range ids {
		m, err := a.deps.Movies.Get(r.Context(), id)
		if err != nil {
			resp.Errors = append(resp.Errors, bulkItemError{ID: id, Msg: "couldn't read the movie"})
			continue
		}
		if !m.Monitored {
			resp.NotMonitored++ // never searched or upgraded while unmonitored
			continue
		}
		var spec = a.movieBulkSearchJob(id)
		switch {
		case !m.HasFile:
			if req.Search == bulkSearchNone {
				continue
			}
		default:
			if judge && a.deps.Quality != nil {
				rej, rejected := a.deps.Quality.WouldRejectReason(r.Context(), m.QualityProfile, a.deps.Automation.CurrentMovieFile(r.Context(), m))
				if rejected {
					it := exceedsItem{ID: m.ID, Title: m.Title, Reason: rej.Reason, Smaller: rej.Ceiling != ""}
					if m.File != nil {
						it.SizeBytes = m.File.SizeBytes
					}
					resp.ExceedsProfile = append(resp.ExceedsProfile, it)
					continue
				}
			}
			if req.Search != bulkSearchAll {
				continue
			}
			if batch == nil {
				batch = a.deps.Automation.NewUpgradeBatch(r.Context())
			}
			spec = a.movieBulkUpgradeJob(id, batch)
		}
		q, err := a.enqueueMovie(r, id, spec)
		switch {
		case err != nil:
			resp.Errors = append(resp.Errors, bulkItemError{ID: id, Msg: "couldn't queue its search just now"})
		case q.Existing:
			resp.Duplicates++
		default:
			resp.Queued++
		}
	}
	a.writeJSON(w, http.StatusOK, resp)
}

// handleBulkRegrabMovies is POST /movies/bulk/regrab: "Download smaller versions" for the
// films a profile change said no longer fit. Each is re-searched under its current profile
// through the movie search queue; its file is replaced when the new one imports.
func (a *api) handleBulkRegrabMovies(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	ids := uniqueIDs(req.IDs)
	if len(ids) == 0 || len(ids) > maxBulkSearch {
		a.writeError(w, http.StatusBadRequest, fmt.Sprintf("ids must list between 1 and %d movies", maxBulkSearch))
		return
	}
	if a.deps.Automation == nil {
		a.writeError(w, http.StatusServiceUnavailable, "searching isn't available")
		return
	}
	queued, duplicates := 0, 0
	errs := []bulkItemError{}
	for _, id := range ids {
		q, err := a.enqueueMovie(r, id, a.movieRegrabJob(id))
		switch {
		case err != nil:
			errs = append(errs, bulkItemError{ID: id, Msg: "couldn't queue its search just now"})
		case q.Existing:
			duplicates++
		default:
			queued++
		}
	}
	a.writeJSON(w, http.StatusAccepted, map[string]any{"queued": queued, "duplicates": duplicates, "errors": errs})
}

// bulkDeleteResult is what deleting one film did.
type bulkDeleteResult struct {
	ID      int64    `json:"id"`
	Deleted bool     `json:"deleted"`
	Error   string   `json:"error,omitempty"`
	Moved   []string `json:"moved,omitempty"`  // files already in the bin when the bin refused
	Failed  []string `json:"failed,omitempty"` // the file the bin refused
}

// handleBulkDeleteMovies is DELETE /movies/bulk: remove many films, each exactly as the
// movie page's Delete does (its downloads settled, its files to the recycle bin when
// asked). Results are per film. When the bin refuses a file the film is kept, and the
// films after it aren't tried: a bin that refused one will refuse the rest, and nothing
// is ever deleted for good as a fallback.
func (a *api) handleBulkDeleteMovies(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs             []int64 `json:"ids"`
		DeleteFiles     bool    `json:"delete_files"`
		CancelDownloads bool    `json:"cancel_downloads"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	ids := uniqueIDs(req.IDs)
	if len(ids) == 0 || len(ids) > maxBulkSearch {
		a.writeError(w, http.StatusBadRequest, fmt.Sprintf("ids must list between 1 and %d movies", maxBulkSearch))
		return
	}
	opts := automation.DeleteMovieOpts{DeleteFiles: req.DeleteFiles, CancelDownloads: req.CancelDownloads}
	// Detached: moving files to a bin on another disk is a copy, and a browser giving up
	// part-way must not leave a film's files binned while the film stays.
	ctx := context.WithoutCancel(r.Context())
	results := make([]bulkDeleteResult, 0, len(ids))
	deleted := 0
	binRefused := false
	for _, id := range ids {
		if binRefused {
			results = append(results, bulkDeleteResult{ID: id, Error: "not tried: the recycle bin refused an earlier file"})
			continue
		}
		var err error
		if a.deps.Automation != nil {
			err = a.deps.Automation.DeleteMovie(ctx, id, opts)
		} else {
			err = a.deps.Movies.Delete(ctx, id, opts.DeleteFiles)
		}
		res := bulkDeleteResult{ID: id, Deleted: err == nil}
		if err != nil {
			var fe *movies.FilesNotRemovedError
			switch {
			case errors.Is(err, movies.ErrNotFound):
				res.Error = "not in the library"
			case errors.As(err, &fe):
				res.Error, res.Moved, res.Failed = err.Error(), fe.Moved, fe.Failed
				binRefused = true
			case errors.Is(err, library.ErrBinRefused) || errors.Is(err, library.ErrReplacementRefused):
				res.Error = err.Error()
				binRefused = true
			default:
				res.Error = "couldn't delete it"
				a.deps.Log.Warn("bulk delete: movie not deleted", "movie_id", id, "err", err)
			}
		} else {
			deleted++
		}
		results = append(results, res)
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted, "failed": len(ids) - deleted, "results": results})
}
