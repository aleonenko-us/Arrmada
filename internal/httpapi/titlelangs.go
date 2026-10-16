package httpapi

import (
	"context"
	"time"

	"github.com/tristenlammi/arrmada/internal/safego"
)

// titleBackfillEvery is the pace of the translated-titles backfill: one title every two
// seconds, as the startup alternative-titles backfill, so a large library never bursts
// TMDB.
const titleBackfillEvery = 2 * time.Second

// titleLanguagesChanged reads the translated titles of the newly saved "Also match titles
// in" languages for every movie and show not yet read for them — a follow-up of the save,
// not work the page waits on or follows. It runs on the run group (cancelled at shutdown)
// rather than as a job: a pass over a large library takes hours at this pace and would
// hold a job lane all that time. Movies, then shows; each backfill runs one pass at a time
// and a save while one runs makes it go round again, so repeated saves don't stack up.
func (a *api) titleLanguagesChanged() {
	run := func(ctx context.Context) {
		if a.deps.Movies != nil {
			if n, err := a.deps.Movies.BackfillAltTitles(ctx, titleBackfillEvery); err != nil && ctx.Err() == nil {
				a.deps.Log.Warn("movies: translated titles backfill failed", "filled", n, "err", err)
			} else if n > 0 {
				a.deps.Log.Info("movies: read translated titles", "movies", n)
			}
		}
		if a.deps.Series != nil {
			if n, err := a.deps.Series.BackfillLocalTitles(ctx, titleBackfillEvery); err != nil && ctx.Err() == nil {
				a.deps.Log.Warn("series: translated titles backfill failed", "filled", n, "err", err)
			} else if n > 0 {
				a.deps.Log.Info("series: read translated titles", "series", n)
			}
		}
	}
	if g := a.deps.RunGroup; g != nil {
		g.Go("titles: translated titles backfill", run)
		return
	}
	safego.Go(a.deps.Log, "titles: translated titles backfill", func() { run(a.runCtx()) })
}
