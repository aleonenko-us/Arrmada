package audioserver

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/metadata"
)

type renameNaming struct{}

func (renameNaming) AudiobookNaming() library.BookNaming {
	return library.BookNaming{Folder: "{Author}/{Series}/{Series Position} - {Title}", File: "{Title}", Track: "{Title} - Part {Part}"}
}
func (renameNaming) EbookNaming() library.BookNaming { return library.DefaultEbookNaming }

// A listening app sees a renamed audiobook as the same item: same id, same files in the
// same order (renamed and moved to the new folder), and the listener's place untouched.
func TestBookRenameKeepsItemOrderAndPlace(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	ctx := context.Background()
	key := itemKeyFor(h.book.ID, 0)
	dir := h.book.Audiobook.Path
	// Sizes that tell the three files apart, to read their order back.
	for name, size := range map[string]int{"Part 1.mp3": 12000, "Part 2.mp3": 6000, "Part 10.mp3": 3000} {
		if err := os.WriteFile(filepath.Join(dir, name), bytes.Repeat([]byte("x"), size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h.srv.Invalidate()
	h.json("PATCH", "/api/me/progress/"+key, map[string]any{"currentTime": 1234.5, "duration": 36000})
	files := func() (sizes []float64, names []string, firstIno string) {
		media := obj1(t, h.json("GET", "/api/items/"+key, nil)["media"])
		for i, af := range list1(t, media["audioFiles"]) {
			meta := obj1(t, obj1(t, af)["metadata"])
			sizes = append(sizes, meta["size"].(float64))
			names = append(names, meta["filename"].(string))
			if i == 0 {
				firstIno = obj1(t, af)["ino"].(string)
			}
		}
		return sizes, names, firstIno
	}
	before, _, inoBefore := files()
	if len(before) != 3 || before[0] != 12000 || before[2] != 3000 {
		t.Fatalf("before the rename: sizes %v", before)
	}

	if err := h.srv.books.SetSeries(ctx, h.book.ID, "Dungeon Crawler Carl", 1); err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(dir))
	imp := library.NewImporter("", slog.Default())
	imp.SetRoots("", "", root, root)
	imp.SetBookNaming(renameNaming{})
	c := automation.New(nil, nil, nil, nil, h.srv.db, nil, slog.Default(), "")
	c.SetSeries(nil, imp)
	c.SetBooks(h.srv.books)
	plan, err := c.BookRenamePlan(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var todo []automation.BookRenameItem
	for _, it := range plan {
		if it.Status == automation.BookRenameOK {
			todo = append(todo, it)
		}
	}
	if len(todo) != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	res, err := c.ApplyBookRename(ctx, todo, nil)
	if err != nil || len(res) != 1 || !res[0].Renamed {
		t.Fatalf("rename: %+v %v", res, err)
	}
	// What the book.files_changed outbox consumer does after a rename.
	h.srv.OnBookChanged(ctx, h.book.ID, nil)

	after, names, ino := files()
	if ino != inoBefore {
		t.Errorf("the first file's id changed with its name: %s → %s", inoBefore, ino)
	}
	for i := range before {
		if after[i] != before[i] {
			t.Fatalf("play order changed: sizes %v → %v", before, after)
		}
	}
	want := []string{"Dungeon Crawler Carl - Part 01.mp3", "Dungeon Crawler Carl - Part 02.mp3", "Dungeon Crawler Carl - Part 03.mp3"}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("file %d = %q, want %q", i, names[i], want[i])
		}
	}
	if _, err := os.Stat(filepath.Join(root, "Matt Dinniman", "Dungeon Crawler Carl", "1 - Dungeon Crawler Carl", want[0])); err != nil {
		t.Errorf("the files should be in the scheme's folder: %v", err)
	}
	if got := h.json("GET", "/api/me/progress/"+key, nil)["currentTime"]; got != 1234.5 {
		t.Errorf("place after the rename = %v, want 1234.5", got)
	}
	if code, _ := h.do("GET", "/api/items/"+key+"/file/"+ino, nil, nil); code != 200 {
		t.Errorf("the renamed first file should stream: HTTP %d", code)
	}
}

type fileNaming struct{}

func (fileNaming) AudiobookNaming() library.BookNaming {
	return library.BookNaming{Folder: library.DefaultAudiobookFolder, File: "{Author} - {Title}", Track: library.DefaultAudiobookTrack}
}
func (fileNaming) EbookNaming() library.BookNaming { return library.DefaultEbookNaming }

// A renamed file keeps the id listening apps know it by: the item's files, a new play
// session's tracks and a session opened before the rename all reach it the same way, so a
// phone's download and a book playing right now carry on. The id it gets is the one its
// name always gave it, so nothing anyone already holds changed when ids began to be kept.
func TestRenamedFileKeepsItsID(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	ctx := context.Background()
	root := filepath.Dir(filepath.Dir(h.book.Audiobook.Path))
	added, _ := h.srv.books.AddWorks(ctx, []metadata.BookResult{{Key: "hc:2", Title: "Project Hail Mary", Author: "Andy Weir"}}, "", true)
	if len(added) != 1 {
		t.Fatal("book not added")
	}
	src := filepath.Join(root, "Andy Weir", "Project Hail Mary", "Project Hail Mary.m4b")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, bytes.Repeat([]byte("phm"), 4000), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.srv.books.MarkImported(ctx, added[0].ID, books.KindAudiobook, src, "M4B", 12000, 1); err != nil {
		t.Fatal(err)
	}
	h.srv.Invalidate()
	key := itemKeyFor(added[0].ID, 0)
	itemIno := func() string {
		media := obj1(t, h.json("GET", "/api/items/"+key, nil)["media"])
		return obj1(t, list1(t, media["audioFiles"])[0])["ino"].(string)
	}
	trackURL := func() string {
		play := h.json("POST", "/api/items/"+key+"/play", map[string]any{
			"deviceInfo": map[string]string{"clientName": "Lissen", "deviceId": "dev-1", "deviceName": "Pixel 8"},
		})
		return obj1(t, list1(t, play["audioTracks"])[0])["contentUrl"].(string)
	}
	before := itemIno()
	if before != inoFor("Project Hail Mary.m4b") {
		t.Fatalf("a first listing must give the id the name always gave: %s", before)
	}
	oldURL := trackURL()
	if !strings.HasSuffix(oldURL, "/file/"+before) {
		t.Fatalf("track URL %q doesn't name file %s", oldURL, before)
	}

	imp := library.NewImporter("", slog.Default())
	imp.SetRoots("", "", root, root)
	imp.SetBookNaming(fileNaming{})
	c := automation.New(nil, nil, nil, nil, h.srv.db, nil, slog.Default(), "")
	c.SetSeries(nil, imp)
	c.SetBooks(h.srv.books)
	moved, skipped, err := c.RenameBookFiles(ctx, added[0].ID)
	if err != nil || moved != 1 {
		t.Fatalf("rename: moved %d, skipped %+v, err %v", moved, skipped, err)
	}
	h.srv.OnBookChanged(ctx, added[0].ID, nil)
	if _, err := os.Stat(filepath.Join(filepath.Dir(src), "Andy Weir - Project Hail Mary.m4b")); err != nil {
		t.Fatalf("the file should have its new name: %v", err)
	}
	if after := itemIno(); after != before {
		t.Errorf("the item's file id changed with the rename: %s → %s", before, after)
	}
	if u := trackURL(); u != oldURL {
		t.Errorf("a new session's track URL changed: %s → %s", oldURL, u)
	}
	if code, _ := h.do("GET", oldURL, nil, nil); code != 200 {
		t.Errorf("the track URL from before the rename should still stream: HTTP %d", code)
	}
}
