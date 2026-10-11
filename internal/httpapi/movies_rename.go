package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/tristenlammi/arrmada/internal/movies"
)

// handleRenamePreview lists every track of a movie with its current file and the name the
// naming scheme gives it (ok, same, conflict or missing). Nothing moves.
func (a *api) handleRenamePreview(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	if _, err := a.deps.Movies.Get(r.Context(), id); err != nil {
		if errors.Is(err, movies.ErrNotFound) {
			a.writeError(w, http.StatusNotFound, "movie not found")
			return
		}
		a.writeError(w, http.StatusInternalServerError, "could not preview rename")
		return
	}
	items, err := a.deps.Movies.RenamePlan(r.Context(), []int64{id})
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not preview rename")
		return
	}
	if items == nil {
		items = []movies.RenameItem{}
	}
	matches := true
	for _, it := range items {
		if it.Status != movies.RenameSame {
			matches = false
		}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"items": items, "matches": matches})
}

// handleRename applies the previewed renames the person ticked ({items} as the preview
// returned them). Each is checked against a fresh plan: one whose file or new name changed
// since, or that would now overwrite something, is skipped and reported, never forced.
func (a *api) handleRename(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Items []movies.RenameItem `json:"items"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if _, err := a.deps.Movies.Get(r.Context(), id); err != nil {
		if errors.Is(err, movies.ErrNotFound) {
			a.writeError(w, http.StatusNotFound, "movie not found")
			return
		}
		a.writeError(w, http.StatusInternalServerError, "could not rename")
		return
	}
	var items []movies.RenameItem
	for _, it := range req.Items {
		if it.MovieID == id { // this page's movie only
			items = append(items, it)
		}
	}
	// Detached: files are moving, and a browser giving up part-way mustn't leave a file
	// moved with its record unwritten.
	results, err := a.deps.Movies.ApplyRename(context.WithoutCancel(r.Context()), items)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not rename: "+err.Error())
		return
	}
	a.writeJSON(w, http.StatusOK, renameReply(results))
}

// renameReply counts what a rename did, with every item's outcome.
func renameReply(results []movies.RenameResult) map[string]any {
	if results == nil {
		results = []movies.RenameResult{}
	}
	renamed, skipped := 0, 0
	for _, res := range results {
		if res.Renamed {
			renamed++
		} else {
			skipped++
		}
	}
	return map[string]any{"renamed": renamed, "skipped": skipped, "results": results}
}
