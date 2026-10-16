package store

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

// 0322 keys the needs-review list by library folder too: existing rows survive unchanged
// (each keeps the folder its scan walked), and two folders may each list a same-named one.
func TestLibraryUnmatchedRootKeyMigration(t *testing.T) {
	ctx := context.Background()
	db := testDB(t, 1)
	all := embeddedMigrations()
	names, err := listMigrations(all)
	if err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	for _, n := range names {
		if n >= "0322" {
			break
		}
		body, err := fs.ReadFile(all, n)
		if err != nil {
			t.Fatal(err)
		}
		before[n] = &fstest.MapFile{Data: body}
	}
	if err := runMigrations(ctx, db, before); err != nil {
		t.Fatalf("migrate to 0321: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO library_unmatched (media, root, folder, title, year, candidates_json, scanned_at)
		VALUES ('movie', '/storage/movies', 'Frozen (2013)', 'Frozen', 2013, '[{"tmdb_id":1}]', '2026-10-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(ctx, db, all); err != nil {
		t.Fatalf("migrate the rest: %v", err)
	}
	var title, cands string
	if err := db.QueryRow(`SELECT title, candidates_json FROM library_unmatched WHERE media = 'movie' AND root = '/storage/movies' AND folder = 'Frozen (2013)'`).Scan(&title, &cands); err != nil || title != "Frozen" || cands != `[{"tmdb_id":1}]` {
		t.Errorf("existing row = %q %q, %v", title, cands, err)
	}
	if _, err := db.Exec(`INSERT INTO library_unmatched (media, root, folder, scanned_at) VALUES ('movie', '/storage/kids', 'Frozen (2013)', 'x')`); err != nil {
		t.Errorf("the same folder name in another library folder: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO library_unmatched (media, root, folder, scanned_at) VALUES ('movie', '/storage/kids', 'Frozen (2013)', 'x')`); err == nil {
		t.Error("a duplicate row was accepted")
	}
}
