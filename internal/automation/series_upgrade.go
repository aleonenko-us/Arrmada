package automation

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/parser"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/series"
)

// The series upgrade sweep's reach. One title query per show returns one capped page of
// mostly recent single episodes, so a finished show — whose upgrades mostly appear as
// BluRay season packs — and an anime numbered absolutely were never upgraded at all.

// maxUpgradeSeasonQueries is how many seasons one upgrade sweep asks for by number, per
// show. Smaller than the missing sweep's maxSeasonQueries: upgrades run for every season
// that isn't at its target, not only the ones with gaps, and nothing is missing while
// they wait. The cursor walks the rest over the following sweeps.
const maxUpgradeSeasonQueries = 4

// maxEpisodeUpgradeSpan is the most episodes a release may hold and still be one episode's
// upgrade: a double episode ("S01E01E02") is, a batch isn't.
const maxEpisodeUpgradeSpan = 2

// rotateFrom reorders seasons to start at the first one at or past cursor, wrapping. The
// cursor is a SEASON NUMBER, not an index (see rotateSeasons).
func rotateFrom(seasons []int, cursor int) []int {
	start := 0
	for i, sn := range seasons {
		if sn >= cursor {
			start = i
			break
		}
	}
	return append(append([]int{}, seasons[start:]...), seasons[:start]...)
}

// upgradeSeasonWindow is the seasons this sweep queries, and the cursor the next sweep
// starts from (moved is false when every season fits and the cursor stays put).
func upgradeSeasonWindow(seasons []int, cursor, limit int) (window []int, next int, moved bool) {
	if len(seasons) <= limit {
		return seasons, cursor, false
	}
	out := rotateFrom(seasons, cursor)
	return out[:limit], out[limit], true
}

// upgradeSeasonQueries picks this sweep's seasons and advances the show's cursor.
func (c *Coordinator) upgradeSeasonQueries(ctx context.Context, s series.Series, seasons []int) []int {
	window, next, moved := upgradeSeasonWindow(seasons, c.series.UpgradeCursor(ctx, s.ID), maxUpgradeSeasonQueries)
	if moved {
		c.series.SetUpgradeCursor(ctx, s.ID, next)
	}
	return window
}

// episodeReleaseIndex maps each episode to the single- and multi-episode releases that hold
// it, resolved the way the grab side resolves them (releaseEpisodes): an alias' own
// numbering, anime absolute and per-cour numbers, then plain SxxExx. Built once per sweep,
// since the anime resolver isn't free and every episode asks.
//
// A release holding more than maxEpisodeUpgradeSpan episodes is left out: an anime batch
// ("[Grp] Show - 01-12") is a pack in all but name, and one episode's upgrade mustn't
// download a dozen.
func (c *Coordinator) episodeReleaseIndex(ctx context.Context, s series.Series, idx runtimeIndex, byName map[string]indexer.Release) map[epKey][]string {
	out := map[epKey][]string{}
	for name := range byName {
		p := parser.Parse(name)
		if p.Kind() != parser.KindEpisode {
			continue
		}
		keys := c.releaseEpisodes(ctx, s, idx, p)
		if len(keys) > maxEpisodeUpgradeSpan {
			continue
		}
		for _, k := range keys {
			out[k] = append(out[k], name)
		}
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

// packUpgradeDecision decides whether a season pack is worth grabbing as an upgrade.
// withFiles are the episodes it covers that already have a file; verdict says, for each,
// whether the pack's copy would be an upgrade and whether it would be a downgrade. It
// takes the pack when at least half of them would be upgraded and none would be
// downgraded. The import gate still judges every file on arrival, so only the episodes the
// pack really beats are replaced.
func packUpgradeDecision(withFiles []epKey, verdict func(epKey) (better, worse bool)) (ok bool, upgraded int) {
	if len(withFiles) == 0 {
		return false, 0
	}
	for _, k := range withFiles {
		better, worse := verdict(k)
		if worse {
			return false, 0
		}
		if better {
			upgraded++
		}
	}
	return upgraded > 0 && upgraded*2 >= len(withFiles), upgraded
}

// seasonFullyAired reports whether every episode TMDB lists for a season has aired. A pack
// of a season still airing is missing its newest episodes and will be followed by a fuller
// one, so it's never an upgrade.
func seasonFullyAired(s series.Series, season int) bool {
	for _, sn := range s.Seasons {
		if sn.SeasonNumber != season {
			continue
		}
		if len(sn.Episodes) == 0 {
			return false
		}
		for _, e := range sn.Episodes {
			if !aired(e.AirDate) {
				return false
			}
		}
		return true
	}
	return false
}

// fileResRank is the resolution rank of the file on disk: what Convert probed, else the
// release it came from, and for a converted file at least what it was converted from.
// 0 when nothing says.
func fileResRank(cur quality.CurrentFile) int {
	p := parser.Parse(cur.Release)
	if cur.Facts != nil {
		p = quality.ReleaseWithFacts(p, *cur.Facts)
	}
	rank := parser.ResolutionRank(p.Resolution)
	if strings.TrimSpace(cur.OrigRelease) != "" {
		rank = max(rank, parser.ResolutionRank(parser.Parse(cur.OrigRelease).Resolution))
	}
	return rank
}

// seriesGrabbedTitles is every release ever grabbed for a show, by normTitle. An upgrade —
// a season pack or one episode's release — is offered once: after a pack imports, the
// episodes it didn't beat keep their files, and after an episode's upgrade arrives the
// import gate may still keep the old file (or find the download already imported). Either
// way the same release mustn't come round again every sweep: Reacher's S04E06 was grabbed
// ten times, every six hours, by an older build.
func (c *Coordinator) seriesGrabbedTitles(ctx context.Context, seriesID int64) (map[string]bool, error) {
	rows, err := c.db.QueryContext(ctx,
		`SELECT title FROM grabs WHERE movie_id = ? AND media_type = 'series'`, seriesID)
	if err != nil {
		return nil, fmt.Errorf("grab history unreadable: %w", err)
	}
	defer rows.Close()
	set := map[string]bool{}
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			return nil, fmt.Errorf("grab history unreadable: %w", err)
		}
		set[normTitle(title)] = true
	}
	return set, rows.Err()
}

