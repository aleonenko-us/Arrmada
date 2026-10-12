package quality

import (
	"errors"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/parser"
)

// fmatch runs one custom format through an engine the way Evaluate does.
func fmatch(f CustomFormat, name string) bool {
	e := NewEngine([]CustomFormat{f})
	return e.match(e.formats[f.Name], name, parser.Parse(name))
}

func fmtOf(match string, conds ...Condition) CustomFormat {
	return CustomFormat{Name: "Test", Match: match, Conditions: conds}
}

// Each alias family matches what the parser reads, whatever name the value was typed in.
func TestCustomFormatAliases(t *testing.T) {
	hevc := "Movie.2020.2160p.WEB-DL.DDP5.1.Atmos.DV.HDR10Plus.H.265-FLUX"
	avc := "Movie.2020.1080p.BluRay.DTS-HD.MA.5.1.x264-SPARKS"
	av1 := "Movie.2020.1080p.WEBRip.AV1.Opus-GRP"
	dtsx := "Movie.2020.2160p.UHD.BluRay.REMUX.DTS-X.7.1-GRP"
	cases := []struct {
		typ   ConditionType
		value string
		name  string
		want  bool
	}{
		{CondCodec, "HEVC", hevc, true},
		{CondCodec, "H.265", hevc, true},
		{CondCodec, "h265", hevc, true},
		{CondCodec, "HEVC", avc, false},
		{CondCodec, "AVC", avc, true},
		{CondCodec, "H.264", avc, true},
		{CondCodec, "av1", av1, true},
		{CondDynamicRange, "Dolby Vision", hevc, true},
		{CondDynamicRange, "DoVi", hevc, true},
		{CondDynamicRange, "HDR10Plus", hevc, true},
		{CondDynamicRange, "Dolby Vision", avc, false},
		{CondAudio, "DTS-HD MA", avc, true},
		{CondAudio, "DTS:X", dtsx, true},
		{CondAudio, "Dolby Atmos", hevc, true},
		{CondAudio, "Dolby Atmos", avc, false},
		{CondSource, "Blu-ray", avc, true},
		{CondSource, "WEB", hevc, true}, // WEB-DL
		{CondSource, "WEB", av1, true},  // WEBRip: plain WEB is the whole WEB tier
		{CondSource, "WEB", avc, false},
		{CondSource, "Blu-ray", hevc, false},
		{CondResolution, "4K", hevc, true},
		{CondResolution, "1080", avc, true},
	}
	for _, c := range cases {
		if got := fmatch(fmtOf("", Condition{Type: c.typ, Value: c.value}), c.name); got != c.want {
			t.Errorf("%s = %q on %s: got %v, want %v", c.typ, c.value, c.name, got, c.want)
		}
	}
}

func TestCustomFormatCaseInsensitive(t *testing.T) {
	name := "Movie.2020.1080p.BluRay.x265-GRP"
	for _, c := range []Condition{
		{Type: CondCodec, Value: "X265"},
		{Type: CondSource, Value: "bluray"},
		{Type: CondResolution, Value: "1080P"},
		{Type: CondReleaseGroup, Value: "grp"},
	} {
		if !fmatch(fmtOf("", c), name) {
			t.Errorf("%s = %q should match %s", c.Type, c.Value, name)
		}
	}
	// Stored values are untouched: the engine normalises its own copy.
	f := fmtOf("", Condition{Type: CondCodec, Value: "HEVC"})
	NewEngine([]CustomFormat{f})
	if f.Conditions[0].Value != "HEVC" {
		t.Errorf("NewEngine rewrote the caller's format: %q", f.Conditions[0].Value)
	}
}

func TestCustomFormatEdition(t *testing.T) {
	name := "Movie.2020.Directors.Cut.1080p.BluRay.x264-GRP"
	for _, v := range []string{"Director's Cut", "Directors Cut", "director’s cut", "DIRECTORS.CUT"} {
		if !fmatch(fmtOf("", Condition{Type: CondEdition, Value: v}), name) {
			t.Errorf("edition %q should match %s", v, name)
		}
	}
	if fmatch(fmtOf("", Condition{Type: CondEdition, Value: "Extended"}), name) {
		t.Error("Extended matched a Director's Cut")
	}
}

