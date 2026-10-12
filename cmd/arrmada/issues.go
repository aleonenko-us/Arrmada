package main

import (
	"context"
	"log/slog"

	"github.com/tristenlammi/arrmada/internal/issues"
	"github.com/tristenlammi/arrmada/internal/subtitles"
)

// subtitleOutcomes tells the issues service what became of the subtitle job a subtitles
// fix queued (issues.Service.Sweep polls it every minute). Job ids are the saved queue's
// row ids, so they survive a restart and never repeat: the job found is the one the fix
// queued — back in the queue if the app restarted under it. The file check only guards
// against a mixed-up id.
//
// The job-done hook below settles a fix the moment its job ends; this poll is the
// fallback for a job dropped from the queue before it ran.
type subtitleOutcomes struct {
	ctx context.Context
	svc *subtitles.Service
	log *slog.Logger
}

func (s subtitleOutcomes) Outcome(jobID int64, t issues.Target, _ int64) issues.SubtitleOutcome {
	j, ok, err := s.svc.Job(s.ctx, jobID)
	if err != nil {
		// Unreadable isn't gone: report it pending, so the next minute's poll asks again
		// rather than reopening the problem over a database hiccup.
		s.log.Warn("issues: could not read a subtitle job", "job_id", jobID, "err", err)
		return issues.SubtitleOutcome{Found: true, Pending: true}
	}
	if !ok || !jobIsFor(j, t) {
		return issues.SubtitleOutcome{}
	}
	switch j.State {
	case subtitles.StateQueued, subtitles.StateRunning:
		return issues.SubtitleOutcome{Found: true, Pending: true}
	case subtitles.StateDone:
		return issues.SubtitleOutcome{Found: true, Done: true, Note: j.Note}
	default: // skipped, failed, cancelled: nothing was made
		return issues.SubtitleOutcome{Found: true, Note: j.Note}
	}
}

// subtitleJobDone settles a reported problem's subtitles fix the moment its job finishes
// (Subtitles' job-done hook), instead of at the next minute's poll.
func subtitleJobDone(ctx context.Context, svc *issues.Service, log *slog.Logger) func(subtitles.Job) {
	return func(j subtitles.Job) {
		t := issues.Target{MediaType: issues.MediaMovie, LibraryID: j.MovieID}
		if j.Kind == "episode" {
			t = issues.Target{MediaType: issues.MediaSeries, LibraryID: j.SeriesID, Season: j.Season, Episode: j.Episode}
		}
		o := issues.SubtitleOutcome{Found: true, Done: j.State == subtitles.StateDone, Note: j.Note}
		if err := svc.OnSubtitleJobDone(ctx, t, j.ID, o); err != nil {
			log.Warn("issues: could not settle a subtitles fix", "job_id", j.ID, "err", err)
		}
	}
}

// jobIsFor reports whether a subtitle job is for an issue's movie or episode.
func jobIsFor(j subtitles.Job, t issues.Target) bool {
	if t.MediaType == issues.MediaSeries {
		return j.Kind == "episode" && j.SeriesID == t.LibraryID && j.Season == t.Season && j.Episode == t.Episode
	}
	return j.Kind == "movie" && j.MovieID == t.LibraryID
}
