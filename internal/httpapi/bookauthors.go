package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/books"
)

// Book authors as entities: /api/v1/book-authors.
//
// They deliberately don't live under /api/v1/books/authors/{id}: ServeMux panics at
// startup on patterns that overlap with neither more specific, and that one would
// overlap GET /api/v1/books/{id}/series, /history, /covers and the rest.
//
//	GET /api/v1/book-authors          → {authors: [Author]} — every author with books (or
//	                                    followed), with books/owned/wanted counts
//	GET /api/v1/book-authors?name=X   → {authors: [Author]} — the authors the name X can
//	                                    mean: one is that author, several need a person
//	                                    to choose (old /books/author/<name> links)
//	GET /api/v1/book-authors/{id}     → Author, with counts and the spellings on its books
//	PUT /api/v1/book-authors/{id}     ← {monitored?, monitor_new?, quality_profile?,
//	                                     catalogue_key?} → Author. Fields left out are
//	                                    kept. Setting a catalogue_key another author row
//	                                    already has makes the two rows one: the answer is
//	                                    the row that stays (its id may differ from {id}).
//
// Manager only, like the rest of the Books admin API.

func (a *api) handleListBookAuthors(w http.ResponseWriter, r *http.Request) {
	var (
		list []books.Author
		err  error
	)
	if name := strings.TrimSpace(r.URL.Query().Get("name")); name != "" {
		list, err = a.deps.Books.AuthorsNamed(r.Context(), name)
	} else {
		list, err = a.deps.Books.ListAuthors(r.Context())
	}
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not list authors")
		return
	}
	if list == nil {
		list = []books.Author{}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"authors": list})
}

func (a *api) handleGetBookAuthor(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	a.writeBookAuthor(r.Context(), w, id)
}

func (a *api) writeBookAuthor(ctx context.Context, w http.ResponseWriter, id int64) {
	au, err := a.deps.Books.GetAuthor(ctx, id)
	if errors.Is(err, books.ErrAuthorNotFound) {
		a.writeError(w, http.StatusNotFound, "author not found")
		return
	}
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not load the author")
		return
	}
	a.writeJSON(w, http.StatusOK, au)
}

func (a *api) handleUpdateBookAuthor(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Monitored      *bool   `json:"monitored"`
		MonitorNew     *bool   `json:"monitor_new"`
		QualityProfile *string `json:"quality_profile"`
		CatalogueKey   *string `json:"catalogue_key"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	cur, err := a.deps.Books.GetAuthor(r.Context(), id)
	if errors.Is(err, books.ErrAuthorNotFound) {
		a.writeError(w, http.StatusNotFound, "author not found")
		return
	}
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not load the author")
		return
	}
	if req.CatalogueKey != nil && strings.TrimSpace(*req.CatalogueKey) != cur.CatalogueKey {
		// The catalogue's photo and biography are fetched after the write; give that a
		// bound so a slow catalogue can't hold the request open.
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		id, err = a.deps.Books.SetCatalogueKey(ctx, id, *req.CatalogueKey)
		cancel()
		if errors.Is(err, books.ErrBadAuthorKey) {
			a.writeError(w, http.StatusBadRequest, "that isn't a catalogue author key")
			return
		}
		if err != nil {
			a.writeError(w, http.StatusInternalServerError, "could not save the author")
			return
		}
		if cur, err = a.deps.Books.GetAuthor(r.Context(), id); err != nil {
			a.writeError(w, http.StatusInternalServerError, "could not load the author")
			return
		}
	}
	if req.Monitored != nil || req.MonitorNew != nil || req.QualityProfile != nil {
		mon, monNew, profile := cur.Monitored, cur.MonitorNew, cur.QualityProfile
		if req.Monitored != nil {
			mon = *req.Monitored
		}
		if req.MonitorNew != nil {
			monNew = *req.MonitorNew
		}
		if req.QualityProfile != nil {
			profile = *req.QualityProfile
		}
		if err := a.deps.Books.SetMonitoring(r.Context(), id, mon, monNew, profile); err != nil {
			a.writeError(w, http.StatusInternalServerError, "could not save the author")
			return
		}
	}
	a.writeBookAuthor(r.Context(), w, id)
}
