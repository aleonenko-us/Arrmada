package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/displaytitle"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/series"
	"github.com/tristenlammi/arrmada/internal/settings"
)

// "Show titles as" is English by default, takes a language only with a language, and
// stores canonical codes; anything else is refused without saving.
func TestSettingsDisplayTitles(t *testing.T) {
	a := settingsAPI(t)
	_, got := getSettings(t, a)
	if got["display_title_mode"] != "english" || got["display_title_language"] != "" || !reflect.DeepEqual(got["display_title_original_in"], []any{}) {
		t.Fatalf("defaults = %v / %v / %v", got["display_title_mode"], got["display_title_language"], got["display_title_original_in"])
	}
	if w := putSettings(t, a, `{"display_title_mode":"language"}`); w.Code != http.StatusBadRequest {
		t.Errorf("a language mode with no language: HTTP %d, want 400", w.Code)
	}
	if w := putSettings(t, a, `{"display_title_mode":"klingon"}`); w.Code != http.StatusBadRequest {
		t.Errorf("unknown mode: HTTP %d, want 400", w.Code)
	}
	if w := putSettings(t, a, `{"display_title_mode":"language","display_title_language":"klingonish"}`); w.Code != http.StatusBadRequest {
		t.Errorf("unknown language: HTTP %d, want 400", w.Code)
	}
	if _, got := getSettings(t, a); got["display_title_mode"] != "english" {
		t.Errorf("a refused PUT changed the mode: %v", got["display_title_mode"])
	}
	if w := putSettingsAs(t, a, auth.RoleManager, `{"display_title_mode":"language","display_title_language":"Italian"}`); w.Code != http.StatusOK {
		t.Fatalf("manager PUT: HTTP %d %s", w.Code, w.Body.String())
	}
	if w := putSettings(t, a, `{"display_title_original_in":["Greek","it","el"]}`); w.Code != http.StatusOK {
		t.Fatalf("originals: HTTP %d %s", w.Code, w.Body.String())
	}
	_, got = getSettings(t, a)
	if got["display_title_mode"] != "language" || got["display_title_language"] != "it" || !reflect.DeepEqual(got["display_title_original_in"], []any{"el", "it"}) {
		t.Errorf("saved = %v / %v / %v", got["display_title_mode"], got["display_title_language"], got["display_title_original_in"])
	}
	// The language stays stored while another mode is picked, so going back needs no re-pick.
	if w := putSettings(t, a, `{"display_title_mode":"original"}`); w.Code != http.StatusOK {
		t.Fatalf("original: HTTP %d", w.Code)
	}
	if w := putSettings(t, a, `{"display_title_mode":"language"}`); w.Code != http.StatusOK {
		t.Errorf("back to the stored language: HTTP %d %s", w.Code, w.Body.String())
	}
}

// offlineMovies is TMDB switched off: the pages read only what's stored.
type offlineMovies struct{ metadata.MovieProvider }

func (offlineMovies) Available() bool { return false }

