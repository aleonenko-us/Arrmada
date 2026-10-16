package automation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/movies"
)

// sparseVideo writes a sparse video file of size bytes at path.
func sparseVideo(t *testing.T, path string, size int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

// An import that has to copy (downloads and library on different drives) reports how far
// the copy has got, up to the whole download, and still lands and settles the review.
func TestReviewImportReportsCopyProgress(t *testing.T) {
	defer library.ForceCopies()()
	c, _, ctx := reviewTestCoord(t)
	mustExec(t, c, `INSERT INTO movies (id, tmdb_id, title, year, monitored) VALUES (5, 1, 'Arrival', 2016, 1)`)
	dl := t.TempDir()
	const size = 60 << 20
	sparseVideo(t, filepath.Join(dl, "Arrival.2016.1080p.BluRay.mkv"), size)
	id := seedReview(t, c, "movie", 5, dl)

	var mu sync.Mutex
	var seen [][2]int64
	pctx := WithImportProgress(ctx, func(copied, total int64) {
		mu.Lock()
		seen = append(seen, [2]int64{copied, total})
		mu.Unlock()
	})
	if err := c.ImportReview(pctx, id, 0, ""); err != nil {
		t.Fatal(err)
	}
	if len(seen) < 2 || seen[0][0] != 0 {
		t.Fatalf("progress reports = %v, want a start and an end", seen)
	}
	last := seen[len(seen)-1]
	if last[0] != size || last[1] != size {
		t.Errorf("last report = %v, want %d of %d", last, size, size)
	}
	for i := 1; i < len(seen); i++ {
		if seen[i][0] < seen[i-1][0] {
			t.Fatalf("progress went backwards: %v", seen)
		}
	}
	var status string
	_ = c.db.QueryRow(`SELECT status FROM import_reviews WHERE id = ?`, id).Scan(&status)
	if status != "resolved" {
		t.Errorf("review status = %q, want resolved", status)
	}
	var imported int
	_ = c.db.QueryRow(`SELECT COUNT(*) FROM movie_events WHERE movie_id = 5 AND event = 'imported'`).Scan(&imported)
	if imported != 1 {
		t.Errorf("imported history lines = %d, want 1", imported)
	}
}

// Import into… for a download Arrmada didn't grab reports its copy the same way, and its
// up-front checks refuse what they can before anything runs.
func TestUnmanagedImportReportsCopyProgress(t *testing.T) {
	defer library.ForceCopies()()
	c, _, ctx := reviewTestCoord(t)
	mustExec(t, c, `INSERT INTO movies (id, tmdb_id, title, year, monitored) VALUES (5, 1, 'Arrival', 2016, 1)`)
	dl := t.TempDir()
	const size = 60 << 20
	sparseVideo(t, filepath.Join(dl, "Arrival.2016.1080p.BluRay.mkv"), size)
	it := download.Item{Hash: "feed", Name: "Arrival.2016.1080p.BluRay"}

	if _, err := c.CheckUnmanagedImport(ctx, it, filepath.Join(dl, "gone"), "movie", 5); !errors.Is(err, ErrDownloadGone) {
		t.Errorf("gone files: %v", err)
	}
	if _, err := c.CheckUnmanagedImport(ctx, it, dl, "series", 5); !errors.Is(err, ErrModuleOff) {
		t.Errorf("module off: %v", err)
	}
	if title, err := c.CheckUnmanagedImport(ctx, it, dl, "movie", 5); err != nil || title != "Arrival" {
		t.Fatalf("good import: %q, %v", title, err)
	}
	var last [2]int64
	pctx := WithImportProgress(ctx, func(copied, total int64) { last = [2]int64{copied, total} })
	if err := c.ImportUnmanaged(pctx, it, dl, "movie", 5); err != nil {
		t.Fatal(err)
	}
	if last != [2]int64{size, size} {
		t.Errorf("last report = %v, want %d of %d", last, size, size)
	}
	if _, err := c.CheckUnmanagedImport(ctx, it, dl, "movie", 5); !errors.Is(err, ErrAlreadyImported) {
		t.Errorf("second import: %v", err)
	}
}

// An import that fails past its checks says why in the title's history (Activity →
// History lists it under Failed) and leaves the review open to try something else.
func TestReviewImportFailureIsInHistory(t *testing.T) {
	c, _, ctx := reviewTestCoord(t)
	mustExec(t, c, `INSERT INTO movies (id, tmdb_id, title, year, monitored) VALUES (5, 1, 'Arrival', 2016, 1)`)
	dl := t.TempDir() // nothing in it to import
	if err := os.WriteFile(filepath.Join(dl, "readme.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	id := seedReview(t, c, "movie", 5, dl)
	err := c.ImportReview(ctx, id, 0, "")
	if err == nil {
		t.Fatal("an import of nothing succeeded")
	}
	var detail string
	if qerr := c.db.QueryRow(`SELECT detail FROM movie_events WHERE movie_id = 5 AND event = 'failed'`).Scan(&detail); qerr != nil {
		t.Fatalf("no failed history line: %v", qerr)
	}
	if !strings.Contains(detail, "Couldn't import from review") || !strings.Contains(detail, "Some.Release") {
		t.Errorf("history detail = %q", detail)
	}
	var status string
	_ = c.db.QueryRow(`SELECT status FROM import_reviews WHERE id = ?`, id).Scan(&status)
	if status != "pending" {
		t.Errorf("review status = %q, want still pending", status)
	}
}

// What can be refused is refused by CheckReviewImport, before anything runs, with the
// same errors the import gives — and none of them writes a failed line to history.
func TestCheckReviewImportRefusals(t *testing.T) {
	c, _, ctx := reviewTestCoord(t)
	mustExec(t, c, `INSERT INTO movies (id, tmdb_id, title, year, monitored) VALUES (5, 1, 'Arrival', 2016, 1)`)

	gone := seedReview(t, c, "movie", 5, filepath.Join(t.TempDir(), "removed"))
	if _, err := c.CheckReviewImport(ctx, gone, 0, ""); !errors.Is(err, ErrDownloadGone) {
		t.Errorf("gone download: %v", err)
	}
	// Series isn't wired on this coordinator: the module is off.
	off := seedReview(t, c, "series", 3, t.TempDir())
	if _, err := c.CheckReviewImport(ctx, off, 0, ""); !errors.Is(err, ErrModuleOff) {
		t.Errorf("module off: %v", err)
	}
	if _, err := c.CheckReviewImport(ctx, 999, 0, ""); !errors.Is(err, ErrReviewNotFound) {
		t.Errorf("missing review: %v", err)
	}
	settled := seedReview(t, c, "movie", 5, t.TempDir())
	mustExec(t, c, `UPDATE import_reviews SET status = 'resolved' WHERE id = ?`, settled)
	if _, err := c.CheckReviewImport(ctx, settled, 0, ""); !errors.Is(err, ErrReviewNotFound) {
		t.Errorf("settled review: %v", err)
	}
	ok := seedReview(t, c, "movie", 5, t.TempDir())
	tg, err := c.CheckReviewImport(ctx, ok, 0, "")
	if err != nil || tg.Dest != 5 || tg.Title != "Arrival" {
		t.Errorf("good review: %+v, %v", tg, err)
	}
	// The import refuses the same way, and a refusal is not a failure in history.
	if err := c.ImportReview(ctx, gone, 0, ""); !errors.Is(err, ErrDownloadGone) {
		t.Errorf("import of a gone download: %v", err)
	}
	var failed int
	_ = c.db.QueryRow(`SELECT COUNT(*) FROM movie_events WHERE event = 'failed'`).Scan(&failed)
	if failed != 0 {
		t.Errorf("refusals wrote %d failed history line(s)", failed)
	}
}

// A failed line in History is for an import that failed: not for one stopped (a cancel,
// a shutdown), and not a second line for a refusal the film's own import already wrote.
func TestImportFailedHistoryLines(t *testing.T) {
	c, _, ctx := reviewTestCoord(t)
	mustExec(t, c, `INSERT INTO movies (id, tmdb_id, title, year, monitored) VALUES (5, 1, 'Arrival', 2016, 1)`)
	r := Review{Name: "Arrival.2016", MediaType: "movie"}
	count := func() int {
		var n int
		_ = c.db.QueryRow(`SELECT COUNT(*) FROM movie_events WHERE movie_id = 5 AND event = 'failed'`).Scan(&n)
		return n
	}
	stopped, cancel := context.WithCancel(ctx)
	cancel()
	c.importFailed(stopped, r, 5, "review", stopped.Err())
	c.importFailed(ctx, r, 5, "review", fmt.Errorf("copying: %w", context.Canceled))
	c.importFailed(ctx, r, 5, "review", fmt.Errorf("%w (4K)", movies.ErrPathOwnedByOtherTrack))
	if n := count(); n != 0 {
		t.Fatalf("%d failed line(s) for stops and a refusal already explained", n)
	}
	c.importFailed(ctx, r, 5, "review", errors.New("no space left on device"))
	if n := count(); n != 1 {
		t.Fatalf("a real failure wrote %d line(s), want 1", n)
	}
}

// A mapping job that finds its download gone when it starts refuses, the way the checks
// would have — no failed line, the review still open.
func TestApplyReviewMapRefusesAGoneDownload(t *testing.T) {
	h, rid, dir, _ := mapHarness(t)
	plan, err := h.c.PlanReviewMap(h.ctx, rid, 0, []FileMapping{{RelPath: "Pilot.mkv", Season: 1, Episodes: []int{1}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := h.c.ApplyReviewMap(h.ctx, plan); !errors.Is(err, ErrDownloadGone) {
		t.Fatalf("apply after the download went: %v, want ErrDownloadGone", err)
	}
	var failed int
	_ = h.c.db.QueryRow(`SELECT COUNT(*) FROM series_events WHERE event = 'failed'`).Scan(&failed)
	if failed != 0 {
		t.Errorf("a refusal wrote %d failed line(s)", failed)
	}
}
