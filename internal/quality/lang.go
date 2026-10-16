package quality

import (
	"sort"
	"strings"

	"github.com/tristenlammi/arrmada/internal/langs"
	"github.com/tristenlammi/arrmada/internal/parser"
)

// Audio-language rules: the target's "Audio language" row (IdealFile.Language). Like the
// other rows each entry is Must, Prefer (stored "want") or Avoid, keyed by a base ISO 639-1
// code or LangOriginal, which stands for the title's own original language (TMDB's
// original_language) — so "Original + German" is one profile for every title.
//
//   - Must: a release whose name doesn't carry the language is refused; a file on disk
//     without it no longer fits (it is judged by its probed tracks, which win over its
//     name). Each Must is needed — they are languages a file has together, not
//     alternatives like codecs.
//   - Prefer: every preferred (or required) language a release has counts on the
//     preferences key (ranking v2), and scores like a preferred format (classic).
//   - Avoid: a release whose every language is avoided drops to the avoided tier. An extra
//     audio track costs nothing, so a dual-audio release with one avoided language isn't.
//
// No entries is no rules: every release and file is judged exactly as before.
//
// What a release carries, by its name: an untagged release is the original language; a
// MULTi/DUAL/DL one the original plus the languages it names (MULTi alone: the original and
// others it doesn't name); a tagged one ("GERMAN", "iTALiAN") exactly the languages it
// names. A title whose original language isn't known, with a release that names none,
// can't be judged, and no rule is applied to it.

// LangOriginal is the Audio language entry for the title's original language.
const LangOriginal = "original"

// langSet is the audio a release or file carries, as the language rules read it.
type langSet struct {
	codes []string // base ISO 639-1 codes, no duplicates, in the order found
	// open: there may be languages it doesn't name (MULTi with no language, or a file with
	// an untagged track the name can't speak for). Nothing is ever all-avoided while open.
	open bool
	// known: there's something to judge. False for an untagged release of a title whose
	// original language isn't known.
	known bool
	// assumed: the codes came from assuming an untagged release is the original language.
	assumed bool
}

func (s langSet) has(code string) bool { return containsStr(s.codes, code) }

// describe is the set as a release row shows it: its codes, and a note when they're an
// assumption or not the whole story.
func (s langSet) describe() ([]string, string) {
	if !s.known {
		return nil, ""
	}
	switch {
	case s.assumed:
		return s.codes, "not tagged — read as the original language"
	case s.open:
		return s.codes, "and others it doesn't name"
	}
	return s.codes, ""
}

func (s *langSet) add(code string) {
	if code != "" && !s.has(code) {
		s.codes = append(s.codes, code)
	}
}

// baseLang is a tag's base code, "" when the table doesn't know it.
func baseLang(tag string) string { return langs.ISO1(tag) }

// releaseLangs reads the audio languages a release name carries (see the top of the file).
func releaseLangs(r parser.Release, orig string) langSet {
	o := baseLang(orig)
	var s langSet
	switch {
	case len(r.Languages) == 0 && !r.Multi:
		if o == "" {
			return langSet{}
		}
		return langSet{codes: []string{o}, known: true, assumed: true}
	case r.Multi:
		s = langSet{known: true, open: len(r.Languages) == 0}
		s.add(o)
	default:
		s = langSet{known: true}
	}
	for _, l := range r.Languages {
		s.add(baseLang(l))
	}
	return s
}

// fileLangs lays a file's probed audio over what its name says: the probe's tagged tracks
// win; an untagged track is read the way its name reads (an untagged name: the original
// language), and with nothing to read it by the set stays open. No probed audio at all
// leaves the name's reading.
func fileLangs(f *FileFacts, name langSet) langSet {
	if f == nil || (len(f.AudioLangs) == 0 && !f.AudioUntagged) {
		return name
	}
	s := langSet{known: true}
	for _, c := range f.AudioLangs {
		s.add(baseLang(c))
	}
	if f.AudioUntagged {
		if name.known {
			for _, c := range name.codes {
				s.add(c)
			}
			s.open, s.assumed = name.open, name.assumed
		} else {
			s.open = true
			s.known = len(s.codes) > 0
		}
	}
	return s
}

