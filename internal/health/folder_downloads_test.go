package health

import (
	"context"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/libroots"
)

// The library folders' own downloads folders join the folders the health panel checks:
// one role each, listed once however many library folders share it, and never the
// Downloads folder twice.
func TestDownloadFolders(t *testing.T) {
	got := DownloadFolders("/dl", []libroots.FolderDownloads{
		{Media: libroots.MediaMovie, ID: 0, LibraryLabel: "Movies", Library: "/m", Path: "/disk2/dl"},
		{Media: libroots.MediaSeries, ID: 4, LibraryLabel: "Anime", Library: "/a", Path: "/disk3/dl"},
		{Media: libroots.MediaMovie, ID: 2, LibraryLabel: "Kids", Library: "/k", Path: "/disk2/dl/"}, // shared
		{Media: libroots.MediaSeries, ID: 0, LibraryLabel: "TV", Library: "/t", Path: "/dl"},         // the Downloads folder
	})
	if len(got) != 2 {
		t.Fatalf("got %+v, want two folders", got)
	}
	if got[0].Role != "downloads-movies" || got[0].Label != "Downloads for Movies" || got[1].Role != "downloads-tv-4" {
		t.Errorf("got %+v", got)
	}
	for _, f := range append(got, Folder{Role: "downloads"}) {
		if !IsDownloads(f) {
			t.Errorf("%s isn't a downloads folder", f.Role)
		}
	}
	if IsDownloads(Folder{Role: "movies-3"}) {
		t.Error("a library counted as downloads")
	}
}

// A library folder's own downloads folder on another filesystem is a warning (imports
// copy); on the same one, or when it can't be told, nothing.
func TestFolderDownloadsCheck(t *testing.T) {
	src := func(context.Context) []libroots.FolderDownloads {
		return []libroots.FolderDownloads{
			{Media: libroots.MediaMovie, ID: 0, LibraryLabel: "Movies", Library: "/same/movies", Path: "/same/dl"},
			{Media: libroots.MediaMovie, ID: 3, LibraryLabel: "Kids", Library: "/disk2/kids", Path: "/disk1/dl"},
			{Media: libroots.MediaSeries, ID: 0, LibraryLabel: "TV", Library: "/unknown/tv", Path: "/unknown/dl"},
		}
	}
	same := func(a, b string) (bool, bool) {
		switch {
		case strings.HasPrefix(a, "/unknown"):
			return false, false
		case strings.HasPrefix(a, "/same"):
			return true, true
		}
		return false, true
	}
	got := folderDownloadsCheck(src, same).Run(context.Background())
	if len(got) != 1 || got[0].Key != "library.downloads.movies-3" || got[0].Level != LevelWarning || got[0].Fix != FixMoreFolders ||
		!strings.Contains(got[0].Message, "copied, not hardlinked") {
		t.Errorf("findings = %+v", got)
	}
}

// Free space is judged per downloads volume: the Downloads folder keeps its old key and
// wording; another folder's line names it; two folders on one volume are one line.
func TestDownloadsFreeCheck(t *testing.T) {
	free := map[string]float64{"/dl": 1, "/disk2/dl": 5, "/disk2/other": 5, "/disk3/dl": 500}
	dirs := func() []Folder {
		return []Folder{
			{Role: "downloads", Label: "Downloads", Path: "/dl"},
			{Role: "downloads-movies-2", Label: "Downloads for Kids", Path: "/disk2/dl"},
			{Role: "downloads-tv", Label: "Downloads for TV", Path: "/disk2/other"},
			{Role: "downloads-tv-3", Label: "Downloads for Anime", Path: "/disk3/dl"},
		}
	}
	disk := func(p string) string { return strings.SplitN(strings.TrimPrefix(p, "/"), "/", 2)[0] }
	same := func(a, b string) (bool, bool) { return disk(a) == disk(b), true }
	got := downloadsFreeCheck(dirs, func(p string) (float64, bool) { v, ok := free[p]; return v, ok }, same).Run(context.Background())
	if len(got) != 2 {
		t.Fatalf("findings = %+v, want two", got)
	}
	if got[0].Key != "disk.free" || got[0].Level != LevelError || !strings.Contains(got[0].Message, "the downloads volume") {
		t.Errorf("main = %+v", got[0])
	}
	if got[1].Key != "disk.free.downloads-movies-2" || got[1].Level != LevelWarning || !strings.Contains(got[1].Message, "Downloads for Kids") {
		t.Errorf("own = %+v", got[1])
	}
}
