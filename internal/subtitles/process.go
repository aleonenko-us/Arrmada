package subtitles

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/toolrun"
)

// fileRef is everything a job needs to know about its file, resolved once up front: where
// it is, and the metadata a provider search keys on.
type fileRef struct {
	Path          string
	IMDB          string
	Title         string
	Year          int
	Season        int // 0 for movies
	Episode       int
	MediaKey      string // Job.key() format: "m:<id>" or "e:<series>:<season>:<episode>"
	SourceRelease string // the release name the file was imported from, when recorded

	FPS float64 // the video's frame rate from the probe (0 = unknown); scoring compares it

	wants []Want // the title's profile wants (profiles.go): SDH preferences, forced extras
}

// mkOutcome is one rung's Outcome for one wanted language's full subtitle.
func mkOutcome(lang string, rung Rung, res Result, detail string) Outcome {
	return mkOutcomeV(lang, VariantFull, rung, res, detail)
}

// mkOutcomeV is mkOutcome for a want's variant (VariantForced for a profile's forced want).
func mkOutcomeV(lang, variant string, rung Rung, res Result, detail string) Outcome {
	return Outcome{Lang: lang, Variant: variant, Rung: rung, Result: res, Detail: detail}
}

// extractPick is one embedded text track to pull out to a sidecar.
type extractPick struct {
	Index   int    // 0:s:N
	Lang    string // the wanted language it stands in for
	Variant string // VariantFull | VariantSDH (the language's full subtitle) | VariantForced (an extra)
	Path    string // where the SRT goes
}

// aiRunner is the local AI (whisper) as process() sees it. *whisperGen is the real one;
// tests swap in a fake that writes a sidecar without running anything.
type aiRunner interface {
	available() bool
	// canRun reports whether a model for that task is installed: translating needs
	// large-v3, transcribing takes turbo or large-v3.
	canRun(translate bool) bool
	// srcLang is the audio track's language tag ("" = untagged); translating passes it to
	// whisper as the spoken language.
	generate(ctx context.Context, ffmpeg, video, srt, lang, srcLang string, translate bool, stream int, progress func(int)) error
	Backend() string
	Device() string
}

// The seams below default to the real code paths; only tests set the fields.

func (s *Service) resolveRef(ctx context.Context, job *Job) (fileRef, bool) {
	if s.resolve != nil {
		return s.resolve(ctx, job)
	}
	return s.resolveFile(ctx, job)
}

func (s *Service) probeFile(ctx context.Context, path string) (*mediaInfo, error) {
	if s.probe != nil {
		return s.probe(ctx, path)
	}
	return s.probeCached(ctx, path)
}

func (s *Service) runExtract(ctx context.Context, path string, picks []extractPick) error {
	if s.extract != nil {
		return s.extract(ctx, path, picks)
	}
	return s.ffmpegExtract(ctx, path, picks)
}

func (s *Service) aiGen() aiRunner {
	if s.ai != nil {
		return s.ai
	}
	return s.whisper
}

