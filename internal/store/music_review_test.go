package store_test

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"

	"github.com/tristenlammi/arrmada/internal/music"
	"github.com/tristenlammi/arrmada/internal/store"
)

// Cases from the MUS-28/29 review: upgrades of libraries the old model could hold, read
// back through the music service.

func execAll(t *testing.T, db *sql.DB, qs ...string) {
	t.Helper()
	for _, q := range qs {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func countOf(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// upgradeWith fills a database the old model's way, then runs MUS-28's migration.
func upgradeWith(t *testing.T, fill func(db *sql.DB)) *sql.DB {
	t.Helper()
	ctx := context.Background()
	db := store.TestDB(t, 1)
	before, all := store.MigrationsBefore(t, store.MusicModelMigration)
	if err := store.RunMigrations(ctx, db, before); err != nil {
		t.Fatal(err)
	}
	fill(db)
	if err := store.RunMigrations(ctx, db, all); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// F1: an album that was complete before the upgrade, two of whose tracks recorded the same
// file, reads 2/3 after it (the second track's claim is a note). The sweep must not grab it
// again over that: it stays out of WantedAlbums until a person resolves the note, which
// the album page shows, or the track gets a file.
func TestSharedPathAlbumStaysOutOfTheSweep(t *testing.T) {
	db := upgradeWith(t, func(db *sql.DB) {
		execAll(t, db,
			`INSERT INTO artists (id, mbid, name, monitored) VALUES (1, 'a1', 'A', 1)`,
			`INSERT INTO albums (id, artist_id, mbid, title, monitored, release_date, year) VALUES (10, 1, 'rg1', 'X', 1, '2000-01-01', 2000)`,
			`INSERT INTO tracks (id, album_id, disc_number, track_number, title, monitored, has_file, file_path, format, bitrate_kbps)
			 VALUES (1, 10, 1, 1, 'One', 1, 1, '/m/A/X/01.flac', 'FLAC', 900),
			        (2, 10, 1, 2, 'Two', 1, 1, '/m/A/X/01.flac', 'FLAC', 900),
			        (3, 10, 1, 3, 'Three', 1, 1, '/m/A/X/03.flac', 'FLAC', 900)`)
	})
	ctx := context.Background()
	svc := music.NewService(db, noMusicBrainz{}, slog.Default())
	inSweep := func() bool {
		t.Helper()
		wanted, err := svc.WantedAlbums(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, al := range wanted {
			if al.ID == 10 {
				return true
			}
		}
		return false
	}

	al, err := svc.GetAlbum(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if al.HaveTracks != 2 || al.TrackCount != 3 || len(al.Notes) != 1 || al.Notes[0].LegacyTrackID != 2 || al.Notes[0].Kind != "shared_path" {
		t.Fatalf("album = %d/%d, notes %+v; want 2/3 with track 2's shared_path note", al.HaveTracks, al.TrackCount, al.Notes)
	}
	if inSweep() {
		t.Fatal("an album with an open upgrade note is in the sweep's list: it would be grabbed again")
	}

	// Resolving it (the person fixed the files or accepts the gap) brings the album back.
	if err := svc.ResolveNote(ctx, 10, al.Notes[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.ResolveNote(ctx, 10, al.Notes[0].ID); err == nil {
		t.Error("resolving a resolved note again should say not found")
	}
	if !inSweep() {
		t.Error("after resolving the note the album should be searched again")
	}
	if al, _ := svc.GetAlbum(ctx, 10); len(al.Notes) != 0 {
		t.Errorf("a resolved note still shows: %+v", al.Notes)
	}

	// A note whose track has since got a file is closed by that, resolved or not.
	execAll(t, db, `UPDATE music_migration_notes SET resolved_at = NULL`)
	if inSweep() {
		t.Fatal("re-opened note: the album should be out of the sweep again")
	}
	if err := music.NewRepo(db).SetTrackFile(ctx, 2, "/m/A/X/02.flac", "FLAC", 900, 1, ""); err != nil {
		t.Fatal(err)
	}
	if notes, _ := music.NewRepo(db).OpenNotes(ctx, 10); len(notes) != 0 {
		t.Errorf("track 2 has its file now, but its note is still open: %+v", notes)
	}
}

// F2: a known listing with no track wanted is complete (nothing left to get), so the sweep
// doesn't grab it; an album with no listing is still never complete.
func TestKnownListingNothingWantedIsComplete(t *testing.T) {
	db := upgradeWith(t, func(db *sql.DB) {
		execAll(t, db,
			`INSERT INTO artists (id, mbid, name, monitored) VALUES (1, 'a1', 'A', 1)`,
			`INSERT INTO albums (id, artist_id, mbid, title, monitored) VALUES (10, 1, 'rg1', 'X', 1), (11, 1, 'rg2', 'Y', 1)`,
			`INSERT INTO tracks (id, album_id, disc_number, track_number, title, monitored, has_file, file_path, format)
			 VALUES (1, 10, 1, 1, 'One', 0, 0, '', ''), (2, 10, 1, 2, 'Two', 0, 1, '/m/2.flac', 'FLAC')`)
	})
	svc := music.NewService(db, noMusicBrainz{}, slog.Default())
	ctx := context.Background()
	al, err := svc.AlbumRecord(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if al.TrackCount != 0 || al.EditionTracks != 2 || !al.Complete() {
		t.Errorf("nothing wanted on a known listing: %d/%d of %d complete=%v, want complete", al.HaveTracks, al.TrackCount, al.EditionTracks, al.Complete())
	}
	if unlisted, _ := svc.AlbumRecord(ctx, 11); unlisted.Complete() {
		t.Error("an album with no listing reads complete")
	}
	a, _ := svc.GetArtist(ctx, 1)
	if a.Stats.AlbumsComplete != 1 || a.Stats.AlbumsWanted != 1 || a.Stats.AlbumsUnlisted != 1 {
		t.Errorf("stats = %+v, want the listed album complete and the unlisted one wanted", *a.Stats)
	}
}

// Values the old table could hold but the old code never wrote: disc 0 and -1, track 0,
// a negative length, a path with no extension or a dot only in a folder, has_file = 2, an
// empty title, an album whose artist is gone. The migration must not fail (a failed
// migration stops the app from booting) and must lose nothing.
func TestOddOldValuesMigrate(t *testing.T) {
	db := upgradeWith(t, func(db *sql.DB) {
		execAll(t, db,
			`INSERT INTO artists (id, mbid, name, monitored) VALUES (1, '', '', 1), (2, 'a2', 'B', 0)`,
			`INSERT INTO albums (id, artist_id, mbid, title, monitored, album_type) VALUES (10, 1, 'rg1', '', 1, 'DJ-mix'), (11, 2, 'rg2', 'Y', 0, '')`,
			`INSERT INTO tracks (id, album_id, disc_number, track_number, title, duration_sec, monitored, has_file, file_path, format)
			 VALUES (1, 10, 0, 0, '', -5, 1, 1, '/m/Mr. Bungle/noext', 'MP3'),
			        (2, 10, 1, 0, 'b', 0, 1, 2, '/m/two', 'MP3'),
			        (3, 10, -1, 3, 'c', 0, 1, 1, '/m/x.FLAC', 'flac'),
			        (4, 11, 1, 1, 'd', 0, 0, 1, '/m/y.mp3', '')`)
		execAll(t, db, `PRAGMA foreign_keys=OFF`,
			`INSERT INTO albums (id, artist_id, mbid, title, monitored) VALUES (12, 77, 'rg3', 'Z', 1)`,
			`INSERT INTO tracks (id, album_id, disc_number, track_number, title, has_file, file_path, format) VALUES (5, 12, 1, 1, 'e', 1, '/m/z.flac', 'FLAC')`,
			`PRAGMA foreign_keys=ON`)
	})
	if n := countOf(t, db, `SELECT COUNT(*) FROM music_track_files`); n != 5 {
		t.Errorf("track files = %d, want all 5 (has_file = 2 counts as a file)", n)
	}
	for path, container := range map[string]string{"/m/Mr. Bungle/noext": "", "/m/two": "", "/m/x.FLAC": "flac", "/m/y.mp3": "mp3"} {
		var got string
		if err := db.QueryRow(`SELECT container FROM music_track_files WHERE path = ?`, path).Scan(&got); err != nil || got != container {
			t.Errorf("%s: container %q (%v), want %q", path, got, err, container)
		}
	}
	if n := countOf(t, db, `SELECT lossless FROM music_track_files WHERE path = '/m/x.FLAC'`); n != 1 {
		t.Error("a lowercase 'flac' tier should still read lossless")
	}
	svc := music.NewService(db, noMusicBrainz{}, slog.Default())
	ctx := context.Background()
	tracks, err := svc.Tracks(ctx, 10)
	if err != nil || len(tracks) != 3 {
		t.Fatalf("album 10's tracks = %+v (%v), want all three", tracks, err)
	}
	if tracks[0].DiscNumber != -1 || tracks[1].DiscNumber != 0 || tracks[0].DurationSec != 0 {
		t.Errorf("odd discs/lengths: %+v", tracks)
	}
	if al, err := svc.AlbumRecord(ctx, 12); err != nil || al.EditionTracks != 1 || al.HaveTracks != 1 {
		t.Errorf("an album whose artist is gone: %+v (%v), want its track and file", al, err)
	}
}

// Deleting migrated artists: rows only, the new tables cleaned up, no dangling reference,
// and an artist nothing credits any more is gone.
func TestDeleteMigratedArtists(t *testing.T) {
	db := upgradeWith(t, func(db *sql.DB) { store.FillOldMusic(t, db) })
	ctx := context.Background()
	repo := music.NewRepo(db)
	if err := repo.DeleteArtist(ctx, 1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for q, want := range map[string]int{
		`SELECT COUNT(*) FROM artists WHERE id = 1`:                                               0,
		`SELECT COUNT(*) FROM albums WHERE artist_id = 1`:                                         0,
		`SELECT COUNT(*) FROM music_releases WHERE album_id IN (10, 11)`:                          0,
		`SELECT COUNT(*) FROM music_track_files WHERE album_id IN (10, 11)`:                       0,
		`SELECT COUNT(*) FROM music_release_tracks WHERE legacy_track_id IN (100, 101, 102, 103)`: 0,
		`SELECT COUNT(*) FROM music_recordings WHERE legacy_track_id IN (100, 101, 102, 103)`:     0,
		`SELECT COUNT(*) FROM music_artist_credits WHERE credit_key = 'mb-radiohead'`:             0,
		`SELECT COUNT(*) FROM artist_events WHERE artist_id = 1`:                                  0,
		`SELECT COUNT(*) FROM music_migration_notes`:                                              5, // an audit trail: kept
		`SELECT COUNT(*) FROM music_track_files WHERE album_id IN (12, 13)`:                       1, // the other artist's untouched
	} {
		if got := countOf(t, db, q); got != want {
			t.Errorf("%s = %d, want %d", q, got, want)
		}
	}
	if err := repo.DeleteArtist(ctx, 2); err != nil {
		t.Fatalf("delete 2: %v", err)
	}
	if n := countOf(t, db, `SELECT COUNT(*) FROM artists`) + countOf(t, db, `SELECT COUNT(*) FROM music_artist_credits`) +
		countOf(t, db, `SELECT COUNT(*) FROM music_recordings`); n != 0 {
		t.Errorf("%d artist, credit or recording rows left after deleting every artist", n)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, parent string
		var rowid sql.NullInt64
		var fk int
		_ = rows.Scan(&table, &rowid, &parent, &fk)
		// The fixture's track of a vanished album (written with foreign keys off) is the
		// only dangling reference allowed: it was there before the upgrade.
		if table != "tracks" {
			t.Errorf("dangling reference: %s row %v -> %s", table, rowid, parent)
		}
	}
}
