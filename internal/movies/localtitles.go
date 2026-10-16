package movies

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

// Films are released, and folders named, in the household's language: a German tracker
// lists Wild (2014) as "Der.grosse.Trip.Wild.2014.German.DL…", and a German library keeps
// The Godfather in "Der Pate (1972)". The titles a film keeps for matching (TitleKeys)
// are its own, its original and its US/UK/origin-country alternatives, so neither is
// recognised. Two separate answers, by how much is at stake:
//
//   - KnownAs, the import gate's check for a download grabbed for one film: every name
//     TMDB has for that film, in any language. It can only confirm the film the grab was
//     made for, never pick another, so it can afford to be broad.
//   - The translated titles in the languages the owner names (Settings → Library → "Also
//     match titles in"), stored per film and matched library-wide — for scans and for
//     downloads Arrmada didn't grab, where there is no grab to confirm against. Off by
//     default, and stricter than the alternative titles: a translation never takes a name
//     another film goes by, and two films sharing one match neither.

// ErrNoTitles is KnownAs unable to read a film's names (no TMDB key, no TMDB id).
var ErrNoTitles = errors.New("the film's other titles can't be read")

// SetTitlesSource installs where every name of a film is read from (TMDB). Call it at
// startup, before anything runs.
func (s *Service) SetTitlesSource(src metadata.TitlesSource) { s.titles = src }

// SetTitleLanguages installs the "Also match titles in" setting, read on every use as ISO
// 639-1 codes so a change applies without a restart. Call it at startup.
func (s *Service) SetTitleLanguages(fn func() []string) { s.titleLangs = fn }

// localLangs is the languages whose translated titles match library-wide now.
func (s *Service) localLangs() []string {
	if s.titleLangs == nil {
		return nil
	}
	return s.titleLangs()
}

// KnownAs reports whether title (a download's parsed title) is one of m's names in any
// language — its title, original title, every alternative title from every country and
// every translation — with the year, when both are known, within one. err is set when the
// names couldn't be read (no TMDB key, an outage); ok is then false and the caller does
// what it did before this check existed.
//
// A name that is m's own title, original title or a kept alternative (TitleKeys) is never
// confirmed here: Match is the authority on those, with its rules for same-titled films
// and year-less releases, and this check must not overrule what it decided.
func (s *Service) KnownAs(ctx context.Context, m Movie, title string, year int) (bool, error) {
	if year != 0 && m.Year != 0 && abs(m.Year-year) > 1 {
		return false, nil // a same-titled film from another year: the remake guard
	}
	key := normalizeTitle(title)
	if len(key) < minAltKeyLen || slices.Contains(TitleKeys(m, nil), key) {
		return false, nil
	}
	if m.Extra != nil {
		for _, lt := range m.Extra.LocalTitles {
			if normalizeTitle(lt.Title) == key {
				return true, nil // stored already: no TMDB call
			}
		}
	}
	if s.titles == nil || m.TMDBID <= 0 {
		return false, ErrNoTitles
	}
	t, err := s.titles.Titles(ctx, metadata.TitlesMovie, m.TMDBID)
	if err != nil {
		return false, err
	}
	for _, name := range t.All() {
		if normalizeTitle(name) == key {
			return true, nil
		}
	}
	return false, nil
}

// NamedInLibrary reports whether some library film's own title, original title or kept
// alternative title keys the same as title, whatever the year. When one does, Match has
// the say — a film called so, or a name several films share with no year to pick between
// them — and a download by that name is never confirmed by another film's other names
// (KnownAs). When the library can't be read it answers true, so the caller holds.
func (s *Service) NamedInLibrary(ctx context.Context, title string) bool {
	all, err := s.repo.List(ctx)
	if err != nil {
		return true
	}
	own, alt := indexTitleKeys(all)
	k := normalizeTitle(title)
	return len(own[k]) > 0 || len(alt[k]) > 0
}

