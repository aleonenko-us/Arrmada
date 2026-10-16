package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/jobs"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/series"
)

// addArrival records the film reviews in these tests are grabbed for.
func addArrival(t *testing.T, s *routeServer) int64 {
	t.Helper()
	res, err := s.st.DB().Exec(`INSERT INTO movies (tmdb_id, title, year, monitored) VALUES (1, 'Arrival', 2016, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

// heldFilm is a download folder holding one (sparse) film of size bytes.
func heldFilm(t *testing.T, size int64) string {
	t.Helper()
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "Arrival.2016.1080p.BluRay.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	return dir
}

type acceptedBody struct {
	Status   string `json:"status"`
	JobID    int64  `json:"job_id"`
	Existing bool   `json:"existing"`
	Title    string `json:"title"`
}

func decodeAccepted(t *testing.T, body string) acceptedBody {
	t.Helper()
	var b acceptedBody
	if err := json.Unmarshal([]byte(body), &b); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return b
}

// Import anyway answers 202 with a job at once; a second click, or Choose different…
// on the same review, joins that job instead of starting another import over the same
// files. While it runs the list says so, and Reject / Dismiss are refused — Reject would
// delete the files being copied.
func TestImportReviewIsAJob(t *testing.T) {
	s, fj, mgr := reviewServer(t)
	mid := addArrival(t, s)
	rid := addReviewRow(t, s, "aa", "movie", mid, "mismatch", heldFilm(t, 1<<20))

	rec := s.doJSON("POST", fmt.Sprintf("/api/v1/reviews/%d/import", rid), mgr, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("import: HTTP %d: %s", rec.Code, rec.Body)
	}
	first := decodeAccepted(t, rec.Body.String())
	if first.JobID == 0 || first.Existing || first.Status != "importing" || first.Title != "Arrival" {
		t.Fatalf("import answer = %+v", first)
	}
	specs := fj.specs()
	if len(specs) != 1 || specs[0].Kind != reviewImportKind || specs[0].Target != fmt.Sprintf("review:%d", rid) ||
		specs[0].Class != jobs.ClassImport || specs[0].Timeout < time.Hour {
		t.Fatalf("spec = %+v", specs)
	}

	for _, body := range []string{"", fmt.Sprintf(`{"target_id": %d, "target_kind": "movie"}`, mid)} {
		rec = s.doJSON("POST", fmt.Sprintf("/api/v1/reviews/%d/import", rid), mgr, body)
		again := decodeAccepted(t, rec.Body.String())
		if rec.Code != http.StatusAccepted || again.JobID != first.JobID || !again.Existing {
			t.Fatalf("second import (%q): HTTP %d %+v, want the same job", body, rec.Code, again)
		}
	}
	if n := len(fj.specs()); n != 1 {
		t.Fatalf("%d jobs submitted, want 1", n)
	}

	rec = s.doJSON("GET", "/api/v1/reviews", mgr, "")
	var list struct {
		Reviews []struct {
			ID        int64     `json:"id"`
			Name      string    `json:"name"`
			ImportJob *jobs.Job `json:"import_job"`
		} `json:"reviews"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Reviews) != 1 || list.Reviews[0].ImportJob == nil || list.Reviews[0].ImportJob.ID != first.JobID || list.Reviews[0].Name == "" {
		t.Fatalf("list = %s", rec.Body)
	}

	for _, action := range []string{"reject", "dismiss"} {
		rec = s.doJSON("POST", fmt.Sprintf("/api/v1/reviews/%d/%s", rid, action), mgr, "")
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "being imported") {
			t.Errorf("%s while importing: HTTP %d %s", action, rec.Code, rec.Body)
		}
	}
	rec = s.doJSON("POST", "/api/v1/reviews/bulk", mgr, fmt.Sprintf(`{"ids": [%d], "action": "reject"}`, rid))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "being imported") || !strings.Contains(rec.Body.String(), `"done":0`) {
		t.Errorf("bulk reject while importing: HTTP %d %s", rec.Code, rec.Body)
	}
	var status string
	_ = s.st.DB().QueryRow(`SELECT status FROM import_reviews WHERE id = ?`, rid).Scan(&status)
	if status != "pending" {
		t.Errorf("review status = %q, want pending", status)
	}
}

