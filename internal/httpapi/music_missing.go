package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/tristenlammi/arrmada/internal/jobs"
)

// "Search for missing tracks" (MUS-15): the file check flags track files gone from disk but
// never downloads them again by itself — they may only be renamed, or on a disk that's
// away. A person asks for the search here, for one album or for every album with gone
// files.

// handleSearchAlbumMissing is POST /api/v1/music/albums/{id}/search-missing: search for an
// album whose track files are gone from disk, now, as a job (the album search, so a second
// click joins the running one).
func (a *api) handleSearchAlbumMissing(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	if _, err := a.deps.Music.AlbumRecord(r.Context(), id); err != nil {
		a.musicErr(w, err, "could not read that album")
		return
	}
	jobID, existing, ok := a.submitOr503(w, r, a.albumSearchJob(id))
	if !ok {
		return
	}
	a.accepted(w, jobID, existing, map[string]any{"status": "searching"})
}

// handleMissingTracksCount is GET /api/v1/music/search-missing: how many albums have track
// files gone from disk, for the Music page's bulk action.
func (a *api) handleMissingTracksCount(w http.ResponseWriter, r *http.Request) {
	albums, err := a.deps.Music.AlbumsWithMissingFiles(r.Context())
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not list albums with missing files")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"albums": len(albums)})
}

// handleSearchAllMissing is POST /api/v1/music/search-missing: search for every album with
// track files gone from disk, one after another, as one job.
func (a *api) handleSearchAllMissing(w http.ResponseWriter, r *http.Request) {
	jobID, existing, ok := a.submitOr503(w, r, jobs.Spec{Kind: "music.search-missing", Target: "all", Class: jobs.ClassIndexerSearch,
		Timeout: 6 * time.Hour,
		Fn: func(ctx context.Context, p *jobs.Progress) (any, error) {
			res, err := a.deps.Automation.SearchMissingTracks(ctx, func(done, total int) {
				if total > 0 {
					p.Set(float64(done)/float64(total), fmt.Sprintf("Searching %d / %d albums", min(done+1, total), total))
				}
			})
			if err != nil {
				return nil, err
			}
			msg := fmt.Sprintf("Searched %s with missing files; %s grabbed", countOf(res.Searched, "album"), countOf(res.Grabbed, "release"))
			switch {
			case res.Stopped:
				msg += fmt.Sprintf(". Stopped: no indexer is answering; %s still to search — try again later", countOf(res.Left, "album"))
			case res.Left > 0:
				msg += fmt.Sprintf(". %s still to search — run it again", countOf(res.Left, "album"))
			}
			p.SetMessage(msg + ".")
			return res, nil
		}})
	if !ok {
		return
	}
	a.accepted(w, jobID, existing, map[string]any{"status": "searching"})
}
