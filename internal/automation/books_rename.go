package automation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/library"
)

// Book renames follow the movie organize: every edition is planned first and shown to the
// person, and only then does anything move. The plan is worked out again as each edition's
// turn comes, and one whose files or proposed names changed in between is left alone.
// Nothing is ever moved onto an existing file (library.Importer.Move), nothing leaves its
// library folder, and nothing is deleted: folders a rename empties are removed only once
// they're empty.
//
// A multi-file audiobook moves file by file — never as a folder, which could carry another
// edition's files with it — and its files keep their play order: a listening place is a
// time into the whole book, so a rename that reordered the files would move everyone's
// place. Places themselves are keyed by the book's id, not a path, so they stay put.

// What a planned book rename will do (BookRenameItem.Status) — the movie rename's words.
const (
	BookRenameOK       = "ok"
	BookRenameSame     = "same"
	BookRenameConflict = "conflict"
	BookRenameMissing  = "missing"
)

// Why an edition was left alone.
const (
	bookReasonTaken     = "a different file already exists there"
	bookReasonClaimed   = "another book in this rename would get the same name"
	bookReasonOwned     = "another book's file already has that name"
	bookReasonDupName   = "two of its files would get the same name"
	bookReasonOrder     = "its files would play in a different order under the new names, which would move everyone's listening place"
	bookReasonNotOnDisk = "the file isn't on disk"
	bookReasonNoFiles   = "its folder has no book files in it"
	bookReasonChanged   = "changed since the preview"
	bookReasonAlready   = "already named correctly"
	bookReasonGone      = "the book is no longer in the library"
)

