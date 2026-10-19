package stage

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// moveNoClobber puts from at to with one move that can never replace anything: a hard
// link (which fails if to exists) and then the old name removed. A copy in the job ends up
// as the library file in that one step, so a player never sees half of it.
//
// When from and to are on different drives (a library folder that is its own mount), the
// file is first copied into to's folder under a temporary name, synced, and then linked
// into place the same way: still one step in the library folder, never a partial file at
// to. Only on a filesystem with no hard links at all does the last step fall back to a
// rename, straight after a fresh check that to is free.
func moveNoClobber(from, to string) error {
	fi, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if ti, err := os.Lstat(to); err == nil {
		if !os.SameFile(fi, ti) {
			return fmt.Errorf("%w: %s", ErrTargetTaken, to)
		}
		if strings.EqualFold(from, to) && from != to {
			// One file under two spellings on a case-insensitive drive: a rename, not a
			// removal (removing "from" there would remove the file itself).
			return os.Rename(from, to)
		}
		// Two names for one file already: drop the old one.
		return os.Remove(from)
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
	tmp := filepath.Join(filepath.Dir(to), "."+filepath.Base(to)+".arrmada-part")
	_ = os.Remove(tmp) // our own leftover from an interrupted try
	if err := copyNew(from, tmp); err != nil {
		return err
	}
	if err := os.Link(tmp, to); err != nil {
		if errors.Is(err, fs.ErrExist) {
			_ = os.Remove(tmp)
			return fmt.Errorf("%w: %s", ErrTargetTaken, to)
		}
		if _, serr := os.Lstat(to); serr == nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("%w: %s", ErrTargetTaken, to)
		}
		if rerr := os.Rename(tmp, to); rerr != nil {
			_ = os.Remove(tmp)
			return rerr
		}
	} else {
		_ = os.Remove(tmp)
	}
	syncDir(filepath.Dir(to))
	return os.Remove(from)
}

// linkOrCopyNew puts a copy of src at dst without ever replacing anything at dst: a hard
// link when the drive allows it (instant, no space, and fine for a file that is never
// written, like an extra), else a copy linked into place.
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
	tmp := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".arrmada-part")
	_ = os.Remove(tmp)
	if err := copyNew(src, tmp); err != nil {
		return err
	}
	if err := os.Link(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%w: %s", ErrTargetTaken, dst)
		}
		if _, serr := os.Lstat(dst); serr == nil {
			return fmt.Errorf("%w: %s", ErrTargetTaken, dst)
		}
		if err := copyNew(src, dst); err != nil {
			return err
		}
		return nil
	}
	return os.Remove(tmp)
}

// MoveNoClobber is moveNoClobber for callers outside the package (a rename's extras).
func MoveNoClobber(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	return moveNoClobber(from, to)
}
