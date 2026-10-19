package store

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

// 0335 gives shows a remembered monitoring preset. Shows already in the library get ''
// (not known) and keep every other flag: the migration changes nothing they search.
func TestSeriesMonitorPresetMigration(t *testing.T) {
	ctx := context.Background()
	full := embeddedMigrations()
	names, err := listMigrations(full)
	if err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	for _, n := range names {
		if n >= "0335" {
			break
		}
		b, err := fs.ReadFile(full, n)
		if err != nil {
			t.Fatal(err)
		}
		before[n] = &fstest.MapFile{Data: b}
	}
	db := testDB(t, 1)
	if err := runMigrations(ctx, db, before); err != nil {
		t.Fatalf("migrate to 0334: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO series (id, tmdb_id, title, monitored, monitor_new_seasons) VALUES (1, 1, 'On', 1, 1), (2, 2, 'Off', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(ctx, db, full); err != nil {
		t.Fatalf("migrate the rest: %v", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM series WHERE monitor_preset = ''`); n != 2 {
		t.Errorf("shows with an unknown preset = %d, want both", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM series WHERE (id = 1 AND monitored = 1 AND monitor_new_seasons = 1) OR (id = 2 AND monitored = 0 AND monitor_new_seasons = 0)`); n != 2 {
		t.Errorf("shows whose flags survived = %d, want 2", n)
	}
	if _, err := db.Exec(`UPDATE series SET monitor_preset = 'missing' WHERE id = 1`); err != nil {
		t.Fatalf("the column takes a preset: %v", err)
	}
}
