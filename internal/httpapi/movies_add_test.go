package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/quality"
)

// addMeta is a metadata provider that knows a fixed set of films; it counts fetches.
type addMeta struct {
	films   []metadata.MovieResult
	fetches *int
}

func (m addMeta) Available() bool { return true }
func (m addMeta) SearchMovie(_ context.Context, q string) ([]metadata.MovieResult, error) {
	var out []metadata.MovieResult
	for _, f := range m.films {
		if strings.Contains(strings.ToLower(f.Title), strings.ToLower(q)) {
			out = append(out, f)
		}
	}
	return out, nil
}
func (m addMeta) GetMovie(_ context.Context, id int) (*metadata.MovieDetails, error) {
	*m.fetches++
	for _, f := range m.films {
		if f.TMDBID == id {
			return &metadata.MovieDetails{MovieResult: f, Status: "Released"}, nil
		}
	}
	return nil, errors.New("tmdb: not found")
}
func (m addMeta) GetCollection(context.Context, int) (*metadata.Collection, error) {
	return nil, errors.New("no collections")
}

func addServer(t *testing.T, fj *fakeJobs, fetches *int) *routeServer {
	t.Helper()
	meta := addMeta{fetches: fetches, films: []metadata.MovieResult{
		{TMDBID: 1, Title: "Saga One", Year: 2001}, {TMDBID: 2, Title: "Saga Two", Year: 2003},
		{TMDBID: 3, Title: "Saga Three", Year: 2005}, {TMDBID: 4, Title: "Other Film", Year: 2010},
	}}
	return newRouteServer(t, func(d *Deps) {
		db := d.Store.DB()
		mv := movies.NewService(db, meta, nil, t.TempDir(), "", nil, d.Log)
		dl := download.NewService(db, d.Log)
		d.Movies, d.Downloads, d.Quality = mv, dl, quality.NewService(db)
		d.Automation = automation.New(mv, nil, dl, nil, db, nil, d.Log, "")
		d.Jobs = fj
	})
}

