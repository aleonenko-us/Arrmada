package quality

import (
	"bytes"
	"context"
	"encoding/csv"
	"log/slog"
	"strconv"
	"strings"
	"testing"
)

// forEachRanking runs fn under classic ranking and ranking v2, for the tests that pin
// behaviour both must share (and document, inline, where they deliberately differ).
func forEachRanking(t *testing.T, fn func(t *testing.T, mode string)) {
	t.Helper()
	for _, mode := range []string{RankingV1, RankingV2} {
		t.Run(mode, func(t *testing.T) { fn(t, mode) })
	}
}

// engineIn is the default engine ranking in mode.
func engineIn(mode string) *Engine {
	e := NewDefaultEngine()
	e.Ranking = mode
	return e
}

// setRanking switches a test service's ranking mode, as the setting would.
func setRanking(t *testing.T, s *Service, mode string) {
	t.Helper()
	if err := s.settings.Set(context.Background(), KeyRanking, mode); err != nil {
		t.Fatal(err)
	}
}

func v2Decide(p Profile, cands ...Candidate) Decision { return engineIn(RankingV2).Decide(p, cands) }

// decideWith decides with a profile's own engine (which knows its custom formats) in mode.
func decideWith(e *Engine, mode string, p Profile, cands ...Candidate) Decision {
	e.Ranking = mode
	return e.Decide(p, cands)
}

func winnerName(d Decision) string {
	if d.Winner == nil {
		return ""
	}
	return d.Winner.Candidate.Name
}

// The goal resolution can't be outweighed by stacked preferences under v2; classic ranking
// lets four Prefers (+200) beat the 150-point resolution gap.
func TestV2GoalResolutionBeatsStackedPreferences(t *testing.T) {
	p, e := storedFixture(StoredProfile{Name: "both", AllowedResolutions: []string{"2160p", "1080p"},
		Ideal: &IdealFile{Codec: map[string]string{"hevc": PrefWant}, HDR: map[string]string{"HDR10": PrefWant, "HDR10+": PrefWant},
			Audio: map[string]string{"atmos": PrefWant}}})
	uhd := NewCandidate("Film.2021.2160p.WEB-DL.DDP5.1.H.265-UHD", 20, 50)             // 1 Prefer (HEVC)
	fhd := NewCandidate("Film.2021.1080p.BluRay.HDR10+.TrueHD.Atmos.x265-FHD", 14, 50) // 4 Prefers
	if d := e.Decide(p, []Candidate{uhd, fhd}); winnerName(d) != fhd.Name || d.DecidedBy == "" {
		t.Fatalf("classic: winner %q decided by %q (this is the behaviour v2 fixes)", winnerName(d), d.DecidedBy)
	}
	e.Ranking = RankingV2
	d := e.Decide(p, []Candidate{uhd, fhd})
	if winnerName(d) != uhd.Name || d.DecidedBy != KeyResolution {
		t.Fatalf("v2: winner %q decided by %q", winnerName(d), d.DecidedBy)
	}
	if d.Eligible[1].LostOn != KeyResolution || !strings.HasSuffix(d.ChosenOver, "— lower resolution") {
		t.Fatalf("v2: lost on %q, chosen over %q", d.Eligible[1].LostOn, d.ChosenOver)
	}
}

