package music

import "testing"

func TestFoldMusic(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Björk", "bjork"},
		{"Motörhead", "motorhead"},
		{"Sigur Rós", "sigur ros"},
		{"AC/DC", "acdc"},
		{"Sgt. Pepper's Lonely Hearts Club Band", "sgt peppers lonely hearts club band"},
		{"Sgt Pepper’s", "sgt peppers"}, // typographic apostrophe
		{"(What's the Story) Morning Glory?", "whats the story morning glory"},
		{"Simon & Garfunkel", "simon garfunkel"},
		{"Simon and Garfunkel", "simon garfunkel"},
		{"Florence + the Machine", "florence the machine"},
		{"R.E.M.", "rem"},
		{"P.O.D", "pod"},
		{"Mr. Bungle", "mr bungle"}, // not an acronym: "mr" is two letters
		{"The_Beatles-Sgt_Peppers_Lonely_Hearts_Club_Band-REMASTERED-FLAC", "the beatles sgt peppers lonely hearts club band remastered flac"},
		{"R.E.M.Automatic.For.The.People", "rem automatic for the people"},
		{"A.Perfect.Circle", "a perfect circle"}, // one single letter isn't an acronym
		{"Blink-182", "blink 182"},
		{"坂本龍一", "坂本龍一"}, // other scripts fold to themselves, not to nothing
		{"Кино", "кино"},
	}
	for _, c := range cases {
		if got := foldMusic(c.in); got != c.want {
			t.Errorf("foldMusic(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := foldMusicSplit("AC/DC"); got != "ac dc" {
		t.Errorf("foldMusicSplit(AC/DC) = %q, want the slash as a word break", got)
	}
}

func TestNormKeyFolds(t *testing.T) {
	if NormKey("Björk") != NormKey("Bjork") {
		t.Error("an accent must not change the key")
	}
	if NormKey("Simon & Garfunkel") != NormKey("Simon and Garfunkel") {
		t.Error("& and 'and' must key the same")
	}
	if NormKey("坂本龍一") == "" || NormKey("Кино") == "" {
		t.Error("a title in another script must not key as nothing")
	}
	if NormKey("AC/DC") != NormKey("ACDC") || NormKey("AC/DC") != NormKey("AC DC") {
		t.Error("AC/DC must key the same however the slash is written")
	}
}

// MUS-04: releases spelled the way filenames can carry a name still match the name
// MusicBrainz uses — and the looser fold doesn't loosen the whole-word gate.
func TestReleaseIsForAlbumFolds(t *testing.T) {
	yes := []struct{ release, artist, album string }{
		{"Bjork - Homogenic (1997) [FLAC]", "Björk", "Homogenic"},
		{"Motorhead - Ace of Spades [FLAC]", "Motörhead", "Ace of Spades"},
		{"Sigur Ros - Takk [MP3 320]", "Sigur Rós", "Takk..."},
		{"ACDC - Back in Black", "AC/DC", "Back in Black"},
		{"AC.DC-Back.In.Black-FLAC", "AC/DC", "Back in Black"},
		{"The_Beatles-Sgt_Peppers_Lonely_Hearts_Club_Band-REMASTERED-FLAC", "The Beatles", "Sgt. Pepper's Lonely Hearts Club Band"},
		{"Oasis - Whats the Story Morning Glory", "Oasis", "(What's the Story) Morning Glory?"},
		{"Simon and Garfunkel - Bookends", "Simon & Garfunkel", "Bookends"},
		{"Florence and the Machine - Lungs", "Florence + the Machine", "Lungs"},
		{"Beatles - Abbey Road", "The Beatles", "Abbey Road"},
		{"REM - Automatic for the People", "R.E.M.", "Automatic for the People"},
		{"Elliott Smith - Either Or (1997) [FLAC]", "Elliott Smith", "Either/Or"},
		{"Metallica - And Justice for All [FLAC]", "Metallica", "...And Justice for All"},
	}
	for _, c := range yes {
		if !ReleaseIsForAlbum(c.release, c.artist, c.album) {
			t.Errorf("ReleaseIsForAlbum(%q, %q, %q) = false, want true", c.release, c.artist, c.album)
		}
	}
	no := []struct{ release, artist, album, why string }{
		{"Radiohead - Kid Amnesiae (2021) [FLAC]", "Radiohead", "Kid A", "whole words only"},
		{"Radiohead - Discography (1993-2016) [FLAC]", "Radiohead", "OK Computer", "a discography"},
		{"Radiohead - Complete Collection [FLAC]", "Radiohead", "Complete Collection", "a whole-catalogue pack"},
		{"Anyone - Homogenic [FLAC]", "!!!", "Homogenic", "an artist that folds to nothing"},
		{"Anyone - Homogenic [FLAC]", "", "Homogenic", "no artist at all"},
		{"The - Soul Mining [FLAC]", "The The", "Soul Mining", "'The The' minus 'the' is not empty"},
		{"Beatles Tribute Band - Abbey Road", "The Beatles", "Let It Be", "the album must match too"},
		{"Bjork - Homogenic [FLAC]", "Björk", "", "an empty album"},
	}
	for _, c := range no {
		if ReleaseIsForAlbum(c.release, c.artist, c.album) {
			t.Errorf("ReleaseIsForAlbum(%q, %q, %q) = true, want false (%s)", c.release, c.artist, c.album, c.why)
		}
	}
	// A shorter spelling of the artist only counts as the release's whole artist part,
	// never as free words somewhere in it.
	for _, c := range []struct{ release, artist, album string }{
		{"Fall Out Boy - Live in Phoenix [FLAC]", "The Fall", "Live in Phoenix"},
		{"Who Is Fancy - Live [MP3 320]", "The Who", "Live"},
		{"One Direction - Up All Night [FLAC]", "And One", "Up All Night"},
	} {
		if ReleaseIsForAlbum(c.release, c.artist, c.album) {
			t.Errorf("ReleaseIsForAlbum(%q, %q, %q) = true, want false", c.release, c.artist, c.album)
		}
	}
	if !ReleaseIsForAlbum("And One - Bodypop [FLAC]", "And One", "Bodypop") ||
		!ReleaseIsForAlbum("Simon Garfunkel - Bookends [FLAC]", "Simon & Garfunkel", "Bookends") {
		t.Error("the full name, or a shorter spelling as the whole artist part, still matches")
	}
	if ReleaseIsDiscographyFor("Fall Out Boy - Discography [FLAC]", "The Fall") {
		t.Error("the discography gate uses the same artist rule")
	}
	if !ReleaseIsForAlbum("The The - Soul Mining [FLAC]", "The The", "Soul Mining") {
		t.Error("'The The' must still match its own release")
	}
	if !ReleaseIsDiscographyFor("Bjork - Discography [FLAC]", "Björk") {
		t.Error("the discography gate folds the same way")
	}
}
