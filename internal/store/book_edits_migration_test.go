package store

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

// 0334 adds the hand-edit layer to books: new detail columns, edits_json/edited_at, a
// narrator typed before it counted as an edit (with the files' tags, an empty value,
// underneath), and
// triggers that keep an edit through any write that isn't one.
func TestBookEditsMigration(t *testing.T) {
	ctx := context.Background()
	db := testDB(t, 1)
	all := embeddedMigrations()
	names, err := listMigrations(all)
	if err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	for _, n := range names {
		if n >= "0334" {
			break
		}
		body, err := fs.ReadFile(all, n)
		if err != nil {
			t.Fatal(err)
		}
		before[n] = &fstest.MapFile{Data: body}
	}
	if err := runMigrations(ctx, db, before); err != nil {
		t.Fatalf("migrate to 0333: %v", err)
	}
	for _, q := range []string{
		`INSERT INTO books (id, ol_key, title, author) VALUES (1, 'OL1W', 'Dune', 'Frank Herbert')`,
		`INSERT INTO books (id, ol_key, title, author, audiobook_narrator) VALUES (2, 'OL2W', 'Emma', 'Jane Austen', 'Juliet Stevenson')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := runMigrations(ctx, db, all); err != nil {
		t.Fatalf("migrate the rest: %v", err)
	}

	var edits, subtitle, language, isbn, publisher string
	var editedAt int64
	if err := db.QueryRow(`SELECT edits_json, edited_at, subtitle, language, isbn, publisher FROM books WHERE id = 1`).
		Scan(&edits, &editedAt, &subtitle, &language, &isbn, &publisher); err != nil {
		t.Fatal(err)
	}
	if edits != "{}" || editedAt != 0 || subtitle != "" || language != "" || isbn != "" || publisher != "" {
		t.Fatalf("book 1 after 0334: %q %d %q %q %q %q", edits, editedAt, subtitle, language, isbn, publisher)
	}
	if err := db.QueryRow(`SELECT edits_json FROM books WHERE id = 2`).Scan(&edits); err != nil {
		t.Fatal(err)
	}
	if edits != `{"narrator":""}` {
		t.Fatalf("a typed narrator became %q, want it marked edited", edits)
	}

	// A write that isn't an edit (edited_at unchanged) lands underneath an edited field.
	if _, err := db.Exec(`UPDATE books SET audiobook_narrator = 'Tag Reader', title = 'Emma (Penguin)' WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	var narrator, title string
	if err := db.QueryRow(`SELECT audiobook_narrator, title, edits_json FROM books WHERE id = 2`).Scan(&narrator, &title, &edits); err != nil {
		t.Fatal(err)
	}
	if narrator != "Juliet Stevenson" || title != "Emma (Penguin)" || edits != `{"narrator":"Tag Reader"}` {
		t.Fatalf("after a plain write: narrator %q, title %q, edits %s", narrator, title, edits)
	}
	// An edit (edited_at moves) writes the column.
	if _, err := db.Exec(`UPDATE books SET publisher = 'Penguin', edits_json = '{"narrator":"Tag Reader","publisher":""}', edited_at = 5 WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	var metaBefore, metaAfter int64
	if err := db.QueryRow(`SELECT publisher, meta_updated_at FROM books WHERE id = 2`).Scan(&publisher, &metaBefore); err != nil {
		t.Fatal(err)
	}
	if publisher != "Penguin" || metaBefore == 0 {
		t.Fatalf("an edit wrote publisher %q, meta_updated_at %d", publisher, metaBefore)
	}
	// The new details move meta_updated_at, so listening apps refetch.
	if _, err := db.Exec(`UPDATE books SET isbn = '9780141439587' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT meta_updated_at FROM books WHERE id = 1`).Scan(&metaAfter); err != nil {
		t.Fatal(err)
	}
	if metaAfter == 0 {
		t.Fatal("an ISBN change didn't move meta_updated_at")
	}
}
