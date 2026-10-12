package issues

import (
	"context"
	"errors"
	"time"
)

// One-click fixes and how an issue resolves itself.
//
// Staff start a fix from the issue (StartFix): "replace" blocklists the release the current
// file came from and grabs another copy; "subtitles" queues a subtitle job that makes the
// kept languages again. The issue then waits in "fixing", remembering the file that was
// there (action_file, action_size) and the subtitle job (action_job), and resolves itself
// — telling the reporters — when:
//
//   - a new file lands for exactly that item: OnMovieImported (the default track) and
//     OnSeriesImported (only the episodes the import placed), run as outbox consumers on
//     movie.imported and series.imported, so a busy moment or a restart can't lose it;
//   - the subtitle job finishes having made subtitles: OnSubtitleJobDone. Today Sweep
//     polls the subtitles queue for it (SubtitleJobs.Outcome); a job-completion callback
//     can call OnSubtitleJobDone directly instead;
//   - the backstop (Sweep, every minute) finds the item's file is no longer the one the
//     fix started from — a missed signal.
//
// A subtitle job that ends without making any, or can no longer be found, puts the issue
// back to open with a note saying so; so does a fix that has brought nothing in a week.

// Resolutions an issue closes itself with: what the reporters are told.
const (
	ResolvedNewCopy      = "A new copy was downloaded."
	ResolvedNewSubtitles = "New subtitles were made."
)

// fixStaleAfter is how long a fix may wait for a new file before the issue goes back to
// open: a replace whose search found nothing would otherwise sit in "fixing" forever.
const fixStaleAfter = 7 * 24 * time.Hour

// FixFunc starts a fix for an issue's item, whose file is cur. It returns the subtitle job
// it queued (0 for none) and a short note for staff ("Searching for another copy").
type FixFunc func(ctx context.Context, i Issue, cur Item) (jobID int64, note string, err error)

// SubtitleOutcome is what became of the subtitle job a subtitles fix started.
type SubtitleOutcome struct {
	// Found: the job is known. Pending: still queued or running. Done: it finished and made
	// subtitles. Found and neither: it ended without making any, and Note says why.
	Found, Pending, Done bool
	Note                 string
}

// SubtitleJobs answers what became of a subtitle job, for the poll in Sweep. since is when
// the fix queued it (unix seconds). Subtitle job ids are saved with the queue and never
// repeat, so an implementation may ignore it.
type SubtitleJobs interface {
	Outcome(jobID int64, t Target, since int64) SubtitleOutcome
}

// SetSubtitles wires the subtitle-job poll.
func (s *Service) SetSubtitles(j SubtitleJobs) { s.subs = j }

func validAction(a string) bool { return a == ActionReplace || a == ActionSubtitles }

