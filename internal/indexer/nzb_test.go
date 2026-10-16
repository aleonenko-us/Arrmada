package indexer

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	nzbKey  = "NZBSECRETKEY42"
	nzbBody = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE nzb PUBLIC "-//newzBin//DTD NZB 1.1//EN" "http://www.newzbin.com/DTD/nzb/nzb-1.1.dtd">
<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><file subject="x"><segments><segment number="1">a@b</segment></segments></file></nzb>`
)

// nzbIndexer is a Newznab indexer whose /getnzb answer the test chooses, plus the link a
// release from it carries (with the key in its query, as real ones do).
func nzbIndexer(t *testing.T, h http.HandlerFunc) (*Service, string) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	s, _, _ := healthService(t)
	if _, err := s.Create(context.Background(), Indexer{Name: "Geek", Kind: KindNewznab, URL: srv.URL + "/api", APIKey: nzbKey, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	return s, srv.URL + "/getnzb/abc123.nzb?i=7&r=" + nzbKey
}

// A Newznab release's NZB is downloaded on the server and returned as bytes — never as a
// link for the usenet client to fetch with the key in it.
func TestFetchNZBReturnsTheFile(t *testing.T) {
	s, link := nzbIndexer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("r") != nzbKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Disposition", `attachment; filename="Film.2021.1080p.WEB-DL.nzb"`)
		_, _ = w.Write([]byte(nzbBody))
	})
	res, err := s.Fetch(context.Background(), "Geek", link)
	if err != nil {
		t.Fatal(err)
	}
	if res.URL != "" || string(res.File) != nzbBody || res.Filename != "Film.2021.1080p.WEB-DL.nzb" {
		t.Fatalf("result = url %q, file %d bytes, name %q", res.URL, len(res.File), res.Filename)
	}
}

// A gzipped NZB is unpacked; without a Content-Disposition the name comes from the path,
// never the query.
func TestFetchNZBGzipAndName(t *testing.T) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(nzbBody))
	_ = zw.Close()
	s, link := nzbIndexer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-gzip")
		_, _ = w.Write(gz.Bytes())
	})
	res, err := s.Fetch(context.Background(), "Geek", link)
	if err != nil {
		t.Fatal(err)
	}
	if string(res.File) != nzbBody || res.Filename != "abc123.nzb" {
		t.Fatalf("file %q, name %q", res.File, res.Filename)
	}
	for _, tc := range []struct{ disp, path, want string }{
		{"", "/api", "arrmada.nzb"},
		{"", "/getnzb/x", "x.nzb"},
		{`attachment; filename="../../etc/Film.nzb.gz"`, "/api", "Film.nzb"},
		{`attachment; filename="a\\b\\Film"`, "/api", "Film.nzb"},
	} {
		req := httptest.NewRequest("GET", "http://geek"+tc.path+"?apikey="+nzbKey, nil)
		if got := nzbFilename(tc.disp, req); got != tc.want {
			t.Errorf("nzbFilename(%q, %q) = %q, want %q", tc.disp, tc.path, got, tc.want)
		}
	}
}

// Every failure is a plain sentence naming the indexer, never the link or the key.
func TestFetchNZBFailures(t *testing.T) {
	cases := []struct {
		name string
		h    http.HandlerFunc
		want string
	}{
		{"newznab error: bad key", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`<?xml version="1.0"?><error code="100" description="Incorrect user credentials"/>`))
		}, "Geek refused the API key (Incorrect user credentials)"},
		{"newznab error: download limit", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`<error code="501" description="Download limit reached"/>`))
		}, "Geek says its API or download limit is reached"},
		{"newznab error: item gone", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`<error code="300" description="No such item"/>`))
		}, "Geek no longer has this NZB"},
		{"newznab error quoting the key", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`<error code="900" description="Unknown request for key ` + nzbKey + `"/>`))
		}, "Geek couldn't send the NZB: Unknown request for key REDACTED"},
		{"HTTP 429", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
		}, "Geek says its API or download limit is reached"},
		{"HTTP 429 with an error document", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`<error code="429" description="Request limit reached"/>`))
		}, "Geek says its API or download limit is reached"},
		{"HTTP 503", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}, "Geek couldn't send the NZB (HTTP 503)"},
		{"oversize", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("<nzb>"))
			chunk := bytes.Repeat([]byte("x"), 1<<20)
			for i := 0; i < 65; i++ {
				if _, err := w.Write(chunk); err != nil {
					return
				}
			}
		}, "Geek: the indexer sent something larger than 64 MB instead of an NZB"},
		// A web page where the NZB should be is a block (blocked_test.go), not a bad file.
		{"a web page", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`<!doctype html><html><body>Log in</body></html>`))
		}, "Geek blocked Arrmada's request (HTTP 200, a web page instead of an NZB)"},
		{"a bare 401", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}, "Geek refused the API key"},
		{"a torrent", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("d4:infod4:name5:alphaee"))
		}, "Geek: the indexer didn't send an NZB file"},
		{"empty", func(w http.ResponseWriter, r *http.Request) {}, "Geek: the indexer didn't send an NZB file"},
	}
	for _, tc := range cases {
		s, link := nzbIndexer(t, tc.h)
		res, err := s.Fetch(context.Background(), "Geek", link)
		if err == nil {
			t.Errorf("%s: no error, got %d bytes / url %q", tc.name, len(res.File), res.URL)
			continue
		}
		if !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("%s: error %q, want it to start %q", tc.name, err, tc.want)
		}
		if strings.Contains(err.Error(), nzbKey) || strings.Contains(err.Error(), "getnzb") {
			t.Errorf("%s: error carries the link or key: %q", tc.name, err)
		}
	}
}

// An unreachable indexer: Go's error quotes the whole request URL, which is cut out.
func TestFetchNZBTransportErrorHidesTheLink(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	dead := srv.URL
	srv.Close()
	s, _, _ := healthService(t)
	if _, err := s.Create(context.Background(), Indexer{Name: "Geek", Kind: KindNewznab, URL: dead + "/api", APIKey: nzbKey, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Fetch(context.Background(), "Geek", dead+"/api?t=get&id=1&apikey="+nzbKey)
	if err == nil || !strings.HasPrefix(err.Error(), "couldn't download the NZB from Geek") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), nzbKey) || strings.Contains(err.Error(), "t=get") {
		t.Fatalf("error carries the link or key: %q", err)
	}
}

// An NZB download the indexer refuses for its own reasons (a download limit, with its
// Retry-After) counts against the indexer, so background searches back off it; one about
// the release (gone) doesn't; a delivered NZB is the indexer answering.
func TestFetchNZBRecordsIndexerStatus(t *testing.T) {
	var limited atomic.Bool
	limited.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "gone"):
			w.WriteHeader(http.StatusNotFound)
		case limited.Load():
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			_, _ = w.Write([]byte(nzbBody))
		}
	}))
	defer srv.Close()
	s, _, _ := healthService(t)
	idx, err := s.Create(context.Background(), Indexer{Name: "Geek", Kind: KindNewznab, URL: srv.URL + "/api", APIKey: nzbKey, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if _, err := s.Fetch(ctx, "Geek", srv.URL+"/getnzb/gone.nzb?r="+nzbKey); err == nil {
		t.Fatal("a gone NZB was fetched")
	}
	if st, _, ok := s.Status(idx.ID); ok && st.ConsecutiveFailures > 0 {
		t.Errorf("a gone release counted against the indexer: %+v", st)
	}

	if _, err := s.Fetch(ctx, "Geek", srv.URL+"/getnzb/a.nzb?r="+nzbKey); err == nil {
		t.Fatal("a limited indexer's NZB was fetched")
	}
	st, _, ok := s.Status(idx.ID)
	if !ok || st.ConsecutiveFailures != 1 || time.Until(st.BackoffUntil) < 30*time.Minute ||
		!strings.Contains(st.LastError, "limit") || strings.Contains(st.LastError, nzbKey) {
		t.Errorf("after a download limit: %+v", st)
	}

	limited.Store(false)
	if _, err := s.Fetch(ctx, "Geek", srv.URL+"/getnzb/a.nzb?r="+nzbKey); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := s.Status(idx.ID); st.ConsecutiveFailures != 0 || st.LastOKAt.IsZero() {
		t.Errorf("after a delivered NZB: %+v", st)
	}
}
