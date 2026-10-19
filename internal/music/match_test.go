package music

import "testing"

func TestTrackNumberOf(t *testing.T) {
	cases := []struct {
		path        string
		disc, track int
	}{
		{"01 - Airbag.flac", 1, 1},
		{"01. Airbag.mp3", 1, 1},
		{"07_Fitter Happier.flac", 1, 7},
		{"2-04 Some Song.flac", 2, 4},
		{"Airbag.flac", 0, 0},           // no number at all
		{"1997 - Album Rip.flac", 0, 0}, // a year is not a track number
		// MUS-06: a title that starts with a number is not a disc-track pair.
		{"04 - 99 Problems.flac", 1, 4},
		{"10 - 7 Rings.flac", 1, 10},
		{"05. 4 Minutes.flac", 1, 5},
		{"01 - 22 (Taylor's Version).flac", 1, 1},
	}
	for _, c := range cases {
		d, n := TrackNumberOf(c.path)
		if d != c.disc || n != c.track {
			t.Errorf("TrackNumberOf(%q) = (%d,%d), want (%d,%d)", c.path, d, n, c.disc, c.track)
		}
	}
}

// Numbering first, title second, and never position-in-directory: a stray bonus track or
// intro file would shift every song after it, and an album labelled one track out is far
// worse than one left unmatched for a human to look at.
func TestMatchTracks(t *testing.T) {
	tracks := []Track{
		{ID: 1, DiscNumber: 1, TrackNumber: 1, Title: "Airbag"},
		{ID: 2, DiscNumber: 1, TrackNumber: 2, Title: "Paranoid Android"},
		{ID: 3, DiscNumber: 1, TrackNumber: 3, Title: "Subterranean Homesick Alien"},
	}
	files := []AudioFile{
		{Path: "/d/01 - Airbag.flac"},
		{Path: "/d/02 - Paranoid Android.flac"},
		{Path: "/d/Subterranean Homesick Alien.flac"}, // no number — matched by title
		{Path: "/d/bonus - Untitled Jam.flac"},        // matches nothing
	}
	matched, unmatched := MatchTracks(tracks, files)
	if len(matched) != 3 {
		t.Fatalf("want 3 matched, got %d", len(matched))
	}
	if matched[1].Path != "/d/01 - Airbag.flac" || matched[3].Path != "/d/Subterranean Homesick Alien.flac" {
		t.Errorf("wrong mapping: %+v", matched)
	}
	if len(unmatched) != 1 || unmatched[0].Path != "/d/bonus - Untitled Jam.flac" {
		t.Errorf("the unmatched file must be reported, got %+v", unmatched)
	}

	// A file whose number doesn't exist on the album is left alone, not forced onto a slot.
	m2, u2 := MatchTracks(tracks, []AudioFile{{Path: "/d/09 - Ghost Track.flac"}})
	if len(m2) != 0 || len(u2) != 1 {
		t.Errorf("an out-of-range number must not be placed: matched=%v unmatched=%v", m2, u2)
	}
}

// MUS-06: songs whose titles start with a number land on their own slot, never on the
// slot the number in their title points at.
func TestMatchTracksNumericTitles(t *testing.T) {
	tracks := []Track{
		{ID: 1, DiscNumber: 1, TrackNumber: 1, Title: "22 (Taylor's Version)"},
		{ID: 4, DiscNumber: 1, TrackNumber: 4, Title: "99 Problems"},
		{ID: 5, DiscNumber: 1, TrackNumber: 5, Title: "4 Minutes"},
		{ID: 10, DiscNumber: 1, TrackNumber: 10, Title: "7 Rings"},
		{ID: 22, DiscNumber: 1, TrackNumber: 22, Title: "Closer"},
	}
	files := []AudioFile{
		{Path: "/d/04 - 99 Problems.flac"},
		{Path: "/d/10 - 7 Rings.flac"},
		{Path: "/d/05. 4 Minutes.flac"},
		{Path: "/d/01 - 22 (Taylor's Version).flac"},
	}
	matched, unmatched := MatchTracks(tracks, files)
	want := map[int64]string{
		4: "/d/04 - 99 Problems.flac", 10: "/d/10 - 7 Rings.flac",
		5: "/d/05. 4 Minutes.flac", 1: "/d/01 - 22 (Taylor's Version).flac",
	}
	for id, path := range want {
		if matched[id].Path != path {
			t.Errorf("track %d got %q, want %q", id, matched[id].Path, path)
		}
	}
	if _, ok := matched[22]; ok || len(unmatched) != 0 {
		t.Errorf("nothing may land on track 22 and nothing is left over: matched=%v unmatched=%v", matched, unmatched)
	}
}

// The misfile the old parser made: "01 - 7 Rings" read as disc 1, track 7, so it took the
// real track 7's slot.
func TestMatchTracksNoOneOffMisfile(t *testing.T) {
	tracks := []Track{
		{ID: 1, DiscNumber: 1, TrackNumber: 1, Title: "7 Rings"},
		{ID: 7, DiscNumber: 1, TrackNumber: 7, Title: "Fake Smile"},
	}
	matched, unmatched := MatchTracks(tracks, []AudioFile{
		{Path: "/d/01 - 7 Rings.flac"}, {Path: "/d/07 - Fake Smile.flac"},
	})
	if matched[1].Path != "/d/01 - 7 Rings.flac" || matched[7].Path != "/d/07 - Fake Smile.flac" || len(unmatched) != 0 {
		t.Errorf("matched=%v unmatched=%v", matched, unmatched)
	}
	// A file named only by its title, which starts with a number, goes by the title.
	m2, _ := MatchTracks(tracks, []AudioFile{{Path: "/d/7 Rings.flac"}})
	if m2[1].Path != "/d/7 Rings.flac" {
		t.Errorf("'7 Rings.flac' must land on 7 Rings, got %v", m2)
	}
	if _, ok := m2[7]; ok {
		t.Error("'7 Rings.flac' must not land on track 7")
	}
}

