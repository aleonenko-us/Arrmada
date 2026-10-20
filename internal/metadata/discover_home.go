package metadata

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"time"
)

// "Just out at home": films whose first digital, physical or TV release anywhere was in
// the last two weeks, the newest first. TMDB's /discover/movie finds the candidates (a
// home release of any country in the window); each is then checked against its own
// release dates (releaseWindow, the rule a grab uses), so a film that went digital months
// ago somewhere and only reached DVD in one country this week is left out.

// HomeReleases is the optional capability behind the "Just out at home" row.
type HomeReleases interface {
	JustOutAtHome(ctx context.Context) ([]DiscoverItem, error)
}

const (
	justOutDays       = 14            // how recent a first home release counts as "just out"
	justOutCandidates = 30            // candidates checked per build (one release-dates read each)
	justOutTTL        = 6 * time.Hour // the row is the same for everyone and moves slowly
	justOutMinVotes   = 10            // the browse floor's purpose (adult catalogue filler) at a level new films clear
)

// JustOutAtHome is the row, built at most every six hours (the disk cache serves the last
// one while a fresh one is made). The key carries the day, so the window moves daily.
func (t *TMDB) JustOutAtHome(ctx context.Context) ([]DiscoverItem, error) {
	now := time.Now()
	key := "justout:" + now.Format(time.DateOnly)
	t.discMu.Lock()
	if e, ok := t.discCache[key]; ok && now.Before(e.exp) && e.page == nil {
		items := e.items
		t.discMu.Unlock()
		return items, nil
	}
	t.discMu.Unlock()
	items, err := swr(ctx, t.disk, "tmdb:"+key, justOutTTL, func(ctx context.Context) ([]DiscoverItem, error) {
		return t.justOut(ctx, now)
	})
	if err != nil {
		return nil, err
	}
	t.storeDiscover(key, discoverCacheEntry{items: items, added: now, exp: now.Add(justOutTTL)})
	return items, nil
}

// justOut builds the row for the day of now.
func (t *TMDB) justOut(ctx context.Context, now time.Time) ([]DiscoverItem, error) {
	from := now.AddDate(0, 0, -justOutDays).Format(time.DateOnly)
	to := now.Format(time.DateOnly)
	q := url.Values{}
	q.Set("with_release_type", "4|5|6")
	q.Set("release_date.gte", from)
	q.Set("release_date.lte", to)
	q.Set("sort_by", "popularity.desc")
	q.Set("include_adult", "false")
	q.Set("vote_count.gte", strconv.Itoa(justOutMinVotes))
	cands, err := t.discoverListPages(ctx, "/discover/movie", q, "movie", justOutMinVotes, true, 2)
	if err != nil {
		return nil, err
	}
	if len(cands) > justOutCandidates {
		cands = cands[:justOutCandidates]
	}
	out := make([]DiscoverItem, 0, len(cands))
	for _, c := range cands {
		home, err := t.homeReleaseDate(ctx, c.TMDBID)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue // one film TMDB can't answer for just isn't in the row
		}
		if !justOut(home, from, to) {
			continue
		}
		c.HomeRelease = home
		out = append(out, c)
	}
	// Newest home release first; TMDB's popularity order breaks ties (a stable sort).
	sort.SliceStable(out, func(i, j int) bool { return out[i].HomeRelease > out[j].HomeRelease })
	return out, nil
}

// justOut reports whether a first home release day falls in [from, to].
func justOut(home, from, to string) bool {
	return home != "" && home >= from && home <= to
}

// homeReleaseDate is a film's first home release anywhere (releaseWindow), "" when TMDB
// lists none.
func (t *TMDB) homeReleaseDate(ctx context.Context, tmdbID int) (string, error) {
	body, err := t.get(ctx, "/movie/"+strconv.Itoa(tmdbID)+"/release_dates", url.Values{})
	if err != nil {
		return "", err
	}
	var payload struct {
		Results []tmdbReleaseDates `json:"results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	home, _ := releaseWindow(payload.Results)
	return home, nil
}
