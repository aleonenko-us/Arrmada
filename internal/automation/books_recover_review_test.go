package automation

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/library"
)

// Review: the library share is missing at start-up. Recovery must not move anything AND
// must keep the journal for the next start.
func TestReviewRecoverWithLibraryUnmounted(t *testing.T) {
	r := newRenameRig(t)
	ctx := context.Background()
	b := r.book("hc:1", "Dune", "Frank Herbert", "", 0)
	dir := filepath.Join(r.audio, "Frank Herbert", "Dune")
	one := r.write(r.audio, "Frank Herbert/Dune/1.mp3")
	two := r.write(r.audio, "Frank Herbert/Dune/2.mp3")
	_ = r.svc.MarkImported(ctx, b.ID, books.KindAudiobook, dir, "MP3", 20, 2)
	newDir := filepath.Join(r.audio, "Frank Herbert", "Dune Chronicles", "1 - Dune")
	moves := []books.PathMove{{From: one, To: filepath.Join(newDir, "1.mp3")}, {From: two, To: filepath.Join(newDir, "2.mp3")}}
	if err := r.svc.JournalRename(ctx, b.ID, 0, books.KindAudiobook, dir, newDir, moves); err != nil {
		t.Fatal(err)
	}
	if err := r.c.imp.Move(one, moves[0].To); err != nil { // crash after the first move
		t.Fatal(err)
	}
	// "Unmount": the share's contents vanish.
	hidden := r.audio + ".offline"
	if err := os.Rename(r.audio, hidden); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(r.audio, 0o755) // empty mount point
	n := r.c.RecoverBookRenames(ctx)
	left, _ := r.svc.UnfinishedRenames(ctx)
	t.Logf("recovered=%d, journal entries left=%d", n, len(left))
	if len(left) == 0 {
		t.Errorf("DATA-LOSS RISK: journal cleared while the library was offline (recovered=%d)", n)
	}
	// "Remount" and start again.
	_ = os.Remove(r.audio)
	if err := os.Rename(hidden, r.audio); err != nil {
		t.Fatal(err)
	}
	n2 := r.c.RecoverBookRenames(ctx)
	_, errOld := os.Stat(one)
	_, errNew := os.Stat(moves[0].To)
	t.Logf("second start recovered=%d; old place present=%v; new place present=%v", n2, errOld == nil, errNew == nil)
	if errOld != nil && errNew == nil {
		t.Errorf("SPLIT BOOK: 1.mp3 stays in %s while the record says %s; nothing will put it back", newDir, dir)
	}
}

// Review: a crash between the hardlink and the remove (moveNoClobber) leaves one file at
// both places. What does recovery do?
func TestReviewRecoverHardlinkBothPlaces(t *testing.T) {
	r := newRenameRig(t)
	ctx := context.Background()
	b := r.book("hc:1", "Dune", "Frank Herbert", "", 0)
	p := r.write(r.audio, "Frank Herbert/Dune/Dune.m4b")
	_ = r.svc.MarkImported(ctx, b.ID, books.KindAudiobook, p, "M4B", 10, 1)
	to := filepath.Join(r.audio, "Frank Herbert", "Dune Chronicles", "1 - Dune", "Dune.m4b")
	_ = r.svc.JournalRename(ctx, b.ID, 0, books.KindAudiobook, p, to, []books.PathMove{{From: p, To: to}})
	_ = os.MkdirAll(filepath.Dir(to), 0o755)
	if err := os.Link(p, to); err != nil {
		t.Skip("no hardlinks:", err)
	}
	n := r.c.RecoverBookRenames(ctx)
	_, e1 := os.Stat(p)
	_, e2 := os.Stat(to)
	left, _ := r.svc.UnfinishedRenames(ctx)
	t.Logf("recovered=%d old=%v new=%v journal=%d", n, e1 == nil, e2 == nil, len(left))
	if e1 != nil || e2 == nil || len(left) != 0 {
		t.Errorf("the second name should go and the recorded file stay: old=%v new=%v journal=%d", e1 == nil, e2 == nil, len(left))
	}
	if _, err := os.Stat(filepath.Dir(to)); err == nil {
		t.Error("the folder the rename made should be removed once empty")
	}
}

// Review: a journal entry outlives the rename (split failure, or ClearRenameJournal failed)
// and the record later moves to the new place (rescan / re-import). Recovery at the next
// start ignores the record.
func TestReviewRecoverIgnoresRecord(t *testing.T) {
	r := newRenameRig(t)
	ctx := context.Background()
	b := r.book("hc:1", "Dune", "Frank Herbert", "", 0)
	p := r.write(r.audio, "Frank Herbert/Dune/Dune.m4b")
	_ = r.svc.MarkImported(ctx, b.ID, books.KindAudiobook, p, "M4B", 10, 1)
	to := filepath.Join(r.audio, "Frank Herbert", "Dune Chronicles", "1 - Dune", "Dune.m4b")
	_ = r.svc.JournalRename(ctx, b.ID, 0, books.KindAudiobook, p, to, []books.PathMove{{From: p, To: to}})
	if err := r.c.imp.Move(p, to); err != nil {
		t.Fatal(err)
	}
	// Something records the new place without touching the journal.
	_ = r.svc.MarkImported(ctx, b.ID, books.KindAudiobook, to, "M4B", 10, 1)
	r.c.RecoverBookRenames(ctx)
	got, _ := r.svc.Get(ctx, b.ID)
	_, err := os.Stat(got.Audiobook.Path)
	if err != nil {
		t.Errorf("WRONG WAY: recovery moved the file away from the recorded place %s (now at %s)", got.Audiobook.Path, p)
	}
}

// Review: default template, legacy "Author/Series/N - Title" library where a book's title
// is its series. A single-file ebook import for that book.
func TestReviewDefaultImportLegacySeriesLayout(t *testing.T) {
	r := newRenameRig(t)
	ctx := context.Background()
	r.scheme(library.DefaultAudiobookFolder, "{Original}")
	dune := r.book("hc:1", "Dune", "Frank Herbert", "Dune", 1)
	messiah := r.book("hc:2", "Dune Messiah", "Frank Herbert", "Dune", 2)
	md := filepath.Join(r.ebooks, "Frank Herbert", "Dune", "2 - Dune Messiah")
	r.write(r.ebooks, "Frank Herbert/Dune/2 - Dune Messiah/Dune Messiah.epub")
	_ = r.svc.MarkImported(ctx, messiah.ID, books.KindEbook, filepath.Join(md, "Dune Messiah.epub"), "EPUB", 10, 1)
	err := r.c.bookImportRefusal(ctx, dune, bookName(dune, nil, false), []library.FoundFile{{Path: "/dl/Dune.epub"}})
	t.Logf("default-template import refusal: %v", err)
	if err != nil {
		t.Errorf("REGRESSION: default template refuses an import: %v", err)
	}
	// Same with a custom (non-default) template that renders the same folder.
	r.scheme("{Author}/{Title}/", "{Original}")
	err = r.c.bookImportRefusal(ctx, dune, bookName(dune, nil, false), []library.FoundFile{{Path: "/dl/Dune.epub"}})
	t.Logf("custom template rendering Author/Title: %v", err)
}
