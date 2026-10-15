package movies

import (
	"context"
	"fmt"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

// AddOptions says how a film joins the library.
type AddOptions struct {
	QualityProfile string
	Monitored      bool
	// MinAvailability is when it becomes eligible for searching: announced, inCinemas or
	// released ("" = released, the default).
	MinAvailability string
	// FolderID is the Movies folder chosen for it (0 = the default); nil lets the folder
	// rules pick, the default when none matches.
	FolderID *int64
}

// AddWith pulls full metadata for a TMDB id and adds the movie to the library as o says.
// ErrExists when it's already there.
func (s *Service) AddWith(ctx context.Context, tmdbID int, o AddOptions) (Movie, error) {
	if o.MinAvailability != "" && !ValidAvailability(o.MinAvailability) {
		return Movie{}, fmt.Errorf("invalid availability %q", o.MinAvailability)
	}
	details, err := s.meta.GetMovie(ctx, tmdbID)
	if err != nil {
		return Movie{}, fmt.Errorf("fetch metadata: %w", err)
	}
	m := Movie{
		TMDBID:          details.TMDBID,
		IMDBID:          details.IMDBID,
		Title:           details.Title,
		Year:            details.Year,
		Overview:        details.Overview,
		PosterURL:       details.PosterURL,
		Runtime:         details.Runtime,
		Status:          details.Status,
		Monitored:       o.Monitored,
		QualityProfile:  o.QualityProfile,
		MinAvailability: o.MinAvailability,
		Extra:           extraFrom(details),
	}
	if m.LibraryFolderID, err = s.pickFolder(ctx, o.FolderID, m.Extra); err != nil {
		return Movie{}, err
	}
	created, err := s.repo.Create(ctx, m)
	if err != nil {
		return Movie{}, err
	}
	s.log.Info("movie added", "title", created.Title, "year", created.Year)
	_ = s.repo.AddEvent(ctx, created.ID, "added", "Added to library")
	return created, nil
}

// LibraryIDs maps every TMDB id in the library to its movie id, in one query: search
// results and collections mark what's already there and link to it.
func (s *Service) LibraryIDs(ctx context.Context) (map[int]int64, error) {
	return s.repo.ExistingTMDBIDs(ctx)
}

// LookupResult is a metadata search hit, marked when the film is already in the library.
type LookupResult struct {
	metadata.MovieResult
	InLibrary bool  `json:"in_library"`
	LibraryID int64 `json:"library_id,omitempty"`
}

// LookupInLibrary is Lookup with each hit marked in_library (and its library id), so the
// Add dialog links to the film instead of offering an add that can only fail.
func (s *Service) LookupInLibrary(ctx context.Context, query string) ([]LookupResult, error) {
	results, err := s.Lookup(ctx, query)
	if err != nil {
		return nil, err
	}
	have, err := s.LibraryIDs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]LookupResult, 0, len(results))
	for _, r := range results {
		id, ok := have[r.TMDBID]
		out = append(out, LookupResult{MovieResult: r, InLibrary: ok, LibraryID: id})
	}
	return out, nil
}
