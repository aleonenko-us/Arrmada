package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/books"
)

// handleEditBookDetails saves hand edits to a book's details (staff, the book page's Edit
// details dialog): fields set to a value typed by hand, fields reset to the catalogue's,
// or everything reset. The catalogue's values stay underneath each edit (books.Edits).
//
// The edit shows everywhere at once — the page, search, the audiobook server and OPDS
// read the book's shown details — but files are never renamed by it. When the edit
// changes where the book naming would put an edition's files, rename_available says so,
// and the page offers the rename preview.
func (a *api) handleEditBookDetails(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	var req books.EditRequest
	if !a.decodeJSON(w, r, &req) {
		return
	}
	before := a.bookRenameTargets(r.Context(), id)
	res, err := a.deps.Books.EditDetails(r.Context(), id, req)
	switch {
	case errors.Is(err, books.ErrNotFound):
		a.writeError(w, http.StatusNotFound, "book not found")
		return
	case errors.Is(err, books.ErrEditInvalid):
		a.writeError(w, http.StatusBadRequest, editMessage(err))
		return
	case err != nil:
		a.writeError(w, http.StatusInternalServerError, "could not save the book's details")
		return
	}
	a.bookDetailsChanged()
	rename := false
	if len(res.Changed) > 0 {
		rename = renameNewlyOffered(before, a.bookRenameTargets(r.Context(), id))
	}
	b := res.Book
	a.enrichBookWants(r, &b)
	a.setBookAliases(r.Context(), &b)
	setEbookKindle(&b)
	changed := res.Changed
	if changed == nil {
		changed = []string{}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"book": b, "changed": changed, "rename_available": rename})
}

// editMessage is an edit refusal in the page's words, without the sentinel's prefix.
func editMessage(err error) string {
	msg := strings.TrimPrefix(err.Error(), books.ErrEditInvalid.Error()+": ")
	r, size := utf8.DecodeRuneInString(msg)
	if size == 0 {
		return "That edit can't be saved."
	}
	return string(unicode.ToUpper(r)) + msg[size:]
}

// bookDetailsChanged makes a change to a book's shown details visible at once where
// they're cached: the listening apps' catalogue (two minutes otherwise; the item's
// updatedAt already moved, via the books_meta_updated trigger) and the ⌘K index.
func (a *api) bookDetailsChanged() {
	if a.deps.AudioServer != nil {
		a.deps.AudioServer.Invalidate()
	}
	a.search.mu.Lock()
	a.search.built = time.Time{}
	a.search.mu.Unlock()
}

// bookRenameTargets is where the book naming would put each of a book's editions now
// (edition key → proposed path), for telling whether an edit moved any of them. Nil when
// the plan can't be made (no library yet); then no rename is offered.
func (a *api) bookRenameTargets(ctx context.Context, id int64) map[string]automation.BookRenameItem {
	if a.deps.Automation == nil {
		return nil
	}
	plan, err := a.deps.Automation.BookRenamePlan(ctx, []int64{id})
	if err != nil {
		return nil
	}
	out := make(map[string]automation.BookRenameItem, len(plan))
	for _, it := range plan {
		out[automation.BookEditionKey(it)] = it
	}
	return out
}

// renameNewlyOffered reports whether an edit gave any edition a new place to be renamed
// to: one that would move now, to somewhere other than where it would have gone before.
// Files that were already off-scheme before the edit, and stay so the same way, don't
// count — the edit didn't cause that.
func renameNewlyOffered(before, after map[string]automation.BookRenameItem) bool {
	if before == nil || after == nil {
		return false
	}
	for k, it := range after {
		if it.Status != automation.BookRenameOK {
			continue
		}
		if was, ok := before[k]; !ok || was.Proposed != it.Proposed {
			return true
		}
	}
	return false
}
