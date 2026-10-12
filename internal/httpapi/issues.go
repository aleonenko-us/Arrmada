package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/issues"
)

// Reported problems (Requests → Issues). Requesters report a problem with a title in the
// library and follow their own reports; staff see everyone's and close them. A requester
// never sees anyone else's report, name or note — the list is narrowed to the issues they
// reported and shaped (issues.ShapeForRequester) before it leaves.

// issuesPage is how many issues one list answer holds unless asked for fewer.
const issuesPage = 100

// handleReportIssue records a report from the title's sheet.
func (a *api) handleReportIssue(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u == nil {
		a.writeError(w, http.StatusUnauthorized, "sign in first")
		return
	}
	if a.deps.Issues == nil {
		a.writeError(w, http.StatusServiceUnavailable, "reporting problems isn't available")
		return
	}
	var body struct {
		MediaType string `json:"media_type"`
		TMDBID    int    `json:"tmdb_id"`
		Season    int    `json:"season"`
		Episode   int    `json:"episode"`
		Category  string `json:"category"`
		Note      string `json:"note"`
	}
	if !a.decodeJSONLimit(w, r, &body, 16<<10) {
		return
	}
	iss, joined, err := a.deps.Issues.Create(r.Context(), issues.Report{
		MediaType: body.MediaType, TMDBID: body.TMDBID, Season: body.Season, Episode: body.Episode,
		Category: body.Category, Note: body.Note, UserID: u.ID, UserName: u.Username,
	})
	if a.writeIssueError(w, err) {
		return
	}
	list := []issues.Issue{iss}
	if !u.Role.AtLeast(auth.RoleManager) {
		issues.ShapeForRequester(list, u.ID)
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"issue": list[0], "joined": joined})
}

// handleListIssues lists issues: staff see everyone's, anyone else only the ones they
// reported. ?status=open|closed narrows it, ?id= picks one, ?limit= caps the page.
func (a *api) handleListIssues(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u == nil {
		a.writeError(w, http.StatusUnauthorized, "sign in first")
		return
	}
	if a.deps.Issues == nil {
		a.writeJSON(w, http.StatusOK, map[string]any{"issues": []issues.Issue{}, "total": 0, "counts": issues.Counts{}})
		return
	}
	staff := u.Role.AtLeast(auth.RoleManager)
	q := r.URL.Query()
	f := issues.ListFilter{Status: q.Get("status"), Limit: issuesPage}
	switch f.Status {
	case "", issues.FilterOpen, issues.FilterClosed:
	default:
		a.writeError(w, http.StatusBadRequest, "status must be open or closed")
		return
	}
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
		f.Limit = min(n, 200)
	}
	if id, err := strconv.ParseInt(q.Get("id"), 10, 64); err == nil && id > 0 {
		f.ID = id
	}
	if !staff {
		f.UserID = u.ID
	}
	list, total, counts, err := a.deps.Issues.List(r.Context(), f)
	if err != nil {
		a.deps.Log.Error("issues: list failed", "err", err)
		a.writeError(w, http.StatusInternalServerError, "couldn't read the reported problems")
		return
	}
	if list == nil {
		list = []issues.Issue{}
	}
	if !staff {
		issues.ShapeForRequester(list, u.ID)
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"issues": list, "total": total, "counts": counts})
}

// handleIssueEpisodes lists a show's episodes that have a file, for the report sheet's
// season and episode pickers. Only what's in the library, behind the adult filter.
func (a *api) handleIssueEpisodes(w http.ResponseWriter, r *http.Request) {
	tmdb, err := strconv.Atoi(r.PathValue("tmdb"))
	if err != nil || tmdb <= 0 {
		a.writeError(w, http.StatusBadRequest, "bad TMDB id")
		return
	}
	if a.deps.Issues == nil {
		a.writeError(w, http.StatusServiceUnavailable, "reporting problems isn't available")
		return
	}
	seasons, err := a.deps.Issues.Episodes(r.Context(), tmdb)
	if a.writeIssueError(w, err) {
		return
	}
	if seasons == nil {
		seasons = []issues.SeasonFiles{}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"seasons": seasons})
}

// handleCloseIssue resolves or dismisses an issue (staff). The note, when given, is what
// the reporters are told.
func (a *api) handleCloseIssue(dismiss bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := a.pathID(w, r)
		if !ok {
			return
		}
		if a.deps.Issues == nil {
			a.writeError(w, http.StatusServiceUnavailable, "reported problems aren't available")
			return
		}
		var body struct {
			Note string `json:"note"`
		}
		if r.Body != nil && r.ContentLength != 0 && !a.decodeJSONLimit(w, r, &body, 16<<10) {
			return
		}
		var by int64
		var byName string
		if u, ok := userFrom(r); ok && u != nil {
			by, byName = u.ID, u.Username
		}
		var (
			iss issues.Issue
			err error
		)
		if dismiss {
			iss, err = a.deps.Issues.Dismiss(r.Context(), id, body.Note, by, byName)
		} else {
			iss, err = a.deps.Issues.Resolve(r.Context(), id, body.Note, by, byName)
		}
		if a.writeIssueError(w, err) {
			return
		}
		a.writeJSON(w, http.StatusOK, iss)
	}
}

// writeIssueError answers an issues error; false when there was none.
func (a *api) writeIssueError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var inv *issues.InvalidError
	switch {
	case errors.As(err, &inv):
		a.writeError(w, http.StatusBadRequest, inv.Error())
	case errors.Is(err, issues.ErrNotFound), errors.Is(err, issues.ErrNotInLibrary):
		a.writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, issues.ErrNoFile), errors.Is(err, issues.ErrClosed):
		a.writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, issues.ErrNoFix):
		a.writeError(w, http.StatusServiceUnavailable, err.Error())
	default:
		a.deps.Log.Error("issues: request failed", "err", err)
		a.writeError(w, http.StatusInternalServerError, "something went wrong — try again")
	}
	return true
}
