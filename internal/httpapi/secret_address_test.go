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

	rec := httptest.NewRecorder()
	a.handleInsightsTest(rec, httptest.NewRequest("POST", "/api/v1/insights/plex/test", strings.NewReader(`{"url":"`+evil.URL+`"}`)))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Enter the token again") {
		t.Fatalf("test, new address: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	a.handleUpdateInsightsConfig(rec, httptest.NewRequest("PUT", "/api/v1/insights/plex", strings.NewReader(`{"url":"`+evil.URL+`"}`)))
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
	rec = httptest.NewRecorder()
	a.handleInsightsTest(rec, httptest.NewRequest("POST", "/api/v1/insights/plex/test", strings.NewReader(`{"url":"`+pms.URL+`/"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("test, saved address: %d %s", rec.Code, rec.Body)
	}
	// A token typed for the new address is the caller's own to send.
	rec = httptest.NewRecorder()
	a.handleInsightsTest(rec, httptest.NewRequest("POST", "/api/v1/insights/plex/test", strings.NewReader(`{"url":"`+evil.URL+`","token":"typed"}`)))
	if rec.Code != http.StatusOK || hits.Load() == 0 {
		t.Fatalf("test with a typed token: %d %s, hits %d", rec.Code, rec.Body, hits.Load())
	}
}
