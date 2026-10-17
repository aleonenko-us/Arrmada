package libroots

import (
	"context"
	"testing"
)

// A library folder's own downloads folder (GitHub #11): the default folders' come from
// settings, the extras' from their rows. Grabs for a folder without one fall back to the
// Downloads folder; the distinct list puts Downloads first and names each once.
func TestRootsFolderDownloads(t *testing.T) {
	s := &fakeSettings{m: map[string]string{KeyTVDownloads: " /disk3/downloads "}}
	r := New(s.get, envCfg)
	ctx := context.Background()
	r.WithExtras(func(context.Context) []Extra {
		return []Extra{
			{Media: MediaMovie, ID: 1, Path: "/disk2/kids", Label: "Kids", Downloads: "/disk2/downloads"},
			{Media: MediaSeries, ID: 2, Path: "/disk3/anime", Label: "Anime", Downloads: "/disk3/downloads"}, // shared with TV
			{Media: MediaMovie, ID: 3, Path: "/disk4/docs", Label: "Docs"},                                   // none
		}
	})
	cases := []struct{ media, root, want string }{
		{MediaMovie, "", "/env/downloads"},              // default Movies: none set
		{MediaMovie, "/env/movies", "/env/downloads"},   // the same, by path
		{MediaMovie, "/disk2/kids", "/disk2/downloads"}, // an extra with one
		{MediaMovie, "/disk2/kids/", "/disk2/downloads"},
		{MediaMovie, "/disk4/docs", "/env/downloads"},  // an extra without
		{MediaMovie, "/disk3/anime", "/env/downloads"}, // a TV folder isn't a Movies one
		{MediaSeries, "", "/disk3/downloads"},          // default TV: from settings, trimmed
		{MediaSeries, "/env/tv", "/disk3/downloads"},
		{MediaSeries, "/disk3/anime", "/disk3/downloads"},
		{MediaSeries, "/somewhere/else", "/env/downloads"}, // not a folder of the library
	}
	for _, c := range cases {
		if got := r.DownloadsFor(ctx, c.media, c.root); got != c.want {
			t.Errorf("DownloadsFor(%s, %q) = %q, want %q", c.media, c.root, got, c.want)
		}
	}
	if got := r.OwnDownloads(ctx, MediaMovie, "/disk4/docs"); got != "" {
		t.Errorf("OwnDownloads without one = %q, want empty", got)
	}
	per := r.PerFolderDownloads(ctx)
	if len(per) != 3 || per[0].Media != MediaSeries || per[0].ID != 0 || per[0].Library != "/env/tv" || per[1].LibraryLabel != "Kids" {
		t.Errorf("PerFolderDownloads = %+v", per)
	}
	dl := r.DownloadFolders(ctx)
	if len(dl) != 3 || dl[0].Path != "/env/downloads" || dl[1].Path != "/disk3/downloads" || dl[2].Path != "/disk2/downloads" {
		t.Errorf("DownloadFolders = %+v, want Downloads first, then each own folder once", dl)
	}
	// Manual import and the file info check read All: every downloads folder is in it.
	all := r.All(ctx)
	if n := len(all); n < 3 || all[n-1].Path != "/disk2/downloads" || all[n-3].Path != "/env/downloads" {
		t.Errorf("All = %+v", all)
	}
	// None set: exactly as before.
	plain := New((&fakeSettings{m: map[string]string{}}).get, envCfg)
	if got := plain.DownloadFolders(ctx); len(got) != 1 || got[0].Path != "/env/downloads" {
		t.Errorf("DownloadFolders without any = %+v", got)
	}
	if got := plain.DownloadsFor(ctx, MediaMovie, ""); got != "/env/downloads" {
		t.Errorf("DownloadsFor without any = %q", got)
	}
}
