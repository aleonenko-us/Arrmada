package music

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/tristenlammi/arrmada/internal/parser"
)

// foldMusic folds an artist, album or release name for whole-word comparison, so a release
// that spells a name the way a filename can carry it still matches the name MusicBrainz
// uses:
//
//	Björk                     bjork        (accents fold, as everywhere: parser.FoldTitle)
//	Sgt. Pepper's             sgt peppers  (an apostrophe goes without leaving a gap)
//	Simon & Garfunkel         simon garfunkel
//	Florence + the Machine    florence the machine
//	R.E.M.                    rem          (single letters joined by dots are one word)
//	AC/DC                     acdc         (a slash between letters joins them)
//	The_Beatles-Sgt_Peppers   the beatles sgt peppers
//
// The words come from parser.TitleWords, the one word split every title matcher shares, so
// "&", "+" and "and" all end up the same way theirs do: the standalone word "and" is
// dropped, because releases spell it "&", "and" or leave it out entirely. Letters of any
// script are kept, so a CJK or Cyrillic name folds to itself rather than to nothing.
//
// The result is space-separated words; matching on whole words is what stops "Kid A"
// matching inside "Kid Amnesiae".
func foldMusic(s string) string { return foldMusicWith(s, true) }

// foldMusicSplit is foldMusic with a slash read as a word break instead of a join. A
// release can write AC/DC as "ACDC" or as "AC.DC", and an album called Either/Or as
// "Either Or"; the matcher tries both readings rather than picking one.
func foldMusicSplit(s string) string { return foldMusicWith(s, false) }

func foldMusicWith(s string, joinSlash bool) string {
	s = parser.FoldTitle(s)
	// An apostrophe joins its neighbours: "what's" → "whats", which is how a filename
	// that can't carry the apostrophe spells it. FoldTitle has already turned the
	// typographic ones into "'".
	s = strings.ReplaceAll(s, "'", "")
	s = rePlusWord.ReplaceAllString(s, " and ")
	s = joinAcronyms(s)
	if joinSlash {
		s = reLetterSlash.ReplaceAllString(s, "$1$2")
	}
	return strings.Join(parser.TitleWords(s), " ")
}

var (
	// "+" standing on its own, as in "Florence + the Machine". One inside a word
	// ("C++", "Blink+182") is left to the word split.
	rePlusWord = regexp.MustCompile(`(^|\s)\+(\s|$)`)
	// A slash between two letters: "ac/dc". Replaced repeatedly via the two groups, so
	// "a/b/c" needs a second pass — rare enough in names not to matter.
	reLetterSlash = regexp.MustCompile(`(\pL)/(\pL)`)
	// A run of single letters each followed by a dot, optionally ending in a bare letter:
	// "r.e.m.", "p.o.d", "u.s.a.". The boundaries either side are checked by hand, since
	// RE2 has no lookaround.
	reAcronym = regexp.MustCompile(`(?:\pL\.)+\pL?`)
)

// joinAcronyms closes up single-letter dotted acronyms ("r.e.m." → "rem"). Only single
// letters count — the run must start at a word boundary and end on a dot or at one — so
// "mr. bungle" and "sgt. pepper" keep their words, and a dotted scene name such as
// "a.perfect.circle" isn't glued together. In a dotted scene name the dot after the last
// letter is the separator: "r.e.m.automatic" reads as "rem automatic".
func joinAcronyms(s string) string {
	locs := reAcronym.FindAllStringIndex(s, -1)
	if locs == nil {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	last := 0
	for _, loc := range locs {
		start, end := loc[0], loc[1]
		if !wordEdge(s, start, true) {
			continue
		}
		if s[end-1] != '.' && !wordEdge(s, end, false) {
			// The bare letter at the end begins a longer word ("a.perfect"): it isn't
			// part of the acronym, and the run before it must stand on its own.
			_, size := lastRune(s[:end])
			end -= size
		}
		letters := strings.ReplaceAll(s[start:end], ".", "")
		if len([]rune(letters)) < 2 {
			continue
		}
		b.WriteString(s[last:start])
		// A space after keeps the acronym its own word when a dotted scene name runs
		// straight on ("r.e.m.automatic" → "rem automatic").
		b.WriteString(letters)
		b.WriteByte(' ')
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}

// wordEdge reports whether position i in s is a word boundary on the given side: the
// start or end of the string, or a rune that is neither a letter nor a digit.
func wordEdge(s string, i int, before bool) bool {
	if before {
		if i == 0 {
			return true
		}
		r := []rune(s[:i])
		return !isWordRune(r[len(r)-1])
	}
	if i >= len(s) {
		return true
	}
	for _, r := range s[i:] {
		return !isWordRune(r)
	}
	return true
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// lastRune is the final rune of s and its width in bytes.
func lastRune(s string) (rune, int) {
	r := []rune(s)
	if len(r) == 0 {
		return 0, 0
	}
	last := r[len(r)-1]
	return last, len(string(last))
}

// containsWords reports whether the folded words of needle appear, whole and in order, in
// the folded words of hay. Both readings of a slash are tried (see foldMusicSplit).
func containsWords(hay, needle string) bool {
	for _, fold := range []func(string) string{foldMusic, foldMusicSplit} {
		n := fold(needle)
		if n != "" && strings.Contains(" "+fold(hay)+" ", " "+n+" ") {
			return true
		}
	}
	return false
}

// artistNamed reports whether a release names this artist as whole words. An artist that
// folds to nothing never matches: there's nothing to check the release against, and
// waving it through would let any release with the right album title in.
//
// A leading "The" may be missing from the release ("Beatles - Abbey Road") as long as
// something is left after it, and that something isn't just another "the": "The The"
// still has to say "The The".
func artistNamed(hay, artist string) bool {
	if foldMusic(artist) == "" {
		return false
	}
	if containsWords(hay, artist) {
		return true
	}
	words := strings.Fields(foldMusic(artist))
	if len(words) < 2 || words[0] != "the" {
		return false
	}
	rest := words[1:]
	if len(rest) == 1 && rest[0] == "the" {
		return false
	}
	return containsWords(hay, strings.Join(rest, " "))
}
