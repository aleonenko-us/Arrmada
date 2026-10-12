package subtitles

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/tristenlammi/arrmada/internal/library"
)

// The one-shot forced-track repair.
//
// Before forced tracks were told apart (SUB-03), extraction wrote the FIRST text track in a
// language as "<base>.<lang>.srt", and on plenty of releases that first track is the
// forced one: a few dozen lines of signs and foreign dialogue. Those files have looked
// covered ever since while Plex showed next to nothing. This pass finds them once, after
// the first library pass following the upgrade, and puts the full track in its place.
const (
	// ModeRepairForced marks a repair job (Job.Mode).
	ModeRepairForced = "repair-forced"
	// keyForcedRepairDone is set once the pass has run to the end, so it never runs again.
	keyForcedRepairDone = "subs_forced_repair_done"
	// A full subtitle for a feature or an episode has hundreds of cues; a forced one a few
	// dozen. Only sidecars under this are looked at any closer.
	repairSuspectCues = 80
	// A sidecar bigger than this is certainly a full subtitle; it isn't even read.
	repairMaxRead = 2 << 20
)

// forcedRepair is the pass's state in this process.
type forcedRepair struct {
	mu      sync.Mutex
	started bool          // a pass was started here (the settings key stops later starts)
	wake    chan struct{} // nudged when a repair job leaves the queue
	fixed   int           // languages whose full track was put in place
}

// startForcedRepair runs the repair pass in the background unless it has already run to
// the end (the settings key) or is running now. Called after each completed library pass;
// only the first does anything.
func (s *Service) startForcedRepair(ctx context.Context) {
	if s.settings == nil || s.settings.GetBool(ctx, keyForcedRepairDone, false) {
		return
	}
	s.repair.mu.Lock()
	if s.repair.started {
		s.repair.mu.Unlock()
		return
	}
	s.repair.started = true
	s.repair.mu.Unlock()
	go s.forcedRepairPass(ctx)
}

// repairFile is one video the pass looks at, with the job that would repair it.
type repairFile struct {
	job  Job
	path string
}

// forcedRepairPass finds the mis-extracted sidecars, queues a repair job for each file
// (sweep priority, so imports and button presses go first, and visible and cancellable in
// the Queue tab), waits for those jobs to finish, then logs the count and sets the
// settings key. Finding suspects is cheap: a sidecar's cues are counted from the file
// itself, and only the few short ones get their video probed. If the app stops part-way
// the key stays unset and the next start does it again; files already repaired are no
// longer suspects then.
func (s *Service) forcedRepairPass(ctx context.Context) {
	wake := make(chan struct{}, 1)
	s.repair.mu.Lock()
	s.repair.wake = wake
	s.repair.mu.Unlock()
	defer func() {
		s.repair.mu.Lock()
		s.repair.wake = nil
		s.repair.started = false
		s.repair.mu.Unlock()
	}()

	langs := s.languages(ctx)
	suspects := 0
	var queued []*Job
	for _, f := range s.repairFiles(ctx) {
		if ctx.Err() != nil {
			return
		}
		var sus []string
		for _, l := range langs {
			if n, ok := sidecarCues(sidecarPath(f.path, strings.ToLower(l))); ok && suspectCues(n) {
				sus = append(sus, l)
			}
		}
		if len(sus) == 0 {
			continue
		}
		suspects += len(sus)
		mi, err := s.probeFile(ctx, f.path)
		if err != nil || mi == nil {
			s.log.Debug("subtitles: forced-repair couldn't probe a suspect", "path", f.path, "err", err)
			continue
		}
		var confirmed []string
		for _, l := range sus {
			if forcedFirst(mi.Subs, l) {
				confirmed = append(confirmed, l)
			}
		}
		if len(confirmed) == 0 {
			continue
		}
		j := f.job
		j.Mode, j.Priority, j.repairLangs = ModeRepairForced, PrioSweep, confirmed
		if got := s.enqueue(&j); got == &j {
			queued = append(queued, got)
		}
	}
	for !s.jobsSettled(queued) {
		select {
		case <-ctx.Done():
			return
		case <-wake:
		}
	}
	s.repair.mu.Lock()
	fixed := s.repair.fixed
	s.repair.mu.Unlock()
	s.event("info", fmt.Sprintf("forced-repair: fixed %d of %d suspects", fixed, suspects))
	if err := s.settings.SetBool(ctx, keyForcedRepairDone, true); err != nil {
		s.log.Warn("subtitles: couldn't record that the forced-repair pass ran; it will run again next start", "err", err)
	}
}

