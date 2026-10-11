package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/tristenlammi/arrmada/internal/convert"
)

// handleConvertPlan — GET /api/v1/convert/plan?key=: one file's conversion plan (what the
// picture becomes, every track kept or removed and why, the warnings), from the index.
// Manager-only like the rest of Convert.
func (a *api) handleConvertPlan(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		a.writeError(w, http.StatusBadRequest, "pass key=")
		return
	}
	v, err := a.deps.Convert.PlanView(r.Context(), key)
	switch {
	case errors.Is(err, convert.ErrNotIndexed):
		a.writeError(w, http.StatusNotFound, err.Error())
		return
	case errors.Is(err, convert.ErrInvalidItem):
		a.writeError(w, http.StatusBadRequest, "invalid item")
		return
	case err != nil:
		a.writeError(w, http.StatusInternalServerError, "could not build the conversion plan")
		return
	}
	a.writeJSON(w, http.StatusOK, v)
}

// handleConvertAutoPreview — GET /api/v1/convert/auto-preview[?series=&season=]: what
// switching automatic conversion on would do across the library (or what converting one
// show or season now would do), in counts: files, size now and after, and how many lose
// Dolby Vision, closed captions, image subtitles or audio tracks.
func (a *api) handleConvertAutoPreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := convert.PreviewFilter{Season: -1}
	if raw := q.Get("series"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			a.writeError(w, http.StatusBadRequest, "invalid series id")
			return
		}
		f.SeriesID = id
	}
	if raw := q.Get("season"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || f.SeriesID == 0 {
			a.writeError(w, http.StatusBadRequest, "invalid season")
			return
		}
		f.Season = n
	}
	a.writeJSON(w, http.StatusOK, a.deps.Convert.AutoPreview(r.Context(), f))
}
