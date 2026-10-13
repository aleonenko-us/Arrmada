package httpapi

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The library lists are megabytes of JSON for a big library; they go out gzipped to a
// browser that accepts it. Everything else (small answers, files, writes, the websocket)
// is left exactly as it was.
func TestCompressJSON(t *testing.T) {
	big := `{"movies":[` + strings.Repeat(`{"id":1,"title":"Dune"},`, 200) + `{}]}`
	a := &api{}
	serve := func(method, path, ae string, h http.HandlerFunc) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if ae != "" {
			r.Header.Set("Accept-Encoding", ae)
		}
		rec := httptest.NewRecorder()
		a.compressJSON(h).ServeHTTP(rec, r)
		return rec
	}
	jsonOf := func(status int, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			// What writeJSON does: the type, the status, then the whole body in one write.
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body+"\n")
		}
	}
	gunzip := func(t *testing.T, rec *httptest.ResponseRecorder) string {
		t.Helper()
		zr, err := gzip.NewReader(rec.Body)
		if err != nil {
			t.Fatalf("not gzip: %v", err)
		}
		b, _ := io.ReadAll(zr)
		return string(b)
	}

	t.Run("large JSON is gzipped, status and body intact", func(t *testing.T) {
		for _, status := range []int{http.StatusOK, http.StatusInternalServerError} {
			rec := serve("GET", "/api/v1/movies", "gzip, deflate, br", jsonOf(status, big))
			if rec.Code != status || rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Vary") != "Accept-Encoding" {
				t.Fatalf("status %d, headers %v", rec.Code, rec.Header())
			}
			if got := gunzip(t, rec); got != big+"\n" {
				t.Fatalf("body changed: %q", got[:40])
			}
		}
	})

	plain := []struct {
		name, method, path, ae string
		h                      http.HandlerFunc
	}{
		{"small JSON", "GET", "/api/v1/status", "gzip", jsonOf(200, `{"ok":true}`)},
		{"no Accept-Encoding", "GET", "/api/v1/movies", "", jsonOf(200, big)},
		{"gzip refused", "GET", "/api/v1/movies", "gzip;q=0, br", jsonOf(200, big)},
		{"a write", "POST", "/api/v1/movies", "gzip", jsonOf(200, big)},
		{"not the API", "GET", "/opds/books", "gzip", jsonOf(200, big)},
		{"the websocket", "GET", "/api/v1/ws", "gzip", jsonOf(200, big)},
		{"a file", "GET", "/api/v1/books/1/audiobook", "gzip", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "audio/mp4")
			_, _ = w.Write([]byte(big))
		}},
		{"already encoded", "GET", "/api/v1/system/backups/x", "gzip", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "identity")
			_, _ = w.Write([]byte(big))
		}},
	}
	for _, tc := range plain {
		t.Run(tc.name+" passes through", func(t *testing.T) {
			rec := serve(tc.method, tc.path, tc.ae, tc.h)
			if enc := rec.Header().Get("Content-Encoding"); enc == "gzip" {
				t.Fatalf("compressed")
			}
			if !strings.HasPrefix(rec.Body.String(), `{`) {
				t.Fatalf("body changed: %q", rec.Body.String())
			}
		})
	}

	t.Run("a status with no body still goes out", func(t *testing.T) {
		rec := serve("GET", "/api/v1/movies", "gzip", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
		if rec.Code != http.StatusNoContent || rec.Header().Get("Content-Encoding") != "" || rec.Body.Len() != 0 {
			t.Fatalf("got %d %v %q", rec.Code, rec.Header(), rec.Body.String())
		}
	})

	t.Run("a flush mid-answer sends the status", func(t *testing.T) {
		rec := serve("GET", "/api/v1/movies", "gzip", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusAccepted)
			http.NewResponseController(w).Flush()
		})
		if rec.Code != http.StatusAccepted || !rec.Flushed {
			t.Fatalf("got %d flushed=%v", rec.Code, rec.Flushed)
		}
	})
}

func TestAcceptsGzip(t *testing.T) {
	for h, want := range map[string]bool{
		"gzip": true, "gzip, deflate, br, zstd": true, "br;q=1.0, gzip;q=0.8": true, "GZIP": true,
		"": false, "br": false, "gzip;q=0": false, "identity": false,
	} {
		if got := acceptsGzip(h); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", h, got, want)
		}
	}
}