// What can be refused before the import runs is still answered at once, with the status
// it always had, and starts no job.
func TestImportReviewRefusalsAreImmediate(t *testing.T) {
	s, fj, mgr := reviewServer(t)
	mid := addArrival(t, s)
	gone := addReviewRow(t, s, "g1", "movie", mid, "mismatch", filepath.Join(t.TempDir(), "removed"))
	noTarget := addReviewRow(t, s, "n1", "movie", 0, "unmatched", t.TempDir())
	off := addReviewRow(t, s, "o1", "series", 4, "numbering", t.TempDir()) // series isn't wired here
	ok := addReviewRow(t, s, "k1", "movie", mid, "mismatch", t.TempDir())

	for _, c := range []struct {
		name string
		id   int64
		body string
		want int
	}{
		{"missing review", 9999, "", http.StatusNotFound},
		{"download gone", gone, "", http.StatusGone},
		{"no target", noTarget, "", http.StatusUnprocessableEntity},
		{"wrong kind", ok, `{"target_id": 3, "target_kind": "book"}`, http.StatusUnprocessableEntity},
		{"module off", off, "", http.StatusConflict},
	} {
		rec := s.doJSON("POST", fmt.Sprintf("/api/v1/reviews/%d/import", c.id), mgr, c.body)
		if rec.Code != c.want {
			t.Errorf("%s: HTTP %d %s, want %d", c.name, rec.Code, rec.Body, c.want)
		}
	}
	if n := len(fj.specs()); n != 0 {
		t.Errorf("refusals started %d job(s)", n)
	}
}

// reviewJobServer runs review imports on a real job runner.
func reviewJobServer(t *testing.T) (*routeServer, *jobs.Runner, *http.Cookie) {
	t.Helper()
	var runner *jobs.Runner
	s := newRouteServer(t, func(d *Deps) {
		mv := movies.NewService(d.Store.DB(), nil, nil, t.TempDir(), "", nil, d.Log)
		d.Movies = mv
		d.Automation = automation.New(mv, nil, nil, nil, d.Store.DB(), nil, d.Log, "")
		r, err := jobs.New(context.Background(), d.Store.DB(), d.Log, nil)
		if err != nil {
			t.Fatal(err)
		}
		runner = r
		d.Jobs = r
	})
	t.Cleanup(func() { runner.Shutdown(5 * time.Second) })
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	return s, runner, mgr
}

