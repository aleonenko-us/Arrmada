package libfolders

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/tristenlammi/arrmada/internal/diskspace"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/store"
)

// stagingPrefix names a title folder still being copied onto another drive:
// "<library folder>/.arrmada-moving-<its path inside the library>". The dot keeps it out of
// Plex, Jellyfin and Arrmada's own scans (library.SkipScanDir), and it sits directly in the
// destination library folder so a leftover from a crash is found — and discarded — by the
// next move into that folder. It's Arrmada's own duplicate of files whose originals are
// untouched, so it is deleted outright rather than sent to the recycle bin: binning it
// would keep the space and add another copy on every retry. discardStaging only ever
// deletes a path shaped exactly like this.
const stagingPrefix = ".arrmada-moving-"

// stagingPath is where the copy of the folder at rel (inside the library) is staged.
func stagingPath(destRoot, rel string) string {
	return filepath.Join(destRoot, stagingPrefix+strings.ReplaceAll(filepath.ToSlash(rel), "/", "__"))
}

// isStaging reports whether path is a staging folder directly in destRoot.
func isStaging(destRoot, path string) bool {
	path = filepath.Clean(path)
	return strings.TrimSpace(destRoot) != "" &&
		filepath.Dir(path) == filepath.Clean(destRoot) &&
		strings.HasPrefix(filepath.Base(path), stagingPrefix) &&
		len(filepath.Base(path)) > len(stagingPrefix)
}

// ErrNotStaging is a delete aimed at something that isn't a move's staging copy.
var ErrNotStaging = errors.New("not a move's staging copy")

// discardStaging deletes a staging copy for good — the one exception to "deletes go
// through the recycle bin", allowed only for a path isStaging accepts.
func discardStaging(destRoot, path string) error {
	if !isStaging(destRoot, path) {
		return fmt.Errorf("%w: %s", ErrNotStaging, path)
	}
	return os.RemoveAll(path)
}

// discardLeftovers removes staging copies a crashed or killed move left in destRoot.
// Moves run one at a time (Mover.mu), so none of them belongs to a move in progress.
func (m *Mover) discardLeftovers(destRoot string) {
	entries, err := os.ReadDir(destRoot)
	if err != nil {
		return
	}
	for _, e := range entries {
		p := filepath.Join(destRoot, e.Name())
		if e.IsDir() && isStaging(destRoot, p) {
			if err := discardStaging(destRoot, p); err != nil && m.Log != nil {
				m.Log.Warn("library folders: couldn't remove a copy an earlier move left behind", "path", p, "err", err)
			} else if m.Log != nil {
				m.Log.Info("library folders: removed a copy an earlier move left behind", "path", p)
			}
		}
	}
}

// freeMargin is the room a copy must leave on the destination drive beyond its own size.
func freeMargin(size int64) int64 {
	m := size / 100 // 1%
	if m < 256<<20 {
		m = 256 << 20
	}
	return m
}

// Mover moves a title's files to another folder of its library, as a background job.
//
// On one filesystem a folder is renamed, which is instant. Across filesystems (two shares,
// two disks, two bind mounts) it's copied into a temp folder, every file checked against
// its original, the temp folder renamed into place, and only then are the originals
// retired through the recycle bin — never deleted outright. The database follows in one
// transaction per folder (RewritePrefix), the title is reassigned last, and Plex and
// Jellyfin are told about both the old and the new folder.
type Mover struct {
	DB      *sql.DB
	Folders *Service
	// Bin takes the originals after a verified cross-drive copy (library.RootBins).
	Bin library.Bin
	// Scan asks the media servers for a partial scan of a folder ("movie" / "show" kind).
	Scan func(kind, dir string)
	// Busy says why a title can't move right now ("" when it can): a download on its way
	// in, for instance, would land in the old folder half-way through.
	Busy func(ctx context.Context, media string, id int64) string
	// Hold keeps searches of the title from starting while it moves (and release lets
	// them go again), so nothing is grabbed into the old folder half-way. ok is false
	// while a search of it is already running. nil = no hold.
	Hold func(media string, id int64) (release func(), ok bool)
	// ShowFolder is the show folder an episode file lives in (series.Service.ShowFolderFor;
	// "" for a file loose in a library folder).
	ShowFolder func(episodePath string) string
	Log        *slog.Logger

	// mu runs moves one at a time, so a leftover staging copy is never a live one.
	mu sync.Mutex
	// rename is os.Rename; tests make it fail with EXDEV to take the copy path.
	rename func(oldpath, newpath string) error
	// free is the free space on the drive holding a folder (diskspace.Of); tests fake it.
	free func(path string) (uint64, bool)
}