// A custom format scored −5 lowers a release within its tier without sending it below
// every resolution; a target Avoid is still a cliff, and so is the bitrate floor.
func TestV2CustomNegativeIsNotACliff(t *testing.T) {
	p, ce := storedFixture(StoredProfile{Name: "c", AllowedResolutions: []string{"2160p", "1080p"},
		CustomFormats: []CustomFormat{{Name: "Meh", Conditions: []Condition{{Type: CondReleaseGroup, Value: "MEH"}}}},
		FormatScores:  map[string]int{"Meh": -5},
		// A minimum format score of 0 would refuse any negative total outright, in both
		// rankings (MinFormatScore still reads the summed score); open it so ranking decides.
		MinFormatScore: -100,
		Ideal:          &IdealFile{HDR: map[string]string{"DV": PrefAvoid}, Bitrate: map[string]BitrateWindow{"1080p": {Min: 4}}}})
	meh4k := NewCandidate("Film.2021.2160p.WEB-DL.H.265-MEH", 20, 50).WithRuntime(120)
	clean1080 := NewCandidate("Film.2021.1080p.WEB-DL.H.264-OK", 8, 50).WithRuntime(120)
	clean4k := NewCandidate("Film.2021.2160p.WEB-DL.H.265-OK", 20, 50).WithRuntime(120)

	if d := decideWith(ce, RankingV2, p, meh4k, clean1080); winnerName(d) != meh4k.Name {
		t.Fatalf("a −5 custom format dropped the 4K below 1080p: %q", winnerName(d))
	}
	// Within its tier it ranks lower, on the custom key.
	d := decideWith(ce, RankingV2, p, meh4k, clean4k)
	if winnerName(d) != clean4k.Name || d.DecidedBy != KeyCustom {
		t.Fatalf("same tier: %q by %q", winnerName(d), d.DecidedBy)
	}
	// Classic ranking treats the −5 as an avoid: the 1080p wins.
	if d := decideWith(ce, RankingV1, p, meh4k, clean1080); winnerName(d) != clean1080.Name {
		t.Fatalf("classic: %q", winnerName(d))
	}
	// A target Avoid stays a cliff.
	dv4k := NewCandidate("Film.2021.2160p.BluRay.REMUX.DV.HDR10.HEVC-DV", 60, 50).WithRuntime(120)
	if d := decideWith(ce, RankingV2, p, dv4k, clean1080); winnerName(d) != clean1080.Name || d.DecidedBy != KeyAvoid {
		t.Fatalf("target avoid: %q by %q", winnerName(d), d.DecidedBy)
	}
	// The floor counts as an avoid: a starved 1080p loses to a 720p inside its window.
	thin := NewCandidate("Film.2021.1080p.WEB-DL.H.264-THIN", 1.5, 50).WithRuntime(120)
	sd := NewCandidate("Film.2021.720p.WEB-DL.H.264-SD", 2, 50).WithRuntime(120)
	pf, fe := storedFixture(StoredProfile{Name: "f", AllowedResolutions: []string{"1080p", "720p"},
		Ideal: &IdealFile{Bitrate: map[string]BitrateWindow{"1080p": {Min: 4}}}})
	if d := decideWith(fe, RankingV2, pf, thin, sd); winnerName(d) != sd.Name || d.DecidedBy != KeyAvoid {
		t.Fatalf("floor: %q by %q", winnerName(d), d.DecidedBy)
	}
}

// WEB-DL and WEBRip are one source tier under v2: the same resolution and preferences
// are decided by bitrate; classic ranking's source bonus prefers the WEB-DL whatever its
// bitrate.
func TestV2WebTierMerge(t *testing.T) {
	webdl := NewCandidate("Film.2021.1080p.WEB-DL.H.264-DL", 4, 50).WithRuntime(120)
	webrip := NewCandidate("Film.2021.1080p.WEBRip.x264-RIP", 8, 50).WithRuntime(120)
	d := v2Decide(Profile{}, webdl, webrip)
	if winnerName(d) != webrip.Name || d.DecidedBy != KeyBitrate {
		t.Fatalf("v2: %q by %q", winnerName(d), d.DecidedBy)
	}
	if d := engineIn(RankingV1).Decide(Profile{}, []Candidate{webdl, webrip}); winnerName(d) != webdl.Name {
		t.Fatalf("classic: %q", winnerName(d))
	}
	// A better tier still decides on source.
	bd := NewCandidate("Film.2021.1080p.BluRay.x264-BD", 4, 50).WithRuntime(120)
	if d := v2Decide(Profile{}, webrip, bd); winnerName(d) != bd.Name || d.DecidedBy != KeySource ||
		!strings.HasSuffix(d.ChosenOver, "WEBRip, not BluRay") {
		t.Fatalf("source: %q by %q (%q)", winnerName(d), d.DecidedBy, d.ChosenOver)
	}
}

