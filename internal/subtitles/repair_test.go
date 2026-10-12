package subtitles

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

// srtOf makes an SRT with n cues.
func srtOf(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		at := time.Duration(i) * 3 * time.Second
		fmt.Fprintf(&b, "%d\n%s --> %s\nLine %d.\n\n", i+1, fmtSRTTime(at), fmtSRTTime(at+2*time.Second), i)
	}
	return b.String()
}

// forcedFirstInfo is the old bug's file: the first English text track is the forced one,
// the full track comes after it.
func forcedFirstInfo() *mediaInfo {
	mi := englishAudio()
	mi.Subs = []SubTrack{
		{Index: 0, Codec: "subrip", Lang: "eng", Text: true, Forced: true},
		{Index: 1, Codec: "subrip", Lang: "eng", Text: true},
	}
	return mi
}

// repairFixture is a ladder fixture whose extract seam writes a 900-cue SRT, with a
// recycle bin in a temp dir.
func repairFixture(t *testing.T) (*Service, string, *[]extractPick) {
	t.Helper()
	s, video := ladderFixture(t, "en", forcedFirstInfo(), nil, &fakeAI{})
	var picks []extractPick
	s.extract = func(_ context.Context, _ string, p []extractPick) error {
		picks = append(picks, p...)
		for _, pk := range p {
			if err := os.WriteFile(pk.Path, []byte(srtOf(900)), 0o644); err != nil {
				return err
			}
		}
		return nil
	}
	s.SetBin(library.SingleBin(filepath.Join(t.TempDir(), "bin")))
	return s, video, &picks
}

func cuesIn(t *testing.T, path string) int {
	t.Helper()
	n, ok := sidecarCues(path)
	if !ok {
		t.Fatalf("%s is missing", filepath.Base(path))
	}
	return n
}

func runRepairJob(s *Service, langs ...string) *Job {
	job := s.enqueue(&Job{Kind: "movie", MovieID: 1, Title: "Movie", Mode: ModeRepairForced, Priority: PrioSweep, repairLangs: langs})
	s.pop()
	s.process(context.Background(), job)
	return job
}

// The acceptance case: a 12-cue sidecar extracted from the forced track is swapped for
// the 900-cue full track, and the short one is kept as the forced sidecar.
func TestForcedRepairSwapsShortSidecar(t *testing.T) {
	s, video, picks := repairFixture(t)
	if err := os.WriteFile(sidecarPath(video, "en"), []byte(srtOf(12)), 0o644); err != nil {
		t.Fatal(err)
	}
	job := runRepairJob(s, "en")
	if job.State != StateDone {
		t.Fatalf("state=%q note=%q, want done", job.State, job.Note)
	}
	if len(*picks) != 1 || (*picks)[0].Index != 1 {
		t.Errorf("extracted %+v, want the full track (0:s:1) only", *picks)
	}
	if n := cuesIn(t, sidecarPath(video, "en")); n != 900 {
		t.Errorf("en.srt has %d cues, want the full track's 900", n)
	}
	if n := cuesIn(t, sidecarPathV(video, "en", VariantForced)); n != 12 {
		t.Errorf("en.forced.srt has %d cues, want the old 12", n)
	}
	if _, err := os.Stat(sidecarPath(video, "en") + library.PartialSuffix); !os.IsNotExist(err) {
		t.Error("the temp file was left behind")
	}
}

