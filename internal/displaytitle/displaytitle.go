// Package displaytitle decides which of a film's or show's names the app SHOWS: TMDB's
// English title (the default, and the title everything else is keyed on), its original
// title in its own language and script, or its title in the household's language — set
// once in Settings → Library → "Show titles as", with a per-title override on the
// movie and series pages.
//
// The shown title is presentation only. Matching (parser.TitleKey, FitRelease,
// MatchRelease, the importer), file and folder naming and TMDB ids all keep using the
// canonical Title; nothing in this package is ever read by them. Everything here works
// from names already stored with the title (Names, kept in its extra metadata by the
// display-names backfill and by every add and refresh), so showing a page never asks
// TMDB anything.
package displaytitle

import (
	"strings"

	"github.com/tristenlammi/arrmada/internal/langs"
	"github.com/tristenlammi/arrmada/internal/metadata"
)

// Mode is the base rule for every title.
type Mode string

// The modes. English is the default and today's behaviour.
const (
	// ModeEnglish shows TMDB's English title.
	ModeEnglish Mode = "english"
	// ModeOriginal shows the title in the work's original language and script (Ο
	// Θίασος, 千と千尋の神隠し), falling back to the English title when TMDB has none.
	ModeOriginal Mode = "original"
	// ModeLanguage shows the title in one chosen language: the original title when the
	// work is in that language, else its translation, else the original title (as TMDB
	// itself falls back), else the English title.
	ModeLanguage Mode = "language"
)

// MaxOverride is the longest per-title override accepted, in characters.
const MaxOverride = 200

// Policy is the household's setting: the base Mode plus the original languages whose
// works always show their original title whatever the mode ("Greek and Italian films
// in Greek and Italian, everything else in English").
type Policy struct {
	Mode Mode `json:"mode"`
	// Language is the chosen language's canonical code (ModeLanguage only).
	Language string `json:"language,omitempty"`
	// OriginalIn are canonical language codes: a work originally in one of them shows its
	// original title.
	OriginalIn []string `json:"original_in"`
}

// ParseMode reads a stored mode; anything unknown — and "" — is English, so a bad or
// missing value never changes what people see.
func ParseMode(raw string) Mode {
	switch m := Mode(strings.ToLower(strings.TrimSpace(raw))); m {
	case ModeOriginal, ModeLanguage:
		return m
	}
	return ModeEnglish
}

// ParsePolicy reads the stored setting: the mode, the chosen language (a canonical code)
// and the comma list of original languages. A language mode without a known language is
// English: there's nothing to translate into.
func ParsePolicy(mode, language, originalIn string) Policy {
	p := Policy{Mode: ParseMode(mode), OriginalIn: []string{}}
	if l, ok := langs.Lookup(language); ok {
		p.Language = l.Code
	}
	if p.Mode == ModeLanguage && p.Language == "" {
		p.Mode = ModeEnglish
	}
	if p.Mode != ModeLanguage {
		p.Language = ""
	}
	seen := map[string]bool{}
	for _, tok := range strings.Split(originalIn, ",") {
		l, ok := langs.Lookup(tok)
		if !ok || seen[l.Code] {
			continue
		}
		seen[l.Code] = true
		p.OriginalIn = append(p.OriginalIn, l.Code)
	}
	return p
}

// Default reports whether p shows exactly what the app always did: English titles, no
// exceptions. Per-title overrides still apply.
func (p Policy) Default() bool { return p.Mode == ModeEnglish && len(p.OriginalIn) == 0 }

// NeedsOriginal reports whether p ever shows an original title, so the backfill knows
// whether the original titles of every script are worth reading.
func (p Policy) NeedsOriginal() bool { return !p.Default() }

// TranslationLangs is the ISO 639-1 languages whose translated titles p shows (nil when it
// shows none), the languages the display-names backfill reads.
func (p Policy) TranslationLangs() []string {
	if p.Mode != ModeLanguage {
		return nil
	}
	if b := langs.ISO1(p.Language); b != "" {
		return []string{b}
	}
	return nil
}

// Names are the names of one title kept for display, read from TMDB (metadata.Titles) by
// add, refresh and the display-names backfill. Stored in the title's extra metadata as
// "display_names".
type Names struct {
	// Original is TMDB's original title in its own script, unlike the Latin-only original
	// title movies keep for matching.
	Original string `json:"original,omitempty"`
	// OriginalLanguage is TMDB's original_language (ISO 639-1, "el").
	OriginalLanguage string `json:"original_language,omitempty"`
	// Translations are the title's translated names in Langs.
	Translations []metadata.TranslatedTitle `json:"translations,omitempty"`
	// Langs are the ISO 639-1 languages the translations were read for. Read is set once
	// the names were read at all (an empty Langs then means "none asked for").
	Langs []string `json:"langs,omitempty"`
	Read  bool     `json:"read,omitempty"`
}

// FromTitles keeps what display needs from every name TMDB has for a title: the original
// title and language, and the translations in langs (ISO 639-1).
func FromTitles(t *metadata.Titles, langsISO1 []string) *Names {
	n := &Names{Langs: append([]string{}, langsISO1...), Read: true}
	if t == nil {
		return n
	}
	n.Original = strings.TrimSpace(t.OriginalTitle)
	n.OriginalLanguage = strings.ToLower(strings.TrimSpace(t.OriginalLanguage))
	n.Translations = t.InLanguages(langsISO1)
	return n
}

