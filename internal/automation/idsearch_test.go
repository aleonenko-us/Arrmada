package automation

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/series"
)

// INT-22: the indexer tagged the release with the movie's own IMDb id, so a title the
// library doesn't know it by is still the film — as long as the year fits.
func TestMatchedByIMDbAcceptsForeignTitle(t *testing.T) {
	leon := movies.Movie{Title: "Léon: The Professional", Year: 1994, IMDBID: "tt0110413"}
	cases := []struct {
		name string
		rel  indexer.Release
		want bool
	}{
		{"imdb tag, other title", indexer.Release{Title: "Leon.1994.1080p.BluRay.x264-GRP", IMDBID: "tt0110413"}, true},
		{"imdb tag in the indexer's short form", indexer.Release{Title: "Professional.1994.720p-GRP", IMDBID: "tt110413"}, true},
		{"imdb tag, wrong year", indexer.Release{Title: "Leon.2004.1080p.BluRay.x264-GRP", IMDBID: "tt0110413"}, false},
		{"no imdb tag, other title", indexer.Release{Title: "Leon.1994.1080p.BluRay.x264-GRP"}, false},
		{"from an id search, no tag, other title", indexer.Release{Title: "Leon.1994.1080p.BluRay.x264-GRP", MatchedByID: true}, false},
		{"no tag, its own title", indexer.Release{Title: "Leon.The.Professional.1994.1080p.BluRay.x264-GRP"}, true},
		{"another film's imdb tag, same title", indexer.Release{Title: "Leon.The.Professional.1994.1080p.BluRay.x264-GRP", IMDBID: "tt9999999"}, false},
	}
	for _, c := range cases {
		if got := movieReleaseMatches(c.rel, leon, movies.TitleKeys(leon, nil)); got != c.want {
			t.Errorf("%s: movieReleaseMatches(%q) = %v, want %v", c.name, c.rel.Title, got, c.want)
		}
	}
	// A library movie without an IMDb id falls back to its title, whatever the tag says.
	noID := movies.Movie{Title: "Léon: The Professional", Year: 1994}
	if movieReleaseMatches(indexer.Release{Title: "Leon.1994.1080p-GRP", IMDBID: "tt0110413"}, noID, movies.TitleKeys(noID, nil)) {
		t.Error("without the movie's own IMDb id, a tag proves nothing")
	}
}

func TestMovieQueryCarriesIDs(t *testing.T) {
	q := movieQuery(movies.Movie{Title: "Arrival", Year: 2016, IMDBID: "tt2543164", TMDBID: 329865})
	if q.Text != "Arrival 2016" || q.IMDBID != "tt2543164" || q.TMDBID != 329865 || q.MediaType != indexer.MediaMovie {
		t.Fatalf("movieQuery = %+v", q)
	}
}

// Anime searches never name the show by id: the numbering resolver depends on the broad
// text search returning every numbering convention.
func TestAnimeSeriesSearchSendsNoIDs(t *testing.T) {
	anime := series.Series{Title: "Frieren", TVDBID: 424536, IMDBID: "tt22248376", SeriesType: series.SeriesTypeAnime}
	for _, q := range seriesScopeQueries(anime, 1, 13, 13, []string{"Frieren Arc 13"}) {
		if q.TVDBID != 0 || q.IMDBID != "" {
			t.Errorf("anime query %q carries ids: %+v", q.Text, q)
		}
	}
	if q := seriesQuery(anime, "Frieren", 100); q.TVDBID != 0 || q.IMDBID != "" {
		t.Errorf("seriesQuery(anime) = %+v", q)
	}

	std := series.Series{Title: "The Wire", TVDBID: 79126, IMDBID: "tt0306414"}
	qs := seriesScopeQueries(std, 2, 5, 0, nil)
	if qs[0].TVDBID != 79126 || qs[0].IMDBID != "tt0306414" || qs[0].Season != 2 || qs[0].Episode != 5 {
		t.Errorf("standard main query = %+v, want tvdbid with season and episode", qs[0])
	}
	// Aliases are other names, searched as text.
	std.Aliases = []series.Alias{{Title: "Wire"}}
	for _, q := range seriesScopeQueries(std, -1, 0, 0, nil)[1:] {
		if q.TVDBID != 0 || q.IMDBID != "" {
			t.Errorf("alias query %q carries ids", q.Text)
		}
	}
}

