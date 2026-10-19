package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/jobs"
)

// handleListReviews returns downloads held for admin review (content didn't match
// what they were grabbed for). ?media_type= and ?expected_id= narrow it to one title's
// held downloads (the series page's Numbering tab lists the files it couldn't place).
func (a *api) handleListReviews(w http.ResponseWriter, r *http.Request) {
	list, err := a.deps.Automation.ListReviews(r.Context())
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not list reviews")
		return
	}
	mediaType := r.URL.Query().Get("media_type")
	var expectedID int64
	if v := r.URL.Query().Get("expected_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			a.writeError(w, http.StatusBadRequest, "invalid expected_id")
			return
		}
		expectedID = n
	}
	if mediaType != "" || expectedID > 0 {
		kept := list[:0]
		for _, rv := range list {
			if (mediaType == "" || rv.MediaType == mediaType) && (expectedID == 0 || rv.ExpectedID == expectedID) {
				kept = append(kept, rv)
			}
		}
		list = kept
	}
	// Each review carries its import job, so a page opened (or reloaded) mid-import shows
	// it importing, and one whose last import failed shows why. A job that ended any other
	// way (stopped, or succeeded just as the list was read) says nothing about the review.
	active, _ := a.activeHandImports(r.Context())
	last := a.reviewImportJobs(r.Context())
	out := make([]reviewRow, 0, len(list))
	for _, rv := range list {
		run, running := active[rv.ID]
		prev, hasPrev := last[rv.ID]
		out = append(out, reviewRow{Review: rv, ImportJob: shownImportJob(run, running, prev, hasPrev)})
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"reviews": out})
}

// reviewRow is a review as the list serves it: with its import job, when that's running or
// the last one failed.
type reviewRow struct {
	automation.Review
	ImportJob *jobs.Job `json:"import_job,omitempty"`
}

// handleRejectReview removes the held download (+files), blocklists the release,
// and resolves the review. {"find_another": true} then searches the title it was grabbed
// for again, as a job — the blocklisted release can't be picked.
func (a *api) handleRejectReview(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		FindAnother bool `json:"find_another"`
	}
	if r.ContentLength > 0 && !a.decodeJSON(w, r, &req) { // body optional
		return
	}
	if a.refuseWhileImporting(w, r, id) {
		return
	}
	rv, err := a.deps.Automation.GetReview(r.Context(), id)
	if err == nil {
		err = a.deps.Automation.RejectReview(r.Context(), id)
	}
	if err != nil {
		a.writeReviewError(w, err, "could not reject that download")
		return
	}
	if !req.FindAnother || rv.ExpectedID <= 0 {
		a.writeJSON(w, http.StatusOK, map[string]any{"status": "rejected"})
		return
	}
	spec, ok := a.titleSearchJob(rv.MediaType, rv.ExpectedID)
	if !ok {
		a.writeJSON(w, http.StatusOK, map[string]any{"status": "rejected"})
		return
	}
	var jobID int64
	var existing bool
	if rv.MediaType == "movie" {
		// Movie searches wait their turn in the movie search queue.
		var q automation.MovieQueued
		q, err = a.enqueueMovie(r, rv.ExpectedID, spec)
		jobID, existing = q.JobID, q.Existing
	} else {
		jobID, existing, err = a.submit(r, spec)
	}
	if err != nil {
		// The reject itself happened; only the search didn't start.
		a.writeJSON(w, http.StatusOK, map[string]any{"status": "rejected", "search_error": "couldn't start the search just now — search the title by hand"})
		return
	}
	a.accepted(w, jobID, existing, map[string]any{"status": "rejected", "searching": true})
}

// titleSearchJob is the search job for one library item of any kind.
func (a *api) titleSearchJob(kind string, id int64) (jobs.Spec, bool) {
	switch kind {
	case "movie":
		return a.movieSearchJob(id), true
	case "series":
		return a.seriesSearchJob(id), true
	case "book":
		return a.bookSearchJob(id), true
	case "music":
		return a.albumSearchJob(id), true
	}
	return jobs.Spec{}, false
}

// handleDismissReview resolves a review without touching the download.
func (a *api) handleDismissReview(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	if a.refuseWhileImporting(w, r, id) {
		return
	}
	if err := a.deps.Automation.DismissReview(r.Context(), id); err != nil {
		a.writeReviewError(w, err, "could not dismiss that review")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"status": "dismissed"})
}

