package movies

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tristenlammi/arrmada/internal/libfolders"
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
	// SearchPaced marks a film added without a file to be searched in its turn (a bulk add:
	// see automation's paced sweeps). Only AddImported reads it, and sets the mark before the
	// film is monitored.
	SearchPaced bool
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
	s.withLocalTitles(ctx, m.Extra, nil, details.TMDBID, details.Title)
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

// ErrOutsideLibrary is a file to adopt that isn't inside any of the Movies folders: a film
// is only ever given a file Arrmada manages (renames, the recycle bin, moves).
var ErrOutsideLibrary = errors.New("the file isn't inside one of the Movies folders")

// InLibrary reports whether path lies inside one of the Movies folders.
func (s *Service) InLibrary(ctx context.Context, path string) bool {
	if s.folders != nil {
		_, ok := s.folders.Owner(ctx, libfolders.MediaMovie, path)
		return ok
	}
	root := s.libRoot()
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// AddImported adds a film brought over from another app (a Radarr import, named by source)
// as o says and, when file isn't "", with the file it already has: a path inside one of the
// Movies folders, recorded where it is — nothing is moved, renamed or downloaded. It's the
// library scan's quiet adoption: no outbox row and no alert per film, so bringing over a
// whole library doesn't queue Convert and Subtitles work or notices for every file. The
// film belongs to the folder its file is in. ErrExists when it's already in the library.
//
// The film is created unmonitored and only monitored once it's complete — its file
// recorded, or (o.SearchPaced) its paced-search mark set — so a sweep running meanwhile
// never sees a film that wants a file it already has. Everything after the film exists runs
// to the end even when ctx is cancelled (the import stopped or timed out): the film is left
// complete, or unmonitored, never wanted by mistake. When a step fails the film stays
// unmonitored and the error says so.
func (s *Service) AddImported(ctx context.Context, tmdbID int, o AddOptions, file, source string) (Movie, error) {
	if file != "" {
		if !s.InLibrary(ctx, file) {
			return Movie{}, ErrOutsideLibrary
		}
		id := s.ownerID(ctx, file)
		o.FolderID = &id
	}
	monitor := o.Monitored
	o.Monitored = false
	m, err := s.AddWith(ctx, tmdbID, o)
	if err != nil {
		return m, err
	}
	wctx := context.WithoutCancel(ctx)
	if file != "" {
		if err := s.setDefaultFile(wctx, m.ID, file); err != nil {
			return m, fmt.Errorf("added, but its file couldn't be recorded (left unmonitored): %w", err)
		}
		_ = s.repo.AddEvent(wctx, m.ID, "imported", "Brought over from "+source+": "+filepath.Base(file))
		// For the UI only, like the library scan's adoption.
		s.publish("movie.downloaded", map[string]any{"id": m.ID, "version_id": int64(0), "path": file, "source": "scan"})
		m.HasFile, m.MovieFilePath = true, file
	} else if monitor && o.SearchPaced {
		if err := s.repo.SetSearchPaced(wctx, m.ID, true); err != nil {
			return m, fmt.Errorf("added, but couldn't be marked to be searched in its turn (left unmonitored): %w", err)
		}
	}
	if monitor {
		if err := s.repo.SetMonitored(wctx, m.ID, true); err != nil {
			return m, fmt.Errorf("added, but couldn't be monitored (left unmonitored): %w", err)
		}
		m.Monitored = true
	}
	return m, nil
}

// AttachImported gives a film already in the library, without a file, the file another app
// (source) had for it — the scan's attach: recorded where it is, and announced like an
// import because the film was wanted. Refused when the film has a file by now, or the file
// isn't inside a Movies folder.
func (s *Service) AttachImported(ctx context.Context, id int64, file, source string) error {
	if !s.InLibrary(ctx, file) {
		return ErrOutsideLibrary
	}
	m, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	return s.attachExisting(ctx, m, file, "Brought over from "+source+": ")
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
