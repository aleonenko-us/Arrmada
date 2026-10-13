package quality

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Importing custom formats from the TRaSH guides (or a Radarr/Sonarr export of them).
//
// A TRaSH custom format is a list of specifications. Radarr and Sonarr match one by
// grouping its specifications by kind (release title, source, resolution…): within a kind
// that has a required specification, every required one must hold; within a kind with
// none, any one of them must; and every kind must pass. Arrmada's formats are flatter (a
// list of conditions, all or any of which must hold), so each kind becomes one condition:
// the alternatives of a kind fold into one value ("WEB-DL|WEBRip", or patterns joined
// with |), which keeps the meaning exact for nearly every format TRaSH publishes.
//
// What can't be carried over is said, never dropped quietly. Dropping a specification can
// make a format match MORE than it should (a required pattern skipped leaves only "not a
// WEB-DL", which is nearly everything) — a format like that is not imported at all, so a
// big negative score can't land on half the internet. Dropping one alternative of several
// only makes it match less; that format is imported with a warning naming what's missing.

// TrashFormat is one imported custom format and what the import couldn't keep.
type TrashFormat struct {
	Format  CustomFormat `json:"format"`
	TrashID string       `json:"trash_id,omitempty"`
	// SuggestedScore is the guide's default score, for the editor to show. The format is
	// added with no score: the user sets it.
	SuggestedScore *int     `json:"suggested_score,omitempty"`
	Warnings       []string `json:"warnings"`
}

// TrashImport is the result of an import: the formats that can be added, and the formats
// (or input) that couldn't, each with its reason. Nothing is saved.
type TrashImport struct {
	Formats  []TrashFormat `json:"formats"`
	Warnings []string      `json:"warnings"`
}

// maxTrashFormats bounds one paste.
const maxTrashFormats = 200

type trashCF struct {
	TrashID        string         `json:"trash_id"`
	TrashScores    map[string]int `json:"trash_scores"`
	Name           string         `json:"name"`
	Specifications []trashSpec    `json:"specifications"`
}

type trashSpec struct {
	Name           string          `json:"name"`
	Implementation string          `json:"implementation"`
	Negate         bool            `json:"negate"`
	Required       bool            `json:"required"`
	Fields         json.RawMessage `json:"fields"`
}

// value is the specification's "value" field. TRaSH's files write fields as an object
// ({"value": …}); a Radarr/Sonarr API export writes a list ([{"name": "value", "value": …}]).
func (s trashSpec) value() (json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(s.Fields, &obj) == nil {
		v, ok := obj["value"]
		return v, ok
	}
	var list []struct {
		Name  string          `json:"name"`
		Value json.RawMessage `json:"value"`
	}
	if json.Unmarshal(s.Fields, &list) == nil {
		for _, f := range list {
			if f.Name == "value" {
				return f.Value, true
			}
		}
	}
	return nil, false
}

func (s trashSpec) stringValue() (string, bool) {
	raw, ok := s.value()
	if !ok {
		return "", false
	}
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return "", false
	}
	return v, true
}

func (s trashSpec) intValue() (int, bool) {
	raw, ok := s.value()
	if !ok {
		return 0, false
	}
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return n, true
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(str)); err == nil {
			return n, true
		}
	}
	return 0, false
}

// label names a specification in a warning.
func (s trashSpec) label() string {
	if s.Name != "" {
		return fmt.Sprintf("%q", s.Name)
	}
	return s.Implementation
}

