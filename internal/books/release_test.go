package books

import (
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

// The release date round-trips through every write path: create, refresh (which keeps a
// stored date when the catalogue has none), re-match, and bad input is stored as none.
// [BOOK-18]
func TestReleaseDateRoundTrip(t *testing.T) {
	repo, ctx := historyRepo(t)
	b, err := repo.Create(ctx, Book{OLKey: "hc:1", Title: "Wind and Truth", Author: "B", ReleaseDate: "2027-03-12"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.Get(ctx, b.ID); got.ReleaseDate != "2027-03-12" {
		t.Fatalf("created with %q", got.ReleaseDate)
	}
	if err := repo.SetReleaseDate(ctx, b.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.Get(ctx, b.ID); got.ReleaseDate != "2027-03-12" {
		t.Errorf("an unknown date erased the stored one: %q", got.ReleaseDate)
	}
	if err := repo.SetReleaseDate(ctx, b.ID, "2027-04-01"); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.Get(ctx, b.ID); got.ReleaseDate != "2027-04-01" {
		t.Errorf("moved date = %q", got.ReleaseDate)
	}
	bad, err := repo.Create(ctx, Book{OLKey: "hc:2", Title: "Other", Author: "B", ReleaseDate: "soon"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.Get(ctx, bad.ID); got.ReleaseDate != "" {
		t.Errorf("garbage stored as %q", got.ReleaseDate)
	}
	if err := repo.Rematch(ctx, b.ID, Book{OLKey: "hc:3", Title: "Wind and Truth", Author: "B", ReleaseDate: "2027-05-05"}, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.Get(ctx, b.ID); got.ReleaseDate != "2027-05-05" {
		t.Errorf("after re-match = %q", got.ReleaseDate)
	}
}

// Refresh, Add and AddWorks take the catalogue's date.
func TestReleaseDateFromCatalogue(t *testing.T) {
	cat := &refreshCatalogue{books: map[string]*metadata.BookDetails{
		"hc:9": {BookResult: metadata.BookResult{Key: "hc:9", Title: "Upcoming", Author: "A", ReleaseDate: "2027-01-02"}},
	}}
	s, ctx := refreshService(t, cat)
	b, err := s.Add(ctx, "hc:9", "", true, metadata.BookResult{})
	if err != nil {
		t.Fatal(err)
	}
	if b.ReleaseDate != "2027-01-02" {
		t.Errorf("Add stored %q", b.ReleaseDate)
	}
	added, _ := s.AddWorks(ctx, []metadata.BookResult{{Key: "hc:10", Title: "Listed", Author: "A", ReleaseDate: "2027-06-07"}}, "", true)
	if len(added) != 1 || added[0].ReleaseDate != "2027-06-07" {
		t.Fatalf("AddWorks = %+v", added)
	}
	cat.books["hc:10"] = &metadata.BookDetails{BookResult: metadata.BookResult{Key: "hc:10", Title: "Listed", Author: "A", ReleaseDate: "2027-06-09"}}
	if got, _ := s.Refresh(ctx, added[0].ID); got.ReleaseDate != "2027-06-09" {
		t.Errorf("Refresh kept %q, want the catalogue's new date", got.ReleaseDate)
	}
}

// The release-aware ladder: release day for an upcoming book, now once it is out and
// unsearched since, a day's cap inside the window, the ladder outside it.
func TestNextSearchAtFor(t *testing.T) {
	release := time.Date(2027, 3, 12, 0, 0, 0, 0, time.Local)
	stamp := func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05") }
	date := "2027-03-12"
	cases := []struct {
		name   string
		last   string
		misses int
		date   string
		now    time.Time
		want   time.Time
	}{
		{"upcoming, never searched", "", 0, date, release.Add(-48 * time.Hour), release},
		{"upcoming, searched", stamp(release.Add(-72 * time.Hour)), 2, date, release.Add(-time.Hour), release},
		{"release day, searched before", stamp(release.Add(-24 * time.Hour)), 9, date, release.Add(time.Hour), time.Time{}},
		{"release day, never searched", "", 0, date, release.Add(time.Hour), time.Time{}},
		{"window caps the ladder", stamp(release.Add(2 * time.Hour)), 4, date, release.Add(3 * time.Hour), release.Add(26 * time.Hour)},
		{"window keeps a shorter wait", stamp(release.Add(2 * time.Hour)), 0, date, release.Add(3 * time.Hour), time.Time{}},
		{"after the window: ladder", stamp(release.Add(15 * 24 * time.Hour)), 4, date, release.Add(16 * 24 * time.Hour), release.Add(22 * 24 * time.Hour)},
		{"no date: ladder", stamp(release), 1, "", release.Add(time.Hour), release.Add(24 * time.Hour)},
		{"bad date: ladder", stamp(release), 1, "2027", release.Add(time.Hour), release.Add(24 * time.Hour)},
	}
	for _, tc := range cases {
		got := NextSearchAtFor(tc.last, tc.misses, tc.date, tc.now)
		if !got.Equal(tc.want) {
			t.Errorf("%s: next = %v, want %v", tc.name, got, tc.want)
		}
	}
	if Released(date, release.Add(-time.Second)) || !Released(date, release) || !Released("", release) {
		t.Error("Released is wrong around the release instant")
	}
}

// A book's own next-search time (the detail page, My shelf) follows the release date.
func TestFillNextSearchUpcoming(t *testing.T) {
	future := time.Now().AddDate(0, 0, 30).Format("2006-01-02")
	b := Book{Monitored: true, WantEbook: true, ReleaseDate: future}
	b.FillNextSearch()
	if want := ReleaseDay(future).UTC().Format(time.RFC3339); b.NextSearchAt != want {
		t.Errorf("next search = %q, want the release day %q", b.NextSearchAt, want)
	}
}
