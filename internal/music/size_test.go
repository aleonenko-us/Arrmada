package music

import "testing"

// MUS-21: video releases and absurd sizes are refused for an album; normal FLAC and MP3
// albums pass.
func TestReleaseSane(t *testing.T) {
	const mb = int64(1 << 20)
	fortyFive := 45 * 60 // a 45-minute album
	for _, tc := range []struct {
		name  string
		title string
		cats  []int
		size  int64
		sec   int
		ok    bool
		code  string
	}{
		{"concert film named as one", "Pink Floyd The Wall 1982 1080p BluRay FLAC 2.0 x264", nil, 8000 * mb, fortyFive, false, SaneVideo},
		{"concert DVD", "Metallica - Live Shit Binge and Purge DVD9 FLAC", []int{3040}, 7000 * mb, fortyFive, false, SaneVideo},
		{"HEVC concert", "Radiohead - In Rainbows From the Basement 2008 HEVC FLAC", nil, 2000 * mb, fortyFive, false, SaneVideo},
		{"30 MB FLAC for 45 minutes", "Radiohead - In Rainbows (2007) [FLAC]", nil, 30 * mb, fortyFive, false, SaneSize},
		{"300 MB FLAC", "Radiohead - In Rainbows (2007) [FLAC]", nil, 300 * mb, fortyFive, true, ""},
		{"hi-res FLAC", "Radiohead - In Rainbows (2007) [FLAC 24-96]", nil, 1100 * mb, fortyFive, true, ""},
		{"MP3 320 album", "Radiohead - In Rainbows (2007) [MP3 320]", nil, 105 * mb, fortyFive, true, ""},
		{"V0 album", "Radiohead - In Rainbows (2007) [MP3 V0]", nil, 80 * mb, fortyFive, true, ""},
		{"MP3 320 album with scans", "Radiohead - In Rainbows (2007) [MP3 320]", nil, 260 * mb, fortyFive, true, ""},
		{"a 40 GB 'FLAC' album", "Radiohead - In Rainbows (2007) [FLAC]", nil, 40000 * mb, fortyFive, false, SaneSize},
		{"a 4 GB 'MP3' album", "Radiohead - In Rainbows (2007) [MP3 320]", nil, 4000 * mb, fortyFive, false, SaneSize},
		{"movie category", "Radiohead - In Rainbows (2007) [FLAC]", []int{2040}, 300 * mb, fortyFive, false, SaneVideo},
		{"music video category", "Radiohead - In Rainbows (2007) [FLAC]", []int{3020}, 300 * mb, fortyFive, false, SaneVideo},
		{"audiobook category", "Radiohead - In Rainbows (2007) [FLAC]", []int{3030}, 300 * mb, fortyFive, false, SaneVideo},
		{"lossless category", "Radiohead - In Rainbows (2007) [FLAC]", []int{3040}, 300 * mb, fortyFive, true, ""},
		{"no categories", "Radiohead - In Rainbows (2007) [FLAC]", []int{}, 300 * mb, fortyFive, true, ""},
		{"tracker category", "Radiohead - In Rainbows (2007) [FLAC]", []int{100123}, 300 * mb, fortyFive, true, ""},
		{"audio and a tracker category", "Radiohead - In Rainbows (2007) [FLAC]", []int{3000, 100123}, 300 * mb, fortyFive, true, ""},
		{"unknown size", "Radiohead - In Rainbows (2007) [FLAC]", nil, 0, fortyFive, true, ""},
		{"no listing", "Radiohead - In Rainbows (2007) [FLAC]", nil, 30 * mb, 0, true, ""},
		{"a Blu-ray audio rip", "Pink Floyd - Wish You Were Here (BluRay Audio) [FLAC 24-192]", nil, 1500 * mb, fortyFive, true, ""},
		{"a band named with digits", "Blink-182 - Enema of the State (1999) [FLAC]", nil, 300 * mb, 35 * 60, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok, code, reason := ReleaseSane(tc.title, tc.cats, tc.size, DetectQuality(tc.title), tc.sec)
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
	_, _, reason := ReleaseSane("Radiohead - In Rainbows [FLAC]", nil, 30<<20, QualityFLAC, 45*60)
	if want := "30 MB is too small for 45 minutes of FLAC"; reason != want {
		t.Fatalf("reason = %q, want %q", reason, want)
	}
}

// A listing with no durations counts each track as four minutes.
func TestListingSecondsFallback(t *testing.T) {
	tracks := []Track{{DurationSec: 0}, {DurationSec: 0}, {DurationSec: 200}}
	if got := ListingSeconds(tracks); got != 2*240+200 {
		t.Fatalf("ListingSeconds = %d, want %d", got, 2*240+200)
	}
	if ListingSeconds(nil) != 0 {
		t.Fatal("no listing should be 0")
	}
}
