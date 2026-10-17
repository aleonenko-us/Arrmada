package automation

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/library"
)

// One book's files are changed by several things — an import, a rename (one book or the
// whole library), a rescan, a library scan recording what it found, a delete, a merge —
// and two at once could each act on a folder the other is half way through moving. Each
// takes the book's lock first. None waits for it: an import that finds the book busy is
// retried by the next import sweep, a rename or a scan skips the book and says why, and a
// person is told to try again.

// ErrBookBusy is a change to a book's files refused because another one is running.
var ErrBookBusy = errors.New("another change to this book's files is running (an import, a rename, a rescan, a delete or a merge) — try again in a moment")

// bookLock is one book's lock. held counts how many times it has been taken and noted
// the taking last reported busy, so a sweep that keeps finding the book busy (a merge can
// hold it for an hour) says so once per holder, not every 30 seconds.
type bookLock struct {
	mu    sync.Mutex
	held  atomic.Uint64
	noted atomic.Uint64
}

func (c *Coordinator) bookLock(id int64) *bookLock {
	v, _ := c.bookLocks.LoadOrStore(id, &bookLock{})
	return v.(*bookLock)
}

// tryLockBook takes book id's lock, or reports that something else holds it.
func (c *Coordinator) tryLockBook(id int64) (unlock func(), ok bool) {
	l := c.bookLock(id)
	if !l.mu.TryLock() {
		return nil, false
	}
	l.held.Add(1)
	return l.mu.Unlock, true
}

// firstBusy reports whether this is the first time book id was found busy while its lock
// is held by its current holder.
func (c *Coordinator) firstBusy(id int64) bool {
	l := c.bookLock(id)
	g := l.held.Load()
	return l.noted.Swap(g) != g
}

// underBookLock runs fn holding book id's lock, or reports false when another change to
// the book's files holds it (fn didn't run).
func (c *Coordinator) underBookLock(id int64, fn func()) bool {
	unlock, ok := c.tryLockBook(id)
	if !ok {
		if c.firstBusy(id) {
			c.log.Info("book: left for later — the book's files are being changed", "book_id", id)
		}
		return false
	}
	defer unlock()
	fn()
	return true
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

// editionRecord is the path a book's edition is recorded at now ("" for none).
func editionRecord(b books.Book, versionID int64, kind string) string {
	for _, e := range editionsOf(b) {
		if e.versionID() == versionID && e.kind == kind {
			return e.file.Path
		}
	}
	return ""
}

// rootReady reports whether a library folder is there to work in: it exists and isn't
// empty. An unmounted share leaves an empty mount point, and every file would read as gone.
func rootReady(root string) bool {
	if root == "" {
		return false
	}
	entries, err := os.ReadDir(root)
	return err == nil && len(entries) > 0
}

// How a journaled move is undone.
const (
	stepNone       = iota // never moved: the file is at its old place only
	stepMoveBack          // moved: at the new place only
	stepDropSecond        // both names of one file (a crash between link and unlink)
)

// RecoverBookRenames settles the renames a crash (or a power cut) stopped part way, at
// start-up, before anything else touches book files. Each journal entry is settled by the
// edition's record:
//
//   - at the new place: the rename was recorded after all (the entry just outlived it) —
//     the entry is dropped;
//   - at the old place: the rename is rolled back, every file that moved goes back;
//   - anywhere else: something has recorded the edition since — nothing is moved, the
//     entry is dropped, and the book's history says so.
//
// A rollback only acts when it can tell exactly where every file is: at its old place
// only, at its new place only, or (a crash between hardlink and unlink) at both as one
// file. Anything else — a file at neither place, two different files, a library folder
// missing, empty (an unmounted share) or unreadable — keeps the entry for the next start
// and moves nothing. Returns how many entries it settled.
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
		if c.settleRename(ctx, r) {
			n++
		}
	}
	return n
}

