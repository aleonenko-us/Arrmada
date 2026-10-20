package convert

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/libroots"
)

func logMsgs(s *Service) []string {
	var out []string
	for _, l := range s.Logs() {
		out = append(out, l.Msg)
	}
	return out
}

// finalizeRig stages a finished encode of a 1000-byte original, ready for finalizeOutput.
func finalizeRig(t *testing.T) (r *spaceRig, src, dst string, mi *MediaInfo, plan Plan) {
	t.Helper()
	return finalizeRigFor(t, "Remux.mkv")
}

// finalizeRigFor is finalizeRig with the original named name (an .mp4 changes its name).
func finalizeRigFor(t *testing.T, name string) (r *spaceRig, src, dst string, mi *MediaInfo, plan Plan) {
	t.Helper()
	r = newSpaceRig(t)
	src = r.addMovieFile(t, 1, name, 1000)
	mi = film("h264", 1920, 1080, 35000, aud("truehd", "eng", 8))
	mi.SizeBytes = 1000
	plan = Plan{VideoCodec: "hevc"}
	stubProbe(t, &MediaInfo{VideoCodec: "hevc", DurationSec: mi.DurationSec,
		AudioTracks: len(keptAudio(mi, plan)), SubTracks: len(keptSubs(mi, plan))})
	stubDisks(t, r.scratch, 1<<50, 1<<50)
	dst = filepath.Join(r.scratch, "convert-1.mkv")
	writeFile(t, dst, 500, 'y')
	return r, src, dst, mi, plan
}

// convertNow runs finalizeOutput the way process() hands over: the ledger row begun, the
// source facts recorded.
func (r *spaceRig) convertNow(t *testing.T, id int64, title, src, dst string, mi *MediaInfo, plan Plan) *Job {
	t.Helper()
	it, _ := parseKey(movieKey(id))
	job := r.claim(it, title, true)
	if job == nil {
		t.Fatalf("%s is already claimed", title)
	}
	rec := r.record(job)
	rec.srcPath, rec.srcInfo = src, mi
	rec.setPlan(plan)
	r.update(job, func(j *Job) { j.SrcBytes = mi.SizeBytes })
	r.beginHistory(job)
	r.finalizeOutput(context.Background(), job, src, dst, mi, plan)
	return job
}

