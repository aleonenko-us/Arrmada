package settings

import (
	"strings"

	"github.com/tristenlammi/arrmada/internal/langs"
)

// KeyTitleLanguages is Settings → Library's "Also match titles in": the extra languages
// whose translated titles library scans and downloads Arrmada didn't grab may be named
// in (a German household's "Der Pate" folder for The Godfather). A comma list of
// canonical language codes; empty — the default — matches English and original titles
// only, as before the setting existed.
const KeyTitleLanguages = "match_title_languages"

// ParseTitleLanguages reads the stored list: canonical codes, known languages only, each
// once, in the order saved.
func ParseTitleLanguages(raw string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, tok := range strings.Split(raw, ",") {
		l, ok := langs.Lookup(tok)
		if !ok || seen[l.Code] {
			continue
		}
		seen[l.Code] = true
		out = append(out, l.Code)
	}
	return out
}

// TitleLanguageBases is the stored list as the two-letter base codes TMDB files its
// translations under ("pt-br" → "pt"), each once. A regional pick reads every translation
// in its base language: TMDB's regional rows are sparse, and a Brazilian household's
// folders are as likely to carry the Portuguese title.
func TitleLanguageBases(raw string) []string {
	var out []string
	seen := map[string]bool{}
	for _, code := range ParseTitleLanguages(raw) {
		base := langs.ISO1(code)
		if base == "" || seen[base] {
			continue
		}
		seen[base] = true
		out = append(out, base)
	}
	return out
}
