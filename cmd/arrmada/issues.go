package main

import (
	"context"
	"log/slog"

	"github.com/tristenlammi/arrmada/internal/issues"
	"github.com/tristenlammi/arrmada/internal/subtitles"
)

// subtitleOutcomes tells the issues service what became of the subtitle job a subtitles
// fix queued, by reading the Subtitles queue's recent jobs (issues.Service.Sweep polls it
// every minute). The job must be the one the fix queued: the same id and file, queued no
// earlier than the fix (the queue is in memory and its ids restart with the app).
//
// Once Subtitles has a job-completion callback, it can call issues.Service.OnSubtitleJobDone
// directly and this poll can go.
type subtitleOutcomes struct{ svc *subtitles.Service }

func (s subtitleOutcomes) Outcome(jobID int64, t issues.Target, since int64) issues.SubtitleOutcome {
	for _, j := range s.svc.Jobs() {
		if j.ID != jobID || j.At < since-60 || !jobIsFor(j, t) {
			continue
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
	return issues.SubtitleOutcome{}
}

// subtitleJobDone settles a reported problem's subtitles fix the moment its job finishes
// (Subtitles' job-done hook), instead of at the next minute's poll. The poll above stays
// as the fallback for jobs dropped from the queue and for restarts.
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
