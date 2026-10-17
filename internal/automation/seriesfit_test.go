package automation

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/series"
)

const heistGerman = "Haus.des.Geldes.S01E01.German.DL.1080p.WEB.x264-GRP"

// germanSeries is a show already read in German, with these German titles.
func germanSeries(id int64, title string, year int, local ...string) series.Series {
	s := series.Series{ID: id, TMDBID: int(id), Title: title, Year: year, Monitored: true,
		Extra: &series.SeriesExtra{LocalLangs: []string{"de"}}}
	for _, l := range local {
		s.Extra.LocalTitles = append(s.Extra.LocalTitles, metadata.TranslatedTitle{Lang: "de", Title: l})
	}
	return s
}

// The RSS feed: a release named by a show's German title goes to that show; one named by a
// German title two shows share, or by another show's own title, never goes to a show by
// its translation; and with the setting empty the feed matches as it always did.
func TestRSSMatchesTranslatedTitles(t *testing.T) {
	heist := germanSeries(1, "Money Heist", 2017, "Haus des Geldes")
	haus := germanSeries(2, "Haus", 2004)
	other := germanSeries(3, "Other House", 2010, "Haus")
	bridgeUS := germanSeries(4, "The Bridge", 2013, "Die Brücke")
	bridgeDK := germanSeries(5, "Bron", 2011, "Die Brücke")
	all := []series.Series{heist, haus, other, bridgeUS, bridgeDK}
	feed := []indexer.Release{
		{Title: heistGerman},
		{Title: "Haus.S02E03.German.1080p.WEB-GRP"},
		{Title: "Die.Brucke.S01E01.German.DL.1080p-GRP"},
		{Title: "Die.Brucke.2013.S01E01.German.DL.1080p-GRP"},
	}
	svc := series.NewService(nil, nil, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	off := rssMatchesFit(svc.SearchFitFor(all), all, feed)
	if want := rssMatches(all, feed); len(off) != len(want) || len(off[2]) != 1 || len(off) != 1 {
		t.Fatalf("setting empty: %+v, want only Haus's own release (%+v)", off, want)
	}

	svc.SetTitleLanguages(func() []string { return []string{"de"} })
	got := rssMatchesFit(svc.SearchFitFor(all), all, feed)
	if len(got[1]) != 1 || got[1][0].Title != heistGerman {
		t.Errorf("Money Heist = %+v, want its German release", got[1])
	}
	if len(got[2]) != 1 {
		t.Errorf("Haus = %+v, want its own release", got[2])
	}
	for _, id := range []int64{3, 4, 5} {
		if len(got[id]) > 0 {
			t.Errorf("series %d took %+v by a translated title it doesn't own alone", id, got[id])
		}
	}
	// No release goes to two shows.
	seen := map[string]int64{}
	for id, rels := range got {
		for _, r := range rels {
			if prev, dup := seen[r.Title]; dup {
				t.Errorf("%s went to both %d and %d", r.Title, prev, id)
			}
			seen[r.Title] = id
		}
	}
}

// translatedSearchHarness is a harness with an indexer that answers by query, a Money Heist
// read in German with one aired episode missing, and the German title on the indexer only.
func translatedSearchHarness(t *testing.T, langs []string, local ...string) (*stallHarness, *queryTorznab, series.Series) {
	t.Helper()
	h := newStallHarness(t)
	h.c.series.SetTitleLanguages(func() []string { return langs })
	feed := &queryTorznab{byQuery: map[string][]string{"Haus des Geldes": {heistGerman}}}
	srv := httptest.NewServer(feed)
	t.Cleanup(srv.Close)
	if _, err := h.c.indexers.Create(h.ctx, indexer.Indexer{Name: "Q", Kind: indexer.KindTorznab, URL: srv.URL, Priority: 5, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	repo := series.NewRepo(h.c.db)
	g := germanSeries(0, "Money Heist", 2017, local...)
	g.TMDBID = 71446
	sr, err := repo.Create(h.ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.InsertSeasons(h.ctx, sr.ID, []series.Season{{SeasonNumber: 1, Monitored: true, Episodes: []series.Episode{
		{SeasonNumber: 1, EpisodeNumber: 1, AirDate: "2017-05-02", Monitored: true},
	}}}); err != nil {
		t.Fatal(err)
	}
	return h, feed, sr
}

func countQueries(qs []string, sub string) int {
	n := 0
	for _, q := range qs {
		if strings.Contains(q, sub) {
			n++
		}
	}
	return n
}

// Search now: the episode the English title can't find is asked for once more by the
// German title and grabbed — only with the setting on and a German title stored, and only
// ever once a search.
func TestSeriesSearchAsksByTranslatedTitleOnce(t *testing.T) {
	t.Run("German title stored", func(t *testing.T) {
		h, feed, sr := translatedSearchHarness(t, []string{"de"}, "Haus des Geldes")
		out, err := h.c.SearchSeriesNow(h.ctx, sr.ID)
		if err != nil {
			t.Fatal(err)
		}
		if out.Grabbed != 1 || len(out.GrabbedTitles) != 1 || out.GrabbedTitles[0] != heistGerman {
			t.Fatalf("outcome %+v, want the German release grabbed", out)
		}
		if n := countQueries(feed.asked(), "Haus des Geldes"); n != 1 {
			t.Errorf("queries %q: %d by the German title, want one", feed.asked(), n)
		}
	})
	t.Run("setting empty", func(t *testing.T) {
		h, feed, sr := translatedSearchHarness(t, nil, "Haus des Geldes")
		out, err := h.c.SearchSeriesNow(h.ctx, sr.ID)
		if err != nil {
			t.Fatal(err)
		}
		if out.Grabbed != 0 || countQueries(feed.asked(), "Haus") != 0 {
			t.Errorf("outcome %+v, queries %q: want nothing asked by or grabbed under the German title", out, feed.asked())
		}
	})
	t.Run("no German title stored", func(t *testing.T) {
		h, feed, sr := translatedSearchHarness(t, []string{"de"})
		if _, err := h.c.SearchSeriesNow(h.ctx, sr.ID); err != nil {
			t.Fatal(err)
		}
		if countQueries(feed.asked(), "Haus") != 0 {
			t.Errorf("queries %q: asked by a title the show hasn't got", feed.asked())
		}
	})
	t.Run("the English title found it", func(t *testing.T) {
		h, feed, sr := translatedSearchHarness(t, []string{"de"}, "Haus des Geldes")
		feed.byQuery["Money Heist"] = []string{"Money.Heist.S01E01.1080p.WEB.x264-GRP"}
		out, err := h.c.SearchSeriesNow(h.ctx, sr.ID)
		if err != nil {
			t.Fatal(err)
		}
		if out.Grabbed != 1 || countQueries(feed.asked(), "Haus") != 0 {
			t.Errorf("outcome %+v, queries %q: nothing was left to ask the German title for", out, feed.asked())
		}
	})
	t.Run("another show's German release isn't taken", func(t *testing.T) {
		h, feed, sr := translatedSearchHarness(t, []string{"de"}, "Haus des Geldes")
		// A second library show is translated alike: the title names neither.
		g := germanSeries(0, "Money Heist Korea", 2022, "Haus des Geldes")
		g.TMDBID = 113036
		if _, err := series.NewRepo(h.c.db).Create(h.ctx, g); err != nil {
			t.Fatal(err)
		}
		out, err := h.c.SearchSeriesNow(h.ctx, sr.ID)
		if err != nil {
			t.Fatal(err)
		}
		if out.Grabbed != 0 || countQueries(feed.asked(), "Haus") != 0 {
			t.Errorf("outcome %+v, queries %q: a shared German title was asked by or taken", out, feed.asked())
		}
	})
}

// The interactive list asks by the German title once, scoped like the main query, and
// lists what it finds.
func TestInteractiveSeriesSearchAsksByTranslatedTitle(t *testing.T) {
	h, feed, sr := translatedSearchHarness(t, []string{"de"}, "Haus des Geldes")
	list, err := h.c.RankSeriesReleases(h.ctx, sr.ID, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n := countQueries(feed.asked(), "Haus des Geldes"); n != 1 {
		t.Errorf("queries %q: %d by the German title, want one", feed.asked(), n)
	}
	found := false
	for _, r := range list.Releases {
		found = found || r.Title == heistGerman
	}
	if !found {
		t.Errorf("the German release isn't listed: %+v", list.Releases)
	}

	q, ok := translatedScopeQuery(h.c.seriesFit(h.ctx), sr, 1, 1, nil)
	if !ok || q.Text != "Haus des Geldes" || q.Season != 1 || q.Episode != 1 || q.TVDBID != 0 || q.IMDBID != "" {
		t.Errorf("scope query %+v, %v", q, ok)
	}
	if _, ok := translatedScopeQuery(nil, sr, 1, 1, nil); ok {
		t.Error("a query with the setting off")
	}
}

// RSS end to end: the feed's German release is grabbed for the show it names, and a
// German title two library shows share sends nothing to the client.
func TestRSSSyncGrabsByTranslatedTitleOnlyWhenUnambiguous(t *testing.T) {
	h := newStallHarness(t)
	h.c.series.SetTitleLanguages(func() []string { return []string{"de"} })
	repo := series.NewRepo(h.c.db)
	for i, g := range []series.Series{
		germanSeries(0, "Money Heist", 2017, "Haus des Geldes"),
		germanSeries(0, "The Bridge", 2013, "Die Brücke"),
		germanSeries(0, "Bron", 2011, "Die Brücke"),
	} {
		g.TMDBID = 100 + i
		sr, err := repo.Create(h.ctx, g)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.InsertSeasons(h.ctx, sr.ID, []series.Season{{SeasonNumber: 1, Monitored: true, Episodes: []series.Episode{
			{SeasonNumber: 1, EpisodeNumber: 1, AirDate: "2017-05-02", Monitored: true},
		}}}); err != nil {
			t.Fatal(err)
		}
	}
	const bridge = "Die.Brucke.S01E01.German.DL.1080p.WEB.x264-GRP"
	h.ix.offer(heistGerman, bridge)
	h.c.RSSSyncSeries(h.ctx)
	if calls := h.qbit.callLog(); strings.Join(calls, "|") != "add "+magnetFor(heistGerman) {
		t.Fatalf("client calls = %q, want only Money Heist's German release added", calls)
	}
}
