package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/jobs"
	"github.com/tristenlammi/arrmada/internal/music"
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
			albums, err := a.deps.Music.AlbumsWithMissingFiles(ctx)
			if err != nil {
				return nil, err
			}
			grabbed, searched := 0, 0
			for i, al := range albums {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				p.Set(float64(i)/float64(max(len(albums), 1)), fmt.Sprintf("Searching %d / %d albums", i+1, len(albums)))
				out, err := a.deps.Automation.SearchAlbumNow(ctx, al.ID)
				if errors.Is(err, automation.ErrAlreadySearching) {
					continue // a search of it is running already
				}
				if err != nil && !errors.Is(err, music.ErrNotFound) {
					a.deps.Log.Warn("music: searching an album with missing files failed", "album", al.Title, "err", err)
				}
				searched++
				grabbed += out.Grabbed
			}
			p.SetMessage(fmt.Sprintf("Searched %s with missing files; %s grabbed", countOf(searched, "album"), countOf(grabbed, "release")))
			return map[string]int{"albums": searched, "grabbed": grabbed}, nil
		}})
	if !ok {
		return
	}
	a.accepted(w, jobID, existing, map[string]any{"status": "searching"})
}
