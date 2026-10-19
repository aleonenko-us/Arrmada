package httpapi

import (
	"context"
	"strconv"
	"strings"
)

// Overviews in the household's language on Discover (GitHub #35). Settings → Library →
// "Show titles as" set to a language describes titles in it too (displaytitle,
// Policy.OverviewLang): the detail sheet picks its overview from the record's own
// translations (handleMediaDetail, no extra TMDB request), and the hero — the one place a
// row's cards show their overview — reads the trending list once more in that language.
// Titles and the card's own overview stay TMDB's English ones either way, so requests,
// matching and scripts are untouched.

// trendingOverviewSource reads the trending list's overviews in one language. TMDB
// implements it; a Discovery provider without it simply keeps English overviews.
type trendingOverviewSource interface {
	TrendingOverviews(ctx context.Context, media, lang string) (map[string]string, error)
}

// localizeTrendingOverviews sets each trending card's DisplayOverview to its overview in
// the setting's language, when it shows one and TMDB has it. Best effort: a failed read
// leaves the English overviews, never the row.
func (a *api) localizeTrendingOverviews(ctx context.Context, media string, cards []discoverCard) {
	lang := a.displayPolicy(ctx).TMDBLanguage()
	src, ok := a.deps.Discovery.(trendingOverviewSource)
	if lang == "" || !ok || len(cards) == 0 {
		return
	}
	byKey, err := src.TrendingOverviews(ctx, media, lang)
	if err != nil {
		a.deps.Log.Debug("discover: couldn't read trending overviews in the display language; showing English", "lang", lang, "err", err)
		return
	}
	for i := range cards {
		c := &cards[i]
		if o := strings.TrimSpace(byKey[c.MediaType+":"+strconv.Itoa(c.TMDBID)]); o != "" && o != c.Overview {
			c.DisplayOverview = o
		}
	}
}
