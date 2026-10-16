package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/download"
)

const (
	handoffProwlarrKey = "PROWLARRAPIKEY0123456789"
	handoffFinderKey   = "FINDERDOWNLOADKEY987654321"
	handoffSABKey      = "0f1e2d3c4b5a69788796a5b4c3d2e1f0"
)

// syncBuffer is a log buffer safe for the server goroutines that write to it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// sabAPI is a fake SABnzbd API: it has Arrmada's categories, records every command's form,
// and answers addurl with a job id.
type sabAPI struct {
	mu    sync.Mutex
	calls []url.Values
	fail  bool // answer addurl with a refusal
}

func (s *sabAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseMultipartForm(32 << 20)
	form := r.PostForm
	if r.MultipartForm != nil {
		form = url.Values(r.MultipartForm.Value)
	}
	s.mu.Lock()
	s.calls = append(s.calls, form)
	fail := s.fail
	s.mu.Unlock()
	if form.Get("apikey") != handoffSABKey {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("API Key Incorrect"))
		return
	}
	switch form.Get("mode") {
	case "get_cats":
		b, _ := json.Marshal(map[string]any{"categories": []string{"*", download.DefaultMovieCategory, download.CategoryTV, download.CategoryBooks, download.CategoryMusic}})
		_, _ = w.Write(b)
	case "addurl":
		if fail {
			_, _ = w.Write([]byte(`{"status": false, "error": "cannot add ` + form.Get("name") + `"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status": true, "nzo_ids": ["SABnzbd_nzo_HandOff1"]}`))
	case "addfile":
		_, _ = w.Write([]byte(`{"status": true, "nzo_ids": ["SABnzbd_nzo_File1"]}`))
	case "queue":
		_, _ = w.Write([]byte(`{"queue":{"paused":false,"slots":[]}}`))
	case "history":
		_, _ = w.Write([]byte(`{"history":{"slots":[]}}`))
	default:
		_, _ = w.Write([]byte(`{"status": true}`))
	}
}

func (s *sabAPI) adds() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []url.Values
	for _, c := range s.calls {
		if m := c.Get("mode"); m == "addurl" || m == "addfile" {
			out = append(out, c)
		}
	}
	return out
}

// handoffRig is the usenet harness with the real SABnzbd client in front of a fake SABnzbd
// API, the real indexer fetch, a Prowlarr that redirects to the indexer's own link, and the
// indexer behind a Cloudflare-like gate whose answer to Arrmada each test picks.
type handoffRig struct {
	*usenetHarness
	sab        *sabAPI
	gate       http.HandlerFunc
	gateMu     sync.Mutex
	finderLink string
	link       string // what the release carries: Prowlarr's download link
	log        *syncBuffer
}

func newHandoffRig(t *testing.T) *handoffRig {
	t.Helper()
	h := newUsenetHarness(t)
	h.c.fetchRelease = nil // the real indexer fetch
	r := &handoffRig{usenetHarness: h, sab: &sabAPI{}, log: &syncBuffer{}}
	h.c.log = slog.New(slog.NewTextHandler(r.log, nil))

	sabSrv := httptest.NewServer(r.sab)
	t.Cleanup(sabSrv.Close)
	h.c.downloads.SetDownloader(download.KindSABnzbd, download.NewSABnzbd())
	cl, err := h.c.downloads.Get(h.ctx, h.sabID)
	if err != nil {
		t.Fatal(err)
	}
	cl.URL, cl.Password = sabSrv.URL, handoffSABKey
	if _, err := h.c.downloads.Update(h.ctx, cl); err != nil {
		t.Fatal(err)
	}

	gate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.gateMu.Lock()
		g := r.gate
		r.gateMu.Unlock()
		g(w, req)
	}))
	t.Cleanup(gate.Close)
	r.finderLink = gate.URL + "/getnzb/abc123def.nzb?i=4242&r=" + handoffFinderKey
	prowlarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, r.finderLink, http.StatusMovedPermanently)
	}))
	t.Cleanup(prowlarr.Close)
	r.link = prowlarr.URL + "/7/download?apikey=" + handoffProwlarrKey + "&link=c29tZXRoaW5nbG9uZw&file=Show"
	r.setGate(cloudflareGate)
	return r
}

func (r *handoffRig) setGate(h http.HandlerFunc) {
	r.gateMu.Lock()
	r.gate = h
	r.gateMu.Unlock()
}

