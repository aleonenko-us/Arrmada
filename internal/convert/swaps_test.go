package convert

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// addMovieFile records a movie whose file is name (in the rig's library) with size bytes.
func (r *spaceRig) addMovieFile(t *testing.T, id int64, name string, size int) string {
	t.Helper()
	path := filepath.Join(r.lib, name)
	writeFile(t, path, size, 'x')
	if _, err := r.db.Exec(`INSERT INTO movies (id, tmdb_id, title, monitored, has_file, movie_file_path) VALUES (?, ?, ?, 1, 1, ?)`,
		id, 1000+id, strings.TrimSuffix(name, filepath.Ext(name)), path); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFile(t *testing.T, path string, size int, b byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat(string(b), size)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (r *spaceRig) journal(t *testing.T, w swapRow) {
	t.Helper()
	if w.kind == "" {
		w.kind = "movie"
	}
	if w.op == "" {
		w.op = "convert"
	}
	if _, err := r.db.Exec(`INSERT INTO convert_swaps (part, final, src, kind, movie_id, series_id, season, episode, state, hold_path, op, history_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, w.part, w.final, w.src, w.kind, w.movieID, w.seriesID, w.season, w.episode,
		w.state, w.holdPath, w.op, w.historyID); err != nil {
		t.Fatal(err)
	}
}

func (r *spaceRig) swapCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM convert_swaps`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (r *spaceRig) moviePath(t *testing.T, id int64) string {
	t.Helper()
	var p string
	if err := r.db.QueryRow(`SELECT movie_file_path FROM movies WHERE id = ?`, id).Scan(&p); err != nil {
		t.Fatal(err)
	}
	return p
}

func sizeOf(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return -1
	}
	return len(b)
}

// A crash while the encode was being staged leaves a row that names the part: the next
// startup removes the part and the row, and the original is untouched.
func TestRecoverStagingRemovesThePart(t *testing.T) {
	r := newSpaceRig(t)
	src := r.addMovie(t, 1, "Film", 1000)
	part := partPathFor(src)
	writeFile(t, part, 123, 'y') // a copy cut short
	r.journal(t, swapRow{part: part, final: src, src: src, movieID: 1, state: swapStaging})

	r.recoverSwaps(context.Background())
	if exists(part) {
		t.Fatal("the partial staged copy survived the restart")
	}
	if r.swapCount(t) != 0 {
		t.Fatal("the journal row was left behind")
	}
	if sizeOf(src) != 1000 || r.moviePath(t, 1) != src {
		t.Fatal("the original or its record changed")
	}
}

// An MP4 whose original was never moved aside keeps its .mp4, and no .mkv is renamed in
// beside it.
func TestRecoverKeepsAnOriginalThatWasNeverRetired(t *testing.T) {
	r := newSpaceRig(t)
	src := r.addMovieFile(t, 1, "Film.mp4", 1000)
	final := finalPathFor(src)
	part := partPathFor(src)
	writeFile(t, part, 500, 'y')
	r.journal(t, swapRow{part: part, final: final, src: src, movieID: 1, state: swapRetired})

	r.recoverSwaps(context.Background())
	if exists(part) || exists(final) {
		t.Fatalf("part %v, final %v — want neither", exists(part), exists(final))
	}
	if sizeOf(src) != 1000 || r.moviePath(t, 1) != src || r.swapCount(t) != 0 {
		t.Fatal("the original, its record or the journal is wrong")
	}
}

// A swap that can't be journalled never starts: the encode is discarded before anything
// moves, and the original stays exactly where it was.
func TestJournalFailureAbortsBeforeAnyMove(t *testing.T) {
	r, src, dst, mi, plan := finalizeRig(t)
	if _, err := r.db.Exec(`CREATE TRIGGER no_swaps BEFORE INSERT ON convert_swaps BEGIN SELECT RAISE(ABORT, 'disk I/O error'); END`); err != nil {
		t.Fatal(err)
	}
	moved := false
	oldMove := moveFileFn
	moveFileFn = func(from, to string) error { moved = true; return oldMove(from, to) }
	t.Cleanup(func() { moveFileFn = oldMove })

	it, _ := parseKey(movieKey(1))
	job := r.claim(it, "Remux", true)
	r.finalizeOutput(context.Background(), job, src, dst, mi, plan)
	if job.State != StateFailed || !strings.Contains(job.Note, "could not journal the swap") {
		t.Fatalf("state %s (%s)", job.State, job.Note)
	}
	if moved || exists(partPathFor(src)) {
		t.Fatal("the encode was staged without a journal row")
	}
	if sizeOf(src) != 1000 {
		t.Fatal("the original was touched")
	}
}

// The sweep removes an old .arrpart nobody owns, and keeps one the journal names, one a
// running job is writing, and one that's recent.
func TestOrphanPartSweep(t *testing.T) {
	ctx := context.Background()
	r := newSpaceRig(t)
	old := time.Now().Add(-48 * time.Hour)
	mk := func(name string, age time.Time) string {
		p := filepath.Join(r.lib, name)
		writeFile(t, p, 10, 'z')
		if err := os.Chtimes(p, age, age); err != nil {
			t.Fatal(err)
		}
		// The sweep walks the folders of indexed files.
		if err := r.index.upsert(ctx, indexRow{Path: strings.TrimSuffix(p, ".mkv"+partSuffix) + ".mkv", MediaType: "movie",
			MovieID: int64(len(name)), Title: name}); err != nil {
			t.Fatal(err)
		}
		return p
	}
	orphan := mk("Orphan.mkv"+partSuffix, old)
	journalled := mk("Journal.mkv"+partSuffix, old)
	running := mk("Running.mkv"+partSuffix, old)
	recent := mk("Recent.mkv"+partSuffix, time.Now())
	r.journal(t, swapRow{part: journalled, final: "x", src: "x", state: swapStaging})
	r.addMovieFile(t, 77, "Running.mp4", 10) // the file a running job converts
	it, _ := parseKey(movieKey(77))
	if r.claim(it, "Running", false) == nil {
		t.Fatal("couldn't claim")
	}

	if n := r.sweepOrphanParts(ctx); n != 1 {
		t.Fatalf("removed %d, want 1", n)
	}
	if exists(orphan) || !exists(journalled) || !exists(running) || !exists(recent) {
		t.Fatalf("orphan %v journalled %v running %v recent %v", exists(orphan), exists(journalled), exists(running), exists(recent))
	}
}

// A converted MP4 never moves aside an .mkv another library record points at; a stray one
// nobody owns is moved out of the way as before.
func TestSameStemGuard(t *testing.T) {
	r, _, dst, mi, plan := finalizeRig(t)
	src := r.addMovieFile(t, 2, "Other.mp4", 1000)
	taken := finalPathFor(src)
	writeFile(t, taken, 700, 't')
	if _, err := r.db.Exec(`INSERT INTO movies (id, tmdb_id, title, monitored, has_file, movie_file_path) VALUES (3, 1003, 'Owner', 1, 1, ?)`, taken); err != nil {
		t.Fatal(err)
	}
	it, _ := parseKey(movieKey(2))
	job := r.claim(it, "Other", true)
	r.finalizeOutput(context.Background(), job, src, dst, mi, plan)
	if kind, _, _ := r.skipOf(t, movieKey(2)); job.State != StateSkipped || kind != SkipNameTaken {
		t.Fatalf("state %s, skip %q (%s)", job.State, kind, job.Note)
	}
	if sizeOf(taken) != 700 || sizeOf(src) != 1000 || exists(partPathFor(src)) {
		t.Fatal("a file was moved, or the staged copy was left behind")
	}

	// Nobody owns it: it waits in the hold beside the original and the conversion lands.
	if _, err := r.db.Exec(`DELETE FROM movies WHERE id = 3`); err != nil {
		t.Fatal(err)
	}
	r.skips.clear(context.Background(), movieKey(2))
	writeFile(t, dst, 500, 'y')
	job = r.convertNow(t, 2, "Other", src, dst, mi, plan)
	if job.State != StateDone {
		t.Fatalf("state %s (%s), want done", job.State, job.Note)
	}
	if sizeOf(taken) != 500 || r.moviePath(t, 2) != taken {
		t.Fatalf("the converted file isn't in place: %d bytes, record %s", sizeOf(taken), r.moviePath(t, 2))
	}
	held := r.ledger(t, job).HoldPath
	if sizeOf(held) != 1000 || sizeOf(filepath.Join(filepath.Dir(held), "Other.mkv")) != 700 {
		t.Fatal("the original and the stray should both be in the hold")
	}
}
