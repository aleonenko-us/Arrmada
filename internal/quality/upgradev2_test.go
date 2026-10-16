package quality

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
)

// Upgrades under ranking v2 (upgradev2.go): the trigger maps to keys, the keys that never
// upgrade never do, the import gate's verdict is the sweep's, and every upgrade decision
// that changed from classic ranking is pinned and explained (testdata/upgrade_golden.json).

// The trigger matrix, key by key. Under v2 "any" is a list too: group, network, bitrate,
// health and size are never a reason to re-download a file.
func TestV2TriggerAllowsMatrix(t *testing.T) {
	keys := []RankKey{KeyResolution, KeyPreferences, KeyCustom, KeySource, KeyProper, KeyAvoid,
		KeyGroup, KeyNetwork, KeyBitrate, KeyHealth, KeySize, KeyTie, ""}
	any := []bool{true, true, true, true, true, true, false, false, false, false, false, false, false}
	want := map[string][]bool{
		TriggerAny:        any,
		"":                any,
		"nonsense":        any, // read as any
		TriggerSource:     {true, true, true, true, false, true, false, false, false, false, false, false, false},
		TriggerFormat:     {true, true, true, false, false, true, false, false, false, false, false, false, false},
		TriggerResolution: {true, false, false, false, false, true, false, false, false, false, false, false, false},
	}
	for trig, row := range want {
		for i, k := range keys {
			if got := v2TriggerAllows(trig, k); got != row[i] {
				t.Errorf("v2TriggerAllows(%q, %q) = %v, want %v", trig, k, got, row[i])
			}
		}
	}
}

// v2 is the default: an install that never saved "quality_ranking" grabs and upgrades by
// the ordered keys. Saving "v1" — the rollback — puts both grabs and upgrades back on
// classic ranking, and clearing it returns to the default.
func TestRankingDefaultIsV2AndV1RollsBack(t *testing.T) {
	s, ctx := testService(t)
	if got := s.RankingMode(ctx); got != RankingV2 {
		t.Fatalf("default mode = %q, want v2", got)
	}
	var nilSvc *Service
	if nilSvc.RankingMode(ctx) != RankingV2 {
		t.Fatal("a service-less read must be the default too")
	}
	grab := upgradeProfile(t, s, StoredProfile{Name: "grab", AllowedResolutions: []string{"2160p", "1080p"},
		Ideal: &IdealFile{Codec: map[string]string{"hevc": PrefWant}, HDR: map[string]string{"HDR10": PrefWant, "HDR10+": PrefWant},
			Audio: map[string]string{"atmos": PrefWant}}})
	lean := upgradeProfile(t, s, StoredProfile{Name: "lean", SmallBias: 4})
	uhd := NewCandidate("Film.2021.2160p.WEB-DL.DDP5.1.H.265-UHD", 20, 50)
	fhd := NewCandidate("Film.2021.1080p.BluRay.HDR10+.TrueHD.Atmos.x265-FHD", 14, 50)
	cur := cf("Film.2021.1080p.WEB-DL.x264-GRP", 8, 120)
	small := NewCandidate("Film.2021.1080p.WEB-DL.x264-SMALL", 3, 100).WithRuntime(120)
	for _, tc := range []struct {
		saved, wantMode, wantPick string
		wantUpgrade               bool
	}{
		{"", RankingV2, uhd.Name, false},   // never saved: v2
		{"v1", RankingV1, fhd.Name, true},  // the rollback: classic grabs and classic upgrades
		{"v2", RankingV2, uhd.Name, false}, // chosen explicitly
		{"  ", RankingV2, uhd.Name, false}, // a blank is no choice
	} {
		setRanking(t, s, tc.saved)
		d := s.Decide(ctx, grab, []Candidate{fhd, uhd})
		if d.Ranking != tc.wantMode || winnerName(d) != tc.wantPick {
			t.Errorf("saved %q: ranked %q picking %s, want %q picking %s", tc.saved, d.Ranking, winnerName(d), tc.wantMode, tc.wantPick)
		}
		// Classic ranking took a smaller copy of the same release as a size-lean "gain"; v2
		// never upgrades on size.
		if _, _, ok := s.UpgradePick(ctx, lean, cur, []Candidate{small}); ok != tc.wantUpgrade {
			t.Errorf("saved %q: size-lean upgrade = %v, want %v", tc.saved, ok, tc.wantUpgrade)
		}
	}
}

