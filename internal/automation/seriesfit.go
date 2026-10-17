package automation

import (
	"context"

	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/parser"
	"github.com/tristenlammi/arrmada/internal/series"
)

// Searches judge releases by series.SearchFit: FitRelease, widened by a show's stored
// translated titles when Settings → Library → "Also match titles in" is set (see
// series/searchtitles.go for the safeguards). Import routing keeps its own rule
// (MatchRelease and the grab's KnownAs check), which already takes what this accepts.

// seriesFitKey carries one search's SearchFit in its context.
type seriesFitKey struct{}

// withSeriesFit gives a search one SearchFit, so however many releases and queries it
// judges, the library is read at most once for it — and the grab step decides exactly as
// the match before it did.
func (c *Coordinator) withSeriesFit(ctx context.Context) context.Context {
	if c.series == nil {
		return ctx
	}
	if _, ok := ctx.Value(seriesFitKey{}).(*series.SearchFit); ok {
		return ctx
	}
	return context.WithValue(ctx, seriesFitKey{}, c.series.SearchFit(ctx))
}

// withFit attaches fit (RSS's, over the snapshot it matched against) to ctx.
func withFit(ctx context.Context, fit *series.SearchFit) context.Context {
	return context.WithValue(ctx, seriesFitKey{}, fit)
}

// seriesFit is the search rule for ctx: the one attached, else a fresh one. nil (the
// setting empty, or no series service) is FitRelease.
func (c *Coordinator) seriesFit(ctx context.Context) *series.SearchFit {
	if f, ok := ctx.Value(seriesFitKey{}).(*series.SearchFit); ok {
		return f
	}
	if c.series == nil {
		return nil
	}
	return c.series.SearchFit(ctx)
}

// searchIdentity is seriesIdentity for a search: whether a parsed release is this show by
// fit, and if not, what disagreed.
func searchIdentity(fit *series.SearchFit, p parser.Release, s series.Series) (bool, string) {
	f := fit.Fit(p, s)
	return f.OK, f.Why
}

// searchTitleMatches is seriesTitleMatches for a search.
func searchTitleMatches(fit *series.SearchFit, relTitle string, s series.Series) bool {
	ok, _ := searchIdentity(fit, parser.Parse(relTitle), s)
	return ok
}

// translatedQueryText is the text of the one extra query a search makes by the show's
// translated title (SearchFit.QueryTitle), cleaned like every query; "" when there's none
// or it would only repeat one of have.
func translatedQueryText(fit *series.SearchFit, s series.Series, have ...string) string {
	q := indexerQuery(fit.QueryTitle(s))
	if q == "" || q == indexerQuery(s.Title) {
		return ""
	}
	for _, h := range have {
		if q == h {
			return ""
		}
	}
	return q
}

// searchTranslated is the sweep's and Search now's follow-up for the episodes the title,
// alias and season queries left uncovered: ONE text query by the show's translated title
// — a German tracker lists Money Heist only as "Haus des Geldes" — for the one season
// still missing something when it's one (a standard show), else broad. Text only: an
// indexer that takes the show's ids would answer the id search again and ignore the name.
// It goes through the indexer throttle like every query, and costs nothing for a show
// with no translated title worth asking by, or when nothing is left to find.
func (c *Coordinator) searchTranslated(ctx context.Context, s series.Series, remaining []epKey) (grabbed int, left []epKey, returned int) {
	if len(remaining) == 0 {
		return 0, remaining, 0
	}
	text := translatedQueryText(c.seriesFit(ctx), s)
	if text == "" {
		return 0, remaining, 0
	}
	q := indexer.SearchQuery{Text: text, MediaType: indexer.MediaSeries, Limit: 100}
	if seasons := seasonsOf(setOf(remaining)); len(seasons) == 1 && !s.IsAnime() {
		for sn := range seasons {
			q.Season = sn
		}
	}
	res, err := c.search(ctx, q)
	if err != nil {
		// The title search already answered; this one only adds to it.
		c.log.Info("series: translated-title search failed", "series", s.Title, "query", text, "err", err)
		return 0, remaining, 0
	}
	c.log.Info("series: searched by translated title", "series", s.Title, "query", text,
		"season", q.Season, "returned", len(res.Releases))
	notesFrom(ctx).consider(res.Releases)
	if len(res.Releases) == 0 {
		return 0, remaining, 0
	}
	grabbed, left = c.grabSeriesLimited(ctx, s, res.Releases, remaining)
	return grabbed, left, len(res.Releases)
}

// translatedScopeQuery is the interactive list's (and quick Grab buttons') query by the
// show's translated title, scoped like the main query: season and episode as tvsearch
// parameters for a standard show, broad for anime and the whole show. ok is false when
// there's none to make or it repeats one of have.
func translatedScopeQuery(fit *series.SearchFit, s series.Series, season, episode int, have []indexer.SearchQuery) (indexer.SearchQuery, bool) {
	texts := make([]string, 0, len(have))
	for _, q := range have {
		texts = append(texts, q.Text)
	}
	text := translatedQueryText(fit, s, texts...)
	if text == "" {
		return indexer.SearchQuery{}, false
	}
	q := indexer.SearchQuery{Text: text, MediaType: indexer.MediaSeries, Limit: 400}
	if !s.IsAnime() && season > 0 {
		q.Season, q.Episode = season, episode
	}
	return q, true
}
