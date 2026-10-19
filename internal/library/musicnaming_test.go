package library

import (
	"path/filepath"
	"strings"
	"testing"
)

func slashed(p string) string { return filepath.ToSlash(p) }

// MUS-34: the default scheme names an ordinary album (one disc) exactly as
// AlbumTrackTarget always did, so existing libraries don't need a rename.
func TestDefaultMusicNamingMatchesTodaysPaths(t *testing.T) {
	im := NewImporter(t.TempDir(), quiet())
	for _, tc := range []struct {
		artist, album string
		year, track   int
		title, ext    string
	}{
		{"Radiohead", "OK Computer", 1997, 1, "Airbag", ".flac"},
		{"Radiohead", "OK Computer", 1997, 12, "The Tourist", ".flac"},
		{"Sigur Rós", "Ágætis byrjun", 1999, 3, "Ný batterí", ".mp3"},
		{"AC/DC", "Back in Black", 1980, 6, "Back in Black", ".m4a"},
		{"Metallica", "...And Justice for All", 1988, 1, "Blackened", ".flac"},
		{"Prince", "1999", 0, 1, "1999", ".flac"},
		{"", "", 2001, 1, "Untitled", ".ogg"},
		{"Weezer", "Weezer", 1994, 4, "", ".flac"},
		{"Who?", "Who's Next: Deluxe", 1971, 9, "Won't Get Fooled Again", ".flac"},
	} {
		want := im.AlbumTrackTarget(tc.artist, tc.album, tc.year, 1, tc.track, tc.title, tc.ext)
		got := im.MusicTrackTarget(MusicName{AlbumArtist: tc.artist, AlbumTitle: tc.album, OriginalYear: tc.year,
			DiscCount: 1, Disc: 1, Track: tc.track, TrackTitle: tc.title}, tc.ext)
		if got != want {
			t.Errorf("%s / %s: got %s, want %s", tc.artist, tc.album, got, want)
		}
	}
}

