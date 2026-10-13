package subtitles

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/langs"
	"github.com/tristenlammi/arrmada/internal/library"
)

// The upgrade loop. An episode that imports on air night usually gets an AI subtitle,
// or a download that wasn't uploaded for this exact release, because that's all there is
// yet; a human subtitle timed against the release tends to turn up on OpenSubtitles
// within days. Once a day the owned weak subtitles from the last few weeks are searched
// again, and a clearly better one replaces the old, which goes to the recycle bin.
//
// Only rows Arrmada owns are touched: AI-made ones and non-hash downloads, never a
// subtitle the owner picked (locked), adopted, extracted or found beside the release. The
// loop spends at most a share of the day's remaining downloads, so the backlog of
// missing languages still gets its downloads, and sends nothing while the provider is
// paused (quota spent, login rejected).

const (
	// ModeUpgrade marks an upgrade job (Job.Mode); Job.Langs are the languages to look at.
	ModeUpgrade = "upgrade"

	// How long after it was made a subtitle is still revisited, and the share of the day's
	// remaining downloads upgrades may use.
	keyUpgradeDays      = "subs_upgrade_days"
	defaultUpgradeDays  = 21
	keyUpgradeShare     = "subs_upgrade_quota_share"
	defaultUpgradeShare = 0.3

	// upgradeMaxSearches caps the languages searched per daily pass. Searches don't cost
	// downloads, but they do cost the provider's patience.
	upgradeMaxSearches = 30
	// upgradeScore: a non-hash candidate scoring this well replaces the old subtitle once
	// the sync gate keeps it. Below it, a candidate must also fit the audio with
	// upgradeSyncConfidence.
	upgradeScore          = 90
	upgradeSyncConfidence = 80
	// freeTierDownloads is the daily allowance assumed when the provider hasn't said.
	freeTierDownloads = 20
)

// upgradeBudget is how many downloads upgrades may still spend today.
type upgradeBudget struct {
	mu   sync.Mutex
	set  bool
	left int
}

// upgradeWindow is subs_upgrade_days as a duration (1-90 days).
func (s *Service) upgradeWindow(ctx context.Context) time.Duration {
	days := defaultUpgradeDays
	if s.settings != nil {
		if n, err := strconv.Atoi(strings.TrimSpace(s.settings.Get(ctx, keyUpgradeDays, ""))); err == nil && n >= 1 && n <= 90 {
			days = n
		}
	}
	return time.Duration(days) * 24 * time.Hour
}

// upgradeDownloads is the day's download allowance for upgrades: subs_upgrade_quota_share
// of the remaining downloads, rounded down. Remaining downloads are what the provider last
// reported; when it hasn't (or the count is from before a reset), the account's daily
// allowance, else the free tier's.
func (s *Service) upgradeDownloads(ctx context.Context) int {
	share := defaultUpgradeShare
	if s.settings != nil {
		if f, err := strconv.ParseFloat(strings.TrimSpace(s.settings.Get(ctx, keyUpgradeShare, "")), 64); err == nil && f >= 0 && f <= 1 {
			share = f
		}
	}
	remaining := -1
	if q, ok := s.provider.(quotaReporter); ok {
		left, reset := q.Quota()
		if !reset.IsZero() {
			return 0 // spent until the reset
		}
		if left > 0 {
			remaining = left
		}
	}
	if remaining < 0 {
		remaining = freeTierDownloads
		if st := s.providerStatus(); st.Allowed > 0 {
			remaining = st.Allowed
		}
	}
	return int(math.Floor(float64(remaining) * share))
}

// takeUpgradeDownload spends one of the day's upgrade downloads; false when none is left.
// A job that runs without a pass having set the budget in this process (one queued before
// a restart) works it out then.
func (s *Service) takeUpgradeDownload(ctx context.Context) bool {
	b := &s.upgrade
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.set {
		b.set, b.left = true, s.upgradeDownloads(ctx)
	}
	if b.left <= 0 {
		return false
	}
	b.left--
	return true
}

