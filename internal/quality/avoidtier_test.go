package quality

import (
	"strconv"
	"strings"
	"testing"
)

// "Avoid Dolby Vision" must mean "never pick a DV release while a non-DV one exists" — a
// strict lower tier, not a score penalty a better source can outweigh. Here the DV release
// is a 4K REMUX (far higher raw quality) and the alternative is a 1080p WEB-DL; the clean
// one must still win. Both rankings agree: a target Avoid is v2's first key.
func TestAvoidedFormatLosesToLowerQualityAlternative(t *testing.T) {
	forEachRanking(t, func(t *testing.T, mode string) {
		e := engineIn(mode)
		p := Profile{FormatScores: map[string]int{"Dolby Vision": -50, "HDR10": 50, "Atmos": 50}}

		dv := NewCandidate("Obsession 2026 2160p BluRay REMUX DV HDR TrueHD Atmos 7.1-x", 40, 300)
		plain := NewCandidate("Obsession 2026 1080p WEB-DL HDR x265-y", 8, 300)

		d := e.Decide(p, []Candidate{dv, plain})
		if d.Winner == nil || d.Winner.Candidate.Name != plain.Name {
			got := "<nil>"
			if d.Winner != nil {
				got = d.Winner.Candidate.Name
			}
			t.Fatalf("the non-DV release must win despite lower quality, got %q", got)
		}
		if !strings.Contains(d.ChosenOver, "Dolby Vision") || d.DecidedBy != KeyAvoid {
			t.Errorf("ChosenOver should explain the DV release was skipped, got %q (decided by %q)", d.ChosenOver, d.DecidedBy)
		}

		// But a DV release is still grabbable when it's the ONLY option — with a heads-up.
		only := e.Decide(p, []Candidate{dv})
		if only.Winner == nil || only.Winner.Candidate.Name != dv.Name {
			t.Fatalf("with no alternative the DV release should still win")
		}
		found := false
		for _, w := range only.Why {
			if strings.Contains(w, "Only releases with a format you avoid") {
				found = true
			}
		}
		if !found {
			t.Errorf("a DV-only win should flag that only avoided releases were available, got %v", only.Why)
		}
	})
}

// Releases the avoid-tier tests share: a DV-only HEVC release (nothing to offset its −50),
// the same in H.264, and a clean 1080p one.
const (
	dvHEVC  = "Film.2021.2160p.WEB-DL.DDP5.1.DV.H.265-DVONLY"
	dvAVC   = "Film.2021.2160p.WEB-DL.DDP5.1.DV.H.264-DVAVC"
	cleanHD = "Film.2021.1080p.WEB-DL.DDP5.1.H.264-EVO"
)

// avoidDV is a saved profile whose target sets Dolby Vision to Avoid, ranking in mode.
func avoidDV(mode string, edit func(*StoredProfile)) (Profile, *Engine) {
	sp := StoredProfile{Name: "avoid-dv", AllowedResolutions: []string{"2160p", "1080p"},
		Ideal: &IdealFile{HDR: map[string]string{"DV": PrefAvoid}}}
	if edit != nil {
		edit(&sp)
	}
	p, e := storedFixture(sp)
	e.Ranking = mode
	return p, e
}

// At the default minimum score of 0, a target Avoid is the avoided tier — never a reject.
// Its −50 used to count against the floor, so a DV release with nothing to offset it was
// refused as "below the minimum format score" even when it was all there was.
func TestTargetAvoidIsATierAtTheDefaultFloor(t *testing.T) {
	forEachRanking(t, func(t *testing.T, mode string) {
		p, e := avoidDV(mode, nil)
		if p.MinFormatScore != 0 {
			t.Fatalf("fixture floor = %d, want the default 0", p.MinFormatScore)
		}
		dv, clean := NewCandidate(dvHEVC, 18, 90), NewCandidate(cleanHD, 8, 300)

		ev := e.Evaluate(p, dv)
		if !ev.Eligible || ev.RejectCode != "" {
			t.Fatalf("the DV release must stay eligible, got rejected %q (%s)", ev.RejectCode, ev.RejectReason)
		}
		if !ev.Avoided || !ev.TargetAvoided || strings.Join(ev.AvoidedFormats, ",") != "Dolby Vision" {
			t.Errorf("the DV release should be avoided for Dolby Vision, got avoided=%v target=%v %v", ev.Avoided, ev.TargetAvoided, ev.AvoidedFormats)
		}
		if ev.FormatScore != -50 {
			t.Errorf("the avoid still scores within its tier: format score %d, want -50", ev.FormatScore)
		}

		// Ranked last, below a lower-resolution clean release; nothing is rejected.
		d := e.Decide(p, []Candidate{dv, clean})
		if len(d.Rejected) != 0 || len(d.Eligible) != 2 {
			t.Fatalf("both releases should be eligible, got %d eligible, %d rejected", len(d.Eligible), len(d.Rejected))
		}
		if d.Winner.Candidate.Name != cleanHD || d.Eligible[1].Candidate.Name != dvHEVC || d.DecidedBy != KeyAvoid {
			t.Errorf("the clean release must win and DV rank last on the avoid, got winner %q, decided by %q", d.Winner.Candidate.Name, d.DecidedBy)
		}

		// Picked when nothing else is available, with the heads-up.
		only := e.Decide(p, []Candidate{dv})
		if only.Winner == nil || only.Winner.Candidate.Name != dvHEVC {
			t.Fatalf("with no alternative the DV release must be picked, got rejected %+v", only.Rejected)
		}
		if len(only.Why) == 0 || only.Why[0] != "Only releases with a format you avoid were available" {
			t.Errorf("a DV-only pick should say only avoided releases were available, got %v", only.Why)
		}
	})
}

