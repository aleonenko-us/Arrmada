package library

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A name of only dots names no folder: clean gives nothing, so callers fall back.
func TestCleanDotOnlyNames(t *testing.T) {
	for _, in := range []string{".", "..", "...", " .. ", ". .", "..\x00", "/../"} {
		if got := clean(in); got != "" {
			t.Errorf("clean(%q) = %q, want empty", in, got)
		}
	}
	for in, want := range map[string]string{"...And Justice for All": "...And Justice for All", "M.A.S.H.": "M.A.S.H.", "Se7en": "Se7en"} {
		if got := clean(in); got != want {
			t.Errorf("clean(%q) = %q, want %q", in, got, want)
		}
	}
}

// bigVideo makes a sparse file over FindVideos' sample floor.
func bigVideo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(minVideoSize + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// A TMDB show called ".." (no year) used to make a rescan walk the folder ABOVE the TV
// library, and "." the TV library itself, claiming other shows' files. Neither may happen.
func TestSeriesNamedDotDotStaysInTVRoot(t *testing.T) {
	base := t.TempDir()
	tv := filepath.Join(base, "tv")
	// A video above the TV root, and one belonging to another show in it.
	bigVideo(t, filepath.Join(base, "Outside - S01E01.mkv"))
	bigVideo(t, filepath.Join(tv, "Other Show", "Season 1", "Other Show - S01E01.mkv"))
	im := NewImporter(tv, quiet())

	for _, tc := range []struct{ folder, title string }{
		{"", ".."}, {"..", ".."}, {".", "Some Show"}, {"", "."}, {"..", ""},
	} {
		for _, v := range im.SeriesLibraryVideos(tc.folder, tc.title, 0) {
			t.Errorf("SeriesLibraryVideos(%q, %q) found %s, outside the show's own folder", tc.folder, tc.title, v.Path)
		}
		if got := im.SeriesLibraryFilesIn(tc.folder, tc.title, 0); len(got) != 0 {
			t.Errorf("SeriesLibraryFilesIn(%q, %q) found %+v", tc.folder, tc.title, got)
		}
	}

	// An import for the show lands in a folder of its own inside the TV root.
	src := t.TempDir()
	ep := filepath.Join(src, "Show.S01E02.1080p.WEB-DL-GRP.mkv")
	writeFile(t, ep, 3000)
	for _, folder := range []string{"", "..", "."} {
		res, ok, err := im.ImportEpisodeAs(folder, "..", 0, 1, 2, ep)
		if err != nil || !ok {
			t.Fatalf("ImportEpisodeAs(%q): ok=%v err=%v", folder, ok, err)
		}
		if !strings.HasPrefix(res.TargetPath, filepath.Join(tv, "Unknown")+string(filepath.Separator)) {
			t.Errorf("ImportEpisodeAs(%q) placed it at %s, want under %s", folder, res.TargetPath, filepath.Join(tv, "Unknown"))
		}
	}
	if entries, _ := os.ReadDir(base); len(entries) != 2 { // tv and the outside video
		t.Errorf("something was created beside the TV root: %v", entries)
	}
}

// An artist or album called ".." gets the Unknown names, inside the music root, and
// PlaceFile refuses any target outside it.
func TestAlbumNamedDotDotStaysInMusicRoot(t *testing.T) {
	base := t.TempDir()
	music := filepath.Join(base, "music")
	if err := os.MkdirAll(music, 0o755); err != nil {
		t.Fatal(err)
	}
	im := NewImporter(music, quiet())

	got := im.AlbumTrackTarget("..", "..", 0, 1, 3, "..", ".flac")
	want := filepath.Join(music, "Unknown Artist", "Unknown Album", "03.flac")
	if got != want {
		t.Errorf("AlbumTrackTarget(.., ..) = %s, want %s", got, want)
	}
	if got := im.AlbumTrackTarget("Artist", "..", 2001, 1, 1, "Song", ".flac"); got != filepath.Join(music, "Artist", "Unknown Album (2001)", "01 - Song.flac") {
		t.Errorf("album .. with a year = %s", got)
	}

	src := filepath.Join(t.TempDir(), "track.flac")
	writeFile(t, src, 100)
	if err := im.PlaceFile(src, want); err != nil {
		t.Fatalf("PlaceFile inside the root: %v", err)
	}
	for _, dst := range []string{
		filepath.Join(music, "..", "escaped.flac"),
		filepath.Join(music, "Artist", "..", "..", "escaped.flac"),
		filepath.Join(base, "music-old", "x.flac"),
		music,
	} {
		if err := im.PlaceFile(src, dst); !errors.Is(err, ErrOutsideRoot) {
			t.Errorf("PlaceFile(%s) = %v, want ErrOutsideRoot", dst, err)
		}
	}
	if _, err := os.Stat(filepath.Join(base, "escaped.flac")); err == nil {
		t.Error("a file was placed outside the music root")
	}
}
