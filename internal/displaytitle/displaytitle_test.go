package displaytitle

import (
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

// Names TMDB has for the titles the tests use.
var (
	// The Travelling Players (1975): Greek, no Italian translation.
	thiasos = Title{Canonical: "The Travelling Players", Names: &Names{
		Original: "Ο Θίασος", OriginalLanguage: "el", Read: true,
		Translations: []metadata.TranslatedTitle{{Lang: "de", Country: "DE", Title: "Die Wanderschauspieler"}},
	}}
	// The Great Beauty (2013): Italian.
	bellezza = Title{Canonical: "The Great Beauty", Names: &Names{
		Original: "La grande bellezza", OriginalLanguage: "it", Read: true,
		Translations: []metadata.TranslatedTitle{{Lang: "el", Country: "GR", Title: "Η Τέλεια Ομορφιά"}},
	}}
	// Spirited Away (2001): Japanese, with an Italian and two Portuguese translations.
	spirited = Title{Canonical: "Spirited Away", Names: &Names{
		Original: "千と千尋の神隠し", OriginalLanguage: "ja", Read: true,
		Translations: []metadata.TranslatedTitle{
			{Lang: "it", Country: "IT", Title: "La città incantata"},
			{Lang: "pt", Country: "PT", Title: "A Viagem de Chihiro"},
			{Lang: "pt", Country: "BR", Title: "A Viagem de Chihiro (BR)"},
		},
	}}
	// Dune (2021): English.
	dune = Title{Canonical: "Dune", Names: &Names{Original: "Dune", OriginalLanguage: "en", Read: true,
		Translations: []metadata.TranslatedTitle{{Lang: "it", Country: "IT", Title: "Dune"}}}}
)

func TestResolveModes(t *testing.T) {
	english := ParsePolicy("", "", "")
	original := ParsePolicy("original", "", "")
	italian := ParsePolicy("language", "it", "")
	reporter := ParsePolicy("english", "", "el,it") // Greek and Italian originals, the rest English
	cases := []struct {
		name string
		p    Policy
		t    Title
		want string
	}{
		{"english is the default", english, thiasos, "The Travelling Players"},
		{"english, Japanese", english, spirited, "Spirited Away"},
		{"original, Greek script", original, thiasos, "Ο Θίασος"},
		{"original, Japanese script", original, spirited, "千と千尋の神隠し"},
		{"original, English", original, dune, "Dune"},
		{"Italian: a translation", italian, spirited, "La città incantata"},
		{"Italian: an Italian film is its original", italian, bellezza, "La grande bellezza"},
		{"Italian: no Italian translation falls back to the original", italian, thiasos, "Ο Θίασος"},
		{"Italian: English film", italian, dune, "Dune"},
		{"originals for el/it: Greek", reporter, thiasos, "Ο Θίασος"},
		{"originals for el/it: Italian", reporter, bellezza, "La grande bellezza"},
		{"originals for el/it: the rest stays English", reporter, spirited, "Spirited Away"},
		{"originals for el/it: English", reporter, dune, "Dune"},
	}
	for _, c := range cases {
		if got := c.p.Resolve(c.t); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// A language mode with originals: the per-language originals win over the translation.
func TestResolveLanguageWithOriginals(t *testing.T) {
	p := ParsePolicy("language", "el", "it")
	if got := p.Resolve(bellezza); got != "La grande bellezza" {
		t.Errorf("Italian original in a Greek household that wants Italian originals: %q", got)
	}
	if got := p.Resolve(thiasos); got != "Ο Θίασος" {
		t.Errorf("Greek film in Greek: %q", got)
	}
	if got := ParsePolicy("language", "el", "").Resolve(bellezza); got != "Η Τέλεια Ομορφιά" {
		t.Errorf("Greek translation: %q", got)
	}
}

func TestResolveFallbacks(t *testing.T) {
	original := ParsePolicy("original", "", "")
	// Nothing read yet: the canonical title.
	if got := original.Resolve(Title{Canonical: "Heat"}); got != "Heat" {
		t.Errorf("no names: %q", got)
	}
	// A show's stored original title (already in its own script) shows before names are read.
	show := Title{Canonical: "Frieren: Beyond Journey's End", OriginalTitle: "葬送のフリーレン", OriginalLanguage: "ja"}
	if got := original.Resolve(show); got != "葬送のフリーレン" {
		t.Errorf("stored original: %q", got)
	}
	if got := ParsePolicy("", "", "ja").Resolve(show); got != "葬送のフリーレン" {
		t.Errorf("stored original language: %q", got)
	}
	// TMDB gave no original title: English, never empty.
	blank := Title{Canonical: "Heat", Names: &Names{OriginalLanguage: "el", Read: true}}
	if got := ParsePolicy("", "", "el").Resolve(blank); got != "Heat" {
		t.Errorf("no original title: %q", got)
	}
	if got := ParsePolicy("language", "it", "").Resolve(blank); got != "Heat" {
		t.Errorf("no translation and no original: %q", got)
	}
	// A regional pick prefers its own region's translation, else the first in the language.
	if got := ParsePolicy("language", "pt-br", "").Resolve(spirited); got != "A Viagem de Chihiro (BR)" {
		t.Errorf("pt-br: %q", got)
	}
	if got := ParsePolicy("language", "pt", "").Resolve(spirited); got != "A Viagem de Chihiro" {
		t.Errorf("pt: %q", got)
	}
}

// The override beats every mode; cleared, the setting rules again.
func TestResolveOverride(t *testing.T) {
	over := thiasos
	over.Override = "  Thiasos  "
	for _, p := range []Policy{ParsePolicy("", "", ""), ParsePolicy("original", "", ""), ParsePolicy("language", "it", "el")} {
		if got := p.Resolve(over); got != "Thiasos" {
			t.Errorf("%+v: %q, want the override", p, got)
		}
	}
	over.Override = ""
	if got := ParsePolicy("original", "", "").Resolve(over); got != "Ο Θίασος" {
		t.Errorf("cleared: %q", got)
	}
}

func TestParsePolicy(t *testing.T) {
	p := ParsePolicy("LANGUAGE", "Italian", "el, it,xx,el")
	if p.Mode != ModeLanguage || p.Language != "it" || strings.Join(p.OriginalIn, ",") != "el,it" {
		t.Errorf("parsed %+v", p)
	}
	if p := ParsePolicy("language", "", ""); p.Mode != ModeEnglish {
		t.Errorf("a language mode without a language is English: %+v", p)
	}
	if p := ParsePolicy("bogus", "it", ""); p.Mode != ModeEnglish || p.Language != "" {
		t.Errorf("unknown mode: %+v", p)
	}
	if !ParsePolicy("", "", "").Default() || ParsePolicy("", "", "el").Default() {
		t.Error("Default is English with no exceptions only")
	}
	if got := ParsePolicy("language", "pt-br", "").TranslationLangs(); len(got) != 1 || got[0] != "pt" {
		t.Errorf("translations read for pt-br: %v", got)
	}
	if got := ParsePolicy("original", "", "").TranslationLangs(); got != nil {
		t.Errorf("original reads no translations: %v", got)
	}
}

// Covers: read names serve a setting until it asks for a language they weren't read for.
func TestNamesCover(t *testing.T) {
	var none *Names
	if none.Covers(ParsePolicy("original", "", "")) {
		t.Error("nil names cover nothing")
	}
	n := FromTitles(&metadata.Titles{OriginalTitle: "Ο Θίασος", OriginalLanguage: "EL",
		Translations: []metadata.TranslatedTitle{{Lang: "it", Title: "La recita"}, {Lang: "de", Title: "Die Wanderschauspieler"}}}, []string{"it"})
	if n.Original != "Ο Θίασος" || n.OriginalLanguage != "el" || len(n.Translations) != 1 || n.Translations[0].Title != "La recita" {
		t.Fatalf("FromTitles kept %+v", n)
	}
	if !n.Covers(ParsePolicy("original", "", "")) || !n.Covers(ParsePolicy("language", "it", "")) {
		t.Error("read for it: covers original and Italian")
	}
	if n.Covers(ParsePolicy("language", "de", "")) {
		t.Error("read for it: German needs a new read")
	}
	if !FromTitles(nil, nil).Read {
		t.Error("a title TMDB no longer has is still marked read")
	}
}

func TestChoices(t *testing.T) {
	all := &metadata.Titles{Title: "The Travelling Players", OriginalTitle: "Ο Θίασος", OriginalLanguage: "el",
		Translations: []metadata.TranslatedTitle{{Lang: "de", Title: "Die Wanderschauspieler"}, {Lang: "en", Title: "The Travelling Players"}}}
	got := Choices("The Travelling Players", nil, all)
	if len(got) != 3 || got[0].Kind != "english" || got[1].Title != "Ο Θίασος" || got[1].Lang != "el" || got[2].Lang != "de" {
		t.Errorf("choices = %+v", got)
	}
	// TMDB unreachable: the stored names.
	got = Choices("The Travelling Players", thiasos.Names, nil)
	if len(got) != 3 || got[1].Kind != "original" {
		t.Errorf("stored choices = %+v", got)
	}
}

func TestCleanOverride(t *testing.T) {
	if s, ok := CleanOverride("  Ο Θίασος "); !ok || s != "Ο Θίασος" {
		t.Errorf("trim: %q %v", s, ok)
	}
	if s, ok := CleanOverride(""); !ok || s != "" {
		t.Error("empty clears")
	}
	if _, ok := CleanOverride("a\nb"); ok {
		t.Error("a newline is refused")
	}
	if _, ok := CleanOverride(strings.Repeat("Θ", MaxOverride+1)); ok {
		t.Error("too long is refused")
	}
	if _, ok := CleanOverride(strings.Repeat("Θ", MaxOverride)); !ok {
		t.Error("the limit counts characters, not bytes")
	}
}

func TestSQL(t *testing.T) {
	if got := SQL("m"); got != "COALESCE(NULLIF(m.display_title, ''), m.title)" {
		t.Errorf("SQL = %s", got)
	}
}
