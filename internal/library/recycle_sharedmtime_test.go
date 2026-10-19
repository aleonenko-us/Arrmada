package library

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Binning a file that has another name (a library file hardlinked to a torrent that is
// still seeding) moves only the library's name and leaves the shared mtime alone; the
// sidecar records when it was deleted, which is what the bin's retention reads.
func TestRecycleLeavesASharedInodesMtime(t *testing.T) {
	base := t.TempDir()
	seed := filepath.Join(base, "dl", "01.flac")
	lib := filepath.Join(base, "music", "A", "01.flac")
	for _, d := range []string{filepath.Dir(seed), filepath.Dir(lib)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(seed, []byte("AUDIO"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(seed, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(seed, lib); err != nil {
		t.Skip(err)
	}
	dst, err := RecycleFile(filepath.Join(base, "bin"), lib)
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(seed); !fi.ModTime().Equal(old) {
		t.Errorf("the seeding file's mtime changed: %v -> %v", old, fi.ModTime())
	}
	if m := ReadRecycleMeta(dst); m.Deleted < time.Now().Add(-time.Minute).Unix() || m.Orig != lib {
		t.Errorf("sidecar %+v", m)
	}
	// A file with one name is stamped with its deletion time, as before.
	solo := filepath.Join(base, "music", "A", "02.flac")
	_ = os.WriteFile(solo, []byte("X"), 0o644)
	_ = os.Chtimes(solo, old, old)
	dst2, err := RecycleFile(filepath.Join(base, "bin"), solo)
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(dst2); !fi.ModTime().After(old.Add(time.Hour)) {
		t.Error("a single-name file wasn't stamped with its deletion time")
	}
}
