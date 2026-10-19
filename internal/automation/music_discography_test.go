package automation

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/music"
)

// withMusic gives a stall harness the Music module over its own database, with one artist
// (id 5) and, belonging to someone else, a COMPLETE album that happens to have id 5 too —
// the album a discography grab used to be judged by.
func withMusic(t *testing.T, h *stallHarness) *music.Service {
	t.Helper()
	svc := music.NewService(h.c.db, &fakeMusicProvider{}, slog.Default())
	h.c.music = svc
	mustExec(t, h.c, `INSERT INTO artists (id, mbid, name, monitored) VALUES (5, 'a5', 'Radiohead', 1), (6, 'a6', 'Portishead', 1)`)
	mustExec(t, h.c, `INSERT INTO albums (id, artist_id, mbid, title, monitored) VALUES (5, 6, 'rg5', 'Dummy', 1)`)
	mustExec(t, h.c, `INSERT INTO tracks (album_id, disc_number, track_number, title, has_file, file_path, format)
		VALUES (5, 1, 1, 'Mysterons', 1, '/m/1.flac', 'FLAC')`)
	return svc
}

func artistEvents(t *testing.T, svc *music.Service, artistID int64, substr string) int {
	t.Helper()
	evs, err := svc.Events(t.Context(), artistID, 50)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range evs {
		if strings.Contains(e.Detail, substr) {
			n++
		}
	}
	return n
}

// MUS-07: a discography grab is recorded against the artist, as music_artist.
func TestGrabDiscographyRecordsTheArtist(t *testing.T) {
	h := musicTestCoord(t)
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	h.releases = found(rel("Radiohead - Discography (1993-2016) [FLAC]"))
	if err := h.c.GrabDiscography(h.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	var kind string
	var id int64
	if err := h.c.db.QueryRow(`SELECT media_type, movie_id FROM grabs`).Scan(&kind, &id); err != nil {
		t.Fatal(err)
	}
	if kind != mediaMusicArtist || id != a.ID {
		t.Errorf("grab = (%q, %d), want (%q, %d)", kind, id, mediaMusicArtist, a.ID)
	}
	// The same release isn't grabbed twice while the first is in flight.
	if err := h.c.GrabDiscography(h.ctx, a.ID); err == nil {
		t.Error("a second grab of the same discography should be refused while the first is pending")
	}
	// A block is kept for the artist and keeps the release out.
	h.c.addBlockMusicArtist(h.ctx, a.ID, "Radiohead - Discography (1993-2016) [FLAC]", "Test", "bad")
	set, err := h.c.blockedSetMusicArtist(h.ctx, a.ID)
	if err != nil || !set[normTitle("Radiohead - Discography (1993-2016) [FLAC]")] {
		t.Errorf("artist block set = %v (err %v)", set, err)
	}
}

// MUS-07: a discography is judged on its own download, never by the album that shares the
// artist's id. Here that album is complete, which used to close the grab as 'imported'
// within two minutes (and hand a torrent still downloading to seed cleanup).
func TestDiscographyStallNotJudgedByAnAlbum(t *testing.T) {
	h := newStallHarness(t)
	withMusic(t, h)
	const pack = "Radiohead - Discography (1993-2016) [FLAC]"
	res, err := h.c.db.Exec(`INSERT INTO grabs (movie_id, title, indexer, stall_minutes, media_type, info_hash)
		VALUES (5, ?, 'Fake', 60, ?, ?)`, pack, mediaMusicArtist, hashFor(pack))
	if err != nil {
		t.Fatal(err)
	}
	gid, _ := res.LastInsertId()
	h.qbit.mu.Lock()
	h.qbit.torrents = append(h.qbit.torrents, map[string]any{
		"hash": hashFor(pack), "name": pack, "state": "downloading", "progress": 0.4,
		"size": 8 << 30, "amount_left": 5 << 30, "category": download.CategoryMusic, "num_seeds": 9,
	})
	h.qbit.mu.Unlock()

	h.observe()
	if s := h.status(t, gid); s != grabStatusGrabbed {
		t.Fatalf("status = %q, want still grabbed — album 5 being complete says nothing about the discography", s)
	}
	// Once something from it has been imported, it's imported (and its seed rule applies).
	mustExec(t, h.c, `INSERT INTO imports (download_hash, source_path, target_path, title) VALUES (?, '', '', ?)`, hashFor(pack), pack)
	h.observe()
	if s := h.status(t, gid); s != grabStatusImported {
		t.Errorf("status = %q, want imported once its hash is in imports", s)
	}
}

// MUS-07: a discography that leaves the client twice is blocklisted for the ARTIST, and the
// unrelated album with the same id gets nothing.
func TestDiscographyVanishedTwiceBlockedForTheArtist(t *testing.T) {
	h := newStallHarness(t)
	svc := withMusic(t, h)
	const pack = "Radiohead - Discography (1993-2016) [FLAC]"

	first := h.addGoneGrab(t, mediaMusicArtist, 5, pack, grabStatusGrabbed)
	h.goneForTheGrace()
	if s := h.status(t, first); s != grabStatusFailed {
		t.Fatalf("first: status %q, want failed", s)
	}
	again := h.addGoneGrab(t, mediaMusicArtist, 5, pack, grabStatusGrabbed)
	h.goneForTheGrace()
	if s := h.status(t, again); s != grabStatusFailed {
		t.Fatalf("second: status %q, want failed", s)
	}

	var kind string
	var id int64
	if err := h.c.db.QueryRow(`SELECT media_type, movie_id FROM blocklist WHERE norm_title = ?`, normTitle(pack)).Scan(&kind, &id); err != nil {
		t.Fatalf("not blocklisted: %v", err)
	}
	if kind != mediaMusicArtist || id != 5 {
		t.Errorf("block = (%q, %d), want (music_artist, 5)", kind, id)
	}
	if set, _ := h.c.blockedSetMusic(h.ctx, 5); set[normTitle(pack)] {
		t.Error("the album sharing the artist's id must not inherit the block")
	}
	if n := artistEvents(t, svc, 5, "Discography: "+pack+" disappeared from the download client"); n != 2 {
		t.Errorf("artist history lines = %d, want 2", n)
	}
	if n := artistEvents(t, svc, 6, pack); n != 0 {
		t.Errorf("the other artist got %d history lines about it", n)
	}
}
