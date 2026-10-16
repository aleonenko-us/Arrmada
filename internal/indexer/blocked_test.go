package indexer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/httpua"
)

const (
	prowlarrKey = "PROWLARRAPIKEY0123456789"
	finderKey   = "FINDERDOWNLOADKEY987654321"
)

// cfBlockPage is the kind of page Cloudflare answers a request it won't admit with.
const cfBlockPage = `<!DOCTYPE html><html lang="en-US"><head><title>Just a moment...</title>
<script src="/cdn-cgi/challenge-platform/h/b/orchestrate/chl_page/v1"></script></head>
<body><div id="cf-error-details">Sorry, you have been blocked</div></body></html>`

// gatedIndexer is an indexer behind a Cloudflare-like gate that admits only SABnzbd: any
// other User-Agent gets the block page (403, cf-ray), SABnzbd gets the NZB. It records the
// User-Agent of every request.
type gatedIndexer struct {
	*httptest.Server
	mu  sync.Mutex
	uas []string
}

func newGatedIndexer(t *testing.T) *gatedIndexer {
	t.Helper()
	g := &gatedIndexer{}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.uas = append(g.uas, r.UserAgent())
		g.mu.Unlock()
		if !strings.HasPrefix(r.UserAgent(), "SABnzbd/") {
			w.Header().Set("Content-Type", "text/html; charset=UTF-8")
			w.Header().Set("CF-RAY", "8d1f2e3a4b5c6d7e-SYD")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(cfBlockPage))
			return
		}
		_, _ = w.Write([]byte(nzbBody))
	}))
	t.Cleanup(g.Close)
	return g
}

func (g *gatedIndexer) userAgents() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.uas...)
}

// prowlarrRedirect is Prowlarr's /N/download for an indexer whose definition forces
// Redirect: it answers 301 to the indexer's own link.
func prowlarrRedirect(t *testing.T, target string, uas *[]string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*uas = append(*uas, r.UserAgent())
		mu.Unlock()
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func noSecrets(t *testing.T, what, s string) {
	t.Helper()
	for _, sec := range []string{prowlarrKey, finderKey, nzbKey, "getnzb/abc"} {
		if strings.Contains(s, sec) {
			t.Errorf("%s carries a secret (%s): %q", what, sec, s)
		}
	}
}

// The user's report: NZBFinder via Prowlarr. Prowlarr redirects to nzbfinder, whose
// Cloudflare turns Arrmada away. The fetch says it was blocked (not "refused the API key"),
// offers the redirect target — the indexer's own link, not Prowlarr's — for SABnzbd to
// fetch, keeps every key out of its text, sends Arrmada's User-Agent on both hops, and
// doesn't count against the indexer (its searches through Prowlarr work).
func TestFetchNZBBlockedBehindProwlarrRedirect(t *testing.T) {
	gate := newGatedIndexer(t)
	finderLink := gate.URL + "/getnzb/abc123def.nzb?i=4242&r=" + finderKey
	var prowlarrUAs []string
	prowlarr := prowlarrRedirect(t, finderLink, &prowlarrUAs)

	s, _, _ := healthService(t)
	idx, err := s.Create(context.Background(), Indexer{Name: "NZBFinder (Prowlarr)", Kind: KindNewznab,
		URL: prowlarr.URL + "/7/api", APIKey: prowlarrKey, Enabled: true, ProwlarrID: 7})
	if err != nil {
		t.Fatal(err)
	}
	link := prowlarr.URL + "/7/download?apikey=" + prowlarrKey + "&link=c29tZXRoaW5nbG9uZ2VuY3J5cHRlZA&file=Film.2021"
	_, err = s.Fetch(context.Background(), idx.Name, link)
	var be *BlockedError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v (%T), want a BlockedError", err, err)
	}
	want := "NZBFinder (Prowlarr) blocked Arrmada's request (HTTP 403, a web page instead of an NZB) — it may only allow certain apps"
	if err.Error() != want {
		t.Errorf("error = %q\nwant    %q", err, want)
	}
	if strings.Contains(err.Error(), "API key") {
		t.Errorf("a block is reported as a key problem: %q", err)
	}
	if be.ClientLink() != finderLink {
		t.Errorf("ClientLink = %q, want the indexer's own link it redirected to", be.ClientLink())
	}
	noSecrets(t, "the error", err.Error())
	noSecrets(t, "Redact", be.Redact("SABnzbd said: "+finderLink+" and "+link+" key "+finderKey))

	ua := httpua.UserAgent()
	if got := gate.userAgents(); len(got) != 1 || got[0] != ua {
		t.Errorf("the indexer saw User-Agents %q, want %q", got, ua)
	}
	if len(prowlarrUAs) != 1 || prowlarrUAs[0] != ua {
		t.Errorf("Prowlarr saw User-Agents %q, want %q", prowlarrUAs, ua)
	}
	if st, _, ok := s.Status(idx.ID); ok && st.ConsecutiveFailures > 0 {
		t.Errorf("a blocked download counted against the indexer: %+v", st)
	}
}

