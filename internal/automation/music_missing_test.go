package automation

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/music"
)

// markGone flags every recorded file of the named albums as gone from disk, as two file
// checks a day apart would.
func (h *musicHarness) markGone(t *testing.T, titles ...string) {
	t.Helper()
	for _, title := range titles {
		if _, err := h.st.DB().Exec(`UPDATE music_track_files SET missing_seen_at = '2026-10-01T00:00:00Z', missing_since = '2026-10-01T00:00:00Z'
			WHERE album_id IN (SELECT id FROM albums WHERE title = ?)`, title); err != nil {
			t.Fatal(err)
		}
	}
}

// insertGrab records a music grab of an album (decided_mode as given) and returns it.
func (h *musicHarness) insertGrab(t *testing.T, albumID int64, title, mode string) grab {
	t.Helper()
	res, err := h.c.db.Exec(`INSERT INTO grabs (movie_id, title, indexer, media_type, info_hash, decided_mode)
		VALUES (?, ?, 'Test', 'music', ?, ?)`, albumID, title, fmt.Sprintf("%040d", albumID), mode)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return grab{ID: id, MovieID: albumID, Title: title, Indexer: "Test", MediaType: "music"}
}

// A stalled grab of an album with track files gone from disk is never replaced by a search
// of its own (the stalled download is kept); an upgrade grab is replaced only within the
// upgrade gates, whatever the album reads now; a plain grab of an incomplete album is
// replaced as before.
func TestStallFailoverRespectsMissingFiles(t *testing.T) {
	h, _, a, _ := verifyLib(t, 3)
	h.releases = found(rel("Band - Album 00 [FLAC]"), rel("Band - Album 01 [FLAC]"), rel("Band - Album 02 [FLAC]"))

	h.markGone(t, "Album 00")
	gone := h.album(t, a.ID, "Album 00")
	st, ok := h.c.musicStallTarget(h.ctx, gone, h.insertGrab(t, gone.ID, "Band - Album 00 [MP3]", ""))
	if !ok {
		t.Fatal("no stall target")
	}
	if got, err := st.replace(h.ctx, nil); got != "" || err != nil || len(h.queries) != 0 {
		t.Fatalf("missing files: replaced by %q (%v), queries %v; want nothing searched", got, err, h.queries)
	}

	// An upgrade grab of an album that reads incomplete (a track unwanted elsewhere, say)
	// goes through the upgrade gates: the default profile allows no upgrades, so nothing.
	if _, err := h.st.DB().Exec(`DELETE FROM music_track_files WHERE album_id = (SELECT id FROM albums WHERE title = 'Album 01') AND path LIKE '%02 - Two%'`); err != nil {
		t.Fatal(err)
	}
	inc := h.album(t, a.ID, "Album 01")
	st, _ = h.c.musicStallTarget(h.ctx, inc, h.insertGrab(t, inc.ID, "Band - Album 01 [MP3]", decidedUpgrade))
	if got, _ := st.replace(h.ctx, nil); got != "" || len(h.queries) != 0 {
		t.Errorf("upgrade grab: replaced by %q, queries %v; want the upgrade gates, no plain search", got, h.queries)
	}
	// The same album, a plain grab: searched and replaced.
	st, _ = h.c.musicStallTarget(h.ctx, inc, h.insertGrab(t, inc.ID, "Band - Album 01 [MP3 V0]", ""))
	if got, _ := st.replace(h.ctx, nil); got == "" || len(h.queries) == 0 {
		t.Errorf("plain grab of an incomplete album: replaced by %q, queries %v; want a search", got, h.queries)
	}
}

// The new-release search after a refresh skips an album with files gone from disk.
func TestNewReleaseSearchSkipsMissingFiles(t *testing.T) {
	h, _, a, _ := verifyLib(t, 2)
	h.markGone(t, "Album 00")
	h.c.searchNewReleases(h.ctx, []music.Album{h.album(t, a.ID, "Album 00")})
	if len(h.queries) != 0 {
		t.Errorf("queries = %v; want none for an album with files gone from disk", h.queries)
	}
}

