package library

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/parser"
)

// What a library name's "-GROUP" tail really is: a group, or a tag the dash happened to
// precede ("WEB-DL", "1080p-HDR", "S01E01-E02").
func TestNameGroupTellsGroupsFromTags(t *testing.T) {
	groups := []string{"x0r", "NTb", "FLUX", "3L", "playWEB", "CtrlHD", "HiFi", "SubsPlease", "AMIABLE", "FraMeSToR",
		"SuccessfulCrab", "BeyondHD", "TrollHD", "ION10", "EDGE2020", "W4NK3R", "MeGusta", "Kitsune", "SWTYBLZ"}
	for _, g := range groups {
		name := "Movie (2020) [1080p WEB-DL][x264]-" + g + ".mkv"
		if got := nameGroup(name, parser.Parse(name)); got != g {
			t.Errorf("%s: group %q, want %q", name, got, g)
		}
	}
	tags := []string{"DL", "HDR", "HDR10", "DV", "DTS", "Atmos", "10bit", "EAC3", "PROPER", "REPACK", "AMZN", "NF",
		"iNTERNAL", "REMUX", "Rip", "x265", "HEVC", "1080p", "SDR", "UHD"}
	for _, tag := range tags {
		name := "Movie (2020) - 1080p-" + tag + ".mkv"
		if got := nameGroup(name, parser.Parse(name)); got != "" {
			t.Errorf("%s: read %q as a group", name, got)
		}
	}
	for _, name := range []string{
		"Tenet (2020) - 1080p WEB-DL.mkv",
		"Show - S01E02 - The Title - 1080p WEB-DL.mkv",
		"Show (2020) - S01E01-E02.mkv",
		"Movie (2020) Bluray-1080p.mkv",
		"Show (2020) - S01E01-E02 - Pilot WEBDL-1080p.mkv",
	} {
		if got := nameGroup(name, parser.Parse(name)); got != "" {
			t.Errorf("%s: read %q as a group", name, got)
		}
	}
}

// The default scheme's own output for a WEB-DL file ends "WEB-DL", which the parser reads
// as group "DL". It must neither refuse the release nor be written as "-DL", under a dash
// scheme or a {group} without one, and renaming the result again changes nothing.
func TestRenameDefaultWebDLNames(t *testing.T) {
	movieSchemes := map[string]string{
		"issue48":   issue48Scheme,
		"no dash":   "{title} ({year}) - {quality} {codec} {group}",
		"radarrish": "{title} ({year}) {edition} {quality} {codec}-{group}",
	}
	want := map[string]string{
		"issue48":   "Tenet (2020) [1080p WEB-DL][x264]-playWEB.mkv",
		"no dash":   "Tenet (2020) - 1080p WEB-DL x264 playWEB.mkv",
		"radarrish": "Tenet (2020) 1080p WEB-DL x264-playWEB.mkv",
	}
	const movieRelease = "Tenet.2020.1080p.WEB-DL.DDP5.1.H.264-playWEB"
	for label, scheme := range movieSchemes {
		im := NewImporter("/lib", slog.Default())
		im.SetNaming(fixedNaming{Naming{Folder: "{title} ({year})", File: scheme}})
		tr := MovieTrack{Title: "Tenet", Year: 2020}
		first := filepath.Base(im.MovieRenameTarget(tr, RenameSource{FileName: "Tenet (2020) - 1080p WEB-DL.mkv", SourceRelease: movieRelease}, ".mkv"))
		if first != want[label] {
			t.Errorf("%s: %q, want %q", label, first, want[label])
		}
		if again := filepath.Base(im.MovieRenameTarget(tr, RenameSource{FileName: first, SourceRelease: movieRelease}, ".mkv")); again != first {
			t.Errorf("%s: renaming %q again proposes %q", label, first, again)
		}
	}

	episodeSchemes := map[string]string{
		"tokens":  "{title} - {episode} [{quality}][{codec}]-{group}",
		"no dash": "{title} - {episode} - {resolution} {source} {codec} {group}",
	}
	wantEp := map[string]string{
		"tokens":  "Show - S01E02 [1080p WEB-DL][x264]-NTb.mkv",
		"no dash": "Show - S01E02 - 1080p WEB-DL x264 NTb.mkv",
	}
	const epRelease = "Show.S01E02.1080p.WEB-DL.DDP5.1.H.264-NTb"
	for label, scheme := range episodeSchemes {
		im := NewImporter("/tv", slog.Default())
		im.SetSeriesNaming(fixedSeriesNaming{SeriesNaming{Folder: "{title} ({year})", SeasonFolder: "Season {season}", EpisodeFile: scheme}})
		first := filepath.Base(im.EpisodeRenameTarget("", "Show", 2020, 1, 2,
			RenameSource{FileName: "Show - S01E02 - The Title - 1080p WEB-DL.mkv", SourceRelease: epRelease}, ".mkv"))
		if first != wantEp[label] {
			t.Errorf("%s: %q, want %q", label, first, wantEp[label])
		}
		again := filepath.Base(im.EpisodeRenameTarget("", "Show", 2020, 1, 2, RenameSource{FileName: first, SourceRelease: epRelease}, ".mkv"))
		if again != first {
			t.Errorf("%s: renaming %q again proposes %q", label, first, again)
		}
	}
}

// A tag that used to pass for a group no longer blocks a release, while a real group in the
// name still refuses another group's release.
func TestReleaseFitsWithTagTails(t *testing.T) {
	cases := []struct {
		src  RenameSource
		fits bool
	}{
		{RenameSource{FileName: "Movie (2020) - 1080p-HDR.mkv", SourceRelease: "Movie.2020.1080p.BluRay.x264-OTHER"}, true},
		{RenameSource{FileName: "Movie (2020) - 1080p WEB-DL.mkv", SourceRelease: "Movie.2020.1080p.WEB-DL.H.264-OTHER"}, true},
		{RenameSource{FileName: "Movie (2020) [WEBDL-1080p][h264]-playWEB.mkv", SourceRelease: "Movie.2020.1080p.WEB-DL.H.264-OTHER"}, false},
		{RenameSource{FileName: "Movie (2020).mkv", SourceRelease: ""}, false},
	}
	for _, c := range cases {
		if got := c.src.ReleaseFits(); got != c.fits {
			t.Errorf("%s + %q: fits = %v, want %v", c.src.FileName, c.src.SourceRelease, got, c.fits)
		}
	}
	// Positive evidence: the name states the release's resolution or group.
	if !(RenameSource{FileName: "Show.S01E01.1080p.mkv", SourceRelease: "Show.S01E01.1080p.WEB-DL.x264-GRP"}).ReleaseConfirmed() {
		t.Error("a name stating the release's resolution should confirm it")
	}
	if (RenameSource{FileName: "Show S01E01.mkv", SourceRelease: "Show.S01E01.1080p.WEB-DL.x264-GRP"}).ReleaseConfirmed() {
		t.Error("a name stating nothing can't confirm a release")
	}
}
