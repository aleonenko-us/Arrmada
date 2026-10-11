package convert

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/series"
)

const origRelease = "Film.2020.1080p.BluRay.x264-GRP"

// convertedFilm converts the rig's film (name .mkv or .mp4) with a recorded release, and
// returns its ledger row.
func convertedFilm(t *testing.T, name string) (*spaceRig, string, HistoryEntry) {
	t.Helper()
	r, src, dst, mi, plan := finalizeRigFor(t, name)
	if _, err := r.db.Exec(`UPDATE movies SET source_release = ? WHERE id = 1`, origRelease); err != nil {
		t.Fatal(err)
	}
	job := r.convertNow(t, 1, "Film", src, dst, mi, plan)
	if job.State != StateDone {
		t.Fatalf("conversion: %s (%s)", job.State, job.Note)
	}
	e := r.ledger(t, job)
	if e.HoldState != HoldHeld {
		t.Fatalf("not held: %+v", e)
	}
	return r, src, e
}

func (r *spaceRig) movieRow(t *testing.T) (path, release, baseline string) {
	t.Helper()
	if err := r.db.QueryRow(`SELECT movie_file_path, source_release, converted_from_release FROM movies WHERE id = 1`).
		Scan(&path, &release, &baseline); err != nil {
		t.Fatal(err)
	}
	return
}

// Reverting an MKV-source conversion puts the byte-identical original back at the same
// path, with the record's release as it was and no baseline; the converted copy is gone,
// and the runner won't pick the film again.
func TestRevertMKVRoundTrip(t *testing.T) {
	ctx := context.Background()
	r, src, e := convertedFilm(t, "Film.mkv")
	if _, _, base := r.movieRow(t); base != origRelease {
		t.Fatalf("baseline after converting: %q", base)
	}
	got, err := r.Revert(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.HoldState != HoldReverted || got.RevertedAt == 0 {
		t.Fatalf("ledger: %+v", got)
	}
	if b, _ := os.ReadFile(src); string(b) != strings.Repeat("x", 1000) {
		t.Fatal("the original isn't back byte for byte")
	}
	if path, rel, base := r.movieRow(t); path != src || rel != origRelease || base != "" {
		t.Fatalf("record: %s / %q / baseline %q", path, rel, base)
	}
	if exists(filepath.Dir(e.HoldPath)) {
		t.Fatal("the hold folder (and the converted copy) should be gone")
	}
	if kind, _, _ := r.skipOf(t, movieKey(1)); kind != SkipReverted || !r.skips.waitingKeys(ctx)[movieKey(1)] {
		t.Fatalf("skip %q — the runner would pick it again", kind)
	}
	if r.swapCount(t) != 0 {
		t.Fatal("the journal row was left behind")
	}
	if _, err := r.Revert(ctx, e.ID); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("a second revert: %v", err)
	}
}

// Reverting an MP4-source conversion removes the .mkv, restores the .mp4 and repoints.
func TestRevertMP4RoundTrip(t *testing.T) {
	r, src, e := convertedFilm(t, "Film.mp4")
	mkv := finalPathFor(src)
	if _, err := r.Revert(context.Background(), e.ID); err != nil {
		t.Fatal(err)
	}
	if sizeOf(src) != 1000 || exists(mkv) {
		t.Fatalf("mp4 %d bytes, mkv left %v", sizeOf(src), exists(mkv))
	}
	if path, rel, _ := r.movieRow(t); path != src || rel != origRelease {
		t.Fatalf("record: %s / %q", path, rel)
	}
}

// Revert refuses — touching nothing — when the library file isn't the converted one any
// more: a different size on disk (re-imported in place) or another path on the record.
func TestRevertRefusesAChangedFile(t *testing.T) {
	ctx := context.Background()
	r, src, e := convertedFilm(t, "Film.mkv")
	writeFile(t, src, 777, 'u') // an upgrade landed at the same name
	if _, err := r.Revert(ctx, e.ID); !errors.Is(err, ErrRevertChanged) {
		t.Fatalf("got %v, want ErrRevertChanged", err)
	}
	if sizeOf(src) != 777 || sizeOf(e.HoldPath) != 1000 {
		t.Fatal("a refused revert moved something")
	}

	r2, src2, e2 := convertedFilm(t, "Film.mkv")
	other := filepath.Join(filepath.Dir(src2), "Film.2160p.mkv")
	if _, err := r2.db.Exec(`UPDATE movies SET movie_file_path = ? WHERE id = 1`, other); err != nil {
		t.Fatal(err)
	}
	if _, err := r2.Revert(ctx, e2.ID); !errors.Is(err, ErrRevertChanged) {
		t.Fatalf("got %v, want ErrRevertChanged", err)
	}
	if sizeOf(src2) != 500 || sizeOf(e2.HoldPath) != 1000 {
		t.Fatal("a refused revert moved something")
	}
}

