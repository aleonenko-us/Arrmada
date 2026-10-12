package quality

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// unwantedProfile is a saved-shape video profile with the given Unwanted choices.
func unwantedProfile(items map[string]string) StoredProfile {
	sp := StoredProfile{MediaType: MediaMovie, Name: "u", Unwanted: Unwanted{Items: items}}
	normalize(&sp)
	return sp
}

func evalUnwanted(sp StoredProfile, c Candidate) Evaluation {
	return sp.Engine().Evaluate(sp.ToProfile(), c)
}

// Each item in its reject, avoid and allow states.
func TestUnwantedItemStates(t *testing.T) {
	cases := []struct {
		item, name, lang string
		avoidName        string // what the avoided list calls it
		rejectPrefix     string
	}{
		{UnwantedFullDisc, "Film.2020.1080p.Blu-ray.AVC.DTS-HD.MA.5.1-GRP", "", "Full disc image", "A full Blu-ray disc image — Arrmada can't import these"},
		{UnwantedThreeD, "Film.2020.1080p.BluRay.Half-SBS.x264-GRP", "", "3D", "A 3D release"},
		{UnwantedExtras, "Film.2020.EXTRAS.1080p.BluRay.x264-GRP", "", "Extras", "Extras or bonus material"},
		{UnwantedUpscaled, "Film.2020.2160p.UPSCALED.WEB-DL.x265-GRP", "", "Upscaled", "Upscaled from a lower resolution"},
		{UnwantedForeign, "Film.2020.FRENCH.1080p.WEB-DL.x264-GRP", "en", "Foreign-only audio (French)", "French-only — this title's original language is English"},
		{UnwantedLowQualityGroup, "Film.2020.1080p.WEBRip.x264-YTS", "", "Low-quality group (YTS)", "From a low-quality group (YTS)"},
		{UnwantedDVNoFallback, "Film.2020.2160p.WEB-DL.DV.H.265-GRP", "", "Dolby Vision without HDR fallback", "Dolby Vision without an HDR10 fallback"},
	}
	for _, tc := range cases {
		c := NewCandidate(tc.name, 8, 50).WithLanguage(tc.lang)
		t.Run(tc.item, func(t *testing.T) {
			ev := evalUnwanted(unwantedProfile(map[string]string{tc.item: UnwantedReject}), c)
			if ev.Eligible || ev.RejectCode != RejectUnwanted || !strings.HasPrefix(ev.RejectReason, tc.rejectPrefix) {
				t.Errorf("reject: eligible=%v code=%q reason=%q", ev.Eligible, ev.RejectCode, ev.RejectReason)
			}
			ev = evalUnwanted(unwantedProfile(map[string]string{tc.item: UnwantedAvoid}), c)
			if !ev.Eligible || !ev.Avoided || !containsStr(ev.AvoidedFormats, tc.avoidName) {
				t.Errorf("avoid: eligible=%v avoided=%v formats=%v", ev.Eligible, ev.Avoided, ev.AvoidedFormats)
			}
			ev = evalUnwanted(unwantedProfile(map[string]string{tc.item: UnwantedAllow}), c)
			if !ev.Eligible || ev.Avoided {
				t.Errorf("allow: eligible=%v avoided=%v formats=%v reason=%q", ev.Eligible, ev.Avoided, ev.AvoidedFormats, ev.RejectReason)
			}
		})
	}
}