func TestCustomFormatReleaseTitle(t *testing.T) {
	re := Condition{Type: CondReleaseTitle, Value: `\bFraMeSToR\b`}
	if !fmatch(fmtOf("", re), "Dune.Part.Two.2024.2160p.UHD.BluRay.REMUX.DV.HDR.TrueHD.Atmos.7.1-FraMeSToR") {
		t.Error(`\bFraMeSToR\b should match a FraMeSToR release`)
	}
	if !fmatch(fmtOf("", re), "dune.part.two.2024.2160p.remux-framestor") {
		t.Error("release_title should match case-insensitively")
	}
	if fmatch(fmtOf("", re), "Dune.Part.Two.2024.2160p.WEB-DL-FLUX") {
		t.Error("release_title matched a release without the pattern")
	}
	// A pattern that doesn't compile never holds — not even negated, where "doesn't match"
	// would otherwise turn into "matches everything".
	bad := Condition{Type: CondReleaseTitle, Value: `(FraMeSToR`}
	if fmatch(fmtOf("", bad), "Movie-(FraMeSToR") {
		t.Error("an invalid pattern matched")
	}
	bad.Negate = true
	if fmatch(fmtOf("", bad), "Movie.2020.1080p-GRP") {
		t.Error("a negated invalid pattern matched")
	}
	// Without a name (CustomFormat.Matches on a bare Release) the pattern can't hold.
	if fmtOf("", re).Matches(parser.Parse("Movie-FraMeSToR")) {
		t.Error("release_title held without a release name")
	}
}

func TestCustomFormatAnyAllNegate(t *testing.T) {
	groups := fmtOf(MatchAny,
		Condition{Type: CondReleaseGroup, Value: "FLUX"},
		Condition{Type: CondReleaseGroup, Value: "NTb"})
	for _, n := range []string{"Show.S01E01.1080p.WEB-DL.H.264-FLUX", "Show.S01E01.1080p.WEB-DL.H.264-NTb"} {
		if !fmatch(groups, n) {
			t.Errorf("any over FLUX/NTb should match %s", n)
		}
	}
	if fmatch(groups, "Show.S01E01.1080p.WEB-DL.H.264-EVO") {
		t.Error("any over FLUX/NTb matched EVO")
	}
	// The same with alternatives in one value.
	if !fmatch(fmtOf("", Condition{Type: CondReleaseGroup, Value: "FLUX|NTb"}), "Show.S01E01.1080p.WEB-DL.H.264-NTb") {
		t.Error("FLUX|NTb should match NTb")
	}

	notYTS := fmtOf(MatchAll,
		Condition{Type: CondSource, Value: "BluRay"},
		Condition{Type: CondReleaseGroup, Value: "YTS", Negate: true})
	if !fmatch(notYTS, "Movie.2020.1080p.BluRay.x264-SPARKS") {
		t.Error("BluRay and not YTS should match SPARKS")
	}
	if fmatch(notYTS, "Movie.2020.1080p.BluRay.x264-YTS") {
		t.Error("the negated group still matched YTS")
	}
	// Any with a negated condition: either a remux, or anything not from YTS.
	if !fmatch(fmtOf("any", Condition{Type: CondSource, Value: "Remux"}, Condition{Type: CondReleaseGroup, Value: "YTS", Negate: true}),
		"Movie.2020.1080p.WEBRip.x264-GRP") {
		t.Error("any with a negated condition should hold when the negation does")
	}
	if fmatch(fmtOf("all"), "Movie.2020.1080p.BluRay.x264-GRP") || fmatch(fmtOf("any"), "Movie.2020.1080p.BluRay.x264-GRP") {
		t.Error("a format with no conditions matched")
	}
}