// StartFix runs a fix for an open (or already fixing) issue and moves it to "fixing". The
// item must still have a file: that's what is being replaced, or given subtitles.
func (s *Service) StartFix(ctx context.Context, id int64, action string, run FixFunc) (Issue, error) {
	if !validAction(action) {
		return Issue{}, invalid("action must be %s or %s", ActionReplace, ActionSubtitles)
	}
	if run == nil {
		return Issue{}, ErrNoFix
	}
	i, err := getIssue(ctx, s.db, id)
	if err != nil {
		return Issue{}, err
	}
	if i.Status != StatusOpen && i.Status != StatusFixing {
		return Issue{}, ErrClosed
	}
	cur, err := s.lib.Current(ctx, i.Target())
	if err != nil {
		return Issue{}, err
	}
	if cur.Path == "" {
		return Issue{}, ErrNoFile
	}
	job, note, err := run(ctx, i, cur)
	if err != nil {
		return Issue{}, err
	}
	now := s.now().Unix()
	res, err := s.db.ExecContext(ctx, `UPDATE issues SET status = 'fixing', action = ?, action_note = ?, action_file = ?, action_size = ?,
		action_job = ?, action_at = ?, updated_at = ? WHERE id = ? AND status IN ('open', 'fixing')`,
		action, note, cur.Path, cur.Size, job, now, now, id)
	if err != nil {
		return Issue{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Issue{}, ErrClosed // closed while the fix was starting
	}
	if i, err = s.Get(ctx, id); err != nil {
		return Issue{}, err
	}
	s.changed(i)
	s.log.Info("issues: fix started", "issue", id, "action", action, "job", job)
	return i, nil
}

// OnMovieImported resolves the issues being fixed for a movie whose default file was just
// imported. An extra version's import (versionID > 0) isn't the file anyone reported.
// An error means the issues couldn't be read and the outbox should try again.
func (s *Service) OnMovieImported(ctx context.Context, movieID, versionID int64) error {
	if versionID != 0 {
		return nil
	}
	list, err := fixingFor(ctx, s.db, MediaMovie, movieID)
	if err != nil {
		return err
	}
	for _, i := range list {
		s.autoResolve(ctx, i, ResolvedNewCopy)
	}
	return nil
}

// Episode is one (season, episode) an import placed.
type Episode struct{ Season, Episode int }

// OnSeriesImported resolves the issues being fixed for the episodes a show's import just
// placed — those episodes only: an issue on S01E04 isn't resolved by S01E05 arriving.
func (s *Service) OnSeriesImported(ctx context.Context, seriesID int64, eps []Episode) error {
	if len(eps) == 0 {
		return nil
	}
	list, err := fixingFor(ctx, s.db, MediaSeries, seriesID)
	if err != nil {
		return err
	}
	placed := map[Episode]bool{}
	for _, e := range eps {
		placed[e] = true
	}
	for _, i := range list {
		if placed[Episode{i.Season, i.Episode}] {
			s.autoResolve(ctx, i, ResolvedNewCopy)
		}
	}
	return nil
}

// OnSubtitleJobDone settles the issue whose subtitles fix queued jobID for t, once that
// job has finished: resolved when it made subtitles, back to open (with why) when it
// didn't. A job no issue is waiting on is ignored.
func (s *Service) OnSubtitleJobDone(ctx context.Context, t Target, jobID int64, o SubtitleOutcome) error {
	if jobID <= 0 || o.Pending {
		return nil
	}
	list, err := fixingFor(ctx, s.db, t.MediaType, t.LibraryID)
	if err != nil {
		return err
	}
	for _, i := range list {
		if i.Action != ActionSubtitles || i.ActionJob != jobID || i.Season != t.Season || i.Episode != t.Episode {
			continue
		}
		s.settleSubtitles(ctx, i, o)
	}
	return nil
}

func (s *Service) settleSubtitles(ctx context.Context, i Issue, o SubtitleOutcome) {
	switch {
	case o.Done:
		s.autoResolve(ctx, i, ResolvedNewSubtitles)
	case !o.Found:
		s.reopen(ctx, i, "Couldn't find the subtitle job any more, so it's unclear whether it finished. Check the subtitles, then mark it fixed or try again.")
	default:
		note := "The subtitle job didn't make any subtitles"
		if o.Note != "" {
			note += ": " + o.Note
		}
		s.reopen(ctx, i, note)
	}
}

// autoResolve closes an issue a fix has dealt with and tells the reporters.
func (s *Service) autoResolve(ctx context.Context, i Issue, resolution string) {
	ok, err := closeIssue(ctx, s.db, i.ID, StatusResolved, resolution, 0, "", s.now().Unix())
	if err != nil {
		s.log.Warn("issues: couldn't resolve a fixed issue", "issue", i.ID, "err", err)
		return
	}
	if !ok {
		return // someone closed it meanwhile
	}
	done, err := s.Get(ctx, i.ID)
	if err != nil {
		s.log.Warn("issues: couldn't read a resolved issue", "issue", i.ID, "err", err)
		return // Sweep tells the reporters (told_at is still 0)
	}
	s.changed(done)
	s.tellReporters(ctx, done)
	s.log.Info("issues: resolved by its fix", "issue", i.ID, "resolution", resolution)
}

// reopen puts an issue whose fix didn't work back to open, saying why, for staff to try
// something else. Nobody is told: it isn't fixed yet.
func (s *Service) reopen(ctx context.Context, i Issue, note string) {
	res, err := s.db.ExecContext(ctx, `UPDATE issues SET status = 'open', action_note = ?, action_job = 0, updated_at = ?
		WHERE id = ? AND status = 'fixing'`, note, s.now().Unix(), i.ID)
	if err != nil {
		s.log.Warn("issues: couldn't reopen an issue", "issue", i.ID, "err", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return
	}
	i.Status, i.ActionNote = StatusOpen, note
	s.changed(i)
	s.log.Info("issues: fix didn't work, back to open", "issue", i.ID, "note", note)
}

// checkFixing is Sweep's pass over the issues being fixed: the subtitle-job poll, the
// new-file backstop and the stale-fix timeout.
func (s *Service) checkFixing(ctx context.Context) error {
	list, err := fixing(ctx, s.db)
	if err != nil {
		return err
	}
	now := s.now()
	for _, i := range list {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		t := i.Target()
		// A new file wins whatever the fix was: the copy anyone reported is gone.
		if i.ActionFile != "" {
			cur, err := s.lib.Current(ctx, t)
			switch {
			case errors.Is(err, ErrNotInLibrary):
				// The item was removed from the library; staff can dismiss the issue.
			case err != nil:
				s.log.Warn("issues: couldn't read the item being fixed", "issue", i.ID, "err", err)
				continue
			case newFile(i, cur):
				s.autoResolve(ctx, i, ResolvedNewCopy)
				continue
			}
		}
		if i.Action == ActionSubtitles && i.ActionJob > 0 && s.subs != nil {
			if o := s.subs.Outcome(i.ActionJob, t, i.ActionAt); !o.Pending {
				s.settleSubtitles(ctx, i, o)
				continue
			}
		}
		if i.ActionAt > 0 && now.Sub(time.Unix(i.ActionAt, 0)) > fixStaleAfter {
			s.reopen(ctx, i, "Nothing new arrived in a week. Try another fix, or close it.")
		}
	}
	return nil
}

// newFile reports whether the item's file is no longer the one the fix started from: a
// different path, or the same path with a different size (both sizes known).
func newFile(i Issue, cur Item) bool {
	if cur.Path == "" {
		return false // gone, not replaced (yet)
	}
	if cur.Path != i.ActionFile {
		return true
	}
	return i.ActionSize > 0 && cur.Size > 0 && cur.Size != i.ActionSize
}
