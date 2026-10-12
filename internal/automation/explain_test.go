package automation

import (
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/quality"
)

// explainRow carries a Decision's per-release explanation onto the interactive rows: only
// eligible rows get it, and only rows of the main ranking say what they lost on.
func TestExplainRowMapsTheEvaluation(t *testing.T) {
	p := quality.Profile{FormatScores: map[string]int{"HEVC": 50, "Dolby Vision": -50}, MinFormatScore: -100}
	d := quality.NewDefaultEngine().Decide(p, []quality.Candidate{
		quality.NewCandidate("Film.2021.1080p.WEB-DL.x265-GRPA", 6, 10),
		quality.NewCandidate("Film.2021.2160p.BluRay.DV.x264-GRPB", 30, 10),
	})
	if len(d.Eligible) != 2 {
		t.Fatalf("eligible %d", len(d.Eligible))
	}
	win := explainRow(RankedRelease{Eligible: true}, d.Eligible[0], true)
	if len(win.Matched) != 1 || win.Matched[0] != "HEVC" || win.LostOn != "" {
		t.Errorf("winner row = %+v", win)
	}
	lost := explainRow(RankedRelease{Eligible: true}, d.Eligible[1], true)
	if len(lost.Avoided) != 1 || lost.Avoided[0] != "Dolby Vision" || lost.LostOn != "it has Dolby Vision, which you avoid" || lost.LostOnKey != string(quality.KeyAvoid) {
		t.Errorf("avoided row = %+v", lost)
	}
	// Ranked in a side list (usenet, blocklisted, another track): formats, no neighbour.
	side := explainRow(RankedRelease{Eligible: true}, d.Eligible[1], false)
	if side.LostOn != "" || len(side.Avoided) != 1 {
		t.Errorf("side row = %+v", side)
	}
	// A row made ineligible after ranking (usenet, the track filter) says nothing more.
	if gone := explainRow(RankedRelease{Eligible: false, RejectReason: usenetReject}, d.Eligible[1], true); gone.LostOn != "" || gone.Avoided != nil {
		t.Errorf("ineligible row = %+v", gone)
	}
}

// Interactive search: every eligible row behind the recommendation names what it lost on,
// and the list says what the recommendation was chosen over.
func TestRankReleasesExplainsEachRow(t *testing.T) {
	h := newStallHarness(t)
	mid := h.addMovie(t, 1, "Arrival", 2016)
	const (
		uhd  = "Arrival.2016.2160p.WEB-DL.x264-GRPA"
		hevc = "Arrival.2016.1080p.WEB-DL.x265-GRPB"
		avc  = "Arrival.2016.1080p.BluRay.x264-GRPC"
	)
	h.ix.offer(avc, uhd, hevc)
	spec := quality.StoredProfile{MediaType: quality.MediaMovie, Name: "edited", Ideal: &quality.IdealFile{Codec: map[string]string{"hevc": quality.PrefWant}}}
	list, err := h.c.RankReleasesWith(h.ctx, mid, &spec)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{uhd: "", hevc: "lower resolution", avc: "no HEVC, which you prefer"}
	for _, r := range list.Releases {
		w, ok := want[r.Title]
		if !ok {
			continue
		}
		delete(want, r.Title)
		if !r.Eligible || r.LostOn != w || r.Recommended != (r.Title == uhd) {
			t.Errorf("%s: eligible %v, recommended %v, lost on %q, want %q", r.Title, r.Eligible, r.Recommended, r.LostOn, w)
		}
		if r.Title == hevc && (len(r.Matched) != 1 || r.Matched[0] != "HEVC") {
			t.Errorf("%s matched %v", r.Title, r.Matched)
		}
	}
	if len(want) > 0 {
		t.Errorf("rows missing: %v", want)
	}
	if !strings.HasPrefix(list.ChosenOver, "Chosen over the 1080p WEB-DL — lower resolution") {
		t.Errorf("ChosenOver = %q", list.ChosenOver)
	}
}
