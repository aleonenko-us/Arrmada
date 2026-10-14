package store

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

// 0310 files every row already recorded under Plex, and the 0308 viewing triggers keep
// marking viewings for both servers. A Jellyfin play's user and item are 32-hex GUIDs and a
// Plex play's are short numbers, so the two can never be taken for the same person's
// sittings of the same item.
func TestInsightsServerMigration(t *testing.T) {
	ctx := context.Background()
	db := testDB(t, 1)
	all := embeddedMigrations()
	names, err := listMigrations(all)
	if err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	for _, n := range names {
		if n >= "0310" {
			break
		}
		body, err := fs.ReadFile(all, n)
		if err != nil {
			t.Fatal(err)
		}
		before[n] = &fstest.MapFile{Data: body}
	}
	if err := runMigrations(ctx, db, before); err != nil {
		t.Fatalf("migrate to 0309: %v", err)
	}
	const t0 = 1_760_000_000
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`INSERT INTO plex_users (id, username) VALUES ('12345', 'sam')`)
	mustExec(`INSERT INTO stream_sessions (id, session_key, user_id, rating_key, media_type, title, started_at, stopped_at)
		VALUES (1, '7', '12345', '100', 'movie', 'Dune', ?, ?)`, t0, t0+3600)
	mustExec(`INSERT INTO insights_live_sessions (session_key, started_at, last_seen_at) VALUES ('8', 1, 2)`)
	mustExec(`INSERT INTO bandwidth_samples (at, total_kbps, lan_kbps, wan_kbps) VALUES (?, 1, 1, 0)`, t0)

	if err := runMigrations(ctx, db, all); err != nil {
		t.Fatalf("migrate the rest: %v", err)
	}
	for _, q := range []string{
		`SELECT server FROM stream_sessions WHERE id = 1`,
		`SELECT server FROM plex_users WHERE id = '12345'`,
		`SELECT server FROM insights_live_sessions WHERE session_key = '8'`,
		`SELECT server FROM bandwidth_samples LIMIT 1`,
	} {
		var got string
		if err := db.QueryRow(q).Scan(&got); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if got != "plex" {
			t.Errorf("%s = %q, want plex", q, got)
		}
	}

	// Two Jellyfin sittings of one item twenty minutes apart are one viewing; a Plex play of
	// "the same" film by the Plex person stays its own viewing, however close in time.
	const jfUser, jfItem = "4f1c2b8e9a7d4c3b8e2f1a0b9c8d7e6f", "dbc9717de8df66b09eed7dacda39fcc1"
	if len(jfUser) != 32 || len(jfItem) != 32 {
		t.Fatal("fixture GUIDs must be 32 hex digits")
	}
	mustExec(`INSERT INTO stream_sessions (id, server, session_key, user_id, rating_key, media_type, title, started_at, stopped_at)
		VALUES (2, 'jellyfin', 'jellyfin:a', ?, ?, 'movie', 'Dune', ?, ?)`, jfUser, jfItem, t0+60, t0+1800)
	mustExec(`INSERT INTO stream_sessions (id, server, session_key, user_id, rating_key, media_type, title, started_at, stopped_at)
		VALUES (3, 'jellyfin', 'jellyfin:a', ?, ?, 'movie', 'Dune', ?, ?)`, jfUser, jfItem, t0+1800+20*60, t0+5400)
	mustExec(`INSERT INTO stream_sessions (id, session_key, user_id, rating_key, media_type, title, started_at, stopped_at)
		VALUES (4, '9', '12345', '100', 'movie', 'Dune', ?, ?)`, t0+3600+60, t0+7200)
	for id, want := range map[int]int{1: 1, 2: 1, 3: 0, 4: 0} {
		var got int
		if err := db.QueryRow(`SELECT viewing_start FROM stream_sessions WHERE id = ?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("play %d: viewing_start %d, want %d", id, got, want)
		}
	}
	// The Plex resume (4) joined the Plex play (1), not the Jellyfin ones: removing the
	// Jellyfin plays changes nothing on the Plex side.
	mustExec(`DELETE FROM stream_sessions WHERE server = 'jellyfin'`)
	var plexStart int
	if err := db.QueryRow(`SELECT viewing_start FROM stream_sessions WHERE id = 4`).Scan(&plexStart); err != nil {
		t.Fatal(err)
	}
	if plexStart != 0 {
		t.Errorf("Plex resume became a viewing start after the Jellyfin plays went: %d", plexStart)
	}
	// And a Jellyfin play landing between two Plex sittings leaves them one viewing.
	mustExec(`INSERT INTO stream_sessions (id, server, session_key, user_id, rating_key, media_type, title, started_at, stopped_at)
		VALUES (5, 'jellyfin', 'jellyfin:b', ?, ?, 'movie', 'Dune', ?, ?)`, jfUser, jfItem, t0+3600+30, t0+3600+50)
	for id, want := range map[int]int{4: 0, 5: 1} {
		var got int
		if err := db.QueryRow(`SELECT viewing_start FROM stream_sessions WHERE id = ?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("after the Jellyfin insert, play %d: viewing_start %d, want %d", id, got, want)
		}
	}
}
