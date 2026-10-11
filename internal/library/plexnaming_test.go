package library

import (
	"path/filepath"
	"testing"
)

// PLEX-13: id tokens, empty Plex groups dropped, the recommended preset's fallback.

func TestRenderNameDropsEmptyPlexGroups(t *testing.T) {
	cases := map[string]string{
		"{title} ({year}) {edition-{edition}} - {quality}": "Heat (1995) - 1080p",
		"{title} ({year}) {tmdb-{tmdbid}}":                 "Heat (1995)",
		"{title} {imdb-{imdbid}} {tvdb-{tvdbid}}":          "Heat",
		"{title} {edition-} - {quality}":                   "Heat - 1080p", // hand-written and left bare
	}
	tok := map[string]string{"title": "Heat", "year": "1995", "quality": "1080p", "edition": "", "tmdbid": "", "imdbid": "", "tvdbid": ""}
	for format, want := range cases {
		if got := renderName(format, tok); got != want {
			t.Errorf("renderName(%q) = %q, want %q", format, got, want)
		}
	}
}

func TestMovieTargetTMDBToken(t *testing.T) {
	im := NewImporter("/lib", quiet())
	im.SetNaming(fixedNaming{Naming{Folder: "{title} ({year}) {tmdb-{tmdbid}}", File: "{title} ({year}) {edition-{edition}} - {quality}"}})
	got := im.MovieTrackTarget(MovieTrack{Title: "Heat", Year: 1995, IDs: MovieIDs{TMDB: 949, IMDB: "tt0113277"}}, "Heat.1995.1080p.BluRay.x264-GRP", ".mkv")
	want := filepath.Join("/lib", "Heat (1995) {tmdb-949}", "Heat (1995) - 1080p BluRay.mkv")
	if got != want {
		t.Errorf("\n got  %q\n want %q", got, want)
	}
	// No id known: the tag drops out rather than leaving "{tmdb-}".
	got = im.MovieTrackTarget(MovieTrack{Title: "Heat", Year: 1995}, "Heat.1995.1080p.BluRay.x264-GRP", ".mkv")
	if want := filepath.Join("/lib", "Heat (1995)", "Heat (1995) - 1080p BluRay.mkv"); got != want {
		t.Errorf("no id:\n got  %q\n want %q", got, want)
	}
	// {imdbid} too.
	im.SetNaming(fixedNaming{Naming{Folder: "{title} {imdb-{imdbid}}", File: "{title}"}})
	got = im.MovieTrackTarget(MovieTrack{Title: "Heat", Year: 1995, IDs: MovieIDs{IMDB: "tt0113277"}}, "", ".mkv")
	if want := filepath.Join("/lib", "Heat {imdb-tt0113277}", "Heat.mkv"); got != want {
		t.Errorf("imdb:\n got  %q\n want %q", got, want)
	}
}

// An existing movie folder is kept by an import, whatever the scheme says now.
func TestMovieTrackTargetKeepsExistingFolder(t *testing.T) {
	im := NewImporter("/lib", quiet())
	im.SetNaming(fixedNaming{Naming{Folder: "{title} ({year}) {tmdb-{tmdbid}}", File: DefaultMovieFile}})
	old := filepath.Join("/lib", "Heat (1995)")
	got := im.MovieTrackTarget(MovieTrack{Title: "Heat", Year: 1995, IDs: MovieIDs{TMDB: 949}, Folder: old}, "Heat.1995.1080p.BluRay.x264-GRP", ".mkv")
	if want := filepath.Join(old, "Heat (1995) - 1080p BluRay.mkv"); got != want {
		t.Errorf("\n got  %q\n want %q", got, want)
	}
}

func TestEpisodeFolderTVDBTokenFallsBackToTMDB(t *testing.T) {
	im := NewImporter("/tv", quiet())
	im.SetSeriesNaming(fixedSeriesNaming{SeriesNaming{Folder: "{title} ({year}) {tvdb-{tvdbid}}"}})
	ids := map[string]SeriesIDs{
		"The Bear":   {TMDB: 136315, TVDB: 403245},
		"Obscure":    {TMDB: 777},
		"Nobody Has": {},
	}
	im.SetSeriesIDsFunc(func(title string, _ int) SeriesIDs { return ids[title] })
	cases := map[string]string{
		"The Bear":   "The Bear (2022) {tvdb-403245}",
		"Obscure":    "Obscure (2022) {tmdb-777}",
		"Nobody Has": "Nobody Has (2022)",
	}
	for title, wantFolder := range cases {
		got := im.EpisodeTarget(title, 2022, 1, 1, "x.1080p.WEB-DL.mkv", ".mkv")
		if dir := filepath.Base(filepath.Dir(filepath.Dir(got))); dir != wantFolder {
			t.Errorf("%s: folder %q, want %q", title, dir, wantFolder)
		}
	}
	// An existing folder is reused as it is.
	got := im.EpisodeTargetIn("The Bear (2022)", "The Bear", 2022, 1, 1, "x.1080p.WEB-DL.mkv", ".mkv")
	if dir := filepath.Base(filepath.Dir(filepath.Dir(got))); dir != "The Bear (2022)" {
		t.Errorf("existing folder became %q", dir)
	}
}
