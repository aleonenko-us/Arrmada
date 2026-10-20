package music

import (
	"fmt"
	"testing"
)

// With nothing but names to go on (no tags, no fingerprints), a library folder is placed
// only when it is the whole album, or exactly the tracks still wanted; part of an album is
// held, even when its folder names the album (the library scan). A folder named
// "Greatest Hits" holding another compilation of that name must not be filed by overlap.
func TestNameOnlyPlacesOnlyWholeAlbums(t *testing.T) {
	var lib []tr
	for i := 0; i < 17; i++ {
		lib = append(lib, tr{fmt.Sprintf("Hit Song %02d", i+1), 180 + i*7, fmt.Sprintf("rec-hit-%02d", i+1)})
	}
	ed := edition("rel-gh1", "GB", "1981-10-26", "CD", lib)
	c := cand(1, "Greatest Hits", "Queen", 1981, ed, true)
	other := func(overlap int) []tr {
		var out []tr
		for i := 0; i < overlap; i++ {
			out = append(out, lib[(i*2)%17])
		}
		for i := overlap; i < 17; i++ {
			out = append(out, tr{fmt.Sprintf("Other Song %02d", i), 200 + i*11, fmt.Sprintf("rec-other-%02d", i)})
		}
		return out
	}
	for _, overlap := range []int{12, 9, 6} {
		files := filesFor("/m/Queen/Greatest Hits", other(overlap), nil)
		res := Match(MatchInput{Files: files, FolderName: "Queen - Greatest Hits", Candidates: []MatchCandidate{c}, GrabbedAlbumID: 1, ExpectedFromNames: true})
		if res.Decision != MatchReview || len(res.Assignments) != 0 {
			t.Errorf("untagged, %d of 17 songs shared: %s with %d placed; want it held", overlap, res.Decision, len(res.Assignments))
		}
	}
	whole := filesFor("/m/Queen/Greatest Hits", lib, nil)
	res := Match(MatchInput{Files: whole, FolderName: "Queen - Greatest Hits", Candidates: []MatchCandidate{c}, GrabbedAlbumID: 1, ExpectedFromNames: true})
	if res.Decision != MatchImport || len(res.Assignments) != 17 {
		t.Errorf("untagged whole album: %s with %d placed; want it imported", res.Decision, len(res.Assignments))
	}
	// Tagged files of half the album still import as a partial: the tags are evidence.
	half := filesFor("/m/Queen/Greatest Hits", lib[:10], plainTags("Greatest Hits", "Queen"))
	res = Match(MatchInput{Files: half, FolderName: "Queen - Greatest Hits", Candidates: []MatchCandidate{c}, GrabbedAlbumID: 1, ExpectedFromNames: true})
	if res.Decision != MatchPartial {
		t.Errorf("tagged 10 of 17: %s; want a partial import", res.Decision)
	}
}
