package movies

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

func TestDatesDue(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	day := func(d time.Duration) string { return now.Add(d).Format(time.DateOnly) }
	const dayDur = 24 * time.Hour
	read := func(ex MovieExtra) Movie {
		ex.DatesRead = true
		return Movie{TMDBID: 7, Year: 2026, Extra: &ex}
	}
	cases := []struct {
		name string
		m    Movie
		want bool
	}{
		{"no tmdb id", Movie{Year: 2026}, false},
		{"never read", Movie{TMDBID: 7}, true},
		{"read before dates were kept", Movie{TMDBID: 7, Extra: &MovieExtra{TitlesRead: true}}, true},
		{"read yesterday", read(MovieExtra{DatesReadAt: day(-dayDur)}), false},
		{"no home date yet, in cinemas", read(MovieExtra{DatesReadAt: day(-4 * dayDur), TheatricalDate: day(-20 * dayDur)}), true},
		{"home date still to come", read(MovieExtra{DatesReadAt: day(-4 * dayDur), HomeReleaseDate: day(10 * dayDur)}), true},
		{"home release long past", read(MovieExtra{DatesReadAt: day(-40 * dayDur), HomeReleaseDate: day(-90 * dayDur)}), false},
		{"old film TMDB has no home date for", read(MovieExtra{DatesReadAt: day(-40 * dayDur), TheatricalDate: "1999-03-31"}), false},
		{"nothing listed, old film", Movie{TMDBID: 7, Year: 1999, Extra: &MovieExtra{DatesRead: true, DatesReadAt: day(-9 * dayDur)}}, false},
		{"nothing listed, this year", read(MovieExtra{DatesReadAt: day(-9 * dayDur)}), true},
	}
	for _, tc := range cases {
		if got := DatesDue(tc.m, now); got != tc.want {
			t.Errorf("%s: DatesDue = %v, want %v", tc.name, got, tc.want)
		}
	}
}

type datesMeta struct {
	calls atomic.Int32
}

func (f *datesMeta) Available() bool { return true }
func (f *datesMeta) SearchMovie(context.Context, string) ([]metadata.MovieResult, error) {
	return nil, nil
}
func (f *datesMeta) GetMovie(context.Context, int) (*metadata.MovieDetails, error) {
	f.calls.Add(1)
	return &metadata.MovieDetails{HomeReleaseDate: "2026-03-12", TheatricalDate: "2026-02-20"}, nil
}
func (f *datesMeta) GetCollection(context.Context, int) (*metadata.Collection, error) {
	return nil, errors.New("no collections here")
}

// A movie whose titles were read before dates were kept gets its dates from the same
// backfill, without its titles being touched.
func TestBackfillReadsReleaseDates(t *testing.T) {
	meta := &datesMeta{}
	s := titlesService(t, meta)
	ctx := context.Background()
	m, err := s.repo.Create(ctx, Movie{TMDBID: 9, Title: "Resident Evil", Year: 2026,
		Extra: &MovieExtra{TitlesRead: true, OriginalTitle: "Kept", Genres: []string{"Horror"}}})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.BackfillAltTitles(ctx, time.Millisecond); err != nil || n != 1 || meta.calls.Load() != 1 {
		t.Fatalf("filled %d (%d calls), err %v", n, meta.calls.Load(), err)
	}
	got, _ := s.repo.Get(ctx, m.ID)
	ex := got.Extra
	if ex == nil || !ex.DatesRead || ex.HomeReleaseDate != "2026-03-12" || ex.TheatricalDate != "2026-02-20" || ex.DatesReadAt == "" {
		t.Fatalf("extra = %+v, want the dates read", ex)
	}
	if ex.OriginalTitle != "Kept" || len(ex.Genres) != 1 {
		t.Errorf("the rest of the extra changed: %+v", ex)
	}
	// Read today: nothing to do on the next pass.
	if n, _ := s.BackfillAltTitles(ctx, time.Millisecond); n != 0 || meta.calls.Load() != 1 {
		t.Errorf("second pass filled %d (%d calls)", n, meta.calls.Load())
	}
}
