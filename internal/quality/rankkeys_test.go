package quality

import (
	"math"
	"math/rand"
	"strings"
	"testing"
)

// keyProfile is a 4K/1080p profile that prefers HEVC, Dolby Vision and Atmos, scores a
// custom format −5, and has a 1080p bitrate window — every key has something to read.
func keyProfile() (Profile, *Engine) {
	return storedFixture(StoredProfile{Name: "keys", AllowedResolutions: []string{"2160p", "1080p"},
		CustomFormats: []CustomFormat{{Name: "Meh group", Conditions: []Condition{{Type: CondReleaseGroup, Value: "MEH"}}}},
		FormatScores:  map[string]int{"Meh group": -5},
		Ideal: &IdealFile{
			Codec:   map[string]string{"hevc": PrefWant},
			HDR:     map[string]string{"DV": PrefWant},
			Audio:   map[string]string{"atmos": PrefMust},
			Bitrate: map[string]BitrateWindow{"1080p": {Min: 5, Max: 15}},
		}})
}

func TestRankKeyFields(t *testing.T) {
	p, e := keyProfile()
	const rt = 120
	cands := []Candidate{
		NewCandidate("Film.2021.2160p.WEB-DL.DDP5.1.Atmos.DV.H.265-GOOD", 12, 50),
		NewCandidate("Film.2021.1080p.BluRay.Atmos.x264-DISCY", 12, 50),
		NewCandidate("Film.2021.1080p.WEBRip.Atmos.x265-THIN", 2, 50),
		NewCandidate("Film.2021.1080p.WEB-DL.Atmos.x264-MEH", 8, 50),
		NewCandidate("Film.2021.1080p.WEB-DL.Atmos.x264.PROPER-FIX", 8, 50),
		NewCandidate("Film.2021.1080p.Atmos.x264-NOSRC", 8, 50),
		NewCandidate("Film.2021.2160p.WEB-DL.Atmos.DV.H.265-TINY", 1, 50),
		NewCandidate("Film.2021.1080p.HDTV.Atmos.x264-TELLY", 8, 50),
	}
	d := e.Decide(p, withRuntime(cands, rt))
	if len(d.Eligible) != len(cands) {
		t.Fatalf("eligible %d of %d: %+v", len(d.Eligible), len(cands), d.Rejected)
	}
	byGroup := map[string]Evaluation{}
	for _, ev := range d.Eligible {
		byGroup[ev.Candidate.Release.Group] = ev
	}
	br := func(gb, eff float64) float64 { return BitrateMbps(gb, rt) * eff }
	cases := []struct {
		group                        string
		targetAvoided, customAvoided bool
		res, prefs, custom, source   int
		proper, inWindow             bool
		eff                          float64
	}{
		// Goal tier, three preferences (HEVC, Dolby Vision, the required Atmos), WEB.
		{"GOOD", false, false, 5, 3, 0, srcTierWEB, false, true, br(12, 1.6)},
		{"DISCY", false, false, 4, 1, 0, srcTierBluRay, false, true, br(12, 1)},
		// Under the 1080p floor: avoided in the target's own tier, out of the window — and
		// under a third of the best bitrate, so its preferences are waived as well.
		{"THIN", true, false, 4, 0, 0, srcTierWEB, false, false, br(2, 1.6)},
		// A −5 custom format: a custom avoid, a score with its negative kept.
		{"MEH", false, true, 4, 1, -5, srcTierWEB, false, true, br(8, 1)},
		{"FIX", false, false, 4, 1, 0, srcTierWEB, true, true, br(8, 1)},
		{"NOSRC", false, false, 4, 1, 0, srcTierUnknown, false, true, br(8, 1)},
		// Its bitrate collapsed against the best on offer: the preferences are waived.
		{"TINY", false, false, 5, 0, 0, srcTierWEB, false, true, br(1, 1.6)},
		{"TELLY", false, false, 4, 1, 0, srcTierHDTV, false, true, br(8, 1)},
	}
	for _, tc := range cases {
		ev, ok := byGroup[tc.group]
		if !ok {
			t.Fatalf("%s missing", tc.group)
		}
		if ev.TargetAvoided != tc.targetAvoided || ev.CustomAvoided != tc.customAvoided || ev.ResTier != tc.res ||
			ev.PrefCount != tc.prefs || ev.CustomScore != tc.custom || ev.SourceTier != tc.source ||
			ev.ProperFix != tc.proper || ev.InWindow != tc.inWindow || ev.GroupTier != 0 || math.Abs(ev.EffBitrate-tc.eff) > 1e-9 {
			t.Errorf("%s: target-avoided %v custom-avoided %v res %d prefs %d custom %d source %d proper %v window %v eff %.3f; want %+v",
				tc.group, ev.TargetAvoided, ev.CustomAvoided, ev.ResTier, ev.PrefCount, ev.CustomScore, ev.SourceTier,
				ev.ProperFix, ev.InWindow, ev.EffBitrate, tc)
		}
	}
	if !byGroup["TINY"].BonusWaived {
		t.Error("TINY's bonus should be waived")
	}
	// Without a runtime there is no bitrate: no effective bitrate, nothing out of window.
	ev := e.Evaluate(p, NewCandidate("Film.2021.1080p.WEBRip.Atmos.x265-THIN", 2, 50))
	if ev.EffBitrate != 0 || !ev.InWindow || ev.TargetAvoided {
		t.Errorf("no runtime: %+v", ev)
	}
	// An Unwanted avoid (a low-quality group) and a target Avoid are target avoids.
	if ev := e.Evaluate(p, NewCandidate("Film.2021.1080p.WEBRip.Atmos.x264-YTS", 8, 50)); !ev.TargetAvoided || ev.CustomAvoided {
		t.Errorf("YTS: %+v", ev)
	}
	pDV, eDV := storedFixture(StoredProfile{Name: "nodv", Ideal: &IdealFile{HDR: map[string]string{"DV": PrefAvoid}}})
	if ev := eDV.Evaluate(pDV, NewCandidate("Film.2021.2160p.WEB-DL.DV.HDR10.H.265-GRP", 8, 50)); !ev.TargetAvoided || ev.CustomAvoided {
		t.Errorf("Avoid DV: %+v", ev)
	}
	// A negative keyword is a custom avoid only — and, as before, no avoided tier in v1.
	kw := Profile{Keywords: []Keyword{{Term: "HDTV", Score: -10}}}
	if ev := NewDefaultEngine().Evaluate(kw, NewCandidate("Film.2021.1080p.HDTV.x264-GRP", 4, 50)); !ev.CustomAvoided || ev.TargetAvoided || ev.Avoided {
		t.Errorf("negative keyword: %+v", ev)
	}
	// WEB-DL and WEBRip share a tier; a cam is below an unstated source.
	if rankSourceTier("WEB-DL") != rankSourceTier("WEBRip") || rankSourceTier("CAM") >= rankSourceTier("") {
		t.Error("source tiers")
	}
}

