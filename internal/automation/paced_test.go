package automation

import (
	"fmt"
	"testing"

	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/series"
)

// searchedCount counts the titles of a table the sweeps have searched (last_search_at set).
func searchedCount(t *testing.T, h *stallHarness, table string) int {
	t.Helper()
	var n int
	if err := h.c.db.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE last_search_at != ''`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func pacedCount(t *testing.T, h *stallHarness, table string) int {
	t.Helper()
	var n int
	if err := h.c.db.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE search_paced = 1`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A Radarr import of many wanted films doesn't become one burst of searches: each missing
// sweep searches at most PacedSearchesPerSweep of the imported films (and every film that
// wasn't imported, as before), clearing the mark of the ones it searched.
func TestMissingSweepPacesImportedMovies(t *testing.T) {
	h := newStallHarness(t)
	const imported, ordinary = 25, 2
	for i := 1; i <= imported+ordinary; i++ {
		// Out since 2001, so every one is due a search.
		m, err := movies.NewRepo(h.c.db).Create(h.ctx, movies.Movie{TMDBID: i, Title: fmt.Sprintf("Film %d", i), Year: 2001,
			Monitored: true, MinAvailability: "announced"})
		if err != nil {
			t.Fatal(err)
		}
		if i <= imported {
			if err := h.c.movies.SetSearchPaced(h.ctx, m.ID, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Nothing to grab: each search is a miss, which also keeps it from being searched again
	// within the backoff.
	h.ix.offer()

	h.c.SearchMissing(h.ctx)
	if got, want := searchedCount(t, h, "movies"), ordinary+PacedSearchesPerSweep; got != want {
		t.Fatalf("first sweep searched %d films, want %d (%d ordinary + %d imported)", got, want, ordinary, PacedSearchesPerSweep)
	}
	if got := pacedCount(t, h, "movies"); got != imported-PacedSearchesPerSweep {
		t.Fatalf("films still waiting after one sweep = %d, want %d", got, imported-PacedSearchesPerSweep)
	}
	h.c.SearchMissing(h.ctx)
	h.c.SearchMissing(h.ctx)
	if got := searchedCount(t, h, "movies"); got != imported+ordinary {
		t.Fatalf("after three sweeps %d of %d films were searched", got, imported+ordinary)
	}
	if got := pacedCount(t, h, "movies"); got != 0 {
		t.Fatalf("%d films still marked after every one was searched", got)
	}
}

// The same for shows from a Sonarr import.
func TestMissingSweepPacesImportedSeries(t *testing.T) {
	h := newStallHarness(t)
	repo := series.NewRepo(h.c.db)
	const imported = PacedSearchesPerSweep + 3
	for i := 1; i <= imported; i++ {
		sr, err := repo.Create(h.ctx, series.Series{TMDBID: i, Title: fmt.Sprintf("Show %d", i), Year: 2020, Monitored: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.InsertSeasons(h.ctx, sr.ID, []series.Season{{SeasonNumber: 1, Monitored: true, Episodes: []series.Episode{
			{SeasonNumber: 1, EpisodeNumber: 1, AirDate: "2021-01-01", Monitored: true},
		}}}); err != nil {
			t.Fatal(err)
		}
		if err := h.c.series.SetSearchPaced(h.ctx, sr.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	h.ix.offer()

	h.c.SearchSeriesMissing(h.ctx)
	if got := searchedCount(t, h, "series"); got != PacedSearchesPerSweep {
		t.Fatalf("first sweep searched %d shows, want %d", got, PacedSearchesPerSweep)
	}
	h.c.SearchSeriesMissing(h.ctx)
	if got := searchedCount(t, h, "series"); got != imported {
		t.Fatalf("after two sweeps %d of %d shows were searched", got, imported)
	}
	if got := pacedCount(t, h, "series"); got != 0 {
		t.Fatalf("%d shows still marked", got)
	}
}
