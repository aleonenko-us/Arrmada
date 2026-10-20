package store

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

// 0344 moves every cached artist lookup to the key the lookup now asks under
// (inc=aliases+genres), marked due so it's refetched with its aliases; nothing is dropped,
// and the other cached answers stay as they were.
func TestMusicAliasesMigrationMovesArtistCache(t *testing.T) {
	ctx := context.Background()
	db := testDB(t, 1)
	all := embeddedMigrations()
	names, err := listMigrations(all)
	if err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	for _, n := range names {
		if n >= "0344" {
			break
		}
		body, err := fs.ReadFile(all, n)
		if err != nil {
			t.Fatal(err)
		}
		before[n] = &fstest.MapFile{Data: body}
	}
	if err := runMigrations(ctx, db, before); err != nil {
		t.Fatalf("migrate to 0343: %v", err)
	}
	for _, key := range []string{
		"artist:/artist/a74b1b7f?inc=genres",
		"release-group-browse:/release-group?artist=a74b1b7f&limit=100&offset=0&type=album%7Cep",
	} {
		if _, err := db.Exec(`INSERT INTO music_mb_cache (key, body, status, fetched_at, checked_at) VALUES (?, x'00', 200, 5, 5)`, key); err != nil {
			t.Fatal(err)
		}
	}
	if err := runMigrations(ctx, db, all); err != nil {
		t.Fatalf("migrate the rest: %v", err)
	}
	var checked, fetched int64
	if err := db.QueryRow(`SELECT checked_at, fetched_at FROM music_mb_cache WHERE key = 'artist:/artist/a74b1b7f?inc=aliases+genres'`).Scan(&checked, &fetched); err != nil {
		t.Fatalf("the artist answer under its new key: %v", err)
	}
	if checked != 0 || fetched != 5 {
		t.Errorf("checked_at %d fetched_at %d; want it due (0) with its body kept", checked, fetched)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM music_mb_cache`).Scan(&n)
	if n != 2 {
		t.Errorf("%d cached answers, want both kept", n)
	}
	if _, err := db.Exec(`INSERT INTO music_artist_aliases (artist_id, name, folded) VALUES (1, 'x', 'x')`); err == nil {
		t.Error("an alias of no artist was accepted")
	}
}
