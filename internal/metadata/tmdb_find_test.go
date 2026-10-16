package metadata

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A Sonarr show is named by its TVDB id; TMDB's /find turns it into the TMDB id Arrmada
// keys shows by. No answer (or only an adult one) is ErrNotFound.
func TestSeriesByTVDB(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("external_source") != "tvdb_id" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/find/81189":
			_, _ = w.Write([]byte(`{"movie_results":[],"tv_results":[{"id":1396,"name":"Breaking Bad"}]}`))
		case "/find/5":
			_, _ = w.Write([]byte(`{"tv_results":[{"id":77,"adult":true}]}`))
		default:
			_, _ = w.Write([]byte(`{"tv_results":[]}`))
		}
	}))
	defer srv.Close()
	tm := NewTMDB("k")
	tm.base = srv.URL
	ctx := context.Background()
	if id, err := tm.SeriesByTVDB(ctx, 81189); err != nil || id != 1396 {
		t.Errorf("81189 = %d, %v; want 1396", id, err)
	}
	for _, tvdb := range []int{5, 6, 0} {
		if id, err := tm.SeriesByTVDB(ctx, tvdb); !errors.Is(err, ErrNotFound) || id != 0 {
			t.Errorf("%d = %d, %v; want ErrNotFound", tvdb, id, err)
		}
	}
	if _, err := NewTMDB("").SeriesByTVDB(ctx, 81189); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("without a key: %v", err)
	}
}
