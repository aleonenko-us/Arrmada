package httpapi

import (
	"context"
	"strconv"
	"strings"
	"time"
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

// trendingOverviewWait bounds the localized read: the hero is the first thing on Discover,
// and a slow TMDB answer must not hold it — the English overviews show instead.
const trendingOverviewWait = 2 * time.Second

// localizeTrendingOverviews sets each trending card's DisplayOverview to its overview in
// the setting's language, when it shows one and TMDB has it. A regional choice (fr-CA)
// falls back to the base language (fr) for the cards its own list leaves without one, as
// the detail sheet does — an extra read only for regional choices, and only when some
// card is missing. Best effort: a failed or slow read leaves the English overviews, never
// the row.
func (a *api) localizeTrendingOverviews(ctx context.Context, media string, cards []discoverCard) {
	p := a.displayPolicy(ctx)
	lang := p.TMDBLanguage()
	src, ok := a.deps.Discovery.(trendingOverviewSource)
	if lang == "" || !ok || len(cards) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, trendingOverviewWait)
	defer cancel()
	missing := a.applyTrendingOverviews(ctx, src, media, lang, cards)
	if base := p.OverviewLang(); missing && base != lang {
		a.applyTrendingOverviews(ctx, src, media, base, cards)
	}
}

// applyTrendingOverviews reads the trending overviews in lang and sets them on the cards
// that have none yet. It reports whether any card is still without one.
func (a *api) applyTrendingOverviews(ctx context.Context, src trendingOverviewSource, media, lang string, cards []discoverCard) (missing bool) {
	byKey, err := src.TrendingOverviews(ctx, media, lang)
	if err != nil {
		a.deps.Log.Debug("discover: couldn't read trending overviews in the display language; showing English", "lang", lang, "err", err)
		return false
	}
	for i := range cards {
		c := &cards[i]
		if c.DisplayOverview != "" {
			continue
		}
		if o := strings.TrimSpace(byKey[c.MediaType+":"+strconv.Itoa(c.TMDBID)]); o != "" && o != c.Overview {
			c.DisplayOverview = o
		} else {
			missing = true
		}
	}
	return missing
}
