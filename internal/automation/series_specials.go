package automation

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/tristenlammi/arrmada/internal/parser"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/series"
)

// Most specials aren't released as S00Exx at all but by name:
// "Show.The.Christmas.Special.2019.1080p.WEB". Everything here is limited to Specials
// scopes, so a regular episode is never routed by its title.

// specialStopWords don't count towards a special's title: "The Christmas Special" is
// known by "christmas special".
var specialStopWords = map[string]bool{"the": true, "a": true, "an": true, "of": true, "in": true, "on": true, "at": true, "to": true, "for": true}

func significantWords(words []string) []string {
	out := make([]string, 0, len(words))
	for _, w := range words {
		if !specialStopWords[w] {
			out = append(out, w)
		}
	}
	return out
}

// isYearWord reports whether a name word is a plausible year.
func isYearWord(w string) (int, bool) {
	if len(w) != 4 || (!strings.HasPrefix(w, "19") && !strings.HasPrefix(w, "20")) {
		return 0, false
	}
	y, err := strconv.Atoi(w)
	return y, err == nil
}

func airYear(date string) int {
	if len(date) < 4 {
		return 0
	}
	y, _ := strconv.Atoi(date[:4])
	return y
}

// specialTitleMatch reports whether a release is the special ep, named by its title rather
// than by S00Exx. All of these must hold:
//
//   - it carries no numbering at all: an SxxExx of any season, a pack or an absolute
//     number says which episode it is, and a title in the name doesn't overrule that —
//     "Show.S01E05.The.Christmas.Special" is S01E05;
//   - its name starts with the show, by the same identity rule every series match uses
//     (seriesIdentity: title, aliases, year, country); a year straight after the title
//     must be the show's or the special's own;
//   - the words after the show contain the special's title, whole words in order. A title
//     of fewer than two significant words ("Pilot", "Christmas") is too loose to find
//     inside a name, so then the name must say exactly that title and nothing more.
func specialTitleMatch(p parser.Release, name string, s series.Series, ep series.Episode) bool {
	return !p.IsTV() && specialTitleScore(showSplits(p, name, s), s, ep) > 0
}

// showSplit is one way a name divides into the show and what follows it: rest is the
// words after the show (and after a year straight after it, when year is set). plain is
// whether the words before name the show; withYear whether they do with that year read as
// the show's.
type showSplit struct {
	rest            []string
	year            int
	plain, withYear bool
}

// showSplits lists every way a name starts with the show. Worked out once per release —
// the identity rule is the expensive part, and every special is weighed against it.
func showSplits(p parser.Release, name string, s series.Series) []showSplit {
	var out []showSplit
	words := parser.NameWords(name)
	for i := 1; i < len(words); i++ {
		rest := words[i:]
		if _, cc := parser.SplitCountry("x " + rest[0]); cc != "" {
			continue // a country tag belongs to the show's name: "The.Office.US" isn't the UK show
		}
		q := p
		q.Title, q.TitleYear = strings.Join(words[:i], " "), 0
		sp := showSplit{rest: rest}
		sp.plain, _ = seriesIdentity(q, s)
		if y, ok := isYearWord(rest[0]); ok && len(rest) > 1 {
			sp.year, sp.rest = y, rest[1:]
			q.TitleYear = y // the show's year, or another show's
			sp.withYear, _ = seriesIdentity(q, s)
		}
		if sp.plain || sp.withYear {
			out = append(out, sp)
		}
	}
	return out
}

// specialTitleScore is specialTitleMatch as how many significant words matched (0 for no
// match), so the more specific of two specials that both match can win.
func specialTitleScore(splits []showSplit, s series.Series, ep series.Episode) int {
	if ep.SeasonNumber != 0 || len(splits) == 0 {
		return 0
	}
	titles := [][]string{parser.TitleWords(ep.Title)}
	// TMDB often names a special with the show in front ("Top Gear: Patagonia Special"),
	// which the release then doesn't repeat after the show's name.
	if show := parser.TitleWords(s.Title); len(show) > 0 && len(titles[0]) > len(show) &&
		strings.Join(titles[0][:len(show)], " ") == strings.Join(show, " ") {
		titles = append(titles, titles[0][len(show):])
	}
	for _, sp := range splits {
		// A year after the show is the show's own, or the year this special aired.
		ok := sp.plain
		if sp.year > 0 {
			ok = sp.withYear || (sp.plain && sp.year == airYear(ep.AirDate))
		}
		if !ok {
			continue
		}
		for _, all := range titles {
			if sig := significantWords(all); len(sig) > 0 && titleIn(sp.rest, all, sig) {
				return len(sig)
			}
		}
	}
	return 0
}

