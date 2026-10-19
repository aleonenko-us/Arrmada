package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/music"
	"github.com/tristenlammi/arrmada/internal/store"
)

// noMusicBrainz answers nothing: an upgraded library must read back without asking.
type noMusicBrainz struct{}

func (noMusicBrainz) Available() bool { return true }
func (noMusicBrainz) SearchArtists(context.Context, string) ([]metadata.ArtistResult, error) {
	return nil, nil
}
func (noMusicBrainz) GetArtist(context.Context, string) (*metadata.ArtistDetails, error) {
	return nil, fmt.Errorf("%w: test", metadata.ErrMusicBrainzUnavailable)
}
func (noMusicBrainz) ArtistAlbums(context.Context, string) ([]metadata.AlbumResult, error) {
	return nil, nil
}
func (noMusicBrainz) AlbumTracks(context.Context, string) ([]metadata.TrackResult, error) {
	return nil, nil
}

// The read side on a big upgraded library (ARRMADA_MUSIC_MIGRATION_BENCH=1): the library
// list, the sweep's list and one album's page, timed on 1000 artists / 15k albums / 200k
// tracks, so the per-album counts stay cheap enough to run on every page load and sweep.
func TestBigLibraryReads(t *testing.T) {
	if os.Getenv("ARRMADA_MUSIC_MIGRATION_BENCH") != "1" {
		t.Skip("set ARRMADA_MUSIC_MIGRATION_BENCH=1 to time reads on a generated 200k-track library")
	}
	ctx := context.Background()
	db := store.TestDB(t, 4)
	before, all := store.MigrationsBefore(t, store.MusicModelMigration)
	if err := store.RunMigrations(ctx, db, before); err != nil {
		t.Fatal(err)
	}
	store.FillBigOldMusic(t, db)
	if err := store.RunMigrations(ctx, db, all); err != nil {
		t.Fatal(err)
	}
	svc := music.NewService(db, noMusicBrainz{}, slog.Default())
	timed := func(what string, fn func() (int, error)) {
		t.Helper()
		start := time.Now()
		n, err := fn()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		t.Logf("%s: %d row(s) in %s", what, n, time.Since(start).Round(time.Millisecond))
	}
	timed("ListArtists (stats over every album)", func() (int, error) { l, err := svc.ListArtists(ctx); return len(l), err })
	timed("WantedAlbums", func() (int, error) { l, err := svc.WantedAlbums(ctx); return len(l), err })
	timed("GetArtist", func() (int, error) { a, err := svc.GetArtist(ctx, 7); return len(a.Albums), err })
	timed("GetAlbum", func() (int, error) { al, err := svc.GetAlbum(ctx, 7); return len(al.Tracks), err })
}

// oldTrack is one row of the frozen tracks table.
type oldTrack struct {
	id, album                   int64
	disc, number, duration      int
	title, path, format, source string
	monitored, hasFile          bool
	bitrate                     int
	size                        int64
}

