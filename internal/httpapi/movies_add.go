package httpapi

import (
	"errors"
	"net/http"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/movies"
)

// maxBulkAdd bounds one bulk add: a collection is a handful of films, and each new one is
// a metadata fetch made while the request waits.
const maxBulkAdd = 100

type bulkAddMoviesRequest struct {
	TMDBIDs        []int  `json:"tmdb_ids"`
	QualityProfile string `json:"quality_profile"`
	// MinAvailability: announced | inCinemas | released ("" = released).
	MinAvailability string `json:"min_availability"`
	Monitored       *bool  `json:"monitored"` // default true
	// Search queues a search for each film added; default the "search on add" setting.
	// Off adds them unmonitored, as the single add does: a monitored film with no file
	// would be searched by the sweeps anyway.
	Search *bool `json:"search"`
}

// bulkAddResult is what happened to one TMDB id.
type bulkAddResult struct {
	TMDBID int    `json:"tmdb_id"`
	Status string `json:"status"` // added | exists | failed
	ID     int64  `json:"id,omitempty"`
	Title  string `json:"title,omitempty"`
	Error  string `json:"error,omitempty"`
	Queued bool   `json:"queued,omitempty"` // a search was queued for it
}

// handleBulkAddMovies adds several films in one call — a collection's "Add all". It adds
// them one after another (each a metadata fetch through the cache), skips films already in
// the library, and queues each new film's search in the movie search queue rather than
// running it here. The answer has a result per id, in the order asked.
func (a *api) handleBulkAddMovies(w http.ResponseWriter, r *http.Request) {
	var req bulkAddMoviesRequest
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if len(req.TMDBIDs) == 0 || len(req.TMDBIDs) > maxBulkAdd {
		a.writeError(w, http.StatusBadRequest, "tmdb_ids needs between 1 and 100 films")
		return
	}
	for _, id := range req.TMDBIDs {
		if id <= 0 {
			a.writeError(w, http.StatusBadRequest, "tmdb_ids must be TMDB movie ids")
			return
		}
	}
	if req.MinAvailability != "" && !movies.ValidAvailability(req.MinAvailability) {
		a.writeError(w, http.StatusBadRequest, "min_availability must be announced, inCinemas or released")
		return
	}
	if !a.deps.Movies.MetadataAvailable() {
		a.writeError(w, http.StatusBadRequest, "movie metadata isn't configured — add a TMDB key in Settings → Connections (free from themoviedb.org)")
		return
	}
	ctx := r.Context()
	monitored := req.Monitored == nil || *req.Monitored
	search := a.deps.Settings.GetBool(ctx, keySearchOnAdd, true)
	if req.Search != nil {
		search = *req.Search
	}
	if !search {
		monitored = false
	}
	if req.QualityProfile == "" && a.deps.Quality != nil {
		req.QualityProfile = a.deps.Quality.DefaultProfile(ctx, "movie")
	}
	have, err := a.deps.Movies.LibraryIDs(ctx)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not read the library")
		return
	}

	out := make([]bulkAddResult, 0, len(req.TMDBIDs))
	seen := map[int]bool{}
	var added, existing, failed int
	for _, tmdbID := range req.TMDBIDs {
		if seen[tmdbID] {
			continue
		}
		seen[tmdbID] = true
		res := bulkAddResult{TMDBID: tmdbID}
		if id, ok := have[tmdbID]; ok {
			res.Status, res.ID = "exists", id
			existing++
			out = append(out, res)
			continue
		}
		if ctx.Err() != nil {
			res.Status, res.Error = "failed", "the request ended before this film was added"
			failed++
			out = append(out, res)
			continue
		}
		m, err := a.deps.Movies.AddWith(ctx, tmdbID, movies.AddOptions{
			QualityProfile: req.QualityProfile, Monitored: monitored, MinAvailability: req.MinAvailability,
		})
		switch {
		case errors.Is(err, movies.ErrExists):
			// Added since the library was read (a request, another tab).
			res.Status = "exists"
			if cur, gerr := a.deps.Movies.GetByTMDB(ctx, tmdbID); gerr == nil {
				res.ID = cur.ID
			}
			existing++
		case err != nil:
			res.Status, res.Error = "failed", err.Error()
			failed++
		default:
			res.Status, res.ID, res.Title = "added", m.ID, m.Title
			added++
			if m.Monitored && search {
				if _, qerr := a.enqueueMovie(r, m.ID, triggered(automation.TriggerAdd, a.movieSearchJob(m.ID))); qerr == nil {
					res.Queued = true
				}
			}
		}
		out = append(out, res)
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"results": out, "added": added, "existing": existing, "failed": failed,
	})
}
