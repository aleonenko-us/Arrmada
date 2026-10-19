package stage

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The moves. None of them ever replaces a file, and none removes a name unless it is
// proven that another name still holds the same file.

// maxName is the filesystem limit on one name, in bytes.
const maxName = 255

// tempName is our temporary name for path, in path's folder: ".<name>.arrmada-<kind>",
// or a short hash of the name when that would pass the 255-byte limit. Hidden, so a
// scanner or a media server skips it while it exists.
func tempName(path, kind string) string {
	base := filepath.Base(path)
	name := "." + base + ".arrmada-" + kind
	if len(name) > maxName {
		sum := sha256.Sum256([]byte(base))
		name = ".arrmada-" + kind + "-" + hex.EncodeToString(sum[:8]) + strings.ToLower(filepath.Ext(base))
		if len(name) > maxName {
			name = name[:maxName]
		}
	}
	return filepath.Join(filepath.Dir(path), name)
}

// isTempName reports whether a name is one tempName makes.
func isTempName(name string) bool {
	return strings.HasPrefix(name, ".") && strings.Contains(name, ".arrmada-")
}

// moveNoClobber puts from at to with one move that never replaces anything: a hard link
// (which fails if to exists), then the old name is removed. A file lands at to complete
// or not at all, so a player never sees half of one.
//
// When to already names the same file, the move is refused (ErrSameFile) unless it is
// proven that from is a second, separate name for it (distinctNames): to can be the same
// file reached through a symlinked or bind-mounted folder, or another spelling the
// filesystem treats as the same name, and removing from there would remove the file's
// only name. A change of case alone is a rename.
//
// When from and to are on different drives, the file is copied into to's folder under a
// temporary name, synced, and linked into place the same way. Only on a filesystem with
// no hard links does the last step fall back to a rename, after a fresh check that to is
// free.
func moveNoClobber(from, to string) error {
	fi, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if ti, err := os.Lstat(to); err == nil {
		if !os.SameFile(fi, ti) {
			return fmt.Errorf("%w: %s", ErrTargetTaken, to)
		}
		if distinctNames(from, to, fi) {
			return os.Remove(from) // two names for one file: drop the old one
		}
		if from != to && strings.EqualFold(from, to) && filepath.Dir(from) == filepath.Dir(to) {
			// One name in a different case on a case-insensitive drive: a rename changes it.
			return os.Rename(from, to)
		}
		if from == to {
			return nil
		}
		return fmt.Errorf("%w: %s", ErrSameFile, to)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Link(from, to); err == nil {
		syncDir(filepath.Dir(to))
		if rerr := os.Remove(from); rerr != nil {
			// Two names for one file. Drop the new one so the move reads as not done.
			_ = os.Remove(to)
			return rerr
		}
		return nil
	} else if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%w: %s", ErrTargetTaken, to)
	}
	// Another drive (or no links here): copy beside the target, then link it into place.
	tmp := tempName(to, "part")
	_ = os.Remove(tmp) // our own leftover from an interrupted try
	if err := copyNew(from, tmp); err != nil {
		return err
	}
	if err := renameInto(tmp, to); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	syncDir(filepath.Dir(to))
	// The copy is complete and synced at to; the original goes only if it's the size the
	// copy is (nothing changed it under us).
	if ti, err := os.Stat(to); err != nil || ti.Size() != fi.Size() {
		return fmt.Errorf("the copy at %s doesn't match %s — both kept", to, from)
	}
	return os.Remove(from)
}

// distinctNames reports whether from and to are proven to be two separate names (two
// directory entries) for one file, so that removing from leaves the file at to: it has
// at least two links, each name is listed by its own folder under exactly that spelling,
// they aren't the same name in the same folder reached two ways, and they resolve to
// different paths.
func distinctNames(from, to string, fi os.FileInfo) bool {
	if linkCount(fi) < 2 || from == to {
		return false
	}
	if !listed(from) || !listed(to) {
		return false
	}
	fd, ferr := os.Stat(filepath.Dir(from))
	td, terr := os.Stat(filepath.Dir(to))
	if ferr != nil || terr != nil {
		return false
	}
	if os.SameFile(fd, td) && filepath.Base(from) == filepath.Base(to) {
		return false // one entry, reached through two paths to its folder
	}
	rf, e1 := resolved(from)
	rt, e2 := resolved(to)
	return e1 == nil && e2 == nil && rf != rt
}

// listed reports whether path's folder lists an entry spelled exactly like path's name.
// On a drive that folds case or Unicode forms, another spelling isn't listed.
func listed(path string) bool {
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return false
	}
	base := filepath.Base(path)
	for _, e := range entries {
		if e.Name() == base {
			return true
		}
	}
	return false
}

// resolved is path with its folder's symlinks resolved.
func resolved(path string) (string, error) {
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, filepath.Base(path)), nil
}

// SameEntry reports whether two paths reach the same file without being proven separate
// names for it: a move between them would have to remove the file's only name. The music
// rename's planner flags such a move instead of making it.
func SameEntry(from, to string) bool {
	fi, err := os.Lstat(from)
	if err != nil {
		return false
	}
	ti, err := os.Lstat(to)
	if err != nil || !os.SameFile(fi, ti) {
		return false
	}
	if from != to && strings.EqualFold(from, to) && filepath.Dir(from) == filepath.Dir(to) {
		return false // a change of case: a rename does it
	}
	return !distinctNames(from, to, fi)
}

// renameInto gives tmp (a name of ours) the name dst, never replacing anything at dst: a
// hard link to dst, then our temporary name goes. Where links aren't possible, a rename
// straight after a check that dst is free.
func renameInto(tmp, dst string) error {
	if err := os.Link(tmp, dst); err == nil {
		syncDir(filepath.Dir(dst))
		return os.Remove(tmp) // ours; the file stays at dst
	} else if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%w: %s", ErrTargetTaken, dst)
	}
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%w: %s", ErrTargetTaken, dst)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	syncDir(filepath.Dir(dst))
	return nil
}

// linkOrCopyNew puts src's content at dst (which must be free) without touching src: a
// hard link when the drive allows it, else a copy linked into place. Used for the staged
// copy's temporary name beside its target.
func linkOrCopyNew(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%w: %s", ErrTargetTaken, dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Link(src, dst); err == nil {
		return nil
	} else if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%w: %s", ErrTargetTaken, dst)
	}
	return copyNew(src, dst)
}

// copyNoClobber copies src to dst, never over anything at dst. Extras are copied, not
// linked: a media server writing folder.jpg or a .lrc in the album folder must not be
// able to change a file a torrent is still seeding.
func copyNoClobber(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%w: %s", ErrTargetTaken, dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return copyNew(src, dst)
}

// MoveNoClobber is moveNoClobber for callers outside the package.
func MoveNoClobber(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	return moveNoClobber(from, to)
}
