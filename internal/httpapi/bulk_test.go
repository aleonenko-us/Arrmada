package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/eventbus"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/quality"
)

// bulkServer is a temp library with a quality service, the coordinator and a job runner
// that records the searches queued without running them.
type bulkServer struct {
	*routeServer
	root string
	jobs *fakeJobs
	mgr  *http.Cookie
}

func newBulkServer(t *testing.T, bin string) *bulkServer {
	t.Helper()
	root := t.TempDir()
	fj := newFakeJobs(false)
	s := newRouteServer(t, func(d *Deps) {
		d.Bus = eventbus.New(d.Log)
		d.Quality = quality.NewService(d.Store.DB())
		d.Movies = movies.NewService(d.Store.DB(), nil, nil, root, bin, d.Bus, d.Log)
		d.Automation = automation.New(d.Movies, nil, nil, d.Quality, d.Store.DB(), d.Bus, d.Log, "")
		d.Jobs = fj
	})
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	return &bulkServer{routeServer: s, root: root, jobs: fj, mgr: mgr}
}

// profile creates a movie profile and returns its reference.
func (s *bulkServer) profile(t *testing.T, sp quality.StoredProfile) string {
	t.Helper()
	sp.MediaType = quality.MediaMovie
	got, err := s.deps.Quality.Create(context.Background(), sp)
	if err != nil {
		t.Fatal(err)
	}
	return "custom:" + strconv.FormatInt(got.ID, 10)
}

// film adds a movie; release != "" gives it a file named for that release.
func (s *bulkServer) film(t *testing.T, title string, monitored bool, profile, release string) int64 {
	t.Helper()
	res, err := s.st.DB().Exec(`INSERT INTO movies (tmdb_id, title, year, monitored, quality_profile, min_availability)
		VALUES ((SELECT COALESCE(MAX(tmdb_id), 0) + 1 FROM movies), ?, 2021, ?, ?, 'released')`, title, monitored, profile)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if release != "" {
		p := filepath.Join(s.root, title, release+".mkv")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(title), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := s.st.DB().Exec(`UPDATE movies SET has_file = 1, movie_file_path = ?, source_release = ? WHERE id = ?`, p, release, id); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func (s *bulkServer) movie(t *testing.T, id int64) movies.Movie {
	t.Helper()
	m, err := s.deps.Movies.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// An unknown profile, or a film that's no longer in the library, rejects the whole request
// and changes nothing — not even the fields that were fine.
func TestBulkUpdateTransactional(t *testing.T) {
	s := newBulkServer(t, t.TempDir())
	hd := s.profile(t, quality.StoredProfile{Name: "HD", AllowedResolutions: []string{"1080p"}})
	a := s.film(t, "Alpha", false, hd, "")
	b := s.film(t, "Bravo", false, hd, "")

	rec := s.doJSON("PUT", "/api/v1/movies/bulk", s.mgr, fmt.Sprintf(`{"ids":[%d,%d],"monitored":true,"quality_profile":"custom:9999"}`, a, b))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown profile: HTTP %d, want 400", rec.Code)
	}
	rec = s.doJSON("PUT", "/api/v1/movies/bulk", s.mgr, fmt.Sprintf(`{"ids":[%d,%d,99999],"monitored":true,"min_availability":"announced"}`, a, b))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a missing film: HTTP %d (%s), want 404", rec.Code, rec.Body)
	}
	rec = s.doJSON("PUT", "/api/v1/movies/bulk", s.mgr, fmt.Sprintf(`{"ids":[%d],"min_availability":"someday"}`, a))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad availability: HTTP %d, want 400", rec.Code)
	}
	for _, id := range []int64{a, b} {
		if m := s.movie(t, id); m.Monitored || m.MinAvailability != "released" {
			t.Errorf("movie %d changed by a refused request: monitored=%v availability=%s", id, m.Monitored, m.MinAvailability)
		}
	}

	// A good request changes every film at once.
	rec = s.doJSON("PUT", "/api/v1/movies/bulk", s.mgr, fmt.Sprintf(`{"ids":[%d,%d],"monitored":true,"min_availability":"announced"}`, a, b))
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
	}
	for _, id := range []int64{a, b} {
		if m := s.movie(t, id); !m.Monitored || m.MinAvailability != "announced" {
			t.Errorf("movie %d: monitored=%v availability=%s, want both changed", id, m.Monitored, m.MinAvailability)
		}
	}
	if len(s.jobs.submitted) != 0 {
		t.Errorf("search none queued %d jobs", len(s.jobs.submitted))
	}
}