// Keywords and reject terms match with separators folded, on both sides, and keep their
// token bounds.
func TestSeparatorNormalisedTerms(t *testing.T) {
	name := "Movie.2020.Directors.Cut.1080p.BluRay.x264-GRP"
	if got := KeywordScore([]Keyword{{Term: "Directors Cut", Score: 40}}, name); got != 40 {
		t.Errorf("keyword 'Directors Cut' scored %d on %s, want 40", got, name)
	}
	if got := KeywordScore([]Keyword{{Term: "directors.cut", Score: 40}}, "Movie 2020 Directors Cut 1080p"); got != 40 {
		t.Errorf("keyword 'directors.cut' scored %d on a spaced name, want 40", got)
	}
	if !Rejects([]string{"web-dl"}, "Movie.2020.1080p.WEB.DL.x264-GRP") {
		t.Error("reject 'web-dl' should catch WEB.DL")
	}
	if Rejects([]string{"com"}, "Show.S01.Complete.1080p.WEB-DL") {
		t.Error("reject 'com' matched inside 'Complete'")
	}
	if Rejects([]string{"cam"}, "Movie.2020.American.Camera.1080p") {
		t.Error("reject 'cam' matched inside a word")
	}
	if !Rejects([]string{".exe"}, "Movie.2020.1080p.WEB-DL.exe") {
		t.Error("reject '.exe' should catch the extension")
	}

	e := NewDefaultEngine()
	p := Profile{Keywords: []Keyword{{Term: "Directors Cut", Score: 40}}}
	ev := e.Evaluate(p, NewCandidate(name, 10, 10))
	if !containsStr(ev.Matched, "Directors Cut") || ev.CustomScore != 40 {
		t.Errorf("Evaluate: matched %v, custom score %d", ev.Matched, ev.CustomScore)
	}
}

// A release_title format scores through Evaluate, like any other format.
func TestEvaluateScoresReleaseTitleFormat(t *testing.T) {
	sp := StoredProfile{
		CustomFormats: []CustomFormat{{Name: "Tier 1", Conditions: []Condition{{Type: CondReleaseTitle, Value: `-(FraMeSToR|FLUX)$`}}}},
	}
	e := sp.Engine()
	p := Profile{FormatScores: map[string]int{"Tier 1": 100}}
	hit := e.Evaluate(p, NewCandidate("Dune.Part.Two.2024.2160p.WEB-DL.DDP5.1.Atmos.DV.HDR.H.265-FLUX", 24, 10))
	miss := e.Evaluate(p, NewCandidate("Dune.Part.Two.2024.2160p.WEB-DL.DDP5.1.HDR.H.265-RUMOUR", 19, 10))
	if hit.CustomScore != 100 || !containsStr(hit.Matched, "Tier 1") {
		t.Errorf("FLUX: custom %d, matched %v", hit.CustomScore, hit.Matched)
	}
	if miss.CustomScore != 0 {
		t.Errorf("RUMOUR: custom %d", miss.CustomScore)
	}
	// Required works on the pattern too.
	p = Profile{Required: []string{"Tier 1"}}
	if ev := e.Evaluate(p, NewCandidate("Dune.Part.Two.2024.2160p.WEB-DL.DDP5.1.HDR.H.265-RUMOUR", 19, 10)); ev.Eligible {
		t.Error("a release without the required pattern was eligible")
	}
}

