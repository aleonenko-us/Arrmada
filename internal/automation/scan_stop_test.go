package automation

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/store"
)

// stoppingCatalogue is a book catalogue whose first lookup stops the scan (as a Cancel or
// the time budget would) and fails with it. Anything else it's asked panics (nil embed).
type stoppingCatalogue struct {
	metadata.BookProvider
	stop func()
}

func (stoppingCatalogue) Available() bool { return true }
func (c stoppingCatalogue) SearchBooks(ctx context.Context, _ string) ([]metadata.BookResult, error) {
	c.stop()
	return nil, ctx.Err()
}

// Issue #22: a book scan that's stopped says so — an error naming how far it got — rather
// than finishing "successfully" with every folder it never looked at reported as
// unrecognised, and it adds nothing. It reports its progress as it goes.
func TestBookScanStoppedPartWay(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	root := t.TempDir()
	sparse(t, filepath.Join(root, "Author One", "First Book", "First Book.epub"), 1<<20)
	sparse(t, filepath.Join(root, "Author Two", "Second Book", "Second Book.epub"), 1<<20)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := books.NewService(st.DB(), stoppingCatalogue{stop: cancel}, slog.Default())
	c := &Coordinator{books: svc, db: st.DB(), log: slog.Default(), imp: library.NewImporter(t.TempDir(), slog.Default())}
	var progress [][2]int
	res, err := c.ScanBookLibrary(ctx, root, root, func(done, total int) { progress = append(progress, [2]int{done, total}) })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("scan = %+v, %v; want it stopped (context.Canceled)", res, err)
	}
	if len(res.Unmatched) != 0 {
		t.Errorf("unmatched = %v; a lookup that was cut short isn't an unrecognised folder", res.Unmatched)
	}
	if len(progress) == 0 || progress[0] != [2]int{0, 2} {
		t.Errorf("progress = %v, want it to start at 0 of 2 folders", progress)
	}
	if list, _ := svc.List(context.Background()); len(list) != 0 {
		t.Errorf("a stopped scan added %d books", len(list))
	}
}

// The music scan stops between album folders too, saying how far it got, and reports its
// progress (it used to run to the end after a stop, filing every folder left as unmatched).
func TestMusicScanStoppedPartWay(t *testing.T) {
	c, _ := gateTestCoord(t, true)
	root := c.imp.MusicDir()
	writeTrack(t, filepath.Join(root, "Artist One", "Album One", "01 - Song.flac"))
	writeTrack(t, filepath.Join(root, "Artist Two", "Album Two", "01 - Song.flac"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var progress [][2]int
	res, err := c.ScanMusicLibrary(ctx, func(done, total int) {
		progress = append(progress, [2]int{done, total})
		cancel() // stopped before the first folder is looked up
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("scan = %+v, %v; want it stopped (context.Canceled)", res, err)
	}
	if len(res.Unmatched) != 0 || res.Artists != 0 {
		t.Errorf("result = %+v; a stopped scan reported or added folders it never looked at", res)
	}
	if len(progress) != 1 || progress[0] != [2]int{0, 2} {
		t.Errorf("progress = %v, want [[0 2]]", progress)
	}
}
