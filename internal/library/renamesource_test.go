package library

import (
	"log/slog"
	"path/filepath"
	"testing"
)

// issue48Scheme is the movie file scheme from issue #48: more tokens than the files were
// first named with.
const issue48Scheme = "{title} ({year}) {plexedition} [{quality}][{codec}]-{group}"

// A rename names a library file from its recorded release, as the import did, so tokens a
// newer scheme asks for are filled even though the file's current name lost them.
func TestMovieRenameTargetFromSourceRelease(t *testing.T) {
	cases := []struct {
		name  string
		track MovieTrack
		src   RenameSource
		want  string // file base name
	}{
		{
			// Issue #48, first example. The release says UNRATED, so {plexedition} carries it,
			// exactly as an import of this release under this scheme names the file.
			name:  "american psycho",
			track: MovieTrack{Title: "American Psycho", Year: 2000},
			src: RenameSource{FileName: "American Psycho (2000).mkv",
				SourceRelease: "American.Psycho.2000.UNRATED.German.DTSHD.DL.2160p.UHD.BluRay.DV.HDR.x265-VECTOR"},
			want: "American Psycho (2000) {edition-Unrated} [2160p BluRay][x265]-VECTOR.mkv",
		},
		{
			// Issue #48, second example: the old name kept the quality but lost codec and group.
			name:  "avatar",
			track: MovieTrack{Title: "Avatar", Year: 2009},
			src: RenameSource{FileName: "Avatar (2009) - 2160p BluRay.mkv",
				SourceRelease: "Avatar.2009.2160p.UHD.BluRay.x265.10bit.HDR.TrueHD.7.1.Atmos-TERMiNAL"},
			want: "Avatar (2009) [2160p BluRay][x265]-TERMiNAL.mkv",
		},
		{
			// The track's own edition beats the release's.
			name:  "track edition wins",
			track: MovieTrack{Title: "American Psycho", Year: 2000, Edition: "Director's Cut"},
			src: RenameSource{FileName: "American Psycho (2000).mkv",
				SourceRelease: "American.Psycho.2000.UNRATED.2160p.UHD.BluRay.x265-VECTOR"},
			want: "American Psycho (2000) {edition-Director's Cut} [2160p BluRay][x265]-VECTOR.mkv",
		},
		{
			// No recorded release (a scanned file, or a manual import before releases were
			// recorded): the file's name is all there is, as before.
			name:  "no source release",
			track: MovieTrack{Title: "Heat", Year: 1995},
			src:   RenameSource{FileName: "Heat.1995.1080p.BluRay.x264-GRP.mkv"},
			want:  "Heat (1995) [1080p BluRay][x264]-GRP.mkv",
		},
		{
			name:  "no source release, nothing in the name",
			track: MovieTrack{Title: "Heat", Year: 1995},
			src:   RenameSource{FileName: "Heat (1995).mkv"},
			want:  "Heat (1995).mkv",
		},
		{
			// A release that names none of the facts changes nothing; the name speaks.
			name:  "release that doesn't parse",
			track: MovieTrack{Title: "Heat", Year: 1995},
			src:   RenameSource{FileName: "Heat (1995) [1080p BluRay][x264]-GRP.mkv", SourceRelease: "heat"},
			want:  "Heat (1995) [1080p BluRay][x264]-GRP.mkv",
		},
		{
			// The release states some facts; the file's name fills the ones it doesn't.
			name:  "name fills what the release leaves out",
			track: MovieTrack{Title: "Heat", Year: 1995},
			src:   RenameSource{FileName: "heat.1995.1080p.bluray.x264-grp.mkv", SourceRelease: "Heat.1995.1080p.BluRay"},
			want:  "Heat (1995) [1080p BluRay][x264]-grp.mkv",
		},
		{
			// The title is always the library's, whatever the release calls the film.
			name:  "release titled differently",
			track: MovieTrack{Title: "Léon: The Professional", Year: 1994},
			src: RenameSource{FileName: "Leon (1994).mkv",
				SourceRelease: "Leon.1994.Extended.Cut.1080p.BluRay.x264-AMIABLE"},
			want: "Léon The Professional (1994) {edition-Extended} [1080p BluRay][x264]-AMIABLE.mkv",
		},
		{
			// A release stating another resolution than the file has isn't this file's (the
			// file was replaced by hand since): the file's own name is used.
			name:  "release from another file, by probe",
			track: MovieTrack{Title: "Heat", Year: 1995},
			src: RenameSource{FileName: "Heat (1995) [1080p WEBRip]-NEW.mkv", Resolution: "1080p",
				SourceRelease: "Heat.1995.2160p.UHD.BluRay.x265-OLD"},
			want: "Heat (1995) [1080p WEBRip]-NEW.mkv",
		},
		{
			name:  "release from another file, by name",
			track: MovieTrack{Title: "Heat", Year: 1995},
			src: RenameSource{FileName: "Heat (1995) [720p HDTV].mkv",
				SourceRelease: "Heat.1995.2160p.UHD.BluRay.x265-OLD"},
			want: "Heat (1995) [720p HDTV].mkv",
		},
		{
			// A PAL DVD probes as 576p while its release may say 480p: both are SD.
			name:  "SD resolutions agree",
			track: MovieTrack{Title: "Heat", Year: 1995},
			src:   RenameSource{FileName: "Heat (1995).mkv", Resolution: "576p", SourceRelease: "Heat.1995.480p.DVDRip.x264-SD"},
			want:  "Heat (1995) [480p DVD][x264]-SD.mkv",
		},
		{
			// Convert re-encoded the file to AV1 and restamped the release: the release says so.
			name:  "convert restamped release",
			track: MovieTrack{Title: "Dune", Year: 2021},
			src: RenameSource{FileName: "Dune (2021) [2160p WEB-DL][x265]-FLUX.mkv", Codec: "AV1",
				SourceRelease: "Dune.2021.2160p.WEB-DL.DDP5.1.AV1-FLUX"},
			want: "Dune (2021) [2160p WEB-DL][AV1]-FLUX.mkv",
		},
		{
			// A file converted before Convert restamped releases: the probe wins, the
			// release still gives the group.
			name:  "probed codec beats a stale release",
			track: MovieTrack{Title: "Dune", Year: 2021},
			src: RenameSource{FileName: "Dune (2021).mkv", Codec: "AV1",
				SourceRelease: "Dune.2021.2160p.WEB-DL.DDP5.1.x265-FLUX"},
			want: "Dune (2021) [2160p WEB-DL][AV1]-FLUX.mkv",
		},
		{
			// No release, and the name still says the codec the file had before Convert.
			name:  "probed codec beats the name",
			track: MovieTrack{Title: "Dune", Year: 2021},
			src:   RenameSource{FileName: "Dune (2021) [2160p WEB-DL][x264]-FLUX.mkv", Codec: "av1"},
			want:  "Dune (2021) [2160p WEB-DL][AV1]-FLUX.mkv",
		},
		{
			// The same codec spelled the probe's way changes nothing.
			name:  "probe agrees",
			track: MovieTrack{Title: "Dune", Year: 2021},
			src:   RenameSource{FileName: "Dune (2021).mkv", Codec: "hevc", SourceRelease: "Dune.2021.2160p.WEB-DL.HEVC-FLUX"},
			want:  "Dune (2021) [2160p WEB-DL][x265]-FLUX.mkv",
		},
		{
			// A codec no release name could state never overrides one.
			name:  "unnameable probed codec",
			track: MovieTrack{Title: "Dune", Year: 2021},
			src:   RenameSource{FileName: "Dune (2021).mkv", Codec: "other", SourceRelease: "Dune.2021.2160p.WEB-DL.x265-FLUX"},
			want:  "Dune (2021) [2160p WEB-DL][x265]-FLUX.mkv",
		},
	}
	im := NewImporter("/lib", slog.Default())
	im.SetNaming(fixedNaming{Naming{Folder: "{title} ({year})", File: issue48Scheme}})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := im.MovieRenameTarget(tc.track, tc.src, ".mkv")
			if base := filepath.Base(got); base != tc.want {
				t.Errorf("got  %q\nwant %q", base, tc.want)
			}
		})
	}
}

