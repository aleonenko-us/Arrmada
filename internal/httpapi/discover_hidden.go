package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/tristenlammi/arrmada/internal/notinterested"
)

// "Not interested": a person hides a title from their own Discover. It leaves every row,
// browse grid and search result for them (rowCards) until they restore it from the hidden
// list. Nobody else's Discover changes, and nothing about a request or the library does.

// handleHiddenList is the viewer's hidden titles, the most recently hidden first.
//
//	GET /api/v1/discover/hidden → {items: [{media_type, tmdb_id, title, year, poster_url, at}]}
func (a *api) handleHiddenList(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u == nil {
		a.writeError(w, http.StatusUnauthorized, "sign in")
		return
	}
	if a.deps.NotInterested == nil {
		a.writeJSON(w, http.StatusOK, map[string]any{"items": []notinterested.Title{}})
		return
	}
	list, err := a.deps.NotInterested.List(r.Context(), u.ID)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not list hidden titles")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

// handleHide hides a title for the viewer.
//
//	POST /api/v1/discover/hidden {media_type, tmdb_id, title, year, poster_url}
func (a *api) handleHide(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u == nil {
		a.writeError(w, http.StatusUnauthorized, "sign in")
		return
	}
	if a.deps.NotInterested == nil {
		a.writeError(w, http.StatusServiceUnavailable, "hiding titles isn't available")
		return
	}
	var in notinterested.Title
	if !a.decodeJSON(w, r, &in) {
		return
	}
	err := a.deps.NotInterested.Hide(r.Context(), u.ID, in)
	switch {
	case errors.Is(err, notinterested.ErrBadTitle):
		a.writeError(w, http.StatusBadRequest, "only movies and series can be hidden")
	case errors.Is(err, notinterested.ErrFull):
		a.writeError(w, http.StatusConflict, "You’ve hidden as many titles as Discover keeps. Restore some first.")
	case err != nil:
		a.writeError(w, http.StatusInternalServerError, "could not hide the title")
	default:
		a.writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	}
}

// handleUnhide restores a hidden title.
//
//	DELETE /api/v1/discover/hidden/{media}/{id}
func (a *api) handleUnhide(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u == nil {
		a.writeError(w, http.StatusUnauthorized, "sign in")
		return
	}
	if a.deps.NotInterested == nil {
		a.writeError(w, http.StatusServiceUnavailable, "hiding titles isn't available")
		return
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	media := r.PathValue("media")
	if err != nil || id <= 0 || (media != "movie" && media != "series") {
		a.writeError(w, http.StatusBadRequest, "invalid title")
		return
	}
	if err := a.deps.NotInterested.Restore(r.Context(), u.ID, media, id); err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not restore the title")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}