// upgradeProfile saves a movie profile and returns its ref.
func upgradeProfile(t *testing.T, s *Service, sp StoredProfile) string {
	t.Helper()
	sp.MediaType, sp.UpgradesEnabled = MediaMovie, true
	saved, err := s.Create(t.Context(), sp)
	if err != nil {
		t.Fatal(err)
	}
	return "custom:" + strconv.FormatInt(saved.ID, 10)
}

// Each trigger takes exactly the keys it names, on the sweep and at the import gate alike,
// and a same-group PROPER whatever the trigger.
func TestV2TriggerMatrixDecidesUpgrades(t *testing.T) {
	s, ctx := testService(t)
	setRanking(t, s, RankingV2)
	cur := "Film.2021.1080p.WEB-DL.x264-GRP"
	cands := []struct {
		name string
		key  RankKey
	}{
		{"Film.2021.2160p.WEB-DL.x264-OTHER", KeyResolution},
		{"Film.2021.1080p.WEB-DL.x265-OTHER", KeyPreferences},
		{"Film.2021.1080p.WEB-DL.IMAX.x264-OTHER", KeyCustom},
		{"Film.2021.1080p.BluRay.x264-OTHER", KeySource},
		{"Film.2021.1080p.WEB-DL.x264.PROPER-OTHER", KeyProper},
	}
	for _, trig := range []string{TriggerAny, TriggerSource, TriggerFormat, TriggerResolution} {
		ref := upgradeProfile(t, s, StoredProfile{Name: "matrix " + trig, UpgradeTrigger: trig,
			FormatScores: map[string]int{"HEVC": 50}, Keywords: []Keyword{{Term: "IMAX", Score: 30}}})
		for _, c := range cands {
			want := v2TriggerAllows(trig, c.key)
			_, key, ok := s.UpgradePick(ctx, ref, cf(cur, 8, 120), []Candidate{NewCandidate(c.name, 8, 100).WithRuntime(120)})
			if ok != want || (ok && key != c.key) {
				t.Errorf("%s ← %s: ok=%v key=%q, want ok=%v key=%q", trig, c.name, ok, key, want, c.key)
			}
			if gate := s.IsQualityUpgrade(ctx, ref, c.name, 8, cf(cur, 8, 120)); gate != want {
				t.Errorf("%s ← %s: import gate %v, want %v", trig, c.name, gate, want)
			}
		}
		// A PROPER of the release we have is a fix: taken whatever the trigger.
		proper := "Film.2021.1080p.WEB-DL.x264.PROPER-GRP"
		if _, key, ok := s.UpgradePick(ctx, ref, cf(cur, 8, 120), []Candidate{NewCandidate(proper, 8, 100).WithRuntime(120)}); !ok || key != KeyProper {
			t.Errorf("%s ← same-group PROPER: ok=%v key=%q", trig, ok, key)
		}
		if !s.IsQualityUpgrade(ctx, ref, proper, 8, cf(cur, 8, 120)) {
			t.Errorf("%s ← same-group PROPER refused at the import gate", trig)
		}
	}
}