// After a profile change: a monitored film with no file is searched for, one whose file
// still fits gets an upgrade search, one the profile no longer accepts is listed (and left
// alone), and an unmonitored one is neither.
func TestBulkUpdateClassifiesItems(t *testing.T) {
	s := newBulkServer(t, t.TempDir())
	old := s.profile(t, quality.StoredProfile{Name: "Any"})
	hd := s.profile(t, quality.StoredProfile{Name: "HD", AllowedResolutions: []string{"1080p"}, UpgradesEnabled: true})
	missing := s.film(t, "Missing", true, old, "")
	fits := s.film(t, "Fits", true, old, "Fits.2021.1080p.BluRay.x264-GRP")
	big := s.film(t, "Big", true, old, "Big.2021.2160p.BluRay.x265-GRP")
	idle := s.film(t, "Idle", false, old, "")

	body := fmt.Sprintf(`{"ids":[%d,%d,%d,%d],"quality_profile":%q,"search":"missing_and_upgrade"}`, missing, fits, big, idle, hd)
	rec := s.doJSON("PUT", "/api/v1/movies/bulk", s.mgr, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		Updated        int `json:"updated"`
		Queued         int `json:"queued"`
		NotMonitored   int `json:"not_monitored"`
		ExceedsProfile []struct {
			ID     int64  `json:"id"`
			Title  string `json:"title"`
			Reason string `json:"reason"`
		} `json:"exceeds_profile"`
		Errors []bulkItemError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Updated != 4 || got.Queued != 2 || got.NotMonitored != 1 || len(got.Errors) != 0 {
		t.Fatalf("reply = %+v", got)
	}
	if len(got.ExceedsProfile) != 1 || got.ExceedsProfile[0].ID != big || got.ExceedsProfile[0].Reason == "" {
		t.Fatalf("exceeds_profile = %+v, want only Big, with a reason", got.ExceedsProfile)
	}
	var kinds []string
	for _, sp := range s.jobs.submitted {
		kinds = append(kinds, sp.Kind+" "+sp.Target)
	}
	sort.Strings(kinds)
	want := []string{fmt.Sprintf("movie.search movie:%d", missing), fmt.Sprintf("movie.upgrade movie:%d", fits)}
	sort.Strings(want)
	if fmt.Sprint(kinds) != fmt.Sprint(want) {
		t.Fatalf("queued %v, want %v", kinds, want)
	}
	for _, id := range []int64{missing, fits, big, idle} {
		if m := s.movie(t, id); m.QualityProfile != hd {
			t.Errorf("movie %d profile = %s, want %s", id, m.QualityProfile, hd)
		}
	}

	// "Download smaller versions" queues a regrab for the listed film.
	rec = s.doJSON("POST", "/api/v1/movies/bulk/regrab", s.mgr, fmt.Sprintf(`{"ids":[%d]}`, big))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("regrab: HTTP %d: %s", rec.Code, rec.Body)
	}
	last := s.jobs.submitted[len(s.jobs.submitted)-1]
	if last.Kind != "movie.regrab" || last.Target != fmt.Sprintf("movie:%d", big) {
		t.Fatalf("regrab queued %s %s", last.Kind, last.Target)
	}
}

type bulkDeleteReply struct {
	Deleted int                `json:"deleted"`
	Failed  int                `json:"failed"`
	Results []bulkDeleteResult `json:"results"`
}

func (s *bulkServer) bulkDelete(t *testing.T, body string) bulkDeleteReply {
	t.Helper()
	rec := s.doJSON("DELETE", "/api/v1/movies/bulk", s.mgr, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
	}
	var got bulkDeleteReply
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// Each film is deleted as the movie page would delete it, and the reply says what
// happened to each; files go to the recycle bin.
func TestBulkDeletePerItemResults(t *testing.T) {
	bin := t.TempDir()
	s := newBulkServer(t, bin)
	a := s.film(t, "Alpha", true, "", "Alpha.2021.1080p.BluRay-GRP")
	b := s.film(t, "Bravo", true, "", "")
	fileA := s.movie(t, a).MovieFilePath

	got := s.bulkDelete(t, fmt.Sprintf(`{"ids":[%d,%d,99999],"delete_files":true}`, a, b))
	if got.Deleted != 2 || got.Failed != 1 || len(got.Results) != 3 {
		t.Fatalf("reply = %+v", got)
	}
	if !got.Results[0].Deleted || !got.Results[1].Deleted || got.Results[2].Deleted || got.Results[2].Error == "" {
		t.Fatalf("results = %+v", got.Results)
	}
	if _, err := os.Stat(fileA); !os.IsNotExist(err) {
		t.Error("Alpha's file should have left the library")
	}
	found := false
	_ = filepath.WalkDir(bin, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Base(p) == filepath.Base(fileA) {
			found = true
		}
		return nil
	})
	if !found {
		t.Error("Alpha's file should be in the recycle bin")
	}
}

// When the bin refuses a file, that film stays, the rest aren't tried, and no file is
// deleted for good.
func TestBulkDeleteStopsWhenTheBinRefuses(t *testing.T) {
	broken := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(broken, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newBulkServer(t, filepath.Join(broken, "bin"))
	a := s.film(t, "Alpha", true, "", "Alpha.2021.1080p.BluRay-GRP")
	b := s.film(t, "Bravo", true, "", "Bravo.2021.1080p.BluRay-GRP")
	fileA, fileB := s.movie(t, a).MovieFilePath, s.movie(t, b).MovieFilePath

	got := s.bulkDelete(t, fmt.Sprintf(`{"ids":[%d,%d],"delete_files":true}`, a, b))
	if got.Deleted != 0 || got.Results[0].Deleted || got.Results[1].Deleted {
		t.Fatalf("reply = %+v, want nothing deleted", got)
	}
	if got.Results[1].Error == "" {
		t.Error("the second film should say it wasn't tried")
	}
	for _, p := range []string{fileA, fileB} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s must still be on disk: %v", p, err)
		}
	}
	for _, id := range []int64{a, b} {
		if _, err := s.deps.Movies.Get(context.Background(), id); err != nil {
			t.Errorf("movie %d must still be in the library: %v", id, err)
		}
	}
}