// process ensures the kept-language subtitles exist for one file. Each missing language
// walks a ladder, best source first, and falls to the next rung whenever a rung can't
// produce it: an embedded text track (extract) → an OpenSubtitles download → local AI.
// A file with no download match, a provider error or a spent quota still gets its AI
// subtitle in the same job, rather than ending "nothing produced" and being searched
// again by every sweep.
func (s *Service) process(ctx context.Context, job *Job) {
	ref, ok := s.resolveRef(ctx, job)
	if !ok {
		s.finish(job, StateFailed, "file is gone")
		return
	}
	if ref.MediaKey == "" {
		ref.MediaKey = job.key()
	}
	s.update(job, func(j *Job) { j.State = StateRunning; j.StartedAt = time.Now().Unix() })
	if job.Mode == ModeRepairForced {
		s.processRepair(ctx, job, ref)
		return
	}

	mi, _ := s.probeFile(ctx, ref.Path) // best-effort; nil → no embedded tracks known
	if mi != nil {
		ref.FPS = mi.FPS
	}
	// What this title's profile wants (profiles.go), less what its audio makes unnecessary:
	// full subtitles walk the ladder below, forced ones rungForced.
	all := s.titleWants(ctx, job.Kind, job.MovieID, job.SeriesID)
	ref.wants = all
	cov := coverageOf(ref.Path, kindOf(job.Kind), all, mi)
	langs := wantLangs(cov.active, VariantFull)
	var remaining, remainingForced []string
	orphaned := 0
	if job.Redo {
		// Make every wanted subtitle again; the sidecars there now get written over.
		s.event("info", fmt.Sprintf("Redoing %s: replacing its existing subtitles", ref.Title))
		remaining, remainingForced = langs, wantLangs(cov.active, VariantForced)
	} else {
		for _, w := range cov.missing() {
			switch {
			case w.Variant == VariantForced:
				remainingForced = append(remainingForced, w.Lang)
			case job.Priority == PrioSweep && cov.orphaned[strings.ToLower(w.Lang)]:
				// The sweep leaves languages an orphaned subtitle covers alone (see
				// SweepMissing); an import or a button press makes a real paired one.
				orphaned++
			default:
				remaining = append(remaining, w.Lang)
			}
		}
	}
	if len(remaining) == 0 && len(remainingForced) == 0 {
		note := "all kept languages already have subtitles"
		if len(cov.active) == 0 {
			note = "no subtitles wanted: the audio is already in the profile's language"
		}
		if orphaned > 0 {
			note = fmt.Sprintf("%d language(s) covered only by orphaned subtitles — Ensure replaces them", orphaned)
		}
		s.finish(job, StateSkipped, note)
		return
	}

	// A sweep job leaves alone the languages still backing off (it may have been queued
	// before the last run parked them). Import and manual jobs always try.
	if job.Priority == PrioSweep && !job.Redo {
		var parked, parkedForced []string
		remaining, parked = s.dropBackedOff(ctx, ref, remaining)
		remainingForced, parkedForced = s.dropBackedOffV(ctx, ref, remainingForced, VariantForced)
		if len(remaining) == 0 && len(remainingForced) == 0 {
			s.finish(job, StateSkipped, strings.Join(append(parked, parkedForced...), " · "))
			return
		}
	}

	runID := NewRunID()
	s.update(job, func(j *Job) { j.RunID = runID })
	wanted, wantedForced := remaining, remainingForced
	var outcomes []Outcome
	// inFlight is the rung a stop lands in, for the cancelled outcomes below.
	inFlight := RungExtract
	remaining, outs := s.rungExtract(ctx, job, ref, mi, remaining)
	outcomes = append(outcomes, outs...)
	if ctx.Err() == nil {
		inFlight = RungOpenSubtitles
	}
	remaining, outs = s.rungDownload(ctx, job, ref, remaining)
	outcomes = append(outcomes, outs...)
	outcomes = append(outcomes, s.rungForced(ctx, job, ref, mi, remainingForced)...)
	if ctx.Err() == nil {
		inFlight = RungAI
	}
	outcomes = append(outcomes, s.rungAI(ctx, job, ref, mi, remaining)...)

	produced := 0
	for _, o := range outcomes {
		if o.Result == ResultOK {
			produced++
		}
	}
	summary := outcomeNote(langs, outcomes)
	if produced > 0 {
		// Plex rescans the folder so the new subtitle is selectable without a full scan.
		s.sidecarChanged(job.Kind, ref.Path, "", SidecarWritten)
		// A replace: the orphans whose language was just made go to the recycle bin.
		s.retireReplaced(context.WithoutCancel(ctx), job, ref, outcomes)
	}
	if ctx.Err() != nil {
		// Every language the stop left without a subtitle gets a cancelled attempt, so
		// the history says the run was cut short rather than that nothing was tried.
		outcomes = append(outcomes, stoppedOutcomes(wanted, wantedForced, outcomes, inFlight)...)
		s.recordAttempts(ctx, runID, ref, outcomes)
		note := "stopped"
		if summary != "" {
			note = "stopped after " + summary
		}
		s.event("info", fmt.Sprintf("■ %s — %s", ref.Title, note))
		s.finishOutcomes(job, StateCancelled, note, outcomes)
		if produced > 0 {
			s.refreshSnapshot(context.WithoutCancel(ctx), job)
		}
		return
	}
	s.recordAttempts(ctx, runID, ref, outcomes)
	s.updateBackoff(ctx, ref, wanted, outcomes)
	s.updateBackoffV(ctx, ref, wantedForced, VariantForced, outcomes)
	if summary == "" {
		summary = "no source could act" // every rung declined without a reason; shouldn't happen
	}
	state := jobStateFor(outcomes)
	if state == StateFailed {
		s.event("warn", fmt.Sprintf("✗ %s — %s", ref.Title, summary))
	} else {
		s.event("info", fmt.Sprintf("✓ %s — %s", ref.Title, summary))
	}
	s.finishOutcomes(job, state, summary, outcomes)
	// A movie's Library chips show its next-try date, so it's patched even when nothing
	// was written. (A show's roll-up has no per-language chips; re-listing every episode
	// after each sweep job would be wasted disk work.)
	if produced > 0 || job.Kind == "movie" {
		s.refreshSnapshot(ctx, job)
	}
}

