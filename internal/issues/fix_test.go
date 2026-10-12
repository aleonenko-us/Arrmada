package issues

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeSubs answers Outcome from a table, by job id.
type fakeSubs map[int64]SubtitleOutcome

func (f fakeSubs) Outcome(jobID int64, _ Target, _ int64) SubtitleOutcome { return f[jobID] }

// startFix starts action on an issue with a fix that just reports the job it "queued".
func startFix(t *testing.T, f *fixture, id int64, action string, job int64) Issue {
	t.Helper()
	i, err := f.s.StartFix(context.Background(), id, action, func(context.Context, Issue, Item) (int64, string, error) {
		return job, "started", nil
	})
	if err != nil {
		t.Fatalf("start %s on %d: %v", action, id, err)
	}
	return i
}

func status(t *testing.T, f *fixture, id int64) Issue {
	t.Helper()
	i, err := f.s.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func TestStartFix(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	i, _ := f.report(t, "alice", MediaSeries, 1399, 1, 4, "audio", "")
	if _, err := f.s.StartFix(ctx, i.ID, "convert", func(context.Context, Issue, Item) (int64, string, error) { return 0, "", nil }); err == nil {
		t.Error("an unknown action was accepted")
	}
	if _, err := f.s.StartFix(ctx, i.ID, ActionReplace, nil); !errors.Is(err, ErrNoFix) {
		t.Errorf("unwired fix: %v, want ErrNoFix", err)
	}
	// A fix that fails to start leaves the issue open.
	boom := errors.New("indexers down")
	if _, err := f.s.StartFix(ctx, i.ID, ActionReplace, func(context.Context, Issue, Item) (int64, string, error) { return 0, "", boom }); !errors.Is(err, boom) {
		t.Errorf("failing fix: %v", err)
	}
	if got := status(t, f, i.ID); got.Status != StatusOpen {
		t.Fatalf("after a failed start: %s", got.Status)
	}
	var seen Item
	got, err := f.s.StartFix(ctx, i.ID, ActionReplace, func(_ context.Context, _ Issue, cur Item) (int64, string, error) {
		seen = cur
		return 0, "Searching for another copy.", nil
	})
	if err != nil || got.Status != StatusFixing || got.Action != ActionReplace || got.ActionFile != "/tv/Severance/S01E04.mkv" || got.ActionNote != "Searching for another copy." {
		t.Fatalf("started: %+v, %v", got, err)
	}
	if seen.Release != "Severance.1080p-GRP" {
		t.Errorf("the fix was handed %+v", seen)
	}
	// Nobody was told anything yet: it isn't fixed.
	if len(f.told.list()) != 0 {
		t.Errorf("told = %v", f.told.list())
	}
	// A closed issue can't be fixed.
	if _, err := f.s.Resolve(ctx, i.ID, "", 1, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.StartFix(ctx, i.ID, ActionReplace, func(context.Context, Issue, Item) (int64, string, error) { return 0, "", nil }); !errors.Is(err, ErrClosed) {
		t.Errorf("fix on a closed issue: %v", err)
	}
}

func TestAutoResolveOnImport(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	e4, _ := f.report(t, "alice", MediaSeries, 1399, 1, 4, "audio", "")
	f.report(t, "bob", MediaSeries, 1399, 1, 4, "audio", "")
	e5, _ := f.report(t, "carol", MediaSeries, 1399, 1, 5, "video", "")
	movie, _ := f.report(t, "alice", MediaMovie, 603, 0, 0, "video", "")
	open, _ := f.report(t, "bob", MediaMovie, 603, 0, 0, "audio", "") // reported, no fix started
	startFix(t, f, e4.ID, ActionReplace, 0)
	startFix(t, f, e5.ID, ActionReplace, 0)
	startFix(t, f, movie.ID, ActionReplace, 0)

	// S01E05 landing resolves only S01E05.
	if err := f.s.OnSeriesImported(ctx, 21, []Episode{{1, 5}}); err != nil {
		t.Fatal(err)
	}
	if status(t, f, e4.ID).Status != StatusFixing {
		t.Fatal("an import of S01E05 resolved the S01E04 issue")
	}
	if got := status(t, f, e5.ID); got.Status != StatusResolved || got.Resolution != ResolvedNewCopy {
		t.Fatalf("S01E05: %+v", got)
	}
	// Another show's import (same episode numbers) touches nothing.
	_ = f.s.OnSeriesImported(ctx, 99, []Episode{{1, 4}})
	if status(t, f, e4.ID).Status != StatusFixing {
		t.Fatal("another show's import resolved the issue")
	}
	if err := f.s.OnSeriesImported(ctx, 21, []Episode{{1, 4}, {1, 6}}); err != nil {
		t.Fatal(err)
	}
	if status(t, f, e4.ID).Status != StatusResolved {
		t.Fatal("S01E04 not resolved by its own import")
	}
	// The same import again (an outbox retry) tells nobody twice.
	_ = f.s.OnSeriesImported(ctx, 21, []Episode{{1, 4}})

	// A movie: an extra version's import isn't the reported file; the default's is. An
	// open issue with no fix started stays open.
	_ = f.s.OnMovieImported(ctx, 11, 3)
	if status(t, f, movie.ID).Status != StatusFixing {
		t.Fatal("an extra version's import resolved the movie issue")
	}
	if err := f.s.OnMovieImported(ctx, 11, 0); err != nil {
		t.Fatal(err)
	}
	if status(t, f, movie.ID).Status != StatusResolved || status(t, f, open.ID).Status != StatusOpen {
		t.Fatal("the movie import didn't resolve exactly the issue being fixed")
	}

	refs := map[string]int{}
	for _, g := range f.told.list() {
		refs[g]++
	}
	want := []string{
		fmt.Sprintf("%d:series:1399:issue:%d:resolved", f.users["alice"], e4.ID),
		fmt.Sprintf("%d:series:1399:issue:%d:resolved", f.users["bob"], e4.ID),
		fmt.Sprintf("%d:series:1399:issue:%d:resolved", f.users["carol"], e5.ID),
		fmt.Sprintf("%d:movie:603:issue:%d:resolved", f.users["alice"], movie.ID),
	}
	if len(refs) != len(want) {
		t.Errorf("told = %v, want %v", f.told.list(), want)
	}
	for _, w := range want {
		if refs[w] != 1 {
			t.Errorf("%s told %d times", w, refs[w])
		}
	}
}

func TestSubtitleCompletionResolves(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	subs := fakeSubs{}
	f.s.SetSubtitles(subs)
	made, _ := f.report(t, "alice", MediaMovie, 603, 0, 0, "subtitles", "")
	none, _ := f.report(t, "bob", MediaSeries, 1399, 1, 4, "subtitles", "")
	lost, _ := f.report(t, "carol", MediaSeries, 1399, 1, 5, "subtitles", "")
	startFix(t, f, made.ID, ActionSubtitles, 7)
	startFix(t, f, none.ID, ActionSubtitles, 8)
	startFix(t, f, lost.ID, ActionSubtitles, 9)

	// Still running: nothing changes.
	subs[7] = SubtitleOutcome{Found: true, Pending: true}
	subs[8] = SubtitleOutcome{Found: true, Pending: true}
	subs[9] = SubtitleOutcome{Found: true, Pending: true}
	if err := f.s.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{made.ID, none.ID, lost.ID} {
		if status(t, f, id).Status != StatusFixing {
			t.Fatalf("issue %d settled while its job was running", id)
		}
	}
	subs[7] = SubtitleOutcome{Found: true, Done: true}
	subs[8] = SubtitleOutcome{Found: true, Note: "en: no OpenSubtitles match"}
	delete(subs, 9) // gone (pruned from the job history)
	if err := f.s.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if got := status(t, f, made.ID); got.Status != StatusResolved || got.Resolution != ResolvedNewSubtitles {
		t.Errorf("job made subtitles: %+v", got)
	}
	if got := status(t, f, none.ID); got.Status != StatusOpen || !strings.Contains(got.ActionNote, "no OpenSubtitles match") || got.ActionJob != 0 {
		t.Errorf("job made none: %+v", got)
	}
	if got := status(t, f, lost.ID); got.Status != StatusOpen || !strings.Contains(got.ActionNote, "Couldn't find the subtitle job") {
		t.Errorf("job lost: %+v", got)
	}
	if got := f.told.list(); len(got) != 1 || !strings.HasSuffix(got[0], fmt.Sprintf("movie:603:issue:%d:resolved", made.ID)) {
		t.Errorf("told = %v, want only the fixed one's reporter", got)
	}

	// A completion callback settles it the same way, for that job only.
	again, _ := f.report(t, "bob", MediaSeries, 1399, 1, 4, "audio", "")
	startFix(t, f, again.ID, ActionSubtitles, 12)
	_ = f.s.OnSubtitleJobDone(ctx, Target{MediaType: MediaSeries, LibraryID: 21, Season: 1, Episode: 4}, 11, SubtitleOutcome{Found: true, Done: true})
	if status(t, f, again.ID).Status != StatusFixing {
		t.Fatal("another job's completion resolved the issue")
	}
	_ = f.s.OnSubtitleJobDone(ctx, Target{MediaType: MediaSeries, LibraryID: 21, Season: 1, Episode: 4}, 12, SubtitleOutcome{Found: true, Done: true})
	if status(t, f, again.ID).Status != StatusResolved {
		t.Fatal("its own job's completion didn't resolve the issue")
	}
}

func TestBackstopResolvesOnFileChange(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	moved, _ := f.report(t, "alice", MediaMovie, 603, 0, 0, "video", "")
	resized, _ := f.report(t, "bob", MediaSeries, 1399, 1, 4, "video", "")
	same, _ := f.report(t, "carol", MediaSeries, 1399, 1, 5, "video", "")
	for _, id := range []int64{moved.ID, resized.ID, same.ID} {
		startFix(t, f, id, ActionReplace, 0)
	}
	// The import signals were missed: the files changed underneath.
	f.lib.put(MediaMovie, 603, 11, 0, 0, "The Matrix", "/m/The Matrix (1999)/The Matrix 2160p.mkv", 9000)
	f.lib.put(MediaSeries, 1399, 21, 1, 4, "Severance", "/tv/Severance/S01E04.mkv", 1200)
	if err := f.s.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if status(t, f, moved.ID).Status != StatusResolved || status(t, f, resized.ID).Status != StatusResolved {
		t.Error("a new file (path or size) didn't resolve its issue")
	}
	if status(t, f, same.ID).Status != StatusFixing {
		t.Error("an unchanged file resolved its issue")
	}
	// A fix that brings nothing for a week goes back to open.
	f.s.now = func() time.Time { return time.Now().Add(8 * 24 * time.Hour) }
	if err := f.s.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if got := status(t, f, same.ID); got.Status != StatusOpen || !strings.Contains(got.ActionNote, "Nothing new arrived") {
		t.Errorf("stale fix: %+v", got)
	}
}
