package books

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/store"
)

// refreshCatalogue answers GetBook from a map and reports a settable Hardcover usage.
type refreshCatalogue struct {
	stubCatalogue
	books        map[string]*metadata.BookDetails
	fail         map[string]error
	used, budget int
	asked        []string
	// spendPerCall raises used on every lookup, so a run can cross the gate part way.
	spendPerCall int
}

func (c *refreshCatalogue) GetBook(_ context.Context, key string) (*metadata.BookDetails, error) {
	c.asked = append(c.asked, key)
	c.used += c.spendPerCall
	if err := c.fail[key]; err != nil {
		return nil, err
	}
	if d, ok := c.books[key]; ok {
		return d, nil
	}
	return nil, errors.New("not found")
}

func (c *refreshCatalogue) HardcoverUsage() (int, int) { return c.used, c.budget }

func refreshService(t *testing.T, cat *refreshCatalogue) (*Service, context.Context) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewService(st.DB(), cat, slog.New(slog.NewTextHandler(io.Discard, nil))), context.Background()
}

func checkedAt(t *testing.T, s *Service, id int64) string {
	t.Helper()
	var v string
	if err := s.repo.db.QueryRow(`SELECT meta_checked_at FROM books WHERE id = ?`, id).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// RefreshDue takes never-checked books first, then stale ones, oldest first; respects the
// limit; fills in what the catalogue knows; and stamps each book it looked up. [BOOK-17]
func TestRefreshDueOrderLimitAndStamp(t *testing.T) {
	cat := &refreshCatalogue{books: map[string]*metadata.BookDetails{}}
	s, ctx := refreshService(t, cat)
	added, _ := s.AddWorks(ctx, []metadata.BookResult{
		{Key: "hc:1", Title: "Fresh", Author: "A"},
		{Key: "hc:2", Title: "Stale", Author: "A"},
		{Key: "hc:3", Title: "Never One", Author: "A"},
		{Key: "hc:4", Title: "Never Two", Author: "A"},
		{Key: "hc:5", Title: "Older Stale", Author: "A"},
	}, "", true)
	if len(added) != 5 {
		t.Fatalf("seeded %d", len(added))
	}
	for _, b := range added {
		cat.books[b.OLKey] = &metadata.BookDetails{
			BookResult:  metadata.BookResult{Key: b.OLKey, Title: b.Title, Author: b.Author},
			Description: "About " + b.Title, Subjects: []string{"Fantasy"}, SeriesName: "Saga", SeriesPosition: 2,
		}
	}
	db := s.repo.db
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`UPDATE books SET meta_checked_at = datetime('now', '-1 day') WHERE id = ?`, added[0].ID)
	mustExec(`UPDATE books SET meta_checked_at = datetime('now', '-40 days') WHERE id = ?`, added[1].ID)
	mustExec(`UPDATE books SET meta_checked_at = datetime('now', '-90 days') WHERE id = ?`, added[4].ID)

	n, skipped, err := s.RefreshDue(ctx, 3)
	if err != nil || skipped != "" || n != 3 {
		t.Fatalf("RefreshDue = %d, %q, %v; want 3 refreshed", n, skipped, err)
	}
	want := []string{"hc:3", "hc:4", "hc:5"} // never-checked (by id), then the oldest stale
	if len(cat.asked) != 3 || cat.asked[0] != want[0] || cat.asked[1] != want[1] || cat.asked[2] != want[2] {
		t.Fatalf("asked %v, want %v", cat.asked, want)
	}
	got, _ := s.Get(ctx, added[2].ID)
	if got.Description != "About Never One" || len(got.Subjects) != 1 || got.SeriesName != "Saga" {
		t.Errorf("refreshed book = %+v", got)
	}
	if checkedAt(t, s, added[2].ID) == "" {
		t.Error("a refreshed book wasn't stamped")
	}

	// Next run: the 40-day-old book is due; the one checked yesterday is not.
	cat.asked = nil
	if n, _, _ := s.RefreshDue(ctx, 25); n != 1 || len(cat.asked) != 1 || cat.asked[0] != "hc:2" {
		t.Fatalf("second run refreshed %d, asked %v; want only hc:2", n, cat.asked)
	}
	cat.asked = nil
	if n, _, _ := s.RefreshDue(ctx, 25); n != 0 || len(cat.asked) != 0 {
		t.Fatalf("third run refreshed %d, asked %v; want nothing due", n, cat.asked)
	}
}