// jobStateFor is a finished run's honest state: done when anything was written, failed
// when every language it tried ended in an error (a rejected login, a provider or whisper
// failure, a missing model), and skipped when the subtitles simply don't exist anywhere
// it can look.
func jobStateFor(outcomes []Outcome) JobState {
	last := map[string]Result{}
	for _, o := range outcomes {
		if o.Result == ResultOK {
			return StateDone
		}
		last[strings.ToLower(o.Lang)+"|"+o.Variant] = o.Result
	}
	if len(last) == 0 {
		return StateSkipped
	}
	for _, r := range last {
		if !r.IsError() {
			return StateSkipped
		}
	}
	return StateFailed
}

// recordAttempts writes a run's outcomes to the ledger. Bookkeeping: an error is logged,
// never the job's failure, and a stopped job still records what it did.
func (s *Service) recordAttempts(ctx context.Context, runID string, ref fileRef, outcomes []Outcome) {
	if err := s.ledger.RecordAttempts(context.WithoutCancel(ctx), runID, ref.MediaKey, ref.Path, outcomes); err != nil {
		s.log.Warn("subtitles: could not record attempts", "media_key", ref.MediaKey, "err", err)
	}
}

// recordFile writes a sidecar's provenance to the ledger, with the job's identity filled
// in. Logged, never fatal, like recordAttempts.
func (s *Service) recordFile(ctx context.Context, job *Job, ref fileRef, f SubtitleFile) {
	f.MediaKey, f.Kind, f.VideoPath = ref.MediaKey, job.Kind, ref.Path
	f.MovieID, f.SeriesID, f.Season, f.Episode = job.MovieID, job.SeriesID, job.Season, job.Episode
	if _, err := s.ledger.RecordFile(context.WithoutCancel(ctx), f); err != nil {
		s.log.Warn("subtitles: could not record a sidecar's source", "path", f.Path, "err", err)
	}
}

// outcomeNote renders the ladder's outcomes as one line per language, in kept-language
// order, each rung's result chained with arrows: "en: no OpenSubtitles match →
// AI-generated · es: quota spent → whisper can't translate into es". A stop isn't a
// step: the note already starts with "stopped".
func outcomeNote(langs []string, outcomes []Outcome) string {
	var parts []string
	for _, l := range langs {
		var steps []string
		for _, o := range outcomes {
			if strings.EqualFold(o.Lang, l) && o.Result != ResultCancelled && o.Variant != VariantForced {
				steps = append(steps, o.Detail)
			}
		}
		if len(steps) > 0 {
			parts = append(parts, strings.ToLower(l)+": "+strings.Join(steps, " → "))
		}
	}
	// A profile's forced wants follow, "en forced: …", in the order they were tried.
	var forced []string
	steps := map[string][]string{}
	for _, o := range outcomes {
		if o.Variant != VariantForced || o.Result == ResultCancelled {
			continue
		}
		l := strings.ToLower(o.Lang)
		if _, ok := steps[l]; !ok {
			forced = append(forced, l)
		}
		steps[l] = append(steps[l], o.Detail)
	}
	for _, l := range forced {
		parts = append(parts, wantLabel(l, VariantForced)+": "+strings.Join(steps[l], " → "))
	}
	return strings.Join(parts, " · ")
}

