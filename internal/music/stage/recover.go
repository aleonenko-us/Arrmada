package stage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// Recorder records a file that a crash left in the library but not in the database: the
// caller's Ref and Meta say which track it is and what to store. It must be safe to run
// twice for one file.
type Recorder func(ctx context.Context, m Manifest, it Item) error

// RecoverReport is what Recover did.
type RecoverReport struct {
	Jobs      int // job folders found
	Finished  int // files moved into place or recorded
	RolledBak int // copies deleted / renames left undone / old files put back
	Kept      int // jobs kept for a person (or the next start)
}

// Recover settles every job left in libraryRoot's staging folder by a crash or a restart.
// It must run before anything else stages music (at start-up), since it deletes copies
// it finds unfinished. Per file, by the last state the manifest saved:
//
//   - Planned, Staged: the copy never reached the library; it is deleted. The source was
//     never written, so an import is simply tried again.
//   - Verified: the same, unless the copy did land at the final path (a crash inside the
//     final link-and-unlink), or the file it replaces is gone and the copy is all the
//     track has: then the copy goes into place.
//   - Arriving: the old file is still in place; the new one's temporary name and the copy
//     are deleted.
//   - Binning: if the old file is still at the final path, the bin didn't take it: roll
//     back as for Arriving. If it's gone, the bin has it: the new file moves into place.
//   - Binned: the new file moves into place (from its temporary name, else the copy); if
//     it's already there, it's recorded; if neither is left, the old file comes back out
//     of the bin.
//   - Placed: the file is in the library; record it.
//   - A rename's file is settled where it is: at its new name it's recorded, at its old
//     one the rename is simply undone.
//   - A split's pieces (Kind "split", MUS-35) never reach the library themselves: they
//     are all deleted (Planned, Staged and Verified above), and the import that used
//     them settled on its own.
//
// A job whose files are all settled is removed; one that can't be settled is kept with
// every file where it is. Errors on one job don't stop the others.
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
			d.Log.Error("music: a staging job couldn't be settled — kept for the next start, nothing deleted", "dir", dir, "err", err)
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
			// Also the copy's own temporary file inside such a temporary name (copyNew writes
			// "<name>.arrmada-part" beside whatever it fills, ours included).
			part, newTmp := tempName(it.Final, "part"), tempName(it.Final, "new")
			for _, p := range []string{part, tempName(part, "part"), tempName(newTmp, "part")} {
				if isTempName(filepath.Base(p)) {
					_ = os.Remove(p)
				}
			}
		}
		if j.m.Kind == KindRename {
			if err := j.recoverRename(ctx, i, record, rep); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		if err := j.recoverItem(i, rep); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", filepath.Base(it.Final), err))
			continue
		}
		if j.Items()[i].State == Placed {
			if err := record(ctx, j.m, j.Items()[i]); err != nil {
				errs = append(errs, fmt.Errorf("recording %s: %w", it.Final, err))
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

// recoverItem settles one import or retag file by its saved state (see Recover).
func (j *Job) recoverItem(i int, rep *RecoverReport) error {
	it := j.Items()[i]
	finalAt := exists(it.Final)
	switch it.State {
	case Recorded, Discarded:
		return nil
	case Planned, Staged:
		j.discard(i, "interrupted before it reached the library")
		rep.RolledBak++
		return nil
	case Placing:
		if movedIn(it) {
			return j.placed(i)
		}
		j.discard(i, "interrupted before it reached the library")
		rep.RolledBak++
		return nil
	case Verified:
		switch {
		case finalAt && exists(it.Staged) && sameFile(it.Final, it.Staged):
			// The final link was made; the copy's own name wasn't removed yet.
			return j.placed(i)
		case !finalAt && it.Replace != "" && exists(it.Staged):
			// The file it replaces is gone (to the bin before its move was saved): the
			// copy is all the track has, so it goes into place.
			if err := moveNoClobber(it.Staged, it.Final); err != nil {
				return err
			}
			return j.placed(i)
		}
		j.discard(i, "interrupted before it reached the library")
		rep.RolledBak++
		return nil
	case Arriving:
		j.dropTemp(it)
		j.discard(i, "interrupted before it reached the library")
		rep.RolledBak++
		return nil
	case Binning:
		if finalAt {
			// The old file is still in place: the bin never took it.
			j.dropTemp(it)
			j.discard(i, "interrupted before it reached the library")
			rep.RolledBak++
			return nil
		}
		return j.finishForward(i)
	case Binned:
		if finalAt {
			if landed(it) {
				return j.placed(i)
			}
			return fmt.Errorf("another file is at %s while the old one is in the bin (%s) and the new one in staging — left for a person", it.Final, it.Binned)
		}
		if err := j.finishForward(i); err == nil {
			return nil
		}
		if err := j.restore(i); err != nil {
			return err
		}
		rep.RolledBak++
		return nil
	case Placed:
		j.dropTemp(it)
		_ = removeOwn(j.dir, it.Staged)
		j.retire(it)
		return nil
	}
	return fmt.Errorf("unknown state %q", it.State)
}

// finishForward moves an item's new file into place once its old one is in the bin:
// from its temporary name in the album folder, else from the staged copy.
func (j *Job) finishForward(i int) error {
	it := j.Items()[i]
	switch {
	case it.Temp != "" && exists(it.Temp):
		if err := renameInto(it.Temp, it.Final); err != nil {
			return err
		}
	case exists(it.Staged):
		if err := moveNoClobber(it.Staged, it.Final); err != nil {
			return err
		}
	default:
		return errors.New("neither the new file's temporary name nor its staged copy is left")
	}
	return j.placed(i)
}

// recoverRename settles one file of an interrupted rename by where it actually is. Each
// move is a link and an unlink, so a crash leaves the file at its old name, its new one,
// or (between the two) both: then the old name is dropped, but only when they're proven
// to be two separate names (moveNoClobber).
func (j *Job) recoverRename(ctx context.Context, i int, record Recorder, rep *RecoverReport) error {
	it := j.Items()[i]
	switch it.State {
	case Recorded, Discarded:
		return nil
	case Planned:
		fromAt, toAt := exists(it.Source), exists(it.Final)
		switch {
		case fromAt && toAt && sameFile(it.Source, it.Final):
			if err := moveNoClobber(it.Source, it.Final); err != nil {
				// Not provably two names: keep both, as they are, and settle as not moved.
				rep.RolledBak++
				return j.set(i, func(it *Item) { it.State, it.Note = Discarded, "not moved: "+err.Error() })
			}
		case !fromAt && toAt:
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

// HeldInBin lists the recycle-bin paths that jobs still in libraryRoot's staging folder
// may need to put back (a replaced file whose new copy hasn't settled). The bin keeps
// them however old they get, until the job is settled.
func HeldInBin(libraryRoot string) map[string]bool {
	out := map[string]bool{}
	entries, err := os.ReadDir(Root(libraryRoot))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() || !reJobID.MatchString(e.Name()) {
			continue
		}
		m, err := readManifest(filepath.Join(Root(libraryRoot), e.Name()))
		if err != nil {
			continue
		}
		for _, it := range m.Items {
			if it.Binned != "" && !it.State.terminal() {
				out[it.Binned] = true
			}
		}
	}
	return out
}

func exists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Lstat(p)
	return err == nil
}

func sameFile(a, b string) bool {
	ai, err := os.Lstat(a)
	if err != nil {
		return false
	}
	bi, err := os.Lstat(b)
	return err == nil && os.SameFile(ai, bi)
}

// empty reports whether dir is missing or has nothing in it.
func empty(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err != nil || len(entries) == 0
}
