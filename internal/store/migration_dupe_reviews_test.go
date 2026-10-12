package store

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

// 0251 brings pairs the Hardcover re-match flagged (possible_duplicate timeline entries
// naming the other book) into book_dupe_reviews, once per pair, smaller id first.
func TestDupeReviewsBackfill(t *testing.T) {
	ctx := context.Background()
	db := testDB(t, 1)
	all := embeddedMigrations()
	names, err := listMigrations(all)
	if err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	for _, n := range names {
		if n >= "0251" {
			break
		}
		body, err := fs.ReadFile(all, n)
		if err != nil {
			t.Fatal(err)
		}
		before[n] = &fstest.MapFile{Data: body}
	}
	if err := runMigrations(ctx, db, before); err != nil {
		t.Fatalf("migrate to 0250: %v", err)
	}
	for _, q := range []string{
		`INSERT INTO books (id, ol_key, title) VALUES (3, 'OL3W', 'Dune'), (12, 'hc:12', 'Dune'), (20, 'OL20W', 'Emma')`,
		// Both rows carry the note about the other: one pair.
		`INSERT INTO book_events (book_id, event, detail) VALUES
		   (3, 'possible_duplicate', 'Hardcover lists this as the same book as “Dune” (book 12). Not merged; review it under Possible duplicates.'),
		   (12, 'possible_duplicate', 'Hardcover lists this as the same book as “Dune” (book 3). Not merged; review it under Possible duplicates.'),
		   (20, 'possible_duplicate', 'Hardcover lists this as the same book as “Gone” (book 99). Not merged; review it under Possible duplicates.'),
		   (20, 'possible_duplicate', 'something else entirely'),
		   (20, 'merged', 'Merged duplicate entry "Emma (book 3)" (OL1W)')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := runMigrations(ctx, db, all); err != nil {
		t.Fatalf("migrate the rest: %v", err)
	}
	rows, err := db.Query(`SELECT a_id, b_id, status FROM book_dupe_reviews ORDER BY a_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var a, b int64
		var st string
		if err := rows.Scan(&a, &b, &st); err != nil {
			t.Fatal(err)
		}
		got = append(got, st)
		if a != 3 || b != 12 || st != "open" {
			t.Fatalf("pair (%d, %d) %s", a, b, st)
		}
	}
	if len(got) != 1 {
		t.Fatalf("%d pairs, want 1", len(got))
	}
}
