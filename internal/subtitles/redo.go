package subtitles

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tristenlammi/arrmada/internal/langs"
	"github.com/tristenlammi/arrmada/internal/library"
)

// Redo: make one language's subtitle again, because the one there is wrong — out of sync,
// the AI heard the dub, a forced track got extracted. It replaces only that language,
// never hands back the file it is replacing (a downloaded one is blocklisted for this
// video first), and the old file goes to the recycle bin only once the new one exists:
// if nothing better turns up, the old one is put back.

// Redo modes (Job.Mode).
const (
	ModeRedoNext    = "next"    // the next-best source: another download, another track, else AI
	ModeRedoAI      = "ai"      // whisper, whatever else exists (large-v3 when installed)
	ModeRedoExtract = "extract" // an embedded track (TrackIndex, or the best full one)
)

func isRedoMode(m string) bool {
	return m == ModeRedoNext || m == ModeRedoAI || m == ModeRedoExtract
}

// redoWords is how a redo reads in the Queue and the note: "next download", "AI".
func redoWords(mode string) string {
	switch mode {
	case ModeRedoAI:
		return "AI"
	case ModeRedoExtract:
		return "embedded track"
	}
	return "next download"
}

// Errors from QueueRedo, for the handler.
var (
	ErrBadRedoMode = errors.New(`mode must be "next", "ai" or "extract"`)
	ErrBadRedoLang = errors.New("unknown language")
	ErrRedoForced  = errors.New("a forced subtitle can't be redone yet")
)

// QueueRedo queues a redo of one language of one file at manual priority. audioStream
// (mode ai) and trackIndex (mode extract) are optional overrides, checked when the job runs
// against the file's probe.
func (s *Service) QueueRedo(ctx context.Context, m MediaRef, lang, variant, mode string, audioStream, trackIndex *int) (*Job, error) {
	if !isRedoMode(mode) {
		return nil, ErrBadRedoMode
	}
	l, ok := langs.Lookup(lang)
	if !ok {
		return nil, ErrBadRedoLang
	}
	if normVariant(variant) == VariantForced {
		return nil, ErrRedoForced
	}
	if (audioStream != nil && *audioStream < 0) || (trackIndex != nil && *trackIndex < 0) {
		return nil, fmt.Errorf("track numbers can't be negative")
	}
	job := m.job()
	switch m.Kind {
	case "movie":
		mv, err := s.movies.Get(ctx, m.MovieID)
		if err != nil || !mv.HasFile || mv.MovieFilePath == "" {
			return nil, ErrNoVideo
		}
		job.Title = mv.Title
	case "episode":
		path, _ := s.series.EpisodeFilePath(ctx, m.SeriesID, m.Season, m.Episode)
		if path == "" {
			return nil, ErrNoVideo
		}
		job.Title = fmt.Sprintf("S%02dE%02d", m.Season, m.Episode)
		if sm, err := s.series.Get(ctx, m.SeriesID); err == nil {
			job.Title = fmt.Sprintf("%s - S%02dE%02d", sm.Title, m.Season, m.Episode)
		}
	default:
		return nil, ErrNoVideo
	}
	job.Mode, job.Langs, job.Priority = mode, []string{l.Code}, PrioManual
	job.AudioStream, job.TrackIndex = audioStream, trackIndex
	return s.enqueue(job), nil
}

// redoStep is which rungs a redo of one language walks.
type redoStep struct {
	extract, download, ai bool
	mi                    *mediaInfo // the tracks extraction may take (nil = the probe's)
}

