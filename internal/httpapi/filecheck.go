package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/tristenlammi/arrmada/internal/filecheck"
	"github.com/tristenlammi/arrmada/internal/jobs"
)

// The file check (internal/filecheck): what it found inside library files, Find a better
// copy, Ignore, a check of the whole library on demand, and "Replace bad copies
// automatically". The findings also reach Needs you through the attention feed.

// fileCheckTrigger is what a replace the file check started records as its trigger.
const fileCheckTrigger = "filecheck"

// replaceBadCopy is the file check's replace: the release the file came from is
// blocklisted (when known) and a search for another copy starts in the usual queue. The
// file stays where it is until a replacement is imported — a regrab replaces on import,
// it never deletes first — so nothing becomes unwatchable meanwhile. An episode's regrab
// blocklists its own source release.
func (a *api) replaceBadCopy(ctx context.Context, f filecheck.Stored) error {
	if a.deps.Automation == nil {
		return errors.New("searching isn't available")
	}
	switch f.Kind {
	case filecheck.KindMovie:
		id := f.OwnerID
		if f.Release != "" {
			if err := a.deps.Automation.Blocklist(ctx, id, f.Release, "", "", "bad copy: "+fileCheckReason(f.Code)); err != nil {
				return fmt.Errorf("blocklist: %w", err)
			}
			a.deps.Movies.AddEvent(ctx, id, "blocklisted", f.Release+" — "+f.Detail)
		}
		spec := a.movieRegrabJob(id)
		spec.Trigger = fileCheckTrigger
		_, err := a.enqueueMovie(nil, id, spec)
		return err
	case filecheck.KindEpisode:
		seriesID, season, episode := f.OwnerID, f.Season, f.Episode
		_, _, err := a.submit(nil, jobs.Spec{
			Kind: "series.regrab-episode", Target: fmt.Sprintf("series:%d:s%de%d", seriesID, season, episode),
			Class: jobs.ClassIndexerSearch, Timeout: 5 * time.Minute, Trigger: fileCheckTrigger,
			Fn: errFn(func(ctx context.Context) error {
				return a.deps.Automation.RegrabEpisode(ctx, seriesID, season, episode)
			})})
		return err
	}
	return fmt.Errorf("unknown kind %q", f.Kind)
}

// fileCheckReason is a finding code as the blocklist's reason.
func fileCheckReason(code string) string {
	switch code {
	case filecheck.CodeSuspectedCam:
		return "probably a cam"
	case filecheck.CodeNoAudio:
		return "no sound"
	case filecheck.CodeSilentAudio:
		return "silent sound track"
	case filecheck.CodeUnreadable:
		return "damaged or cut short"
	case filecheck.CodeWrongLanguage:
		return "wrong audio language"
	}
	return code
}

type fileChecksPayload struct {
	Findings    []filecheck.Problem `json:"findings"`
	AutoReplace bool                `json:"auto_replace"`
}

func (a *api) fileCheckOr503(w http.ResponseWriter) bool {
	if a.deps.FileCheck == nil {
		a.writeError(w, http.StatusServiceUnavailable, "the file check isn't available")
		return false
	}
	return true
}

// handleFileChecks lists the standing findings (?all=1 adds the ignored ones) and the
// automatic-replace setting.
func (a *api) handleFileChecks(w http.ResponseWriter, r *http.Request) {
	if !a.fileCheckOr503(w) {
		return
	}
	list, err := a.deps.FileCheck.Findings(r.Context(), r.URL.Query().Get("all") == "1")
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "couldn't read the file checks")
		return
	}
	a.writeFileChecks(w, r, list)
}

func (a *api) writeFileChecks(w http.ResponseWriter, r *http.Request, list []filecheck.Problem) {
	if list == nil {
		list = []filecheck.Problem{}
	}
	a.writeJSON(w, http.StatusOK, fileChecksPayload{Findings: list, AutoReplace: a.deps.FileCheck.AutoReplaceOn(r.Context())})
}

// handleMovieFileChecks and handleSeriesFileChecks list one title's standing findings, for
// the badge on its detail page.
func (a *api) handleMovieFileChecks(w http.ResponseWriter, r *http.Request) {
	a.titleFileChecks(w, r, filecheck.KindMovie)
}

func (a *api) handleSeriesFileChecks(w http.ResponseWriter, r *http.Request) {
	a.titleFileChecks(w, r, filecheck.KindEpisode)
}

func (a *api) titleFileChecks(w http.ResponseWriter, r *http.Request, kind string) {
	id, ok := a.pathID(w, r)
	if !ok || !a.fileCheckOr503(w) {
		return
	}
	list, err := a.deps.FileCheck.FindingsFor(r.Context(), kind, id)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "couldn't read the file checks")
		return
	}
	a.writeFileChecks(w, r, list)
}

// handleFileCheckRun checks the library now, as a job: files new or changed since their
// last check, or every file with {"all": true}.
func (a *api) handleFileCheckRun(w http.ResponseWriter, r *http.Request) {
	if !a.fileCheckOr503(w) {
		return
	}
	var req struct {
		All bool `json:"all"`
	}
	if r.ContentLength > 0 && !a.decodeJSON(w, r, &req) {
		return
	}
	fc, all := a.deps.FileCheck, req.All
	jobID, existing, ok := a.submitOr503(w, r, jobs.Spec{Kind: "filecheck.pass", Target: "library", Class: jobs.ClassLibraryScan, Timeout: 24 * time.Hour,
		Fn: func(ctx context.Context, _ *jobs.Progress) (any, error) {
			res, err := fc.Pass(ctx, all)
			return res, err
		}})
	if !ok {
		return
	}
	a.accepted(w, jobID, existing, map[string]any{"status": "checking"})
}

// handleFileCheckReplace is Find a better copy.
func (a *api) handleFileCheckReplace(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok || !a.fileCheckOr503(w) {
		return
	}
	f, err := a.deps.FileCheck.ReplaceNow(r.Context(), id)
	switch {
	case errors.Is(err, filecheck.ErrNotFound):
		a.writeError(w, http.StatusNotFound, "that problem is gone — the file may have been replaced already")
		return
	case err != nil:
		a.deps.Log.Warn("file check: couldn't start a replacement", "finding", id, "err", err)
		a.writeError(w, http.StatusServiceUnavailable, "couldn't start the search just now — try again in a moment")
		return
	}
	msg := "Looking for a better copy."
	if f.Release != "" {
		msg = "Blocklisted that release and looking for a better copy."
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"status": "searching", "message": msg})
}

// handleFileCheckDismiss is Ignore.
func (a *api) handleFileCheckDismiss(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok || !a.fileCheckOr503(w) {
		return
	}
	if err := a.deps.FileCheck.Dismiss(r.Context(), id); err != nil {
		if errors.Is(err, filecheck.ErrNotFound) {
			a.writeError(w, http.StatusNotFound, "that problem is gone")
			return
		}
		a.writeError(w, http.StatusInternalServerError, "couldn't ignore it")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleFileCheckSettings saves "Replace bad copies automatically".
func (a *api) handleFileCheckSettings(w http.ResponseWriter, r *http.Request) {
	if !a.fileCheckOr503(w) {
		return
	}
	var req struct {
		AutoReplace *bool `json:"auto_replace"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if req.AutoReplace == nil {
		a.writeError(w, http.StatusBadRequest, "auto_replace is required")
		return
	}
	if err := a.deps.FileCheck.SetAutoReplace(r.Context(), *req.AutoReplace); err != nil {
		a.writeError(w, http.StatusInternalServerError, "couldn't save the setting")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"auto_replace": *req.AutoReplace})
}