// The defaults: disc images, 3D and extras refused; the rest avoided.
func TestUnwantedDefaults(t *testing.T) {
	sp := unwantedProfile(nil)
	for _, name := range []string{
		"Film.2020.1080p.Blu-ray.AVC.DTS-HD.MA.5.1-GRP",
		"Film.2020.2160p.UHD.BD66.HEVC.TrueHD.Atmos-GRP",
		"Film.2020.1080p.BluRay.Half-SBS.x264-GRP",
		"Film.2020.EXTRAS.1080p.BluRay.x264-GRP",
	} {
		if ev := evalUnwanted(sp, NewCandidate(name, 8, 50)); ev.Eligible {
			t.Errorf("%s is eligible by default", name)
		}
	}
	for _, name := range []string{
		"Film.2020.2160p.UPSCALED.WEB-DL.x265-GRP",
		"Film.2020.1080p.WEBRip.x264-YTS",
		"Film.2020.2160p.WEB-DL.DV.H.265-GRP",
	} {
		if ev := evalUnwanted(sp, NewCandidate(name, 8, 50)); !ev.Eligible || !ev.Avoided {
			t.Errorf("%s: eligible=%v avoided=%v, want avoided by default", name, ev.Eligible, ev.Avoided)
		}
	}
	// A BR-DISK release is never the pick under the defaults, however good it looks.
	d := sp.Engine().Decide(sp.ToProfile(), []Candidate{
		NewCandidate("Film.2020.2160p.UHD.Blu-ray.HEVC.TrueHD.Atmos-DISC", 60, 500),
		NewCandidate("Film.2020.720p.HDTV.x264-GRP", 1, 2),
	})
	if d.Winner == nil || d.Winner.Candidate.Release.Group != "GRP" {
		t.Fatalf("winner = %+v", d.Winner)
	}
	if len(d.Rejected) != 1 || !strings.Contains(d.Rejected[0].RejectReason, "disc image") {
		t.Fatalf("rejected = %+v", d.Rejected)
	}
}

// Dolby Vision without a fallback is fine for a profile that prefers or requires Dolby Vision.
func TestUnwantedDVPreferException(t *testing.T) {
	dvOnly := NewCandidate("Film.2020.2160p.WEB-DL.DV.H.265-GRP", 15, 50)
	for _, st := range []string{PrefWant, PrefMust} {
		sp := StoredProfile{MediaType: MediaMovie, Name: "dv", Ideal: &IdealFile{HDR: map[string]string{"DV": st}}}
		normalize(&sp)
		if ev := evalUnwanted(sp, dvOnly); !ev.Eligible || ev.Avoided {
			t.Errorf("DV %s: eligible=%v avoided=%v %v", st, ev.Eligible, ev.Avoided, ev.AvoidedFormats)
		}
	}
	// An explicit choice wins over the exception.
	sp := StoredProfile{MediaType: MediaMovie, Name: "dv", Ideal: &IdealFile{HDR: map[string]string{"DV": PrefWant}},
		Unwanted: Unwanted{Items: map[string]string{UnwantedDVNoFallback: UnwantedAvoid}}}
	normalize(&sp)
	if ev := evalUnwanted(sp, dvOnly); !ev.Avoided {
		t.Error("an explicit avoid should hold even when DV is preferred")
	}
	// A Dolby Vision release with an HDR10 base is never this item.
	if ev := evalUnwanted(unwantedProfile(nil), NewCandidate("Film.2020.2160p.WEB-DL.DV.HDR10.H.265-GRP", 15, 50)); ev.Avoided {
		t.Errorf("DV with HDR10 avoided: %v", ev.AvoidedFormats)
	}
	// Avoiding Dolby Vision outright names it once.
	sp = StoredProfile{MediaType: MediaMovie, Name: "nodv", Ideal: &IdealFile{HDR: map[string]string{"DV": PrefAvoid}}}
	normalize(&sp)
	if ev := evalUnwanted(sp, dvOnly); len(ev.AvoidedFormats) != 1 || ev.AvoidedFormats[0] != "Dolby Vision" {
		t.Errorf("avoided = %v", ev.AvoidedFormats)
	}
}

