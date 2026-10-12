package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/issues"
	"github.com/tristenlammi/arrmada/internal/jobs"
	"github.com/tristenlammi/arrmada/internal/subtitles"
)

// One-click fixes for a reported problem (staff). The issue moves to "fixing" and resolves
// itself when the new file lands or the subtitle job finishes (internal/issues/fix.go).
// The page confirms a replace before it gets here: the current file goes to the recycle
// bin when the new one is imported.

// fixStartError is a fix that couldn't be started; its message says what to do.
type fixStartError struct{ msg string }

func (e *fixStartError) Error() string { return e.msg }

// handleFixIssue starts a fix: {"action": "replace" | "subtitles"}.
func (a *api) handleFixIssue(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	if a.deps.Issues == nil {
		a.writeError(w, http.StatusServiceUnavailable, "reported problems aren't available")
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	if !a.decodeJSONLimit(w, r, &body, 4<<10) {
		return
	}
	iss, err := a.deps.Issues.StartFix(r.Context(), id, body.Action, a.issueFix(r, body.Action))
	var fe *fixStartError
	if errors.As(err, &fe) {
		a.writeError(w, http.StatusServiceUnavailable, fe.msg)
		return
	}
	if a.writeIssueError(w, err) {
		return
	}
	a.writeJSON(w, http.StatusOK, iss)
}

// issueFix is the work behind an action, or nil when what it needs isn't wired.
func (a *api) issueFix(r *http.Request, action string) issues.FixFunc {
	switch action {
	case issues.ActionReplace:
		if a.deps.Automation == nil {
			return nil
		}
		return func(ctx context.Context, i issues.Issue, cur issues.Item) (int64, string, error) {
			if i.MediaType == issues.MediaSeries {
				return 0, "Blocklisted this episode's copy; searching for another.", a.replaceEpisode(r, i)
			}
			return 0, "Blocklisted this copy; searching for another.", a.replaceMovie(ctx, r, i, cur)
		}
	case issues.ActionSubtitles:
		if a.deps.Subtitles == nil {
			return nil
		}
		return func(ctx context.Context, i issues.Issue, _ issues.Item) (int64, string, error) {
			// Redo: what's there now is what was reported, so every kept language is made
			// again rather than only the missing ones.
			var job *subtitles.Job
			var err error
			if i.MediaType == issues.MediaSeries {
				job, err = a.deps.Subtitles.QueueEpisode(ctx, i.LibraryID, i.Season, i.Episode, true, subtitles.PrioManual)
			} else {
				job, err = a.deps.Subtitles.QueueMovie(ctx, i.LibraryID, true, subtitles.PrioManual)
			}
			if err != nil {
				return 0, "", &fixStartError{"couldn't queue the subtitle job: " + err.Error()}
			}
			return job.ID, "Making the subtitles again.", nil
		}
	}
	return nil
}

// replaceMovie blocklists the release the movie's file came from and re-grabs it, through
// the movie search queue like the movie page's Re-grab.
func (a *api) replaceMovie(ctx context.Context, r *http.Request, i issues.Issue, cur issues.Item) error {
	release := cur.Release
	if release == "" {
		// Imported before the release name was kept: the file's name is all there is. It
		// rarely matches a release title, so the grab may find the same copy again.
		release = strings.TrimSuffix(filepath.Base(cur.Path), filepath.Ext(cur.Path))
	}
	if err := a.deps.Automation.Blocklist(ctx, i.LibraryID, release, "", "", "reported problem"); err != nil {
		return &fixStartError{"couldn't blocklist the current copy — try again"}
	}
	if a.deps.Movies != nil {
		a.deps.Movies.AddEvent(ctx, i.LibraryID, "blocklisted", release)
	}
	if _, err := a.enqueueMovie(r, i.LibraryID, a.movieRegrabJob(i.LibraryID)); err != nil {
		return &fixStartError{"blocklisted the copy, but couldn't start the search — try again in a moment"}
	}
	return nil
}

// replaceEpisode replaces one episode: RegrabEpisode blocklists its source release and
// grabs the best other copy of just that episode.
func (a *api) replaceEpisode(r *http.Request, i issues.Issue) error {
	if err := (automation.SeriesScope{Season: i.Season, Episode: i.Episode, Replace: true}).Validate(); err != nil {
		return &fixStartError{err.Error()}
	}
	id, season, episode := i.LibraryID, i.Season, i.Episode
	_, _, err := a.submit(r, jobs.Spec{
		Kind: "series.regrab-episode", Target: fmt.Sprintf("series:%d:s%de%d", id, season, episode),
		Class: jobs.ClassIndexerSearch, Timeout: 5 * time.Minute,
		Fn: interactive(errFn(func(ctx context.Context) error { return a.deps.Automation.RegrabEpisode(ctx, id, season, episode) }))})
	if err != nil {
		return &fixStartError{"couldn't start the search just now — try again in a moment"}
	}
	return nil
}
