package automation

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/music"
)

// The file check's safety scenarios (the reviews of MUS-15): in none of them does anything
// become wanted or searched by itself; files under a folder that looks away are skipped,
// and the whole pass is refused only for the music folder itself or a mass vanish.

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

// An album folder renamed AND retagged outside Arrmada (Picard, `beet import -w`): the same
// inodes but new sizes and times, so the scan rightly doesn't follow them by inode. It
// matches them as the album's files, and since the recorded paths are gone from disk the
// album's tracks take them: re-linked, nothing flagged gone from disk, nothing to search.
func TestScanRelinksRenamedAndRetaggedAlbum(t *testing.T) {
	h, lib, a, _ := verifyLib(t, 10)
	from := filepath.Join(lib.root, "Band", "Album 00")
	to := filepath.Join(lib.root, "Band", "Album 00 (1999) [FLAC]")
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(3 * time.Hour)
	for i, n := range []string{"01 - One.flac", "02 - Two.flac"} {
		p := filepath.Join(to, n)
		if err := os.Chtimes(p, later, later); err != nil {
			t.Fatal(err)
		}
		lib.mu.Lock()
		lib.tags[p] = tagged("Band", "Album 00", i+1, []string{"One", "Two"}[i])
		lib.mu.Unlock()
	}
	res := h.scan(t, MusicScanOptions{})
	if res.Moved != 0 || res.Tracks != 2 {
		t.Fatalf("scan = %+v; want the two files matched and recorded (not followed)", res)
	}
	al := h.album(t, a.ID, "Album 00")
	for _, title := range []string{"One", "Two"} {
		tr := h.track(t, al.ID, title)
		if !tr.HasFile || tr.Missing || tr.NotFoundSince != "" || !strings.HasPrefix(tr.FilePath, to) {
			t.Errorf("track %s = %+v; want it re-linked to %s", title, tr, to)
		}
	}
	h.backdate(t)
	if v := h.verify(t); v.NotFound != 0 || v.Missing != 0 {
		t.Errorf("verify after the re-link = %+v; want nothing gone", v)
	}
	if got := h.album(t, a.ID, "Album 00"); got.MissingTracks != 0 {
		t.Errorf("album missing_tracks = %d", got.MissingTracks)
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
		if res.Refused != "" || res.NotFound != 0 || res.Missing != 0 || !strings.Contains(res.Warning(), "Share") {
			t.Errorf("pass = %+v; want the share's files skipped and named in the note, the pass not refused", res)
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
	if res := h.verify(t); len(res.Away) != 1 || res.NotFound != 0 {
		t.Errorf("pass = %+v; want the files skipped, their folder named", res)
	}
}

// A person empties an album folder but leaves it behind. Its files are skipped (it looks
// like a mount point that's away) and named in the note; the rest of the library is still
// checked, so a real deletion elsewhere is noted.
func TestVerifyEmptyFolderLeftBehindDoesNotBlockTheCheck(t *testing.T) {
	h, lib, _, _ := verifyLib(t, 10)
	dir := filepath.Join(lib.root, "Band", "Album 00")
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		res := h.verify(t)
		if res.Refused != "" || res.NotFound != 0 || len(res.Away) != 1 || !strings.Contains(h.c.MusicFilesWarning(), "Album 00") {
			t.Fatalf("pass %d = %+v; want Album 00's files skipped and named, the pass not refused", i, res)
		}
		h.backdate(t)
	}
	if err := os.RemoveAll(filepath.Join(lib.root, "Band", "Album 01")); err != nil {
		t.Fatal(err)
	}
	if res := h.verify(t); res.NotFound != 2 || res.Refused != "" {
		t.Errorf("a real deletion elsewhere: %+v; want its two files noted", res)
	}
}

// Device numbers renumbered by a reboot or remount (ZFS, FUSE, NFS, SMB, btrfs): files
// already missing keep the old number, which must not read as "on another disk" for good.
// The pass learns the new number from the files still there.
func TestVerifyDeviceRenumberedAfterReboot(t *testing.T) {
	h, lib, _, paths := verifyLib(t, 10)
	if music.FileIdentity(mustStat(t, paths[0])) == "" {
		t.Skip("no inodes on this platform")
	}
	if err := os.RemoveAll(filepath.Join(lib.root, "Band", "Album 00")); err != nil {
		t.Fatal(err)
	}
	h.verify(t)
	h.backdate(t)
	if res := h.verify(t); res.Missing != 2 {
		t.Fatalf("setup: %+v", res)
	}
	// "Reboot": every recorded device number is now an old one.
	if _, err := h.st.DB().Exec(`UPDATE music_track_files SET file_id = '777777:' || substr(file_id, instr(file_id, ':') + 1)`); err != nil {
		t.Fatal(err)
	}
	if res := h.verify(t); res.Refused != "" || len(res.Away) != 0 {
		t.Fatalf("pass after the reboot = %+v; want nothing away", res)
	}
	if err := os.RemoveAll(filepath.Join(lib.root, "Band", "Album 01")); err != nil { // a real deletion
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		res := h.verify(t)
		if res.Refused != "" || len(res.Away) != 0 {
			t.Errorf("pass %d = %+v; want the check working", i, res)
		}
		if i == 0 && res.NotFound != 2 {
			t.Errorf("the deletion after the reboot wasn't noted: %+v", res)
		}
		h.backdate(t)
	}
}
