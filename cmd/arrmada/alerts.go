package main

import (
	"context"

	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/notify"
	"github.com/tristenlammi/arrmada/internal/series"
)

// alertPosters finds the poster a library alert carries: the image the library already
// stores for the movie, show or book (TMDB or Open Library, public). notify only hands
// Apprise URLs from its allowed image hosts, so anything else is simply left off.
func alertPosters(mv *movies.Service, sr *series.Service, bk *books.Service) notify.PosterFunc {
	return func(ctx context.Context, kind string, id int64) string {
		switch kind {
		case "movie":
			if m, err := mv.Get(ctx, id); err == nil {
				return m.PosterURL
			}
		case "series":
			if s, err := sr.Get(ctx, id); err == nil {
				return s.PosterURL
			}
		case "book":
			if b, err := bk.Get(ctx, id); err == nil {
				return b.CoverURL
			}
		}
		return ""
	}
}