// settleRename settles one entry, reporting whether it's done with (the entry dropped).
func (c *Coordinator) settleRename(ctx context.Context, r books.UnfinishedRename) bool {
	log := c.log.With("book_id", r.BookID, "edition", r.Kind, "version_id", r.VersionID)
	root := c.imp.BookRoot(r.Kind == books.KindAudiobook)
	if !rootReady(root) {
		log.Warn("book rename: an interrupted rename waits — its library folder is missing or empty (offline?)", "root", root)
		return false
	}
	b, err := c.books.Get(ctx, r.BookID)
	if errors.Is(err, books.ErrNotFound) {
		log.Info("book rename: an interrupted rename's book is gone; dropping the entry")
		return c.dropJournal(ctx, r, log)
	}
	if err != nil {
		log.Warn("book rename: couldn't read an interrupted rename's book; it waits", "err", err)
		return false
	}
	switch record := editionRecord(b, r.VersionID, r.Kind); {
	case record != "" && record == r.NewPath:
		log.Info("book rename: an interrupted rename had been recorded; dropping the entry")
		return c.dropJournal(ctx, r, log)
	case record == "" || record != r.OldPath:
		log.Warn("book rename: an interrupted rename's edition has been recorded elsewhere since; moving nothing",
			"record", record, "old", r.OldPath, "new", r.NewPath)
		c.books.AddEvent(ctx, r.BookID, "rename-interrupted", "A rename was interrupted, and the book has been recorded somewhere else since — nothing was moved back")
		return c.dropJournal(ctx, r, log)
	}

	steps := make([]int, len(r.Moves))
	for i, m := range r.Moves {
		to, toErr := os.Lstat(m.To)
		from, fromErr := os.Lstat(m.From)
		switch {
		case errors.Is(toErr, fs.ErrNotExist) && fromErr == nil:
			steps[i] = stepNone
		case toErr == nil && errors.Is(fromErr, fs.ErrNotExist):
			steps[i] = stepMoveBack
		case toErr == nil && fromErr == nil && os.SameFile(to, from):
			steps[i] = stepDropSecond
		default:
			log.Warn("book rename: can't tell where a file of an interrupted rename is; it waits for the next start",
				"old", m.From, "new", m.To, "old_err", fromErr, "new_err", toErr)
			return false
		}
	}
	back := 0
	for i := len(r.Moves) - 1; i >= 0; i-- {
		m := r.Moves[i]
		switch steps[i] {
		case stepMoveBack:
			if err := c.imp.Move(m.To, m.From); err != nil {
				log.Warn("book rename: couldn't move a file back from an interrupted rename; it waits", "file", m.To, "err", err)
				return false
			}
			back++
		case stepDropSecond:
			// The same file under both names: the new name is only a second link to it.
			if err := os.Remove(m.To); err != nil {
				log.Warn("book rename: couldn't drop the second name of a file from an interrupted rename; it waits", "file", m.To, "err", err)
				return false
			}
		}
	}
	// The folders the rename made and its rollback emptied go, up to (never) the root.
	seen := map[string]bool{}
	for _, m := range r.Moves {
		for d := filepath.Dir(m.To); !seen[d] && d != filepath.Clean(root) && library.PathWithin(d, root); d = filepath.Dir(d) {
			seen[d] = true
			if entries, err := os.ReadDir(d); err != nil || len(entries) > 0 {
				break
			}
			c.imp.RemoveDirIfEmpty(d)
		}
	}
	if !c.dropJournal(ctx, r, log) {
		return false
	}
	if back > 0 {
		c.books.AddEvent(ctx, r.BookID, "rename-interrupted", fmt.Sprintf("A rename was interrupted; its %d moved file(s) were put back", back))
	}
	c.bookFilesChanged(ctx, r.BookID)
	log.Info("book rename: rolled back an interrupted rename", "moved_back", back)
	return true
}

func (c *Coordinator) dropJournal(ctx context.Context, r books.UnfinishedRename, log interface {
	Warn(msg string, args ...any)
}) bool {
	if err := c.books.ClearRenameJournal(ctx, r.BookID, r.VersionID, r.Kind); err != nil {
		log.Warn("book rename: couldn't clear the rename journal; the next start settles it again", "err", err)
		return false
	}
	return true
}
