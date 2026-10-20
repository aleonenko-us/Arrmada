package quality

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/langs"
	"github.com/tristenlammi/arrmada/internal/parser"
)

// What a release name carries, as the language rules read it.
func TestReleaseLangs(t *testing.T) {
	cases := []struct {
		name, orig string
		codes      []string
		known      bool
		open       bool
		assumed    bool
	}{
		// Untagged: the original language.
		{"Film.2021.1080p.BluRay.x264-GRP", "en", []string{"en"}, true, false, true},
		{"Film.2021.1080p.BluRay.x264-GRP", "ja", []string{"ja"}, true, false, true},
		// Untagged, original unknown: nothing to judge.
		{"Film.2021.1080p.BluRay.x264-GRP", "", nil, false, false, false},
		// Tagged: exactly what it names.
		{"Film.2021.GERMAN.1080p.BluRay.x264-GRP", "en", []string{"de"}, true, false, false},
		{"Film.2021.TRUEFRENCH.1080p.BluRay.x264-GRP", "en", []string{"fr"}, true, false, false},
		{"Film.2021.ITA.ENG.1080p.BluRay.x264-GRP", "en", []string{"it", "en"}, true, false, false},
		// MULTi / DL: the original plus what it names; MULTi alone, plus unknown others.
		{"Film.2021.German.DL.1080p.BluRay.x264-GRP", "en", []string{"en", "de"}, true, false, false},
		{"Film.2021.MULTi.TRUEFRENCH.1080p.BluRay.x264-GRP", "en", []string{"en", "fr"}, true, false, false},
		{"Film.2021.MULTi.1080p.BluRay.x264-GRP", "en", []string{"en"}, true, true, false},
		{"Film.2021.MULTi.1080p.BluRay.x264-GRP", "", nil, true, true, false},
		// A German film tagged GERMAN.DL is German plus nothing new.
		{"Film.2021.German.DL.1080p.BluRay.x264-GRP", "de", []string{"de"}, true, false, false},
		// A 639-2 original folds to its base code.
		{"Film.2021.1080p.BluRay.x264-GRP", "fre", []string{"fr"}, true, false, true},
	}
	for _, tc := range cases {
		s := releaseLangs(parser.Parse(tc.name), tc.orig)
		if !reflect.DeepEqual(s.codes, tc.codes) || s.known != tc.known || s.open != tc.open || s.assumed != tc.assumed {
			t.Errorf("releaseLangs(%q, %q) = %+v, want codes %v known %v open %v assumed %v",
				tc.name, tc.orig, s, tc.codes, tc.known, tc.open, tc.assumed)
		}
	}
}

// A file on disk: probed tracks win over the name, an untagged track is read as the name
// states, and nothing is ever assumed from an untagged name — a file with nothing to read
// stays unknown. An all-English probe (the tag tools write when nobody set one) can't
// contradict a name that explicitly names other languages: the file is read as both.
func TestFileLangsProbedWinOverTheName(t *testing.T) {
	german := releaseLangs(parser.Parse("Film.2021.GERMAN.1080p.BluRay.x264-GRP"), "en")
	untagged := releaseLangs(parser.Parse("Film.2021.1080p.BluRay.x264-GRP"), "en")
	cases := []struct {
		name  string
		f     *FileFacts
		named langSet
		codes []string
		open  bool
		known bool
	}{
		{"no facts, a name that says German", nil, german, []string{"de"}, false, true},
		{"no facts, an untagged name: unknown", nil, untagged, nil, false, false},
		{"facts without audio", &FileFacts{}, german, []string{"de"}, false, true},
		{"facts without audio, untagged name: unknown", &FileFacts{}, untagged, nil, false, false},
		{"a tagged Italian track wins over the name", &FileFacts{AudioLangs: []string{"ita"}}, german, []string{"it"}, false, true},
		{"an all-English probe and a name that says German: both", &FileFacts{AudioLangs: []string{"eng"}}, german, []string{"en", "de"}, false, true},
		{"an all-English probe and an untagged name: English", &FileFacts{AudioLangs: []string{"eng"}}, untagged, []string{"en"}, false, true},
		{"two tagged tracks", &FileFacts{AudioLangs: []string{"ger", "eng"}}, untagged, []string{"de", "en"}, false, true},
		{"an untagged track beside an untagged name may be anything", &FileFacts{AudioLangs: []string{"ger"}, AudioUntagged: true}, untagged, []string{"de"}, true, true},
		{"only untagged tracks: the name speaks", &FileFacts{AudioUntagged: true}, german, []string{"de"}, false, true},
		{"untagged with nothing to read it by", &FileFacts{AudioLangs: []string{"ger"}, AudioUntagged: true}, langSet{}, []string{"de"}, true, true},
		{"nothing at all", &FileFacts{AudioUntagged: true}, langSet{}, nil, true, false},
		{"Norwegian written forms fold", &FileFacts{AudioLangs: []string{"nob", "nno"}}, untagged, []string{"no"}, false, true},
	}
	for _, tc := range cases {
		s := fileLangs(tc.f, tc.named)
		if !reflect.DeepEqual(s.codes, tc.codes) || s.open != tc.open || s.known != tc.known {
			t.Errorf("%s: %+v, want codes %v open %v known %v", tc.name, s, tc.codes, tc.open, tc.known)
		}
	}
}

// "Original language" resolves to the title's own; a language's own entry wins over it;
// without an original language it is dropped. The saved row is tidied to base codes.
func TestLangRulesOriginalResolution(t *testing.T) {
	row := normLanguageRow(map[string]string{"original": "want", "GER": "must", "fra": "avoid", "xx": "must", "es": "ok"})
	if !reflect.DeepEqual(row, map[string]string{LangOriginal: PrefWant, "de": PrefMust, "fr": PrefAvoid}) {
		t.Fatalf("normLanguageRow = %v", row)
	}
	if got := langRules(row, "ja"); !reflect.DeepEqual(got, map[string]string{"ja": PrefWant, "de": PrefMust, "fr": PrefAvoid}) {
		t.Errorf("original ja: %v", got)
	}
	if got := langRules(row, "de"); got["de"] != PrefMust {
		t.Errorf("German's own Must beats Original's Prefer: %v", got)
	}
	if got := langRules(row, ""); !reflect.DeepEqual(got, map[string]string{"de": PrefMust, "fr": PrefAvoid}) {
		t.Errorf("unknown original: %v", got)
	}
	if got := langRules(map[string]string{LangOriginal: PrefMust}, ""); got != nil {
		t.Errorf("only an unresolvable Original: %v, want no rules", got)
	}
	if normLanguageRow(nil) != nil || normLanguageRow(map[string]string{"en": ""}) != nil {
		t.Error("an empty row is no row")
	}
}

func langProfile(row map[string]string) Profile {
	return Profile{AllowedResolutions: []parser.Resolution{parser.Res1080p}, Languages: row}
}

func enCand(name string, gb float64) Candidate {
	return NewCandidate(name, gb, 50).WithLanguage("en").WithRuntime(120)
}

