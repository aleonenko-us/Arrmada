package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/notinterested"
	"github.com/tristenlammi/arrmada/internal/store"
)

// rowsDiscovery is a provider with the extra rows: popular answers items, and "just out
// at home" answers home.
type rowsDiscovery struct {
	metadata.DiscoveryProvider
	metadata.DiscoverRows
	items []metadata.DiscoverItem
	home  []metadata.DiscoverItem
}

func (rowsDiscovery) Available() bool { return true }
func (d rowsDiscovery) Popular(context.Context, string) ([]metadata.DiscoverItem, error) {
	return d.items, nil
}
func (d rowsDiscovery) JustOutAtHome(context.Context) ([]metadata.DiscoverItem, error) {
	return d.home, nil
}

func hiddenAPI(t *testing.T, d metadata.DiscoveryProvider) (*api, *auth.User, *auth.User) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var users []*auth.User
	for _, name := range []string{"ann", "bob"} {
		res, err := st.DB().Exec(`INSERT INTO users (username, password_hash, role) VALUES (?, 'x', 'requester')`, name)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		users = append(users, &auth.User{ID: id, Username: name, Role: auth.RoleRequester})
	}
	a := &api{deps: Deps{Discovery: d, NotInterested: notinterested.New(st.DB()), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	snap := emptySnap()
	snap.movIn[3], snap.movHave[3] = true, true
	enrichSnaps.Store(a, &enrichSnapEntry{at: time.Now().Add(time.Hour), snap: snap})
	t.Cleanup(func() { enrichSnaps.Delete(a) })
	return a, users[0], users[1]
}

func call(h http.HandlerFunc, u *auth.User, method, target, body string, path map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	for k, v := range path {
		r.SetPathValue(k, v)
	}
	r = withUser(r, u)
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec
}

func itemIDs(t *testing.T, rec *httptest.ResponseRecorder) []int {
	t.Helper()
	var body struct {
		Items []discoverCard `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: %v", rec.Body, err)
	}
	var ids []int
	for _, c := range body.Items {
		ids = append(ids, c.TMDBID)
	}
	return ids
}

// A hidden title leaves the person's rows (and nobody else's) until it's restored; the
// hidden list shows it meanwhile.
func TestNotInterestedHidesForThatPersonOnly(t *testing.T) {
	d := rowsDiscovery{items: []metadata.DiscoverItem{{MediaType: "movie", TMDBID: 1, Title: "One"}, {MediaType: "movie", TMDBID: 2, Title: "Two"}}}
	a, ann, bob := hiddenAPI(t, d)

	if rec := call(a.handleHide, ann, "POST", "/api/v1/discover/hidden", `{"media_type":"movie","tmdb_id":2,"title":"Two","year":2001}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("hide: %d %s", rec.Code, rec.Body)
	}
	if rec := call(a.handleHide, ann, "POST", "/api/v1/discover/hidden", `{"media_type":"book","tmdb_id":2}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("hiding a book: %d", rec.Code)
	}
	if ids := itemIDs(t, call(a.handleDiscoverPopular, ann, "GET", "/api/v1/discover/popular", "", nil)); len(ids) != 1 || ids[0] != 1 {
		t.Errorf("ann's row = %v, want [1]", ids)
	}
	if ids := itemIDs(t, call(a.handleDiscoverPopular, bob, "GET", "/api/v1/discover/popular", "", nil)); len(ids) != 2 {
		t.Errorf("bob's row = %v, want both", ids)
	}
	var list struct {
		Items []notinterested.Title `json:"items"`
	}
	rec := call(a.handleHiddenList, ann, "GET", "/api/v1/discover/hidden", "", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Items) != 1 || list.Items[0].Title != "Two" {
		t.Errorf("hidden list = %s", rec.Body)
	}
	if rec := call(a.handleUnhide, ann, "DELETE", "/api/v1/discover/hidden/movie/2", "", map[string]string{"media": "movie", "id": "2"}); rec.Code != http.StatusOK {
		t.Fatalf("restore: %d", rec.Code)
	}
	if ids := itemIDs(t, call(a.handleDiscoverPopular, ann, "GET", "/api/v1/discover/popular", "", nil)); len(ids) != 2 {
		t.Errorf("after restore ann's row = %v", ids)
	}
	if rec := call(a.handleUnhide, ann, "DELETE", "/x", "", map[string]string{"media": "person", "id": "2"}); rec.Code != http.StatusBadRequest {
		t.Errorf("restore a person: %d", rec.Code)
	}
}

// "Just out at home" leaves out what the library has and what the viewer hid.
func TestJustOutRow(t *testing.T) {
	d := rowsDiscovery{home: []metadata.DiscoverItem{
		{MediaType: "movie", TMDBID: 1, Title: "New", HomeRelease: "2026-10-18"},
		{MediaType: "movie", TMDBID: 3, Title: "Owned", HomeRelease: "2026-10-17"},
		{MediaType: "movie", TMDBID: 4, Title: "Hidden", HomeRelease: "2026-10-16"},
	}}
	a, ann, _ := hiddenAPI(t, d)
	if err := a.deps.NotInterested.Hide(context.Background(), ann.ID, notinterested.Title{MediaType: "movie", TMDBID: 4}); err != nil {
		t.Fatal(err)
	}
	rec := call(a.handleDiscoverRow, ann, "GET", "/api/v1/discover/rows/just_out", "", map[string]string{"kind": "just_out"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if ids := itemIDs(t, rec); len(ids) != 1 || ids[0] != 1 {
		t.Errorf("row = %v, want [1]", ids)
	}
	if !strings.Contains(rec.Body.String(), `"home_release":"2026-10-18"`) {
		t.Errorf("cards lack their home release day: %s", rec.Body)
	}
}

// Collections the library holds two or more of come before one it holds a single film
// of, however complete.
func TestCollectionsPreferTwoOwned(t *testing.T) {
	colls := map[int]*metadata.Collection{
		1: {ID: 1, Name: "Pair", Members: []metadata.MovieResult{member(11, "a", "2000-01-01"), member(12, "b", "2000-01-01"), member(13, "c", "2000-01-01")}},
		2: {ID: 2, Name: "Saga", Members: []metadata.MovieResult{
			member(21, "a", "2000-01-01"), member(22, "b", "2000-01-01"), member(23, "c", "2000-01-01"), member(24, "d", "2000-01-01"),
			member(25, "e", "2000-01-01"), member(26, "f", "2000-01-01"), member(27, "g", "2000-01-01"), member(28, "h", "2000-01-01")}},
	}
	library := []movies.Movie{ownedIn(11, 1), ownedIn(21, 2), ownedIn(22, 2)}
	rows := buildCollectionRows(context.Background(), collectionsDiscovery{colls: colls}, library)
	if len(rows) != 2 || rows[0].id != 2 {
		t.Errorf("rows = %+v, want the two-owned Saga first", rows)
	}
}