// ImportTrashCF converts TRaSH custom-format JSON — one format, or a list of them — into
// Arrmada custom formats. media picks which app's numbering the source values use: TRaSH
// publishes Radarr formats for films and Sonarr formats for series, and the two number
// their sources differently.
func ImportTrashCF(data []byte, media string) (TrashImport, error) {
	cfs, err := parseTrash(data)
	if err != nil {
		return TrashImport{}, err
	}
	out := TrashImport{Formats: []TrashFormat{}, Warnings: []string{}}
	if len(cfs) > maxTrashFormats {
		out.Warnings = append(out.Warnings, fmt.Sprintf("Only the first %d of %d formats were read.", maxTrashFormats, len(cfs)))
		cfs = cfs[:maxTrashFormats]
	}
	builtIn := map[string]bool{}
	for _, f := range DefaultFormats() {
		builtIn[strings.ToLower(f.Name)] = true
	}
	taken := map[string]bool{}
	for _, cf := range cfs {
		tf, why := convertTrash(cf, media)
		name := tf.Format.Name
		if why != "" {
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s wasn't imported: %s", quoteName(name), why))
			continue
		}
		if builtIn[strings.ToLower(name)] {
			renamed := name + " (TRaSH)"
			tf.Warnings = append(tf.Warnings, fmt.Sprintf("Renamed to %q: %s is one of Arrmada's built-in formats.", renamed, name))
			tf.Format.Name = renamed
		}
		if taken[strings.ToLower(tf.Format.Name)] {
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s appears twice; only the first was imported.", quoteName(tf.Format.Name)))
			continue
		}
		if problems := ValidateFormats([]CustomFormat{tf.Format}); len(problems) > 0 {
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s wasn't imported: %s", quoteName(tf.Format.Name), problems[0].Message))
			continue
		}
		taken[strings.ToLower(tf.Format.Name)] = true
		out.Formats = append(out.Formats, tf)
	}
	return out, nil
}

func quoteName(n string) string {
	if n == "" {
		return "A format with no name"
	}
	return fmt.Sprintf("%q", n)
}

// parseTrash reads one format or a list of them.
func parseTrash(data []byte) ([]trashCF, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, errors.New("nothing was pasted")
	}
	var cfs []trashCF
	if data[0] == '[' {
		if err := json.Unmarshal(data, &cfs); err != nil {
			return nil, fmt.Errorf("that isn't custom-format JSON: %v", err)
		}
	} else {
		var one trashCF
		if err := json.Unmarshal(data, &one); err != nil {
			return nil, fmt.Errorf("that isn't custom-format JSON: %v", err)
		}
		cfs = []trashCF{one}
	}
	for _, cf := range cfs {
		if cf.Specifications == nil {
			return nil, errors.New("that isn't custom-format JSON: a format has no \"specifications\" list")
		}
	}
	return cfs, nil
}

// converted is one specification turned into a condition, or the reason it couldn't be.
type converted struct {
	spec trashSpec
	cond *Condition
	why  string   // unsupported: why
	note []string // supported, with a caveat worth showing
}

