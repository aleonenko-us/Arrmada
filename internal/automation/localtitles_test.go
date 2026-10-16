package automation

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/movies"
)

// fakeTMDBTitles is TMDB's every-name lookup from a table (by TMDB id), or err for all.
type fakeTMDBTitles struct {
	titles map[int]*metadata.Titles
	err    error
	calls  atomic.Int32
}

func (f *fakeTMDBTitles) Titles(_ context.Context, _ string, id int) (*metadata.Titles, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	if t, ok := f.titles[id]; ok {
		return t, nil
	}
	return nil, metadata.ErrNotFound
}

func (f *fakeTMDBTitles) SearchLocalized(context.Context, string, string, string) ([]metadata.LocalizedHit, error) {
	return nil, nil
}

// GitHub #13: Wild (2014) grabbed from a German tracker as its German title imports to
// Wild instead of being held as "doesn't match". The film's names come from TMDB's
// alternative titles and translations (every country, every language).
func TestHoldMovieImportAcceptsLocalizedTitle(t *testing.T) {
	const wildRelease = "Der.grosse.Trip.Wild.2014.German.DL.DTSD.2160p.UHD.BluRay.x265-GSG9"
	setup := func(t *testing.T, src *fakeTMDBTitles) (*stallHarness, movies.Movie) {
		t.Helper()
		h := newStallHarness(t)
		h.c.movies.SetTitlesSource(src)
		repo := movies.NewRepo(h.c.db)
		wild, err := repo.Create(h.ctx, movies.Movie{TMDBID: 204082, Title: "Wild", Year: 2014, Monitored: true,
			Extra: &movies.MovieExtra{TitlesRead: true}})
		if err != nil {
			t.Fatal(err)
		}
		// Another film in the library, with a German title of its own.
		if _, err := repo.Create(h.ctx, movies.Movie{TMDBID: 387, Title: "Das Boot", Year: 1981, Monitored: true,
			Extra: &movies.MovieExtra{TitlesRead: true}}); err != nil {
			t.Fatal(err)
		}
		return h, wild
	}
	wildNames := func() *fakeTMDBTitles {
		return &fakeTMDBTitles{titles: map[int]*metadata.Titles{204082: {
			Title: "Wild", OriginalTitle: "Wild", Year: 2014,
			Alternatives: []metadata.AltTitle{{Title: "Der große Trip", Country: "DE"}},
			Translations: []metadata.TranslatedTitle{{Lang: "de", Country: "DE", Title: "Der große Trip – Wild"}},
		}}}
	}

	t.Run("the German title imports", func(t *testing.T) {
		src := wildNames()
		h, wild := setup(t, src)
		addGrab(t, h.c, "movie", wild.ID, wildRelease, hashFor(wildRelease))
		if reason, hold := h.c.HoldMovieImport(h.ctx, hashFor(wildRelease), wildRelease, t.TempDir()); hold {
			t.Fatalf("held Wild's German release: %s", reason)
		}
		if src.calls.Load() != 1 {
			t.Errorf("%d TMDB reads, want one", src.calls.Load())
		}
	})

	t.Run("another film's German title is still held", func(t *testing.T) {
		h, wild := setup(t, wildNames())
		const other = "Das.Boot.1981.German.DL.1080p.BluRay.x264-GRP" // a library film's own title
		addGrab(t, h.c, "movie", wild.ID, other, hashFor(other))
		if _, hold := h.c.HoldMovieImport(h.ctx, hashFor(other), other, t.TempDir()); !hold {
			t.Error("a download that is another library film was let through")
		}
		const stranger = "Die.Wolke.2014.German.DL.1080p.BluRay.x264-GRP" // no film's name at all
		addGrab(t, h.c, "movie", wild.ID, stranger, hashFor(stranger))
		reason, hold := h.c.HoldMovieImport(h.ctx, hashFor(stranger), stranger, t.TempDir())
		if !hold || !strings.Contains(reason, "Grabbed for") {
			t.Errorf("a download named for no name of Wild's: hold=%v %q", hold, reason)
		}
	})

	t.Run("the year guard", func(t *testing.T) {
		h, wild := setup(t, wildNames())
		const remake = "Der.grosse.Trip.Wild.2009.German.DL.1080p.BluRay.x264-GRP"
		addGrab(t, h.c, "movie", wild.ID, remake, hashFor(remake))
		if _, hold := h.c.HoldMovieImport(h.ctx, hashFor(remake), remake, t.TempDir()); !hold {
			t.Error("a download five years off Wild's was let through")
		}
	})

	t.Run("TMDB unavailable holds as before", func(t *testing.T) {
		h, wild := setup(t, &fakeTMDBTitles{err: errors.New("tmdb: HTTP 503")})
		addGrab(t, h.c, "movie", wild.ID, wildRelease, hashFor(wildRelease))
		reason, hold := h.c.HoldMovieImport(h.ctx, hashFor(wildRelease), wildRelease, t.TempDir())
		if !hold || !strings.Contains(reason, "Grabbed for") {
			t.Errorf("TMDB down: hold=%v %q, want the mismatch review as before", hold, reason)
		}
		if r := reviewFor(t, h.c, hashFor(wildRelease)); r.ReasonCode != ReasonMismatch {
			t.Errorf("review %+v, want a mismatch", r)
		}
	})
}

