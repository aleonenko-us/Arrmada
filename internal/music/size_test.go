package music

import "testing"

// MUS-21: video releases and absurd sizes are refused for an album; normal FLAC and MP3
// albums pass, and so do the real releases that only look odd (hi-res and multichannel
// disc rips, scans, low-bitrate Opus, tracker-native categories, a band called HEVC).
func TestReleaseSane(t *testing.T) {
	const mb = int64(1 << 20)
	mins := func(m int) SizeListing { return SizeListing{Seconds: m * 60, Discs: 1} }
	for _, tc := range []struct {
		name    string
		title   string
		cats    []int
		size    int64
		listing SizeListing
		ok      bool
		code    string
	}{
		// Video.
		{"concert film named as one", "Pink Floyd The Wall 1982 1080p BluRay FLAC 2.0 x264", nil, 8000 * mb, mins(45), false, SaneVideo},
		{"concert DVD", "Metallica - Live Shit Binge and Purge DVD9 FLAC", []int{3040}, 7000 * mb, mins(45), false, SaneVideo},
		{"a video codec with a video source", "Radiohead - From the Basement 2008 WEBRip HEVC FLAC", nil, 2000 * mb, mins(45), false, SaneVideo},
		{"movie category", "Radiohead - In Rainbows (2007) [FLAC]", []int{2040}, 300 * mb, mins(45), false, SaneVideo},
		{"music video category", "Radiohead - In Rainbows (2007) [FLAC]", []int{3020}, 300 * mb, mins(45), false, SaneVideo},
		{"audiobook category", "Radiohead - In Rainbows (2007) [FLAC]", []int{3030}, 300 * mb, mins(45), false, SaneVideo},
		{"Newznab other", "Radiohead - In Rainbows (2007) [MP3 320]", []int{8000}, 100 * mb, mins(42), false, SaneVideo},
		// Size.
		{"30 MB FLAC for 45 minutes", "Radiohead - In Rainbows (2007) [FLAC]", nil, 30 * mb, mins(45), false, SaneSize},
		{"a 40 GB 'FLAC' album", "Radiohead - In Rainbows (2007) [FLAC]", nil, 40000 * mb, mins(45), false, SaneSize},
		{"a 4 GB 'MP3' album", "Radiohead - In Rainbows (2007) [MP3 320]", nil, 4000 * mb, mins(45), false, SaneSize},
		{"a whole box set for one album", "Beatles - Mono Box (2009) [FLAC]", nil, 2500 * mb, mins(30), false, SaneSize},
		// Normal albums.
		{"300 MB FLAC", "Radiohead - In Rainbows (2007) [FLAC]", nil, 300 * mb, mins(45), true, ""},
		{"hi-res FLAC", "Radiohead - In Rainbows (2007) [FLAC 24bit 192kHz]", nil, 2200 * mb, mins(42), true, ""},
		{"MP3 320 album", "Radiohead - In Rainbows (2007) [MP3 320]", nil, 105 * mb, mins(45), true, ""},
		{"V0 album", "Radiohead - In Rainbows (2007) [MP3 V0]", nil, 80 * mb, mins(45), true, ""},
		{"V2 album", "Radiohead - In Rainbows (2007) [MP3 V2]", nil, 22 * mb, mins(42), true, ""},
		{"128 album", "Radiohead - In Rainbows (2007) [MP3 128]", nil, 30 * mb, mins(42), true, ""},
		{"AAC album", "Radiohead - In Rainbows (2007) [AAC 256]", nil, 70 * mb, mins(42), true, ""},
		{"ALAC album", "Radiohead - In Rainbows (2007) [ALAC]", nil, 300 * mb, mins(42), true, ""},
		{"low-bitrate Opus", "Opus band - Live Is Life (1985) [OPUS]", nil, 15 * mb, mins(40), true, ""},
		{"MP3 with scans", "Radiohead - In Rainbows (2007) [MP3 320] + scans", nil, 450 * mb, mins(42), true, ""},
		{"a deluxe 3CD in MP3", "Beatles - Abbey Road Super Deluxe (2019) [MP3 320] 3CD", nil, 400 * mb, mins(47), true, ""},
		// Disc rips and odd names that are still albums.
		{"DVD-Audio rip", "Pink Floyd - Dark Side of the Moon (DVD-Audio) [FLAC 24-96]", nil, 1500 * mb, mins(43), true, ""},
		{"DVDA rip", "Pink Floyd - Dark Side of the Moon DVDA 24-96 FLAC", nil, 1500 * mb, mins(43), true, ""},
		{"Blu-ray audio", "Pink Floyd - Wish You Were Here (Blu-ray Audio) [FLAC 24-192]", nil, 2500 * mb, mins(44), true, ""},
		{"BDA", "Pink Floyd - Wish You Were Here BDA 24-192 FLAC", nil, 2500 * mb, mins(44), true, ""},
		{"BDRip multichannel", "Pink Floyd - Wish You Were Here (2011) [BDRip 24-96 FLAC 5.1+2.0]", nil, 4000 * mb, mins(44), true, ""},
		{"BD-Audio 5.1 + 2.0", "Pink Floyd - Wish You Were Here (BD-Audio 24-192 5.1 + 2.0) [FLAC]", nil, 6000 * mb, mins(44), true, ""},
		{"SACD image", "Miles Davis - Kind of Blue (SACD ISO)", nil, 3000 * mb, mins(46), true, ""},
		{"hi-res web", "Miles Davis - Kind of Blue (2015) [FLAC 24-192]", nil, 2000 * mb, mins(46), true, ""},
		{"WEB-DL is a video source", "Taylor Swift - Midnights (2022) [WEB-DL FLAC]", nil, 300 * mb, mins(44), false, SaneVideo},
		{"HDTV concert", "Radiohead - Live at Glastonbury 2017 HDTV FLAC", nil, 3000 * mb, mins(45), false, SaneVideo},
		{"mono 16-bit FLAC", "The Beatles - Please Please Me (Mono) (1963) [FLAC]", nil, 105 * mb, mins(40), true, ""},
		{"DXD FLAC", "Bach - Goldberg Variations (2015) [FLAC 24-352.8 DXD]", nil, 4500 * mb, mins(45), true, ""},
		{"24-bit FLAC", "Radiohead - Kid A (2000) [FLAC 24bit]", nil, 3600 * mb, mins(50), true, ""},
		{"WEB FLAC", "Taylor Swift - Midnights (2022) WEB FLAC", nil, 300 * mb, mins(44), true, ""},
		{"Remux in a name", "Radiohead - In Rainbows Remux (2007) [FLAC]", nil, 300 * mb, mins(42), true, ""},
		{"a number like a resolution", "Some Band - 1080 Days (2019) [FLAC]", nil, 300 * mb, mins(45), true, ""},
		{"a band called HEVC", "HEVC - Debut (2019) [FLAC]", nil, 300 * mb, mins(45), true, ""},
		// Categories.
		{"lossless category", "Radiohead - In Rainbows (2007) [FLAC]", []int{3040}, 300 * mb, mins(45), true, ""},
		{"no categories", "Radiohead - In Rainbows (2007) [FLAC]", []int{}, 300 * mb, mins(45), true, ""},
		{"Jackett tracker category", "Radiohead - In Rainbows (2007) [FLAC]", []int{100123}, 300 * mb, mins(45), true, ""},
		{"audio and a tracker category", "Radiohead - In Rainbows (2007) [FLAC]", []int{3000, 100123}, 300 * mb, mins(45), true, ""},
		{"a native category", "Radiohead - In Rainbows (2007) [MP3 320]", []int{31}, 100 * mb, mins(42), true, ""},
		{"category zero", "Radiohead - In Rainbows (2007) [MP3 320]", []int{0}, 100 * mb, mins(42), true, ""},
		// Nothing to judge a size by.
		{"unknown size", "Radiohead - In Rainbows (2007) [FLAC]", nil, 0, mins(45), true, ""},
		{"no listing", "Radiohead - In Rainbows (2007) [FLAC]", nil, 30 * mb, SizeListing{}, true, ""},
		{"one disc of a box set", "Beatles - Mono Box Disc 1 (2009) [FLAC]", nil, 100 * mb, SizeListing{Seconds: 45 * 60, Discs: 3}, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok, code, reason := ReleaseSane(tc.title, tc.cats, tc.size, DetectQuality(tc.title), tc.listing)
			if ok != tc.ok || code != tc.code {
				t.Fatalf("ReleaseSane(%q) = %v %q (%s), want %v %q", tc.title, ok, code, reason, tc.ok, tc.code)
			}
			if !ok && reason == "" {
				t.Fatal("a refused release must say why")
			}
		})
	}
}

