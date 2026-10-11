package movies

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/store"
)

// missingFixture is a temp movies folder with n films, each in its own folder and recorded
// as its movie's file. Nothing here touches a real library.
func missingFixture(t *testing.T, n int) (*Service, string, []Movie) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	root := filepath.Join(t.TempDir(), "Movies")
	svc := NewService(st.DB(), fakeMeta{}, nil, root, "", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	var out []Movie
	for i := 0; i < n; i++ {
		title := fmt.Sprintf("Film %02d", i)
		p := writeFixture(t, filepath.Join(root, title+" (2000)", title+".mkv"))
		m, err := svc.repo.Create(ctx, Movie{TMDBID: 100 + i, Title: title, Year: 2000, Monitored: true, QualityProfile: "hd"})
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.repo.SetFile(ctx, m.ID, p); err != nil {
			t.Fatal(err)
		}
		m.MovieFilePath, m.HasFile = p, true
		out = append(out, m)
	}
	// The recycle bin lives in the movies folder; it must never count as "something there".
	if err := os.MkdirAll(filepath.Join(root, ".arrmada-recycle"), 0o755); err != nil {
		t.Fatal(err)
	}
	return svc, root, out
}

func missingFlag(t *testing.T, svc *Service, id int64) bool {
	t.Helper()
	m, err := svc.repo.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return m.FileMissing
}