// A file being converted can't be reverted at the same time.
func TestRevertWaitsForARunningJob(t *testing.T) {
	r, _, e := convertedFilm(t, "Film.mkv")
	it, _ := parseKey(movieKey(1))
	if r.claim(it, "Film", false) == nil {
		t.Fatal("couldn't claim")
	}
	if _, err := r.Revert(context.Background(), e.ID); !errors.Is(err, ErrRevertBusy) {
		t.Fatalf("got %v, want ErrRevertBusy", err)
	}
}

// A two-episode file is repointed for both episodes, each getting its release back.
func TestRevertTwoEpisodeFile(t *testing.T) {
	ctx := context.Background()
	r := newSpaceRig(t)
	r.series = series.NewService(r.db, nil, r.lib, r.log)
	src := filepath.Join(r.lib, "Show", "Show - S01E01-E02.mkv")
	writeFile(t, src, 1000, 'x')
	for _, q := range []string{
		`INSERT INTO series (id, tmdb_id, title, monitored) VALUES (1, 7, 'Show', 1)`,
		`INSERT INTO seasons (series_id, season_number) VALUES (1, 1)`,
	} {
		if _, err := r.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for ep := 1; ep <= 2; ep++ {
		if _, err := r.db.Exec(`INSERT INTO episodes (series_id, season_number, episode_number, absolute_number, monitored, has_file, file_path, size_bytes, source_release)
			VALUES (1, 1, ?, ?, 1, 1, ?, 1000, 'Show.S01E01E02.1080p.BluRay.x264-GRP')`, ep, ep, src); err != nil {
			t.Fatal(err)
		}
	}
	mi := film("h264", 1920, 1080, 35000, aud("truehd", "eng", 8))
	mi.SizeBytes = 1000
	plan := Plan{VideoCodec: "hevc"}
	stubProbe(t, &MediaInfo{VideoCodec: "hevc", DurationSec: mi.DurationSec, AudioTracks: 1})
	stubDisks(t, r.scratch, 1<<50, 1<<50)
	dst := filepath.Join(r.scratch, "convert-1.mkv")
	writeFile(t, dst, 500, 'y')
	it, _ := parseKey(episodeKey(1, 1, 1))
	job := r.claim(it, "Show - S01E01", true)
	rec := r.record(job)
	rec.srcPath, rec.srcInfo = src, mi
	rec.setPlan(plan)
	r.beginHistory(job)
	r.finalizeOutput(ctx, job, src, dst, mi, plan)
	if job.State != StateDone {
		t.Fatalf("conversion: %s (%s)", job.State, job.Note)
	}
	e := r.ledger(t, job)
	if _, err := r.Revert(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	for ep := 1; ep <= 2; ep++ {
		f := r.series.CurrentEpisodeFile(ctx, 1, 1, ep)
		if f.Path != src || f.SizeBytes != 1000 || f.SourceRelease != "Show.S01E01E02.1080p.BluRay.x264-GRP" || f.ConvertedFromRelease != "" {
			t.Fatalf("E%02d: %+v", ep, f)
		}
	}
	if sizeOf(src) != 1000 {
		t.Fatal("the original isn't back")
	}
}

// A revert the restart cut off is finished when both files had moved, and rolled back to
// the converted state when only the first had.
func TestRecoverInterruptedRevert(t *testing.T) {
	ctx := context.Background()
	journalOf := func(e HistoryEntry, state string) swapRow {
		return swapRow{part: revertPartFor(e), final: e.SrcPath, src: e.OutPath, kind: "movie", movieID: 1,
			state: state, holdPath: e.HoldPath, op: opRevert, historyID: e.ID}
	}
	t.Run("first move only", func(t *testing.T) {
		r, src, e := convertedFilm(t, "Film.mkv")
		w := journalOf(e, swapReverting)
		r.journal(t, w)
		if err := os.Rename(src, w.part); err != nil { // the converted file moved aside, then the crash
			t.Fatal(err)
		}
		r.recoverSwaps(ctx)
		if sizeOf(src) != 500 || sizeOf(e.HoldPath) != 1000 || exists(w.part) || r.swapCount(t) != 0 {
			t.Fatalf("converted %d, held %d, part %v", sizeOf(src), sizeOf(e.HoldPath), exists(w.part))
		}
		if got := r.ledger(t, &Job{rec: &jobRecord{historyID: e.ID}}); got.HoldState != HoldHeld {
			t.Fatalf("ledger %s, want still held", got.HoldState)
		}
	})
	t.Run("both moves", func(t *testing.T) {
		r, src, e := convertedFilm(t, "Film.mp4")
		w := journalOf(e, swapReverting)
		r.journal(t, w)
		if err := os.Rename(e.HoldPath, src); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(e.OutPath, w.part); err != nil {
			t.Fatal(err)
		}
		r.recoverSwaps(ctx)
		if path, rel, _ := r.movieRow(t); path != src || rel != origRelease || sizeOf(src) != 1000 || exists(e.OutPath) {
			t.Fatalf("record %s / %q, original %d bytes", path, rel, sizeOf(src))
		}
		if got := r.ledger(t, &Job{rec: &jobRecord{historyID: e.ID}}); got.HoldState != HoldReverted || r.swapCount(t) != 0 {
			t.Fatalf("ledger %s, journal %d", got.HoldState, r.swapCount(t))
		}
	})
}
