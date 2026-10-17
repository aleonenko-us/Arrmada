package series

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/tristenlammi/arrmada/internal/metadata"
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
//     now — never a TMDB call — and none shorter than minLocalKeyLen letters;
//   - only for a release that doesn't name the show by its own title or an alias at all
//     (FitRelease decides those, a year or country that disagrees included);
//   - never a name some library show goes by, in any year or country (NamesAShow);
//   - never a name another library show's translated title shares: ambiguous is no
//     match, whatever the year;
//   - a year in the release must be the show's and a country tag its origin, known and
//     agreeing (import's tier lets an unknown origin pass; this one doesn't);
//   - always some evidence the release is the translated show, beyond the name: it says
//     the title's language (German, GER, German DL…), or it is MULTi/DUAL, or its year
//     or country agrees with the show's. A translated title is also some other show's
//     name somewhere — Ghosts (US) is "Fantasmas" in Spanish, and so is HBO's English
//     "Fantasmas", which the library has never heard of; only the language, the year or
//     the country tells them apart;
//   - a translated title of one short word (four letters or fewer: "Haus", "Тьма") needs
//     the year or the country itself — the language alone isn't enough for a word that
//     names that many shows;
//   - and only once every library show's translated titles have been read for those
//     languages: until the backfill has read them all, a show not yet read could share
//     the name unseen.
//
// With the setting empty a SearchFit is FitRelease, exactly.

// SearchFit is the identity rule one search judges its releases by: FitRelease, widened
// by the stored translated titles. The library it checks names against is read once, on
// first need, and only when a show's translated title names a release its own titles
// don't. A nil *SearchFit is FitRelease.
type SearchFit struct {
	langs []string
	load  func() ([]Series, error)
	// unread hears that the tier is off because shows' titles aren't read yet.
	unread func(unread []Series)

	once  sync.Once
	all   []Series
	ready bool
}

// SearchFit is the rule for one search, reading the library from s when first needed —
// only what identity needs (searchLibrary), not List's per-show statistics.
func (s *Service) SearchFit(ctx context.Context) *SearchFit {
	langs := s.localLangs()
	if len(langs) == 0 {
		return nil
	}
	return &SearchFit{langs: langs, load: func() ([]Series, error) { return s.repo.searchLibrary(ctx) }, unread: s.noteUnread}
}

// SearchFitFor is SearchFit over a library snapshot the caller already holds (a sweep,
// RSS).
func (s *Service) SearchFitFor(all []Series) *SearchFit {
	f := newSearchFit(all, s.localLangs())
	if f != nil {
		f.unread = s.noteUnread
	}
	return f
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
		if unread := unreadShows(all, f.langs); len(unread) > 0 {
			if f.unread != nil {
				f.unread(unread)
			}
			return
		}
		f.all, f.ready = all, true
	})
	return f.all, f.ready
}

// unreadShows are the shows whose translated titles haven't been read for every language in
// langs yet.
func unreadShows(all []Series, langs []string) []Series {
	var out []Series
	for _, o := range all {
		if needsLocalTitles(o, langs) {
			out = append(out, o)
		}
	}
	return out
}

// noteUnread says, once per process, that TV searches aren't using translated titles yet
// and which shows they're waiting for — otherwise the tier is off in silence.
func (s *Service) noteUnread(unread []Series) {
	if !s.unreadLogged.CompareAndSwap(false, true) {
		return
	}
	var names []string
	for _, o := range unread {
		if len(names) == 5 {
			break
		}
		names = append(names, o.Title)
	}
	s.log.Info("series: TV searches don't use translated titles until every show's are read",
		"unread", len(unread), "shows", strings.Join(names, ", "))
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
	matched, ok := translatedName(p, sr.searchLocalTitles(f.langs))
	if !ok {
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
// whose query would mostly bring back other shows) — and that an indexer can be asked by:
// Latin script, since queries are folded to ASCII and a Cyrillic title would be asked as
// nothing, or as the digits of "911 служба спасения".
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
		if shortWordTitle(t.Title) || !parser.IsLatin(t.Title) {
			continue
		}
		p := parser.Release{Title: t.Title}
		key, baseKey, _ := releaseKeys(p)
		if NamesAShow(all, p) || sharedTranslation(all, sr.ID, key, baseKey, f.langs) {
			continue
		}
		return t.Title
	}
	return ""
}

