package audioserver

import (
	"context"
	"sync"
	"time"
)

// A listening app asks for the library, a shelf, an author and a search in quick
// succession, and each used to re-read every book and list every audiobook folder. The
// catalogue and each item's files are kept in memory briefly instead, and dropped the
// moment an audiobook is imported so a new book shows up straight away.
//
// A file list lives much longer than the catalogue: listing a folder can wake a sleeping
// array disk, imports, renames and deletes drop it at once, and a file that vanished some
// other way is noticed when an app asks for it (serveFile re-lists once).

const (
	cacheTTL = 2 * time.Minute
	filesTTL = 30 * time.Minute
)

type catalogCache struct {
	mu    sync.Mutex
	items []Item
	at    time.Time
	gen   uint64 // bumped on invalidate, so a load that raced one isn't stored
}

type filesEntry struct {
	files []AudioFile
	at    time.Time
}

type filesCache struct {
	mu  sync.Mutex
	m   map[string]filesEntry
	gen uint64
}

func (c *catalogCache) get(now time.Time) ([]Item, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil || now.Sub(c.at) > cacheTTL {
		return nil, false
	}
	return append([]Item(nil), c.items...), true
}

func (c *catalogCache) generation() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

func (c *catalogCache) put(items []Item, now time.Time, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.gen {
		return
	}
	c.items, c.at = append([]Item(nil), items...), now
}

func (c *catalogCache) clear() {
	c.mu.Lock()
	c.items, c.gen = nil, c.gen+1
	c.mu.Unlock()
}

func (c *filesCache) get(path string, now time.Time) ([]AudioFile, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[path]
	if !ok || now.Sub(e.at) > filesTTL {
		return nil, false
	}
	return append([]AudioFile(nil), e.files...), true
}

func (c *filesCache) generation() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

func (c *filesCache) put(path string, files []AudioFile, now time.Time, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.gen {
		return
	}
	if c.m == nil || len(c.m) > 5000 {
		c.m = map[string]filesEntry{}
	}
	c.m[path] = filesEntry{files: append([]AudioFile(nil), files...), at: now}
}

func (c *filesCache) clear() {
	c.mu.Lock()
	c.m, c.gen = nil, c.gen+1
	c.mu.Unlock()
}

// drop forgets one item's file list.
func (c *filesCache) drop(path string) {
	c.mu.Lock()
	delete(c.m, path)
	c.gen++
	c.mu.Unlock()
}

// Invalidate drops the cached catalogue and file lists.
func (s *Server) Invalidate() {
	s.catalog.clear()
	s.probe.cache.clear()
}

// OnBookChanged keeps the catalogue fresh after an import, or after a book's files were
// renamed or deleted (an outbox consumer): the book is marked for a re-read, the cache
// is dropped and, when the server is on, warm is called so the audiobook is probed
// straight away and an app gets its length and chapters on first open. warm runs the
// probing in the background (main hands it to the run group); the handler itself
// returns at once. Idempotent: marking and dropping twice costs a re-read.
func (s *Server) OnBookChanged(ctx context.Context, bookID int64, warm func(fn func(ctx context.Context))) {
	s.markDirty(bookID)
	s.Invalidate()
	if warm != nil && s.settings != nil && s.settings.GetBool(ctx, KeyEnabled, false) {
		warm(func(ctx context.Context) { s.Warm(ctx) })
	}
}
