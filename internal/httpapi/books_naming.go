package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/jobs"
	"github.com/tristenlammi/arrmada/internal/library"
)

// Book naming (issue #25): audiobook and ebook folder/file templates in Settings → Naming
// & metadata, a live preview of them on sample books, and the movie organize's
// preview-then-apply for the books already in the library. The rename itself is
// automation.BookRenamePlan / ApplyBookRename; this is only its HTTP face. Nothing here
// names a listener: the plan is about files, and listening places are keyed by book.

// Settings keys for the book templates.
const (
	keyNamingAudiobookFolder = "naming_audiobook_folder"
	keyNamingAudiobookFile   = "naming_audiobook_file"
	keyNamingAudiobookTrack  = "naming_audiobook_track"
	keyNamingEbookFolder     = "naming_ebook_folder"
	keyNamingEbookFile       = "naming_ebook_file"
)

// bookNamingFields are the template settings: key, default, and the check a value must
// pass before it's saved.
var bookNamingFields = []struct {
	key, def string
	check    func(string) error
}{
	{keyNamingAudiobookFolder, library.DefaultAudiobookFolder, library.ValidateBookFolder},
	{keyNamingAudiobookFile, library.DefaultAudiobookFile, library.ValidateBookFile},
	{keyNamingAudiobookTrack, library.DefaultAudiobookTrack, library.ValidateBookTrack},
	{keyNamingEbookFolder, library.DefaultEbookFolder, library.ValidateBookFolder},
	{keyNamingEbookFile, library.DefaultEbookFile, library.ValidateBookFile},
}

// bookNamingSettings is the saved templates (defaults filled), for GET /settings.
func (a *api) bookNamingSettings(ctx context.Context) map[string]string {
	out := map[string]string{}
	for _, f := range bookNamingFields {
		out[f.key] = a.deps.Settings.Get(ctx, f.key, f.def)
	}
	return out
}

// bookNamingUpdate pairs each sent template with its key, in bookNamingFields order.
func bookNamingUpdate(req *settingsUpdate) map[string]*string {
	return map[string]*string{
		keyNamingAudiobookFolder: req.NamingAudiobookFolder,
		keyNamingAudiobookFile:   req.NamingAudiobookFile,
		keyNamingAudiobookTrack:  req.NamingAudiobookTrack,
		keyNamingEbookFolder:     req.NamingEbookFolder,
		keyNamingEbookFile:       req.NamingEbookFile,
	}
}

// checkBookNaming validates every sent book template, returning the first problem in
// words the settings page shows under the field.
func checkBookNaming(req *settingsUpdate) string {
	sent := bookNamingUpdate(req)
	for _, f := range bookNamingFields {
		if p := sent[f.key]; p != nil {
			if err := f.check(*p); err != nil {
				return err.Error()
			}
		}
	}
	return ""
}

// handleBookNamingPreview renders the sample books with the templates in the query
// (kind=audiobook|ebook, folder, file, track) — what the settings page shows as you type,
// before anything is saved. A template with a problem answers 200 with the problem, so
// the page can mark the field without treating it as a failed request.
func (a *api) handleBookNamingPreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	audio := q.Get("kind") != "ebook"
	nm := library.BookNaming{Folder: q.Get("folder"), File: q.Get("file"), Track: q.Get("track")}
	samples, perr := library.PreviewBookNaming(nm, audio)
	if perr != nil {
		a.writeJSON(w, http.StatusOK, map[string]any{"samples": []library.BookNamingSample{}, "error": perr})
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"samples": samples})
}

// handleBookNamingPresets lists the ready-made schemes.
func (a *api) handleBookNamingPresets(w http.ResponseWriter, _ *http.Request) {
	a.writeJSON(w, http.StatusOK, map[string]any{"presets": library.BookNamingPresets, "tokens": library.BookTokens})
}

// writeBookBusy answers 409 when another change to the book's files was running (an
// import, a rename, a rescan, a delete or a merge), so the page says to try again rather
// than that something broke. It reports whether it answered.
func (a *api) writeBookBusy(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, automation.ErrBookBusy) {
		return false
	}
	a.writeError(w, http.StatusConflict, "This book's files are busy (an import, rename, rescan or merge is running) — try again in a moment.")
	return true
}

// bookOrganizeCounts is what a book rename preview found, by edition.
type bookOrganizeCounts struct {
	Rename   int `json:"rename"`
	Same     int `json:"same"`
	Conflict int `json:"conflict"`
	Missing  int `json:"missing"`
}

// bookOrganizeSummary is the book organize job's result.
type bookOrganizeSummary struct {
	Total   int `json:"total"`
	Renamed int `json:"renamed"`
	Skipped int `json:"skipped"`
}