// stoppedOutcomes is a cancelled outcome for every want a stopped run left unmade, so the
// history says the run was cut short rather than that nothing was tried.
func stoppedOutcomes(wanted, wantedForced []string, outcomes []Outcome, inFlight Rung) []Outcome {
	got := map[string]bool{}
	for _, o := range outcomes {
		if o.Result == ResultOK {
			got[strings.ToLower(o.Lang)+"|"+outcomeVariant(o)] = true
		}
	}
	var out []Outcome
	for _, l := range wanted {
		if !got[strings.ToLower(l)+"|"+VariantFull] {
			out = append(out, mkOutcome(l, inFlight, ResultCancelled, "stopped"))
		}
	}
	for _, l := range wantedForced {
		if !got[strings.ToLower(l)+"|"+VariantForced] {
			out = append(out, mkOutcomeV(l, VariantForced, inFlight, ResultCancelled, "stopped"))
		}
	}
	return out
}

// without returns langs minus the ones in done (case-insensitive).
func without(langs []string, done map[string]bool) []string {
	var out []string
	for _, l := range langs {
		if !done[strings.ToLower(l)] {
			out = append(out, l)
		}
	}
	return out
}

// rungExtract pulls an embedded text track out for each language that has a full (plain or
// SDH) one. A track that extracts to nothing (an empty or broken stream) counts as not
// produced, so the language falls through to the next rung. Returns the languages still
// missing.
//
// While ffmpeg is reading the file anyway, each wanted language's forced track comes out
// too, as "<base>.<lang>.forced.srt", so Plex keeps showing the foreign-dialogue lines.
// It is an extra: it never counts as the language's subtitle.
func (s *Service) rungExtract(ctx context.Context, job *Job, ref fileRef, mi *mediaInfo, langs []string) ([]string, []Outcome) {
	if ctx.Err() != nil || mi == nil || len(langs) == 0 {
		return langs, nil
	}
	var picks []extractPick
	for _, l := range langs {
		if t, ok := pickFullTrackPref(mi.Subs, l, sdhFor(ref.wants, l)); ok {
			v := VariantFull
			if t.SDH {
				v = VariantSDH
			}
			picks = append(picks, extractPick{Index: t.Index, Lang: l, Variant: v, Path: sidecarPathV(ref.Path, strings.ToLower(l), v)})
		}
	}
	if len(picks) == 0 {
		return langs, nil
	}
	extras := s.languages(ctx)
	if ref.wants != nil {
		extras = langsOf(ref.wants)
	}
	for _, l := range extras {
		t, ok := pickForcedTrack(mi.Subs, l)
		if !ok {
			continue
		}
		p := sidecarPathV(ref.Path, strings.ToLower(l), VariantForced)
		if _, err := os.Stat(p); err == nil {
			continue // already there (from the release, or an earlier run)
		}
		picks = append(picks, extractPick{Index: t.Index, Lang: l, Variant: VariantForced, Path: p})
	}
	s.update(job, func(j *Job) { j.Stage = "extracting embedded subtitles" })
	err := s.runExtract(ctx, ref.Path, picks)
	if ctx.Err() != nil {
		return langs, nil // stopped: the caller reports it; nothing falls through
	}
	if err != nil {
		s.event("warn", fmt.Sprintf("%s: extract failed: %v", ref.Title, err))
	}
	var outs []Outcome
	done := map[string]bool{}
	for _, p := range picks {
		fi, serr := os.Stat(p.Path)
		if p.Variant == VariantForced {
			if serr == nil && fi.Size() == 0 {
				_ = os.Remove(p.Path)
			} else if serr == nil {
				idx := p.Index
				s.recordFile(ctx, job, ref, SubtitleFile{Path: p.Path, Lang: strings.ToLower(p.Lang), Variant: VariantForced, Source: SourceEmbedded, EmbeddedIndex: &idx})
			}
			continue // an extra, not the language's outcome
		}
		switch {
		case serr != nil:
			outs = append(outs, mkOutcome(p.Lang, RungExtract, ResultExtractFailed, "extract failed"))
		case fi.Size() == 0:
			_ = os.Remove(p.Path)
			outs = append(outs, mkOutcome(p.Lang, RungExtract, ResultEmpty, "embedded track was empty"))
		default:
			done[strings.ToLower(p.Lang)] = true
			idx := p.Index
			s.recordFile(ctx, job, ref, SubtitleFile{Path: p.Path, Lang: strings.ToLower(p.Lang), Variant: p.Variant, Source: SourceEmbedded, EmbeddedIndex: &idx})
			outs = append(outs, mkOutcome(p.Lang, RungExtract, ResultOK, "extracted"))
		}
	}
	return without(langs, done), outs
}