// A file from a release the profile avoids (YTS, a default low-quality group) is replaced
// by a clean copy that gives nothing up, whatever "Replace for" says — under both rankings,
// on the sweep and at the import gate. A clean copy at a lower resolution still isn't one.
func TestLeavingAnAvoidedFileCountsUnderEveryTrigger(t *testing.T) {
	s, ctx := testService(t)
	cur := cf("Film.2021.1080p.WEB-DL.x264-YTS", 2, 120)
	clean := "Film.2021.1080p.WEB-DL.x264-GRP"
	lower := "Film.2021.720p.WEB-DL.x264-GRP"
	for _, mode := range []string{RankingV2, RankingV1} {
		setRanking(t, s, mode)
		for _, trig := range []string{TriggerAny, TriggerSource, TriggerFormat, TriggerResolution} {
			ref := upgradeProfile(t, s, StoredProfile{Name: "avoid " + mode + " " + trig, UpgradeTrigger: trig})
			if _, key, ok := s.UpgradePick(ctx, ref, cur, []Candidate{NewCandidate(clean, 8, 100).WithRuntime(120)}); !ok || key != KeyAvoid {
				t.Errorf("%s/%s: YTS ← clean copy: ok=%v key=%q, want an avoid upgrade", mode, trig, ok, key)
			}
			if !s.IsQualityUpgrade(ctx, ref, clean, 8, cur) {
				t.Errorf("%s/%s: YTS ← clean copy refused at the import gate", mode, trig)
			}
			if _, _, ok := s.UpgradePick(ctx, ref, cur, []Candidate{NewCandidate(lower, 4, 100).WithRuntime(120)}); ok {
				t.Errorf("%s/%s: YTS 1080p ← clean 720p was taken", mode, trig)
			}
		}
	}
}

// Under v2 a candidate that only wins on the release group, its health, its size or the
// network never replaces a file — and classic ranking's two such upgrades (a size-lean
// gain, a dead torrent) are gone. The bitrate step is its own rule and stays (see
// TestBitrateUpgradeUnderBothRankings).
func TestV2NeverUpgradesOnGroupHealthSizeOrNetwork(t *testing.T) {
	cur := cf("Film.2021.1080p.WEB-DL.x264-GRP", 8, 120)
	cases := []struct {
		name string
		sp   StoredProfile
		cand Candidate
		v1   bool // what classic ranking does with it
	}{
		{"a tier-1 group", StoredProfile{GroupTiers: &GroupTiers{Tier1: []string{"FLUX"}}},
			NewCandidate("Film.2021.1080p.WEB-DL.x264-FLUX", 8, 100).WithRuntime(120), false},
		{"better seeded", StoredProfile{},
			NewCandidate("Film.2021.1080p.WEB-DL.x264-GRP2", 8, 5000).WithRuntime(120), false},
		{"a heavier file", StoredProfile{},
			NewCandidate("Film.2021.1080p.WEB-DL.x264-BIG", 30, 100).WithRuntime(120), false},
		// Classic ranking scores the size lean (−GB × bias), so a smaller file of the same
		// release "scores higher" and the any trigger took it.
		{"a smaller file under a size lean", StoredProfile{SmallBias: 4},
			NewCandidate("Film.2021.1080p.WEB-DL.x264-SMALL", 3, 100).WithRuntime(120), true},
		{"on the preferred network", StoredProfile{},
			usenet(NewCandidate("Film.2021.1080p.WEB-DL.x264-NZB", 8, 0).WithRuntime(120)), false},
		// A dead torrent never downloads; classic ranking's upgrade took it on resolution.
		{"a dead torrent of a higher resolution", StoredProfile{},
			NewCandidate("Film.2021.2160p.WEB-DL.x264-DEAD", 20, 0).WithRuntime(120), true},
	}
	forEachRanking(t, func(t *testing.T, mode string) {
		s, ctx := testService(t)
		setRanking(t, s, mode)
		if err := s.settings.Set(ctx, KeyNetworkPreference, NetworkUsenet); err != nil {
			t.Fatal(err)
		}
		for _, tc := range cases {
			sp := tc.sp
			sp.Name = tc.name
			ref := upgradeProfile(t, s, sp)
			_, key, ok := s.UpgradePick(ctx, ref, cur, []Candidate{tc.cand})
			want := mode == RankingV1 && tc.v1
			if ok != want {
				t.Errorf("%s: upgrade ok=%v (key %q), want %v", tc.name, ok, key, want)
			}
		}
	})
}

func usenet(c Candidate) Candidate {
	c.Usenet = true
	return c
}