// displayServer is a route server whose movies and shows follow the stored "Show titles
// as" setting, as main wires them.
func displayServer(t *testing.T) (*routeServer, *movies.Service, *series.Service) {
	t.Helper()
	var ms *movies.Service
	var ss *series.Service
	s := newRouteServer(t, func(d *Deps) {
		root := t.TempDir()
		policy := func() displaytitle.Policy {
			return settings.DisplayTitlePolicy(func(key string) string { return d.Settings.Get(context.Background(), key, "") })
		}
		ms = movies.NewService(d.Store.DB(), offlineMovies{}, nil, root, "", nil, d.Log)
		ms.SetDisplayPolicy(policy)
		ss = series.NewService(d.Store.DB(), noSeriesMeta{}, root, d.Log)
		ss.SetDisplayPolicy(policy)
		d.Movies, d.Series = ms, ss
	})
	for _, q := range []string{
		`INSERT INTO movies (id, tmdb_id, title, year, monitored, extra_json) VALUES
			(21, 601, 'The Travelling Players', 1975, 1, '{"release_date":"2026-10-10","display_names":{"original":"Ο Θίασος","original_language":"el","read":true}}'),
			(22, 602, 'Dune', 2021, 1, '{"original_language":"en"}')`,
		`INSERT INTO series (id, tmdb_id, title, monitored, extra_json) VALUES
			(11, 501, 'Gomorrah', 1, '{"original_title":"Gomorra - La serie","original_language":"it"}')`,
	} {
		if _, err := s.st.DB().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return s, ms, ss
}

func decodeDisplay[T any](t *testing.T, body []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return v
}

// The API keeps `title` canonical and adds `display_title` on the movie and series list
// and detail endpoints; the calendar shows the display title; staff set or clear a
// per-title override, which requesters can't.
func TestDisplayTitleAPI(t *testing.T) {
	s, ms, ss := displayServer(t)
	ctx := context.Background()
	_, admin := s.user(t, "admin@example.com", auth.RoleAdmin)
	_, kid := s.user(t, "kid@example.com", auth.RoleRequester)

	if rec := s.doBody("PUT", "/api/v1/settings", `{"display_title_original_in":["el","it"]}`, admin); rec.Code != http.StatusOK {
		t.Fatalf("settings: HTTP %d %s", rec.Code, rec.Body)
	}
	// The save works every title out again in the background; do it here to read it back.
	if _, err := ms.SyncDisplayTitles(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.SyncDisplayTitles(ctx); err != nil {
		t.Fatal(err)
	}

	rec := s.do("GET", "/api/v1/movies", admin)
	list := decodeDisplay[struct {
		Movies []movies.MovieSummary `json:"movies"`
	}](t, rec.Body.Bytes())
	byID := map[int64]movies.MovieSummary{}
	for _, m := range list.Movies {
		byID[m.ID] = m
	}
	if m := byID[21]; m.Title != "The Travelling Players" || m.DisplayTitle != "Ο Θίασος" || m.SortTitle != "ο θίασος" {
		t.Errorf("list row = %+v, want the canonical title kept and the Greek one shown", m)
	}
	if m := byID[22]; m.DisplayTitle != "Dune" {
		t.Errorf("an English film shows %q", m.DisplayTitle)
	}

	rec = s.do("GET", "/api/v1/series", admin)
	shows := decodeDisplay[struct {
		Series []series.Series `json:"series"`
	}](t, rec.Body.Bytes())
	if len(shows.Series) != 1 || shows.Series[0].Title != "Gomorrah" || shows.Series[0].DisplayTitle != "Gomorra - La serie" {
		t.Errorf("series list = %+v", shows.Series)
	}

	cal := getCalendar(t, s, "/api/v1/calendar?start=2026-10-01&end=2026-10-31", kid)
	if len(cal.Items) != 1 || cal.Items[0].Title != "Ο Θίασος" {
		t.Errorf("calendar = %+v, want the shown title", cal.Items)
	}

	// The override: staff only, checked, answered with both titles.
	if rec := s.doBody("PUT", "/api/v1/movies/21/display-title", `{"title":"Thiasos"}`, kid); rec.Code != http.StatusForbidden {
		t.Errorf("requester override: HTTP %d, want 403", rec.Code)
	}
	if rec := s.doBody("PUT", "/api/v1/movies/21/display-title", `{"title":"two\nlines"}`, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("a newline: HTTP %d, want 400", rec.Code)
	}
	if rec := s.doBody("PUT", "/api/v1/movies/999/display-title", `{"title":"x"}`, admin); rec.Code != http.StatusNotFound {
		t.Errorf("unknown movie: HTTP %d, want 404", rec.Code)
	}
	rec = s.doBody("PUT", "/api/v1/movies/21/display-title", `{"title":" Thiasos "}`, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("override: HTTP %d %s", rec.Code, rec.Body)
	}
	var set map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &set)
	want := map[string]any{"id": float64(21), "title": "The Travelling Players", "display_title": "Thiasos", "title_override": "Thiasos"}
	if !reflect.DeepEqual(set, want) {
		t.Errorf("override answer = %v, want %v", set, want)
	}
	rec = s.do("GET", "/api/v1/movies/21", admin)
	detail := decodeDisplay[movies.Movie](t, rec.Body.Bytes())
	if detail.Title != "The Travelling Players" || detail.DisplayTitle != "Thiasos" || detail.TitleOverride != "Thiasos" {
		t.Errorf("detail = %q / %q / %q", detail.Title, detail.DisplayTitle, detail.TitleOverride)
	}
	if rec := s.doBody("PUT", "/api/v1/series/11/display-title", `{"title":""}`, admin); rec.Code != http.StatusOK {
		t.Errorf("series clear: HTTP %d %s", rec.Code, rec.Body)
	}

	// The picker's names: without TMDB, the stored ones.
	rec = s.do("GET", "/api/v1/movies/21/title-choices", admin)
	choices := decodeDisplay[struct {
		Choices []displaytitle.Choice `json:"choices"`
	}](t, rec.Body.Bytes())
	if rec.Code != http.StatusOK || len(choices.Choices) != 2 || choices.Choices[1].Title != "Ο Θίασος" {
		t.Errorf("choices: HTTP %d %+v", rec.Code, choices.Choices)
	}
	if rec := s.do("GET", "/api/v1/series/11/title-choices", kid); rec.Code != http.StatusForbidden {
		t.Errorf("requester choices: HTTP %d, want 403", rec.Code)
	}
}
