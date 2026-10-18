package httpua

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// N5: a request carrying a key never follows a redirect to another server; one on the
// same server, or a request with no key (a CDN download), does.
func TestNoCredentialRedirects(t *testing.T) {
	var elsewhereHits atomic.Int32
	var sawKey atomic.Value
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhereHits.Add(1)
		sawKey.Store(r.Header.Get("X-Api-Key") + r.Header.Get("X-Plex-Token") + r.Header.Get("Api-Key"))
	}))
	defer elsewhere.Close()
	// 127.0.0.1 and localhost are different hosts as far as a redirect goes.
	other := strings.Replace(elsewhere.URL, "127.0.0.1", "localhost", 1)
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/away":
			http.Redirect(w, r, other+"/stolen", http.StatusFound)
		case "/here":
			http.Redirect(w, r, "/landed", http.StatusFound)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer service.Close()
	c := KeyedClient(0)

	for _, h := range []string{"X-Api-Key", "X-Plex-Token", "Api-Key"} {
		req, _ := http.NewRequest("GET", service.URL+"/away", nil)
		req.Header.Set(h, "secret")
		if _, err := c.Do(req); err == nil || !strings.Contains(err.Error(), "refused a redirect") {
			t.Fatalf("%s: cross-host redirect followed (err %v)", h, err)
		}
	}
	if elsewhereHits.Load() != 0 {
		t.Fatalf("the other server was reached %d times", elsewhereHits.Load())
	}

	req, _ := http.NewRequest("GET", service.URL+"/here", nil)
	req.Header.Set("X-Api-Key", "secret")
	resp, err := c.Do(req)
	if err != nil || resp.Request.URL.Path != "/landed" {
		t.Fatalf("same-host redirect: %v", err)
	}
	resp.Body.Close()

	req, _ = http.NewRequest("GET", service.URL+"/away", nil)
	resp, err = c.Do(req)
	if err != nil || elsewhereHits.Load() != 1 || sawKey.Load() != "" {
		t.Fatalf("keyless redirect: %v, hits %d", err, elsewhereHits.Load())
	}
	resp.Body.Close()
}
