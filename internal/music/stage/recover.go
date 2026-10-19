package stage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/tristenlammi/arrmada/internal/library"
)

// Recorder records a file that a crash left in the library but not in the database: the
// caller's Ref and Meta say which track it is and what to store. It must be safe to run
// twice for one file.
type Recorder func(ctx context.Context, m Manifest, it Item) error

// RecoverReport is what Recover did.
type RecoverReport struct {
	Jobs      int // job folders found
	Finished  int // files moved into place or recorded
	RolledBak int // copies deleted / renames left undone
	Kept      int // jobs kept for a person (or the next start)
}

// Recover settles every job left in libraryRoot's staging folder by a crash or a restart.
// It must run before anything else stages music (at start-up), since it deletes copies
// it finds unfinished. Per file:
//
//   - Planned, Staged, Verified: the copy never reached the library. It is deleted (the
//     source was never written, so an import is simply tried again).
//   - Binned: the file it replaces is already in the recycle bin, so the copy is the
//     track's newest file: it is moved into place. If its target is taken after all,
//     the old file comes back out of the bin and the copy is dropped; if neither can be
//     done, the job is kept and nothing is deleted.
//   - Placed: the file is in the library; record it.
//   - A rename's file is settled where it is: at its new name it's recorded, at its old
//     one the rename is simply undone.
//
// A job whose files are all settled is removed. Errors on one job don't stop the others.
func Recover(ctx context.Context, libraryRoot string, d Deps, record Recorder) (RecoverReport, error) {
	var rep RecoverReport
	if d.Log == nil {
		d.Log = slog.Default()
	}
	root := Root(libraryRoot)
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return rep, nil
	}
	if err != nil {
		return rep, err
	}
	var errs []error
	for _, e := range entries {
		if !e.IsDir() || !reJobID.MatchString(e.Name()) {
			continue
		}
		rep.Jobs++
		dir := filepath.Join(root, e.Name())
		m, err := readManifest(dir)
		if errors.Is(err, fs.ErrNotExist) {
			// Start writes the manifest before anything else, so a job without one never
			// staged a file: only the folders it made are there.
			if empty(filepath.Join(dir, filesDir)) {
				_ = removeJobDir(dir)
				continue
			}
		}
		if err != nil {
			rep.Kept++
			d.Log.Error("music: a staging job can't be read — left for a person, nothing deleted", "dir", dir, "err", err)
			errs = append(errs, err)
			continue
		}
		j := &Job{dir: dir, m: *m, d: d}
		if err := j.recover(ctx, record, &rep); err != nil {
			rep.Kept++
			d.Log.Error("music: a staging job couldn't be settled — kept for the next start", "dir", dir, "err", err)
			errs = append(errs, err)
			continue
		}
		if err := j.Close(); err != nil {
			rep.Kept++
			errs = append(errs, err)
		}
	}
	return rep, errors.Join(errs...)
}

func (j *Job) recover(ctx context.Context, record Recorder, rep *RecoverReport) error {
	var errs []error
	for i, it := range j.Items() {
		// A copy onto another drive goes through ".<name>.arrmada-part" beside its target;
		// one a crash left there is ours, and never the only copy of anything.
		if it.Final != "" {
			_ = os.Remove(filepath.Join(filepath.Dir(it.Final), "."+filepath.Base(it.Final)+".arrmada-part"))
		}
		if j.m.Kind == KindRename {
			if err := j.recoverRename(ctx, i, record, rep); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		switch it.State {
		case Planned, Staged, Verified:
			j.discard(i, "interrupted before it reached the library")
			rep.RolledBak++
			continue
		case Binned:
			if err := j.recoverBinned(i, rep); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		if j.Items()[i].State == Placed {
			j.retire(j.Items()[i])
			if err := record(ctx, j.m, j.Items()[i]); err != nil {
				errs = append(errs, fmt.Errorf("recording %s: %w", j.Items()[i].Final, err))
				continue
			}
			if err := j.Recorded(i); err != nil {
				errs = append(errs, err)
				continue
			}
			rep.Finished++
		}
	}
	return errors.Join(errs...)
}

// recoverBinned finishes a file whose old version went to the bin: the staged copy goes
// into place. Failing that, the old file comes back out of the bin, so the track is never
// left with no file.
func (j *Job) recoverBinned(i int, rep *RecoverReport) error {
	it := j.Items()[i]
	if _, err := os.Lstat(it.Staged); err == nil {
		if err := os.MkdirAll(filepath.Dir(it.Final), 0o755); err != nil {
			return err
		}
		if err := moveNoClobber(it.Staged, it.Final); err == nil {
			return j.set(i, func(it *Item) { it.State = Placed })
		} else if !errors.Is(err, ErrTargetTaken) {
			return err
		}
	}
	// The copy can't go in (it's gone, or something now sits at the target): put the old
	// file back where it was, if the bin still has it and its place is free.
	if it.Binned != "" {
		if _, err := os.Lstat(it.Binned); err == nil {
			if err := moveNoClobber(it.Binned, it.Replace); err != nil {
				return fmt.Errorf("the replaced file is in the recycle bin (%s) and couldn't go back: %w", it.Binned, err)
			}
			_ = os.Remove(it.Binned + library.RecycleMetaExt) // the bin's note about it, now stale
		}
	}
	if _, err := os.Lstat(it.Staged); err == nil {
		// Only now, with the old file back in place, is the copy just a copy again.
		if err := removeOwn(j.dir, it.Staged); err != nil {
			return err
		}
	}
	rep.RolledBak++
	return j.set(i, func(it *Item) { it.State, it.Note = Discarded, "rolled back: the old file is back in place" })
}

// recoverRename settles one file of an interrupted rename by where it actually is. Each
// move is a link and an unlink, so a crash leaves the file at its old name, its new one,
// or (between the two) both: then the old name is dropped.
func (j *Job) recoverRename(ctx context.Context, i int, record Recorder, rep *RecoverReport) error {
	it := j.Items()[i]
	switch it.State {
	case Recorded, Discarded:
		return nil
	case Planned:
		fi, ferr := os.Lstat(it.Source)
		ti, terr := os.Lstat(it.Final)
		switch {
		case ferr == nil && terr == nil && os.SameFile(fi, ti):
			if err := moveNoClobber(it.Source, it.Final); err != nil {
				return err
			}
		case ferr != nil && terr == nil:
			// Moved; the old name is gone.
		default:
			rep.RolledBak++
			return j.set(i, func(it *Item) { it.State, it.Note = Discarded, "not moved" })
		}
		if err := j.set(i, func(it *Item) { it.State = Placed }); err != nil {
			return err
		}
	}
	if err := record(ctx, j.m, j.Items()[i]); err != nil {
		return fmt.Errorf("recording %s: %w", it.Final, err)
	}
	rep.Finished++
	return j.Recorded(i)
}

// empty reports whether dir is missing or has nothing in it.
func empty(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err != nil || len(entries) == 0
}
