package audioserver

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/tristenlammi/arrmada/internal/library"
)

// diskCounter counts the prober's filesystem calls (the test seam in probe.go).
type diskCounter struct{ stats, walks atomic.Int64 }

func (d *diskCounter) install(p *prober) {
	p.stat = func(path string) (os.FileInfo, error) { d.stats.Add(1); return os.Stat(path) }
	p.walk = func(path string) []library.FoundFile { d.walks.Add(1); return library.FindBookFiles(path) }
}

func (d *diskCounter) calls() int64 { return d.stats.Load() + d.walks.Load() }

// audioDir is the harness book's audiobook folder.
func (h *harness) audioDir() string { return h.book.Audiobook.Path }

// remember stores probed details for a file, as a successful ffprobe would have (the
// test image has no ffprobe). A file that exists gets its real size and time, so a
// listing takes the row as current.
func (h *harness) remember(path string, seconds float64) {
	h.t.Helper()
	h.rememberAs(path, seconds, "", metaVersion)
}

// rememberAs is remember with a narrator read from the tags, written by the probe of
// the given version (1 = from before narrators were read).
func (h *harness) rememberAs(path string, seconds float64, narrator string, version int) {
	h.t.Helper()
	var size, mtime int64 = 1000, 1
	if fi, err := os.Stat(path); err == nil {
		size, mtime = fi.Size(), fi.ModTime().UnixMilli()
	}
	if _, err := h.srv.db.Exec(`INSERT INTO audio_file_meta (path, size, mtime, duration, bitrate, codec, title, narrator, chapters, meta_version)
		VALUES (?, ?, ?, ?, 64000, 'mp3', '', ?, '[]', ?)`, path, size, mtime, seconds, narrator, version); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) rememberAll() {
	h.t.Helper()
	for _, n := range []string{"Part 1.mp3", "Part 2.mp3", "Part 10.mp3"} {
		h.remember(filepath.Join(h.audioDir(), n), 600)
	}
}

func (h *harness) storedPaths() map[string]bool {
	h.t.Helper()
	paths, err := h.srv.probe.knownPaths(context.Background(), h.audioDir())
	if err != nil {
		h.t.Fatal(err)
	}
	out := map[string]bool{}
	for _, p := range paths {
		out[p] = true
	}
	return out
}

// A warm-up with every audiobook already probed and nothing changed reads the database
// only: no stat, no folder walk.
func TestWarmSkipsKnownItemsWithoutDisk(t *testing.T) {
	h := newHarness(t)
	h.rememberAll()
	var d diskCounter
	d.install(h.srv.probe)
	if n := h.srv.Warm(context.Background()); n != 0 {
		t.Fatalf("warm probed %d items, want 0", n)
	}
	if d.calls() != 0 {
		t.Fatalf("warm touched the disk: %d stats, %d walks", d.stats.Load(), d.walks.Load())
	}
	// An item ffprobe can't read is tried once, then left alone until it changes.
	h2 := newHarness(t)
	var d2 diskCounter
	d2.install(h2.srv.probe)
	if n := h2.srv.Warm(context.Background()); n != 1 {
		t.Fatalf("first warm of an unread book probed %d, want 1", n)
	}
	before := d2.calls()
	h2.srv.Warm(context.Background())
	if d2.calls() != before {
		t.Fatal("an item ffprobe can't read was listed again on the next warm-up")
	}
}

// A book marked changed (an import, rename or delete) is listed and read again, files
// that are gone are forgotten, and the mark clears.
func TestWarmProbesDirtyItem(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.rememberAll()
	gone := filepath.Join(h.audioDir(), "Part 11.mp3")
	h.remember(gone, 30)
	sibling := h.audioDir() + " (Full cast)"
	h.remember(filepath.Join(sibling, "01.mp3"), 30) // a neighbour's file, not this book's

	h.srv.OnBookChanged(ctx, h.book.ID, nil)
	it, err := h.srv.item(ctx, itemKeyFor(h.book.ID, 0))
	if err != nil {
		t.Fatal(err)
	}
	if h.srv.ready(ctx, it) {
		t.Fatal("a changed book counts as ready before it's read again")
	}
	var d diskCounter
	d.install(h.srv.probe)
	if n := h.srv.Warm(ctx); n != 1 {
		t.Fatalf("warm probed %d items, want the changed one", n)
	}
	if d.walks.Load() == 0 {
		t.Fatal("the changed book's folder wasn't listed")
	}
	stored := h.storedPaths()
	if stored[gone] || len(stored) != 3 {
		t.Fatalf("after warm, stored = %v; the vanished file should be forgotten", stored)
	}
	if k := h.srv.probe.knownUnder(ctx, sibling); k.Files != 1 {
		t.Fatal("a sibling folder's files were touched")
	}
	if h.srv.isDirty(h.book.ID) || !h.srv.ready(ctx, it) {
		t.Fatal("the mark didn't clear after the warm-up read the book")
	}
}

