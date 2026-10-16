package parser

import "testing"

// A film released without a year: the title ends at the first resolution, source or
// codec, with release tags left before it trimmed — so it can match the film it was
// grabbed for ("PAW Patrol: Jet to the Rescue" was held as "doesn't match" because its
// parsed title ran on through every tag).
func TestYearlessMovieTitle(t *testing.T) {
	for _, tc := range []struct {
		name, title string
		source      Source
	}{
		{"PAW.Patrol.Jet.to.the.Rescue.1080p.NORDiC.WEB-DL.AAC2.0.H.264-BONDE", "PAW Patrol Jet to the Rescue", SourceWebDL},
		{"PAW.Patrol.Jet.to.the.Rescue.NORDiC.1080p.WEB-DL.H.264-BONDE", "PAW Patrol Jet to the Rescue", SourceWebDL},
		{"Some.Movie.1080p.BluRay.x264-GRP", "Some Movie", SourceBluray},
		{"Some.Movie.PROPER.720p.WEBRip.x264-GRP", "Some Movie", SourceWebRip},
		{"The.French.Dispatch.2160p.WEB-DL.DDP5.1.HEVC-GRP", "The French Dispatch", SourceWebDL},
		{"A.Proper.Violence.1080p.WEB-DL-GRP", "A Proper Violence", SourceWebDL},
		{"Movie.Name.x265-GRP", "Movie Name", ""},
		// A cam stays a cam: the tag before the codec is trimmed off the title, and the
		// source is still read from the whole name.
		{"Movie.HDCAM.x264-GRP", "Movie", SourceCAM},
	} {
		r := Parse(tc.name)
		if r.Title != tc.title {
			t.Errorf("%s: title %q, want %q", tc.name, r.Title, tc.title)
		}
		if tc.source != SourceUnknown && r.Source != tc.source {
			t.Errorf("%s: source %q, want %q", tc.name, r.Source, tc.source)
		}
		if r.Year != 0 {
			t.Errorf("%s: year %d, want none", tc.name, r.Year)
		}
	}
	// Names with a year or a season marker are cut exactly as before.
	for _, tc := range []struct{ name, title string }{
		{"PAW.Patrol.Jet.to.the.Rescue.2020.1080p.NORDiC.WEB-DL.AAC2.0.H.264-BONDE", "PAW Patrol Jet to the Rescue"},
		{"Blade.Runner.2049.2017.2160p.UHD.BluRay.x265-GRP", "Blade Runner 2049"},
		{"The.Wire.S02E03.1080p.WEB-DL-GRP", "The Wire"},
	} {
		if r := Parse(tc.name); r.Title != tc.title {
			t.Errorf("%s: title %q, want %q", tc.name, r.Title, tc.title)
		}
	}
}
