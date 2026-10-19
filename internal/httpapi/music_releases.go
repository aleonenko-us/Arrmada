package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/music"
)

// An album's interactive search and a person's grab from it (MUS-17).

// handleAlbumReleases is GET /api/v1/music/albums/{id}/releases: the album's interactive
// search. Each release says whether the automatic search would take it and why not; the
// rows carry tokens, never download links.
func (a *api) handleAlbumReleases(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	if a.deps.Automation == nil {
		a.writeError(w, http.StatusServiceUnavailable, "searching isn't available")
		return
	}
	ctx, cancel := context.WithTimeout(indexer.WithInteractive(r.Context()), 90*time.Second)
	defer cancel()
	list, err := a.deps.Automation.RankAlbumReleases(ctx, id)
	if errors.Is(err, music.ErrNotFound) {
		a.writeError(w, http.StatusNotFound, "album not found")
		return
	}
	if err != nil {
		a.writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	a.tokenize(r, &list, automation.ReleaseRef{MediaKind: automation.ReleaseKindAlbum, MediaID: id})
	a.writeJSON(w, http.StatusOK, list)
}

// handleGrabAlbum is POST /api/v1/music/albums/{id}/grab {token}: grab a release a person
// chose in the album's interactive search. The import matches it against this album.
func (a *api) handleGrabAlbum(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Token string `json:"token"` // from this album's interactive search
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	ref, ok := a.resolveRelease(w, r, req.Token, automation.ReleaseKindAlbum, id)
	if !ok {
		return
	}
	err := a.deps.Automation.GrabForAlbum(r.Context(), id, ref.Indexer, ref.DownloadURL, ref.Title)
	switch {
	case errors.Is(err, automation.ErrModuleOff):
		a.writeError(w, http.StatusNotFound, musicOffMessage)
		return
	case errors.Is(err, music.ErrNotFound):
		a.writeError(w, http.StatusNotFound, "album not found")
		return
	case errors.Is(err, automation.ErrGrabInFlight):
		a.writeError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		a.deps.Log.Warn("music grab failed", "indexer", ref.Indexer, "title", ref.Title, "err", err)
		a.writeGrabError(w, err, ref)
		return
	}
	a.forgetWanted()
	a.writeJSON(w, http.StatusOK, map[string]any{"status": "grabbed", "title": ref.Title})
}
