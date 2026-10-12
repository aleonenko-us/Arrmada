package convert

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Revert: one click puts a conversion's original back from the hold. Nothing in Convert
// could undo a conversion before, and the recycle bin's Restore refused whenever a file sat
// at the original's path — always, for an MKV source, whose converted file takes the very
// same name.
//
// A revert is journalled in convert_swaps like a conversion (op 'revert'): part is where
// the converted file goes in the hold, final the original's path, src the converted file's
// path. States:
//
//	reverting  the files are being swapped
//	reverted   the original is back and the converted file is in the hold; the library
//	           record is repointed next
//
// recoverRevert finishes a revert whose files had both moved and rolls back one that
// hadn't, so the library always ends up holding one whole file with the record on it.

// SkipReverted: you reverted this file's conversion, so the runner never picks it again on
// its own. Converting it by hand clears it (a request clears skips).
const SkipReverted = "reverted"

const (
	swapReverting = "reverting"
	swapReverted  = "reverted"
	opRevert      = "revert"
)

// ErrRevertChanged means the library file is no longer the converted one (an upgrade or a
// re-import replaced it), so putting the original back would undo that.
var ErrRevertChanged = errors.New("the library file has changed since (an upgrade or re-import), so reverting would undo that")

// ErrRevertBusy means the file is being converted (or reverted) right now.
var ErrRevertBusy = errors.New("this file is being converted right now — try again once that's finished")

// Revert puts the original of ledger row id back in the library in place of its converted
// file: the record points at the original again (with its release, and without the
// pre-conversion baseline), the converted file is deleted, and the runner never picks the
// file again on its own. Returns the updated row.
func (s *Service) Revert(ctx context.Context, id int64) (HistoryEntry, error) {
	e, err := s.history.get(ctx, id)
	if err != nil {
		return e, err
	}
	release, ok := s.claimItem(e.Key)
	if !ok {
		return e, ErrRevertBusy
	}
	defer release()
	// The hold's lock: the original can't be released (or expire) while it's put back.
	s.holdMu.Lock()
	defer s.holdMu.Unlock()
	if e, err = s.history.get(ctx, id); err != nil { // as it is now, under the lock
		return e, err
	}
	if err := s.revertable(ctx, e); err != nil {
		return e, err
	}
	w := swapRow{part: revertPartFor(e), final: e.SrcPath, src: e.OutPath, kind: e.Kind,
		movieID: e.MovieID, seriesID: e.SeriesID, season: e.Season, episode: e.Episode,
		state: swapReverting, holdPath: e.HoldPath, op: opRevert, historyID: id}
	if err := s.journalRevert(ctx, w); err != nil {
		return e, fmt.Errorf("could not journal the revert (%v) — nothing was changed", err)
	}
	if err := s.revertMoves(w); err != nil {
		s.clearSwap(w.part) // revertMoves put everything back
		return e, err
	}
	if err := s.setSwapState(ctx, w.part, swapReverted); err != nil {
		if s.undoRevertMoves(w) {
			s.clearSwap(w.part)
			return e, fmt.Errorf("could not journal the revert (%v) — nothing was changed", err)
		}
		return e, fmt.Errorf("could not journal the revert (%v) — it will be finished at the next startup", err)
	}
	if err := s.finishRevert(ctx, e, w); err != nil {
		// The files are swapped and the record may be half-updated: only finishing it is
		// consistent, and the journal lets the next startup do that.
		s.log.Error("convert: revert swapped the files but couldn't update the library", "title", e.Title, "err", err)
		return e, fmt.Errorf("the original is back, but the library record couldn't be updated (%v) — it will be finished at the next startup", err)
	}
	return s.history.get(ctx, id)
}

