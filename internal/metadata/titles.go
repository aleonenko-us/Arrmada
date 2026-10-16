package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/adultfilter"
	"github.com/tristenlammi/arrmada/internal/parser"
)

// Media kinds Titles and SearchLocalized take.
const (
	TitlesMovie  = "movie"
	TitlesSeries = "series"
)

// Titles is every name TMDB knows one film or show by: its own title, its original title,
// the alternative titles of EVERY country and the translated title of every language.
//
// It is deliberately broader than the alternative titles a movie or show keeps for
// library-wide matching (movieAltTitles, pickTMDBAliases): those name a title among every
// other one in the library, so a long tail of translations would only add wrong matches.
// These answer a narrower question — "is this download the film it was grabbed for?" — or
// supply the few languages the owner asked to match in.
type Titles struct {
	Title         string            `json:"title"`
	OriginalTitle string            `json:"original_title,omitempty"`
	Year          int               `json:"year,omitempty"`
	Alternatives  []AltTitle        `json:"alternatives,omitempty"`
	Translations  []TranslatedTitle `json:"translations,omitempty"`
}

// TranslatedTitle is a title's name in one language (TMDB's translations).
type TranslatedTitle struct {
	Lang    string `json:"lang"`              // ISO 639-1, lower case: "de"
	Country string `json:"country,omitempty"` // ISO 3166-1: "DE", "AT"
	Title   string `json:"title"`
}

// All is every distinct non-empty name in t: the title, the original title, every
// alternative and every translation.
func (t *Titles) All() []string {
	if t == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	add(t.Title)
	add(t.OriginalTitle)
	for _, a := range t.Alternatives {
		add(a.Title)
	}
	for _, tr := range t.Translations {
		add(tr.Title)
	}
	return out
}

// InLanguages is the translated titles in the given ISO 639-1 languages, in TMDB's order.
func (t *Titles) InLanguages(iso1 []string) []TranslatedTitle {
	if t == nil || len(iso1) == 0 {
		return nil
	}
	want := map[string]bool{}
	for _, l := range iso1 {
		want[strings.ToLower(l)] = true
	}
	var out []TranslatedTitle
	for _, tr := range t.Translations {
		if want[tr.Lang] && strings.TrimSpace(tr.Title) != "" {
			out = append(out, tr)
		}
	}
	return out
}

// LocalizedHit is a search result with its title in the language asked for.
type LocalizedHit struct {
	TMDBID int    `json:"tmdb_id"`
	Title  string `json:"title"`
	Year   int    `json:"year"`
}

// TitlesSource reads every name of a title, and searches by a title in another language.
// TMDB implements it; the movies and series services take one (SetTitlesSource) so the
// import gate and localized matching can be tested without TMDB.
type TitlesSource interface {
	Titles(ctx context.Context, media string, tmdbID int) (*Titles, error)
	SearchLocalized(ctx context.Context, media, query, lang string) ([]LocalizedHit, error)
}

// titlesTTL is how long a title's names are kept. They change rarely, and the import gate
// and the localized-titles backfill read them for the same few titles again and again.
const titlesTTL = 7 * 24 * time.Hour

// tmdbTitlesPath is the TMDB path prefix for a media kind.
func tmdbTitlesPath(media string) (string, error) {
	switch media {
	case TitlesMovie:
		return "/movie/", nil
	case TitlesSeries, "tv":
		return "/tv/", nil
	}
	return "", fmt.Errorf("tmdb: no titles for media %q", media)
}

// Titles reads every name of a film or show in one request (alternative_titles and
// translations appended to the bare detail call — no credits, no seasons), kept for
// titlesTTL in the disk cache under its own key so the Discover detail cache (v5) is
// untouched.
func (t *TMDB) Titles(ctx context.Context, media string, tmdbID int) (*Titles, error) {
	path, err := tmdbTitlesPath(media)
	if err != nil {
		return nil, err
	}
	if media == "tv" {
		media = TitlesSeries
	}
	key := "tmdb:titles:v1:" + media + ":" + strconv.Itoa(tmdbID)
	return swr(ctx, t.disk, key, titlesTTL, func(ctx context.Context) (*Titles, error) {
		q := url.Values{}
		q.Set("append_to_response", "alternative_titles,translations")
		body, err := t.get(ctx, path+strconv.Itoa(tmdbID), q)
		if err != nil {
			return nil, err
		}
		return parseTMDBTitles(body)
	})
}

