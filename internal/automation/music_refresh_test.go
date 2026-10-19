package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/music"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/store"
)

// makeDue puts every artist's last refresh a day back, so the next run refreshes them.
func makeDue(t *testing.T, st *store.Store) {
	t.Helper()
	if _, err := st.DB().Exec(`UPDATE artists SET last_refreshed_at = datetime('now', '-1 day')`); err != nil {
		t.Fatal(err)
	}
}

// MUS-13: a new release a monitored artist puts out is found without anyone pressing
// Refresh, written to the history, and searched exactly once; one announced for later is
// wanted but not searched before its day. A second run straight after changes nothing.
func TestRefreshMusicArtistsSearchesNewReleasesOnce(t *testing.T) {
	h := musicTestCoord(t)
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	// The add searched nothing; the album is wanted but the sweep isn't run here.
	makeDue(t, h.st)

	mbid := "artist-Radiohead"
	out := time.Now().AddDate(0, 0, -3).Format("2006-01-02")
	soon := time.Now().AddDate(0, 0, 30).Format("2006-01-02")
	h.fake.mu.Lock()
	h.fake.albums[mbid] = append(h.fake.albums[mbid],
		metadata.AlbumResult{MBID: "rg-out", Title: "Out Now", Type: "Album", ReleaseDate: out},
		metadata.AlbumResult{MBID: "rg-soon", Title: "Coming Soon", Type: "Album", ReleaseDate: soon})
	h.fake.tracks["rg-out"] = []metadata.TrackResult{{Title: "One", Disc: 1, Number: 1}}
	h.fake.tracks["rg-soon"] = []metadata.TrackResult{{Title: "Two", Disc: 1, Number: 1}}
	h.fake.mu.Unlock()

	rep, err := h.c.RefreshMusicArtists(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Artists != 1 || rep.Added != 2 || rep.Wanted != 2 || rep.Searched != 1 {
		t.Fatalf("report %+v, want 1 artist, 2 added and wanted, 1 searched", rep)
	}
	if len(h.queries) != 1 || !strings.Contains(h.queries[0], "Out Now") {
		t.Errorf("searches = %q, want one for Out Now", h.queries)
	}
	if al := h.album(t, a.ID, "Coming Soon"); !al.Monitored {
		t.Error("the announced album isn't wanted")
	}
	evs, _ := h.svc.Events(h.ctx, a.ID, 20)
	found := false
	for _, e := range evs {
		found = found || strings.HasPrefix(e.Detail, "New release: Out Now (Album, ")
	}
	if !found {
		t.Errorf("no new-release event: %+v", evs)
	}

	rep, err = h.c.RefreshMusicArtists(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Artists != 0 || len(h.queries) != 1 {
		t.Errorf("second run refreshed %d artists and searched %d times in all, want 0 and still 1", rep.Artists, len(h.queries))
	}
}

// The listing fill stops at its cap per run, and an album MusicBrainz has nothing for
// waits a day instead of taking a slot every run.
func TestRefreshMusicArtistsListingFillIsCapped(t *testing.T) {
	h := musicTestCoord(t)
	titles := make([]string, musicListingFills+5)
	noListing := map[string]bool{}
	for i := range titles {
		titles[i] = fmt.Sprintf("Album %02d", i)
		noListing[titles[i]] = true
	}
	h.addArtist(t, "Prolific", "", titles, noListing, "2001-01-01")
	before := h.fake.listing
	if _, err := h.c.RefreshMusicArtists(h.ctx); err != nil {
		t.Fatal(err)
	}
	if got := h.fake.listing - before; got != musicListingFills {
		t.Fatalf("first run asked for %d listings, want the cap %d", got, musicListingFills)
	}
	before = h.fake.listing
	if _, err := h.c.RefreshMusicArtists(h.ctx); err != nil {
		t.Fatal(err)
	}
	if got := h.fake.listing - before; got != 5 {
		t.Errorf("second run asked for %d listings, want the 5 left (the others wait a day)", got)
	}
}

// mbStubServer is a MusicBrainz stand-in over HTTP for the real client: every artist has
// albums release groups, each with one CD release of ten tracks. It counts requests.
type mbStubServer struct {
	srv      *httptest.Server
	requests atomic.Int64
	albums   int
	down     atomic.Bool // answer every request with a 500
}

func newMBStub(t testing.TB, albumsPerArtist int) *mbStubServer {
	s := &mbStubServer{albums: albumsPerArtist}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *mbStubServer) handle(w http.ResponseWriter, r *http.Request) {
	s.requests.Add(1)
	if s.down.Load() {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/ws/2")
	q := r.URL.Query()
	var body any
	switch {
	case strings.HasPrefix(p, "/artist/"):
		id := strings.TrimPrefix(p, "/artist/")
		body = map[string]any{"id": id, "name": "Artist " + id, "sort-name": id}
	case p == "/release-group" && q.Get("type") == "album|ep":
		var rgs []map[string]any
		if q.Get("offset") == "0" {
			for i := 0; i < s.albums; i++ {
				rgs = append(rgs, map[string]any{"id": fmt.Sprintf("%s-rg-%d", q.Get("artist"), i), "title": fmt.Sprintf("Album %d", i),
					"primary-type": "Album", "first-release-date": fmt.Sprintf("%d-01-01", 1990+i)})
			}
		}
		body = map[string]any{"release-groups": rgs, "release-group-count": len(rgs)}
	case p == "/release-group":
		body = map[string]any{"release-groups": []any{}, "release-group-count": 0}
	case p == "/release" && q.Get("release-group") != "":
		rg := q.Get("release-group")
		body = map[string]any{"release-count": 1, "releases": []any{map[string]any{
			"id": "rel-" + rg, "title": "Album", "status": "Official", "date": "2001-01-01", "country": "GB",
			"media": []any{map[string]any{"position": 1, "format": "CD", "track-count": 10}},
		}}}
	case strings.HasPrefix(p, "/release/"):
		id := strings.TrimPrefix(p, "/release/")
		var tracks []any
		for i := 1; i <= 10; i++ {
			tracks = append(tracks, map[string]any{"id": fmt.Sprintf("%s-t%d", id, i), "title": fmt.Sprintf("Track %d", i),
				"number": fmt.Sprint(i), "position": i, "length": 200000, "recording": map[string]any{"id": fmt.Sprintf("%s-r%d", id, i)}})
		}
		body = map[string]any{"id": id, "title": "Album", "status": "Official", "date": "2001-01-01",
			"media": []any{map[string]any{"position": 1, "format": "CD", "track-count": 10, "tracks": tracks}}}
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Not Found"}`))
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}

// realMusicCoord is a coordinator whose music service talks to the stub through the real
// MusicBrainz client (cache, limiter and all), at a mirror's pace so the test doesn't
// wait a second a call.
func realMusicCoord(t testing.TB, stub *mbStubServer) (*Coordinator, *music.Service, *store.Store) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	mb := metadata.NewMusicBrainz(st.DB(), func() metadata.MBConfig {
		return metadata.MBConfig{BaseURL: stub.srv.URL, MirrorRate: metadata.MBMaxMirrorRate}
	})
	svc := music.NewService(st.DB(), mb, slog.New(slog.NewTextHandler(ioDiscard{}, nil)))
	c := &Coordinator{music: svc, quality: quality.NewService(st.DB()), db: st.DB(), log: slog.New(slog.NewTextHandler(ioDiscard{}, nil))}
	return c, svc, st
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }

// seedArtists puts n artists in the library as an upgrade leaves them: never refreshed,
// nothing seen. The stub's albums are all years old, so the first look lists them without
// wanting them, and the listing fill (monitored albums only) stays out of the measurement.
func seedArtists(t testing.TB, st *store.Store, n int) {
	repo := music.NewRepo(st.DB())
	for i := 0; i < n; i++ {
		if _, err := repo.CreateArtist(context.Background(), music.Artist{MBID: fmt.Sprintf("a%04d", i), Name: fmt.Sprintf("Artist %d", i), Monitored: true}); err != nil {
			t.Fatal(err)
		}
	}
}

// One run never sends more MusicBrainz requests than its budget (give or take the one
// artist in hand), takes the never-refreshed artists first, and the next run carries on
// with the rest; a run over fresh answers costs nothing.
func TestRefreshMusicArtistsKeepsToItsCallBudget(t *testing.T) {
	stub := newMBStub(t, 3)
	c, svc, st := realMusicCoord(t, stub)
	seedArtists(t, st, 12)
	c.musicRefreshBudget = 20
	ctx := context.Background()
	// An artist refreshed already, with a wanted album that has no listing yet: the
	// follow-up step's work.
	repo := music.NewRepo(st.DB())
	filler, err := repo.CreateArtist(ctx, music.Artist{MBID: "fill", Name: "Filler", Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.StampRefreshed(ctx, filler.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpsertAlbum(ctx, music.Album{ArtistID: filler.ID, MBID: "fill-rg-0", Title: "Wanted", Monitored: true}); err != nil {
		t.Fatal(err)
	}

	rep, err := c.RefreshMusicArtists(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Two requests an artist (the artist, its album list): the artists' three quarters of
	// the budget (15) is used up by eight; the rest is kept for the follow-up steps, and
	// the listing fill gets it (the edition list and the edition's tracks).
	if rep.Artists != 8 || rep.Stopped != "artist_share" || rep.Listings != 1 || rep.Calls != 18 {
		t.Fatalf("run 1: %+v, want 8 artists, the listing filled, 18 calls, stopped at the artists' share", rep)
	}
	due, _ := svc.ArtistsDueForRefresh(ctx, musicRefreshEvery, 100)
	if len(due) != 4 || due[0].MBID != "a0008" {
		t.Fatalf("after run 1, %d due starting %q; want the 4 not yet refreshed, in order", len(due), firstMBID(due))
	}
	for run := 2; run <= 3; run++ {
		if _, err := c.RefreshMusicArtists(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if due, _ := svc.ArtistsDueForRefresh(ctx, musicRefreshEvery, 100); len(due) != 0 {
		t.Fatalf("%d artists still due after three runs", len(due))
	}
	// Every answer is fresh now: making them all due again costs no request.
	if _, err := st.DB().Exec(`UPDATE artists SET last_refreshed_at = datetime('now', '-1 day') WHERE mbid != 'fill'`); err != nil {
		t.Fatal(err)
	}
	before := stub.requests.Load()
	c.musicRefreshBudget = 0
	rep, err = c.RefreshMusicArtists(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Artists != 12 || rep.Calls != 0 || stub.requests.Load() != before {
		t.Errorf("a run over fresh answers: %+v and %d requests, want 12 artists and none", rep, stub.requests.Load()-before)
	}
}

// With MusicBrainz down, a run gives up after a few artists rather than waiting out
// every request, and changes nothing: no album added or removed, nobody stamped as
// refreshed beyond those it tried.
func TestRefreshMusicArtistsStopsWhenMusicBrainzIsDown(t *testing.T) {
	stub := newMBStub(t, 3)
	c, svc, st := realMusicCoord(t, stub)
	seedArtists(t, st, 10)
	stub.down.Store(true)
	rep, err := c.RefreshMusicArtists(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Artists != musicUnreachableStop || rep.Stopped != "musicbrainz_unreachable" || rep.Unanswered != rep.Artists || rep.Added != 0 {
		t.Fatalf("report %+v, want %d artists tried, all unanswered, then a stop", rep, musicUnreachableStop)
	}
	if n := stub.requests.Load(); n > int64(2*musicUnreachableStop) {
		t.Errorf("%d requests, want at most two an artist tried", n)
	}
	var albums int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM albums`).Scan(&albums); err != nil || albums != 0 {
		t.Errorf("%d albums after an outage (%v), want none", albums, err)
	}
	if due, _ := svc.ArtistsDueForRefresh(context.Background(), musicRefreshEvery, 100); len(due) != 10-musicUnreachableStop {
		t.Errorf("%d artists still due, want the %d not tried", len(due), 10-musicUnreachableStop)
	}
}

func firstMBID(as []music.Artist) string {
	if len(as) == 0 {
		return ""
	}
	return as[0].MBID
}

// TestMusicRefreshCallCounts measures what refresh-music-artists asks of MusicBrainz for a
// 1,000-artist library (15 albums each): an upgraded library's first runs with an empty
// cache, a run over fresh answers, and runs once the album lists (3 days) and then the
// artists (7 days) go stale. Gated: ARRMADA_MUSIC_REFRESH_BENCH=1 (it takes a minute or two
// at a mirror's pace).
func TestMusicRefreshCallCounts(t *testing.T) {
	if os.Getenv("ARRMADA_MUSIC_REFRESH_BENCH") != "1" {
		t.Skip("set ARRMADA_MUSIC_REFRESH_BENCH=1 to measure")
	}
	const artists = 1000
	stub := newMBStub(t, 15)
	c, _, st := realMusicCoord(t, stub)
	seedArtists(t, st, artists)
	ctx := context.Background()

	cycle := func(label string) {
		t.Helper()
		start := stub.requests.Load()
		runs, refreshed := 0, 0
		for {
			rep, err := c.RefreshMusicArtists(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if rep.Artists == 0 {
				break
			}
			runs++
			refreshed += rep.Artists
			t.Logf("%s run %d: %d artists, %d calls, stopped=%q", label, runs, rep.Artists, rep.Calls, rep.Stopped)
		}
		total := stub.requests.Load() - start
		t.Logf("%s: %d artists refreshed in %d runs, %d MusicBrainz requests (%.2f per artist)",
			label, refreshed, runs, total, float64(total)/float64(max(refreshed, 1)))
	}
	age := func(d time.Duration) {
		if _, err := st.DB().Exec(`UPDATE music_mb_cache SET checked_at = checked_at - ?`, d.Milliseconds()); err != nil {
			t.Fatal(err)
		}
		makeDue(t, st)
	}

	cycle("cold cache")
	makeDue(t, st)
	cycle("fresh cache")
	age(4 * 24 * time.Hour)
	cycle("album lists stale (day 4)")
	age(4 * 24 * time.Hour)
	cycle("album lists and artists stale (day 8)")
}