func TestMusicNamingRender(t *testing.T) {
	base := MusicName{AlbumArtist: "The Beatles", AlbumArtistSort: "Beatles, The", AlbumTitle: "The Beatles",
		OriginalYear: 1968, ReleaseYear: 2009, Edition: "2009 remaster", DiscCount: 1, Disc: 1, Track: 4, TrackTitle: "Glass Onion"}
	with := func(f func(*MusicName)) MusicName { n := base; f(&n); return n }
	for _, tc := range []struct {
		name string
		nm   MusicNaming
		n    MusicName
		want string
	}{
		{"one disc", DefaultMusicNaming, base, "The Beatles/The Beatles (1968)/04 - Glass Onion.flac"},
		{"two discs, disc 1", DefaultMusicNaming, with(func(n *MusicName) { n.DiscCount = 2 }), "The Beatles/The Beatles (1968)/1-04 - Glass Onion.flac"},
		{"two discs, disc 2", DefaultMusicNaming, with(func(n *MusicName) { n.DiscCount, n.Disc = 2, 2 }), "The Beatles/The Beatles (1968)/2-04 - Glass Onion.flac"},
		{"compilation", DefaultMusicNaming, with(func(n *MusicName) {
			n.AlbumArtist, n.AlbumArtistSort, n.AlbumTitle, n.Compilation, n.TrackArtist = "Various Artists", "", "Pulp Fiction", true, "Dick Dale & His Del-Tones"
		}), "Various Artists/Pulp Fiction (1968)/04 - Dick Dale & His Del-Tones - Glass Onion.flac"},
		{"compilation, no track artist falls back to the album artist", DefaultMusicNaming, with(func(n *MusicName) {
			n.AlbumArtist, n.Compilation = "Various Artists", true
		}), "Various Artists/The Beatles (1968)/04 - Various Artists - Glass Onion.flac"},
		{"featured artist in the compilation template", DefaultMusicNaming, with(func(n *MusicName) {
			n.AlbumArtist, n.Compilation, n.TrackArtist = "Various Artists", true, "Daft Punk feat. Pharrell Williams"
		}), "Various Artists/The Beatles (1968)/04 - Daft Punk feat. Pharrell Williams - Glass Onion.flac"},
		{"a featured artist on an ordinary album keeps the plain template", DefaultMusicNaming, with(func(n *MusicName) {
			n.TrackArtist = "The Beatles feat. Billy Preston"
		}), "The Beatles/The Beatles (1968)/04 - Glass Onion.flac"},
		{"illegal characters", DefaultMusicNaming, with(func(n *MusicName) {
			n.AlbumArtist, n.AlbumTitle, n.TrackTitle = "AC/DC", `Who: "Live" <at> Leeds?`, "Q*bert|Theme"
		}), "ACDC/Who Live at Leeds (1968)/04 - QbertTheme.flac"},
		{"dot-only values name nothing", DefaultMusicNaming, with(func(n *MusicName) {
			n.AlbumArtist, n.AlbumTitle, n.TrackTitle = "..", ".", "..."
		}), "Unknown Artist/Unknown Album (1968)/04.flac"},
		{"no year", DefaultMusicNaming, with(func(n *MusicName) { n.OriginalYear = 0 }), "The Beatles/The Beatles/04 - Glass Onion.flac"},
		{"edition in an optional part", MusicNaming{Folder: "{Album Artist}/{Album Title} ({Original Year})< [{Edition}]>", Track: DefaultMusicTrack, Compilation: DefaultMusicCompilationTrack},
			base, "The Beatles/The Beatles (1968) [2009 remaster]/04 - Glass Onion.flac"},
		{"edition part drops out", MusicNaming{Folder: "{Album Artist}/{Album Title}< [{Edition}]>", Track: DefaultMusicTrack, Compilation: DefaultMusicCompilationTrack},
			with(func(n *MusicName) { n.Edition = "" }), "The Beatles/The Beatles/04 - Glass Onion.flac"},
		{"disc folders preset", MusicNamingPresets[2].Naming, with(func(n *MusicName) { n.DiscCount, n.Disc = 2, 2 }), "The Beatles/The Beatles (1968)/Disc 2/04 - Glass Onion.flac"},
		{"lidarr preset", MusicNamingPresets[1].Naming, base, "The Beatles/The Beatles (2009)/The Beatles - The Beatles - 04 - Glass Onion.flac"},
		{"a template with no disc gets one on a multi-disc album", MusicNaming{Folder: DefaultMusicFolder, Track: "{Track:00} - {Track Title}", Compilation: DefaultMusicCompilationTrack},
			with(func(n *MusicName) { n.DiscCount, n.Disc = 2, 2 }), "The Beatles/The Beatles (1968)/2-04 - Glass Onion.flac"},
		{"first letter and sort", MusicNaming{Folder: "{Album Artist First Letter}/{Album Artist Sort}/{Album Title}", Track: "{Track:000} {Track Title} [{Quality} {Bit Depth}-{Sample Rate}]", Compilation: DefaultMusicCompilationTrack},
			with(func(n *MusicName) { n.Quality, n.BitDepth, n.SampleRate = "FLAC-24", 24, 96000 }), "B/Beatles, The/The Beatles/004 Glass Onion [FLAC-24 24-96].flac"},
		{"empty facts leave no debris", MusicNaming{Folder: DefaultMusicFolder, Track: "{Track:00} - {Track Title} [{Quality}]", Compilation: DefaultMusicCompilationTrack},
			base, "The Beatles/The Beatles (1968)/04 - Glass Onion.flac"},
		{"broken templates fall back to the defaults", MusicNaming{Folder: "{Nope}", Track: "", Compilation: "{Track Title}"},
			base, "The Beatles/The Beatles (1968)/04 - Glass Onion.flac"},
	} {
		if got := slashed(MusicTrackRel(tc.nm, tc.n, ".flac")); got != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", tc.name, got, tc.want)
		}
	}
}

func TestMusicNamingFirstLetterOfNonLetters(t *testing.T) {
	if got := firstLetter(MusicName{AlbumArtist: "!!!"}); got != "#" {
		t.Errorf("!!! files under %q", got)
	}
	if got := firstLetter(MusicName{AlbumArtist: "ünloved"}); got != "Ü" {
		t.Errorf("ünloved files under %q", got)
	}
}

