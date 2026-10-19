package automation

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/music"
)

// One file is never two tracks: when the file a track would be placed at is already
// another track's (the upgrade's shared_path case: an old scan recorded one file for two
// tracks), the import leaves that file alone, counts the track as taken, and the download
// goes to Review instead of retrying.
func TestImportNeverReplacesAnotherTracksFile(t *testing.T) {
	h, _ := musicImportHarness(t)
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	if err := h.svc.EnsureTracks(h.ctx, al); err != nil {
		t.Fatal(err)
	}
	tracks, _ := h.svc.Tracks(h.ctx, al.ID)
	// Track Two holds the file track One's import would be named.
	clash := h.c.imp.AlbumTrackTarget(a.Name, al.Title, al.Year, 1, 1, "One", ".flac")
	blobTrack(t, filepath.Dir(clash), filepath.Base(clash), 9)
	want, _ := os.ReadFile(clash)
	if err := music.NewRepo(h.st.DB()).SetTrackFile(h.ctx, tracks[1].ID, clash, "FLAC", 900, int64(len(want)), ""); err != nil {
		t.Fatal(err)
	}

	res := h.importDownload(t, a, al, "Radiohead - OK Computer (1997) [FLAC]", ".flac", 1)
	if res.taken != 1 || res.placed != 0 {
		t.Fatalf("import = %+v, want track One taken (its path is Two's) and nothing placed", res)
	}
	if got, _ := os.ReadFile(clash); !bytes.Equal(got, want) {
		t.Error("the file another track holds was replaced")
	}
	if owner, _ := h.svc.FileOwner(h.ctx, clash); owner != tracks[1].ID {
		t.Errorf("the clashing file now belongs to track %d, want Two (%d)", owner, tracks[1].ID)
	}

	it := download.Item{Hash: "clashhash", Name: "Radiohead - OK Computer (1997) [FLAC]", ContentPath: t.TempDir()}
	h.c.musicTakenReview(h.ctx, it, res, al.ID, al.Title)
	if !h.c.hasReview(h.ctx, it.Hash) {
		t.Error("a download whose tracks would replace another track's file should wait in Review")
	}
}
