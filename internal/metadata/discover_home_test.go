package metadata

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// homeTMDB serves /discover/movie with three candidates and each film's release dates:
// 1 went digital three days ago, 2 went digital months ago somewhere and only reached disc
// this week elsewhere, 3 is out on disc a week ago.
func homeTMDB(t *testing.T, now time.Time) (*TMDB, *string) {
	t.Helper()
	day := func(d int) string { return now.AddDate(0, 0, d).Format(time.DateOnly) + "T00:00:00.000Z" }
	var discoverQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/discover/movie":
			discoverQuery = r.URL.RawQuery
			fmt.Fprint(w, `{"page":1,"total_pages":1,"results":[
				{"id":1,"title":"Fresh","poster_path":"/a.jpg","vote_count":40,"release_date":"2026-01-01"},
				{"id":2,"title":"Stale","poster_path":"/b.jpg","vote_count":900,"release_date":"2025-01-01"},
				{"id":3,"title":"Disc","poster_path":"/c.jpg","vote_count":60,"release_date":"2025-11-01"}]}`)
		case r.URL.Path == "/movie/1/release_dates":
			fmt.Fprintf(w, `{"results":[{"iso_3166_1":"US","release_dates":[{"type":3,"release_date":%q},{"type":4,"release_date":%q}]}]}`, day(-40), day(-3))
		case r.URL.Path == "/movie/2/release_dates":
			fmt.Fprintf(w, `{"results":[{"iso_3166_1":"US","release_dates":[{"type":4,"release_date":%q}]},{"iso_3166_1":"DE","release_dates":[{"type":5,"release_date":%q}]}]}`, day(-90), day(-2))
		case r.URL.Path == "/movie/3/release_dates":
			fmt.Fprintf(w, `{"results":[{"iso_3166_1":"GB","release_dates":[{"type":5,"release_date":%q}]}]}`, day(-7))
		case strings.HasPrefix(r.URL.Path, "/genre/"):
			fmt.Fprint(w, `{"genres":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	tm := NewTMDB("k")
	tm.base = srv.URL
	return tm, &discoverQuery
}

// The row keeps films whose first home release anywhere is in the last two weeks, newest
// first, and drops one that was out at home long before (its disc date elsewhere is new).
func TestJustOutAtHome(t *testing.T) {
	now := time.Now()
	tm, q := homeTMDB(t, now)
	items, err := tm.JustOutAtHome(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].TMDBID != 1 || items[1].TMDBID != 3 {
		t.Fatalf("items = %+v, want Fresh then Disc", items)
	}
	if items[0].HomeRelease != now.AddDate(0, 0, -3).Format(time.DateOnly) {
		t.Errorf("home release = %q", items[0].HomeRelease)
	}
	for _, want := range []string{"with_release_type=4%7C5%7C6", "include_adult=false", "release_date.lte=" + now.Format(time.DateOnly)} {
		if !strings.Contains(*q, want) {
			t.Errorf("discover query %q lacks %q", *q, want)
		}
	}
}

func TestJustOutWindow(t *testing.T) {
	for _, c := range []struct {
		home string
		want bool
	}{{"", false}, {"2026-10-05", false}, {"2026-10-06", true}, {"2026-10-20", true}, {"2026-10-21", false}} {
		if got := justOut(c.home, "2026-10-06", "2026-10-20"); got != c.want {
			t.Errorf("justOut(%q) = %v, want %v", c.home, got, c.want)
		}
	}
}

// The vote floor only goes up, and an age rating goes out with the region's scale; one
// that doesn't look like a rating never reaches TMDB.
func TestBrowseVotesAndCertification(t *testing.T) {
	ctx := context.Background()
	tm, seen := fakeTMDB(t, emptyPage)
	tm.SetRegionFunc(func() string { return "gb" })
	if _, err := tm.Browse(ctx, BrowseQuery{Media: "movie", VotesMin: 1000, Certification: "12A"}); err != nil {
		t.Fatal(err)
	}
	if _, err := tm.Browse(ctx, BrowseQuery{Media: "movie", VotesMin: 5, Certification: "R&x=1"}); err != nil {
		t.Fatal(err)
	}
	urls := seen()
	if len(urls) != 2 {
		t.Fatalf("%d calls", len(urls))
	}
	a, b := urls[0].Query(), urls[1].Query()
	if a.Get("vote_count.gte") != "1000" || a.Get("certification.lte") != "12A" || a.Get("certification_country") != "GB" {
		t.Errorf("first query = %v", a)
	}
	if b.Get("vote_count.gte") != "50" || b.Has("certification.lte") || b.Has("certification_country") {
		t.Errorf("second query = %v", b)
	}
}

func TestCertificationsForRegion(t *testing.T) {
	tm, _ := fakeTMDB(t, `{"certifications":{"US":[{"certification":"R","order":4},{"certification":"G","order":1},{"certification":"bad code!","order":2}],"GB":[{"certification":"15","order":4}]}}`)
	tm.SetRegionFunc(func() string { return "US" })
	c, err := tm.Certifications(context.Background(), "movie")
	if err != nil {
		t.Fatal(err)
	}
	if c.Country != "US" || len(c.Items) != 2 || c.Items[0].Code != "G" || c.Items[1].Code != "R" {
		t.Errorf("certs = %+v", c)
	}
}
