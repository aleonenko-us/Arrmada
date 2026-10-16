package store

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

// 0321 is additive: every film and show already in the library belongs to the default
// folder (0), where its files are, and there are no extra folders until someone adds one.
func TestLibraryFoldersMigration(t *testing.T) {
	ctx := context.Background()
	db := testDB(t, 1)
	all := embeddedMigrations()
	names, err := listMigrations(all)
	if err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	for _, n := range names {
		if n >= "0321" {
			break
		}
		body, err := fs.ReadFile(all, n)
		if err != nil {
			t.Fatal(err)
		}
		before[n] = &fstest.MapFile{Data: body}
	}
	if err := runMigrations(ctx, db, before); err != nil {
		t.Fatalf("migrate to 0320: %v", err)
	}
	for _, q := range []string{
		`INSERT INTO movies (id, tmdb_id, title, has_file, movie_file_path) VALUES (1, 949, 'Heat', 1, '/storage/movies/Heat (1995)/Heat.mkv')`,
		`INSERT INTO series (id, tmdb_id, title) VALUES (1, 1396, 'Breaking Bad')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := runMigrations(ctx, db, all); err != nil {
		t.Fatalf("migrate the rest: %v", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM movies WHERE library_folder_id = 0`); n != 1 {
		t.Errorf("films in the default folder: %d of 1", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM series WHERE library_folder_id = 0`); n != 1 {
		t.Errorf("shows in the default folder: %d of 1", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM library_folders`); n != 0 {
		t.Errorf("extra folders after the upgrade: %d, want none", n)
	}
	// Only Movies and TV, and only known rules.
	if _, err := db.Exec(`INSERT INTO library_folders (media, path) VALUES ('book', '/storage/books2')`); err == nil {
		t.Error("a books folder was accepted")
	}
	if _, err := db.Exec(`INSERT INTO library_folders (media, path, rule) VALUES ('movie', '/storage/kids', 'romance')`); err == nil {
		t.Error("an unknown rule was accepted")
	}
	if _, err := db.Exec(`INSERT INTO library_folders (media, path, rule) VALUES ('movie', '/storage/kids', 'kids')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO library_folders (media, path) VALUES ('movie', '/storage/kids')`); err == nil {
		t.Error("the same folder twice was accepted")
	}
}
