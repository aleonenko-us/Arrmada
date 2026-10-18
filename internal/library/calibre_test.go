package library

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// Calibre names book folders "Title (123)", the number being the book's id in its
// library. The scan's title guess drops it only for a folder holding Calibre's
// metadata.opf. A cover.jpg or a "Title - Author" file name proves nothing: "Saga (2)" and
// "Mistborn (3)" stripped to the series name would match volume 1 and merge the volumes.
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
	write("Pierce Brown/Golden Son (42)/Golden Son - Pierce Brown.epub")
	write("Pierce Brown/Golden Son (42)/metadata.opf")                      // Calibre's OPF: stripped
	write("Pierce Brown/Morning Star (7)/Morning Star - Pierce Brown.epub") // the name alone: kept
	write("Brian K Vaughan/Saga (2)/saga-two.epub")
	write("Brian K Vaughan/Saga (2)/cover.jpg") // a cover alone: kept
	write("Brandon Sanderson/Mistborn (3)/Mistborn 3.m4b")
	write("Brandon Sanderson/Mistborn (3)/cover.jpg")
	write("Ann Leckie/Ancillary Justice/Ancillary Justice.epub")

	im := NewImporter(t.TempDir(), slog.Default())
	var got []string
	dirs := map[string]string{}
	for _, bf := range im.FindBookFoldersIn(root) {
		got = append(got, bf.Author+"/"+bf.Title)
		dirs[bf.Title] = bf.Dir
	}
	sort.Strings(got)
	want := []string{"Ann Leckie/Ancillary Justice", "Brandon Sanderson/Mistborn (3)", "Brian K Vaughan/Saga (2)", "Pierce Brown/Golden Son", "Pierce Brown/Morning Star (7)"}
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
