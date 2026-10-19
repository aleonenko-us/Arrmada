package music

import (
	"fmt"
	"testing"
)

// Review findings on the matcher (MUS-31), one test each.

// "Import anyway" whose files fit nothing places nothing, and switches nothing either.
func TestForcedImportOfNothingSwitchesNothing(t *testing.T) {
	okc := okcTracks()
	std := edition("rel-f-std", "GB", "1997-05-21", "CD", okc)
	files := filesFor("/dl/x", []tr{{"Foo", 100, ""}, {"Bar", 500, ""}}, plainTags("Something Else", "Nobody"))
	for i := range files {
		files[i].Tags.Track = 0
		files[i].Path = fmt.Sprintf("/dl/x/zz%d.flac", i)
	}
	res := Match(MatchInput{Files: files, Forced: true,
		Candidates: []MatchCandidate{cand(1, "OK Computer", "Radiohead", 1997, std, false)}})
	if len(res.Assignments) != 0 || res.Switch || res.Decision != MatchReview {
		t.Errorf("placed %d, switch %v, decision %s: want nothing placed and no switch", len(res.Assignments), res.Switch, res.Decision)
	}
}

// Files with no recording ids in their tags, which AcoustID hears as two recordings each
// (the same take on a compilation, listed first, and on this album): the album's one
// counts, nothing reads as a tag naming another album, and the files import.
func TestFingerprintsDontMakeTagConflicts(t *testing.T) {
	okc := okcTracks()
	std := edition("rel-fp-std", "GB", "1997-05-21", "CD", okc)
	files := filesFor("/dl/okc", okc, plainTags("OK Computer", "Radiohead"))
	for i := range files {
		files[i].Fingerprint = []string{fmt.Sprintf("rec-compilation-%d", i), okc[i].rec}
	}
	res := Match(MatchInput{Files: files, GrabbedAlbumID: 1,
		Candidates: []MatchCandidate{cand(1, "OK Computer", "Radiohead", 1997, std, true)}})
	if res.Decision != MatchImport || res.Reason == MatchReasonTagConflict {
		t.Fatalf("decision %s/%s: %s", res.Decision, res.Reason, res.Summary)
	}
	// And a file AcoustID hears as another recording is penalised on the id.
	tk := candTrack{Title: "Airbag", RecordingMBID: "rec-okc-01"}
	f := MatchFile{Tags: FileTags{Title: "Airbag"}, Fingerprint: []string{"rec-something-else"}}
	if d, _, notes := trackDistance(&f, &tk, false); d == 0 {
		t.Errorf("a fingerprint naming another recording cost nothing (%v)", notes)
	}
}

// "The target wins a close call" holds between editions of one album only: two albums
// that fit almost equally always ask a person, target or not.
func TestCloseCallBetweenAlbumsGoesToReview(t *testing.T) {
	okc := okcTracks()
	a := cand(1, "OK Computer", "Radiohead", 1997, edition("rel-a1", "GB", "1997-05-21", "CD", okc), true)
	b := cand(2, "OK Computer", "Radiohead", 1997, edition("rel-b1", "GB", "1997-05-21", "CD", okc), false)
	res := Match(MatchInput{Files: filesFor("/dl/okc", okc, plainTags("OK Computer", "Radiohead")),
		Candidates: []MatchCandidate{b, a}})
	if res.Decision != MatchReview || res.Reason != MatchReasonCloseEditions {
		t.Errorf("decision %s/%s, want close_editions between two albums: %s", res.Decision, res.Reason, res.Summary)
	}
}
