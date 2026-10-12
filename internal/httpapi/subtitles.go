package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/tristenlammi/arrmada/internal/jobs"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/subtitles"
)

// handleSubtitleModels returns local-AI (whisper) binary + model status.
func (a *api) handleSubtitleModels(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, http.StatusOK, a.deps.Subtitles.WhisperStatus(r.Context()))
}

// handleSubtitleDownloadModel starts a background download of a whisper model.
func (a *api) handleSubtitleDownloadModel(w http.ResponseWriter, r *http.Request) {
	if err := a.deps.Subtitles.DownloadModel(r.PathValue("name")); err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.writeJSON(w, http.StatusAccepted, map[string]any{"status": "downloading"})
}

// handleSubtitleJobs returns recent + active subtitle-ensure jobs (polled for the Queue tab).
func (a *api) handleSubtitleJobs(w http.ResponseWriter, r *http.Request) {
	jobs := a.deps.Subtitles.Jobs()
	if jobs == nil {
		jobs = []subtitles.Job{}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

// handleSubtitleCancelJob stops one job: drops it if queued, kills it if running.
func (a *api) handleSubtitleCancelJob(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	if err := a.deps.Subtitles.Cancel(id); err != nil {
		a.writeError(w, http.StatusConflict, err.Error())
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"status": "cancelling"})
}

// handleSubtitleClearQueue drops every queued (not yet started) job.
func (a *api) handleSubtitleClearQueue(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, http.StatusOK, map[string]any{"cleared": a.deps.Subtitles.ClearQueue()})
}

// handleSubtitleLogs returns the recent Subtitles activity console lines.
func (a *api) handleSubtitleLogs(w http.ResponseWriter, r *http.Request) {
	logs := a.deps.Subtitles.Logs()
	if logs == nil {
		logs = []subtitles.LogLine{}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"lines": logs})
}

// handleSubtitleHistory pages the attempt ledger, newest first: every (language, rung) a
// job tried and its typed outcome, kept across restarts. ?media_key= ("m:<id>" or
// "e:<series>:<season>:<episode>") and ?run_id= narrow it; ?before= is the next_before
// of the previous page.
func (a *api) handleSubtitleHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	aq := subtitles.AttemptQuery{MediaKey: q.Get("media_key"), RunID: q.Get("run_id"), Limit: 50}
	if v := q.Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			a.writeError(w, http.StatusBadRequest, "before must be an attempt id")
			return
		}
		aq.Before = n
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			a.writeError(w, http.StatusBadRequest, "limit must be a positive number")
			return
		}
		aq.Limit = min(n, 500)
	}
	rows, err := a.deps.Subtitles.Ledger().Attempts(r.Context(), aq)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not read the subtitle history")
		return
	}
	if rows == nil {
		rows = []subtitles.Attempt{}
	}
	var next int64
	if len(rows) == aq.Limit {
		next = rows[len(rows)-1].ID
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"attempts": rows, "next_before": next})
}

// handleSubtitleQueueMovie queues a subtitle-ensure job for one movie; ?redo=1 replaces
// the sidecars already there.
func (a *api) handleSubtitleQueueMovie(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	job, err := a.deps.Subtitles.QueueMovie(r.Context(), id, r.URL.Query().Get("redo") == "1", subtitles.PrioManual)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.writeJSON(w, http.StatusAccepted, job)
}

// handleSubtitleQueueEpisode queues a subtitle-ensure job for one TV episode (?redo=1 as
// for movies).
func (a *api) handleSubtitleQueueEpisode(w http.ResponseWriter, r *http.Request) {
	seriesID, ok := a.pathValueID(w, r, "series")
	if !ok {
		return
	}
	season, err := strconv.Atoi(r.PathValue("season"))
	if err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid season")
		return
	}
	episode, err := strconv.Atoi(r.PathValue("episode"))
	if err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid episode")
		return
	}
	job, err := a.deps.Subtitles.QueueEpisode(r.Context(), seriesID, season, episode, r.URL.Query().Get("redo") == "1", subtitles.PrioManual)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.writeJSON(w, http.StatusAccepted, job)
}

// handleSubtitleSweep queues an ensure job for every file missing a kept-language subtitle.
func (a *api) handleSubtitleSweep(w http.ResponseWriter, r *http.Request) {
	media := "movies"
	if r.URL.Query().Get("media") == "tv" {
		media = "tv"
	}
	n, err := a.deps.Subtitles.SweepMissing(r.Context(), media)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not start the sweep")
		return
	}
	a.writeJSON(w, http.StatusAccepted, map[string]any{"queued": n})
}

