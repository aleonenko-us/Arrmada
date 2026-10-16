package library

import (
	"os"
	"path/filepath"
	"testing"
)

// A copy out of a watched folder reports its bytes as it goes and its whole size at the
// end; a copy out of anywhere else, or after the watch stops, reports nothing.
func TestWatchCopiesReportsBytes(t *testing.T) {
	defer ForceCopies()()
	src := t.TempDir()
	other := t.TempDir()
	dst := t.TempDir()
	const size = 40 << 20
	write := func(p string) {
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(size); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
	watched := filepath.Join(src, "Sub", "Film.mkv")
	if err := os.MkdirAll(filepath.Dir(watched), 0o755); err != nil {
		t.Fatal(err)
	}
	write(watched)
	elsewhere := filepath.Join(other, "Other.mkv")
	write(elsewhere)

	var reports []int64
	stop := WatchCopies(src, func(from string, done, total int64) {
		if from != watched || total != size {
			t.Errorf("report for %s (%d) — want %s (%d)", from, total, watched, size)
		}
		reports = append(reports, done)
	})
	if method, err := linkOrCopy(watched, filepath.Join(dst, "a.mkv")); err != nil || method != "copy" {
		t.Fatalf("copy: %q, %v", method, err)
	}
	if _, err := linkOrCopy(elsewhere, filepath.Join(dst, "b.mkv")); err != nil {
		t.Fatal(err)
	}
	if len(reports) < 3 || reports[0] != 0 || reports[len(reports)-1] != size {
		t.Fatalf("reports = %v, want 0 … %d in steps", reports, size)
	}
	for i := 1; i < len(reports); i++ {
		if reports[i] < reports[i-1] {
			t.Fatalf("reports went backwards: %v", reports)
		}
	}

	stop()
	n := len(reports)
	if _, err := linkOrCopy(watched, filepath.Join(dst, "c.mkv")); err != nil {
		t.Fatal(err)
	}
	if len(reports) != n {
		t.Errorf("a stopped watch still heard %d report(s)", len(reports)-n)
	}
}
