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

// reportMeta answers searches from a fixed table, the way TMDB answers for the owner's NAS.
type reportMeta struct {
	search map[string][]metadata.SeriesResult
	shows  map[int]metadata.SeriesDetails
}

func (m reportMeta) Available() bool { return true }
func (m reportMeta) SearchSeries(_ context.Context, q string) ([]metadata.SeriesResult, error) {
	return m.search[strings.ToLower(q)], nil
}
func (m reportMeta) GetSeries(_ context.Context, id int) (*metadata.SeriesDetails, error) {
	d := m.shows[id]
	return &d, nil
}

func TestScanReportsEveryFolderItDidNotImport(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	tv := t.TempDir()
	uk := metadata.SeriesResult{TMDBID: 2996, Title: "The Office", Year: 2001, OriginCountry: []string{"GB"}}
	us := metadata.SeriesResult{TMDBID: 2316, Title: "The Office", Year: 2005, OriginCountry: []string{"US"}}
	looney := metadata.SeriesResult{TMDBID: 9000, Title: "Looney Tunes", Year: 1930, OriginCountry: []string{"US"}}
	dated := metadata.SeriesResult{TMDBID: 9001, Title: "Dated Show", Year: 1990}
	meta := reportMeta{
		search: map[string][]metadata.SeriesResult{"the office": {uk, us}, "looney tunes": {looney}, "dated show": {dated}},
		shows: map[int]metadata.SeriesDetails{
			2316: {SeriesResult: us, Seasons: listing(3)},
			9001: {SeriesResult: dated, Seasons: aired(listing(2), "1990")},
			9000: {SeriesResult: looney, Seasons: aired(listing(2, 1), "1931", "1932")}, // numbered 1, 2 — not by year
		},
	}
	touchEpisode(t, filepath.Join(tv, "The Office (US)", "Season 01", "The Office (US) - S01E01.mkv"))
	touchEpisode(t, filepath.Join(tv, "Looney Tunes", "Season 1931", "Looney Tunes - S1931E01 - Lady Play Your Mandolin.mkv"))
	touchEpisode(t, filepath.Join(tv, "Looney Tunes", "Season 1932", "Looney Tunes - S1932E01 - Crosby, Columbo and Vallee.mkv"))
	touchEpisode(t, filepath.Join(tv, "Dated Show", "Season 1945", "Dated Show - S1945E01.mkv"))
	touchEpisode(t, filepath.Join(tv, "Zzyzx Unknown", "Zzyzx - S01E01.mkv"))
	touchEpisode(t, filepath.Join(tv, "Empty Folder", "notes.txt"))

	svc := NewService(st.DB(), meta, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetRootFunc(func() string { return tv })
	res, err := svc.ScanLibrary(context.Background(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 3 {
		t.Fatalf("imported %d, want 3", res.Imported)
	}
	// The US show, not the UK one.
	if _, err := svc.GetByTMDB(context.Background(), 2316); err != nil {
		t.Errorf("The Office (US) folder wasn't imported as the US show: %v", err)
	}
	if _, err := svc.GetByTMDB(context.Background(), 2996); err == nil {
		t.Error("The Office (US) folder became the UK show")
	}
	got := map[string]UnmatchedFolder{}
	for _, u := range res.Unmatched {
		got[u.Folder] = u
	}
	if len(got) != 3 {
		t.Fatalf("needs-you folders = %+v, want Dated Show, Zzyzx Unknown and Empty Folder", res.Unmatched)
	}
	// Looney Tunes' year-numbered seasons map onto the episodes that aired in those years.
	lt, err := svc.GetByTMDB(context.Background(), 9000)
	if err != nil {
		t.Fatal(err)
	}
	if p := mustFile(t, svc, context.Background(), lt.ID, 1, 1); !strings.Contains(p, "S1931E01") {
		t.Errorf("S1931E01 landed on %q, want the first 1931 episode (TMDB S1E1)", p)
	}
	if p := mustFile(t, svc, context.Background(), lt.ID, 2, 1); !strings.Contains(p, "S1932E01") {
		t.Errorf("S1932E01 landed on %q, want TMDB S2E1", p)
	}
	ds := got["Dated Show"]
	if ds.Reason != ReasonNoEpisodes || ds.SeriesID == 0 || !strings.Contains(ds.Detail, "numbered by year") {
		t.Errorf("Dated Show = %+v, want no_episodes naming the year numbering", ds)
	}
	if got["Zzyzx Unknown"].Reason != ReasonNoResults || got["Empty Folder"].Reason != ReasonNoVideo {
		t.Errorf("reasons: %+v", got)
	}
	for _, u := range got {
		if u.Detail == "" {
			t.Errorf("%s has no plain-language reason", u.Folder)
		}
	}

	// The list survives a restart (a new service on the same database), and a rescan that
	// finds the folder fixed clears it.
	again := NewService(st.DB(), meta, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	again.SetRootFunc(func() string { return tv })
	list, err := again.LastUnmatched(context.Background())
	if err != nil || len(list) != 3 {
		t.Fatalf("after restart: %+v, %v; want 3", list, err)
	}
	touchEpisode(t, filepath.Join(tv, "Empty Folder", "Empty - S01E01.mkv"))
	if _, err := again.ScanLibrary(context.Background(), "", nil); err != nil {
		t.Fatal(err)
	}
	list, _ = again.LastUnmatched(context.Background())
	for _, u := range list {
		if u.Folder == "Empty Folder" && u.Reason == ReasonNoVideo {
			t.Errorf("Empty Folder still says it has no video: %+v", u)
		}
	}
}

func TestStripCountryTag(t *testing.T) {
	for in, want := range map[string][2]string{
		"The Office (US)": {"The Office", "US"}, "Shameless [UK]": {"Shameless", "GB"}, "Doctor Who (2005)": {"", ""}, "Heroes (xx)": {"", ""},
	} {
		base, c, ok := stripCountryTag(in)
		if (want[0] != "") != ok || base != want[0] || c != want[1] {
			t.Errorf("%q -> %q %q %v", in, base, c, ok)
		}
	}
}

// aired stamps each season's episodes with an air date in the given year, in order.
func aired(seasons []metadata.SeasonDetails, years ...string) []metadata.SeasonDetails {
	for i := range seasons {
		for j := range seasons[i].Episodes {
			seasons[i].Episodes[j].AirDate = years[i] + "-01-01"
		}
	}
	return seasons
}
