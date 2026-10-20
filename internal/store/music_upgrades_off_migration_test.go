package store

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

// 0343 turns music upgrades off on every music profile saved before the upgrade sweep
// existed (their switch was hidden, so "on" was never a choice); video and book profiles
// are left as they were.
func TestMusicUpgradesOffMigration(t *testing.T) {
	ctx := context.Background()
	db := testDB(t, 1)
	all := embeddedMigrations()
	names, err := listMigrations(all)
	if err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	for _, n := range names {
		if n >= "0343" {
			break
		}
		body, err := fs.ReadFile(all, n)
		if err != nil {
			t.Fatal(err)
		}
		before[n] = &fstest.MapFile{Data: body}
	}
	if err := runMigrations(ctx, db, before); err != nil {
		t.Fatalf("migrate to 0342: %v", err)
	}
	for _, q := range []string{
		`INSERT INTO quality_profiles (id, media_type, name, upgrades_enabled) VALUES (901, 'music', 'Lossless, or the best MP3', 1)`,
		`INSERT INTO quality_profiles (id, media_type, name, upgrades_enabled) VALUES (902, 'music', 'Lossless only', 0)`,
		`INSERT INTO quality_profiles (id, media_type, name, upgrades_enabled) VALUES (903, 'movie', 'HD', 1)`,
		`INSERT INTO quality_profiles (id, media_type, name, upgrades_enabled) VALUES (904, 'book', 'EPUB', 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := runMigrations(ctx, db, all); err != nil {
		t.Fatalf("migrate the rest: %v", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM quality_profiles WHERE media_type = 'music' AND upgrades_enabled != 0`); n != 0 {
		t.Errorf("music profiles still upgrading: %d", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM quality_profiles WHERE id IN (903, 904) AND upgrades_enabled = 1`); n != 2 {
		t.Errorf("other profiles changed: %d of 2 still upgrading", n)
	}
}
