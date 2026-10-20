package automation

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/music"
)

// The file check's safety scenarios (the review of MUS-15): in none of them does anything
// become wanted or searched by itself, and in the unmounted ones nothing is even marked.

// searchedFor reports whether any search the sweep made named the album.
func (h *musicHarness) searchedFor(title string) bool {
	for _, q := range h.queries {
		if strings.Contains(q, title) {
			return true
		}
	}
	return false
}

// twoPasses runs the check, moves its not-found marks past the grace period, runs it again
// and then the search sweep.
func (h *musicHarness) twoPasses(t *testing.T) (MusicVerifyResult, MusicVerifyResult) {
	t.Helper()
	first := h.verify(t)
	h.backdate(t)
	second := h.verify(t)
	h.c.SearchMusicMissing(h.ctx)
	return first, second
}

// An album folder renamed by hand (a file manager, another tagger) with no scan after: the
// check finds its files gone and flags them, but nothing downloads the album again. A
// person can still ask, and then it is searched.
func TestVerifyRenameWithoutScanIsNeverSearched(t *testing.T) {
	h, lib, a, _ := verifyLib(t, 10)
	from := filepath.Join(lib.root, "Band", "Album 00")
	if err := os.Rename(from, filepath.Join(lib.root, "Band", "Album 00 (1999) [FLAC]")); err != nil {
		t.Fatal(err)
	}
	_, second := h.twoPasses(t)
	if second.Missing != 2 {
		t.Fatalf("second pass = %+v; want the two files flagged gone", second)
	}
	if h.searchedFor("Album 00") {
		t.Errorf("the sweep searched for an album whose files are only renamed: %v", h.queries)
	}
	wanted, err := h.svc.WantedAlbums(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range wanted {
		if w.Title == "Album 00" {
			t.Errorf("Album 00 is in the sweep's wanted list: %+v", w)
		}
	}
	al := h.album(t, a.ID, "Album 00")
	if _, err := h.c.SearchAlbumNow(h.ctx, al.ID); err != nil {
		t.Fatal(err)
	}
	if !h.searchedFor("Album 00") {
		t.Errorf("Search for missing tracks made no search: %v", h.queries)
	}
}

// One disk of a pool away (mergerfs): 15% of the files vanish at once while the music
// folder stays mounted. More than a deletion would explain: nothing is marked.
func TestVerifyPartialPoolUnmountMarksNothing(t *testing.T) {
	h, lib, _, _ := verifyLib(t, 70) // 140 files
	for i := 0; i < 11; i++ {        // 22 files, 15.7%
		if err := os.RemoveAll(filepath.Join(lib.root, "Band", fmt.Sprintf("Album %02d", i))); err != nil {
			t.Fatal(err)
		}
	}
	first, second := h.twoPasses(t)
	for _, res := range []MusicVerifyResult{first, second} {
		if res.Refused == "" || res.NotFound != 0 || res.Missing != 0 {
			t.Errorf("pass = %+v; want nothing marked and a warning", res)
		}
	}
	if len(h.queries) != 0 {
		t.Errorf("searches = %v; want none", h.queries)
	}
}

// A share mounted on a subfolder of the music folder goes away: its mount point stays,
// empty. The music folder itself is fine, so only the nearest folder still there tells.
func TestVerifySubfolderShareUnmountedMarksNothing(t *testing.T) {
	h := musicTestCoord(t)
	lib := newScanLib(t, h)
	var titles []string
	for i := 0; i < 12; i++ {
		titles = append(titles, fmt.Sprintf("Album %c", 'A'+i))
	}
	a := h.addArtist(t, "Band", "", titles, nil, "")
	for i, title := range titles {
		dir := "Local/Band/"
		if i < 2 {
			dir = "Share/Band/" // 4 of 24 files live on the share
		}
		lib.add(dir+title+"/01 - One.flac", tagged("Band", title, 1, "One"))
		lib.add(dir+title+"/02 - Two.flac", tagged("Band", title, 2, "Two"))
	}
	h.scan(t, MusicScanOptions{})
	if err := os.RemoveAll(filepath.Join(lib.root, "Share", "Band")); err != nil { // the mount point stays, empty
		t.Fatal(err)
	}
	first, second := h.twoPasses(t)
	for _, res := range []MusicVerifyResult{first, second} {
		if res.Refused == "" || res.NotFound != 0 || res.Missing != 0 || !strings.Contains(res.Refused, "Share") {
			t.Errorf("pass = %+v; want nothing marked and the share named", res)
		}
	}
	if tr := h.track(t, h.album(t, a.ID, "Album A").ID, "One"); !tr.HasFile || tr.NotFoundSince != "" {
		t.Errorf("a track on the share = %+v; want it untouched", tr)
	}
	if len(h.queries) != 0 {
		t.Errorf("searches = %v; want none", h.queries)
	}
}

// The share's mount point isn't empty (something else is in it), but it is now on another
// device than the files were: also away.
func TestVerifyFolderOnAnotherDeviceMarksNothing(t *testing.T) {
	h, lib, _, paths := verifyLib(t, 3)
	if music.FileIdentity(mustStat(t, paths[0])) == "" {
		t.Skip("no inodes on this platform")
	}
	// The files were recorded on another device, and their album folder is gone.
	if _, err := h.st.DB().Exec(`UPDATE music_track_files SET file_id = '4242:' || id WHERE path LIKE ?`, "%Album 00%"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(lib.root, "Band", "Album 00")); err != nil {
		t.Fatal(err)
	}
	if res := h.verify(t); res.Refused == "" || res.NotFound != 0 {
		t.Errorf("pass = %+v; want nothing marked", res)
	}
}