// MoveResult is what a move did, stored as the job's result.
type MoveResult struct {
	Folder string     `json:"folder"` // the destination folder's label
	Method string     `json:"method"` // "rename", "copy", "none" (no files) or "mixed"
	Moved  []MovedDir `json:"moved"`  // each title folder that moved
	Paths  int64      `json:"paths"`  // stored paths repointed
	// Warning names files that turned up in an original folder after it was copied: they
	// weren't moved and were left where they were.
	Warning string `json:"warning,omitempty"`
}

// MovedDir is one title folder that moved.
type MovedDir struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Method string `json:"method"` // "rename" or "copy"
	Files  int    `json:"files"`
}

// ErrBusy is a title that can't move right now; the message says why.
var ErrBusy = errors.New("this title can't move right now")

// Progress is the job runner's reporter, narrowed (a nil one is fine).
type Progress interface {
	Set(pct float64, msg string)
}

type title struct {
	media    string
	id       int64
	name     string
	folderID int64
	files    []string
}

// Move moves title id of library media to folder to (0 = the default folder).
func (m *Mover) Move(ctx context.Context, media string, id, to int64, p Progress) (MoveResult, error) {
	report := func(pct float64, msg string) {
		if p != nil {
			p.Set(pct, msg)
		}
	}
	if !ValidMedia(media) {
		return MoveResult{}, ErrBadMedia
	}
	dest, ok := m.Folders.Get(ctx, media, to)
	if !ok {
		return MoveResult{}, ErrNotFound
	}
	if strings.TrimSpace(dest.Path) == "" {
		return MoveResult{}, fmt.Errorf("%w: the %s folder isn't set", ErrBadPath, dest.Label)
	}
	t, err := m.load(ctx, media, id)
	if err != nil {
		return MoveResult{}, err
	}
	res := MoveResult{Folder: dest.Label, Moved: []MovedDir{}}
	if m.Hold != nil {
		release, ok := m.Hold(media, id)
		if !ok {
			return res, fmt.Errorf("%w: a search for it is running — try again once it finishes", ErrBusy)
		}
		defer release()
	}
	if m.Busy != nil {
		if why := m.Busy(ctx, media, id); why != "" {
			return res, fmt.Errorf("%w: %s", ErrBusy, why)
		}
	}
	if why, err := m.convertOpen(ctx, t); err != nil {
		return res, err
	} else if why != "" {
		return res, fmt.Errorf("%w: %s", ErrBusy, why)
	}
	if fi, err := os.Stat(dest.Path); err != nil || !fi.IsDir() {
		return res, fmt.Errorf("%w: %s isn't there (is it mounted?)", ErrBadPath, dest.Path)
	}
	// One move at a time; and the destination can't be removed while files go into it.
	m.mu.Lock()
	defer m.mu.Unlock()
	done, err := m.Folders.beginMove(ctx, media, to)
	if err != nil {
		return res, err
	}
	defer done()
	m.discardLeftovers(dest.Path)

	report(0.02, "Checking "+t.name+"'s files")
	plans, err := m.plan(ctx, t, dest)
	if err != nil {
		return res, err
	}
	kind := "movie"
	if media == MediaSeries {
		kind = "show"
	}
	methods := map[string]bool{}
	for i, pl := range plans {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		base := 0.05 + 0.9*float64(i)/float64(len(plans))
		span := 0.9 / float64(len(plans))
		report(base, "Moving "+filepath.Base(pl.from))
		md, n, late, err := m.moveDir(ctx, t.media, to, pl, func(f float64, msg string) { report(base+span*f, msg) })
		res.Paths += n
		if late > 0 {
			res.Warning = strings.TrimSpace(res.Warning + fmt.Sprintf(" %d file(s) turned up in %s after it was copied and were left there.", late, pl.from))
		}
		if err != nil {
			if len(res.Moved) > 0 {
				return res, fmt.Errorf("moved %d of %d folders, then: %w — run Move again to finish", len(res.Moved), len(plans), err)
			}
			return res, err
		}
		res.Moved = append(res.Moved, md)
		methods[md.Method] = true
		if m.Scan != nil {
			// The old folder is gone: the scanner widens to the nearest folder still there,
			// as it does after a delete, so the server drops the title from there.
			m.Scan(kind, pl.from)
			m.Scan(kind, pl.to)
		}
	}
	// Every folder is where it belongs: now the title itself. (The destination can't have
	// been removed — beginMove holds it — but assign checks again inside its transaction.)
	if err := m.assign(ctx, t, to, dest.Label); err != nil {
		return res, err
	}
	switch len(methods) {
	case 0:
		res.Method = "none"
	case 1:
		for k := range methods {
			res.Method = k
		}
	default:
		res.Method = "mixed"
	}
	report(1, "Moved to "+dest.Label)
	if m.Log != nil {
		m.Log.Info("library folders: moved a title", "media", media, "id", id, "title", t.name, "to", dest.Path, "folders", len(res.Moved), "method", res.Method)
	}
	return res, nil
}

