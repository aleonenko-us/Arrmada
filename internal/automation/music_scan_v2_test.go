package automation

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/music"
)

// scanLib is a music folder of sparse files whose tags a fake reader serves, counting
// every file it opens and how many it has open at once.
type scanLib struct {
	t      *testing.T
	root   string
	mu     sync.Mutex
	tags   map[string]music.FileTags
	opened atomic.Int64
	active atomic.Int32
	peak   atomic.Int32
}

func newScanLib(t *testing.T, h *musicHarness) *scanLib {
	t.Helper()
	l := &scanLib{t: t, root: h.c.imp.MusicDir(), tags: map[string]music.FileTags{}}
	if err := os.MkdirAll(l.root, 0o755); err != nil {
		t.Fatal(err)
	}
	h.c.musicTagsFn = l.read
	return l
}

func (l *scanLib) add(rel string, tags music.FileTags) string {
	l.t.Helper()
	p := filepath.Join(l.root, filepath.FromSlash(rel))
	sparse(l.t, p, 300<<10)
	l.mu.Lock()
	l.tags[p] = tags
	l.mu.Unlock()
	return p
}

func (l *scanLib) read(path string) (music.FileTags, int, error) {
	n := l.active.Add(1)
	defer l.active.Add(-1)
	for {
		p := l.peak.Load()
		if n <= p || l.peak.CompareAndSwap(p, n) {
			break
		}
	}
	time.Sleep(time.Millisecond) // long enough for the workers to overlap
	l.opened.Add(1)
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.tags[path], 0, nil
}

// snapshot is every file under the music folder with its size and modification time.
func (l *scanLib) snapshot() map[string]string {
	l.t.Helper()
	out := map[string]string{}
	_ = filepath.WalkDir(l.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		out[p] = fmt.Sprintf("%d/%d", fi.Size(), fi.ModTime().UnixNano())
		return nil
	})
	return out
}

func tagged(artist, album string, track int, title string) music.FileTags {
	return music.FileTags{Artist: artist, AlbumArtist: artist, Album: album, Track: track, Title: title}
}