// The bitrate step (IsBitrateUpgrade) decides the same under both rankings: a heavier encode
// of the same quality clears it, one under the margin doesn't, and one that gives up quality
// for weight never replaces the file.
func TestBitrateUpgradeUnderBothRankings(t *testing.T) {
	forEachRanking(t, func(t *testing.T, mode string) {
		s, ctx := testService(t)
		setRanking(t, s, mode)
		ref := upgradeProfile(t, s, StoredProfile{Name: "step", UpgradeMinPercent: 25, UpgradeTrigger: TriggerResolution,
			FormatScores: map[string]int{"HEVC": 50}})
		cur := cf("Film.2021.1080p.WEB-DL.x265-GRP", 4, 120)
		cases := []struct {
			cand string
			gb   float64
			want bool
		}{
			{"Film.2021.1080p.WEB-DL.x265-HEAVY", 9, true},    // +125%: clears the step
			{"Film.2021.1080p.WEB-DL.x265-LIGHT", 4.4, false}, // +10%: under the 20% floor
			{"Film.2021.1080p.WEB-DL.x264-HEAVY", 9, false},   // heavier, but gives up the HEVC it has
			{"Film.2021.1080p.WEB-DL.x264-HUGE", 30, false},   // codec-normalised or not, it loses HEVC
		}
		for _, tc := range cases {
			_, key, ok := s.UpgradePick(ctx, ref, cur, []Candidate{NewCandidate(tc.cand, tc.gb, 100).WithRuntime(120)})
			if ok != tc.want || (ok && key != KeyBitrate) {
				t.Errorf("%s (%.1f GB): ok=%v key=%q, want %v (bitrate)", tc.cand, tc.gb, ok, key, tc.want)
			}
			if gate := s.IsBitrateUpgrade(ctx, ref, Encode{SizeGB: tc.gb, Codec: NewCandidate(tc.cand, 0, 0).Release.Codec},
				Encode{SizeGB: 4, Codec: cur.release().Codec}, 120); tc.want && !gate {
				t.Errorf("%s: the import gate's bitrate check refused what the sweep took", tc.cand)
			}
		}
	})
}

// --- The shared fixture set -------------------------------------------------------------

// upgradeFixtureProfiles are the profiles the upgrade fixtures run under.
func upgradeFixtureProfiles() map[string]StoredProfile {
	target := &IdealFile{Codec: map[string]string{"hevc": PrefWant}, HDR: map[string]string{"HDR10": PrefWant},
		Audio: map[string]string{"atmos": PrefWant}}
	return map[string]StoredProfile{
		"plain-any":         {AllowedResolutions: []string{"2160p", "1080p", "720p"}},
		"target-any":        {AllowedResolutions: []string{"2160p", "1080p", "720p"}, Ideal: target},
		"target-format":     {AllowedResolutions: []string{"2160p", "1080p", "720p"}, Ideal: target, UpgradeTrigger: TriggerFormat},
		"target-resolution": {AllowedResolutions: []string{"2160p", "1080p", "720p"}, Ideal: target, UpgradeTrigger: TriggerResolution},
		"avoid-dv-source": {AllowedResolutions: []string{"2160p", "1080p", "720p"}, UpgradeTrigger: TriggerSource,
			Ideal: &IdealFile{HDR: map[string]string{"DV": PrefAvoid}}},
		"custom-any": {AllowedResolutions: []string{"2160p", "1080p", "720p"},
			CustomFormats: []CustomFormat{
				{Name: "No GRPX", Conditions: []Condition{{Type: CondReleaseGroup, Value: "GRPX"}}},
				{Name: "Good group", Conditions: []Condition{{Type: CondReleaseGroup, Value: "SPARKS|EVO"}}},
			},
			FormatScores: map[string]int{"No GRPX": -5, "Good group": 15},
			Keywords:     []Keyword{{Term: "IMAX", Score: 30}}},
		"lean-any": {AllowedResolutions: []string{"2160p", "1080p", "720p"}, SmallBias: 4,
			Ideal: &IdealFile{Codec: map[string]string{"hevc": PrefWant}}},
		"must-hevc": {Ideal: &IdealFile{Codec: map[string]string{"hevc": PrefMust}}},
	}
}