// load reads the title and every library file it has.
func (m *Mover) load(ctx context.Context, media string, id int64) (title, error) {
	t := title{media: media, id: id}
	var q string
	var fq []string
	if media == MediaSeries {
		q = `SELECT title, library_folder_id FROM series WHERE id = ?`
		fq = []string{`SELECT file_path FROM episodes WHERE series_id = ? AND has_file = 1 AND file_path <> ''`}
	} else {
		q = `SELECT title, library_folder_id FROM movies WHERE id = ?`
		fq = []string{
			`SELECT movie_file_path FROM movies WHERE id = ? AND has_file = 1 AND movie_file_path <> ''`,
			`SELECT file_path FROM movie_versions WHERE movie_id = ? AND has_file = 1 AND file_path <> ''`,
		}
	}
	if err := m.DB.QueryRowContext(ctx, q, id).Scan(&t.name, &t.folderID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return t, fmt.Errorf("%w: no such title", ErrNotFound)
		}
		return t, err
	}
	seen := map[string]bool{}
	for _, query := range fq {
		rows, err := m.DB.QueryContext(ctx, query, id)
		if err != nil {
			return t, err
		}
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				rows.Close()
				return t, err
			}
			if !seen[p] {
				seen[p] = true
				t.files = append(t.files, p)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return t, err
		}
	}
	sort.Strings(t.files)
	return t, nil
}

// convertOpen says why Convert holds the title ("" when it doesn't): an open swap means a
// converted file is being put in place, and moving its folder underneath would strand it.
func (m *Mover) convertOpen(ctx context.Context, t title) (string, error) {
	var n int
	var err error
	if t.media == MediaSeries {
		err = m.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM convert_swaps WHERE kind = 'episode' AND series_id = ?`, t.id).Scan(&n)
	} else {
		err = m.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM convert_swaps WHERE kind = 'movie' AND movie_id = ?`, t.id).Scan(&n)
	}
	if err != nil {
		return "", err
	}
	if n > 0 {
		return "Convert is replacing one of its files — try again once that finishes", nil
	}
	return "", nil
}

// movePlan is one title folder and where it goes.
type movePlan struct {
	from, to string
	root     string // the destination library folder
	staging  string // where a copy onto another drive is built (stagingPath)
	files    int
}