// Must refuses a release whose name doesn't carry the language, in both rankings, with
// the language named in the reason.
//
// Before issue #58 the MULTi release here was eligible ("MULTi alone may carry German"):
// a Must the name didn't mention was unknown, so every MULTi got through a Must, and a
// profile requiring Italian kept grabbing English-plus-something MULTi releases. A Must now
// has to be named (or be the original language), so the MULTi is refused with a reason that
// says the name doesn't name German — and taken again only with the profile's
// MultiCountsLanguage switch on (TestMustNamedTable, TestMultiCountsSwitch).
func TestLanguageMustRefusesByName(t *testing.T) {
	forEachRanking(t, func(t *testing.T, mode string) {
		p := langProfile(map[string]string{"de": PrefMust})
		d := engineIn(mode).Decide(p, []Candidate{
			enCand("Film.2021.1080p.BluRay.x264-ENG", 12),
			enCand("Film.2021.German.DL.1080p.WEB-DL.x264-GER", 6),
			enCand("Film.2021.MULTi.1080p.BluRay.x264-MUL", 11), // doesn't say German
			enCand("Film.2021.GERMAN.1080p.WEB-DL.x264-GO", 5),
			enCand("Film.2021.iTALiAN.1080p.WEB-DL.x264-IT", 5),
		})
		if len(d.Eligible) != 2 || len(d.Rejected) != 3 {
			t.Fatalf("eligible %d rejected %d, want only the two German releases", len(d.Eligible), len(d.Rejected))
		}
		for _, ev := range d.Rejected {
			want := "Missing German audio"
			if ev.Candidate.Name == "Film.2021.MULTi.1080p.BluRay.x264-MUL" {
				want = "Doesn't name German audio"
			}
			if ev.RejectCode != RejectMissingLanguage || !strings.HasPrefix(ev.RejectReason, want) {
				t.Errorf("%s: %s %q, want %q…", ev.Candidate.Name, ev.RejectCode, ev.RejectReason, want)
			}
		}
		// German with the original beats German alone on nothing language-wise (both have
		// the Must); the heavier one wins as it would without the rule.
		if winnerName(d) != "Film.2021.German.DL.1080p.WEB-DL.x264-GER" {
			t.Errorf("winner %s", winnerName(d))
		}
	})
}

// DUAL / Dual Audio on a foreign title is the original plus English, so an English Must
// grabs anime dual-audio releases. A MULTi that names nothing is the original and unnamed
// others: a French Must takes a French film's MULTi (French is its original), and — since
// issue #58 — refuses an English film's MULTi, which used to pass as "may carry French".
// Dual Audio on an English title names only English, so a German Must refuses it too
// (it used to be unknown, and taken).
func TestDualAudioAndMultiUnderAMust(t *testing.T) {
	forEachRanking(t, func(t *testing.T, mode string) {
		en := Profile{AllowedResolutions: []parser.Resolution{parser.Res1080p}, Languages: map[string]string{"en": PrefMust}}
		for _, n := range []string{"[Group] Show - 05 (1080p) [Dual Audio] [ABCD1234]", "Show.S01E05.1080p.BluRay.x264.Dual-Audio-GRP"} {
			ev := engineIn(mode).Evaluate(en, NewCandidate(n, 1, 50).WithLanguage("ja"))
			if !ev.Eligible || !containsStr(ev.Matched, "English audio") || !reflect.DeepEqual(ev.AudioLangs, []string{"ja", "en"}) {
				t.Errorf("%s: eligible %v %q matched %v langs %v", n, ev.Eligible, ev.RejectReason, ev.Matched, ev.AudioLangs)
			}
		}
		// A MULTi on a Japanese title doesn't say English: refused.
		multi := engineIn(mode).Evaluate(en, NewCandidate("Show.S01E05.1080p.WEB.x264.MULTi-GRP", 1, 50).WithLanguage("ja"))
		if multi.Eligible || !strings.HasPrefix(multi.RejectReason, "Doesn't name English audio") {
			t.Errorf("MULTi on a Japanese title: eligible %v %q", multi.Eligible, multi.RejectReason)
		}
		fr := Profile{Languages: map[string]string{"fr": PrefMust}}
		if ev := engineIn(mode).Evaluate(fr, NewCandidate("Film.2021.MULTi.1080p.BluRay.x264-GRP", 8, 50).WithLanguage("fr")); !ev.Eligible {
			t.Errorf("French Must refused a French film's MULTi: %q", ev.RejectReason)
		}
		if ev := engineIn(mode).Evaluate(fr, NewCandidate("Film.2021.MULTi.1080p.BluRay.x264-GRP", 8, 50).WithLanguage("en")); ev.Eligible {
			t.Errorf("French Must took an English film's MULTi that doesn't say French")
		}
		de := Profile{Languages: map[string]string{"de": PrefMust}}
		ev := engineIn(mode).Evaluate(de, NewCandidate("Film.2021.1080p.BluRay.x264.Dual-Audio-GRP", 8, 50).WithLanguage("en"))
		if ev.Eligible || ev.RejectReason != "Doesn't name German audio — your profile requires it, and a Dual Audio release only counts for the title's original language and the ones its name says" {
			t.Errorf("Dual Audio on an English film: eligible %v %q", ev.Eligible, ev.RejectReason)
		}
		// The row still says what it may carry.
		if ev.AudioLangsNote == "" {
			t.Errorf("Dual Audio on an English film lost its note: %+v", ev.AudioLangs)
		}
	})
}

