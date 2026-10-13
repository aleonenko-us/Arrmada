package quality

import (
	"reflect"
	"strings"
	"testing"
)

func decideTiers(mode string, sp StoredProfile, cands []Candidate) Decision {
	normalize(&sp)
	e := sp.Engine()
	e.Ranking = mode
	return e.Decide(sp.ToProfile(), cands)
}

func remuxPair() []Candidate {
	return []Candidate{
		NewCandidate("Movie.2020.2160p.UHD.BluRay.REMUX.HDR10.TrueHD.Atmos.7.1-GRPZ", 60, 50).WithRuntime(120),
		NewCandidate("Movie.2020.2160p.UHD.BluRay.REMUX.HDR10.TrueHD.Atmos.7.1-FraMeSToR", 60, 50).WithRuntime(120),
	}
}

// Under v2, two otherwise-equal remuxes rank the tier-1 group first and say why; classic
// ranking ignores tiers and keeps the order it had.
func TestGroupTierDecidesUnderV2Only(t *testing.T) {
	sp := StoredProfile{MediaType: MediaMovie, Name: "4K", AllowedResolutions: []string{"2160p"}}
	d := decideTiers(RankingV2, sp, remuxPair())
	if d.Winner == nil || d.Winner.Candidate.Release.Group != "FraMeSToR" {
		t.Fatalf("v2 winner = %+v", d.Winner)
	}
	if d.DecidedBy != KeyGroup || !strings.Contains(d.ChosenOver, "not a tier-1 release group") {
		t.Errorf("decided by %q: %q", d.DecidedBy, d.ChosenOver)
	}
	if d.Winner.GroupTier != groupTierOne || d.Eligible[1].GroupTier != groupTierNone {
		t.Errorf("tiers = %d, %d", d.Winner.GroupTier, d.Eligible[1].GroupTier)
	}

	v1 := decideTiers(RankingV1, sp, remuxPair())
	if v1.Winner == nil || v1.Winner.Candidate.Release.Group != "GRPZ" {
		t.Errorf("v1 winner = %+v — tiers must not change classic ranking", v1.Winner)
	}
}

func TestGroupTierOrderAndLowQuality(t *testing.T) {
	sp := StoredProfile{MediaType: MediaMovie, Name: "x", GroupTiers: &GroupTiers{Tier1: []string{"ALPHA"}, Tier2: []string{"beta", "YTS"}}}
	p := (func() Profile { normalize(&sp); return sp.ToProfile() })()
	cases := map[string]int{"ALPHA": groupTierOne, "alpha": groupTierOne, "BETA": groupTierTwo, "GAMMA": groupTierNone, "": groupTierNone,
		"ALPHAX": groupTierNone, "YTS": groupTierNone /* a low-quality group never gets a tier */}
	for g, want := range cases {
		if got := p.tierOf(g); got != want {
			t.Errorf("tierOf(%q) = %d, want %d", g, got, want)
		}
	}
	// Tier 2 beats untiered under v2; nothing is ever rejected for its group.
	cands := []Candidate{
		NewCandidate("Show.S01E01.1080p.WEB-DL.DDP5.1.H.264-GAMMA", 2, 50).WithRuntime(45),
		NewCandidate("Show.S01E01.1080p.WEB-DL.DDP5.1.H.264-BETA", 2, 50).WithRuntime(45),
	}
	d := decideTiers(RankingV2, sp, cands)
	if d.Winner.Candidate.Release.Group != "BETA" || !strings.Contains(d.ChosenOver, "not in one of your release-group tiers") {
		t.Errorf("winner %s: %q", d.Winner.Candidate.Release.Group, d.ChosenOver)
	}
	if len(d.Rejected) != 0 {
		t.Errorf("rejected = %+v", d.Rejected)
	}
	// Book and music profiles have no tiers.
	if (StoredProfile{MediaType: MediaBook}).ToProfile().GroupTiers.Tier1 != nil {
		t.Error("a book profile got group tiers")
	}
}

