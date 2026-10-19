package music

import (
	"fmt"
	"testing"
)

// A two-disc rip whose tags name each disc ("In Rainbows (Disc 1)" in CD1, "(Disc 2)" in
// CD2) is one album: one group, and it imports whole.
func TestDiscTaggedRipIsOneAlbum(t *testing.T) {
	twoDisc := edition("rel-2cd-t", "GB", "2007-12-03", "CD",
		[]tr{{"15 Step", 237, ""}, {"Bodysnatchers", 242, ""}, {"Nude", 255, ""}},
		[]tr{{"MK 1", 64, ""}, {"Down Is the New Up", 299, ""}, {"Go Slowly", 228, ""}})
	var files []MatchFile
	for _, m := range twoDisc.Media {
		for i, tk := range m.Tracks {
			files = append(files, MatchFile{
				Path:       fmt.Sprintf("/dl/In Rainbows/CD%d/%02d - %s.flac", m.Position, i+1, tk.Title),
				DurationMs: tk.LengthMs,
				Tags: FileTags{Album: fmt.Sprintf("In Rainbows (Disc %d)", m.Position), AlbumArtist: "Radiohead",
					Artist: "Radiohead", Title: tk.Title, Track: i + 1},
			})
		}
	}
	groups := GroupFiles(files)
	if len(groups) != 1 || len(groups[0].Files) != 6 {
		t.Fatalf("groups = %d, want one of 6", len(groups))
	}
	for _, s := range []string{"In Rainbows (Disc 1)", "In Rainbows [CD 2]", "In Rainbows CD2", "In Rainbows - Disc 1 of 2"} {
		if got := StripDiscSuffix(s); got != "In Rainbows" {
			t.Errorf("StripDiscSuffix(%q) = %q", s, got)
		}
	}
	res := Match(MatchInput{Files: groups[0].Files, Candidates: []MatchCandidate{cand(3, "In Rainbows", "Radiohead", 2007, twoDisc, true)}})
	if res.Decision != MatchImport || len(res.Assignments) != 6 {
		t.Fatalf("decision %s/%s placed %d: %s", res.Decision, res.Reason, len(res.Assignments), res.Summary)
	}
}

// Disc folders of one album whose tags name two different albums outright still merge:
// they sit side by side as CD1 and CD2.
func TestDiscFolderSiblingsMerge(t *testing.T) {
	g := GroupFiles([]MatchFile{
		{Path: "/dl/X/CD1/01.flac", Tags: FileTags{Album: "Part One", AlbumArtist: "X"}},
		{Path: "/dl/X/CD1/02.flac", Tags: FileTags{Album: "Part One", AlbumArtist: "X"}},
		{Path: "/dl/X/CD2/01.flac", Tags: FileTags{Album: "Part Two", AlbumArtist: "X"}},
		{Path: "/dl/X/CD2/02.flac", Tags: FileTags{Album: "Part Two", AlbumArtist: "X"}},
	})
	if len(g) != 1 || len(g[0].Files) != 4 {
		t.Fatalf("groups %+v, want one of four", g)
	}
}