// Blocked on Prowlarr's own link (no redirect): handing that over would put Prowlarr's API
// key in SABnzbd's history, so there's no link to hand over — just the honest message.
func TestFetchNZBBlockedProwlarrLinkIsNotHandedOver(t *testing.T) {
	gate := newGatedIndexer(t)
	s, _, _ := healthService(t)
	idx, err := s.Create(context.Background(), Indexer{Name: "Synced", Kind: KindNewznab,
		URL: gate.URL + "/7/api", APIKey: prowlarrKey, Enabled: true, ProwlarrID: 7})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Fetch(context.Background(), idx.Name, gate.URL+"/7/download?apikey="+prowlarrKey+"&link=x")
	var be *BlockedError
	if !errors.As(err, &be) || be.ClientLink() != "" {
		t.Fatalf("err = %v, link %q; want a block with nothing to hand over", err, func() string {
			if be != nil {
				return be.ClientLink()
			}
			return ""
		}())
	}
	noSecrets(t, "the error", err.Error())
}

// What counts as a block, and what keeps its own meaning (and never offers a link).
func TestFetchNZBBlockOrNot(t *testing.T) {
	html := func(status int, hdr map[string]string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			for k, v := range hdr {
				w.Header().Set(k, v)
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte("<!DOCTYPE html><html><body>nope</body></html>"))
		}
	}
	plain := func(status int, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}
	}
	cases := []struct {
		name    string
		h       http.HandlerFunc
		blocked bool
		want    string
	}{
		{"403 web page", html(403, nil), true, "Geek blocked Arrmada's request (HTTP 403, a web page instead of an NZB) — it may only allow certain apps"},
		{"403 bare", plain(403, "Forbidden"), true, "Geek blocked Arrmada's request (HTTP 403, a refusal instead of an NZB) — it may only allow certain apps"},
		{"cf-mitigated challenge", html(403, map[string]string{"Cf-Mitigated": "challenge"}), true, "— it may only allow certain apps"},
		{"Cloudflare 503", html(503, map[string]string{"Cf-Ray": "1-SYD"}), true, "(HTTP 503, a web page instead of an NZB) — it may only allow certain apps"},
		{"200 web page", html(200, nil), true, "Geek blocked Arrmada's request (HTTP 200, a web page instead of an NZB)"},
		{"200 page by its start", plain(200, "\n  <!doctype html><html><body>Log in</body></html>"), true, "(HTTP 200, a web page instead of an NZB)"},
		{"401 web page", html(401, nil), true, "(HTTP 401, a web page instead of an NZB)"},
		// Not blocks: the indexer's own <error>, a limit, gone, a plain server error.
		{"newznab error 100", plain(200, `<error code="100" description="Incorrect user credentials"/>`), false, "Geek refused the API key (Incorrect user credentials)"},
		{"newznab error 100 with a 403", plain(403, `<error code="100" description="Incorrect user credentials"/>`), false, "Geek refused the API key"},
		{"newznab error 101 with a 403", plain(403, `<?xml version="1.0"?><error code="101" description="Account suspended"/>`), false, "Geek refused the API key (Account suspended)"},
		{"bare 401", plain(401, ""), false, "Geek refused the API key"},
		{"429 web page", html(429, nil), false, "Geek says its API or download limit is reached"},
		{"newznab limit", plain(200, `<error code="429" description="Request limit reached"/>`), false, "Geek says its API or download limit is reached"},
		{"404 web page", html(404, nil), false, "Geek no longer has this NZB"},
		{"410", plain(410, ""), false, "Geek no longer has this NZB"},
		{"newznab gone", plain(200, `<error code="300" description="No such item"/>`), false, "Geek no longer has this NZB"},
		{"502 web page", html(502, nil), false, "Geek couldn't send the NZB (HTTP 502)"},
		{"200 JSON", plain(200, `{"error":"x"}`), false, "Geek: the indexer didn't send an NZB file"},
		{"NZB served as text/html", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(nzbBody))
		}, false, ""},
	}
	for _, tc := range cases {
		s, link := nzbIndexer(t, tc.h)
		_, err := s.Fetch(context.Background(), "Geek", link)
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		var be *BlockedError
		switch {
		case err == nil:
			t.Errorf("%s: no error", tc.name)
			continue
		case errors.As(err, &be) != tc.blocked:
			t.Errorf("%s: blocked = %v, want %v (%v)", tc.name, !tc.blocked, tc.blocked, err)
		case tc.blocked && be.ClientLink() != link:
			t.Errorf("%s: ClientLink = %q, want the link", tc.name, be.ClientLink())
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q, want it to contain %q", tc.name, err, tc.want)
		}
		if tc.blocked && strings.Contains(err.Error(), "API key") {
			t.Errorf("%s: a block reads as a key problem: %q", tc.name, err)
		}
		noSecrets(t, tc.name, err.Error())
	}
}