func (h *musicHarness) scan(t *testing.T, opt MusicScanOptions) MusicScanResult {
	t.Helper()
	res, err := h.c.ScanMusicLibraryWith(h.ctx, opt, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// trackFiles maps an album's track titles to their recorded paths.
func (h *musicHarness) trackFiles(t *testing.T, albumID int64) map[string]string {
	t.Helper()
	tracks, err := h.svc.Tracks(h.ctx, albumID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, tr := range tracks {
		if tr.HasFile {
			out[tr.Title] = tr.FilePath
		}
	}
	return out
}

// MUS-37: a scan records the albums its folders are; a second scan of the unchanged
// library opens no file at all. Nothing on disk changes either time.
func TestMusicScanIsIncremental(t *testing.T) {
	h := musicTestCoord(t)
	lib := newScanLib(t, h)
	rh := h.addArtist(t, "Radiohead", "", []string{"OK Computer", "Kid A"}, nil, "")
	lib.add("Radiohead/OK Computer (1997)/01 - One.flac", tagged("Radiohead", "OK Computer", 1, "One"))
	lib.add("Radiohead/OK Computer (1997)/02 - Two.flac", tagged("Radiohead", "OK Computer", 2, "Two"))
	lib.add("Radiohead/Kid A/01 - One.flac", tagged("Radiohead", "Kid A", 1, "One"))
	lib.add("Radiohead/Kid A/02 - Two.flac", tagged("Radiohead", "Kid A", 2, "Two"))
	before := lib.snapshot()

	first := h.scan(t, MusicScanOptions{})
	if first.Folders != 2 || first.Albums != 2 || first.Tracks != 4 || first.Reviews != 0 || first.Opened != 4 {
		t.Fatalf("first scan = %+v; want both albums recorded from 4 opened files", first)
	}
	if got := h.trackFiles(t, h.album(t, rh.ID, "OK Computer").ID); len(got) != 2 || !strings.HasSuffix(got["One"], "01 - One.flac") {
		t.Errorf("OK Computer's files = %v", got)
	}
	opened := lib.opened.Load()
	second := h.scan(t, MusicScanOptions{})
	if second.Skipped != 2 || second.Opened != 0 || second.Tracks != 0 || lib.opened.Load() != opened {
		t.Errorf("second scan = %+v (opened %d more); want every folder skipped and nothing opened", second, lib.opened.Load()-opened)
	}
	full := h.scan(t, MusicScanOptions{Full: true})
	if full.Skipped != 0 || full.Tracks != 0 {
		t.Errorf("a full scan = %+v; want every folder looked at and nothing recorded twice", full)
	}
	if after := lib.snapshot(); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Errorf("the scan changed files on disk:\nbefore %v\nafter  %v", before, after)
	}
	if last, _, _, ok, err := h.svc.LastScanRun(h.ctx); err != nil || !ok || !strings.Contains(last, `"folders":2`) {
		t.Errorf("last scan summary = %s, %v, %v", last, ok, err)
	}
}

// Files that carry MusicBrainz ids are matched by them, wherever they sit and whatever the
// folders are called; no artist search is needed.
func TestMusicScanMatchesByMBID(t *testing.T) {
	h := musicTestCoord(t)
	lib := newScanLib(t, h)
	bj := h.addArtist(t, "Björk", "", []string{"Homogenic"}, nil, "")
	rg := "artist-Björk-rg-0"
	h.fake.mu.Lock()
	h.fake.tracks[rg] = []metadata.TrackResult{
		{Title: "Hunter", Disc: 1, Number: 1, MBID: "rt-1", RecordingMBID: "rec-1", ReleaseMBID: "rel-homogenic"},
		{Title: "Jóga", Disc: 1, Number: 2, MBID: "rt-2", RecordingMBID: "rec-2", ReleaseMBID: "rel-homogenic"},
	}
	h.fake.mu.Unlock()
	for i, title := range []string{"Hunter", "Jóga"} {
		lib.add(fmt.Sprintf("Unsorted/rip-0042/track%02d.flac", i+1), music.FileTags{
			Title: title, Track: i + 1, Artist: "Björk", AlbumArtist: "Björk", Album: "Homogenic",
			RecordingMBID: fmt.Sprintf("rec-%d", i+1), ReleaseMBID: "rel-homogenic", ReleaseGroupMBID: rg,
		})
	}
	res := h.scan(t, MusicScanOptions{})
	if res.Tracks != 2 || res.Reviews != 0 {
		t.Fatalf("scan = %+v; want both files placed by their ids", res)
	}
	got := h.trackFiles(t, h.album(t, bj.ID, "Homogenic").ID)
	if !strings.HasSuffix(got["Hunter"], "track01.flac") || !strings.HasSuffix(got["Jóga"], "track02.flac") {
		t.Errorf("Homogenic's files = %v", got)
	}
	if h.fake.searches != 0 {
		t.Errorf("%d artist search(es) for files whose MBIDs already named the album", h.fake.searches)
	}
}

// A folder the scan can't place goes to Reviews once (a second scan neither re-reads it
// nor holds it twice). It can't be rejected, which would delete library files; Match…
// records its files where they are.
func TestMusicScanUnmatchedFolderGoesToReviews(t *testing.T) {
	h := musicTestCoord(t)
	lib := newScanLib(t, h)
	rh := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "")
	one := lib.add("Nobody/Some Bootleg/01 - One.flac", music.FileTags{})
	two := lib.add("Nobody/Some Bootleg/02 - Two.flac", music.FileTags{})
	before := lib.snapshot()

	res := h.scan(t, MusicScanOptions{})
	if res.Reviews != 1 || len(res.Unmatched) != 1 || res.Tracks != 0 {
		t.Fatalf("scan = %+v; want the folder held for review", res)
	}
	list, err := h.c.ListReviews(h.ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("reviews = %+v, %v", list, err)
	}
	rv := list[0]
	if rv.Source != ReviewSourceLibraryScan || rv.ReasonCode != ReasonUnmatched || rv.MediaType != "music" ||
		rv.Name != "Nobody/Some Bootleg" || !strings.Contains(rv.Reason, "Nobody") {
		t.Errorf("review = %+v", rv)
	}
	opened := lib.opened.Load()
	again := h.scan(t, MusicScanOptions{})
	if again.Opened != 0 || again.Skipped != 1 || lib.opened.Load() != opened {
		t.Errorf("second scan = %+v; want the held folder skipped unread", again)
	}
	if list, _ := h.c.ListReviews(h.ctx); len(list) != 1 {
		t.Errorf("reviews after a second scan = %d, want still 1", len(list))
	}

	if err := h.c.RejectReview(h.ctx, rv.ID); !errors.Is(err, ErrWrongReason) {
		t.Errorf("reject = %v, want it refused", err)
	}
	if err := h.c.DismissReview(h.ctx, rv.ID); err != nil {
		t.Fatal(err)
	}
	// Held again only once the folder changes; then matched by hand.
	h.svc.ForgetScanDir(h.ctx, filepath.Dir(one))
	h.scan(t, MusicScanOptions{})
	list, _ = h.c.ListReviews(h.ctx)
	if len(list) != 1 {
		t.Fatalf("reviews after a rescan = %+v", list)
	}
	al := h.album(t, rh.ID, "OK Computer")
	if err := h.c.ImportReview(h.ctx, list[0].ID, al.ID, "music"); err != nil {
		t.Fatalf("match: %v", err)
	}
	got := h.trackFiles(t, al.ID)
	if got["One"] != one || got["Two"] != two {
		t.Errorf("after Match… the files are %v; want them recorded where they are", got)
	}
	if list, _ := h.c.ListReviews(h.ctx); len(list) != 0 {
		t.Errorf("the review is still pending: %+v", list)
	}
	if after := lib.snapshot(); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Errorf("files on disk changed:\nbefore %v\nafter  %v", before, after)
	}
}

