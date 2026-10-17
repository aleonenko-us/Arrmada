package automation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/metadata"
)

// journaled sets up Dune as a single-file audiobook with a rename to "Dune Chronicles"
// journaled but not started; it returns the old and new paths.
func journaled(t *testing.T, r *renameRig) (books.Book, string, string) {
	t.Helper()
	ctx := context.Background()
	b := r.book("hc:1", "Dune", "Frank Herbert", "", 0)
	p := r.write(r.audio, "Frank Herbert/Dune/Dune.m4b")
	_ = r.svc.MarkImported(ctx, b.ID, books.KindAudiobook, p, "M4B", 10, 1)
	to := filepath.Join(r.audio, "Frank Herbert", "Dune Chronicles", "1 - Dune", "Dune.m4b")
	if err := r.svc.JournalRename(ctx, b.ID, 0, books.KindAudiobook, p, to, []books.PathMove{{From: p, To: to}}); err != nil {
		t.Fatal(err)
	}
	return b, p, to
}

func journalLen(t *testing.T, r *renameRig) int {
	t.Helper()
	left, err := r.svc.UnfinishedRenames(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return len(left)
}

// A file at neither place is unknown: nothing moves, and the entry waits for a later start.
func TestRecoverWaitsWhenAFileIsNowhere(t *testing.T) {
	r := newRenameRig(t)
	r.write(r.audio, "Other/keep.m4b") // the library isn't empty
	_, p, _ := journaled(t, r)
	if err := os.Rename(p, p+".elsewhere"); err != nil {
		t.Fatal(err)
	}
	if n := r.c.RecoverBookRenames(context.Background()); n != 0 || journalLen(t, r) != 1 {
		t.Errorf("recovered %d, journal %d: an unknown state must keep the entry", n, journalLen(t, r))
	}
}

// The record names the new place: the rename was recorded after all. The entry is
// dropped and nothing moves.
func TestRecoverDropsAnEntryTheRecordAlreadyReflects(t *testing.T) {
	r := newRenameRig(t)
	ctx := context.Background()
	b, p, to := journaled(t, r)
	if err := r.c.imp.Move(p, to); err != nil {
		t.Fatal(err)
	}
	if _, err := r.st.DB().Exec(`UPDATE books SET audiobook_path = ? WHERE id = ?`, to, b.ID); err != nil {
		t.Fatal(err)
	}
	if n := r.c.RecoverBookRenames(ctx); n != 1 || journalLen(t, r) != 0 {
		t.Fatalf("recovered %d, journal %d", n, journalLen(t, r))
	}
	r.mustHave(to, "Dune.m4b")
	r.mustBeGone(p)
}

// The record names somewhere else entirely: nothing moves, the entry goes, and the book's
// history says so.
func TestRecoverMovesNothingWhenRecordedElsewhere(t *testing.T) {
	r := newRenameRig(t)
	ctx := context.Background()
	b, p, to := journaled(t, r)
	if err := r.c.imp.Move(p, to); err != nil {
		t.Fatal(err)
	}
	elsewhere := r.write(r.audio, "Frank Herbert/Dune (2021)/Dune.m4b")
	if _, err := r.st.DB().Exec(`UPDATE books SET audiobook_path = ? WHERE id = ?`, elsewhere, b.ID); err != nil {
		t.Fatal(err)
	}
	if n := r.c.RecoverBookRenames(ctx); n != 1 || journalLen(t, r) != 0 {
		t.Fatalf("recovered %d, journal %d", n, journalLen(t, r))
	}
	r.mustHave(to, "Dune.m4b")
	r.mustBeGone(p)
}

// Recording or clearing an edition any other way settles its journal entry.
func TestRecordingAnEditionClearsItsJournal(t *testing.T) {
	r := newRenameRig(t)
	ctx := context.Background()
	b, p, _ := journaled(t, r)
	if err := r.svc.MarkImported(ctx, b.ID, books.KindAudiobook, p, "M4B", 10, 1); err != nil {
		t.Fatal(err)
	}
	if journalLen(t, r) != 0 {
		t.Error("MarkImported must clear the edition's journal entry")
	}
	_ = r.svc.JournalRename(ctx, b.ID, 0, books.KindAudiobook, p, p+"x", []books.PathMove{{From: p, To: p + "x"}})
	if err := r.svc.ClearEdition(ctx, b.ID, books.KindAudiobook); err != nil {
		t.Fatal(err)
	}
	if journalLen(t, r) != 0 {
		t.Error("ClearEdition must clear the edition's journal entry")
	}
	v, _ := r.svc.AddAudioVersion(ctx, b.ID, "Full cast", nil, true)
	_ = r.svc.JournalRename(ctx, b.ID, v.ID, books.KindAudiobook, "a", "b", []books.PathMove{{From: "a", To: "b"}})
	if err := r.svc.SetAudioVersionFile(ctx, v.ID, p, "M4B", 1, 1); err != nil {
		t.Fatal(err)
	}
	if journalLen(t, r) != 0 {
		t.Error("SetAudioVersionFile must clear the version's journal entry")
	}
}

// A busy book is reported once per holder of its lock, not on every sweep.
func TestBusyIsNotedOncePerHolder(t *testing.T) {
	r := newRenameRig(t)
	unlock, _ := r.c.tryLockBook(7)
	if !r.c.firstBusy(7) || r.c.firstBusy(7) || r.c.firstBusy(7) {
		t.Error("the first busy check reports, the rest stay quiet")
	}
	unlock()
	unlock, _ = r.c.tryLockBook(7)
	defer unlock()
	if !r.c.firstBusy(7) {
		t.Error("a new holder is reported again")
	}
}

// Every way to change a busy book's files says it's busy, so a person is told to try again.
func TestBusyBookRefusesEveryChange(t *testing.T) {
	r := newRenameRig(t)
	ctx := context.Background()
	b := r.book("hc:1", "Dune", "Frank Herbert", "", 0)
	p := r.write(r.audio, "Frank Herbert/Dune/Dune.m4b")
	_ = r.svc.MarkImported(ctx, b.ID, books.KindAudiobook, p, "M4B", 10, 1)
	v, _ := r.svc.AddAudioVersion(ctx, b.ID, "Full cast", nil, true)
	src := r.write(t.TempDir(), "full.m4b")
	unlock, _ := r.c.tryLockBook(b.ID)
	defer unlock()
	for name, err := range map[string]error{
		"rescan":         r.c.RescanBook(ctx, b.ID),
		"version import": r.c.ManualImportAudioVersion(ctx, b.ID, v.ID, src),
		"delete":         r.c.DeleteBookEdition(ctx, b.ID, books.KindAudiobook),
		"version delete": r.c.DeleteAudioVersionFile(ctx, b.ID, v.ID),
		"version remove": r.c.RemoveAudioVersion(ctx, b.ID, v.ID, true),
	} {
		if !errors.Is(err, ErrBookBusy) {
			t.Errorf("%s while busy: %v", name, err)
		}
	}
	r.mustHave(p, "Dune.m4b")
}

// A pack whose other book is being changed waits for the next sweep as a whole: counting
// it imported would leave that book's files behind.
func TestPackWaitsForABusyBook(t *testing.T) {
	c, svc, _, ctx := packTestCoord(t)
	added, _ := svc.AddWorks(ctx, []metadata.BookResult{
		{Key: "hc:1", Title: "Fire & Blood", Author: "George R.R. Martin"},
		{Key: "hc:2", Title: "A Game of Thrones", Author: "George R.R. Martin"},
	}, "", true)
	fire, got := added[0], added[1]
	dl := filepath.Join(t.TempDir(), "pack")
	for _, n := range []string{"A Game of Thrones/01.m4b", "A Game of Thrones/02.m4b"} {
		p := filepath.Join(dl, n)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte("audio"), 0o644)
	}
	unlock, _ := c.tryLockBook(got.ID)
	imported, hadFiles, waiting := c.importBookContentWaiting(ctx, fire, dl, "h1", "pack")
	unlock()
	if imported || !hadFiles || !waiting {
		t.Fatalf("imported=%v hadFiles=%v waiting=%v: the pack must wait", imported, hadFiles, waiting)
	}
	if b, _ := svc.Get(ctx, got.ID); b.Audiobook != nil {
		t.Error("a busy book must not be imported into")
	}
	if imported, _, waiting := c.importBookContentWaiting(ctx, fire, dl, "h1", "pack"); !imported || waiting {
		t.Errorf("once free the pack imports: imported=%v waiting=%v", imported, waiting)
	}
}
