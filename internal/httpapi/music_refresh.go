package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/tristenlammi/arrmada/internal/music"
)

// Keeping Music current (MUS-13, MUS-14), behind musicRoute: an artist's new-release and
// release-type options (with the "Want matching albums" preview and apply), the library
// defaults those options fall back to, and the New releases list.
//
// The rule these endpoints keep: changing an option never wants or unwants an album by
// itself. It decides what NEW albums get and which release groups are listed; applying it
// to the albums already listed is a separate POST that only goes through when the counts
// the person was shown still hold.

// releaseTypeChoices are the types the options offer, in MusicBrainz's order.
func releaseTypeChoices() map[string][]string {
	return map[string][]string{"primary": music.PrimaryTypes, "secondary": music.SecondaryTypes}
}

// handleArtistOptions answers an artist's options, what's in effect, the defaults and
// what "Want matching albums" would do now.
func (a *api) handleArtistOptions(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	opts, err := a.deps.Music.Options(r.Context(), id)
	if err != nil {
		a.musicErr(w, err, "could not read the artist's options")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"options": opts, "choices": releaseTypeChoices()})
}

// handleSetArtistOptions saves an artist's options. Only the fields sent change:
// monitor_new ("" = the library default), release_types (an object, or null for the
// library default) and quality_profile. When the release types in effect change, the
// artist is refreshed (through the cache) so newly allowed albums are listed, unwanted.
func (a *api) handleSetArtistOptions(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		MonitorNew     *string         `json:"monitor_new"`
		ReleaseTypes   json.RawMessage `json:"release_types"`
		QualityProfile *string         `json:"quality_profile"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	cur, err := a.deps.Music.Options(ctx, id)
	if err != nil {
		a.musicErr(w, err, "could not read the artist's options")
		return
	}
	monitorNew := cur.MonitorNew
	if req.MonitorNew != nil {
		monitorNew = strings.TrimSpace(*req.MonitorNew)
		if !music.ValidMonitorNew(monitorNew) {
			a.writeError(w, http.StatusBadRequest, `monitor_new must be "all", "none" or "" (the library default)`)
			return
		}
	}
	types := cur.ReleaseTypes
	if len(req.ReleaseTypes) > 0 {
		if string(req.ReleaseTypes) == "null" {
			types = nil
		} else {
			var t music.ReleaseTypes
			if err := json.Unmarshal(req.ReleaseTypes, &t); err != nil {
				a.writeError(w, http.StatusBadRequest, "release_types must be {primary: [...], secondary: [...]}")
				return
			}
			n, err := t.Normalize()
			if err != nil {
				a.writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			types = &n
		}
	}
	if req.QualityProfile != nil {
		if a.deps.Automation == nil || !a.deps.Automation.KnownProfile(ctx, *req.QualityProfile) {
			a.writeError(w, http.StatusBadRequest, "unknown quality profile")
			return
		}
	}
	changed, err := a.deps.Music.SetOptions(ctx, id, monitorNew, types)
	if err != nil {
		a.musicErr(w, err, "could not save the artist's options")
		return
	}
	if req.QualityProfile != nil {
		if err := a.deps.Music.SetQualityProfile(ctx, id, *req.QualityProfile); err != nil {
			a.musicErr(w, err, "could not save the quality profile")
			return
		}
	}
	if changed {
		// Mostly cache answers; a first look at singles is a request or a few.
		if _, err := a.deps.Music.RefreshArtist(ctx, id, music.RefreshOptions{Scheduled: true, ListOnly: true}); err != nil {
			a.deps.Log.Warn("music: refreshing after a release-type change failed", "artist_id", id, "err", err)
		}
	}
	opts, err := a.deps.Music.Options(ctx, id)
	if err != nil {
		a.musicErr(w, err, "could not read the artist's options")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"options": opts, "choices": releaseTypeChoices(), "refreshed": changed})
}

// handleApplyArtistOptions carries out "Want matching albums": the counts the person was
// shown must still be the plan, or nothing changes (409, with the new plan).
func (a *api) handleApplyArtistOptions(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Want    int `json:"want"`
		Unwant  int `json:"unwant"`
		Singles int `json:"singles"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	plan, err := a.deps.Music.ApplyOptions(r.Context(), id, req.Want, req.Unwant, req.Singles)
	switch {
	case errors.Is(err, music.ErrPlanChanged):
		a.writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "apply": plan})
		return
	case err != nil:
		a.musicErr(w, err, "could not apply the options")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"applied": plan})
}

// releaseDefaults is the library-wide new-release choice and release types.
type releaseDefaults struct {
	MonitorNew   string             `json:"monitor_new"`
	ReleaseTypes music.ReleaseTypes `json:"release_types"`
}

// handleGetReleaseDefaults answers the library defaults and the type choices.
func (a *api) handleGetReleaseDefaults(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, http.StatusOK, map[string]any{
		"settings": releaseDefaults{MonitorNew: a.deps.Music.MonitorNewDefault(), ReleaseTypes: a.deps.Music.ReleaseTypesDefault()},
		"defaults": releaseDefaults{MonitorNew: music.MonitorNewAll, ReleaseTypes: music.DefaultReleaseTypes()},
		"choices":  releaseTypeChoices(),
	})
}

// handleSetReleaseDefaults saves the library defaults. Like an artist's own options, they
// decide what new albums get and what is listed from the next refresh on; nothing already
// listed is wanted or unwanted by the change.
func (a *api) handleSetReleaseDefaults(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MonitorNew   *string             `json:"monitor_new"`
		ReleaseTypes *music.ReleaseTypes `json:"release_types"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	if req.MonitorNew != nil {
		v := strings.TrimSpace(*req.MonitorNew)
		if v != music.MonitorNewAll && v != music.MonitorNewNone {
			a.writeError(w, http.StatusBadRequest, `monitor_new must be "all" or "none"`)
			return
		}
		if err := a.deps.Settings.Set(ctx, music.KeyMonitorNew, v); err != nil {
			a.writeError(w, http.StatusInternalServerError, "could not save the setting")
			return
		}
	}
	if req.ReleaseTypes != nil {
		t, err := req.ReleaseTypes.Normalize()
		if err != nil {
			a.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := a.deps.Settings.Set(ctx, music.KeyReleaseTypes, t.String()); err != nil {
			a.writeError(w, http.StatusInternalServerError, "could not save the setting")
			return
		}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"settings": releaseDefaults{MonitorNew: a.deps.Music.MonitorNewDefault(), ReleaseTypes: a.deps.Music.ReleaseTypesDefault()},
	})
}

// handleNewReleases lists the albums refreshes found lately (?days=, default 30, at most
// 365): wanted ones and the ones only listed, each saying why.
func (a *api) handleNewReleases(w http.ResponseWriter, r *http.Request) {
	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 365 {
			a.writeError(w, http.StatusBadRequest, "days must be 1 to 365")
			return
		}
		days = n
	}
	list, err := a.deps.Music.NewReleases(r.Context(), days)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not list new releases")
		return
	}
	if list == nil {
		list = []music.NewRelease{}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"albums": list, "days": days})
}
