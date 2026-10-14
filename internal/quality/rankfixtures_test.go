package quality

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// The ranking fixtures: a matrix of profiles × candidate sets covering every ranking rule
// (resolution, source tiers, PROPER, preferences and their waiver, custom negatives and
// keywords, the bitrate floor and ceiling, the size lean, seeders and dead torrents, the
// Unwanted pack). The golden file pins what classic (v1) ranking decides over them, so a
// change meant to be invisible — the rank-key decomposition, shadow mode — is proven
// invisible. Regenerate only for an intended v1 change:
//
//	go test ./internal/quality -run TestRankingGolden -update

var updateGolden = flag.Bool("update", false, "rewrite the ranking golden files")

type rankFixture struct {
	name  string
	p     Profile
	e     *Engine
	cands []Candidate
}

// mixedMovie is a broad set of releases of one film, every trait the engine reads.
func mixedMovie() []Candidate {
	type c struct {
		name    string
		gb      float64
		seeders int
	}
	list := []c{
		{"Film.2021.2160p.UHD.BluRay.REMUX.DV.HDR10.TrueHD.Atmos.7.1-FraMeSToR", 70, 40},
		{"Film.2021.2160p.UHD.BluRay.x265.HDR10.DTS-HD.MA.5.1-GRPA", 30, 80},
		{"Film.2021.2160p.WEB-DL.DDP5.1.Atmos.DV.HDR10.H.265-FLUX", 22, 210},
		{"Film.2021.2160p.WEB-DL.DDP5.1.DV.H.265-DVONLY", 18, 90},
		{"Film.2021.2160p.WEBRip.DDP5.1.HDR10.x265-GRPB", 16, 60},
		{"Film.2021.2160p.UPSCALED.WEB-DL.x265-UPS", 12, 30},
		{"Film.2021.2160p.AMZN.WEB-DL.DDP5.1.HDR10+.H.265-GRPC", 20, 0},
		{"Film.2021.1080p.BluRay.x264.DTS-HD.MA.5.1-SPARKS", 14, 180},
		{"Film.2021.1080p.BluRay.x264.PROPER.DTS-HD.MA.5.1-SPARKS", 14, 20},
		{"Film.2021.1080p.WEB-DL.DDP5.1.H.264-EVO", 8, 340},
		{"Film.2021.1080p.WEBRip.x264.AAC-GRPD", 7.6, 30},
		{"Film.2021.1080p.WEB-DL.x265.10bit.AAC5.1-PSA", 2.2, 400},
		{"Film.2021.1080p.WEBRip.x265.AAC5.1-YTS", 1.9, 990},
		{"Film.2021.FRENCH.1080p.BluRay.x264-FRGRP", 12, 50},
		{"Film.2021.MULTi.1080p.BluRay.x264-MULGRP", 11, 50},
		{"Film.2021.1080p.BluRay.IMAX.x264-IMX", 13, 25},
		{"Film.2021.1080p.x264-NOSRC", 6, 15},
		{"Film.2021.1080p.HDTV.x264-TVGRP", 5, 10},
		{"Film.2021.720p.BluRay.x264-SEVEN", 5, 70},
		{"Film.2021.720p.WEB-DL.AV1-AVONE", 1.2, 45},
		{"Film.2021.DVDRip.XviD-OLD", 1.4, 12},
		{"Film.2021.HDCAM.x264-CAMGRP", 1.5, 500},
		{"Film.2021.1080p.Blu-ray.AVC.DTS-HD.MA.5.1-DISC", 40, 20},
		{"Film.2021.1080p.BluRay.Half-SBS.x264-THREED", 9, 20},
		{"Film.2021.EXTRAS.1080p.BluRay.x264-BONUS", 3, 20},
		{"Film.2021.1080p.WEB-DL.DDP5.1.H.264-GRPX", 8.2, 300},
	}
	out := make([]Candidate, len(list))
	for i, x := range list {
		out[i] = NewCandidate(x.name, x.gb, x.seeders).WithLanguage("en")
	}
	return out
}