// BookRenameFile is one file a planned rename moves.
type BookRenameFile struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// BookRenameItem is one edition's planned rename: a book's ebook, its standard audiobook,
// or one of its audiobook versions.
type BookRenameItem struct {
	Media     string `json:"media"` // "book"
	BookID    int64  `json:"book_id"`
	VersionID int64  `json:"version_id"` // 0 = the book's own ebook or audiobook
	Kind      string `json:"kind"`       // ebook | audiobook
	Title     string `json:"title"`
	Author    string `json:"author,omitempty"`
	Label     string `json:"label,omitempty"` // the version's label
	// Current is the recorded path: the file, or the folder of a multi-file edition.
	// Proposed is where the naming scheme puts it.
	Current   string `json:"current"`
	Proposed  string `json:"proposed"`
	FileCount int    `json:"file_count"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
	// Files are the moves, one per file. Not sent to the browser (a long audiobook has
	// hundreds); the plan token covers them.
	Files []BookRenameFile `json:"-"`
	// folder: the edition is a folder of files rather than one file.
	folder bool
}

// BookRenameResult is what applying one planned edition did.
type BookRenameResult struct {
	BookID    int64  `json:"book_id"`
	VersionID int64  `json:"version_id"`
	Kind      string `json:"kind"`
	From      string `json:"from"`
	To        string `json:"to"`
	Files     int    `json:"files"`
	Renamed   bool   `json:"renamed"`
	Reason    string `json:"reason,omitempty"`
}

// bookName is what a book edition is named from: the book's details, and for an
// audiobook its narrator (typed by hand) and, for a version, the version's label.
func bookName(b books.Book, v *books.AudioVersion, audio bool) library.BookName {
	n := library.BookName{Author: b.Author, Title: b.Title, Series: b.SeriesName, SeriesPosition: b.SeriesPosition, Year: b.Year}
	if audio {
		n.Narrator = b.AudiobookNarrator
		if v != nil {
			n.Edition, n.Narrator, n.VersionID = v.Label, v.Narrator, v.ID
		}
	}
	return n
}

// bookEdition is one edition of a book with files recorded.
type bookEdition struct {
	book    books.Book
	version *books.AudioVersion
	kind    string
	file    *books.BookFile
}

func (e bookEdition) audio() bool { return e.kind == books.KindAudiobook }

func (e bookEdition) versionID() int64 {
	if e.version != nil {
		return e.version.ID
	}
	return 0
}

func (e bookEdition) key() string { return editionKey(e.book.ID, e.versionID(), e.kind) }

func editionKey(bookID, versionID int64, kind string) string {
	return strconv.FormatInt(bookID, 10) + ":" + strconv.FormatInt(versionID, 10) + ":" + kind
}

// editionsOf lists a book's editions that have files recorded: the ebook, the standard
// audiobook, then each audiobook version.
func editionsOf(b books.Book) []bookEdition {
	var out []bookEdition
	if b.Ebook != nil && b.Ebook.Path != "" {
		out = append(out, bookEdition{book: b, kind: books.KindEbook, file: b.Ebook})
	}
	if b.Audiobook != nil && b.Audiobook.Path != "" {
		out = append(out, bookEdition{book: b, kind: books.KindAudiobook, file: b.Audiobook})
	}
	for i := range b.AudioVersions {
		v := &b.AudioVersions[i]
		if v.File != nil && v.File.Path != "" {
			out = append(out, bookEdition{book: b, version: v, kind: books.KindAudiobook, file: v.File})
		}
	}
	return out
}

// editionFiles are an edition's own files in its folder — its kind only, since an ebook
// and an audiobook can share a folder — sorted by path.
func editionFiles(dir string, audio bool) []string {
	var out []string
	for _, f := range library.FindBookFiles(dir) {
		if library.IsAudiobookFile(f.Path) == audio {
			out = append(out, f.Path)
		}
	}
	sort.Strings(out)
	return out
}

// bookPlaces knows where every book's editions are (and are about to be), to keep a new
// name from nesting one book inside another: a multi-file edition is everything under its
// folder, so a book whose folder held another book's would list, play and delete that
// book's files as its own. The default scheme puts every book exactly two folders down and
// can't nest; a scheme whose folders come and go ("{Author}/{Series}/{Title}") can, when
// one book's title is another's series.
type bookPlaces struct {
	folders map[string]int64 // a multi-file edition's folder → its book
	paths   []bookPlace      // every edition's path, sorted
	sorted  bool
}

type bookPlace struct {
	path string
	book int64
}

func newBookPlaces(all []books.Book) *bookPlaces {
	p := &bookPlaces{folders: map[string]int64{}}
	for _, b := range all {
		for _, e := range editionsOf(b) {
			p.add(e.file.Path, b.ID, e.file.FileCount > 1)
		}
	}
	return p
}

func (p *bookPlaces) add(path string, book int64, folder bool) {
	path = filepath.Clean(path)
	if folder {
		p.folders[path] = book
	}
	p.paths = append(p.paths, bookPlace{path: path, book: book})
	p.sorted = false
}

// nests says why a book's edition can't live in dir, or "" when it can: dir is inside
// another book's multi-file folder, or another book's files are already in or under dir.
// The second holds for a single-file edition too — its folder is the one a rescan reads,
// and a later second file would make it a folder edition holding the other book's files.
func (p *bookPlaces) nests(dir string, book int64) string {
	dir = filepath.Clean(dir)
	for d := dir; ; {
		if other, ok := p.folders[d]; ok && other != book {
			return "inside another book's folder (" + filepath.Base(d) + ")"
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	if !p.sorted {
		sort.Slice(p.paths, func(i, j int) bool { return p.paths[i].path < p.paths[j].path })
		p.sorted = true
	}
	prefix := dir + string(filepath.Separator)
	for i := sort.Search(len(p.paths), func(i int) bool { return p.paths[i].path >= prefix }); i < len(p.paths) && strings.HasPrefix(p.paths[i].path, prefix); i++ {
		if p.paths[i].book != book {
			return "around another book's files (" + filepath.Base(p.paths[i].path) + ")"
		}
	}
	return ""
}

// bookPlanner plans renames across the library, remembering what earlier editions in the
// same plan claimed.
type bookPlanner struct {
	c       *Coordinator
	places  *bookPlaces
	owned   map[string]string // a single-file edition's recorded file → its edition key
	claimed map[string]bool
}

func (c *Coordinator) newBookPlanner(all []books.Book) *bookPlanner {
	p := &bookPlanner{c: c, places: newBookPlaces(all), owned: map[string]string{}, claimed: map[string]bool{}}
	for _, b := range all {
		for _, e := range editionsOf(b) {
			p.owned[filepath.Clean(e.file.Path)] = e.key()
		}
	}
	return p
}

// plan works out one edition's rename and judges it against the disk and the rest of the
// library. It changes nothing.
func (p *bookPlanner) plan(e bookEdition) BookRenameItem {
	b := e.book
	it := BookRenameItem{Media: "book", BookID: b.ID, VersionID: e.versionID(), Kind: e.kind, Title: b.Title, Author: b.Author,
		Current: e.file.Path, FileCount: max(1, e.file.FileCount)}
	if e.version != nil {
		it.Label = e.version.Label
	}
	audio := e.audio()
	what := "Ebooks"
	if audio {
		what = "Audiobooks"
	}
	root := p.c.imp.BookRoot(audio)
	if root == "" {
		it.Status, it.Reason = BookRenameConflict, "no "+what+" folder is set"
		return it
	}
	st, err := os.Stat(it.Current)
	if err != nil {
		it.Status, it.Reason = BookRenameMissing, bookReasonNotOnDisk
		if !errors.Is(err, fs.ErrNotExist) {
			it.Reason = "couldn't read it: " + err.Error()
		}
		return it
	}
	if filepath.Clean(it.Current) == filepath.Clean(root) || !library.PathWithin(it.Current, root) {
		it.Status, it.Reason = BookRenameConflict, "it isn't inside the "+what+" folder, so it stays where it is"
		return it
	}
	name := bookName(b, e.version, audio)
	nm := p.c.imp.BookNamingFor(audio)
	if st.IsDir() {
		it.folder = true
		files := editionFiles(it.Current, audio)
		if len(files) == 0 {
			it.Status, it.Reason = BookRenameMissing, bookReasonNoFiles
			return it
		}
		it.FileCount = len(files)
		format := e.file.Format
		if format == "" {
			format = library.BookFileFormat(files[0])
		}
		newDir := library.BookFolderPath(root, nm, audio, name, format)
		rels := make([]string, len(files))
		for i, f := range files {
			rel, err := filepath.Rel(it.Current, f)
			if err != nil {
				rel = filepath.Base(f)
			}
			rels[i] = filepath.ToSlash(rel)
		}
		// A multi-file audiobook's files are named by the track template, in play order,
		// and keep any sub-folders (CD1/, CD2/) they're in. A multi-file ebook — the same
		// book in several formats — keeps its names.
		names := make([]string, len(files))
		if audio {
			names = library.BookTrackNames(nm, name, rels)
		} else {
			for i, r := range rels {
				names[i] = path.Base(r)
			}
		}
		for i, f := range files {
			to := filepath.Join(newDir, filepath.FromSlash(path.Dir(rels[i])), names[i])
			it.Files = append(it.Files, BookRenameFile{From: f, To: to})
		}
		it.Proposed = newDir
		it.Status, it.Reason = p.judge(&it, root, rels)
		if it.Status == BookRenameOK {
			if why := occupiedBy(newDir, audio, files); why != "" {
				it.Status, it.Reason = BookRenameConflict, why
			}
		}
	} else {
		to := filepath.Join(library.BookFolderPath(root, nm, audio, name, library.BookFileFormat(it.Current)), library.BookFileName(nm, audio, name, it.Current))
		it.Files = []BookRenameFile{{From: it.Current, To: to}}
		it.Proposed = to
		it.Status, it.Reason = p.judge(&it, root, nil)
	}
	if it.Status == BookRenameOK {
		for _, f := range it.Files {
			p.claimed[f.To] = true
		}
		p.places.add(it.Proposed, it.BookID, it.folder)
	}
	return it
}

// judge decides an edition's status. rels are a folder edition's files relative to its
// folder (nil for one file).
func (p *bookPlanner) judge(it *BookRenameItem, root string, rels []string) (string, string) {
	same := true
	seen := map[string]bool{}
	for _, f := range it.Files {
		if f.From != f.To {
			same = false
		}
		// Case-blind: on a case-insensitive disk "Part 1.mp3" and "part 1.mp3" are one name.
		k := strings.ToLower(f.To)
		if seen[k] {
			return BookRenameConflict, bookReasonDupName
		}
		seen[k] = true
	}
	if same {
		return BookRenameSame, ""
	}
	dir := it.Proposed
	if !it.folder {
		dir = filepath.Dir(it.Proposed)
	}
	if filepath.Clean(dir) == filepath.Clean(root) || !library.PathWithin(dir, root) {
		return BookRenameConflict, "the new name would be outside the library folder"
	}
	if rels != nil && it.Kind == books.KindAudiobook && !samePlayOrder(rels, it) {
		return BookRenameConflict, bookReasonOrder
	}
	self := editionKey(it.BookID, it.VersionID, it.Kind)
	for _, f := range it.Files {
		if f.From == f.To {
			continue
		}
		if owner, ok := p.owned[filepath.Clean(f.To)]; ok && owner != self {
			return BookRenameConflict, bookReasonOwned
		}
		if p.claimed[f.To] {
			return BookRenameConflict, bookReasonClaimed
		}
		if fi, err := os.Lstat(f.To); err == nil {
			// The same file under another name (a hardlink, or a case-only change on a
			// case-insensitive disk) isn't in the way: Move just drops the old name.
			cur, cerr := os.Lstat(f.From)
			if cerr != nil || !os.SameFile(cur, fi) {
				return BookRenameConflict, bookReasonTaken
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return BookRenameConflict, "couldn't check the new name: " + err.Error()
		}
	}
	if why := p.places.nests(dir, it.BookID); why != "" {
		return BookRenameConflict, "the new name would put it " + why
	}
	return BookRenameOK, ""
}

// samePlayOrder reports whether a folder edition's files, sorted the way the audiobook
// server plays them, come in the same order under their new names.
func samePlayOrder(rels []string, it *BookRenameItem) bool {
	newRels := make([]string, len(it.Files))
	for i, f := range it.Files {
		r, err := filepath.Rel(it.Proposed, f.To)
		if err != nil {
			return false
		}
		newRels[i] = filepath.ToSlash(r)
	}
	order := func(names []string) []int {
		idx := make([]int, len(names))
		for i := range idx {
			idx[i] = i
		}
		sort.SliceStable(idx, func(a, b int) bool { return library.NaturalLess(names[idx[a]], names[idx[b]]) })
		return idx
	}
	before, after := order(rels), order(newRels)
	for i := range before {
		if before[i] != after[i] {
			return false
		}
	}
	return true
}

// BookRenamePlan lists every edition of the given books (all of them when ids is empty)
// with where its files are and where the naming scheme puts them. It changes nothing.
func (c *Coordinator) BookRenamePlan(ctx context.Context, ids []int64) ([]BookRenameItem, error) {
	if c.books == nil || c.imp == nil {
		return nil, errBooksNotReady
	}
	all, err := c.books.List(ctx)
	if err != nil {
		return nil, err
	}
	want := map[int64]bool{}
	for _, id := range ids {
		want[id] = true
	}
	p := c.newBookPlanner(all)
	var out []BookRenameItem
	for _, b := range all {
		if len(ids) > 0 && !want[b.ID] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, e := range editionsOf(b) {
			out = append(out, p.plan(e))
		}
	}
	return out, nil
}

// BookPlanToken fingerprints the renames a plan would make, so applying it can tell
// whether the library or the scheme changed since the preview it was asked from.
func BookPlanToken(items []BookRenameItem) string {
	var lines []string
	for _, it := range items {
		if it.Status != BookRenameOK {
			continue
		}
		parts := []string{strconv.FormatInt(it.BookID, 10), strconv.FormatInt(it.VersionID, 10), it.Kind, it.Current, it.Proposed}
		for _, f := range it.Files {
			parts = append(parts, f.From, f.To)
		}
		lines = append(lines, strings.Join(parts, "\x00"))
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:16])
}

// ApplyBookRename carries out planned renames, an edition at a time. Each edition is
// planned again first, from the book as it is now: one whose files or proposed names
// changed, or that's no longer a clean move, is left alone and reported. progress, when
// set, hears after each edition.
func (c *Coordinator) ApplyBookRename(ctx context.Context, items []BookRenameItem, progress func(done int, res BookRenameResult)) ([]BookRenameResult, error) {
	if c.books == nil || c.imp == nil {
		return nil, errBooksNotReady
	}
	all, err := c.books.List(ctx)
	if err != nil {
		return nil, err
	}
	p := c.newBookPlanner(all)
	var out []BookRenameResult
	for i, it := range items {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		res := c.applyBookEdition(ctx, p, it)
		out = append(out, res)
		if progress != nil {
			progress(i+1, res)
		}
	}
	return out, nil
}

func (c *Coordinator) applyBookEdition(ctx context.Context, p *bookPlanner, asked BookRenameItem) BookRenameResult {
	res := BookRenameResult{BookID: asked.BookID, VersionID: asked.VersionID, Kind: asked.Kind, From: asked.Current, To: asked.Proposed, Files: len(asked.Files)}
	unlock, ok := c.tryLockBook(asked.BookID)
	if !ok {
		res.Reason = ErrBookBusy.Error()
		return res
	}
	defer unlock()
	b, err := c.books.Get(ctx, asked.BookID)
	if err != nil {
		res.Reason = bookReasonGone
		return res
	}
	var e *bookEdition
	for _, ed := range editionsOf(b) {
		if ed.key() == editionKey(asked.BookID, asked.VersionID, asked.Kind) {
			e = &ed
			break
		}
	}
	if e == nil {
		res.Reason = bookReasonChanged
		return res
	}
	now := p.plan(*e)
	switch {
	case now.Current != asked.Current || now.Proposed != asked.Proposed || !sameMoves(now.Files, asked.Files):
		res.Reason = bookReasonChanged
		return res
	case now.Status == BookRenameSame:
		res.Reason = bookReasonAlready
		return res
	case now.Status != BookRenameOK:
		res.Reason = now.Reason
		return res
	}
	return c.moveBookEdition(ctx, *e, now, res)
}

func sameMoves(a, b []BookRenameFile) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// moveBookEdition moves one planned edition's files and records the new place. If a file
// can't be moved, or the record can't be written, the files already moved are put back,
// so the record and the disk never disagree.
func (c *Coordinator) moveBookEdition(ctx context.Context, e bookEdition, it BookRenameItem, res BookRenameResult) BookRenameResult {
	var planned []books.PathMove
	for _, f := range it.Files {
		if f.From != f.To {
			planned = append(planned, books.PathMove{From: f.From, To: f.To})
		}
	}
	// Each file keeps the id listening apps know it by. One with no id stored yet (probed
	// before ids were kept, or never listed) gets the one its old name has always given it
	// now, before the move, so the move carries it.
	if err := c.books.PinFileIDs(ctx, fileIDsBefore(it, e.audio())); err != nil {
		res.Reason = "couldn't keep the files' ids before the rename: " + err.Error()
		return res
	}
	// The moves are written down before the first one, so a crash part way through is
	// undone at the next start (RecoverBookRenames) instead of leaving a book split
	// between two folders with its record pointing at one.
	if err := c.books.JournalRename(ctx, it.BookID, it.VersionID, it.Kind, it.Current, it.Proposed, planned); err != nil {
		res.Reason = "couldn't note the rename before starting it: " + err.Error()
		return res
	}
	// From the first move on, nothing waits on the request: the record, the clean-up and
	// the catalogue refresh must happen even if the caller has gone.
	wctx := context.WithoutCancel(ctx)
	var done []books.PathMove
	for _, m := range planned {
		if err := c.imp.Move(m.From, m.To); err != nil {
			if errors.Is(err, library.ErrTargetExists) {
				res.Reason = bookReasonTaken
			} else {
				res.Reason = "couldn't move " + filepath.Base(m.From) + ": " + err.Error()
			}
			if berr := c.putBookFilesBack(done); berr != nil {
				res.Reason += "; " + splitMessage(it, done, berr)
				c.bookFilesChanged(wctx, it.BookID)
				return res
			}
			c.endJournal(wctx, it)
			return res
		}
		done = append(done, m)
	}
	what := "ebook"
	if e.audio() {
		what = "audiobook"
		if e.version != nil {
			what = fmt.Sprintf("%q audiobook", e.version.Label)
		}
	}
	detail := fmt.Sprintf("Renamed the %s to the naming scheme: %s → %s", what, filepath.Base(it.Current), filepath.Base(it.Proposed))
	if err := c.books.RepointEdition(wctx, it.BookID, it.VersionID, it.Kind, it.Proposed, done, detail); err != nil {
		if berr := c.putBookFilesBack(done); berr == nil {
			c.endJournal(wctx, it)
			res.Reason = "couldn't record the new name, so the files were put back: " + err.Error()
		} else {
			c.log.Error("book rename: the record couldn't be written and the files couldn't all be put back",
				"book_id", it.BookID, "old", it.Current, "new", it.Proposed, "err", err, "move_back_err", berr)
			res.Reason = fmt.Sprintf("couldn't record the new name (%v); %s", err, splitMessage(it, done, berr))
		}
		c.bookFilesChanged(wctx, it.BookID)
		return res
	}
	oldDir, newDir := it.Current, it.Proposed
	if !it.folder {
		oldDir, newDir = filepath.Dir(it.Current), filepath.Dir(it.Proposed)
	}
	c.leaveOldBookFolder(oldDir, newDir, c.imp.BookRoot(e.audio()), it.folder)
	c.log.Info("book renamed", "book_id", it.BookID, "edition", it.Kind, "files", len(done))
	c.bookFilesChanged(wctx, it.BookID)
	res.Renamed = true
	return res
}

// endJournal ends a rename's journal entry once its moves are undone. If that fails the
// entry stays, and the next start finds the record at the old place with the files there
// too: it drops the entry without moving anything.
func (c *Coordinator) endJournal(ctx context.Context, it BookRenameItem) {
	if err := c.books.ClearRenameJournal(ctx, it.BookID, it.VersionID, it.Kind); err != nil {
		c.log.Warn("book rename: couldn't clear the rename journal after undoing a rename; the next start settles it",
			"book_id", it.BookID, "err", err)
	}
}

// fileIDsBefore is each file's id from its current name, the one it has had all along:
// relative to the audiobook item's folder for an audiobook, the ebook's own name for an
// ebook (as the audiobook server gives them).
func fileIDsBefore(it BookRenameItem, audio bool) []books.FileID {
	base := library.ItemBase(it.Current, it.folder)
	var out []books.FileID
	for _, f := range it.Files {
		if f.From == f.To {
			continue
		}
		id := library.AudioFileID(library.RelPath(base, f.From))
		if !audio {
			id = library.EbookFileID(filepath.Base(f.From))
		}
		out = append(out, books.FileID{Path: f.From, ID: id})
	}
	return out
}

// splitMessage says where a book's files are after a rename that could neither finish nor
// be undone: some in the new folder, the rest in the old, with the record on the old. A
// rescan alone can't fix that — it would record whichever half is in the scheme's folder.
func splitMessage(it BookRenameItem, done []books.PathMove, berr error) string {
	oldDir, newDir := it.Current, it.Proposed
	if !it.folder {
		oldDir, newDir = filepath.Dir(it.Current), filepath.Dir(it.Proposed)
	}
	return fmt.Sprintf("moving the files back failed too (%v), so up to %d of them are in %s and the rest in %s, and the book still points at %s — move them back into %s by hand",
		berr, len(done), newDir, oldDir, it.Current, oldDir)
}

// occupiedBy says why a multi-file edition can't move into dir, or "": dir already holds
// book files of its kind that aren't the edition's own. They'd become part of the book —
// listed, played and deleted with it. Own files are recognised with os.SameFile, so a
// case-only change on a case-insensitive disk (the same files, another spelling) is fine.
func occupiedBy(dir string, audio bool, own []string) string {
	if _, err := os.Stat(dir); err != nil {
		return ""
	}
	var mine []os.FileInfo
	for _, p := range own {
		if fi, err := os.Stat(p); err == nil {
			mine = append(mine, fi)
		}
	}
	for _, p := range editionFiles(dir, audio) {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		ours := false
		for _, m := range mine {
			if os.SameFile(m, fi) {
				ours = true
				break
			}
		}
		if !ours {
			return "its new folder already has other files in it (" + filepath.Base(p) + "), which would become part of this book"
		}
	}
	return ""
}

// putBookFilesBack undoes a part-done rename, newest move first.
func (c *Coordinator) putBookFilesBack(done []books.PathMove) error {
	var first error
	for i := len(done) - 1; i >= 0; i-- {
		if err := c.imp.Move(done[i].To, done[i].From); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// bookFolderSidecars are the files a book folder holds for the book as a whole — what
// Audiobookshelf, Calibre and taggers write — which follow the book to its new folder once
// none of its book files are left in the old one. Anything else stays where it is.
var bookFolderSidecars = []string{
	"cover.jpg", "cover.jpeg", "cover.png", "folder.jpg", "folder.png",
	"metadata.opf", "metadata.json", "metadata.abs", "desc.txt", "reader.txt",
}

// leaveOldBookFolder finishes a rename that moved a book out of oldDir: once no book file
// is left there (an ebook beside the audiobook stays, and so does its folder), the book's
// cover and metadata files follow it (never replacing what newDir already has), and the
// folders the move emptied are removed — sub-folders, the book folder, then empty series
// and author folders above it, never the library folder itself. Only empty folders are
// ever removed.
func (c *Coordinator) leaveOldBookFolder(oldDir, newDir, root string, folderEdition bool) {
	oldDir, newDir = filepath.Clean(oldDir), filepath.Clean(newDir)
	if oldDir == newDir || root == "" || oldDir == filepath.Clean(root) || !library.PathWithin(oldDir, root) {
		return
	}
	if len(library.FindBookFiles(oldDir)) > 0 {
		return
	}
	// Sub-folders the move emptied (CD1/, CD2/), deepest first.
	var dirs []string
	_ = filepath.WalkDir(oldDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() && p != oldDir {
			dirs = append(dirs, p)
		}
		return nil
	})
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, d := range dirs {
		c.imp.RemoveDirIfEmpty(d)
	}
	// The cover and metadata follow only from a folder that was the book's own. One that
	// still has folders in it is something else's — an author folder a flat layout kept
	// its files in — and its cover.jpg is the author's.
	if !hasSubdir(oldDir) && (folderEdition || depthUnder(oldDir, root) >= 2) {
		for _, name := range bookFolderSidecars {
			from := filepath.Join(oldDir, name)
			if _, err := os.Lstat(from); err != nil {
				continue
			}
			if err := c.imp.Move(from, filepath.Join(newDir, name)); err != nil {
				c.log.Info("book rename: left a folder file behind", "file", from, "err", err)
			}
		}
	}
	// Then the folder itself and the series and author folders above it, while empty.
	for d := oldDir; d != filepath.Clean(root) && library.PathWithin(d, root); d = filepath.Dir(d) {
		if entries, err := os.ReadDir(d); err != nil || len(entries) > 0 {
			return
		}
		c.imp.RemoveDirIfEmpty(d)
	}
}

// depthUnder is how many folders down from root dir is ("Author" 1, "Author/Title" 2).
func depthUnder(dir, root string) int {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." {
		return 0
	}
	return len(strings.Split(filepath.ToSlash(rel), "/"))
}

// hasSubdir reports whether dir has a folder in it (or can't be read: then assume so).
func hasSubdir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	for _, e := range entries {
		if e.IsDir() {
			return true
		}
	}
	return false
}

// RenameBookFiles applies every clean rename for one book (its plan's "ok" editions),
// returning how many editions moved. Renames that couldn't be done are reported in the
// book's timeline and the log, never forced.
func (c *Coordinator) RenameBookFiles(ctx context.Context, bookID int64) (int, []BookRenameResult, error) {
	if c.books == nil || c.imp == nil {
		return 0, nil, errBooksNotReady
	}
	if _, err := c.books.Get(ctx, bookID); err != nil {
		return 0, nil, err
	}
	plan, err := c.BookRenamePlan(ctx, []int64{bookID})
	if err != nil {
		return 0, nil, err
	}
	var todo []BookRenameItem
	var skipped []BookRenameResult
	for _, it := range plan {
		switch it.Status {
		case BookRenameOK:
			todo = append(todo, it)
		case BookRenameConflict:
			skipped = append(skipped, BookRenameResult{BookID: it.BookID, VersionID: it.VersionID, Kind: it.Kind, From: it.Current, To: it.Proposed, Files: len(it.Files), Reason: it.Reason})
		}
	}
	res, err := c.ApplyBookRename(ctx, todo, nil)
	moved := 0
	for _, r := range res {
		if r.Renamed {
			moved++
		} else {
			skipped = append(skipped, r)
		}
	}
	return moved, skipped, err
}

// editionFormat is an edition's recorded format, "" when it has none.
func editionFormat(f *books.BookFile) string {
	if f == nil {
		return ""
	}
	return f.Format
}

// bookPathIndex is every recorded book file and multi-file folder, for a library scan to
// recognise folders it has nothing to add from. A folder is known per kind: an ebook
// dropped into a recorded audiobook's folder (they share one under the default scheme) is
// still news to the scan.
type bookPathIndex struct {
	files   map[string]bool
	folders map[folderKind]bool
}

type folderKind struct {
	path  string
	audio bool
}

func knownBookPaths(all []books.Book) bookPathIndex {
	k := bookPathIndex{files: map[string]bool{}, folders: map[folderKind]bool{}}
	for _, b := range all {
		for _, e := range editionsOf(b) {
			if e.file.FileCount > 1 {
				k.folders[folderKind{filepath.Clean(e.file.Path), e.audio()}] = true
			} else {
				k.files[filepath.Clean(e.file.Path)] = true
			}
		}
	}
	return k
}

// covers reports whether every file in a scanned folder already belongs to a book.
func (k bookPathIndex) covers(bf library.BookFolder) bool {
	if len(k.files) == 0 && len(k.folders) == 0 {
		return false
	}
	known := func(p string) bool {
		p = filepath.Clean(p)
		if k.files[p] {
			return true
		}
		audio := library.IsAudiobookFile(p)
		for d := filepath.Dir(p); ; d = filepath.Dir(d) {
			if k.folders[folderKind{d, audio}] {
				return true
			}
			if filepath.Dir(d) == d {
				return false
			}
		}
	}
	for _, f := range bf.Ebooks {
		if !known(f.Path) {
			return false
		}
	}
	for _, f := range bf.Audiobooks {
		if !known(f.Path) {
			return false
		}
	}
	return len(bf.Ebooks)+len(bf.Audiobooks) > 0
}

// bookImportRefusal says why a book's files can't be imported where the naming scheme
// puts them, or nil. Only a scheme whose folders come and go can nest one book inside
// another (see bookPlaces); the default never does and is never checked. The library read
// failing refuses the import: better a retry than a book filed inside another.
func (c *Coordinator) bookImportRefusal(ctx context.Context, b books.Book, n library.BookName, files []library.FoundFile) error {
	if len(files) == 0 || c.imp == nil {
		return nil
	}
	audio := library.IsAudiobookFile(files[0].Path)
	if c.imp.BookNamingFor(audio).Folder == library.DefaultAudiobookFolder {
		return nil
	}
	dir := c.imp.BookDir(audio, n, library.BookFileFormat(files[0].Path))
	if dir == "" {
		return nil // no folder for this kind: the import itself says so
	}
	all, err := c.books.List(ctx)
	if err != nil {
		return fmt.Errorf("couldn't check where the book's folder would go: %w", err)
	}
	if why := newBookPlaces(all).nests(dir, b.ID); why != "" {
		return fmt.Errorf("the book naming scheme would put %q %s — change the book folder template in Settings → Naming & metadata", b.Title, why)
	}
	return nil
}