// The Must rule, release by release (issue #58): a release passes a Must language only when
// its name names it, or the language is the title's original and the release is untagged or
// MULTi/DL/Dual (which always carry the original). multi is the profile's "MULTi releases
// count" switch.
func TestMustNamedTable(t *testing.T) {
	cases := []struct {
		name  string
		orig  string // the title's original language; "" = unknown
		row   map[string]string
		multi bool
		ok    bool
		why   string // the start of the refusal
	}{
		// Italian Must on an English film.
		{"Film.2021.MULTi.1080p.BluRay.x264-GRP", "en", map[string]string{"it": PrefMust}, false, false, "Doesn't name Italian audio — your profile requires it, and a MULTi release only counts"},
		{"Film.2021.MULTi.ITA.1080p.BluRay.x264-GRP", "en", map[string]string{"it": PrefMust}, false, true, ""},
		{"Film.2021.ITA.ENG.1080p.BluRay.x264-GRP", "en", map[string]string{"it": PrefMust}, false, true, ""},
		{"Film.2021.iTALiAN.1080p.BluRay.x264-GRP", "en", map[string]string{"it": PrefMust}, false, true, ""},
		{"Film.2021.DUAL.1080p.BluRay.x264-GRP", "en", map[string]string{"it": PrefMust}, false, false, "Doesn't name Italian audio — your profile requires it, and a Dual Audio release"},
		{"Film.2021.1080p.BluRay.x264-GRP", "en", map[string]string{"it": PrefMust}, false, false, "Missing Italian audio"},
		{"Film.2021.1080p.BluRay.x264.Dual-Audio-GRP", "en", map[string]string{"it": PrefMust}, false, false, "Doesn't name Italian audio"},
		// The issue's profile: Italian Must, Original Avoid. MULTi/DUAL refused; ITA.ENG taken.
		{"Film.2021.MULTi.1080p.BluRay.x264-GRP", "en", map[string]string{"it": PrefMust, LangOriginal: PrefAvoid}, false, false, "Doesn't name Italian audio"},
		{"Film.2021.ITA.ENG.1080p.BluRay.x264-GRP", "en", map[string]string{"it": PrefMust, LangOriginal: PrefAvoid}, false, true, ""},
		// The switch brings back the old reading for MULTi/Dual naming no language only.
		{"Film.2021.MULTi.1080p.BluRay.x264-GRP", "en", map[string]string{"it": PrefMust}, true, true, ""},
		{"Film.2021.DUAL.1080p.BluRay.x264-GRP", "en", map[string]string{"it": PrefMust}, true, true, ""},
		{"Film.2021.1080p.BluRay.x264-GRP", "en", map[string]string{"it": PrefMust}, true, false, "Missing Italian audio"},
		{"Film.2021.MULTi.TRUEFRENCH.1080p.BluRay.x264-GRP", "en", map[string]string{"it": PrefMust}, true, false, "Missing Italian audio"},
		// French Must on a French film: anything that carries the original passes.
		{"Film.2021.MULTi.1080p.BluRay.x264-GRP", "fr", map[string]string{"fr": PrefMust}, false, true, ""},
		{"Film.2021.1080p.BluRay.x264-GRP", "fr", map[string]string{"fr": PrefMust}, false, true, ""},
		{"Film.2021.DUAL.1080p.BluRay.x264-GRP", "fr", map[string]string{"fr": PrefMust}, false, true, ""},
		{"Film.2021.ENGLISH.1080p.BluRay.x264-GRP", "fr", map[string]string{"fr": PrefMust}, false, false, "Missing French audio"},
		// French Must on an English film: MULTi only with the switch; VFF/TRUEFRENCH always.
		{"Film.2021.MULTi.1080p.BluRay.x264-GRP", "en", map[string]string{"fr": PrefMust}, false, false, "Doesn't name French audio"},
		{"Film.2021.MULTi.1080p.BluRay.x264-GRP", "en", map[string]string{"fr": PrefMust}, true, true, ""},
		{"Film.2021.VFF.1080p.BluRay.x264-GRP", "en", map[string]string{"fr": PrefMust}, false, true, ""},
		{"Film.2021.MULTi.VFF.1080p.BluRay.x264-GRP", "en", map[string]string{"fr": PrefMust}, false, true, ""},
		{"Film.2021.MULTi.VFQ.1080p.BluRay.x264-GRP", "en", map[string]string{"fr": PrefMust}, false, true, ""},
		{"Film.2021.TRUEFRENCH.1080p.BluRay.x264-GRP", "en", map[string]string{"fr": PrefMust}, false, true, ""},
		// Original-language Must: untagged and MULTi carry it; a dub alone doesn't.
		{"Film.2021.1080p.BluRay.x264-GRP", "ja", map[string]string{LangOriginal: PrefMust}, false, true, ""},
		{"Film.2021.MULTi.1080p.BluRay.x264-GRP", "ja", map[string]string{LangOriginal: PrefMust}, false, true, ""},
		{"Film.2021.DUAL.1080p.BluRay.x264-GRP", "ja", map[string]string{LangOriginal: PrefMust}, false, true, ""},
		{"Film.2021.ENGLISH.1080p.BluRay.x264-GRP", "ja", map[string]string{LangOriginal: PrefMust}, false, false, "Missing Japanese audio"},
		// Unknown original language: a release naming none can't be judged, so it isn't
		// refused (as before #58); a MULTi that doesn't name the Must is (the #58 rule), with
		// the reason saying the original isn't known. "Original language" resolves to
		// nothing, so no rule.
		{"Film.2021.1080p.BluRay.x264-GRP", "", map[string]string{"it": PrefMust}, false, true, ""},
		{"Film.2021.1080p.BluRay.x264-GRP", "", map[string]string{"it": PrefMust}, true, true, ""},
		{"Film.2021.MULTi.1080p.BluRay.x264-GRP", "", map[string]string{"it": PrefMust}, false, false, "Doesn't name Italian audio — your profile requires it, and a MULTi release only counts for the title's original language and the ones its name says (this title's original language isn't known)"},
		{"Film.2021.DUAL.1080p.BluRay.x264-GRP", "", map[string]string{"it": PrefMust}, false, false, "Doesn't name Italian audio — your profile requires it, and a Dual Audio release"},
		{"Film.2021.MULTi.1080p.BluRay.x264-GRP", "", map[string]string{"it": PrefMust}, true, true, ""},
		{"Film.2021.iTALiAN.1080p.BluRay.x264-GRP", "", map[string]string{"it": PrefMust}, false, true, ""},
		{"Film.2021.1080p.BluRay.x264-GRP", "", map[string]string{LangOriginal: PrefMust}, false, true, ""},
		// A code the table doesn't know reads as unknown too (TMDB's "sh").
		{"Film.2021.1080p.BluRay.x264-GRP", "sh", map[string]string{"en": PrefMust}, false, true, ""},
		// TMDB "xx", no spoken language: an untagged release isn't refused under a Must; a
		// MULTi that doesn't name it is.
		{"Film.2021.1080p.BluRay.x264-GRP", "xx", map[string]string{"en": PrefMust}, false, true, ""},
		{"Film.2021.1080p.BluRay.x264-GRP", "xx", map[string]string{LangOriginal: PrefMust, "en": PrefWant}, false, true, ""},
		{"Film.2021.MULTi.1080p.BluRay.x264-GRP", "xx", map[string]string{"en": PrefMust}, false, false, "Doesn't name English audio"},
		// A Hong Kong film (TMDB "cn", Cantonese) has an original language now: Chinese. An
		// untagged release is Chinese, like an untagged French film is French — so an English
		// Must takes a release that says English, and refuses one that names nothing.
		{"Film.2021.1080p.BluRay.x264-GRP", "cn", map[string]string{"en": PrefMust}, false, false, "Missing English audio"},
		{"Film.2021.ENG.1080p.BluRay.x264-GRP", "cn", map[string]string{"en": PrefMust}, false, true, ""},
		{"Film.2021.1080p.BluRay.x264.Dual-Audio-GRP", "cn", map[string]string{"en": PrefMust}, false, true, ""},
		{"Film.2021.1080p.BluRay.x264-GRP", "cn", map[string]string{LangOriginal: PrefMust}, false, true, ""},
		{"Film.2021.1080p.BluRay.x264-GRP", "yue", map[string]string{"zh": PrefMust}, false, true, ""},
		{"Film.2021.MULTi.1080p.BluRay.x264-GRP", "cn", map[string]string{"zh": PrefMust}, false, true, ""},
		// …and with no Must, nothing is refused for it.
		{"Film.2021.1080p.BluRay.x264-GRP", "", map[string]string{"it": PrefWant, "en": PrefAvoid}, false, true, ""},
		{"Film.2021.MULTi.1080p.BluRay.x264-GRP", "", map[string]string{"it": PrefWant}, false, true, ""},
		{"Film.2021.1080p.BluRay.x264-GRP", "", nil, false, true, ""},
		// Prefer and Avoid never refuse.
		{"Film.2021.MULTi.1080p.BluRay.x264-GRP", "en", map[string]string{"it": PrefWant}, false, true, ""},
		{"Film.2021.1080p.BluRay.x264-GRP", "en", map[string]string{"en": PrefAvoid}, false, true, ""},
	}
	for _, tc := range cases {
		forEachRanking(t, func(t *testing.T, mode string) {
			p := Profile{Languages: normLanguageRow(tc.row), MultiCountsLanguage: tc.multi}
			c := NewCandidate(tc.name, 8, 50)
			c.OrigLanguage = tc.orig
			ev := engineIn(mode).Evaluate(p, c)
			if ev.Eligible != tc.ok {
				t.Errorf("%s (orig %q, %v, multi %v): eligible %v %q, want %v", tc.name, tc.orig, tc.row, tc.multi, ev.Eligible, ev.RejectReason, tc.ok)
				return
			}
			if !tc.ok && (ev.RejectCode != RejectMissingLanguage || !strings.HasPrefix(ev.RejectReason, tc.why)) {
				t.Errorf("%s (orig %q, %v, multi %v): %s %q, want %q…", tc.name, tc.orig, tc.row, tc.multi, ev.RejectCode, ev.RejectReason, tc.why)
			}
		})
	}
}

