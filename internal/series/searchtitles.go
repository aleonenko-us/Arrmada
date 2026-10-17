package series

import (
	"context"
	"fmt"
	"sync"

	"github.com/tristenlammi/arrmada/internal/parser"
)

// Searches — the sweeps, Search now, the interactive list and quick Grab buttons, upgrades
// and RSS — accept a release by FitRelease. With Settings → Library → "Also match titles
// in" set they also accept one named by the show's stored translated title in those
// languages: a German tracker lists Money Heist only as "Haus.des.Geldes.S01E01.German…".
//
// A search grabs what it accepts, with nobody looking, so this tier is stricter than import
// routing's (matchLocal), which only ever places a download that is already here:
//
//   - only the translated titles stored for the show (local_titles) in the languages set
//     now — never a TMDB call — and none shorter than minLocalKeyLen;
//   - only for a release that doesn't name the show by its own title or an alias at all
//     (FitRelease decides those, a year or country that disagrees included);
//   - never a name some library show goes by, in any year or country (NamesAShow);
//   - never a name another library show's translated title shares: ambiguous is no
//     match, whatever the year;
//   - a year in the release must be the show's and a country tag its origin, known and
//     agreeing (import's tier lets an unknown origin pass; this one doesn't);
//   - a translated title of one short word (four letters or fewer: "Haus", "Lost") needs
//     the release's year or country tag to agree. Such a word is the name of too many
//     shows the library has never heard of to go by alone — the extra query below would
//     bring them in by the page;
//   - and only once every library show's translated titles have been read for those
//     languages: until the backfill has read them all, a show not yet read could share
//     the name unseen.
//
// With the setting empty a SearchFit is FitRelease, exactly.

// SearchFit is the identity rule one search judges its releases by: FitRelease, widened
// by the stored translated titles. The library it checks names against is read once, on
// first need, and only when a show with translated titles meets a release its own titles
// don't name. A nil *SearchFit is FitRelease.
type SearchFit struct {
	langs []string
	load  func() ([]Series, error)

	once  sync.Once
	all   []Series
	ready bool
}

// SearchFit is the rule for one search, reading the library from s when first needed.
func (s *Service) SearchFit(ctx context.Context) *SearchFit {
	langs := s.localLangs()
	if len(langs) == 0 {
		return nil
	}
	return &SearchFit{langs: langs, load: func() ([]Series, error) { return s.List(ctx) }}
}

// SearchFitFor is SearchFit over a library snapshot the caller already holds (RSS).
func (s *Service) SearchFitFor(all []Series) *SearchFit {
	return newSearchFit(all, s.localLangs())
}

func newSearchFit(all []Series, langs []string) *SearchFit {
	if len(langs) == 0 {
		return nil
	}
	return &SearchFit{langs: langs, load: func() ([]Series, error) { return all, nil }}
}

// library is the library snapshot, and whether the translated tier may use it: read, and
// every show's translated titles read for the languages set. Fails closed.
func (f *SearchFit) library() ([]Series, bool) {
	f.once.Do(func() {
		all, err := f.load()
		if err != nil {
			return
		}
		for _, o := range all {
			if needsLocalTitles(o, f.langs) {
				return
			}
		}
		f.all, f.ready = all, true
	})
	return f.all, f.ready
}

// Fit judges a parsed release against sr for a search: FitRelease, then the translated
// tier (see above). A release matched by a translated title has Local set; one named by a
// translated title that disagrees has Title and Local set and Why says why.
func (f *SearchFit) Fit(p parser.Release, sr Series) ReleaseFit {
	fit := FitRelease(p, sr)
	if fit.Title || f == nil {
		return fit
	}
	// Which of the show's translated titles names the release, before anything reads the
	// library: most releases a search sees are named by no translated title at all.
	matched := translatedName(p, sr.searchLocalTitles(f.langs))
	if matched == "" {
		return fit
	}
	all, ok := f.library()
	if !ok {
		return fit
	}
	return fitTranslated(all, p, sr, matched, f.langs)
}

