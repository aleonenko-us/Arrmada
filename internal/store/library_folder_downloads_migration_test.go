package store

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

// 0329 gives each extra library folder an optional downloads folder: existing folders
// keep everything and get an empty one (the Downloads folder, as before).
func TestLibraryFolderDownloadsMigration(t *testing.T) {
	ctx := context.Background()
	db := testDB(t, 1)
	all := embeddedMigrations()
	names, err := listMigrations(all)
	if err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	for _, n := range names {
		if n >= "0329" {
			break
		}
		body, err := fs.ReadFile(all, n)
		if err != nil {
			t.Fatal(err)
		}
		before[n] = &fstest.MapFile{Data: body}
	}
	if err := runMigrations(ctx, db, before); err != nil {
		t.Fatalf("migrate to 0328: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO library_folders (id, media, path, label, rule, position) VALUES (3, 'movie', '/storage/kids', 'Kids', 'kids', 1)`); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(ctx, db, all); err != nil {
		t.Fatalf("migrate the rest: %v", err)
	}
	var path, label, rule, dl string
	if err := db.QueryRow(`SELECT path, label, rule, downloads_path FROM library_folders WHERE id = 3`).Scan(&path, &label, &rule, &dl); err != nil {
		t.Fatal(err)
	}
	if path != "/storage/kids" || label != "Kids" || rule != "kids" || dl != "" {
		t.Errorf("existing folder after 0329 = %q %q %q downloads %q", path, label, rule, dl)
	}
	if _, err := db.Exec(`UPDATE library_folders SET downloads_path = '/storage/disk2/downloads' WHERE id = 3`); err != nil {
		t.Errorf("setting a downloads folder: %v", err)
	}
}