func TestUnwantedForeignLanguage(t *testing.T) {
	sp := unwantedProfile(nil)
	foreign := func(name, orig string, owner ...string) bool {
		p := sp.ToProfile()
		p.OwnerLanguages = owner
		ev := sp.Engine().Evaluate(p, NewCandidate(name, 8, 50).WithLanguage(orig))
		return containsStr(ev.AvoidedFormats, "Foreign-only audio (French)") ||
			containsStr(ev.AvoidedFormats, "Foreign-only audio (Japanese)") ||
			containsStr(ev.AvoidedFormats, "Foreign-only audio (German)")
	}
	// An English-original film: a French-only dub is foreign, a MULTi release isn't.
	if !foreign("Film.2020.FRENCH.1080p.WEB-DL.x264-GRP", "en") {
		t.Error("FRENCH-only for an English original should be foreign")
	}
	if foreign("Film.2020.MULTi.FRENCH.1080p.WEB-DL.x264-GRP", "en") {
		t.Error("MULTi is never foreign-only")
	}
	if foreign("Film.2020.1080p.WEB-DL.x264-GRP", "en") {
		t.Error("a release stating no language is never judged")
	}
	// A Japanese-original anime: a JAPANESE release is the original, not foreign.
	if foreign("Anime.2020.JAPANESE.1080p.BluRay.x264-GRP", "ja") {
		t.Error("JAPANESE for a Japanese original is not foreign")
	}
	// No original language known: no judgement.
	if foreign("Film.2020.FRENCH.1080p.WEB-DL.x264-GRP", "") || foreign("Film.2020.FRENCH.1080p.WEB-DL.x264-GRP", "xx") {
		t.Error("an unknown original language means no judgement")
	}
	// The household's languages: a French-speaking household keeps the French dub.
	if foreign("Film.2020.FRENCH.1080p.WEB-DL.x264-GRP", "en", "fr", "en") {
		t.Error("a release in an owner language is not foreign")
	}
	if !foreign("Film.2020.GERMAN.1080p.WEB-DL.x264-GRP", "ja", "fr") {
		t.Error("German for a Japanese original in a French household is foreign")
	}
	// English is the owner language when none are set.
	if foreign("Film.2020.ENGLISH.1080p.WEB-DL.x264-GRP", "ja") {
		t.Error("an English release with no owner languages set is not foreign")
	}
	// For an English-original film the reason names both languages.
	ev := sp.Engine().Evaluate(sp.ToProfile(), NewCandidate("Film.2020.FRENCH.1080p.WEB-DL.x264-GRP", 8, 50).WithLanguage("en"))
	if !ev.Avoided {
		t.Fatalf("not avoided: %+v", ev)
	}
}

// A FRENCH-only release for an English film ranks below every other eligible release.
func TestForeignOnlyRanksInTheAvoidedTier(t *testing.T) {
	sp := unwantedProfile(nil)
	d := sp.Engine().Decide(sp.ToProfile(), []Candidate{
		NewCandidate("Film.2020.FRENCH.2160p.BluRay.REMUX.HEVC-FR", 60, 100).WithLanguage("en"),
		NewCandidate("Film.2020.720p.WEB-DL.x264-GRP", 2, 10).WithLanguage("en"),
		NewCandidate("Film.2020.MULTi.1080p.BluRay.x264-MUL", 10, 10).WithLanguage("en"),
	})
	if d.Winner == nil || d.Winner.Candidate.Release.Group != "MUL" {
		t.Fatalf("winner = %+v", d.Winner)
	}
	last := d.Eligible[len(d.Eligible)-1]
	if last.Candidate.Release.Group != "FR" || !last.Avoided {
		t.Fatalf("last = %+v", last)
	}
}

// Groups match exactly (case aside), never as a substring, and the tag YTS puts at the end
// counts with or without its site suffix.
func TestLowQualityGroupMatching(t *testing.T) {
	sp := unwantedProfile(nil)
	cases := []struct {
		name string
		want bool
	}{
		{"Film.2020.1080p.WEBRip.x264-YTS", true},
		{"Film.2020.1080p.WEBRip.x264-yts", true},
		{"Film (2020) [1080p] [WEBRip] [5.1] [YTS.MX]", true},
		{"Film.2020.1080p.WEBRip.x264-YTS.MX", true},
		{"Film.2020.720p.BrRip.x264-YIFY", true},
		{"Film.2020.1080p.WEB-DL.x265-MeGusta", true},
		{"Film.2020.1080p.WEB-DL.x264-MYTSGRP", false},
		{"Film.2020.1080p.WEB-DL.x264-YTSX", false},
		{"Yts.The.Movie.2020.1080p.WEB-DL.x264-GRP", false},
	}
	for _, tc := range cases {
		ev := evalUnwanted(sp, NewCandidate(tc.name, 3, 50))
		if got := ev.Avoided; got != tc.want {
			t.Errorf("%s: avoided=%v (%v), want %v", tc.name, got, ev.AvoidedFormats, tc.want)
		}
	}
	// An edited list replaces the defaults; an empty one matches nothing.
	custom := StoredProfile{MediaType: MediaMovie, Name: "g", Unwanted: Unwanted{LowQualityGroups: []string{" BadGrp ", "badgrp", ""}}}
	normalize(&custom)
	if got := custom.Unwanted.LowQualityGroups; len(got) != 1 || got[0] != "BadGrp" {
		t.Fatalf("normalized groups = %q", got)
	}
	if ev := evalUnwanted(custom, NewCandidate("Film.2020.1080p.WEBRip.x264-YTS", 3, 50)); ev.Avoided {
		t.Error("YTS isn't on the edited list")
	}
	if ev := evalUnwanted(custom, NewCandidate("Film.2020.1080p.WEBRip.x264-BADGRP", 3, 50)); !ev.Avoided {
		t.Error("BadGrp is on the edited list")
	}
	none := StoredProfile{MediaType: MediaMovie, Name: "g", Unwanted: Unwanted{LowQualityGroups: []string{}}}
	normalize(&none)
	if ev := evalUnwanted(none, NewCandidate("Film.2020.1080p.WEBRip.x264-YTS", 3, 50)); ev.Avoided {
		t.Error("an empty list matches nothing")
	}
}