// Prefer and Avoid read a MULTi naming no language as before, switch or not: a Prefer it
// doesn't name earns no credit and costs nothing; an Avoid never puts an open set in the
// avoided tier, so the language it doesn't name keeps it out.
func TestMultiPreferAvoidUnchanged(t *testing.T) {
	forEachRanking(t, func(t *testing.T, mode string) {
		for _, multi := range []bool{false, true} {
			pref := Profile{Languages: map[string]string{"it": PrefWant}, MultiCountsLanguage: multi}
			ev := engineIn(mode).Evaluate(pref, enCand("Film.2021.MULTi.1080p.BluRay.x264-GRP", 8))
			if !ev.Eligible || containsStr(ev.Matched, "Italian audio") || !containsStr(ev.langUnknown, "it") {
				t.Errorf("multi %v: Prefer Italian on MULTi: eligible %v matched %v unknown %v", multi, ev.Eligible, ev.Matched, ev.langUnknown)
			}
			avoid := Profile{Languages: map[string]string{LangOriginal: PrefAvoid}, MultiCountsLanguage: multi}
			if ev := engineIn(mode).Evaluate(avoid, enCand("Film.2021.MULTi.1080p.BluRay.x264-GRP", 8)); !ev.Eligible || ev.TargetAvoided {
				t.Errorf("multi %v: Avoid original put a MULTi in the avoided tier: %v", multi, ev.AvoidedFormats)
			}
			if ev := engineIn(mode).Evaluate(avoid, enCand("Film.2021.1080p.BluRay.x264-GRP", 8)); !ev.Eligible || !ev.TargetAvoided {
				t.Errorf("multi %v: Avoid original on an untagged English release: eligible %v avoided %v", multi, ev.Eligible, ev.TargetAvoided)
			}
		}
	})
}

// The switch only changes releases on offer. A file on disk is never refused on what its
// name leaves out: a MULTi-named file with nothing probed is left alone under an Italian
// Must, so the stricter reading sends nothing back to the indexers.
func TestMustNamedLeavesFilesAlone(t *testing.T) {
	sp := StoredProfile{MediaType: MediaMovie, Name: "it", AllowedResolutions: []string{"1080p"},
		Ideal: &IdealFile{Language: map[string]string{"it": PrefMust}}}
	normalize(&sp)
	for _, name := range []string{"Film.2021.MULTi.1080p.BluRay.x264-GRP", "Film.2021.1080p.BluRay.x264.Dual-Audio-GRP"} {
		cur := CurrentFile{Release: name, SizeGB: 8, RuntimeMin: 120, OrigLanguage: "en"}
		if v := sp.JudgeFile(cur); !v.Eligible {
			t.Errorf("%s on disk: refused %q", name, v.Current.RejectReason)
		}
		if fit := sp.LanguageFit(cur); fit != nil {
			t.Errorf("%s on disk: badge %+v, want none (unknown)", name, fit)
		}
	}
	// With nothing to go on and an unknown original language, still left alone.
	if v := sp.JudgeFile(CurrentFile{Release: "Film.2021.1080p.BluRay.x264-GRP", SizeGB: 8, RuntimeMin: 120}); !v.Eligible {
		t.Errorf("untagged file, unknown original: refused %q", v.Current.RejectReason)
	}
}

// The switch round-trips through the stored profile into the engine.
func TestMultiCountsSwitch(t *testing.T) {
	c := enCand("Film.2021.MULTi.1080p.BluRay.x264-GRP", 8)
	for _, on := range []bool{false, true} {
		sp := StoredProfile{MediaType: MediaMovie, Name: "fr", AllowedResolutions: []string{"1080p"}, MultiCountsLanguage: on,
			Ideal: &IdealFile{Language: map[string]string{"fr": PrefMust}}}
		normalize(&sp)
		p := sp.ToProfile()
		if p.MultiCountsLanguage != on {
			t.Fatalf("ToProfile dropped the switch (%v)", on)
		}
		if ev := sp.Engine().Evaluate(p, c); ev.Eligible != on {
			t.Errorf("switch %v: MULTi eligible %v %q", on, ev.Eligible, ev.RejectReason)
		}
	}
}

// The builder's "Test a release name" has no title, so no original language: an untagged
// name is judged as before (not refused under a Must), a name that says the language is
// taken, and a MULTi that doesn't say it is refused with a reason that explains both the
// MULTi rule and why the original can't count here.
func TestExplainUnderAMust(t *testing.T) {
	sp := StoredProfile{MediaType: MediaMovie, Name: "it", AllowedResolutions: []string{"1080p"},
		Ideal: &IdealFile{Language: map[string]string{"it": PrefMust}}}
	s := &Service{}
	for _, name := range []string{"Film.2021.1080p.BluRay.x264-GRP", "Film.2021.MULTi.ITA.1080p.BluRay.x264-GRP"} {
		if ev := s.Explain(sp, name, 0, 0).Evaluation; !ev.Eligible {
			t.Errorf("%s: refused %q", name, ev.RejectReason)
		}
	}
	ev := s.Explain(sp, "Film.2021.MULTi.1080p.BluRay.x264-GRP", 0, 0).Evaluation
	want := "Doesn't name Italian audio — your profile requires it, and a MULTi release only counts for the title's original language and the ones its name says (a name tested here has no title, so no original language)"
	if ev.Eligible || ev.RejectReason != want {
		t.Errorf("MULTi: eligible %v %q", ev.Eligible, ev.RejectReason)
	}
	// With the switch on, the MULTi is taken (unknown, no credit).
	sp.MultiCountsLanguage = true
	if ev := s.Explain(sp, "Film.2021.MULTi.1080p.BluRay.x264-GRP", 0, 0).Evaluation; !ev.Eligible {
		t.Errorf("MULTi with the switch on: refused %q", ev.RejectReason)
	}
}

// The spellings read since issue #58 (FRA, DEU, NLD, ESP, JAP, VFB) count for releases on
// offer only. A file already in the library is judged as it was before them, so updating
// changes no file's verdict: a Must it would stop meeting, a Prefer that would make it an
// upgrade candidate, or its badge.
func TestNewSpellingsDontMoveFileVerdicts(t *testing.T) {
	forEachRanking(t, func(t *testing.T, mode string) {
		s, ctx := testService(t)
		setRanking(t, s, mode)
		// An unprobed English film named ESP: before #58 the name said nothing (unknown);
		// now it would read as Spanish-only and lack English.
		cur := onDisk("Film.2021.ESP.1080p.BluRay.x264-GRP", nil)
		mustEN := langStored(map[string]string{"en": PrefMust}, TriggerAny)
		normalize(&mustEN)
		if v := mustEN.JudgeFile(cur); !v.Eligible || v.Current.langShort {
			t.Errorf("Must English: file refused %q (short %v)", v.Current.RejectReason, v.Current.langShort)
		}
		if f := mustEN.LanguageFit(cur); f != nil {
			t.Errorf("Must English: badge %+v, want none", f)
		}
		// The same name as a release on offer is read as Spanish, and refused.
		if ev := mustEN.Engine().Evaluate(mustEN.ToProfile(), enCand(cur.Release, 8)); ev.Eligible {
			t.Error("the release on offer wasn't read as Spanish")
		}
		// Prefer French: a file named FRA isn't credited (no change), and an untagged-name
		// file isn't upgraded to a FRA release either way — no language was read on it.
		ref := upgradeProfile(t, s, langStored(map[string]string{"fr": PrefWant}, TriggerAny))
		fra := onDisk("Film.2021.FRA.1080p.BluRay.x264-GRP", nil)
		if _, _, ok := s.UpgradePick(ctx, ref, fra, []Candidate{enCand("Film.2021.TRUEFRENCH.1080p.BluRay.x264-FR", 8)}); ok {
			t.Error("a FRA file was upgraded to a French release for a language it may have")
		}
		// A probed file is read by its tracks whatever its name says.
		probed := onDisk("Film.2021.ESP.1080p.BluRay.x264-GRP", englishOnly)
		if v := mustEN.JudgeFile(probed); !v.Eligible {
			t.Errorf("probed English file refused %q", v.Current.RejectReason)
		}
	})
}