// upgradeFixtureFiles are the files on disk (two-hour films).
func upgradeFixtureFiles() []CurrentFile {
	return []CurrentFile{
		cf("Film.2021.1080p.WEB-DL.DDP5.1.H.264-EVO", 8, 120),
		cf("Film.2021.1080p.WEBRip.x264-YTS", 2, 120), // a low-quality group
		cf("Film.2021.1080p.WEB-DL.DDP5.1.Atmos.DV.H.265-FLUX", 10, 120),
		cf("Film.2021.720p.BluRay.x264-SEVEN", 5, 120),
		cf("Film.2021.2160p.WEB-DL.DDP5.1.H.265-GRPC", 18, 120),
	}
}

// upgradeFixtureCands are the releases on offer.
func upgradeFixtureCands() []Candidate {
	type c struct {
		name    string
		gb      float64
		seeders int
	}
	list := []c{
		{"Film.2021.2160p.UHD.BluRay.REMUX.DV.HDR10.TrueHD.Atmos.7.1-FraMeSToR", 70, 40},
		{"Film.2021.2160p.UHD.BluRay.x265.HDR10.DTS-HD.MA.5.1-GRPA", 30, 80},
		{"Film.2021.2160p.WEB-DL.DDP5.1.Atmos.DV.HDR10.H.265-FLUX", 22, 210},
		{"Film.2021.2160p.WEB-DL.H.265-GRPX", 20, 0},
		{"Film.2021.2160p.WEBRip.DDP5.1.HDR10.x265-GRPB", 16, 60},
		{"Film.2021.1080p.BluRay.x264.DTS-HD.MA.5.1-SPARKS", 14, 180},
		{"Film.2021.1080p.BluRay.x264.PROPER.DTS-HD.MA.5.1-SPARKS", 14, 20},
		{"Film.2021.1080p.WEB-DL.DDP5.1.H.264.PROPER-EVO", 8, 100},
		{"Film.2021.1080p.WEB-DL.DDP5.1.H.264-GRPX", 8.2, 300},
		{"Film.2021.1080p.WEB-DL.x265.10bit.AAC5.1-PSA", 2.2, 400},
		{"Film.2021.1080p.WEB-DL.IMAX.x264-IMX", 9, 40},
		{"Film.2021.1080p.WEB-DL.x264-DEAD", 9, 0},
		{"Film.2021.1080p.WEB-DL.DDP5.1.Atmos.H.264-NTb", 7, 90},
		{"Film.2021.1080p.WEBRip.x264-GRPD", 7.6, 30},
		{"Film.2021.1080p.WEB-DL.DDP5.1.HDR10.H.265-GRPE", 6, 50},
		{"Film.2021.720p.WEB-DL.AV1-AVONE", 1.2, 45},
	}
	out := make([]Candidate, len(list))
	for i, x := range list {
		out[i] = NewCandidate(x.name, x.gb, x.seeders).WithRuntime(120).WithLanguage("en")
	}
	return out
}

