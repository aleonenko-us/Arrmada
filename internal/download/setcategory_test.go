package download

import (
	"context"
	"net/http"
	"sync"
	"testing"
)

// SetCategory posts the hashes and category to setCategory. qBittorrent answers 409 for a
// category it doesn't have yet (Arrmada's are created by the first download filed under
// each), so that one is created and the move retried once.
func TestQBittorrentSetCategory(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	created := false
	var logins int32
	srv := qbitTestServer(t, &logins, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("form: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/v2/torrents/setCategory":
			calls = append(calls, "set "+r.FormValue("hashes")+" "+r.FormValue("category"))
			if !created {
				w.WriteHeader(http.StatusConflict)
				return
			}
		case "/api/v2/torrents/createCategory":
			calls = append(calls, "create "+r.FormValue("category"))
			created = true
		default:
			http.NotFound(w, r)
		}
	})
	q := NewQBittorrent()
	dc := Client{ID: 1, URL: srv.URL}
	if err := q.SetCategory(context.Background(), dc, "abc123", "arrmada-tv"); err != nil {
		t.Fatalf("SetCategory: %v", err)
	}
	want := []string{"set abc123 arrmada-tv", "create arrmada-tv", "set abc123 arrmada-tv"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %q, want %q", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, calls[i], want[i])
		}
	}
	// Once it exists, one call does it.
	calls = nil
	if err := q.SetCategory(context.Background(), dc, "abc123", "arrmada-tv"); err != nil || len(calls) != 1 {
		t.Errorf("second SetCategory: err %v calls %q", err, calls)
	}
}
