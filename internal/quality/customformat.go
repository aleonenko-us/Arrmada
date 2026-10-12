package quality

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/tristenlammi/arrmada/internal/parser"
)

// CondReleaseTitle matches the whole release name against a regular expression (Go's RE2
// syntax, case-insensitive). It's the escape hatch for anything the parser doesn't read
// into a field: a group spelled several ways, a tag only some releases carry.
const CondReleaseTitle ConditionType = "release_title"

// How a custom format's conditions combine (CustomFormat.Match).
const (
	MatchAll = "all" // every condition must hold — the default
	MatchAny = "any" // one is enough
)

// Limits on what a profile's custom formats may hold. RE2 runs in linear time, so no
// pattern can hang the engine the way a backtracking regex can, but every pattern runs
// against every release of every search: these keep one profile from making that slow.
const (
	maxCustomFormats = 200
	maxConditions    = 32
	maxFormatName    = 80
	maxPatternLen    = 4096
	maxValueLen      = 1000
)

// knownCondition reports whether the engine can evaluate a condition type.
func knownCondition(t ConditionType) bool {
	switch t {
	case CondDynamicRange, CondAudio, CondCodec, CondSource, CondResolution, CondEdition,
		CondReleaseGroup, CondLossless, CondReleaseTitle:
		return true
	}
	return false
}

// valueless reports a condition type that takes no value.
func valueless(t ConditionType) bool { return t == CondLossless }

// --- Aliases ----------------------------------------------------------------

// conditionAliases rewrites the names people (and TRaSH) use for a value to the label the
// parser gives it, keyed by aliasKey. A value may expand to alternatives: plain "WEB" is
// either WEB label, the same single WEB tier the source gates use. The parser's own labels
// are listed too, which is what makes "hevc" or "bluray" match regardless of case.
var conditionAliases = map[ConditionType]map[string]string{
	CondCodec: {
		"x265": "x265", "hevc": "x265", "h265": "x265",
		"x264": "x264", "avc": "x264", "h264": "x264",
		"av1":  "AV1",
		"xvid": "XviD", "divx": "XviD",
		"vc1": "VC-1",
	},
	CondSource: {
		"remux": "Remux", "bdremux": "Remux",
		"bluray": "BluRay", "bd": "BluRay", "bdrip": "BluRay", "brrip": "BluRay",
		"webdl": "WEB-DL", "web": "WEB-DL|WEBRip", "webrip": "WEBRip",
		"hdtv": "HDTV", "tv": "HDTV",
		"dvd": "DVD", "dvdrip": "DVD",
		"cam": "CAM", "telesync": "CAM", "ts": "CAM", "telecine": "CAM", "screener": "CAM",
	},
	CondResolution: {
		"2160p": "2160p", "2160": "2160p", "4k": "2160p", "uhd": "2160p",
		"1080p": "1080p", "1080": "1080p", "1080i": "1080p", "fhd": "1080p",
		"720p": "720p", "720": "720p",
		"576p": "576p", "576": "576p",
		"480p": "480p", "480": "480p", "sd": "480p|576p",
	},
	CondDynamicRange: {
		"dv": "DV", "dolbyvision": "DV", "dovi": "DV",
		"hdr10+": "HDR10+", "hdr10plus": "HDR10+",
		"hdr10": "HDR10", "hdr": "HDR10",
		"hlg": "HLG",
		"sdr": "SDR",
	},
	CondAudio: {
		"atmos": "Atmos", "dolbyatmos": "Atmos",
		"truehd": "TrueHD", "dolbytruehd": "TrueHD",
		"dtshd": "DTS-HD", "dtshdma": "DTS-HD", "dtsma": "DTS-HD", "dts:x": "DTS-HD", "dtsx": "DTS-HD",
		"dts": "DTS",
		"ddp": "DDP", "dd+": "DDP", "eac3": "DDP", "ddplus": "DDP", "dolbydigitalplus": "DDP",
		"dd": "DD", "ac3": "DD", "dolbydigital": "DD",
		"aac":  "AAC",
		"flac": "FLAC",
		"lpcm": "LPCM", "pcm": "LPCM",
	},
}

