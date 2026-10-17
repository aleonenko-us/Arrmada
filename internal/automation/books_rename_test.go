package automation

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/listening"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/store"
)

type testBookNaming struct{ audio, ebook library.BookNaming }

func (n testBookNaming) AudiobookNaming() library.BookNaming { return n.audio }
func (n testBookNaming) EbookNaming() library.BookNaming     { return n.ebook }

type renameRig struct {
	t             *testing.T
	c             *Coordinator
	svc           *books.Service
	st            *store.Store
	ebooks, audio string
}

func newRenameRig(t *testing.T) *renameRig {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	lib := t.TempDir()
	r := &renameRig{t: t, st: st, ebooks: filepath.Join(lib, "ebooks"), audio: filepath.Join(lib, "audiobooks")}
	for _, d := range []string{r.ebooks, r.audio} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r.svc = books.NewService(st.DB(), nil, slog.Default())
	imp := library.NewImporter("", slog.Default())
	imp.SetRoots("", "", r.ebooks, r.audio)
	r.c = &Coordinator{books: r.svc, db: st.DB(), log: slog.Default(), imp: imp}
	return r
}

// scheme switches the importer to a naming scheme (track template for audiobooks).
func (r *renameRig) scheme(folder, track string) {
	r.c.imp.SetBookNaming(testBookNaming{
		audio: library.BookNaming{Folder: folder, File: "{Title}", Track: track},
		ebook: library.BookNaming{Folder: folder, File: "{Title}"},
	})
}

func (r *renameRig) book(key, title, author, series string, pos float64) books.Book {
	r.t.Helper()
	ctx := context.Background()
	added, _ := r.svc.AddWorks(ctx, []metadata.BookResult{{Key: key, Title: title, Author: author}}, "", true)
	if len(added) != 1 {
		r.t.Fatalf("book %q not added", title)
	}
	if series != "" {
		if err := r.svc.SetSeries(ctx, added[0].ID, series, pos); err != nil {
			r.t.Fatal(err)
		}
	}
	b, _ := r.svc.Get(ctx, added[0].ID)
	return b
}

// write puts a file whose content is its own name at root/rel.
func (r *renameRig) write(root, rel string) string {
	r.t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(filepath.Base(rel)), 0o644); err != nil {
		r.t.Fatal(err)
	}
	return p
}

func (r *renameRig) mustHave(path, content string) {
	r.t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		r.t.Errorf("expected %s: %v", path, err)
		return
	}
	if string(b) != content {
		r.t.Errorf("%s holds %q, want %q", path, b, content)
	}
}

func (r *renameRig) mustBeGone(path string) {
	r.t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		r.t.Errorf("%s should be gone (err=%v)", path, err)
	}
}

func (r *renameRig) plan() []BookRenameItem {
	r.t.Helper()
	items, err := r.c.BookRenamePlan(context.Background(), nil)
	if err != nil {
		r.t.Fatal(err)
	}
	return items
}

func statuses(items []BookRenameItem) map[string]string {
	out := map[string]string{}
	for _, it := range items {
		k := it.Title + "/" + it.Kind
		if it.Label != "" {
			k += "/" + it.Label
		}
		out[k] = it.Status
		if it.Reason != "" {
			out[k] += ": " + it.Reason
		}
	}
	return out
}

func okItems(items []BookRenameItem) []BookRenameItem {
	var out []BookRenameItem
	for _, it := range items {
		if it.Status == BookRenameOK {
			out = append(out, it)
		}
	}
	return out
}

// A library imported under the default scheme is already named right: the plan moves
// nothing.
func TestBookRenameDefaultSchemeMovesNothing(t *testing.T) {
	r := newRenameRig(t)
	ctx := context.Background()
	b := r.book("hc:1", "Golden Son", "Pierce Brown", "Red Rising Saga", 2)
	single := r.write(r.audio, "Pierce Brown/Golden Son/Golden Son.m4b")
	_ = r.svc.MarkImported(ctx, b.ID, books.KindAudiobook, single, "M4B", 10, 1)
	eb := r.write(r.ebooks, "Pierce Brown/Golden Son/Golden Son.epub")
	_ = r.svc.MarkImported(ctx, b.ID, books.KindEbook, eb, "EPUB", 10, 1)
	d := r.book("hc:2", "Dune", "Frank Herbert", "", 0)
	for _, n := range []string{"Dune 1.mp3", "Dune 2.mp3", "Dune 10.mp3"} {
		r.write(r.audio, "Frank Herbert/Dune/"+n)
	}
	_ = r.svc.MarkImported(ctx, d.ID, books.KindAudiobook, filepath.Join(r.audio, "Frank Herbert", "Dune"), "MP3", 30, 3)
	for k, s := range statuses(r.plan()) {
		if s != BookRenameSame {
			t.Errorf("%s: %s, want same", k, s)
		}
	}
}

