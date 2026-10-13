package series

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/store"
)

// The TV library scan never offers a show from the recycle bin or Convert's originals hold:
// every folder it looks at is searched for, and nothing in .arrmada-recycle or
// .arrmada-hold may be.
func TestSeriesScanSkipsRecycleDir(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	root := t.TempDir()
	for _, p := range []string{
		filepath.Join(root, "Kept Show (2020)", "Season 01", "Kept Show - S01E01.mkv"),
		filepath.Join(root, ".arrmada-recycle", "Gone Show (2019)", "Gone Show - S01E01.mkv"),
		// Convert's originals hold: an original kept for undo is not a show.
		filepath.Join(root, ".arrmada-hold", "12", "Held Show - S01E01.mkv"),
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		_ = f.Truncate(200 << 20) // sparse: big enough to count as an episode, no disk used
		_ = f.Close()
	}
	svc := NewService(st.DB(), &fakeMeta{}, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetRootFunc(func() string { return root })
	res, err := svc.ScanLibrary(context.Background(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	// fakeMeta finds nothing, so every folder scanned ends up unmatched.
	if len(res.Unmatched) != 1 || res.Unmatched[0].Folder != "Kept Show (2020)" {
		t.Fatalf("unmatched = %+v, want only the kept show (the bin and the hold must not be scanned)", res.Unmatched)
	}
}

// CFG-25: the scan says how far it's got, counting show folders only (not the bins), and
// ends on done == total.
func TestSeriesScanReportsProgress(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	root := t.TempDir()
	for _, d := range []string{"Show A (2020)", "Show B (2021)", "Empty Show", ".arrmada-recycle"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewService(st.DB(), &fakeMeta{}, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	var got [][2]int
	if _, err := svc.ScanLibrary(context.Background(), root, func(done, total int) { got = append(got, [2]int{done, total}) }); err != nil {
		t.Fatal(err)
	}
	want := [][2]int{{0, 3}, {1, 3}, {2, 3}, {3, 3}}
	if len(got) != len(want) {
		t.Fatalf("progress = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("progress = %v, want %v", got, want)
		}
	}
}