// Preferring AMZN ranks an AMZN WEB-DL above an otherwise-equal one, in both modes.
func TestPreferStreamingFormat(t *testing.T) {
	sp := StoredProfile{MediaType: MediaSeries, Name: "tv", FormatScores: map[string]int{"AMZN": 50}, GroupTiers: &GroupTiers{}}
	cands := []Candidate{
		NewCandidate("Show.S01E01.1080p.WEB-DL.DDP5.1.H.264-GRP", 2, 50).WithRuntime(45),
		NewCandidate("Show.S01E01.1080p.AMZN.WEB-DL.DDP5.1.H.264-GRP", 2, 50).WithRuntime(45),
	}
	for _, mode := range []string{RankingV1, RankingV2} {
		d := decideTiers(mode, sp, cands)
		if d.Winner == nil || d.Winner.Candidate.Release.Streaming != "AMZN" {
			t.Errorf("%s: winner %+v", mode, d.Winner)
		}
	}
	// Requiring two services means either.
	sp = StoredProfile{MediaType: MediaSeries, Name: "tv", FormatScores: map[string]int{}, RequiredFormats: []string{"AMZN", "NF"}}
	d := decideTiers(RankingV1, sp, cands)
	if len(d.Eligible) != 1 || d.Eligible[0].Candidate.Release.Streaming != "AMZN" {
		t.Errorf("required AMZN or NF: eligible %+v", d.Eligible)
	}
	for _, f := range Catalog() {
		if f.Name == "AMZN" && (f.Group != "streaming" || f.Target) {
			t.Errorf("AMZN catalog entry = %+v", f)
		}
	}
}

func TestGroupTiersError(t *testing.T) {
	if msg := GroupTiersError(nil, Unwanted{}); msg != "" {
		t.Errorf("nil: %q", msg)
	}
	if msg := GroupTiersError(&GroupTiers{Tier1: []string{"FLUX"}, Tier2: []string{"flux"}}, Unwanted{}); !strings.Contains(msg, "both") {
		t.Errorf("both tiers: %q", msg)
	}
	if msg := GroupTiersError(&GroupTiers{Tier2: []string{"YTS"}}, Unwanted{}); !strings.Contains(msg, "low-quality") {
		t.Errorf("default low-quality group: %q", msg)
	}
	if msg := GroupTiersError(&GroupTiers{Tier1: []string{"YTS"}}, Unwanted{LowQualityGroups: []string{}}); msg != "" {
		t.Errorf("YTS with an emptied low-quality list: %q", msg)
	}
	for _, g := range append(DefaultGroupTiers().Tier1, DefaultGroupTiers().Tier2...) {
		for _, l := range DefaultLowQualityGroups {
			if strings.EqualFold(g, l) {
				t.Errorf("default tier group %s is a default low-quality group", g)
			}
		}
	}
}

// Tiers round-trip: never set reads as the defaults (nil), edited lists persist, an emptied
// list stays empty, and a set equal to the defaults is stored as "follow the defaults".
func TestGroupTiersRoundTrip(t *testing.T) {
	s, db, ctx := storeService(t)
	get := func(id int64) StoredProfile {
		t.Helper()
		sp, err := s.repo.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return sp
	}
	stored := func(id int64) string {
		var v string
		if err := db.QueryRow(`SELECT group_tiers FROM quality_profiles WHERE id = ?`, id).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	sp, err := s.Create(ctx, StoredProfile{MediaType: MediaMovie, Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if got := get(sp.ID); got.GroupTiers != nil || stored(sp.ID) != "" {
		t.Errorf("default: %+v / %q", got.GroupTiers, stored(sp.ID))
	}
	if !reflect.DeepEqual(get(sp.ID).ToProfile().GroupTiers, DefaultGroupTiers()) {
		t.Error("an unset profile doesn't run the default tiers")
	}

	sp.GroupTiers = &GroupTiers{Tier1: []string{" ALPHA ", "alpha"}, Tier2: []string{}}
	if err := s.Update(ctx, sp.ID, sp); err != nil {
		t.Fatal(err)
	}
	if got := get(sp.ID).GroupTiers; got == nil || !reflect.DeepEqual(*got, GroupTiers{Tier1: []string{"ALPHA"}, Tier2: []string{}}) {
		t.Errorf("edited: %+v", got)
	}

	sp.GroupTiers = &GroupTiers{Tier1: []string{}, Tier2: []string{}}
	if err := s.Update(ctx, sp.ID, sp); err != nil {
		t.Fatal(err)
	}
	if got := get(sp.ID); got.GroupTiers == nil || len(got.ToProfile().GroupTiers.Tier1) != 0 {
		t.Errorf("emptied: %+v", got.GroupTiers)
	}

	d := DefaultGroupTiers()
	sp.GroupTiers = &d
	if err := s.Update(ctx, sp.ID, sp); err != nil {
		t.Fatal(err)
	}
	if stored(sp.ID) != "" {
		t.Errorf("defaults stored as %q", stored(sp.ID))
	}
}