// repairNudge tells a waiting repair pass that one of its jobs left the queue. Called with
// s.mu held (from retireLocked); it never blocks.
func (s *Service) repairNudge() {
	s.repair.mu.Lock()
	ch := s.repair.wake
	s.repair.mu.Unlock()
	if ch != nil {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// jobsSettled reports whether every one of these jobs has finished, one way or another.
func (s *Service) jobsSettled(jobs []*Job) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range jobs {
		if j.State == StateQueued || j.State == StateRunning {
			return false
		}
	}
	return true
}

// repairFiles lists every movie and episode file on disk, each with the job that would
// repair it.
func (s *Service) repairFiles(ctx context.Context) []repairFile {
	var out []repairFile
	if s.movies != nil {
		if list, err := s.movies.List(ctx); err == nil {
			for _, m := range list {
				if m.HasFile && m.MovieFilePath != "" {
					out = append(out, repairFile{Job{Kind: "movie", MovieID: m.ID, Title: m.Title}, m.MovieFilePath})
				}
			}
		}
	}
	if s.series != nil {
		if list, err := s.series.List(ctx); err == nil {
			for _, sm := range list {
				if ctx.Err() != nil {
					return out
				}
				full, err := s.series.Get(ctx, sm.ID)
				if err != nil {
					continue
				}
				for _, sn := range full.Seasons {
					for _, e := range sn.Episodes {
						if !e.HasFile || e.FilePath == "" {
							continue
						}
						out = append(out, repairFile{Job{Kind: "episode", SeriesID: full.ID, Season: e.SeasonNumber, Episode: e.EpisodeNumber,
							Title: fmt.Sprintf("%s - S%02dE%02d", full.Title, e.SeasonNumber, e.EpisodeNumber)}, e.FilePath})
					}
				}
			}
		}
	}
	return out
}

// suspectCues reports whether a sidecar with n cues could be a forced track's: a few, but
// some. ffmpeg writes UTF-8 SRT, so a sidecar with no cues parseSRT can read (UTF-16, ASS
// renamed to .srt) wasn't extracted by Arrmada and is left alone.
func suspectCues(n int) bool { return n > 0 && n < repairSuspectCues }

// sidecarCues counts a sidecar's cues; ok is false when there's no such file. A file too
// big to be a forced subtitle isn't read: it reports the suspect threshold.
func sidecarCues(path string) (int, bool) {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return 0, false
	}
	if fi.Size() > repairMaxRead {
		return repairSuspectCues, true
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	return len(parseSRT(string(b))), true
}

// forcedFirst reports the old bug's fingerprint for a language: the first text track in it
// is forced, so that's what the old extraction took, and a full track exists to take
// instead.
func forcedFirst(subs []SubTrack, lang string) bool {
	for _, t := range subs {
		if !t.Text || !langMatches(t.Lang, lang) {
			continue
		}
		if !t.Forced {
			return false
		}
		_, ok := pickFullTrack(subs, lang)
		return ok
	}
	return false
}

// processRepair is process() for a ModeRepairForced job: for each suspect language,
// extract the full track beside the short sidecar and swap them. The video is never
// touched.
func (s *Service) processRepair(ctx context.Context, job *Job, ref fileRef) {
	mi, err := s.probeFile(ctx, ref.Path)
	if err != nil || mi == nil {
		s.finish(job, StateFailed, "couldn't read the file's subtitle tracks")
		return
	}
	s.update(job, func(j *Job) { j.Stage = "repairing a subtitle taken from a forced track" })
	var notes []string
	var outs []Outcome
	fixed := 0
	runID := NewRunID()
	s.update(job, func(j *Job) { j.RunID = runID })
	defer func() { s.recordAttempts(ctx, runID, ref, outs) }()
	for _, l := range job.repairLangs {
		if ctx.Err() != nil {
			break
		}
		ok, note, err := s.repairLang(ctx, ref, mi, l)
		if err != nil {
			s.event("warn", fmt.Sprintf("%s: forced-repair %s failed: %v", ref.Title, l, err))
			note = "failed: " + shortErr(err, 120)
			outs = append(outs, Outcome{Lang: l, Rung: RungRepair, Result: ResultExtractFailed, Detail: note})
		}
		if ok {
			fixed++
			outs = append(outs, Outcome{Lang: l, Rung: RungRepair, Result: ResultOK, Detail: note})
			s.recordRepaired(ctx, job, ref, mi, l)
		}
		notes = append(notes, strings.ToLower(l)+": "+note)
	}
	s.repair.mu.Lock()
	s.repair.fixed += fixed
	s.repair.mu.Unlock()
	note := strings.Join(notes, " · ")
	if fixed > 0 {
		s.sidecarChanged(job.Kind, ref.Path, "", SidecarWritten)
		s.refreshSnapshot(context.WithoutCancel(ctx), job)
	}
	switch {
	case ctx.Err() != nil:
		s.finish(job, StateCancelled, "stopped")
	case fixed > 0:
		s.event("info", fmt.Sprintf("✓ %s — %s", ref.Title, note))
		s.finish(job, StateDone, note)
	default:
		s.finish(job, StateSkipped, note)
	}
}

// repairLang swaps one language's short sidecar for the full track. The full track goes
// to a temp file first; only when the existing sidecar has under a third of its cues is
// anything moved. The short one is kept as "<base>.<lang>.forced.srt" (it IS the forced
// subtitle), or goes to the recycle bin when that name is taken. Returns whether the
// swap happened and a word for the job's note.
func (s *Service) repairLang(ctx context.Context, ref fileRef, mi *mediaInfo, lang string) (bool, string, error) {
	cur := sidecarPath(ref.Path, strings.ToLower(lang))
	oldCues, ok := sidecarCues(cur)
	switch {
	case !ok:
		return false, "the subtitle is gone", nil
	case !suspectCues(oldCues):
		return false, "already a full subtitle", nil
	case !forcedFirst(mi.Subs, lang):
		// The file changed since the pass looked (an upgrade): this sidecar wasn't taken
		// from a forced track of it.
		return false, "not taken from a forced track", nil
	}
	t, ok := pickFullTrack(mi.Subs, lang)
	if !ok {
		return false, "no full track to extract", nil
	}
	// The temp name doesn't end in a subtitle extension, so nothing (scanSidecars, Plex)
	// takes it for a sidecar while it's being written.
	tmp := cur + library.PartialSuffix
	defer func() { _ = os.Remove(tmp) }()
	if err := s.runExtract(ctx, ref.Path, []extractPick{{Index: t.Index, Lang: lang, Variant: VariantFull, Path: tmp}}); err != nil {
		if ctx.Err() != nil {
			return false, "stopped", nil
		}
		return false, "", fmt.Errorf("extract: %w", err)
	}
	newCues, ok := sidecarCues(tmp)
	if !ok || newCues == 0 {
		return false, "the full track came out empty", nil
	}
	if oldCues*3 >= newCues {
		return false, "kept — not much shorter than the full track", nil
	}
	forced := sidecarPathV(ref.Path, strings.ToLower(lang), VariantForced)
	keptAsForced := false
	if _, err := os.Stat(forced); err == nil {
		if err := s.retireSidecar(ctx, cur); err != nil {
			return false, "", err // nothing moved: the short sidecar stays
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(cur, forced); err != nil {
			return false, "", err
		}
		keptAsForced = true
	} else {
		return false, "", err
	}
	if err := os.Rename(tmp, cur); err != nil {
		if keptAsForced {
			_ = os.Rename(forced, cur) // put the short one back rather than leave a gap
		}
		return false, "", err
	}
	note := fmt.Sprintf("full track restored (%d cues, was %d)", newCues, oldCues)
	if keptAsForced {
		note += "; the short one kept as .forced"
	} else {
		note += "; the short one went to the recycle bin"
	}
	return true, note, nil
}

// recordRepaired writes the ledger rows for a repaired language: the full track now at
// "<base>.<lang>.srt", and the short one kept as ".forced.srt" when it was kept.
func (s *Service) recordRepaired(ctx context.Context, job *Job, ref fileRef, mi *mediaInfo, lang string) {
	if t, ok := pickFullTrack(mi.Subs, lang); ok {
		idx := t.Index
		s.recordFile(ctx, job, ref, SubtitleFile{Path: sidecarPath(ref.Path, strings.ToLower(lang)), Lang: lang,
			Source: SourceEmbedded, EmbeddedIndex: &idx})
	}
	forced := sidecarPathV(ref.Path, strings.ToLower(lang), VariantForced)
	if t, ok := pickForcedTrack(mi.Subs, lang); ok {
		if live, found, _ := s.ledger.LiveFile(ctx, forced); !found || live.Source != SourceEmbedded {
			idx := t.Index
			s.recordFile(ctx, job, ref, SubtitleFile{Path: forced, Lang: lang, Variant: VariantForced,
				Source: SourceEmbedded, EmbeddedIndex: &idx})
		}
	}
}