// A rename of a file in the library with its recorded release lands where an import of that
// release would have put it.
func TestMovieRenameTargetMatchesImport(t *testing.T) {
	im := NewImporter("/lib", slog.Default())
	im.SetNaming(fixedNaming{Naming{Folder: "{title} ({year})", File: issue48Scheme}})
	release := "Blade.Runner.2049.2017.2160p.UHD.BluRay.x265-TERMiNAL"
	tr := MovieTrack{Title: "Blade Runner 2049", Year: 2017}
	imported := im.MovieTrackTarget(tr, release+".mkv", ".mkv")
	renamed := im.MovieRenameTarget(tr, RenameSource{FileName: "Blade Runner 2049 (2017).mkv", SourceRelease: release}, ".mkv")
	if imported != renamed {
		t.Fatalf("rename %q, import %q", renamed, imported)
	}
}

// Episodes take their release facts from the recorded release too, but never their
// numbering: a season pack's release names no episode, and a double episode stays one.
func TestEpisodeRenameTargetFromSourceRelease(t *testing.T) {
	im := NewImporter("/tv", slog.Default())
	im.SetSeriesNaming(fixedSeriesNaming{SeriesNaming{
		Folder: "{title} ({year})", SeasonFolder: "Season {season}",
		EpisodeFile: "{title} - {episode} [{quality}][{codec}]-{group}",
	}})
	cases := []struct {
		name    string
		episode int
		src     RenameSource
		want    string
	}{
		{
			name: "episode release", episode: 3,
			src:  RenameSource{FileName: "Andor - S01E03.mkv", SourceRelease: "Andor.S01E03.1080p.DSNP.WEB-DL.DDP5.1.H.264-NTb"},
			want: "Andor - S01E03 [1080p WEB-DL][x264]-NTb.mkv",
		},
		{
			name: "season pack release", episode: 3,
			src:  RenameSource{FileName: "Andor - S01E03.mkv", SourceRelease: "Andor.S01.2160p.DSNP.WEB-DL.DDP5.1.HDR.H.265-FLUX"},
			want: "Andor - S01E03 [2160p WEB-DL][x265]-FLUX.mkv",
		},
		{
			name: "double episode from a pack", episode: 1,
			src:  RenameSource{FileName: "Andor - S01E01-E02.mkv", SourceRelease: "Andor.S01.1080p.WEB-DL.H.264-NTb"},
			want: "Andor - S01E01-E02 [1080p WEB-DL][x264]-NTb.mkv",
		},
		{
			name: "no release", episode: 3,
			src:  RenameSource{FileName: "Andor.S01E03.720p.HDTV.x264-KILLERS.mkv"},
			want: "Andor - S01E03 [720p HDTV][x264]-KILLERS.mkv",
		},
		{
			name: "converted episode", episode: 3,
			src: RenameSource{FileName: "Andor - S01E03 [1080p WEB-DL][x264]-NTb.mkv", Codec: "av1", Resolution: "1080p",
				SourceRelease: "Andor.S01E03.1080p.DSNP.WEB-DL.DDP5.1.H.264-NTb"},
			want: "Andor - S01E03 [1080p WEB-DL][AV1]-NTb.mkv",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := im.EpisodeRenameTarget("", "Andor", 2022, 1, tc.episode, tc.src, ".mkv")
			if base := filepath.Base(got); base != tc.want {
				t.Errorf("got  %q\nwant %q", base, tc.want)
			}
			if dir := filepath.Base(filepath.Dir(got)); dir != "Season 1" {
				t.Errorf("season folder %q", dir)
			}
		})
	}
}