// The reason names the size and the album: what interactive search shows.
func TestReleaseSaneReasonWords(t *testing.T) {
	_, _, reason := ReleaseSane("Radiohead - In Rainbows [FLAC]", nil, 30<<20, QualityFLAC, SizeListing{Seconds: 45 * 60, Discs: 1})
	if want := "30 MB is too small for 45 minutes of FLAC"; reason != want {
		t.Fatalf("reason = %q, want %q", reason, want)
	}
}

// A listing is only good for the size check when every track has a plausible length.
func TestListingOf(t *testing.T) {
	full := []Track{{DiscNumber: 1, DurationSec: 200}, {DiscNumber: 1, DurationSec: 260}, {DiscNumber: 2, DurationSec: 300}}
	if got := ListingOf(full); got.Seconds != 760 || got.Discs != 2 {
		t.Fatalf("ListingOf = %+v, want 760 s over 2 discs", got)
	}
	// One unknown length: unjudged, rather than guessing four minutes for it (a
	// symphony's movements would make a real release look far too big).
	if got := ListingOf([]Track{{DurationSec: 1500}, {DurationSec: 0}}); got.Seconds != 0 {
		t.Fatalf("a listing with an unknown length = %+v, want unjudged", got)
	}
	// Lengths that can't be right (12 tracks of 10 s): unjudged.
	var short []Track
	for i := 0; i < 12; i++ {
		short = append(short, Track{DiscNumber: 1, DurationSec: 10})
	}
	if got := ListingOf(short); got.Seconds != 0 {
		t.Fatalf("an implausibly short listing = %+v, want unjudged", got)
	}
	if got := ListingOf(nil); got.Seconds != 0 {
		t.Fatal("no listing should be unjudged")
	}
	// The Mahler case: three movements with no lengths, a 200 MB MP3 release passes.
	mahler := ListingOf([]Track{{}, {}, {}})
	if ok, _, why := ReleaseSane("Mahler - Symphony 2 (MP3 320)", nil, 200<<20, QualityMP3320, mahler); !ok {
		t.Fatalf("refused: %s", why)
	}
}
