package convert

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/movies"
)

// A movie can hold several files: the default track (the movie row) and extra version
// tracks (movie_versions), each with its own path, profile and history. Convert indexes
// and converts every one of them, and treats each as its own item:
//
//   - 'movie:ID' is the default track, as it always was, so requests, skips and failures
//     recorded before versions were indexed keep meaning the same file;
//   - 'movie:ID:v:VID' is extra track VID.
//
// Whatever a conversion of one track does — the swap, the record repoint, the baseline,
// the reindex — reaches that track's file and record only. The record updates are by path
// (movies.Service.RepointMovieFile / SetConvertedFrom), so they follow the track whose
// file was converted and no other.

// movieVersionKey is the item key of a movie track: versionID 0 is the default.
func movieVersionKey(movieID, versionID int64) string {
	if versionID > 0 {
		return fmt.Sprintf("movie:%d:v:%d", movieID, versionID)
	}
	return movieKey(movieID)
}

// trackTitle is the display title of a movie track: the film's title, plus the track's
// label for an extra version ("Heat · 4K Remux"), so a job, a Problems entry or a ledger
// row says which of the film's files it was.
func trackTitle(title, label string, versionID int64) string {
	if versionID <= 0 {
		return title
	}
	if label = strings.TrimSpace(label); label == "" {
		label = fmt.Sprintf("version %d", versionID)
	}
	return title + " · " + label
}

// movieTrack is one movie track's current record, read from the database alone.
type movieTrack struct {
	movie   movies.Movie
	version movies.Version // the track: IsDefault for the movie row
	others  []string       // the files the movie's other tracks hold
}

// title is the track's display title.
func (t movieTrack) title() string {
	return trackTitle(t.movie.Title, t.version.Label, t.version.ID)
}

// lookupMovieTrack reads a movie's track versionID (0 = default). ok is false when the
// movie or the track is gone or the track records no file.
func (s *Service) lookupMovieTrack(ctx context.Context, movieID, versionID int64) (movieTrack, bool) {
	if s.movies == nil {
		return movieTrack{}, false
	}
	m, err := s.movies.Get(ctx, movieID)
	if err != nil {
		return movieTrack{}, false
	}
	rows, err := s.movies.VersionRows(ctx, movieID)
	if err != nil {
		return movieTrack{}, false
	}
	t := movieTrack{movie: m}
	found := false
	for _, v := range rows {
		if v.ID == versionID && v.IsDefault == (versionID == 0) {
			t.version, found = v, true
			continue
		}
		if v.HasFile && v.FilePath != "" {
			t.others = append(t.others, v.FilePath)
		}
	}
	if !found || !t.version.HasFile || t.version.FilePath == "" {
		return movieTrack{}, false
	}
	return t, true
}

// otherTrackOwns reports whether path is a file another track of the job's movie holds
// (the same path, or the same file under another name). A conversion writes its result to
// <stem>.mkv, and a different file already there is moved aside first: when that file is
// another version of the same film, the swap would recycle it, so the job must not run.
func (s *Service) otherTrackOwns(ctx context.Context, job *Job, path string) bool {
	if job.Kind == "episode" {
		return false
	}
	t, ok := s.lookupMovieTrack(ctx, job.MovieID, job.VersionID)
	if !ok {
		return false
	}
	return library.OwnedByOther(path, t.others)
}

// otherTrackNote is the Problems entry for a conversion that would land on another
// track's file. It waits rather than gives up: renaming either file (Organize library)
// clears the way.
func otherTrackNote(final string) string {
	return "another version of this film is already at " + filepath.Base(final) +
		" — converting would replace it, so it's left alone until one of them is renamed"
}

// convertedPath is where a conversion of src lands: the same name, as Matroska. It's the
// name finalizeOutput swaps the result in under.
func convertedPath(src string) string {
	return strings.TrimSuffix(src, filepath.Ext(src)) + ".mkv"
}
