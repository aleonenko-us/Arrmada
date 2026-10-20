package series

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/store"
)

// The library scan marks only the files a rescan would read: adding "Star Trek" from
// tv/Star Trek never claims the untracked TNG episodes in a folder of their own inside it,
// nor a featurette in its Extras.
func TestScanLibraryReadsOnlyTheShowsOwnFiles(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	tv := t.TempDir()
	meta := shelfMeta{shows: map[int]metadata.SeriesDetails{
		1: {SeriesResult: metadata.SeriesResult{TMDBID: 1, Title: "Star Trek", Year: 1966}, Seasons: listing(3, 3)},
	}}
	svc := NewService(st.DB(), meta, tv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	own := filepath.Join(tv, "Star Trek", "Season 1", "Star Trek - S01E01.mkv")
	touchEpisode(t, own)
	touchEpisode(t, filepath.Join(tv, "Star Trek", "Star Trek The Next Generation", "Season 1", "Star Trek The Next Generation - S01E02.mkv"))
	touchEpisode(t, filepath.Join(tv, "Star Trek", "Star Trek The Next Generation", "Season 1", "S01E03.mkv"))
	touchEpisode(t, filepath.Join(tv, "Star Trek", "Extras", "Star Trek - S02E01 - Behind the Scenes.mkv"))
	res, err := svc.ScanLibrary(context.Background(), "", nil)
	if err != nil || res.Imported != 1 {
		t.Fatalf("scan = %+v, %v", res, err)
	}
	s, err := svc.GetByTMDB(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if cur := svc.CurrentEpisodeFile(context.Background(), s.ID, 1, 1); cur.Path != own {
		t.Errorf("S01E01 = %q, want the show's own", cur.Path)
	}
	for _, ep := range [][2]int{{1, 2}, {1, 3}, {2, 1}} {
		if cur := svc.CurrentEpisodeFile(context.Background(), s.ID, ep[0], ep[1]); cur.Path != "" {
			t.Errorf("S%02dE%02d = %q: claimed another show's file or a featurette", ep[0], ep[1], cur.Path)
		}
	}
}
