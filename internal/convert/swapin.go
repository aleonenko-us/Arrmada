package convert

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// swapIn replaces a library file with its verified conversion at dst. Every step is
// journalled before it's taken (swaps.go), and the original is never deleted here: it
// moves to the originals hold (hold.go), on the same drive, where it stays for the undo
// window. In order:
//
//  1. the ledger row's id names the original's folder in the hold;
//  2. journal (staging) → copy the encode next to the original as .arrpart → staged;
//  3. journal the hold path (holding) → move the original into the hold;
//  4. a stray file already at the .mkv name (no record owns it) joins it there;
//  5. journal held, record the hold in the ledger → rename the .arrpart into place;
//  6. repoint the library record, clear the journal.
//
// Any failure before the rename puts the original back and discards the copy, so in no
// failure — or crash, see recoverSwaps — are the original and the conversion both lost.
func (s *Service) swapIn(ctx context.Context, job *Job, src, dst string, mi *MediaInfo, plan Plan, outSize int64) {
	rec := s.record(job)
	s.update(job, func(j *Job) { j.State = StateReplacing })
	// The encode may have taken hours: make sure the library file is still the one we
	// converted. An import or upgrade landing mid-encode must not be undone.
	if cur, _, _, ok := s.resolveSource(ctx, job); !ok || cur != src {
		s.finish(job, StateSkipped, "the library file changed during the conversion — left the new file alone")
		return
	}
	if fi, err := os.Stat(src); err != nil || fi.Size() != mi.SizeBytes {
		s.finish(job, StateSkipped, "the source file changed during the conversion — left it alone")
		return
	}
	rec.reclaimDeferred = fileLinks(src) > 1
	finalPath, part := finalPathFor(src), partPathFor(src)
	if s.swapJournalled(ctx, part) {
		// An earlier swap of this file is still unreconciled; its part may be the only copy
		// of something. Startup recovery deals with it — not a new job.
		s.finishSkip(job, SkipTransient, "an earlier swap of this file hasn't been reconciled yet — the encode was discarded and the original kept")
		return
	}
	_ = os.Remove(part) // a leftover from a crash before swaps were journalled
	// The .mkv name may already be taken. Another version of this film (versions.go) or a file
	// another library record points at is that track's or record's, so it's never moved
	// aside; this one waits instead (and the rename below would otherwise overwrite it).
	if finalPath != src && s.otherTrackOwns(ctx, job, finalPath) {
		s.finishSkip(job, SkipTransient, otherTrackNote(finalPath))
		return
	}
	if finalPath != src && exists(finalPath) {
		if why := s.nameOwner(ctx, finalPath); why != "" {
			s.finishSkip(job, SkipNameTaken, why+" — the encode was discarded and the original kept")
			return
		}
	}
	id := s.ensureHistory(job)
	if id == 0 {
		s.finish(job, StateFailed, "could not record the conversion in the ledger — kept the original")
		return
	}
	held, err := s.holdPathFor(src, id)
	if err != nil {
		s.finishSkip(job, SkipNoHold, "there's nowhere to keep the original ("+err.Error()+") — the encode was discarded and the original kept")
		return
	}
	holdDir := filepath.Dir(held)
	strayHeld := filepath.Join(holdDir, filepath.Base(finalPath)) // MP4 sources only

	// Journal first, then stage: a crash mid-copy leaves a row that names the part, so the
	// next startup removes it instead of leaving a multi-GB orphan beside the original.
	if err := s.recordSwap(ctx, job, part, finalPath, src, swapStaging, id); err != nil {
		_ = os.Remove(holdDir)
		s.finish(job, StateFailed, "could not journal the swap ("+err.Error()+") — kept the original")
		return
	}
	// discard abandons the swap while the original is still in place.
	discard := func() {
		_ = os.Remove(part)
		s.clearSwap(part)
		_ = os.Remove(holdDir) // only if empty
	}
	if err := moveFileFn(dst, part); err != nil {
		discard()
		if errors.Is(err, syscall.ENOSPC) {
			// The library disk filled up. Retrying straight away would re-run the whole encode
			// only to fail the same way, so wait for space to be freed.
			s.finishSkip(job, SkipLibraryFull, fmt.Sprintf("the library disk had %s free; staging needed %s — the encode was discarded and the original kept",
				humanBytes(int64(freeBytesFn(filepath.Dir(part)))), humanBytes(outSize)))
			return
		}
		s.finish(job, StateFailed, "could not stage the converted file: "+err.Error()+" — kept the original")
		return
	}
	if err := s.setSwapState(ctx, part, swapStaged); err != nil {
		discard()
		s.finish(job, StateFailed, "could not journal the swap ("+err.Error()+") — kept the original")
		return
	}
	if err := s.setSwapHolding(ctx, part, held); err != nil {
		discard()
		s.finish(job, StateFailed, "could not journal the swap ("+err.Error()+") — kept the original")
		return
	}
	if err := makeHoldDir(held); err != nil {
		discard()
		s.finish(job, StateFailed, "could not make the original's folder in the originals hold: "+err.Error()+" — kept the original")
		return
	}
	if err := s.moveKept(src, held); err != nil {
		discard()
		s.finish(job, StateFailed, "could not move the original into the originals hold: "+err.Error()+" — kept the original")
		return
	}

	// From here the original is in the hold. Until the rename, any failure puts it back.
	putBack := func(why string) {
		restored := true
		if finalPath != src && exists(strayHeld) {
			if err := s.moveKept(strayHeld, finalPath); err != nil {
				restored = false
			}
		}
		if err := s.moveKept(held, src); err != nil {
			restored = false
			s.log.Error("convert: couldn't put the original back from the hold — the next startup reconciles it",
				"held", held, "src", src, "err", err)
		}
		if !restored {
			// The journal still describes where everything is; recovery finishes the job.
			s.finish(job, StateFailed, why+" — the original is safe in the hold; it will be reconciled at the next startup")
			return
		}
		_ = s.history.setHold(context.Background(), id, "", 0, "")
		discard()
		s.finish(job, StateFailed, why+" — kept the original")
	}
	if finalPath != src && exists(finalPath) {
		// A stray at the .mkv name (no record owns it, checked above) waits in the hold with
		// the original rather than being overwritten — unless another track of this film
		// took that name meanwhile: then nothing of that track's is touched.
		if s.otherTrackOwns(ctx, job, finalPath) {
			putBack(otherTrackNote(finalPath))
			return
		}
		if err := s.moveKept(finalPath, strayHeld); err != nil {
			putBack("could not move the file already at " + filepath.Base(finalPath) + " out of the way: " + err.Error())
			return
		}
	}
	if err := s.setSwapState(ctx, part, swapHeld); err != nil {
		putBack("could not journal the swap (" + err.Error() + ")")
		return
	}
	days := s.holdDays(ctx)
	until := time.Now().AddDate(0, 0, days).Unix()
	if err := s.history.setHold(ctx, id, held, until, HoldHeld); err != nil {
		putBack("could not record the held original in the ledger (" + err.Error() + ")")
		return
	}
	if exists(finalPath) {
		putBack("something else appeared at " + filepath.Base(finalPath) + " during the swap")
		return
	}
	if err := os.Rename(part, finalPath); err != nil {
		putBack("the converted file could not be renamed into place (" + err.Error() + ")")
		return
	}
	rec.outPath = finalPath
	if err := s.markConverted(ctx, job, src, finalPath, plan.VideoCodec, mi.SizeBytes); err != nil {
		s.log.Error("convert: library record update failed after the swap", "title", job.Title, "err", err)
		s.finish(job, StateFailed, "converted, but the library record could not be updated: "+err.Error()+
			" — it will be reconciled at the next startup (the original is safe in the hold)")
		return
	}
	s.clearSwap(part)
	s.swapped(job, finalPath)
	s.measured.forget(ctx, src)
	s.reindexConverted(ctx, job)
	s.update(job, func(j *Job) { j.OutBytes = outSize })
	if rec.reclaimDeferred {
		s.event("info", job.Title+": the download client still holds a hardlinked copy of the original, so releasing it won't free that space")
	}
	notes := []string{}
	if t := trackSummary(mi, plan); t != "" {
		notes = append(notes, t)
	}
	for _, w := range planWarnings(mi, plan) {
		s.event("warn", job.Title+": "+w)
		notes = append(notes, w)
	}
	if days > 0 {
		s.event("info", fmt.Sprintf("%s: the original is kept until %s, so this can be undone from History",
			job.Title, time.Unix(until, 0).Format("2 Jan")))
	}
	s.finish(job, StateDone, strings.Join(notes, " · "))
	s.log.Info("convert: done", "title", job.Title, "src_mb", mi.SizeBytes>>20, "out_mb", outSize>>20)
	if days == 0 {
		// No undo was chosen: the original goes now the conversion is in place and recorded.
		if _, err := s.releaseHold(ctx, id, "you chose not to keep originals"); err != nil {
			s.log.Warn("convert: couldn't delete the original after the conversion — it stays in the hold", "title", job.Title, "err", err)
		}
	}
}