func waitReviewJob(t *testing.T, r *jobs.Runner, id int64) jobs.Job {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := r.Wait(ctx, id); err != nil {
		t.Fatal(err)
	}
	j, err := r.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func historyEvents(t *testing.T, s *routeServer, c *http.Cookie) []historyEvent {
	t.Helper()
	rec := s.doJSON("GET", "/api/v1/history?kind=movie", c, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("history: HTTP %d %s", rec.Code, rec.Body)
	}
	var page struct {
		Events []historyEvent `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	return page.Events
}

// The job does the import: a copy onto another drive lands the film, settles the review
// and leaves "Imported into Arrival." as its message, and History has the import.
func TestImportReviewJobSucceeds(t *testing.T) {
	defer library.ForceCopies()()
	s, runner, mgr := reviewJobServer(t)
	mid := addArrival(t, s)
	rid := addReviewRow(t, s, "aa", "movie", mid, "mismatch", heldFilm(t, 60<<20))

	rec := s.doJSON("POST", fmt.Sprintf("/api/v1/reviews/%d/import", rid), mgr, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("import: HTTP %d %s", rec.Code, rec.Body)
	}
	j := waitReviewJob(t, runner, decodeAccepted(t, rec.Body.String()).JobID)
	if j.Status != jobs.StatusSucceeded || j.Message != "Imported into Arrival." || j.Progress != 1 {
		t.Fatalf("job = %+v", j)
	}
	var status string
	_ = s.st.DB().QueryRow(`SELECT status FROM import_reviews WHERE id = ?`, rid).Scan(&status)
	if status != "resolved" {
		t.Errorf("review status = %q, want resolved", status)
	}
	found := false
	for _, ev := range historyEvents(t, s, mgr) {
		if ev.ItemID == mid && ev.Category == evImported {
			found = true
		}
	}
	if !found {
		t.Error("History has no import for the film")
	}
	// A settled review lists nothing, and a new import of it is a plain 404.
	if rec := s.doJSON("POST", fmt.Sprintf("/api/v1/reviews/%d/import", rid), mgr, ""); rec.Code != http.StatusNotFound {
		t.Errorf("import of a settled review: HTTP %d", rec.Code)
	}
}

// An import that fails inside the job fails the job with the reason in plain words, keeps
// the review (with that job on it, so a reloaded page shows the error), and History lists
// the failure under Failed.
func TestImportReviewJobFailure(t *testing.T) {
	s, runner, mgr := reviewJobServer(t)
	mid := addArrival(t, s)
	dl := t.TempDir()
	if err := os.WriteFile(filepath.Join(dl, "readme.txt"), []byte("no film here"), 0o644); err != nil {
		t.Fatal(err)
	}
	rid := addReviewRow(t, s, "aa", "movie", mid, "mismatch", dl)

	rec := s.doJSON("POST", fmt.Sprintf("/api/v1/reviews/%d/import", rid), mgr, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("import: HTTP %d %s", rec.Code, rec.Body)
	}
	j := waitReviewJob(t, runner, decodeAccepted(t, rec.Body.String()).JobID)
	if j.Status != jobs.StatusFailed || j.Error == "" {
		t.Fatalf("job = %+v", j)
	}
	rec = s.doJSON("GET", "/api/v1/reviews", mgr, "")
	if !strings.Contains(rec.Body.String(), `"status":"failed"`) || !strings.Contains(rec.Body.String(), "import_job") {
		t.Errorf("the review doesn't carry its failed import: %s", rec.Body)
	}
	var failed *historyEvent
	for _, ev := range historyEvents(t, s, mgr) {
		if ev.ItemID == mid && ev.Category == evFailed {
			failed = &ev
		}
	}
	if failed == nil || !strings.Contains(failed.Detail, "Couldn't import from review") {
		t.Fatalf("History failure line = %+v", failed)
	}
	// The import is over: Dismiss works again.
	if rec := s.doJSON("POST", fmt.Sprintf("/api/v1/reviews/%d/dismiss", rid), mgr, ""); rec.Code != http.StatusOK {
		t.Errorf("dismiss after a failed import: HTTP %d %s", rec.Code, rec.Body)
	}
}

// The card's progress line: gigabytes copied of the download's size, short of full until
// the import itself finishes.
func TestCopyProgressMessage(t *testing.T) {
	pct, msg := copyProgress(12_300<<20, 48<<30)
	if msg != "Copying… 12.0 of 48.0 GB" {
		t.Errorf("message = %q", msg)
	}
	if pct <= 0.2 || pct >= 0.95 {
		t.Errorf("pct = %v", pct)
	}
	if pct, _ := copyProgress(48<<30, 48<<30); pct != 0.95 {
		t.Errorf("a finished copy is %v, want 0.95 (the import isn't done yet)", pct)
	}
	if pct, msg := copyProgress(5, 0); pct != 0 || !strings.HasPrefix(msg, "Copying… 0.0 of 0.0 GB") {
		t.Errorf("no total: %v %q", pct, msg)
	}
}

// Map files… imports as a job however few files it maps, under the same single-flight as
// Import anyway: an import asked for while the mapping runs joins it.
func TestMapReviewIsAJob(t *testing.T) {
	base := t.TempDir()
	downloads := filepath.Join(base, "downloads")
	fj := newFakeJobs(false)
	s := newRouteServer(t, func(d *Deps) {
		d.Config.DownloadsDir = downloads
		co := automation.New(nil, nil, nil, nil, d.Store.DB(), nil, d.Log, "")
		co.SetSeries(series.NewService(d.Store.DB(), nil, filepath.Join(base, "tv"), d.Log), library.NewImporter(filepath.Join(base, "tv"), d.Log))
		d.Automation = co
		d.Jobs = fj
	})
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	res, err := s.st.DB().Exec(`INSERT INTO series (tmdb_id, title, year, monitored) VALUES (7, 'The Bear', 2022, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	sid, _ := res.LastInsertId()
	if _, err := s.st.DB().Exec(`INSERT INTO episodes (series_id, season_number, episode_number, monitored) VALUES (?, 1, 1, 1)`, sid); err != nil {
		t.Fatal(err)
	}
	pack := filepath.Join(downloads, "The.Bear.S01")
	if err := os.MkdirAll(pack, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "bear-one.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rid := addReviewRow(t, s, "aa", "series", sid, "numbering", pack)
	body := `{"files":[{"rel_path":"bear-one.mkv","season":1,"episodes":[1]}]}`

	rec := s.doJSON("POST", fmt.Sprintf("/api/v1/reviews/%d/map", rid), mgr, body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("map: HTTP %d %s", rec.Code, rec.Body)
	}
	first := decodeAccepted(t, rec.Body.String())
	if first.JobID == 0 || first.Existing || first.Title != "The Bear" {
		t.Fatalf("map answer = %+v", first)
	}
	rec = s.doJSON("POST", fmt.Sprintf("/api/v1/reviews/%d/import", rid), mgr, "")
	if again := decodeAccepted(t, rec.Body.String()); rec.Code != http.StatusAccepted || again.JobID != first.JobID || !again.Existing {
		t.Fatalf("import during the mapping: HTTP %d %+v", rec.Code, again)
	}
	if specs := fj.specs(); len(specs) != 1 || specs[0].Kind != reviewImportKind {
		t.Fatalf("specs = %+v", specs)
	}
}