// countOnDisk counts the regular files under dir: the sweep must never remove or move one.
func countOnDisk(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

// A removed file is flagged with a "missing" event, the list shows it, and nothing else
// changes; once the file is back, the next sweep clears the flag.
func TestCheckMissingFiles(t *testing.T) {
	svc, root, ms := missingFixture(t, 5)
	ctx := context.Background()
	gone := ms[2]
	if err := os.Remove(gone.MovieFilePath); err != nil {
		t.Fatal(err)
	}
	before := countOnDisk(t, root)

	res, err := svc.CheckMissingFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Checked != 5 || res.Flagged != 1 || res.Refused != "" {
		t.Fatalf("sweep = %+v, want 5 checked, 1 flagged", res)
	}
	if !missingFlag(t, svc, gone.ID) || missingFlag(t, svc, ms[0].ID) {
		t.Fatal("only the removed film should be flagged")
	}
	assertLastEvent(t, svc, gone.ID, "missing")
	got, _ := svc.repo.Get(ctx, gone.ID)
	if !got.HasFile || got.MovieFilePath != gone.MovieFilePath || !got.Monitored {
		t.Fatalf("the sweep changed more than the flag: %+v", got)
	}
	sums, _ := svc.ListSummaries(ctx)
	for _, s := range sums {
		if (s.ID == gone.ID) != s.FileMissing {
			t.Errorf("summary %d file_missing = %v", s.ID, s.FileMissing)
		}
	}
	if after := countOnDisk(t, root); after != before {
		t.Fatalf("files on disk %d → %d: the sweep must never delete or move a file", before, after)
	}

	// A second pass doesn't flag (or log an event) again.
	if res, _ = svc.CheckMissingFiles(ctx); res.Flagged != 0 {
		t.Fatalf("second sweep flagged %d again", res.Flagged)
	}

	// Back on disk: cleared.
	writeFixture(t, gone.MovieFilePath)
	if res, _ = svc.CheckMissingFiles(ctx); res.Cleared != 1 || missingFlag(t, svc, gone.ID) {
		t.Fatalf("after restoring: %+v, flag %v", res, missingFlag(t, svc, gone.ID))
	}
	assertLastEvent(t, svc, gone.ID, "detected")
}

// The guard: a movies folder that isn't there (an unmounted share), or is empty apart from
// the recycle bin (an array that isn't started), flags nothing — and says so for the
// health panel. Nothing is deleted or cleared.
func TestCheckMissingFilesUnmountedRoot(t *testing.T) {
	for _, mode := range []string{"missing", "empty"} {
		t.Run(mode, func(t *testing.T) {
			svc, root, ms := missingFixture(t, 4)
			ctx := context.Background()
			switch mode {
			case "missing":
				if err := os.Rename(root, root+".unmounted"); err != nil {
					t.Fatal(err)
				}
			case "empty":
				for _, m := range ms {
					if err := os.RemoveAll(filepath.Dir(m.MovieFilePath)); err != nil {
						t.Fatal(err)
					}
				}
			}
			res, err := svc.CheckMissingFiles(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if res.Flagged != 0 || res.Skipped != 4 || !strings.Contains(res.Refused, "looks unmounted") {
				t.Fatalf("sweep = %+v, want nothing flagged and a warning", res)
			}
			for _, m := range ms {
				got, _ := svc.repo.Get(ctx, m.ID)
				if got.FileMissing || !got.HasFile || got.MovieFilePath != m.MovieFilePath {
					t.Fatalf("film %d changed under an unmounted folder: %+v", m.ID, got)
				}
			}
			if svc.MissingSweepWarning() == "" {
				t.Fatal("the health warning should be set")
			}
			// The detail page doesn't flag it either while the folder looks unmounted.
			if _, err := svc.VersionsLive(ctx, ms[0].ID); err != nil {
				t.Fatal(err)
			}
			if missingFlag(t, svc, ms[0].ID) {
				t.Fatal("opening a film with the array down must not flag it")
			}
		})
	}
}

// More files vanishing in one pass than a deletion would explain flags none of them.
func TestCheckMissingFilesAboveThreshold(t *testing.T) {
	svc, _, ms := missingFixture(t, 30)
	ctx := context.Background()
	for _, m := range ms[:21] { // over max(20, 5%)
		if err := os.Remove(m.MovieFilePath); err != nil {
			t.Fatal(err)
		}
	}
	res, err := svc.CheckMissingFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Flagged != 0 || res.Refused == "" {
		t.Fatalf("sweep = %+v, want nothing flagged over the threshold", res)
	}
	for _, m := range ms {
		if missingFlag(t, svc, m.ID) {
			t.Fatalf("film %d flagged over the threshold", m.ID)
		}
	}
	// At the threshold it goes ahead: put one back.
	writeFixture(t, ms[0].MovieFilePath)
	if res, _ = svc.CheckMissingFiles(ctx); res.Flagged != 20 || res.Refused != "" || svc.MissingSweepWarning() != "" {
		t.Fatalf("sweep at the threshold = %+v", res)
	}
}

// Only "no such file" is missing: a file the sweep can't read (permissions, a flaky share)
// is skipped, not flagged.
func TestCheckMissingFilesUnreadableIsNotMissing(t *testing.T) {
	svc, _, ms := missingFixture(t, 3)
	svc.SetFileAccess(nil, func(p string) (os.FileInfo, error) {
		if p == ms[1].MovieFilePath {
			return nil, &fs.PathError{Op: "stat", Path: p, Err: fs.ErrPermission}
		}
		return os.Stat(p)
	})
	res, err := svc.CheckMissingFiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Flagged != 0 || res.Skipped != 1 || missingFlag(t, svc, ms[1].ID) {
		t.Fatalf("sweep = %+v", res)
	}
}

// The detail page keeps the flag in step with its own stat, and a new file recorded for
// the film resets it.
func TestDetailPageAndNewFileUpdateMissingFlag(t *testing.T) {
	svc, _, ms := missingFixture(t, 3)
	ctx := context.Background()
	m := ms[0]
	if err := os.Remove(m.MovieFilePath); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VersionsLive(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if !missingFlag(t, svc, m.ID) {
		t.Fatal("the detail page should flag a file it sees gone")
	}
	writeFixture(t, m.MovieFilePath)
	if _, err := svc.VersionsLive(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if missingFlag(t, svc, m.ID) {
		t.Fatal("the detail page should clear the flag once the file is back")
	}

	// Flag it again, then record a different file: the flag goes with the old record.
	if err := os.Remove(m.MovieFilePath); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CheckMissingFiles(ctx); err != nil {
		t.Fatal(err)
	}
	if !missingFlag(t, svc, m.ID) {
		t.Fatal("sweep should flag it")
	}
	if err := svc.repo.SetFile(ctx, m.ID, writeFixture(t, filepath.Join(filepath.Dir(m.MovieFilePath), "new.mkv"))); err != nil {
		t.Fatal(err)
	}
	if missingFlag(t, svc, m.ID) {
		t.Fatal("a newly recorded file must reset the missing flag")
	}
}

// A sweep cut short decides nothing.
func TestCheckMissingFilesCancelledWritesNothing(t *testing.T) {
	svc, _, ms := missingFixture(t, 2)
	if err := os.Remove(ms[0].MovieFilePath); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.CheckMissingFiles(ctx); err == nil {
		t.Fatal("want the context error")
	}
	if missingFlag(t, svc, ms[0].ID) {
		t.Fatal("a cancelled sweep must not flag")
	}
}

func TestMissingThreshold(t *testing.T) {
	for checked, want := range map[int]int{0: 20, 100: 20, 400: 20, 1000: 50, 4000: 200} {
		if got := missingThreshold(checked); got != want {
			t.Errorf("missingThreshold(%d) = %d, want %d", checked, got, want)
		}
	}
}