// A raised floor is the owner's explicit minimum on what a release earns — its
// preferences, custom formats and keywords. A target Avoid neither sinks a release below
// it nor lifts one over it: the avoided tier already handles the avoid.
func TestRaisedFloorJudgesEarnedScoreNotTheAvoid(t *testing.T) {
	forEachRanking(t, func(t *testing.T, mode string) {
		p, e := avoidDV(mode, func(sp *StoredProfile) {
			sp.MinFormatScore = 10
			sp.Ideal.Codec = map[string]string{"hevc": PrefWant}
		})
		// HEVC earns 50: over the floor, still in the avoided tier for its DV.
		if ev := e.Evaluate(p, NewCandidate(dvHEVC, 18, 90)); !ev.Eligible || !ev.TargetAvoided {
			t.Errorf("DV HEVC earns 50 against a floor of 10: want eligible and avoided, got eligible=%v avoided=%v (%s)", ev.Eligible, ev.TargetAvoided, ev.RejectReason)
		}
		// Earning nothing is under the floor, DV or not — the same reject for both.
		for _, name := range []string{dvAVC, cleanHD} {
			if ev := e.Evaluate(p, NewCandidate(name, 10, 90)); ev.Eligible || ev.RejectCode != RejectMinFormatScore {
				t.Errorf("%s earns 0 against a floor of 10: want rejected %q, got eligible=%v code %q", name, RejectMinFormatScore, ev.Eligible, ev.RejectCode)
			}
		}
	})
}

// Must is still a requirement next to an Avoid: a release without it is refused.
func TestMustStillRejectsBesideAnAvoid(t *testing.T) {
	forEachRanking(t, func(t *testing.T, mode string) {
		p, e := avoidDV(mode, func(sp *StoredProfile) { sp.Ideal.Codec = map[string]string{"hevc": PrefMust} })
		if ev := e.Evaluate(p, NewCandidate(dvAVC, 18, 90)); ev.Eligible || ev.RejectCode != RejectMissingRequired {
			t.Errorf("an H.264 release under HEVC Must: want rejected %q, got eligible=%v code %q", RejectMissingRequired, ev.Eligible, ev.RejectCode)
		}
		if ev := e.Evaluate(p, NewCandidate(dvHEVC, 18, 90)); !ev.Eligible || !ev.TargetAvoided {
			t.Errorf("a DV HEVC release meets the Must and is only avoided, got eligible=%v avoided=%v (%s)", ev.Eligible, ev.TargetAvoided, ev.RejectReason)
		}
	})
}

// Only built-in Avoids leave the floor. A negative custom format or keyword the owner wrote
// is a plain score, and at the default floor it still refuses a release it pushes under 0.
func TestCustomNegativeStillCountsAgainstTheFloor(t *testing.T) {
	forEachRanking(t, func(t *testing.T, mode string) {
		p, e := avoidDV(mode, func(sp *StoredProfile) {
			sp.CustomFormats = []CustomFormat{{Name: "No GRPX", Conditions: []Condition{{Type: CondReleaseGroup, Value: "GRPX"}}}}
			sp.FormatScores = map[string]int{"No GRPX": -5}
			sp.Keywords = []Keyword{{Term: "HDTV", Score: -10}}
		})
		for _, name := range []string{"Film.2021.1080p.WEB-DL.DDP5.1.H.264-GRPX", "Film.2021.1080p.HDTV.x264-TVGRP"} {
			if ev := e.Evaluate(p, NewCandidate(name, 8, 90)); ev.Eligible || ev.RejectCode != RejectMinFormatScore {
				t.Errorf("%s: want rejected %q, got eligible=%v code %q", name, RejectMinFormatScore, ev.Eligible, ev.RejectCode)
			}
		}
	})
}