// Foreign-only stands aside per release, only for one carrying a language the profile
// asked for: "Prefer original" or "Prefer German" doesn't let an Italian-only 2160p beat
// the English 1080p.
func TestForeignOnlyStillGuardsPreferProfiles(t *testing.T) {
	for _, row := range []map[string]string{nil, {LangOriginal: PrefWant}, {"de": PrefWant}} {
		forEachRanking(t, func(t *testing.T, mode string) {
			p := Profile{AllowedResolutions: []parser.Resolution{parser.Res1080p, parser.Res2160p}, Languages: row, OwnerLanguages: []string{"en", "de"}}
			d := engineIn(mode).Decide(p, []Candidate{
				enCand("Film.2021.1080p.BluRay.x264-GRP", 10),
				enCand("Film.2021.iTALiAN.2160p.UHD.BluRay.x265-GRP", 30),
			})
			if winnerName(d) != "Film.2021.1080p.BluRay.x264-GRP" {
				t.Errorf("rules %v: winner %s", row, winnerName(d))
			}
		})
	}
}

// Prefer ranks a release with the language up — on the preferences key under v2, as a
// preferred format's score under classic — above a better source, never above the goal
// resolution.
func TestLanguagePreferRanks(t *testing.T) {
	forEachRanking(t, func(t *testing.T, mode string) {
		p := Profile{AllowedResolutions: []parser.Resolution{parser.Res2160p, parser.Res1080p}, Languages: map[string]string{"de": PrefWant}}
		remux := enCand("Film.2021.1080p.BluRay.REMUX.AVC-ENG", 30)
		german := enCand("Film.2021.German.DL.1080p.WEB-DL.x264-GER", 6)
		uhd := enCand("Film.2021.2160p.WEB-DL.x265-UHD", 15)
		d := engineIn(mode).Decide(p, []Candidate{remux, german})
		if winnerName(d) != german.Name {
			t.Fatalf("winner %s, want the German release", winnerName(d))
		}
		if mode == RankingV2 && d.DecidedBy != KeyPreferences {
			t.Errorf("decided by %s, want preferences", d.DecidedBy)
		}
		if !containsStr(d.Winner.Matched, "German audio") || !strings.Contains(d.ChosenOver, "German audio") {
			t.Errorf("explanation: matched %v, chosen over %q", d.Winner.Matched, d.ChosenOver)
		}
		if d := engineIn(mode).Decide(p, []Candidate{german, uhd}); winnerName(d) != uhd.Name {
			t.Errorf("a preferred language outranked the goal resolution: %s", winnerName(d))
		}
	})
}

// Avoid puts a release whose every language is avoided in the avoided tier; one that also
// carries another language isn't.
func TestLanguageAvoidTier(t *testing.T) {
	forEachRanking(t, func(t *testing.T, mode string) {
		p := langProfile(map[string]string{"fr": PrefAvoid})
		french := enCand("Film.2021.TRUEFRENCH.1080p.BluRay.REMUX.AVC-FR", 30)
		multi := enCand("Film.2021.MULTi.TRUEFRENCH.1080p.BluRay.x264-MUL", 12)
		web := enCand("Film.2021.1080p.WEB-DL.x264-ENG", 5)
		d := engineIn(mode).Decide(p, []Candidate{french, multi, web})
		if len(d.Eligible) != 3 {
			t.Fatalf("avoid must never reject: %d eligible", len(d.Eligible))
		}
		last := d.Eligible[2]
		if last.Candidate.Name != french.Name || !last.TargetAvoided || !containsStr(last.AvoidedFormats, "only French audio") {
			t.Errorf("last = %s avoided %v %v", last.Candidate.Name, last.TargetAvoided, last.AvoidedFormats)
		}
		for _, ev := range d.Eligible[:2] {
			if ev.Avoided {
				t.Errorf("%s avoided: %v", ev.Candidate.Name, ev.AvoidedFormats)
			}
		}
		// Only avoided releases: still grabbed, and the decision says why.
		d = engineIn(mode).Decide(p, []Candidate{french})
		if winnerName(d) != french.Name || d.Why[0] != "Only releases with a format you avoid were available" {
			t.Errorf("only avoided: %s %v", winnerName(d), d.Why)
		}
	})
}

// "Original + German": a German speaker who likes original audio. Dual audio wins, then
// either alone; a third language only is refused when both are Musts.
func TestOriginalPlusGerman(t *testing.T) {
	forEachRanking(t, func(t *testing.T, mode string) {
		p := langProfile(map[string]string{LangOriginal: PrefWant, "de": PrefWant})
		dual := enCand("Film.2021.German.DL.1080p.WEB-DL.x264-DUAL", 6)
		orig := enCand("Film.2021.1080p.BluRay.x264-ORIG", 12)
		dub := enCand("Film.2021.GERMAN.1080p.BluRay.x264-DUB", 12)
		d := engineIn(mode).Decide(p, []Candidate{orig, dub, dual})
		if winnerName(d) != dual.Name {
			t.Errorf("winner %s, want the dual-audio release", winnerName(d))
		}
		// For a Japanese film, "original" is Japanese.
		jp := NewCandidate("Anime.2021.1080p.BluRay.x264-JP", 8, 50).WithLanguage("ja")
		ev := engineIn(mode).Evaluate(p, jp)
		if !containsStr(ev.Matched, "Japanese audio") {
			t.Errorf("Japanese original: matched %v", ev.Matched)
		}
		must := langProfile(map[string]string{LangOriginal: PrefMust, "de": PrefMust})
		if ev := engineIn(mode).Evaluate(must, enCand("Film.2021.1080p.BluRay.x264-ORIG", 12)); ev.Eligible || ev.RejectReason != "Missing German audio — your profile requires it" {
			t.Errorf("original only under two Musts: %q", ev.RejectReason)
		}
		if ev := engineIn(mode).Evaluate(must, dual); !ev.Eligible {
			t.Errorf("dual audio under two Musts: %q", ev.RejectReason)
		}
	})
}

// No language rules: Evaluate is what it was — the golden files pin the whole matrix, and
// this pins the new fields as pure description.
func TestNoLanguageRulesChangeNothing(t *testing.T) {
	e := NewDefaultEngine()
	c := enCand("Film.2021.German.DL.1080p.WEB-DL.x264-GER", 6)
	ev := e.Evaluate(Profile{}, c)
	if ev.langPrefs != 0 || ev.langShort || ev.TargetAvoided || ev.PrefCount != 0 || len(ev.Matched) != 0 {
		t.Fatalf("rules applied without rules: %+v", ev)
	}
	if !reflect.DeepEqual(ev.AudioLangs, []string{"en", "de"}) {
		t.Errorf("audio langs %v", ev.AudioLangs)
	}
	if got := e.Evaluate(Profile{}, enCand("Film.2021.1080p.WEB-DL.x264-GRP", 6)); got.AudioLangsNote == "" {
		t.Error("an untagged release's languages are an assumption, and say so")
	}
}