// The whole rename: a series book's audiobook, ebook and full-cast version move to the
// Audiobookshelf layout; a multi-file audiobook moves file by file, renumbered in play
// order; probed details follow the files; the old folders go once empty, the cover goes
// along; and everyone's listening place stays exactly where it was.
func TestBookRenameAppliesSchemeAndKeepsPlaces(t *testing.T) {
	r := newRenameRig(t)
	ctx := context.Background()
	db := r.st.DB()

	gs := r.book("hc:1", "Golden Son", "Pierce Brown", "Red Rising Saga", 2)
	gsAudio := r.write(r.audio, "Pierce Brown/Golden Son/Golden Son.m4b")
	r.write(r.audio, "Pierce Brown/Golden Son/cover.jpg")
	_ = r.svc.MarkImported(ctx, gs.ID, books.KindAudiobook, gsAudio, "M4B", 10, 1)
	gsEbook := r.write(r.ebooks, "Pierce Brown/Golden Son/Golden Son.epub")
	_ = r.svc.MarkImported(ctx, gs.ID, books.KindEbook, gsEbook, "EPUB", 10, 1)
	v, err := r.svc.AddAudioVersion(ctx, gs.ID, "Full cast", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	gsFull := r.write(r.audio, "Pierce Brown/Golden Son (Full cast)/Golden Son (Full cast).m4b")
	_ = r.svc.SetAudioVersionFile(ctx, v.ID, gsFull, "M4B", 10, 1)

	dune := r.book("hc:2", "Dune", "Frank Herbert", "Dune Chronicles", 1)
	duneDir := filepath.Join(r.audio, "Frank Herbert", "Dune")
	old := map[string]string{} // content → old path
	for _, n := range []string{"Dune 10.mp3", "Dune 2.mp3", "Dune 1.mp3"} {
		old[n] = r.write(r.audio, "Frank Herbert/Dune/"+n)
		if _, err := db.Exec(`INSERT INTO audio_file_meta (path, size, mtime, duration) VALUES (?, 1, 1, 600)`, old[n]); err != nil {
			t.Fatal(err)
		}
	}
	_ = r.svc.MarkImported(ctx, dune.ID, books.KindAudiobook, duneDir, "MP3", 30, 3)

	// A listener is 25 minutes into Dune (in its third file) and halfway through the
	// full-cast Golden Son.
	res, err := db.Exec(`INSERT INTO users (username, password_hash, role) VALUES ('listener', 'x', 'requester')`)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := res.LastInsertId()
	ls := listening.NewStore(db)
	for key, pos := range map[string]float64{listening.ItemKey(dune.ID, 0): 1500, listening.ItemKey(gs.ID, v.ID): 3000} {
		if _, err := ls.SetProgress(ctx, uid, key, pos, 6000, nil, "Pixel"); err != nil {
			t.Fatal(err)
		}
	}

	r.scheme("{Author}/{Series}/{Series Position} - {Title}", "{Title} - Part {Part}")
	plan := r.plan()
	if got := len(okItems(plan)); got != 4 {
		t.Fatalf("want 4 editions to rename, got %v", statuses(plan))
	}
	token := BookPlanToken(plan)
	if again := BookPlanToken(r.plan()); again != token {
		t.Fatal("planning twice must give the same token")
	}
	results, err := r.c.ApplyBookRename(ctx, okItems(plan), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, res := range results {
		if !res.Renamed {
			t.Errorf("%+v not renamed", res)
		}
	}

	newGS := filepath.Join(r.audio, "Pierce Brown", "Red Rising Saga", "2 - Golden Son")
	r.mustHave(filepath.Join(newGS, "Golden Son.m4b"), "Golden Son.m4b")
	r.mustHave(filepath.Join(newGS, "cover.jpg"), "cover.jpg")
	r.mustHave(filepath.Join(r.ebooks, "Pierce Brown", "Red Rising Saga", "2 - Golden Son", "Golden Son.epub"), "Golden Son.epub")
	r.mustHave(filepath.Join(r.audio, "Pierce Brown", "Red Rising Saga", "2 - Golden Son (Full cast)", "Golden Son (Full cast).m4b"), "Golden Son (Full cast).m4b")
	newDune := filepath.Join(r.audio, "Frank Herbert", "Dune Chronicles", "1 - Dune")
	for content, want := range map[string]string{"Dune 1.mp3": "Dune - Part 01.mp3", "Dune 2.mp3": "Dune - Part 02.mp3", "Dune 10.mp3": "Dune - Part 03.mp3"} {
		r.mustHave(filepath.Join(newDune, want), content)
	}
	// Emptied folders are gone; nothing was deleted along the way.
	for _, gone := range []string{gsAudio, filepath.Dir(gsAudio), filepath.Dir(gsFull), duneDir, filepath.Join(r.ebooks, "Pierce Brown", "Golden Son")} {
		r.mustBeGone(gone)
	}

	// The records point at the new places.
	gs2, _ := r.svc.Get(ctx, gs.ID)
	if gs2.Audiobook.Path != filepath.Join(newGS, "Golden Son.m4b") || !strings.HasSuffix(gs2.Ebook.Path, filepath.Join("2 - Golden Son", "Golden Son.epub")) {
		t.Errorf("paths not repointed: %q / %q", gs2.Audiobook.Path, gs2.Ebook.Path)
	}
	if len(gs2.AudioVersions) != 1 || !strings.HasSuffix(gs2.AudioVersions[0].File.Path, filepath.Join("2 - Golden Son (Full cast)", "Golden Son (Full cast).m4b")) {
		t.Errorf("version not repointed: %+v", gs2.AudioVersions)
	}
	dune2, _ := r.svc.Get(ctx, dune.ID)
	if dune2.Audiobook.Path != newDune || dune2.Audiobook.FileCount != 3 {
		t.Errorf("multi-file audiobook at %q (%d files), want %q", dune2.Audiobook.Path, dune2.Audiobook.FileCount, newDune)
	}
	// Probed durations followed the files: none left at an old path, all three at new ones.
	var stale, moved int
	_ = db.QueryRow(`SELECT COUNT(*) FROM audio_file_meta WHERE path LIKE ?`, duneDir+string(filepath.Separator)+"%").Scan(&stale)
	_ = db.QueryRow(`SELECT COUNT(*) FROM audio_file_meta WHERE path LIKE ?`, newDune+string(filepath.Separator)+"%").Scan(&moved)
	if stale != 0 || moved != 3 {
		t.Errorf("audio_file_meta: %d rows at the old folder, %d at the new, want 0 and 3", stale, moved)
	}
	// Listening places are keyed by book, and untouched.
	for key, want := range map[string]float64{listening.ItemKey(dune.ID, 0): 1500, listening.ItemKey(gs.ID, v.ID): 3000} {
		p, ok, err := ls.Progress(ctx, uid, key)
		if err != nil || !ok || p.Position != want {
			t.Errorf("place in %s = %+v (%v, %v), want %v", key, p, ok, err, want)
		}
	}
	// The timeline says what happened; the plan is now clean.
	if evs, _ := r.svc.Events(ctx, dune.ID, 10); len(evs) == 0 || evs[0].Event != "renamed" {
		t.Errorf("Dune's timeline should end with the rename: %+v", evs)
	}
	for k, s := range statuses(r.plan()) {
		if s != BookRenameSame {
			t.Errorf("after the rename %s: %s, want same", k, s)
		}
	}
	// And a rescan finds the book where the scheme put it.
	if got := r.c.imp.BookLibraryFiles(bookName(gs2, nil, false), "EPUB", "M4B"); len(got) != 2 {
		t.Errorf("rescan should find the ebook and the audiobook in their scheme folders: %+v", got)
	}
}

// A rename never overwrites: a file already at the new name is a conflict, and a library
// that changed after the preview is left alone.
func TestBookRenameConflictsAndChanges(t *testing.T) {
	r := newRenameRig(t)
	ctx := context.Background()
	b := r.book("hc:1", "Golden Son", "Pierce Brown", "Red Rising Saga", 2)
	p := r.write(r.audio, "Pierce Brown/Golden Son/Golden Son.m4b")
	_ = r.svc.MarkImported(ctx, b.ID, books.KindAudiobook, p, "M4B", 10, 1)
	other := r.book("hc:2", "Morning Star", "Pierce Brown", "Red Rising Saga", 3)
	op := r.write(r.audio, "Pierce Brown/Morning Star/Morning Star.m4b")
	_ = r.svc.MarkImported(ctx, other.ID, books.KindAudiobook, op, "M4B", 10, 1)
	r.write(r.audio, "Pierce Brown/Red Rising Saga/2 - Golden Son/Golden Son.m4b") // someone else's file

	r.scheme("{Author}/{Series}/{Series Position} - {Title}", "{Original}")
	plan := r.plan()
	st := statuses(plan)
	if !strings.HasPrefix(st["Golden Son/audiobook"], BookRenameConflict) || st["Morning Star/audiobook"] != BookRenameOK {
		t.Fatalf("plan = %v", st)
	}
	// The file is moved by hand after the preview: applying skips it, and moves nothing.
	if err := os.Rename(op, op+".bak"); err != nil {
		t.Fatal(err)
	}
	res, err := r.c.ApplyBookRename(ctx, okItems(plan), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Renamed || res[0].Reason != bookReasonChanged {
		t.Errorf("result = %+v, want skipped as changed", res)
	}
	r.mustHave(p, "Golden Son.m4b")
	r.mustHave(op+".bak", "Morning Star.m4b")
}

// A scheme whose series folder comes and goes can put one book's folder inside
// another's — a book titled "Mistborn" and the books of the Mistborn series. Neither a
// rename nor an import may do it: a multi-file audiobook is everything under its folder.
func TestBookRenameAndImportRefuseNesting(t *testing.T) {
	r := newRenameRig(t)
	ctx := context.Background()
	a := r.book("hc:1", "Mistborn", "Brandon Sanderson", "", 0)
	for _, n := range []string{"01.mp3", "02.mp3"} {
		r.write(r.audio, "Brandon Sanderson/Mistborn/"+n)
	}
	_ = r.svc.MarkImported(ctx, a.ID, books.KindAudiobook, filepath.Join(r.audio, "Brandon Sanderson", "Mistborn"), "MP3", 20, 2)
	b := r.book("hc:2", "The Final Empire", "Brandon Sanderson", "Mistborn", 1)
	bp := r.write(r.audio, "Brandon Sanderson/The Final Empire/The Final Empire.m4b")
	_ = r.svc.MarkImported(ctx, b.ID, books.KindAudiobook, bp, "M4B", 10, 1)

	r.scheme("{Author}/{Series}/{Series Position} - {Title}", "{Original}")
	st := statuses(r.plan())
	if st["Mistborn/audiobook"] != BookRenameSame || !strings.Contains(st["The Final Empire/audiobook"], "inside another book's folder") {
		t.Errorf("plan = %v", st)
	}
	files := []library.FoundFile{{Path: r.write(t.TempDir(), "tfe.m4b")}}
	if err := r.c.bookImportRefusal(ctx, b, bookName(b, nil, true), files); err == nil {
		t.Error("an import into another book's folder must be refused")
	}
	// The default scheme can't nest, and is never refused.
	r.scheme(library.DefaultAudiobookFolder, library.DefaultAudiobookTrack)
	if err := r.c.bookImportRefusal(ctx, b, bookName(b, nil, true), files); err != nil {
		t.Errorf("default scheme refused: %v", err)
	}
}

// Renaming a multi-file audiobook must never change what plays when.
func TestSamePlayOrder(t *testing.T) {
	it := &BookRenameItem{Proposed: "/new", Files: []BookRenameFile{
		{From: "/old/1.mp3", To: "/new/a.mp3"}, {From: "/old/2.mp3", To: "/new/b.mp3"}, {From: "/old/10.mp3", To: "/new/c.mp3"},
	}}
	if !samePlayOrder([]string{"1.mp3", "2.mp3", "10.mp3"}, it) {
		t.Error("a, b, c keep 1, 2, 10's order")
	}
	it.Files[2].To = "/new/0.mp3"
	if samePlayOrder([]string{"1.mp3", "2.mp3", "10.mp3"}, it) {
		t.Error("moving the last file first must be caught")
	}
}

// A library scan skips folders whose every file is already a book's — a folder a scheme
// named "2 - Golden Son" isn't looked up again — but not one with a new file in it.
func TestScanSkipsKnownFolders(t *testing.T) {
	known := knownBookPaths([]books.Book{{
		ID:        1,
		Audiobook: &books.BookFile{Path: "/a/Dune", FileCount: 3},
		Ebook:     &books.BookFile{Path: "/e/Golden Son/Golden Son.epub", FileCount: 1},
	}})
	if !known.covers(library.BookFolder{Audiobooks: []library.FoundFile{{Path: "/a/Dune/CD1/1.mp3"}}}) {
		t.Error("files under a recorded folder are known")
	}
	if !known.covers(library.BookFolder{Ebooks: []library.FoundFile{{Path: "/e/Golden Son/Golden Son.epub"}}}) {
		t.Error("a recorded file is known")
	}
	if known.covers(library.BookFolder{Ebooks: []library.FoundFile{{Path: "/e/Golden Son/Golden Son.epub"}, {Path: "/e/Golden Son/Golden Son.mobi"}}}) {
		t.Error("a folder with a file no book has must be scanned")
	}
}