// seriesEpisodes is one episode's releases (50 minutes) plus a 10-episode pack.
func seriesEpisodes() []Candidate {
	type c struct {
		name    string
		gb      float64
		seeders int
		runtime int
	}
	list := []c{
		{"Show.S01E05.2160p.WEB-DL.DDP5.1.DV.HDR.H.265-NTb", 6, 40, 50},
		{"Show.S01E05.1080p.WEB.h264-SCENE", 2, 120, 50},
		{"Show.S01E05.1080p.AMZN.WEB-DL.DDP5.1.x265-GRP", 1.2, 80, 50},
		{"Show.S01E05.1080p.BluRay.x265-HEAVY", 8, 15, 50},
		{"Show.S01E05.720p.HDTV.x264-KILLERS", 0.8, 60, 50},
		{"[SubsPlease] Show - 05 (1080p) [ABCD1234]", 1.4, 300, 50},
		{"Show.S01.1080p.BluRay.x264-PACK", 30, 25, 500},
		{"Show.S01E05.1080p.WEB.h264-DEAD", 2, 0, 50},
		{"Show.S01E05.1080p.WEB.h264.REPACK-SCENE", 2.05, 30, 50},
		{"Show.S01E05.1080p.WEBRip.x264-RIPPER", 2.1, 100, 50},
	}
	out := make([]Candidate, len(list))
	for i, x := range list {
		out[i] = NewCandidate(x.name, x.gb, x.seeders).WithRuntime(x.runtime).WithLanguage("ko")
	}
	return out
}

func withRuntime(cands []Candidate, min int) []Candidate {
	out := append([]Candidate(nil), cands...)
	for i := range out {
		out[i].RuntimeMin = min
	}
	return out
}

// storedFixture is a saved-shape profile compiled the way a save compiles it.
func storedFixture(sp StoredProfile) (Profile, *Engine) {
	if sp.MediaType == "" {
		sp.MediaType = MediaMovie
	}
	normalize(&sp)
	return sp.ToProfile(), sp.Engine()
}

func rankFixtures() []rankFixture {
	var out []rankFixture
	sets := []struct {
		name  string
		cands []Candidate
	}{
		{"dune", duneCandidates()},
		{"mixed", withRuntime(mixedMovie(), 120)},
		{"mixed-no-runtime", mixedMovie()},
		{"episodes", seriesEpisodes()},
	}
	profiles := map[string]func() (Profile, *Engine){}
	for name, p := range testProfiles() {
		p := p
		profiles["preset-"+name] = func() (Profile, *Engine) { return p, NewDefaultEngine() }
	}
	profiles["fallback"] = func() (Profile, *Engine) { return fallbackProfile(), NewDefaultEngine() }
	profiles["zero"] = func() (Profile, *Engine) { return Profile{}, NewDefaultEngine() }
	profiles["target-4k"] = func() (Profile, *Engine) {
		return storedFixture(StoredProfile{Name: "target-4k", AllowedResolutions: []string{"2160p", "1080p"},
			Ideal: &IdealFile{
				Codec:   map[string]string{"hevc": PrefWant},
				HDR:     map[string]string{"DV": PrefWant, "HDR10": PrefWant},
				Audio:   map[string]string{"atmos": PrefWant},
				Bitrate: map[string]BitrateWindow{"2160p": {Min: 15, Max: 60}, "1080p": {Min: 5, Max: 25}},
			}})
	}
	profiles["avoid-dv"] = func() (Profile, *Engine) {
		return storedFixture(StoredProfile{Name: "avoid-dv", AllowedResolutions: []string{"2160p", "1080p", "720p"},
			Ideal: &IdealFile{HDR: map[string]string{"DV": PrefAvoid}}})
	}
	// The "Other formats" Avoid button (-50) on built-ins the target doesn't cover: the
	// avoided tier at the default floor, like a target Avoid — never a reject.
	profiles["avoid-other"] = func() (Profile, *Engine) {
		return storedFixture(StoredProfile{Name: "avoid-other", AllowedResolutions: []string{"2160p", "1080p", "720p"},
			FormatScores: map[string]int{"AMZN": -50, "DTS-HD": -50}})
	}
	profiles["must-hevc"] = func() (Profile, *Engine) {
		return storedFixture(StoredProfile{Name: "must-hevc", Ideal: &IdealFile{Codec: map[string]string{"hevc": PrefMust, "av1": PrefMust}}})
	}
	profiles["custom"] = func() (Profile, *Engine) {
		return storedFixture(StoredProfile{Name: "custom", AllowedResolutions: []string{"1080p", "720p"},
			CustomFormats: []CustomFormat{
				{Name: "No GRPX", Conditions: []Condition{{Type: CondReleaseGroup, Value: "GRPX"}}},
				{Name: "Good group", Conditions: []Condition{{Type: CondReleaseGroup, Value: "SPARKS|EVO"}}},
			},
			FormatScores: map[string]int{"No GRPX": -5, "Good group": 15},
			Keywords:     []Keyword{{Term: "IMAX", Score: 30}, {Term: "HDTV", Score: -10}},
			Ideal:        &IdealFile{Codec: map[string]string{"hevc": PrefWant}},
		})
	}
	profiles["lean"] = func() (Profile, *Engine) {
		return storedFixture(StoredProfile{Name: "lean", AllowedResolutions: []string{"1080p", "720p"}, SmallBias: 4, MinSeeders: 1,
			Ideal: &IdealFile{Codec: map[string]string{"hevc": PrefWant, "av1": PrefWant},
				Bitrate: map[string]BitrateWindow{"1080p": {Min: 3, Max: 8}, "720p": {Min: 2, Max: 5}}}})
	}
	profiles["bluray-up"] = func() (Profile, *Engine) {
		return storedFixture(StoredProfile{Name: "bluray-up", MinSource: "BluRay", AllowedResolutions: []string{"2160p", "1080p"}})
	}
	profiles["web-only"] = func() (Profile, *Engine) {
		return storedFixture(StoredProfile{Name: "web-only", MinSource: "WEB-DL", MaxSource: "WEB-DL"})
	}
	profiles["prerelease-ok"] = func() (Profile, *Engine) {
		return storedFixture(StoredProfile{Name: "prerelease-ok", AllowPreRelease: true})
	}
	profiles["unwanted-allow"] = func() (Profile, *Engine) {
		items := map[string]string{}
		for _, it := range unwantedItems {
			items[it.Key] = UnwantedAllow
		}
		return storedFixture(StoredProfile{Name: "unwanted-allow", Unwanted: Unwanted{Items: items}})
	}
	names := make([]string, 0, len(profiles))
	for n := range profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, set := range sets {
		for _, n := range names {
			p, e := profiles[n]()
			out = append(out, rankFixture{name: n + "/" + set.name, p: p, e: e, cands: append([]Candidate(nil), set.cands...)})
		}
	}
	return out
}