// pauser is implemented by providers that can sit out a spent quota or rejected
// credentials; while paused, the download rung makes no requests at all — not even a search.
type pauser interface{ Paused() bool }

// pauseReasoner says why a provider is paused, as the outcome the skipped rung records.
type pauseReasoner interface{ PauseReason() (Result, string) }

// providerPaused reports whether the provider is waiting out a spent quota or a rejected login.
func (s *Service) providerPaused() bool {
	p, ok := s.provider.(pauser)
	return ok && p.Paused()
}

// pauseOutcome is why the provider is paused; a provider that can't say is out of quota.
func (s *Service) pauseOutcome() (Result, string) {
	if p, ok := s.provider.(pauseReasoner); ok {
		return p.PauseReason()
	}
	return ResultQuota, "quota spent"
}

// rungDownload searches OpenSubtitles for each language. A language with no match, a
// provider error or a spent quota stays in the returned list for the AI rung. Skipped
// without a single request when downloads aren't configured or the quota is paused.
func (s *Service) rungDownload(ctx context.Context, job *Job, ref fileRef, langs []string) ([]string, []Outcome) {
	return s.rungDownloadV(ctx, job, ref, langs, VariantFull)
}

// rungDownloadV is rungDownload for one variant's wants: VariantForced searches the
// foreign-parts-only uploads and writes "<base>.<lang>.forced.srt".
func (s *Service) rungDownloadV(ctx context.Context, job *Job, ref fileRef, langs []string, variant string) ([]string, []Outcome) {
	if ctx.Err() != nil || len(langs) == 0 || s.provider == nil || !s.provider.CanDownload() {
		return langs, nil
	}
	mkOutcome := func(lang string, rung Rung, res Result, detail string) Outcome {
		return mkOutcomeV(lang, variant, rung, res, detail)
	}
	var outs []Outcome
	if s.providerPaused() {
		res, detail := s.pauseOutcome()
		for _, l := range langs {
			outs = append(outs, mkOutcome(l, RungOpenSubtitles, res, detail))
		}
		return langs, outs
	}
	// One hash per file, shared by every language's search.
	s.update(job, func(j *Job) { j.Stage = "searching OpenSubtitles" })
	hash, herr := osHash(ref.Path)
	if herr != nil {
		s.log.Debug("subtitles: could not hash file for provider search", "path", ref.Path, "err", herr)
	}
	done := map[string]bool{}
	// stopAll is a provider-wide failure (a spent quota, a rejected login): every further
	// language would fail the same way, so they get the same outcome without a request.
	var stopAll *Outcome
	waited := false // a 429 is waited out once per job, then recorded
	for _, l := range langs {
		if ctx.Err() != nil {
			return without(langs, done), outs
		}
		if stopAll != nil {
			o := *stopAll
			o.Lang = l
			outs = append(outs, o)
			continue
		}
		res, err := s.grabOneV(ctx, ref, hash, l, variant)
		var rl *RateLimitError
		if errors.As(err, &rl) && !waited {
			waited = true
			wait := rl.RetryAfter
			if s.rateWait > 0 {
				wait = s.rateWait
			}
			s.event("info", fmt.Sprintf("%s: OpenSubtitles rate-limited, retrying in %s", ref.Title, rl.RetryAfter.Round(time.Second)))
			select {
			case <-ctx.Done():
			case <-time.After(wait):
				res, err = s.grabOneV(ctx, ref, hash, l, variant)
			}
		}
		switch {
		case ctx.Err() != nil:
			return without(langs, done), outs
		case errors.Is(err, ErrQuotaExhausted):
			// Say it once per job. The sweep re-queues anything still missing after the reset.
			s.event("warn", fmt.Sprintf("%s: OpenSubtitles daily download quota is used up — will retry after it resets", ref.Title))
			o := mkOutcome(l, RungOpenSubtitles, ResultQuota, "quota spent")
			stopAll = &o
			outs = append(outs, o)
		case errors.Is(err, ErrRateLimited):
			// Still limited after the wait: the rest goes to AI, and the sweep tries again
			// in minutes — never a quota pause.
			s.event("warn", fmt.Sprintf("%s: OpenSubtitles is still rate-limiting — leaving the rest for later", ref.Title))
			o := mkOutcome(l, RungOpenSubtitles, ResultRateLimited, "OpenSubtitles rate limit")
			stopAll = &o
			outs = append(outs, o)
		case errors.Is(err, ErrAuthFailed):
			s.event("warn", fmt.Sprintf("%s: %v — check the OpenSubtitles credentials", ref.Title, err))
			o := mkOutcome(l, RungOpenSubtitles, ResultAuthFailed, err.Error())
			stopAll = &o
			outs = append(outs, o)
		case err != nil:
			s.event("warn", fmt.Sprintf("%s: download %s failed: %v", ref.Title, l, err))
			outs = append(outs, mkOutcome(l, RungOpenSubtitles, ResultProviderError, "OpenSubtitles error"))
		case res.Written:
			done[strings.ToLower(l)] = true
			written := variant
			if variant == VariantFull && res.Pick.HearingImpaired {
				written = VariantSDH
			}
			score := res.Pick.Score
			s.recordFile(ctx, job, ref, SubtitleFile{
				Path: res.Path, Lang: strings.ToLower(l), Variant: written, Source: SourceOpenSubtitles,
				Provider: providerOpenSubtitles, ProviderFileID: res.Pick.FileID, ReleaseName: res.Pick.Release,
				HashMatch: res.Pick.HashMatch, Score: &score,
			})
			o := mkOutcome(l, RungOpenSubtitles, ResultOK, "downloaded")
			o.Score, o.Reasons = res.Pick.Score, res.Pick.Reasons
			outs = append(outs, o)
		default:
			// no_match, below_score (with the scoring explanation) or out_of_sync: the
			// language falls through to AI either way.
			o := mkOutcome(l, RungOpenSubtitles, grabOutcome(res.Outcome), res.Detail)
			if res.Outcome != "no_match" {
				o.Score, o.Reasons = res.Pick.Score, res.Pick.Reasons
			}
			outs = append(outs, o)
		}
	}
	return without(langs, done), outs
}

