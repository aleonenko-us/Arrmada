package subtitles

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

// orphanFixture is a Service over a real movies module: each movie is a video file in its
// own temp folder with the extra files named beside it. A recycle bin lives in a temp dir,
// and the ladder's seams point at the movie being processed.
type orphanFixture struct {
	s      *Service
	st     *store.Store
	videos map[int64]string
	bin    string
	ai     *fakeAI
}

func newOrphanFixture(t *testing.T, langs string) *orphanFixture {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	set := settings.NewService(st.DB())
	if err := set.Set(context.Background(), keyLanguages, langs); err != nil {
		t.Fatal(err)
	}
	f := &orphanFixture{st: st, videos: map[int64]string{}, bin: filepath.Join(t.TempDir(), "bin"), ai: &fakeAI{avail: true, transcribe: true}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	f.s = &Service{settings: set, log: log, wake: make(chan struct{}, 1), ai: f.ai,
		movies: movies.NewService(st.DB(), nil, nil, t.TempDir(), "", nil, log), ledger: NewLedger(st.DB())}
	f.s.SetBin(library.SingleBin(f.bin))
	f.s.probe = func(context.Context, string) (*mediaInfo, error) { return englishAudio(), nil }
	f.s.resolve = func(_ context.Context, j *Job) (fileRef, bool) {
		v, ok := f.videos[j.MovieID]
		return fileRef{Path: v, Title: j.Title, MediaKey: j.key()}, ok
	}
	return f
}

// movie adds movie id with its video (first name) and the other files in one folder.
func (f *orphanFixture) movie(t *testing.T, id int64, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(fakeSRT), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	video := filepath.Join(dir, names[0])
	if _, err := f.st.DB().Exec(`INSERT INTO movies (id, tmdb_id, title, monitored, has_file, movie_file_path) VALUES (?, ?, ?, 1, 1, ?)`,
		id, 100+id, names[0], video); err != nil {
		t.Fatal(err)
	}
	f.videos[id] = video
	return video
}

// pass records a completed library pass over the fixture's movies, as Rescan would.
func (f *orphanFixture) pass(t *testing.T) {
	t.Helper()
	f.s.snap.mu.Lock()
	f.s.snap.at = time.Now()
	f.s.snap.mu.Unlock()
	for id := range f.videos {
		m, err := f.s.movies.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		f.s.patchMovie(context.Background(), m)
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestAdoptOrphanRenames(t *testing.T) {
	f := newOrphanFixture(t, "en")
	video := f.movie(t, 1, "Anchorman (2004) Bluray-2160p.mkv", "Anchorman.eng.srt")
	f.pass(t)

	name, err := f.s.AdoptOrphan(context.Background(), 1, "Anchorman.eng.srt", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if name != "Anchorman (2004) Bluray-2160p.en.srt" {
		t.Errorf("adopted as %q", name)
	}
	dir := filepath.Dir(video)
	if exists(filepath.Join(dir, "Anchorman.eng.srt")) || !exists(filepath.Join(dir, name)) {
		t.Error("the orphan wasn't renamed into place")
	}
	if p := scanSidecars(video, []string{"en"}, "movie").Present; !reflect.DeepEqual(p, []string{"en"}) {
		t.Errorf("present after adopt = %v, want [en]", p)
	}
	// Arrmada owns it now: the ledger has its live row, as adopted.
	if row, ok, err := f.s.Ledger().LiveFile(context.Background(), filepath.Join(dir, name)); err != nil || !ok ||
		row.Source != SourceAdopted || row.Lang != "en" || row.MediaKey != "m:1" || row.MovieID != 1 {
		t.Errorf("ledger row = %+v ok=%v err=%v, want a live adopted en row", row, ok, err)
	}
	// The Library entry turns ✓ without waiting for a pass.
	list, _ := f.s.SnapshotMovies()
	if len(list) != 1 || list[0].Missing != 0 || len(list[0].Orphans) != 0 {
		t.Errorf("snapshot after adopt = %+v", list)
	}
}

// An explicit language and variant name the file; an untagged orphan takes the first
// kept language; the extension is kept.
func TestAdoptOrphanLangAndVariant(t *testing.T) {
	f := newOrphanFixture(t, "en,es")
	video := f.movie(t, 1, "Heat (1995).mkv", "Heat.srt", "heat-signs.ass")
	if name, err := f.s.AdoptOrphan(context.Background(), 1, "Heat.srt", nil, nil); err != nil || name != "Heat (1995).en.srt" {
		t.Errorf("untagged: %q %v", name, err)
	}
	es, forced := "spa", "forced"
	if name, err := f.s.AdoptOrphan(context.Background(), 1, "heat-signs.ass", &es, &forced); err != nil || name != "Heat (1995).es.forced.ass" {
		t.Errorf("explicit: %q %v", name, err)
	}
	if !exists(filepath.Join(filepath.Dir(video), "Heat (1995).es.forced.ass")) {
		t.Error("explicit adopt didn't land")
	}
}

func TestAdoptRefusesExistingTarget(t *testing.T) {
	f := newOrphanFixture(t, "en")
	video := f.movie(t, 1, "Anchorman (2004) Bluray-2160p.mkv", "Anchorman.eng.srt", "Anchorman (2004) Bluray-2160p.en.srt")
	if err := os.WriteFile(sidecarPath(video, "en"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := f.s.AdoptOrphan(context.Background(), 1, "Anchorman.eng.srt", nil, nil)
	if !errors.Is(err, ErrOrphanTargetExists) {
		t.Fatalf("err = %v, want ErrOrphanTargetExists", err)
	}
	if b, _ := os.ReadFile(sidecarPath(video, "en")); string(b) != "mine" {
		t.Error("the paired subtitle was overwritten")
	}
	if !exists(filepath.Join(filepath.Dir(video), "Anchorman.eng.srt")) {
		t.Error("the orphan moved anyway")
	}
}

func TestAdoptRejectsNonOrphan(t *testing.T) {
	f := newOrphanFixture(t, "en")
	f.movie(t, 1, "Anchorman (2004) Bluray-2160p.mkv", "Anchorman.eng.srt", "Anchorman (2004) Bluray-2160p.es.srt")
	for _, name := range []string{"Anchorman (2004) Bluray-2160p.es.srt", "../Anchorman.eng.srt", "nope.srt", "Anchorman (2004) Bluray-2160p.mkv", ""} {
		if _, err := f.s.AdoptOrphan(context.Background(), 1, name, nil, nil); !errors.Is(err, ErrNotOrphan) {
			t.Errorf("adopt %q: err = %v, want ErrNotOrphan", name, err)
		}
	}
	for _, lang := range []string{"../x", "en/..", "zz", "english language"} {
		l := lang
		if _, err := f.s.AdoptOrphan(context.Background(), 1, "Anchorman.eng.srt", &l, nil); !errors.Is(err, ErrBadLanguage) {
			t.Errorf("lang %q: err = %v, want ErrBadLanguage", lang, err)
		}
	}
	bad := "../../x"
	if _, err := f.s.AdoptOrphan(context.Background(), 1, "Anchorman.eng.srt", nil, &bad); !errors.Is(err, ErrBadLanguage) {
		t.Errorf("variant: err = %v, want ErrBadLanguage", err)
	}
	if _, err := f.s.AdoptOrphan(context.Background(), 99, "Anchorman.eng.srt", nil, nil); !errors.Is(err, movies.ErrNotFound) {
		t.Errorf("unknown movie: err = %v", err)
	}
}

// runQueued pops and processes the next job.
func (f *orphanFixture) runQueued() *Job {
	job := f.s.pop()
	if job != nil {
		f.s.process(context.Background(), job)
	}
	return job
}

func TestReplaceRetiresOnlyOnSuccess(t *testing.T) {
	// The language is made: the orphan goes to the recycle bin, not away for good.
	f := newOrphanFixture(t, "en")
	video := f.movie(t, 1, "Anchorman (2004) Bluray-2160p.mkv", "Anchorman.eng.srt")
	job, err := f.s.ReplaceOrphan(context.Background(), 1, "Anchorman.eng.srt")
	if err != nil {
		t.Fatal(err)
	}
	if job.Priority != PrioManual {
		t.Errorf("priority %d, want manual", job.Priority)
	}
	if exists(filepath.Join(filepath.Dir(video), "Anchorman.eng.srt")) == false {
		t.Fatal("the orphan moved before anything was made")
	}
	f.runQueued()
	if job.State != StateDone {
		t.Fatalf("state=%q note=%q", job.State, job.Note)
	}
	assertSidecar(t, video, "en")
	if exists(filepath.Join(filepath.Dir(video), "Anchorman.eng.srt")) {
		t.Error("the orphan is still there after its language was made")
	}
	binned, _ := filepath.Glob(filepath.Join(f.bin, "*", "Anchorman.eng.srt"))
	if len(binned) != 1 {
		t.Errorf("the orphan isn't in the recycle bin (found %v)", binned)
	}

	// Nothing is made: the orphan stays.
	f2 := newOrphanFixture(t, "en")
	f2.ai.avail = false
	video2 := f2.movie(t, 1, "Anchorman (2004) Bluray-2160p.mkv", "Anchorman.eng.srt")
	if _, err := f2.s.ReplaceOrphan(context.Background(), 1, "Anchorman.eng.srt"); err != nil {
		t.Fatal(err)
	}
	f2.runQueued()
	if !exists(filepath.Join(filepath.Dir(video2), "Anchorman.eng.srt")) {
		t.Error("the orphan was retired though nothing replaced it")
	}

	// The bin refuses: the orphan stays even though the new subtitle was written.
	f3 := newOrphanFixture(t, "en")
	f3.s.bin = nil
	video3 := f3.movie(t, 1, "Anchorman (2004) Bluray-2160p.mkv", "Anchorman.eng.srt")
	if _, err := f3.s.ReplaceOrphan(context.Background(), 1, "Anchorman.eng.srt"); err != nil {
		t.Fatal(err)
	}
	f3.runQueued()
	if !exists(filepath.Join(filepath.Dir(video3), "Anchorman.eng.srt")) {
		t.Error("the orphan was removed with no recycle bin to take it")
	}
}

func TestReplaceRefusals(t *testing.T) {
	f := newOrphanFixture(t, "en")
	f.movie(t, 1, "Heat (1995).mkv", "Heat.de.srt", "Heat.en.forced.srt", "Heat.eng.srt")
	if _, err := f.s.ReplaceOrphan(context.Background(), 1, "Heat.de.srt"); !errors.Is(err, ErrNotKept) {
		t.Errorf("not kept: %v", err)
	}
	if _, err := f.s.ReplaceOrphan(context.Background(), 1, "Heat.en.forced.srt"); !errors.Is(err, ErrForcedOrphan) {
		t.Errorf("forced: %v", err)
	}
	// A job already running for the movie can't take the orphan on.
	running := f.s.enqueue(&Job{Kind: "movie", MovieID: 1, Title: "Heat"})
	f.s.pop()
	f.s.update(running, func(j *Job) { j.State = StateRunning })
	if _, err := f.s.ReplaceOrphan(context.Background(), 1, "Heat.eng.srt"); !errors.Is(err, ErrJobBusy) {
		t.Errorf("running job: %v", err)
	}
}

// A replace on a movie the sweep already queued joins that job and moves it up.
func TestReplaceJoinsQueuedSweepJob(t *testing.T) {
	f := newOrphanFixture(t, "en")
	f.movie(t, 1, "Heat (1995).mkv", "Heat.eng.srt")
	sweep := f.s.enqueue(&Job{Kind: "movie", MovieID: 1, Title: "Heat", Priority: PrioSweep})
	job, err := f.s.ReplaceOrphan(context.Background(), 1, "Heat.eng.srt")
	if err != nil || job != sweep || job.Priority != PrioManual || len(job.retire) != 1 {
		t.Fatalf("job=%p sweep=%p prio=%d retire=%v err=%v", job, sweep, job.Priority, job.retire, err)
	}
}

func TestBulkAdoptSkipsAmbiguous(t *testing.T) {
	f := newOrphanFixture(t, "en")
	a := f.movie(t, 1, "Alien (1979).mkv", "Alien.eng.srt", "alien.en.srt")
	b := f.movie(t, 2, "Blade Runner (1982).mkv", "Blade.Runner.eng.srt")
	c := f.movie(t, 3, "Casablanca (1942).mkv", "Casablanca.srt")
	f.pass(t)
	res, err := f.s.BulkOrphans(context.Background(), "adopt", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Done != 1 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v, want one adopted", res)
	}
	if !exists(sidecarPath(b, "en")) {
		t.Error("the unambiguous orphan wasn't adopted")
	}
	if exists(sidecarPath(a, "en")) || exists(sidecarPath(c, "en")) {
		t.Error("an ambiguous or untagged orphan was adopted")
	}
	reasons := map[string]string{}
	for _, sk := range res.Skipped {
		reasons[sk.Name] = sk.Reason
	}
	if len(reasons) != 3 || reasons["Alien.eng.srt"] == "" || reasons["alien.en.srt"] == "" || reasons["Casablanca.srt"] == "" {
		t.Errorf("skipped = %+v, want both Alien orphans and the untagged one with reasons", res.Skipped)
	}

	// Only the listed movies are touched.
	f2 := newOrphanFixture(t, "en")
	x := f2.movie(t, 1, "X (2000).mkv", "X.eng.srt")
	y := f2.movie(t, 2, "Y (2000).mkv", "Y.eng.srt")
	f2.pass(t)
	if res, err := f2.s.BulkOrphans(context.Background(), "adopt", []int64{2}); err != nil || res.Done != 1 {
		t.Fatalf("listed adopt: %+v %v", res, err)
	}
	if exists(sidecarPath(x, "en")) || !exists(sidecarPath(y, "en")) {
		t.Error("bulk adopt touched a movie it wasn't given")
	}
}

func TestBulkReplaceQueuesKeptLanguages(t *testing.T) {
	f := newOrphanFixture(t, "en")
	f.movie(t, 1, "Heat (1995).mkv", "Heat.eng.srt", "Heat.de.srt")
	f.pass(t)
	res, err := f.s.BulkOrphans(context.Background(), "replace", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Done != 1 || len(res.Skipped) != 1 || res.Skipped[0].Name != "Heat.de.srt" {
		t.Fatalf("result = %+v", res)
	}
	jobs := f.s.Jobs()
	if len(jobs) != 1 || jobs[0].Priority != PrioManual {
		t.Fatalf("jobs = %+v, want one manual job", jobs)
	}
}
