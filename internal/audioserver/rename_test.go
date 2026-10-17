package audioserver

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/library"
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
	before, _, _ := files()
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
