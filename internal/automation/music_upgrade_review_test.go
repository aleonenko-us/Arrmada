package automation

import (
	"errors"
	"fmt"
	"testing"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/indexer"
)

// musicGrab is the in-flight music grab for album id (the stall check's view of it).
func musicGrab(t *testing.T, c *Coordinator, albumID int64) grab {
	t.Helper()
	pending, err := c.pendingGrabs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range pending {
		if g.MediaType == "music" && g.MovieID == albumID {
			return g
		}
	}
	t.Fatalf("no in-flight grab for album %d in %+v", albumID, pending)
	return grab{}
}

func grabStatusOf(t *testing.T, c *Coordinator, id int64) string {
	t.Helper()
	var s string
	if err := c.db.QueryRow(`SELECT status FROM grabs WHERE id = ?`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// downloading is the queue with g still downloading.
func downloading(g grab) []download.Item {
	return []download.Item{{Hash: g.InfoHash, Name: g.Title, State: "downloading", Progress: 0.1, Category: musicCategory}}
}

// Review fix: an upgrade grab of a complete album is a download meant for a complete album.
// The stall check judged it "another download completed the album" and closed it as
// dismissed two minutes in: its files kept for ever, a second upgrade grabbed beside it,
// and its import lost. Now it stays in flight, imports, and closes as imported.
func TestMusicUpgradeGrabSurvivesTheStallCheck(t *testing.T) {
	files := trackFileNames(okcTitles)
	r, held := upgradeRig(t, files, false)
	r.tagAll(files, okcTitles)
	g := musicGrab(t, r.h.c, r.album.ID)

	r.h.c.detectStalledMusic(r.h.ctx, g, downloading(g), &stallTick{left: 3})
	if s := grabStatusOf(t, r.h.c, g.ID); s != grabStatusGrabbed {
		t.Fatalf("an upgrade still downloading was closed as %q by the stall check", s)
	}
	// While it downloads, Search now grabs nothing beside it.
	out, err := r.h.c.SearchAlbumNow(r.h.ctx, r.album.ID)
	if err != nil || out.Reason != ReasonAlreadyDownloading {
		t.Fatalf("Search now during the upgrade: %+v %v, want already downloading", out, err)
	}

	r.h.c.ImportMusicDownloads(r.h.ctx)
	if s := r.grabStatus(t); s != grabStatusImported {
		t.Fatalf("after the import the grab is %q, want imported", s)
	}
	if n := countFiles(t, r.bin); n < len(held) {
		t.Errorf("recycle bin holds %d files, want the %d replaced MP3s", n, len(held))
	}
}

// The same for a person's own grab of a complete album; an ordinary grab made before
// another download completed the album is still closed as before.
func TestMusicStallCheckOnACompleteAlbum(t *testing.T) {
	h := musicTestCoord(t)
	ref := musicProfileRef(t, h, "Lossless, or the best MP3", losslessOrMP3, true)
	a := h.addArtist(t, "Radiohead", ref, []string{"OK Computer", "Kid A"}, nil, "1997-05-21")
	okc := holdAlbum(t, h, h.album(t, a.ID, "OK Computer"), "MP3-320", 320, 0)
	if err := h.c.GrabForAlbum(h.ctx, okc.ID, "Test", "magnet:x", "Radiohead - OK Computer (1997) [FLAC]"); err != nil {
		t.Fatal(err)
	}
	g := musicGrab(t, h.c, okc.ID)
	h.c.detectStalledMusic(h.ctx, g, downloading(g), &stallTick{left: 3})
	if s := grabStatusOf(t, h.c, g.ID); s != grabStatusGrabbed {
		t.Fatalf("a person's grab still downloading was closed as %q", s)
	}

	// Kid A: grabbed while missing, then completed by another download.
	kid := h.album(t, a.ID, "Kid A")
	h.c.recordMusicGrab(h.ctx, "music", kid.ID, "Radiohead - Kid A (2000) [FLAC]", "Test", ref, fmt.Sprintf("%040d", 99))
	if _, err := h.c.db.Exec(`UPDATE grabs SET grabbed_at = datetime('now', '-1 hour') WHERE movie_id = ?`, kid.ID); err != nil {
		t.Fatal(err)
	}
	holdAlbum(t, h, kid, "FLAC", 0, 0)
	kg := musicGrab(t, h.c, kid.ID)
	h.c.detectStalledMusic(h.ctx, kg, downloading(kg), &stallTick{left: 3})
	if s := grabStatusOf(t, h.c, kg.ID); s != grabStatusDismissed {
		t.Errorf("a grab overtaken by another download = %q, want dismissed as before", s)
	}
}

// Review fix: a manual grab of a release already on its way for the album is refused.
func TestGrabForAlbumRefusesARepeat(t *testing.T) {
	h := musicTestCoord(t)
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	title := "Radiohead - OK Computer (1997) [FLAC]"
	if err := h.c.GrabForAlbum(h.ctx, al.ID, "Test", "magnet:x", title); err != nil {
		t.Fatal(err)
	}
	if err := h.c.GrabForAlbum(h.ctx, al.ID, "Test", "magnet:x", title); !errors.Is(err, ErrGrabInFlight) {
		t.Fatalf("second grab: %v, want ErrGrabInFlight", err)
	}
	if len(h.grabs) != 1 {
		t.Errorf("grabs %v, want one", h.grabs)
	}
}

// Review fix: an artist's Search now searches at most artistSearchCap albums and leaves the
// rest to the sweep, and stops when indexers keep failing.
func TestSearchArtistNowIsCapped(t *testing.T) {
	h := musicTestCoord(t)
	var titles []string
	for i := 0; i < artistSearchCap+5; i++ {
		titles = append(titles, fmt.Sprintf("Album %02d", i))
	}
	a := h.addArtist(t, "Prolific", "", titles, nil, "2001-01-01")
	res, err := h.c.SearchArtistNow(h.ctx, a.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Albums != artistSearchCap || res.Left != 5 || len(h.queries) != artistSearchCap {
		t.Fatalf("searched %d (queries %d), left %d; want %d searched and 5 left", res.Albums, len(h.queries), res.Left, artistSearchCap)
	}
}

func TestSearchArtistNowStopsOnFailingIndexers(t *testing.T) {
	h := musicTestCoord(t)
	a := h.addArtist(t, "Radiohead", "", []string{"A", "B", "C", "D", "E"}, nil, "2001-01-01")
	h.releases = func(indexer.SearchQuery) (indexer.SearchResult, error) {
		// One of two indexers times out on every search.
		return indexer.SearchResult{Asked: 2, Errors: map[string]string{"Flaky": "timeout"}}, nil
	}
	res, err := h.c.SearchArtistNow(h.ctx, a.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Stopped || res.Albums != artistSearchFailStreak || res.Left != 5-artistSearchFailStreak {
		t.Fatalf("result %+v, want it stopped after %d searches", res, artistSearchFailStreak)
	}
	if len(h.queries) != artistSearchFailStreak {
		t.Errorf("queries %d, want %d", len(h.queries), artistSearchFailStreak)
	}
}

// Review fix: the upgrade's re-grab guard reads imported grabs only, by info hash where it
// is known, for 90 days.
func TestImportedReleasesGuard(t *testing.T) {
	h := musicTestCoord(t)
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	for _, q := range []string{
		`INSERT INTO grabs (movie_id, title, media_type, info_hash, status) VALUES (?, 'Hashed [FLAC]', 'music', 'AAA', 'imported')`,
		`INSERT INTO grabs (movie_id, title, media_type, info_hash, status) VALUES (?, 'Hashless [FLAC]', 'music', '', 'seeded')`,
		`INSERT INTO grabs (movie_id, title, media_type, info_hash, status) VALUES (?, 'Failed [FLAC]', 'music', 'BBB', 'failed')`,
		`INSERT INTO grabs (movie_id, title, media_type, info_hash, status, grabbed_at) VALUES (?, 'Old [FLAC]', 'music', 'CCC', 'imported', datetime('now', '-120 days'))`,
	} {
		if _, err := h.c.db.Exec(q, al.ID); err != nil {
			t.Fatal(err)
		}
	}
	ir, err := h.c.musicImportedReleases(h.ctx, al.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		title, hash string
		want        bool
	}{
		{"Hashed [FLAC]", "aaa", true},   // same torrent
		{"Hashed [FLAC]", "ddd", false},  // same name, another torrent
		{"Hashed [FLAC]", "", true},      // no hash to tell by: the name
		{"Hashless [FLAC]", "eee", true}, // the grab knew no hash: the name
		{"Failed [FLAC]", "bbb", false},  // failures are the blocklist's
		{"Old [FLAC]", "ccc", false},     // long ago
		{"Something else", "fff", false},
	} {
		if got := ir.has(indexer.Release{Title: tc.title, InfoHash: tc.hash}); got != tc.want {
			t.Errorf("%s/%s: %v, want %v", tc.title, tc.hash, got, tc.want)
		}
	}
}
