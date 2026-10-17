package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/eventbus"
	"github.com/tristenlammi/arrmada/internal/jobs"
	"github.com/tristenlammi/arrmada/internal/libfolders"
	"github.com/tristenlammi/arrmada/internal/libroots"
	"github.com/tristenlammi/arrmada/internal/series"
)

// scanRootServer is a route server with real temp Movies and TV folders and extra ones:
// Kids (Movies, one film folder in it), Anime (TV, one show in it) and Cartoons (TV,
// empty). A real series service that recognises nothing sits behind the TV scan.
type scanRootServer struct {
	*routeServer
	base                  string
	kids, anime, cartoons libfolders.Folder
	// runner is the real job runner, when jr was nil.
	runner *jobs.Runner
}

// newScanRootServer records jobs with jr, or runs them on a real runner when jr is nil.
func newScanRootServer(t *testing.T, jr JobRunner) scanRootServer {
	t.Helper()
	out := scanRootServer{base: t.TempDir()}
	for _, d := range []string{"movies", "tv", "kids/Film (2020)", "anime", "cartoons"} {
		if err := os.MkdirAll(filepath.Join(out.base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// One show in the main TV folder and one in Anime, each with an episode file.
	for _, p := range []string{"tv/Main Show (2019)/Season 01/Main Show - S01E01.mkv", "anime/Anime Show (2021)/Season 01/Anime Show - S01E01.mkv"} {
		sparseVideo(t, filepath.Join(out.base, p))
	}
	out.routeServer = newRouteServer(t, func(d *Deps) {
		d.Bus = eventbus.New(d.Log)
		d.Jobs = jr
		if jr == nil {
			r, err := jobs.New(context.Background(), d.Store.DB(), d.Log, d.Bus)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { r.Shutdown(2 * time.Second) })
			out.runner, d.Jobs = r, r
		}
		d.Config.MoviesDir, d.Config.TVDir = filepath.Join(out.base, "movies"), filepath.Join(out.base, "tv")
		roots := libroots.New(d.Settings.Get, d.Config)
		d.Folders = libfolders.New(d.Store.DB(), roots, d.Config.DataDir)
		roots.WithExtras(d.Folders.Extras)
		add := func(media, dir string) libfolders.Folder {
			f, err := d.Folders.Add(context.Background(), libfolders.AddInput{Media: media, Path: filepath.Join(out.base, dir)})
			if err != nil {
				t.Fatal(err)
			}
			return f
		}
		out.kids = add(libfolders.MediaMovie, "kids")
		out.anime = add(libfolders.MediaSeries, "anime")
		out.cartoons = add(libfolders.MediaSeries, "cartoons")
		svc := series.NewService(d.Store.DB(), noSeriesMeta{}, d.Config.TVDir, d.Log)
		svc.SetFolders(d.Folders)
		d.Series = svc
	})
	return out
}

// sparseVideo writes a file big enough to count as an episode, using no disk.
func sparseVideo(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Truncate(200 << 20)
	_ = f.Close()
}

func rootBody(root string) string {
	b, _ := json.Marshal(map[string]string{"root": root})
	return string(b)
}

// A scan can be asked for one library folder (the folder just added in Settings →
// Library): staff only, only one of that library's folders, never one that's missing or
// looks unmounted, and its job is named by the folder's id, never its path.
func TestScanOneFolder(t *testing.T) {
	fj := newFakeJobs(false)
	s := newScanRootServer(t, fj)
	_, kid := s.user(t, "kid@example.com", auth.RoleRequester)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)

	if rec := s.doBody("POST", "/api/v1/movies/scan", rootBody(s.kids.Path), kid); rec.Code != http.StatusForbidden {
		t.Errorf("requester: HTTP %d, want 403", rec.Code)
	}
	// Not one of the Movies folders: a TV folder, the data folder, a folder elsewhere.
	for _, root := range []string{s.anime.Path, s.deps.Config.DataDir, filepath.Join(s.base, "kids", "Film (2020)"), "/"} {
		if rec := s.doBody("POST", "/api/v1/movies/scan", rootBody(root), mgr); rec.Code != http.StatusBadRequest {
			t.Errorf("root %s: HTTP %d %s, want 400", root, rec.Code, rec.Body)
		}
	}
	if rec := s.doBody("POST", "/api/v1/series/scan", rootBody(s.kids.Path), mgr); rec.Code != http.StatusBadRequest {
		t.Errorf("a Movies folder as a TV root: HTTP %d, want 400", rec.Code)
	}
	// Empty (a share that isn't mounted looks like this) and missing: refused, saying so.
	rec := s.doBody("POST", "/api/v1/series/scan", rootBody(s.cartoons.Path), mgr)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "nothing to scan") {
		t.Errorf("empty folder: HTTP %d %s, want 409", rec.Code, rec.Body)
	}
	if n := len(fj.specs()); n != 0 {
		t.Fatalf("%d scans submitted by refused requests", n)
	}

	rec = s.doBody("POST", "/api/v1/movies/scan", rootBody(s.kids.Path+"/"), mgr)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("scan of Kids: HTTP %d %s", rec.Code, rec.Body)
	}
	specs := fj.specs()
	if len(specs) != 1 || specs[0].Kind != "movie.scan" || specs[0].Target != "folder:"+itoa64(s.kids.ID) {
		t.Fatalf("submitted %+v, want one movie.scan of folder:%d", specs, s.kids.ID)
	}
	// A whole-library scan is a separate job (it isn't "already running").
	if rec := s.do("POST", "/api/v1/movies/scan", mgr); rec.Code != http.StatusAccepted || fj.specs()[1].Target != "all" {
		t.Errorf("full scan: HTTP %d, target %q", rec.Code, fj.specs()[len(fj.specs())-1].Target)
	}
	// The same folder again follows the running one.
	rec = s.doBody("POST", "/api/v1/movies/scan", rootBody(s.kids.Path), mgr)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "a scan of kids is already running") {
		t.Errorf("second scan of Kids: HTTP %d %s", rec.Code, rec.Body)
	}

	if err := os.RemoveAll(s.kids.Path); err != nil { // the Kids share goes away
		t.Fatal(err)
	}
	rec = s.doBody("POST", "/api/v1/movies/scan", rootBody(s.kids.Path), mgr)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "can't be found") {
		t.Errorf("missing folder: HTTP %d %s, want 409", rec.Code, rec.Body)
	}
}