func (a *api) handleGetSubtitleSettings(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, http.StatusOK, a.deps.Subtitles.GetSettings(r.Context()))
}

func (a *api) handleUpdateSubtitleSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MoviesAuto *bool     `json:"movies_auto"`
		SeriesAuto *bool     `json:"series_auto"`
		Languages  *[]string `json:"languages"`
		MinScore   *int      `json:"min_score"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if req.MinScore != nil {
		if err := a.deps.Subtitles.SetMinScore(r.Context(), *req.MinScore); err != nil {
			if errors.Is(err, subtitles.ErrBadMinScore) {
				a.writeError(w, http.StatusBadRequest, "Minimum match score must be between 0 and 100.")
				return
			}
			a.writeError(w, http.StatusInternalServerError, "could not save subtitle settings")
			return
		}
	}
	var langs []string
	if req.Languages != nil {
		langs = *req.Languages
	}
	if err := a.deps.Subtitles.SetSettings(r.Context(), req.MoviesAuto, req.SeriesAuto, langs); err != nil {
		if errors.Is(err, subtitles.ErrUnknownLanguage) {
			a.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.writeError(w, http.StatusInternalServerError, "could not save subtitle settings")
		return
	}
	a.writeJSON(w, http.StatusOK, a.deps.Subtitles.GetSettings(r.Context()))
}

// handleSubtitleLibrary returns per-file subtitle coverage for the Subtitles Library tab,
// computed against the kept languages: movies (the default), TV per show (group=series),
// or one show's episodes (series=<id>).
func (a *api) handleSubtitleLibrary(w http.ResponseWriter, r *http.Request) {
	media := "movies"
	if r.URL.Query().Get("media") == "tv" {
		media = "tv"
	}
	q := r.URL.Query()
	// TV grouped by show. The flat list is one row per EPISODE, each probed before the
	// page can render — at library scale that's tens of thousands of rows and a probe
	// sweep per page load. Shows first, episodes only for the one you open.
	if media == "tv" && q.Get("group") == "series" {
		// From the last library pass, not a fresh walk — see librarySnapshot.
		groups, scanned := a.deps.Subtitles.SnapshotGroups()
		if groups == nil {
			groups = []subtitles.SeriesGroup{}
		}
		a.writeJSON(w, http.StatusOK, map[string]any{"groups": groups, "scanning": !scanned, "stale": a.deps.Subtitles.Stale()})
		return
	}
	if media == "tv" && q.Get("series") != "" {
		id, err := strconv.ParseInt(q.Get("series"), 10, 64)
		if err != nil {
			a.writeError(w, http.StatusBadRequest, "invalid series id")
			return
		}
		list, err := a.deps.Subtitles.SeriesEpisodes(r.Context(), id)
		if err != nil {
			a.writeError(w, http.StatusInternalServerError, "could not load that show's episodes")
			return
		}
		a.writeJSON(w, http.StatusOK, map[string]any{"items": list})
		return
	}

	if media == "tv" {
		// The flat list was one probed row per episode — tens of thousands of probes for
		// one page load. Nothing in the app asks for it any more.
		a.writeError(w, http.StatusBadRequest, "use group=series or series=<id>")
		return
	}
	list, scanned := a.deps.Subtitles.SnapshotMovies()
	if list == nil {
		list = []subtitles.FileSubs{}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"items": list, "scanning": !scanned, "stale": a.deps.Subtitles.Stale()})
}

// handleSubtitleQueueSeries queues subtitle-ensure jobs for every episode of a show
// still missing a kept language.
func (a *api) handleSubtitleQueueSeries(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	n, err := a.deps.Subtitles.QueueSeries(r.Context(), id)
	if err != nil {
		a.writeError(w, http.StatusNotFound, "series not found")
		return
	}
	a.writeJSON(w, http.StatusAccepted, map[string]any{"queued": n})
}

// handleSubtitleCoverage returns the Overview's totals from the last library pass.
func (a *api) handleSubtitleCoverage(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, http.StatusOK, a.deps.Subtitles.Coverage())
}

// handleSubtitleRescan starts a fresh library pass in the background.
func (a *api) handleSubtitleRescan(w http.ResponseWriter, r *http.Request) {
	// Not the request's context: the pass outlives the response.
	started := a.deps.Subtitles.Rescan(context.WithoutCancel(r.Context()))
	a.writeJSON(w, http.StatusAccepted, map[string]any{"started": started})
}

// orphanError maps an orphan action's error to its HTTP answer.
func (a *api) orphanError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, movies.ErrNotFound):
		a.writeError(w, http.StatusNotFound, "movie not found")
	case errors.Is(err, subtitles.ErrOrphanTargetExists), errors.Is(err, subtitles.ErrJobBusy):
		a.writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, subtitles.ErrNotOrphan), errors.Is(err, subtitles.ErrBadLanguage),
		errors.Is(err, subtitles.ErrNotKept), errors.Is(err, subtitles.ErrForcedOrphan):
		a.writeError(w, http.StatusBadRequest, err.Error())
	default:
		a.deps.Log.Warn("subtitles: orphan action failed", "err", err)
		a.writeError(w, http.StatusInternalServerError, "couldn't do that — see the Subtitles log")
	}
}

// handleSubtitleAdoptOrphan renames one of a movie's orphaned subtitles to pair with its
// file. name must be one of the movie's orphans as the folder is now; it is never a path.
func (a *api) handleSubtitleAdoptOrphan(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Name    string  `json:"name"`
		Lang    *string `json:"lang"`
		Variant *string `json:"variant"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	name, err := a.deps.Subtitles.AdoptOrphan(r.Context(), id, req.Name, req.Lang, req.Variant)
	if err != nil {
		a.orphanError(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"name": name})
}