// A timeout is a failed connection, not a block: no link to hand over.
func TestFetchNZBTimeoutIsNotABlock(t *testing.T) {
	s, link := nzbIndexer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := s.Fetch(ctx, "Geek", link)
	var be *BlockedError
	if err == nil || errors.As(err, &be) {
		t.Fatalf("err = %v, want a failed download that isn't a block", err)
	}
	noSecrets(t, "timeout", err.Error())
}

// A direct Newznab indexer behind the same gate: its Test and its searches say it was
// blocked — never "refused the API key" — while a 200 web page without Cloudflare's marks
// is still the "wrong address" it usually is, and a real 401 is still the key.
func TestTorznabBlockedAnswers(t *testing.T) {
	gate := newGatedIndexer(t)
	s, _, _ := healthService(t)
	idx, err := s.Create(context.Background(), Indexer{Name: "NZBFinder", Kind: KindNewznab, URL: gate.URL + "/api", APIKey: finderKey, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	want := "NZBFinder blocked Arrmada's request (HTTP 403, a web page instead of results) — it may only allow certain apps"
	err = s.Test(context.Background(), idx.ID)
	if err == nil || err.Error() != want {
		t.Errorf("Test = %v\nwant %q", err, want)
	}
	if st, _, ok := s.Status(idx.ID); !ok || st.LastError == "" || strings.Contains(st.LastError, "API key") || strings.Contains(st.LastError, finderKey) {
		t.Errorf("status after the Test: %+v", st)
	}
	if _, err := s.TestSettings(context.Background(), idx); err == nil || err.Error() != want {
		t.Errorf("TestSettings = %v", err)
	}
	ts := NewTorznabSearcher()
	_, err = ts.Search(context.Background(), idx, SearchQuery{Text: "film"})
	if err == nil || err.Error() != want {
		t.Errorf("Search = %v\nwant %q", err, want)
	}
	for _, ua := range gate.userAgents() {
		if ua != httpua.UserAgent() {
			t.Errorf("the indexer saw User-Agent %q", ua)
		}
	}

	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<!doctype html><html><head><title>Prowlarr</title></head></html>"))
	}))
	defer page.Close()
	_, err = ts.TestCaps(context.Background(), Indexer{Name: "P", Kind: KindTorznab, URL: page.URL})
	if !errors.Is(err, errWebPage) {
		t.Errorf("a 200 web page without Cloudflare = %v, want errWebPage", err)
	}

	cf200 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(cfBlockPage))
	}))
	defer cf200.Close()
	_, err = ts.TestCaps(context.Background(), Indexer{Name: "CF", Kind: KindNewznab, URL: cf200.URL})
	var be *BlockedError
	if !errors.As(err, &be) || be.ClientLink() != "" {
		t.Errorf("a 200 Cloudflare challenge = %v, want a block", err)
	}

	unauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`<error code="100" description="Incorrect user credentials"/>`))
	}))
	defer unauth.Close()
	_, err = ts.TestCaps(context.Background(), Indexer{Name: "K", Kind: KindNewznab, URL: unauth.URL, APIKey: finderKey})
	var he *HTTPStatusError
	if !errors.As(err, &he) || he.Code != 401 || !strings.Contains(err.Error(), "Incorrect user credentials") {
		t.Errorf("a 401 <error> = %v, want the indexer's own answer", err)
	}
}

// A search feed that came labelled text/html is still a feed, even with a release named
// like Cloudflare's challenge title: only an HTML document is searched for its markers.
func TestBlockedAnswerReadsFeedsAsFeeds(t *testing.T) {
	feed := []byte(`<?xml version="1.0"?><rss><channel><item><title>Just a moment...</title><description>cf-chl</description><link>http://x/1</link></item></channel></rss>`)
	h := http.Header{"Content-Type": {"text/html"}}
	if blocked, _, gate := blockedAnswer(http.StatusOK, h, feed); blocked && gate {
		t.Error("a feed with a challenge-like title read as a Cloudflare block")
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(feed)
	}))
	defer ts.Close()
	got, err := NewTorznabSearcher().Search(context.Background(), Indexer{Name: "T", Kind: KindTorznab, URL: ts.URL}, SearchQuery{Text: "x"})
	if err != nil || len(got) != 1 {
		t.Errorf("search = %d releases, %v; want the feed's one", len(got), err)
	}
}