// fixtureRefs saves every fixture profile on s, by name.
func fixtureRefs(t *testing.T, s *Service) map[string]string {
	t.Helper()
	refs := map[string]string{}
	for name, sp := range upgradeFixtureProfiles() {
		sp.Name = name
		refs[name] = upgradeProfile(t, s, sp)
	}
	return refs
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// The import gate's verdict is the sweep's, on one shared fixture set, under both rankings.
// A candidate shaped like the gate's — no runtime, no seeder term — is judged identically by
// both (the sweep's own resolution check aside: the gate's caller refuses a lower resolution
// before it asks). A real candidate the sweep takes is never refused by the gate: a quality
// gain is a quality gain there too, and the bitrate step is the same IsBitrateUpgrade.
func TestIsQualityUpgradeAgreesWithUpgradePick(t *testing.T) {
	forEachRanking(t, func(t *testing.T, mode string) {
		s, ctx := testService(t)
		setRanking(t, s, mode)
		refs := fixtureRefs(t, s)
		taken := 0
		for _, name := range sortedKeys(refs) {
			ref := refs[name]
			for _, cur := range upgradeFixtureFiles() {
				curRes := resRank[cur.release().Resolution]
				for _, real := range upgradeFixtureCands() {
					gateShaped := NewCandidate(real.Name, real.SizeGB, 1_000_000)
					_, _, sweep := s.UpgradePick(ctx, ref, cur, []Candidate{gateShaped})
					gate := s.IsQualityUpgrade(ctx, ref, real.Name, real.SizeGB, cur)
					lower := resRank[gateShaped.Release.Resolution] < curRes
					switch {
					case lower && mode == RankingV2 && gate:
						t.Errorf("%s: %s ← %s: v2's gate took a lower resolution", name, cur.Release, real.Name)
					case !lower && sweep != gate:
						t.Errorf("%s: %s ← %s: sweep %v, import gate %v", name, cur.Release, real.Name, sweep, gate)
					}
					// The real release, with its seeders and runtime.
					pick, key, ok := s.UpgradePick(ctx, ref, cur, []Candidate{real})
					if !ok {
						continue
					}
					taken++
					if mode == RankingV2 && (key == KeyGroup || key == KeyHealth || key == KeySize || key == KeyNetwork) {
						t.Errorf("%s: %s ← %s: upgraded on %q", name, cur.Release, real.Name, key)
					}
					higher := resRank[pick.Release.Resolution] > curRes
					if key == KeyBitrate || higher {
						continue // the gate takes these before it asks IsQualityUpgrade
					}
					if !s.IsQualityUpgrade(ctx, ref, pick.Name, pick.SizeGB, cur) {
						t.Errorf("%s: %s ← %s: the sweep grabbed it (%s) and the import gate refuses it", name, cur.Release, pick.Name, key)
					}
				}
			}
		}
		if taken == 0 {
			t.Fatal("no fixture upgraded at all")
		}
	})
}

// --- The upgrade golden -----------------------------------------------------------------

// goldenUpgrade is one fixture's verdict under both rankings: the deciding key, or "no".
// Why explains a verdict that changed from classic ranking to v2.
type goldenUpgrade struct {
	V1  string `json:"v1"`
	V2  string `json:"v2"`
	Why string `json:"why,omitempty"`
}

// What every upgrade fixture decides under classic ranking and under v2 is pinned, and every
// decision that changed carries the reason it changed — worked out from the evaluations, so
// a change nobody can explain fails here instead of reaching a library. Regenerate with
// -update when a change is intended, and read the diff:
//
//	go test ./internal/quality -run TestUpgradeGolden -update
func TestUpgradeGolden(t *testing.T) {
	s, ctx := testService(t)
	refs := fixtureRefs(t, s)
	type fixture struct {
		name, ref string
		cur       CurrentFile
		cand      Candidate
		keys      [2]RankKey
		oks       [2]bool
	}
	var all []*fixture
	for _, name := range sortedKeys(refs) {
		for _, cur := range upgradeFixtureFiles() {
			for _, cand := range upgradeFixtureCands() {
				all = append(all, &fixture{name: name, ref: refs[name], cur: cur, cand: cand})
			}
		}
	}
	for i, mode := range []string{RankingV1, RankingV2} {
		setRanking(t, s, mode)
		for _, f := range all {
			_, f.keys[i], f.oks[i] = s.UpgradePick(ctx, f.ref, f.cur, []Candidate{f.cand})
		}
	}
	got := map[string]goldenUpgrade{}
	for _, f := range all {
		g := goldenUpgrade{V1: verdict(f.keys[0], f.oks[0]), V2: verdict(f.keys[1], f.oks[1])}
		if g.V1 != g.V2 {
			if g.Why = whyUpgradeChanged(ctx, s, f.ref, f.cur, f.cand, f.keys, f.oks); g.Why == "" {
				t.Errorf("%s: %s ← %s: v1 %s, v2 %s — nothing explains the change", f.name, f.cur.Release, f.cand.Name, g.V1, g.V2)
			}
		}
		got[f.name+" | "+f.cur.Release+" ← "+f.cand.Name] = g
	}
	checkUpgradeGolden(t, got)
}

func verdict(k RankKey, ok bool) string {
	if !ok {
		return "no"
	}
	return string(k)
}

// whyUpgradeChanged says why a fixture's upgrade verdict differs between the rankings, from
// the evaluations both made: "" when none of the known differences accounts for it.
func whyUpgradeChanged(ctx context.Context, s *Service, ref string, cur CurrentFile, cand Candidate, keys [2]RankKey, oks [2]bool) string {
	sp, _ := s.GetStored(ctx, ref)
	p, e := s.Resolve(ctx, ref)
	e.Ranking = RankingV1
	b1 := cur.upgradeBar(p, e)
	e.Ranking = RankingV2
	b2 := cur.upgradeBar(p, e)
	d := e.Decide(p, []Candidate{cand})
	if len(d.Eligible) == 0 {
		return ""
	}
	ev := d.Eligible[0]
	trig := NormalizeTrigger(sp.UpgradeTrigger)
	c, k := compareQualityKeys(&ev, &b2.ev)
	v1, v2 := keys[0], keys[1]
	switch {
	case oks[0] && oks[1]:
		return fmt.Sprintf("both upgrade: classic names the first part of the score that rose (%s), v2 the key that decided (%s)", v1, v2)

	case oks[0]: // classic upgraded, v2 doesn't
		switch {
		case !ev.Candidate.alive():
			return "a dead torrent: v2 ranks a release no one seeds below the file (health), so it never upgrades"
		case v1 == KeySize:
			return "a size-lean gain: classic scores −GB × bias, v2 never upgrades on size"
		case b2.intoAvoid(ev):
			return "v2: an avoided release never replaces a file whose only avoid was a negative custom score"
		case v1 == KeyBitrate && c > 0:
			return fmt.Sprintf("bitrate step refused: the file ranks higher on %s, and a heavier encode must give up nothing", k)
		case c > 0:
			return fmt.Sprintf("v2: the file ranks higher on %s; classic's summed score let the %s gain outweigh it", k, v1)
		case c == 0 && v1 == KeySource && ev.SourceTier == b2.ev.SourceTier:
			return "v2: WEB-DL and WEBRip are one source tier, so a WEBRip→WEB-DL step is no upgrade"
		case c == 0 && v1 == KeyPreferences && ev.PrefCount == b2.ev.PrefCount:
			return "v2 counts the preferred formats a release has, not their summed scores"
		case c == 0:
			return fmt.Sprintf("v2: equal on every quality key — classic counted a %s gain from the score", v1)
		case c < 0 && !v2TriggerAllows(trig, k):
			return fmt.Sprintf("v2: decided on %s, which the %q trigger doesn't count (classic named the gain %s)", k, trig, v1)
		}

	case oks[1]: // v2 upgrades, classic didn't
		switch {
		case b1.intoAvoid(ev) && !ev.TargetAvoided:
			return fmt.Sprintf("v2: a negative custom format is a score, not an avoid — the %s gain counts", v2)
		case ev.Total <= b1.total:
			return fmt.Sprintf("v2: wins on %s, which ranks above what classic's summed score favoured", v2)
		case !triggerAllows(trig, improvementKey(ev, b1.ev)):
			return fmt.Sprintf("v2 names the gain %s, which the %q trigger counts; classic named it %s", v2, trig, improvementKey(ev, b1.ev))
		}
	}
	return ""
}

// checkUpgradeGolden compares got against testdata/upgrade_golden.json, or rewrites it with
// -update.
func checkUpgradeGolden(t *testing.T, got map[string]goldenUpgrade) {
	t.Helper()
	path := filepath.Join("testdata", "upgrade_golden.json")
	if *updateGolden {
		b, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	var want map[string]goldenUpgrade
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(got) {
		t.Errorf("golden has %d upgrade decisions, got %d", len(want), len(got))
	}
	for _, name := range sortedKeys(got) {
		w, ok := want[name]
		if !ok {
			t.Errorf("%s: not in the golden file", name)
			continue
		}
		if g := got[name]; g != w {
			t.Errorf("%s changed:\n got  %+v\n want %+v", name, g, w)
		}
	}
}