// readLocalTitles reads m's translated titles in langs, or reports why it couldn't.
func (s *Service) readLocalTitles(ctx context.Context, tmdbID int, title string, langs []string) ([]metadata.TranslatedTitle, error) {
	if s.titles == nil || tmdbID <= 0 {
		return nil, ErrNoTitles
	}
	return metadata.ReadLocalTitles(ctx, s.titles, metadata.TitlesMovie, tmdbID, title, langs)
}

// withLocalTitles fills a fresh extra blob's translated titles for the languages set now,
// on add and refresh. When they can't be read the ones stored before (prev) are kept, so a
// TMDB hiccup during a refresh doesn't drop them, and the titles backfill tries again.
func (s *Service) withLocalTitles(ctx context.Context, ex, prev *MovieExtra, tmdbID int, title string) {
	if ex == nil {
		return
	}
	if prev != nil {
		ex.LocalTitles, ex.LocalLangs = prev.LocalTitles, prev.LocalLangs
	}
	langs := s.localLangs()
	if len(langs) == 0 {
		return
	}
	got, err := s.readLocalTitles(ctx, tmdbID, title, langs)
	if err != nil {
		if !errors.Is(err, ErrNoTitles) {
			s.log.Debug("movies: couldn't read translated titles; the titles backfill tries again", "movie", title, "err", err)
		}
		return
	}
	ex.LocalTitles, ex.LocalLangs = got, append([]string(nil), langs...)
}

// needsLocalTitles reports whether m's translated titles haven't been read for every
// language in langs.
func needsLocalTitles(m Movie, langs []string) bool {
	if len(langs) == 0 || m.TMDBID <= 0 {
		return false
	}
	if m.Extra == nil {
		return true
	}
	return !metadata.CoversLangs(m.Extra.LocalLangs, langs)
}

// indexLocalKeys maps each translated title key in langs to the films it names. A key that
// is any film's own, original or alternative title (own, alt) is left out: that name is
// taken, and a translation never outranks it — the German title of one film that is
// another film's English title stays the English film's.
func indexLocalKeys(all []Movie, own, alt map[string][]Movie, langs []string) map[string][]Movie {
	if len(langs) == 0 {
		return nil
	}
	want := map[string]bool{}
	for _, l := range langs {
		want[l] = true
	}
	local := map[string][]Movie{}
	for _, m := range all {
		if m.Extra == nil {
			continue
		}
		seen := map[string]bool{}
		for _, lt := range m.Extra.LocalTitles {
			k := normalizeTitle(lt.Title)
			if !want[lt.Lang] || len(k) < minAltKeyLen || seen[k] || len(own[k]) > 0 || len(alt[k]) > 0 {
				continue
			}
			seen[k] = true
			local[k] = append(local[k], m)
		}
	}
	return local
}

// pickLocal is pick for translated titles, stricter: the release must carry a year, and the
// film must be the ONLY one named so whose year fits. Two films sharing a translated title
// ("Der Pate" for The Godfather and a namesake) match neither, and a year-less name is
// never matched library-wide by a translation — the library may hold only one of the
// films a translation names.
func (s *Service) pickLocal(candidates []Movie, title string, year int) (Movie, bool) {
	if year == 0 {
		return Movie{}, false
	}
	var fit []Movie
	for _, m := range candidates {
		if year == 0 || m.Year == 0 || abs(m.Year-year) <= 1 {
			fit = append(fit, m)
		}
	}
	if len(fit) == 1 {
		return fit[0], true
	}
	if len(fit) > 1 && s.log != nil {
		names := make([]string, 0, len(fit))
		for _, c := range fit {
			names = append(names, fmt.Sprintf("%s (%d)", c.Title, c.Year))
		}
		s.log.Debug("translated title names several films — not matching",
			"title", title, "candidates", strings.Join(names, ", "))
	}
	return Movie{}, false
}

// localizedScanMatch identifies a scanned folder named in one of the owner's extra title
// languages (metadata.LocalizedMatch: exactly one title so named, or none).
func (s *Service) localizedScanMatch(ctx context.Context, title string, year int) (int, bool) {
	langs := s.localLangs()
	if len(langs) == 0 || s.titles == nil {
		return 0, false
	}
	return metadata.LocalizedMatch(ctx, s.titles, metadata.TitlesMovie, title, year, langs)
}
