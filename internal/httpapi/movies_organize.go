package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/jobs"
	"github.com/tristenlammi/arrmada/internal/movies"
)

// Organize applies the naming scheme across the library (or a selection) after a naming
// change: a dry-run preview first, then the renames as one background job. It reuses the
// per-movie rename (movies.RenamePlan / ApplyRename), so it never overwrites a file and
// skips anything that changed since the preview.

// organizeCounts is what a preview found: how many tracks would be renamed, are already
// right, can't be renamed without overwriting something, or have no file on disk.
type organizeCounts struct {
	Rename   int `json:"rename"`
	Same     int `json:"same"`
	Conflict int `json:"conflict"`
	Missing  int `json:"missing"`
}

// organizeSummary is the organize job's result.
type organizeSummary struct {
	Total   int `json:"total"`
	Renamed int `json:"renamed"`
	Skipped int `json:"skipped"`
}

const (
	organizePageDefault = 200
	organizePageMax     = 1000
)

// organizeIDs reads ?ids=1,2,3 ("" or "all" = the whole library).
func organizeIDs(raw string) ([]int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "all" {
		return nil, nil
	}
	var ids []int64
	for _, part := range strings.Split(raw, ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("ids must be movie ids separated by commas, or all")
		}
		ids = append(ids, id)
	}
	ids = uniqueIDs(ids)
	if len(ids) > maxBulkSearch {
		return nil, fmt.Errorf("ids must list at most %d movies", maxBulkSearch)
	}
	return ids, nil
}

// handleOrganizePreview is the dry run: counts first, then the tracks whose path would
// change and the conflicts, a page at a time, and a token naming exactly this plan.
// Nothing moves.
func (a *api) handleOrganizePreview(w http.ResponseWriter, r *http.Request) {
	ids, err := organizeIDs(r.URL.Query().Get("ids"))
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = organizePageDefault
	}
	if limit > organizePageMax {
		limit = organizePageMax
	}
	plan, err := a.deps.Movies.RenamePlan(r.Context(), ids)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not preview the rename")
		return
	}
	var counts organizeCounts
	listed := []movies.RenameItem{}
	for _, it := range plan {
		switch it.Status {
		case movies.RenameOK:
			counts.Rename++
		case movies.RenameSame:
			counts.Same++
		case movies.RenameConflict:
			counts.Conflict++
		case movies.RenameMissing:
			counts.Missing++
		}
		if it.Status == movies.RenameOK || it.Status == movies.RenameConflict {
			listed = append(listed, it)
		}
	}
	total := len(listed)
	page := listed[min(offset, total):min(offset+limit, total)]
	a.writeJSON(w, http.StatusOK, map[string]any{
		"counts": counts, "items": page, "total": total, "offset": offset, "token": movies.PlanToken(plan),
	})
}

// handleOrganizeApply starts the renames a preview showed, as a background job (202 with
// its id). The plan is computed again first: if it no longer matches the preview's token
// — the scheme or the library changed — it answers 409 and nothing moves. Conflicts are
// never applied; each movie is checked once more as its turn comes.
func (a *api) handleOrganizeApply(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string  `json:"token"`
		IDs   []int64 `json:"ids"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if req.Token == "" {
		a.writeError(w, http.StatusBadRequest, "token is required: preview first")
		return
	}
	ids := uniqueIDs(req.IDs)
	if len(ids) > maxBulkSearch {
		a.writeError(w, http.StatusBadRequest, fmt.Sprintf("ids must list at most %d movies", maxBulkSearch))
		return
	}
	plan, err := a.deps.Movies.RenamePlan(r.Context(), ids)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not check the rename")
		return
	}
	if movies.PlanToken(plan) != req.Token {
		a.writeError(w, http.StatusConflict, "the library or the naming scheme changed since the preview — preview again")
		return
	}
	var todo []movies.RenameItem
	for _, it := range plan {
		if it.Status == movies.RenameOK {
			todo = append(todo, it)
		}
	}
	spec := jobs.Spec{Kind: "movie.organize", Target: "movies", Class: jobs.ClassLibraryScan, Timeout: 6 * time.Hour,
		Fn: func(ctx context.Context, p *jobs.Progress) (any, error) { return a.organize(ctx, p, todo) }}
	id, existing, ok := a.submitOr503(w, r, spec)
	if !ok {
		return
	}
	if existing {
		a.alreadyRunning(w, id, "an organize is already running")
		return
	}
	a.accepted(w, id, false, map[string]any{"status": "organizing", "total": len(todo)})
}

// organize applies the planned renames a movie at a time, reporting progress as it goes.
// A skipped item (changed since the preview, or now in the way of another file) is
// counted, never forced.
func (a *api) organize(ctx context.Context, p *jobs.Progress, todo []movies.RenameItem) (organizeSummary, error) {
	sum := organizeSummary{Total: len(todo)}
	asked := map[int64]int{}
	for _, it := range todo {
		asked[it.MovieID]++
	}
	done := 0
	// One pass for the whole job: the library's records are read and resolved once, not
	// again for every movie. It stops between movies when the job is cancelled.
	_, err := a.deps.Movies.ApplyRenameEach(ctx, todo, func(id int64, res []movies.RenameResult) {
		for _, r := range res {
			if r.Renamed {
				sum.Renamed++
			} else {
				sum.Skipped++
			}
		}
		done += asked[id]
		if sum.Total > 0 && done < sum.Total {
			p.Set(float64(done)/float64(sum.Total), organizeMessage(sum, done))
		}
	})
	if err != nil {
		return sum, err
	}
	p.Set(1, organizeMessage(sum, done))
	return sum, nil
}

// organizeMessage words the job's progress and its end: "Renamed 118 of 120 · 2 skipped".
func organizeMessage(s organizeSummary, done int) string {
	if done < s.Total {
		msg := fmt.Sprintf("Renaming: %d of %d done", done, s.Total)
		if s.Skipped > 0 {
			msg += fmt.Sprintf(" · %d skipped", s.Skipped)
		}
		return msg
	}
	msg := "Renamed " + countOf(s.Renamed, "file")
	if s.Skipped > 0 {
		msg += fmt.Sprintf(" · %d skipped", s.Skipped)
	}
	return msg
}
