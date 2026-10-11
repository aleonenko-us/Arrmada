package convert

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The staged-replace journal (convert_swaps, migrations 0064 and 0211). finalizeOutput
// writes the row BEFORE each step it takes and moves its state on as the steps complete,
// so at startup any row still present says exactly how far an interrupted swap got:
//
//	staging  the encode is being copied next to the original as .arrpart (original untouched)
//	staged   the copy is complete (original untouched)
//	retired  the original was handed to the recycle bin; the part is renamed in next
//
// The row is cleared once the rename AND the library repoint have both succeeded.
const (
	swapStaging = "staging"
	swapStaged  = "staged"
	swapRetired = "retired"
)

// swapRow is one journal entry.
type swapRow struct {
	part, final, src, kind string
	movieID, seriesID      int64
	season, episode        int
	state, holdPath, op    string
	historyID              int64
}

// recordSwap journals a swap about to start, in its first state. An error means the swap
// must not go ahead: without the row, a crash part-way would leave files nobody knows
// about.
func (s *Service) recordSwap(ctx context.Context, job *Job, part, final, src, state string, historyID int64) error {
	kind := "movie"
	if job.Kind == "episode" {
		kind = "episode"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO convert_swaps (part, final, src, kind, movie_id, series_id, season, episode, state, hold_path, op, history_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', 'convert', ?)`,
		part, final, src, kind, job.MovieID, job.SeriesID, job.Season, job.Episode, state, historyID)
	return err
}

// setSwapState moves a journalled swap on to its next state. An error (or a row that has
// gone) means the swap must stop: the journal no longer describes it.
func (s *Service) setSwapState(ctx context.Context, part, state string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE convert_swaps SET state = ? WHERE part = ?`, state, part)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("the swap's journal entry is missing")
	}
	return nil
}

// clearSwap removes a completed (or abandoned) swap's journal row.
func (s *Service) clearSwap(part string) {
	_, _ = s.db.ExecContext(context.Background(), `DELETE FROM convert_swaps WHERE part = ?`, part)
}

// swapRows reads every journal entry.
func (s *Service) swapRows(ctx context.Context) ([]swapRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT part, final, src, kind, movie_id, series_id, season, episode, state, hold_path, op, history_id FROM convert_swaps`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []swapRow
	for rows.Next() {
		var w swapRow
		if rows.Scan(&w.part, &w.final, &w.src, &w.kind, &w.movieID, &w.seriesID, &w.season, &w.episode,
			&w.state, &w.holdPath, &w.op, &w.historyID) == nil {
			out = append(out, w)
		}
	}
	return out, rows.Err()
}

func exists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Lstat(path)
	return err == nil
}

// recoverSwaps replays interrupted swaps at startup. Whatever the crash cut off, it ends
// with one of the two files in the library and the record pointing at it — never neither:
//
//   - staging / staged: the original was never touched, so the part is removed.
//   - retired: when the original is still at its path it was never retired, so the part
//     goes (an MP4 source used to get its .mkv renamed in next to it, a duplicate, and an
//     MKV source had its original stamped as converted); otherwise the rename is completed
//     and the record repointed.
func (s *Service) recoverSwaps(ctx context.Context) {
	swaps, err := s.swapRows(ctx)
	if err != nil {
		return // table may predate the migration runner in tests; nothing to recover
	}
	for _, w := range swaps {
		switch w.state {
		case swapStaging, swapStaged:
			s.abandonSwap(w, "an interrupted conversion was rolled back — the original was never touched")
			continue
		}
		s.recoverRetired(ctx, w)
	}
}

// abandonSwap removes a swap's part and its row, the original being known to be in place.
func (s *Service) abandonSwap(w swapRow, msg string) {
	if err := os.Remove(w.part); err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.log.Warn("convert: could not remove an interrupted conversion's staged file", "part", w.part, "err", err)
		return // keep the row so it's tried again
	}
	s.clearSwap(w.part)
	s.log.Info("convert: "+msg, "file", w.src)
}

// recoverRetired finishes (or undoes) a swap whose original was handed to the recycle bin.
func (s *Service) recoverRetired(ctx context.Context, w swapRow) {
	if exists(w.part) {
		switch {
		case exists(w.src):
			// The original is still at its path: it was never retired (the crash came before
			// the bin took it — the rename only ever follows that, and replaces the part).
			// Keep it; the converted copy is discarded.
			s.abandonSwap(w, "an interrupted conversion was rolled back — the original was still in place")
			return
		case exists(w.final):
			// The final name is taken by a file that isn't the original: the rename happened
			// by another route and the part is a stale duplicate.
			_ = os.Remove(w.part)
		default:
			if err := os.Rename(w.part, w.final); err != nil {
				s.log.Warn("convert: could not complete an interrupted swap", "part", w.part, "err", err)
				return // keep the row; maybe the volume comes back
			}
			s.log.Info("convert: completed an interrupted file swap", "final", w.final)
		}
	} else if !exists(w.final) {
		// Neither part nor final exists — stale row (files handled by hand).
		s.clearSwap(w.part)
		return
	}
	s.repointRecovered(ctx, w)
}

