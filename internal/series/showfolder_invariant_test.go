package series

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The structural checks behind a show's folder, on real folders with shows on disk that
// Arrmada doesn't track: whatever a layout looks like, the show's folder is never a folder
// above the one holding its files (except the show's folder above its season folder), never
// a folder holding another show, and when it can't be told, nothing is guessed.
func TestShowFolderInvariantWithUntrackedNeighbours(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name      string
		files     []string // the show's files, relative to the TV folder
		untracked []string // another show's files on disk, Arrmada doesn't track it
		want      string   // the show's folder; "" = refused
	}{
		// 1. Folders tagged like a release are show folders when the show's season folders
		// are in them, and never stepped out of.
		{"quality tag, seasoned", []string{"Drama/Breaking Bad (2008) [1080p]/Season 01/Breaking Bad - S01E01.mkv"},
			[]string{"Drama/The Wire/Season 1/The Wire - S01E01.mkv"}, "Drama/Breaking Bad (2008) [1080p]"},
		{"quality tag, flat", []string{"Drama/Breaking Bad 1080p/Breaking Bad - S01E01.mkv"},
			[]string{"Drama/The Wire/Season 1/The Wire - S01E01.mkv"}, ""},
		{"pack name, seasoned", []string{"Cartoons/Futurama S01-S07/Season 1/Futurama - S01E01.mkv"},
			[]string{"Cartoons/Bluey/Season 1/Bluey - S01E01.mkv"}, "Cartoons/Futurama S01-S07"},
		{"pack name, flat", []string{"Anime/Jujutsu Kaisen S2/Jujutsu Kaisen - S02E01.mkv"},
			[]string{"Anime/Dandadan/Season 1/Dandadan - S01E01.mkv"}, ""},
		{"quality tag, too-high TV folder", []string{"TV/Dark (2017) 2160p/Season 1/Dark - S01E01.mkv"}, nil, "TV/Dark (2017) 2160p"},
		// 2. A show folder named like a disc.
		{"disc-named show, flat", []string{"Music/CD2/CD2 - S01E01.mkv"}, nil, ""},
		{"disc-named show, seasoned", []string{"Music/DVD1/Season 1/DVD1 - S01E01.mkv"}, nil, "Music/DVD1"},
		{"disc folder in a season folder", []string{"Kids/Bluey/Season 1/Disc 1/Bluey - S01E01.mkv"},
			[]string{"Kids/Peppa Pig/Season 1/Peppa Pig - S01E01.mkv"}, "Kids/Bluey"},
		// 3. Names never promote a folder above the files: a category named like the show,
		// files named like the category.
		{"category named like the show", []string{"Star Trek/Star Trek TOS/Star Trek - S01E01.mkv"},
			[]string{"Star Trek/Star Trek Picard/Season 1/Star Trek Picard - S01E01.mkv"}, "Star Trek/Star Trek TOS"},
		{"files named like the category", []string{"Anime/Sousou no Frieren/Anime.S01E01.1080p.WEB.mkv"},
			[]string{"Anime/Dandadan/Season 1/Dandadan - S01E01.mkv"}, "Anime/Sousou no Frieren"},
		// 4. Two of one show's folders side by side never make the folder above them its.
		{"two folders of one show", []string{"Anime/Sousou no Frieren/Frieren - S01E01.mkv", "Anime/Sousou no Frieren 2nd Season/Frieren - S02E01.mkv"},
			[]string{"Anime/Dandadan/Season 1/Dandadan - S01E01.mkv"}, ""},
		// 5. A season pack dumped in a category.
		{"season pack in a category", []string{"Anime/Frieren.S01.1080p.BluRay-GRP/Frieren.S01E01.1080p.BluRay-GRP/Frieren.S01E01.1080p.BluRay-GRP.mkv"},
			[]string{"Anime/Dandadan/Season 1/Dandadan - S01E01.mkv"}, ""},
		// The chosen folder holding another show's folder: refused.
		{"show folder holding another show", []string{"Avatar/Season 1/Avatar - S01E01.mkv"},
			[]string{"Avatar/Legend of Korra/Season 1/Korra - S01E01.mkv"}, ""},
		// Ordinary layouts still work beside untracked shows.
		{"category, seasoned", []string{"Anime/Frieren/Season 1/Frieren - S01E01.mkv"},
			[]string{"Anime/Dandadan/Season 1/Dandadan - S01E01.mkv"}, "Anime/Frieren"},
		{"show's own release folders and extras", []string{"Show/Season 1/Show.S01E01.1080p.WEB-GRP/Show.S01E01.1080p.WEB-GRP.mkv"},
			[]string{"Show/Extras/Behind the scenes.mkv"}, "Show"},
	} {
		t.Run(c.name, func(t *testing.T) {
			tv := t.TempDir()
			f := newFolderFixture(t, tv)
			f.svc.fileGone = nil
			for _, rel := range append(append([]string{}, c.files...), c.untracked...) {
				p := filepath.Join(tv, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			files := make([]string, len(c.files))
			for i, rel := range c.files {
				files[i] = filepath.ToSlash(filepath.Join(tv, filepath.FromSlash(rel)))
			}
			id := f.show("Show", files...)
			info := f.svc.ShowFolderOf(ctx, id)
			want := ""
			if c.want != "" {
				want = filepath.Join(tv, filepath.FromSlash(c.want))
			}
			if info.Path != want || (want == "") != info.Unknown {
				t.Fatalf("ShowFolderOf = %+v, want %q", info, c.want)
			}
		})
	}
}
