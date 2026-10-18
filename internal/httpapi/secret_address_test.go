package httpapi

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

// trap is a server someone with a manager's role points a saved connection at, hoping the
// saved secret comes along. It counts every request it gets.
func trap(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// N1: a sync with a new Prowlarr address and the key left blank is refused before anything
// is sent; the same address (spelled differently) reuses the saved key.
func TestProwlarrSyncSavedKeyStaysWithItsAddress(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	set := settings.NewService(st.DB())
	idx := indexer.NewService(st.DB(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	a := &api{deps: Deps{Settings: set, Indexers: idx}}
	ctx := context.Background()
	_ = set.Set(ctx, indexer.SettingProwlarrURL, "http://prowlarr.invalid:9696")
	_ = set.Set(ctx, indexer.SettingProwlarrKey, "prowlarr-secret")
	evil, hits := trap(t)

	w := callIndexer(t, a.handleProwlarrSync, http.MethodPost, "/api/v1/indexers/prowlarr/sync", "", `{"url":"`+evil.URL+`","api_key":""}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Enter the API key again for the new address") {
		t.Fatalf("new address, blank key: %d %s", w.Code, w.Body)
	}
	if hits.Load() != 0 {
		t.Fatalf("the new address was contacted %d times", hits.Load())
	}
	if got := set.Get(ctx, indexer.SettingProwlarrURL, ""); got != "http://prowlarr.invalid:9696" {
		t.Fatalf("saved URL changed to %q", got)
	}
	// A key typed for the new address goes there (and only that one).
	w = callIndexer(t, a.handleProwlarrSync, http.MethodPost, "/api/v1/indexers/prowlarr/sync", "", `{"url":"`+evil.URL+`","api_key":"its-own-key"}`)
	if w.Code == http.StatusBadRequest || hits.Load() == 0 {
		t.Fatalf("typed key: %d %s, hits %d", w.Code, w.Body, hits.Load())
	}
}

// N1: an indexer edit or a test of an edit with a new address and the key left blank is
// refused and sends nothing; the saved address keeps working with the saved key.
func TestIndexerSavedKeyStaysWithItsAddress(t *testing.T) {
	a, svc := indexerAPI(t)
	ctx := context.Background()
	row, err := svc.Create(ctx, indexer.Indexer{Name: "Saved", Kind: indexer.KindTorznab, URL: "http://tracker.invalid/api", APIKey: "saved-key", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	evil, hits := trap(t)
	id := strconv.FormatInt(row.ID, 10)

	w := callIndexer(t, a.handleTestIndexerSettings, http.MethodPost, "/api/v1/indexers/test", "",
		`{"id":`+id+`,"name":"Saved","kind":"torznab","url":"`+evil.URL+`","api_key":""}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Enter the API key again") {
		t.Fatalf("test, new address: %d %s", w.Code, w.Body)
	}
	w = callIndexer(t, a.handleUpdateIndexer, http.MethodPut, "/api/v1/indexers/"+id, id,
		`{"name":"Saved","kind":"torznab","url":"`+evil.URL+`","api_key":""}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("update, new address: %d %s", w.Code, w.Body)
	}
	if got, _ := svc.Get(ctx, row.ID); got.URL != "http://tracker.invalid/api" || got.APIKey != "saved-key" {
		t.Fatalf("a refused edit changed the row: %+v", got)
	}
	// The saved row's own test goes to its own address.
	_ = callIndexer(t, a.handleTestIndexer, http.MethodPost, "/api/v1/indexers/"+id+"/test", id, ``)
	if hits.Load() != 0 {
		t.Fatalf("the new address was contacted %d times", hits.Load())
	}
	// The same address, written differently, keeps the key.
	w = callIndexer(t, a.handleUpdateIndexer, http.MethodPut, "/api/v1/indexers/"+id, id,
		`{"name":"Renamed","kind":"torznab","url":"HTTP://tracker.invalid/api/","api_key":""}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("update, same address: %d %s", w.Code, w.Body)
	}
	if got, _ := svc.Get(ctx, row.ID); got.APIKey != "saved-key" || got.Name != "Renamed" {
		t.Fatalf("same-address edit: %+v", got)
	}
	// A key typed with the new address is fine.
	w = callIndexer(t, a.handleUpdateIndexer, http.MethodPut, "/api/v1/indexers/"+id, id,
		`{"name":"Renamed","kind":"torznab","url":"`+evil.URL+`","api_key":"new-key"}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("update with a typed key: %d %s", w.Code, w.Body)
	}
}

// N1 (review): hopping through a site-bound kind (TorrentLeech, MyAnonaMouse) can't move
// the saved key either: a kind change with the key left blank is refused, the site-bound
// kinds keep no address, and the key never reaches the new server.
func TestIndexerSavedKeyCantHopThroughAnotherKind(t *testing.T) {
	for _, hop := range []indexer.Kind{indexer.KindTorrentLeech, indexer.KindMAM} {
		t.Run(string(hop), func(t *testing.T) {
			a, svc := indexerAPI(t)
			ctx := context.Background()
			row, err := svc.Create(ctx, indexer.Indexer{Name: "Saved", Kind: indexer.KindTorznab, URL: "http://tracker.invalid/api", APIKey: "saved-key", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			var got atomic.Value
			got.Store("")
			evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if k := r.URL.Query().Get("apikey"); k != "" {
					got.Store(k)
				}
				_, _ = w.Write([]byte(`<caps><searching><movie-search available="yes" supportedParams="q"/></searching><categories><category id="2000"/></categories></caps>`))
			}))
			defer evil.Close()
			id := strconv.FormatInt(row.ID, 10)

			w := callIndexer(t, a.handleUpdateIndexer, http.MethodPut, "/api/v1/indexers/"+id, id,
				`{"name":"Saved","kind":"`+string(hop)+`","url":"`+evil.URL+`","api_key":""}`)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "another kind of indexer") {
				t.Fatalf("kind change with a blank key: %d %s", w.Code, w.Body)
			}
			w = callIndexer(t, a.handleUpdateIndexer, http.MethodPut, "/api/v1/indexers/"+id, id,
				`{"name":"Saved","kind":"torznab","url":"`+evil.URL+`","api_key":""}`)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("then a new address: %d %s", w.Code, w.Body)
			}
			_ = callIndexer(t, a.handleTestIndexer, http.MethodPost, "/api/v1/indexers/"+id+"/test", id, ``)
			if got.Load().(string) == "saved-key" {
				t.Fatal("the saved key reached the new server")
			}

			// A site-bound row keeps no address, so it can't carry one into a later edit.
			if hop == indexer.KindTorrentLeech {
				tl, err := svc.Create(ctx, indexer.Indexer{Name: "TL", Kind: indexer.KindTorrentLeech, Username: "u", Password: "pw", Enabled: true})
				if err != nil {
					t.Fatal(err)
				}
				tid := strconv.FormatInt(tl.ID, 10)
				w = callIndexer(t, a.handleUpdateIndexer, http.MethodPut, "/api/v1/indexers/"+tid, tid,
					`{"name":"TL","kind":"torrentleech","url":"`+evil.URL+`","username":"u","password":""}`)
				if w.Code != http.StatusNoContent {
					t.Fatalf("TorrentLeech edit: %d %s", w.Code, w.Body)
				}
				if got, _ := svc.Get(ctx, tl.ID); got.URL != "" || got.Password != "pw" {
					t.Fatalf("TorrentLeech row = url %q, password kept %v", got.URL, got.Password == "pw")
				}
			}
		})
	}
}

// N1: a SABnzbd edit with a new address and the API key left blank is refused (with the
// key's wording), and testing the client afterwards still goes to its saved address.
func TestDownloadClientSavedKeyStaysWithItsAddress(t *testing.T) {
	s, dl := newClientServer(t)
	ctx := context.Background()
	c, err := dl.Create(ctx, download.Client{Name: "sab", Kind: download.KindSABnzbd, URL: "http://sab.invalid:8080", Password: "sab-api-key", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	evil, hits := trap(t)
	path := fmt.Sprintf("/api/v1/downloadclients/%d", c.ID)

	rec := s.doBody("PUT", path, `{"name":"sab","kind":"sabnzbd","url":"`+evil.URL+`","password":""}`, mgr)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Enter the API key again") {
		t.Fatalf("new address, blank key: HTTP %d %s", rec.Code, rec.Body)
	}
	_ = s.do("POST", path+"/test", mgr)
	if hits.Load() != 0 {
		t.Fatalf("the new address was contacted %d times", hits.Load())
	}
	if stored, _ := dl.Get(ctx, c.ID); stored.URL != "http://sab.invalid:8080" {
		t.Fatalf("a refused edit changed the client: %+v", stored)
	}
}

// N1: the Plex test and save with a new address and no token are refused before the
// owner's token goes anywhere; an empty address (clearing) and the saved one still work.
func TestPlexSavedTokenStaysWithItsAddress(t *testing.T) {
	pms := (&fakePMS{}).server(t)
	a := plexScanAPI(t, pms.URL)
	evil, hits := trap(t)

	admin := &auth.User{ID: 1, Username: "owner", Role: auth.RoleAdmin}
	call := func(h http.HandlerFunc, method, path, body string, u *auth.User) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h(rec, withUser(httptest.NewRequest(method, path, strings.NewReader(body)), u))
		return rec
	}

	rec := call(a.handleInsightsTest, "POST", "/api/v1/insights/plex/test", `{"url":"`+evil.URL+`"}`, admin)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Enter the token again") {
		t.Fatalf("test, new address: %d %s", rec.Code, rec.Body)
	}
	rec = call(a.handleUpdateInsightsConfig, "PUT", "/api/v1/insights/plex", `{"url":"`+evil.URL+`"}`, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("save, new address: %d %s", rec.Code, rec.Body)
	}
	if hits.Load() != 0 {
		t.Fatalf("the new address was contacted %d times", hits.Load())
	}
	if got := a.deps.Insights.Config(t.Context()); got.URL != pms.URL || !got.TokenSet {
		t.Fatalf("a refused save changed the connection: %+v", got)
	}
	// The saved address, with a trailing slash, still tests with the saved token.
	if rec = call(a.handleInsightsTest, "POST", "/api/v1/insights/plex/test", `{"url":"`+pms.URL+`/"}`, admin); rec.Code != http.StatusOK {
		t.Fatalf("test, saved address: %d %s", rec.Code, rec.Body)
	}
	// A token typed for the new address is the admin's own to send.
	rec = call(a.handleInsightsTest, "POST", "/api/v1/insights/plex/test", `{"url":"`+evil.URL+`","token":"typed"}`, admin)
	if rec.Code != http.StatusOK || hits.Load() == 0 {
		t.Fatalf("test with a typed token: %d %s, hits %d", rec.Code, rec.Body, hits.Load())
	}
}

// The Plex connection is the admin's (its server gates Plex sign-in): a manager tests
// only the saved connection (no other address, no token) and changes only monitoring.
// The connection's own routes being admin-only is in testdata/routes.golden.
func TestPlexConnectionIsTheAdmins(t *testing.T) {
	pms := (&fakePMS{}).server(t)
	a := plexScanAPI(t, pms.URL)
	evil, hits := trap(t)
	mgr := &auth.User{ID: 2, Username: "mgr", Role: auth.RoleManager}
	call := func(h http.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h(rec, withUser(httptest.NewRequest(method, path, strings.NewReader(body)), mgr))
		return rec
	}
	for _, body := range []string{`{"url":"` + evil.URL + `"}`, `{"url":"` + evil.URL + `","token":"x"}`, `{"token":"x"}`} {
		if rec := call(a.handleInsightsTest, "POST", "/api/v1/insights/plex/test", body); rec.Code != http.StatusForbidden {
			t.Errorf("manager test %s: %d %s", body, rec.Code, rec.Body)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("a manager's test reached another address %d times", hits.Load())
	}
	for _, body := range []string{`{}`, `{"url":"` + pms.URL + `/"}`} {
		if rec := call(a.handleInsightsTest, "POST", "/api/v1/insights/plex/test", body); rec.Code != http.StatusOK {
			t.Errorf("manager test of the saved connection %s: %d %s", body, rec.Code, rec.Body)
		}
	}
	rec := call(a.handlePlexMonitoring, "PUT", "/api/v1/insights/plex/monitoring", `{"enabled":false,"poll_seconds":12}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("monitoring: %d %s", rec.Code, rec.Body)
	}
	if got := a.deps.Insights.Config(t.Context()); got.Enabled || got.PollSeconds != 12 || got.URL != pms.URL || !got.TokenSet {
		t.Fatalf("after monitoring: %+v", got)
	}
	if rec := call(a.handlePlexMonitoring, "PUT", "/api/v1/insights/plex/monitoring", `{"poll_seconds":1}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("interval 1: %d", rec.Code)
	}
}
