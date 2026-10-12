package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/quality"
)

// The preview names its samples, and a series profile gets the episode set.
func TestQualityPreviewLabelsItsSamples(t *testing.T) {
	s := newRouteServer(t, func(d *Deps) { d.Quality = quality.NewService(d.Store.DB()) })
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	for media, want := range map[string]string{"series": "An episode of a 50-minute show", "movie": "Dune: Part Two (2024), 166 min"} {
		rec := s.doJSON("POST", "/api/v1/quality/preview", mgr, `{"media_type":"`+media+`","name":"x"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: HTTP %d: %s", media, rec.Code, rec.Body)
		}
		var got struct {
			SampleLabel string           `json:"sample_label"`
			Decision    quality.Decision `json:"decision"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.SampleLabel != want || got.Decision.Winner == nil {
			t.Errorf("%s: label %q, winner %v", media, got.SampleLabel, got.Decision.Winner)
		}
	}
}

// The "Test a release name" box judges a name against the profile as edited — never saved
// here — and says what it parsed, the verdict and which formats matched. Managers only.
func TestQualityExplainJudgesAnUnsavedProfile(t *testing.T) {
	s := newRouteServer(t, func(d *Deps) { d.Quality = quality.NewService(d.Store.DB()) })
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	_, req := s.user(t, "req@example.com", auth.RoleRequester)

	explain := func(sp quality.StoredProfile, name string) quality.Explanation {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"profile": sp, "name": name})
		rec := s.doJSON("POST", "/api/v1/quality/explain", mgr, string(body))
		if rec.Code != http.StatusOK {
			t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
		}
		var got quality.Explanation
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	mustHEVC := quality.StoredProfile{MediaType: quality.MediaSeries, Name: "edited",
		Ideal: &quality.IdealFile{Codec: map[string]string{"hevc": quality.PrefMust}}}
	got := explain(mustHEVC, "Show.S01E01.1080p.WEB.h264-GROUP")
	if got.Release.Resolution != "1080p" || got.Release.Codec != "x264" || got.Release.Group != "GROUP" {
		t.Errorf("parsed %+v", got.Release)
	}
	if got.Evaluation.Eligible || got.Evaluation.RejectReason != "No HEVC — your profile requires it" {
		t.Errorf("verdict = eligible %v, %q", got.Evaluation.Eligible, got.Evaluation.RejectReason)
	}
	var hevc, h264 *quality.FormatCheck
	for i := range got.Formats {
		switch got.Formats[i].Name {
		case "HEVC":
			hevc = &got.Formats[i]
		case "H.264":
			h264 = &got.Formats[i]
		}
	}
	if hevc == nil || hevc.Matched || !hevc.Required || h264 == nil || !h264.Matched {
		t.Errorf("formats: HEVC %+v, H.264 %+v", hevc, h264)
	}

	// A custom format in the unsaved edit is ticked when the name matches it.
	custom := quality.StoredProfile{MediaType: quality.MediaMovie, Name: "edited",
		CustomFormats: []quality.CustomFormat{{Name: "Favourite group", Conditions: []quality.Condition{{Type: quality.CondReleaseGroup, Value: "GROUP"}}}},
		FormatScores:  map[string]int{"Favourite group": 40}}
	got = explain(custom, "Film.2021.1080p.BluRay.x264-GROUP")
	if !got.Evaluation.Eligible {
		t.Fatalf("rejected: %s", got.Evaluation.RejectReason)
	}
	found := false
	for _, f := range got.Formats {
		if f.Name == "Favourite group" {
			found = f.Matched && f.Custom && f.Score == 40
		}
	}
	if !found || len(got.Evaluation.Matched) != 1 || got.Evaluation.Matched[0] != "Favourite group" {
		t.Errorf("custom format not ticked: formats %+v, matched %v", got.Formats, got.Evaluation.Matched)
	}

	// Nothing to judge, and a requester, are both refused.
	if rec := s.doJSON("POST", "/api/v1/quality/explain", mgr, `{"profile":{},"name":"  "}`); rec.Code != http.StatusBadRequest {
		t.Errorf("blank name: HTTP %d", rec.Code)
	}
	if rec := s.doJSON("POST", "/api/v1/quality/explain", req, `{"profile":{},"name":"Film.2021.1080p.WEB-DL-GRP"}`); rec.Code != http.StatusForbidden {
		t.Errorf("requester: HTTP %d, want 403", rec.Code)
	}
}