// Bitrate: inside the window first; then higher, or lower with a small-file lean.
func TestV2BitrateKey(t *testing.T) {
	lean := Profile{SmallBias: 2}
	a := NewCandidate("Film.2021.1080p.WEB-DL.H.264-A", 4, 50).WithRuntime(120)
	b := NewCandidate("Film.2021.1080p.WEB-DL.H.264-B", 9, 50).WithRuntime(120)
	if d := v2Decide(Profile{}, a, b); winnerName(d) != b.Name || d.DecidedBy != KeyBitrate {
		t.Fatalf("higher bitrate: %q by %q", winnerName(d), d.DecidedBy)
	}
	if d := v2Decide(lean, a, b); winnerName(d) != a.Name || d.DecidedBy != KeyBitrate {
		t.Fatalf("small lean: %q by %q", winnerName(d), d.DecidedBy)
	}
	// Codec-normalised: a 6 GB HEVC (≈ 9.6 GB of H.264) beats an 8 GB H.264.
	hevc := NewCandidate("Film.2021.1080p.WEB-DL.H.265-H", 6, 50).WithRuntime(120)
	avc := NewCandidate("Film.2021.1080p.WEB-DL.H.264-X", 8, 50).WithRuntime(120)
	if d := v2Decide(Profile{}, avc, hevc); winnerName(d) != hevc.Name {
		t.Fatalf("codec-normalised: %q", winnerName(d))
	}
	// The window: a release under the floor loses whatever its other merits (the floor is
	// an avoid); InWindow orders what's left by the same rule.
	w := Profile{Windows: map[string]BitrateWindow{"1080p": {Min: 3, Max: 20}}}
	under := NewCandidate("Film.2021.1080p.BluRay.x264-UNDER", 2, 500).WithRuntime(120)
	in := NewCandidate("Film.2021.1080p.WEB-DL.H.264-IN", 6, 5).WithRuntime(120)
	if d := v2Decide(w, under, in); winnerName(d) != in.Name {
		t.Fatalf("window: %q", winnerName(d))
	}
}

// Health: seeders decide between releases within 10% of each other; a dead torrent goes
// after every seeded one in its tier, however good.
func TestV2HealthKey(t *testing.T) {
	a := NewCandidate("Film.2021.1080p.WEB-DL.H.264-A", 10, 2).WithRuntime(120)
	b := NewCandidate("Film.2021.1080p.WEB-DL.H.264-B", 9.5, 150).WithRuntime(120)
	if d := v2Decide(Profile{}, a, b); winnerName(d) != b.Name || d.DecidedBy != KeyHealth || !strings.HasSuffix(d.ChosenOver, "fewer seeders") {
		t.Fatalf("band: %q by %q (%q)", winnerName(d), d.DecidedBy, d.ChosenOver)
	}
	dead := NewCandidate("Film.2021.2160p.BluRay.REMUX.HEVC-DEAD", 60, 0).WithRuntime(120)
	alive := NewCandidate("Film.2021.720p.HDTV.x264-ALIVE", 1, 3).WithRuntime(120)
	d := v2Decide(Profile{}, dead, alive)
	if winnerName(d) != alive.Name || d.DecidedBy != KeyHealth || !strings.HasSuffix(d.ChosenOver, "it has no seeders") {
		t.Fatalf("dead: %q by %q (%q)", winnerName(d), d.DecidedBy, d.ChosenOver)
	}
	// Still the pick when nothing seeded is eligible.
	if d := v2Decide(Profile{}, dead); winnerName(d) != dead.Name || d.DecidedBy != KeyOnly {
		t.Fatalf("only: %q by %q", winnerName(d), d.DecidedBy)
	}
	// Outside the band, with no runtimes, size decides — the larger, or the smaller with a lean.
	big := NewCandidate("Film.2021.1080p.WEB-DL.H.264-BIG", 12, 5)
	small := NewCandidate("Film.2021.1080p.WEB-DL.H.264-SMALL", 6, 500)
	if d := v2Decide(Profile{}, small, big); winnerName(d) != big.Name || d.DecidedBy != KeySize {
		t.Fatalf("size: %q by %q", winnerName(d), d.DecidedBy)
	}
	if d := v2Decide(Profile{SmallBias: 1}, small, big); winnerName(d) != small.Name || d.DecidedBy != KeySize {
		t.Fatalf("size lean: %q by %q", winnerName(d), d.DecidedBy)
	}
}

// Every decision carries DecidedBy, and every eligible release after the first its LostOn,
// in both modes, across the whole fixture matrix.
func TestEveryDecisionRecordsItsKey(t *testing.T) {
	forEachRanking(t, func(t *testing.T, mode string) {
		for _, f := range rankFixtures() {
			f.e.Ranking = mode
			d := f.e.Decide(f.p, f.cands)
			if d.Ranking != mode {
				t.Fatalf("%s: ranking %q", f.name, d.Ranking)
			}
			switch {
			case len(d.Eligible) == 0 && d.DecidedBy != "":
				t.Fatalf("%s: decided by %q with nothing eligible", f.name, d.DecidedBy)
			case len(d.Eligible) == 1 && d.DecidedBy != KeyOnly:
				t.Fatalf("%s: lone release decided by %q", f.name, d.DecidedBy)
			case len(d.Eligible) > 1 && d.DecidedBy == "":
				t.Fatalf("%s: no deciding key", f.name)
			}
			for i, ev := range d.Eligible {
				if (i == 0) != (ev.LostOn == "") {
					t.Fatalf("%s: #%d lost on %q", f.name, i, ev.LostOn)
				}
			}
			if len(d.Eligible) > 1 && d.DecidedBy != d.Eligible[1].LostOn {
				t.Fatalf("%s: decided by %q, runner-up lost on %q", f.name, d.DecidedBy, d.Eligible[1].LostOn)
			}
		}
	})
}