// revertable checks a ledger row can be reverted right now.
func (s *Service) revertable(ctx context.Context, e HistoryEntry) error {
	if e.Outcome != OutcomeDone || e.HoldState != HoldHeld || e.HoldPath == "" || e.OutPath == "" || e.SrcPath == "" {
		return ErrNotHeld
	}
	if busy, err := s.swapInFlight(ctx, e.ID); err != nil || busy {
		if err == nil {
			err = ErrSwapBusy
		}
		return err
	}
	if !isHoldFile(e.HoldPath, e.ID) {
		return fmt.Errorf("the recorded hold path %s isn't inside an originals hold — left alone", e.HoldPath)
	}
	if got := fileSize(e.HoldPath); !exists(e.HoldPath) || (e.SrcSize > 0 && got != e.SrcSize) {
		return fmt.Errorf("the held original isn't whole (%s of %s) — left as it is", humanBytes(got), humanBytes(e.SrcSize))
	}
	// The library must still hold the converted file: recorded at out_path, at its size.
	owned, err := s.recordAt(ctx, e, e.OutPath)
	if err != nil {
		return err
	}
	if !owned || !exists(e.OutPath) || (e.OutSize > 0 && fileSize(e.OutPath) != e.OutSize) {
		return ErrRevertChanged
	}
	if e.SrcPath != e.OutPath && exists(e.SrcPath) {
		return fmt.Errorf("a file is already at %s — left as it is", filepath.Base(e.SrcPath))
	}
	if exists(revertPartFor(e)) {
		return fmt.Errorf("an earlier revert of this file left %s in the hold — left as it is", filepath.Base(revertPartFor(e)))
	}
	return nil
}

// recordAt reports whether the row's own track — the episode, the movie's default file or
// the extra version its key names — records path as its file now.
func (s *Service) recordAt(ctx context.Context, e HistoryEntry, path string) (bool, error) {
	it, ok := parseKey(e.Key)
	if !ok {
		return false, fmt.Errorf("the conversion record names no library item (%q)", e.Key)
	}
	src, _, _, ok := s.resolveSource(ctx, it.job())
	return ok && src == path, nil
}

// revertPartFor is where a revert puts the converted file: in the row's hold folder, under
// a name that can't collide with the original's (for an MKV source both share one name).
func revertPartFor(e HistoryEntry) string {
	return filepath.Join(filepath.Dir(e.HoldPath), "converted-"+filepath.Base(e.OutPath))
}

func (s *Service) journalRevert(ctx context.Context, w swapRow) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO convert_swaps (part, final, src, kind, movie_id, series_id, season, episode, state, hold_path, op, history_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		w.part, w.final, w.src, w.kind, w.movieID, w.seriesID, w.season, w.episode, w.state, w.holdPath, w.op, w.historyID)
	return err
}

// revertMoves swaps the files: the original back to its path, the converted file into the
// hold. On failure everything it moved goes back and the error says what happened.
//
//   - MKV source (one name for both): converted file → hold first, then the original back.
//   - Otherwise: the original back first (its name is free), then the converted file away.
func (s *Service) revertMoves(w swapRow) error {
	out, conv, orig, held := w.src, w.part, w.final, w.holdPath
	if out == orig {
		if err := s.moveKept(out, conv); err != nil {
			return fmt.Errorf("could not move the converted file aside: %v — nothing was changed", err)
		}
		if err := s.moveKept(held, orig); err != nil {
			if back := s.moveKept(conv, out); back != nil {
				s.log.Error("convert: revert couldn't put the converted file back — the next startup will", "file", out, "err", back)
			}
			return fmt.Errorf("could not move the original back: %v — nothing was changed", err)
		}
		return nil
	}
	if err := s.moveKept(held, orig); err != nil {
		return fmt.Errorf("could not move the original back: %v — nothing was changed", err)
	}
	if err := s.moveKept(out, conv); err != nil {
		if back := s.moveKept(orig, held); back != nil {
			s.log.Error("convert: revert couldn't return the original to the hold — the next startup will", "file", orig, "err", back)
		}
		return fmt.Errorf("could not move the converted file aside: %v — nothing was changed", err)
	}
	return nil
}

