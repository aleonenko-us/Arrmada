package automation

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// The Activity queue reads the acquisition record and the pending reviews every time it
// refreshes — on each queue event and its fallback poll, from every open tab — though
// both change only when a grab is recorded or moves on, or a review is held or settled.
// They are kept here behind version counters: every write to grabs bumps one
// (grabsChanged, called from each function that writes the table; a source test keeps it
// that way), every review change bumps the other (reviewsChanged). A cached answer is
// used only while its version is current, and never for longer than feedCacheMaxAge, so a
// write that somehow skipped the counter is still seen within half a minute.
//
// The transfer list itself is never cached here: it is the download client's 2-second
// snapshot.

// feedCacheMaxAge bounds how stale a cached answer can be whatever the counters say.
const feedCacheMaxAge = 30 * time.Second

// feedCache holds the cached reads and their versions. Its zero value is ready to use.
type feedCache struct {
	grabsVer    atomic.Int64
	reviewsVer  atomic.Int64
	attemptsVer atomic.Int64

	mu      sync.Mutex
	links   *cachedLinks
	reviews *cachedReviews
}

type cachedLinks struct {
	ver   int64
	at    time.Time
	links QueueLinks
}

type cachedReviews struct {
	ver  int64
	at   time.Time
	list []Review
}

// grabsChanged says the grabs table was written.
func (c *Coordinator) grabsChanged() { c.feed.grabsVer.Add(1) }

// attemptsChanged says a search attempt was stored.
func (c *Coordinator) attemptsChanged() { c.feed.attemptsVer.Add(1) }

// WantedVersion changes whenever what the Wanted view reads from the pipeline may have:
// a grab recorded or moved on, a review held or settled, a search attempt stored. It
// says nothing about the libraries themselves (a title added or unmonitored), so a cache
// keyed on it must still expire on its own.
func (c *Coordinator) WantedVersion() int64 {
	return c.feed.grabsVer.Load() + c.feed.reviewsVer.Load() + c.feed.attemptsVer.Load()
}

// QueueLinks is the acquisition record as the Activity queue joins it to the client: the
// live grabs by lowercased info hash, the legacy hashless ones, and each grab's seed
// rule (SeedPolicies' keys). Shared between callers: read it, never change it.
type QueueLinks struct {
	ByHash map[string]Acquisition
	Legacy []Acquisition
	Seed   map[string]SeedPolicy
}

// QueueLinks is LiveByHash and SeedPolicies in one, from the cache while no grab has been
// written since. An unreadable record is an error, never an empty answer.
func (c *Coordinator) QueueLinks(ctx context.Context) (QueueLinks, error) {
	// The version is read before the database: a write landing during the read leaves the
	// cached copy a version behind, so the next call reads again.
	ver := c.feed.grabsVer.Load()
	now := c.clock()
	c.feed.mu.Lock()
	cur := c.feed.links
	c.feed.mu.Unlock()
	if cur != nil && cur.ver == ver && now.Sub(cur.at) < feedCacheMaxAge {
		return cur.links, nil
	}
	byHash, legacy, err := c.LiveByHash(ctx)
	if err != nil {
		return QueueLinks{}, err
	}
	links := QueueLinks{ByHash: byHash, Legacy: legacy, Seed: c.SeedPolicies(ctx)}
	c.feed.mu.Lock()
	c.feed.links = &cachedLinks{ver: ver, at: now, links: links}
	c.feed.mu.Unlock()
	return links, nil
}

// PendingReviews is ListReviews from the cache while no review has been held or settled
// since. The slice is the caller's own; the reviews in it are shared values.
func (c *Coordinator) PendingReviews(ctx context.Context) ([]Review, error) {
	ver := c.feed.reviewsVer.Load()
	now := c.clock()
	c.feed.mu.Lock()
	cur := c.feed.reviews
	c.feed.mu.Unlock()
	if cur != nil && cur.ver == ver && now.Sub(cur.at) < feedCacheMaxAge {
		return append([]Review(nil), cur.list...), nil
	}
	list, err := c.ListReviews(ctx)
	if err != nil {
		return nil, err
	}
	c.feed.mu.Lock()
	c.feed.reviews = &cachedReviews{ver: ver, at: now, list: list}
	c.feed.mu.Unlock()
	return append([]Review(nil), list...), nil
}
