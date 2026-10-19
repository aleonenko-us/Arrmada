package library

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func writeBlob(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func binFiles(t *testing.T, bin string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(bin, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Ext(p) != RecycleMetaExt {
			out = append(out, filepath.Base(p))
		}
		return nil
	})
	return out
}

func albumImporter(t *testing.T) (*Importer, string) {
	t.Helper()
	im := NewImporter(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	bin := t.TempDir()
	im.SetRecycleDir(bin)
	return im, bin
}

// A better copy replacing the file at the same path: the new copy is made first, the old
// one goes to the bin, and nothing is ever lost in between.
func TestPlaceAlbumTrackReplacesThroughBin(t *testing.T) {
	im, bin := albumImporter(t)
	dst := filepath.Join(im.MusicDir(), "A", "B", "01 - X.flac")
	writeBlob(t, dst, "old")
	src := filepath.Join(t.TempDir(), "new.flac")
	writeBlob(t, src, "new")
	if err := im.PlaceAlbumTrack(src, dst, ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "new" {
		t.Errorf("dst holds %q, want the new copy", b)
	}
	if got := binFiles(t, bin); len(got) != 1 {
		t.Errorf("bin holds %v, want the old copy", got)
	}
	if _, err := os.Stat(dst + ".arrmada-part"); !os.IsNotExist(err) {
		t.Error("no temporary file may be left behind")
	}
}

// When the new copy can't be made at all, the held file is untouched and nothing went to
// the bin.
func TestPlaceAlbumTrackFailedCopyKeepsHeld(t *testing.T) {
	im, bin := albumImporter(t)
	dst := filepath.Join(im.MusicDir(), "A", "B", "01 - X.flac")
	writeBlob(t, dst, "old")
	err := im.PlaceAlbumTrack(filepath.Join(t.TempDir(), "missing.flac"), dst, "")
	if err == nil {
		t.Fatal("placing a missing source should fail")
	}
	if b, _ := os.ReadFile(dst); string(b) != "old" {
		t.Errorf("dst holds %q, want the old file untouched", b)
	}
	if got := binFiles(t, bin); len(got) != 0 {
		t.Errorf("bin holds %v, want nothing", got)
	}
	if _, err := os.Stat(dst + ".arrmada-part"); !os.IsNotExist(err) {
		t.Error("no temporary file may be left behind")
	}
}

// The "old" sibling being the very file at dst (a case-insensitive share spells it two
// ways; a hard link stands in for that here) isn't recycled as a second file.
func TestPlaceAlbumTrackOldSameAsDst(t *testing.T) {
	im, bin := albumImporter(t)
	dst := filepath.Join(im.MusicDir(), "A", "B", "01 - X.flac")
	writeBlob(t, dst, "old")
	alias := filepath.Join(im.MusicDir(), "A", "B", "01 - X.FLAC.alias")
	if err := os.Link(dst, alias); err != nil {
		t.Skip("no hard links here:", err)
	}
	src := filepath.Join(t.TempDir(), "new.flac")
	writeBlob(t, src, "new")
	if err := im.PlaceAlbumTrack(src, dst, alias); err != nil {
		t.Fatal(err)
	}
	if got := binFiles(t, bin); len(got) != 1 {
		t.Errorf("bin holds %v, want the replaced file once", got)
	}
}

// With no bin, replacing is refused and the held file stays exactly as it was.
func TestPlaceAlbumTrackNoBinRefuses(t *testing.T) {
	im := NewImporter(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	dst := filepath.Join(im.MusicDir(), "A", "B", "01 - X.flac")
	writeBlob(t, dst, "old")
	src := filepath.Join(t.TempDir(), "new.flac")
	writeBlob(t, src, "new")
	if err := im.PlaceAlbumTrack(src, dst, ""); !errors.Is(err, ErrReplacementRefused) {
		t.Fatalf("err = %v, want ErrReplacementRefused", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "old" {
		t.Errorf("dst holds %q", b)
	}
}
