package store

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

// 0305 spreads the books an older build had given up on across their ladder wait, so the
// first sweep after the update doesn't find them all due at once. Books still inside
// their wait, unmonitored ones and ones with fewer than two misses are left alone.
func TestBookSearchSpread(t *testing.T) {
	ctx := context.Background()
	db := testDB(t, 1)
	all := embeddedMigrations()
	names, err := listMigrations(all)
	if err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	for _, n := range names {
		if n >= "0305" {
			break
		}
		body, err := fs.ReadFile(all, n)
		if err != nil {
			t.Fatal(err)
		}
		before[n] = &fstest.MapFile{Data: body}
	}
	if err := runMigrations(ctx, db, before); err != nil {
		t.Fatalf("migrate to 0304: %v", err)
	}
	for _, q := range []string{
		// Given up on long ago by the two-tries sweep.
		`INSERT INTO books (id, ol_key, title, monitored, search_misses, last_search_at) VALUES (1, 'OL1W', 'A', 1, 2, '2026-01-01 00:00:00')`,
		`INSERT INTO books (id, ol_key, title, monitored, search_misses, last_search_at) VALUES (3, 'OL3W', 'C', 1, 2, '2025-06-01 10:00:00')`,
		// Still inside its 3-day wait.
		`INSERT INTO books (id, ol_key, title, monitored, search_misses, last_search_at) VALUES (2, 'OL2W', 'B', 1, 2, datetime('now', '-1 hours'))`,
		// Not wanted, or not yet backed off.
		`INSERT INTO books (id, ol_key, title, monitored, search_misses, last_search_at) VALUES (4, 'OL4W', 'D', 0, 5, '2026-01-01 00:00:00')`,
		`INSERT INTO books (id, ol_key, title, monitored, search_misses, last_search_at) VALUES (5, 'OL5W', 'E', 1, 1, '2026-01-01 00:00:00')`,
		`INSERT INTO books (id, ol_key, title, monitored, search_misses, last_search_at) VALUES (6, 'OL6W', 'F', 1, 0, NULL)`,
		// Weekly and monthly steps, one with no stamp at all.
		`INSERT INTO books (id, ol_key, title, monitored, search_misses, last_search_at) VALUES (13, 'OL13W', 'G', 1, 4, NULL)`,
		`INSERT INTO books (id, ol_key, title, monitored, search_misses, last_search_at) VALUES (47, 'OL47W', 'H', 1, 12, '2025-01-01 00:00:00')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := runMigrations(ctx, db, all); err != nil {
		t.Fatalf("migrate the rest: %v", err)
	}

	// Days back from now each spread book was stamped: id % its wait in days.
	spread := map[int64]int{1: 1, 3: 0, 13: 6, 47: 17}
	for id, days := range spread {
		var got int
		if err := db.QueryRow(`SELECT CAST(ROUND(julianday('now') - julianday(last_search_at)) AS INTEGER) FROM books WHERE id = ?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != days {
			t.Errorf("book %d: last search %d days ago, want %d", id, got, days)
		}
	}
	untouched := map[int64]string{4: "2026-01-01 00:00:00", 5: "2026-01-01 00:00:00", 6: ""}
	for id, want := range untouched {
		var got string
		if err := db.QueryRow(`SELECT COALESCE(last_search_at, '') FROM books WHERE id = ?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("book %d: last_search_at = %q, want it left at %q", id, got, want)
		}
	}
	var recent int
	if err := db.QueryRow(`SELECT COUNT(*) FROM books WHERE id = 2 AND last_search_at > datetime('now', '-2 hours')`).Scan(&recent); err != nil || recent != 1 {
		t.Errorf("a book still inside its wait was moved (err %v)", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM books WHERE search_misses = 2 AND id IN (1, 3)`); n != 2 {
		t.Errorf("miss counts changed: %d of 2 kept", n)
	}
}
