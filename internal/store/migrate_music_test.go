package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// musicModelMigration is MUS-28's migration (the new music model and the lossless copy).
const musicModelMigration = "0339_music_model.sql"

// migrationsBefore is the embedded migration set up to (not including) the named file, plus
// the full set, so a test can fill a database the way an older build left it and then
// upgrade it.
func migrationsBefore(t *testing.T, name string) (before, all fs.FS) {
	t.Helper()
	emb := embeddedMigrations()
	names, err := listMigrations(emb)
	if err != nil {
		t.Fatal(err)
	}
	m := fstest.MapFS{}
	found := false
	for _, n := range names {
		if n >= name {
			found = found || n == name
			continue
		}
		body, err := fs.ReadFile(emb, n)
		if err != nil {
			t.Fatal(err)
		}
		m[n] = &fstest.MapFile{Data: body}
	}
	if !found {
		t.Fatalf("no migration %s", name)
	}
	return m, emb
}

// dumpRows renders every row of a query as one string, so a table can be compared before
// and after a migration column for column.
func dumpRows(t *testing.T, db *sql.DB, q string) []string {
	t.Helper()
	rows, err := db.Query(q)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(vals))
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			parts[i] = fmt.Sprintf("%s=%v", cols[i], v)
		}
		out = append(out, strings.Join(parts, " "))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func mustExecAll(t *testing.T, db *sql.DB, stmts ...string) {
	t.Helper()
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// fillOldMusic writes a library the way Phase 1 left it: a monitored artist with a
// profile, a scanned artist with none, a two-disc album with measured files, an album
// whose listing was never fetched, an unmonitored album, two tracks claiming one file (in
// one album and across two), a file flag with no path, a stale path on a track without a
// file, a track of an album that's gone, and the rows that point at music ids from outside
// (grabs, a discography grab, blocklist, search attempts, a review, imports, events).
func fillOldMusic(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExecAll(t, db,
		`INSERT INTO artists (id, mbid, name, sort_name, monitored, quality_profile, added_at)
		 VALUES (1, 'mb-radiohead', 'Radiohead', 'Radiohead', 1, 'lossless', '2026-01-02 03:04:05'),
		        (2, 'mb-scanned', 'Scanned Act', '', 0, '', '2026-02-03 04:05:06')`,
		`INSERT INTO albums (id, artist_id, mbid, title, year, album_type, cover_url, release_date, monitored,
		                     last_search_at, search_misses)
		 VALUES (10, 1, 'rg-okc', 'OK Computer', 1997, 'Album', 'http://c/okc', '1997-05-21', 1, '2026-10-01 00:00:00', 3),
		        (11, 1, 'rg-unlisted', 'Future Thing', 2027, 'EP', '', '2027', 1, NULL, 0),
		        (12, 2, 'rg-live', 'Live Set', 2001, 'Live', '', '', 0, NULL, 7),
		        (13, 2, 'rg-comp', 'Best Of', 2005, 'Compilation', '', '', 1, NULL, 0)`,
		// OK Computer: two discs; disc 1 complete and measured, disc 2 partly held, one
		// track unmonitored.
		`INSERT INTO tracks (id, album_id, mbid, disc_number, track_number, title, duration_sec, monitored,
		                     has_file, file_path, format, bitrate_kbps, size_bytes, source_release)
		 VALUES (100, 10, 'rt-1', 1, 1, 'Airbag', 284, 1, 1, '/music/Radiohead/OK Computer/01 - Airbag.flac', 'FLAC-24', 2100, 40000000, 'Radiohead - OK Computer [FLAC 24]'),
		        (101, 10, 'rt-2', 1, 2, 'Paranoid Android', 383, 1, 1, '/music/Radiohead/OK Computer/02 - Paranoid Android.flac', 'FLAC', 900, 30000000, 'Radiohead - OK Computer [FLAC]'),
		        (102, 10, 'rt-3', 2, 1, 'Lull', 120, 0, 0, '', '', 0, 0, ''),
		        (103, 10, 'shared-mbid', 2, 2, 'Meeting in the Aisle', 190, 1, 1, '/music/Radiohead/OK Computer/2-02 - Meeting.mp3', 'MP3', 0, 5000000, '')`,
		// Live Set: two rows claim one path; a file flag with no path; a stale path.
		`INSERT INTO tracks (id, album_id, mbid, disc_number, track_number, title, duration_sec, monitored,
		                     has_file, file_path, format, bitrate_kbps, size_bytes, source_release)
		 VALUES (200, 12, '', 1, 1, 'Intro', 60, 0, 1, '/music/Scanned Act/Live Set/01.mp3', 'MP3-320', 320, 2000000, 'scan'),
		        (201, 12, '', 1, 2, 'Intro (again)', 61, 0, 1, '/music/Scanned Act/Live Set/01.mp3', 'MP3-320', 320, 2000000, 'scan'),
		        (202, 12, '', 1, 3, 'No Path', 62, 0, 1, '', 'MP3', 0, 0, ''),
		        (203, 12, '', 1, 4, 'Stale', 63, 0, 0, '/music/Scanned Act/Live Set/04.mp3', '', 0, 0, '')`,
		// Best Of: the same release-track MBID string as OK Computer's disc 2 track 2, and a
		// file that OK Computer's track 100 already claims.
		`INSERT INTO tracks (id, album_id, mbid, disc_number, track_number, title, duration_sec, monitored,
		                     has_file, file_path, format, bitrate_kbps, size_bytes, source_release)
		 VALUES (300, 13, 'shared-mbid', 1, 1, 'Meeting in the Aisle', 190, 1, 0, '', '', 0, 0, ''),
		        (301, 13, '', 1, 2, 'Airbag (again)', 284, 1, 1, '/music/Radiohead/OK Computer/01 - Airbag.flac', 'FLAC', 0, 40000000, 'other')`,
		`INSERT INTO artist_events (artist_id, event, detail) VALUES (1, 'added', 'Added to library'), (2, 'imported', 'Found Live Set')`,
		// Album grabs and a discography grab (artist id in movie_id), a blocklisted release.
		`INSERT INTO grabs (id, movie_id, title, media_type, info_hash, status)
		 VALUES (1, 10, 'Radiohead - OK Computer [FLAC]', 'music', 'aaaa', 'imported'),
		        (2, 1, 'Radiohead - Discography [FLAC]', 'music_artist', 'bbbb', 'grabbed')`,
		`INSERT INTO blocklist (movie_id, norm_title, title, media_type, reason)
		 VALUES (10, 'radioheadokcomputermp3', 'Radiohead - OK Computer [MP3]', 'music', 'stalled'),
		        (1, 'radioheaddiscography', 'Radiohead - Discography', 'music_artist', 'stalled')`,
		`INSERT INTO search_attempts (media_type, media_id, scope, started_at, outcome) VALUES ('music', 10, 'album', 1, 'grabbed')`,
		`INSERT INTO import_reviews (hash, name, media_type, expected_id, reason, reason_code)
		 VALUES ('cccc', 'Radiohead - OK Computer', 'music', 10, 'Grabbed for OK Computer', 'mismatch')`,
		`INSERT INTO imports (download_hash, source_path, target_path, title) VALUES ('aaaa', '', '', 'OK Computer')`,
	)
	// A track whose album is gone: only possible on a database that once ran with foreign
	// keys off, but its file path must survive all the same.
	mustExecAll(t, db,
		`PRAGMA foreign_keys=OFF`,
		`INSERT INTO tracks (id, album_id, disc_number, track_number, title, has_file, file_path, format)
		 VALUES (400, 99, 1, 1, 'Orphan', 1, '/music/gone/01.flac', 'FLAC')`,
		`PRAGMA foreign_keys=ON`,
	)
}

// untouched lists the queries whose rows MUS-28 must leave exactly as they were.
var untouched = []string{
	`SELECT id, mbid, name, sort_name, overview, image_url, genres_json, monitored, quality_profile, added_at FROM artists ORDER BY id`,
	`SELECT id, artist_id, mbid, title, year, album_type, cover_url, release_date, monitored, added_at,
	        last_search_at, search_misses FROM albums ORDER BY id`,
	`SELECT * FROM tracks ORDER BY id`,
	`SELECT * FROM artist_events ORDER BY id`,
	`SELECT * FROM grabs ORDER BY id`,
	`SELECT * FROM blocklist ORDER BY id`,
	`SELECT * FROM search_attempts ORDER BY id`,
	`SELECT * FROM import_reviews ORDER BY id`,
	`SELECT * FROM imports ORDER BY id`,
}

func TestMusicModelMigration(t *testing.T) {
	ctx := context.Background()
	db := testDB(t, 1)
	before, all := migrationsBefore(t, musicModelMigration)
	if err := runMigrations(ctx, db, before); err != nil {
		t.Fatalf("migrate to the old model: %v", err)
	}
	fillOldMusic(t, db)
	// The old table's own constraint is what keeps a disc/position clash out of the copy:
	// two rows on one (album, disc, track) can't exist to begin with.
	if _, err := db.Exec(`INSERT INTO tracks (album_id, disc_number, track_number, title) VALUES (10, 1, 1, 'dupe')`); err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "unique") {
		t.Fatalf("a second row on (album 10, disc 1, track 1): err = %v, want the old UNIQUE to refuse it", err)
	}
	snap := map[string][]string{}
	for _, q := range untouched {
		snap[q] = dumpRows(t, db, q)
	}

	if err := runMigrations(ctx, db, all); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for _, q := range untouched {
		if got := dumpRows(t, db, q); strings.Join(got, "\n") != strings.Join(snap[q], "\n") {
			t.Errorf("%s changed:\nbefore %v\nafter  %v", q, snap[q], got)
		}
	}

	// Artists: still in the library, each with its one-name credit.
	for _, id := range []int{1, 2} {
		var inLib int
		var key, display, credited string
		if err := db.QueryRow(`SELECT a.in_library, c.credit_key, c.display, n.credited_name
		                         FROM artists a JOIN music_artist_credit_names n ON n.artist_id = a.id
		                         JOIN music_artist_credits c ON c.id = n.credit_id WHERE a.id = ?`, id).
			Scan(&inLib, &key, &display, &credited); err != nil {
			t.Fatalf("artist %d credit: %v", id, err)
		}
		if inLib != 1 || key == "" || display != credited {
			t.Errorf("artist %d: in_library=%d key=%q display=%q credited=%q", id, inLib, key, display, credited)
		}
	}
	if got := count(t, db, `SELECT COUNT(*) FROM music_artist_credits WHERE credit_key = 'mb-radiohead'`); got != 1 {
		t.Errorf("Radiohead's credit key: %d rows, want its MBID", got)
	}

	// Albums: types split, credit set, target edition only where a listing existed.
	type albumWant struct {
		primary, secondary string
		target             bool
	}
	for id, w := range map[int]albumWant{
		10: {"Album", "", true}, 11: {"EP", "", false}, 12: {"Album", "Live", true}, 13: {"Album", "Compilation", true},
	} {
		var primary, secondary string
		var target, credit sql.NullInt64
		if err := db.QueryRow(`SELECT primary_type, secondary_types, target_release_id, artist_credit_id FROM albums WHERE id = ?`, id).
			Scan(&primary, &secondary, &target, &credit); err != nil {
			t.Fatal(err)
		}
		if primary != w.primary || secondary != w.secondary || target.Valid != w.target || !credit.Valid {
			t.Errorf("album %d: primary=%q secondary=%q target=%v credit=%v, want %+v", id, primary, secondary, target, credit, w)
		}
	}

	// OK Computer's legacy edition: two discs, four release tracks with the old ids.
	var rel, discs, tracks, legacy int
	var relMBID string
	if err := db.QueryRow(`SELECT r.id, r.mbid, r.disc_count, r.track_count, r.legacy FROM music_releases r
	                        JOIN albums al ON al.target_release_id = r.id WHERE al.id = 10`).
		Scan(&rel, &relMBID, &discs, &tracks, &legacy); err != nil {
		t.Fatal(err)
	}
	if relMBID != "" || discs != 2 || tracks != 4 || legacy != 1 {
		t.Errorf("OK Computer's edition: mbid=%q discs=%d tracks=%d legacy=%d", relMBID, discs, tracks, legacy)
	}
	if got := dumpRows(t, db, `SELECT m.position, m.track_count FROM music_media m WHERE m.release_id = `+fmt.Sprint(rel)+` ORDER BY m.position`); strings.Join(got, ";") !=
		"position=1 track_count=2;position=2 track_count=2" {
		t.Errorf("OK Computer's discs: %v", got)
	}
	got := dumpRows(t, db, `SELECT rt.id, m.position AS disc, rt.position, rt.number, rt.title, rt.mbid, rt.length_ms,
	                               rt.monitored, rt.legacy_track_id, rt.recording_id
	                          FROM music_release_tracks rt JOIN music_media m ON m.id = rt.medium_id
	                         WHERE m.release_id = `+fmt.Sprint(rel)+` ORDER BY rt.id`)
	want := []string{
		"id=100 disc=1 position=1 number=1 title=Airbag mbid=rt-1 length_ms=284000 monitored=1 legacy_track_id=100 recording_id=100",
		"id=101 disc=1 position=2 number=2 title=Paranoid Android mbid=rt-2 length_ms=383000 monitored=1 legacy_track_id=101 recording_id=101",
		"id=102 disc=2 position=1 number=1 title=Lull mbid=rt-3 length_ms=120000 monitored=0 legacy_track_id=102 recording_id=102",
		"id=103 disc=2 position=2 number=2 title=Meeting in the Aisle mbid=shared-mbid length_ms=190000 monitored=1 legacy_track_id=103 recording_id=103",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("OK Computer's release tracks:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// One recording per old row, none with an MBID (tracks.mbid was a release-track id).
	if n := count(t, db, `SELECT COUNT(*) FROM music_recordings`); n != 10 {
		t.Errorf("recordings = %d, want one per old row of a live album (10)", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM music_recordings WHERE mbid != ''`); n != 0 {
		t.Errorf("%d recordings carry an MBID, want none", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM music_release_tracks WHERE mbid = 'shared-mbid'`); n != 2 {
		t.Errorf("the release-track MBID shared by two albums is on %d release tracks, want 2", n)
	}

	// Files: copied with their facts; clashes noted, never dropped.
	got = dumpRows(t, db, `SELECT album_id, release_track_id, path, size_bytes, container, lossless, bitrate_kbps, quality,
	                              tag_state, source_release, probed_at FROM music_track_files ORDER BY release_track_id`)
	want = []string{
		"album_id=10 release_track_id=100 path=/music/Radiohead/OK Computer/01 - Airbag.flac size_bytes=40000000 container=flac lossless=1 bitrate_kbps=2100 quality=FLAC-24 tag_state=as_found source_release=Radiohead - OK Computer [FLAC 24] probed_at=<nil>",
		"album_id=10 release_track_id=101 path=/music/Radiohead/OK Computer/02 - Paranoid Android.flac size_bytes=30000000 container=flac lossless=1 bitrate_kbps=900 quality=FLAC tag_state=as_found source_release=Radiohead - OK Computer [FLAC] probed_at=<nil>",
		"album_id=10 release_track_id=103 path=/music/Radiohead/OK Computer/2-02 - Meeting.mp3 size_bytes=5000000 container=mp3 lossless=0 bitrate_kbps=0 quality=MP3 tag_state=as_found source_release= probed_at=<nil>",
		"album_id=12 release_track_id=200 path=/music/Scanned Act/Live Set/01.mp3 size_bytes=2000000 container=mp3 lossless=0 bitrate_kbps=320 quality=MP3-320 tag_state=as_found source_release=scan probed_at=<nil>",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("track files:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	got = dumpRows(t, db, `SELECT legacy_track_id, album_id, kind, path FROM music_migration_notes ORDER BY legacy_track_id`)
	want = []string{
		"legacy_track_id=201 album_id=12 kind=shared_path path=/music/Scanned Act/Live Set/01.mp3",
		"legacy_track_id=202 album_id=12 kind=no_path path=",
		"legacy_track_id=203 album_id=12 kind=stale_path path=/music/Scanned Act/Live Set/04.mp3",
		"legacy_track_id=301 album_id=13 kind=shared_path path=/music/Radiohead/OK Computer/01 - Airbag.flac",
		"legacy_track_id=400 album_id=99 kind=orphan path=/music/gone/01.flac",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("migration notes:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	var note string
	_ = db.QueryRow(`SELECT note FROM music_migration_notes WHERE legacy_track_id = 301`).Scan(&note)
	if !strings.Contains(note, "track 100") {
		t.Errorf("the shared-path note %q should name the track that keeps the file", note)
	}

	// The counts the acceptance names: every old path is in the files or the notes.
	if n := count(t, db, `SELECT COUNT(*) FROM tracks t WHERE t.file_path != ''
	                        AND t.file_path NOT IN (SELECT path FROM music_track_files)
	                        AND t.id NOT IN (SELECT legacy_track_id FROM music_migration_notes)`); n != 0 {
		t.Errorf("%d old file paths went nowhere", n)
	}
	mapped := count(t, db, `SELECT COUNT(*) FROM music_track_files`)
	noted := count(t, db, `SELECT COUNT(*) FROM music_migration_notes WHERE kind != 'stale_path'`)
	if old := count(t, db, `SELECT COUNT(*) FROM tracks WHERE has_file = 1`); mapped+noted != old {
		t.Errorf("mapped %d + noted %d != old file rows %d", mapped, noted, old)
	}
	// The self-check's scratch tables are gone with the migration's temp copy.
	if n := count(t, db, `SELECT COUNT(*) FROM sqlite_temp_master WHERE name LIKE 'music_model%'`); n != 0 {
		t.Errorf("%d temp tables left behind", n)
	}
}

// A copy that doesn't add up fails the migration: nothing of it is applied, the old
// table is untouched, and the migration stays pending (the snapshot is the way back).
func TestMusicModelMigrationFailsItsSelfCheck(t *testing.T) {
	for name, sabotage := range map[string]string{
		"a lost release track": `DELETE FROM music_release_tracks WHERE id = 101;`,
		"a lost file":          `DELETE FROM music_track_files WHERE release_track_id = 101;`,
		"a changed tier":       `UPDATE music_track_files SET quality = 'MP3-128' WHERE release_track_id = 100;`,
		"a flipped monitor":    `UPDATE music_release_tracks SET monitored = 1 - monitored WHERE id = 102;`,
		"no target edition":    `UPDATE albums SET target_release_id = NULL WHERE id = 10;`,
		"an uncredited album":  `UPDATE albums SET artist_credit_id = NULL WHERE id = 13;`,
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			db := testDB(t, 1)
			before, all := migrationsBefore(t, musicModelMigration)
			if err := runMigrations(ctx, db, before); err != nil {
				t.Fatal(err)
			}
			fillOldMusic(t, db)
			oldTracks := dumpRows(t, db, `SELECT * FROM tracks ORDER BY id`)

			body, err := fs.ReadFile(all, musicModelMigration)
			if err != nil {
				t.Fatal(err)
			}
			marker := "-- @self-check"
			if !strings.Contains(string(body), marker) {
				t.Fatalf("the migration has no %q marker to sabotage before", marker)
			}
			broken := strings.Replace(string(body), marker, sabotage+"\n"+marker, 1)
			m := before.(fstest.MapFS)
			m[musicModelMigration] = &fstest.MapFile{Data: []byte(broken)}

			err = runMigrations(ctx, db, m)
			if err == nil || !strings.Contains(err.Error(), "music_model self-check") {
				t.Fatalf("err = %v, want the self-check to fail the migration", err)
			}
			if n := count(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'music_%' AND name NOT LIKE 'music_mb_cache%'`) + count(t, db, `SELECT COUNT(*) FROM sqlite_temp_master`); n != 0 {
				t.Errorf("%d music_* objects exist after a failed migration, want none", n)
			}
			if n := count(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE version = '0339_music_model'`); n != 0 {
				t.Error("the failed migration was recorded as applied")
			}
			if got := dumpRows(t, db, `SELECT * FROM tracks ORDER BY id`); strings.Join(got, "\n") != strings.Join(oldTracks, "\n") {
				t.Error("the old tracks table changed under a failed migration")
			}
			// And the untouched file applies cleanly on the next boot.
			if err := runMigrations(ctx, db, all); err != nil {
				t.Fatalf("the real migration after a failed one: %v", err)
			}
		})
	}
}

// The big-library timing: ARRMADA_MUSIC_MIGRATION_BENCH=1 builds a 1000-artist / 15k-album
// / 200k-track library on disk (half the tracks with files) and times the migration. Off
// by default: it is a measurement for the roadmap, not a check that needs to run on every
// push, though it does assert the copy is complete.
func TestMusicModelMigrationBigLibrary(t *testing.T) {
	if os.Getenv("ARRMADA_MUSIC_MIGRATION_BENCH") != "1" {
		t.Skip("set ARRMADA_MUSIC_MIGRATION_BENCH=1 to time the migration on a generated 200k-track library")
	}
	ctx := context.Background()
	db := testDB(t, 1)
	before, all := migrationsBefore(t, musicModelMigration)
	if err := runMigrations(ctx, db, before); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	fillBigOldMusic(t, db)
	t.Logf("generated the library in %s", time.Since(start).Round(time.Millisecond))

	start = time.Now()
	if err := runMigrations(ctx, db, all); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	took := time.Since(start)
	t.Logf("MUS-28 migration of 1000 artists / 15000 albums / 200000 tracks took %s", took.Round(time.Millisecond))
	if n := count(t, db, `SELECT COUNT(*) FROM music_release_tracks`); n != 200000 {
		t.Errorf("release tracks = %d, want 200000", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM music_track_files`); n != 100000 {
		t.Errorf("track files = %d, want 100000", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM music_releases`); n != 14000 {
		t.Errorf("editions = %d, want 14000", n)
	}
}

// fillBigOldMusic writes a 1000-artist / 15k-album / 200k-track library the way the old
// model held it: 200k tracks over the first 14,000 albums (about 14 each; every 7th album
// two discs), the last 1,000 albums unlisted, half the tracks with a file.
func fillBigOldMusic(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExecAll(t, db,
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 1000)
		 INSERT INTO artists (id, mbid, name, monitored, quality_profile) SELECT i, 'mb-artist-' || i, 'Artist ' || i, i % 2, '' FROM n`,
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 15000)
		 INSERT INTO albums (id, artist_id, mbid, title, album_type, monitored)
		 SELECT i, 1 + (i % 1000), 'rg-' || i, 'Album ' || i, CASE i % 5 WHEN 0 THEN 'Live' WHEN 1 THEN 'EP' ELSE 'Album' END, 1 FROM n`,
		// 200k tracks over the first 14,000 albums (about 14 each; every 7th album two discs),
		// the last 1,000 albums unlisted. Half the tracks hold a file.
		`WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM n WHERE i < 199999)
		 INSERT INTO tracks (album_id, mbid, disc_number, track_number, title, duration_sec, monitored,
		                     has_file, file_path, format, bitrate_kbps, size_bytes, source_release)
		 SELECT 1 + (i % 14000),
		        'rt-' || i,
		        CASE WHEN (1 + (i % 14000)) % 7 = 0 AND (i / 14000) % 2 = 1 THEN 2 ELSE 1 END,
		        1 + (i / 14000),
		        'Track ' || i, 200, 1,
		        i % 2,
		        CASE WHEN i % 2 = 1 THEN '/music/a/' || (1 + (i % 14000)) || '/' || i || '.flac' ELSE '' END,
		        CASE WHEN i % 2 = 1 THEN 'FLAC' ELSE '' END, 0, 30000000, 'release'
		   FROM n`,
	)
}
