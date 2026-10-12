package subtitles

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	ModeRedoPick    = "pick"    // one OpenSubtitles file the owner chose (PickFileID)
)

func isRedoMode(m string) bool {
	return m == ModeRedoNext || m == ModeRedoAI || m == ModeRedoExtract || m == ModeRedoPick
}

// redoWords is how a redo reads in the Queue and the note: "next download", "AI".
func redoWords(mode string) string {
	switch mode {
	case ModeRedoAI:
		return "AI"
	case ModeRedoExtract:
		return "embedded track"
	case ModeRedoPick:
		return "chosen download"
	}
	return "next download"
}

// Errors from QueueRedo, for the handler.
var (
	ErrBadRedoMode = errors.New(`mode must be "next", "ai", "extract" or "pick"`)
	ErrBadRedoLang = errors.New("unknown language")
	ErrRedoForced  = errors.New("a forced subtitle can't be redone yet")
	ErrRedoPickID  = errors.New(`mode "pick" needs the OpenSubtitles file_id`)
	// ErrRedoBusy: a redo of this file in this mode is already waiting, for other
	// languages or with other choices; this one wasn't queued.
	ErrRedoBusy = errors.New("a redo of this file is already waiting — try again when it has run")
)

// RedoRequest is one language's redo. AudioStream (0:a:N, mode ai), TrackIndex (0:s:N,
// mode extract) and FileID (mode pick) are the owner's choices, checked when the job runs
// against the file's probe.
type RedoRequest struct {
	Lang        string
	Variant     string // "" / "full" or "sdh": the language's subtitle; "forced" is refused
	Mode        string
	AudioStream *int
	TrackIndex  *int
	FileID      string
}

