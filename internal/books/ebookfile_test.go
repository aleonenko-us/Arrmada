package books

import (
	"os"
	"path/filepath"
	"testing"
)

// A stored path is normally the file; a folder yields the most readable format in it.
func TestEbookFilePicksTheBestFormatInAFolder(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"book.pdf", "book.mobi", "book.epub", "cover.jpg", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := EbookFile(dir)
	if err != nil || filepath.Base(got) != "book.epub" {
		t.Errorf("folder pick = %q, %v; want book.epub", got, err)
	}
	single := filepath.Join(dir, "book.mobi")
	if got, err := EbookFile(single); err != nil || got != single {
		t.Errorf("single file = %q, %v; want it back unchanged", got, err)
	}
	if _, err := EbookFile(filepath.Join(dir, "missing.epub")); err == nil {
		t.Error("a missing file must error, not serve nothing")
	}
	empty := t.TempDir()
	if _, err := EbookFile(empty); err == nil {
		t.Error("a folder with no ebook must error")
	}
}