// handleRetryReview clears an "import keeps failing" review so the import sweep tries
// the download again now — once the cause (a folder, a full disk) is fixed. It imports
// nothing itself (it settles the review and drops the sweep's back-off), so it answers
// at once; the import shows in the queue as the sweep takes it.
func (a *api) handleRetryReview(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	if a.refuseWhileImporting(w, r, id) {
		return
	}
	if err := a.deps.Automation.RetryReviewImport(r.Context(), id); err != nil {
		a.writeReviewError(w, err, "could not retry that import")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"status": "retrying"})
}

// handleReviewFiles lists the files inside a held download, with what each name says
// about its episode numbering. It reads only the review's own download folder.
func (a *api) handleReviewFiles(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	files, truncated, err := a.deps.Automation.ReviewFiles(r.Context(), id)
	if err != nil {
		a.writeReviewError(w, err, "could not list the download's files")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"files": files, "truncated": truncated})
}

// handleMapReview imports a held series download by mapping its files to episodes by hand:
// {series_id, files:[{rel_path, season, episodes}]}. The mapping is checked before
// anything is touched; the import then runs as a job (202) — even a few files can be a
// long copy onto another drive.
func (a *api) handleMapReview(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		SeriesID int64                    `json:"series_id"`
		Files    []automation.FileMapping `json:"files"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	rv, err := a.deps.Automation.GetReview(r.Context(), id)
	if err != nil {
		a.writeReviewError(w, err, "")
		return
	}
	// The same folder rule as a manual import: only what's inside the downloads or
	// library folders goes into the library.
	if _, err := a.checkImportPath(r.Context(), rv.ContentPath); err != nil {
		a.writeError(w, http.StatusUnprocessableEntity, "This download is outside your downloads and library folders, so it can't be imported from here. "+err.Error())
		return
	}
	plan, err := a.deps.Automation.PlanReviewMap(r.Context(), id, req.SeriesID, req.Files)
	if err != nil {
		a.writeReviewError(w, err, "could not check that mapping")
		return
	}
	title := plan.Title()
	// The same mapping again joins the running import; a different one is refused.
	key, _ := json.Marshal(req)
	a.startHandImport(w, r, handImportRequest{Kind: reviewImportKind, Target: jobTarget("review", id), Hash: rv.Hash,
		Key: "map:" + string(key), Title: title,
		Fn: func(ctx context.Context, p *jobs.Progress) (any, error) {
			n, err := a.deps.Automation.ApplyReviewMap(withImportProgress(ctx, p), plan)
			if err != nil {
				return nil, err // the import's own reason, which the card shows
			}
			res := importedInto(p, title)
			p.SetMessage("Imported " + countOf(n, "episode") + " into " + title + ".")
			res["placed"] = n
			return res, nil
		}}, map[string]any{"background": true})
}

// maxBulkReviews bounds one bulk action: each Reject is a call to the download client.
const maxBulkReviews = 100

// bulkFailure is one review a bulk action couldn't settle, and why.
type bulkFailure struct {
	ID    int64  `json:"id"`
	Error string `json:"error"`
}

// handleBulkReviews dismisses or rejects several reviews at once: {ids, action}. Each is
// settled on its own, so one failure doesn't stop the rest; the answer says which failed.
func (a *api) handleBulkReviews(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs    []int64 `json:"ids"`
		Action string  `json:"action"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if req.Action != "dismiss" && req.Action != "reject" {
		a.writeError(w, http.StatusBadRequest, "action must be dismiss or reject")
		return
	}
	if len(req.IDs) == 0 || len(req.IDs) > maxBulkReviews {
		a.writeError(w, http.StatusBadRequest, "pick between 1 and 100 reviews")
		return
	}
	done, failed := 0, []bulkFailure{}
	importing, _ := a.activeHandImports(r.Context())
	for _, id := range req.IDs {
		if _, ok := importing[id]; ok {
			failed = append(failed, bulkFailure{ID: id, Error: errReviewImporting.Error()})
			continue
		}
		var err error
		if req.Action == "dismiss" {
			err = a.deps.Automation.DismissReview(r.Context(), id)
		} else {
			err = a.deps.Automation.RejectReview(r.Context(), id)
		}
		if err != nil {
			msg := err.Error()
			if reviewErrorStatus(err) == http.StatusInternalServerError {
				msg = "failed — see the log"
				a.deps.Log.Warn("review: bulk action failed", "action", req.Action, "id", id, "err", err)
			}
			failed = append(failed, bulkFailure{ID: id, Error: msg})
			continue
		}
		done++
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"done": done, "failed": failed})
}