// normLanguageRow tidies a saved Audio language row: base codes (any spelling the language
// table knows, "pt-BR" → "pt"), LangOriginal, and only the three states. Anything else is
// dropped.
func normLanguageRow(row map[string]string) map[string]string {
	if len(row) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, st := range row {
		st = strings.ToLower(strings.TrimSpace(st))
		if st != PrefMust && st != PrefWant && st != PrefAvoid {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(k))
		if key != LangOriginal {
			key = baseLang(key)
		}
		if key != "" {
			out[key] = st
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// langRules is the row for one title: LangOriginal becomes the title's original language
// (and is dropped when that isn't known), and a language's own entry wins over it.
func langRules(row map[string]string, orig string) map[string]string {
	if len(row) == 0 {
		return nil
	}
	out := map[string]string{}
	if st, ok := row[LangOriginal]; ok {
		if o := baseLang(orig); o != "" {
			out[o] = st
		}
	}
	for k, st := range row {
		if k != LangOriginal {
			out[k] = st
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// langVerdict is a language set judged against a title's rules.
type langVerdict struct {
	judged  bool     // there were rules and something to judge them by
	missing []string // the Musts it lacks
	prefs   []string // the preferred or required languages it has
	short   bool     // a Must or a Prefer it lacks: it isn't the target yet
	wants   int      // how many languages the rules prefer
	wanted  int      // how many of those it has
	avoided bool     // every language it carries is avoided
}

func judgeLangs(rules map[string]string, s langSet) langVerdict {
	var v langVerdict
	if len(rules) == 0 || !s.known {
		return v
	}
	v.judged = true
	for _, code := range sortedLangKeys(rules) {
		st, has := rules[code], s.has(code)
		switch st {
		case PrefMust:
			if has {
				v.prefs = append(v.prefs, code)
			} else {
				v.missing = append(v.missing, code)
			}
		case PrefWant:
			v.wants++
			if has {
				v.prefs = append(v.prefs, code)
				v.wanted++
			}
		}
		if (st == PrefMust || st == PrefWant) && !has {
			v.short = true
		}
	}
	if !s.open && len(s.codes) > 0 {
		v.avoided = true
		for _, c := range s.codes {
			if rules[c] != PrefAvoid {
				v.avoided = false
				break
			}
		}
	}
	v.short = v.short || v.avoided
	return v
}

func sortedLangKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// positiveLangRules reports rules that say which languages are wanted (a Must or a Prefer):
// the Unwanted pack's foreign-only item then defers to them.
func positiveLangRules(rules map[string]string) bool {
	for _, st := range rules {
		if st == PrefMust || st == PrefWant {
			return true
		}
	}
	return false
}

// langNames is "German", "German and French": codes named for a sentence.
func langNames(codes []string) string {
	names := make([]string, len(codes))
	for i, c := range codes {
		names[i] = langs.Name(c)
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// langLabel names a language as Matched lists it: "German audio".
func langLabel(code string) string { return langs.Name(code) + " audio" }

// titleLang is the title's original language as the language rules read it.
func (c Candidate) titleLang() string {
	if c.OrigLanguage != "" {
		return c.OrigLanguage
	}
	return c.ruleLang
}

// audioLangs is what the release (or, for a file on disk, the file) carries.
func (c Candidate) audioLangs() langSet {
	s := releaseLangs(c.Release, c.titleLang())
	if c.heard != nil {
		s = fileLangs(c.heard, s)
	}
	return s
}

// languageRules is the profile's language row for this candidate's title.
func (p Profile) languageRules(c Candidate) map[string]string {
	return langRules(p.Languages, c.titleLang())
}

// LanguageFit judges a file's audio languages against the profile's language rules, the
// way the engine reads them (probed tracks over the release name, an untagged one as the
// name reads it): the colour of the detail pages' language badges. nil when the profile
// has no rules for this title, or nothing about the file can be judged.
func (sp StoredProfile) LanguageFit(cur CurrentFile) *langs.AudioFit {
	if sp.Ideal == nil {
		return nil
	}
	rules := langRules(normLanguageRow(sp.Ideal.Language), cur.OrigLanguage)
	if len(rules) == 0 || !cur.known() {
		return nil
	}
	v := judgeLangs(rules, cur.candidate().audioLangs())
	if !v.judged {
		return nil
	}
	var wants []string
	for _, code := range sortedLangKeys(rules) {
		if rules[code] == PrefWant {
			wants = append(wants, code)
		}
	}
	switch {
	case len(v.missing) > 0:
		return &langs.AudioFit{Status: langs.AudioFitBad, Note: "No " + langNames(v.missing) + " audio, which this profile requires"}
	case v.avoided:
		return &langs.AudioFit{Status: langs.AudioFitBad, Note: "Only languages this profile avoids"}
	case v.wants > 0 && v.wanted == 0:
		return &langs.AudioFit{Status: langs.AudioFitPartial, Note: "No " + langNamesOr(wants) + " audio, which this profile prefers"}
	}
	if len(v.prefs) == 0 {
		return &langs.AudioFit{Status: langs.AudioFitGood, Note: "Nothing this profile avoids"}
	}
	return &langs.AudioFit{Status: langs.AudioFitGood, Note: "Has " + langNames(v.prefs) + " audio, as this profile asks"}
}

// langNamesOr is "German or French".
func langNamesOr(codes []string) string {
	s := langNames(codes)
	if i := strings.LastIndex(s, " and "); i >= 0 {
		s = s[:i] + " or " + s[i+len(" and "):]
	}
	return s
}

// languageSummary is the row in the profile's summary line: "German audio required ·
// original audio preferred".
func languageSummary(row map[string]string) []string {
	row = normLanguageRow(row)
	if len(row) == 0 {
		return nil
	}
	name := func(k string) string {
		if k == LangOriginal {
			return "original"
		}
		return langs.Name(k)
	}
	groups := map[string][]string{}
	for _, k := range sortedLangKeys(row) {
		groups[row[k]] = append(groups[row[k]], name(k))
	}
	var out []string
	for _, g := range []struct{ st, word string }{{PrefMust, "required"}, {PrefWant, "preferred"}, {PrefAvoid, "avoided"}} {
		if l := groups[g.st]; len(l) > 0 {
			out = append(out, strings.Join(l, "/")+" audio "+g.word)
		}
	}
	return out
}
