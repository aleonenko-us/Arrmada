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

// Every row, refused or not, shows the audio languages its name carries, and a release a
// language Must refuses says which language it lacks.
func TestExplainRowShowsAudioLanguages(t *testing.T) {
	p := quality.Profile{Languages: map[string]string{"de": quality.PrefMust}}
	d := quality.NewDefaultEngine().Decide(p, []quality.Candidate{
		quality.NewCandidate("Film.2021.German.DL.1080p.WEB-DL.x264-GER", 6, 10).WithLanguage("en"),
		quality.NewCandidate("Film.2021.1080p.WEB-DL.x264-ENG", 6, 10).WithLanguage("en"),
	})
	if len(d.Eligible) != 1 || len(d.Rejected) != 1 {
		t.Fatalf("eligible %d rejected %d", len(d.Eligible), len(d.Rejected))
	}
	ok := explainRow(RankedRelease{Eligible: true}, d.Eligible[0], true)
	if strings.Join(ok.AudioLangs, ",") != "en,de" || ok.AudioLangsNote != "" {
		t.Errorf("eligible row = %+v", ok)
	}
	ev := d.Rejected[0]
	no := explainRow(RankedRelease{Eligible: false, RejectReason: ev.RejectReason}, ev, false)
	if strings.Join(no.AudioLangs, ",") != "en" || !strings.Contains(no.AudioLangsNote, "original") ||
		!strings.HasPrefix(no.RejectReason, "Missing German audio") {
		t.Errorf("refused row = %+v", no)
	}
	// A MULTi that doesn't name German is refused (issue #58), and the row says why in
	// plain words: the name doesn't say German.
	multi := quality.NewDefaultEngine().Evaluate(p, quality.NewCandidate("Film.2021.MULTi.1080p.WEB-DL.x264-MUL", 6, 10).WithLanguage("en"))
	row := explainRow(RankedRelease{Eligible: multi.Eligible, RejectReason: multi.RejectReason}, multi, false)
	if row.Eligible || row.RejectReason != "Doesn't name German audio — your profile requires it, and a MULTi release only counts for the title's original language and the ones its name says" ||
		strings.Join(row.AudioLangs, ",") != "en" || row.AudioLangsNote != "and others it doesn't name" {
		t.Errorf("MULTi row = %+v", row)
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

// Interactive search under a target that avoids Dolby Vision, at the default minimum score:
// a DV release is an eligible, avoided row that names what it lost on — never a "below
// the minimum format score" reject — and it's the recommendation when it's all there is.
func TestRankReleasesShowsTargetAvoidAsAvoided(t *testing.T) {
	h := newStallHarness(t)
	mid := h.addMovie(t, 1, "Arrival", 2016)
	const (
		dv    = "Arrival.2016.2160p.WEB-DL.DDP5.1.DV.H.265-GRPA"
		clean = "Arrival.2016.1080p.WEB-DL.DDP5.1.H.264-GRPB"
	)
	spec := quality.StoredProfile{MediaType: quality.MediaMovie, Name: "edited", Ideal: &quality.IdealFile{HDR: map[string]string{"DV": quality.PrefAvoid}}}

	h.ix.offer(dv, clean)
	list, err := h.c.RankReleasesWith(h.ctx, mid, &spec)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, r := range list.Releases {
		switch r.Title {
		case dv:
			seen++
			if !r.Eligible || r.Recommended || r.RejectReason != "" {
				t.Errorf("DV row: eligible %v, recommended %v, reject %q — want eligible, not recommended, no reject", r.Eligible, r.Recommended, r.RejectReason)
			}
			if len(r.Avoided) != 1 || r.Avoided[0] != "Dolby Vision" || r.LostOn != "it has Dolby Vision, which you avoid" {
				t.Errorf("DV row avoided %v, lost on %q", r.Avoided, r.LostOn)
			}
		case clean:
			seen++
			if !r.Recommended {
				t.Errorf("the clean release should be recommended over the avoided DV one")
			}
		}
	}
	if seen != 2 {
		t.Fatalf("rows found %d of 2: %+v", seen, list.Releases)
	}

	h.ix.offer(dv)
	only, err := h.c.RankReleasesWith(h.ctx, mid, &spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(only.Releases) != 1 || !only.Releases[0].Recommended {
		t.Fatalf("a DV release on its own should be the recommendation, got %+v", only.Releases)
	}
	if len(only.Why) == 0 || only.Why[0] != "Only releases with a format you avoid were available" {
		t.Errorf("Why = %v", only.Why)
	}
}