// undoRevertMoves puts an unfinished revert's files back as the conversion left them,
// reporting whether it managed.
func (s *Service) undoRevertMoves(w swapRow) bool {
	out, conv, orig, held := w.src, w.part, w.final, w.holdPath
	ok := true
	if out != orig && exists(orig) && !exists(held) {
		if err := s.moveKept(orig, held); err != nil {
			ok = false
		}
	}
	if out == orig && exists(orig) && !exists(held) && exists(conv) {
		// Both had moved for an MKV source: the original goes back to the hold first.
		if err := s.moveKept(orig, held); err != nil {
			return false
		}
	}
	if exists(conv) && !exists(out) {
		if err := s.moveKept(conv, out); err != nil {
			ok = false
		}
	}
	return ok
}

// finishRevert updates the library and the ledger once the original is back: repoint the
// record (restoring its release and, unless the original was itself a conversion, clearing
// the pre-conversion baseline), keep the runner off the file, mark the row reverted, then
// delete the converted copy and tidy the hold. Safe to run again (startup recovery).
func (s *Service) finishRevert(ctx context.Context, e HistoryEntry, w swapRow) error {
	keepBaseline := s.history.convertedBefore(ctx, e)
	srcSize := fileSize(e.SrcPath)
	if e.Kind == "episode" {
		if s.series == nil {
			return errors.New("the TV module isn't available")
		}
		n, err := s.series.RestoreConvertedFile(ctx, e.SeriesID, e.OutPath, e.SrcPath, srcSize, e.SrcRelease, keepBaseline)
		if err != nil {
			return err
		}
		if n == 0 {
			if done, err := s.recordAt(ctx, e, e.SrcPath); err != nil || !done {
				return fmt.Errorf("no episode record at %s to repoint", filepath.Base(e.OutPath))
			}
		}
	} else {
		if s.movies == nil {
			return errors.New("the movies module isn't available")
		}
		n, err := s.movies.RestoreConvertedFile(ctx, e.MovieID, e.OutPath, e.SrcPath, e.SrcRelease, keepBaseline)
		if err != nil {
			return err
		}
		if n == 0 {
			if done, err := s.recordAt(ctx, e, e.SrcPath); err != nil || !done {
				return fmt.Errorf("no movie record at %s to repoint", filepath.Base(e.OutPath))
			}
		}
	}
	s.skips.record(ctx, e.Key, SkipReverted, "you reverted its conversion — convert it by hand to try again")
	if err := s.history.markReverted(ctx, e.ID); err != nil {
		return err
	}
	s.clearSwap(w.part)
	// The converted copy goes only now the record points at the original.
	if err := os.Remove(w.part); err != nil && exists(w.part) {
		s.log.Warn("convert: couldn't delete the converted copy after a revert", "file", w.part, "err", err)
	}
	// A stray moved aside when this was converted gets its name back.
	if stray := filepath.Join(filepath.Dir(e.HoldPath), filepath.Base(e.OutPath)); e.OutPath != e.SrcPath && exists(stray) && !exists(e.OutPath) {
		if err := s.moveKept(stray, e.OutPath); err != nil {
			s.log.Warn("convert: couldn't put back a file moved aside by the conversion", "file", stray, "err", err)
		}
	}
	_ = os.Remove(filepath.Dir(e.HoldPath)) // only if empty
	job := &Job{Kind: e.Kind, MovieID: e.MovieID, SeriesID: e.SeriesID, Season: e.Season, Episode: e.Episode, Title: e.Title}
	if it, ok := parseKey(e.Key); ok {
		job = it.job() // the track itself (a movie version's key names it)
		job.Title = e.Title
	}
	s.reindexConverted(ctx, job)
	s.swapped(job, e.SrcPath) // Plex rescans the folder (movies also go through movie.changed)
	s.event("info", fmt.Sprintf("Reverted %s — the original (%s) is back", e.Title, humanBytes(e.SrcSize)))
	s.publish(TopicReverted, RevertedEvent{Title: e.Title, Key: e.Key, Kind: e.Kind, MovieID: e.MovieID, VersionID: e.VersionID,
		SeriesID: e.SeriesID, Season: e.Season, Episode: e.Episode, Path: e.SrcPath, OldPath: e.OutPath,
		SrcBytes: e.SrcSize, OutBytes: e.OutSize, HistoryID: e.ID, At: time.Now().Unix()})
	s.holdFreed(ctx)
	return nil
}