func TestValidateFormats(t *testing.T) {
	ok := []CustomFormat{
		{Name: "Tier 1", Match: "any", Conditions: []Condition{{Type: CondReleaseGroup, Value: "FLUX"}, {Type: CondReleaseTitle, Value: `\bNTb\b`}}},
		{Name: "Lossless audio", Conditions: []Condition{{Type: CondLossless}}},
	}
	if errs := (StoredProfile{CustomFormats: ok}).Validate(); len(errs) != 0 {
		t.Fatalf("valid formats refused: %v", errs)
	}
	cases := []struct {
		f    CustomFormat
		want string
	}{
		{CustomFormat{Name: "Bad regex", Conditions: []Condition{{Type: CondReleaseTitle, Value: `(FLUX`}}}, "isn't valid"},
		{CustomFormat{Name: "Lookahead", Conditions: []Condition{{Type: CondReleaseTitle, Value: `^(?!.*REMUX).*BluRay`}}}, "lookarounds"},
		{CustomFormat{Name: "Huge", Conditions: []Condition{{Type: CondReleaseTitle, Value: strings.Repeat("a", maxPatternLen+1)}}}, "limit"},
		{CustomFormat{Name: "Unknown", Conditions: []Condition{{Type: "bitrate", Value: "9"}}}, "unknown type"},
		{CustomFormat{Name: "Empty", Conditions: []Condition{{Type: CondCodec, Value: "  "}}}, "needs a value"},
		{CustomFormat{Name: "None"}, "at least one condition"},
		{CustomFormat{Name: "", Conditions: []Condition{{Type: CondCodec, Value: "x265"}}}, "give it a name"},
		{CustomFormat{Name: "hevc", Conditions: []Condition{{Type: CondCodec, Value: "x265"}}}, "built-in"},
		{CustomFormat{Name: "Mode", Match: "most", Conditions: []Condition{{Type: CondCodec, Value: "x265"}}}, "all or any"},
	}
	for _, c := range cases {
		errs := (StoredProfile{CustomFormats: []CustomFormat{c.f}}).Validate()
		if len(errs) == 0 {
			t.Errorf("%q: no error", c.f.Name)
			continue
		}
		msg := errs[0].Error()
		if !strings.Contains(msg, c.want) {
			t.Errorf("%q: %q doesn't say %q", c.f.Name, msg, c.want)
		}
		if c.f.Name != "" && !strings.Contains(msg, c.f.Name) {
			t.Errorf("%q: the error doesn't name the format: %q", c.f.Name, msg)
		}
	}
	dup := []CustomFormat{
		{Name: "Tier", Conditions: []Condition{{Type: CondReleaseGroup, Value: "A"}}},
		{Name: "tier", Conditions: []Condition{{Type: CondReleaseGroup, Value: "B"}}},
	}
	if errs := (StoredProfile{CustomFormats: dup}).Validate(); len(errs) != 1 {
		t.Errorf("duplicate names: %v", errs)
	}
}

func TestFormatWarnings(t *testing.T) {
	fs := []CustomFormat{{Name: "Odd", Conditions: []Condition{
		{Type: CondCodec, Value: "HEVC|h266"},
		{Type: CondEdition, Value: "Directors Cut"},
		{Type: CondEdition, Value: "Criterion"},
		{Type: CondReleaseGroup, Value: "Anything"},
	}}}
	w := FormatWarnings(fs)
	if len(w) != 2 {
		t.Fatalf("warnings = %v, want h266 and Criterion", w)
	}
	if w[0].Condition != 0 || !strings.Contains(w[0].Message, "h266") {
		t.Errorf("first warning: %+v", w[0])
	}
	if w[1].Condition != 2 || !strings.Contains(w[1].Message, "Criterion") {
		t.Errorf("second warning: %+v", w[1])
	}
}