// returnUpgradeDownload gives back a download that wasn't spent (the request failed).
func (s *Service) returnUpgradeDownload() {
	s.upgrade.mu.Lock()
	s.upgrade.left++
	s.upgrade.mu.Unlock()
}

// nextUpgradeCheck is when a subtitle made at created and last searched at last is
// searched again: daily in its first week, when new uploads are likeliest, then every
// third day. The few hours' slack keeps a daily task from skipping a day.
func nextUpgradeCheck(created, last time.Time) time.Time {
	if last.Sub(created) < 7*24*time.Hour {
		return last.Add(20 * time.Hour)
	}
	return last.Add(68 * time.Hour)
}

// UpgradeSweep is the daily upgrade pass: it picks the owned weak subtitles due a look
// and queues one upgrade job per file (sweep priority, fast lane, visible and stoppable
// in the Queue). Returns how many jobs were queued. Nothing is queued while downloads
// aren't set up, the provider is paused, or the day's share of downloads rounds to none.
func (s *Service) UpgradeSweep(ctx context.Context) int {
	if s.provider == nil || !s.provider.CanDownload() {
		return 0
	}
	if s.providerPaused() {
		_, why := s.pauseOutcome()
		s.log.Info("subtitles: upgrade check skipped — OpenSubtitles is paused", "why", why)
		return 0
	}
	budget := s.upgradeDownloads(ctx)
	if budget < 1 {
		s.log.Info("subtitles: upgrade check skipped — no downloads to spare today")
		return 0
	}
	now := time.Now()
	since := now.Add(-s.upgradeWindow(ctx))
	rows, err := s.ledger.UpgradeCandidates(ctx, since)
	if err != nil {
		s.log.Warn("subtitles: could not list subtitles to upgrade", "err", err)
		return 0
	}
	last, err := s.ledger.LastUpgradeChecks(ctx, since)
	if err != nil {
		s.log.Warn("subtitles: could not read past upgrade checks", "err", err)
		return 0
	}
	byKey := map[string]*Job{}
	var jobs []*Job
	seen := map[string]bool{}
	searches := 0
	for _, f := range rows {
		if searches >= upgradeMaxSearches || ctx.Err() != nil {
			break
		}
		k := f.MediaKey + "|" + f.Lang
		if seen[k] {
			continue
		}
		seen[k] = true
		if at, ok := last[k]; ok && now.Before(nextUpgradeCheck(time.Unix(f.CreatedAt, 0), time.Unix(at, 0))) {
			continue
		}
		j := byKey[f.MediaKey]
		if j == nil {
			j = &Job{Kind: f.Kind, MovieID: f.MovieID, SeriesID: f.SeriesID, Season: f.Season, Episode: f.Episode,
				Mode: ModeUpgrade, Priority: PrioSweep, Lane: LaneFast}
			j.Title = s.upgradeTitle(ctx, j)
			byKey[f.MediaKey] = j
			jobs = append(jobs, j)
		}
		j.Langs = append(j.Langs, f.Lang)
		searches++
	}
	s.upgrade.mu.Lock()
	s.upgrade.set, s.upgrade.left = true, budget
	s.upgrade.mu.Unlock()
	if len(jobs) == 0 {
		return 0
	}
	_, added := s.enqueueJobs("upgrade", jobs)
	if added > 0 {
		s.event("info", fmt.Sprintf("Looking for better subtitles for %d file(s) (up to %d downloads today)", added, budget))
	}
	return added
}

// upgradeTitle is the Queue's name for an upgrade job's file.
func (s *Service) upgradeTitle(ctx context.Context, j *Job) string {
	if j.Kind == "episode" {
		t := fmt.Sprintf("S%02dE%02d", j.Season, j.Episode)
		if s.series != nil {
			if sm, err := s.series.Get(ctx, j.SeriesID); err == nil {
				t = sm.Title + " - " + t
			}
		}
		return t
	}
	if s.movies != nil {
		if m, err := s.movies.Get(ctx, j.MovieID); err == nil {
			return m.Title
		}
	}
	return j.key()
}