// The episode import gate: a release grabbed for a show under the show's name in another
// language imports into it; any other name, a year that isn't the show's, or names TMDB
// can't give right now are held for review as before.
func TestSeriesImportAcceptsLocalizedTitle(t *testing.T) {
	showNames := func() *fakeTMDBTitles {
		return &fakeTMDBTitles{titles: map[int]*metadata.Titles{3: {
			Title: "Show", Year: 2017,
			Translations: []metadata.TranslatedTitle{{Lang: "de", Country: "DE", Title: "Die Sendung"}},
		}}}
	}
	setup := func(t *testing.T, src *fakeTMDBTitles) *lifecycleHarness {
		t.Helper()
		h := newLifecycleHarness(t)
		mustExec(t, h.c, `UPDATE series SET year = 2017 WHERE id = ?`, h.seriesID)
		h.c.series.SetTitlesSource(src)
		return h
	}
	grab := func(t *testing.T, h *lifecycleHarness, name string) string {
		t.Helper()
		hash := hashFor(name)
		h.seriesGrab(t, name, hash)
		h.completedTorrent(name, seriesCategory, h.download(t, name, name+".mkv"))
		return hash
	}

	t.Run("the German title imports", func(t *testing.T) {
		src := showNames()
		h := setup(t, src)
		grab(t, h, "Die.Sendung.S01E01.German.1080p.WEB-DL.x264-GRP")
		h.c.ImportSeriesDownloads(h.ctx)
		if n := pendingReviews(t, h); n != 0 {
			t.Fatalf("%d review(s) held for the German grab, want it imported", n)
		}
		if !episodeHasFile(t, h, 1, 1) {
			t.Fatal("S01E01 has no file after importing the German-named grab")
		}
		// Still seeding: the next sweeps don't ask TMDB again, and never send it to Review.
		calls := src.calls.Load()
		src.err = errors.New("tmdb: HTTP 503")
		h.c.ImportSeriesDownloads(h.ctx)
		if src.calls.Load() != calls || pendingReviews(t, h) != 0 {
			t.Errorf("a later sweep asked TMDB %d more times, %d reviews", src.calls.Load()-calls, pendingReviews(t, h))
		}
	})

	t.Run("another name is held", func(t *testing.T) {
		h := setup(t, showNames())
		hash := grab(t, h, "Andere.Sendung.S01E02.German.1080p.WEB-DL.x264-GRP")
		h.c.ImportSeriesDownloads(h.ctx)
		if r := reviewFor(t, h.c, hash); r.ReasonCode != ReasonMismatch {
			t.Errorf("review %+v, want a mismatch", r)
		}
		if episodeHasFile(t, h, 1, 2) {
			t.Error("another show's episode was imported")
		}
	})

	t.Run("the year guard", func(t *testing.T) {
		h := setup(t, showNames())
		hash := grab(t, h, "Die.Sendung.1990.S01E02.German.1080p.WEB-DL.x264-GRP")
		h.c.ImportSeriesDownloads(h.ctx)
		if r := reviewFor(t, h.c, hash); r.ReasonCode != ReasonMismatch {
			t.Errorf("review %+v, want a mismatch for the 1990 show", r)
		}
	})

	t.Run("TMDB unavailable holds as before", func(t *testing.T) {
		h := setup(t, &fakeTMDBTitles{err: errors.New("tmdb: HTTP 503")})
		hash := grab(t, h, "Die.Sendung.S01E01.German.1080p.WEB-DL.x264-GRP")
		h.c.ImportSeriesDownloads(h.ctx)
		if r := reviewFor(t, h.c, hash); r.ReasonCode != ReasonMismatch {
			t.Errorf("review %+v, want the mismatch review as before", r)
		}
	})
}
