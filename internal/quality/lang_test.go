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

// Probed tracks win over the name; an untagged track is read the way the name reads, and
// with no audio probed the name stands.
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
		{"no facts", nil, german, []string{"de"}, false, true},
		{"facts without audio", &FileFacts{}, german, []string{"de"}, false, true},
		{"the probe says English only, whatever the name", &FileFacts{AudioLangs: []string{"eng"}}, german, []string{"en"}, false, true},
		{"two tagged tracks", &FileFacts{AudioLangs: []string{"ger", "eng"}}, untagged, []string{"de", "en"}, false, true},
		{"a tagged track and an untagged one read as the name", &FileFacts{AudioLangs: []string{"ger"}, AudioUntagged: true}, untagged, []string{"de", "en"}, false, true},
		{"only untagged tracks: the name speaks", &FileFacts{AudioUntagged: true}, german, []string{"de"}, false, true},
		{"untagged with nothing to read it by", &FileFacts{AudioLangs: []string{"ger"}, AudioUntagged: true}, langSet{}, []string{"de"}, true, true},
		{"nothing at all", &FileFacts{AudioUntagged: true}, langSet{}, nil, true, false},
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
func TestLanguageMustRefusesByName(t *testing.T) {
	forEachRanking(t, func(t *testing.T, mode string) {
		p := langProfile(map[string]string{"de": PrefMust})
		d := engineIn(mode).Decide(p, []Candidate{
			enCand("Film.2021.1080p.BluRay.x264-ENG", 12),
			enCand("Film.2021.German.DL.1080p.WEB-DL.x264-GER", 6),
			enCand("Film.2021.MULTi.1080p.BluRay.x264-MUL", 11), // MULTi alone doesn't say German
			enCand("Film.2021.GERMAN.1080p.WEB-DL.x264-GO", 5),
		})
		if len(d.Eligible) != 2 {
			t.Fatalf("eligible %d, want the two German releases", len(d.Eligible))
		}
		for _, ev := range d.Rejected {
			if ev.RejectCode != RejectMissingLanguage || !strings.HasPrefix(ev.RejectReason, "Missing German audio") {
				t.Errorf("%s: %s %q", ev.Candidate.Name, ev.RejectCode, ev.RejectReason)
			}
		}
		// German with the original beats German alone on nothing language-wise (both have
		// the Must); the heavier one wins as it would without the rule.
		if winnerName(d) != "Film.2021.German.DL.1080p.WEB-DL.x264-GER" {
			t.Errorf("winner %s", winnerName(d))
		}
	})
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

// A file missing a Want language is upgraded to a release that has it, on the preferences
// key, by the sweep and the import gate alike, in both rankings.
func TestLanguageWantUpgrades(t *testing.T) {
	forEachRanking(t, func(t *testing.T, mode string) {
		s, ctx := testService(t)
		setRanking(t, s, mode)
		ref := upgradeProfile(t, s, langStored(map[string]string{"de": PrefWant}, TriggerFormat))
		cur := onDisk("Film.2021.1080p.BluRay.x264-ENG", nil)
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

// A file on disk is judged by its probed tracks, which win over its name: a file named
// GERMAN whose only track is English lacks German; an untagged-name file whose tracks
// include German has it.
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

		namedOnly := onDisk("Film.2021.GERMAN.1080p.BluRay.x264-GO", &FileFacts{Resolution: "1080p", Codec: "h264", HDR: "SDR", AudioLangs: []string{"eng"}})
		if v := sp.JudgeFile(namedOnly); v.Eligible || v.Current.RejectCode != RejectMissingLanguage {
			t.Errorf("an English-only file named GERMAN fits a German Must: %+v", v.Current.RejectReason)
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
}

// The save dry run counts what a new language Must would make replaceable, reading each
// file the way the sweeps do: its probed tracks, its title's original language.
func TestImpactCountsALanguageMust(t *testing.T) {
	s, ctx := testService(t)
	old := impactProfile(t, s, ctx, nil)
	files := []ImpactFile{
		{Title: "English only", Release: "A.2020.1080p.BluRay.x264-GRP", Bytes: bytesAt(10), RuntimeMin: 120, OrigLanguage: "en"},
		{Title: "Dual audio", Release: "B.2020.German.DL.1080p.BluRay.x264-GRP", Bytes: bytesAt(10), RuntimeMin: 120, OrigLanguage: "en"},
		{Title: "German film", Release: "C.2020.1080p.BluRay.x264-GRP", Bytes: bytesAt(10), RuntimeMin: 120, OrigLanguage: "de"},
		{Title: "Probed German", Release: "D.2020.1080p.BluRay.x264-GRP", Bytes: bytesAt(10), RuntimeMin: 120, OrigLanguage: "en",
			Facts: &FileFacts{Resolution: "1080p", Codec: "h264", HDR: "SDR", AudioLangs: []string{"en", "de"}}},
	}
	edited := old
	edited.Ideal = &IdealFile{Language: map[string]string{"de": PrefMust}}
	got := s.Impact(ctx, old, edited, files)
	if got.Replace.Files != 1 || got.Replace.Examples[0] != "English only" {
		t.Errorf("replace = %+v, want only the English-only file", got.Replace)
	}
}