// The file side is unchanged: a DV file doesn't fit a target that avoids DV, so the
// upgrader keeps looking — but the profile would still grab it, so it isn't a file the
// profile refuses (a profile switch isn't a downgrade for it). Its real score is now the
// bar an upgrade must beat: a clean release replaces it, another DV release doesn't.
func TestAvoidedFileStillDoesNotFit(t *testing.T) {
	sp := StoredProfile{MediaType: MediaMovie, Name: "avoid-dv", AllowedResolutions: []string{"2160p", "1080p"}, UpgradesEnabled: true,
		Ideal: &IdealFile{HDR: map[string]string{"DV": PrefAvoid}, Bitrate: map[string]BitrateWindow{"2160p": {Min: 10, Max: 60}}}}
	normalize(&sp)
	cur := cf(dvHEVC, 18, 120)

	v := sp.JudgeFile(cur)
	if !v.Eligible {
		t.Errorf("a DV file is avoided, not refused: got rejected (%s)", v.Current.RejectReason)
	}
	if v.TargetMet {
		t.Error("a DV file must not meet a target that avoids Dolby Vision")
	}
	if fit := CheckFit(*sp.Ideal, sp.AllowedResolutions, v.Facts); fit.Status != FitMismatch || len(fit.Issues) != 1 || fit.Issues[0].Kind != "hdr" {
		t.Errorf("library fit = %+v, want a mismatch on hdr", fit)
	}

	forEachRanking(t, func(t *testing.T, mode string) {
		s, ctx := testService(t)
		setRanking(t, s, mode)
		saved, err := s.Create(ctx, sp)
		if err != nil {
			t.Fatal(err)
		}
		ref := "custom:" + strconv.FormatInt(saved.ID, 10)
		if _, _, ok := s.UpgradePick(ctx, ref, cur, []Candidate{NewCandidate("Film.2021.2160p.WEB-DL.DDP5.1.HDR10.H.265-CLEAN", 18, 50)}); !ok {
			t.Error("a clean 4K release should replace the avoided DV file")
		}
		if c, _, ok := s.UpgradePick(ctx, ref, cur, []Candidate{NewCandidate("Film.2021.2160p.WEB-DL.DDP5.1.DV.H.265-OTHER", 18, 50)}); ok {
			t.Errorf("another group's DV release of the same quality is no upgrade, got %q", c.Name)
		}
	})
}

// otherAvoid is a saved profile that sets the built-in AMZN format — one of "Other formats",
// not a target format — to the Avoid button's -50, ranking in mode, at the default floor.
func otherAvoid(mode string, edit func(*StoredProfile)) (Profile, *Engine) {
	sp := StoredProfile{Name: "other-avoid", AllowedResolutions: []string{"2160p", "1080p"},
		FormatScores: map[string]int{"AMZN": -50}}
	if edit != nil {
		edit(&sp)
	}
	p, e := storedFixture(sp)
	e.Ranking = mode
	return p, e
}

