package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/apikeys"
	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/connstatus"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/flaresolverr"
	"github.com/tristenlammi/arrmada/internal/indexer"
)

// Secrets seeded into every connection; none may appear in any response.
const (
	secretIndexerKey   = "idx-key-5f2a9c"
	secretIndexerPass  = "tl-pass-77c1e0"
	secretQueryKey     = "query-key-0b8d"
	secretClientPass   = "qb-pass-31aa4e"
	secretTMDB         = "tmdb-key-9e14c2d7"
	secretProwlarrKey  = "prowlarr-key-c0ffee"
	secretOSPassword   = "os-pass-a1b2c3"
	secretFlareURLUser = "fsuser:fspass"
)

type connServer struct {
	*routeServer
	ix      *indexer.Service
	dl      *download.Service
	tracker *connstatus.Tracker
	tl      indexer.Indexer
	feed    indexer.Indexer
	client  download.Client
}

// newConnServer wires an indexer service, a download client, the key store and the status
// tracker over a scratch database, seeded with a secret in every place one can live.
func newConnServer(t *testing.T, verifiers map[string]func(context.Context, string) (string, error)) *connServer {
	t.Helper()
	cs := &connServer{}
	cs.routeServer = newRouteServer(t, func(d *Deps) {
		cs.tracker = connstatus.New(d.Store.DB(), d.Log)
		d.ConnStatus = cs.tracker
		d.APIKeys = apikeys.NewStore(d.Settings)
		d.FlareSolverr = flaresolverr.NewFunc(d.APIKeys.Func("flaresolverr"))
		// As main wires it: every FlareSolverr answer is recorded.
		d.FlareSolverr.OnResult(func(err error) {
			cs.tracker.Record(connstatus.KindFlareSolverr, flareSolverrRef, connstatus.Outcome{Err: err})
		})
		cs.ix = indexer.NewService(d.Store.DB(), d.Log, d.FlareSolverr)
		cs.ix.SetStatus(cs.tracker)
		d.Indexers = cs.ix
		cs.dl = download.NewService(d.Store.DB(), d.Log)
		cs.dl.SetStatus(cs.tracker)
		d.Downloads = cs.dl
		d.KeyVerifiers = verifiers
	})
	ctx := context.Background()
	var err error
	cs.tl, err = cs.ix.Create(ctx, indexer.Indexer{Name: "TorrentLeech", Kind: indexer.KindTorrentLeech, Username: "me", Password: secretIndexerPass, APIKey: secretIndexerKey, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	// Nothing listens on port 1: a Test fails at once.
	cs.feed, err = cs.ix.Create(ctx, indexer.Indexer{Name: "Feed", Kind: indexer.KindTorznab, URL: "http://127.0.0.1:1/api?apikey=" + secretQueryKey, APIKey: secretIndexerKey, Enabled: true, ProwlarrID: 7})
	if err != nil {
		t.Fatal(err)
	}
	cs.client, err = cs.dl.Create(ctx, download.Client{Name: "qb", Kind: download.KindQbittorrent, URL: "http://admin:" + secretClientPass + "@127.0.0.1:1", Username: "admin", Password: secretClientPass, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for id, v := range map[string]string{"tmdb": secretTMDB, "opensubtitles_password": secretOSPassword, "flaresolverr": "http://" + secretFlareURLUser + "@127.0.0.1:1"} {
		if err := cs.deps.APIKeys.Set(ctx, id, v); err != nil {
			t.Fatal(err)
		}
	}
	_ = cs.deps.Settings.Set(ctx, keyProwlarrURL, "http://prowlarr:9696")
	_ = cs.deps.Settings.Set(ctx, keyProwlarrKey, secretProwlarrKey)
	return cs
}

type connList struct {
	Sections []connectionSection `json:"sections"`
}

func (cs *connServer) list(t *testing.T, c *http.Cookie) (connList, string) {
	t.Helper()
	rec := cs.do("GET", "/api/v1/connections", c)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /connections: HTTP %d %s", rec.Code, rec.Body)
	}
	var out connList
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out, rec.Body.String()
}

func (l connList) item(kind, ref string) (connectionItem, bool) {
	for _, s := range l.Sections {
		for _, it := range s.Items {
			if it.Kind == kind && it.Ref == ref {
				return it, true
			}
		}
	}
	return connectionItem{}, false
}

// Every integration is listed in its section with a state; an indexer carries its 24-hour
// queries, failures and grabs and whether Prowlarr manages it; something never asked is
// "unknown", a failing one carries its error.
func TestConnectionsListShape(t *testing.T) {
	cs := newConnServer(t, nil)
	_, admin := cs.user(t, "admin@example.com", auth.RoleAdmin)
	tlRef := strconv.FormatInt(cs.tl.ID, 10)
	cs.tracker.Record(connstatus.KindIndexer, tlRef, connstatus.Outcome{})
	cs.tracker.Record(connstatus.KindIndexer, tlRef, connstatus.Outcome{Err: errors.New("login failed")})
	if _, err := cs.st.DB().Exec(`INSERT INTO grabs (movie_id, title, indexer) VALUES (1, 'A', 'TorrentLeech'), (2, 'B', 'TorrentLeech')`); err != nil {
		t.Fatal(err)
	}

	l, _ := cs.list(t, admin)
	var ids []string
	for _, s := range l.Sections {
		ids = append(ids, s.ID)
	}
	if strings.Join(ids, ",") != "search,downloads,metadata,other" {
		t.Errorf("sections = %v", ids)
	}
	for _, want := range [][2]string{
		{"prowlarr", "default"}, {"flaresolverr", "default"}, {"indexer", tlRef},
		{"indexer", strconv.FormatInt(cs.feed.ID, 10)}, {"download_client", strconv.FormatInt(cs.client.ID, 10)},
		{"apikey", "tmdb"}, {"apikey", "tvdb"}, {"apikey", "omdb"}, {"apikey", "hardcover"}, {"apikey", "opensubtitles_api"},
	} {
		if it, ok := l.item(want[0], want[1]); !ok || it.State == "" || it.Card == "" {
			t.Errorf("%s/%s missing or stateless: %+v", want[0], want[1], it)
		}
	}
	if _, ok := l.item("apikey", "opensubtitles_password"); ok {
		t.Error("the OpenSubtitles password has a card of its own")
	}

	tl, _ := l.item("indexer", tlRef)
	if tl.State != "failing" || tl.LastError != "login failed" || tl.Card != "indexer-"+tlRef || tl.Counts == nil ||
		tl.Counts.Queries24h != 2 || tl.Counts.Failures24h != 1 || tl.Counts.Grabs24h != 2 || tl.LastOKAt == nil {
		t.Errorf("TorrentLeech card: %+v counts %+v", tl, tl.Counts)
	}
	feed, _ := l.item("indexer", strconv.FormatInt(cs.feed.ID, 10))
	if feed.State != "unknown" || feed.ManagedBy != "prowlarr" || feed.Subtitle != "http://127.0.0.1:1/api" {
		t.Errorf("Prowlarr feed card: %+v", feed)
	}
	if p, _ := l.item("prowlarr", "default"); !p.Configured || p.Detail != "Syncs 1 indexer" || !p.Testable {
		t.Errorf("Prowlarr card: %+v", p)
	}
	if f, _ := l.item("flaresolverr", "default"); !f.Configured || f.Source != "settings" || !strings.Contains(f.Detail, "TorrentLeech") {
		t.Errorf("FlareSolverr card: %+v", f)
	}
	if k, _ := l.item("apikey", "tmdb"); !k.Configured || k.State != "unknown" || !k.Testable {
		t.Errorf("TMDB card: %+v", k)
	}
	if k, _ := l.item("apikey", "tvdb"); k.Configured || k.Testable {
		t.Errorf("TheTVDB card (not set): %+v", k)
	}

	// A manager sees the same list, but can't test a key (that's the admin's).
	_, mgr := cs.user(t, "mgr@example.com", auth.RoleManager)
	ml, _ := cs.list(t, mgr)
	if k, _ := ml.item("apikey", "tmdb"); k.Testable {
		t.Errorf("a manager's TMDB card offers Test: %+v", k)
	}
}

// No response — the list, a Test's answer — carries a key, password, token or a URL's
// credentials or query string, and none says api_key/password/token at all.
func TestConnectionsNeverLeakSecrets(t *testing.T) {
	cs := newConnServer(t, map[string]func(context.Context, string) (string, error){
		"tmdb": func(context.Context, string) (string, error) {
			return "", fmt.Errorf("GET https://api.themoviedb.org/3/configuration?api_key=%s: 401", secretTMDB)
		},
	})
	_, admin := cs.user(t, "admin@example.com", auth.RoleAdmin)
	cs.tracker.Record(connstatus.KindDownloadClient, strconv.FormatInt(cs.client.ID, 10),
		connstatus.Outcome{Err: fmt.Errorf("Post \"http://127.0.0.1:1/api/v2/auth/login?password=%s\": refused", secretClientPass)})

	bodies := []string{}
	_, body := cs.list(t, admin)
	bodies = append(bodies, body)
	for _, path := range []string{
		"/api/v1/connections/apikey/tmdb/test",
		fmt.Sprintf("/api/v1/connections/indexer/%d/test", cs.feed.ID),
		fmt.Sprintf("/api/v1/connections/download_client/%d/test", cs.client.ID),
		"/api/v1/connections/flaresolverr/default/test",
	} {
		rec := cs.do("POST", path, admin)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: HTTP %d %s", path, rec.Code, rec.Body)
		}
		bodies = append(bodies, rec.Body.String())
	}
	_, body = cs.list(t, admin)
	bodies = append(bodies, body)

	for _, b := range bodies {
		for _, s := range []string{secretIndexerKey, secretIndexerPass, secretQueryKey, secretClientPass, secretTMDB, secretProwlarrKey, secretOSPassword, secretFlareURLUser, "fspass"} {
			if strings.Contains(b, s) {
				t.Errorf("a response carries the secret %q: %s", s, b)
			}
		}
		for _, field := range []string{`"api_key"`, `"password"`, `"token"`, "apikey="} {
			if strings.Contains(strings.ToLower(b), field) {
				t.Errorf("a response mentions %s: %s", field, b)
			}
		}
	}
}

// Test runs a real check, records it, and answers with the refreshed card; an unknown
// kind or id is a 404; a requester gets 403 everywhere and a manager can't test a key.
func TestConnectionTest(t *testing.T) {
	prowlarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != secretProwlarrKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"version":"1.24.3"}`))
	}))
	defer prowlarr.Close()
	cs := newConnServer(t, map[string]func(context.Context, string) (string, error){
		"tmdb": func(_ context.Context, candidate string) (string, error) {
			if candidate != "" {
				t.Errorf("the saved key's Test passed a candidate %q", candidate)
			}
			return "TMDB accepted the key.", nil
		},
	})
	_ = cs.deps.Settings.Set(context.Background(), keyProwlarrURL, prowlarr.URL)
	_, admin := cs.user(t, "admin@example.com", auth.RoleAdmin)
	_, mgr := cs.user(t, "mgr@example.com", auth.RoleManager)
	_, kid := cs.user(t, "kid@example.com", auth.RoleRequester)

	type answer struct {
		OK     bool           `json:"ok"`
		Detail string         `json:"detail"`
		Item   connectionItem `json:"item"`
	}
	post := func(path string, c *http.Cookie) (int, answer) {
		rec := cs.do("POST", path, c)
		var a answer
		_ = json.Unmarshal(rec.Body.Bytes(), &a)
		return rec.Code, a
	}

	code, a := post(fmt.Sprintf("/api/v1/connections/indexer/%d/test", cs.feed.ID), mgr)
	if code != http.StatusOK || a.OK || a.Detail == "" || a.Item.State != "failing" || a.Item.LastError == "" {
		t.Errorf("failing indexer Test: %d %+v", code, a)
	}
	code, a = post("/api/v1/connections/flaresolverr/default/test", mgr)
	if code != http.StatusOK || a.OK || a.Item.State != "failing" || strings.Contains(a.Detail, "fspass") {
		t.Errorf("dead FlareSolverr Test: %d %+v", code, a)
	}
	code, a = post("/api/v1/connections/prowlarr/default/test", mgr)
	if code != http.StatusOK || !a.OK || !strings.Contains(a.Detail, "1.24.3") || a.Item.State != "ok" || a.Item.Subtitle != prowlarr.URL {
		t.Errorf("Prowlarr Test: %d %+v", code, a)
	}
	code, a = post("/api/v1/connections/apikey/tmdb/test", admin)
	if code != http.StatusOK || !a.OK || a.Item.State != "ok" || a.Item.LastOKAt == nil {
		t.Errorf("TMDB Test: %d %+v", code, a)
	}
	// Saving a new key forgets what the old one's Tests said.
	if rec := cs.doBody("PUT", "/api/v1/apikeys/tmdb", `{"value":"another-key"}`, admin); rec.Code != http.StatusOK {
		t.Fatalf("save key: %d", rec.Code)
	}
	if l, _ := cs.list(t, admin); func() bool { k, _ := l.item("apikey", "tmdb"); return k.State != "unknown" }() {
		t.Error("a replaced TMDB key kept the old key's Test result")
	}

	if code, _ := post("/api/v1/connections/apikey/tmdb/test", mgr); code != http.StatusForbidden {
		t.Errorf("manager testing a key: HTTP %d, want 403", code)
	}
	for _, path := range []string{"/api/v1/connections/indexer/9999/test", "/api/v1/connections/indexer/abc/test",
		"/api/v1/connections/nope/default/test", "/api/v1/connections/prowlarr/other/test", "/api/v1/connections/apikey/opensubtitles_password/test"} {
		c := mgr
		if strings.Contains(path, "/apikey/") {
			c = admin
		}
		if code, _ := post(path, c); code != http.StatusNotFound {
			t.Errorf("%s: HTTP %d, want 404", path, code)
		}
	}
	if rec := cs.do("GET", "/api/v1/connections", kid); rec.Code != http.StatusForbidden {
		t.Errorf("requester list: HTTP %d, want 403", rec.Code)
	}
	if code, _ := post(fmt.Sprintf("/api/v1/connections/indexer/%d/test", cs.tl.ID), kid); code != http.StatusForbidden {
		t.Errorf("requester Test: HTTP %d, want 403", code)
	}
}

// displayURL keeps scheme, host and path, and drops credentials and the query.
func TestDisplayURL(t *testing.T) {
	for in, want := range map[string]string{
		"http://user:pw@qb:8080/":           "http://qb:8080",
		"https://t.example/api?apikey=s3cr": "https://t.example/api",
		"http://arrmada-prowlarr:9696":      "http://arrmada-prowlarr:9696",
		"":                                  "",
		"not a url":                         "",
	} {
		if got := displayURL(in); got != want {
			t.Errorf("displayURL(%q) = %q, want %q", in, got, want)
		}
	}
}