// Property: over random releases and profiles, the keys agree with what v1 already
// computed — they describe the evaluation, they never contradict it.
func TestRankKeysAgreeWithClassicScores(t *testing.T) {
	rng := rand.New(rand.NewSource(14))
	parts := [][]string{
		{"2160p", "1080p", "720p", "480p", ""},
		{"BluRay.REMUX", "BluRay", "WEB-DL", "WEBRip", "WEB", "HDTV", "DVDRip", "", "HDCAM"},
		{"x265", "x264", "AV1", "XviD", ""},
		{"DV", "DV.HDR10", "HDR10+", "HDR", "HLG", ""},
		{"TrueHD.Atmos", "DTS-HD.MA", "DDP5.1", "AAC", ""},
		{"PROPER", "REPACK", "IMAX", "UPSCALED", "FRENCH", "MULTi", ""},
		{"GRP", "YTS", "MEH", "SPARKS", "EVO"},
	}
	fixtures := rankFixtures()
	for i := 0; i < 3000; i++ {
		var b strings.Builder
		b.WriteString("Film.2021")
		for _, opts := range parts[:len(parts)-1] {
			if o := opts[rng.Intn(len(opts))]; o != "" {
				b.WriteString("." + o)
			}
		}
		b.WriteString("-" + parts[len(parts)-1][rng.Intn(len(parts[len(parts)-1]))])
		c := NewCandidate(b.String(), 0.5+rng.Float64()*60, rng.Intn(3)*rng.Intn(200)).WithLanguage("en")
		if rng.Intn(3) > 0 {
			c = c.WithRuntime(40 + rng.Intn(140))
		}
		f := fixtures[rng.Intn(len(fixtures))]
		ev := f.e.Evaluate(f.p, c)
		if !ev.Eligible {
			continue
		}
		r := c.Release
		switch {
		case ev.TargetAvoided && !ev.Avoided:
			t.Fatalf("%s under %s: a target avoid outside v1's avoided tier", c.Name, f.name)
		case ev.Avoided && !ev.TargetAvoided && !ev.CustomAvoided:
			t.Fatalf("%s under %s: avoided by neither kind", c.Name, f.name)
		case ev.ResTier != resRank[r.Resolution]:
			t.Fatalf("%s: res tier %d", c.Name, ev.ResTier)
		case ev.PrefCount > len(ev.Matched):
			t.Fatalf("%s: %d preferences, %d matched", c.Name, ev.PrefCount, len(ev.Matched))
		case ev.ProperFix != (r.Proper || r.Repack):
			t.Fatalf("%s: proper", c.Name)
		case (ev.SourceTier > srcTierWEB) != (sourceTier(r.Source) > webTier):
			t.Fatalf("%s: source tier %d disagrees with the gate tiers", c.Name, ev.SourceTier)
		case c.RuntimeMin <= 0 && ev.EffBitrate != 0:
			t.Fatalf("%s: a bitrate with no runtime", c.Name)
		case !ev.InWindow && !ev.TargetAvoided:
			t.Fatalf("%s under %s: out of the window but not avoided", c.Name, f.name)
		case ev.TargetScore+ev.CustomScore != ev.FormatScore:
			t.Fatalf("%s: the score split no longer adds up", c.Name)
		}
	}
}
