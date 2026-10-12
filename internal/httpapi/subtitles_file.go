package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/tristenlammi/arrmada/internal/subtitles"
)

// The per-file subtitle drawer (SUB-27): one video's sidecars, embedded tracks and state,
// plus nudging a sidecar's timing and sending one to the recycle bin. The client names the
// video by ids and the sidecar by its bare file name; the subtitles service resolves both
// against the database and the folder, never a client path.

// mediaRefFromQuery reads ?kind=movie&id=… or ?kind=episode&series=…&season=…&episode=….
func mediaRefFromQuery(q url.Values) (subtitles.MediaRef, bool) {
	num := func(k string) (int64, bool) {
		n, err := strconv.ParseInt(q.Get(k), 10, 64)
		return n, err == nil
	}
	ref := subtitles.MediaRef{Kind: q.Get("kind")}
	switch ref.Kind {
	case "movie":
		id, ok := num("id")
		ref.MovieID = id
		return ref, ok
	case "episode":
		sid, ok1 := num("series")
		season, ok2 := num("season")
		ep, ok3 := num("episode")
		ref.SeriesID, ref.Season, ref.Episode = sid, int(season), int(ep)
		return ref, ok1 && ok2 && ok3
	}
	return ref, false
}

// subtitleFileError maps a per-file action's error to its HTTP answer.
func (a *api) subtitleFileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, subtitles.ErrMediaNotFound):
		a.writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, subtitles.ErrBadMediaRef), errors.Is(err, subtitles.ErrNotSidecar),
		errors.Is(err, subtitles.ErrNotShiftable), errors.Is(err, subtitles.ErrBadShift):
		a.writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, subtitles.ErrFileBusy):
		a.writeError(w, http.StatusConflict, err.Error())
	default:
		a.deps.Log.Warn("subtitles: file action failed", "err", err)
		a.writeError(w, http.StatusInternalServerError, "Couldn't do that, so the file was left as it was: "+err.Error())
	}
}

// handleSubtitleFile is GET /subtitles/file: the drawer's whole picture of one video.
func (a *api) handleSubtitleFile(w http.ResponseWriter, r *http.Request) {
	ref, ok := mediaRefFromQuery(r.URL.Query())
	if !ok {
		a.writeError(w, http.StatusBadRequest, subtitles.ErrBadMediaRef.Error())
		return
	}
	v, err := a.deps.Subtitles.FileView(r.Context(), ref)
	if err != nil {
		a.subtitleFileError(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, v)
}

// handleSubtitleFileShift is POST /subtitles/file/shift {kind, movie_id | series_id +
// season + episode, name, offset_ms}: moves one SRT's cues by offset_ms (+ = later).
func (a *api) handleSubtitleFileShift(w http.ResponseWriter, r *http.Request) {
	var req struct {
		subtitles.MediaRef
		Name     string `json:"name"`
		OffsetMs int    `json:"offset_ms"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	sv, err := a.deps.Subtitles.ShiftSidecar(r.Context(), req.MediaRef, req.Name, req.OffsetMs)
	if err != nil {
		a.subtitleFileError(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, sv)
}

// handleSubtitleSearch is POST /subtitles/search {kind, movie_id | series_id + season +
// episode, lang, variant, refresh}: OpenSubtitles' candidates for one video's language,
// scored and flagged (SUB-28). No download quota is spent; a recent answer is reused
// unless refresh is set.
func (a *api) handleSubtitleSearch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		subtitles.MediaRef
		Lang    string `json:"lang"`
		Variant string `json:"variant"`
		Refresh bool   `json:"refresh"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	v, err := a.deps.Subtitles.SearchCandidates(r.Context(), req.MediaRef, req.Lang, req.Variant, req.Refresh)
	var rl *subtitles.RateLimitError
	switch {
	case err == nil:
		if v.Candidates == nil {
			v.Candidates = []subtitles.ScoredCandidate{}
		}
		a.writeJSON(w, http.StatusOK, v)
	case errors.Is(err, subtitles.ErrBadWant):
		a.writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, subtitles.ErrSearchUnavailable):
		a.writeError(w, http.StatusConflict, err.Error())
	case errors.As(err, &rl):
		w.Header().Set("Retry-After", strconv.Itoa(int(rl.RetryAfter.Seconds())+1))
		a.writeError(w, http.StatusTooManyRequests, rl.Error())
	case errors.Is(err, subtitles.ErrMediaNotFound), errors.Is(err, subtitles.ErrBadMediaRef):
		a.subtitleFileError(w, err)
	case errors.Is(err, subtitles.ErrAuthFailed), errors.Is(err, subtitles.ErrRateLimited), errors.Is(err, subtitles.ErrNotConfigured):
		a.writeError(w, http.StatusBadGateway, err.Error())
	default:
		a.deps.Log.Warn("subtitles: interactive search failed", "err", err)
		a.writeError(w, http.StatusBadGateway, "OpenSubtitles search failed — see the Subtitles log")
	}
}

// handleSubtitleFileDelete is DELETE /subtitles/file?kind=…&…&name=…: one subtitle (paired
// or orphaned) to the recycle bin. A bin failure keeps the file and answers 500.
func (a *api) handleSubtitleFileDelete(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ref, ok := mediaRefFromQuery(q)
	if !ok {
		a.writeError(w, http.StatusBadRequest, subtitles.ErrBadMediaRef.Error())
		return
	}
	if err := a.deps.Subtitles.RemoveSidecar(r.Context(), ref, q.Get("name")); err != nil {
		a.subtitleFileError(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"removed": q.Get("name")})
}
