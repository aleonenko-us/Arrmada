package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/eventbus"
	"github.com/tristenlammi/arrmada/internal/jobs"
	"github.com/tristenlammi/arrmada/internal/movies"
)

// organizeServer is a temp library with a job runner; add films with addFilm.
type organizeServer struct {
	*routeServer
	root   string
	runner *jobs.Runner
	mgr    *http.Cookie
}

func newOrganizeServer(t *testing.T) *organizeServer {
	t.Helper()
	root := t.TempDir()
	var runner *jobs.Runner
	s := newRouteServer(t, func(d *Deps) {
		d.Bus = eventbus.New(d.Log)
		d.Movies = movies.NewService(d.Store.DB(), nil, nil, root, t.TempDir(), d.Bus, d.Log)
		r, err := jobs.New(context.Background(), d.Store.DB(), d.Log, nil)
		if err != nil {
			t.Fatal(err)
		}
		runner = r
		d.Jobs = r
	})
	t.Cleanup(func() { runner.Shutdown(time.Second) })
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	return &organizeServer{routeServer: s, root: root, runner: runner, mgr: mgr}
}

// addFilm records a film whose file is at rel (under the library root), written with body.
func (s *organizeServer) addFilm(t *testing.T, tmdb int, title string, year int, rel, body string) (int64, string) {
	t.Helper()
	res, err := s.st.DB().Exec(`INSERT INTO movies (tmdb_id, title, year, monitored) VALUES (?, ?, ?, 1)`, tmdb, title, year)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	p := filepath.Join(s.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.st.DB().Exec(`UPDATE movies SET has_file = 1, movie_file_path = ? WHERE id = ?`, p, id); err != nil {
		t.Fatal(err)
	}
	return id, p
}

type organizePreview struct {
	Counts organizeCounts      `json:"counts"`
	Items  []movies.RenameItem `json:"items"`
	Total  int                 `json:"total"`
	Token  string              `json:"token"`
}

func (s *organizeServer) preview(t *testing.T, query string) organizePreview {
	t.Helper()
	rec := s.do("GET", "/api/v1/movies/organize"+query, s.mgr)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: HTTP %d: %s", rec.Code, rec.Body)
	}
	var p organizePreview
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func fileBody(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

// Films already named to the scheme list nothing to rename, and the preview moves nothing.
func TestOrganizePreviewNoChangesWhenSchemeMatches(t *testing.T) {
	s := newOrganizeServer(t)
	_, p := s.addFilm(t, 1, "Heat", 1995, filepath.Join("Heat (1995)", "Heat (1995).mkv"), "heat")
	got := s.preview(t, "")
	if got.Counts.Rename != 0 || got.Counts.Same != 1 || len(got.Items) != 0 || got.Total != 0 {
		t.Fatalf("preview = %+v, want one film already named and nothing listed", got)
	}
	if fileBody(t, p) != "heat" {
		t.Error("the preview must not touch the file")
	}
}

// A film whose new name is taken by another file is listed as a conflict.
func TestOrganizePreviewDetectsConflict(t *testing.T) {
	s := newOrganizeServer(t)
	s.addFilm(t, 1, "Heat", 1995, filepath.Join("Heat (1995)", "heat.1995.1080p.bluray.mkv"), "heat")
	taken := filepath.Join(s.root, "Heat (1995)", "Heat (1995) - 1080p BluRay.mkv")
	if err := os.WriteFile(taken, []byte("someone else"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := s.preview(t, "")
	if got.Counts.Conflict != 1 || got.Counts.Rename != 0 {
		t.Fatalf("counts = %+v, want one conflict", got.Counts)
	}
	if len(got.Items) != 1 || got.Items[0].Status != movies.RenameConflict || got.Items[0].Proposed != taken {
		t.Fatalf("items = %+v", got.Items)
	}
}

// Apply refuses a stale token outright, and with a current one renames only the clean
// items, skipping the conflict, in a background job.
func TestOrganizeApplySkipsConflictsAndStaleToken(t *testing.T) {
	s := newOrganizeServer(t)
	heatID, heat := s.addFilm(t, 1, "Heat", 1995, filepath.Join("heat.1995", "heat.1995.1080p.bluray.mkv"), "heat")
	_, ronin := s.addFilm(t, 2, "Ronin", 1998, filepath.Join("Ronin (1998)", "ronin.1998.720p.bluray.mkv"), "ronin")
	taken := filepath.Join(s.root, "Ronin (1998)", "Ronin (1998) - 720p BluRay.mkv")
	if err := os.WriteFile(taken, []byte("someone else"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := s.preview(t, "")
	if got.Counts.Rename != 1 || got.Counts.Conflict != 1 {
		t.Fatalf("counts = %+v, want one rename and one conflict", got.Counts)
	}

	rec := s.doJSON("POST", "/api/v1/movies/organize", s.mgr, `{"token":"stale"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale token: HTTP %d, want 409", rec.Code)
	}
	if fileBody(t, heat) != "heat" {
		t.Fatal("a refused apply must move nothing")
	}

	rec = s.doJSON("POST", "/api/v1/movies/organize", s.mgr, `{"token":"`+got.Token+`"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("apply: HTTP %d: %s", rec.Code, rec.Body)
	}
	var started struct {
		JobID int64 `json:"job_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil || started.JobID == 0 {
		t.Fatalf("apply reply %s: %v", rec.Body, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.runner.Wait(ctx, started.JobID); err != nil {
		t.Fatal(err)
	}
	job, err := s.runner.Get(ctx, started.JobID)
	if err != nil {
		t.Fatal(err)
	}
	var sum organizeSummary
	if err := json.Unmarshal(job.Result, &sum); err != nil {
		t.Fatalf("job %+v: %v", job, err)
	}
	if sum.Renamed != 1 || sum.Skipped != 0 || sum.Total != 1 {
		t.Fatalf("summary = %+v, want the one clean rename", sum)
	}
	m, err := s.deps.Movies.Get(ctx, heatID)
	if err != nil {
		t.Fatal(err)
	}
	if m.MovieFilePath == heat || fileBody(t, m.MovieFilePath) != "heat" {
		t.Errorf("Heat is at %q, want it renamed", m.MovieFilePath)
	}
	if _, err := os.Stat(filepath.Dir(heat)); !os.IsNotExist(err) {
		t.Error("Heat's emptied old folder should be gone")
	}
	if fileBody(t, ronin) != "ronin" || fileBody(t, taken) != "someone else" {
		t.Error("the conflict must leave both files alone")
	}

	// The same token again is stale now: the plan changed.
	rec = s.doJSON("POST", "/api/v1/movies/organize", s.mgr, `{"token":"`+got.Token+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("reused token: HTTP %d, want 409", rec.Code)
	}
}

// A selection previews only its own films.
func TestOrganizePreviewScopedToIDs(t *testing.T) {
	s := newOrganizeServer(t)
	heatID, _ := s.addFilm(t, 1, "Heat", 1995, filepath.Join("heat.1995", "heat.1995.1080p.bluray.mkv"), "heat")
	s.addFilm(t, 2, "Ronin", 1998, filepath.Join("ronin", "ronin.1998.720p.bluray.mkv"), "ronin")
	got := s.preview(t, "?ids="+jsonInt(heatID))
	if got.Counts.Rename != 1 || len(got.Items) != 1 || got.Items[0].MovieID != heatID {
		t.Fatalf("preview = %+v, want only Heat", got)
	}
	rec := s.do("GET", "/api/v1/movies/organize?ids=abc", s.mgr)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad ids: HTTP %d, want 400", rec.Code)
	}
}

func jsonInt(n int64) string { b, _ := json.Marshal(n); return string(b) }
