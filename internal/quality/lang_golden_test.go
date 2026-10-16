package quality

import "testing"

// The language golden: profiles with Audio language rules over a film's releases in
// several languages, decided by both rankings. The existing ranking and upgrade goldens
// (no language rules anywhere) are what prove a profile without rules is unchanged; this
// one pins what the rules themselves decide. Regenerate only for an intended change:
//
//	go test ./internal/quality -run TestLanguageGolden -update

func languageReleases() []Candidate {
	type c struct {
		name    string
		gb      float64
		seeders int
	}
	list := []c{
		{"Film.2021.1080p.BluRay.x264-ENG", 12, 80},
		{"Film.2021.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-REMUX", 30, 40},
		{"Film.2021.German.DL.1080p.BluRay.x264-DUAL", 13, 60},
		{"Film.2021.GERMAN.1080p.WEB-DL.x264-DUB", 6, 120},
		{"Film.2021.MULTi.1080p.BluRay.x264-MULGRP", 11, 50},
		{"Film.2021.MULTi.TRUEFRENCH.1080p.BluRay.x265-FRM", 9, 40},
		{"Film.2021.TRUEFRENCH.1080p.BluRay.x264-FRGRP", 12, 50},
		{"Film.2021.iTALiAN.1080p.WEB-DL.x264-ITA", 5, 30},
		{"Film.2021.2160p.WEB-DL.DDP5.1.H.265-UHD", 18, 90},
		{"Film.2021.German.DL.2160p.WEB-DL.H.265-UHDDL", 20, 25},
		{"Film.2021.720p.WEB-DL.x264-SMALL", 3, 200},
	}
	out := make([]Candidate, len(list))
	for i, x := range list {
		out[i] = NewCandidate(x.name, x.gb, x.seeders).WithLanguage("en").WithRuntime(120)
	}
	return out
}

func languageFixtures() []rankFixture {
	profiles := []struct {
		name string
		row  map[string]string
	}{
		{"must-german", map[string]string{"de": PrefMust}},
		{"prefer-german", map[string]string{"de": PrefWant}},
		{"original-plus-german", map[string]string{LangOriginal: PrefWant, "de": PrefWant}},
		{"must-original-prefer-german", map[string]string{LangOriginal: PrefMust, "de": PrefWant}},
		{"avoid-french", map[string]string{"fr": PrefAvoid}},
		{"german-must-avoid-italian", map[string]string{"de": PrefMust, "it": PrefAvoid}},
	}
	var out []rankFixture
	for _, pr := range profiles {
		p, e := storedFixture(StoredProfile{Name: pr.name, AllowedResolutions: []string{"2160p", "1080p", "720p"},
			Ideal: &IdealFile{Codec: map[string]string{"hevc": PrefWant}, Language: pr.row}})
		out = append(out, rankFixture{name: pr.name, p: p, e: e, cands: languageReleases()})
	}
	return out
}

func TestLanguageGolden(t *testing.T) {
	got := map[string]goldenDecision{}
	for _, f := range languageFixtures() {
		for _, mode := range []string{RankingV1, RankingV2} {
			got[mode+"/"+f.name] = goldenOf(decideWith(f.e, mode, f.p, append([]Candidate(nil), f.cands...)...))
		}
	}
	checkGolden(t, "ranking_lang_golden.json", got)
}
