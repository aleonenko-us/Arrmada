package series

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/store"
)

// stopDuringAdd is shelfMeta that stops the scan (as a Cancel or the time budget would)
// while the first show is being added.
type stopDuringAdd struct {
	shelfMeta
	stop func()
}

func (m stopDuringAdd) GetSeries(ctx context.Context, id int) (*metadata.SeriesDetails, error) {
	m.stop()
	return m.shelfMeta.GetSeries(ctx, id)
}

// Issue #22: a scan stopped part-way keeps what it finished and touches nothing after.
// The show it was adding when the stop came is finished with its episode files (a show
// added without them would be skipped by every later scan as "already in the library"),
// the shows after it aren't added, and the needs-review list isn't replaced by a partial one.
func TestSeriesScanStoppedPartWayLeavesNoHalfState(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	tv := t.TempDir()
	show := func(id int, title string) metadata.SeriesDetails {
		return metadata.SeriesDetails{SeriesResult: metadata.SeriesResult{TMDBID: id, Title: title, Year: 2020}, Seasons: listing(2)}
	}
	shelf := shelfMeta{shows: map[int]metadata.SeriesDetails{1: show(1, "Alpha Show"), 2: show(2, "Beta Show")}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// A full scan first, with one folder nobody can identify: it's on the review list.
	touchEpisode(t, filepath.Join(tv, "Unknown Thing", "Season 01", "Unknown Thing - S01E01.mkv"))
	first := NewService(st.DB(), shelfMeta{shows: map[int]metadata.SeriesDetails{}}, "", log)
	first.SetRootFunc(func() string { return tv })
	if _, err := first.ScanLibrary(context.Background(), "", nil); err != nil {
		t.Fatal(err)
	}

	touchEpisode(t, filepath.Join(tv, "Alpha Show (2020)", "Season 01", "Alpha Show - S01E01.mkv"))
	touchEpisode(t, filepath.Join(tv, "Beta Show (2020)", "Season 01", "Beta Show - S01E02.mkv"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := NewService(st.DB(), stopDuringAdd{shelf, cancel}, "", log)
	svc.SetRootFunc(func() string { return tv })

	_, err = svc.ScanLibrary(ctx, "", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("scan = %v, want it stopped (context.Canceled)", err)
	}
	alpha, err := svc.GetByTMDB(context.Background(), 1)
	if err != nil {
		t.Fatalf("the show being added when the scan stopped wasn't finished: %v", err)
	}
	if p := mustFile(t, svc, context.Background(), alpha.ID, 1, 1); p == "" {
		t.Error("the show was added without the episode file the scan found")
	}
	if _, err := svc.GetByTMDB(context.Background(), 2); err == nil {
		t.Error("a show after the stop was added")
	}
	if got, _ := svc.LastUnmatched(context.Background()); len(got) != 1 || got[0].Folder != "Unknown Thing" {
		t.Errorf("needs-review list = %+v, want the full scan's, untouched", got)
	}
}
