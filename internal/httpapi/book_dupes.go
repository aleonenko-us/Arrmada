package httpapi

import (
	"errors"
	"net/http"

	"github.com/tristenlammi/arrmada/internal/books"
)

// The possible-duplicates review (Books → Library tools → Possible duplicates): groups of
// rows that may be one book, merged or dismissed by a manager. Nothing merges without a
// click, and nothing here carries listening data — not even how many places a merge
// moved.

// maxMergeDrops caps one merge request: a group is a handful of rows, never hundreds.
const maxMergeDrops = 20

// handleBookDuplicates — GET /api/v1/books/duplicates → {groups, past_merges}
func (a *api) handleBookDuplicates(w http.ResponseWriter, r *http.Request) {
	groups, err := a.deps.Books.PossibleDuplicates(r.Context())
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not work out possible duplicates")
		return
	}
	past, err := a.deps.Books.PastMerges(r.Context(), 200)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not read earlier merges")
		return
	}
	if groups == nil {
		groups = []books.DupGroup{}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"groups": groups, "past_merges": past})
}

// mergeOutcome is one merge as the review shows it: what moved and what was left on
// disk. books.MergeResult never serialises its listening counts; this names the fields
// explicitly so a later field can't slip out by accident.
type mergeOutcome struct {
	DropID         int64          `json:"drop_id"`
	DropTitle      string         `json:"drop_title"`
	MovedEbook     bool           `json:"moved_ebook"`
	MovedAudiobook bool           `json:"moved_audiobook"`
	MergedCopyID   int64          `json:"merged_copy_id,omitempty"`
	Versions       int            `json:"versions"`
	LeftFiles      []string       `json:"left_files"`
	Note           string         `json:"note,omitempty"`
	Moved          map[string]int `json:"moved"`
}

// handleMergeBookDuplicates — POST /api/v1/books/duplicates/merge {keep_id, drop_ids}.
// Only books the review lists in one group with keep_id are merged (400 otherwise); each
// merge is its own transaction and never deletes a file.
func (a *api) handleMergeBookDuplicates(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KeepID  int64   `json:"keep_id"`
		DropIDs []int64 `json:"drop_ids"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if req.KeepID <= 0 || len(req.DropIDs) == 0 || len(req.DropIDs) > maxMergeDrops {
		a.writeError(w, http.StatusBadRequest, "pick the book to keep and at least one to merge into it")
		return
	}
	var opts books.MergeOptions
	if a.deps.Quality != nil {
		opts.Editions = a.deps.Quality.BookEditions
	}
	results, err := a.deps.Books.MergeDuplicates(r.Context(), req.KeepID, req.DropIDs, opts)
	out := make([]mergeOutcome, 0, len(results))
	leftFiles := []string{}
	for _, res := range results {
		out = append(out, mergeOutcome{DropID: res.DropID, DropTitle: res.DropTitle, MovedEbook: res.MovedEbook,
			MovedAudiobook: res.MovedAudiobook, MergedCopyID: res.MergedCopyID, Versions: res.Versions,
			LeftFiles: res.LeftFiles, Note: res.Note, Moved: res.Moved})
		leftFiles = append(leftFiles, res.LeftFiles...)
	}
	if err != nil {
		status, msg := http.StatusInternalServerError, "the merge failed and was undone: "+err.Error()
		switch {
		case errors.Is(err, books.ErrNotDuplicates):
			status, msg = http.StatusBadRequest, err.Error()
		case errors.Is(err, books.ErrNotFound):
			status, msg = http.StatusNotFound, "one of those books no longer exists"
		case errors.Is(err, books.ErrMergeUnavailable):
			status, msg = http.StatusServiceUnavailable, err.Error()
		}
		if len(out) > 0 {
			// Earlier merges in the request went through; say which.
			a.writeJSON(w, status, map[string]any{"error": msg, "results": out, "left_files": leftFiles})
			return
		}
		a.writeError(w, status, msg)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"keep_id": req.KeepID, "results": out, "left_files": leftFiles})
}

// handleIgnoreBookDuplicates — POST /api/v1/books/duplicates/ignore {ids}: every pair
// among ids is different books, for good.
func (a *api) handleIgnoreBookDuplicates(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if len(req.IDs) < 2 || len(req.IDs) > maxMergeDrops+1 {
		a.writeError(w, http.StatusBadRequest, "pick the books that aren't the same")
		return
	}
	if err := a.deps.Books.IgnoreDuplicates(r.Context(), req.IDs); err != nil {
		switch {
		case errors.Is(err, books.ErrNotFound):
			a.writeError(w, http.StatusNotFound, "one of those books no longer exists")
		case errors.Is(err, books.ErrPickTwo):
			a.writeError(w, http.StatusBadRequest, err.Error())
		default:
			a.writeError(w, http.StatusInternalServerError, "could not save that")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