// aliasKey is the form aliases are looked up in: lowercase, without the spaces, dots,
// dashes and underscores that "Dolby Vision", "H.265" and "Blu-ray" are written with.
func aliasKey(v string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '.', '-', '_':
			return -1
		}
		return r
	}, strings.ToLower(strings.TrimSpace(v)))
}

// normalizeValue rewrites a condition's value into the form the engine compares: aliases
// replaced by the parser's labels, alternatives trimmed, empty and repeated ones dropped.
// A release_title value is a pattern and stays exactly as written.
func normalizeValue(t ConditionType, value string) string {
	if t == CondReleaseTitle {
		return value
	}
	aliases := conditionAliases[t]
	var out []string
	seen := map[string]bool{}
	for _, alt := range strings.Split(value, "|") {
		alt = strings.TrimSpace(alt)
		if alt == "" {
			continue
		}
		expanded := alt
		if canon, ok := aliases[aliasKey(alt)]; ok {
			expanded = canon
		}
		for _, v := range strings.Split(expanded, "|") {
			if k := strings.ToLower(v); !seen[k] {
				seen[k] = true
				out = append(out, v)
			}
		}
	}
	return strings.Join(out, "|")
}

// normalizeFormat is f as the engine compares it: Match spelled MatchAll or MatchAny and
// every value normalised. The conditions are copied, so the caller's format is untouched.
func normalizeFormat(f CustomFormat) CustomFormat {
	if strings.EqualFold(strings.TrimSpace(f.Match), MatchAny) {
		f.Match = MatchAny
	} else {
		f.Match = MatchAll
	}
	conds := make([]Condition, len(f.Conditions))
	for i, c := range f.Conditions {
		c.Value = normalizeValue(c.Type, c.Value)
		conds[i] = c
	}
	f.Conditions = conds
	return f
}

// --- Matching helpers -------------------------------------------------------