// processRedo is process() for a redo job. The legacy whole-file redo (Job.Redo, no mode)
// is a "next" redo of every kept language.
func (s *Service) processRedo(ctx context.Context, job *Job, ref fileRef) {
	mode := job.Mode
	if mode == "" {
		mode = ModeRedoNext
	}
	kept := s.languages(ctx)
	targets := job.Langs
	if len(targets) == 0 {
		targets = kept
	}
	mi, _ := s.probeFile(ctx, ref.Path)
	if mi != nil {
		ref.FPS = mi.FPS
	}
	ref.audio = s.newJobAudio(ref.Path, mi)
	defer ref.audio.close()
	if mode == ModeRedoAI {
		ref.preferLarge = true
		if job.AudioStream != nil {
			ref.aiStream = job.AudioStream
		}
	}
	s.event("info", fmt.Sprintf("Redoing %s (%s) with the %s", ref.Title, strings.Join(targets, ", "), redoWords(mode)))

	runID := NewRunID()
	s.update(job, func(j *Job) { j.RunID = runID })
	var outcomes []Outcome
	var notes []string
	made := 0
	for _, l := range targets {
		if ctx.Err() != nil {
			break
		}
		outs, note, ok := s.redoLang(ctx, job, ref, mi, l, mode, len(kept) > 0 && langs.Canonical(l) == kept[0])
		outcomes = append(outcomes, outs...)
		if note != "" {
			notes = append(notes, strings.ToLower(l)+": "+note)
		}
		if ok {
			made++
		}
	}
	s.recordAttempts(ctx, runID, ref, outcomes)
	summary := outcomeNote(targets, outcomes)
	if len(notes) > 0 {
		summary = strings.TrimPrefix(summary+" · "+strings.Join(notes, " · "), " · ")
	}
	summary = "redo " + strings.Join(targets, ", ") + " (" + redoWords(mode) + ") — " + summary
	if made > 0 {
		s.sidecarChanged(job.Kind, ref.Path, "", SidecarWritten)
		s.refreshSnapshot(context.WithoutCancel(ctx), job)
	}
	state := jobStateFor(outcomes)
	switch {
	case ctx.Err() != nil:
		state = StateCancelled
	case made == 0 && state == StateDone:
		state = StateSkipped // every new subtitle was put back: nothing changed
	}
	s.event("info", fmt.Sprintf("%s %s — %s", ifElse(made > 0, "✓", "•"), ref.Title, summary))
	s.finishOutcomes(job, state, summary, outcomes)
}

// redoLang redoes one language. The sidecars there now go to the recycle bin before the
// new one is made — a bin that won't take them stops the redo with the old file kept —
// and come back from the bin when nothing could be made. Returns the rungs' outcomes, a
// note for what happened to the old file, and whether a new subtitle is in place.
func (s *Service) redoLang(ctx context.Context, job *Job, ref fileRef, mi *mediaInfo, lang, mode string, untagged bool) ([]Outcome, string, bool) {
	olds := pairedSidecars(ref.Path, lang, untagged)
	cctx := context.WithoutCancel(ctx)
	// What the newest current one is, for which source to try next.
	var cur SubtitleFile
	owned := false
	for _, p := range olds {
		if f, ok, err := s.ledger.LiveFile(cctx, p); err == nil && ok && (!owned || f.CreatedAt >= cur.CreatedAt) {
			cur, owned = f, true
		}
	}
	step := redoStep{extract: true, download: true, ai: true}
	block := "" // the download to blocklist once the old file is safely in the bin
	switch mode {
	case ModeRedoAI:
		step = redoStep{ai: true}
	case ModeRedoExtract:
		step = redoStep{extract: true, mi: chosenTrack(mi, lang, job.TrackIndex)}
		if step.mi == nil {
			return []Outcome{mkOutcome(lang, RungExtract, ResultExtractFailed, "no such text track to extract")}, "kept the old subtitle", false
		}
	default:
		switch {
		case owned && cur.Source == SourceOpenSubtitles && cur.ProviderFileID != "":
			block = cur.ProviderFileID
			step.extract = false
		case owned && cur.Source == SourceEmbedded && cur.EmbeddedIndex != nil && mi != nil:
			step.mi = withoutTrack(mi, *cur.EmbeddedIndex) // another track, if there is one
		}
	}

	// The old files go to the bin first, so the new one can take the canonical name.
	type binned struct{ orig, at string }
	var gone []binned
	putBack := func() string {
		lost := 0
		for _, b := range gone {
			if b.at == "" || fileExists(b.orig) || os.Rename(b.at, b.orig) != nil {
				lost++
				continue
			}
			_ = os.Remove(b.at + library.RecycleMetaExt)
			if f, ok := s.oldRow(cctx, b.orig, cur); ok {
				s.recordFile(cctx, job, ref, f)
			}
		}
		if lost > 0 {
			return "nothing better found — the old subtitle is in the recycle bin"
		}
		return "nothing better found — kept the old subtitle"
	}
	for _, p := range olds {
		at, err := s.retireSidecarTo(cctx, p)
		if err != nil {
			putBack()
			return nil, "kept the old subtitle — " + shortErr(err, 140), false
		}
		gone = append(gone, binned{p, at})
	}
	if block != "" {
		// Never the same download again for this video.
		if err := s.ledger.Block(cctx, BlockEntry{Provider: providerOpenSubtitles, FileID: block,
			MediaKey: ref.MediaKey, Lang: lang, Reason: BlockRedo}); err != nil {
			putBack()
			return nil, "kept the old subtitle — couldn't blocklist it: " + shortErr(err, 100), false
		}
	}

	var outs []Outcome
	rem := []string{lang}
	var o []Outcome
	if step.extract && ctx.Err() == nil {
		x := mi
		if step.mi != nil {
			x = step.mi
		}
		rem, o = s.rungExtract(ctx, job, ref, x, rem)
		outs = append(outs, o...)
	}
	if step.download && ctx.Err() == nil {
		rem, o = s.rungDownload(ctx, job, ref, rem)
		outs = append(outs, o...)
	}
	if step.ai && len(rem) > 0 && ctx.Err() == nil {
		outs = append(outs, s.rungAI(ctx, job, ref, mi, rem)...)
	}
	if len(rem) == 0 || madeIt(outs) {
		if len(olds) > 0 {
			s.sidecarChanged(job.Kind, ref.Path, olds[0], SidecarRemoved)
			return outs, "the old subtitle is in the recycle bin", true
		}
		return outs, "", true
	}
	if len(olds) == 0 {
		return outs, "", false
	}
	return outs, putBack(), false
}