// plan works out which folders move: each folder holding the title's files (for a show,
// the show folder above its season folders), from the library folder it's in to the same
// place under dest. It refuses anything it can't move cleanly — a file sitting loose in a
// library folder, a folder shared with another title, or a destination already taken —
// before a single file moves.
func (m *Mover) plan(ctx context.Context, t title, dest Folder) ([]movePlan, error) {
	dirs := map[string]int{}
	for _, f := range t.files {
		dir := filepath.Dir(f)
		if t.media == MediaSeries && m.ShowFolder != nil {
			if show := m.ShowFolder(f); show != "" {
				dir = show
			}
		}
		dirs[filepath.Clean(dir)]++
	}
	// A folder inside another of the title's folders moves with it.
	var list []string
	for d := range dirs {
		list = append(list, d)
	}
	sort.Strings(list)
	var tops []string
	for _, d := range list {
		inside := false
		for _, top := range tops {
			if strictlyUnder(d, top) {
				dirs[top] += dirs[d]
				inside = true
				break
			}
		}
		if !inside {
			tops = append(tops, d)
		}
	}
	var out []movePlan
	taken := map[string]string{} // destination → the folder going there
	for _, dir := range tops {
		if strictlyUnder(dir, dest.Path) || samePath(dir, dest.Path) {
			continue // already in the destination folder
		}
		owner, ok := m.Folders.Owner(ctx, t.media, dir)
		if !ok {
			if loose, isRoot := m.isRoot(ctx, t.media, dir); isRoot {
				return nil, fmt.Errorf("%w: %s sits loose in the %s folder, not in a folder of its own — rename it (Organize) first so it has one", ErrBadPath, t.name, loose)
			}
			return nil, fmt.Errorf("%w: %s isn't inside any of this library's folders, so Arrmada won't move it", ErrBadPath, dir)
		}
		rel, err := filepath.Rel(filepath.Clean(owner.Path), dir)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			return nil, fmt.Errorf("%w: can't place %s under %s", ErrBadPath, dir, dest.Path)
		}
		// A show's files in a folder named like a season right inside the library folder
		// (<TV>/S13/…) were put there by mistake (#56): moving that folder along would carry
		// the mistake to the new library folder.
		if t.media == MediaSeries && rel == filepath.Base(dir) {
			if _, season := library.SeasonFolderNumber(rel); season {
				return nil, fmt.Errorf("%w: some of %s's files are in %s, a folder named like a season — put them back into the show's folder first (the show's page offers to)", ErrBadPath, t.name, dir)
			}
		}
		if other, err := m.sharedWith(ctx, t, dir); err != nil {
			return nil, err
		} else if other != "" {
			return nil, fmt.Errorf("%w: %s also holds files of %s — move or tidy that first", ErrBusy, dir, other)
		}
		to := filepath.Join(dest.Path, rel)
		if _, err := os.Lstat(to); err == nil {
			return nil, fmt.Errorf("%w: there's already a folder %s in %s", ErrBadPath, rel, dest.Label)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		// Two of the title's folders (in two library folders) can have the same place in
		// the destination: the second would land on the first.
		if prev, dup := taken[filepath.Clean(to)]; dup {
			return nil, fmt.Errorf("%w: %s and %s would both become %s — tidy one of them first", ErrBadPath, prev, dir, to)
		}
		taken[filepath.Clean(to)] = dir
		out = append(out, movePlan{from: dir, to: to, root: dest.Path, staging: stagingPath(dest.Path, rel), files: dirs[dir]})
	}
	return out, nil
}

// isRoot reports whether dir is one of the library's folders, and its label.
func (m *Mover) isRoot(ctx context.Context, media, dir string) (string, bool) {
	for _, f := range m.Folders.List(ctx, media) {
		if strings.TrimSpace(f.Path) != "" && samePath(f.Path, dir) {
			return f.Label, true
		}
	}
	return "", false
}