// goldenDecision is what a decision is pinned by: the order, the scores that made it, and
// every sentence shown about it.
type goldenDecision struct {
	Winner     string         `json:"winner,omitempty"`
	Why        []string       `json:"why,omitempty"`
	ChosenOver string         `json:"chosen_over,omitempty"`
	Eligible   []goldenEval   `json:"eligible,omitempty"`
	Rejected   []goldenReject `json:"rejected,omitempty"`
}

type goldenEval struct {
	Name     string   `json:"name"`
	Total    int      `json:"total"`
	Avoided  []string `json:"avoided,omitempty"`
	Waived   bool     `json:"waived,omitempty"`
	Matched  []string `json:"matched,omitempty"`
	Format   int      `json:"format"`
	Target   int      `json:"target,omitempty"`
	Custom   int      `json:"custom,omitempty"`
	Quality  int      `json:"quality"`
	SizeLean int      `json:"size_lean,omitempty"`
}

type goldenReject struct {
	Name, Code string
}

func goldenOf(d Decision) goldenDecision {
	g := goldenDecision{Why: d.Why, ChosenOver: d.ChosenOver}
	if d.Winner != nil {
		g.Winner = d.Winner.Candidate.Name
	}
	for _, ev := range d.Eligible {
		g.Eligible = append(g.Eligible, goldenEval{Name: ev.Candidate.Name, Total: ev.Total, Avoided: ev.AvoidedFormats,
			Waived: ev.BonusWaived, Matched: ev.Matched, Format: ev.FormatScore, Target: ev.TargetScore,
			Custom: ev.CustomScore, Quality: ev.QualityScore, SizeLean: ev.SizeScore})
	}
	for _, ev := range d.Rejected {
		g.Rejected = append(g.Rejected, goldenReject{ev.Candidate.Name, ev.RejectCode})
	}
	return g
}

// checkGolden compares got against testdata/<file>, or rewrites it with -update.
func checkGolden(t *testing.T, file string, got map[string]goldenDecision) {
	t.Helper()
	path := filepath.Join("testdata", file)
	b, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	var wantMap map[string]goldenDecision
	if err := json.Unmarshal(want, &wantMap); err != nil {
		t.Fatal(err)
	}
	if len(wantMap) != len(got) {
		t.Errorf("golden has %d decisions, got %d", len(wantMap), len(got))
	}
	for name, g := range got {
		w, ok := wantMap[name]
		if !ok {
			t.Errorf("%s: not in the golden file", name)
			continue
		}
		gb, _ := json.Marshal(g)
		wb, _ := json.Marshal(w)
		if string(gb) != string(wb) {
			t.Errorf("%s changed:\n got  %s\n want %s", name, gb, wb)
		}
	}
}

// Classic ranking over the whole fixture matrix is pinned.
func TestRankingGolden(t *testing.T) {
	got := map[string]goldenDecision{}
	for _, f := range rankFixtures() {
		got[f.name] = goldenOf(f.e.Decide(f.p, f.cands))
	}
	checkGolden(t, "ranking_v1_golden.json", got)
}
