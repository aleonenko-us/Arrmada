package automation

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/movies"
)

// releaseIsForMovie is the title check on its own: a release name against the movie's own,
// original and alternative titles (no collision guard), year within one.
func releaseIsForMovie(relTitle string, m movies.Movie) bool {
	return movieReleaseMatches(indexer.Release{Title: relTitle}, m, movies.TitleKeys(m, nil))
}

// INT-24 / MOV-24: a release named by the film's original or alternative title is for it;
// the year check still keeps a same-titled film from another year out.
func TestReleaseIsForMovieAltTitles(t *testing.T) {
	leon := movies.Movie{ID: 1, Title: "Léon: The Professional", Year: 1994, Extra: &movies.MovieExtra{
		OriginalTitle: "Léon",
		AltTitles:     []string{"The Professional", "Leon the Professional", "Up", "レオン"},
	}}
	cases := []struct {
		rel  string
		want bool
	}{
		{"Leon.The.Professional.1994.1080p.BluRay.x264-GRP", true},
		{"Leon.1994.Extended.1080p.BluRay.x264-GRP", true}, // the original title
		{"The.Professional.1994.720p.BluRay-GRP", true},    // an alternative title
		{"The.Professional.1981.1080p.BluRay-GRP", false},  // another film by that name
		{"Leon.2026.1080p.WEB-DL-GRP", false},              // a remake: the year check
		{"Up.1994.1080p.WEB-GRP", false},                   // too short to stand for the film
		{"The.Professionals.1994.1080p.BluRay-GRP", false}, // a different title
	}
	for _, c := range cases {
		if got := releaseIsForMovie(c.rel, leon); got != c.want {
			t.Errorf("releaseIsForMovie(%q) = %v, want %v", c.rel, got, c.want)
		}
	}
	// A movie read before alternative titles were kept still matches its own title.
	old := movies.Movie{Title: "Léon: The Professional", Year: 1994}
	if !releaseIsForMovie("Leon.The.Professional.1994.1080p-GRP", old) || releaseIsForMovie("Leon.1994.1080p-GRP", old) {
		t.Error("without alt titles only the movie's own title matches")
	}
}