// titleIn reports whether the name words after the show hold the special's title (see
// specialTitleMatch).
func titleIn(rest, all, sig []string) bool {
	if len(sig) < 2 {
		// The whole title and nothing else, but an air year after it.
		for len(rest) > 0 {
			if _, ok := isYearWord(rest[len(rest)-1]); !ok {
				break
			}
			rest = rest[:len(rest)-1]
		}
		return strings.Join(rest, " ") == strings.Join(all, " ")
	}
	have := significantWords(rest)
	for i := 0; i+len(sig) <= len(have); i++ {
		match := true
		for j, w := range sig {
			if have[i+j] != w {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// specialByTitle is the special a release names by title, or 0. The show's specials are
// all weighed, not only the one wanted: a name matching two of them is the more specific
// one, then the one that aired in the year the name carries, and when that still doesn't
// settle it the release is nobody's — it must not land on a guess. want > 0 then accepts
// only that special.
func specialByTitle(p parser.Release, name string, s series.Series, want int) int {
	if p.IsTV() {
		return 0
	}
	splits := showSplits(p, name, s)
	if len(splits) == 0 {
		return 0 // not this show
	}
	best, bestScore := []series.Episode(nil), 0
	for _, sn := range s.Seasons {
		if sn.SeasonNumber != 0 {
			continue
		}
		for _, e := range sn.Episodes {
			switch n := specialTitleScore(splits, s, e); {
			case n > bestScore:
				best, bestScore = []series.Episode{e}, n
			case n > 0 && n == bestScore:
				best = append(best, e)
			}
		}
	}
	if len(best) > 1 && p.Year > 0 {
		var sameYear []series.Episode
		for _, e := range best {
			if airYear(e.AirDate) == p.Year {
				sameYear = append(sameYear, e)
			}
		}
		best = sameYear
	}
	if len(best) != 1 || (want > 0 && best[0].EpisodeNumber != want) {
		return 0
	}
	return best[0].EpisodeNumber
}

// asSpecial is a title-matched release read as the special it names, so everything after
// the match — the S00 scope checks, the planner's single-episode pass, the runtime the
// bitrate window is costed on — treats it as the S00Exx it is.
func asSpecial(r parser.Release, episode int) parser.Release {
	r.Season, r.Episodes, r.Seasons, r.SeasonExplicit = 0, []int{episode}, nil, true
	return r
}

// specialCandidate is newSeriesCandidate for a release matched to special ep by title:
// read as S00Exx, and costed over that special's runtime.
func (c *Coordinator) specialCandidate(cand quality.Candidate, idx runtimeIndex, episode int) quality.Candidate {
	cand.Release = asSpecial(cand.Release, episode)
	return cand.WithRuntime(idx.sum([]epKey{{0, episode}}))
}

// specialTitleQuery is the indexer query that names a special by its title, or "" when
// its title is too thin to search for.
func specialTitleQuery(title string, ep series.Episode) string {
	if len(significantWords(parser.TitleWords(ep.Title))) == 0 {
		return ""
	}
	q := indexerQuery(ep.Title)
	if q == "" {
		return ""
	}
	return title + " " + q
}

// specialEpisode is the show's season-0 episode n.
func specialEpisode(s series.Series, n int) (series.Episode, bool) {
	for _, sn := range s.Seasons {
		if sn.SeasonNumber != 0 {
			continue
		}
		for _, e := range sn.Episodes {
			if e.EpisodeNumber == n {
				return e, true
			}
		}
	}
	return series.Episode{}, false
}

// grabTargetSpecial decides whether a download's one unnumbered video goes onto the
// special its grab was made for. A special released by title carries no numbering, so the
// file can't be placed by its name — but the grab already said which special it's for.
// Only ever a single-video download whose file says nothing about its episode, and only
// for a grab scoped to exactly one special: two unnumbered videos could be anything, and a
// file with numbering is placed (or refused) by that numbering.
func grabTargetSpecial(videos int, file parser.Release, target GrabScope) (series.EpisodeRef, bool) {
	if videos != 1 || file.IsTV() || target.Season != 0 || target.Episode <= 0 {
		return series.EpisodeRef{}, false
	}
	return series.EpisodeRef{Season: 0, Episode: target.Episode}, true
}

// grabbedSpecialByTitle reports whether a download grabbed for one special is that special
// named by title — what lets the import route it to the show it was grabbed for.
func grabbedSpecialByTitle(p parser.Release, name string, s series.Series, target GrabScope) bool {
	return target.Season == 0 && target.Episode > 0 && specialByTitle(p, name, s, target.Episode) == target.Episode
}

// narrowSpecialScope narrows a pick from the Specials list to the one special it names by
// title, so the say-so and the import's placement both know which special it's for.
func (c *Coordinator) narrowSpecialScope(ctx context.Context, seriesID int64, title string, scope GrabScope) GrabScope {
	if scope.Season != 0 || scope.Episode != 0 || c.series == nil {
		return scope
	}
	s, err := c.series.Get(ctx, seriesID)
	if err != nil {
		return scope
	}
	if n := specialByTitle(parser.Parse(title), title, s, 0); n > 0 {
		return GrabScope{Season: 0, Episode: n}
	}
	return scope
}

// specialLabel is the search list's "Resolves" text for a title-matched special.
func specialLabel(n int) string { return fmt.Sprintf("S00E%02d (by title)", n) }
