package audioserver

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// What the catalogue knows about each audiobook's files comes from the database
// (audio_file_meta), not from listing folders: the warm-up used to walk and stat the
// whole audiobook tree every 30 minutes, and every admin page load and app library list
// did the same, which kept an Unraid array's disks spinning. Now an item is "ready" once
// its files have been probed, and stays so until something says its files changed:
// an import, a rename or a delete (OnBookChanged marks the book dirty), or the daily
// verify walk finding a file gone.

// warmState is what the warm-up has still to do.
type warmState struct {
	mu    sync.Mutex
	seq   uint64
	dirty map[int64]uint64 // book id → when it was marked (a sequence number)
	// nothing holds items a warm-up probed and still knows nothing about (ffprobe can't
	// read them), so the next warm-up doesn't list their folder again. A change to the
	// book or the daily verify tries them again.
	nothing map[string]bool
	again   atomic.Bool // a warm-up was asked for while one was running
}

func (s *Server) markDirty(bookID int64) {
	w := &s.warm
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.dirty == nil {
		w.dirty = map[int64]uint64{}
	}
	w.seq++
	w.dirty[bookID] = w.seq
	for key := range w.nothing {
		if b, _, ok := parseLegacyItemKey(key); ok && b == bookID {
			delete(w.nothing, key)
		}
	}
}

func (s *Server) isDirty(bookID int64) bool {
	s.warm.mu.Lock()
	defer s.warm.mu.Unlock()
	_, ok := s.warm.dirty[bookID]
	return ok
}

func (s *Server) dirtySnapshot() map[int64]uint64 {
	s.warm.mu.Lock()
	defer s.warm.mu.Unlock()
	out := make(map[int64]uint64, len(s.warm.dirty))
	for id, seq := range s.warm.dirty {
		out[id] = seq
	}
	return out
}

// cleanBook clears a book's mark, unless it was marked again since seq.
func (s *Server) cleanBook(bookID int64, seq uint64) {
	s.warm.mu.Lock()
	defer s.warm.mu.Unlock()
	if s.warm.dirty[bookID] == seq {
		delete(s.warm.dirty, bookID)
	}
}

func (s *Server) setNothing(key string, none bool) {
	s.warm.mu.Lock()
	defer s.warm.mu.Unlock()
	if !none {
		delete(s.warm.nothing, key)
		return
	}
	if s.warm.nothing == nil {
		s.warm.nothing = map[string]bool{}
	}
	s.warm.nothing[key] = true
}

func (s *Server) knownNothing(key string) bool {
	s.warm.mu.Lock()
	defer s.warm.mu.Unlock()
	return s.warm.nothing[key]
}

// ready reports whether an item's files are probed and nothing has changed them since.
func (s *Server) ready(ctx context.Context, it Item) bool {
	return s.probe.knownUnder(ctx, it.Path).Files > 0 && !s.isDirty(it.Book.ID)
}

// Warm probes the audiobooks whose files aren't known yet, or changed, so an app opening
// a book gets its chapters and durations at once rather than waiting on ffprobe. A book
// already probed costs one database read and no disk access. It pauses briefly after
// each book it reads, to stay out of the way.
func (s *Server) Warm(ctx context.Context) (probed int) {
	if !s.warming.CompareAndSwap(false, true) {
		s.warm.again.Store(true) // the run in progress goes round once more
		return 0
	}
	for {
		n, finished := s.warmOnce(ctx)
		probed += n
		if finished && s.warm.again.Swap(false) {
			continue
		}
		s.warming.Store(false)
		// A request that came in between the check above and the release saw a run still
		// going and left it to that run; pick it up rather than lose it.
		if !finished || !s.warm.again.Load() || !s.warming.CompareAndSwap(false, true) {
			return probed
		}
		s.warm.again.Store(false)
	}
}

// warmOnce is one pass; finished is false when ctx ended it early.
func (s *Server) warmOnce(ctx context.Context) (probed int, finished bool) {
	items, err := s.items(ctx)
	if err != nil {
		return 0, false
	}
	marks := s.dirtySnapshot()
	for _, it := range items {
		if ctx.Err() != nil {
			return probed, false
		}
		_, changed := marks[it.Book.ID]
		// Files probed before narrators were read (Stale) are read once more.
		if k := s.probe.knownUnder(ctx, it.Path); !changed && (s.knownNothing(it.Key) || k.Files > 0 && k.Stale == 0) {
			continue
		}
		files, err := s.probe.filesFor(ctx, it.Path, it.Book.Author, true)
		if changed && err == nil {
			s.forgetVanished(ctx, it.Path, files)
		}
		s.setNothing(it.Key, s.probe.knownUnder(ctx, it.Path).Files == 0)
		probed++
		select {
		case <-ctx.Done():
			return probed, false
		case <-time.After(200 * time.Millisecond):
		}
	}
	for id, seq := range marks {
		s.cleanBook(id, seq)
	}
	return probed, true
}

// forgetVanished drops the stored details of files under root that a fresh listing
// (files) no longer has.
func (s *Server) forgetVanished(ctx context.Context, root string, files []AudioFile) int {
	stored, err := s.probe.knownPaths(ctx, root)
	if err != nil {
		return 0
	}
	listed := make(map[string]bool, len(files))
	for _, f := range files {
		listed[f.Path] = true
	}
	var gone []string
	for _, p := range stored {
		if !listed[p] {
			gone = append(gone, p)
		}
	}
	return s.probe.forget(ctx, gone)
}

// Verify is the daily full walk (the audioserver-verify task): it lists every
// audiobook's files, forgets the stored details of files deleted outside Arrmada, and
// reads files it doesn't know yet. An item whose folder can't be found is skipped whole
// — a disk or share that's offline mustn't wipe what's known about it. It returns how
// many files it forgot.
func (s *Server) Verify(ctx context.Context) (forgotten int) {
	items, err := s.items(ctx)
	if err != nil {
		return 0
	}
	for _, it := range items {
		if ctx.Err() != nil {
			break
		}
		if _, err := s.probe.statPath(it.Path); err != nil {
			continue
		}
		files, err := s.probe.list(ctx, it.Path, false)
		if err != nil {
			continue
		}
		forgotten += s.forgetVanished(ctx, it.Path, files)
		unknown := false
		for _, f := range files {
			if f.Duration <= 0 {
				unknown = true
			}
		}
		s.probe.cache.drop(it.Path)
		if !unknown {
			continue
		}
		_, _ = s.probe.filesFor(ctx, it.Path, it.Book.Author, true)
		s.setNothing(it.Key, s.probe.knownUnder(ctx, it.Path).Files == 0)
		select {
		case <-ctx.Done():
			return forgotten
		case <-time.After(200 * time.Millisecond):
		}
	}
	if forgotten > 0 {
		s.Invalidate()
	}
	return forgotten
}

// LibraryStats counts the audiobooks served and how many have been probed, from the
// database alone.
func (s *Server) LibraryStats(ctx context.Context) (items, ready int) {
	list, err := s.items(ctx)
	if err != nil {
		return 0, 0
	}
	for _, it := range list {
		if s.ready(ctx, it) {
			ready++
		}
	}
	return len(list), ready
}
