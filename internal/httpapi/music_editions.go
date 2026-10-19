package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/tristenlammi/arrmada/internal/jobs"
	"github.com/tristenlammi/arrmada/internal/music"
)

// An album's editions (MUS-30), behind musicRoute.

// handleAlbumEditions lists an album's editions for the picker: each with a label, whether
// it's acceptable, whether it has the target's tracks, and how many files sit on it.
func (a *api) handleAlbumEditions(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	eds, pinned, err := a.deps.Music.AlbumEditions(r.Context(), id)
	if err != nil {
		a.musicErr(w, err, "could not list the album's editions")
		return
	}
	if eds == nil {
		eds = []music.EditionView{}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"editions": eds, "pinned": pinned})
}

// editionSwitchTimeout bounds a switch that first reads the edition's tracks from
// MusicBrainz (one call, which can wait its turn behind others at one a second).
const editionSwitchTimeout = 2 * time.Minute

// handleSetAlbumEdition makes one of an album's editions its target, by hand, pinned or
// not. The album's files move with it (rows only; nothing on disk). When the edition's
// tracks aren't stored yet they're read from MusicBrainz first, as a job (202).
func (a *api) handleSetAlbumEdition(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		ReleaseID int64 `json:"release_id"`
		Pinned    bool  `json:"pinned"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if req.ReleaseID <= 0 {
		a.writeError(w, http.StatusBadRequest, "release_id is required")
		return
	}
	ctx := r.Context()
	if _, err := a.deps.Music.AlbumRecord(ctx, id); err != nil {
		a.musicErr(w, err, "could not load the album")
		return
	}
	needs, err := a.deps.Music.EditionNeedsFetch(ctx, id, req.ReleaseID)
	if errors.Is(err, music.ErrNotFound) {
		a.writeError(w, http.StatusBadRequest, "that isn't one of this album's editions")
		return
	}
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not read the album's editions")
		return
	}
	pinned, rid := req.Pinned, req.ReleaseID
	if !needs {
		res, err := a.deps.Music.SetEdition(ctx, id, rid, pinned)
		if err != nil {
			a.editionErr(w, err)
			return
		}
		a.writeJSON(w, http.StatusOK, map[string]any{"moved": res.Moved, "kept": res.Kept, "pinned": pinned})
		return
	}
	jobID, existing, ok := a.submitOr503(w, r, jobs.Spec{Kind: "album.edition", Target: jobTarget("album", id), Timeout: editionSwitchTimeout,
		Fn: func(ctx context.Context, p *jobs.Progress) (any, error) {
			res, err := a.deps.Music.SetEdition(ctx, id, rid, pinned)
			if err != nil {
				return nil, err
			}
			p.SetMessage(editionMovedWords(res))
			return map[string]any{"moved": res.Moved, "kept": res.Kept}, nil
		}})
	if !ok {
		return
	}
	a.accepted(w, jobID, existing, map[string]any{"status": "switching"})
}

func (a *api) editionErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, music.ErrNotFound):
		a.writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, music.ErrEditionNotListed):
		a.writeError(w, http.StatusConflict, "that edition's tracks aren't known yet; try again in a moment")
	default:
		a.writeError(w, http.StatusInternalServerError, "could not switch the edition")
	}
}

func editionMovedWords(res music.SwitchResult) string {
	switch {
	case res.Moved == 0 && res.Kept == 0:
		return "Switched the edition"
	case res.Kept == 0:
		return "Switched the edition; " + strconv.Itoa(res.Moved) + " file(s) moved to its tracks"
	}
	return "Switched the edition; " + strconv.Itoa(res.Moved) + " file(s) moved, " + strconv.Itoa(res.Kept) + " kept as extras"
}