// QueryTitle is the translated title a search for sr may also ask indexers by, "" when
// there's none worth asking for: the first, in the setting's order, that the translated
// tier could ever accept a release by — no library show's name, not shared with another
// show, not a single short word (whose releases need a year or country to be taken, and
// whose query would mostly bring back other shows).
func (f *SearchFit) QueryTitle(sr Series) string {
	if f == nil {
		return ""
	}
	titles := sr.searchLocalTitles(f.langs)
	if len(titles) == 0 {
		return ""
	}
	all, ok := f.library()
	if !ok {
		return ""
	}
	for _, t := range titles {
		if shortWordTitle(t) {
			continue
		}
		p := parser.Release{Title: t}
		key, baseKey, _ := releaseKeys(p)
		if NamesAShow(all, p) || sharedTranslation(all, sr.ID, key, baseKey, f.langs) {
			continue
		}
		return t
	}
	return ""
}

// searchLocalTitles are sr's stored translated titles in langs, in the setting's order,
// minus the ones too short to stand for a show and the ones FitRelease already judges by.
func (s Series) searchLocalTitles(langs []string) []string {
	if s.Extra == nil || len(s.Extra.LocalTitles) == 0 {
		return nil
	}
	skip := s.ownKeys()
	var out []string
	for _, l := range langs {
		out = append(out, otherNames(titlesOf(s.Extra.LocalTitles, []string{l}), skip)...)
	}
	return out
}

// translatedName is the one of titles a parsed release is named by — its title exactly,
// else its title with a country tag taken off — or "".
func translatedName(p parser.Release, titles []string) string {
	if len(titles) == 0 {
		return ""
	}
	key, baseKey, _ := releaseKeys(p)
	if len(key) < minLocalKeyLen {
		return ""
	}
	for _, t := range titles {
		if parser.TitleKey(t) == key {
			return t
		}
	}
	if baseKey != "" {
		for _, t := range titles {
			if parser.TitleKey(t) == baseKey {
				return t
			}
		}
	}
	return ""
}

// fitTranslated is the translated tier of SearchFit.Fit for a release FitRelease found no
// name of sr's in, and sr's translated title matched does.
func fitTranslated(all []Series, p parser.Release, sr Series, matched string, langs []string) ReleaseFit {
	key, baseKey, cc := releaseKeys(p)
	if NamesAShow(all, p) {
		return ReleaseFit{} // another library show's own name or alias: never this one's
	}
	if sharedTranslation(all, sr.ID, key, baseKey, langs) {
		return ReleaseFit{Title: true, Local: true,
			Why: fmt.Sprintf("%q is the translated title of another show in the library too", matched)}
	}
	f, _ := sr.fitTitles(p, key, baseKey, cc, []string{matched})
	f.Local = true
	if !f.OK {
		return f
	}
	if cc != "" && !f.Country {
		if _, own := parser.SplitCountry(matched); own != cc {
			f.OK, f.Why = false, fmt.Sprintf("tagged %s, and the show's country isn't known yet", cc)
			return f
		}
	}
	if p.TitleYear > 0 && !f.Year {
		f.OK, f.Why = false, fmt.Sprintf("the release says %d, and the show's year can't be checked", p.TitleYear)
		return f
	}
	if shortWordTitle(matched) && !f.Year && !f.Country {
		f.OK, f.Why = false, fmt.Sprintf("%q is too common a name to go by without the show's year or country", matched)
		return f
	}
	return f
}

// sharedTranslation reports whether a show other than id has a stored translated title in
// langs keyed key (or baseKey, when set).
func sharedTranslation(all []Series, id int64, key, baseKey string, langs []string) bool {
	for _, o := range all {
		if o.ID == id || o.Extra == nil {
			continue
		}
		for _, t := range otherNames(titlesOf(o.Extra.LocalTitles, langs), nil) {
			if k := parser.TitleKey(t); k == key || (baseKey != "" && k == baseKey) {
				return true
			}
		}
	}
	return false
}

// shortWordTitle reports whether a title is one word of four letters or fewer.
func shortWordTitle(t string) bool {
	return len(parser.TitleWords(t)) == 1 && len(parser.TitleKey(t)) <= 4
}