// RevertedEvent is convert.reverted (events.go): a conversion was undone and its original
// is back in the library. Path is the original, OldPath the converted file it replaced.
type RevertedEvent struct {
	Title     string `json:"title"`
	Key       string `json:"key"`
	Kind      string `json:"kind"` // "movie" | "episode"
	MovieID   int64  `json:"movie_id,omitempty"`
	VersionID int64  `json:"version_id,omitempty"`
	SeriesID  int64  `json:"series_id,omitempty"`
	Season    int    `json:"season"`
	Episode   int    `json:"episode,omitempty"`
	Path      string `json:"path"`
	OldPath   string `json:"old_path"`
	SrcBytes  int64  `json:"src_bytes"`
	OutBytes  int64  `json:"out_bytes"`
	HistoryID int64  `json:"history_id"`
	At        int64  `json:"at"`
}

// convertedBefore reports whether the row's original was itself the output of an earlier
// conversion still in force — then the record's pre-conversion baseline belongs to that
// first original and must stay.
func (h *historyStore) convertedBefore(ctx context.Context, e HistoryEntry) bool {
	var n int
	_ = h.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM convert_history WHERE item_key = ? AND id < ? AND outcome = ? AND out_path = ? AND hold_state <> ?`,
		e.Key, e.ID, OutcomeDone, e.SrcPath, HoldReverted).Scan(&n)
	return n > 0
}

// markReverted records that a row's original was put back.
func (h *historyStore) markReverted(ctx context.Context, id int64) error {
	_, err := h.db.ExecContext(ctx,
		`UPDATE convert_history SET hold_state = ?, reverted_at = ? WHERE id = ?`, HoldReverted, time.Now().Unix(), id)
	return err
}

// recoverRevert finishes or rolls back a revert a restart interrupted: with the original
// back and the converted file moved away it's finished; otherwise the files go back to how
// the conversion left them.
func (s *Service) recoverRevert(ctx context.Context, w swapRow) {
	e, err := s.history.get(ctx, w.historyID)
	if err != nil {
		s.log.Warn("convert: an interrupted revert has no ledger row — left for a look", "file", w.final, "err", err)
		return
	}
	out, orig, held := w.src, w.final, w.holdPath
	origBack := exists(orig) && !exists(held)
	if origBack && (out == orig || !exists(out)) {
		if err := s.finishRevert(ctx, e, w); err != nil {
			s.log.Warn("convert: could not finish an interrupted revert — will try again at the next startup", "file", orig, "err", err)
			return
		}
		s.log.Info("convert: finished an interrupted revert", "file", orig)
		return
	}
	if !s.undoRevertMoves(w) {
		s.log.Warn("convert: could not roll back an interrupted revert — will try again at the next startup", "file", orig)
		return
	}
	s.clearSwap(w.part)
	s.log.Info("convert: rolled back an interrupted revert — the converted file is still in place", "file", out)
}

// claimItem keeps the runner (and requests) off an item while something other than a
// conversion works on its files. It refuses while the item is being converted; release
// ends the claim.
func (s *Service) claimItem(key string) (release func(), ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.pending[key]; busy {
		return nil, false
	}
	sentinel := &Job{Key: key, State: StateReplacing}
	s.pending[key] = sentinel
	return func() { s.releasePending(key, sentinel) }, true
}

// WithRevert marks the files whose latest conversion can still be reverted (its original is
// in the hold) with that ledger row's id, for the Library's Revert button.
func (s *Service) WithRevert(ctx context.Context, list []Candidate) []Candidate {
	if s.history == nil || s.history.db == nil || len(list) == 0 {
		return list
	}
	rows, err := s.history.db.QueryContext(ctx,
		`SELECT item_key, MAX(id) FROM convert_history WHERE hold_state = ? AND outcome = ? GROUP BY item_key`, HoldHeld, OutcomeDone)
	if err != nil {
		return list
	}
	defer rows.Close()
	held := map[string]int64{}
	for rows.Next() {
		var key string
		var id int64
		if rows.Scan(&key, &id) == nil {
			held[key] = id
		}
	}
	for i := range list {
		list[i].RevertHistoryID = held[list[i].Key]
	}
	return list
}