// cloudflareGate admits SABnzbd and blocks everything else with a challenge page.
func cloudflareGate(w http.ResponseWriter, req *http.Request) {
	if !strings.HasPrefix(req.UserAgent(), "SABnzbd/") {
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		w.Header().Set("Cf-Ray", "8d1f2e3a4b5c6d7e-SYD")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<!DOCTYPE html><html><head><title>Just a moment...</title></head><body>blocked</body></html>`))
		return
	}
	_, _ = w.Write([]byte(testNZB))
}

func (r *handoffRig) noSecrets(t *testing.T, what, s string) {
	t.Helper()
	for _, sec := range []string{handoffProwlarrKey, handoffFinderKey, handoffSABKey, "getnzb/abc"} {
		if strings.Contains(s, sec) {
			t.Errorf("%s carries a secret (%s): %q", what, sec, s)
		}
	}
}

// The user's report end to end: Prowlarr redirects to NZBFinder, whose Cloudflare blocks
// Arrmada. The grab hands SABnzbd the indexer's own link (addurl) under the release's name
// and category, records the grab by SABnzbd's job id, and says so in the title's history
// and the log — with no key anywhere Arrmada writes.
func TestUsenetGrabHandsBlockedLinkToSABnzbd(t *testing.T) {
	r := newHandoffRig(t)
	ctx := r.ctx
	name := "Show.S01.1080p.WEB-DL-GRP"

	key, err := r.c.grabTo(ctx, "NZBIdx", r.link, name, seriesCategory)
	if err != nil {
		t.Fatal(err)
	}
	if key != "sab-sabnzbd_nzo_handoff1" {
		t.Fatalf("key = %q, want SABnzbd's job id", key)
	}
	adds := r.sab.adds()
	if len(adds) != 1 || adds[0].Get("mode") != "addurl" {
		t.Fatalf("SABnzbd got %v, want one addurl", adds)
	}
	for field, want := range map[string]string{
		"name": r.finderLink, "nzbname": name, "cat": seriesCategory, "priority": "-100", "pp": "3",
	} {
		if got := adds[0].Get(field); got != want {
			t.Errorf("addurl %s = %q, want %q", field, got, want)
		}
	}

	r.c.recordSeriesGrab(ctx, r.seriesID, name, "NZBIdx", "", key)
	if g := grabRowOf(t, r.c, name); g.hash != key || g.transport != "usenet" || g.seed {
		t.Errorf("grab row = %+v", g)
	}
	var detail string
	if err := r.c.db.QueryRow(`SELECT detail FROM series_events WHERE series_id = ? AND event = ?`, r.seriesID, handoffEvent).Scan(&detail); err != nil {
		t.Fatalf("no hand-off line in the history: %v", err)
	}
	if want := "NZBIdx blocked Arrmada's download; SABnzbd fetched the NZB itself — " + name; detail != want {
		t.Errorf("history = %q, want %q", detail, want)
	}
	// Recorded once: a second grab of the same release says nothing.
	r.c.recordSeriesGrab(ctx, r.seriesID, name, "NZBIdx", "", key)
	var n int
	_ = r.c.db.QueryRow(`SELECT COUNT(*) FROM series_events WHERE series_id = ? AND event = ?`, r.seriesID, handoffEvent).Scan(&n)
	if n != 1 {
		t.Errorf("%d hand-off lines, want 1", n)
	}

	logs := r.log.String()
	if !strings.Contains(logs, "SABnzbd fetches the NZB itself") {
		t.Errorf("no log line for the hand-off:\n%s", logs)
	}
	r.noSecrets(t, "the log", logs)
	r.noSecrets(t, "the history", detail)
}

// Answers that aren't a block keep today's behaviour: the grab fails with the indexer's
// own reason and SABnzbd is never handed a link.
func TestUsenetGrabNoHandoffUnlessBlocked(t *testing.T) {
	cases := []struct {
		name string
		gate http.HandlerFunc
		want string
	}{
		{"newznab bad key", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`<error code="100" description="Incorrect user credentials"/>`))
		}, "refused the API key"},
		{"newznab bad key with a 403", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`<error code="101" description="Account suspended"/>`))
		}, "refused the API key"},
		{"download limit", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`<html><body>slow down</body></html>`))
		}, "API or download limit is reached"},
		{"gone", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`<html><body>not found</body></html>`))
		}, "no longer has this NZB"},
		{"down for maintenance behind Cloudflare", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Cf-Ray", "8d1f2e3a4b5c6d7e-SYD")
			w.Header().Set("Retry-After", "600")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`<html><body>maintenance</body></html>`))
		}, "couldn't send the NZB (HTTP 503)"},
		{"a page served as a 200", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!DOCTYPE html><html><body>Daily download limit reached</body></html>`))
		}, "didn't send an NZB file"},
		{"newznab gone", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`<error code="300" description="No such item"/>`))
		}, "no longer has this NZB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newHandoffRig(t)
			r.setGate(tc.gate)
			_, err := r.c.grabTo(r.ctx, "NZBIdx", r.link, "Show.S01.1080p.WEB-DL-GRP", seriesCategory)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if adds := r.sab.adds(); len(adds) != 0 {
				t.Errorf("SABnzbd was handed %v", adds)
			}
			r.noSecrets(t, "the error", err.Error())
		})
	}

	// A timeout isn't a block either.
	r := newHandoffRig(t)
	r.setGate(func(w http.ResponseWriter, req *http.Request) {
		select {
		case <-req.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	ctx, cancel := context.WithTimeout(r.ctx, 300*time.Millisecond)
	defer cancel()
	if _, err := r.c.grabTo(ctx, "NZBIdx", r.link, "Show.S01.1080p.WEB-DL-GRP", seriesCategory); err == nil {
		t.Fatal("a timed-out fetch grabbed")
	}
	if adds := r.sab.adds(); len(adds) != 0 {
		t.Errorf("SABnzbd was handed %v after a timeout", adds)
	}
}

// SABnzbd refusing the handed-over link fails the grab with both reasons, and no key.
func TestUsenetGrabHandoffRefused(t *testing.T) {
	r := newHandoffRig(t)
	r.sab.mu.Lock()
	r.sab.fail = true
	r.sab.mu.Unlock()
	_, err := r.c.grabTo(r.ctx, "NZBIdx", r.link, "Show.S01.1080p.WEB-DL-GRP", seriesCategory)
	if err == nil || !strings.Contains(err.Error(), "NZBIdx blocked Arrmada's request (HTTP 403") ||
		!strings.Contains(err.Error(), "handing the link to SABnzbd failed too") {
		t.Fatalf("err = %v", err)
	}
	r.noSecrets(t, "the error", err.Error())
	r.noSecrets(t, "the log", r.log.String())
}