// idTorznab answers an imdbid search with releases tagged with that id, and a text search
// with textTitles; it records every query.
type idTorznab struct {
	mu         sync.Mutex
	idTitles   []string
	textTitles []string
	queries    []url.Values
}

func (f *idTorznab) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := r.URL.Query()
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>`)
	if q.Get("offset") == "" {
		f.queries = append(f.queries, q)
		titles, imdb := f.textTitles, ""
		if id := q.Get("imdbid"); id != "" {
			titles, imdb = f.idTitles, id
		}
		for _, title := range titles {
			fmt.Fprintf(&b, `<item><title>%s</title><link>%s</link><size>%d</size><torznab:attr name="seeders" value="25"/>`,
				html.EscapeString(title), html.EscapeString(magnetFor(title)), int64(4<<30))
			if imdb != "" {
				fmt.Fprintf(&b, `<torznab:attr name="imdb" value="%s"/>`, imdb)
			}
			b.WriteString(`</item>`)
		}
	}
	b.WriteString(`</channel></rss>`)
	fmt.Fprint(w, b.String())
}

// End to end: an indexer whose caps take imdbid is asked t=movie&imdbid=…, and the
// release it answers under the film's original title is grabbed for it.
func TestMovieSearchByIMDbGrabsForeignTitle(t *testing.T) {
	h := newStallHarness(t)
	feed := &idTorznab{idTitles: []string{"Le.Fabuleux.Destin.d.Amelie.Poulain.2001.1080p.BluRay.x264-GRP"}}
	srv := httptest.NewServer(feed)
	t.Cleanup(srv.Close)
	ix, err := h.c.indexers.Create(h.ctx, indexer.Indexer{Name: "ByID", Kind: indexer.KindTorznab, URL: srv.URL, Priority: 5, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.c.db.Exec(`UPDATE indexers SET caps_json = ? WHERE id = ?`,
		`{"movie":{"available":true,"params":["q","imdbid"]}}`, ix.ID); err != nil {
		t.Fatal(err)
	}
	m, err := movies.NewRepo(h.c.db).Create(h.ctx, movies.Movie{TMDBID: 194, IMDBID: "tt0211915", Title: "Amélie", Year: 2001, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}

	out, err := h.c.SearchMovie(h.ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Grabbed != 1 {
		t.Fatalf("outcome = %+v, want the id-tagged release grabbed", out)
	}
	feed.mu.Lock()
	defer feed.mu.Unlock()
	if len(feed.queries) != 1 || feed.queries[0].Get("t") != "movie" || feed.queries[0].Get("imdbid") != "0211915" || feed.queries[0].Has("q") {
		t.Errorf("queries = %v, want one t=movie&imdbid=0211915 without q", feed.queries)
	}
}

// The id search finding nothing falls back to text on that indexer, and a text result
// still has to be the film by title.
func TestMovieSearchFallsBackToTextAndStillMatchesTitle(t *testing.T) {
	h := newStallHarness(t)
	feed := &idTorznab{textTitles: []string{"Arrival.Of.The.Dead.2016.1080p.WEB-GRP", "Arrival.2016.1080p.BluRay.x264-GRP"}}
	srv := httptest.NewServer(feed)
	t.Cleanup(srv.Close)
	ix, err := h.c.indexers.Create(h.ctx, indexer.Indexer{Name: "ByID", Kind: indexer.KindTorznab, URL: srv.URL, Priority: 5, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.c.db.Exec(`UPDATE indexers SET caps_json = ? WHERE id = ?`,
		`{"movie":{"available":true,"params":["q","imdbid"]}}`, ix.ID); err != nil {
		t.Fatal(err)
	}
	m, err := movies.NewRepo(h.c.db).Create(h.ctx, movies.Movie{TMDBID: 329865, IMDBID: "tt2543164", Title: "Arrival", Year: 2016, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	list, err := h.c.RankReleases(h.ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Releases) != 1 || list.Releases[0].Title != "Arrival.2016.1080p.BluRay.x264-GRP" {
		t.Fatalf("rows = %+v, want only the film's own release", list.Releases)
	}
	feed.mu.Lock()
	defer feed.mu.Unlock()
	if len(feed.queries) != 2 || feed.queries[0].Get("imdbid") == "" || feed.queries[1].Get("q") != "Arrival 2016" {
		t.Errorf("queries = %v, want the id search then the text search", feed.queries)
	}
}
