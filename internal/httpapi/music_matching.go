package httpapi

import (
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/tristenlammi/arrmada/internal/music"
)

// The music matcher's settings (MUS-31) and the edition policy (MUS-30), behind
// musicRoute: Settings → Naming & metadata → Music matching.

// matchingSettings is the Music matching card: the matcher's thresholds and the edition
// policy, with the defaults so the page can offer "reset".
type matchingSettings struct {
	music.MatchSettings
	EditionPolicy    string   `json:"edition_policy"`
	EditionCountries []string `json:"edition_countries"`
	EditionMedia     []string `json:"edition_media"`
}

func (a *api) currentMatching() matchingSettings {
	p := a.deps.Music.EditionPolicy()
	return matchingSettings{MatchSettings: a.deps.Music.MatchSettings(), EditionPolicy: p.Mode,
		EditionCountries: nonNilStrings(p.Countries), EditionMedia: nonNilStrings(p.Media)}
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// handleGetMusicMatching answers the matching settings in effect and their defaults.
func (a *api) handleGetMusicMatching(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, http.StatusOK, map[string]any{
		"settings": a.currentMatching(),
		"defaults": matchingSettings{MatchSettings: music.DefaultMatchSettings(), EditionPolicy: music.PolicyOriginal,
			EditionCountries: []string{}, EditionMedia: music.DefaultEditionMedia},
	})
}

// handleSetMusicMatching saves the matching settings. Only the fields sent change; the
// thresholds are checked together (strong ≤ expected ≤ review…) before anything is saved.
func (a *api) handleSetMusicMatching(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Strong           *float64  `json:"strong"`
		Expected         *float64  `json:"expected"`
		Review           *float64  `json:"review"`
		TrackMax         *float64  `json:"track_max"`
		Gap              *float64  `json:"gap"`
		PartialMin       *float64  `json:"partial_min"`
		EditionPolicy    *string   `json:"edition_policy"`
		EditionCountries *[]string `json:"edition_countries"`
		EditionMedia     *[]string `json:"edition_media"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	cur := a.currentMatching()
	next := cur.MatchSettings
	set := map[string]string{}
	for _, f := range []struct {
		key string
		in  *float64
		out *float64
	}{
		{music.KeyMatchStrong, req.Strong, &next.Strong}, {music.KeyMatchExpected, req.Expected, &next.Expected},
		{music.KeyMatchReview, req.Review, &next.Review}, {music.KeyMatchTrackMax, req.TrackMax, &next.TrackMax},
		{music.KeyMatchGap, req.Gap, &next.Gap}, {music.KeyMatchPartialMin, req.PartialMin, &next.PartialMin},
	} {
		if f.in == nil {
			continue
		}
		v := math.Round(*f.in*1000) / 1000
		*f.out = v
		set[f.key] = strconv.FormatFloat(v, 'f', -1, 64)
	}
	if err := next.Validate(); err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.EditionPolicy != nil {
		p := strings.TrimSpace(*req.EditionPolicy)
		if !music.ValidPolicy(p) {
			a.writeError(w, http.StatusBadRequest, "edition_policy must be original, most_complete or preferred")
			return
		}
		set[music.KeyEditionPolicy] = p
	}
	for key, list := range map[string]*[]string{music.KeyEditionCountries: req.EditionCountries, music.KeyEditionMedia: req.EditionMedia} {
		if list == nil {
			continue
		}
		var clean []string
		for _, v := range *list {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			if strings.Contains(v, ",") || len(v) > 40 {
				a.writeError(w, http.StatusBadRequest, "each country or medium is one short name, without commas")
				return
			}
			if key == music.KeyEditionCountries {
				v = strings.ToUpper(v)
			}
			clean = append(clean, v)
		}
		if len(clean) > 20 {
			a.writeError(w, http.StatusBadRequest, "up to 20 countries or media")
			return
		}
		set[key] = strings.Join(clean, ",")
	}
	for key, v := range set {
		if err := a.deps.Settings.Set(r.Context(), key, v); err != nil {
			a.writeError(w, http.StatusInternalServerError, "could not save the matching settings")
			return
		}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"settings": a.currentMatching()})
}
