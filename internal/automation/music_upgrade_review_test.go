package automation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/music"
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

// The same for a person's own grab of a complete album, even one whose client gave no
// info hash (it is marked by its row); an ordinary grab of an album that reads complete is
// still closed as before, however the album got there.
func TestMusicStallCheckOnACompleteAlbum(t *testing.T) {
	h := musicTestCoord(t)
	ref := musicProfileRef(t, h, "Lossless, or the best MP3", losslessOrMP3, true)
	a := h.addArtist(t, "Radiohead", ref, []string{"OK Computer", "Kid A"}, nil, "1997-05-21")
	okc := holdAlbum(t, h, h.album(t, a.ID, "OK Computer"), "MP3-320", 320, 0)
	h.c.musicGrabFn = func(context.Context, string, string, string, string) (string, error) { return "", nil } // hashless
	if err := h.c.GrabForAlbum(h.ctx, okc.ID, "Test", "magnet:x", "Radiohead - OK Computer (1997) [FLAC]", ""); err != nil {
		t.Fatal(err)
	}
	g := musicGrab(t, h.c, okc.ID)
	if g.InfoHash != "" {
		t.Fatalf("want a hashless grab, got %q", g.InfoHash)
	}
	h.c.detectStalledMusic(h.ctx, g, downloading(g), &stallTick{left: 3})
	if s := grabStatusOf(t, h.c, g.ID); s == grabStatusDismissed {
		t.Fatalf("a person's hashless grab was dismissed by the stall check")
	}

	// Kid A: an ordinary grab, and the album reads complete (here made after the files,
	// as when unwanting a track or an edition switch completes it without a new file).
	kid := holdAlbum(t, h, h.album(t, a.ID, "Kid A"), "FLAC", 0, 0)
	h.c.recordMusicGrab(h.ctx, "music", kid.ID, "Radiohead - Kid A (2000) [FLAC]", "Test", ref, fmt.Sprintf("%040d", 99))
	kg := musicGrab(t, h.c, kid.ID)
	h.c.detectStalledMusic(h.ctx, kg, downloading(kg), &stallTick{left: 3})
	if s := grabStatusOf(t, h.c, kg.ID); s != grabStatusDismissed {
		t.Errorf("an ordinary grab of a complete album = %q, want dismissed as before", s)
	}
}

// insertMusicGrab writes a grab row for an album directly.
func insertMusicGrab(t *testing.T, h *musicHarness, albumID int64, title, status, mode, age string) {
	t.Helper()
	if _, err := h.c.db.Exec(`INSERT INTO grabs (movie_id, title, media_type, info_hash, status, decided_mode, grabbed_at)
		VALUES (?, ?, 'music', '', ?, ?, datetime('now', ?))`, albumID, title, status, mode, age); err != nil {
		t.Fatal(err)
	}
}

// Review fix: a manual grab is refused exactly while a grab for the album is in flight
// (downloading or in Reviews, however old), with words that say which; a release
// dismissed or removed before can be grabbed again.
func TestGrabForAlbumInFlightRules(t *testing.T) {
	title := "Radiohead - OK Computer (1997) [FLAC]"
	for _, tc := range []struct {
		name, status, mode, age, other string
		refused                        string // "" = grabbed
	}{
		{"downloading for two days", grabStatusGrabbed, "", "-2 days", "", "already downloading"},
		{"waiting in Reviews", grabStatusHeld, "", "-40 days", "", "waiting in Reviews"},
		{"dismissed last week", grabStatusDismissed, "", "-7 days", "", ""},
		{"removed yesterday", grabStatusRemoved, "", "-1 day", "", ""},
		{"an upgrade downloading", grabStatusGrabbed, decidedUpgrade, "-1 hour", "Radiohead - OK Computer (1997) [FLAC 24bit]", "an upgrade is already downloading"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := musicTestCoord(t)
			a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
			al := h.album(t, a.ID, "OK Computer")
			existing := title
			if tc.other != "" {
				existing = tc.other
			}
			insertMusicGrab(t, h, al.ID, existing, tc.status, tc.mode, tc.age)
			err := h.c.GrabForAlbum(h.ctx, al.ID, "Test", "magnet:x", title, "")
			if tc.refused == "" {
				if err != nil || len(h.grabs) != 1 {
					t.Fatalf("err %v grabs %v, want it grabbed", err, h.grabs)
				}
				return
			}
			if !errors.Is(err, ErrGrabInFlight) || !strings.Contains(err.Error(), tc.refused) || len(h.grabs) != 0 {
				t.Fatalf("err %v grabs %v, want refused: %q", err, h.grabs, tc.refused)
			}
		})
	}
}

// Review fix: two grabs of one release at once (a double click, two tabs) give one grab.
func TestGrabForAlbumConcurrent(t *testing.T) {
	h := musicTestCoord(t)
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	var mu sync.Mutex
	var grabs int
	h.c.musicGrabFn = func(context.Context, string, string, string, string) (string, error) {
		time.Sleep(50 * time.Millisecond) // the client takes a moment
		mu.Lock()
		defer mu.Unlock()
		grabs++
		return fmt.Sprintf("%040d", grabs), nil
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = h.c.GrabForAlbum(h.ctx, al.ID, "Test", "magnet:x", "Radiohead - OK Computer (1997) [FLAC]", "")
		}(i)
	}
	wg.Wait()
	if grabs != 1 {
		t.Fatalf("grabs %d (errors %v), want exactly one", grabs, errs)
	}
	if (errs[0] == nil) == (errs[1] == nil) || !(errors.Is(errs[0], ErrGrabInFlight) || errors.Is(errs[1], ErrGrabInFlight)) {
		t.Errorf("errors %v, want one grab and one refusal", errs)
	}
}

