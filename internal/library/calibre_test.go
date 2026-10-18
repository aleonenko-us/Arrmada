package library

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// Calibre names book folders "Title (123)", the number being the book's id in its
// library. The scan's title guess drops it for a folder Calibre made, and keeps a number
// in someone's own folder name.
func TestFindBookFoldersStripsCalibreID(t *testing.T) {
	root := t.TempDir()
	write := func(rel string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Pierce Brown/Golden Son (42)/Golden Son - Pierce Brown.epub") // Calibre's file name
	write("Pierce Brown/Morning Star (7)/whatever.epub")
	write("Pierce Brown/Morning Star (7)/metadata.opf") // Calibre's OPF
	write("Frank Herbert/Dune (1)/book.epub")
	write("Frank Herbert/Dune (1)/cover.jpg")       // Calibre's cover
	write("Brian K Vaughan/Saga (2)/saga-two.epub") // someone's own
	write("Ann Leckie/Ancillary Justice/Ancillary Justice.epub")

	im := NewImporter(t.TempDir(), slog.Default())
	var got []string
	dirs := map[string]string{}
	for _, bf := range im.FindBookFoldersIn(root) {
		got = append(got, bf.Author+"/"+bf.Title)
		dirs[bf.Title] = bf.Dir
	}
	sort.Strings(got)
	want := []string{"Ann Leckie/Ancillary Justice", "Brian K Vaughan/Saga (2)", "Frank Herbert/Dune", "Pierce Brown/Golden Son", "Pierce Brown/Morning Star"}
	if len(got) != len(want) {
		t.Fatalf("folders = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("folders = %v, want %v", got, want)
			break
		}
	}
	if d := dirs["Golden Son"]; d != filepath.Join(root, "Pierce Brown", "Golden Son (42)") {
		t.Errorf("Dir = %q, want the book's own folder", d)
	}
}