// Search results already in the library say so and carry the film's id; the rest don't.
func TestLookupAnnotatesInLibrary(t *testing.T) {
	fetches := 0
	s := addServer(t, newFakeJobs(false), &fetches)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	have, err := s.deps.Movies.Add(context.Background(), 2, "hd", true)
	if err != nil {
		t.Fatal(err)
	}
	rec := s.do("GET", "/api/v1/movies/lookup?q=saga", mgr)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
	}
	var body struct {
		Results []struct {
			TMDBID    int   `json:"tmdb_id"`
			InLibrary bool  `json:"in_library"`
			LibraryID int64 `json:"library_id"`
		} `json:"results"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Results) != 3 {
		t.Fatalf("results = %+v", body.Results)
	}
	for _, r := range body.Results {
		if (r.TMDBID == 2) != r.InLibrary || (r.TMDBID == 2 && r.LibraryID != have.ID) || (r.TMDBID != 2 && r.LibraryID != 0) {
			t.Errorf("result %+v", r)
		}
	}
}

// The availability chosen when adding is stored; a value the sweeps don't know is refused;
// and adding never touches the global "search on add" default.
func TestAddMovieMinAvailability(t *testing.T) {
	fetches := 0
	s := addServer(t, newFakeJobs(false), &fetches)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	ctx := context.Background()
	if err := s.deps.Settings.Set(ctx, keySearchOnAdd, "true"); err != nil {
		t.Fatal(err)
	}
	rec := s.doJSON("POST", "/api/v1/movies", mgr, `{"tmdb_id":1,"quality_profile":"hd","min_availability":"announced","search_on_add":false}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
	}
	m, err := s.deps.Movies.GetByTMDB(ctx, 1)
	if err != nil || m.MinAvailability != "announced" || m.Monitored {
		t.Fatalf("added = %+v (%v), want announced and unmonitored (search off)", m, err)
	}
	if !s.deps.Settings.GetBool(ctx, keySearchOnAdd, false) {
		t.Fatal("a per-add choice must not change the global search-on-add default")
	}
	if rec := s.doJSON("POST", "/api/v1/movies", mgr, `{"tmdb_id":3,"min_availability":"soon"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad availability: HTTP %d", rec.Code)
	}
	if _, err := s.deps.Movies.GetByTMDB(ctx, 3); !errors.Is(err, movies.ErrNotFound) {
		t.Fatal("a refused add must not add the film")
	}
	// Left out, it's the default (released).
	if rec := s.doJSON("POST", "/api/v1/movies", mgr, `{"tmdb_id":3}`); rec.Code != http.StatusCreated {
		t.Fatalf("HTTP %d", rec.Code)
	}
	if m, _ := s.deps.Movies.GetByTMDB(ctx, 3); m.MinAvailability != "released" {
		t.Fatalf("default availability = %q", m.MinAvailability)
	}
}

// A collection's "Add all" is one call: films already in the library are skipped (with
// their id), the rest are added with the chosen settings, a bad id fails on its own, and
// each new film's search is queued in the movie search queue, not run in the request.
func TestBulkAddSkipsExisting(t *testing.T) {
	fetches := 0
	fj := newFakeJobs(false) // queued jobs never run
	s := addServer(t, fj, &fetches)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	ctx := context.Background()
	have, err := s.deps.Movies.Add(ctx, 2, "hd", true)
	if err != nil {
		t.Fatal(err)
	}
	fetches = 0
	rec := s.doJSON("POST", "/api/v1/movies/bulk-add", mgr,
		`{"tmdb_ids":[1,2,3,999,1],"quality_profile":"hd","min_availability":"inCinemas","search":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
	}
	var body struct {
		Results                 []bulkAddResult `json:"results"`
		Added, Existing, Failed int
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Results) != 4 || body.Added != 2 || body.Existing != 1 || body.Failed != 1 {
		t.Fatalf("answer = %+v", body)
	}
	want := map[int]string{1: "added", 2: "exists", 3: "added", 999: "failed"}
	for _, r := range body.Results {
		if r.Status != want[r.TMDBID] {
			t.Errorf("tmdb %d: %+v, want %s", r.TMDBID, r, want[r.TMDBID])
		}
		if r.TMDBID == 2 && r.ID != have.ID {
			t.Errorf("existing film's id = %d, want %d", r.ID, have.ID)
		}
		if r.Status == "added" && (!r.Queued || r.ID == 0) {
			t.Errorf("added film %+v should have its search queued", r)
		}
	}
	if fetches != 3 { // 1, 3 and the unknown 999; never the film already there, never the repeat
		t.Errorf("%d metadata fetches, want 3", fetches)
	}
	for _, id := range []int{1, 3} {
		m, err := s.deps.Movies.GetByTMDB(ctx, id)
		if err != nil || !m.Monitored || m.QualityProfile != "hd" || m.MinAvailability != "inCinemas" {
			t.Errorf("film %d = %+v (%v)", id, m, err)
		}
	}
	searches := 0
	for _, sp := range fj.specs() {
		if sp.Kind == "movie.search" {
			searches++
		}
	}
	if searches != 2 {
		t.Errorf("%d searches submitted, want 2 (queued, not run)", searches)
	}

	// Search off: added unmonitored, nothing queued; the global default is untouched.
	if err := s.deps.Settings.Set(ctx, keySearchOnAdd, "true"); err != nil {
		t.Fatal(err)
	}
	rec = s.doJSON("POST", "/api/v1/movies/bulk-add", mgr, `{"tmdb_ids":[4],"search":false}`)
	body.Results = nil // decode afresh: omitted fields mustn't keep the last answer's values
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusOK || body.Added != 1 || body.Results[0].Queued {
		t.Fatalf("search off: HTTP %d %+v", rec.Code, body)
	}
	if m, _ := s.deps.Movies.GetByTMDB(ctx, 4); m.Monitored {
		t.Error("search off should add unmonitored")
	}

	// Refusals.
	for _, bad := range []string{`{"tmdb_ids":[]}`, `{"tmdb_ids":[0]}`, `{"tmdb_ids":[1],"min_availability":"later"}`} {
		if rec := s.doJSON("POST", "/api/v1/movies/bulk-add", mgr, bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: HTTP %d, want 400", bad, rec.Code)
		}
	}
	// Staff only.
	_, kid := s.user(t, "kid@example.com", auth.RoleRequester)
	if rec := s.doJSON("POST", "/api/v1/movies/bulk-add", kid, `{"tmdb_ids":[1]}`); rec.Code != http.StatusForbidden {
		t.Errorf("requester: HTTP %d, want 403", rec.Code)
	}
}