// packUpgrades finds, for each season in seasons, the season pack that would upgrade the
// most of it (packUpgradeDecision). Only single-season packs of fully aired seasons, never
// one grabbed before (before, seriesGrabbedTitles) or one an episode was already imported
// or converted from.
func (c *Coordinator) packUpgrades(ctx context.Context, s series.Series, idx runtimeIndex, profile string, byName map[string]indexer.Release, seasons map[int]bool, before map[string]bool) []episodeUpgrade {
	eps := map[epKey]series.Episode{}
	for _, sn := range s.Seasons {
		for _, e := range sn.Episodes {
			eps[epKey{e.SeasonNumber, e.EpisodeNumber}] = e
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	best := map[int]episodeUpgrade{}
	for _, name := range names {
		p := parser.Parse(name)
		if p.Kind() != parser.KindSeasonPack || before[normTitle(name)] {
			continue
		}
		keys := c.releaseEpisodes(ctx, s, idx, p)
		season, one := singleSeason(keys)
		if !one || !seasons[season] || !seasonFullyAired(s, season) {
			continue
		}
		var withFiles []epKey
		have := false
		for _, k := range keys {
			e := eps[k]
			if !e.HasFile {
				continue
			}
			withFiles = append(withFiles, k)
			if sameRelease(name, e.SourceRelease) || sameRelease(name, e.ConvertedFromRelease) {
				have = true // its files are already here (or were, before Convert)
			}
		}
		if have {
			continue
		}
		cand := c.newSeriesCandidate(ctx, s, idx, byName[name])
		packRank := parser.ResolutionRank(cand.Release.Resolution)
		ok, n := packUpgradeDecision(withFiles, func(k epKey) (better, worse bool) {
			e := eps[k]
			cur := c.currentEpisodeFile(ctx, e)
			if r := fileResRank(cur); r > 0 && packRank < r {
				worse = true
			}
			// A held episode, an unmonitored one, or one with no recorded release to beat
			// counts against the pack: it has a file the pack won't replace.
			if !e.Monitored || e.UpgradeHold || e.SourceRelease == "" {
				return false, worse
			}
			_, better = c.upgradePick(ctx, profile, cur, []quality.Candidate{cand}) // notes the gain for the grab record
			return better, worse
		})
		if !ok {
			continue
		}
		if cur, seen := best[season]; !seen || n > cur.upgraded {
			best[season] = episodeUpgrade{season: season, pick: cand, pack: true, upgraded: n, of: len(withFiles)}
		}
	}
	out := make([]episodeUpgrade, 0, len(best))
	for _, sn := range sortedSeasons(seasonSet(best)) {
		out = append(out, best[sn])
	}
	return out
}

// untried is names without the releases grabbed for the show before (seriesGrabbedTitles).
func untried(names []string, before map[string]bool) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if !before[normTitle(n)] {
			out = append(out, n)
		}
	}
	return out
}

// singleSeason is the one season every key is in; false for none or several.
func singleSeason(keys []epKey) (int, bool) {
	if len(keys) == 0 {
		return 0, false
	}
	for _, k := range keys[1:] {
		if k.season != keys[0].season {
			return 0, false
		}
	}
	return keys[0].season, keys[0].season > 0
}

// sameRelease reports whether two release names are the same release, codec aside (a
// converted file's stamped name differs only there).
func sameRelease(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(a, b) || parser.WithoutCodec(a) == parser.WithoutCodec(b)
}

func seasonSet[T any](m map[int]T) map[int]bool {
	out := make(map[int]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}
