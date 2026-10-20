package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/music"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/requests"
	"github.com/tristenlammi/arrmada/internal/series"
	"github.com/tristenlammi/arrmada/internal/settings"
)

// musicServer is a route server with the libraries, requests and Music (never the
// network: no metadata provider). Music starts off, as on a fresh install.
func musicServer(t *testing.T) *routeServer {
	t.Helper()
	root := t.TempDir()
	return newRouteServer(t, func(d *Deps) {
		db := d.Store.DB()
		d.Books = books.NewService(db, nil, d.Log)
		d.Quality = quality.NewService(db)
		d.Movies = movies.NewService(db, nil, nil, root, "", nil, d.Log)
		d.Series = series.NewService(db, nil, root, d.Log)
		d.Requests = requests.NewService(db, d.Movies, d.Series, d.Books, nil, d.Quality, nil, "", d.Log)
		d.Music = music.NewService(db, nil, d.Log)
	})
}

func setMusic(t *testing.T, s *routeServer, on bool) {
	t.Helper()
	if err := s.deps.Settings.SetBool(context.Background(), settings.KeyModuleMusic, on); err != nil {
		t.Fatal(err)
	}
}

// musicAlbum adds an album with a listing of n tracks, the first have of them on disk
// (size 100 each).
func musicAlbum(t *testing.T, s *routeServer, al music.Album, n, have int) int64 {
	t.Helper()
	ctx := context.Background()
	repo := music.NewRepo(s.st.DB())
	id, err := repo.UpsertAlbum(ctx, al)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		return id
	}
	tracks := make([]music.Track, n)
	for i := range tracks {
		tracks[i] = music.Track{DiscNumber: 1, TrackNumber: i + 1, Title: "Track"}
	}
	if err := repo.UpsertTracks(ctx, id, tracks); err != nil {
		t.Fatal(err)
	}
	listed, err := repo.TracksFor(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < have; i++ {
		path := "/music/" + al.MBID + "/" + string(rune('a'+i)) + ".flac"
		if err := repo.SetTrackFile(ctx, listed[i].ID, path, "FLAC", 900, 100, "rel"); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func musicArtist(t *testing.T, s *routeServer, mbid, name string, monitored bool) int64 {
	t.Helper()
	a, err := music.NewRepo(s.st.DB()).CreateArtist(context.Background(), music.Artist{MBID: mbid, Name: name, Monitored: monitored})
	if err != nil {
		t.Fatal(err)
	}
	return a.ID
}

type dashBody struct {
	Library map[string]any  `json:"library"`
	Music   *dashboardMusic `json:"music"`
	Act     []activityEvent `json:"activity"`
}

func getDashboard(t *testing.T, s *routeServer, c *http.Cookie) (dashBody, string) {
	t.Helper()
	rec := s.do("GET", "/api/v1/dashboard", c)
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard: HTTP %d %s", rec.Code, rec.Body)
	}
	var b dashBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	return b, rec.Body.String()
}

// MUS-24: with Music on, the Dashboard carries the music counts (the Wanted page's own
// rules for wanted and missing), the newest New releases and music imports, and music in
// Recent activity. With it off there is no music at all, not even zeros.
func TestDashboardMusicOnlyWhenOn(t *testing.T) {
	s := musicServer(t)
	_, mgr := s.user(t, "mgr", auth.RoleManager)
	low := musicArtist(t, s, "a-low", "Low", true)
	paused := musicArtist(t, s, "a-paused", "Paused Band", false)
	mustExecAll(t, s, `INSERT INTO artists (mbid, name, in_library) VALUES ('a-guest', 'Guest', 0)`)
	now := time.Now().UTC().Format("2006-01-02 15:04:05")

	musicAlbum(t, s, music.Album{ArtistID: low, MBID: "rg-done", Title: "Done", Year: 2020, Monitored: true}, 2, 2)
	musicAlbum(t, s, music.Album{ArtistID: low, MBID: "rg-half", Title: "Half", Year: 2021, Monitored: true}, 2, 1)
	musicAlbum(t, s, music.Album{ArtistID: low, MBID: "rg-soon", Title: "Soon", ReleaseDate: "2099-01-01", Year: 2099, Monitored: true,
		FoundAt: now, FoundAs: music.FoundNew}, 0, 0)
	musicAlbum(t, s, music.Album{ArtistID: low, MBID: "rg-off", Title: "Not Wanted", Year: 2022, FoundAt: now, FoundAs: music.FoundNotNew}, 0, 0)
	musicAlbum(t, s, music.Album{ArtistID: paused, MBID: "rg-paused", Title: "Paused Album", Year: 2019, Monitored: true}, 0, 0)
	if _, err := s.st.DB().Exec(`INSERT INTO artist_events (artist_id, event, detail) VALUES (?1, 'imported', 'Imported Done'), (?1, 'grabbed', 'Half')`, low); err != nil {
		t.Fatal(err)
	}
	mustExecAll(t, s,
		`INSERT INTO movies (id, tmdb_id, title) VALUES (1, 10, 'Dune')`,
		`INSERT INTO movie_events (movie_id, event, detail) VALUES (1, 'grabbed', '2160p')`,
	)

	off, raw := getDashboard(t, s, mgr)
	if off.Music != nil || strings.Contains(raw, `"music"`) {
		t.Errorf("Music is off, but the dashboard carries it: %s", raw)
	}
	if _, ok := off.Library["artists"]; ok {
		t.Errorf("library still counts artists with Music off: %v", off.Library)
	}
	for _, e := range off.Act {
		if e.Kind == "music" {
			t.Errorf("a music event in Recent activity with Music off: %+v", e)
		}
	}
	if len(off.Act) != 1 {
		t.Errorf("activity = %+v, want the movie's event", off.Act)
	}

	setMusic(t, s, true)
	on, _ := getDashboard(t, s, mgr)
	m := on.Music
	if m == nil {
		t.Fatal("Music is on, but the dashboard has no music")
	}
	want := musicCounts{Artists: 2, Albums: 2, Tracks: 3, SizeBytes: 300, AlbumsWanted: 2, AlbumsMissing: 1}
	if m.musicCounts != want {
		t.Errorf("counts = %+v\nwant     %+v", m.musicCounts, want)
	}
	if m.NewReleasesTotal != 2 || len(m.NewReleases) != 2 {
		t.Fatalf("new releases = %d %+v, want both found albums", m.NewReleasesTotal, m.NewReleases)
	}
	for _, nr := range m.NewReleases {
		if nr.ArtistName != "Low" || (nr.Title == "Soon") != nr.Monitored {
			t.Errorf("new release %+v", nr)
		}
	}
	if len(m.Imports) != 1 || m.Imports[0].Title != "Low" || m.Imports[0].Event != "imported" || m.Imports[0].Kind != "music" {
		t.Errorf("imports = %+v, want Low's one import (not the grab)", m.Imports)
	}
	musicEvents := 0
	for _, e := range on.Act {
		if e.Kind == "music" {
			musicEvents++
		}
	}
	if musicEvents != 2 {
		t.Errorf("activity = %+v, want both music events with Music on", on.Act)
	}
}

// calendarAlbumsSeed dates four albums inside October 2026: one wanted, one of a paused
// artist, one not wanted, and one by an artist the adult filter catches.
func calendarAlbumsSeed(t *testing.T, s *routeServer, dates [4]string) {
	t.Helper()
	low := musicArtist(t, s, "a-low", "Low", true)
	paused := musicArtist(t, s, "a-paused", "Paused Band", false)
	adult := musicArtist(t, s, "a-adult", "Hardcore Porn Band", true)
	musicAlbum(t, s, music.Album{ArtistID: low, MBID: "rg-soon", Title: "Soon", AlbumType: "EP", ReleaseDate: dates[0], Monitored: true}, 0, 0)
	musicAlbum(t, s, music.Album{ArtistID: paused, MBID: "rg-paused", Title: "Paused Album", ReleaseDate: dates[1], Monitored: true}, 0, 0)
	musicAlbum(t, s, music.Album{ArtistID: low, MBID: "rg-skip", Title: "Not Wanted", ReleaseDate: dates[2]}, 0, 0)
	musicAlbum(t, s, music.Album{ArtistID: adult, MBID: "rg-adult", Title: "Innocent Name", ReleaseDate: dates[3], Monitored: true}, 0, 0)
}

// MUS-24: album releases are on staff's Calendar while Music is on, with the words the
// episodes use (a paused artist's wanted album is Searching off; an unwanted one isn't
// monitored); never on a requester's, and never while Music is off.
func TestCalendarAlbumsStaffOnly(t *testing.T) {
	s := musicServer(t)
	seedCalendar(t, s)
	calendarAlbumsSeed(t, s, [4]string{"2026-10-15", "2026-10-16", "2026-10-17", "2026-10-18"})
	_, kid := s.user(t, "kid", auth.RoleRequester)
	_, mgr := s.user(t, "mgr", auth.RoleManager)
	const window = "/api/v1/calendar?start=2026-10-01&end=2026-10-31"
	albums := func(c *http.Cookie, path string) map[string]CalendarItem {
		t.Helper()
		out := map[string]CalendarItem{}
		for _, it := range getCalendar(t, s, path, c).Items {
			if it.Type == "album" {
				out[it.Title] = it
			}
		}
		return out
	}

	if got := albums(mgr, window); len(got) != 0 {
		t.Errorf("Music is off, but staff see albums: %+v", got)
	}
	setMusic(t, s, true)
	if got := albums(kid, window); len(got) != 0 {
		t.Errorf("a requester sees albums: %+v", got)
	}
	if got := len(getCalendar(t, s, window, kid).Items); got != 4 {
		t.Errorf("requester items = %d, want the 4 episodes and movies as before", got)
	}

	got := albums(mgr, window)
	if len(got) != 3 {
		t.Fatalf("staff albums = %+v, want three (the adult one filtered)", got)
	}
	soon := got["Soon"]
	if soon.Date != "2026-10-15" || soon.MediaType != "music" || soon.Artist != "Low" || soon.AlbumType != "EP" ||
		!soon.Monitored || soon.SearchingOff || soon.HasFile || soon.RefID == 0 || soon.RequestedByMe {
		t.Errorf("wanted album = %+v", soon)
	}
	if p := got["Paused Album"]; p.Monitored || !p.SearchingOff {
		t.Errorf("a paused artist's wanted album = %+v, want searching off", p)
	}
	if n := got["Not Wanted"]; n.Monitored || n.SearchingOff {
		t.Errorf("an unwanted album = %+v, want neither monitored nor searching off", n)
	}
	if _, ok := got["Innocent Name"]; ok {
		t.Error("an adult artist's album reached the calendar")
	}
	// Staff's 'My requests': nobody requests music yet, so no album is theirs.
	if mine := albums(mgr, window+"&mine=1"); len(mine) != 0 {
		t.Errorf("albums in staff's 'My requests': %+v", mine)
	}
}

// The feed shows what its owner's Calendar shows: album releases for staff while Music is
// on, never for a requester, adult filter applied.
func TestICSAlbumsForStaff(t *testing.T) {
	s := musicServer(t)
	day := func(n int) string { return time.Now().AddDate(0, 0, n).Format("2006-01-02") }
	calendarAlbumsSeed(t, s, [4]string{day(5), day(6), day(7), day(8)})
	_, kid := s.user(t, "kid", auth.RoleRequester)
	_, mgr := s.user(t, "mgr", auth.RoleManager)
	body := func(c *http.Cookie, everything bool) string {
		t.Helper()
		path := feedPath(t, makeFeed(t, s, c).URL)
		if everything {
			path = strings.Replace(path, "mine=1", "mine=0", 1)
		}
		rec := s.do("GET", path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("feed: HTTP %d", rec.Code)
		}
		return strings.ReplaceAll(rec.Body.String(), "\r\n ", "")
	}

	if feed := body(mgr, false); strings.Contains(feed, "Soon") {
		t.Errorf("Music is off, but the staff feed has an album:\n%s", feed)
	}
	setMusic(t, s, true)
	staff := body(mgr, false)
	for _, want := range []string{"SUMMARY:Soon — Low", "UID:arrmada-album-", "SUMMARY:Paused Album — Paused Band"} {
		if !strings.Contains(staff, want) {
			t.Errorf("staff feed missing %q:\n%s", want, staff)
		}
	}
	if strings.Contains(staff, "Porn") || strings.Contains(staff, "Innocent Name") {
		t.Error("an adult artist's album reached the feed")
	}
	if feed := body(kid, true); strings.Contains(feed, "Soon") || strings.Contains(feed, "arrmada-album") {
		t.Errorf("a requester's whole-library feed has an album:\n%s", feed)
	}
}

// The homepage widget counts music like the other libraries while Music is on, and its
// released, incomplete albums join wanted.total.
func TestWidgetCountsMusic(t *testing.T) {
	s := musicServer(t)
	_, admin := s.user(t, "owner", auth.RoleAdmin)
	low := musicArtist(t, s, "a-low", "Low", true)
	musicAlbum(t, s, music.Album{ArtistID: low, MBID: "rg-half", Title: "Half", Year: 2021, Monitored: true}, 3, 2)
	musicAlbum(t, s, music.Album{ArtistID: low, MBID: "rg-soon", Title: "Soon", ReleaseDate: "2099-01-01", Monitored: true}, 0, 0)
	mustExecAll(t, s, `INSERT INTO movies (tmdb_id, title, monitored, has_file) VALUES (1, 'Want', 1, 0)`)
	setMusic(t, s, true)
	k := makeWidgetKey(t, s, admin)
	rec := fetchWidget(s, widgetPath, map[string]string{"X-Api-Key": k.Key})
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "Low") || strings.Contains(rec.Body.String(), "Half") {
		t.Errorf("the widget names music: %s", rec.Body)
	}
	var got homepageWidget
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Artists != 1 || got.Albums != 1 || got.Tracks != 2 || got.Wanted.Albums != 1 || got.Wanted.Movies != 1 || got.Wanted.Total != 2 {
		t.Errorf("widget = %+v", got)
	}
}
