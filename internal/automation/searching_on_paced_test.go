package automation

import (
	"fmt"
	"testing"

	"github.com/tristenlammi/arrmada/internal/series"
)

// scannedShows adds n shows the way a library scan does: searching off, nothing
// monitored, one aired episode without a file.
func scannedShows(t *testing.T, h *stallHarness, n int) []int64 {
	t.Helper()
	repo := series.NewRepo(h.c.db)
	ids := make([]int64, 0, n)
	for i := 1; i <= n; i++ {
		sr, err := repo.Create(h.ctx, series.Series{TMDBID: 5000 + i, Title: fmt.Sprintf("Scanned %d", i), Year: 2020})
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.InsertSeasons(h.ctx, sr.ID, []series.Season{{SeasonNumber: 1, Episodes: []series.Episode{
			{SeasonNumber: 1, EpisodeNumber: 1, AirDate: "2021-01-01"},
		}}}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, sr.ID)
	}
	return ids
}

// GH-53 review: a scanned library turned on all at once — Select all → Searching on, or a
// bulk preset — is searched a few shows per sweep, as a Sonarr import is, not in one burst
// of searches and grabs.
func TestSearchingOnInBulkIsPaced(t *testing.T) {
	for _, tc := range []struct {
		name string
		on   func(h *stallHarness, id int64) error
	}{
		{"Searching on", func(h *stallHarness, id int64) error { return h.c.series.SetMonitored(h.ctx, id, true) }},
		{"a preset", func(h *stallHarness, id int64) error {
			return h.c.series.SetMonitoring(h.ctx, id, series.MonitorChange{Preset: series.PresetMissing})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newStallHarness(t)
			const shows = PacedSearchesPerSweep + 4
			for _, id := range scannedShows(t, h, shows) {
				if err := tc.on(h, id); err != nil {
					t.Fatal(err)
				}
			}
			if got := pacedCount(t, h, "series"); got != shows {
				t.Fatalf("shows marked to wait their turn = %d, want all %d", got, shows)
			}
			h.ix.offer()
			h.c.SearchSeriesMissing(h.ctx)
			if got := searchedCount(t, h, "series"); got != PacedSearchesPerSweep {
				t.Fatalf("first sweep searched %d shows, want %d", got, PacedSearchesPerSweep)
			}
			h.c.SearchSeriesMissing(h.ctx)
			if got := searchedCount(t, h, "series"); got != shows {
				t.Fatalf("after two sweeps %d of %d shows were searched", got, shows)
			}
			if got := pacedCount(t, h, "series"); got != 0 {
				t.Fatalf("%d shows still marked", got)
			}
		})
	}
}

// One show turned on alone waits for nothing: the next sweep searches it.
func TestSearchingOnOneShowIsSearchedNextSweep(t *testing.T) {
	h := newStallHarness(t)
	id := scannedShows(t, h, 1)[0]
	if err := h.c.series.SetMonitored(h.ctx, id, true); err != nil {
		t.Fatal(err)
	}
	h.ix.offer()
	h.c.SearchSeriesMissing(h.ctx)
	if got := searchedCount(t, h, "series"); got != 1 {
		t.Fatalf("the next sweep searched %d shows, want the one turned on", got)
	}
	// Turning searching on for a show already on doesn't mark it again.
	if err := h.c.series.SetMonitored(h.ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if got := pacedCount(t, h, "series"); got != 0 {
		t.Fatalf("a show already searching was marked again (%d marked)", got)
	}
}
