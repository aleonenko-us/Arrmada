package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/music"
	"github.com/tristenlammi/arrmada/internal/settings"
)

// The Music matching settings: 404 while Music is off; the defaults; a valid change; and
// refusals that save nothing.
func TestMusicMatchingRoutes(t *testing.T) {
	h, set, session, _ := musicEditionServer(t)
	for _, method := range []string{"GET", "PUT"} {
		if rec := musicCallBody(h, method, "/api/v1/music/matching", session, `{}`); rec.Code != http.StatusNotFound {
			t.Errorf("%s matching while off = %d, want 404", method, rec.Code)
		}
	}
	if err := set.SetBool(context.Background(), settings.KeyModuleMusic, true); err != nil {
		t.Fatal(err)
	}
	rec := musicCallBody(h, "GET", "/api/v1/music/matching", session, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"strong":0.04`) || !strings.Contains(rec.Body.String(), `"edition_policy":"original"`) {
		t.Fatalf("matching = %d %s", rec.Code, rec.Body.String())
	}
	if rec := musicCallBody(h, "PUT", "/api/v1/music/matching", session, `{"strong": 0.5}`); rec.Code != http.StatusBadRequest {
		t.Errorf("strong above expected = %d, want 400", rec.Code)
	}
	if rec := musicCallBody(h, "PUT", "/api/v1/music/matching", session, `{"edition_policy": "biggest"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown policy = %d, want 400", rec.Code)
	}
	rec = musicCallBody(h, "PUT", "/api/v1/music/matching", session, `{"expected": 0.2, "edition_policy": "preferred", "edition_countries": ["gb", " us "]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("save = %d %s", rec.Code, rec.Body.String())
	}
	if v, _ := set.Lookup(music.KeyMatchExpected); v != "0.2" {
		t.Errorf("expected saved as %q", v)
	}
	if v, _ := set.Lookup(music.KeyEditionCountries); v != "GB,US" {
		t.Errorf("countries saved as %q", v)
	}
	if v, ok := set.Lookup(music.KeyMatchStrong); ok {
		t.Errorf("a refused save stored strong = %q", v)
	}
}

// A review the matcher held carries its breakdown in the list Reviews reads.
func TestReviewListCarriesMatchBreakdown(t *testing.T) {
	a, st := reviewAPI(t)
	rep := music.MatchReport{Reason: music.MatchReasonCloseEditions, Summary: "Two editions fit almost equally.",
		Candidates: []music.ReportCandidate{{AlbumID: 1, ReleaseID: 2, Album: "OK Computer", Edition: "GB CD 1997 · 12 tracks",
			Distance: 0.02, Components: []music.Component{{Name: "tracks", Weight: 2, Penalty: 0.1, Count: 12, Share: 0.01}},
			Tracks: []music.ReportTrack{{File: "07.flac", Track: "07 Fitter Happier", Distance: 0.4, Notes: []string{"length off by 45 s"}}}}}}
	raw, _ := json.Marshal(rep)
	if _, err := st.DB().Exec(`INSERT INTO import_reviews (hash, name, content_path, media_type, expected_id, reason, reason_code, match_json)
		VALUES ('h1', 'Radiohead - OK Computer', '/dl/x', 'music', 1, 'Two editions fit almost equally.', 'close_editions', ?)`, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`INSERT INTO import_reviews (hash, name, content_path, media_type, expected_id, reason)
		VALUES ('h2', 'Some Show S01', '/dl/y', 'series', 3, 'held')`); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	a.handleListReviews(rec, httptest.NewRequest(http.MethodGet, "/api/v1/reviews", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d", rec.Code)
	}
	var body struct {
		Reviews []struct {
			Name       string             `json:"name"`
			ReasonCode string             `json:"reason_code"`
			Match      *music.MatchReport `json:"match"`
		} `json:"reviews"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, r := range body.Reviews {
		switch r.Name {
		case "Radiohead - OK Computer":
			if r.ReasonCode != "close_editions" || r.Match == nil || len(r.Match.Candidates) != 1 ||
				r.Match.Candidates[0].Tracks[0].Notes[0] != "length off by 45 s" {
				t.Errorf("music review %+v", r)
			}
		default:
			if r.Match != nil {
				t.Errorf("a review the matcher didn't hold carries a breakdown: %+v", r)
			}
		}
	}
}
