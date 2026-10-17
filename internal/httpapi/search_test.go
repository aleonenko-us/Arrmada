package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/settings"
)

// searchAPI is a real store and settings with a small library in every module.
func searchAPI(t *testing.T) (*api, *router) {
	t.Helper()
	a := dashAPI(t)
	db := a.deps.Store.DB()
	a.deps.Settings = settings.NewService(db)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO movies (id, tmdb_id, title, year, poster_url) VALUES
			(1, 101, 'Dune', 2021, 'https://image.tmdb.org/t/p/w500/dune.jpg'),
			(2, 102, 'Dune: Part Two', 2024, ''),
			(3, 103, 'Pokémon: The First Movie', 1998, ''),
			(4, 104, 'Fast & Furious', 2009, ''),
			(5, 105, 'Amélie', 2001, ''),
			(6, 106, 'The Secret Life of Pets', 2016, '')`,
		`INSERT INTO series (id, tmdb_id, title, year, poster_url) VALUES
			(1, 201, 'The Office', 2005, 'https://image.tmdb.org/t/p/w500/office.jpg'),
			(2, 202, 'Dune: Prophecy', 2024, '')`,
		`INSERT INTO books (id, ol_key, title, author, year, cover_url) VALUES
			(1, 'OL1W', 'Dune Messiah', 'Frank Herbert', 1969, 'https://covers.openlibrary.org/b/id/1-L.jpg')`,
		`INSERT INTO artists (id, mbid, name, image_url) VALUES (1, 'a-1', 'Dune Rats', '')`,
		`INSERT INTO albums (id, artist_id, mbid, title, year) VALUES (1, 1, 'rg-1', 'The Kids Will Know It''s Bullshit', 2017)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	rt := newRouter(a)
	a.registerRoutes(rt)
	return a, rt
}

// searchAs calls the real route as role and decodes the answer.
func searchAs(t *testing.T, rt *router, role auth.Role, q string) (int, []map[string]any) {
	t.Helper()
	r := asRole(httptest.NewRequest(http.MethodGet, "/api/v1/library/search?q="+url.QueryEscape(q), nil), role)
	rec := httptest.NewRecorder()
	rt.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		return rec.Code, nil
	}
	var body struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if body.Results == nil {
		t.Fatalf("results is null, want a list: %s", rec.Body.String())
	}
	return rec.Code, body.Results
}

func titles(rows []map[string]any) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, fmt.Sprintf("%s:%v", r["kind"], r["title"]))
	}
	return out
}

// The library is staff's: a requester, a read-only account and a personal API key (even an
// admin's) never reach it, and nobody signed out does.
func TestLibrarySearchIsStaffOnly(t *testing.T) {
	_, rt := searchAPI(t)
	for _, role := range []auth.Role{auth.RoleManager, auth.RoleAdmin} {
		if code, rows := searchAs(t, rt, role, "dune"); code != http.StatusOK || len(rows) == 0 {
			t.Errorf("%s: HTTP %d with %d rows, want 200 with results", role, code, len(rows))
		}
	}
	for _, role := range []auth.Role{auth.RoleRequester, auth.RoleReadonly} {
		if code, _ := searchAs(t, rt, role, "dune"); code != http.StatusForbidden {
			t.Errorf("%s: HTTP %d, want 403", role, code)
		}
	}
	if code, _ := searchAs(t, rt, "", "dune"); code != http.StatusUnauthorized {
		t.Errorf("anonymous: HTTP %d, want 401", code)
	}
	for _, scope := range []auth.KeyScope{auth.KeyScopeRead, auth.KeyScopeFull} {
		r := withKeyUse(httptest.NewRequest(http.MethodGet, "/api/v1/library/search?q=dune", nil),
			&auth.KeyUse{User: &auth.User{ID: 1, Username: "boss", Role: auth.RoleAdmin}, KeyID: 1, Scope: scope})
		rec := httptest.NewRecorder()
		rt.ServeHTTP(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Errorf("admin's %s API key: HTTP %d, want 403", scope, rec.Code)
		}
	}
	if apiKeyRoutes["GET /api/v1/library/search"] {
		t.Error("the library search must stay off the personal API key list")
	}
}

// Each row carries only what the palette draws: kind, id, title, year, a picture and,
// for a book or an album, whose it is.
func TestLibrarySearchResultShape(t *testing.T) {
	_, rt := searchAPI(t)
	_, rows := searchAs(t, rt, auth.RoleManager, "dune")
	allowed := map[string]bool{"kind": true, "id": true, "title": true, "year": true, "subtitle": true, "poster_url": true}
	for _, r := range rows {
		for k := range r {
			if !allowed[k] {
				t.Errorf("row %v carries %q", r, k)
			}
		}
	}
	first := rows[0]
	want := map[string]any{"kind": "movie", "id": float64(1), "title": "Dune", "year": float64(2021), "poster_url": "https://image.tmdb.org/t/p/w500/dune.jpg"}
	if !reflect.DeepEqual(first, want) {
		t.Errorf("first row = %v, want %v", first, want)
	}
	var book map[string]any
	for _, r := range rows {
		if r["kind"] == "book" {
			book = r
		}
	}
	if book == nil || book["subtitle"] != "Frank Herbert" || book["title"] != "Dune Messiah" {
		t.Errorf("book row = %v, want Dune Messiah by Frank Herbert", book)
	}
	// Music is off by default (a preview module): no artists or albums.
	for _, r := range rows {
		if r["kind"] == "artist" || r["kind"] == "album" {
			t.Errorf("music row %v while the module is off", r)
		}
	}
}