func TestServiceRefusesInvalidFormats(t *testing.T) {
	s, _, ctx := storeService(t)
	sp := StoredProfile{Name: "Bad", MediaType: MediaMovie, CustomFormats: []CustomFormat{
		{Name: "Broken", Conditions: []Condition{{Type: CondReleaseTitle, Value: `[FLUX`}}},
	}}
	_, err := s.Create(ctx, sp)
	var invalid *InvalidProfileError
	if !errors.As(err, &invalid) || !strings.Contains(err.Error(), `"Broken"`) {
		t.Fatalf("Create: %v", err)
	}
	sp.CustomFormats[0].Conditions[0].Value = `\[FLUX\]`
	created, err := s.Create(ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	created.CustomFormats[0].Conditions[0].Value = `(`
	if err := s.Update(ctx, created.ID, created); !errors.As(err, &invalid) {
		t.Fatalf("Update: %v", err)
	}
}

// legacyMatches is the matcher as it was before aliases, regex and any/all: exact,
// case-sensitive values, every condition required. Frozen here so the regression test
// below can prove formats saved under it still match exactly the same releases.
func legacyMatches(f CustomFormat, r parser.Release) bool {
	one := func(c Condition, v string) bool {
		switch c.Type {
		case CondDynamicRange:
			return containsStr(r.HDR, v) || (v == "SDR" && len(r.HDR) == 0)
		case CondAudio:
			return containsStr(r.Audio, v)
		case CondCodec:
			return string(r.Codec) == v
		case CondSource:
			return string(r.Source) == v
		case CondResolution:
			return string(r.Resolution) == v
		case CondEdition:
			return strings.EqualFold(r.Edition, v)
		case CondReleaseGroup:
			return strings.EqualFold(r.Group, v)
		case CondLossless:
			return losslessAudio(r)
		}
		return false
	}
	for _, c := range f.Conditions {
		hit := false
		for _, v := range strings.Split(c.Value, "|") {
			if one(c, v) {
				hit = true
				break
			}
		}
		if c.Negate {
			hit = !hit
		}
		if !hit {
			return false
		}
	}
	return len(f.Conditions) > 0
}

// Every built-in format, and custom formats in the shape the old editor saved them (one
// exact parser value per condition, the odd hand-written alternative or negation), match
// the same releases under the new engine as under the old matcher.
func TestStoredFormatsMatchAsBefore(t *testing.T) {
	formats := append(DefaultFormats(),
		CustomFormat{Name: "FraMeSToR", Conditions: []Condition{{Type: CondReleaseGroup, Value: "FraMeSToR"}}},
		CustomFormat{Name: "Remux", Conditions: []Condition{{Type: CondSource, Value: "Remux"}}},
		CustomFormat{Name: "Web", Conditions: []Condition{{Type: CondSource, Value: "WEB-DL"}}},
		CustomFormat{Name: "Rip", Conditions: []Condition{{Type: CondSource, Value: "WEBRip"}}},
		CustomFormat{Name: "UHD", Conditions: []Condition{{Type: CondResolution, Value: "2160p"}}},
		CustomFormat{Name: "Directors", Conditions: []Condition{{Type: CondEdition, Value: "Director's Cut"}}},
		CustomFormat{Name: "Imax", Conditions: []Condition{{Type: CondEdition, Value: "IMAX"}}},
		CustomFormat{Name: "Lossless surround", Conditions: []Condition{{Type: CondAudio, Value: "TrueHD|DTS-HD|FLAC"}}},
		CustomFormat{Name: "BluRay not YTS", Conditions: []Condition{{Type: CondSource, Value: "BluRay"}, {Type: CondReleaseGroup, Value: "YTS", Negate: true}}},
		CustomFormat{Name: "DV HEVC", Conditions: []Condition{{Type: CondDynamicRange, Value: "DV"}, {Type: CondCodec, Value: "x265"}}},
		CustomFormat{Name: "Not HDR", Conditions: []Condition{{Type: CondDynamicRange, Value: "HDR10", Negate: true}}},
	)
	names := []string{
		"Movie.2020.Directors.Cut.1080p.BluRay.x264-YTS",
		"Movie.2020.IMAX.2160p.WEB-DL.DDP5.1.Atmos.DV.HDR10.H.265-FLUX",
		"Movie.2020.1080p.WEBRip.x265.AAC-RARBG",
		"Movie.2020.2160p.UHD.BluRay.REMUX.HDR10Plus.HEVC.TrueHD.Atmos.7.1-FraMeSToR",
		"Movie.2020.720p.HDTV.x264-GALAXY",
		"Movie.2020.DVDRip.XviD-GRP",
		"Movie 2020 1080p BluRay FLAC 2.0 AV1-GRP",
		"[SubsPlease] Show - 01 (1080p) [ABCD1234].mkv",
		"Show.S01E01.1080p.AMZN.WEB-DL.DDP5.1.H.264-NTb",
		"Movie.2020.HDCAM.x264-GRP",
		"Movie.2020.Extended.Remastered.1080p.BluRay.DTS-HD.HRA.x264-GRP",
		"Movie.2020.2160p.HLG.WEB-DL.LPCM.VC-1-GRP",
	}
	for _, c := range SampleCandidates() {
		names = append(names, c.Name)
	}
	e := NewEngine(formats)
	for _, f := range formats {
		for _, n := range names {
			r := parser.Parse(n)
			if got, want := e.match(e.formats[f.Name], n, r), legacyMatches(f, r); got != want {
				t.Errorf("%s on %s: now %v, before %v", f.Name, n, got, want)
			}
		}
	}
}
