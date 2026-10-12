package automation

import (
	"testing"
	"time"
)

func setReleaseDate(t *testing.T, c *Coordinator, id int64, date string) {
	t.Helper()
	if _, err := c.db.Exec(`UPDATE books SET release_date = ? WHERE id = ?`, date, id); err != nil {
		t.Fatal(err)
	}
}

// An upcoming book isn't searched before its release day; on release day the misses it
// piled up before it existed are reset and it is searched; for two weeks it waits at most
// a day between searches; then the ordinary ladder takes over. [BOOK-18]
func TestBookSweepReleaseDay(t *testing.T) {
	// Release day is a calendar day in the server's zone, so the clock is too.
	release := time.Date(2027, 3, 12, 0, 0, 0, 0, time.Local)
	clock := release.Add(-24 * time.Hour).Add(9 * time.Hour) // the day before, 9am
	c, bk, asked, ctx := emptyBookCoord(t, clock)
	c.now = func() time.Time { return clock }
	b := addBooks(t, bk, ctx, 1)[0]
	setReleaseDate(t, c, b.ID, "2027-03-12")

	// Never searched, but not out: nothing.
	c.SearchBooksMissing(ctx)
	if q := asked(); len(q) != 0 {
		t.Fatalf("searched the day before release: %v", q)
	}

	// The add-time search of an unreleased book never records a miss.
	if _, err := c.SearchBookNow(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	asked()
	if _, misses := bk.SearchState(ctx, b.ID); misses != 0 {
		t.Errorf("add-time search recorded %d misses", misses)
	}

	// Searched (by hand) a week before release and missed 3 times since: on release day
	// the misses go and it is searched, though the ladder alone would wait a week.
	setSearchState(t, c, b.ID, release.Add(-7*24*time.Hour), 3)
	clock = release.Add(6 * time.Hour)
	c.SearchBooksMissing(ctx)
	if q := asked(); len(q) != 1 {
		t.Fatalf("release day: asked %v, want one search", q)
	}
	if _, misses := bk.SearchState(ctx, b.ID); misses != 1 {
		t.Errorf("after the release-day search misses = %d, want 1 (reset, then this miss)", misses)
	}

	// Within the window: misses 4 would mean a week on the ladder; it is searched again
	// a day later, not before.
	setSearchState(t, c, b.ID, release.Add(6*time.Hour), 4)
	clock = release.Add(29 * time.Hour) // 23h after the last search
	c.SearchBooksMissing(ctx)
	if q := asked(); len(q) != 0 {
		t.Errorf("23h after the last search: %v", q)
	}
	clock = release.Add(31 * time.Hour)
	c.SearchBooksMissing(ctx)
	if q := asked(); len(q) != 1 {
		t.Errorf("25h after the last search, inside the window: asked %v, want one", q)
	}
	if _, misses := bk.SearchState(ctx, b.ID); misses != 5 {
		t.Errorf("inside the window misses = %d, want 5 (no reset once searched since release)", misses)
	}

	// After the window: the ladder again (5 misses = weekly).
	last := release.Add(20 * 24 * time.Hour)
	setSearchState(t, c, b.ID, last, 5)
	clock = last.Add(2 * 24 * time.Hour)
	c.SearchBooksMissing(ctx)
	if q := asked(); len(q) != 0 {
		t.Errorf("after the window, two days on: %v (want the weekly ladder)", q)
	}
}