// Review fix: an artist's Search now searches at most artistSearchCap albums and leaves the
// rest to the sweep; a second click moves on to the albums not searched yet.
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
	first := map[string]bool{}
	for _, q := range h.queries {
		first[q] = true
	}
	h.queries = nil
	if _, err := h.c.SearchArtistNow(h.ctx, a.ID, nil); err != nil {
		t.Fatal(err)
	}
	fresh := 0
	for _, q := range h.queries[:5] {
		if !first[q] {
			fresh++
		}
	}
	if fresh != 5 {
		t.Errorf("the second click's first searches %v, want the 5 albums the first left", h.queries[:5])
	}
}

// Review fix: one indexer paused or failing while others answer doesn't stop the run; no
// indexer answering twice in a row does.
func TestSearchArtistNowAndFailingIndexers(t *testing.T) {
	h := musicTestCoord(t)
	a := h.addArtist(t, "Radiohead", "", []string{"A", "B", "C", "D", "E"}, nil, "2001-01-01")
	h.releases = func(indexer.SearchQuery) (indexer.SearchResult, error) {
		return indexer.SearchResult{Asked: 2, Releases: []indexer.Release{rel("Muse - Absolution (2003) [FLAC]")},
			Errors: map[string]string{"Flaky": "timeout"}, Skipped: map[string]string{"Paused": "backing off"}}, nil
	}
	res, err := h.c.SearchArtistNow(h.ctx, a.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stopped || res.Albums != 5 {
		t.Fatalf("one indexer down: %+v, want all 5 searched", res)
	}

	h2 := musicTestCoord(t)
	a2 := h2.addArtist(t, "Radiohead", "", []string{"A", "B", "C", "D", "E"}, nil, "2001-01-01")
	h2.releases = func(indexer.SearchQuery) (indexer.SearchResult, error) {
		return indexer.SearchResult{Asked: 2, Errors: map[string]string{"One": "timeout", "Two": "refused"}}, nil
	}
	res, err = h2.c.SearchArtistNow(h2.ctx, a2.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Stopped || res.Albums != artistSearchFailStreak || res.Left != 5-artistSearchFailStreak || len(h2.queries) != artistSearchFailStreak {
		t.Fatalf("full outage: %+v (queries %d), want it stopped after %d", res, len(h2.queries), artistSearchFailStreak)
	}
}

// Review fix: a stalled upgrade's replacement waits while another grab for the album is in
// flight, and when the six-hour upgrade budget is used.
func TestReplaceUpgradeHoldsBack(t *testing.T) {
	setup := func(t *testing.T) (*musicHarness, music.Artist, music.Album) {
		h := musicTestCoord(t)
		ref := musicProfileRef(t, h, "Lossless, or the best MP3", losslessOrMP3, true)
		a := h.addArtist(t, "Radiohead", ref, []string{"OK Computer"}, nil, "1997-05-21")
		al := holdAlbum(t, h, h.album(t, a.ID, "OK Computer"), "MP3-320", 320, 0)
		h.releases = found(rel("Radiohead - OK Computer (1997) [FLAC]"))
		return h, a, al
	}
	stalled := map[string]bool{normTitle("Radiohead - OK Computer (1997) [ALAC] stalled"): true}

	h, a, al := setup(t)
	insertMusicGrab(t, h, al.ID, "Radiohead - OK Computer (1997) [ALAC] stalled", grabStatusGrabbed, decidedUpgrade, "-1 hour")
	if out := h.c.replaceUpgrade(h.ctx, a, al, stalled); out.Code != outcomeGrabbed {
		t.Fatalf("only the stalled grab in flight: %+v, want a replacement", out)
	}

	h, a, al = setup(t)
	insertMusicGrab(t, h, al.ID, "Radiohead - OK Computer (1997) [ALAC] stalled", grabStatusGrabbed, decidedUpgrade, "-1 hour")
	insertMusicGrab(t, h, al.ID, "Radiohead - OK Computer (1997) [FLAC 24bit]", grabStatusGrabbed, "", "-1 hour")
	if out := h.c.replaceUpgrade(h.ctx, a, al, stalled); out.Code != "" || len(h.grabs) != 0 {
		t.Fatalf("another grab in flight: %+v grabs %v, want nothing", out, h.grabs)
	}

	h, a, al = setup(t)
	h.c.SetUpgradeBudget(func(context.Context) int { return 2 })
	insertMusicGrab(t, h, al.ID, "Radiohead - OK Computer (1997) [ALAC] stalled", grabStatusFailed, decidedUpgrade, "-1 hour")
	insertMusicGrab(t, h, al.ID+100, "Something else [FLAC]", grabStatusImported, decidedUpgrade, "-2 hours")
	if out := h.c.replaceUpgrade(h.ctx, a, al, stalled); out.Code != "" || len(h.grabs) != 0 {
		t.Fatalf("budget used: %+v grabs %v, want nothing", out, h.grabs)
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