// convertTrash turns one TRaSH format into an Arrmada format. A non-empty second result
// is why the format can't be imported at all.
func convertTrash(cf trashCF, media string) (TrashFormat, string) {
	name := strings.TrimSpace(cf.Name)
	if len(name) > maxFormatName {
		name = strings.TrimSpace(name[:maxFormatName])
	}
	tf := TrashFormat{Format: CustomFormat{Name: name}, TrashID: cf.TrashID, Warnings: []string{}}
	if s, ok := cf.TrashScores["default"]; ok {
		tf.SuggestedScore = &s
	}
	if name == "" {
		return tf, "it has no name"
	}
	if len(cf.Specifications) == 0 {
		return tf, "it has no specifications"
	}

	// Group by kind, in the order kinds first appear.
	var kinds []string
	groups := map[string][]converted{}
	for _, s := range cf.Specifications {
		c := convertSpec(s, media)
		if _, seen := groups[s.Implementation]; !seen {
			kinds = append(kinds, s.Implementation)
		}
		groups[s.Implementation] = append(groups[s.Implementation], c)
	}

	var all []Condition  // conditions that must each hold
	var lone []converted // the alternatives of the single kind that couldn't be folded
	var unfoldable int   // kinds whose alternatives couldn't be folded into one condition
	for _, kind := range kinds {
		g := groups[kind]
		var required []converted
		for _, c := range g {
			if c.spec.Required {
				required = append(required, c)
			}
		}
		if len(required) > 0 {
			// A kind with required specifications passes on those alone: its optional
			// ones can't change the outcome, so they're not carried over.
			for _, c := range required {
				if c.cond == nil && c.spec.Negate {
					// An exclusion that can't be checked: the rest still says what the
					// format is about, but it now matches more, which a negative score
					// needs to know.
					tf.Warnings = append(tf.Warnings, fmt.Sprintf("Matches MORE than the original: the exclusion %s %s, so it was left out — check before giving this format a negative score.", c.spec.label(), c.why))
					continue
				}
				if c.cond == nil {
					return tf, fmt.Sprintf("its required specification %s %s — without it the format would match far more than it should", c.spec.label(), c.why)
				}
				all = append(all, *c.cond)
				tf.Warnings = append(tf.Warnings, c.note...)
			}
			continue
		}
		var ok []converted
		var skipped []string
		for _, c := range g {
			if c.cond == nil {
				skipped = append(skipped, fmt.Sprintf("%s (%s)", c.spec.label(), c.why))
				continue
			}
			ok = append(ok, c)
			tf.Warnings = append(tf.Warnings, c.note...)
		}
		if len(ok) == 0 {
			return tf, fmt.Sprintf("none of its %s alternatives can be used: %s", kindNoun(kind), strings.Join(skipped, "; "))
		}
		if len(skipped) > 0 {
			tf.Warnings = append(tf.Warnings, fmt.Sprintf("Matches less than the original: skipped %s.", strings.Join(skipped, "; ")))
		}
		if folded, fine := fold(ok); fine {
			all = append(all, folded)
			continue
		}
		unfoldable++
		lone = ok
	}

	switch {
	case unfoldable == 0:
		tf.Format.Conditions = all
	case unfoldable == 1 && len(all) == 0:
		// The whole format is one set of alternatives: "match any" says it exactly.
		tf.Format.Match = MatchAny
		for _, c := range lone {
			tf.Format.Conditions = append(tf.Format.Conditions, *c.cond)
		}
	default:
		return tf, "it mixes negated and plain alternatives alongside other required parts, which an Arrmada format can't express exactly"
	}
	if len(tf.Format.Conditions) == 0 {
		return tf, "nothing in it is something Arrmada can check"
	}
	if len(tf.Format.Conditions) > maxConditions {
		return tf, fmt.Sprintf("it would need more than %d conditions", maxConditions)
	}
	return tf, ""
}

// fold turns the alternatives of one kind into one condition when that's exact: one
// alternative, or several un-negated ones of the same condition type.
func fold(alts []converted) (Condition, bool) {
	if len(alts) == 1 {
		return *alts[0].cond, true
	}
	first := *alts[0].cond
	var values []string
	for _, a := range alts {
		if a.cond.Negate || a.cond.Type != first.Type || valueless(a.cond.Type) {
			return Condition{}, false
		}
		v := a.cond.Value
		if first.Type == CondReleaseTitle {
			v = "(?:" + v + ")"
		}
		values = append(values, v)
	}
	return Condition{Type: first.Type, Value: strings.Join(values, "|")}, true
}

// kindNoun names a specification kind inside a sentence.
func kindNoun(kind string) string {
	switch kind {
	case "ReleaseTitleSpecification":
		return "release-title"
	case "ReleaseGroupSpecification":
		return "release-group"
	case "SourceSpecification":
		return "source"
	case "ResolutionSpecification":
		return "resolution"
	}
	return kind
}

