package requests

import (
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/metadata"
)

// BOOK-18: a request for a book that isn't out yet reads "Not out yet", with the release
// day as the next check and the date itself for "Out 12 Mar", instead of "Searching".
// Once the day has passed it is an ordinary search again.
func TestTrackBookNotOutYet(t *testing.T) {
	s, repo, db, ctx := bookLinkFixture(t, catalogue{byKey: map[string]metadata.BookResult{}})
	date := time.Now().AddDate(0, 2, 0).Format("2006-01-02")
	b, err := repo.Create(ctx, books.Book{OLKey: "hc:1", Title: "Wind and Truth", Author: "B", Monitored: true, ReleaseDate: date})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.repo.Create(ctx, Request{MediaType: "book", OLKey: "hc:1", BookID: b.ID, Title: "Wind and Truth", Status: StatusApproved, RequestedBy: 7}); err != nil {
		t.Fatal(err)
	}
	track := func() *Tracking {
		t.Helper()
		reqs, _, err := s.List(ctx, ListFilter{UserID: 7})
		if err != nil || len(reqs) != 1 {
			t.Fatalf("list: %v (%d)", err, len(reqs))
		}
		s.Track(ctx, reqs, nil, true)
		return reqs[0].Tracking
	}
	want := books.ReleaseDay(date).UTC().Format(time.RFC3339)
	if tr := track(); tr == nil || tr.Stage != StageSearching || tr.Note != "Not out yet" || tr.NextCheckAt != want || tr.ReleaseDate != date {
		t.Errorf("upcoming tracking = %+v, want Not out yet / next check %s / release %s", tr, want, date)
	}

	// Out (the date has passed): plain searching, no release date.
	if _, err := db.Exec(`UPDATE books SET release_date = ? WHERE id = ?`, time.Now().AddDate(0, 0, -1).Format("2006-01-02"), b.ID); err != nil {
		t.Fatal(err)
	}
	if tr := track(); tr == nil || tr.Note != "" || tr.ReleaseDate != "" {
		t.Errorf("released tracking = %+v, want plain searching", tr)
	}
}