// QueueRedo queues a redo of one language of one file at manual priority. A second redo
// of the same file in the same mode while the first waits joins it when it makes the same
// choices (one more language), and is refused with ErrRedoBusy when it doesn't.
func (s *Service) QueueRedo(ctx context.Context, m MediaRef, r RedoRequest) (*Job, error) {
	if !isRedoMode(r.Mode) {
		return nil, ErrBadRedoMode
	}
	l, ok := langs.Lookup(r.Lang)
	if !ok {
		return nil, ErrBadRedoLang
	}
	if normVariant(r.Variant) == VariantForced {
		return nil, ErrRedoForced
	}
	if (r.AudioStream != nil && *r.AudioStream < 0) || (r.TrackIndex != nil && *r.TrackIndex < 0) {
		return nil, fmt.Errorf("track numbers can't be negative")
	}
	r.FileID = strings.TrimSpace(r.FileID)
	if (r.Mode == ModeRedoPick) != (r.FileID != "") {
		return nil, ErrRedoPickID
	}
	job := m.job()
	var path string
	switch m.Kind {
	case "movie":
		mv, err := s.movies.Get(ctx, m.MovieID)
		if err != nil || !mv.HasFile || mv.MovieFilePath == "" {
			return nil, ErrNoVideo
		}
		job.Title, path = mv.Title, mv.MovieFilePath
	case "episode":
		path, _ = s.series.EpisodeFilePath(ctx, m.SeriesID, m.Season, m.Episode)
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
	job.Mode, job.Langs, job.Priority = r.Mode, []string{l.Code}, PrioManual
	job.AudioStream, job.TrackIndex, job.PickFileID = r.AudioStream, r.TrackIndex, r.FileID
	if r.Mode == ModeRedoAI && s.lanesOn() {
		// Whisper work waits in the GPU lane, behind nothing the fast lane does.
		job.Lane = LaneGPU
		if mi, err := s.probeFile(ctx, path); err == nil && mi != nil {
			job.AudioSec = int64(mi.DurationSec)
		}
	}
	got := s.enqueue(job)
	if got != job && !inLangs(got.Langs, l.Code) {
		return got, ErrRedoBusy
	}
	return got, nil
}

// sameRedoChoices reports whether two redo jobs choose the same tracks and file, so one
// job can do both languages.
func sameRedoChoices(a, b *Job) bool {
	eq := func(x, y *int) bool { return (x == nil) == (y == nil) && (x == nil || *x == *y) }
	return eq(a.AudioStream, b.AudioStream) && eq(a.TrackIndex, b.TrackIndex) && a.PickFileID == b.PickFileID
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
	// The title's own profile: its full languages, the first claiming an untagged sidecar.
	kept := s.titleLangs(ctx, job.Kind, job.MovieID, job.SeriesID)
	ref.wants = s.titleWants(ctx, job.Kind, job.MovieID, job.SeriesID)
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
	gpu := job.lane() == LaneGPU
	if !gpu {
		s.event("info", fmt.Sprintf("Redoing %s (%s) with the %s", ref.Title, strings.Join(targets, ", "), redoWords(mode)))
	}

	runID := job.RunID // a redo in the GPU lane carries on its fast-lane run
	if runID == "" {
		runID = NewRunID()
		s.update(job, func(j *Job) { j.RunID = runID })
	}
	prior := job.Outcomes // what the fast lane did, for a redo now in the GPU lane
	var outcomes []Outcome
	var notes []string
	var later []string // languages whose AI part waits in the GPU lane
	made := madeIt(prior)
	// The fast lane hands whisper work to the GPU lane; a job already there, or a queue
	// without lanes, does it on the spot.
	handoff := !gpu && s.lanesOn()
	for pass := 0; pass < 2; pass++ {
		langsNow := targets
		if pass == 1 {
			// The GPU lane wouldn't take them (Stop was pressed): whisper here after all.
			if len(later) == 0 || ctx.Err() != nil {
				break
			}
			langsNow, later, handoff = later, nil, false
		}
		for _, l := range langsNow {
			if ctx.Err() != nil {
				break
			}
			outs, note, ok, wait := s.redoLang(ctx, job, ref, mi, l, mode, len(kept) > 0 && langs.Canonical(l) == kept[0], handoff)
			outcomes = append(outcomes, outs...)
			if wait {
				later = append(later, l)
				continue
			}
			if note != "" {
				notes = append(notes, strings.ToLower(l)+": "+note)
			}
			if ok {
				made = true
			}
		}
		if len(later) > 0 && ctx.Err() == nil && s.toGPU(ctx, job, later, append(append([]Outcome(nil), prior...), outcomes...), audioSecOf(mi)) {
			s.recordAttempts(ctx, runID, ref, outcomes)
			if made {
				s.sidecarChanged(job.Kind, ref.Path, "", SidecarWritten)
			}
			s.event("info", fmt.Sprintf("%s: redo waiting for AI (%s)", ref.Title, strings.Join(later, ", ")))
			return
		}
	}
	s.recordAttempts(ctx, runID, ref, outcomes)
	outcomes = append(append([]Outcome(nil), prior...), outcomes...)
	summary := outcomeNote(targets, outcomes)
	if len(notes) > 0 {
		summary = strings.TrimPrefix(summary+" · "+strings.Join(notes, " · "), " · ")
	}
	summary = "redo " + strings.Join(targets, ", ") + " (" + redoWords(mode) + ") — " + summary
	if made {
		s.sidecarChanged(job.Kind, ref.Path, "", SidecarWritten)
		s.refreshSnapshot(context.WithoutCancel(ctx), job)
	}
	state := jobStateFor(outcomes)
	switch {
	case ctx.Err() != nil:
		state = StateCancelled
	case !made && state == StateDone:
		state = StateSkipped // every new subtitle was put back: nothing changed
	}
	s.event("info", fmt.Sprintf("%s %s — %s", ifElse(made, "✓", "•"), ref.Title, summary))
	s.finishOutcomes(job, state, summary, outcomes)
}

// redoLang redoes one language. The sidecars there now go to the recycle bin before the
// new one is made — a bin that won't take them stops the redo with the old file kept —
// and come back from the bin when nothing could be made. Returns the rungs' outcomes, a
// note for what happened to the old file, whether a new subtitle is in place, and — with
// handoff, in the fast lane — whether the language waits for the GPU lane's whisper
// instead (the old file is back in place meanwhile).
func (s *Service) redoLang(ctx context.Context, job *Job, ref fileRef, mi *mediaInfo, lang, mode string, untagged, handoff bool) ([]Outcome, string, bool, bool) {
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
	case ModeRedoPick:
		step = redoStep{} // the chosen file, below
	case ModeRedoExtract:
		step = redoStep{extract: true, mi: chosenTrack(mi, lang, job.TrackIndex)}
		if step.mi == nil {
			return []Outcome{mkOutcome(lang, RungExtract, ResultExtractFailed, "no such text track to extract")}, "kept the old subtitle", false, false
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
	if job.lane() == LaneGPU {
		step = redoStep{ai: true} // the fast lane already tried the rest
	}
	if handoff && step.ai && !step.extract && !step.download && s.aiWouldRun(mi, []string{lang}) {
		return nil, "", false, true // whisper only: straight to the GPU lane, nothing moved yet
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
			return nil, "kept the old subtitle — " + shortErr(err, 140), false, false
		}
		gone = append(gone, binned{p, at})
	}
	if block != "" {
		// Never the same download again for this video.
		if err := s.ledger.Block(cctx, BlockEntry{Provider: providerOpenSubtitles, FileID: block,
			MediaKey: ref.MediaKey, Lang: lang, Reason: BlockRedo}); err != nil {
			putBack()
			return nil, "kept the old subtitle — couldn't blocklist it: " + shortErr(err, 100), false, false
		}
	}

	var outs []Outcome
	rem := []string{lang}
	var o []Outcome
	if mode == ModeRedoPick && ctx.Err() == nil {
		po := s.pickDownload(ctx, job, ref, lang)
		outs = append(outs, po)
		if po.Result == ResultOK {
			rem = nil
		}
	}
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
	if step.ai && len(rem) > 0 && ctx.Err() == nil && handoff && s.aiWouldRun(mi, rem) {
		// Whisper's turn comes in the GPU lane; the old file goes back until it's done.
		putBack()
		return outs, "", false, true
	}
	if step.ai && len(rem) > 0 && ctx.Err() == nil {
		outs = append(outs, s.rungAI(ctx, job, ref, mi, rem)...)
	}
	if len(rem) == 0 || madeIt(outs) {
		if len(olds) > 0 {
			s.sidecarChanged(job.Kind, ref.Path, olds[0], SidecarRemoved)
			return outs, "the old subtitle is in the recycle bin", true, false
		}
		return outs, "", true, false
	}
	if len(olds) == 0 {
		return outs, "", false, false
	}
	return outs, putBack(), false, false
}

// audioSecOf is a probe's running time in whole seconds (0 unknown), for the AI backlog.
func audioSecOf(mi *mediaInfo) int64 {
	if mi == nil {
		return 0
	}
	return int64(mi.DurationSec)
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

// pickDownload saves the OpenSubtitles file the owner chose as the language's subtitle.
// It is their call, so the sync check only warns — a confident correction is still
// applied — and the row is locked: no automatic redo or upgrade replaces it.
func (s *Service) pickDownload(ctx context.Context, job *Job, ref fileRef, lang string) Outcome {
	id := job.PickFileID
	if s.provider == nil || !s.provider.CanDownload() {
		return mkOutcome(lang, RungOpenSubtitles, ResultProviderError, "OpenSubtitles downloads aren't set up")
	}
	data, err := s.provider.Download(ctx, id)
	switch {
	case errors.Is(err, ErrQuotaExhausted):
		return mkOutcome(lang, RungOpenSubtitles, ResultQuota, "quota spent")
	case errors.Is(err, ErrAuthFailed):
		return mkOutcome(lang, RungOpenSubtitles, ResultAuthFailed, err.Error())
	case err != nil:
		return mkOutcome(lang, RungOpenSubtitles, ResultProviderError, "couldn't download file "+id+": "+shortErr(err, 100))
	}
	f := SubtitleFile{Lang: lang, Variant: VariantFull, Source: SourceOpenSubtitles, Provider: providerOpenSubtitles,
		ProviderFileID: id, Locked: true}
	detail := "downloaded the chosen file"
	if track, terr := ref.audio.track(ctx, ref.audio.syncStream(lang)); terr == nil {
		res, judged := track.CheckJudged(data)
		if judged {
			check := SyncCheck{OffsetMs: res.OffsetMs, Confidence: res.Confidence, At: time.Now().Unix()}
			switch {
			case res.Confidence >= syncKeep && res.NeedsFix(syncTolMs):
				data = []byte(shiftSRT(string(data), res.ShiftMs(), res.Scale))
				check.ShiftMs, check.Scale = res.ShiftMs(), res.Scale
				detail += " · " + shiftWords(res.ShiftMs(), res.Scale)
			case res.Confidence < syncReject:
				detail += fmt.Sprintf(" · it doesn't seem to match the audio (sync confidence %d)", res.Confidence)
			case res.Confidence < syncKeep:
				detail += fmt.Sprintf(" · sync uncertain (confidence %d)", res.Confidence)
			}
			check.apply(&f)
		}
	}
	f.Path = sidecarPathV(ref.Path, lang, VariantFull)
	if err := os.WriteFile(f.Path, data, 0o644); err != nil {
		return mkOutcome(lang, RungOpenSubtitles, ResultProviderError, "couldn't save it: "+shortErr(err, 100))
	}
	s.recordFile(ctx, job, ref, f)
	return mkOutcome(lang, RungOpenSubtitles, ResultOK, detail)
}
