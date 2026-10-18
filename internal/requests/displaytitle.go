package requests

import (
	"context"

	"github.com/tristenlammi/arrmada/internal/books"
)

// A request keeps the title it was made under (TMDB's English title): it names the request
// in the API and in a staff search. What people READ — the request list, and the notices
// once the title is in the library — follows the library's shown title instead (Settings →
// Library → "Show titles as", or the title's own override), so a Greek household's
// request for "The Travelling Players" arrives as "Ο Θίασος" like the rest of the library.

// withShownTitle is req with its title as the library shows it, for a notice's text. A
// title not in the library (yet), or one shown under its own name, keeps req.Title.
func (s *Service) withShownTitle(ctx context.Context, req Request) Request {
	if shown := s.shownTitle(ctx, req); shown != "" {
		req.Title = shown
	}
	return req
}

// shownTitle is the library's shown title for the movie or show req asked for, or "". A
// book's is its title edited by hand on the book page, when it has one.
func (s *Service) shownTitle(ctx context.Context, req Request) string {
	if req.MediaType == "book" {
		if req.BookID > 0 && s.books != nil {
			if b, err := s.books.Get(ctx, req.BookID); err == nil && b.Edited(books.FieldTitle) {
				return b.Title
			}
		}
		return ""
	}
	if req.TMDBID <= 0 {
		return ""
	}
	switch req.MediaType {
	case "movie":
		if s.movies != nil {
			if m, err := s.movies.GetByTMDB(ctx, req.TMDBID); err == nil {
				return m.Shown()
			}
		}
	case "series":
		if s.series != nil {
			if sr, err := s.series.GetByTMDB(ctx, req.TMDBID); err == nil {
				return sr.Shown()
			}
		}
	}
	return ""
}