// A scan of one TV folder reads only that folder, and leaves the other folders' review
// rows as they were.
func TestScanOneFolderScansOnlyIt(t *testing.T) {
	s := newScanRootServer(t, nil)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	// run starts a scan and waits for its job to end.
	run := func(body string) jobs.Job {
		t.Helper()
		rec := s.doBody("POST", "/api/v1/series/scan", body, mgr)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("scan %s: HTTP %d %s", body, rec.Code, rec.Body)
		}
		var started struct {
			JobID int64 `json:"job_id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &started)
		ctx, stop := context.WithTimeout(context.Background(), 60*time.Second) // a ceiling for slow -race runners
		defer stop()
		if err := s.runner.Wait(ctx, started.JobID); err != nil {
			t.Fatalf("scan job never finished: %v", err)
		}
		job, err := s.runner.Get(context.Background(), started.JobID)
		if err != nil || job.Status != jobs.StatusSucceeded {
			t.Fatalf("scan job = %+v, %v", job, err)
		}
		return job
	}
	run("")
	if got := len(s.deps.Series.LastUnmatched()); got != 2 {
		t.Fatalf("after the full scan %d folders need review, want 2 (one per folder)", got)
	}
	sparseVideo(t, filepath.Join(s.anime.Path, "Second Show (2022)", "Second Show - S01E01.mkv"))

	job := run(rootBody(s.anime.Path))
	var sum scanSummary
	_ = json.Unmarshal(job.Result, &sum)
	if job.Target != "folder:"+itoa64(s.anime.ID) || sum.Unmatched != 2 {
		t.Errorf("job %+v, result %+v; want Anime's two shows only", job, sum)
	}
	roots := map[string]int{}
	for _, u := range s.deps.Series.LastUnmatched() {
		roots[filepath.Clean(u.Root)]++
	}
	if roots[filepath.Clean(s.anime.Path)] != 2 || roots[filepath.Clean(s.deps.Config.TVDir)] != 1 || len(roots) != 2 {
		t.Errorf("needs review by folder = %v, want Anime's 2 and the main folder's 1 kept", roots)
	}
}

// Settings → Library asks how much each extra folder already holds (?disk=1): a count
// when it's there, "empty" (never counted: an unmounted share looks so) or "missing".
// Add dialogs, which don't ask, never touch the disk.
func TestLibraryFoldersDisk(t *testing.T) {
	s := newScanRootServer(t, newFakeJobs(false))
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	if err := os.RemoveAll(filepath.Join(s.base, "cartoons")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.kids.Path, ".arrmada-recycle"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.anime.Path, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	read := func(q string) foldersBody {
		rec := s.do("GET", "/api/v1/library/folders"+q, mgr)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET: HTTP %d %s", rec.Code, rec.Body)
		}
		var got foldersBody
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	plain := read("")
	for _, f := range append(plain.Movie.Folders, plain.Series.Folders...) {
		if f.Disk != "" || f.OnDisk != 0 {
			t.Errorf("without ?disk=1, %s = %q/%d", f.Label, f.Disk, f.OnDisk)
		}
	}
	got := read("?disk=1")
	byID := map[int64]folderView{}
	for _, f := range append(got.Movie.Folders, got.Series.Folders...) {
		byID[f.ID] = f
	}
	if f := byID[s.kids.ID]; f.Disk != diskOK || f.OnDisk != 1 {
		t.Errorf("Kids = %q/%d, want ok/1 (the bin isn't counted)", f.Disk, f.OnDisk)
	}
	if f := byID[s.anime.ID]; f.Disk != diskOK || f.OnDisk != 1 {
		t.Errorf("Anime = %q/%d, want ok/1 (show folders only)", f.Disk, f.OnDisk)
	}
	if f := byID[s.cartoons.ID]; f.Disk != diskMissing || f.OnDisk != 0 {
		t.Errorf("Cartoons = %q/%d, want missing", f.Disk, f.OnDisk)
	}
	if err := os.MkdirAll(filepath.Join(s.base, "cartoons", ".arrmada-recycle"), 0o755); err != nil {
		t.Fatal(err)
	}
	got = read("?disk=1")
	for _, f := range got.Series.Folders {
		if f.ID == s.cartoons.ID && (f.Disk != diskEmpty || f.OnDisk != 0) {
			t.Errorf("Cartoons with only a bin = %q/%d, want empty", f.Disk, f.OnDisk)
		}
		if f.Default && f.Disk != "" {
			t.Errorf("the main TV folder = %q, want it left to Media folders", f.Disk)
		}
	}
}
