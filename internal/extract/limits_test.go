package extract

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copyFixtures copies testdata files whose names start with prefix into a temp dir.
func copyFixtures(t *testing.T, prefix string) string {
	t.Helper()
	dir := t.TempDir()
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), prefix) || strings.HasSuffix(e.Name(), ".sha256") {
			continue
		}
		b, err := os.ReadFile(filepath.Join("testdata", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func fileSHA(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Real multi-part RAR sets still unpack under the limits: a stored release is about its
// own size, so the 1.5× allowance is never in the way. Both namings: RAR5 .partN.rar and
// RAR4 .rar/.r00/.r01 (testdata, made with rar -m0 -v10k).
func TestExtractMultiPartRar(t *testing.T) {
	old := topSlack
	topSlack = 0 // only the ratio: the set's own size must cover it
	defer func() { topSlack = old }()
	for _, tc := range []struct{ prefix, file string }{
		{"newstyle", "Show.S01E02.mkv"},
		{"oldstyle", "Show.S01E01.mkv"},
	} {
		t.Run(tc.prefix, func(t *testing.T) {
			dir := copyFixtures(t, tc.prefix)
			n, err := ExtractTree(dir)
			if err != nil || n != 1 {
				t.Fatalf("ExtractTree = %d, %v; want the one set", n, err)
			}
			want, _ := os.ReadFile(filepath.Join("testdata", tc.prefix+".sha256"))
			if got := fileSHA(t, filepath.Join(dir, tc.file)); got != strings.TrimSpace(string(want)) {
				t.Errorf("%s unpacked wrong: sha %s", tc.file, got)
			}
			// The set's size counts every volume, not just the first.
			if size, first := archiveSize(filepath.Join(dir, firstVolume(t, dir))), statSize(t, filepath.Join(dir, firstVolume(t, dir))); size < 2*first {
				t.Errorf("archiveSize = %d, first volume %d: the other volumes weren't counted", size, first)
			}
			// A second pass writes nothing new.
			if _, err := ExtractTree(dir); err != nil {
				t.Fatalf("second pass: %v", err)
			}
		})
	}
}

func firstVolume(t *testing.T, dir string) string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(strings.ToLower(e.Name()), ".rar") && isFirstRarVolume(e.Name()) {
			return e.Name()
		}
	}
	t.Fatal("no first volume")
	return ""
}

func statSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}

// zipBytes builds a zip in memory from name → content pairs.
func zipBytes(t *testing.T, entries [][2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func noneLeft(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			t.Errorf("%s was written", n)
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".arrmada-tmp") {
			t.Errorf("temp file left: %s", e.Name())
		}
	}
}

// A small archive that inflates far past its own size is refused before anything is
// written: a zip of zeros compresses about 1000:1.
func TestExtractRatioLimit(t *testing.T) {
	old := topSlack
	topSlack = 64 << 10
	defer func() { topSlack = old }()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bomb.zip"), zipBytes(t, [][2]string{{"zeros.bin", strings.Repeat("\x00", 4<<20)}}), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ExtractTree(dir)
	if !errors.Is(err, ErrUnpackLimit) {
		t.Fatalf("err = %v, want ErrUnpackLimit", err)
	}
	noneLeft(t, dir, "zeros.bin")

	// Headers that lie (declaring less than the entry holds) are caught while writing.
	lim := int64(1000)
	err = writeFile(strings.NewReader(strings.Repeat("x", 5000)), filepath.Join(dir, "liar.bin"), &budget{remaining: 1 << 30}, &lim)
	if !errors.Is(err, ErrUnpackLimit) {
		t.Fatalf("writeFile past the archive's limit = %v", err)
	}
	noneLeft(t, dir, "liar.bin")
}

// An archive with more entries than maxEntries is refused before anything is written.
func TestExtractEntryLimit(t *testing.T) {
	old := maxEntries
	maxEntries = 10
	defer func() { maxEntries = old }()
	dir := t.TempDir()
	var es [][2]string
	for i := 0; i < 11; i++ {
		es = append(es, [2]string{fmt.Sprintf("f%02d.txt", i), "x"})
	}
	if err := os.WriteFile(filepath.Join(dir, "many.zip"), zipBytes(t, es), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractTree(dir); !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("err = %v, want ErrTooManyEntries", err)
	}
	noneLeft(t, dir, "f00.txt")
	// Ten is fine.
	if err := os.WriteFile(filepath.Join(dir, "many.zip"), zipBytes(t, es[:10]), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractTree(dir); err != nil {
		t.Fatalf("ten entries: %v", err)
	}
}

// What won't fit in the free space is refused up front.
func TestExtractNoSpace(t *testing.T) {
	old := freeBytes
	freeBytes = func(string) (uint64, bool) { return 100, true }
	defer func() { freeBytes = old }()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rel.zip"), zipBytes(t, [][2]string{{"video.mkv", strings.Repeat("v", 1000)}}), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractTree(dir); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("err = %v, want ErrNoSpace", err)
	}
	noneLeft(t, dir, "video.mkv")
}

// Archives inside archives are unpacked with their parent, against the same budget, as
// deep as maxNestDepth, and never peeled one level further by a later pass.
func TestExtractNestedArchives(t *testing.T) {
	old := maxNestDepth
	maxNestDepth = 1
	defer func() { maxNestDepth = old }()
	dir := t.TempDir()
	// outer.zip > mid.zip > deep.zip > deep.txt
	deep := zipBytes(t, [][2]string{{"deep.txt", "the bottom"}})
	mid := zipBytes(t, [][2]string{{"mid.txt", "middle"}, {"deep.zip", string(deep)}})
	outer := zipBytes(t, [][2]string{{"outer.txt", "top"}, {"mid.zip", string(mid)}})
	if err := os.WriteFile(filepath.Join(dir, "outer.zip"), outer, 0o644); err != nil {
		t.Fatal(err)
	}
	for pass := 1; pass <= 3; pass++ {
		n, err := ExtractTree(dir)
		if err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		if n != 1 {
			t.Errorf("pass %d extracted %d top-level archives, want 1 (outer.zip only)", pass, n)
		}
		for _, f := range []string{"outer.txt", "mid.zip", "mid.txt", "deep.zip"} {
			if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
				t.Errorf("pass %d: %s missing", pass, f)
			}
		}
		if _, err := os.Stat(filepath.Join(dir, "deep.txt")); err == nil {
			t.Fatalf("pass %d unpacked past maxNestDepth", pass)
		}
	}

	// The nested budget is the call's: a nested archive that would take the total past it
	// fails the whole extraction.
	old2 := maxUnpackedBytes
	maxUnpackedBytes = int64(len(mid)) + 3 + 5 // outer.txt + mid.zip, then "mid.txt" (6) is one too many
	defer func() { maxUnpackedBytes = old2 }()
	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, "outer.zip"), outer, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractTree(dir2); !errors.Is(err, ErrUnpackLimit) {
		t.Fatalf("nested past the call budget: %v", err)
	}
}
