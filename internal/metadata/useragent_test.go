package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/tristenlammi/arrmada/internal/httpua"
)

// TMDB, Open Library, Google Books and Hardcover say they're Arrmada, with its real version
// (they used to claim "Arrmada/1.0", or nothing).
func TestMetadataRequestsSendUserAgent(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.UserAgent()
		mu.Unlock()
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()
	ctx := context.Background()

	tm := NewTMDB("tmdbkey")
	tm.base = srv.URL + "/tmdb"
	if _, err := tm.get(ctx, "/configuration", url.Values{}); err != nil {
		t.Fatal(err)
	}
	ol := NewOpenLibrary()
	ol.base = srv.URL + "/ol"
	if _, err := ol.get(ctx, "/search.json", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := NewGoogleBooks().fetch(ctx, srv.URL+"/gb"); err != nil {
		t.Fatal(err)
	}
	hc := &Hardcover{key: func() string { return "hckey" }, endpoint: srv.URL + "/hc", http: &http.Client{}}
	var out map[string]any
	if _, err := hc.queryOnce(ctx, "{ me { id } }", nil, &out); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, p := range []string{"/tmdb/configuration", "/ol/search.json", "/gb", "/hc"} {
		if got, ok := seen[p]; !ok || got != httpua.UserAgent() {
			t.Errorf("%s: User-Agent %q (asked: %v), want %q", p, got, ok, httpua.UserAgent())
		}
	}
}
