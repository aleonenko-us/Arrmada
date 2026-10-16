package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/jobs"
)

// unmanagedFilm is a finished torrent Arrmada didn't grab — holding a (sparse) film, or
// only a text file — and the movie it can be imported into.
func (e *unmanagedEnv) unmanagedFilm(t *testing.T, hash string, withFilm bool) {
	t.Helper()
	src := filepath.Join(e.a.deps.Config.DownloadsDir, "Some.Film.2001.1080p.BluRay-GRP")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if withFilm {
		f, err := os.Create(filepath.Join(src, "Some.Film.2001.1080p.BluRay-GRP.mkv"))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(60 << 20); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	} else if err := os.WriteFile(filepath.Join(src, "readme.txt"), []byte("no film here"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.DB().Exec(`INSERT INTO movies (id, tmdb_id, title, year, monitored, min_availability) VALUES (1, 1, 'Some Film', 2001, 1, 'announced')`); err != nil {
		t.Fatal(err)
	}
	e.set(doneTorrent(hash, "Some.Film.2001.1080p.BluRay-GRP", "", src))
}

func (e *unmanagedEnv) listBody(t *testing.T) string {
	t.Helper()
	rec := httptest.NewRecorder()
	e.a.handleUnmanaged(rec, httptest.NewRequest(http.MethodGet, "/api/v1/downloads/unmanaged", nil))
	return rec.Body.String()
}

// Import into… answers 202 with a job; a second click joins it. While it runs the list
// carries the job (a reloaded page finds it) and Ignore is refused.
func TestImportUnmanagedIsAJob(t *testing.T) {
	e := newUnmanagedEnv(t)
	fj := newFakeJobs(false)
	e.a.deps.Jobs = fj
	e.unmanagedFilm(t, strangerA, true)

	rec := e.post(t, e.a.handleImportUnmanaged, "/", strangerA, `{"kind":"movie","target_id":1}`)
	first := decodeAccepted(t, rec.Body.String())
	if rec.Code != http.StatusAccepted || first.JobID == 0 || first.Existing || first.Title != "Some Film" {
		t.Fatalf("import = %d %s", rec.Code, rec.Body.String())
	}
	rec = e.post(t, e.a.handleImportUnmanaged, "/", strangerA, `{"kind":"movie","target_id":1}`)
	if again := decodeAccepted(t, rec.Body.String()); rec.Code != http.StatusAccepted || again.JobID != first.JobID || !again.Existing || again.Title != "Some Film" {
		t.Fatalf("second click = %d %+v, want the same job", rec.Code, again)
	}
	// Into another film while it runs: refused, naming what it's importing into.
	if _, err := e.st.DB().Exec(`INSERT INTO movies (id, tmdb_id, title, year, monitored, min_availability) VALUES (2, 2, 'Other Film', 2002, 1, 'announced')`); err != nil {
		t.Fatal(err)
	}
	rec = e.post(t, e.a.handleImportUnmanaged, "/", strangerA, `{"kind":"movie","target_id":2}`)
	if busy := decodeAccepted(t, rec.Body.String()); rec.Code != http.StatusConflict || busy.JobID != first.JobID || busy.Title != "Some Film" {
		t.Fatalf("a different import mid-import = %d %s, want 409", rec.Code, rec.Body.String())
	}
	if specs := fj.specs(); len(specs) != 1 || specs[0].Kind != downloadImportKind || specs[0].Target != "torrent:"+strangerA || specs[0].Class != jobs.ClassImport {
		t.Fatalf("specs = %+v", specs)
	}
	if body := e.listBody(t); !strings.Contains(body, `"import_job":{"id":`+fmt.Sprint(first.JobID)) {
		t.Errorf("the list doesn't carry the running import: %s", body)
	}
	if rec := e.post(t, e.a.handleIgnoreDownload, "/", strangerA, ""); rec.Code != http.StatusConflict {
		t.Errorf("ignore mid-import = %d %s, want 409", rec.Code, rec.Body.String())
	}
}

// A refusal that can be told up front is answered at once and starts no job.
func TestImportUnmanagedRefusalsAreImmediate(t *testing.T) {
	e := newUnmanagedEnv(t)
	fj := newFakeJobs(false)
	e.a.deps.Jobs = fj
	e.unmanagedFilm(t, strangerA, true)
	// Series isn't wired here: its module is off.
	if rec := e.post(t, e.a.handleImportUnmanaged, "/", strangerA, `{"kind":"series","target_id":1}`); rec.Code != http.StatusConflict {
		t.Errorf("module off = %d %s, want 409", rec.Code, rec.Body.String())
	}
	if rec := e.post(t, e.a.handleImportUnmanaged, "/", strangerB, `{"kind":"movie","target_id":1}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown torrent = %d, want 404", rec.Code)
	}
	if n := len(fj.specs()); n != 0 {
		t.Errorf("refusals started %d job(s)", n)
	}
}

// An import that fails inside the job fails it with the import's reason, leaves the
// torrent on the list with that job (so the card shows why), and History lists it.
func TestImportUnmanagedJobFailure(t *testing.T) {
	e := newUnmanagedEnv(t)
	e.unmanagedFilm(t, strangerA, false)
	j := e.importJob(t, e.post(t, e.a.handleImportUnmanaged, "/", strangerA, `{"kind":"movie","target_id":1}`))
	if j.Status != jobs.StatusFailed || j.Error == "" {
		t.Fatalf("job = %+v", j)
	}
	if body := e.listBody(t); !strings.Contains(body, `"status":"failed"`) {
		t.Errorf("the list doesn't carry the failed import: %s", body)
	}
	var detail string
	if err := e.st.DB().QueryRow(`SELECT detail FROM movie_events WHERE movie_id = 1 AND event = 'failed'`).Scan(&detail); err != nil || !strings.Contains(detail, "Couldn't import from Needs you") {
		t.Errorf("history failure line = %q, %v", detail, err)
	}
}