// A renamed album folder is followed by inode, not read as gone and new.
func TestMusicScanFollowsRenamedFolder(t *testing.T) {
	h := musicTestCoord(t)
	lib := newScanLib(t, h)
	rh := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "")
	lib.add("Radiohead/OK Computer/01 - One.flac", tagged("Radiohead", "OK Computer", 1, "One"))
	lib.add("Radiohead/OK Computer/02 - Two.flac", tagged("Radiohead", "OK Computer", 2, "Two"))
	h.scan(t, MusicScanOptions{})
	from, to := filepath.Join(lib.root, "Radiohead", "OK Computer"), filepath.Join(lib.root, "Radiohead", "OK Computer (1997) [FLAC]")
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
	opened := lib.opened.Load()
	res := h.scan(t, MusicScanOptions{})
	if music.FileIdentity(mustStat(t, to)) == "" {
		t.Skip("no inodes on this platform")
	}
	if res.Moved != 2 || res.Tracks != 0 || lib.opened.Load() != opened {
		t.Errorf("scan = %+v (opened %d); want both files followed, none read", res, lib.opened.Load()-opened)
	}
	got := h.trackFiles(t, h.album(t, rh.ID, "OK Computer").ID)
	if !strings.HasPrefix(got["One"], to) || !strings.HasPrefix(got["Two"], to) {
		t.Errorf("files = %v, want them at %s", got, to)
	}
}

