package music

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Comparing names and the small facts editions and the matcher share (MUS-30/31).

// bracketPenalty is what a bracketed suffix ("(2011 Remaster)", "[Bonus Track]") or a
// "feat." clause costs when only one side has it, instead of the whole string's worth.
const bracketPenalty = 0.1

// reSuffix finds the parts of a title that say which version it is rather than what it is
// called: anything in brackets, and a "feat." clause.
var reSuffix = regexp.MustCompile(`(?i)\s*(\([^)]*\)|\[[^\]]*\]|\{[^}]*\}|\s(?:feat\.?|ft\.|featuring)\s.*$)`)

// splitSuffix separates a title from its version suffixes.
func splitSuffix(s string) (base, suffix string) {
	var parts []string
	base = reSuffix.ReplaceAllStringFunc(s, func(m string) string {
		parts = append(parts, m)
		return " "
	})
	return strings.TrimSpace(base), strings.Join(parts, " ")
}

// stringDist compares two names after foldMusic, as 1 − Levenshtein similarity. A
// bracketed suffix or "feat." clause only one side has (or that differs) costs a fixed
// bracketPenalty rather than its share of the string: "Airbag" and "Airbag (2017
// Remaster)" are the same song. Spaces don't count, so "Pablo Honey" matches "PabloHoney".
func stringDist(a, b string) float64 {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == b {
		return 0
	}
	if a == "" || b == "" {
		return 1
	}
	baseA, sufA := splitSuffix(a)
	baseB, sufB := splitSuffix(b)
	if baseA == "" { // a title that is all brackets: compare it whole
		baseA, sufA = a, ""
	}
	if baseB == "" {
		baseB, sufB = b, ""
	}
	d := levRatio(compactFold(baseA), compactFold(baseB))
	if compactFold(sufA) != compactFold(sufB) {
		d += bracketPenalty
	}
	return math.Min(d, 1)
}

// compactFold is foldMusic without spaces; a name that folds to nothing (all punctuation)
// is compared lower-cased as written.
func compactFold(s string) string {
	if f := strings.ReplaceAll(foldMusic(s), " ", ""); f != "" {
		return f
	}
	return strings.ToLower(strings.Join(strings.Fields(s), ""))
}

// levRatio is the Levenshtein distance between a and b over the longer one's length, in
// runes (so a CJK title counts per character).
func levRatio(a, b string) float64 {
	if a == b {
		return 0
	}
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 || len(rb) == 0 {
		return 1
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	n := len(ra)
	if len(rb) > n {
		n = len(rb)
	}
	return float64(prev[len(rb)]) / float64(n)
}

// yearOf reads the leading year from a date ("1997", "1997-05", "1997-05-21").
func yearOf(date string) int {
	date = strings.TrimSpace(date)
	if len(date) < 4 {
		return 0
	}
	y, err := strconv.Atoi(date[:4])
	if err != nil || y < 1000 {
		return 0
	}
	return y
}

// priority is a value's place in an ordered preference list, as a penalty: the first
// costs 0, unlisted values cost 1.
func priority(value string, prefs []string) float64 {
	if len(prefs) == 0 {
		return 0
	}
	unit := 1.0 / float64(len(prefs))
	for i, p := range prefs {
		if strings.EqualFold(strings.TrimSpace(p), strings.TrimSpace(value)) {
			return float64(i) * unit
		}
	}
	return 1
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// primaryFormat is an edition's first (non-ignored) disc format.
func primaryFormat(e Edition) string {
	for _, m := range e.Media {
		if m.Format != "" && !IgnoredMedium(m.Format) {
			return m.Format
		}
	}
	return firstSummaryFormat(e.MediaSummary)
}
