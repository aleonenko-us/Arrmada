package automation

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/series"
)

func spanishShow(id int64, title string, year int, local string) series.Series {
	return series.Series{ID: id, TMDBID: int(id), Title: title, Year: year, Monitored: true,
		Extra: &series.SeriesExtra{OriginCountry: []string{"US"}, LocalLangs: []string{"es"},
			LocalTitles: []metadata.TranslatedTitle{{Lang: "es", Title: local}}}}
}

// RSS: HBO's English "Fantasmas" is not Ghosts, whose Spanish title it is. Nothing in the
// name says Spanish, the year or the country, so the feed gives it to no show; a Spanish
// release of it goes to Ghosts.
func TestRSSTranslatedTitleNeedsEvidence(t *testing.T) {
	ghosts := spanishShow(1, "Ghosts", 2021, "Fantasmas")
	all := []series.Series{ghosts, spanishShow(2, "Severance", 2022, "Separación")}
	svc := series.NewService(nil, nil, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetTitleLanguages(func() []string { return []string{"es"} })
	feed := []indexer.Release{
		{Title: "Fantasmas.S01E03.1080p.WEB.h264-GRP"},
		{Title: "Fantasmas.S01E03.1080p.AMZN.WEB-DL.DDP5.1-GRP"},
		{Title: "Fantasmas.S02E01.SPANISH.1080p.WEB.h264-GRP"},
	}
	got := rssMatchesFit(svc.SearchFitFor(all), all, feed)
	if len(got[1]) != 1 || got[1][0].Title != feed[2].Title {
		t.Errorf("Ghosts = %+v, want only the Spanish release", got[1])
	}
	if len(got[2]) != 0 {
		t.Errorf("Severance = %+v", got[2])
	}
}

// The extra query's results face the same rule: a release it brings back with nothing to
// say it's the show isn't grabbed, and isn't counted as matching.
func TestTranslatedQueryResultsNeedEvidence(t *testing.T) {
	h := newStallHarness(t)
	h.c.series.SetTitleLanguages(func() []string { return []string{"es"} })
	feed := &queryTorznab{byQuery: map[string][]string{"Fantasmas": {"Fantasmas.S01E01.1080p.WEB.h264-GRP"}}}
	srv := httptest.NewServer(feed)
	t.Cleanup(srv.Close)
	if _, err := h.c.indexers.Create(h.ctx, indexer.Indexer{Name: "Q", Kind: indexer.KindTorznab, URL: srv.URL, Priority: 5, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	repo := series.NewRepo(h.c.db)
	g := spanishShow(0, "Ghosts", 2021, "Fantasmas")
	g.TMDBID = 105971
	sr, err := repo.Create(h.ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.InsertSeasons(h.ctx, sr.ID, []series.Season{{SeasonNumber: 1, Monitored: true, Episodes: []series.Episode{
		{SeasonNumber: 1, EpisodeNumber: 1, AirDate: "2021-10-07", Monitored: true},
	}}}); err != nil {
		t.Fatal(err)
	}
	out, err := h.c.SearchSeriesNow(h.ctx, sr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if countQueries(feed.asked(), "Fantasmas") != 1 {
		t.Fatalf("queries %q, want one by the Spanish title", feed.asked())
	}
	if out.Grabbed != 0 || out.Matching != 0 || out.Returned != 1 {
		t.Errorf("outcome %+v: want the English show's release returned, not matched, not grabbed", out)
	}
	if calls := h.qbit.callLog(); len(calls) != 0 {
		t.Errorf("client calls %q", calls)
	}

	// The same query bringing back a Spanish release grabs it, counted as matching.
	h2, feed2, sr2 := translatedSearchHarness(t, []string{"de"}, "Haus des Geldes")
	out, err = h2.c.SearchSeriesNow(h2.ctx, sr2.ID)
	if err != nil || out.Grabbed != 1 || out.Matching != 1 {
		t.Errorf("German release: %+v, %v (queries %q)", out, err, feed2.asked())
	}
}

// A torrent added by hand under the show's translated title holds its season back in the
// sweep, as the episode indicator shows it downloading; without the setting it doesn't.
func TestInFlightUntrackedByTranslatedTitle(t *testing.T) {
	heist := germanSeries(1, "Money Heist", 2017, "Haus des Geldes")
	untracked := []download.Item{tvItem("Haus.des.Geldes.S02.German.DL.1080p.WEB.x264-GRP", 0.4, "downloading")}
	if seasons, _, _ := seriesInFlightScope(nil, untracked, heist); len(seasons) != 0 {
		t.Errorf("setting empty: held %v", seasons)
	}
	svc := series.NewService(nil, nil, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetTitleLanguages(func() []string { return []string{"de"} })
	fit := svc.SearchFitFor([]series.Series{heist})
	if seasons, whole, _ := seriesInFlightScopeFit(fit, nil, untracked, heist); whole || !seasons[2] || len(seasons) != 1 {
		t.Errorf("held %v (whole %v), want season 2", seasons, whole)
	}
	if busy := seriesBusyFit(fit, nil, untracked, heist); busy == "" {
		t.Error("the upgrade sweep's check doesn't see it")
	}
}
