package automation

import (
	"strings"
	"testing"
)

// heldReview puts the rig's download in Reviews as the matcher would.
func (r *matchRig) heldReview(t *testing.T) int64 {
	t.Helper()
	res, err := r.h.c.db.Exec(`INSERT INTO import_reviews (hash, name, content_path, media_type, expected_id, reason, reason_code)
		VALUES (?, ?, ?, 'music', ?, 'held', 'low_confidence')`, hashFor(r.release), r.release, r.dl, r.album.ID)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (r *matchRig) editionID(t *testing.T, mbid string) int64 {
	t.Helper()
	eds, err := r.svc.Editions(r.h.ctx, r.album.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range eds {
		if e.MBID == mbid {
			return e.ID
		}
	}
	t.Fatalf("no edition %s", mbid)
	return 0
}

// "Import as <edition>" uses the edition a person chose even when the matcher would never
// weigh it (a bootleg), and says so plainly when the edition can't be used.
func TestReviewImportIntoAnEditionNotWeighed(t *testing.T) {
	all := append(append([]string(nil), okcTitles...), okcBonus...)
	r := newMatchRig(t, false, trackFileNames(all))
	boot := r.editionID(t, "rel-boot")
	id := r.heldReview(t)
	if err := r.h.c.ImportReview(WithMusicEdition(r.h.ctx, boot), id, 0, ""); err != nil {
		t.Fatal(err)
	}
	al, _ := r.svc.AlbumRecord(r.h.ctx, r.album.ID)
	if al.TargetEditionID != boot || al.HaveTracks != 9 {
		t.Errorf("target %d (want the bootleg %d), have %d (want 9)", al.TargetEditionID, boot, al.HaveTracks)
	}

	// An edition of another album is refused with a reason, and nothing changes.
	r2 := newMatchRig(t, false, trackFileNames(all))
	before, _ := r2.svc.AlbumRecord(r2.h.ctx, r2.album.ID)
	err := r2.h.c.ImportReview(WithMusicEdition(r2.h.ctx, 987654), r2.heldReview(t), 0, "")
	if err == nil || !strings.Contains(err.Error(), "chosen edition") {
		t.Fatalf("err = %v, want the chosen edition refused", err)
	}
	if after, _ := r2.svc.AlbumRecord(r2.h.ctx, r2.album.ID); after.TargetEditionID != before.TargetEditionID || after.HaveTracks != 0 {
		t.Errorf("a refused import changed the album: target %d→%d, have %d", before.TargetEditionID, after.TargetEditionID, after.HaveTracks)
	}
}

// "Import anyway" with files that fit no track places nothing, and leaves the album's
// edition as it was.
func TestImportAnywayOfNothingKeepsTheEdition(t *testing.T) {
	files := []string{"zz - Totally Unrelated.flac", "zz - Also Unrelated.flac"}
	r := newMatchRig(t, false, files)
	before, _ := r.svc.AlbumRecord(r.h.ctx, r.album.ID)
	if err := r.h.c.ImportReview(r.h.ctx, r.heldReview(t), 0, ""); err == nil {
		t.Fatal("importing files that fit nothing succeeded")
	}
	if after, _ := r.svc.AlbumRecord(r.h.ctx, r.album.ID); after.TargetEditionID != before.TargetEditionID {
		t.Errorf("the edition switched (%d → %d) though nothing was placed", before.TargetEditionID, after.TargetEditionID)
	}
}
