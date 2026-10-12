package automation

import (
	"context"
	"strings"

	"github.com/tristenlammi/arrmada/internal/langs"
)

// Preferred book languages. A German or Spanish edition that keeps the original title
// ("Dune", "Fourth Wing") passes the title gate and used to win on seeders, so a book
// counted as done in a language nobody in the house reads — for an audiobook, hours of
// it. Automatic grabs now turn down a release whose stated language isn't one of these;
// the interactive search still lists it, marked, so it can be picked by hand.

// KeyBookLanguages is the setting (Settings → Library → Books) holding the languages
// automatic book grabs accept, as a comma list of canonical codes from internal/langs.
// Saved empty means any language.
const KeyBookLanguages = "books_languages"

// DefaultBookLanguages is the list when nothing is saved.
const DefaultBookLanguages = "en"

// ParseBookLanguages reads a stored list into canonical codes, in order, without
// duplicates. Tokens the language table doesn't know are dropped: an unknown code would
// otherwise reject every release that states a real language.
func ParseBookLanguages(raw string) []string {
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

// bookLangs is the set of base languages automatic grabs accept. Empty accepts all.
type bookLangs map[string]bool

func newBookLangs(codes []string) bookLangs {
	set := bookLangs{}
	for _, c := range codes {
		if b := langs.Base(c); b != "" {
			set[b] = true
		}
	}
	return set
}

// allows reports whether a release stating lang may be grabbed automatically. A release
// that states no language — most Torznab ones — or one the table can't read is allowed:
// not knowing is not the same as being wrong. Regions don't matter for a book (a tracker
// says "POR", not which Portuguese), so the base languages are compared.
func (l bookLangs) allows(lang string) bool {
	if len(l) == 0 {
		return true
	}
	known, ok := langs.Lookup(lang)
	if !ok {
		return true
	}
	return l[known.ISO1]
}

// SetBookLanguages wires the book languages setting. Asked once per search or RSS pass,
// so a change in Settings applies to the next one. Unset (tests) accepts every language.
func (c *Coordinator) SetBookLanguages(fn func(ctx context.Context) []string) { c.bookLangsFn = fn }

// bookLanguages is the accepted set for this pass.
func (c *Coordinator) bookLanguages(ctx context.Context) bookLangs {
	if c.bookLangsFn == nil {
		return nil
	}
	return newBookLangs(c.bookLangsFn(ctx))
}

// bookLangRejectReason is what the interactive search shows beside a release in another
// language: "Language: de — not in your book languages".
func bookLangRejectReason(lang string) string {
	code := langs.Canonical(lang)
	if code == "" {
		code = strings.TrimSpace(lang)
	}
	return "Language: " + code + " — not in your book languages"
}
