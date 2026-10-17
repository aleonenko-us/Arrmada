package audioserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/books"
)

// The start-up backfill gives every known file with no id the one its name gives it,
// from the database alone: with the folder gone it still does, and it never changes an id
// already stored.
func TestBackfillFileIDsFromTheDatabaseOnly(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	dir := h.book.Audiobook.Path
	for _, n := range []string{"Part 1.mp3", "Part 2.mp3"} {
		if _, err := h.srv.db.ExecContext(ctx,
			`INSERT INTO audio_file_meta (path, size, mtime, duration) VALUES (?, 1, 1, 100)`, filepath.Join(dir, n)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.srv.db.ExecContext(ctx,
		`INSERT INTO audio_file_meta (path, size, mtime, duration, ino) VALUES (?, 1, 1, 100, 'kept')`, filepath.Join(dir, "Part 10.mp3")); err != nil {
		t.Fatal(err)
	}
	away := dir + ".offline"
	if err := os.Rename(dir, away); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(away, dir) })
	n, err := h.srv.BackfillFileIDs(ctx)
	if err != nil || n != 2 {
		t.Fatalf("backfilled %d (%v), want 2", n, err)
	}
	for name, want := range map[string]string{"Part 1.mp3": inoFor("Part 1.mp3"), "Part 2.mp3": inoFor("Part 2.mp3"), "Part 10.mp3": "kept"} {
		if got := h.srv.probe.storedIno(ctx, filepath.Join(dir, name)); got != want {
			t.Errorf("%s: id %q, want %q", name, got, want)
		}
	}
	if n, _ := h.srv.BackfillFileIDs(ctx); n != 0 {
		t.Errorf("a second backfill stored %d", n)
	}
}

// A file the daily verify finds gone loses its probed details but keeps its id, so if it
// comes back (a share remounted) apps still know it.
func TestForgetKeepsTheFileID(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.rememberAll()
	victim := filepath.Join(h.audioDir(), "Part 2.mp3")
	_, _ = h.srv.db.ExecContext(ctx, `UPDATE audio_file_meta SET ino = 'x2' WHERE path = ?`, victim)
	if err := os.Rename(victim, victim+".gone"); err != nil {
		t.Fatal(err)
	}
	if n := h.srv.Verify(ctx); n != 1 {
		t.Fatalf("verify forgot %d", n)
	}
	if got := h.srv.probe.storedIno(ctx, victim); got != "x2" {
		t.Errorf("the forgotten file's id = %q, want it kept", got)
	}
	if err := os.Rename(victim+".gone", victim); err != nil {
		t.Fatal(err)
	}
	h.srv.Invalidate()
	h.signIn()
	media := obj1(t, h.json("GET", "/api/items/"+itemKeyFor(h.book.ID, 0), nil)["media"])
	if ino := obj1(t, list1(t, media["audioFiles"])[1])["ino"]; ino != "x2" {
		t.Errorf("the returned file's id = %v, want x2", ino)
	}
}

// An ebook's id is stored like an audio file's: renaming the ebook keeps it.
func TestEbookIDSurvivesARename(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	ctx := context.Background()
	epub := filepath.Join(h.audioDir(), "Dungeon Crawler Carl.epub")
	if err := os.WriteFile(epub, []byte("epub"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.srv.books.MarkImported(ctx, h.book.ID, books.KindEbook, epub, "EPUB", 4, 1); err != nil {
		t.Fatal(err)
	}
	h.srv.Invalidate()
	key := itemKeyFor(h.book.ID, 0)
	ebookIno := func() string {
		media := obj1(t, h.json("GET", "/api/items/"+key, nil)["media"])
		return obj1(t, media["ebookFile"])["ino"].(string)
	}
	before := ebookIno()
	if before != inoFor("ebook/Dungeon Crawler Carl.epub") {
		t.Fatalf("first id %s isn't the one the name always gave", before)
	}
	renamed := filepath.Join(h.audioDir(), "Carl.epub")
	if err := os.Rename(epub, renamed); err != nil {
		t.Fatal(err)
	}
	if err := h.srv.books.RepointEdition(ctx, h.book.ID, 0, books.KindEbook, renamed,
		[]books.PathMove{{From: epub, To: renamed}}, "renamed"); err != nil {
		t.Fatal(err)
	}
	h.srv.Invalidate()
	if after := ebookIno(); after != before {
		t.Errorf("the ebook's id changed with its name: %s → %s", before, after)
	}
}
