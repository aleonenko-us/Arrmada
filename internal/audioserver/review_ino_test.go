package audioserver

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/library"
)

// Review: a file probed BEFORE the upgrade (row with ino = '') and not listed since the
// upgrade, then renamed: does the id a listener's download holds survive?
func TestReviewPreUpgradeRowRenamedBeforeListing(t *testing.T) {
	for _, withRow := range []bool{true, false} {
		t.Run(map[bool]string{true: "probed-row", false: "no-row"}[withRow], func(t *testing.T) {
			h := newHarness(t)
			h.signIn()
			ctx := context.Background()
			key := itemKeyFor(h.book.ID, 0)
			dir := h.book.Audiobook.Path
			// What the pre-upgrade code gave each file (and what Lissen downloaded by).
			oldIDs := map[string]string{}
			for _, n := range []string{"Part 1.mp3", "Part 2.mp3", "Part 10.mp3"} {
				oldIDs[n] = inoFor(n)
				if withRow {
					fi, err := os.Stat(filepath.Join(dir, n))
					if err != nil {
						t.Fatal(err)
					}
					// A row the old probe wrote: no ino column value (migration default '').
					if _, err := h.srv.db.ExecContext(ctx,
						`INSERT INTO audio_file_meta (path, size, mtime, duration, bitrate, codec, title, chapters, meta_version) VALUES (?, ?, ?, 100, 64000, 'mp3', '', '[]', 2)`,
						filepath.Join(dir, n), fi.Size(), fi.ModTime().UnixMilli()); err != nil {
						t.Fatal(err)
					}
				}
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
			res, err := c.ApplyBookRename(ctx, todo, nil)
			if err != nil || len(res) != 1 || !res[0].Renamed {
				t.Fatalf("rename: %+v %v", res, err)
			}
			h.srv.OnBookChanged(ctx, h.book.ID, nil)
			media := obj1(t, h.json("GET", "/api/items/"+key, nil)["media"])
			first := obj1(t, list1(t, media["audioFiles"])[0])["ino"].(string)
			if first != oldIDs["Part 1.mp3"] {
				t.Errorf("ID CHANGE: first file's id was %s before the upgrade, %s after the rename", oldIDs["Part 1.mp3"], first)
			}
		})
	}
}
