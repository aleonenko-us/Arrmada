package library

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func sparseFile(t *testing.T, p string, size int64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

// An importer taken with Using(ctx) reports its own copies, in order, up to the whole
// file; the same importer without the context, copying out of the same folder, reports
// nothing — progress is never matched by folder.
func TestUsingReportsOnlyItsOwnCopies(t *testing.T) {
	defer ForceCopies()()
	src := t.TempDir()
	dst := t.TempDir()
	const size = 150 << 20 // three reports' worth
	mine := filepath.Join(src, "Sub", "Film.mkv")
	other := filepath.Join(src, "Other.mkv") // same folder: a no-subfolder download layout
	sparseFile(t, mine, size)
	sparseFile(t, other, size)

	var mu sync.Mutex
	var reports []int64
	ctx := WithCopyReport(context.Background(), func(from string, done, total int64) {
		mu.Lock()
		defer mu.Unlock()
		if from != mine || total != size {
			t.Errorf("report for %s (%d) — want %s (%d)", from, total, mine, size)
		}
		reports = append(reports, done)
	})
	im := NewImporter(dst, slog.Default())
	if method, err := im.Using(ctx).linkOrCopy(mine, filepath.Join(dst, "a.mkv")); err != nil || method != "copy" {
		t.Fatalf("copy: %q, %v", method, err)
	}
	if _, err := im.linkOrCopy(other, filepath.Join(dst, "b.mkv")); err != nil {
		t.Fatal(err)
	}
	if im.copyReport != nil {
		t.Fatal("Using changed the shared importer")
	}
	if len(reports) < 3 || reports[0] != 0 || reports[len(reports)-1] != size {
		t.Fatalf("reports = %v, want 0 … %d in steps", reports, size)
	}
	for i := 1; i < len(reports); i++ {
		if reports[i] < reports[i-1] {
			t.Fatalf("reports went backwards: %v", reports)
		}
	}
	// The innermost report wins.
	var inner int
	ctx2 := WithCopyReport(ctx, func(string, int64, int64) { inner++ })
	if _, err := im.Using(ctx2).linkOrCopy(mine, filepath.Join(dst, "c.mkv")); err != nil || inner == 0 {
		t.Fatalf("inner report heard %d, err %v", inner, err)
	}
}

// A reported copy still hands the kernel what its fast paths need (copy_file_range
// reflinks on btrfs/XFS and copies server-side on some shares): an *os.File writer and a
// LimitedReader over an *os.File — never a wrapping reader that forces a userspace copy.
func TestReportedCopyKeepsTheFastPath(t *testing.T) {
	src := filepath.Join(t.TempDir(), "Film.mkv")
	sparseFile(t, src, 100<<20)
	type call struct{ w, r string }
	var calls []call
	orig := copyTo
	copyTo = func(w io.Writer, r io.Reader) (int64, error) {
		c := call{w: typeName(w), r: typeName(r)}
		if lr, ok := r.(*io.LimitedReader); ok {
			c.r += "<" + typeName(lr.R) + ">"
		}
		calls = append(calls, c)
		return orig(w, r)
	}
	defer func() { copyTo = orig }()

	var last int64
	if err := copyFile(src, filepath.Join(t.TempDir(), "out.mkv"), func(_ string, done, _ int64) { last = done }); err != nil {
		t.Fatal(err)
	}
	if last != 100<<20 || len(calls) < 2 {
		t.Fatalf("last report %d, %d chunk(s)", last, len(calls))
	}
	for _, c := range calls {
		if c.w != "*os.File" || c.r != "*io.LimitedReader<*os.File>" {
			t.Fatalf("copy handed %s ← %s; want *os.File ← *io.LimitedReader<*os.File>", c.w, c.r)
		}
	}
	// Unreported, the file itself goes straight to io.Copy.
	calls = nil
	if err := copyFile(src, filepath.Join(t.TempDir(), "out2.mkv"), nil); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != (call{w: "*os.File", r: "*os.File"}) {
		t.Fatalf("unreported copy = %+v", calls)
	}
}

func typeName(v any) string {
	switch v.(type) {
	case *os.File:
		return "*os.File"
	case *io.LimitedReader:
		return "*io.LimitedReader"
	}
	return "other"
}
