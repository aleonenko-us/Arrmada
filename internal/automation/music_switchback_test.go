package automation

import (
	"os"
	"testing"
)

// A match that switches the album's edition and then places nothing (here the music
// folder is missing; equally no space, or a tag that didn't check out) puts the album
// back on the edition it had: the switch only stands with files to show for it.
func TestMatcherSwitchStandsOnlyWithAPlacement(t *testing.T) {
	all := append(append([]string(nil), okcTitles...), okcBonus...)
	files := trackFileNames(all)
	r := newMatchRig(t, true, files)
	r.tagAll(files, all)
	before, _ := r.svc.AlbumRecord(r.h.ctx, r.album.ID)
	if err := os.RemoveAll(r.h.c.imp.MusicDir()); err != nil {
		t.Fatal(err)
	}
	r.h.c.ImportMusicDownloads(r.h.ctx)
	after, _ := r.svc.AlbumRecord(r.h.ctx, r.album.ID)
	if after.HaveTracks != 0 {
		t.Fatalf("have %d tracks with no music folder", after.HaveTracks)
	}
	if after.TargetEditionID != before.TargetEditionID || after.EditionPinned != before.EditionPinned || after.EditionTracks != 6 {
		t.Errorf("edition %d (%d tracks, pinned %v), want %d (6 tracks, pinned %v) as before",
			after.TargetEditionID, after.EditionTracks, after.EditionPinned, before.TargetEditionID, before.EditionPinned)
	}
	// It did switch (the download fits the deluxe) and then switched back.
	if n := artistEvents(t, r.svc, r.artist.ID, "back: nothing"); n != 1 {
		t.Errorf("history lines about switching back = %d, want 1", n)
	}
}
