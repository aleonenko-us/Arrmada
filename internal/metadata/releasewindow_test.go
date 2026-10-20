package metadata

import "testing"

// The release dates a movie's detail carries: per country, each with a type. Home is the
// earliest digital/physical/TV day anywhere, theatrical the earliest cinema day.
func TestParseTMDBMovieReleaseWindow(t *testing.T) {
	body := `{"id": 1, "title": "Resident Evil", "release_date": "2026-02-20",
	  "release_dates": {"results": [
	    {"iso_3166_1": "US", "release_dates": [
	      {"certification": "R", "type": 3, "release_date": "2026-02-27T00:00:00.000Z"},
	      {"certification": "R", "type": 4, "release_date": "2026-03-20T00:00:00.000Z"},
	      {"certification": "", "type": 5, "release_date": "2026-05-01T00:00:00.000Z"}]},
	    {"iso_3166_1": "GB", "release_dates": [
	      {"type": 2, "release_date": "2026-02-20T00:00:00.000Z"},
	      {"type": 4, "release_date": "2026-03-12T00:00:00.000Z"}]},
	    {"iso_3166_1": "FR", "release_dates": [
	      {"type": 1, "release_date": "2026-01-10T00:00:00.000Z"},
	      {"type": 6, "release_date": "bad"}]}
	  ]}}`
	d, err := parseTMDBMovie([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if d.HomeReleaseDate != "2026-03-12" {
		t.Errorf("home = %q, want the GB digital day 2026-03-12", d.HomeReleaseDate)
	}
	if d.TheatricalDate != "2026-02-20" {
		t.Errorf("theatrical = %q, want the GB limited day 2026-02-20 (not the premiere)", d.TheatricalDate)
	}
	if d.Certification != "R" {
		t.Errorf("certification = %q", d.Certification)
	}
}

func TestReleaseWindowFallbacks(t *testing.T) {
	type rd = struct {
		Certification string `json:"certification"`
		Type          int    `json:"type"`
		ReleaseDate   string `json:"release_date"`
	}
	// Only a festival premiere and a TV airing: theatrical falls back to the premiere, and
	// TV counts as home.
	home, theatrical := releaseWindow([]tmdbReleaseDates{{Country: "DE", ReleaseDates: []rd{
		{Type: 1, ReleaseDate: "2025-09-05T00:00:00.000Z"},
		{Type: 6, ReleaseDate: "2026-01-02T00:00:00.000Z"},
	}}})
	if home != "2026-01-02" || theatrical != "2025-09-05" {
		t.Errorf("home, theatrical = %q, %q", home, theatrical)
	}
	// Nothing listed: both unknown.
	if home, theatrical := releaseWindow(nil); home != "" || theatrical != "" {
		t.Errorf("empty: %q, %q", home, theatrical)
	}
}