// convertSpec maps one specification onto a condition.
func convertSpec(s trashSpec, media string) converted {
	c := converted{spec: s}
	set := func(t ConditionType, v string) {
		c.cond = &Condition{Type: t, Value: v, Negate: s.Negate}
	}
	switch s.Implementation {
	case "ReleaseTitleSpecification":
		p, ok := s.stringValue()
		if !ok || strings.TrimSpace(p) == "" {
			c.why = "has no pattern"
			return c
		}
		if why := patternProblem(p); why != "" {
			c.why = why
			return c
		}
		set(CondReleaseTitle, p)
	case "ReleaseGroupSpecification":
		p, ok := s.stringValue()
		if !ok || strings.TrimSpace(p) == "" {
			c.why = "has no pattern"
			return c
		}
		if names, ok := literalNames(p); ok {
			set(CondReleaseGroup, strings.Join(names, "|"))
			return c
		}
		// Not a plain list of names: match the pattern as a whole word anywhere in the
		// release name, which is close but not the same as matching the group alone.
		wide := `\b(?:` + stripAnchors(p) + `)\b`
		if why := patternProblem(wide); why != "" {
			c.why = why
			return c
		}
		set(CondReleaseTitle, wide)
		c.note = append(c.note, fmt.Sprintf("The release-group pattern %s is matched against the whole release name, not just the group.", s.label()))
	case "EditionSpecification":
		p, ok := s.stringValue()
		if !ok || strings.TrimSpace(p) == "" {
			c.why = "has no pattern"
			return c
		}
		if why := patternProblem(p); why != "" {
			c.why = why
			return c
		}
		set(CondReleaseTitle, p)
		c.note = append(c.note, fmt.Sprintf("The edition pattern %s is matched against the whole release name.", s.label()))
	case "SourceSpecification":
		n, ok := s.intValue()
		if !ok {
			c.why = "has no source value"
			return c
		}
		v, known := trashSource(media, n)
		if !known {
			c.why = fmt.Sprintf("uses source %d, which Arrmada doesn't read from release names", n)
			return c
		}
		set(CondSource, v)
	case "ResolutionSpecification":
		n, ok := s.intValue()
		if !ok {
			c.why = "has no resolution value"
			return c
		}
		switch n {
		case 480, 576, 720, 1080, 2160:
			set(CondResolution, strconv.Itoa(n)+"p")
		default:
			c.why = fmt.Sprintf("uses resolution %d, which Arrmada doesn't grade", n)
		}
	case "QualityModifierSpecification":
		n, _ := s.intValue()
		switch n {
		case 5: // REMUX
			set(CondSource, "Remux")
		case 2: // SCREENER: Arrmada files screeners with cams
			set(CondSource, "CAM")
		case 4: // BRDISK
			c.cond = &Condition{Type: CondFullDisc, Negate: s.Negate}
		default:
			c.why = "uses a quality modifier (raw HD or regional) Arrmada doesn't read"
		}
	case "LanguageSpecification":
		n, ok := s.intValue()
		if !ok {
			c.why = "has no language value"
			return c
		}
		code, why := trashLanguage(n)
		if why != "" {
			c.why = why
			return c
		}
		set(CondLanguage, code)
	case "IndexerFlagSpecification":
		c.why = "matches on indexer flags, which Arrmada doesn't receive"
	case "SizeSpecification":
		c.why = "matches on size; use the profile's bitrate window instead"
	case "YearSpecification":
		c.why = "matches on the year, which Arrmada's custom formats don't"
	case "ReleaseTypeSpecification":
		c.why = "matches on episode/season-pack type, which Arrmada's custom formats don't"
	default:
		c.why = fmt.Sprintf("is a %s, which Arrmada doesn't support", s.Implementation)
	}
	return c
}

// patternProblem is why a TRaSH pattern can't run here ("" when it can). The guides'
// patterns are written for .NET, which has lookarounds and backtracking that Go's RE2
// engine deliberately doesn't.
func patternProblem(p string) string {
	if _, err := compileRegex(p); err != nil {
		for _, look := range []string{"(?=", "(?!", "(?<=", "(?<!"} {
			if strings.Contains(p, look) {
				return "uses a lookaround ((?= (?! (?<= (?<!), which Arrmada's regex engine can't run"
			}
		}
		if strings.Contains(p, "(?>") {
			return "uses an atomic group ((?>), which Arrmada's regex engine can't run"
		}
		return "has a pattern Arrmada's regex engine can't run: " + strings.TrimPrefix(err.Error(), "error parsing regexp: ")
	}
	return ""
}

