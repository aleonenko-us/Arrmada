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
	if !errors.Is(err, ErrArchiveLimit) || errors.Is(err, ErrUnpackLimit) {
		t.Fatalf("err = %v, want ErrArchiveLimit", err)
	}
	noneLeft(t, dir, "zeros.bin")

	// Headers that lie (declaring less than the entry holds) are caught while writing.
	lim := int64(1000)
	_, err = writeFile(strings.NewReader(strings.Repeat("x", 5000)), filepath.Join(dir, "liar.bin"), &budget{remaining: 1 << 30}, &lim)
	if !errors.Is(err, ErrArchiveLimit) {
		t.Fatalf("writeFile past the archive's limit = %v", err)
	}
	noneLeft(t, dir, "liar.bin")
	// … and past the call's total, that's the total's error.
	lim = 1 << 30
	_, err = writeFile(strings.NewReader(strings.Repeat("x", 5000)), filepath.Join(dir, "liar.bin"), &budget{remaining: 1000}, &lim)
	if !errors.Is(err, ErrUnpackLimit) {
		t.Fatalf("writeFile past the total = %v", err)
	}
}

// One archive over its own allowance (or with too many entries) is skipped and reported;
// every other archive in the download still unpacks, on every pass.
func TestExtractOneRefusalDoesntStopTheRest(t *testing.T) {
	old, oldMax := topSlack, maxEntries
	topSlack, maxEntries = 64<<10, 10
	defer func() { topSlack, maxEntries = old, oldMax }()
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var many [][2]string
	for i := 0; i < 11; i++ {
		many = append(many, [2]string{fmt.Sprintf("f%02d.txt", i), "x"})
	}
	write := func(p string, b []byte) {
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(a, "1-bomb.zip"), zipBytes(t, [][2]string{{"zeros.bin", strings.Repeat("\x00", 4<<20)}}))
	write(filepath.Join(a, "2-many.zip"), zipBytes(t, many))
	write(filepath.Join(a, "3-good.zip"), zipBytes(t, [][2]string{{"good-a.mkv", "video a"}}))
	write(filepath.Join(b, "good.zip"), zipBytes(t, [][2]string{{"good-b.mkv", "video b"}}))
	for pass := 1; pass <= 2; pass++ {
		n, err := ExtractTree(root)
		if !errors.Is(err, ErrArchiveLimit) || !errors.Is(err, ErrTooManyEntries) || n != 2 {
			t.Errorf("pass %d: %d archives, %v", pass, n, err)
		}
		for _, p := range []string{filepath.Join(a, "good-a.mkv"), filepath.Join(b, "good-b.mkv")} {
			if _, err := os.Stat(p); err != nil {
				t.Errorf("pass %d: %s wasn't unpacked", pass, filepath.Base(p))
			}
		}
	}
	noneLeft(t, a, "zeros.bin", "f00.txt")
}

// 0-day style: each zip holds one volume of a RAR set (and a .diz). The set is unpacked
// once every zip has been, in the same pass.
func TestExtractZipsOfRarVolumes(t *testing.T) {
	for _, set := range [][]string{
		{"oldstyle.rar", "oldstyle.r00", "oldstyle.r01"},
		{"newstyle.part1.rar", "newstyle.part2.rar", "newstyle.part3.rar"},
	} {
		dir := t.TempDir()
		for i, vol := range set {
			b, err := os.ReadFile(filepath.Join("testdata", vol))
			if err != nil {
				t.Fatal(err)
			}
			// The first volume in the last zip: its siblings' volumes come later in the pass.
			name := fmt.Sprintf("rel%02d.zip", len(set)-i)
			if err := os.WriteFile(filepath.Join(dir, name), zipBytes(t, [][2]string{{vol, string(b)}, {"file_id.diz", "diz"}}), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		n, err := ExtractTree(dir)
		if err != nil || n != 3 {
			t.Fatalf("%s: %d archives, %v", set[0], n, err)
		}
		file, sum := "Show.S01E01.mkv", "oldstyle.sha256"
		if strings.HasPrefix(set[0], "new") {
			file, sum = "Show.S01E02.mkv", "newstyle.sha256"
		}
		want, _ := os.ReadFile(filepath.Join("testdata", sum))
		if got := fileSHA(t, filepath.Join(dir, file)); got != strings.TrimSpace(string(want)) {
			t.Errorf("%s: unpacked wrong", file)
		}
		// A second pass: nothing new, no errors, the volumes left as the zips' contents.
		if _, err := ExtractTree(dir); err != nil {
			t.Errorf("second pass: %v", err)
		}
	}
}

// Scene vobsubs: Subs/<ep>.subs.zip holds the .idx and another zip with a large, very
// compressible .sub. The inner zip draws on its parent's allowance (not a small fixed
// one), and the episodes beside it unpack too.
func TestExtractNestedSubsZip(t *testing.T) {
	root := t.TempDir()
	sub := strings.Repeat("\x00\x00\x01\xba\xff\xff", 5<<20) // 30 MB that deflates to very little
	inner := zipBytesDeflate(t, "x.subs.sub", sub)
	for _, ep := range []string{"E01", "E02", "E03"} {
		d := filepath.Join(root, "Show.S01"+ep)
		if err := os.MkdirAll(filepath.Join(d, "Subs"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, ep+".zip"), zipBytes(t, [][2]string{{"Show.S01" + ep + ".mkv", strings.Repeat("v", 1<<20)}}), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "Subs", ep+".subs.zip"), zipBytes(t, [][2]string{{ep + ".subs.idx", "idx"}, {ep + ".inner.zip", string(inner)}}), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for pass := 1; pass <= 2; pass++ {
		if _, err := ExtractTree(root); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	for _, ep := range []string{"E01", "E02", "E03"} {
		if _, err := os.Stat(filepath.Join(root, "Show.S01"+ep, "Show.S01"+ep+".mkv")); err != nil {
			t.Errorf("%s's video never unpacked", ep)
		}
		if fi, err := os.Stat(filepath.Join(root, "Show.S01"+ep, "Subs", "x.subs.sub")); err != nil || fi.Size() != int64(len(sub)) {
			t.Errorf("%s's subtitles never unpacked: %v", ep, err)
		}
	}
}

// zipBytesDeflate is one deflated entry: what a real zip of compressible data looks like.
func zipBytesDeflate(t *testing.T, name, data string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(data)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// An archive that holds a file with its own name never takes itself for one it held.
func TestExtractArchiveNeverNestsItself(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "self.zip"), zipBytes(t, [][2]string{{"self.zip", "not really"}, {"a.mkv", "video"}}), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, err := ExtractTree(dir); err != nil || n != 1 {
		t.Fatalf("= %d, %v", n, err)
	}
	if loadNested(dir).has(filepath.Join(dir, "self.zip")) {
		t.Error("the archive marked itself as one it held")
	}
	if n, _ := ExtractTree(dir); n != 1 {
		t.Errorf("a later pass skipped it (%d)", n)
	}
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
