package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/jobs"
)

// Music searching from the album and artist pages (MUS-09, MUS-16). An album's Search now
// is Wanted's (POST /api/v1/wanted/music/{id}/search); these add the artist's Search now
// and where an album stands (its last search, its next one, and its upgrade state).

// artistSearchTimeout bounds an artist's Search now: one album search after another, each
// up to a few minutes when indexers are slow.
const artistSearchTimeout = 30 * time.Minute

// artistSearchJob searches an artist's wanted albums, one at a time (single-flight per
// artist through the job runner). interactive is a person's click: their search may ask
// an indexer that is backing off. The search an add starts is background work, and leaves
// those indexers alone, as a sweep does.
func (a *api) artistSearchJob(id int64, interactive bool) jobs.Spec {
	return jobs.Spec{Kind: "artist.search", Target: jobTarget("artist", id), Class: jobs.ClassIndexerSearch, Timeout: artistSearchTimeout,
		Fn: func(ctx context.Context, p *jobs.Progress) (any, error) {
			if interactive {
				ctx = indexer.WithInteractive(ctx)
			}
			ctx = automation.WithDefaultSearchTrigger(ctx, automation.TriggerManual)
			res, err := a.deps.Automation.SearchArtistNow(ctx, id, func(done, total int) {
				if total > 0 {
					p.Set(float64(done)/float64(total), fmt.Sprintf("Searched %d of %d albums", done, total))
				}
			})
			if err != nil {
				return res, err
			}
			p.SetMessage(res.Message())
			return res, nil
		}}
}

// handleArtistSearch is POST /api/v1/music/artists/{id}/search: Search now for every
// monitored album of the artist that is missing tracks (202 with job_id).
func (a *api) handleArtistSearch(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	if a.deps.Automation == nil {
		a.writeError(w, http.StatusServiceUnavailable, "searching isn't available")
		return
	}
	if _, err := a.deps.Music.GetArtist(r.Context(), id); err != nil {
		a.musicErr(w, err, "could not load the artist")
		return
	}
	jobID, existing, ok := a.submitOr503(w, r, a.artistSearchJob(id, true))
	if !ok {
		return
	}
	a.forgetWanted()
	a.accepted(w, jobID, existing, map[string]any{"status": "searching", "started_at_ms": time.Now().UnixMilli()})
}

// albumSearchState is GET /api/v1/music/albums/{id}/search-state: why the album is or
// isn't downloading. LastSearch is its latest search for missing tracks (upgrade searches
// are left out), NextSearchAt when the sweep looks again ("" = its next run), and Upgrade
// where it stands against its profile's cutoff.
type albumSearchState struct {
	LastSearch   *automation.AttemptSummary `json:"last_search,omitempty"`
	NextSearchAt string                     `json:"next_search_at,omitempty"`
	Upgrade      automation.AlbumUpgrade    `json:"upgrade"`
}

func (a *api) handleAlbumSearchState(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	if a.deps.Automation == nil {
		a.writeError(w, http.StatusServiceUnavailable, "searching isn't available")
		return
	}
	ctx := r.Context()
	al, err := a.deps.Music.AlbumRecord(ctx, id)
	if err != nil {
		a.musicErr(w, err, "could not load the album")
		return
	}
	var out albumSearchState
	if sums, err := a.deps.Automation.LatestAttempts(ctx, automation.AttemptMusic, []int64{id}); err == nil {
		if s, ok := sums[id]; ok {
			out.LastSearch = &s
		}
	}
	if !al.Complete() && al.Monitored && al.StaleSince == "" {
		slot := automation.SweepSchedule(automation.AttemptMusic, al.LastSearchAt, al.SearchMisses)
		if slot.Next.After(time.Now()) {
			out.NextSearchAt = slot.Next.UTC().Format(time.RFC3339)
		}
	}
	up, err := a.deps.Automation.AlbumUpgradeState(ctx, id)
	if err != nil {
		a.musicErr(w, err, "could not read the album's upgrade state")
		return
	}
	out.Upgrade = up
	a.writeJSON(w, http.StatusOK, out)
}
