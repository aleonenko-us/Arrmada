package indexer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

// INT-23: book and music searches use t=book / t=music when the stored caps say so, and an
// indexer with caps but no categories of its own is asked for the media type's usual ones.
func TestBuildURLSearchModesAndDefaultCategories(t *testing.T) {
	general := Caps{
		Search:     SearchMode{Available: true, Params: []string{"q"}},
		Movie:      SearchMode{Available: true, Params: []string{"q", "imdbid"}},
		Book:       SearchMode{Available: true, Params: []string{"q", "author", "title"}},
		Music:      SearchMode{Available: true, Params: []string{"q", "artist", "album"}},
		Categories: []int{2000, 2040, 3000, 3030, 5000, 7000, 7020},
	}
	on := func(q SearchQuery) SearchQuery { q.defaultCats = true; return q }
	cases := []struct {
		name       string
		caps       *Caps
		categories []int // the indexer's own
		q          SearchQuery
		want       map[string]string // param → value ("" = absent)
	}{
		{
			name: "audiobook on a general tracker: cat 3030 and no edition word",
			caps: &general,
			q:    on(SearchQuery{Text: "Dune", MediaType: MediaBook, BookEdition: "audiobook"}),
			want: map[string]string{"t": "search", "cat": "3030", "q": "Dune"},
		},
		{
			name: "book with author and title uses t=book",
			caps: &general,
			q:    on(SearchQuery{Text: "Frank Herbert Dune", Author: "Frank Herbert", BookTitle: "Dune", MediaType: MediaBook, BookEdition: "ebook"}),
			want: map[string]string{"t": "book", "author": "Frank Herbert", "title": "Dune", "q": "", "cat": "7000,7020"},
		},
		{
			name: "book-search without author stays t=search",
			caps: &Caps{Book: SearchMode{Available: true, Params: []string{"q", "title"}}},
			q:    on(SearchQuery{Text: "Frank Herbert Dune", Author: "Frank Herbert", BookTitle: "Dune", MediaType: MediaBook}),
			want: map[string]string{"t": "search", "q": "Frank Herbert Dune", "author": "", "title": "", "cat": ""},
		},
		{
			name: "title-only book query on a book-search that takes title",
			caps: &general,
			q:    on(SearchQuery{Text: "Dune", BookTitle: "Dune", MediaType: MediaBook}),
			want: map[string]string{"t": "book", "title": "Dune", "author": "", "cat": "7000,7020,3030"},
		},
		{
			name: "album uses t=music with artist and album",
			caps: &general,
			q:    on(SearchQuery{Text: "Radiohead OK Computer", Artist: "Radiohead", Album: "OK Computer", MediaType: MediaMusic}),
			want: map[string]string{"t": "music", "artist": "Radiohead", "album": "OK Computer", "q": "", "cat": "3000"},
		},
		{
			name: "music without music-search stays t=search",
			caps: &Caps{Categories: []int{3000}},
			q:    on(SearchQuery{Text: "Radiohead OK Computer", Artist: "Radiohead", Album: "OK Computer", MediaType: MediaMusic}),
			want: map[string]string{"t": "search", "q": "Radiohead OK Computer", "artist": "", "cat": "3000"},
		},
		{
			name: "default categories intersect with caps",
			caps: &Caps{Categories: []int{2000, 7000}},
			q:    on(SearchQuery{Text: "x", MediaType: MediaBook}),
			want: map[string]string{"cat": "7000"},
		},
		{
			name: "nothing in common sends no cat",
			caps: &Caps{Categories: []int{100001}},
			q:    on(SearchQuery{Text: "x", MediaType: MediaMovie}),
			want: map[string]string{"cat": ""},
		},
		{
			name:       "the indexer's own categories win",
			caps:       &general,
			categories: []int{2045},
			q:          on(SearchQuery{Text: "x", MediaType: MediaMovie}),
			want:       map[string]string{"cat": "2045"},
		},
		{
			name:       "the indexer's own audiobook category keeps today's query word",
			caps:       &general,
			categories: []int{3030},
			q:          on(SearchQuery{Text: "Dune", MediaType: MediaBook, BookEdition: "audiobook"}),
			want:       map[string]string{"cat": "3030", "q": "Dune audiobook"},
		},
		{
			name: "the query's categories win",
			caps: &general,
			q:    on(SearchQuery{Text: "x", MediaType: MediaMovie, Categories: []int{2040}}),
			want: map[string]string{"cat": "2040"},
		},
		{
			name: "setting off sends no cat",
			caps: &general,
			q:    SearchQuery{Text: "x", MediaType: MediaSeries},
			want: map[string]string{"cat": ""},
		},
		{
			name: "movie and series defaults",
			caps: &general,
			q:    on(SearchQuery{Text: "x", MediaType: MediaSeries}),
			want: map[string]string{"t": "tvsearch", "cat": "5000"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			idx := Indexer{URL: "http://prowlarr:9696/3/api", APIKey: "k", Kind: KindTorznab, Categories: c.categories}
			if c.caps != nil {
				idx.CapsJSON = capsJSON(t, *c.caps)
			}
			raw, err := buildURL(idx, "search", c.q)
			if err != nil {
				t.Fatal(err)
			}
			u, _ := url.Parse(raw)
			got := u.Query()
			for k, v := range c.want {
				if got.Get(k) != v {
					t.Errorf("%s = %q, want %q (url %s)", k, got.Get(k), v, raw)
				}
			}
		})
	}
}

