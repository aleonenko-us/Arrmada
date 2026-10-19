package store

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

// 0336 marks a 'grabbed' album grab imported when its download's hash is already in
// imports, and leaves every other row alone.
func TestMusicPartialGrabsMigration(t *testing.T) {
	ctx := context.Background()
	db := testDB(t, 1)
	all := embeddedMigrations()
	names, err := listMigrations(all)
	if err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	for _, n := range names {
		if n >= "0336" {
			break
		}
		body, err := fs.ReadFile(all, n)
		if err != nil {
			t.Fatal(err)
		}
		before[n] = &fstest.MapFile{Data: body}
	}
	if err := runMigrations(ctx, db, before); err != nil {
		t.Fatalf("migrate to 0335: %v", err)
	}
	for _, q := range []string{
		`INSERT INTO imports (download_hash, source_path, target_path, title) VALUES ('AAAA', '', '', 'partial album')`,
		`INSERT INTO imports (download_hash, source_path, target_path, title) VALUES ('cccc', '', '', 'a movie')`,
		// Imported by hash (case differs), still 'grabbed': healed.
		`INSERT INTO grabs (id, movie_id, title, media_type, info_hash, status) VALUES (1, 3, 'A - B [FLAC]', 'music', 'aaaa', 'grabbed')`,
		// Not imported: left downloading.
		`INSERT INTO grabs (id, movie_id, title, media_type, info_hash, status) VALUES (2, 3, 'A - B [MP3]', 'music', 'bbbb', 'grabbed')`,
		// Imported, but a movie: not this migration's business.
		`INSERT INTO grabs (id, movie_id, title, media_type, info_hash, status) VALUES (3, 3, 'Film.2020', 'movie', 'cccc', 'grabbed')`,
		// Already failed: stays failed.
		`INSERT INTO grabs (id, movie_id, title, media_type, info_hash, status) VALUES (4, 3, 'A - B [FLAC]', 'music', 'aaaa', 'failed')`,
		// No hash at all: can't be told.
		`INSERT INTO grabs (id, movie_id, title, media_type, info_hash, status) VALUES (5, 3, 'A - C [FLAC]', 'music', '', 'grabbed')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := runMigrations(ctx, db, all); err != nil {
		t.Fatalf("migrate the rest: %v", err)
	}
	for id, want := range map[int]string{1: "imported", 2: "grabbed", 3: "grabbed", 4: "failed", 5: "grabbed"} {
		var got string
		if err := db.QueryRow(`SELECT status FROM grabs WHERE id = ?`, id).Scan(&got); err != nil || got != want {
			t.Errorf("grab %d: status %q (err %v), want %q", id, got, err, want)
		}
	}
}