// containsFold reports whether xs holds v, ignoring case.
func containsFold(xs []string, v string) bool {
	for _, x := range xs {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

// sameEdition compares two editions however they're written: "Directors.Cut", "director's
// cut" and "Director’s Cut" are one edition, and accents don't count ("Rémastered").
func sameEdition(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	if parser.NormalizeEdition(a) == parser.NormalizeEdition(b) {
		return true
	}
	return editionKey(a) == editionKey(b)
}

var apostrophes = strings.NewReplacer("'", "", "’", "", "`", "")

func editionKey(s string) string {
	return foldSeparators(apostrophes.Replace(strings.ToLower(parser.FoldAccents(s))))
}

// foldSeparators turns the '.', '_' and '-' that separate release-name parts into spaces
// and collapses runs of them, so a term written with spaces matches a name written with
// dots. Token boundaries survive: a space is as much a boundary as the dot it replaced.
func foldSeparators(s string) string {
	b := []byte(s)
	changed := false
	for i, c := range b {
		switch c {
		case '.', '_', '-':
			b[i] = ' '
			changed = true
		}
	}
	if !changed && !strings.Contains(s, "  ") && strings.TrimSpace(s) == s {
		return s
	}
	return strings.Join(strings.Fields(string(b)), " ")
}

// --- Patterns ---------------------------------------------------------------

// compileRegex compiles a release_title pattern the way the engine runs it: case-insensitive,
// like every other comparison in a custom format.
func compileRegex(pattern string) (*regexp.Regexp, error) {
	if len(pattern) > maxPatternLen {
		return nil, fmt.Errorf("the pattern is %d characters long; the limit is %d", len(pattern), maxPatternLen)
	}
	if _, err := regexp.Compile(pattern); err != nil {
		// Compiled without the (?i) prefix first, so the message quotes only what was typed.
		return nil, err
	}
	return regexp.Compile("(?i)" + pattern)
}

// regexCache keeps compiled patterns across engines: a profile resolves into a new engine
// for every title a sweep looks at, and the patterns rarely change. A pattern that doesn't
// compile is cached as nil. Capped, so a stream of edited previews can't grow it forever.
var regexCache = struct {
	sync.Mutex
	m map[string]*regexp.Regexp
}{m: map[string]*regexp.Regexp{}}

const regexCacheMax = 1024

// cachedRegex returns the compiled pattern, or nil when it doesn't compile.
func cachedRegex(pattern string) *regexp.Regexp {
	regexCache.Lock()
	re, ok := regexCache.m[pattern]
	regexCache.Unlock()
	if ok {
		return re
	}
	re, err := compileRegex(pattern)
	if err != nil {
		re = nil
	}
	regexCache.Lock()
	if len(regexCache.m) < regexCacheMax {
		regexCache.m[pattern] = re
	}
	regexCache.Unlock()
	return re
}

// compileFormats compiles every release_title pattern the formats use, for an engine.
func compileFormats(formats map[string]CustomFormat) map[string]*regexp.Regexp {
	var out map[string]*regexp.Regexp
	for _, f := range formats {
		for _, c := range f.Conditions {
			if c.Type != CondReleaseTitle {
				continue
			}
			if out == nil {
				out = map[string]*regexp.Regexp{}
			}
			if _, done := out[c.Value]; !done {
				out[c.Value] = cachedRegex(c.Value)
			}
		}
	}
	return out
}

// --- Validation -------------------------------------------------------------

// FormatProblem is one thing wrong with (or worth knowing about) a custom format, placed
// so the editor can show it next to the row it's about.
type FormatProblem struct {
	Format string `json:"format"`
	// Index is the format's position in the profile's custom formats; Condition the
	// condition's position in it, -1 when the problem is the format as a whole.
	Index     int    `json:"index"`
	Condition int    `json:"condition"`
	Message   string `json:"message"`
}

func (p FormatProblem) Error() string {
	if p.Format == "" {
		return "Custom formats: " + p.Message
	}
	return fmt.Sprintf("Custom format %q: %s", p.Format, p.Message)
}

// InvalidProfileError is a save refused because the profile's custom formats don't hold
// together. Its message names every format at fault.
type InvalidProfileError struct{ Problems []FormatProblem }

func (e *InvalidProfileError) Error() string {
	msgs := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		msgs[i] = p.Error()
	}
	return strings.Join(msgs, "; ")
}

// Validate reports what would make the profile's custom formats misbehave: a pattern that
// doesn't compile, an unknown condition type, a missing value, a name that's empty, taken
// twice or taken by a built-in format. Saving refuses a profile with any of these.
func (sp StoredProfile) Validate() []error {
	var out []error
	for _, p := range ValidateFormats(sp.CustomFormats) {
		out = append(out, p)
	}
	return out
}

// ValidateFormats checks a profile's custom formats (see StoredProfile.Validate).
func ValidateFormats(formats []CustomFormat) []FormatProblem {
	var out []FormatProblem
	if len(formats) > maxCustomFormats {
		out = append(out, FormatProblem{Index: -1, Condition: -1, Message: fmt.Sprintf("a profile can hold at most %d custom formats", maxCustomFormats)})
	}
	builtIn := map[string]bool{}
	for _, f := range DefaultFormats() {
		builtIn[strings.ToLower(f.Name)] = true
	}
	seen := map[string]bool{}
	for i, f := range formats {
		add := func(cond int, msg string, args ...any) {
			out = append(out, FormatProblem{Format: f.Name, Index: i, Condition: cond, Message: fmt.Sprintf(msg, args...)})
		}
		name := strings.TrimSpace(f.Name)
		key := strings.ToLower(name)
		switch {
		case name == "":
			add(-1, "format %d has no name — give it one", i+1)
		case len(name) > maxFormatName:
			add(-1, "the name is longer than %d characters", maxFormatName)
		case builtIn[key]:
			add(-1, "%s is a built-in format — give this one another name", name)
		case seen[key]:
			add(-1, "two custom formats have this name — give one of them another")
		}
		seen[key] = true
		if m := strings.ToLower(strings.TrimSpace(f.Match)); m != "" && m != MatchAll && m != MatchAny {
			add(-1, "match must be all or any, not %q", f.Match)
		}
		switch {
		case len(f.Conditions) == 0:
			add(-1, "add at least one condition")
		case len(f.Conditions) > maxConditions:
			add(-1, "a format can have at most %d conditions", maxConditions)
		}
		for j, c := range f.Conditions {
			n := j + 1
			switch {
			case !knownCondition(c.Type):
				add(j, "condition %d has an unknown type %q", n, c.Type)
			case valueless(c.Type):
			case strings.TrimSpace(c.Value) == "":
				add(j, "condition %d needs a value", n)
			case c.Type == CondReleaseTitle:
				if _, err := compileRegex(c.Value); err != nil {
					add(j, "condition %d's release-name pattern isn't valid: %s", n, regexProblem(c.Value, err))
				}
			case len(c.Value) > maxValueLen:
				add(j, "condition %d's value is longer than %d characters", n, maxValueLen)
			}
		}
	}
	return out
}

// regexProblem explains why a pattern was refused, in terms that point at the fix.
func regexProblem(pattern string, err error) string {
	msg := strings.TrimPrefix(err.Error(), "error parsing regexp: ")
	for _, look := range []string{"(?=", "(?!", "(?<=", "(?<!"} {
		if strings.Contains(pattern, look) {
			return msg + " — lookarounds like (?= and (?! aren't supported (Arrmada uses Go's RE2 engine, which never backtracks)"
		}
	}
	return msg
}

// FormatWarnings reports conditions that are valid but can never match: a value the
// parser never produces for that type ("codec = h266"). They don't block a save — a value
// may be ahead of the parser — but the editor shows them.
func FormatWarnings(formats []CustomFormat) []FormatProblem {
	vocab := ConditionVocabulary()
	var out []FormatProblem
	for i, f := range formats {
		for j, c := range f.Conditions {
			known, enumerable := vocab[c.Type]
			if !enumerable || c.Type == CondReleaseGroup || c.Type == CondReleaseTitle {
				continue
			}
			for _, v := range strings.Split(normalizeValue(c.Type, c.Value), "|") {
				if v == "" || inVocabulary(c.Type, known, v) {
					continue
				}
				out = append(out, FormatProblem{Format: f.Name, Index: i, Condition: j,
					Message: fmt.Sprintf("condition %d: %q isn't a %s Arrmada reads from release names, so it never matches", j+1, v, conditionNoun(c.Type))})
			}
		}
	}
	return out
}

func inVocabulary(t ConditionType, known []VocabValue, v string) bool {
	for _, k := range known {
		if strings.EqualFold(k.Value, v) || (t == CondEdition && sameEdition(k.Value, v)) {
			return true
		}
	}
	return false
}

// conditionNoun names a condition type inside a sentence.
func conditionNoun(t ConditionType) string {
	switch t {
	case CondDynamicRange:
		return "dynamic range"
	case CondReleaseGroup:
		return "release group"
	case CondReleaseTitle:
		return "release name"
	}
	return string(t)
}

// FormatReport is the editor's live read on a profile's custom formats: what would block a
// save, what can never match, and how many of the sample releases each format matches.
type FormatReport struct {
	Errors   []FormatProblem `json:"errors"`
	Warnings []FormatProblem `json:"warnings"`
	// Matches is, per format (by position), how many of the Samples releases it matches.
	Matches []int `json:"matches"`
	Samples int   `json:"samples"`
}

// CheckFormats validates formats and counts their matches over the sample releases.
func CheckFormats(formats []CustomFormat) FormatReport {
	samples := SampleCandidates()
	out := FormatReport{
		Errors:   ValidateFormats(formats),
		Warnings: FormatWarnings(formats),
		Matches:  make([]int, len(formats)),
		Samples:  len(samples),
	}
	if out.Errors == nil {
		out.Errors = []FormatProblem{}
	}
	if out.Warnings == nil {
		out.Warnings = []FormatProblem{}
	}
	for i, f := range formats {
		nf := normalizeFormat(f)
		for _, c := range samples {
			if nf.holds(c.Name, c.Release, nil) {
				out.Matches[i]++
			}
		}
	}
	return out
}