// No stored caps: book, music and every other search build exactly today's URL, setting
// on or not, structured parts or not.
func TestBuildURLNoCapsIsToday(t *testing.T) {
	idx := Indexer{URL: "http://prowlarr:9696/3/api", APIKey: "k", Kind: KindTorznab}
	for _, q := range []SearchQuery{
		{Text: "Frank Herbert Dune", Author: "Frank Herbert", BookTitle: "Dune", MediaType: MediaBook, BookEdition: "audiobook", Limit: 60},
		{Text: "Radiohead OK Computer", Artist: "Radiohead", Album: "OK Computer", MediaType: MediaMusic, Limit: 100},
		{Text: "Arrival 2016", MediaType: MediaMovie, Limit: 100},
	} {
		plain := SearchQuery{Text: q.Text, MediaType: q.MediaType, BookEdition: q.BookEdition, Limit: q.Limit}
		q.defaultCats = true
		got, _ := buildURL(idx, "search", q)
		want, _ := buildURL(idx, "search", plain)
		if got != want {
			t.Errorf("url = %s, want today's %s", got, want)
		}
	}
	// And today's audiobook query still carries the word.
	raw, _ := buildURL(idx, "search", SearchQuery{Text: "Dune", MediaType: MediaBook, BookEdition: "audiobook"})
	if u, _ := url.Parse(raw); u.Query().Get("q") != "Dune audiobook" || u.Query().Get("t") != "search" {
		t.Errorf("no-caps audiobook url = %s", raw)
	}
}

// The indexer_default_categories setting reaches the URL through Service.Search: on (and
// unwired) asks the usual categories, off asks none.
func TestServiceSearchDefaultCategoriesSetting(t *testing.T) {
	var mu sync.Mutex
	var cats []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		cats = append(cats, r.URL.Query().Get("cat"))
		mu.Unlock()
		fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel></channel></rss>`)
	}))
	defer srv.Close()
	s, _, ix := healthService(t, srv.URL)
	if _, err := s.repo.db.Exec(`UPDATE indexers SET caps_json = ? WHERE id = ?`, capsJSON(t, Caps{Categories: []int{2000, 7000}}), ix[0].ID); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.Search(ctx, dune); err != nil {
		t.Fatal(err)
	}
	s.SetDefaultCategories(func(context.Context) bool { return false })
	if _, err := s.Search(ctx, dune); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(cats) != 2 || cats[0] != "2000" || cats[1] != "" {
		t.Fatalf("cat per search = %q, want [2000, none]", cats)
	}
}