func (r *spaceRig) ledger(t *testing.T, job *Job) HistoryEntry {
	t.Helper()
	e, err := r.history.get(context.Background(), r.record(job).historyID)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// A converted original moves into <library>/.arrmada-hold/<ledger id>/ — the same file, not
// a copy — hidden from Plex, never into the recycle bin; the ledger says where and until when.
// Space saved isn't counted until it's released.
func TestHoldKeepsTheOriginalOnItsDrive(t *testing.T) {
	r, src, dst, mi, plan := finalizeRig(t)
	before, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	job := r.convertNow(t, 1, "Remux", src, dst, mi, plan)
	if job.State != StateDone {
		t.Fatalf("state %s (%s), want done", job.State, job.Note)
	}
	e := r.ledger(t, job)
	want := filepath.Join(r.lib, library.HoldDirName, strconv.FormatInt(e.ID, 10), "Remux.mkv")
	if e.HoldState != HoldHeld || e.HoldPath != want {
		t.Fatalf("hold %q at %q, want held at %q", e.HoldState, e.HoldPath, want)
	}
	// 14 calendar days in local time: an hour either way when a daylight-saving change falls inside.
	if d := time.Until(time.Unix(e.HoldUntil, 0)); d < 14*24*time.Hour-time.Hour-time.Minute || d > 14*24*time.Hour+time.Hour+time.Minute {
		t.Fatalf("held for %s, want 14 days", d)
	}
	after, err := os.Stat(want)
	if err != nil || !os.SameFile(before, after) || after.Size() != 1000 {
		t.Fatalf("the held file isn't the original moved in place: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(r.lib, library.HoldDirName, ".plexignore")); strings.TrimSpace(string(b)) != "*" {
		t.Fatalf(".plexignore = %q", b)
	}
	// Jellyfin skips a folder holding a .ignore (an empty one ignores everything).
	if b, err := os.ReadFile(filepath.Join(r.lib, library.HoldDirName, ".ignore")); err != nil || len(b) != 0 {
		t.Fatalf(".ignore = %q, %v", b, err)
	}
	if sizeOf(src) != 500 || r.moviePath(t, 1) != src {
		t.Fatal("the converted file isn't in place")
	}
	if entries, _ := os.ReadDir(r.recycle); len(entries) != 0 {
		t.Fatal("the original went to the recycle bin")
	}
	if n := r.Reclaimed(context.Background()); n != 0 {
		t.Fatalf("space saved counted %d bytes while the original is still held", n)
	}
	if r.swapCount(t) != 0 {
		t.Fatal("the journal row was left behind")
	}
}

// The hold goes on the most specific library folder holding the file, and never inside the
// app's data folder.
func TestHoldRoot(t *testing.T) {
	base := t.TempDir()
	lib, tv, data := filepath.Join(base, "lib"), filepath.Join(base, "lib", "tv"), filepath.Join(base, "data")
	s := newTestService(t)
	s.SetHoldRoots(func() []libroots.Root {
		return []libroots.Root{{Name: "movies", Path: lib}, {Name: "tv", Path: tv}, {Name: "music", Path: filepath.Join(data, "music")}}
	}, data)
	if root, why := s.holdRootFor(filepath.Join(tv, "Show", "S01E01.mkv")); root != tv || why != "" {
		t.Fatalf("tv file: root %q (%s)", root, why)
	}
	if root, why := s.holdRootFor(filepath.Join(lib, "Film", "Film.mkv")); root != lib || why != "" {
		t.Fatalf("movie file: root %q (%s)", root, why)
	}
	if root, why := s.holdRootFor(filepath.Join(data, "music", "a.mkv")); root != "" || !strings.Contains(why, "data folder") {
		t.Fatalf("under the data folder: root %q (%s)", root, why)
	}
	if root, why := s.holdRootFor(filepath.Join(base, "elsewhere", "a.mkv")); root != "" || why == "" {
		t.Fatalf("outside every folder: root %q (%s)", root, why)
	}
}

// A file with nowhere to keep its original is never started.
func TestNoHoldSkipsBeforeAnyWork(t *testing.T) {
	r := newSpaceRig(t)
	r.SetHoldRoots(func() []libroots.Root { return nil }, "")
	src := r.addMovie(t, 1, "Remux", 64)
	stubProbe(t, film("h264", 3840, 2160, 85000, aud("truehd", "eng", 8)))
	stubDisks(t, r.scratch, 1<<50, 1<<50)
	job := r.run(t, 1, "Remux")
	if kind, _, _ := r.skipOf(t, movieKey(1)); job.State != StateSkipped || kind != SkipNoHold {
		t.Fatalf("state %s, skip %q (%s)", job.State, kind, job.Note)
	}
	if sizeOf(src) != 64 || strings.Contains(strings.Join(logMsgs(r.Service), "\n"), "Encoding") {
		t.Fatal("work started for a file whose original has nowhere to go")
	}
}

// heldSetup puts a swap in the state a crash between hold and rename leaves: the original in
// the hold, the journal at 'held', a ledger row. partThere says whether the staged copy is
// still waiting to be renamed in.
func heldSetup(t *testing.T, r *spaceRig, name string, partThere bool) (src, final, part, held string, id int64) {
	t.Helper()
	src = r.addMovieFile(t, 1, name, 1000)
	final, part = finalPathFor(src), partPathFor(src)
	id = r.history.begin(context.Background(), historyRow{key: movieKey(1), kind: "movie", movieID: 1, srcPath: src, srcSize: 1000})
	held = filepath.Join(r.lib, library.HoldDirName, strconv.FormatInt(id, 10), name)
	writeFile(t, held, 1000, 'x')
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if partThere {
		writeFile(t, part, 500, 'y')
	}
	r.journal(t, swapRow{part: part, final: final, src: src, movieID: 1, state: swapHeld, holdPath: held, historyID: id})
	return
}

// Killed between hold and rename: with the staged copy still there the rename is finished
// and the record repointed; with it gone the original goes back. Either way one of the two
// files is in the library — never neither — for MKV and MP4 sources alike.
func TestRecoverHeldSwap(t *testing.T) {
	for _, name := range []string{"Film.mkv", "Film.mp4"} {
		t.Run(name+"/part present", func(t *testing.T) {
			r := newSpaceRig(t)
			src, final, part, held, id := heldSetup(t, r, name, true)
			r.recoverSwaps(context.Background())
			if exists(part) || sizeOf(final) != 500 || r.moviePath(t, 1) != final {
				t.Fatalf("part %v, final %d bytes, record %s", exists(part), sizeOf(final), r.moviePath(t, 1))
			}
			if sizeOf(held) != 1000 || (src != final && exists(src)) {
				t.Fatal("the original should stay in the hold")
			}
			e, _ := r.history.get(context.Background(), id)
			if e.HoldState != HoldHeld || e.Outcome != OutcomeDone || r.swapCount(t) != 0 {
				t.Fatalf("ledger %s/%s, journal %d", e.Outcome, e.HoldState, r.swapCount(t))
			}
		})
		t.Run(name+"/part gone", func(t *testing.T) {
			r := newSpaceRig(t)
			src, final, part, held, id := heldSetup(t, r, name, false)
			r.recoverSwaps(context.Background())
			if sizeOf(src) != 1000 || exists(held) || exists(part) || r.moviePath(t, 1) != src {
				t.Fatalf("original %d bytes, held %v, record %s", sizeOf(src), exists(held), r.moviePath(t, 1))
			}
			if src != final && exists(final) {
				t.Fatal("a converted .mkv was left beside the restored original")
			}
			e, _ := r.history.get(context.Background(), id)
			if e.HoldState != "" || e.Outcome != OutcomeFailed || r.swapCount(t) != 0 {
				t.Fatalf("ledger %s/%q, journal %d", e.Outcome, e.HoldState, r.swapCount(t))
			}
		})
	}
}

// Killed while the original was moving into the hold: it's still in place, so the swap is
// undone and nothing is left in the hold.
func TestRecoverHoldingSwap(t *testing.T) {
	r := newSpaceRig(t)
	src := r.addMovie(t, 1, "Film", 1000)
	part := partPathFor(src)
	writeFile(t, part, 500, 'y')
	held := filepath.Join(r.lib, library.HoldDirName, "3", "Film.mkv")
	writeFile(t, held+".partial", 400, 'x') // a copy across filesystems cut short
	r.journal(t, swapRow{part: part, final: src, src: src, movieID: 1, state: swapHolding, holdPath: held, historyID: 3})
	r.recoverSwaps(context.Background())
	if sizeOf(src) != 1000 || exists(part) || exists(held+".partial") || exists(filepath.Dir(held)) || r.swapCount(t) != 0 {
		t.Fatalf("original %d bytes, part %v, partial %v, journal %d", sizeOf(src), exists(part), exists(held+".partial"), r.swapCount(t))
	}
}

// After hold_until the hourly task deletes the original, marks it released, and only then
// counts the space saved; one already gone is marked missing and counts nothing.
func TestExpireHolds(t *testing.T) {
	ctx := context.Background()
	r, src, dst, mi, plan := finalizeRig(t)
	job := r.convertNow(t, 1, "Remux", src, dst, mi, plan)
	e := r.ledger(t, job)
	if err := r.ExpireHolds(ctx); err != nil || !exists(e.HoldPath) {
		t.Fatalf("released before its time: %v", err)
	}
	if _, err := r.db.Exec(`UPDATE convert_history SET hold_until = ? WHERE id = ?`, time.Now().Add(-time.Minute).Unix(), e.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.ExpireHolds(ctx); err != nil {
		t.Fatal(err)
	}
	e = r.ledger(t, job)
	if e.HoldState != HoldReleased || e.ReleasedAt == 0 || exists(e.HoldPath) || exists(filepath.Dir(e.HoldPath)) {
		t.Fatalf("after expiry: %s, file %v", e.HoldState, exists(e.HoldPath))
	}
	if n := r.Reclaimed(ctx); n != 500 {
		t.Fatalf("space saved %d, want 500", n)
	}
	if sizeOf(src) != 500 {
		t.Fatal("the converted file was touched")
	}

	// A held file that vanished is recorded as missing, with nothing counted.
	r2, src2, dst2, mi2, plan2 := finalizeRig(t)
	job2 := r2.convertNow(t, 1, "Remux", src2, dst2, mi2, plan2)
	e2 := r2.ledger(t, job2)
	if err := os.Remove(e2.HoldPath); err != nil {
		t.Fatal(err)
	}
	if _, err := r2.db.Exec(`UPDATE convert_history SET hold_until = 1 WHERE id = ?`, e2.ID); err != nil {
		t.Fatal(err)
	}
	if err := r2.ExpireHolds(ctx); err != nil {
		t.Fatal(err)
	}
	if e2 = r2.ledger(t, job2); e2.HoldState != HoldMissing || r2.Reclaimed(ctx) != 0 {
		t.Fatalf("vanished hold: %s, reclaimed %d", e2.HoldState, r2.Reclaimed(ctx))
	}
}

// With no undo chosen the original is deleted — but only once the conversion is in place and
// the record points at it. If the repoint fails, the original stays held and the next
// startup finishes the job.
func TestNoUndoDeletesOnlyAfterTheRepoint(t *testing.T) {
	ctx := context.Background()
	// An MP4: the repoint changes the recorded path, which the trigger below refuses.
	r, src, dst, mi, plan := finalizeRigFor(t, "Remux.mp4")
	set(t, r.Service, map[string]string{keyHoldDays: "0"})
	if _, err := r.db.Exec(`CREATE TRIGGER no_repoint BEFORE UPDATE OF movie_file_path, media_json ON movies BEGIN SELECT RAISE(ABORT, 'database is locked'); END`); err != nil {
		t.Fatal(err)
	}
	job := r.convertNow(t, 1, "Remux", src, dst, mi, plan)
	e := r.ledger(t, job)
	if job.State != StateFailed || e.HoldState != HoldHeld || sizeOf(e.HoldPath) != 1000 {
		t.Fatalf("state %s (%s); hold %s, held file %d bytes", job.State, job.Note, e.HoldState, sizeOf(e.HoldPath))
	}

	if _, err := r.db.Exec(`DROP TRIGGER no_repoint`); err != nil {
		t.Fatal(err)
	}
	r.recoverSwaps(ctx)
	if e = r.ledger(t, job); e.Outcome != OutcomeDone || e.HoldState != HoldHeld {
		t.Fatalf("after recovery: %s/%s", e.Outcome, e.HoldState)
	}
	if err := r.ExpireHolds(ctx); err != nil {
		t.Fatal(err)
	}
	if e = r.ledger(t, job); e.HoldState != HoldReleased || exists(e.HoldPath) || r.Reclaimed(ctx) != 500 {
		t.Fatalf("no-undo original not released after the repoint: %s", e.HoldState)
	}

	// And on the normal path it goes straight away.
	r2, src2, dst2, mi2, plan2 := finalizeRig(t)
	set(t, r2.Service, map[string]string{keyHoldDays: "0"})
	job2 := r2.convertNow(t, 1, "Remux", src2, dst2, mi2, plan2)
	if e2 := r2.ledger(t, job2); job2.State != StateDone || e2.HoldState != HoldReleased || exists(e2.HoldPath) {
		t.Fatalf("state %s, hold %s", job2.State, e2.HoldState)
	}
}

// The self-check proves a move into each Movies/TV hold is a rename, without touching a
// library file, and cleans up after itself.
func TestHoldSelfCheck(t *testing.T) {
	base := t.TempDir()
	movies, tv := filepath.Join(base, "movies"), filepath.Join(base, "tv")
	for _, d := range []string{movies, tv} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s := newTestService(t)
	s.SetHoldRoots(func() []libroots.Root {
		return []libroots.Root{{Name: "movies", Path: movies}, {Name: "tv", Path: tv}, {Name: "music", Path: filepath.Join(base, "music")},
			{Name: "ebooks", Path: movies}}
	}, filepath.Join(base, "data"))
	got := s.holdSelfCheck(context.Background())
	if len(got) != 2 || !got[0].Instant || !got[1].Instant || got[0].Err != "" || got[0].Label != "Movies" || got[1].Label != "TV" {
		t.Fatalf("checks %+v", got)
	}
	for _, d := range []string{movies, tv} {
		kids, _ := os.ReadDir(filepath.Join(d, library.HoldDirName))
		// Only the marker files: .ignore and .plexignore, in name order.
		if len(kids) != 2 || kids[0].Name() != ".ignore" || kids[1].Name() != ".plexignore" {
			t.Fatalf("hold in %s holds %v after the check", d, kids)
		}
		if exists(filepath.Join(d, library.HoldDirName+".probe")) {
			t.Fatal("the probe was left behind")
		}
	}
	if len(s.HoldChecks()) != 2 {
		t.Fatal("the result isn't kept for the settings page")
	}
}

// A finished swap tells Plex's scanner about the file's folder, once; a swap that didn't
// happen tells it nothing.
func TestFinalizeOutputCallsLibraryChanged(t *testing.T) {
	r, src, dst, mi, plan := finalizeRig(t)
	var got []string
	r.SetLibraryChanged(func(kind, dir string) { got = append(got, kind+"|"+dir) })
	job := r.convertNow(t, 1, "Remux", src, dst, mi, plan)
	if job.State != StateDone {
		t.Fatalf("state %s (%s), want done", job.State, job.Note)
	}
	if want := "movie|" + filepath.Dir(src); len(got) != 1 || got[0] != want {
		t.Fatalf("reported %v, want [%s]", got, want)
	}

	r2, src2, dst2, mi2, plan2 := finalizeRig(t)
	got = nil
	r2.SetLibraryChanged(func(kind, dir string) { got = append(got, kind+"|"+dir) })
	r2.SetHoldRoots(func() []libroots.Root { return nil }, "") // nowhere to keep the original
	job2 := r2.convertNow(t, 1, "Remux", src2, dst2, mi2, plan2)
	if job2.State == StateDone || len(got) != 0 || sizeOf(src2) != 1000 {
		t.Fatalf("a skipped swap (%s) reported %v", job2.State, got)
	}
}

// Saving the bin's settings still clears a bin_full skip an older version left, and leaves
// every other kind alone.
func TestBinSettingsChangedClearsBinFull(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)
	s.skips.record(ctx, movieKey(1), SkipBinFull, "too big")
	s.skips.record(ctx, movieKey(2), SkipNoScratch, "full")
	s.BinSettingsChanged(ctx)
	waiting := s.skips.waitingKeys(ctx)
	if waiting[movieKey(1)] || !waiting[movieKey(2)] {
		t.Fatalf("waiting after the change: %v", waiting)
	}
}
