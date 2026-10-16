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
	challenge := func(status int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(cfBlockPage))
		}
	}
	const (
		handOff  = iota // a block, with the link for SABnzbd
		noLink          // a block, nothing handed over
		notBlock        // keeps its own meaning
	)
	cases := []struct {
		name string
		h    http.HandlerFunc
		kind int
		want string
		// counts: the failure is the indexer's and counts against it (backoff, Retry-After).
		counts bool
	}{
		{"403 web page", html(403, nil), handOff, "Geek blocked Arrmada's request (HTTP 403, a web page instead of an NZB) — it may only allow certain apps", false},
		{"403 bare", plain(403, "Forbidden"), handOff, "Geek blocked Arrmada's request (HTTP 403, a refusal instead of an NZB) — it may only allow certain apps", false},
		{"cf-mitigated challenge", html(403, map[string]string{"Cf-Mitigated": "challenge"}), handOff, "— it may only allow certain apps", false},
		{"Cloudflare challenge page as a 503", challenge(503), handOff, "(HTTP 503, a web page instead of an NZB) — it may only allow certain apps", false},
		// A challenge served as a 200 is reported, but nothing is handed over on a 200.
		{"Cloudflare challenge page as a 200", challenge(200), noLink, "(HTTP 200, a web page instead of an NZB)", false},
		// Not blocks: the indexer's own <error>, a limit, gone, a server error (behind
		// Cloudflare or not), a page served as an answer.
		{"Cloudflare 503 without a challenge", html(503, map[string]string{"Cf-Ray": "1-SYD"}), notBlock, "Geek couldn't send the NZB (HTTP 503)", true},
		{"Cloudflare 403 cf-ray and a plain page is still a 403", html(403, map[string]string{"Cf-Ray": "1-SYD"}), handOff, "HTTP 403", false},
		{"200 web page", html(200, nil), notBlock, "Geek: the indexer didn't send an NZB file", false},
		{"200 page by its start", plain(200, "\n  <!doctype html><html><body>Log in</body></html>"), notBlock, "Geek: the indexer didn't send an NZB file", false},
		{"401 web page", html(401, nil), notBlock, "Geek refused the API key", true},
		{"newznab error 100", plain(200, `<error code="100" description="Incorrect user credentials"/>`), notBlock, "Geek refused the API key (Incorrect user credentials)", true},
		{"newznab error 100 with a 403", plain(403, `<error code="100" description="Incorrect user credentials"/>`), notBlock, "Geek refused the API key", true},
		{"newznab error 101 with a 403", plain(403, `<?xml version="1.0"?><error code="101" description="Account suspended"/>`), notBlock, "Geek refused the API key (Account suspended)", true},
		{"newznab error 900 with a 403", plain(403, `<error code="900" description="Account disabled"/>`), notBlock, "Geek refused the download (HTTP 403: Account disabled)", true},
		{"newznab error 300 with a 401", plain(401, `<error code="300" description="No such item"/>`), notBlock, "Geek refused the download (HTTP 401: No such item)", true},
		{"bare 401", plain(401, ""), notBlock, "Geek refused the API key", true},
		{"429 web page", html(429, nil), notBlock, "Geek says its API or download limit is reached", true},
		{"newznab limit", plain(200, `<error code="429" description="Request limit reached"/>`), notBlock, "Geek says its API or download limit is reached", true},
		{"404 web page", html(404, nil), notBlock, "Geek no longer has this NZB", false},
		{"410", plain(410, ""), notBlock, "Geek no longer has this NZB", false},
		{"newznab gone", plain(200, `<error code="300" description="No such item"/>`), notBlock, "Geek no longer has this NZB", false},
		{"502 web page", html(502, nil), notBlock, "Geek couldn't send the NZB (HTTP 502)", true},
		{"200 JSON", plain(200, `{"error":"x"}`), notBlock, "Geek: the indexer didn't send an NZB file", false},
		{"NZB served as text/html", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(nzbBody))
		}, notBlock, "", false},
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
		var ie *nzbIndexerError
		switch {
		case err == nil:
			t.Errorf("%s: no error", tc.name)
			continue
		case errors.As(err, &be) != (tc.kind != notBlock):
			t.Errorf("%s: blocked = %v, want kind %d (%v)", tc.name, be != nil, tc.kind, err)
		case tc.kind == handOff && be.ClientLink() != link:
			t.Errorf("%s: ClientLink = %q, want the link", tc.name, be.ClientLink())
		case tc.kind == noLink && be.ClientLink() != "":
			t.Errorf("%s: ClientLink = %q, want none", tc.name, be.ClientLink())
		}
		if errors.As(err, &ie) != tc.counts {
			t.Errorf("%s: counts against the indexer = %v, want %v (%v)", tc.name, !tc.counts, tc.counts, err)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q, want it to contain %q", tc.name, err, tc.want)
		}
		if tc.kind != notBlock && strings.Contains(err.Error(), "API key") {
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
	if blocked, _ := blockedAnswer(http.StatusOK, h, feed); blocked {
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

// Which link a block hands to SABnzbd: through Prowlarr, only the indexer link it redirected
// to — never Prowlarr's own link, nor a later redirect to a login or block page; straight
// from an indexer, the release's own link; never anything at Prowlarr's or NZBHydra's address.
func TestHandoffLinkChoice(t *testing.T) {
	synced := Indexer{Name: "S", Kind: KindNewznab, URL: "http://10.0.0.5:9797/7/api", APIKey: prowlarrKey, ProwlarrID: 7}
	direct := Indexer{Name: "D", Kind: KindNewznab, URL: "https://nzbfinder.example/api", APIKey: finderKey}
	prowlarrHosts := map[string]bool{"10.0.0.5:9797": true}
	prowlarrLink := "http://10.0.0.5:9797/7/download?apikey=" + prowlarrKey + "&link=abc&file=Film"
	indexerLink := "https://nzbfinder.example/getnzb/abc123.nzb?i=1&r=" + finderKey
	directLink := "https://nzbfinder.example/api?t=get&id=abc123&apikey=" + finderKey
	cases := []struct {
		name string
		idx  Indexer
		hops []string
		want string
	}{
		{"Prowlarr redirected to the indexer", synced, []string{prowlarrLink, indexerLink}, indexerLink},
		{"then on to a login page", synced, []string{prowlarrLink, indexerLink, "https://nzbfinder.example/login?next=x"}, indexerLink},
		{"then on to the home page", synced, []string{prowlarrLink, indexerLink, "https://nzbfinder.example/"}, indexerLink},
		{"blocked at Prowlarr itself", synced, []string{prowlarrLink}, ""},
		{"Prowlarr redirected straight to a login page", synced, []string{prowlarrLink, "https://nzbfinder.example/login"}, ""},
		{"Prowlarr redirected to a block page", synced, []string{prowlarrLink, "https://nzbfinder.example/account/blocked"}, ""},
		{"Prowlarr redirected to a challenge", synced, []string{prowlarrLink, "https://nzbfinder.example/cdn-cgi/challenge-platform/x"}, ""},
		{"Prowlarr redirected back to itself", synced, []string{prowlarrLink, "http://10.0.0.5:9797/api/v1/indexer/7/download?x=1"}, ""},
		{"a Prowlarr-shaped link on an unknown address", direct, []string{"http://192.168.1.9:8080/prowlarr/12/download?apikey=k", indexerLink}, indexerLink},
		{"a Prowlarr-shaped link not redirected", direct, []string{"http://192.168.1.9:8080/12/download?apikey=k"}, ""},
		{"a direct indexer", direct, []string{directLink}, directLink},
		{"a direct indexer redirecting to a login page", direct, []string{directLink, "https://nzbfinder.example/login"}, directLink},
		{"NZBHydra's download link", direct, []string{"http://192.168.1.9:8080/getnzb/api/123456?apikey=" + finderKey}, ""},
		{"an address named for an aggregator", direct, []string{"http://nzbhydra:8080/api?t=get&apikey=k"}, ""},
		{"Prowlarr's port", direct, []string{"http://192.168.1.9:9696/x?apikey=k"}, ""},
		{"a synced row's link carrying its key", synced, []string{"https://elsewhere.example/get?apikey=" + prowlarrKey}, ""},
		{"not http", direct, []string{"ftp://nzbfinder.example/x.nzb"}, ""},
	}
	for _, tc := range cases {
		if got := handoffLink(tc.idx, tc.hops, prowlarrHosts); got != tc.want {
			t.Errorf("%s: handoffLink = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// End to end: Prowlarr redirects to the indexer, the indexer redirects Arrmada on to a login
// page that turns it away. SABnzbd gets the indexer's link, not the login page.
func TestFetchNZBBlockedAfterLoginRedirect(t *testing.T) {
	var gate *httptest.Server
	gate = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/getnzb/") {
			http.Redirect(w, r, gate.URL+"/login?next=getnzb", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(cfBlockPage))
	}))
	defer gate.Close()
	finderLink := gate.URL + "/getnzb/abc123def.nzb?i=4242&r=" + finderKey
	var uas []string
	prowlarr := prowlarrRedirect(t, finderLink, &uas)
	s, _, _ := healthService(t)
	idx, err := s.Create(context.Background(), Indexer{Name: "F", Kind: KindNewznab, URL: prowlarr.URL + "/7/api", APIKey: prowlarrKey, Enabled: true, ProwlarrID: 7})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Fetch(context.Background(), idx.Name, prowlarr.URL+"/7/download?apikey="+prowlarrKey+"&link=x")
	var be *BlockedError
	if !errors.As(err, &be) || be.ClientLink() != finderLink {
		t.Fatalf("err = %v; want a block handing over the indexer's link", err)
	}
}

// A Cloudflare-fronted indexer that's down (a 503 with cf-ray and no challenge) is a
// struggling indexer, as before: its searches back off and honour its Retry-After.
func TestTorznabCloudflare503IsNotABlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cf-Ray", "8d1f2e3a4b5c6d7e-SYD")
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Retry-After", "600")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`<!DOCTYPE html><html><body>Down for maintenance</body></html>`))
	}))
	defer srv.Close()
	_, err := NewTorznabSearcher().TestCaps(context.Background(), Indexer{Name: "M", Kind: KindNewznab, URL: srv.URL})
	var he *HTTPStatusError
	if !errors.As(err, &he) || he.Code != 503 || he.RetryAfter != 600*time.Second {
		t.Fatalf("err = %#v, want HTTP 503 with its Retry-After", err)
	}
	s, link := nzbIndexer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cf-Ray", "8d1f2e3a4b5c6d7e-SYD")
		w.Header().Set("Retry-After", "600")
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	_, err = s.Fetch(context.Background(), "Geek", link)
	var ie *nzbIndexerError
	if !errors.As(err, &ie) || ie.retryAfter != 600*time.Second {
		t.Fatalf("NZB fetch = %v, want the indexer's error with its Retry-After", err)
	}
}