// The foreign-only item defers to a profile that says which languages it wants, and isn't
// named twice beside a language Avoid.
func TestForeignOnlyDefersToLanguageRules(t *testing.T) {
	german := enCand("Film.2021.GERMAN.1080p.WEB-DL.x264-GO", 6)
	e := NewDefaultEngine()
	// Without rules a German-only release of an English film is foreign-only (avoided).
	if ev := e.Evaluate(Profile{}, german); !ev.TargetAvoided {
		t.Fatalf("foreign-only no longer avoided without rules: %+v", ev.AvoidedFormats)
	}
	// Preferring German: it's what's wanted, not foreign.
	if ev := e.Evaluate(Profile{Languages: map[string]string{"de": PrefWant}}, german); ev.TargetAvoided {
		t.Errorf("German preferred yet avoided: %v", ev.AvoidedFormats)
	}
	// Only an Avoid: the foreign-only item still judges what the rules don't mention…
	if ev := e.Evaluate(Profile{Languages: map[string]string{"fr": PrefAvoid}}, german); !ev.TargetAvoided {
		t.Error("an unrelated Avoid switched off foreign-only")
	}
	// …and a release both avoid is named once, by the language rule.
	fr := enCand("Film.2021.FRENCH.1080p.WEB-DL.x264-FR", 6)
	ev := e.Evaluate(Profile{Languages: map[string]string{"fr": PrefAvoid}}, fr)
	if !reflect.DeepEqual(ev.AvoidedFormats, []string{"only French audio"}) {
		t.Errorf("avoided %v, want the language rule alone", ev.AvoidedFormats)
	}
	// A foreign-only item set to reject still refuses it under an Avoid-only profile.
	rej := Profile{Languages: map[string]string{"fr": PrefAvoid}, Unwanted: Unwanted{Items: map[string]string{UnwantedForeign: UnwantedReject}}}
	if ev := e.Evaluate(rej, fr); ev.Eligible {
		t.Error("foreign-only set to reject let a French-only release through")
	}
}

// ---- Files on disk, upgrades and the import gate ----

func langStored(row map[string]string, trigger string) StoredProfile {
	return StoredProfile{Name: "lang", AllowedResolutions: []string{"1080p"}, UpgradeTrigger: trigger,
		Ideal: &IdealFile{Language: row}}
}

func onDisk(release string, facts *FileFacts) CurrentFile {
	return CurrentFile{Release: release, SizeGB: 8, RuntimeMin: 120, Facts: facts, OrigLanguage: "en"}
}

// englishOnly is a probed file whose one track is English.
var englishOnly = &FileFacts{Resolution: "1080p", Codec: "h264", HDR: "SDR", AudioLangs: []string{"eng"}}

// A file known to lack a Want language is upgraded to a release that has it, on the
// preferences key, by the sweep and the import gate alike, in both rankings.
func TestLanguageWantUpgrades(t *testing.T) {
	forEachRanking(t, func(t *testing.T, mode string) {
		s, ctx := testService(t)
		setRanking(t, s, mode)
		ref := upgradeProfile(t, s, langStored(map[string]string{"de": PrefWant}, TriggerFormat))
		cur := onDisk("Film.2021.1080p.BluRay.x264-ENG", englishOnly)
		german := enCand("Film.2021.German.DL.1080p.WEB-DL.x264-GER", 6)
		pick, key, ok := s.UpgradePick(ctx, ref, cur, []Candidate{german})
		if !ok || pick.Name != german.Name || key != KeyPreferences {
			t.Fatalf("upgrade = %v %s %s, want the German release on preferences", ok, pick.Name, key)
		}
		if !s.IsQualityUpgrade(ctx, ref, german.Name, 6, cur) {
			t.Error("the import gate refused what the sweep grabbed")
		}
		// A file that already has it isn't upgraded for it.
		have := onDisk("Film.2021.German.DL.1080p.BluRay.x264-HAVE", nil)
		if _, _, ok := s.UpgradePick(ctx, ref, have, []Candidate{german}); ok {
			t.Error("upgraded a file that has German for a worse German release")
		}
		// Under "resolution only" a language is no reason to re-download.
		res := upgradeProfile(t, s, langStored(map[string]string{"de": PrefWant}, TriggerResolution))
		if _, _, ok := s.UpgradePick(ctx, res, cur, []Candidate{german}); ok {
			t.Error("a resolution-only profile upgraded for a language")
		}
	})
}

// A file with nothing to read — no analysis and a name that states no language, or only
// untagged tracks — is never judged on a guess: it still fits a Must, isn't upgraded for
// a language under a Must or a Prefer (sweep and import gate alike), meets the target
// otherwise, and gets no badge colour.
func TestUnreadFilesAreLeftAlone(t *testing.T) {
	forEachRanking(t, func(t *testing.T, mode string) {
		s, ctx := testService(t)
		setRanking(t, s, mode)
		german := enCand("Film.2021.German.DL.1080p.WEB-DL.x264-GER", 6)
		for _, row := range []map[string]string{{"de": PrefMust}, {"de": PrefWant}, {LangOriginal: PrefMust, "de": PrefWant}} {
			ref := upgradeProfile(t, s, langStored(row, TriggerAny))
			sp, _ := s.GetStored(ctx, ref)
			for _, cur := range []CurrentFile{
				onDisk("Film.2021.1080p.BluRay.x264-ENG", nil),
				onDisk("Film 2021 1080p BluRay x264", &FileFacts{Resolution: "1080p", Codec: "h264", HDR: "SDR", AudioUntagged: true}),
			} {
				if v := sp.JudgeFile(cur); !v.Eligible || v.Current.langShort {
					t.Errorf("%v %q: eligible %v short %v %q", row, cur.Release, v.Eligible, v.Current.langShort, v.Current.RejectReason)
				}
				if _, key, ok := s.UpgradePick(ctx, ref, cur, []Candidate{german}); ok {
					t.Errorf("%v %q: upgraded for a language nobody read (%s)", row, cur.Release, key)
				}
				if s.IsQualityUpgrade(ctx, ref, german.Name, 6, cur) {
					t.Errorf("%v %q: the import gate took a language upgrade over an unread file", row, cur.Release)
				}
				if f := sp.LanguageFit(cur); f != nil {
					t.Errorf("%v %q: coloured %+v", row, cur.Release, f)
				}
			}
		}
		// An untagged track beside a German one may be English: under "German + Prefer
		// original" the file isn't upgraded for English, and has no colour.
		ref := upgradeProfile(t, s, langStored(map[string]string{"de": PrefMust, LangOriginal: PrefWant}, TriggerAny))
		cur := onDisk("Film.2021.1080p.BluRay.x264-GRP", &FileFacts{Resolution: "1080p", Codec: "h264", HDR: "SDR", AudioLangs: []string{"ger"}, AudioUntagged: true})
		if _, _, ok := s.UpgradePick(ctx, ref, cur, []Candidate{enCand("Film.2021.German.DL.1080p.WEB-DL.x264-DUAL", 6)}); ok {
			t.Error("upgraded for English the untagged track may already be")
		}
	})
}