// Covers reports whether n was read for every language p shows translations in. A
// language p no longer asks for needs no new read.
func (n *Names) Covers(p Policy) bool {
	if n == nil || !n.Read {
		return false
	}
	return metadata.CoversLangs(n.Langs, p.TranslationLangs())
}

// Title is everything Resolve looks at for one film or show.
type Title struct {
	// Canonical is the title everything else uses (TMDB's English title).
	Canonical string
	// Override is the per-title "Display title…" pick; "" follows the setting.
	Override string
	// OriginalLanguage is the stored original language when Names doesn't carry one
	// (titles whose display names haven't been read yet).
	OriginalLanguage string
	// OriginalTitle is a stored original title to fall back on when Names hasn't been
	// read (a show's original_name is kept in every script already).
	OriginalTitle string
	Names         *Names
}

// original is t's original title and language, from its display names when read.
func (t Title) original() (title, lang string) {
	title, lang = t.OriginalTitle, t.OriginalLanguage
	if t.Names != nil {
		if s := strings.TrimSpace(t.Names.Original); s != "" {
			title = s
		}
		if l := strings.TrimSpace(t.Names.OriginalLanguage); l != "" {
			lang = l
		}
	}
	return strings.TrimSpace(title), strings.ToLower(strings.TrimSpace(lang))
}

// sameLanguage reports whether a TMDB original language ("el") is the setting's language
// code ("el", or "pt-br" for a Portuguese original): the base languages are compared.
func sameLanguage(tmdbLang, code string) bool {
	if tmdbLang == "" || code == "" {
		return false
	}
	return langs.Base(tmdbLang) == langs.Base(code)
}

// Resolve is the title to show for t under p. It is never empty while t.Canonical isn't.
func (p Policy) Resolve(t Title) string {
	if o := strings.TrimSpace(t.Override); o != "" {
		return o
	}
	canonical := strings.TrimSpace(t.Canonical)
	orig, origLang := t.original()
	or := func(s string) string {
		if s != "" {
			return s
		}
		return canonical
	}
	for _, code := range p.OriginalIn {
		if sameLanguage(origLang, code) {
			return or(orig)
		}
	}
	switch p.Mode {
	case ModeOriginal:
		return or(orig)
	case ModeLanguage:
		// TMDB leaves a translation's title empty in the work's own language: the
		// original title is that language's title.
		if sameLanguage(origLang, p.Language) && orig != "" {
			return orig
		}
		if tr := translation(t.Names, p.Language); tr != "" {
			return tr
		}
		return or(orig)
	}
	return canonical
}

// translation is the stored translated title in a language (a canonical code), preferring
// the code's own region ("pt-br" → Brazil's) and then the first TMDB lists.
func translation(n *Names, code string) string {
	if n == nil {
		return ""
	}
	base := langs.ISO1(code)
	region := ""
	if i := strings.IndexByte(code, '-'); i > 0 {
		region = strings.ToUpper(code[i+1:])
	}
	first := ""
	for _, tr := range n.Translations {
		title := strings.TrimSpace(tr.Title)
		if title == "" || !strings.EqualFold(tr.Lang, base) {
			continue
		}
		if region == "" || strings.EqualFold(tr.Country, region) {
			return title
		}
		if first == "" {
			first = title
		}
	}
	return first
}

// Choice is one name a person may pick as a title's override on its page.
type Choice struct {
	Title string `json:"title"`
	// Kind is where it comes from: "english", "original" or "translation".
	Kind string `json:"kind"`
	// Lang is the translation's language (ISO 639-1), and Country its region.
	Lang    string `json:"lang,omitempty"`
	Country string `json:"country,omitempty"`
}

// Choices lists the names a person may pick for a title: its English title, its original
// title, then every translation in t (TMDB's full list when it could be read, else the
// stored ones), each distinct title once.
func Choices(canonical string, names *Names, all *metadata.Titles) []Choice {
	out := []Choice{}
	seen := map[string]bool{}
	add := func(c Choice) {
		c.Title = strings.TrimSpace(c.Title)
		if c.Title == "" || seen[c.Title] {
			return
		}
		seen[c.Title] = true
		out = append(out, c)
	}
	add(Choice{Title: canonical, Kind: "english"})
	var translations []metadata.TranslatedTitle
	switch {
	case all != nil:
		add(Choice{Title: all.OriginalTitle, Kind: "original", Lang: strings.ToLower(all.OriginalLanguage)})
		translations = all.Translations
	case names != nil:
		add(Choice{Title: names.Original, Kind: "original", Lang: names.OriginalLanguage})
		translations = names.Translations
	}
	for _, tr := range translations {
		add(Choice{Title: tr.Title, Kind: "translation", Lang: tr.Lang, Country: tr.Country})
	}
	return out
}

// SQL is the shown title of a movies or series row in a query, under its table alias
// ("m"): m's display_title, or its title when display_title is empty. For feeds that
// read titles straight from SQL (the calendar, history, the blocklist). Never for
// matching.
func SQL(alias string) string {
	return "COALESCE(NULLIF(" + alias + ".display_title, ''), " + alias + ".title)"
}

// CleanOverride trims an override and reports whether it is acceptable: "" (clear) or
// at most MaxOverride characters with no control characters.
func CleanOverride(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if len([]rune(s)) > MaxOverride {
		return "", false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	return s, true
}