// The bulk "Search for missing tracks" searches a few albums a run, says how many are left,
// and stops when no indexer answers.
func TestSearchMissingTracksIsBounded(t *testing.T) {
	h, _, _, _ := verifyLib(t, musicMissingPerRun+2)
	var titles []string
	for i := 0; i < musicMissingPerRun+2; i++ {
		titles = append(titles, fmt.Sprintf("Album %02d", i))
	}
	h.markGone(t, titles...)
	res, err := h.c.SearchMissingTracks(h.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Searched != musicMissingPerRun || res.Left != 2 || res.Stopped {
		t.Errorf("first run = %+v; want %d searched and 2 left", res, musicMissingPerRun)
	}

	h.queries = nil
	h.releases = func(indexer.SearchQuery) (indexer.SearchResult, error) {
		return indexer.SearchResult{}, fmt.Errorf("every indexer down")
	}
	res, err = h.c.SearchMissingTracks(h.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Stopped || len(h.queries) > musicMissingOutageStreak {
		t.Errorf("outage run = %+v after %d searches; want it stopped after %d", res, len(h.queries), musicMissingOutageStreak)
	}
}

// Runs capped at a few albums each still reach every album: the ones never searched go
// first, then the longest since their last search.
func TestSearchMissingTracksReachesEveryAlbum(t *testing.T) {
	n := musicMissingPerRun + 2
	h, _, _, _ := verifyLib(t, n)
	var titles []string
	for i := 0; i < n; i++ {
		titles = append(titles, fmt.Sprintf("Album %02d", i))
	}
	h.markGone(t, titles...)
	for run := 0; run < 2; run++ {
		if _, err := h.c.SearchMissingTracks(h.ctx, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, title := range titles {
		if !h.searchedFor(title) {
			t.Errorf("%s was never searched in two runs", title)
		}
	}
}

// A folder whose matched files are held back by a track's other file still on disk (a
// duplicate copy) isn't cached as done: the next scan looks at it again.
func TestScanDuplicateFolderIsNotCachedDone(t *testing.T) {
	h, lib, _, _ := verifyLib(t, 2)
	lib.add("Band/Album 00 (copy)/01 - One.flac", tagged("Band", "Album 00", 1, "One"))
	lib.add("Band/Album 00 (copy)/02 - Two.flac", tagged("Band", "Album 00", 2, "Two"))
	first := h.scan(t, MusicScanOptions{})
	if first.Held != 1 || first.Tracks != 0 {
		t.Fatalf("scan = %+v; want the copy held back, nothing recorded", first)
	}
	opened := lib.opened.Load()
	second := h.scan(t, MusicScanOptions{})
	if second.Held != 1 || lib.opened.Load() == opened {
		t.Errorf("second scan = %+v; want the copy looked at again, not skipped as done", second)
	}
	// The original goes away (the copy was a move half done): the copy is linked.
	if err := os.RemoveAll(filepath.Join(lib.root, "Band", "Album 00")); err != nil {
		t.Fatal(err)
	}
	// The scan that finds the original gone holds the copy back on its first pass; its file
	// check notes the original gone, and its follow-up pass records the copy.
	if third := h.scan(t, MusicScanOptions{}); third.Tracks != 2 || third.Held != 0 || !third.FollowUp {
		t.Errorf("third scan = %+v; want the copy recorded by the follow-up once the original is found gone", third)
	}
}

// A share mounted on a subfolder of the music folder drops (its mount point left empty)
// while a leftover copy of one of its albums sits elsewhere in the music folder. The file
// check skips the share's files, so they are never marked gone, and the scan never moves
// the album's records to the leftover copy; when the share is back, its files are still
// the album's.
func TestScanShareAwayNeverGivesTracksToACopy(t *testing.T) {
	h := musicTestCoord(t)
	lib := newScanLib(t, h)
	a := h.addArtist(t, "Band", "", []string{"Album A", "Album B"}, nil, "")
	one := lib.add("Mount/Band/Album A/01 - One.flac", tagged("Band", "Album A", 1, "One"))
	two := lib.add("Mount/Band/Album A/02 - Two.flac", tagged("Band", "Album A", 2, "Two"))
	lib.add("Local/Band/Album B/01 - One.flac", tagged("Band", "Album B", 1, "One"))
	lib.add("Local/Band/Album B/02 - Two.flac", tagged("Band", "Album B", 2, "Two"))
	h.scan(t, MusicScanOptions{})
	lib.add("Local/leftovers/Band - Album A/01 - One.flac", tagged("Band", "Album A", 1, "One"))
	lib.add("Local/leftovers/Band - Album A/02 - Two.flac", tagged("Band", "Album A", 2, "Two"))
	if res := h.scan(t, MusicScanOptions{}); res.Held != 1 {
		t.Fatalf("with both copies: %+v; want the leftover held", res)
	}
	al := h.album(t, a.ID, "Album A")
	away := filepath.Join(lib.root, "Mount", "Band")
	parked := lib.root + "-parked"
	if err := os.Rename(away, parked); err != nil { // the share drops: its mount point stays, empty
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		res := h.scan(t, MusicScanOptions{})
		if res.Tracks != 0 {
			t.Fatalf("scan %d with the share away = %+v; want nothing recorded", i, res)
		}
		if got := h.trackFiles(t, al.ID); got["One"] != one || got["Two"] != two {
			t.Fatalf("scan %d: records = %v; want them still on the share", i, got)
		}
		h.backdate(t)
	}
	if err := os.Rename(parked, away); err != nil { // back
		t.Fatal(err)
	}
	h.scan(t, MusicScanOptions{})
	if got := h.trackFiles(t, al.ID); got["One"] != one || got["Two"] != two {
		t.Errorf("share back: records = %v", got)
	}
	if tr := h.track(t, al.ID, "One"); tr.NotFoundSince != "" || tr.Missing {
		t.Errorf("share back: track = %+v; want no marks", tr)
	}
}