// A file on disk is judged by its probed tracks, which win over its name: a file named
// GERMAN whose tagged track is Italian lacks German. A probe that says only English can't
// contradict the name, though — "eng" is what tools write when nobody set a language — so
// that file is read as both. An untagged-name file whose tracks include German has it.
func TestLanguageProbedFileWinsOverName(t *testing.T) {
	forEachRanking(t, func(t *testing.T, mode string) {
		s, ctx := testService(t)
		setRanking(t, s, mode)
		// Under v2 not fitting is a reason to replace under every trigger, "resolution only"
		// too; classic ranking needs a trigger that counts the preferences gain — exactly as
		// for a format Must.
		trigger := TriggerResolution
		if mode == RankingV1 {
			trigger = TriggerFormat
		}
		ref := upgradeProfile(t, s, langStored(map[string]string{"de": PrefMust}, trigger))
		sp, _ := s.GetStored(ctx, ref)

		namedOnly := onDisk("Film.2021.GERMAN.1080p.BluRay.x264-GO", &FileFacts{Resolution: "1080p", Codec: "h264", HDR: "SDR", AudioLangs: []string{"ita"}})
		if v := sp.JudgeFile(namedOnly); v.Eligible || v.Current.RejectCode != RejectMissingLanguage {
			t.Errorf("an Italian-only file named GERMAN fits a German Must: %+v", v.Current.RejectReason)
		}
		defaultTag := onDisk("Film.2021.GERMAN.1080p.BluRay.x264-GO", englishOnly)
		if v := sp.JudgeFile(defaultTag); !v.Eligible {
			t.Errorf("an eng-tagged file named GERMAN doesn't fit: %s", v.Current.RejectReason)
		}
		probedDE := onDisk("Film.2021.1080p.BluRay.x264-ENG", &FileFacts{Resolution: "1080p", Codec: "h264", HDR: "SDR", AudioLangs: []string{"eng", "ger"}})
		if v := sp.JudgeFile(probedDE); !v.Eligible {
			t.Errorf("a file with a German track doesn't fit: %s", v.Current.RejectReason)
		}
		german := enCand("Film.2021.German.DL.1080p.WEB-DL.x264-GER", 6)
		pick, key, ok := s.UpgradePick(ctx, ref, namedOnly, []Candidate{german})
		if !ok || pick.Name != german.Name {
			t.Fatalf("a file lacking a Must language wasn't replaced (key %s)", key)
		}
		if _, _, ok := s.UpgradePick(ctx, ref, probedDE, []Candidate{german}); ok {
			t.Error("a fitting file was replaced")
		}
		// The original language comes from the title: without it an untagged-name file
		// with only untagged tracks can't be judged, and isn't refused.
		unknown := CurrentFile{Release: "Film.2021.1080p.BluRay.x264-ENG", SizeGB: 8, RuntimeMin: 120,
			Facts: &FileFacts{Resolution: "1080p", Codec: "h264", HDR: "SDR", AudioUntagged: true}}
		if v := sp.JudgeFile(unknown); !v.Eligible {
			t.Errorf("an unjudgeable file was refused: %s", v.Current.RejectReason)
		}
	})
}

// A file whose only language is avoided leaves the avoided tier for one that isn't, as a
// preferences gain (so "format" and "source" triggers take it, like Avoid Dolby Vision).
func TestLanguageAvoidedFileUpgrades(t *testing.T) {
	forEachRanking(t, func(t *testing.T, mode string) {
		s, ctx := testService(t)
		setRanking(t, s, mode)
		ref := upgradeProfile(t, s, langStored(map[string]string{"fr": PrefAvoid}, TriggerFormat))
		cur := onDisk("Film.2021.FRENCH.1080p.BluRay.x264-FR", nil)
		eng := enCand("Film.2021.1080p.BluRay.x264-ENG", 8)
		_, key, ok := s.UpgradePick(ctx, ref, cur, []Candidate{eng})
		if !ok || key != KeyPreferences {
			t.Errorf("leaving an avoided language: %v %s", ok, key)
		}
		// Never into one.
		fine := onDisk("Film.2021.1080p.BluRay.x264-ENG", nil)
		if _, _, ok := s.UpgradePick(ctx, ref, fine, []Candidate{enCand("Film.2021.FRENCH.1080p.BluRay.REMUX.AVC-FR", 30)}); ok {
			t.Error("upgraded into an avoided language")
		}
	})
}

// The target isn't met while a preferred language is missing, so upgrading continues.
func TestTargetMetNeedsPreferredLanguage(t *testing.T) {
	sp := StoredProfile{MediaType: MediaMovie, AllowedResolutions: []string{"1080p"},
		Ideal: &IdealFile{Bitrate: map[string]BitrateWindow{"1080p": {Min: 4, Max: 20}}, Language: map[string]string{"de": PrefWant}}}
	normalize(&sp)
	facts := &FileFacts{Resolution: "1080p", Codec: "h264", HDR: "SDR", BitrateMbps: 8, AudioLangs: []string{"eng"}}
	if sp.JudgeFile(onDisk("Film.2021.1080p.BluRay.x264-ENG", facts)).TargetMet {
		t.Error("met the target without the preferred German")
	}
	facts.AudioLangs = []string{"eng", "ger"}
	if !sp.JudgeFile(onDisk("Film.2021.1080p.BluRay.x264-ENG", facts)).TargetMet {
		t.Error("not met with German")
	}
}

// The detail pages' badge colour, from the same reading of the file.
func TestLanguageFitStatuses(t *testing.T) {
	fit := func(row map[string]string, cur CurrentFile) *langs.AudioFit {
		return StoredProfile{MediaType: MediaMovie, Ideal: &IdealFile{Language: row}}.LanguageFit(cur)
	}
	en := &FileFacts{AudioLangs: []string{"eng"}}
	ende := &FileFacts{AudioLangs: []string{"eng", "ger"}}
	fr := &FileFacts{AudioLangs: []string{"fre"}}
	cases := []struct {
		name   string
		row    map[string]string
		facts  *FileFacts
		status string
	}{
		{"has every Must and a Want", map[string]string{"de": PrefMust, LangOriginal: PrefWant}, ende, langs.AudioFitGood},
		{"Must, no Want set", map[string]string{"de": PrefMust}, ende, langs.AudioFitGood},
		{"every Must but no Want", map[string]string{LangOriginal: PrefMust, "de": PrefWant}, en, langs.AudioFitPartial},
		{"lacks a Must", map[string]string{"de": PrefMust}, en, langs.AudioFitBad},
		{"only avoided languages", map[string]string{"fr": PrefAvoid}, fr, langs.AudioFitBad},
		{"an avoided language beside another", map[string]string{"fr": PrefAvoid}, &FileFacts{AudioLangs: []string{"fre", "eng"}}, langs.AudioFitGood},
	}
	for _, tc := range cases {
		got := fit(tc.row, onDisk("Film.2021.1080p.BluRay.x264-GRP", tc.facts))
		if got == nil || got.Status != tc.status || got.Note == "" {
			t.Errorf("%s: %+v, want %s", tc.name, got, tc.status)
		}
	}
	if got := fit(nil, onDisk("Film.2021.1080p.BluRay.x264-GRP", en)); got != nil {
		t.Errorf("no rules, no colour: %+v", got)
	}
	if got := (StoredProfile{MediaType: MediaMovie}).LanguageFit(onDisk("x", en)); got != nil {
		t.Errorf("no target, no colour: %+v", got)
	}
	// Only an "original" rule, for a title whose original language isn't known: no colour.
	cur := onDisk("Film.2021.1080p.BluRay.x264-GRP", en)
	cur.OrigLanguage = ""
	if got := fit(map[string]string{LangOriginal: PrefMust}, cur); got != nil {
		t.Errorf("unresolvable original: %+v", got)
	}
}

