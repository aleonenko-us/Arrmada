package displaytitle

import (
	"testing"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

// The overview rule (GitHub #35): the language mode describes titles in its language when
// TMDB has the overview, else in English; every other mode, and "original titles for
// these languages", keeps English.
func TestOverviewRule(t *testing.T) {
	const english = "A girl wanders into the spirit world."
	list := []metadata.TranslatedOverview{
		{Lang: "ja", Country: "JP", Overview: "千尋は神々の世界に迷い込む。"},
		{Lang: "fr", Country: "FR", Overview: "Chihiro se perd dans le monde des esprits."},
		{Lang: "fr", Country: "CA", Overview: "Chihiro s'égare au pays des esprits."},
		{Lang: "pt", Country: "PT", Overview: "Chihiro perde-se no mundo dos espíritos."},
		{Lang: "de", Country: "DE", Overview: "   "},
	}
	cases := []struct {
		name     string
		p        Policy
		want     string
		lang     string // OverviewLang
		tmdbLang string // TMDBLanguage
	}{
		{"english default", ParsePolicy("", "", ""), english, "", ""},
		{"original mode keeps English (no Japanese for an anime)", ParsePolicy("original", "", ""), english, "", ""},
		{"original titles for Japanese: titles only", ParsePolicy("english", "", "ja"), english, "", ""},
		{"French", ParsePolicy("language", "fr", ""), "Chihiro se perd dans le monde des esprits.", "fr", "fr"},
		{"French spelled fre", ParsePolicy("language", "fre", ""), "Chihiro se perd dans le monde des esprits.", "fr", "fr"},
		{"Canadian French prefers Canada", ParsePolicy("language", "fr-ca", ""), "Chihiro s'égare au pays des esprits.", "fr", "fr-CA"},
		{"Brazilian Portuguese takes Portugal's when Brazil has none", ParsePolicy("language", "pt-br", ""), "Chihiro perde-se no mundo dos espíritos.", "pt", "pt-BR"},
		{"French with Japanese originals: French overview", ParsePolicy("language", "fr", "ja"), "Chihiro se perd dans le monde des esprits.", "fr", "fr"},
		{"German blank: English fallback", ParsePolicy("language", "de", ""), english, "de", "de"},
		{"Italian missing: English fallback", ParsePolicy("language", "it", ""), english, "it", "it"},
		{"English as the language: English", ParsePolicy("language", "en", ""), english, "", ""},
	}
	for _, c := range cases {
		if got := c.p.Overview(english, list); got != c.want {
			t.Errorf("%s: Overview = %q, want %q", c.name, got, c.want)
		}
		if got := c.p.OverviewLang(); got != c.lang {
			t.Errorf("%s: OverviewLang = %q, want %q", c.name, got, c.lang)
		}
		if got := c.p.TMDBLanguage(); got != c.tmdbLang {
			t.Errorf("%s: TMDBLanguage = %q, want %q", c.name, got, c.tmdbLang)
		}
	}
	if got := ParsePolicy("language", "fr", "").ShownOverview(english, nil); got != english {
		t.Errorf("no stored names: %q, want English", got)
	}
}

// NamesFrom keeps only the overview language's overviews, records the language even when
// TMDB had none (so the backfill doesn't ask again), and Covers wants names read for it.
func TestNamesFromOverviews(t *testing.T) {
	fr := ParsePolicy("language", "fr", "")
	titles := &metadata.Titles{OriginalTitle: "千と千尋の神隠し", OriginalLanguage: "ja", OverviewLang: "fr",
		Translations: []metadata.TranslatedTitle{{Lang: "fr", Country: "FR", Title: "Le Voyage de Chihiro"}},
		Overviews: []metadata.TranslatedOverview{
			{Lang: "fr", Country: "FR", Overview: "Chihiro se perd."},
			{Lang: "ja", Country: "JP", Overview: "千尋は迷い込む。"},
		}}
	n := fr.NamesFrom(titles)
	if n.OverviewLang != "fr" || len(n.Overviews) != 1 || n.Overviews[0].Lang != "fr" || len(n.Translations) != 1 {
		t.Fatalf("names = %+v, want the French title and overview only", n)
	}
	if !n.Covers(fr) {
		t.Error("names read for French don't cover French")
	}
	if n.Covers(ParsePolicy("language", "it", "")) {
		t.Error("names read for French cover Italian")
	}
	if missing := fr.NamesFrom(nil); missing.OverviewLang != "fr" || !missing.Covers(fr) {
		t.Errorf("a title TMDB no longer has: %+v, want read for French", missing)
	}
	// Names read before overviews were kept cover the title languages, not the overview.
	old := FromTitles(titles, fr.TranslationLangs())
	if old.Covers(fr) {
		t.Error("names without overviews cover the French setting: existing libraries would never get overviews")
	}
	// The English and original modes read no overviews and need none.
	orig := ParsePolicy("original", "", "")
	if on := orig.NamesFrom(titles); on.OverviewLang != "" || len(on.Overviews) != 0 || !old.Covers(orig) {
		t.Errorf("original mode names = %+v", on)
	}
}