// grabOutcome types grabOne's outcome word for the ledger.
func grabOutcome(o string) Result {
	switch o {
	case "below_score":
		return ResultBelowScore
	case "out_of_sync":
		return ResultOutOfSync
	case "done":
		return ResultOK
	}
	return ResultNoMatch
}

// rungAI generates each language still missing from the file's audio with local whisper —
// the last rung, so every outcome it records says why when it can't.
func (s *Service) rungAI(ctx context.Context, job *Job, ref fileRef, mi *mediaInfo, langs []string) []Outcome {
	if ctx.Err() != nil || len(langs) == 0 {
		return nil
	}
	ai := s.aiGen()
	var audioLangs []string
	if mi != nil {
		audioLangs = mi.AudioLangs
	}
	var outs []Outcome
	for _, l := range langs {
		if ctx.Err() != nil {
			break
		}
		if !ai.available() {
			outs = append(outs, mkOutcome(l, RungAI, ResultModelMissing, "no whisper model installed"))
			continue
		}
		// Only what whisper can actually do: transcribe when the audio is that language, or
		// translate to English. It can't translate into other languages.
		plan := aiPlan(audioLangs, l)
		if plan == "" {
			outs = append(outs, mkOutcome(l, RungAI, ResultImpossible, "whisper can't translate into "+strings.ToLower(l)))
			continue
		}
		translate := plan == "translate"
		if !ai.canRun(translate) {
			outs = append(outs, mkOutcome(l, RungAI, ResultModelMissing, "needs large-v3 to translate"))
			continue
		}
		verb := "transcribing"
		if translate {
			verb = "translating → en"
		}
		// Which audio track: a MULTI release has the dub too, and the wrong one gives a
		// subtitle in the wrong language under the right name.
		stream, track := audioStreamFor(mi, l, translate)
		from := ""
		if stream >= 0 {
			from = fmt.Sprintf(" from audio track %d/%d", stream+1, len(mi.Audio))
			if track.Lang != "" {
				from += " (" + track.Lang + ")"
			}
		}
		s.event("info", fmt.Sprintf("AI %s %s (%s)%s…", verb, ref.Title, l, from))
		started := time.Now()
		s.update(job, func(j *Job) { j.Stage = "AI " + verb + " (" + l + ")"; j.Progress = 0 })
		onProgress := func(pct int) { s.update(job, func(j *Job) { j.Progress = pct }) }
		dst := sidecarPath(ref.Path, strings.ToLower(l))
		if err := ai.generate(ctx, s.ffmpeg, ref.Path, dst, l, track.Lang, translate, stream, onProgress); err != nil {
			if ctx.Err() != nil {
				break
			}
			s.event("warn", fmt.Sprintf("%s: AI %s failed: %v", ref.Title, l, err))
			outs = append(outs, mkOutcome(l, RungAI, ResultAIFailed, "AI failed: "+shortErr(err, 120)))
			continue
		}
		src := SourceAITranscribe
		if translate {
			src = SourceAITranslate
		}
		s.recordFile(ctx, job, ref, SubtitleFile{Path: dst, Lang: strings.ToLower(l), Variant: VariantFull, Source: src, Model: aiModelName(ai, translate)})
		outs = append(outs, mkOutcome(l, RungAI, ResultOK, "AI-generated"))
		took := time.Since(started)
		msg := fmt.Sprintf("%s: AI subtitle written (%s) in %s on %s", ref.Title, l, took.Round(time.Second), ai.Backend())
		if dev := ai.Device(); dev != "" {
			msg += " [" + dev + "]"
		}
		if mi != nil && mi.DurationSec > 0 && took > 0 {
			// The number that says whether the GPU is pulling its weight.
			dur := float64(mi.DurationSec)
			msg += fmt.Sprintf(" — %s of audio, %.1f× realtime", fmtAudio(dur), dur/took.Seconds())
		}
		s.event("info", msg)
	}
	return outs
}