// sharedWith names another title with a file under dir ("" when none has one).
func (m *Mover) sharedWith(ctx context.Context, t title, dir string) (string, error) {
	prefix := filepath.Clean(dir) + string(filepath.Separator)
	var name string
	var err error
	if t.media == MediaSeries {
		err = m.DB.QueryRowContext(ctx, `SELECT s.title FROM episodes e JOIN series s ON s.id = e.series_id
			WHERE e.series_id <> ?1 AND e.has_file = 1 AND substr(e.file_path, 1, length(?2)) = ?2 LIMIT 1`, t.id, prefix).Scan(&name)
	} else {
		err = m.DB.QueryRowContext(ctx, `SELECT title FROM movies WHERE id <> ?1 AND (
				(has_file = 1 AND substr(movie_file_path, 1, length(?2)) = ?2)
				OR id IN (SELECT movie_id FROM movie_versions WHERE has_file = 1 AND substr(file_path, 1, length(?2)) = ?2))
			LIMIT 1`, t.id, prefix).Scan(&name)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return name, err
}

// moveDir moves one title folder and repoints the database at it. It returns how many
// stored paths changed, and how many files turned up in the original after it was copied
// (they're left where they are).
func (m *Mover) moveDir(ctx context.Context, media string, destID int64, pl movePlan, report func(float64, string)) (MovedDir, int64, int, error) {
	md := MovedDir{From: pl.from, To: pl.to, Files: pl.files}
	if err := os.MkdirAll(filepath.Dir(pl.to), 0o775); err != nil {
		return md, 0, 0, fmt.Errorf("couldn't make %s: %w", filepath.Dir(pl.to), err)
	}
	rename := m.rename
	if rename == nil {
		rename = os.Rename
	}
	err := rename(pl.from, pl.to)
	if err == nil {
		md.Method = "rename"
		n, derr := m.repoint(ctx, media, destID, pl.from, pl.to)
		if derr != nil {
			// Put the folder back so the database (unchanged) is right again.
			if berr := rename(pl.to, pl.from); berr != nil {
				return md, 0, 0, fmt.Errorf("the folder moved to %s but the database couldn't follow (%v), and moving it back failed too (%v) — move it back by hand", pl.to, derr, berr)
			}
			return md, 0, 0, fmt.Errorf("the database couldn't follow the move, so the folder was put back: %w", derr)
		}
		return md, n, 0, nil
	}
	if !errors.Is(err, syscall.EXDEV) {
		return md, 0, 0, fmt.Errorf("couldn't move %s: %w", pl.from, err)
	}

	// Another drive: room check, copy into the staging folder, verify, swap in, repoint,
	// then retire the originals that were copied.
	md.Method = "copy"
	src := manifest(pl.from)
	var size int64
	for _, n := range src {
		size += n
	}
	free := m.free
	if free == nil {
		free = diskFree
	}
	if avail, ok := free(pl.root); ok && int64(avail) < size+freeMargin(size) {
		return md, 0, 0, fmt.Errorf("%w: %s needs %s free on the destination drive and it has %s — nothing was moved",
			ErrNoRoom, filepath.Base(pl.from), gib(size+freeMargin(size)), gib(int64(avail)))
	}
	discard := func() {
		if derr := discardStaging(pl.root, pl.staging); derr != nil && m.Log != nil {
			m.Log.Warn("library folders: couldn't remove a move's staging copy", "path", pl.staging, "err", derr)
		}
	}
	if err := m.copyTree(ctx, pl.from, pl.staging, report); err != nil {
		discard()
		return md, 0, 0, err
	}
	copied := manifest(pl.staging)
	// Nothing may have changed in the original while it was copied (an import that slipped
	// in, a subtitle written): the originals are about to go, so the copy must be all of them.
	if !sameTree(manifest(pl.from), copied) {
		discard()
		return md, 0, 0, fmt.Errorf("%w: files in %s changed while they were being copied — nothing was moved; try again", ErrBusy, filepath.Base(pl.from))
	}
	if err := rename(pl.staging, pl.to); err != nil {
		discard()
		return md, 0, 0, fmt.Errorf("couldn't put the copy in place: %w", err)
	}
	n, derr := m.repoint(ctx, media, destID, pl.from, pl.to)
	if derr != nil {
		// The originals are untouched and the database still points at them: the copy goes
		// back to its staging name and is deleted.
		if rerr := rename(pl.to, pl.staging); rerr == nil {
			discard()
		} else if m.Log != nil {
			m.Log.Warn("library folders: a copy whose move failed is still in place", "path", pl.to, "err", rerr)
		}
		return md, 0, 0, fmt.Errorf("the database couldn't follow the copy, so the copy was taken back out: %w", derr)
	}
	report(0.95, "Retiring the originals to the recycle bin")
	left, late := m.retireCopied(pl.from, copied)
	if left > 0 {
		return md, n, late, fmt.Errorf("the copy is in place and in use, but %d original file(s) couldn't go to the recycle bin and are still in %s", left, pl.from)
	}
	return md, n, late, nil
}

// ErrNoRoom is a copy the destination drive hasn't the space for.
var ErrNoRoom = errors.New("not enough free space")

func diskFree(path string) (uint64, bool) {
	u, ok := diskspace.Of(path)
	return u.FreeBytes, ok
}

func gib(n int64) string { return fmt.Sprintf("%.1f GB", float64(n)/(1<<30)) }

// repoint rewrites every stored path under from to to, in one transaction — and only
// while the destination folder still exists, checked inside it.
func (m *Mover) repoint(ctx context.Context, media string, destID int64, from, to string) (int64, error) {
	var n int64
	err := store.WithTx(ctx, m.DB, func(tx *sql.Tx) error {
		if ok, err := folderExists(ctx, tx, media, destID); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("%w: the destination folder was removed", ErrNotFound)
		}
		c, err := RewritePrefix(ctx, tx, from, to)
		if err != nil {
			return err
		}
		n = c.Total()
		return nil
	})
	return n, err
}