// searchLocalTitles are sr's stored translated titles in langs, in the setting's order,
// minus the ones too short to stand for a show and the ones FitRelease already judges by.
func (s Series) searchLocalTitles(langs []string) []metadata.TranslatedTitle {
	if s.Extra == nil || len(s.Extra.LocalTitles) == 0 {
		return nil
	}
	skip := s.ownKeys()
	var out []metadata.TranslatedTitle
	for _, l := range langs {
		for _, t := range s.Extra.LocalTitles {
			if k := parser.TitleKey(t.Title); t.Lang == l && keyLen(k) >= minLocalKeyLen && !skip[k] {
				out = append(out, t)
			}
		}
	}
	return out
}

// translatedName is the one of titles a parsed release is named by — its title exactly,
// else its title with a country tag taken off.
func translatedName(p parser.Release, titles []metadata.TranslatedTitle) (metadata.TranslatedTitle, bool) {
	if len(titles) == 0 {
		return metadata.TranslatedTitle{}, false
	}
	key, baseKey, _ := releaseKeys(p)
	if keyLen(key) < minLocalKeyLen {
		return metadata.TranslatedTitle{}, false
	}
	for _, t := range titles {
		if parser.TitleKey(t.Title) == key {
			return t, true
		}
	}
	if baseKey != "" {
		for _, t := range titles {
			if parser.TitleKey(t.Title) == baseKey {
				return t, true
			}
		}
	}
	return metadata.TranslatedTitle{}, false
}

// fitTranslated is the translated tier of SearchFit.Fit for a release FitRelease found no
// name of sr's in, and sr's translated title matched does.
func fitTranslated(all []Series, p parser.Release, sr Series, matched metadata.TranslatedTitle, langs []string) ReleaseFit {
	key, baseKey, cc := releaseKeys(p)
	if NamesAShow(all, p) {
		return ReleaseFit{} // another library show's own name or alias: never this one's
	}
	if sharedTranslation(all, sr.ID, key, baseKey, langs) {
		return ReleaseFit{Title: true, Local: true,
			Why: fmt.Sprintf("%q is the translated title of another show in the library too", matched.Title)}
	}
	f, _ := sr.fitTitles(p, key, baseKey, cc, []string{matched.Title})
	f.Local = true
	if !f.OK {
		return f
	}
	if cc != "" && !f.Country {
		if _, own := parser.SplitCountry(matched.Title); own != cc {
			f.OK, f.Why = false, fmt.Sprintf("tagged %s, and the show's country isn't known yet", cc)
			return f
		}
	}
	if p.TitleYear > 0 && !f.Year {
		f.OK, f.Why = false, fmt.Sprintf("the release says %d, and the show's year can't be checked", p.TitleYear)
		return f
	}
	if shortWordTitle(matched.Title) && !f.Year && !f.Country {
		f.OK, f.Why = false, fmt.Sprintf("%q is too common a name to go by without the show's year or country", matched.Title)
		return f
	}
	if !f.Year && !f.Country && !saysLanguage(p, matched.Lang) {
		f.OK, f.Why = false, fmt.Sprintf("named %q, but nothing in the release — its language, year or country — says it is this show", matched.Title)
		return f
	}
	return f
}

// saysLanguage reports whether a release names lang (ISO 639-1) among its audio languages,
// or carries several (MULTi, DUAL), as a release in a translated title's language does.
func saysLanguage(p parser.Release, lang string) bool {
	return p.Multi || p.Dual || slices.Contains(p.Languages, lang)
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
	return len(parser.TitleWords(t)) == 1 && keyLen(parser.TitleKey(t)) <= 4
}

// searchLibrary is the library as the search rule reads it: each show's id, title, year,
// TMDB id, type, extra metadata and aliases — not List's per-show statistics, which cost
// an aggregate over every episode. Unlike List it fails when the aliases can't be read: a
// missing alias would let a translated title take a name a show goes by.
func (r *Repo) searchLibrary(ctx context.Context) ([]Series, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, tmdb_id, title, year, extra_json, series_type FROM series`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Series
	for rows.Next() {
		var s Series
		var extra sql.NullString
		if err := rows.Scan(&s.ID, &s.TMDBID, &s.Title, &s.Year, &extra, &s.SeriesType); err != nil {
			return nil, err
		}
		if extra.String != "" {
			var ex SeriesExtra
			if json.Unmarshal([]byte(extra.String), &ex) == nil {
				s.Extra = &ex
			}
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	aliases, err := r.aliasTitles(ctx)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Aliases = aliases[out[i].ID]
	}
	return out, nil
}