// The row is stored with the target, read back tidy, and named in the summary line.
func TestIdealLanguageRoundTripAndSummary(t *testing.T) {
	var f IdealFile
	if err := json.Unmarshal([]byte(`{"language":{"original":"want","GER":"must","fre":"avoid","zz":"must"}}`), &f); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.Language, map[string]string{LangOriginal: PrefWant, "de": PrefMust, "fr": PrefAvoid}) || f.Empty() {
		t.Fatalf("read %v", f.Language)
	}
	sp := StoredProfile{MediaType: MediaMovie, Name: "x", AllowedResolutions: []string{"1080p"}, Ideal: &f}
	normalize(&sp)
	if sp.Ideal == nil || len(sp.ToProfile().Languages) != 3 {
		t.Fatalf("compiled away: %+v", sp.Ideal)
	}
	sum := sp.Summary()
	for _, want := range []string{"German audio required", "original audio preferred", "French audio avoided"} {
		if !strings.Contains(sum, want) {
			t.Errorf("summary %q lacks %q", sum, want)
		}
	}
	// Book and music profiles never apply it.
	book := StoredProfile{MediaType: MediaBook, Ideal: &IdealFile{Language: map[string]string{"de": PrefMust}}}
	if book.ToProfile().Languages != nil {
		t.Error("a book profile carries language rules")
	}
	// Saved and read back through the repository.
	s, ctx := testService(t)
	saved, err := s.Create(ctx, StoredProfile{MediaType: MediaMovie, Name: "Deutsch", Ideal: &IdealFile{Language: map[string]string{"de": PrefMust}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetStored(ctx, "custom:"+strconv.FormatInt(saved.ID, 10))
	if err != nil || got.Ideal == nil || got.Ideal.Language["de"] != PrefMust {
		t.Fatalf("read back %+v %v", got.Ideal, err)
	}
	// A profile without one has none after a save, as before.
	plain, _ := s.Create(ctx, StoredProfile{MediaType: MediaMovie, Name: "plain"})
	if got, _ := s.GetStored(ctx, "custom:"+strconv.FormatInt(plain.ID, 10)); got.Ideal != nil && len(got.Ideal.Language) > 0 {
		t.Errorf("plain profile grew language rules: %v", got.Ideal.Language)
	}
	// The "MULTi releases count" switch: off unless set, saved on, saved off again.
	if got.MultiCountsLanguage {
		t.Error("the switch is on for a profile that never set it")
	}
	got.MultiCountsLanguage = true
	if err := s.Update(ctx, saved.ID, got); err != nil {
		t.Fatal(err)
	}
	ref := "custom:" + strconv.FormatInt(saved.ID, 10)
	if back, _ := s.GetStored(ctx, ref); !back.MultiCountsLanguage {
		t.Error("the switch didn't save")
	}
	if p, _ := s.Resolve(ctx, ref); !p.MultiCountsLanguage {
		t.Error("the engine's profile doesn't carry the switch")
	}
	got.MultiCountsLanguage = false
	if err := s.Update(ctx, saved.ID, got); err != nil {
		t.Fatal(err)
	}
	if back, _ := s.GetStored(ctx, ref); back.MultiCountsLanguage {
		t.Error("the switch didn't turn off")
	}
}

// The save dry run counts what a new language Must would make replaceable, reading each
// file the way the sweeps do: its probed tracks, its name when it states languages, and
// never a guess — a file with nothing to read isn't counted.
func TestImpactCountsALanguageMust(t *testing.T) {
	s, ctx := testService(t)
	old := impactProfile(t, s, ctx, nil)
	files := []ImpactFile{
		{Title: "English only", Release: "A.2020.1080p.BluRay.x264-GRP", Bytes: bytesAt(10), RuntimeMin: 120, OrigLanguage: "en", Facts: englishOnly},
		{Title: "Dual audio", Release: "B.2020.German.DL.1080p.BluRay.x264-GRP", Bytes: bytesAt(10), RuntimeMin: 120, OrigLanguage: "en"},
		{Title: "French dub", Release: "C.2020.FRENCH.1080p.BluRay.x264-GRP", Bytes: bytesAt(10), RuntimeMin: 120, OrigLanguage: "en"},
		{Title: "Probed German", Release: "D.2020.1080p.BluRay.x264-GRP", Bytes: bytesAt(10), RuntimeMin: 120, OrigLanguage: "en",
			Facts: &FileFacts{Resolution: "1080p", Codec: "h264", HDR: "SDR", AudioLangs: []string{"en", "de"}}},
		{Title: "Not read", Release: "E.2020.1080p.BluRay.x264-GRP", Bytes: bytesAt(10), RuntimeMin: 120, OrigLanguage: "en"},
	}
	edited := old
	edited.Ideal = &IdealFile{Language: map[string]string{"de": PrefMust}}
	got := s.Impact(ctx, old, edited, files)
	if got.Replace.Files != 2 || got.Replace.Examples[0] != "English only" || got.Replace.Examples[1] != "French dub" {
		t.Errorf("replace = %+v, want the English-only file and the French dub", got.Replace)
	}
}

// Book and music names are titles, not video tags: no languages read, none judged.
func TestNoLanguagesForBookProfiles(t *testing.T) {
	p := Profile{NoUnwanted: true, Languages: map[string]string{"de": PrefMust}}
	ev := NewDefaultEngine().Evaluate(p, NewCandidate("Die Verwandlung German Edition EPUB", 0.01, 5).WithLanguage("de"))
	if !ev.Eligible || ev.AudioLangs != nil || ev.langPrefs != 0 {
		t.Errorf("book release judged on audio languages: %+v", ev)
	}
}

// A converted file's original is read by its name with the title's language, like any
// release: preferring German, its untagged original (an English film) doesn't count as
// German, so a German release is still an upgrade over both.
func TestConvertedOriginalReadWithTheTitleLanguage(t *testing.T) {
	s, ctx := testService(t)
	setRanking(t, s, RankingV2)
	ref := upgradeProfile(t, s, langStored(map[string]string{"de": PrefWant}, TriggerFormat))
	cur := onDisk("Film.2021.1080p.BluRay.x265-GRP", englishOnly)
	cur.OrigRelease, cur.OrigSizeGB = "Film.2021.1080p.BluRay.x264-GRP", 14
	german := enCand("Film.2021.German.DL.1080p.BluRay.x264-GER", 12)
	if _, key, ok := s.UpgradePick(ctx, ref, cur, []Candidate{german}); !ok || key != KeyPreferences {
		t.Errorf("upgrade over a converted English file = %v %s, want the German release on preferences", ok, key)
	}
}

// German TV's dub names carry German under a German Must, however they're written; an
// episode title with a language word in it doesn't.
func TestGermanTVNamesUnderAGermanMust(t *testing.T) {
	forEachRanking(t, func(t *testing.T, mode string) {
		p := Profile{AllowedResolutions: []parser.Resolution{parser.Res720p, parser.Res1080p}, Languages: map[string]string{"de": PrefMust}}
		for _, n := range []string{
			"Show.S01E01.German.720p.WEB.h264-GRP",
			"Show.S01E01.German.Dubbed.720p.WEB.h264-GRP",
			"Show.S01E01.German.DL.720p.WEB.h264-GRP",
			"Show.S01E01.GERMAN.720p.WEB.h264-GRP",
			"Show.S01.German.720p.WEB.h264-GRP",
			"Show.S01E01.German.AC3.720p.WEB.h264-GRP",
		} {
			if ev := engineIn(mode).Evaluate(p, NewCandidate(n, 1, 50).WithLanguage("en")); !ev.Eligible {
				t.Errorf("%s refused: %q", n, ev.RejectReason)
			}
		}
		if ev := engineIn(mode).Evaluate(p, NewCandidate("Show.S01E01.Going.Dutch.720p.WEB.h264-GRP", 1, 50).WithLanguage("en")); ev.Eligible {
			t.Error("an episode titled Going Dutch read as a dub")
		}
	})
}

// Norwegian's written forms are one language: a Bokmål or Nynorsk track meets a Norwegian
// title's "Original language" Must, and a nb/nn rule matches a "nor" track.
func TestNorwegianFolds(t *testing.T) {
	sp := StoredProfile{MediaType: MediaMovie, Ideal: &IdealFile{Language: map[string]string{LangOriginal: PrefMust}}}
	for _, tag := range []string{"nor", "nob", "no", "nb", "nno"} {
		cur := CurrentFile{Release: "Film.2021.1080p.BluRay.x264-GRP", SizeGB: 10, OrigLanguage: "no",
			Facts: &FileFacts{Resolution: "1080p", Codec: "h264", AudioLangs: []string{tag}}}
		if v := sp.JudgeFile(cur); !v.Eligible {
			t.Errorf("track %s: %q", tag, v.Current.RejectReason)
		}
		if f := sp.LanguageFit(cur); f == nil || f.Status != langs.AudioFitGood {
			t.Errorf("track %s: fit %+v", tag, f)
		}
	}
	if row := normLanguageRow(map[string]string{"nb": PrefMust}); row["no"] != PrefMust {
		t.Errorf("a Bokmål rule: %v", row)
	}
}