// The YTS reason names the group instead of a hidden penalty.
func TestLowQualityGroupIsNamed(t *testing.T) {
	sp := unwantedProfile(nil)
	d := sp.Engine().Decide(sp.ToProfile(), []Candidate{
		NewCandidate("Film.2020.1080p.WEBRip.x265-YTS", 2, 900),
		NewCandidate("Film.2020.1080p.WEB-DL.x264-GRP", 6, 20),
	})
	if d.Winner == nil || d.Winner.Candidate.Release.Group != "GRP" {
		t.Fatalf("winner = %+v", d.Winner)
	}
	if !strings.Contains(d.ChosenOver, "it has Low-quality group (YTS), which you avoid") {
		t.Fatalf("chosen over = %q", d.ChosenOver)
	}
}

// Book and music profiles are never judged by video tags: "Extras" can be a book's title
// and "TS" an author's initials.
func TestUnwantedIsVideoOnly(t *testing.T) {
	for _, media := range []string{MediaBook, MediaMusic} {
		sp := StoredProfile{MediaType: media, Name: "b", Unwanted: Unwanted{Items: map[string]string{UnwantedExtras: UnwantedReject}}}
		normalize(&sp)
		if !sp.Unwanted.IsDefault() {
			t.Errorf("%s: unwanted kept on a non-video profile: %+v", media, sp.Unwanted)
		}
		p := sp.ToProfile()
		if !p.NoUnwanted {
			t.Errorf("%s: the pack is on", media)
		}
		if ev := sp.Engine().Evaluate(p, NewCandidate("Some.Author.2020.EXTRAS.1080p.BluRay.x264-YTS", 1, 5)); !ev.Eligible || ev.Avoided {
			t.Errorf("%s: judged by video tags: %+v", media, ev)
		}
	}
}

// Invalid stored choices are dropped rather than trusted.
func TestUnwantedNormalize(t *testing.T) {
	u := Unwanted{Items: map[string]string{"three_d": " Allow ", "bogus": "reject", UnwantedExtras: "maybe"}}.normalized()
	if len(u.Items) != 1 || u.Items[UnwantedThreeD] != UnwantedAllow {
		t.Fatalf("items = %v", u.Items)
	}
	if u.LowQualityGroups != nil {
		t.Fatal("a missing list must stay the default list")
	}
	if !(Unwanted{}).IsDefault() || (Unwanted{Items: map[string]string{UnwantedThreeD: UnwantedAllow}}).IsDefault() {
		t.Fatal("IsDefault")
	}
}

