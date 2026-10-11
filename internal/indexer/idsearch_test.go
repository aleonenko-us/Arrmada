package indexer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// capsJSON stores caps on an indexer the way a Test or a Prowlarr sync leaves them.
func capsJSON(t *testing.T, c Caps) string {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestBuildURLIDParams(t *testing.T) {
	movieCaps := Caps{Movie: SearchMode{Available: true, Params: []string{"q", "imdbid", "tmdbid"}}}
	tvCaps := Caps{TV: SearchMode{Available: true, Params: []string{"q", "tvdbid", "season", "ep"}}}
	cases := []struct {
		name    string
		caps    *Caps
		q       SearchQuery
		want    map[string]string // param → value ("" = must be absent)
		wantURL string            // when set, the whole URL must equal buildURL of this query without ids
	}{
		{
			name: "imdbid with tt stripped and no q",
			caps: &movieCaps,
			q:    SearchQuery{Text: "Leon 1994", MediaType: MediaMovie, IMDBID: "tt0110413", TMDBID: 101},
			want: map[string]string{"t": "movie", "imdbid": "0110413", "q": "", "tmdbid": ""},
		},
		{
			name: "tmdbid when imdbid isn't listed",
			caps: &Caps{Movie: SearchMode{Available: true, Params: []string{"q", "tmdbid"}}},
			q:    SearchQuery{Text: "Leon 1994", MediaType: MediaMovie, IMDBID: "tt0110413", TMDBID: 101},
			want: map[string]string{"t": "movie", "tmdbid": "101", "q": "", "imdbid": ""},
		},
		{
			name: "caps without id params search by text",
			caps: &Caps{Movie: SearchMode{Available: true, Params: []string{"q"}}},
			q:    SearchQuery{Text: "Leon 1994", MediaType: MediaMovie, IMDBID: "tt0110413"},
			want: map[string]string{"t": "movie", "q": "Leon 1994", "imdbid": ""},
		},
		{
			name: "a listed param on an unavailable mode is not used",
			caps: &Caps{Movie: SearchMode{Available: false, Params: []string{"q", "imdbid"}}},
			q:    SearchQuery{Text: "Leon 1994", MediaType: MediaMovie, IMDBID: "tt0110413"},
			want: map[string]string{"q": "Leon 1994", "imdbid": ""},
		},
		{
			name: "tvdbid with season and episode",
			caps: &tvCaps,
			q:    SearchQuery{Text: "The Wire", MediaType: MediaSeries, TVDBID: 79126, Season: 2, Episode: 5},
			want: map[string]string{"t": "tvsearch", "tvdbid": "79126", "season": "2", "ep": "5", "q": ""},
		},
		{
			name: "movie caps don't apply to a TV search",
			caps: &movieCaps,
			q:    SearchQuery{Text: "The Wire", MediaType: MediaSeries, IMDBID: "tt0306414", TVDBID: 79126},
			want: map[string]string{"t": "tvsearch", "q": "The Wire", "imdbid": "", "tvdbid": ""},
		},
		{
			name: "a book search never sends ids",
			caps: &Caps{Search: SearchMode{Available: true, Params: []string{"q", "imdbid"}}, Movie: movieCaps.Movie},
			q:    SearchQuery{Text: "Dune", MediaType: MediaBook, IMDBID: "tt0110413"},
			want: map[string]string{"t": "search", "q": "Dune", "imdbid": ""},
		},
		{
			name:    "no stored caps builds exactly today's URL",
			q:       SearchQuery{Text: "Leon 1994", MediaType: MediaMovie, IMDBID: "tt0110413", TMDBID: 101, Limit: 100},
			wantURL: "same",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			idx := Indexer{URL: "http://prowlarr:9696/3/api", APIKey: "k", Kind: KindTorznab}
			if c.caps != nil {
				idx.CapsJSON = capsJSON(t, *c.caps)
			}
			raw, err := buildURL(idx, "search", c.q)
			if err != nil {
				t.Fatal(err)
			}
			if c.wantURL != "" {
				today, _ := buildURL(idx, "search", c.q.withoutIDs())
				if raw != today {
					t.Fatalf("url = %s, want today's %s", raw, today)
				}
				return
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

// idFeed answers an id search with idTitles and a text search with textTitles, counting
// both.
type idFeed struct {
	mu          sync.Mutex
	idTitles    []string
	textTitles  []string
	idStatus    int // non-zero: answer id searches with this HTTP status
	idError     bool
	idCalls     int
	textCalls   int
	lastIDQuery url.Values
}

func (f *idFeed) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := r.URL.Query()
	if q.Get("offset") != "" {
		fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel></channel></rss>`) // the one page is all there is
		return
	}
	titles := f.textTitles
	if q.Get("imdbid") != "" || q.Get("tmdbid") != "" || q.Get("tvdbid") != "" {
		f.idCalls++
		f.lastIDQuery = q
		if f.idStatus != 0 {
			http.Error(w, "nope", f.idStatus)
			return
		}
		if f.idError {
			fmt.Fprint(w, `<?xml version="1.0"?><error code="201" description="Incorrect parameter"/>`)
			return
		}
		titles = f.idTitles
	} else {
		f.textCalls++
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>`)
	for i, title := range titles {
		fmt.Fprintf(&b, `<item><title>%s</title><link>http://x/%d</link><size>100</size><torznab:attr name="imdb" value="0110413"/></item>`, title, i)
	}
	b.WriteString(`</channel></rss>`)
	fmt.Fprint(w, b.String())
}

func idIndexer(t *testing.T, srvURL string) Indexer {
	return Indexer{Name: "p", Kind: KindTorznab, URL: srvURL + "/api", APIKey: "k",
		CapsJSON: capsJSON(t, Caps{Movie: SearchMode{Available: true, Params: []string{"q", "imdbid"}}})}
}

var leonQuery = SearchQuery{Text: "Leon 1994", MediaType: MediaMovie, IMDBID: "tt0110413", Limit: 100}

func TestSearchByIDMarksReleases(t *testing.T) {
	feed := &idFeed{idTitles: []string{"Leon.The.Professional.1994.1080p.BluRay.x264-GRP"}}
	srv := httptest.NewServer(feed)
	defer srv.Close()

	got, err := NewTorznabSearcher().Search(context.Background(), idIndexer(t, srv.URL), leonQuery)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].MatchedByID || got[0].IMDBID != "tt0110413" {
		t.Fatalf("releases = %+v, want one marked MatchedByID with its imdb tag", got)
	}
	if feed.idCalls != 1 || feed.textCalls != 0 {
		t.Errorf("id calls %d, text calls %d; want 1 and 0", feed.idCalls, feed.textCalls)
	}
	if feed.lastIDQuery.Get("t") != "movie" || feed.lastIDQuery.Get("imdbid") != "0110413" || feed.lastIDQuery.Has("q") {
		t.Errorf("id query = %v, want t=movie&imdbid=0110413 and no q", feed.lastIDQuery)
	}
}

func TestSearchFallsBackToTextWhenIDFindsNothing(t *testing.T) {
	feed := &idFeed{textTitles: []string{"Leon.1994.720p.WEB-GRP"}}
	srv := httptest.NewServer(feed)
	defer srv.Close()

	got, err := NewTorznabSearcher().Search(context.Background(), idIndexer(t, srv.URL), leonQuery)
	if err != nil {
		t.Fatal(err)
	}
	if feed.idCalls != 1 || feed.textCalls != 1 {
		t.Fatalf("id calls %d, text calls %d; want one of each", feed.idCalls, feed.textCalls)
	}
	if len(got) != 1 || got[0].MatchedByID {
		t.Fatalf("releases = %+v, want the text result, not marked MatchedByID", got)
	}
}

// The indexer's own <error> for the id is a refusal of the parameter: the text search
// still runs. An HTTP failure is the indexer struggling: no second request.
func TestSearchIDFailures(t *testing.T) {
	refused := &idFeed{idError: true, textTitles: []string{"Leon.1994.720p.WEB-GRP"}}
	srv := httptest.NewServer(refused)
	defer srv.Close()
	got, err := NewTorznabSearcher().Search(context.Background(), idIndexer(t, srv.URL), leonQuery)
	if err != nil || len(got) != 1 || refused.textCalls != 1 {
		t.Fatalf("refused id: got %d releases, err %v, text calls %d; want the text result", len(got), err, refused.textCalls)
	}

	down := &idFeed{idStatus: http.StatusBadGateway, textTitles: []string{"x"}}
	srv2 := httptest.NewServer(down)
	defer srv2.Close()
	if _, err := NewTorznabSearcher().Search(context.Background(), idIndexer(t, srv2.URL), leonQuery); err == nil {
		t.Fatal("an HTTP failure on the id search must be the search's error")
	}
	if down.textCalls != 0 {
		t.Errorf("text calls = %d after an HTTP failure, want 0", down.textCalls)
	}
}

func TestParseFeedReadsIDs(t *testing.T) {
	rels, err := ParseFeed([]byte(`<?xml version="1.0"?><rss xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>
		<item><title>A</title><torznab:attr name="imdb" value="110413"/><torznab:attr name="tmdbid" value="101"/></item>
		<item><title>B</title><torznab:attr name="imdbid" value="tt0306414"/><torznab:attr name="tvdbid" value="79126"/></item>
		<item><title>C</title><torznab:attr name="imdb" value="0"/></item>
	</channel></rss>`))
	if err != nil {
		t.Fatal(err)
	}
	if rels[0].IMDBID != "tt0110413" || rels[0].TMDBID != 101 {
		t.Errorf("A = %+v", rels[0])
	}
	if rels[1].IMDBID != "tt0306414" || rels[1].TVDBID != 79126 {
		t.Errorf("B = %+v", rels[1])
	}
	if rels[2].IMDBID != "" {
		t.Errorf("an imdb of 0 is no id, got %q", rels[2].IMDBID)
	}
}

func TestNormalizeIMDB(t *testing.T) {
	for in, want := range map[string]string{
		"tt0111161": "tt0111161", "0111161": "tt0111161", "111161": "tt0111161",
		"TT12345678": "tt12345678", "": "", "0": "", "tt": "", "abc": "", "tt-5": "",
	} {
		if got := NormalizeIMDB(in); got != want {
			t.Errorf("NormalizeIMDB(%q) = %q, want %q", in, got, want)
		}
	}
	if !SameIMDB("tt0111161", "111161") || SameIMDB("", "") || SameIMDB("tt1", "tt2") {
		t.Error("SameIMDB compares the numbers and never matches unknown ids")
	}
}