// An alternative title that is another library film's own title never takes that film's
// releases: neither on the search side nor in the queue.
func TestMovieKeysCollisionGuard(t *testing.T) {
	h := newStallHarness(t)
	repo := movies.NewRepo(h.c.db)
	dune, err := repo.Create(h.ctx, movies.Movie{TMDBID: 438631, Title: "Dune", Year: 2021, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	two, err := repo.Create(h.ctx, movies.Movie{TMDBID: 693134, Title: "Dune: Part Two", Year: 2021, Monitored: true,
		Extra: &movies.MovieExtra{AltTitles: []string{"Dune", "Dune Part 2"}, TitlesRead: true}})
	if err != nil {
		t.Fatal(err)
	}
	keys := h.c.movieKeys(h.ctx, two)
	rel := indexer.Release{Title: "Dune.2021.2160p.WEB-DL.DV-GRP"}
	if movieReleaseMatches(rel, two, keys) {
		t.Errorf("keys %v: Dune (2021)'s release matched Dune: Part Two", keys)
	}
	if !movieReleaseMatches(indexer.Release{Title: "Dune.Part.2.2021.1080p-GRP"}, two, keys) {
		t.Errorf("keys %v: its own alternative title must still match", keys)
	}
	if !movieReleaseMatches(rel, dune, h.c.movieKeys(h.ctx, dune)) {
		t.Error("Dune's own release must match Dune")
	}
}

// queryTorznab answers by query: a release under the original title only for a q naming
// it, and an unrelated film otherwise. It records every q.
type queryTorznab struct {
	mu      sync.Mutex
	byQuery map[string][]string // q substring → titles
	queries []string
}

func (f *queryTorznab) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>`)
	if r.URL.Query().Get("offset") == "" {
		q := r.URL.Query().Get("q")
		f.queries = append(f.queries, q)
		for sub, titles := range f.byQuery {
			if !strings.Contains(q, sub) {
				continue
			}
			for _, title := range titles {
				fmt.Fprintf(&b, `<item><title>%s</title><link>%s</link><size>%d</size><torznab:attr name="seeders" value="25"/></item>`,
					html.EscapeString(title), html.EscapeString(magnetFor(title)), int64(4<<30))
			}
		}
	}
	b.WriteString(`</channel></rss>`)
	fmt.Fprint(w, b.String())
}

func (f *queryTorznab) asked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.queries...)
}

// The original-title query runs only when the primary search finds nothing for the film,
// and finds what the primary couldn't.
func TestOriginalTitleQueryOnlyWhenPrimaryFindsNothing(t *testing.T) {
	const original = "Le.Fabuleux.Destin.d.Amelie.Poulain.2001.1080p.BluRay.x264-GRP"
	extra := &movies.MovieExtra{OriginalTitle: "Le Fabuleux Destin d'Amélie Poulain", TitlesRead: true}

	t.Run("primary finds nothing", func(t *testing.T) {
		h := newStallHarness(t)
		feed := &queryTorznab{byQuery: map[string][]string{
			"Amelie 2001": {"Amelie.Of.Montmartre.2001.720p-GRP"}, // another film entirely
			"Fabuleux":    {original},
		}}
		srv := httptest.NewServer(feed)
		t.Cleanup(srv.Close)
		if _, err := h.c.indexers.Create(h.ctx, indexer.Indexer{Name: "Q", Kind: indexer.KindTorznab, URL: srv.URL, Priority: 5, Enabled: true}); err != nil {
			t.Fatal(err)
		}
		m, err := movies.NewRepo(h.c.db).Create(h.ctx, movies.Movie{TMDBID: 194, Title: "Amélie", Year: 2001, Monitored: true, Extra: extra})
		if err != nil {
			t.Fatal(err)
		}
		out, err := h.c.SearchMovie(h.ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if out.Grabbed != 1 {
			t.Fatalf("outcome %+v, want the original-title release grabbed", out)
		}
		if got := feed.asked(); len(got) != 2 || got[1] != "Le Fabuleux Destin d'Amelie Poulain 2001" {
			t.Errorf("queries = %q, want the title query then one original-title query", got)
		}
	})

	t.Run("primary finds the film", func(t *testing.T) {
		h := newStallHarness(t)
		feed := &queryTorznab{byQuery: map[string][]string{
			"Amelie 2001": {"Amelie.2001.1080p.BluRay.x264-GRP"},
			"Fabuleux":    {original},
		}}
		srv := httptest.NewServer(feed)
		t.Cleanup(srv.Close)
		if _, err := h.c.indexers.Create(h.ctx, indexer.Indexer{Name: "Q", Kind: indexer.KindTorznab, URL: srv.URL, Priority: 5, Enabled: true}); err != nil {
			t.Fatal(err)
		}
		m, err := movies.NewRepo(h.c.db).Create(h.ctx, movies.Movie{TMDBID: 194, Title: "Amélie", Year: 2001, Monitored: true, Extra: extra})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.c.RankReleases(h.ctx, m.ID); err != nil {
			t.Fatal(err)
		}
		if got := feed.asked(); len(got) != 1 {
			t.Errorf("queries = %q, want only the title query", got)
		}
	})
}

// MOV-24: a download grabbed under the film's original title imports to it instead of
// being held as "looks like another film"; one named for a different film still is.
func TestHoldMovieImportAcceptsOriginalTitle(t *testing.T) {
	h := newStallHarness(t)
	m, err := movies.NewRepo(h.c.db).Create(h.ctx, movies.Movie{TMDBID: 194, Title: "Amélie", Year: 2001, Monitored: true,
		Extra: &movies.MovieExtra{OriginalTitle: "Le Fabuleux Destin d'Amélie Poulain", TitlesRead: true}})
	if err != nil {
		t.Fatal(err)
	}
	const name = "Le.Fabuleux.Destin.d.Amelie.Poulain.2001.1080p.BluRay.x264-GRP"
	addGrab(t, h.c, "movie", m.ID, name, hashFor(name))
	if reason, hold := h.c.HoldMovieImport(h.ctx, hashFor(name), name, t.TempDir()); hold {
		t.Errorf("held the film's own original-title download: %s", reason)
	}
	const other = "Amelie.Of.Montmartre.2001.1080p-GRP"
	addGrab(t, h.c, "movie", m.ID, other, hashFor(other))
	if _, hold := h.c.HoldMovieImport(h.ctx, hashFor(other), other, t.TempDir()); !hold {
		t.Error("a download for another film must still be held")
	}
}

func TestOriginalTitleQuery(t *testing.T) {
	m := movies.Movie{Title: "Amélie", Year: 2001, Extra: &movies.MovieExtra{OriginalTitle: "Le Fabuleux Destin d'Amélie Poulain"}}
	if got := originalTitleQuery(m, movies.TitleKeys(m, nil)); got != "Le Fabuleux Destin d'Amélie Poulain 2001" {
		t.Errorf("originalTitleQuery = %q", got)
	}
	same := movies.Movie{Title: "Arrival", Year: 2016, Extra: &movies.MovieExtra{OriginalTitle: "Arrival"}}
	if got := originalTitleQuery(same, movies.TitleKeys(same, nil)); got != "" {
		t.Errorf("an original title the same as the title is no query, got %q", got)
	}
	kana := movies.Movie{Title: "Spirited Away", Year: 2001, Extra: &movies.MovieExtra{OriginalTitle: "千と千尋の神隠し"}}
	if got := originalTitleQuery(kana, movies.TitleKeys(kana, nil)); got != "" {
		t.Errorf("a non-Latin original title is no query, got %q", got)
	}
}
