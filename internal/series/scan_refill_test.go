package series

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/store"
)

// The owner's NAS: an earlier build's scan added Looney Tunes without a single file (it
// couldn't read "Season 1954" then), and every scan after skipped it as already in the
// library, so it stayed empty. A scan now reads the folder of a show that has no files.
// The Office (US) folder, named the way the release group named its files, is the US show.
func TestScanFillsInAShowAddedWithoutFiles(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	tv := t.TempDir()
	uk := metadata.SeriesResult{TMDBID: 2996, Title: "The Office", Year: 2001, OriginCountry: []string{"GB"}}
	us := metadata.SeriesResult{TMDBID: 2316, Title: "The Office", Year: 2005, OriginCountry: []string{"US"}}
	looney := metadata.SeriesResult{TMDBID: 333432, Title: "Looney Tunes", Year: 1929, OriginCountry: []string{"US"}}
	meta := reportMeta{
		search: map[string][]metadata.SeriesResult{"the office": {uk, us}, "looney tunes": {looney}},
		shows: map[int]metadata.SeriesDetails{
			2316:   {SeriesResult: us, Seasons: listing(9, 9, 9, 9, 9, 9, 22)},
			333432: {SeriesResult: looney, Seasons: aired(listing(1, 4), "1929", "1954")}, // seasons 1, 2 — aired 1929, 1954
		},
	}
	svc := NewService(st.DB(), meta, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetRootFunc(func() string { return tv })

	// What the old scan left: the show, with none of its files.
	lt, err := svc.AddWith(ctx, 333432, "n/a", AddOptions{Monitored: false, Preset: PresetNone})
	if err != nil {
		t.Fatal(err)
	}
	show := filepath.Join(tv, "Looney Tunes", "Season 1954")
	touchEpisode(t, filepath.Join(show, "Looney.Tunes.S1954E01.Dog.Pounded.720p.BluRay.h264.AAC-MaG-Chamele0n.mp4"))
	touchEpisode(t, filepath.Join(show, "Looney.Tunes.S1954E04.Feline.Frame-Up.1080p.BluRay.AC3.x265.mkv"))
	office := filepath.Join(tv, "The Office (US)", "Season 7")
	touchEpisode(t, filepath.Join(office, "The.Office.S07E01.Nepotism.AAC5.1.1080p.Bluray.x265-SiQ.mkv"))
	touchEpisode(t, filepath.Join(office, "The.Office.S07E22.Goodbye.Michael.AAC5.1.1080p.Bluray.x265-SiQ.mkv"))

	res, err := svc.ScanLibrary(ctx, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 2 || len(res.Unmatched) != 0 {
		t.Fatalf("imported %d, unmatched %+v; want both shows and nothing needing you", res.Imported, res.Unmatched)
	}
	if p := mustFile(t, svc, ctx, lt.ID, 2, 1); !strings.Contains(p, "S1954E01") {
		t.Errorf("S1954E01 landed on %q, want the first 1954 episode (TMDB S2E1)", p)
	}
	if p := mustFile(t, svc, ctx, lt.ID, 2, 4); !strings.Contains(p, "S1954E04") {
		t.Errorf("S1954E04 landed on %q, want TMDB S2E4", p)
	}
	of, err := svc.GetByTMDB(ctx, 2316)
	if err != nil {
		t.Fatalf("The Office (US) wasn't added as the US show: %v", err)
	}
	if p := mustFile(t, svc, ctx, of.ID, 7, 22); !strings.Contains(p, "S07E22") {
		t.Errorf("S07E22 landed on %q", p)
	}

	// A show that has its files is left alone by the next scan.
	res, err = svc.ScanLibrary(ctx, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 0 || res.Skipped != 2 {
		t.Errorf("second scan: imported %d, skipped %d; want 0 and 2", res.Imported, res.Skipped)
	}
}
