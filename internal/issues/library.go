package issues

import (
	"context"
	"errors"
	"sort"

	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/series"
)

// Item is a library item as an issue sees it: what it's called and the file it has now.
type Item struct {
	LibraryID int64
	TMDBID    int
	Title     string
	Year      int
	PosterURL string
	Path      string // "" = no file
	Size      int64  // bytes, 0 = unknown
	// Release is the release name the file was imported from ("" = not recorded), which
	// a replace blocklists.
	Release string
}

// SeasonFiles is one season of a show and the episodes in it that have a file, for the
// report sheet's season and episode pickers.
type SeasonFiles struct {
	Season   int           `json:"season"`
	Episodes []EpisodeFile `json:"episodes"`
}

// EpisodeFile is one episode with a file.
type EpisodeFile struct {
	Episode int    `json:"episode"`
	Title   string `json:"title,omitempty"`
}

// Library reads the library items issues are about.
type Library interface {
	// Find is the library item for a TMDB title (and, for a show, one episode):
	// ErrNotInLibrary when it isn't there.
	Find(ctx context.Context, media string, tmdbID, season, episode int) (Item, error)
	// Current is the item's file now (Path "" when it has none); ErrNotInLibrary when the
	// item itself is gone.
	Current(ctx context.Context, t Target) (Item, error)
	// EpisodesWithFiles is a show's title and its episodes that have a file, by season.
	EpisodesWithFiles(ctx context.Context, tmdbID int) (string, []SeasonFiles, error)
}

// MediaLibrary is the Library over the Movies and Series services. Reads are database
// only: nothing here stats or probes a file.
func MediaLibrary(mv *movies.Service, sr *series.Service) Library {
	return mediaLibrary{mv: mv, sr: sr}
}

type mediaLibrary struct {
	mv *movies.Service
	sr *series.Service
}

func (l mediaLibrary) Find(ctx context.Context, media string, tmdbID, season, episode int) (Item, error) {
	switch media {
	case MediaMovie:
		if l.mv == nil {
			return Item{}, ErrNotInLibrary
		}
		m, err := l.mv.GetByTMDB(ctx, tmdbID)
		if errors.Is(err, movies.ErrNotFound) {
			return Item{}, ErrNotInLibrary
		}
		if err != nil {
			return Item{}, err
		}
		return l.Current(ctx, Target{MediaType: MediaMovie, LibraryID: m.ID})
	case MediaSeries:
		if l.sr == nil {
			return Item{}, ErrNotInLibrary
		}
		s, err := l.sr.GetByTMDB(ctx, tmdbID)
		if errors.Is(err, series.ErrNotFound) {
			return Item{}, ErrNotInLibrary
		}
		if err != nil {
			return Item{}, err
		}
		return l.Current(ctx, Target{MediaType: MediaSeries, LibraryID: s.ID, Season: season, Episode: episode})
	}
	return Item{}, ErrNotInLibrary
}

func (l mediaLibrary) Current(ctx context.Context, t Target) (Item, error) {
	switch t.MediaType {
	case MediaMovie:
		if l.mv == nil {
			return Item{}, ErrNotInLibrary
		}
		vs, err := l.mv.VersionRows(ctx, t.LibraryID)
		if errors.Is(err, movies.ErrNotFound) {
			return Item{}, ErrNotInLibrary
		}
		if err != nil {
			return Item{}, err
		}
		m, err := l.mv.Get(ctx, t.LibraryID)
		if err != nil {
			return Item{}, err
		}
		it := Item{LibraryID: m.ID, TMDBID: m.TMDBID, Title: m.Title, Year: m.Year, PosterURL: m.PosterURL}
		// The default track: what Plex plays and what a request asked for.
		if def := vs[0]; def.HasFile && def.FilePath != "" {
			it.Path, it.Size, it.Release = def.FilePath, def.SizeBytes, def.SourceRelease
		}
		return it, nil
	case MediaSeries:
		if l.sr == nil {
			return Item{}, ErrNotInLibrary
		}
		s, err := l.sr.Get(ctx, t.LibraryID)
		if errors.Is(err, series.ErrNotFound) {
			return Item{}, ErrNotInLibrary
		}
		if err != nil {
			return Item{}, err
		}
		it := Item{LibraryID: s.ID, TMDBID: s.TMDBID, Title: s.Title, Year: s.Year, PosterURL: s.PosterURL}
		found := false
		for _, sn := range s.Seasons {
			if sn.SeasonNumber != t.Season {
				continue
			}
			for _, e := range sn.Episodes {
				if e.EpisodeNumber == t.Episode {
					found = true
					if e.HasFile && e.FilePath != "" {
						it.Path, it.Size, it.Release = e.FilePath, e.SizeBytes, e.SourceRelease
					}
				}
			}
		}
		if !found {
			return Item{}, ErrNotInLibrary // no such episode in the show
		}
		return it, nil
	}
	return Item{}, ErrNotInLibrary
}

func (l mediaLibrary) EpisodesWithFiles(ctx context.Context, tmdbID int) (string, []SeasonFiles, error) {
	if l.sr == nil {
		return "", nil, ErrNotInLibrary
	}
	s, err := l.sr.GetByTMDB(ctx, tmdbID)
	if errors.Is(err, series.ErrNotFound) {
		return "", nil, ErrNotInLibrary
	}
	if err != nil {
		return "", nil, err
	}
	full, err := l.sr.Get(ctx, s.ID)
	if err != nil {
		return "", nil, err
	}
	var out []SeasonFiles
	for _, sn := range full.Seasons {
		sf := SeasonFiles{Season: sn.SeasonNumber}
		for _, e := range sn.Episodes {
			if e.HasFile && e.FilePath != "" {
				sf.Episodes = append(sf.Episodes, EpisodeFile{Episode: e.EpisodeNumber, Title: e.Title})
			}
		}
		if len(sf.Episodes) > 0 {
			sort.Slice(sf.Episodes, func(a, b int) bool { return sf.Episodes[a].Episode < sf.Episodes[b].Episode })
			out = append(out, sf)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Season < out[b].Season })
	return full.Title, out, nil
}
