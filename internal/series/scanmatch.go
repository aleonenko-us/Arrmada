package series

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/parser"
)

// Why a scanned folder needs you. The web UI keys each row's fix action on the code; the
// Detail beside it says the same in plain words.
const (
	ReasonNoResults    = "no_results"    // TMDB found nothing under the folder's name
	ReasonAmbiguous    = "ambiguous"     // there are hits, none confidently the show
	ReasonImportFailed = "import_failed" // matched, but adding the show failed
	ReasonNoVideo      = "no_video"      // the folder holds no episode files
	ReasonDuplicate    = "duplicate"     // another folder already became this show
	ReasonNoEpisodes   = "no_episodes"   // added, but no file matched an episode
)

// countryTags are the "(US)" style tags folder names use to tell same-named shows apart,
// mapped to the ISO 3166 code TMDB gives a show as its origin country.
var countryTags = map[string]string{
	"us": "US", "usa": "US", "uk": "GB", "gb": "GB", "au": "AU", "ca": "CA", "nz": "NZ",
	"ie": "IE", "de": "DE", "fr": "FR", "es": "ES", "it": "IT", "nl": "NL", "se": "SE",
	"no": "NO", "dk": "DK", "fi": "FI", "jp": "JP", "kr": "KR", "br": "BR", "mx": "MX",
	"in": "IN", "il": "IL", "pl": "PL", "tr": "TR", "ru": "RU", "cn": "CN",
}

var countryTagRe = regexp.MustCompile(`(?i)^(.*\S)\s*[(\[]\s*([a-z]{2,3})\s*[)\]]\s*$`)

// stripCountryTag splits "The Office (US)" into "The Office" and "US" (TMDB's code for it).
// ok is false when the title doesn't end in a country tag.
func stripCountryTag(title string) (base, country string, ok bool) {
	m := countryTagRe.FindStringSubmatch(strings.TrimSpace(title))
	if m == nil {
		return "", "", false
	}
	c, known := countryTags[strings.ToLower(m[2])]
	if !known {
		return "", "", false
	}
	return strings.TrimSpace(m[1]), c, true
}

// countryMatch picks the show a "Title (US)" folder names: the one result whose title is
// the folder's without the tag and whose origin country is the tag's. Exactly one — two
// shows that fit are the owner's to choose between.
func countryMatch(results []metadata.SeriesResult, base, country string, year int) (metadata.SeriesResult, bool) {
	want := metadata.NormalizeTitle(base)
	var hit metadata.SeriesResult
	n := 0
	for _, r := range results {
		if metadata.NormalizeTitle(r.Title) != want {
			continue
		}
		in := false
		for _, c := range r.OriginCountry {
			if strings.EqualFold(c, country) {
				in = true
			}
		}
		if !in || (year > 0 && r.Year > 0 && (r.Year-year > 1 || year-r.Year > 1)) {
			continue
		}
		hit = r
		n++
	}
	return hit, n == 1
}

// scanMatch finds the TMDB show a folder name stands for. results are the search hits to
// offer as candidates when there is no confident match. A folder with a country tag
// ("The Office (US)") is searched for without it and told apart by origin country; the
// plain title search, and the owner's extra title languages, follow.
func (s *Service) scanMatch(ctx context.Context, rel parser.Release) (match metadata.SeriesResult, ok bool, results []metadata.SeriesResult, err error) {
	var tagged []metadata.SeriesResult
	if base, country, has := stripCountryTag(rel.Title); has {
		if tagged, err = s.meta.SearchSeries(ctx, base); err == nil {
			if m, found := countryMatch(tagged, base, country, rel.Year); found {
				return m, true, tagged, nil
			}
		}
	}
	results, err = s.meta.SearchSeries(ctx, rel.Title)
	if err == nil && len(results) > 0 {
		match, ok = bestSeriesMatch(results, rel.Title, rel.Year)
	}
	if !ok && err == nil {
		// A folder named in one of the owner's extra title languages ("Haus des Geldes"):
		// asked again in those languages, exactly one show must be named so.
		if id, found := s.localizedScanMatch(ctx, rel.Title, rel.Year); found {
			match, ok = metadata.SeriesResult{TMDBID: id}, true
		}
	}
	if !ok && len(results) == 0 {
		results = tagged // the tag's own search is the best the owner can pick from
	}
	return match, ok, results, err
}

// noEpisodesDetail says why a show added from a folder got no episode files. A show whose
// files are numbered by year (Looney Tunes: "Season 1931") is named as such.
func noEpisodesDetail(title string, videos []library.FoundVideo) string {
	byYear := 0
	for _, v := range videos {
		if p := parser.Parse(filepath.Base(v.Path)); p.Season >= 1900 {
			byYear++
		}
	}
	n := len(videos)
	files := fmt.Sprintf("%d video file%s", n, plural(n))
	if byYear > 0 {
		return fmt.Sprintf("Added as “%s”, but none of its %s matched an episode: its files are numbered by year (S1931E01…) and this listing's seasons aren't. Open the show to check its seasons.", title, files)
	}
	return fmt.Sprintf("Added as “%s”, but none of its %s matched an episode of it. The folder may hold a different show, or number its episodes differently from TMDB. Open the show to check.", title, files)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