// The column round-trips, and ” reads as the defaults.
func TestUnwantedRepoRoundTrip(t *testing.T) {
	s, db, ctx := storeService(t)
	plain, err := s.Create(ctx, StoredProfile{MediaType: MediaMovie, Name: "plain"})
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.QueryRow(`SELECT unwanted FROM quality_profiles WHERE id = ?`, plain.ID).Scan(&raw); err != nil || raw != "" {
		t.Fatalf("defaults stored as %q (%v), want ''", raw, err)
	}
	if !plain.Unwanted.IsDefault() {
		t.Fatalf("read back %+v", plain.Unwanted)
	}
	edited := StoredProfile{MediaType: MediaMovie, Name: "edited", Unwanted: Unwanted{
		Items:            map[string]string{UnwantedThreeD: UnwantedAllow, UnwantedForeign: UnwantedReject},
		LowQualityGroups: []string{"BadGrp"},
	}}
	got, err := s.Create(ctx, edited)
	if err != nil {
		t.Fatal(err)
	}
	if got.Unwanted.Items[UnwantedThreeD] != UnwantedAllow || got.Unwanted.Items[UnwantedForeign] != UnwantedReject ||
		len(got.Unwanted.LowQualityGroups) != 1 || got.Unwanted.LowQualityGroups[0] != "BadGrp" {
		t.Fatalf("round trip = %+v", got.Unwanted)
	}
	// Back to the defaults stores '' again.
	got.Unwanted = Unwanted{}
	if err := s.Update(ctx, got.ID, got); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT unwanted FROM quality_profiles WHERE id = ?`, got.ID).Scan(&raw); err != nil || raw != "" {
		t.Fatalf("reset stored as %q (%v)", raw, err)
	}
	// Turning an item to Allow shows in the live preview: YTS stops being avoided.
	allow := StoredProfile{MediaType: MediaMovie, Name: "p", Unwanted: Unwanted{Items: map[string]string{UnwantedLowQualityGroup: UnwantedAllow}}}
	for _, ev := range s.Preview(allow).Eligible {
		if ev.Candidate.Release.Group == "YTS" && ev.Avoided {
			t.Fatal("YTS still avoided in the preview with the item allowed")
		}
	}
	avoided := false
	for _, ev := range s.Preview(StoredProfile{MediaType: MediaMovie, Name: "p"}).Eligible {
		avoided = avoided || (ev.Candidate.Release.Group == "YTS" && ev.Avoided)
	}
	if !avoided {
		t.Fatal("YTS should be avoided in the default preview")
	}
}

// The owner's languages come from the hook main wires to Convert, English without one.
func TestOwnerLanguagesHook(t *testing.T) {
	s, _, ctx := storeService(t)
	if got := s.ownerLanguages(ctx); len(got) != 1 || got[0] != "en" {
		t.Fatalf("default = %v", got)
	}
	s.SetOwnerLanguages(func(context.Context) []string { return []string{"fr"} })
	sp := StoredProfile{MediaType: MediaMovie, Name: "fr"}
	d := s.DecideSpec(sp, []Candidate{NewCandidate("Film.2020.FRENCH.1080p.WEB-DL.x264-GRP", 4, 10).WithLanguage("en")})
	if d.Winner == nil || d.Winner.Avoided {
		t.Fatalf("a French household's French dub: %+v", d.Winner)
	}
}

// A low-quality group's file is still upgraded to a clean release that scores no worse
// (it used to carry a hidden −180), under the "any" trigger only.
func TestLowQualityGroupFileUpgrades(t *testing.T) {
	s, ctx := testService(t)
	for _, tc := range []struct {
		trigger string
		want    bool
	}{{TriggerAny, true}, {TriggerSource, false}} {
		sp, err := s.Create(ctx, StoredProfile{MediaType: MediaMovie, Name: "up", UpgradesEnabled: true, UpgradeTrigger: tc.trigger})
		if err != nil {
			t.Fatal(err)
		}
		ref := "custom:" + strconv.FormatInt(sp.ID, 10)
		cur := CurrentFile{Release: "Film.2020.1080p.WEBRip.x264-YTS", SizeGB: 2, RuntimeMin: 120}
		_, ok := s.UpgradeCandidate(ctx, ref, cur, []Candidate{NewCandidate("Film.2020.1080p.WEBRip.x264-GRP", 4, 50)})
		if ok != tc.want {
			t.Errorf("%s: upgraded = %v, want %v", tc.trigger, ok, tc.want)
		}
		if got := s.IsQualityUpgrade(ctx, ref, "Film.2020.1080p.WEBRip.x264-GRP", 4, cur); got != tc.want {
			t.Errorf("%s: import gate = %v, want %v", tc.trigger, got, tc.want)
		}
	}
	// A new Unwanted item doesn't reach back into the library: an upscaled file isn't
	// replaced by an equal-scoring clean release.
	sp, err := s.Create(ctx, StoredProfile{MediaType: MediaMovie, Name: "up2", UpgradesEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	cur := CurrentFile{Release: "Film.2020.2160p.UPSCALED.WEB-DL.x265-GRPA", SizeGB: 10, RuntimeMin: 120}
	if _, ok := s.UpgradeCandidate(ctx, "custom:"+strconv.FormatInt(sp.ID, 10), cur, []Candidate{NewCandidate("Film.2020.2160p.WEB-DL.x265-GRPB", 10, 50)}); ok {
		t.Error("an upscaled file was replaced by an equal release")
	}
}
