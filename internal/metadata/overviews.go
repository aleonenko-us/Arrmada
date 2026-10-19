package metadata

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// Overviews in another language (GitHub #35). TMDB's own overview is English; the
// translations append that display titles already read carries each language's overview
// beside its title, so the household's language can describe a title too. Which language
// is shown is the display-title setting's business (displaytitle.Policy.OverviewLang);
// this file only reads what TMDB has.

// TranslatedOverview is a title's overview in one language (TMDB's translations).
type TranslatedOverview struct {
	Lang     string `json:"lang"`              // ISO 639-1, lower case: "fr"
	Country  string `json:"country,omitempty"` // ISO 3166-1: "FR", "CA"
	Overview string `json:"overview"`
}

// tmdbOverviewTranslations is the overview part of a movie or TV detail call's
// "translations" append (movies and TV name it the same).
type tmdbOverviewTranslations struct {
	Translations []struct {
		Lang    string `json:"iso_639_1"`
		Country string `json:"iso_3166_1"`
		Data    struct {
			Overview string `json:"overview"`
		} `json:"data"`
	} `json:"translations"`
}

// overviews is every non-empty overview in tr whose language keep accepts. English is
// never kept: TMDB's own overview is the English one already.
func (tr tmdbOverviewTranslations) overviews(keep func(lang string) bool) []TranslatedOverview {
	var out []TranslatedOverview
	for _, x := range tr.Translations {
		lang := strings.ToLower(strings.TrimSpace(x.Lang))
		text := strings.TrimSpace(x.Data.Overview)
		if text == "" || lang == "" || lang == "en" || !keep(lang) {
			continue
		}
		out = append(out, TranslatedOverview{Lang: lang, Country: strings.ToUpper(strings.TrimSpace(x.Country)), Overview: text})
	}
	return out
}

// anyLang keeps every language (the Discover detail record, cached for any setting).
func anyLang(string) bool { return true }

// OverviewTitlesSource is a TitlesSource that can also read a title's overviews in one
// language with its names, from the same request. TMDB implements it.
type OverviewTitlesSource interface {
	TitlesIn(ctx context.Context, media string, tmdbID int, overviewLang string) (*Titles, error)
}

// withOverviews is a TitlesSource whose Titles keeps the overviews in lang.
type withOverviews struct {
	TitlesSource
	lang string
}

func (w withOverviews) Titles(ctx context.Context, media string, tmdbID int) (*Titles, error) {
	if src, ok := w.TitlesSource.(OverviewTitlesSource); ok {
		return src.TitlesIn(ctx, media, tmdbID, w.lang)
	}
	// A source that can't read overviews has none in any language: its answer counts as
	// read for lang (with nothing to show), so the backfill doesn't ask it again. A copy,
	// never the source's own (possibly shared) record.
	t, err := w.TitlesSource.Titles(ctx, media, tmdbID)
	if err != nil || t == nil {
		return t, err
	}
	cp := *t
	cp.OverviewLang, cp.Overviews = w.lang, nil
	return &cp, nil
}

// WithOverviews is src reading every title with its overviews in lang (ISO 639-1), so the
// import gate, the translated-titles backfill and the display names all share one cached
// request per title. Every answer says which language its overviews were read for
// (Titles.OverviewLang). src itself when lang is "".
func WithOverviews(src TitlesSource, lang string) TitlesSource {
	if src == nil || strings.TrimSpace(lang) == "" {
		return src
	}
	return withOverviews{TitlesSource: src, lang: strings.ToLower(strings.TrimSpace(lang))}
}

// TrendingOverviews is this week's trending titles' overviews in one language (lang is
// TMDB's language parameter: "fr", "pt-BR"), keyed "movie:123" / "series:123", for the
// Discover hero. One more request per list refresh, cached like the list itself; a title
// TMDB has no overview for in that language is simply missing from the map. The list's
// own titles stay English: only the overviews are taken from this one.
func (t *TMDB) TrendingOverviews(ctx context.Context, media, lang string) (map[string]string, error) {
	seg := "all"
	switch {
	case media == "movie":
		seg = "movie"
	case tvish(media):
		seg = "tv"
	}
	q := url.Values{}
	q.Set("language", lang)
	items, err := t.cachedDiscoverListN(ctx, "/trending/"+seg+"/week", q, "", discoverListTTL, browseMinVotes, true, 2)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(items))
	for _, it := range items {
		if o := strings.TrimSpace(it.Overview); o != "" {
			out[it.MediaType+":"+strconv.Itoa(it.TMDBID)] = o
		}
	}
	return out, nil
}