// v2's order is a strict weak order over the fixtures: no release is ranked above one that
// beats it, so the deciding keys it records are the comparisons it actually made.
func TestV2OrderIsConsistent(t *testing.T) {
	for _, f := range rankFixtures() {
		f.e.Ranking = RankingV2
		d := f.e.Decide(f.p, f.cands)
		for i := 0; i < len(d.Eligible); i++ {
			for j := i + 1; j < len(d.Eligible); j++ {
				if c, k := compareV2(f.p, &d.Eligible[i], &d.Eligible[j]); c > 0 {
					t.Fatalf("%s: %s ranked above %s, which beats it on %s", f.name,
						d.Eligible[i].Candidate.Name, d.Eligible[j].Candidate.Name, k)
				}
			}
		}
	}
}

// Shadow mode: under classic ranking the automatic pick is v1's, unchanged — the golden
// file again, now through the service's shadow path — and v2's choice rides alongside.
func TestShadowModeLeavesPicksUnchanged(t *testing.T) {
	got := map[string]goldenDecision{}
	for _, f := range rankFixtures() {
		d := f.e.Decide(f.p, f.cands)
		d.Shadow = shadowOf(f.p, d)
		got[f.name] = goldenOf(d)
		if d.Winner != nil && (d.Shadow == nil || d.Shadow.Winner == "" || d.Shadow.DecidedBy == "") {
			t.Fatalf("%s: shadow = %+v", f.name, d.Shadow)
		}
		if d.Shadow != nil && d.Shadow.Differs != (d.Shadow.Winner != d.Winner.Candidate.Name) {
			t.Fatalf("%s: differs flag wrong: %+v", f.name, d.Shadow)
		}
	}
	checkGolden(t, "ranking_v1_golden.json", got)
}

// What v2 picks over the fixture matrix is pinned too, so a change to the comparator shows
// up as a reviewed diff. Regenerate with -update when the change is intended.
func TestRankingV2Golden(t *testing.T) {
	got := map[string]goldenDecision{}
	for _, f := range rankFixtures() {
		f.e.Ranking = RankingV2
		got[f.name] = goldenOf(f.e.Decide(f.p, f.cands))
	}
	checkGolden(t, "ranking_v2_golden.json", got)
}

// logCapture is a slog handler that keeps the messages.
type logCapture struct{ msgs *[]string }

func (h logCapture) Enabled(context.Context, slog.Level) bool { return true }
func (h logCapture) Handle(_ context.Context, r slog.Record) error {
	*h.msgs = append(*h.msgs, r.Message)
	return nil
}
func (h logCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h logCapture) WithGroup(string) slog.Handler      { return h }

