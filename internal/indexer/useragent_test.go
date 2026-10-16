package indexer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/tristenlammi/arrmada/internal/httpua"
)

// Every request Arrmada makes to an indexer or Prowlarr says it's Arrmada — never Go's
// default, which Cloudflare-fronted indexers turn away as a bot.
func TestIndexerRequestsSendUserAgent(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.UserAgent()
		mu.Unlock()
		switch r.URL.Path {
		case "/api":
			if r.URL.Query().Get("t") == "caps" {
				_, _ = w.Write([]byte(`<caps><searching><search available="yes" supportedParams="q"/></searching></caps>`))
				return
			}
			_, _ = w.Write([]byte(`<rss><channel></channel></rss>`))
		case "/getnzb/a.nzb":
			_, _ = w.Write([]byte(nzbBody))
		case "/t.torrent":
			_, _ = w.Write([]byte("d4:infod4:name1:aee"))
		case "/ping":
			_, _ = w.Write([]byte(`{"status":"OK"}`))
		case "/api/v1/indexer":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	idx := Indexer{Name: "T", Kind: KindNewznab, URL: srv.URL + "/api", APIKey: "k"}

	ts := NewTorznabSearcher()
	if _, err := ts.TestCaps(ctx, idx); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Search(ctx, idx, SearchQuery{Text: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fetchNZBPayload(ctx, idx, srv.URL+"/getnzb/a.nzb", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := fetchTorrentPayload(ctx, srv.URL+"/t.torrent"); err != nil {
		t.Fatal(err)
	}
	if !ProwlarrReachable(ctx, srv.URL) {
		t.Fatal("ping failed")
	}
	if _, err := prowlarrDo(ctx, srv.URL, "k", http.MethodGet, "/api/v1/indexer", nil); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, p := range []string{"/api", "/getnzb/a.nzb", "/t.torrent", "/ping", "/api/v1/indexer"} {
		if got, ok := seen[p]; !ok || got != httpua.UserAgent() {
			t.Errorf("%s: User-Agent %q (asked: %v), want %q", p, got, ok, httpua.UserAgent())
		}
	}
}
