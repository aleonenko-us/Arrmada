package automation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/mediainfo"
	"github.com/tristenlammi/arrmada/internal/music"
)

// verifyLib is a scanned library of albums with two tracks each, recorded by a scan.
func verifyLib(t *testing.T, albums int) (*musicHarness, *scanLib, music.Artist, []string) {
	t.Helper()
	h := musicTestCoord(t)
	lib := newScanLib(t, h)
	var titles []string
	for i := 0; i < albums; i++ {
		titles = append(titles, fmt.Sprintf("Album %02d", i))
	}
	a := h.addArtist(t, "Band", "", titles, nil, "")
	var paths []string
	for _, title := range titles {
		paths = append(paths,
			lib.add("Band/"+title+"/01 - One.flac", tagged("Band", title, 1, "One")),
			lib.add("Band/"+title+"/02 - Two.flac", tagged("Band", title, 2, "Two")))
	}
	if res := h.scan(t, MusicScanOptions{}); res.Tracks != 2*albums {
		t.Fatalf("setup scan = %+v", res)
	}
	return h, lib, a, paths
}

func (h *musicHarness) verify(t *testing.T) MusicVerifyResult {
	t.Helper()
	res, err := h.c.VerifyMusicFiles(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// backdate moves every not-found mark back past the grace period.
func (h *musicHarness) backdate(t *testing.T) {
	t.Helper()
	at := time.Now().Add(-musicMissingGrace - time.Hour).UTC().Format(time.RFC3339)
	if _, err := h.st.DB().Exec(`UPDATE music_track_files SET missing_seen_at = ? WHERE missing_seen_at IS NOT NULL`, at); err != nil {
		t.Fatal(err)
	}
}

func (h *musicHarness) track(t *testing.T, albumID int64, title string) music.Track {
	t.Helper()
	tracks, err := h.svc.Tracks(h.ctx, albumID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range tracks {
		if tr.Title == title {
			return tr
		}
	}
	t.Fatalf("no track %q", title)
	return music.Track{}
}

// MUS-15: a file gone from disk is only noted on the first pass; a pass after the grace
// period marks it missing (flagged as gone, its row kept, never searched); when it's back it's
// linked again. Nothing is ever deleted.
func TestVerifyMarksMissingAfterGrace(t *testing.T) {
	h, lib, a, paths := verifyLib(t, 2)
	al := h.album(t, a.ID, "Album 00")
	if err := os.Remove(paths[0]); err != nil {
		t.Fatal(err)
	}
	before := lib.snapshot()

	first := h.verify(t)
	if first.NotFound != 1 || first.Missing != 0 || first.Refused != "" {
		t.Fatalf("first pass = %+v; want the file noted, not missing", first)
	}
	if tr := h.track(t, al.ID, "One"); !tr.HasFile || tr.NotFoundSince == "" {
		t.Errorf("after one miss the track = %+v; want it still held, noted as not found", tr)
	}
	if again := h.verify(t); again.Missing != 0 || again.NotFound != 0 {
		t.Errorf("a second pass within the grace period = %+v; want nothing new", again)
	}

	h.backdate(t)
	second := h.verify(t)
	if second.Missing != 1 {
		t.Fatalf("pass after the grace period = %+v; want the file missing", second)
	}
	tr := h.track(t, al.ID, "One")
	if tr.HasFile || !tr.Missing || tr.FileID == 0 || tr.FilePath != paths[0] {
		t.Errorf("missing track = %+v; want it flagged with its file row kept", tr)
	}
	if got := h.album(t, a.ID, "Album 00"); got.MissingTracks != 1 {
		t.Errorf("album missing_tracks = %d, want 1", got.MissingTracks)
	}
	events, _ := h.svc.Events(h.ctx, a.ID, 20)
	found := false
	for _, e := range events {
		found = found || (e.Event == "missing" && strings.Contains(e.Detail, "Album 00"))
	}
	if !found {
		t.Errorf("no missing event in %+v", events)
	}
	if after := lib.snapshot(); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Errorf("the check changed files on disk:\nbefore %v\nafter  %v", before, after)
	}

	// Back on disk (a remount): linked again.
	sparse(t, paths[0], 300<<10)
	back := h.verify(t)
	if back.Back != 1 {
		t.Errorf("pass with the file back = %+v", back)
	}
	if tr := h.track(t, al.ID, "One"); !tr.HasFile || tr.Missing || tr.NotFoundSince != "" {
		t.Errorf("track back on disk = %+v", tr)
	}
}

// An unmounted or empty music folder marks nothing and raises a warning; so does a pass
// where more files vanish than a deletion would explain.
func TestVerifyGuardsAgainstUnmountedLibrary(t *testing.T) {
	h, lib, a, _ := verifyLib(t, 13)
	moved := lib.root + ".away"
	if err := os.Rename(lib.root, moved); err != nil {
		t.Fatal(err)
	}
	res := h.verify(t)
	if res.Refused == "" || res.NotFound != 0 || res.Missing != 0 || h.c.MusicFilesWarning() == "" {
		t.Fatalf("unmounted folder: %+v; want nothing marked and a warning", res)
	}
	if err := os.MkdirAll(lib.root, 0o755); err != nil { // mounted point, nothing in it
		t.Fatal(err)
	}
	if res := h.verify(t); res.Refused == "" || res.NotFound != 0 {
		t.Fatalf("empty music folder: %+v; want nothing marked", res)
	}
	if err := os.Remove(lib.root); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(moved, lib.root); err != nil {
		t.Fatal(err)
	}
	if res := h.verify(t); res.Refused != "" || res.NotFound != 0 || h.c.MusicFilesWarning() != "" {
		t.Fatalf("remounted: %+v; want all well again", res)
	}

	// 22 of 26 files gone at once (11 album folders): more than max(20, 5%).
	for i := 0; i < 11; i++ {
		if err := os.RemoveAll(filepath.Join(lib.root, "Band", fmt.Sprintf("Album %02d", i))); err != nil {
			t.Fatal(err)
		}
	}
	res = h.verify(t)
	if res.Refused == "" || res.NotFound != 0 {
		t.Fatalf("mass vanish: %+v; want nothing marked", res)
	}
	h.backdate(t)
	if res := h.verify(t); res.Missing != 0 {
		t.Errorf("mass vanish, later pass: %+v; want still nothing marked", res)
	}
	for _, title := range []string{"Album 00", "Album 05", "Album 10"} {
		if tr := h.track(t, h.album(t, a.ID, title).ID, "One"); !tr.HasFile || tr.NotFoundSince != "" {
			t.Errorf("%s after refused passes = %+v; want untouched", title, tr)
		}
	}
}

// A file changed outside Arrmada is measured again and its house-tags state rechecked.
func TestVerifyRemeasuresChangedFiles(t *testing.T) {
	h, _, a, paths := verifyLib(t, 1)
	al := h.album(t, a.ID, "Album 00")
	if _, err := h.st.DB().Exec(`UPDATE music_track_files SET tag_state = 'house'`); err != nil {
		t.Fatal(err)
	}
	probed := 0
	h.c.probeAudioFn = func(_ context.Context, path string) (mediainfo.AudioInfo, error) {
		probed++
		return mediainfo.AudioInfo{Codec: "flac", BitrateKbps: 900, SampleRateHz: 96000, BitsPerSample: 24, Channels: 2, DurationSec: 200}, nil
	}
	h.c.musicVerify.houseFn = func(_ music.TagContext, _ int64, path string) (bool, bool) {
		return !strings.HasSuffix(path, "01 - One.flac"), true // only One was retagged elsewhere
	}
	if res := h.verify(t); res.Changed != 0 || probed != 0 {
		t.Fatalf("unchanged library: %+v, %d probes", res, probed)
	}
	if err := os.Truncate(paths[0], 400<<10); err != nil { // another tool rewrote it
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(paths[0], future, future); err != nil {
		t.Fatal(err)
	}
	res := h.verify(t)
	if res.Changed != 1 || probed != 1 {
		t.Fatalf("changed file: %+v, %d probes; want it measured again", res, probed)
	}
	one, two := h.track(t, al.ID, "One"), h.track(t, al.ID, "Two")
	if one.ChangedAt == "" || one.Format != string(music.QualityFLAC24) || one.TagState != music.TagStateAsFound || one.SizeBytes != 400<<10 {
		t.Errorf("changed track = %+v; want it measured (FLAC-24), as found, the new size recorded", one)
	}
	if two.ChangedAt != "" || two.TagState != music.TagStateHouse {
		t.Errorf("unchanged track = %+v", two)
	}
	if got := h.album(t, a.ID, "Album 00"); got.ChangedTracks != 1 {
		t.Errorf("album changed_tracks = %d, want 1", got.ChangedTracks)
	}
	if res := h.verify(t); res.Changed != 0 {
		t.Errorf("a second pass re-measured the same change: %+v", res)
	}
}