// aiModelName is the whisper model file a run used, for the sidecar's provenance; "" when
// the runner can't say (a test fake).
func aiModelName(ai aiRunner, translate bool) string {
	if m, ok := ai.(interface{ modelPath(bool) string }); ok {
		if p := m.modelPath(translate); p != "" {
			return filepath.Base(p)
		}
	}
	return ""
}

// shortErr is the tail of an error message, for a job note that has to fit on one line.
func shortErr(err error, n int) string {
	msg := strings.ReplaceAll(err.Error(), "\n", " ")
	if r := []rune(msg); len(r) > n {
		return "…" + string(r[len(r)-n:])
	}
	return msg
}

// resolveFile resolves a job's file path + metadata (title/year/imdb, needed for provider searches).
func (s *Service) resolveFile(ctx context.Context, job *Job) (fileRef, bool) {
	if job.Kind == "episode" {
		full, err := s.series.Get(ctx, job.SeriesID)
		if err != nil {
			return fileRef{}, false
		}
		p, _ := s.series.EpisodeFilePath(ctx, job.SeriesID, job.Season, job.Episode)
		if p == "" {
			return fileRef{}, false
		}
		ref := fileRef{Path: p, IMDB: full.IMDBID, Title: full.Title, Year: full.Year,
			Season: job.Season, Episode: job.Episode, MediaKey: job.key()}
		for _, sn := range full.Seasons {
			for _, e := range sn.Episodes {
				if e.SeasonNumber == job.Season && e.EpisodeNumber == job.Episode {
					ref.SourceRelease = e.SourceRelease
				}
			}
		}
		return ref, true
	}
	m, err := s.movies.Get(ctx, job.MovieID)
	if err != nil || !m.HasFile || m.MovieFilePath == "" {
		return fileRef{}, false
	}
	return fileRef{Path: m.MovieFilePath, IMDB: m.IMDBID, Title: m.Title, Year: m.Year,
		MediaKey: job.key(), SourceRelease: m.SourceRelease}, true
}

// ffmpegExtract pulls every picked embedded track out to its SRT sidecar in a single
// ffmpeg pass (one read of the file, all tracks). Empty outputs are left for the caller to
// judge and prune.
func (s *Service) ffmpegExtract(ctx context.Context, path string, picks []extractPick) error {
	if len(picks) == 0 {
		return nil
	}
	args := []string{"-y", "-hide_banner", "-i", path}
	for _, p := range picks {
		// -f srt: the output name doesn't always end in .srt (a repair writes to a temp
		// name first), so the format can't be left to ffmpeg's guess from the extension.
		args = append(args, "-map", fmt.Sprintf("0:s:%d", p.Index), "-c:s", "srt", "-f", "srt", p.Path)
	}
	return toolrun.Command(ctx, toolrun.Background, s.ffmpeg, args...).Run()
}