// Over 70% of the day's Hardcover budget, the job skips; crossing the gate mid-run, or a
// budget refusal, stops it without stamping the book it couldn't look up.
func TestRefreshDueBudgetGate(t *testing.T) {
	cat := &refreshCatalogue{books: map[string]*metadata.BookDetails{}, budget: 100, used: 71}
	s, ctx := refreshService(t, cat)
	added, _ := s.AddWorks(ctx, []metadata.BookResult{
		{Key: "hc:1", Title: "One", Author: "A"}, {Key: "hc:2", Title: "Two", Author: "A"},
		{Key: "hc:3", Title: "Three", Author: "A"},
	}, "", true)
	for _, b := range added {
		cat.books[b.OLKey] = &metadata.BookDetails{BookResult: metadata.BookResult{Key: b.OLKey, Title: b.Title, Author: "A"}}
	}
	if !s.HardcoverBudgetLow() {
		t.Fatal("71 of 100 should read as low")
	}
	n, skipped, err := s.RefreshDue(ctx, 25)
	if n != 0 || skipped != SkippedHardcoverBudget || err != nil || len(cat.asked) != 0 {
		t.Fatalf("over the gate: %d, %q, %v, asked %v", n, skipped, err, cat.asked)
	}

	// 70 of 100: the run starts; each lookup spends one, so it stops after the first.
	cat.used, cat.spendPerCall = 70, 1
	n, skipped, _ = s.RefreshDue(ctx, 25)
	if n != 1 || skipped != SkippedHardcoverBudget {
		t.Fatalf("crossing the gate: refreshed %d, %q; want 1 then skipped", n, skipped)
	}

	// A budget refusal from Hardcover itself stops the run, and that book stays due.
	cat.used, cat.spendPerCall = 0, 0
	cat.fail = map[string]error{"hc:2": metadata.ErrHardcoverBudget}
	cat.asked = nil
	n, skipped, _ = s.RefreshDue(ctx, 25)
	if n != 0 || skipped != SkippedHardcoverBudget || len(cat.asked) != 1 {
		t.Fatalf("budget refusal: %d, %q, asked %v", n, skipped, cat.asked)
	}
	if checkedAt(t, s, added[1].ID) != "" {
		t.Error("a book refused for budget must stay never-checked")
	}

	// No Hardcover (budget 0): never low.
	cat.budget, cat.used = 0, 999
	if s.HardcoverBudgetLow() {
		t.Error("budget 0 means no Hardcover — nothing to hold back for")
	}
}

// A key the catalogue no longer knows is stamped anyway, so it can't sit at the front of
// the queue and starve every other book.
func TestRefreshDueStampsFailedLookups(t *testing.T) {
	cat := &refreshCatalogue{books: map[string]*metadata.BookDetails{}}
	s, ctx := refreshService(t, cat)
	added, _ := s.AddWorks(ctx, []metadata.BookResult{{Key: "OL1W", Title: "Gone", Author: "A"}}, "", true)
	if n, skipped, err := s.RefreshDue(ctx, 25); n != 0 || skipped != "" || err != nil {
		t.Fatalf("RefreshDue = %d, %q, %v", n, skipped, err)
	}
	if checkedAt(t, s, added[0].ID) == "" {
		t.Error("a failed lookup must still be stamped")
	}
}

// Books added in bulk are refreshed before their first search; the gate stops it.
func TestRefreshBooks(t *testing.T) {
	cat := &refreshCatalogue{books: map[string]*metadata.BookDetails{}, budget: 100}
	s, ctx := refreshService(t, cat)
	added, _ := s.AddWorks(ctx, []metadata.BookResult{
		{Key: "hc:1", Title: "One", Author: "Wrong Name"}, {Key: "hc:2", Title: "Two", Author: "A"},
	}, "", true)
	cat.books["hc:1"] = &metadata.BookDetails{BookResult: metadata.BookResult{Key: "hc:1", Title: "One", Author: "Right Name"}}
	cat.books["hc:2"] = &metadata.BookDetails{BookResult: metadata.BookResult{Key: "hc:2", Title: "Two", Author: "A"}}
	if n := s.RefreshBooks(ctx, []int64{added[0].ID, added[1].ID}); n != 2 {
		t.Fatalf("refreshed %d, want 2", n)
	}
	if b, _ := s.Get(ctx, added[0].ID); b.Author != "Right Name" {
		t.Errorf("author = %q, want the catalogue's", b.Author)
	}
	cat.used, cat.asked = 90, nil
	if n := s.RefreshBooks(ctx, []int64{added[0].ID}); n != 0 || len(cat.asked) != 0 {
		t.Errorf("over the gate: refreshed %d, asked %v", n, cat.asked)
	}
}