// parseTMDBTitles reads a movie or TV detail body with alternative_titles and translations
// appended. Movies and TV differ in every field name: title/name, original_title/
// original_name, release_date/first_air_date, alternative_titles' "titles"/"results", and
// a translation's data.title/data.name.
func parseTMDBTitles(body []byte) (*Titles, error) {
	type alt struct {
		Country string `json:"iso_3166_1"`
		Title   string `json:"title"`
		Type    string `json:"type"`
	}
	var p struct {
		Title             string `json:"title"`
		Name              string `json:"name"`
		OriginalTitle     string `json:"original_title"`
		OriginalName      string `json:"original_name"`
		ReleaseDate       string `json:"release_date"`
		FirstAirDate      string `json:"first_air_date"`
		AlternativeTitles struct {
			Titles  []alt `json:"titles"`
			Results []alt `json:"results"`
		} `json:"alternative_titles"`
		Translations struct {
			Translations []struct {
				Lang    string `json:"iso_639_1"`
				Country string `json:"iso_3166_1"`
				Data    struct {
					Title string `json:"title"`
					Name  string `json:"name"`
				} `json:"data"`
			} `json:"translations"`
		} `json:"translations"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("tmdb: parse titles: %w", err)
	}
	first := func(a, b string) string {
		if s := strings.TrimSpace(a); s != "" {
			return s
		}
		return strings.TrimSpace(b)
	}
	t := &Titles{
		Title:         first(p.Title, p.Name),
		OriginalTitle: first(p.OriginalTitle, p.OriginalName),
		Year:          yearOf(first(p.ReleaseDate, p.FirstAirDate)),
	}
	for _, a := range append(p.AlternativeTitles.Titles, p.AlternativeTitles.Results...) {
		if title := strings.TrimSpace(a.Title); title != "" {
			t.Alternatives = append(t.Alternatives, AltTitle{Title: title, Country: strings.ToUpper(a.Country), Type: a.Type})
		}
	}
	for _, tr := range p.Translations.Translations {
		// An empty title means the language uses the original one; nothing new to match.
		if title := first(tr.Data.Title, tr.Data.Name); title != "" {
			t.Translations = append(t.Translations, TranslatedTitle{
				Lang: strings.ToLower(tr.Lang), Country: strings.ToUpper(tr.Country), Title: title,
			})
		}
	}
	return t, nil
}

// SearchLocalized searches TMDB for a film or show with the results' titles in lang (ISO
// 639-1): a library scan's folder named in the household's language finds its title by
// that name. Not cached — a scan asks each folder once. Adult results are dropped, as on
// every other search surface.
func (t *TMDB) SearchLocalized(ctx context.Context, media, query, lang string) ([]LocalizedHit, error) {
	path := "/search/movie"
	if media == TitlesSeries || media == "tv" {
		path = "/search/tv"
	}
	q := url.Values{}
	q.Set("query", query)
	q.Set("include_adult", "false")
	q.Set("language", lang)
	body, err := t.get(ctx, path, q)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Results []struct {
			ID           int    `json:"id"`
			Adult        bool   `json:"adult"`
			Title        string `json:"title"`
			Name         string `json:"name"`
			ReleaseDate  string `json:"release_date"`
			FirstAirDate string `json:"first_air_date"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("tmdb: parse search: %w", err)
	}
	out := make([]LocalizedHit, 0, len(payload.Results))
	for _, r := range payload.Results {
		title := r.Title
		if title == "" {
			title = r.Name
		}
		if r.Adult || adultfilter.Matches(title) {
			continue
		}
		date := r.ReleaseDate
		if date == "" {
			date = r.FirstAirDate
		}
		out = append(out, LocalizedHit{TMDBID: r.ID, Title: title, Year: yearOf(date)})
	}
	return out, nil
}

// minLocalKeyLen is the shortest translated-title key matched on. A one- or two-letter
// title ("M", "Up") names far too many folders and downloads to stand for one title.
const minLocalKeyLen = 3

// ReadLocalTitles reads a title's translated names in langs (ISO 639-1) for library-wide
// matching: one per distinct title key, leaving out any that key the same as the title's
// own name (own), which already matches.
func ReadLocalTitles(ctx context.Context, src TitlesSource, media string, tmdbID int, own string, langs []string) ([]TranslatedTitle, error) {
	t, err := src.Titles(ctx, media, tmdbID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{parser.TitleKey(own): true}
	out := []TranslatedTitle{}
	for _, tr := range t.InLanguages(langs) {
		k := parser.TitleKey(tr.Title)
		if len(k) < minLocalKeyLen || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, tr)
	}
	return out, nil
}

// CoversLangs reports whether the languages a title's translated names were read for
// (have) include every language asked for now (want). A language taken off the list needs
// no new read — its titles just stop counting.
func CoversLangs(have, want []string) bool {
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}

// LocalizedMatch identifies a scanned folder named in one of langs: TMDB is searched with
// its results' titles in each language, and the folder matches only when exactly ONE
// title is named so (title key equal; when the folder has a year, the title's is known and
// within one) across
// all of them. Two films sharing a translated title, or a year-less folder that fits
// several, stay unmatched for a manual pick rather than take a guess.
func LocalizedMatch(ctx context.Context, src TitlesSource, media, title string, year int, langs []string) (int, bool) {
	want := parser.TitleKey(title)
	if src == nil || len(langs) == 0 || len(want) < minLocalKeyLen {
		return 0, false
	}
	ids := map[int]bool{}
	for _, l := range langs {
		hits, err := src.SearchLocalized(ctx, media, title, l)
		if err != nil {
			continue // one language's search failing leaves the others to decide
		}
		for _, h := range hits {
			if parser.TitleKey(h.Title) != want {
				continue
			}
			// A folder with a year only takes a title whose year is known and fits: a hit
			// with no release date can't confirm it.
			if year != 0 && (h.Year == 0 || absYear(h.Year-year) > 1) {
				continue
			}
			ids[h.TMDBID] = true
		}
	}
	if len(ids) != 1 {
		return 0, false
	}
	for id := range ids {
		return id, true
	}
	return 0, false
}
