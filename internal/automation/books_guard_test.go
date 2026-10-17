package automation

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/library"
)

// The nesting guard works both ways: a book whose folder would hold another book's files
// is refused, as is one whose folder would sit inside another book's.
func TestNestingGuardIsSymmetric(t *testing.T) {
	r := newRenameRig(t)
	ctx := context.Background()
	r.scheme(absFolder, "{Original}")
	series := r.book("hc:2", "The Well of Ascension", "Brandon Sanderson", "Mistborn", 2)
	r.write(r.audio, "Brandon Sanderson/Mistborn/2 - The Well of Ascension/01.mp3")
	r.write(r.audio, "Brandon Sanderson/Mistborn/2 - The Well of Ascension/02.mp3")
	_ = r.svc.MarkImported(ctx, series.ID, books.KindAudiobook, filepath.Join(r.audio, "Brandon Sanderson", "Mistborn", "2 - The Well of Ascension"), "MP3", 20, 2)
	standalone := r.book("hc:1", "Mistborn", "Brandon Sanderson", "", 0)
	err := r.c.bookImportRefusal(ctx, standalone, bookName(standalone, nil, true), []library.FoundFile{{Path: "/dl/mistborn.m4b"}})
	if err == nil || !strings.Contains(err.Error(), "around another book's files") {
		t.Errorf("a single-file book whose folder holds another book must be refused, got %v", err)
	}
}

// A book whose files are being changed is left alone by everything else: an import waits
// for the next sweep, a rename skips it and says why, a rescan skips it, a merge refuses.
func TestBookLockKeepsChangesApart(t *testing.T) {
	r := newRenameRig(t)
	ctx := context.Background()
	b := r.book("hc:1", "Golden Son", "Pierce Brown", "Red Rising Saga", 2)
	p := r.write(r.audio, "Pierce Brown/Golden Son/Golden Son.m4b")
	_ = r.svc.MarkImported(ctx, b.ID, books.KindAudiobook, p, "M4B", 10, 1)
	r.scheme(absFolder, "{Original}")

	unlock, ok := r.c.tryLockBook(b.ID)
	if !ok {
		t.Fatal("the lock should be free")
	}
	if _, again := r.c.tryLockBook(b.ID); again {
		t.Fatal("a held lock must not be taken twice")
	}
	moved, skipped, err := r.c.RenameBookFiles(ctx, b.ID)
	if err != nil || moved != 0 || len(skipped) != 1 || skipped[0].Reason != errBookBusy.Error() {
		t.Errorf("rename while busy: moved %d, skipped %+v, err %v", moved, skipped, err)
	}
	src := r.write(t.TempDir(), "new.epub")
	if r.c.importBookEdition(ctx, b, books.KindEbook, []library.FoundFile{{Path: src}}, "", "") {
		t.Error("an import into a busy book must wait")
	}
	if err := r.c.ManualImportBook(ctx, b.ID, 0, src); err == nil || !strings.Contains(err.Error(), "try again") {
		t.Errorf("a manual import into a busy book: %v", err)
	}
	r.mustHave(p, "Golden Son.m4b")
	unlock()

	if moved, _, err := r.c.RenameBookFiles(ctx, b.ID); err != nil || moved != 1 {
		t.Errorf("rename once free: moved %d, err %v", moved, err)
	}
}

// A rename a crash stopped part way is rolled back at the next start: the files that had
// moved go back where the record says the book is, and the journal is cleared.
func TestRecoverInterruptedBookRename(t *testing.T) {
	r := newRenameRig(t)
	ctx := context.Background()
	b := r.book("hc:1", "Dune", "Frank Herbert", "", 0)
	dir := filepath.Join(r.audio, "Frank Herbert", "Dune")
	one := r.write(r.audio, "Frank Herbert/Dune/1.mp3")
	two := r.write(r.audio, "Frank Herbert/Dune/2.mp3")
	_ = r.svc.MarkImported(ctx, b.ID, books.KindAudiobook, dir, "MP3", 20, 2)
	newDir := filepath.Join(r.audio, "Frank Herbert", "Dune Chronicles", "1 - Dune")
	moves := []books.PathMove{{From: one, To: filepath.Join(newDir, "1.mp3")}, {From: two, To: filepath.Join(newDir, "2.mp3")}}
	if err := r.svc.JournalRename(ctx, b.ID, 0, books.KindAudiobook, moves); err != nil {
		t.Fatal(err)
	}
	// The crash: the first file moved, the second didn't, the record wasn't written.
	if err := r.c.imp.Move(one, moves[0].To); err != nil {
		t.Fatal(err)
	}
	if n := r.c.RecoverBookRenames(ctx); n != 1 {
		t.Fatalf("recovered %d editions, want 1", n)
	}
	r.mustHave(one, "1.mp3")
	r.mustHave(two, "2.mp3")
	r.mustBeGone(moves[0].To)
	if left, _ := r.svc.UnfinishedRenames(ctx); len(left) != 0 {
		t.Errorf("journal not cleared: %+v", left)
	}
	if n := r.c.RecoverBookRenames(ctx); n != 0 {
		t.Errorf("a second start recovered %d", n)
	}
	// A finished rename leaves no journal behind.
	r.scheme(absFolder, "{Original}")
	_ = r.svc.SetSeries(ctx, b.ID, "Dune Chronicles", 1)
	if moved, _, err := r.c.RenameBookFiles(ctx, b.ID); err != nil || moved != 1 {
		t.Fatalf("rename: moved %d, err %v", moved, err)
	}
	if left, _ := r.svc.UnfinishedRenames(ctx); len(left) != 0 {
		t.Errorf("a finished rename left a journal entry: %+v", left)
	}
}

// A cover in a flat author folder is the author's: it stays when the last book leaves.
func TestAuthorCoverStaysInFlatFolder(t *testing.T) {
	r := newRenameRig(t)
	ctx := context.Background()
	b := r.book("hc:1", "Golden Son", "Pierce Brown", "", 0)
	p := r.write(r.audio, "PB/Golden Son.m4b")
	cover := r.write(r.audio, "PB/cover.jpg")
	_ = r.svc.MarkImported(ctx, b.ID, books.KindAudiobook, p, "M4B", 10, 1)
	if moved, _, err := r.c.RenameBookFiles(ctx, b.ID); err != nil || moved != 1 {
		t.Fatalf("rename: moved %d, err %v", moved, err)
	}
	r.mustHave(filepath.Join(r.audio, "Pierce Brown", "Golden Son", "Golden Son.m4b"), "Golden Son.m4b")
	r.mustHave(cover, "cover.jpg")
	r.mustBeGone(filepath.Join(r.audio, "Pierce Brown", "Golden Son", "cover.jpg"))
}

// A version whose label has nothing a file name can hold still gets a folder of its own.
func TestVersionWithUnprintableLabelKeepsItsOwnFolder(t *testing.T) {
	r := newRenameRig(t)
	b := r.book("hc:1", "Golden Son", "Pierce Brown", "", 0)
	v := books.AudioVersion{ID: 7, Label: "???"}
	std := r.c.imp.BookDir(true, bookName(b, nil, true), "M4B")
	ver := r.c.imp.BookDir(true, bookName(b, &v, true), "M4B")
	if ver == std || filepath.Base(ver) != "Golden Son (Version 7)" {
		t.Errorf("standard %q, version %q", std, ver)
	}
}
