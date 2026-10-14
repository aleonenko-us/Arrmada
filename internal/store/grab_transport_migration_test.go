package store

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

// 0320 gives every grab a transport. Every grab before it was a torrent, so existing rows
// read 'torrent'; a row written without one later does too, and a usenet grab keeps its
// own.
func TestGrabTransportMigration(t *testing.T) {
	ctx := context.Background()
	db := testDB(t, 1)
	all := embeddedMigrations()
	names, err := listMigrations(all)
	if err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	for _, n := range names {
		if n >= "0320" {
			break
		}
		body, err := fs.ReadFile(all, n)
		if err != nil {
			t.Fatal(err)
		}
		before[n] = &fstest.MapFile{Data: body}
	}
	if err := runMigrations(ctx, db, before); err != nil {
		t.Fatalf("migrate to 0319: %v", err)
	}
	for _, q := range []string{
		`INSERT INTO grabs (id, movie_id, title, media_type, info_hash, status) VALUES (1, 1, 'Heat.1995.1080p', 'movie', '0123456789abcdef0123456789abcdef01234567', 'imported')`,
		`INSERT INTO grabs (id, movie_id, title, media_type, info_hash, status) VALUES (2, 2, 'Show.S01.1080p', 'series', '', 'grabbed')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := runMigrations(ctx, db, all); err != nil {
		t.Fatalf("migrate the rest: %v", err)
	}

	if n := count(t, db, `SELECT COUNT(*) FROM grabs WHERE transport = 'torrent' AND id IN (1, 2)`); n != 2 {
		t.Errorf("existing grabs reading 'torrent': %d of 2", n)
	}
	if _, err := db.Exec(`INSERT INTO grabs (id, movie_id, title, media_type, info_hash) VALUES (3, 3, 'New.Torrent', 'movie', 'abc')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO grabs (id, movie_id, title, media_type, info_hash, transport, seed_enabled) VALUES (4, 4, 'New.Nzb', 'movie', 'sab-sabnzbd_nzo_abc123', 'usenet', 0)`); err != nil {
		t.Fatal(err)
	}
	var t3, t4 string
	if err := db.QueryRow(`SELECT transport FROM grabs WHERE id = 3`).Scan(&t3); err != nil || t3 != "torrent" {
		t.Errorf("a grab written without a transport = %q (err %v), want torrent", t3, err)
	}
	if err := db.QueryRow(`SELECT transport FROM grabs WHERE id = 4`).Scan(&t4); err != nil || t4 != "usenet" {
		t.Errorf("a usenet grab = %q (err %v), want usenet", t4, err)
	}
}
