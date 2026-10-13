package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/jobs"
	"github.com/tristenlammi/arrmada/internal/movies"
)

// listJobs is a JobRunner that only lists: the Recent jobs handler reads, never submits.
type listJobs struct {
	rows []jobs.Job
	got  jobs.Filter
}

func (l *listJobs) Submit(context.Context, jobs.Spec) (int64, bool, error) { return 0, false, nil }
func (l *listJobs) Get(context.Context, int64) (jobs.Job, error)           { return jobs.Job{}, jobs.ErrNotFound }
func (l *listJobs) Cancel(context.Context, int64) error                    { return nil }
func (l *listJobs) List(_ context.Context, f jobs.Filter) ([]jobs.Job, error) {
	l.got = f
	return l.rows, nil
}

// Recent jobs come back in words: each linked to its movie (by its title), the person who
// started it named, a request linked, a running job's time so far, a failure's error, and
// a target that names nothing readable (a Kindle send's reference) left unshown.
func TestRecentJobsNamesTargetsAndTriggers(t *testing.T) {
	fj := &listJobs{}
	s := newRouteServer(t, func(d *Deps) {
		d.Jobs = fj
		d.Movies = movies.NewService(d.Store.DB(), nil, nil, t.TempDir(), "", nil, d.Log)
	})
	u, mgr := s.user(t, "sam", auth.RoleManager)
	m, err := movies.NewRepo(s.st.DB()).Create(context.Background(), movies.Movie{TMDBID: 1, Title: "Arrival", Year: 2016, Monitored: true, MinAvailability: "announced"})
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().Add(-time.Minute)
	t1 := t0.Add(1500 * time.Millisecond)
	fj.rows = []jobs.Job{
		{ID: 3, Kind: "movie.search", Target: jobTarget("movie", m.ID), Trigger: "user:" + itoa(int(u.ID)), Status: jobs.StatusSucceeded, CreatedAt: &t0, StartedAt: &t0, FinishedAt: &t1},
		{ID: 2, Kind: "book.search", Target: "book:9", Trigger: "request:41", Status: jobs.StatusFailed, Error: "no indexers", CreatedAt: &t0, StartedAt: &t0, FinishedAt: &t1},
		{ID: 1, Kind: "kindle.send", Target: "kindle:abc123", Trigger: "user:" + itoa(int(u.ID)), Status: jobs.StatusRunning, CreatedAt: &t0, StartedAt: &t0},
	}

	rec := s.do("GET", "/api/v1/system/jobs?limit=500", mgr)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d %s", rec.Code, rec.Body)
	}
	if fj.got.Limit != recentJobsMax {
		t.Errorf("limit passed on = %d, want the %d cap", fj.got.Limit, recentJobsMax)
	}
	var out struct {
		Jobs []recentJob `json:"jobs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Jobs) != 3 {
		t.Fatalf("decode: %v (%d rows)", err, len(out.Jobs))
	}
	mv, bk, ks := out.Jobs[0], out.Jobs[1], out.Jobs[2]
	if mv.Label != "Search for a movie" || mv.TargetLabel != "Arrival (2016)" || mv.TargetPath != "/movies/"+itoa(int(m.ID)) ||
		mv.TriggerLabel != "sam" || mv.DurationMS != 1500 {
		t.Errorf("movie row = %+v", mv)
	}
	if bk.TargetLabel != "Book #9" || bk.TargetPath != "/books/9" || bk.TriggerLabel != "Request #41" || bk.TriggerPath != "/requests?id=41" || bk.Error != "no indexers" {
		t.Errorf("book row = %+v", bk)
	}
	if ks.Label != "Send to Kindle" || ks.TargetLabel != "" || ks.TargetPath != "" || ks.DurationMS < 59_000 {
		t.Errorf("kindle row = %+v", ks)
	}

	// Staff only.
	_, req := s.user(t, "kid", auth.RoleRequester)
	if rec := s.do("GET", "/api/v1/system/jobs", req); rec.Code != http.StatusForbidden {
		t.Errorf("requester: HTTP %d, want 403", rec.Code)
	}
	if rec := s.do("GET", "/api/v1/system/jobs?limit=0", mgr); rec.Code != http.StatusBadRequest {
		t.Errorf("limit=0: HTTP %d, want 400", rec.Code)
	}
}

func TestJobKindLabelFallsBackToWords(t *testing.T) {
	if got := jobKindLabel("convert.group_retry"); got != "Convert group retry" {
		t.Errorf("fallback = %q", got)
	}
	if got := jobKindLabel("task"); got != "Run now" {
		t.Errorf("task = %q", got)
	}
}