// processUpgrade is process() for an upgrade job: each language's owned weak subtitle is
// searched again and replaced when something clearly better exists.
func (s *Service) processUpgrade(ctx context.Context, job *Job, ref fileRef) {
	runID := NewRunID()
	s.update(job, func(j *Job) { j.RunID, j.Stage = runID, "looking for a better subtitle" })
	cctx := context.WithoutCancel(ctx)
	rows, err := s.ledger.FilesFor(cctx, ref.MediaKey)
	if err != nil {
		s.finish(job, StateFailed, "couldn't read which subtitles Arrmada made: "+shortErr(err, 100))
		return
	}
	since := time.Now().Add(-s.upgradeWindow(ctx))
	mi, _ := s.probeFile(ctx, ref.Path)
	if mi != nil {
		ref.FPS = mi.FPS
	}
	ref.audio = s.newJobAudio(ref.Path, mi)
	defer ref.audio.close()
	ref.wants = s.titleWants(ctx, job.Kind, job.MovieID, job.SeriesID)
	wanted := s.titleLangs(ctx, job.Kind, job.MovieID, job.SeriesID)
	hash, herr := osHash(ref.Path)
	if herr != nil {
		s.log.Debug("subtitles: could not hash file for an upgrade search", "path", ref.Path, "err", herr)
	}

	var outs []Outcome
	var notes []string
	upgraded := 0
	for _, l := range job.Langs {
		if ctx.Err() != nil {
			break
		}
		lang := strings.ToLower(l)
		row, ok := upgradeRow(rows, l, ref.Path, since)
		switch {
		case !ok:
			notes = append(notes, lang+": nothing to upgrade any more")
			continue
		case !inLangs(wanted, langs.Canonical(l)):
			notes = append(notes, lang+": no longer wanted")
			continue
		}
		o, stop := s.upgradeLang(ctx, job, ref, hash, row)
		outs = append(outs, o)
		if o.Result == ResultOK {
			upgraded++
		}
		notes = append(notes, lang+": "+o.Detail)
		if stop {
			break // the provider is paused or the day's share is spent: the rest waits too
		}
	}
	s.recordAttempts(ctx, runID, ref, outs)
	if upgraded > 0 {
		s.sidecarChanged(job.Kind, ref.Path, "", SidecarWritten)
		s.refreshSnapshot(cctx, job)
	}
	note := "upgrade — " + strings.Join(notes, " · ")
	state := StateSkipped
	switch {
	case ctx.Err() != nil:
		state, note = StateCancelled, "stopped · "+note
	case upgraded > 0:
		state = StateDone
		s.event("info", fmt.Sprintf("✓ %s — %s", ref.Title, note))
	case len(outs) > 0 && jobStateFor(outs) == StateFailed:
		state = StateFailed
	}
	s.finishOutcomes(job, state, note, outs)
}

// upgradeRow is the subtitle an upgrade would replace for lang: the newest live row that is
// still a candidate (owned, weak, unlocked, recent, a full subtitle), for the video as it
// is now, and still on disk.
func upgradeRow(rows []SubtitleFile, lang, video string, since time.Time) (SubtitleFile, bool) {
	var best SubtitleFile
	found := false
	for _, f := range rows {
		weak := f.Source == SourceAITranscribe || f.Source == SourceAITranslate || (f.Source == SourceOpenSubtitles && !f.HashMatch)
		if !weak || f.Locked || f.SupersededAt != 0 || f.Variant == VariantForced || f.CreatedAt < since.Unix() ||
			langs.Canonical(f.Lang) != langs.Canonical(lang) || f.VideoPath != video || !fileExists(f.Path) {
			continue
		}
		if !found || f.CreatedAt > best.CreatedAt {
			best, found = f, true
		}
	}
	return best, found
}

// betterCandidate is the candidate worth a download to replace row: a hash match, else
// the best usable candidate that outscores the current subtitle (an AI one scores 0).
func betterCandidate(ranked []ScoredCandidate, row SubtitleFile) (ScoredCandidate, bool) {
	cur := 0
	if row.Score != nil {
		cur = *row.Score
	}
	for _, c := range ranked {
		if !c.usable() {
			break // sorted usable first
		}
		if c.FileID == row.ProviderFileID && row.Source == SourceOpenSubtitles {
			continue
		}
		if c.HashMatch {
			return c, true
		}
		if c.Score < scoreFloor || c.Score <= cur {
			break // sorted by score: nothing further down beats it
		}
		return c, true
	}
	return ScoredCandidate{}, false
}

