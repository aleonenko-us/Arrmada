package httpapi

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/series"
)

// POST /series/{id}/search with a list of episodes is one scoped search of exactly those
// (a series.grab-scope job); with no body it is still the whole-show search.
func TestSearchEpisodeListScope(t *testing.T) {
	fj := newFakeJobs(false)
	s := newRouteServer(t, func(d *Deps) { d.Jobs = fj })
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)

	if rec := s.doJSON("POST", "/api/v1/series/7/search", mgr, ""); rec.Code != http.StatusAccepted {
		t.Fatalf("no body: HTTP %d %s", rec.Code, rec.Body)
	}
	if got := fj.submitted[len(fj.submitted)-1]; got.Kind != "series.search" {
		t.Fatalf("no body submitted %q, want the whole-show series.search", got.Kind)
	}
	if rec := s.doJSON("POST", "/api/v1/series/7/search", mgr, `{"episodes": []}`); rec.Code != http.StatusAccepted || len(fj.submitted) != 1 {
		t.Fatalf("an empty list: HTTP %d, %d jobs — want the same whole-show job (already running)", rec.Code, len(fj.submitted))
	}

	rec := s.doJSON("POST", "/api/v1/series/7/search", mgr, `{"episodes": [{"season": 3, "episode": 5}, {"season": 3, "episode": 2}, {"season": 3, "episode": 5}]}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("list: HTTP %d %s", rec.Code, rec.Body)
	}
	got := fj.submitted[len(fj.submitted)-1]
	if got.Kind != "series.grab-scope" || !strings.HasPrefix(got.Target, "series:7:s3:list-") {
		t.Fatalf("list submitted %s %s, want a series.grab-scope job for season 3", got.Kind, got.Target)
	}

	for _, body := range []string{
		`{"episodes": [{"season": 3, "episode": 1}, {"season": 4, "episode": 1}]}`, // two seasons
		`{"episodes": [{"season": 3, "episode": 0}]}`,                              // no episode
		`{"episodes": [{"season": -1, "episode": 2}]}`,
		`{"eps": []}`,
		`{"episodes": [` + strings.Repeat(`{"season": 1, "episode": 1},`, 200) + `{"season": 1, "episode": 2}]}`,
	} {
		if rec := s.doJSON("POST", "/api/v1/series/7/search", mgr, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%.60s…: HTTP %d, want 400", body, rec.Code)
		}
	}
}

func TestEpisodeListScopeBuildsTheList(t *testing.T) {
	var body seriesSearchList
	for _, e := range []int{9, 3, 9, 4} {
		body.Episodes = append(body.Episodes, struct {
			Season  int `json:"season"`
			Episode int `json:"episode"`
		}{2, e})
	}
	sc, err := episodeListScope(body)
	if err != nil {
		t.Fatal(err)
	}
	if sc.Season != 2 || sc.Episode != 0 || !reflect.DeepEqual(sc.Episodes, []int{3, 4, 9}) || sc.Trigger != "bulk" {
		t.Fatalf("scope = %+v", sc)
	}
	if sc, err := episodeListScope(seriesSearchList{}); sc != nil || err != nil {
		t.Fatalf("no list: %+v, %v", sc, err)
	}
}

// PUT /series/{id}/monitor/episodes sets the picked episodes, and refuses another show's.
func TestBulkMonitorRoute(t *testing.T) {
	s, mgr := seriesMonitorServer(t)
	rec := s.doJSON("POST", "/api/v1/series", mgr, `{"tmdb_id": 5, "quality_profile": "hd", "search_on_add": false, "monitor": "none"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add: HTTP %d %s", rec.Code, rec.Body)
	}
	rec = s.doJSON("POST", "/api/v1/series", mgr, `{"tmdb_id": 6, "quality_profile": "hd", "search_on_add": false, "monitor": "none"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add: HTTP %d %s", rec.Code, rec.Body)
	}
	all, _ := s.deps.Series.List(context.Background())
	shows := map[int]int64{}
	for _, sr := range all {
		shows[sr.TMDBID] = sr.ID
	}
	first, _ := s.deps.Series.Get(context.Background(), shows[5])
	other, _ := s.deps.Series.Get(context.Background(), shows[6])
	epID := func(sr series.Series, season int) int64 {
		for _, sn := range sr.Seasons {
			if sn.SeasonNumber == season {
				return sn.Episodes[0].ID
			}
		}
		t.Fatalf("no season %d", season)
		return 0
	}
	path := "/api/v1/series/" + itoa(int(first.ID)) + "/monitor/episodes"
	if rec := s.doJSON("PUT", path, mgr, `{"episode_ids": [`+itoa(int(epID(other, 1)))+`], "monitored": true}`); rec.Code != http.StatusBadRequest {
		t.Errorf("another show's episode: HTTP %d, want 400", rec.Code)
	}
	if rec := s.doJSON("PUT", path, mgr, `{"episode_ids": [`+itoa(int(epID(first, 1)))+`], "monitored": true}`); rec.Code != http.StatusOK {
		t.Fatalf("bulk monitor: HTTP %d %s", rec.Code, rec.Body)
	}
	got, _ := s.deps.Series.Get(context.Background(), first.ID)
	for _, sn := range got.Seasons {
		if sn.SeasonNumber == 1 && (!sn.Monitored || !sn.Episodes[0].Monitored) {
			t.Errorf("season 1 after bulk monitor: season %v, episode %v — want both monitored", sn.Monitored, sn.Episodes[0].Monitored)
		}
	}
}
