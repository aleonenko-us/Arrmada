package movies

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

// stoppingMeta stops the scan (as a Cancel or the time budget would) at a set point: while
// a film is being added (GetMovie), or during one folder's lookup.
type stoppingMeta struct {
	fakeMeta
	stop       func()
	stopSearch string // a title whose lookup is cut short; "" stops during the add
}

func (m stoppingMeta) SearchMovie(ctx context.Context, q string) ([]metadata.MovieResult, error) {
	if q == m.stopSearch {
		m.stop()
		return nil, ctx.Err()
	}
	return m.fakeMeta.SearchMovie(ctx, q)
}

func (m stoppingMeta) GetMovie(ctx context.Context, id int) (*metadata.MovieDetails, error) {
	if m.stopSearch == "" {
		m.stop()
	}
	return m.fakeMeta.GetMovie(ctx, id)
}

// Issue #22: a scan stopped part-way leaves no half-state. The film being added when the
// stop came is finished with its file; nothing after it is added; a folder whose lookup
// was cut short isn't filed as needing review; and nothing is taken off a film.
func TestStoppedScanFinishesTheFolderItWasOn(t *testing.T) {
	svc, _, root := scanFixture(t, "Movie (2010)/Movie.2010.mkv", "Nope (2001)/Nope.mkv", "Other (2020)/Other.2020.mkv")
	ctx := context.Background()
	plain := svc.meta.(fakeMeta)

	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	svc.meta = stoppingMeta{fakeMeta: plain, stop: cancel}
	if _, err := svc.ScanLibrary(cctx, "", ScanOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("scan = %v, want it stopped", err)
	}
	m, err := svc.GetByTMDB(ctx, 10)
	if err != nil || !m.HasFile || filepath.Clean(m.MovieFilePath) != filepath.Join(root, "Movie (2010)", "Movie.2010.mkv") {
		t.Fatalf("the film being added when the scan stopped = %+v, %v; want it with its file", m, err)
	}
	if _, err := svc.GetByTMDB(ctx, 20); err == nil {
		t.Error("a film after the stop was added")
	}
	if list, _ := svc.LastUnmatched(ctx); len(list) != 0 {
		t.Errorf("needs review = %+v, want nothing (the scan never looked at Nope)", list)
	}

	// Stopped during a lookup: that folder isn't "unrecognised", it just wasn't finished.
	cctx2, cancel2 := context.WithCancel(ctx)
	defer cancel2()
	svc.meta = stoppingMeta{fakeMeta: plain, stop: cancel2, stopSearch: "Nope"}
	if _, err := svc.ScanLibrary(cctx2, "", ScanOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("scan = %v, want it stopped", err)
	}
	if list, _ := svc.LastUnmatched(ctx); len(list) != 0 {
		t.Errorf("needs review = %+v, want nothing: Nope's lookup was cut short", list)
	}
	if m, _ := svc.GetByTMDB(ctx, 10); !m.HasFile {
		t.Error("the stopped scan took the file off a film it had found")
	}
}