// stripAnchors removes a pattern's outer ^ and $.
func stripAnchors(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "^")
	if strings.HasSuffix(p, "$") && !strings.HasSuffix(p, `\$`) {
		p = strings.TrimSuffix(p, "$")
	}
	return p
}

var literalName = regexp.MustCompile(`^[A-Za-z0-9 ._-]+$`)

// literalNames reads a release-group pattern that is only a list of names — "^(FLUX)$",
// "^(Tigole|QxR)$", "\b(D-Z0N3)\b" — as those names. ok is false for anything that's
// really a pattern.
func literalNames(p string) ([]string, bool) {
	p = stripAnchors(p)
	p = strings.TrimPrefix(p, `\b`)
	p = strings.TrimSuffix(p, `\b`)
	if strings.HasPrefix(p, "(") && strings.HasSuffix(p, ")") {
		p = strings.TrimPrefix(p[1:len(p)-1], "?:")
	}
	p = strings.NewReplacer(`\.`, ".", `\-`, "-", `\ `, " ", `\_`, "_").Replace(p)
	var out []string
	for _, alt := range strings.Split(p, "|") {
		if !literalName.MatchString(alt) {
			return nil, false
		}
		out = append(out, alt)
	}
	return out, len(out) > 0
}

// trashLanguages maps Radarr/Sonarr language ids (the two share the numbering) onto the
// ISO 639-1 codes the parser reads. Only languages a release name states as a tag.
var trashLanguages = map[int]string{
	2: "fr", 3: "es", 4: "de", 5: "it", 6: "da", 7: "nl", 8: "ja", 10: "zh", 11: "ru", 12: "pl",
	14: "sv", 15: "no", 16: "fi", 17: "tr", 18: "pt", 21: "ko", 26: "hi", 31: "ar", 32: "uk",
	37: "es", // Spanish (Latino)
}

// trashLanguage is the code for a language id, or why it can't be carried over. English,
// "Original", "Any" and "Unknown" need what a name doesn't say: English releases almost
// never state their language, so "not English" would hit nearly every release.
func trashLanguage(n int) (string, string) {
	switch n {
	case 1:
		return "", "matches English, which release names almost never state — Arrmada can't tell an English release from an untagged one"
	case -2:
		return "", "matches the title's original language, which Arrmada's custom formats can't compare against (the Unwanted pack's foreign-only rule does)"
	case -1, 0:
		return "", "matches any or an unknown language, which a release name can't show"
	}
	if code, ok := trashLanguages[n]; ok {
		return code, ""
	}
	return "", fmt.Sprintf("uses language %d, which Arrmada doesn't map", n)
}

// trashSource maps a Radarr (films) or Sonarr (series) source number onto Arrmada's.
func trashSource(media string, n int) (string, bool) {
	if media == MediaSeries {
		switch n {
		case 1, 2: // Television, TelevisionRaw
			return "HDTV", true
		case 3: // Web
			return "WEB-DL", true
		case 4: // WebRip
			return "WEBRip", true
		case 5: // DVD
			return "DVD", true
		case 6: // Bluray
			return "BluRay", true
		case 7: // BlurayRaw (a remux)
			return "Remux", true
		}
		return "", false
	}
	switch n {
	case 1, 2, 3, 4: // CAM, TELESYNC, TELECINE, WORKPRINT
		return "CAM", true
	case 5:
		return "DVD", true
	case 6:
		return "HDTV", true
	case 7:
		return "WEB-DL", true
	case 8:
		return "WEBRip", true
	case 9: // Radarr's Blu-ray source includes remuxes (a remux is Blu-ray + the REMUX modifier)
		return "BluRay|Remux", true
	}
	return "", false
}