// The service logs and records exactly when the two rankings disagree, once per pair of
// winners, and the record reads back (and exports) for review.
func TestShadowLogsAndRecordsDisagreements(t *testing.T) {
	s, db, ctx := storeService(t)
	var msgs []string
	s.SetLogger(slog.New(logCapture{&msgs}))
	sp, err := s.Create(ctx, StoredProfile{MediaType: MediaMovie, Name: "both", AllowedResolutions: []string{"2160p", "1080p"},
		Ideal: &IdealFile{Codec: map[string]string{"hevc": PrefWant}, HDR: map[string]string{"HDR10": PrefWant, "HDR10+": PrefWant},
			Audio: map[string]string{"atmos": PrefWant}}})
	if err != nil {
		t.Fatal(err)
	}
	ref := "custom:" + strconv.FormatInt(sp.ID, 10)
	uhd := NewCandidate("Film.2021.2160p.WEB-DL.DDP5.1.H.265-UHD", 20, 50)
	fhd := NewCandidate("Film.2021.1080p.BluRay.HDR10+.TrueHD.Atmos.x265-FHD", 14, 50)

	// Classic ranking deciding (the rollback, saved explicitly): v2 runs in the shadow.
	setRanking(t, s, RankingV1)
	// They agree: no log, no row.
	d := s.Decide(ctx, ref, []Candidate{uhd, NewCandidate("Film.2021.1080p.WEB-DL.H.264-LOW", 4, 50)})
	if d.Ranking != RankingV1 || d.Shadow == nil || d.Shadow.Differs || len(msgs) != 0 {
		t.Fatalf("agreeing: ranking %q shadow %+v logs %v", d.Ranking, d.Shadow, msgs)
	}
	// They disagree: v1's pick stands, v2's is logged with both names and the key.
	d = s.Decide(ctx, ref, []Candidate{uhd, fhd})
	if winnerName(d) != fhd.Name || d.Shadow == nil || !d.Shadow.Differs || d.Shadow.Winner != uhd.Name || d.Shadow.OverKey != KeyResolution {
		t.Fatalf("disagreeing: winner %q shadow %+v", winnerName(d), d.Shadow)
	}
	want := "quality: ranking v2 would pick " + uhd.Name + " instead of " + fhd.Name + " (decided by resolution)"
	if len(msgs) != 1 || msgs[0] != want {
		t.Fatalf("logs = %q, want [%q]", msgs, want)
	}
	// The same disagreement again is counted, not logged again.
	s.Decide(ctx, ref, []Candidate{fhd, uhd})
	if len(msgs) != 1 {
		t.Fatalf("a repeat was logged again: %q", msgs)
	}
	rep, err := s.ShadowReport(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Mode != RankingV1 || rep.Decisions != 3 || rep.Disagreements != 2 || len(rep.Days) != 1 || len(rep.Rows) != 1 {
		t.Fatalf("report = %+v", rep)
	}
	row := rep.Rows[0]
	if row.Profile != ref || row.V1Winner != fhd.Name || row.V2Winner != uhd.Name || row.V2Key != "resolution" ||
		row.V1Key != "preferences" || row.Seen != 2 || row.Candidates != 2 {
		t.Fatalf("row = %+v", row)
	}
	var buf bytes.Buffer
	if err := s.WriteShadowCSV(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	recs, err := csv.NewReader(&buf).ReadAll()
	if err != nil || len(recs) != 2 || recs[1][1] != fhd.Name || recs[1][3] != uhd.Name {
		t.Fatalf("csv = %q (%v)", recs, err)
	}
	// A lone release has no choice to make and isn't counted.
	s.Decide(ctx, ref, []Candidate{uhd})
	if rep, _ := s.ShadowReport(ctx, 0); rep.Decisions != 3 {
		t.Fatalf("a lone release was counted: %d", rep.Decisions)
	}

	// Unsaved profiles (the builder) show the shadow pick but never record it.
	if d := s.DecideSpec(sp, []Candidate{uhd, fhd}); d.Shadow == nil || !d.Shadow.Differs || d.Shadow.Mode != RankingV2 {
		t.Fatalf("DecideSpec shadow = %+v", d.Shadow)
	}
	var n int
	if err := db.QueryRow(`SELECT seen FROM ranking_shadow`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("DecideSpec recorded: seen %d (%v)", n, err)
	}

	// v2 deciding (the default once "v1" is cleared): classic runs in the shadow, in reverse.
	// The pick is v2's, classic's rides along, and the disagreement lands on the SAME row —
	// recorded as classic picked fhd, v2 picked uhd — so the record reads the same across
	// the switch.
	setRanking(t, s, "")
	d = s.Decide(ctx, ref, []Candidate{uhd, fhd})
	if d.Ranking != RankingV2 || winnerName(d) != uhd.Name || d.DecidedBy != KeyResolution {
		t.Fatalf("v2 mode: %+v", d)
	}
	if d.Shadow == nil || d.Shadow.Mode != RankingV1 || !d.Shadow.Differs || d.Shadow.Winner != fhd.Name ||
		d.Shadow.OverKey != KeyResolution || d.Shadow.V1Key != KeyPreferences {
		t.Fatalf("v2 mode shadow = %+v", d.Shadow)
	}
	rep, err = s.ShadowReport(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Mode != RankingV2 || rep.Decisions != 4 || rep.Disagreements != 3 || len(rep.Rows) != 1 {
		t.Fatalf("v2 mode report = %+v", rep)
	}
	if row := rep.Rows[0]; row.V1Winner != fhd.Name || row.V2Winner != uhd.Name || row.V1Key != "preferences" ||
		row.V2Key != "resolution" || row.Seen != 3 {
		t.Fatalf("v2 mode row = %+v", row)
	}
	if len(msgs) != 1 {
		t.Fatalf("a disagreement already on record was logged again: %q", msgs)
	}
	// A new disagreement under v2 is logged the reverse way round.
	low := NewCandidate("Film.2021.1080p.BluRay.HDR10+.TrueHD.Atmos.x265-OTHER", 14, 50)
	s.Decide(ctx, ref, []Candidate{low, uhd})
	want = "quality: classic ranking would pick " + low.Name + " instead of " + uhd.Name + " (v2 decided by resolution)"
	if len(msgs) != 2 || msgs[1] != want {
		t.Fatalf("v2 mode logs = %q, want second %q", msgs, want)
	}
}

// A service without the shadow tables (or a database that fails) still decides.
func TestShadowRecordFailureNeverBlocksAPick(t *testing.T) {
	s, ctx := testService(t) // no ranking_shadow table
	sp, err := s.Create(ctx, StoredProfile{MediaType: MediaMovie, Name: "x", AllowedResolutions: []string{"2160p", "1080p"},
		Ideal: &IdealFile{Audio: map[string]string{"atmos": PrefWant}, Codec: map[string]string{"hevc": PrefWant}, HDR: map[string]string{"HDR10+": PrefWant, "HDR10": PrefWant}}})
	if err != nil {
		t.Fatal(err)
	}
	d := s.Decide(ctx, "custom:"+strconv.FormatInt(sp.ID, 10), []Candidate{
		NewCandidate("Film.2021.2160p.WEB-DL.DDP5.1.H.265-UHD", 20, 50),
		NewCandidate("Film.2021.1080p.BluRay.HDR10+.TrueHD.Atmos.x265-FHD", 14, 50),
	})
	if d.Winner == nil || d.Shadow == nil || !d.Shadow.Differs {
		t.Fatalf("decision = %+v", d)
	}
}

// Upgrades record the kind of gain that decided them.
func TestUpgradePickNamesTheGain(t *testing.T) {
	s, ctx := testService(t)
	sp, err := s.Create(ctx, StoredProfile{MediaType: MediaMovie, Name: "up", UpgradesEnabled: true, UpgradeMinPercent: 25,
		AllowedResolutions: []string{"2160p", "1080p"}})
	if err != nil {
		t.Fatal(err)
	}
	ref := "custom:" + strconv.FormatInt(sp.ID, 10)
	cur := cf("Film.2021.1080p.WEB-DL.H.264-GRP", 4, 120)
	cases := []struct {
		cand Candidate
		want RankKey
	}{
		{NewCandidate("Film.2021.2160p.WEB-DL.H.265-GRP", 15, 50), KeyResolution},
		{NewCandidate("Film.2021.1080p.BluRay.x264-GRP", 4, 50), KeySource},
		{NewCandidate("Film.2021.1080p.WEB-DL.H.264.PROPER-GRP", 4, 50), KeyProper},
		{NewCandidate("Film.2021.1080p.WEB-DL.H.264-HEAVY", 9, 50).WithRuntime(120), KeyBitrate},
	}
	for _, tc := range cases {
		_, key, ok := s.UpgradePick(ctx, ref, cur, []Candidate{tc.cand})
		if !ok || key != tc.want {
			t.Errorf("%s: ok=%v key=%q, want %q", tc.cand.Name, ok, key, tc.want)
		}
	}
	// The low-quality group escape names the avoid it left.
	if _, key, ok := s.UpgradePick(ctx, ref, cf("Film.2021.1080p.WEBRip.x264-YTS", 2, 120), []Candidate{NewCandidate("Film.2021.1080p.WEBRip.x264-GRP", 2, 50)}); !ok || key != KeyAvoid {
		t.Errorf("YTS escape: ok=%v key=%q", ok, key)
	}
}

// The setting reads v1 unless it says v2.
func TestNormalizeRanking(t *testing.T) {
	for in, want := range map[string]string{"": RankingV1, "v1": RankingV1, "v2": RankingV2, " V2 ": RankingV2, "v3": RankingV1} {
		if got := NormalizeRanking(in); got != want {
			t.Errorf("NormalizeRanking(%q) = %q, want %q", in, got, want)
		}
	}
	if !ValidRanking("v1") || !ValidRanking("v2") || ValidRanking("") || ValidRanking("V2") {
		t.Error("ValidRanking")
	}
}