// handleSubtitleSyncCheck measures how one language's sidecar lines up with its video's
// speech and stores the result as its Health. The file is named by catalog identity
// ({kind, movie_id | series_id+season+episode, lang, variant}); its path is found server
// side, never taken from the request. Reading the soundtrack takes a while, so the check
// runs as a job: an unknown title or a missing sidecar is answered at once, the
// measurement follows.
func (a *api) handleSubtitleSyncCheck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		subtitles.MediaRef
		Lang    string `json:"lang"`
		Variant string `json:"variant"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	t, err := a.deps.Subtitles.SyncTarget(r.Context(), req.MediaRef, req.Lang, req.Variant)
	switch {
	case errors.Is(err, subtitles.ErrNoVideo), errors.Is(err, subtitles.ErrNoSidecar):
		a.writeError(w, http.StatusNotFound, err.Error())
		return
	case errors.Is(err, subtitles.ErrNotSRT):
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		a.writeError(w, http.StatusInternalServerError, "could not find that subtitle")
		return
	}
	jobID, existing, ok := a.submitOr503(w, r, jobs.Spec{Kind: "subtitles.sync-check", Target: t.Key, Timeout: 30 * time.Minute,
		Fn: func(ctx context.Context, p *jobs.Progress) (any, error) {
			p.SetMessage("Reading the audio of " + t.Title + "…")
			rep, err := a.deps.Subtitles.CheckSync(ctx, req.MediaRef, req.Lang, req.Variant)
			if err != nil {
				return nil, err
			}
			msg := t.Name + ": " + rep.Health.Note
			if !rep.Judged {
				msg = t.Name + ": too little speech or too few lines to judge its sync"
			}
			p.SetMessage(msg)
			return rep, nil
		}})
	if !ok {
		return
	}
	a.accepted(w, jobID, existing, map[string]any{"status": "checking", "file": t.Name})
}

// handleSubtitleReplaceOrphan queues a fresh subtitle for an orphan's language; the
// orphan goes to the recycle bin only once that subtitle has been written.
func (a *api) handleSubtitleReplaceOrphan(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	job, err := a.deps.Subtitles.ReplaceOrphan(r.Context(), id, req.Name)
	if err != nil {
		a.orphanError(w, err)
		return
	}
	a.writeJSON(w, http.StatusAccepted, job)
}

// handleSubtitleBulkOrphans adopts or replaces the orphans of every movie that has any
// (or of the listed movies), and says what it did and what it left alone.
func (a *api) handleSubtitleBulkOrphans(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action   string  `json:"action"`
		MovieIDs []int64 `json:"movie_ids"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if req.Action != "adopt" && req.Action != "replace" {
		a.writeError(w, http.StatusBadRequest, `action must be "adopt" or "replace"`)
		return
	}
	if _, scanned := a.deps.Subtitles.SnapshotMovies(); !scanned {
		a.writeError(w, http.StatusConflict, "the library is still being scanned — try again in a few minutes")
		return
	}
	res, err := a.deps.Subtitles.BulkOrphans(r.Context(), req.Action, req.MovieIDs)
	if err != nil {
		a.orphanError(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, res)
}