// An "Other formats" Avoid is the same Avoid as the target's: at the default floor of 0 it
// ranks a release last but never refuses it. Its -50 used to count against the floor, so
// "Avoid AMZN" silently acted as "Reject AMZN".
func TestOtherFormatAvoidIsATierAtTheDefaultFloor(t *testing.T) {
	const (
		amzn  = "Show.S01E01.1080p.AMZN.WEB-DL.DDP5.1.H.264-NTb"
		clean = "Show.S01E01.1080p.NF.WEB-DL.DDP5.1.H.264-FLUX"
		amzn4 = "Show.S01E01.2160p.AMZN.WEB-DL.DDP5.1.H.265-NTb"
	)
	forEachRanking(t, func(t *testing.T, mode string) {
		p, e := otherAvoid(mode, nil)
		if p.MinFormatScore != 0 || p.FormatScores["AMZN"] != -50 {
			t.Fatalf("fixture floor %d, AMZN %d: want the default 0 and the Avoid button's -50", p.MinFormatScore, p.FormatScores["AMZN"])
		}
		ev := e.Evaluate(p, NewCandidate(amzn, 2, 90))
		if !ev.Eligible || ev.RejectCode != "" {
			t.Fatalf("the AMZN release must stay eligible, got rejected %q (%s)", ev.RejectCode, ev.RejectReason)
		}
		if !ev.Avoided || !ev.TargetAvoided || ev.CustomAvoided || strings.Join(ev.AvoidedFormats, ",") != "AMZN" {
			t.Errorf("the AMZN release should be in the avoided tier for AMZN, got avoided=%v tier=%v custom=%v %v", ev.Avoided, ev.TargetAvoided, ev.CustomAvoided, ev.AvoidedFormats)
		}
		if ev.FormatScore != -50 || ev.CustomScore != -50 {
			t.Errorf("the avoid still scores within its tier: format %d custom %d, want -50 both", ev.FormatScore, ev.CustomScore)
		}

		// Ranked below a clean release of the same resolution, and below a clean release
		// of a lower one too: it's a tier under both rankings, not a score resolution beats.
		for _, avoided := range []string{amzn, amzn4} {
			d := e.Decide(p, []Candidate{NewCandidate(avoided, 2, 90), NewCandidate(clean, 2, 90)})
			if len(d.Rejected) != 0 || len(d.Eligible) != 2 {
				t.Fatalf("%s: both releases should be eligible, got %d eligible, %d rejected", avoided, len(d.Eligible), len(d.Rejected))
			}
			if d.Winner.Candidate.Name != clean || d.DecidedBy != KeyAvoid {
				t.Errorf("%s: the clean release must win on the avoid, got %q decided by %q", avoided, d.Winner.Candidate.Name, d.DecidedBy)
			}
		}

		// Picked when it's all there is, with the heads-up.
		only := e.Decide(p, []Candidate{NewCandidate(amzn, 2, 90)})
		if only.Winner == nil || only.Winner.Candidate.Name != amzn {
			t.Fatalf("with no alternative the AMZN release must be picked, got rejected %+v", only.Rejected)
		}
		if len(only.Why) == 0 || only.Why[0] != "Only releases with a format you avoid were available" {
			t.Errorf("an AMZN-only pick should say only avoided releases were available, got %v", only.Why)
		}
	})
}

// The owner's own custom format and "Name contains" keyword at the same -50 are scores, and
// the default floor of 0 still refuses what they push under it — the deliberate power-user
// way to keep something out.
func TestCustomFormatAtAvoidScoreStillRefusedAtTheDefaultFloor(t *testing.T) {
	forEachRanking(t, func(t *testing.T, mode string) {
		p, e := otherAvoid(mode, func(sp *StoredProfile) {
			sp.CustomFormats = []CustomFormat{{Name: "No NTb", Conditions: []Condition{{Type: CondReleaseGroup, Value: "NTb"}}}}
			sp.FormatScores = map[string]int{"No NTb": -50}
			sp.Keywords = []Keyword{{Term: "HDTV", Score: -50}}
		})
		for _, name := range []string{"Show.S01E01.1080p.WEB-DL.DDP5.1.H.264-NTb", "Show.S01E01.1080p.HDTV.x264-TVGRP"} {
			if ev := e.Evaluate(p, NewCandidate(name, 2, 90)); ev.Eligible || ev.RejectCode != RejectMinFormatScore {
				t.Errorf("%s: want rejected %q, got eligible=%v code %q", name, RejectMinFormatScore, ev.Eligible, ev.RejectCode)
			}
		}
		// A raised floor still judges what the release earns: an AMZN Avoid neither sinks a
		// release under it nor lifts one over it.
		p, e = otherAvoid(mode, func(sp *StoredProfile) {
			sp.MinFormatScore = 10
			sp.FormatScores["HEVC"] = 50
		})
		if ev := e.Evaluate(p, NewCandidate("Show.S01E01.1080p.AMZN.WEB-DL.DDP5.1.H.265-NTb", 2, 90)); !ev.Eligible || !ev.TargetAvoided {
			t.Errorf("AMZN HEVC earns 50 against a floor of 10: want eligible and avoided, got eligible=%v (%s)", ev.Eligible, ev.RejectReason)
		}
		if ev := e.Evaluate(p, NewCandidate("Show.S01E01.1080p.AMZN.WEB-DL.DDP5.1.H.264-NTb", 2, 90)); ev.Eligible || ev.RejectCode != RejectMinFormatScore {
			t.Errorf("AMZN H.264 earns 0 against a floor of 10: want rejected %q, got eligible=%v code %q", RejectMinFormatScore, ev.Eligible, ev.RejectCode)
		}
	})
}
