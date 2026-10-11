package httpapi

import (
	"context"
	"sync"
	"time"
)

// Activity → Wanted rebuilds every row from the libraries, the search history and the
// queue — not something to redo for each search.finished of a sweep, from each open tab.
// The answer is kept for up to a minute and rebuilt sooner only once the pipeline has
// moved (a grab recorded or settled, a review, a stored search attempt:
// Coordinator.WantedVersion), and then at most every few seconds, so a sweep finishing
// dozens of searches costs a handful of rebuilds. Library edits (a title added or
// unmonitored) don't move the version: the minute is their bound, which the page's own
// refresh after an action covers in practice.

const (
	wantedCacheTTL     = time.Minute
	wantedRebuildEvery = 5 * time.Second
)

// wantedCache is the last full Wanted answer. Its zero value is ready to use.
type wantedCache struct {
	mu     sync.Mutex
	lists  *wantedLists
	ver    int64
	at     time.Time
	mods   [2]bool // Books and Music on when it was built: switching one on lists its titles at once
	builds int     // how many times it was rebuilt (tests)
	// versionFn replaces Coordinator.WantedVersion in tests.
	versionFn func() int64
}

func (a *api) wantedVersion() int64 {
	if a.wanted.versionFn != nil {
		return a.wanted.versionFn()
	}
	if a.deps.Automation == nil {
		return 0
	}
	return a.deps.Automation.WantedVersion()
}

// cachedWanted is buildWanted over every kind, from the cache while it holds.
func (a *api) cachedWanted(ctx context.Context) wantedLists {
	ver := a.wantedVersion()
	mods := [2]bool{a.deps.Books != nil && a.booksEnabled(ctx), a.deps.Music != nil && a.musicEnabled(ctx)}
	now := time.Now()
	c := &a.wanted
	c.mu.Lock()
	cur, at, curVer, curMods := c.lists, c.at, c.ver, c.mods
	c.mu.Unlock()
	if cur != nil && curMods == mods {
		age := now.Sub(at)
		if age < wantedCacheTTL && (curVer == ver || age < wantedRebuildEvery) {
			return *cur
		}
	}
	lists := a.buildWanted(ctx, nil)
	c.mu.Lock()
	c.builds++
	// An answer built while the client couldn't be read says "unknown" everywhere; it
	// isn't kept, so the client coming back shows at once.
	if lists.QueueKnown {
		c.lists, c.at, c.ver, c.mods = &lists, now, ver, mods
	} else {
		c.lists = nil
	}
	c.mu.Unlock()
	return lists
}

// forgetWanted drops the cached answer: a Search now just reset a title's backoff.
func (a *api) forgetWanted() {
	a.wanted.mu.Lock()
	a.wanted.lists = nil
	a.wanted.mu.Unlock()
}