// upgradeLang searches again for one language and replaces row when a clearly better
// subtitle exists. stop says the rest of the job's languages should wait as well.
func (s *Service) upgradeLang(ctx context.Context, job *Job, ref fileRef, hash string, row SubtitleFile) (Outcome, bool) {
	lang := strings.ToLower(row.Lang)
	out := func(res Result, detail string) Outcome { return mkOutcome(lang, RungUpgrade, res, detail) }
	if s.providerPaused() {
		res, detail := s.pauseOutcome()
		return out(res, detail), true
	}
	m := matchContextFor(ref)
	m.SDH = sdhFor(ref.wants, lang)
	results, err := s.provider.Search(ctx, SearchRequest{
		IMDBID: ref.IMDB, Title: ref.Title, Year: ref.Year, Season: ref.Season, Episode: ref.Episode,
		Language: lang, MovieHash: hash,
	})
	if err != nil {
		return upgradeErr(lang, err)
	}
	blocked, err := s.blockedFiles(ctx, ref.MediaKey)
	if err != nil {
		return out(ResultProviderError, "couldn't read the subtitle blocklist"), false // fail closed
	}
	c, ok := betterCandidate(rankCandidates(results, m, blocked), row)
	if !ok {
		return out(ResultNoMatch, noBetterYet(row, time.Now(), s.upgradeWindow(ctx))), false
	}
	written := VariantFull
	if c.HearingImpaired {
		written = VariantSDH
	}
	dst := sidecarPathV(ref.Path, lang, written)
	if dst != row.Path && fileExists(dst) {
		// Another subtitle (maybe not Arrmada's) has the name the better one would take.
		return out(ResultNoMatch, "a better subtitle exists, but "+filepath.Base(dst)+" is already taken"), false
	}
	needsFit := !c.HashMatch && c.Score < upgradeScore
	if needsFit {
		// Don't spend a download on a candidate whose fit can't be measured.
		if err := s.syncReady(ctx, ref, lang); err != nil {
			return out(ResultOutOfSync, "a better match exists, but the audio couldn't be read to check its sync"), false
		}
	}
	if !s.takeUpgradeDownload(ctx) {
		return out(ResultQuota, "today's share of the OpenSubtitles downloads for upgrades is used"), true
	}
	data, err := s.provider.Download(ctx, c.FileID)
	if err != nil {
		s.returnUpgradeDownload()
		return upgradeErr(lang, err)
	}
	if !c.HashMatch {
		if s.syncGate != nil {
			kept, ok, detail := s.syncGate(ctx, ref, lang, c, data)
			if !ok {
				return withScore(out(ResultOutOfSync, detail), c), false
			}
			data = kept
		}
		if needsFit {
			chk, judged := ref.audio.verdict(lang, c.FileID)
			if !judged || chk.Confidence < upgradeSyncConfidence {
				// Not proven to fit: never fetched again for this video, so tomorrow's check
				// doesn't spend another download on the same file.
				conf := "couldn't be measured"
				if judged {
					conf = fmt.Sprintf("confidence %d", chk.Confidence)
				}
				if err := s.ledger.Block(context.WithoutCancel(ctx), BlockEntry{Provider: providerOpenSubtitles, FileID: c.FileID,
					MediaKey: ref.MediaKey, Lang: lang, Reason: BlockOutOfSync}); err != nil {
					s.log.Warn("subtitles: could not blocklist a refused upgrade", "media", ref.MediaKey, "file_id", c.FileID, "err", err)
				}
				return withScore(out(ResultOutOfSync, fmt.Sprintf("a better-scoring download's sync %s; an upgrade needs %d", conf, upgradeSyncConfidence)), c), false
			}
		}
	}
	if err := s.swapSidecar(ctx, row, dst, data); err != nil {
		s.event("warn", fmt.Sprintf("%s: %s upgrade not saved — %v", ref.Title, lang, err))
		return out(ResultProviderError, "kept the old subtitle — "+shortErr(err, 140)), false
	}
	score := c.Score
	f := SubtitleFile{Path: dst, Lang: lang, Variant: written, Source: SourceOpenSubtitles, Provider: providerOpenSubtitles,
		ProviderFileID: c.FileID, ReleaseName: c.Release, HashMatch: c.HashMatch, Score: &score}
	if chk, ok := ref.audio.verdict(lang, c.FileID); ok {
		chk.apply(&f)
	}
	s.recordFile(ctx, job, ref, f)
	s.sidecarChanged(job.Kind, ref.Path, row.Path, SidecarRemoved)
	old := "the AI subtitle"
	if row.Source == SourceOpenSubtitles {
		old = "the earlier download"
		if row.Score != nil {
			old = fmt.Sprintf("the earlier download (score %d)", *row.Score)
		}
	}
	what := fmt.Sprintf("a better download (score %d)", c.Score)
	if c.HashMatch {
		what = "a download made for this exact file"
	}
	s.log.Info("subtitle upgraded", "media", ref.MediaKey, "lang", lang, "old_source", row.Source, "file_id", c.FileID,
		"score", c.Score, "hash_match", c.HashMatch)
	return withScore(out(ResultOK, "replaced "+old+" with "+what+"; the old one is in the recycle bin"), c), false
}