// handleReviewTargets lists the library items a review's download could be imported
// into — always items of the review's own kind, filtered by ?q= on title (and author or
// artist for books and albums).
func (a *api) handleReviewTargets(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	list, truncated, err := a.deps.Automation.ReviewTargets(r.Context(), id, r.URL.Query().Get("q"), 0)
	if err != nil {
		a.writeReviewError(w, err, "could not list library items")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"targets": list, "truncated": truncated})
}

// handleImportReview imports a held download into the item it was grabbed for, or
// into a different library item when target_id is given (reassign). target_kind says
// what kind of item target_id is; empty means the review's own kind.
func (a *api) handleImportReview(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		TargetID   int64  `json:"target_id"`
		TargetKind string `json:"target_kind"`
		// ReleaseID (music): the edition of the album to place the files on.
		ReleaseID int64 `json:"release_id"`
	}
	if r.ContentLength > 0 && !a.decodeJSON(w, r, &req) { // body optional; 0 = import into the expected item
		return
	}
	// What can be refused is refused now, with its own status; the import itself (a
	// hardlink, or a copy across drives that can run for many minutes) is a job, so a
	// proxy's request timeout can't cut it off and the card can show how far it got.
	t, err := a.deps.Automation.CheckReviewImport(r.Context(), id, req.TargetID, req.TargetKind)
	if err != nil {
		a.writeReviewError(w, err, "")
		return
	}
	targetID, targetKind, releaseID := req.TargetID, req.TargetKind, req.ReleaseID
	// Import anyway and Choose different… into the same item are the same import.
	a.startHandImport(w, r, handImportRequest{Kind: reviewImportKind, Target: jobTarget("review", id), Hash: t.Review.Hash,
		Key: "import:" + t.Review.MediaType + ":" + strconv.FormatInt(t.Dest, 10), Title: t.Title,
		Fn: func(ctx context.Context, p *jobs.Progress) (any, error) {
			ctx = automation.WithMusicEdition(ctx, releaseID)
			if err := a.deps.Automation.ImportReview(withImportProgress(ctx, p), id, targetID, targetKind); err != nil {
				return nil, err // the import's own reason, which the card shows
			}
			return importedInto(p, t.Title), nil
		}}, nil)
}

// errReviewImporting refuses to reject, dismiss or retry a review while its download is
// being imported: rejecting would delete the files the import is copying.
var errReviewImporting = errors.New("this download is being imported — wait for the import to finish")

// refuseWhileImporting answers 409 when review id has an import running, naming the job
// so the page can follow it. It reports whether it answered.
func (a *api) refuseWhileImporting(w http.ResponseWriter, r *http.Request, id int64) bool {
	active, _ := a.activeHandImports(r.Context())
	j, busy := active[id]
	if !busy {
		return false
	}
	a.alreadyRunning(w, j.ID, errReviewImporting.Error())
	return true
}

// writeReviewError answers a review action's error with the status that says whose
// problem it is. Only a genuine fault is a 500; everything else is something the user
// can act on, and keeps its message. fallback, when set, replaces the raw error text
// on a 500.
func (a *api) writeReviewError(w http.ResponseWriter, err error, fallback string) {
	status := reviewErrorStatus(err)
	msg := err.Error()
	if status == http.StatusInternalServerError && fallback != "" {
		msg = fallback
	}
	a.writeError(w, status, msg)
}

func reviewErrorStatus(err error) int {
	switch {
	case errors.Is(err, automation.ErrReviewNotFound):
		return http.StatusNotFound
	// A review outliving its download is ordinary, not a server fault: the torrent
	// was removed or the folder cleaned up after the item was held. It used to answer
	// 500 with the raw stat error, which told the user nothing actionable.
	case errors.Is(err, automation.ErrDownloadGone):
		return http.StatusGone
	// The module the review belongs to isn't running; turning it on is the fix.
	case errors.Is(err, automation.ErrModuleOff):
		return http.StatusConflict
	// Nothing placeable in the download, a target of the wrong kind, or no target at all
	// are the user's to act on, not server faults — 422 with the reason intact. A review
	// with no item used to reach a library lookup of id 0 and come back as a 500.
	case errors.Is(err, automation.ErrNothingToImport),
		errors.Is(err, automation.ErrWrongTargetKind),
		errors.Is(err, automation.ErrNeedsTarget),
		errors.Is(err, automation.ErrWrongReason),
		errors.Is(err, automation.ErrBadMapping):
		return http.StatusUnprocessableEntity
	}
	return http.StatusInternalServerError
}