// Library stats, list replies and the admin's counts come from the database: they never
// walk the audiobook tree.
func TestLibraryStatsFromDB(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.rememberAll()
	h.signIn()
	var d diskCounter
	d.install(h.srv.probe)

	if items, ready := h.srv.LibraryStats(ctx); items != 1 || ready != 1 {
		t.Fatalf("LibraryStats = %d, %d; want 1, 1", items, ready)
	}
	stats := h.json("GET", "/api/libraries/"+libraryID+"/stats", nil)
	if stats["totalDuration"].(float64) != 1800 || stats["numAudioTracks"].(float64) != 3 {
		t.Fatalf("stats = %v", stats)
	}
	libs := h.json("GET", "/api/libraries?include=stats", nil)
	if st := obj1(t, obj1(t, list1(t, libs["libraries"])[0])["stats"]); st["totalDuration"].(float64) != 1800 {
		t.Fatalf("include=stats = %v", st)
	}
	items := h.json("GET", "/api/libraries/"+libraryID+"/items?sort=media.duration&limit=10&page=0", nil)
	media := obj1(t, obj1(t, list1(t, items["results"])[0])["media"])
	if media["duration"].(float64) != 1800 || media["numAudioFiles"].(float64) != 3 || media["numChapters"].(float64) != 3 {
		t.Fatalf("list item media = %v", media)
	}
	if code, _ := h.do("GET", "/api/libraries/"+libraryID+"/personalized", nil, nil); code != 200 {
		t.Fatalf("personalized: HTTP %d", code)
	}
	if d.calls() != 0 {
		t.Fatalf("stats and lists touched the disk: %d stats, %d walks", d.stats.Load(), d.walks.Load())
	}

	// A book not probed yet still says it has audio files (ShelfPlayer drops one that
	// doesn't), from what the import recorded.
	if _, err := h.srv.db.Exec(`DELETE FROM audio_file_meta`); err != nil {
		t.Fatal(err)
	}
	h.srv.Invalidate()
	items = h.json("GET", "/api/libraries/"+libraryID+"/items?limit=10&page=0", nil)
	media = obj1(t, obj1(t, list1(t, items["results"])[0])["media"])
	if media["numAudioFiles"].(float64) != 3 || media["duration"].(float64) != 0 {
		t.Fatalf("unprobed list item media = %v", media)
	}
	if _, ready := h.srv.LibraryStats(ctx); ready != 0 {
		t.Fatal("an unprobed book counts as ready")
	}
}

// The daily verify forgets a file deleted outside Arrmada.
func TestVerifyPrunesVanishedFiles(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.rememberAll()
	victim := filepath.Join(h.audioDir(), "Part 2.mp3")
	if err := os.Remove(victim); err != nil {
		t.Fatal(err)
	}
	if n := h.srv.Verify(ctx); n != 1 {
		t.Fatalf("verify forgot %d files, want 1", n)
	}
	if stored := h.storedPaths(); stored[victim] || len(stored) != 2 {
		t.Fatalf("stored after verify = %v", stored)
	}
}

// A folder that can't be found (a disk or share offline) keeps everything known about it.
func TestVerifyKeepsRowsWhenRootMissing(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.rememberAll()
	away := h.audioDir() + ".offline"
	if err := os.Rename(h.audioDir(), away); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(away, h.audioDir()) })
	if n := h.srv.Verify(ctx); n != 0 {
		t.Fatalf("verify forgot %d files of a missing folder", n)
	}
	if len(h.storedPaths()) != 3 {
		t.Fatal("rows of an offline folder were dropped")
	}
}

// A file replaced outside Arrmada while its folder's listing is cached: asking for the
// new file lists the folder again instead of answering 404 for half an hour.
func TestServeFileRelistsAfterVanishedPath(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	key := itemKeyFor(h.book.ID, 0)
	old := inoFor("Part 2.mp3")
	if code, _ := h.do("GET", "/api/items/"+key+"/file/"+old, nil, nil); code != 200 {
		t.Fatalf("first fetch: HTTP %d", code)
	}
	dir := h.audioDir()
	if err := os.Rename(filepath.Join(dir, "Part 2.mp3"), filepath.Join(dir, "Part 02.mp3")); err != nil {
		t.Fatal(err)
	}
	if code, _ := h.do("GET", "/api/items/"+key+"/file/"+inoFor("Part 02.mp3"), nil, nil); code != 200 {
		t.Fatalf("renamed file: HTTP %d, want 200 after a re-list", code)
	}
	if code, _ := h.do("GET", "/api/items/"+key+"/file/"+old, nil, nil); code != 404 {
		t.Fatalf("vanished file: HTTP %d, want 404", code)
	}
}