func withScore(o Outcome, c ScoredCandidate) Outcome {
	o.Score, o.Reasons = c.Score, c.Reasons
	return o
}

// upgradeErr types a provider failure. A spent quota, a rate limit or a rejected login
// stops the job's other languages too; anything else is this language's alone.
func upgradeErr(lang string, err error) (Outcome, bool) {
	var rl *RateLimitError
	switch {
	case errors.Is(err, ErrQuotaExhausted):
		return mkOutcome(lang, RungUpgrade, ResultQuota, "quota spent"), true
	case errors.As(err, &rl), errors.Is(err, ErrRateLimited):
		return mkOutcome(lang, RungUpgrade, ResultRateLimited, "OpenSubtitles rate limit"), true
	case errors.Is(err, ErrAuthFailed):
		return mkOutcome(lang, RungUpgrade, ResultAuthFailed, err.Error()), true
	}
	return mkOutcome(lang, RungUpgrade, ResultProviderError, "OpenSubtitles error: "+shortErr(err, 100)), false
}

// noBetterYet is the note for a search that found nothing better: when the next look is,
// or that this was the last one.
func noBetterYet(row SubtitleFile, now time.Time, window time.Duration) string {
	created := time.Unix(row.CreatedAt, 0)
	next := nextUpgradeCheck(created, now)
	if next.After(created.Add(window)) {
		return fmt.Sprintf("nothing better in %d days — keeping this one", int(window.Hours()/24))
	}
	return "nothing better yet — next look " + next.Format("Jan 2")
}

// swapSidecar puts data in place of the old sidecar: written beside it under a temporary
// name, the old one to the recycle bin, then the new one renamed into place. A bin that
// won't take the old file stops the swap with nothing changed; if the final rename fails,
// the old file comes back from the bin.
func (s *Service) swapSidecar(ctx context.Context, old SubtitleFile, dst string, data []byte) error {
	// The temp name doesn't end in a subtitle extension, so nothing takes it for a sidecar.
	tmp := dst + library.PartialSuffix
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	at, err := s.retireSidecarTo(ctx, old.Path)
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("the old subtitle couldn't go to the recycle bin: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		if at != "" && !fileExists(old.Path) && os.Rename(at, old.Path) == nil {
			_ = os.Remove(at + library.RecycleMetaExt)
			back := old
			back.ID, back.SupersededAt = 0, 0
			if _, rerr := s.ledger.RecordFile(context.WithoutCancel(ctx), back); rerr != nil {
				s.log.Warn("subtitles: could not record a restored subtitle", "path", old.Path, "err", rerr)
			}
		}
		return err
	}
	return nil
}