// organizeScope is the books a rename preview or apply covers: every book, or one
// (?book_id= on the preview, "book_id" in the apply's body) — the book page's "Preview
// rename" after an edit to its details. ok is false for a book id that isn't a number.
func organizeScope(raw string) (ids []int64, ok bool) {
	if raw == "" || raw == "0" {
		return nil, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return nil, false
	}
	return []int64{id}, true
}

// handleBookOrganizePreview is the dry run over every book (or one, ?book_id=): counts,
// then the editions that would move and the conflicts, a page at a time, and a token
// naming exactly this plan. Nothing moves.
func (a *api) handleBookOrganizePreview(w http.ResponseWriter, r *http.Request) {
	ids, ok := organizeScope(r.URL.Query().Get("book_id"))
	if !ok {
		a.writeError(w, http.StatusBadRequest, "book_id must be a book's id")
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
	plan, err := a.deps.Automation.BookRenamePlan(r.Context(), ids)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not preview the rename")
		return
	}
	var counts bookOrganizeCounts
	listed := []automation.BookRenameItem{}
	for _, it := range plan {
		switch it.Status {
		case automation.BookRenameOK:
			counts.Rename++
		case automation.BookRenameSame:
			counts.Same++
		case automation.BookRenameConflict:
			counts.Conflict++
		case automation.BookRenameMissing:
			counts.Missing++
		}
		if it.Status == automation.BookRenameOK || it.Status == automation.BookRenameConflict {
			listed = append(listed, it)
		}
	}
	total := len(listed)
	page := listed[min(offset, total):min(offset+limit, total)]
	a.writeJSON(w, http.StatusOK, map[string]any{
		"counts": counts, "items": page, "total": total, "offset": offset, "token": automation.BookPlanToken(plan),
	})
}

// handleBookOrganizeApply starts the renames a preview showed, as a background job (202
// with its id). The plan is worked out again first: if it no longer matches the preview's
// token — the scheme or the library changed — it answers 409 and nothing moves.
func (a *api) handleBookOrganizeApply(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token  string `json:"token"`
		BookID int64  `json:"book_id"` // 0 = every book, as previewed
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if req.Token == "" {
		a.writeError(w, http.StatusBadRequest, "token is required: preview first")
		return
	}
	ids, ok := organizeScope(strconv.FormatInt(req.BookID, 10))
	if !ok {
		a.writeError(w, http.StatusBadRequest, "book_id must be a book's id")
		return
	}
	plan, err := a.deps.Automation.BookRenamePlan(r.Context(), ids)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not check the rename")
		return
	}
	if automation.BookPlanToken(plan) != req.Token {
		a.writeError(w, http.StatusConflict, "the library or the naming scheme changed since the preview — preview again")
		return
	}
	var todo []automation.BookRenameItem
	for _, it := range plan {
		if it.Status == automation.BookRenameOK {
			todo = append(todo, it)
		}
	}
	spec := jobs.Spec{Kind: "book.organize", Target: "books", Class: jobs.ClassLibraryScan, Timeout: 6 * time.Hour,
		Fn: func(ctx context.Context, p *jobs.Progress) (any, error) { return a.organizeBooks(ctx, p, todo) }}
	id, existing, ok := a.submitOr503(w, r, spec)
	if !ok {
		return
	}
	if existing {
		a.alreadyRunning(w, id, "a book rename is already running")
		return
	}
	a.accepted(w, id, false, map[string]any{"status": "organizing", "total": len(todo)})
}

// organizeBooks applies the planned renames an edition at a time, reporting progress. A
// skipped edition (changed since the preview, or now in the way of another file) is
// counted, never forced.
func (a *api) organizeBooks(ctx context.Context, p *jobs.Progress, todo []automation.BookRenameItem) (bookOrganizeSummary, error) {
	sum := bookOrganizeSummary{Total: len(todo)}
	_, err := a.deps.Automation.ApplyBookRename(ctx, todo, func(done int, res automation.BookRenameResult) {
		if res.Renamed {
			sum.Renamed++
		} else {
			sum.Skipped++
		}
		if sum.Total > 0 {
			p.Set(float64(done)/float64(sum.Total), bookOrganizeMessage(sum, done))
		}
	})
	if err != nil {
		return sum, err
	}
	p.Set(1, bookOrganizeMessage(sum, sum.Total))
	return sum, nil
}

// bookOrganizeMessage words the job's progress and its end: "Renamed 118 books · 2 skipped".
func bookOrganizeMessage(s bookOrganizeSummary, done int) string {
	if done < s.Total {
		msg := fmt.Sprintf("Renaming: %d of %d done", done, s.Total)
		if s.Skipped > 0 {
			msg += fmt.Sprintf(" · %d skipped", s.Skipped)
		}
		return msg
	}
	msg := "Renamed " + countOf(s.Renamed, "edition")
	if s.Skipped > 0 {
		msg += fmt.Sprintf(" · %d skipped", s.Skipped)
	}
	return msg
}
