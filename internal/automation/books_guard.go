package automation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/tristenlammi/arrmada/internal/library"
)

// One book's files are changed by several things — an import, a rename (one book or the
// whole library), a rescan, a merge — and two at once could each act on a folder the
// other is half way through moving. Each takes the book's lock first. None waits for it:
// an import that finds the book busy is retried by the next import sweep, a rename skips
// the book and says why, and a person is told to try again.

var errBookBusy = errors.New("another change to this book's files is running (an import, a rename, a rescan or a merge) — try again in a moment")

// tryLockBook takes book id's lock, or reports that something else holds it.
func (c *Coordinator) tryLockBook(id int64) (unlock func(), ok bool) {
	v, _ := c.bookLocks.LoadOrStore(id, &sync.Mutex{})
	m := v.(*sync.Mutex)
	if !m.TryLock() {
		return nil, false
	}
	return m.Unlock, true
}

// notOthersFilter returns a test that is true for a path that belongs to no other
// edition: not another one's recorded file, and not inside a folder another one records.
// self are the edition keys being rescanned, whose own paths don't count. A rescan reads a
// book's scheme folder with everything under it, and a scheme whose folders come and go
// can put another book's folder inside it ("Author/Mistborn" and the Mistborn series'
// "Author/Mistborn/2 - The Well of Ascension"): without this the rescan recorded that
// book's files as this one's. A failed read keeps nothing.
func (c *Coordinator) notOthersFilter(ctx context.Context, self ...string) (func(string) bool, error) {
	all, err := c.books.List(ctx)
	if err != nil {
		return nil, err
	}
	mine := map[string]bool{}
	for _, k := range self {
		mine[k] = true
	}
	var others []string
	for _, b := range all {
		for _, e := range editionsOf(b) {
			if !mine[e.key()] {
				others = append(others, filepath.Clean(e.file.Path))
			}
		}
	}
	return func(p string) bool {
		for _, o := range others {
			if library.PathWithin(p, o) {
				return false
			}
		}
		return true
	}, nil
}

func keepFiles(files []library.FoundFile, keep func(string) bool) []library.FoundFile {
	out := files[:0:0]
	for _, f := range files {
		if keep(f.Path) {
			out = append(out, f)
		}
	}
	return out
}

// RecoverBookRenames rolls back the renames a crash (or a power cut) stopped part way, at
// start-up, before anything else touches book files. The record of each still names the
// old place — it changes in the same transaction that ends the journal entry — so every
// file that already moved is moved back. A file that's at neither place, or whose old
// place is taken again, is left where it is and logged. Returns how many editions it
// rolled back.
func (c *Coordinator) RecoverBookRenames(ctx context.Context) int {
	if c.books == nil || c.imp == nil {
		return 0
	}
	pending, err := c.books.UnfinishedRenames(ctx)
	if err != nil {
		c.log.Warn("book rename: couldn't read the rename journal", "err", err)
		return 0
	}
	n := 0
	for _, r := range pending {
		back, stuck := 0, 0
		for i := len(r.Moves) - 1; i >= 0; i-- {
			m := r.Moves[i]
			if _, err := os.Lstat(m.To); err != nil {
				continue // never moved (or already back)
			}
			if _, err := os.Lstat(m.From); err == nil {
				stuck++
				c.log.Warn("book rename: a file from an interrupted rename is at both places; left as it is",
					"book_id", r.BookID, "old", m.From, "new", m.To)
				continue
			}
			if err := c.imp.Move(m.To, m.From); err != nil {
				stuck++
				c.log.Warn("book rename: couldn't move a file back from an interrupted rename", "book_id", r.BookID, "file", m.To, "err", err)
				continue
			}
			back++
		}
		if stuck > 0 {
			c.books.AddEvent(ctx, r.BookID, "rename-interrupted",
				fmt.Sprintf("A rename was interrupted: %d file(s) were moved back, %d couldn't be — see the log", back, stuck))
		} else if back > 0 {
			c.books.AddEvent(ctx, r.BookID, "rename-interrupted",
				fmt.Sprintf("A rename was interrupted; its %d moved file(s) were put back", back))
		}
		if err := c.books.ClearRenameJournal(ctx, r.BookID, r.VersionID, r.Kind); err != nil {
			c.log.Warn("book rename: couldn't clear the rename journal", "book_id", r.BookID, "err", err)
			continue
		}
		c.bookFilesChanged(ctx, r.BookID)
		c.log.Info("book rename: rolled back an interrupted rename", "book_id", r.BookID, "edition", r.Kind, "moved_back", back, "stuck", stuck)
		n++
	}
	return n
}