// When a .forced.srt is already there, the short sidecar goes to the recycle bin rather
// than overwriting it — and is never just deleted.
func TestForcedRepairBinsShortSidecarWhenForcedExists(t *testing.T) {
	s, video, _ := repairFixture(t)
	for p, n := range map[string]int{sidecarPath(video, "en"): 12, sidecarPathV(video, "en", VariantForced): 10} {
		if err := os.WriteFile(p, []byte(srtOf(n)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if job := runRepairJob(s, "en"); job.State != StateDone {
		t.Fatalf("state=%q note=%q", job.State, job.Note)
	}
	if n := cuesIn(t, sidecarPathV(video, "en", VariantForced)); n != 10 {
		t.Errorf("the existing forced sidecar was replaced (%d cues)", n)
	}
	if n := cuesIn(t, sidecarPath(video, "en")); n != 900 {
		t.Errorf("en.srt has %d cues, want 900", n)
	}

	// Without a bin the swap is refused and both files stay as they were.
	s2, video2, _ := repairFixture(t)
	s2.bin = nil
	for p, n := range map[string]int{sidecarPath(video2, "en"): 12, sidecarPathV(video2, "en", VariantForced): 10} {
		if err := os.WriteFile(p, []byte(srtOf(n)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if job := runRepairJob(s2, "en"); job.State == StateDone {
		t.Fatalf("swapped with no recycle bin: note=%q", job.Note)
	}
	if n := cuesIn(t, sidecarPath(video2, "en")); n != 12 {
		t.Errorf("en.srt changed to %d cues with no bin to take the old one", n)
	}
}

// A short sidecar that isn't much shorter than the full track is left alone.
func TestForcedRepairKeepsComparableSidecar(t *testing.T) {
	s, video, _ := repairFixture(t)
	s.extract = func(_ context.Context, _ string, p []extractPick) error {
		return os.WriteFile(p[0].Path, []byte(srtOf(60)), 0o644)
	}
	if err := os.WriteFile(sidecarPath(video, "en"), []byte(srtOf(40)), 0o644); err != nil {
		t.Fatal(err)
	}
	if job := runRepairJob(s, "en"); job.State != StateSkipped {
		t.Errorf("state=%q note=%q, want skipped", job.State, job.Note)
	}
	if n := cuesIn(t, sidecarPath(video, "en")); n != 40 {
		t.Errorf("en.srt has %d cues, want it untouched", n)
	}
}

// passFixture is a repair fixture over a real movies module listing movie 1 at the video.
func passFixture(t *testing.T) (*Service, string, *[]extractPick) {
	t.Helper()
	s, video, picks := repairFixture(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s.settings = settings.NewService(st.DB())
	if err := s.settings.Set(context.Background(), keyLanguages, "en"); err != nil {
		t.Fatal(err)
	}
	s.movies = movies.NewService(st.DB(), nil, nil, t.TempDir(), "", nil, s.log)
	s.ledger = NewLedger(st.DB())
	if _, err := st.DB().Exec(`INSERT INTO movies (id, tmdb_id, title, monitored, has_file, movie_file_path) VALUES (1, 101, 'Movie', 1, 1, ?)`, video); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return s, video, picks
}

// A sidecar with a normal cue count is never probed or rewritten, and the pass still
// records that it ran.
func TestForcedRepairSkipsNormalSidecar(t *testing.T) {
	s, video, picks := passFixture(t)
	if err := os.WriteFile(sidecarPath(video, "en"), []byte(srtOf(400)), 0o644); err != nil {
		t.Fatal(err)
	}
	s.probe = func(context.Context, string) (*mediaInfo, error) {
		t.Error("probed a file whose sidecar has a normal cue count")
		return nil, nil
	}
	s.forcedRepairPass(context.Background())
	if len(*picks) != 0 || len(s.Jobs()) != 0 {
		t.Errorf("extracted %v, jobs %v; want nothing touched", *picks, s.Jobs())
	}
	if n := cuesIn(t, sidecarPath(video, "en")); n != 400 {
		t.Errorf("en.srt has %d cues", n)
	}
	if !s.settings.GetBool(context.Background(), keyForcedRepairDone, false) {
		t.Error("the pass didn't record that it ran")
	}
}

// The pass repairs through a queued job, waits for it, then sets the guard key; after
// that it never runs again, even with a short sidecar back on disk.
func TestForcedRepairRunsOnce(t *testing.T) {
	s, video, _ := passFixture(t)
	if err := os.WriteFile(sidecarPath(video, "en"), []byte(srtOf(12)), 0o644); err != nil {
		t.Fatal(err)
	}
	s.forcedRepairPass(context.Background())
	if n := cuesIn(t, sidecarPath(video, "en")); n != 900 {
		t.Fatalf("en.srt has %d cues after the pass, want 900", n)
	}
	jobs := s.Jobs()
	if len(jobs) != 1 || jobs[0].Mode != ModeRepairForced || jobs[0].State != StateDone {
		t.Fatalf("jobs = %+v, want one finished repair job", jobs)
	}
	if !s.settings.GetBool(context.Background(), keyForcedRepairDone, false) {
		t.Fatal("guard key not set after the pass")
	}
	// The ledger knows both files' sources, and the run's repair attempt.
	ctx := context.Background()
	if row, ok, _ := s.Ledger().LiveFile(ctx, sidecarPath(video, "en")); !ok || row.Source != SourceEmbedded || row.EmbeddedIndex == nil || *row.EmbeddedIndex != 1 {
		t.Errorf("en.srt ledger row = %+v (live %v), want embedded track 1", row, ok)
	}
	if row, ok, _ := s.Ledger().LiveFile(ctx, sidecarPathV(video, "en", VariantForced)); !ok || row.Variant != VariantForced {
		t.Errorf("en.forced.srt ledger row = %+v (live %v), want a forced row", row, ok)
	}
	if at, err := s.Ledger().Attempts(ctx, AttemptQuery{MediaKey: "m:1"}); err != nil || len(at) != 1 || at[0].Rung != RungRepair || at[0].Outcome != ResultOK {
		t.Errorf("attempts = %+v err=%v, want one ok repair", at, err)
	}

	// A restart after completion: the key stops it before it looks at anything.
	if err := os.WriteFile(sidecarPath(video, "en"), []byte(srtOf(12)), 0o644); err != nil {
		t.Fatal(err)
	}
	s.startForcedRepair(context.Background())
	s.repair.mu.Lock()
	started := s.repair.started
	s.repair.mu.Unlock()
	if started || len(s.Jobs()) != 1 {
		t.Errorf("the pass started again after it had completed (jobs %d)", len(s.Jobs()))
	}
}

// A repair job is its own piece of work: an ensure job for the same file doesn't swallow it.
func TestRepairJobNotDedupedWithEnsure(t *testing.T) {
	s := queueFixture()
	a := s.enqueue(&Job{Kind: "movie", MovieID: 1, Title: "x", Priority: PrioSweep})
	b := s.enqueue(&Job{Kind: "movie", MovieID: 1, Title: "x", Priority: PrioSweep, Mode: ModeRepairForced})
	if a == b {
		t.Error("the repair job was folded into the ensure job")
	}
}

// A sidecar the cue counter can't read (UTF-16 here) wasn't written by ffmpeg, and a
// file whose first track is no longer forced (upgraded since) isn't the bug: neither is
// touched.
func TestForcedRepairLeavesUnreadableAndChangedFiles(t *testing.T) {
	s, video, picks := repairFixture(t)
	utf16 := []byte{0xFF, 0xFE, '1', 0, '\n', 0}
	if err := os.WriteFile(sidecarPath(video, "en"), utf16, 0o644); err != nil {
		t.Fatal(err)
	}
	if job := runRepairJob(s, "en"); job.State == StateDone || len(*picks) != 0 {
		t.Errorf("repaired an unreadable sidecar: state=%q note=%q", job.State, job.Note)
	}

	s2, video2, picks2 := repairFixture(t)
	s2.probe = func(context.Context, string) (*mediaInfo, error) {
		mi := englishAudio()
		mi.Subs = []SubTrack{{Index: 0, Codec: "subrip", Lang: "eng", Text: true}, {Index: 1, Codec: "subrip", Lang: "eng", Text: true, Forced: true}}
		return mi, nil
	}
	if err := os.WriteFile(sidecarPath(video2, "en"), []byte(srtOf(12)), 0o644); err != nil {
		t.Fatal(err)
	}
	if job := runRepairJob(s2, "en"); job.State == StateDone || len(*picks2) != 0 {
		t.Errorf("repaired a file whose first track isn't forced: state=%q note=%q", job.State, job.Note)
	}
	if n := cuesIn(t, sidecarPath(video2, "en")); n != 12 {
		t.Errorf("en.srt changed (%d cues)", n)
	}
}
