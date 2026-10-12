package httpapi

import (
	"errors"
	"net/http"

	"github.com/tristenlammi/arrmada/internal/subtitles"
)

// Subtitle language profiles (internal/subtitles/profiles.go): which subtitles each title
// wants, by language and variant, and the titles pinned to a profile other than the
// default. Any change that moves what a title wants recounts its coverage.

// profileError maps a profile or override error to its HTTP answer.
func (a *api) profileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, subtitles.ErrProfileNotFound), errors.Is(err, subtitles.ErrOverrideNoTitle):
		a.writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, subtitles.ErrProfileNameTaken), errors.Is(err, subtitles.ErrDefaultProfile):
		a.writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, subtitles.ErrProfileInvalid), errors.Is(err, subtitles.ErrUnknownLanguage),
		errors.Is(err, subtitles.ErrOverrideKind):
		a.writeError(w, http.StatusBadRequest, err.Error())
	default:
		a.deps.Log.Warn("subtitle profiles", "err", err)
		a.writeError(w, http.StatusInternalServerError, "could not save the subtitle profile")
	}
}

// handleSubtitleProfiles lists every profile (the default first) and every override.
func (a *api) handleSubtitleProfiles(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, http.StatusOK, map[string]any{
		"profiles":  a.deps.Subtitles.Profiles(r.Context()),
		"overrides": a.deps.Subtitles.Overrides(r.Context()),
	})
}

type profileBody struct {
	Name  *string                 `json:"name"`
	Items []subtitles.ProfileItem `json:"items"`
}

// handleCreateSubtitleProfile adds a profile: {name, items}.
func (a *api) handleCreateSubtitleProfile(w http.ResponseWriter, r *http.Request) {
	var req profileBody
	if !a.decodeJSON(w, r, &req) {
		return
	}
	name := ""
	if req.Name != nil {
		name = *req.Name
	}
	p, err := a.deps.Subtitles.CreateProfile(r.Context(), name, req.Items)
	if err != nil {
		a.profileError(w, err)
		return
	}
	a.writeJSON(w, http.StatusCreated, p)
}

// handleUpdateSubtitleProfile renames a profile and/or replaces its items (an omitted
// field is left as is).
func (a *api) handleUpdateSubtitleProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	var req profileBody
	if !a.decodeJSON(w, r, &req) {
		return
	}
	p, err := a.deps.Subtitles.UpdateProfile(r.Context(), id, req.Name, req.Items)
	if err != nil {
		a.profileError(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, p)
}

// handleDeleteSubtitleProfile removes a profile; its titles go back to the default. The
// default profile can't be deleted.
func (a *api) handleDeleteSubtitleProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	if err := a.deps.Subtitles.DeleteProfile(r.Context(), id); err != nil {
		a.profileError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSetSubtitleOverride pins a movie or a series (every episode) to a profile:
// {profile_id}. Pinning to the default profile clears the override.
func (a *api) handleSetSubtitleOverride(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		ProfileID int64 `json:"profile_id"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if err := a.deps.Subtitles.SetOverride(r.Context(), r.PathValue("kind"), id, req.ProfileID); err != nil {
		a.profileError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleClearSubtitleOverride puts a title back on the default profile.
func (a *api) handleClearSubtitleOverride(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	if err := a.deps.Subtitles.ClearOverride(r.Context(), r.PathValue("kind"), id); err != nil {
		a.profileError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