// repointRecovered points the library record at a recovered swap's final file and clears
// the row; on failure the row stays for the next startup.
func (s *Service) repointRecovered(ctx context.Context, w swapRow) bool {
	job := &Job{Kind: w.kind, MovieID: w.movieID, SeriesID: w.seriesID, Season: w.season, Episode: w.episode}
	if err := s.markConverted(ctx, job, w.src, w.final, "", 0); err != nil {
		s.log.Warn("convert: could not repoint the library record for a recovered swap",
			"final", w.final, "err", err)
		return false
	}
	s.log.Info("convert: reconciled an interrupted conversion", "final", w.final)
	s.clearSwap(w.part)
	s.swapped(job, w.final)
	return true
}

// partSuffix marks a converted file staged next to its original before the swap.
const partSuffix = ".arrpart"

// orphanPartAge is how old an unjournalled .arrpart must be before the sweep removes it. A
// copy in progress keeps touching its file, and every swap is journalled before it starts,
// so this is only a second guard.
const orphanPartAge = 24 * time.Hour

// sweepOrphanParts removes .arrpart files no swap owns from the folders of indexed files:
// left by a crash before swaps were journalled first, they could sit there for good — a
// multi-GB copy of a film Plex doesn't see. A part the journal names, one a running job
// will produce, or one written in the last day is left alone.
func (s *Service) sweepOrphanParts(ctx context.Context) int {
	if s.index == nil || s.index.db == nil {
		return 0
	}
	rows, err := s.index.db.QueryContext(ctx, `SELECT path FROM convert_library`)
	if err != nil {
		return 0
	}
	dirs := map[string]bool{}
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil && p != "" {
			dirs[filepath.Dir(p)] = true
		}
	}
	rows.Close()
	journalled := map[string]bool{}
	if swaps, err := s.swapRows(ctx); err == nil {
		for _, w := range swaps {
			journalled[w.part] = true
		}
	} else {
		return 0 // can't tell what's owned: touch nothing
	}
	owned := s.pendingParts()
	removed := 0
	for dir := range dirs {
		if ctx.Err() != nil {
			break
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), partSuffix) {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if journalled[p] || owned[p] {
				continue
			}
			fi, err := e.Info()
			if err != nil || time.Since(fi.ModTime()) < orphanPartAge {
				continue
			}
			if os.Remove(p) == nil {
				removed++
				s.log.Info("convert: removed an orphaned staged file", "path", p, "size_mb", fi.Size()>>20)
			}
		}
	}
	return removed
}

// pendingParts is the .arrpart path every running job would stage, so the orphan sweep never
// touches one that is being written.
func (s *Service) pendingParts() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]bool{}
	for _, j := range s.pending {
		if j.rec != nil && j.rec.srcPath != "" {
			out[partPathFor(j.rec.srcPath)] = true
		}
	}
	return out
}

// nameOwner says why the converted file may not take path, which already holds a file:
// another library record points at it (a second version of a film, an episode filed under
// the same name). "" means no record owns it, so it's a stray that can be moved aside. A
// lookup that fails counts as owned — a file is never moved on a guess.
func (s *Service) nameOwner(ctx context.Context, path string) string {
	name := filepath.Base(path)
	if s.movies != nil {
		_, _, ok, err := s.movies.FileOwner(ctx, path)
		if err != nil {
			return "couldn't check whether another library record owns " + name
		}
		if ok {
			return "another library record owns " + name
		}
	}
	if s.series != nil {
		_, ok, err := s.series.EpisodeFileOwner(ctx, path)
		if err != nil {
			return "couldn't check whether another library record owns " + name
		}
		if ok {
			return "another library record owns " + name
		}
	}
	return ""
}

// finalPathFor is where a conversion of src lands: the same name as an .mkv.
func finalPathFor(src string) string { return strings.TrimSuffix(src, filepath.Ext(src)) + ".mkv" }

// partPathFor is where a conversion of src is staged before the swap.
func partPathFor(src string) string { return finalPathFor(src) + partSuffix }