func mustStat(t *testing.T, p string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

// Disc folders are one album; a folder named for an artist by an alias is the artist's.
func TestMusicScanDiscFoldersAndAliases(t *testing.T) {
	h := musicTestCoord(t)
	lib := newScanLib(t, h)
	u := h.addArtist(t, "宇多田ヒカル", "", []string{"First Love"}, nil, "")
	h.fake.mu.Lock()
	h.fake.tracks["artist-宇多田ヒカル-rg-0"] = []metadata.TrackResult{{Title: "One", Disc: 1, Number: 1}, {Title: "Two", Disc: 2, Number: 1}}
	h.fake.mu.Unlock()
	h.withAliases(t, u, metadata.ArtistAlias{Name: "Utada Hikaru", Type: "Artist name"})
	lib.add("Utada Hikaru/First Love/CD1/01 - One.flac", music.FileTags{})
	lib.add("Utada Hikaru/First Love/CD2/01 - Two.flac", music.FileTags{})
	res := h.scan(t, MusicScanOptions{})
	if res.Folders != 1 || res.Tracks != 2 {
		t.Fatalf("scan = %+v; want one two-disc album placed", res)
	}
	if h.fake.searches != 0 {
		t.Errorf("the artist was searched for %d time(s); the alias should have found it", h.fake.searches)
	}
}

// The workers are bounded, the per-run cap leaves the rest for the next scan, and the
// parallel scan is race-free (run under -race).
func TestMusicScanWorkersAndCap(t *testing.T) {
	h := musicTestCoord(t)
	lib := newScanLib(t, h)
	var titles []string
	for i := 0; i < 12; i++ {
		titles = append(titles, fmt.Sprintf("Album %02d", i))
	}
	a := h.addArtist(t, "Prolific", "", titles, nil, "")
	for _, title := range titles {
		lib.add("Prolific/"+title+"/01 - One.flac", tagged("Prolific", title, 1, "One"))
		lib.add("Prolific/"+title+"/02 - Two.flac", tagged("Prolific", title, 2, "Two"))
	}
	first := h.scan(t, MusicScanOptions{Workers: 3, FoldersPerRun: 8})
	if first.Tracks != 16 || first.Remaining != 4 {
		t.Fatalf("first scan = %+v; want 8 folders done and 4 left", first)
	}
	if p := lib.peak.Load(); p > 3 {
		t.Errorf("%d files read at once; want at most 3 with 3 workers", p)
	} else {
		t.Logf("at most %d files read at once", p)
	}
	second := h.scan(t, MusicScanOptions{Workers: 3, FoldersPerRun: 8})
	if second.Tracks != 8 || second.Remaining != 0 || second.Skipped != 8 {
		t.Errorf("second scan = %+v; want the other 4 done and the first 8 skipped", second)
	}
	for _, title := range titles {
		if got := h.trackFiles(t, h.album(t, a.ID, title).ID); len(got) != 2 {
			t.Errorf("%s has %d file(s)", title, len(got))
		}
	}
}

// Folders held in Reviews, or waiting for their album's listing, never hold new folders back
// from the per-run cap.
func TestMusicScanBacklogDoesNotStarveNewFolders(t *testing.T) {
	h := musicTestCoord(t)
	lib := newScanLib(t, h)
	a := h.addArtist(t, "Band", "", []string{"Announced", "Later One", "Later Two"}, map[string]bool{"Announced": true}, "")
	lib.add("Aaa Unknown/Bootleg/01 - x.flac", music.FileTags{})
	lib.add("Band/Announced/01 - One.flac", tagged("Band", "Announced", 1, "One"))
	first := h.scan(t, MusicScanOptions{})
	if first.Reviews != 1 || first.Waiting != 1 {
		t.Fatalf("first scan = %+v; want one folder held and one waiting", first)
	}
	lib.add("Band/Later One/01 - One.flac", tagged("Band", "Later One", 1, "One"))
	lib.add("Band/Later Two/01 - One.flac", tagged("Band", "Later Two", 1, "One"))
	second := h.scan(t, MusicScanOptions{FoldersPerRun: 1})
	if second.Tracks != 1 || second.Remaining != 2 {
		t.Fatalf("second scan = %+v; want a new folder read first, the waiting one left with the other", second)
	}
	third := h.scan(t, MusicScanOptions{FoldersPerRun: 1})
	if third.Tracks != 1 {
		t.Errorf("third scan = %+v; want the other new folder read before the waiting one", third)
	}
	if got := h.trackFiles(t, h.album(t, a.ID, "Later One").ID); len(got) != 1 {
		t.Errorf("Later One = %v", got)
	}
	if got := h.trackFiles(t, h.album(t, a.ID, "Later Two").ID); len(got) != 1 {
		t.Errorf("Later Two = %v", got)
	}
}

// An artist the library doesn't have is added unmonitored when MusicBrainz has exactly one
// by that name; two of one name, or none, is a review saying so.
func TestMusicScanAddsArtistsCarefully(t *testing.T) {
	h := musicTestCoord(t)
	lib := newScanLib(t, h)
	h.fake.mu.Lock()
	h.fake.artists["artist-Portishead"] = metadata.ArtistDetails{ArtistResult: metadata.ArtistResult{MBID: "artist-Portishead", Name: "Portishead"}}
	h.fake.albums["artist-Portishead"] = []metadata.AlbumResult{{MBID: "ph-rg-0", Title: "Dummy", Type: "Album"}}
	h.fake.tracks["ph-rg-0"] = []metadata.TrackResult{{Title: "One", Disc: 1, Number: 1}, {Title: "Two", Disc: 1, Number: 2}}
	h.fake.search = map[string][]metadata.ArtistResult{
		"Portishead": {{MBID: "artist-Portishead", Name: "Portishead"}, {MBID: "other", Name: "Portishead Tribute"}},
		"Nirvana":    {{MBID: "nirvana-us", Name: "Nirvana"}, {MBID: "nirvana-uk", Name: "Nirvana"}},
	}
	h.fake.mu.Unlock()
	lib.add("Portishead/Dummy/01 - One.flac", tagged("Portishead", "Dummy", 1, "One"))
	lib.add("Portishead/Dummy/02 - Two.flac", tagged("Portishead", "Dummy", 2, "Two"))
	lib.add("Nirvana/Nevermind/01 - x.flac", tagged("Nirvana", "Nevermind", 1, "x"))
	res := h.scan(t, MusicScanOptions{Workers: 2})
	if res.Artists != 1 || res.Tracks != 2 || res.Reviews != 1 {
		t.Fatalf("scan = %+v; want Portishead added and placed, Nirvana held", res)
	}
	artists, _ := h.svc.LibraryArtists(h.ctx)
	for _, a := range artists {
		if a.Name == "Portishead" && a.Monitored {
			t.Error("a scanned artist was added monitored")
		}
	}
	list, _ := h.c.ListReviews(h.ctx)
	if len(list) != 1 || !strings.Contains(list[0].Reason, "2 artists on MusicBrainz") {
		t.Errorf("reviews = %+v", list)
	}
}

// A stopped scan says how far it got and touches nothing after.
func TestMusicScanStopsBetweenFolders(t *testing.T) {
	h := musicTestCoord(t)
	lib := newScanLib(t, h)
	h.addArtist(t, "Radiohead", "", []string{"OK Computer", "Kid A", "Amnesiac"}, nil, "")
	lib.add("Radiohead/Amnesiac/01 - One.flac", tagged("Radiohead", "Amnesiac", 1, "One"))
	lib.add("Radiohead/Kid A/01 - One.flac", tagged("Radiohead", "Kid A", 1, "One"))
	lib.add("Radiohead/OK Computer/01 - One.flac", tagged("Radiohead", "OK Computer", 1, "One"))
	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	res, err := h.c.ScanMusicLibraryWith(ctx, MusicScanOptions{Workers: 1}, func(done, total int) {
		if done == 1 {
			cancel()
		}
	})
	// One worker: when the first folder finishes, at most one more has been handed out.
	if !errors.Is(err, context.Canceled) || res.Tracks > 2 || res.Tracks < 1 {
		t.Fatalf("scan = %+v, %v; want it stopped after one or two folders", res, err)
	}
}
