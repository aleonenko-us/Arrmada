package automation

import (
	"context"
	"log/slog"
)

// Titles added in bulk — a Radarr or Sonarr import — carry a "paced" mark (migration 0323).
// The missing sweeps search every monitored title without a file on each run, so an import
// of five hundred wanted films would otherwise be five hundred searches, and as many grabs,
// on the next run. Each sweep searches at most PacedSearchesPerSweep marked titles and
// clears the mark on the ones it searched; the rest wait for the following runs. Titles
// without the mark are searched exactly as before.

// PacedSearchesPerSweep is how many marked titles one missing sweep searches: with movies
// swept every 5 minutes that's at most 120 imported films an hour, shows every 15 minutes 40.
const PacedSearchesPerSweep = 10

// pacer is one sweep's allowance of marked titles.
type pacer struct {
	marked map[int64]bool
	left   int
	clear  func(ctx context.Context, id int64) error
	log    *slog.Logger
	kind   string
}

func newPacer(marked map[int64]bool, clear func(ctx context.Context, id int64) error, log *slog.Logger, kind string) *pacer {
	return &pacer{marked: marked, left: PacedSearchesPerSweep, clear: clear, log: log, kind: kind}
}

// hold reports whether the sweep must leave title id for a later run: it's marked and this
// run's allowance is spent. A marked title it lets through spends one.
func (p *pacer) hold(id int64) bool {
	if p == nil || !p.marked[id] {
		return false
	}
	if p.left <= 0 {
		return true
	}
	p.left--
	return false
}

// searched clears the mark of a title the sweep has now searched: from here on it is an
// ordinary wanted title.
func (p *pacer) searched(ctx context.Context, id int64) {
	if p == nil || !p.marked[id] {
		return
	}
	delete(p.marked, id)
	if err := p.clear(ctx, id); err != nil && p.log != nil {
		p.log.Warn("automation: couldn't clear an imported title's paced-search mark", "kind", p.kind, "id", id, "err", err)
	}
}