// ensureHistory returns the job's ledger row, writing it now if the encode's start couldn't
// (0 when the ledger can't be written at all — the swap then doesn't happen: the hold is
// named by this id).
func (s *Service) ensureHistory(job *Job) int64 {
	rec := s.record(job)
	if rec.historyID == 0 {
		rec.historyID = s.history.begin(context.Background(), s.historyRowFor(job, OutcomeInProgress, ""))
	}
	return rec.historyID
}

// holdRoom checks, before any heavy work, that this file's original will have somewhere to
// go: a library folder with a hold on its drive, and room under the hold's budget. A full
// hold pauses the file until the next release — nothing is deleted to make room. The
// budget is checked here, not again at the swap: an original that arrives over it after a
// long encode still goes in (it takes no new space — it was already on the disk).
func (s *Service) holdRoom(ctx context.Context, job *Job, src string, mi *MediaInfo) bool {
	if _, why := s.holdRootFor(src); why != "" {
		s.finishSkip(job, SkipNoHold, why)
		return false
	}
	if s.holdDays(ctx) == 0 {
		return true // nothing is kept, so there's no budget to keep within
	}
	if ok, why, retryAt := s.holdFits(ctx, mi.SizeBytes); !ok {
		s.finishSkipUntil(job, SkipHoldFull, why, retryAt)
		return false
	}
	return true
}