func TestMatchTracksMultiDisc(t *testing.T) {
	twoDisc := []Track{
		{ID: 101, DiscNumber: 1, TrackNumber: 1, Title: "Opening"},
		{ID: 104, DiscNumber: 1, TrackNumber: 4, Title: "Airbag"},
		{ID: 201, DiscNumber: 2, TrackNumber: 1, Title: "X"},
		{ID: 203, DiscNumber: 2, TrackNumber: 3, Title: "Third"},
	}
	// The compact disc-track shape, two files of it in one folder: believed.
	m, u := MatchTracks(twoDisc, []AudioFile{{Path: "/d/1-04 Airbag.flac"}, {Path: "/d/2-01 X.flac"}})
	if m[104].Path != "/d/1-04 Airbag.flac" || m[201].Path != "/d/2-01 X.flac" || len(u) != 0 {
		t.Errorf("multi-disc: matched=%v unmatched=%v", m, u)
	}
	// A three-digit number on a multi-disc listing is disc and track.
	if m, _ := MatchTracks(twoDisc, []AudioFile{{Path: "/d/101 - Something Else.flac"}}); m[101].Path == "" {
		t.Errorf("'101 - …' should be disc 1 track 1, got %v", m)
	}
	// A folder hint says which disc a bare track number is on.
	if m, _ := MatchTracks(twoDisc, []AudioFile{{Path: "/d/CD2/03 - Something.flac", Disc: 2}}); m[203].Path == "" {
		t.Errorf("a Disc=2 hint with '03 - …' should be disc 2 track 3, got %v", m)
	}

	oneDisc := []Track{
		{ID: 4, DiscNumber: 1, TrackNumber: 4, Title: "99 Problems"},
		{ID: 9, DiscNumber: 1, TrackNumber: 9, Title: "Other"},
	}
	// Alone on a one-disc listing the compact shape is a track number and a title.
	if m, _ := MatchTracks(oneDisc, []AudioFile{{Path: "/d/04-99 Problems.flac"}}); m[4].Path == "" {
		t.Errorf("'04-99 Problems' alone should be track 4, got %v", m)
	}
	// One compact-shaped file in a folder isn't enough to believe the shape.
	threeTracks := []Track{
		{ID: 1, DiscNumber: 1, TrackNumber: 1, Title: "A"},
		{ID: 2, DiscNumber: 1, TrackNumber: 2, Title: "B"},
	}
	if m, _ := MatchTracks(threeTracks, []AudioFile{{Path: "/d/01-02 Untitled.flac"}, {Path: "/d/02 B.flac"}}); m[1].Path != "/d/01-02 Untitled.flac" {
		t.Errorf("a single compact-shaped file falls back to its track number, got %v", m)
	}
}

// The gate that stops a search for one album grabbing a different one by the same artist —
// the exact hole the Books module had.
func TestReleaseIsForAlbum(t *testing.T) {
	if !ReleaseIsForAlbum("Radiohead - OK Computer (1997) [FLAC]", "Radiohead", "OK Computer") {
		t.Error("the album's own release should pass")
	}
	if ReleaseIsForAlbum("Radiohead - Kid A (2000) [FLAC]", "Radiohead", "OK Computer") {
		t.Error("a different album by the same artist must be refused")
	}
	if ReleaseIsForAlbum("Radiohead - Kid Amnesiae (2021) [FLAC]", "Radiohead", "Kid A") {
		t.Error("word boundaries matter — Kid A must not match inside Kid Amnesiae")
	}
	if ReleaseIsForAlbum("Radiohead - Discography (1993-2016) [FLAC]", "Radiohead", "OK Computer") {
		t.Error("a discography would fill one album's folder with the whole catalogue")
	}
	if ReleaseIsForAlbum("Muse - OK Computer Tribute [FLAC]", "Radiohead", "OK Computer") {
		t.Error("the artist must match too")
	}
}

// A discography must be recognisable for the artist it belongs to, and only through the
// explicit path — ReleaseIsForAlbum still refuses it, so an automatic album sweep can never
// pick one up by accident.
func TestReleaseIsDiscographyFor(t *testing.T) {
	const disco = "Radiohead - Discography (1993-2016) [FLAC]"
	if !ReleaseIsDiscographyFor(disco, "Radiohead") {
		t.Error("a discography naming the artist should match")
	}
	if ReleaseIsDiscographyFor(disco, "Portishead") {
		t.Error("a discography by a different artist must not match")
	}
	// A single album is not a discography, whichever function asks.
	if ReleaseIsDiscographyFor("Radiohead - OK Computer (1997) [FLAC]", "Radiohead") {
		t.Error("a single album must not be treated as a discography")
	}
	// The album path must still refuse it, so the automatic sweep can't grab one.
	if ReleaseIsForAlbum(disco, "Radiohead", "OK Computer") {
		t.Error("the album gate must keep refusing discographies")
	}
	// Other whole-catalogue wordings.
	for _, n := range []string{
		"Radiohead - Complete Collection [FLAC]",
		"Radiohead Anthology 1993-2016 [MP3-320]",
	} {
		if !ReleaseIsDiscographyFor(n, "Radiohead") {
			t.Errorf("%q should read as a whole-catalogue pack", n)
		}
	}
}
