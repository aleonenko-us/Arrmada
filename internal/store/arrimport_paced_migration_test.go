package store

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

// 0324 is additive: every film and show already in the library is searched as before
// (search_paced 0); only titles a Radarr/Sonarr import adds carry the mark.
func TestArrImportPacedSearchMigration(t *testing.T) {
	ctx := context.Background()
	db := testDB(t, 1)
	all := embeddedMigrations()
	names, err := listMigrations(all)
	if err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	for _, n := range names {
		if n >= "0324_arrimport" {
			break
		}
		body, err := fs.ReadFile(all, n)
		if err != nil {
			t.Fatal(err)
		}
		before[n] = &fstest.MapFile{Data: body}
	}
	if err := runMigrations(ctx, db, before); err != nil {
		t.Fatalf("migrate to 0323: %v", err)
	}
	for _, q := range []string{
		`INSERT INTO movies (id, tmdb_id, title, monitored) VALUES (1, 949, 'Heat', 1)`,
		`INSERT INTO series (id, tmdb_id, title, monitored) VALUES (1, 1396, 'Breaking Bad', 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := runMigrations(ctx, db, all); err != nil {
		t.Fatalf("migrate the rest: %v", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM movies WHERE search_paced = 0`); n != 1 {
		t.Errorf("films searched as before: %d of 1", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM series WHERE search_paced = 0`); n != 1 {
		t.Errorf("shows searched as before: %d of 1", n)
	}
}