func TestMusicNamingLongNamesAreCut(t *testing.T) {
	n := MusicName{AlbumArtist: "A", AlbumTitle: strings.Repeat("Long ", 80), DiscCount: 1, Disc: 1, Track: 1, TrackTitle: strings.Repeat("é", 200)}
	for _, seg := range strings.Split(slashed(MusicTrackRel(DefaultMusicNaming, n, ".flac")), "/") {
		if len(seg) > maxNameBytes {
			t.Errorf("segment of %d bytes: %q", len(seg), seg)
		}
	}
	if !strings.HasSuffix(MusicTrackRel(DefaultMusicNaming, n, ".flac"), ".flac") {
		t.Error("the extension was cut")
	}
}

func TestValidateMusicNaming(t *testing.T) {
	for _, s := range []string{DefaultMusicFolder, "{Album Artist}/{Album Title}", "{Album Artist First Letter}/{Album Artist}/{Original Year} - {Album Title}<{Edition}>", "{Artist Name}/{Album Title} ({Release Year})"} {
		if err := ValidateMusicFolder(s); err != nil {
			t.Errorf("folder %q: %v", s, err)
		}
	}
	for s, want := range map[string]string{
		"{Album Artist}":                          "{Album Title}",
		"{Album Title}/{Album Artist}":            "{Album Title}",
		"{Album Artist}/<{Album Title}>":          "{Album Title}",
		"{Album Artist}/{Album Title} - {Track}":  "names a track",
		"{Album Artist}/{Album Title} {Quality}":  "names a track",
		"{Album Artist}/{Album Title} {Nonsense}": "isn't a token",
		"{Album Artist:00}/{Album Title}":         "zero-padded",
		"":                                        "can't be empty",
	} {
		if err := ValidateMusicFolder(s); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("folder %q: %v, want it to mention %q", s, err, want)
		}
	}
	for _, s := range []string{DefaultMusicTrack, DefaultMusicCompilationTrack, "Disc {Disc}/{Track:00} - {Track Title}", "{track:00} {title}"} {
		if err := ValidateMusicTrack(s); err != nil {
			t.Errorf("track %q: %v", s, err)
		}
	}
	for _, s := range []string{"{Track Title}", "{Track}/{Track Title}", "<{Track}> {Track Title}", "{Track:0a}"} {
		if ValidateMusicTrack(s) == nil {
			t.Errorf("track %q passed", s)
		}
	}
	for _, p := range MusicNamingPresets {
		if ValidateMusicFolder(p.Naming.Folder) != nil || ValidateMusicTrack(p.Naming.Track) != nil || ValidateMusicTrack(p.Naming.Compilation) != nil {
			t.Errorf("preset %s doesn't validate", p.ID)
		}
	}
}

func TestPreviewMusicNaming(t *testing.T) {
	samples, perr := PreviewMusicNaming(DefaultMusicNaming)
	if perr != nil || len(samples) != len(musicSamples) {
		t.Fatalf("preview: %v %v", samples, perr)
	}
	if got := samples[1].Paths[1]; got != "The Beatles/The Beatles (1968)/2-01 - Birthday.flac" {
		t.Errorf("two-disc sample: %s", got)
	}
	if got := samples[2].Paths[0]; got != "Various Artists/Pulp Fiction Music From the Motion Picture (1994)/02 - Dick Dale & His Del-Tones - Misirlou.mp3" {
		t.Errorf("compilation sample: %s", got)
	}
	if _, perr := PreviewMusicNaming(MusicNaming{Folder: DefaultMusicFolder, Track: "{Track Title}", Compilation: DefaultMusicCompilationTrack}); perr == nil || perr.Field != "track" {
		t.Errorf("bad track template: %v", perr)
	}
}

// Books name exactly as before the engine was shared (their own tests are the guard;
// this pins that the vocabularies don't leak into each other).
func TestNamingVocabulariesStayApart(t *testing.T) {
	if ValidateBookFolder("{Album Artist}/{Title}") == nil {
		t.Error("a music token passed as a book token")
	}
	if ValidateMusicFolder("{Author}/{Album Title}") == nil {
		t.Error("a book token passed as a music token")
	}
}