// MUS-29's acceptance, end to end: after upgrading a database that has music, the music
// service shows every album with the same tracks and files as before (a file two tracks
// claimed stays with the first and is explained in the album's notes), the same
// completeness or better (an album with no listing is unknown, not complete), and the
// sweep's list is the same albums.
func TestUpgradedLibraryReadsTheSame(t *testing.T) {
	ctx := context.Background()
	db := store.TestDB(t, 1)
	before, all := store.MigrationsBefore(t, store.MusicModelMigration)
	if err := store.RunMigrations(ctx, db, before); err != nil {
		t.Fatal(err)
	}
	store.FillOldMusic(t, db)
	if err := store.RunMigrations(ctx, db, all); err != nil {
		t.Fatal(err)
	}
	svc := music.NewService(db, noMusicBrainz{}, slog.Default())
	repo := music.NewRepo(db)

	rows, err := db.Query(`SELECT t.id, t.album_id, t.disc_number, t.track_number, t.duration_sec, t.title, t.file_path,
	                              t.format, t.source_release, t.monitored, t.has_file, t.bitrate_kbps, t.size_bytes
	                         FROM tracks t JOIN albums al ON al.id = t.album_id ORDER BY t.id`)
	if err != nil {
		t.Fatal(err)
	}
	var old []oldTrack
	for rows.Next() {
		var o oldTrack
		if err := rows.Scan(&o.id, &o.album, &o.disc, &o.number, &o.duration, &o.title, &o.path, &o.format, &o.source,
			&o.monitored, &o.hasFile, &o.bitrate, &o.size); err != nil {
			t.Fatal(err)
		}
		old = append(old, o)
	}
	rows.Close()

	noted := map[int64]bool{}
	byID := map[int64]music.Track{}
	for _, albumID := range []int64{10, 12, 13} {
		tracks, err := svc.Tracks(ctx, albumID)
		if err != nil {
			t.Fatal(err)
		}
		for _, tr := range tracks {
			byID[tr.ID] = tr
		}
		notes, err := repo.MigrationNotes(ctx, albumID)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range notes {
			noted[n.LegacyTrackID] = true
		}
	}
	for _, o := range old {
		tr, ok := byID[o.id]
		if !ok {
			t.Errorf("old track %d (%q) has no track after the upgrade", o.id, o.title)
			continue
		}
		if tr.AlbumID != o.album || tr.DiscNumber != o.disc || tr.TrackNumber != o.number || tr.Title != o.title ||
			tr.Monitored != o.monitored || tr.DurationSec != o.duration {
			t.Errorf("track %d: %+v, want %+v", o.id, tr, o)
		}
		switch {
		case o.hasFile && noted[o.id]:
			if tr.HasFile {
				t.Errorf("track %d: a noted clash still shows a file", o.id)
			}
		case o.hasFile:
			if !tr.HasFile || tr.FilePath != o.path || tr.Format != o.format || tr.BitrateKbps != o.bitrate ||
				tr.SizeBytes != o.size || tr.SourceRelease != o.source {
				t.Errorf("track %d's file: %+v, want %q %s/%d %d bytes from %q", o.id, tr, o.path, o.format, o.bitrate, o.size, o.source)
			}
		default:
			if tr.HasFile {
				t.Errorf("track %d: shows a file it never had", o.id)
			}
		}
	}

	type counts struct{ tracks, have, edition int }
	for id, want := range map[int64]counts{
		10: {3, 3, 4}, // the unmonitored track isn't wanted, so 3 of 3: complete
		11: {0, 0, 0}, // never listed: unknown
		12: {4, 1, 4}, // unmonitored album: all its tracks count
		13: {2, 0, 2},
	} {
		al, err := svc.AlbumRecord(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if al.TrackCount != want.tracks || al.HaveTracks != want.have || al.EditionTracks != want.edition {
			t.Errorf("album %d: %d/%d of %d, want %d/%d of %d", id, al.HaveTracks, al.TrackCount, al.EditionTracks, want.have, want.tracks, want.edition)
		}
		if id == 11 && al.Complete() {
			t.Error("an album with no listing reads as complete")
		}
	}
	full, err := svc.GetAlbum(ctx, 10)
	if err != nil || len(full.Tracks) != 4 || len(full.Editions) != 1 || !full.Editions[0].Legacy || len(full.Discs) != 2 ||
		full.HeldTier != music.QualityUnknown || len(full.Extras) != 0 {
		// The held tier is unknown: one old file is an unmeasured "MP3" at 0 kbps.
		t.Errorf("album 10 in full: %+v (err %v)", full, err)
	}

	artists, err := svc.ListArtists(ctx)
	if err != nil || len(artists) != 2 {
		t.Fatalf("artists = %+v (err %v)", artists, err)
	}
	for _, a := range artists {
		detail, err := svc.GetArtist(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if *detail.Stats != *a.Stats {
			t.Errorf("%s: list stats %+v, detail stats %+v", a.Name, *a.Stats, *detail.Stats)
		}
		switch a.ID {
		case 1:
			if s := a.Stats; s.Albums != 2 || s.AlbumsMonitored != 2 || s.AlbumsComplete != 1 || s.AlbumsUnlisted != 1 || s.AlbumsWanted != 1 {
				t.Errorf("Radiohead's stats: %+v", *s)
			}
		case 2:
			if s := a.Stats; s.Albums != 2 || s.AlbumsMonitored != 1 || s.AlbumsComplete != 0 || s.AlbumsWithFiles != 1 || s.AlbumsWanted != 1 {
				t.Errorf("Scanned Act's stats: %+v", *s)
			}
		}
	}
	wanted, err := svc.WantedAlbums(ctx)
	if err != nil || len(wanted) != 2 || wanted[0].ID != 10 || wanted[1].ID != 11 {
		t.Errorf("wanted albums = %+v (err %v), want Radiohead's two", wanted, err)
	}
	// Ids outside the music tables still resolve.
	var albumOfGrab int64
	if err := db.QueryRow(`SELECT al.id FROM grabs g JOIN albums al ON al.id = g.movie_id WHERE g.id = 1`).Scan(&albumOfGrab); err != nil || albumOfGrab != 10 {
		t.Errorf("grab 1 → album %d (%v), want 10", albumOfGrab, err)
	}
	var none sql.NullInt64
	if err := db.QueryRow(`SELECT target_release_id FROM albums WHERE id = 11`).Scan(&none); err != nil || none.Valid {
		t.Errorf("the unlisted album's target edition = %v (%v), want none", none, err)
	}
}
