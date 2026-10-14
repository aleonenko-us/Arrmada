package automation

import (
	"context"
	"fmt"
	"strings"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/parser"
	"github.com/tristenlammi/arrmada/internal/series"
)

// Stale grab rows, and why the stall check settles them without a search.
//
// The update to a build with fail-over on by default met months of rows still 'grabbed':
// series grabs were never stall-checked before, and an older build re-grabbed a release
// every day its import couldn't be matched, writing a new row each time. Their torrents had
// long left the client — removed by Arrmada at their seed goal, by hand, or never imported
// at all — and every row failed over on its own, three every two minutes for hours: The
// Expanse's complete pack nine times over, each with a blocklist line, a "failed" history
// line and a search, for episodes that were already in the library.
//
// So, before a vanished download is failed over:
//   - rows for the same download of the same title are one decision (collapseDuplicateGrabs);
//   - if another row for it was already settled — seeded, imported, removed, failed over —
//     the stale one takes that outcome quietly, and one waiting in Review is left to the
//     review (settleVanished);
//   - if what it was for is already in the library, it closes as imported (stallTarget.landed;
//     movies, books and music test that before judging at all).
//
// None of these searches, so none spends the per-check fail-over budget, and DetectStalled
// says once per check how many it closed. The evidence rule is untouched: nothing here acts
// until the torrent has been gone for stallEvidenceGrace.

// collapseDuplicateGrabs folds the pending grabs that record one download of one title onto
// a single grab: same item and version, and the same info hash or the same release name.
// The newest row represents the rest (its Dups); groups keep the order of their oldest row,
// so the backlog is still worked oldest first.
func collapseDuplicateGrabs(pending []grab) []grab {
	parent := make([]int, len(pending))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}
	first := map[string]int{}
	for i, g := range pending {
		item := fmt.Sprintf("%s|%d|%d|", g.MediaType, g.MovieID, g.VersionID)
		var keys []string
		if g.InfoHash != "" {
			keys = append(keys, item+"h:"+strings.ToLower(g.InfoHash))
		}
		if n := normRelease(g.Title); n != "" {
			keys = append(keys, item+"n:"+n)
		}
		for _, k := range keys {
			j, seen := first[k]
			if !seen {
				first[k] = i
				continue
			}
			if a, b := find(j), find(i); a != b {
				parent[b] = a
			}
		}
	}
	members := map[int][]int{}
	var order []int
	for i := range pending {
		r := find(i)
		if _, ok := members[r]; !ok {
			order = append(order, r)
		}
		members[r] = append(members[r], i)
	}
	out := make([]grab, 0, len(order))
	for _, r := range order {
		group := members[r]
		rep := group[0]
		for _, m := range group {
			if pending[m].ID > pending[rep].ID {
				rep = m
			}
		}
		g := pending[rep]
		g.Dups = nil
		for _, m := range group {
			if m != rep {
				g.Dups = append(g.Dups, pending[m])
			}
		}
		out = append(out, g)
	}
	return out
}

// findQueuedGroup is findQueued for a grab and its duplicates: the torrent any of them
// recorded. One live copy is enough for the download not to have vanished.
func findQueuedGroup(queue []download.Item, g grab) (download.Item, bool) {
	if it, ok := findQueued(queue, g); ok {
		return it, true
	}
	for _, d := range g.Dups {
		if it, ok := findQueued(queue, d); ok {
			return it, true
		}
	}
	return download.Item{}, false
}

// settleVanished settles a grab whose torrent has left the client the way another row for
// the same download of the same title was already settled, and reports whether it did
// (or must wait). That other row is what Arrmada knows about the torrent: it seeded out and
// was removed at its goal, was imported, was removed by the user, or was failed over
// already. None of those is a download that disappeared, and failing it over again would
// blocklist a release that did its job and search for what it already brought.
//
// A row waiting in Review means the download finished and a person is deciding about it:
// the review closes every row for it, so nothing is done meanwhile. An unreadable table
// also does nothing this check — the fail-over it would otherwise allow can't be undone.
func (c *Coordinator) settleVanished(ctx context.Context, g grab, tick *stallTick) bool {
	twins, err := c.grabsWhere(ctx, `NOT (`+stallWatchWhere+`) AND media_type = ? AND movie_id = ? AND version_id = ?`,
		g.MediaType, g.MovieID, g.VersionID)
	if err != nil {
		c.log.Warn("automation: couldn't read how the grab's duplicates settled — leaving it for the next check",
			"release", g.Title, "err", err)
		return true
	}
	status := ""
	for i := len(twins) - 1; i >= 0; i-- { // newest first: the latest word on the download
		if !twinOf(g, twins[i]) {
			continue
		}
		if twins[i].Status == grabStatusHeld {
			return true
		}
		if status == "" {
			status = twins[i].Status
		}
	}
	if status == "" {
		return false
	}
	c.closeStale(ctx, g, status, tick)
	return true
}

// twinOf reports whether row t records the same download as grab g or any duplicate folded
// onto it, by hash or by release name. Looser than sameDownload on purpose: g's torrent is
// gone either way, and a release of that name that already settled is reason enough not to
// fail it over a second time.
func twinOf(g grab, t grab) bool {
	for _, m := range append([]grab{g}, g.Dups...) {
		if m.InfoHash != "" && strings.EqualFold(m.InfoHash, t.InfoHash) {
			return true
		}
		if n := normRelease(m.Title); n != "" && n == normRelease(t.Title) {
			return true
		}
	}
	return false
}

// closeStale closes grab g (and its duplicates) without a search, and counts them for the
// check's summary line.
func (c *Coordinator) closeStale(ctx context.Context, g grab, status string, tick *stallTick) {
	c.closeGrab(ctx, g, status)
	tick.noteClosed(g)
}

// seriesReleaseLanded reports whether a series release has nothing left to bring: none of
// the episodes it covers is still wanted, and at least one of them is on disk. That is a
// vanished download whose show already has what it was for — a pack imported by an older
// build that never closed its grab, say.
//
// It only ever settles a download that has left the client. An in-flight grab whose episodes
// already have files may be an upgrade on its way, and closing it would hand its torrent to
// seed cleanup before the import.
func (c *Coordinator) seriesReleaseLanded(ctx context.Context, s series.Series, title string) bool {
	r := parser.Parse(title)
	wanted, _ := wantedEpisodes(s)
	if len(c.coveredByFor(ctx, s, r, setOf(wanted))) > 0 {
		return false
	}
	have := map[epKey]bool{}
	for _, sn := range s.Seasons {
		for _, e := range sn.Episodes {
			if e.HasFile {
				have[epKey{e.SeasonNumber, e.EpisodeNumber}] = true
			}
		}
	}
	return len(c.coveredByFor(ctx, s, r, have)) > 0
}
