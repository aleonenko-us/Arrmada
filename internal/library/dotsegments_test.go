package library

import (
	"path/filepath"
	"testing"
)

// A name that starts with dots would be a hidden folder or file, which scanners and media
// servers skip: the leading dots go, from every path segment, for music and books alike.
func TestMusicNamingDropsLeadingDots(t *testing.T) {
	n := MusicName{AlbumArtist: ".38 Special", AlbumTitle: "...And Justice for All", OriginalYear: 1988,
		DiscCount: 1, Disc: 1, Track: 1, TrackTitle: "...Baby One More Time"}
	got := filepath.ToSlash(MusicTrackRel(MusicNaming{Folder: DefaultMusicFolder, Track: "{Track Title} {Track}", Compilation: DefaultMusicCompilationTrack}, n, ".flac"))
	if got != "38 Special/And Justice for All (1988)/Baby One More Time 01.flac" {
		t.Errorf("got %s", got)
	}
	// Inside a name, dots stay.
	n.AlbumTitle = "M.A.S.H."
	if got := filepath.ToSlash(MusicAlbumFolderRel(DefaultMusicNaming, n)); got != "38 Special/M.A.S.H. (1988)" {
		t.Errorf("got %s", got)
	}
}

func TestBookNamingDropsLeadingDots(t *testing.T) {
	n := BookName{Author: "...Author", Title: ".hack Infection"}
	if got := filepath.ToSlash(BookFolderPath("", DefaultEbookNaming, false, n, "EPUB")); got != "Author/hack Infection" {
		t.Errorf("folder = %s", got)
	}
	if f := BookFileName(DefaultEbookNaming, false, n, "x.epub"); f != "hack Infection.epub" {
		t.Errorf("file = %s", f)
	}
}