// oldRow is the provenance a put-back sidecar had before the redo: its own superseded
// row when it was the one the redo looked at, else an unknown-source row.
func (s *Service) oldRow(_ context.Context, path string, cur SubtitleFile) (SubtitleFile, bool) {
	if cur.Path == path {
		f := cur
		f.ID, f.SupersededAt, f.CreatedAt = 0, 0, 0
		return f, true
	}
	return SubtitleFile{}, false
}

// pairedSidecars lists the full (or SDH) sidecars of a video in one language, under any
// spelling — "<base>.en.srt", "<base>.eng.srt", "<base>.en.sdh.srt", and the untagged
// "<base>.srt" when untagged (the language is the first kept one).
func pairedSidecars(video, lang string, untagged bool) []string {
	sc := scanSidecars(video, []string{lang}, "episode")
	var out []string
	for i, lv := range sc.Variants {
		if i >= len(sc.Names) || !coversFull(lv.Variant) {
			continue
		}
		if (lv.Lang == "" && untagged) || (lv.Lang != "" && langMatches(lv.Lang, lang)) {
			out = append(out, filepath.Join(filepath.Dir(video), sc.Names[i]))
		}
	}
	return out
}

// withoutTrack is the probe minus one embedded subtitle track, so extraction picks
// another one in the same language (or none).
func withoutTrack(mi *mediaInfo, index int) *mediaInfo {
	cp := *mi
	cp.Subs = nil
	for _, t := range mi.Subs {
		if t.Index != index {
			cp.Subs = append(cp.Subs, t)
		}
	}
	return &cp
}

// chosenTrack is the probe with only the track a re-extract should take, labelled as the
// language's full subtitle: the owner's pick (any text track — tags are often wrong), or
// the best full track in the language. nil when there is no such text track.
func chosenTrack(mi *mediaInfo, lang string, index *int) *mediaInfo {
	if mi == nil {
		return nil
	}
	var t SubTrack
	found := false
	if index != nil {
		for _, s := range mi.Subs {
			if s.Index == *index && s.Text {
				t, found = s, true
			}
		}
	} else {
		t, found = pickFullTrack(mi.Subs, lang)
	}
	if !found {
		return nil
	}
	t.Lang, t.Forced = lang, false
	cp := *mi
	cp.Subs = []SubTrack{t}
	return &cp
}
