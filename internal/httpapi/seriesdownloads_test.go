package httpapi

import (
	"io"
	"log/slog"
	"testing"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/parser"
	"github.com/tristenlammi/arrmada/internal/series"
)

// One pass over the queue: each item parsed once however many episodes the show has, a
// season pack lighting its whole season, an episode release only its own episode, and
// another show's, a finished one's and a movie's nothing.
func TestEpisodeDownloadsSinglePass(t *testing.T) {
	queue := []download.Item{
		{Name: "Show.S02.1080p.WEB-DL-GRP", Category: download.CategoryTV, State: "downloading", Progress: 0.4},
		{Name: "Show.S01E03.1080p.WEB-DL-GRP", Category: download.CategoryTV, State: "downloading", Progress: 0.7},
		{Name: "Other.Show.S01E01.1080p.WEB-DL-GRP", Category: download.CategoryTV, State: "downloading", Progress: 0.1},
		{Name: "Show.S01E04.1080p.WEB-DL-GRP", Category: download.CategoryTV, State: "seeding", Progress: 1},
		{Name: "Show.S01E05.1080p.WEB-DL-GRP", Category: "arrmada-movies", State: "downloading", Progress: 0.2},
	}
	s := series.Series{Title: "Show"}
	for sn := 1; sn <= 2; sn++ {
		season := series.Season{SeasonNumber: sn}
		for ep := 1; ep <= 10; ep++ {
			season.Episodes = append(season.Episodes, series.Episode{SeasonNumber: sn, EpisodeNumber: ep})
		}
		s.Seasons = append(s.Seasons, season)
	}

	parses := map[string]int{}
	idx := indexEpisodeDownloads(queue, s, func(name string) parser.Release {
		parses[name]++
		return parser.Parse(name)
	})
	for name, n := range parses {
		if n != 1 {
			t.Errorf("%q parsed %d times, want once", name, n)
		}
	}
	if len(parses) != 3 {
		t.Errorf("parsed %d items, want the 3 incomplete TV downloads only", len(parses))
	}

	for ep := 1; ep <= 10; ep++ {
		if d := idx.lookup(2, ep); d == nil || d.Progress != 0.4 {
			t.Errorf("S02E%02d = %+v, want the season pack", ep, d)
		}
	}
	if d := idx.lookup(1, 3); d == nil || d.Progress != 0.7 {
		t.Errorf("S01E03 = %+v, want its own release", d)
	}
	for _, ep := range []int{1, 2, 4, 5} {
		if d := idx.lookup(1, ep); d != nil {
			t.Errorf("S01E%02d = %+v, want nothing (other show, seeding, wrong category or not downloading)", ep, d)
		}
	}
}

// A complete-series pack covers every season; an anime absolute-numbered release, which
// names no season, doesn't light up the specials.
func TestEpisodeDownloadsCompleteAndAbsolute(t *testing.T) {
	queue := []download.Item{
		{Name: "[SubsPlease] Show - 05 (1080p)", Category: download.CategoryTV, State: "downloading", Progress: 0.3},
	}
	idx := indexEpisodeDownloads(queue, series.Series{Title: "Show"}, parser.Parse)
	if d := idx.lookup(0, 1); d != nil {
		t.Errorf("an absolute-numbered release lit up a special: %+v", d)
	}
	queue = append(queue, download.Item{Name: "Show.S01-S05.Complete.1080p.BluRay-GRP", Category: download.CategoryTV, State: "downloading", Progress: 0.5})
	idx = indexEpisodeDownloads(queue, series.Series{Title: "Show"}, parser.Parse)
	if d := idx.lookup(3, 7); d == nil || d.Progress != 0.5 {
		t.Errorf("S03E07 = %+v, want the complete pack", d)
	}
}

// A download a search took by the show's German title lights its episode, by the search's
// own rule (series.SearchFit); without the setting it doesn't.
func TestEpisodeDownloadsByTranslatedTitle(t *testing.T) {
	heist := series.Series{ID: 1, TMDBID: 71446, Title: "Money Heist", Year: 2017, Extra: &series.SeriesExtra{
		LocalLangs: []string{"de"}, LocalTitles: []metadata.TranslatedTitle{{Lang: "de", Title: "Haus des Geldes"}}}}
	heist.Seasons = []series.Season{{SeasonNumber: 1, Episodes: []series.Episode{{SeasonNumber: 1, EpisodeNumber: 1}}}}
	queue := []download.Item{{Name: "Haus.des.Geldes.S01E01.German.DL.1080p.WEB.x264-GRP", Category: download.CategoryTV, State: "downloading", Progress: 0.5}}
	svc := series.NewService(nil, nil, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if d := indexEpisodeDownloadsFit(queue, heist, parser.Parse, svc.SearchFitFor([]series.Series{heist})).lookup(1, 1); d != nil {
		t.Errorf("setting empty: %+v", d)
	}
	svc.SetTitleLanguages(func() []string { return []string{"de"} })
	if d := indexEpisodeDownloadsFit(queue, heist, parser.Parse, svc.SearchFitFor([]series.Series{heist})).lookup(1, 1); d == nil || d.Progress != 0.5 {
		t.Errorf("S01E01 = %+v, want the German download", d)
	}
}
