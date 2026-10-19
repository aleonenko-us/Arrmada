package store

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

// 0335 moves discography grabs and blocks to media_type 'music_artist' (they hold an
// artist id) and leaves album grabs, and every other kind, alone.
func TestMusicDiscographyGrabsMigration(t *testing.T) {
	ctx := context.Background()
	db := testDB(t, 1)
	all := embeddedMigrations()
	names, err := listMigrations(all)
	if err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	for _, n := range names {
		if n >= "0335" {
			break
		}
		body, err := fs.ReadFile(all, n)
		if err != nil {
			t.Fatal(err)
		}
		before[n] = &fstest.MapFile{Data: body}
	}
	if err := runMigrations(ctx, db, before); err != nil {
		t.Fatalf("migrate to 0334: %v", err)
	}
	for _, q := range []string{
		`INSERT INTO grabs (id, movie_id, title, media_type, info_hash, status) VALUES (1, 7, 'Artist - Discography (1990-2010) [FLAC]', 'music', 'aa', 'grabbed')`,
		`INSERT INTO grabs (id, movie_id, title, media_type, info_hash, status) VALUES (2, 7, 'Artist - Album [FLAC]', 'music', 'bb', 'grabbed')`,
		`INSERT INTO grabs (id, movie_id, title, media_type, info_hash, status) VALUES (3, 7, 'Artist - Complete Collection [MP3-320]', 'music', 'cc', 'imported')`,
		`INSERT INTO grabs (id, movie_id, title, media_type, info_hash, status) VALUES (4, 7, 'Some.Show.Discography.Special.S01E01', 'series', 'dd', 'grabbed')`,
		`INSERT INTO blocklist (id, movie_id, norm_title, title, media_type) VALUES (1, 7, 'x', 'Artist - Box Set [FLAC]', 'music')`,
		`INSERT INTO blocklist (id, movie_id, norm_title, title, media_type) VALUES (2, 7, 'y', 'Artist - Album [FLAC]', 'music')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := runMigrations(ctx, db, all); err != nil {
		t.Fatalf("migrate the rest: %v", err)
	}
	for id, want := range map[int]string{1: "music_artist", 2: "music", 3: "music_artist", 4: "series"} {
		var got string
		if err := db.QueryRow(`SELECT media_type FROM grabs WHERE id = ?`, id).Scan(&got); err != nil || got != want {
			t.Errorf("grab %d: media_type %q (err %v), want %q", id, got, err, want)
		}
	}
	for id, want := range map[int]string{1: "music_artist", 2: "music"} {
		var got string
		if err := db.QueryRow(`SELECT media_type FROM blocklist WHERE id = ?`, id).Scan(&got); err != nil || got != want {
			t.Errorf("block %d: media_type %q (err %v), want %q", id, got, err, want)
		}
	}
}