// copyTree copies every file under src into dst (made fresh), each one verified against
// its original and given the original's modification time. Symlinks are refused: a link
// in a title folder could point anywhere, and copying what it points at isn't a move.
func (m *Mover) copyTree(ctx context.Context, src, dst string, report func(float64, string)) error {
	var total, done int64
	_ = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, e := d.Info(); e == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	if err := os.Mkdir(dst, 0o775); err != nil {
		return fmt.Errorf("couldn't start the copy in %s: %w", filepath.Dir(dst), err)
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		switch {
		case rel == ".":
			return nil
		case d.IsDir():
			return os.MkdirAll(target, 0o775)
		case d.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is a symlink — Arrmada won't move a title folder holding links", p)
		case !d.Type().IsRegular():
			return nil // sockets, devices: nothing a library holds
		}
		fi, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		if total > 0 {
			report(0.9*float64(done)/float64(total), "Copying "+d.Name())
		}
		if _, err := library.CopyVerified(p, target); err != nil {
			return fmt.Errorf("copying %s: %w", d.Name(), err)
		}
		_ = os.Chtimes(target, fi.ModTime(), fi.ModTime())
		done += fi.Size()
		return nil
	})
}

// manifest is every regular file under dir by its path inside it, with its size.
func manifest(dir string) map[string]int64 {
	out := map[string]int64{}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return nil
		}
		if fi, ierr := d.Info(); ierr == nil {
			out[rel] = fi.Size()
		}
		return nil
	})
	return out
}

func sameTree(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// retireCopied sends the originals that were copied to the recycle bin and removes the
// folders that leaves empty. The folder is looked at again first: only a file that's in
// the copy, at the size it was copied, goes; one that turned up (or changed) since is left
// exactly where it is and counted as late. left counts files the bin couldn't take.
func (m *Mover) retireCopied(dir string, copied map[string]int64) (left, late int) {
	var files, dirs []string
	now := manifest(dir)
	for rel, size := range now {
		if want, ok := copied[rel]; ok && want == size {
			files = append(files, filepath.Join(dir, rel))
		} else {
			late++
		}
	}
	sort.Strings(files)
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	for _, f := range files {
		if _, err := library.RemoveToBin(m.Bin, f); err != nil {
			left++
			if m.Log != nil {
				m.Log.Warn("library folders: couldn't retire a moved file's original", "path", f, "err", err)
			}
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, d := range dirs {
		_ = os.Remove(d) // only empty folders go
	}
	return left, late
}

// assign records the title's new folder and a timeline event, while that folder exists.
func (m *Mover) assign(ctx context.Context, t title, to int64, label string) error {
	return store.WithTx(ctx, m.DB, func(tx *sql.Tx) error {
		if ok, err := folderExists(ctx, tx, t.media, to); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("%w: the destination folder was removed", ErrNotFound)
		}
		detail := "Moved to the " + label + " folder"
		if t.media == MediaSeries {
			if _, err := tx.ExecContext(ctx, `UPDATE series SET library_folder_id = ? WHERE id = ?`, to, t.id); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO series_events (series_id, event, detail) VALUES (?, 'moved', ?)`, t.id, detail)
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE movies SET library_folder_id = ? WHERE id = ?`, to, t.id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO movie_events (movie_id, event, detail) VALUES (?, 'moved', ?)`, t.id, detail)
		return err
	})
}