// Ranking: the exact title, then titles that start with it, then a word match, across
// kinds; a shorter title wins a tie.
func TestLibrarySearchRanking(t *testing.T) {
	_, rt := searchAPI(t)
	_, rows := searchAs(t, rt, auth.RoleManager, "dune")
	got := titles(rows)
	want := []string{"movie:Dune", "movie:Dune: Part Two", "series:Dune: Prophecy", "book:Dune Messiah"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dune = %v, want %v", got, want)
	}
	// Words in any order, each starting a word of the title.
	if _, rows := searchAs(t, rt, auth.RoleManager, "pets secret"); !reflect.DeepEqual(titles(rows), []string{"movie:The Secret Life of Pets"}) {
		t.Errorf("pets secret = %v", titles(rows))
	}
	// Letters inside a word still find it, last.
	if _, rows := searchAs(t, rt, auth.RoleManager, "ffice"); !reflect.DeepEqual(titles(rows), []string{"series:The Office"}) {
		t.Errorf("ffice = %v", titles(rows))
	}
}

// The one title normaliser: accents, case, punctuation and "&" against "and" don't matter.
func TestLibrarySearchNormalises(t *testing.T) {
	_, rt := searchAPI(t)
	for q, want := range map[string]string{
		"pokemon":           "movie:Pokémon: The First Movie",
		"POKÉMON the first": "movie:Pokémon: The First Movie",
		"amelie":            "movie:Amélie",
		"fast and furious":  "movie:Fast & Furious",
		"fast & furious":    "movie:Fast & Furious",
		"fast furious":      "movie:Fast & Furious",
		"the office":        "series:The Office",
		"office":            "series:The Office",
	} {
		_, rows := searchAs(t, rt, auth.RoleManager, q)
		if len(rows) == 0 || titles(rows)[0] != want {
			t.Errorf("%q = %v, want %s first", q, titles(rows), want)
		}
	}
	// Nothing to match on: an empty query, punctuation, or only "and".
	for _, q := range []string{"", "   ", "&", "and", "?!"} {
		if _, rows := searchAs(t, rt, auth.RoleManager, q); len(rows) != 0 {
			t.Errorf("%q = %v, want nothing", q, titles(rows))
		}
	}
}

// At most 20 rows however many match, fewer when asked, never more.
func TestLibrarySearchLimit(t *testing.T) {
	a, rt := searchAPI(t)
	db := a.deps.Store.DB()
	for i := 0; i < 40; i++ {
		if _, err := db.Exec(`INSERT INTO movies (tmdb_id, title, year) VALUES (?, ?, 2000)`, 1000+i, fmt.Sprintf("Star Voyage %02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, rows := searchAs(t, rt, auth.RoleManager, "star"); len(rows) != searchLimit {
		t.Errorf("star: %d rows, want %d", len(rows), searchLimit)
	}
	call := func(q string) int {
		r := asRole(httptest.NewRequest(http.MethodGet, "/api/v1/library/search?"+q, nil), auth.RoleManager)
		rec := httptest.NewRecorder()
		rt.ServeHTTP(rec, r)
		return strings.Count(rec.Body.String(), `"kind"`)
	}
	if n := call("q=star&limit=5"); n != 5 {
		t.Errorf("limit=5: %d rows", n)
	}
	if n := call("q=star&limit=500"); n != searchLimit {
		t.Errorf("limit=500: %d rows, want the cap %d", n, searchLimit)
	}
	// A very long paste is cut, not refused.
	if n := call("q=" + strings.Repeat("star", 200)); n != 0 {
		t.Errorf("long query: %d rows, want 0", n)
	}
}

// A module that's switched off keeps its titles out, and switching one on shows them
// without waiting out the cache.
func TestLibrarySearchFollowsModules(t *testing.T) {
	a, rt := searchAPI(t)
	ctx := context.Background()
	if _, rows := searchAs(t, rt, auth.RoleManager, "rats"); len(rows) != 0 {
		t.Fatalf("music off: %v", titles(rows))
	}
	if err := a.deps.Settings.Set(ctx, settings.KeyModuleMusic, "true"); err != nil {
		t.Fatal(err)
	}
	if _, rows := searchAs(t, rt, auth.RoleManager, "dune rats"); !reflect.DeepEqual(titles(rows), []string{"artist:Dune Rats"}) {
		t.Errorf("music on: %v", titles(rows))
	}
	_, rows := searchAs(t, rt, auth.RoleManager, "kids will know")
	if len(rows) != 1 || rows[0]["kind"] != "album" || rows[0]["subtitle"] != "Dune Rats" {
		t.Errorf("album = %v, want the album with its artist", rows)
	}
	if err := a.deps.Settings.Set(ctx, settings.KeyModuleBooks, "false"); err != nil {
		t.Fatal(err)
	}
	for _, r := range func() []map[string]any { _, rows := searchAs(t, rt, auth.RoleManager, "dune"); return rows }() {
		if r["kind"] == "book" {
			t.Errorf("books off, still found %v", r)
		}
	}
}

// A second name (a display title) matches like the title itself.
func TestMatchSearchUsesEveryName(t *testing.T) {
	entries := []searchEntry{
		newSearchEntry(searchHit{Kind: "series", ID: 1, Title: "Haus des Geldes"}, 1, "Haus des Geldes", "Money Heist"),
		newSearchEntry(searchHit{Kind: "movie", ID: 2, Title: "Money Monster"}, 0, "Money Monster"),
	}
	got := matchSearch(entries, "money heist", 20)
	if len(got) != 1 || got[0].ID != 1 {
		t.Errorf("money heist = %+v, want the show by its second name", got)
	}
}
