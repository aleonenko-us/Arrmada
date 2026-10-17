package automation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/library"
)

const absFolder = "{Author}/{Series}/{Series Position} - {Title}"

// 1. A single-file book whose folder holds another book's folder (Title == Series) is
// allowed by the guard; a manual rescan then records the other book's files as its own.
func TestReviewRescanSwallowsNestedBook(t *testing.T) {
	r := newRenameRig(t)
	ctx := context.Background()
	r.scheme(absFolder, "{Original}")
	a := r.book("OL1W", "Mistborn", "Brandon Sanderson", "", 0)
	b := r.book("OL2W", "The Well of Ascension", "Brandon Sanderson", "Mistborn", 2)

	// Import guard for each.
	if err := r.c.bookImportRefusal(ctx, a, bookName(a, nil, true), []library.FoundFile{{Path: "/x/a.m4b"}}); err != nil {
		t.Logf("A refused: %v", err)
	}
	ap := r.write(r.audio, "Brandon Sanderson/Mistborn/Mistborn.m4b")
	_ = r.svc.MarkImported(ctx, a.ID, books.KindAudiobook, ap, "M4B", 10, 1)
	if err := r.c.bookImportRefusal(ctx, b, bookName(b, nil, true), []library.FoundFile{{Path: "/x/01.mp3"}, {Path: "/x/02.mp3"}}); err != nil {
		t.Logf("B refused: %v", err)
	} else {
		t.Logf("B allowed into %s", r.c.imp.BookDir(true, bookName(b, nil, true), "MP3"))
	}
	bdir := filepath.Join(r.audio, "Brandon Sanderson", "Mistborn", "2 - The Well of Ascension")
	r.write(r.audio, "Brandon Sanderson/Mistborn/2 - The Well of Ascension/01.mp3")
	r.write(r.audio, "Brandon Sanderson/Mistborn/2 - The Well of Ascension/02.mp3")
	_ = r.svc.MarkImported(ctx, b.ID, books.KindAudiobook, bdir, "MP3", 20, 2)

	r.c.RescanBook(ctx, a.ID)
	got, _ := r.svc.Get(ctx, a.ID)
	t.Logf("after rescan, Mistborn audiobook = %s (%d files)", got.Audiobook.Path, got.Audiobook.FileCount)
	if got.Audiobook.Path != ap {
		t.Errorf("BUG: rescan repointed Mistborn's audiobook at %s (%d files)", got.Audiobook.Path, got.Audiobook.FileCount)
	}
}

// 2. A folder edition renamed into a folder that already holds other (unrecorded) audio
// files is "ok": they join the book.
func TestReviewRenameIntoOccupiedFolder(t *testing.T) {
	r := newRenameRig(t)
	ctx := context.Background()
	b := r.book("OL3W", "Golden Son", "Pierce Brown", "Red Rising Saga", 2)
	dir := filepath.Join(r.audio, "Pierce Brown", "Golden Son")
	r.write(r.audio, "Pierce Brown/Golden Son/01.mp3")
	r.write(r.audio, "Pierce Brown/Golden Son/02.mp3")
	_ = r.svc.MarkImported(ctx, b.ID, books.KindAudiobook, dir, "MP3", 20, 2)
	// Leftover, unrecorded copy where the new scheme puts it.
	r.write(r.audio, "Pierce Brown/Red Rising Saga/2 - Golden Son/Golden Son - old rip.mp3")
	r.scheme(absFolder, "{Original}")
	items := r.plan()
	for _, it := range items {
		t.Logf("plan: %s %s -> %s [%s] %s", it.Title, it.Current, it.Proposed, it.Status, it.Reason)
	}
	var todo []BookRenameItem
	for _, it := range items {
		if it.Status == BookRenameOK {
			todo = append(todo, it)
		}
	}
	res, _ := r.c.ApplyBookRename(ctx, todo, nil)
	t.Logf("res: %+v", res)
	got, _ := r.svc.Get(ctx, b.ID)
	n := 0
	for _, f := range library.FindBookFiles(got.Audiobook.Path) {
		n++
		t.Logf("  item file: %s", f.Path)
	}
	if n != 2 {
		t.Errorf("BUG: the renamed audiobook now has %d files (recorded %d)", n, got.Audiobook.FileCount)
	}
}

// 3. A track-only rename keeps the folder: does meta_updated_at move?
func TestReviewTrackOnlyRenameBumpsMeta(t *testing.T) {
	r := newRenameRig(t)
	ctx := context.Background()
	b := r.book("OL4W", "Dune", "Frank Herbert", "", 0)
	dir := filepath.Join(r.audio, "Frank Herbert", "Dune")
	r.write(r.audio, "Frank Herbert/Dune/a.mp3")
	r.write(r.audio, "Frank Herbert/Dune/b.mp3")
	_ = r.svc.MarkImported(ctx, b.ID, books.KindAudiobook, dir, "MP3", 20, 2)
	before, _ := r.svc.Get(ctx, b.ID)
	r.scheme(library.DefaultAudiobookFolder, "{Title} - Part {Part}")
	var todo []BookRenameItem
	for _, it := range r.plan() {
		t.Logf("plan: %s -> %s [%s]", it.Current, it.Proposed, it.Status)
		if it.Status == BookRenameOK {
			todo = append(todo, it)
		}
	}
	res, _ := r.c.ApplyBookRename(ctx, todo, nil)
	t.Logf("res: %+v", res)
	after, _ := r.svc.Get(ctx, b.ID)
	t.Logf("meta_updated_at %d -> %d", before.MetaUpdatedAt, after.MetaUpdatedAt)
	if after.MetaUpdatedAt == before.MetaUpdatedAt {
		t.Errorf("BUG: files renamed (new inos) but meta_updated_at did not move")
	}
}

// 4. Default scheme: long titles render differently from the old bookDirIn/clean.
func TestReviewDefaultLongTitle(t *testing.T) {
	title := strings.Repeat("Ж", 122) // 244 bytes
	n := library.BookName{Author: "A", Title: title}
	got := library.BookFolderPath("/r", library.DefaultAudiobookNaming, true, n, "M4B")
	old := filepath.Join("/r", "A", title)
	if got != old {
		t.Errorf("REGRESSION: default folder %d bytes vs old %d bytes", len(filepath.Base(got)), len(filepath.Base(old)))
	}
	f := library.BookFileName(library.DefaultAudiobookNaming, true, n, "/x/y.m4b")
	if f != title+".m4b" {
		t.Errorf("REGRESSION: default file name %d bytes vs old %d", len(f), len(title)+4)
	}
	_ = os.Getenv
}

// 5. An ebook dropped into a recorded multi-file audiobook folder (shared root) is
// "known" and the scan never backfills the ebook edition.
func TestReviewScanHidesEbookInAudioFolder(t *testing.T) {
	known := knownBookPaths([]books.Book{{
		ID:        1,
		Audiobook: &books.BookFile{Path: "/books/Frank Herbert/Dune", FileCount: 3},
	}})
	bf := library.BookFolder{
		Ebooks:     []library.FoundFile{{Path: "/books/Frank Herbert/Dune/Dune.epub"}},
		Audiobooks: []library.FoundFile{{Path: "/books/Frank Herbert/Dune/01.mp3"}},
	}
	if known.covers(bf) {
		t.Error("REGRESSION: an unrecorded ebook inside a recorded audiobook folder is treated as known; the scan skips it")
	}
}
