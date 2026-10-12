package main

import (
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

// jobIsFor reports whether a subtitle job is for an issue's movie or episode.
func jobIsFor(j subtitles.Job, t issues.Target) bool {
	if t.MediaType == issues.MediaSeries {
		return j.Kind == "episode" && j.SeriesID == t.LibraryID && j.Season == t.Season && j.Episode == t.Episode
	}
	return j.Kind == "movie" && j.MovieID == t.LibraryID
}
