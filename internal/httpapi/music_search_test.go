package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/eventbus"
	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/jobs"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/music"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/settings"
)

// searchProvider is MusicBrainz with one artist and one 12-track, 48-minute album.
type searchProvider struct{}

func (searchProvider) Available() bool { return true }
func (searchProvider) SearchArtists(context.Context, string) ([]metadata.ArtistResult, error) {
	return nil, nil
}
func (searchProvider) GetArtist(_ context.Context, mbid string) (*metadata.ArtistDetails, error) {
	return &metadata.ArtistDetails{ArtistResult: metadata.ArtistResult{MBID: mbid, Name: "Radiohead"}}, nil
}
func (searchProvider) ArtistAlbums(context.Context, string) ([]metadata.AlbumResult, error) {
	return []metadata.AlbumResult{{MBID: "rg-okc", Title: "OK Computer", Type: "Album", PrimaryType: "Album", ReleaseDate: "1997-05-21", Year: 1997}}, nil
}
func (searchProvider) AlbumTracks(context.Context, string) ([]metadata.TrackResult, error) {
	var out []metadata.TrackResult
	for i := 1; i <= 12; i++ {
		out = append(out, metadata.TrackResult{Title: fmt.Sprintf("Song %d", i), Disc: 1, Number: i, DurationSec: 240})
	}
	return out, nil
}

// musicTracker is a Torznab indexer answering every search with the same music releases,
// whose links carry its apikey; it records the downloads it serves.
type musicTracker struct {
	mu      sync.Mutex
	fetched []string
	srv     *httptest.Server
}

func newMusicTracker(t *testing.T) *musicTracker {
	mt := &musicTracker{}
	mt.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/dl/") {
			mt.mu.Lock()
			mt.fetched = append(mt.fetched, r.URL.RequestURI())
			mt.mu.Unlock()
			fmt.Fprint(w, "d4:infod4:name5:alpha12:piece lengthi16384e6:pieces0:6:lengthi1eee")
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		item := func(n int, title string, size int64) string {
			link := fmt.Sprintf("%s/dl/%d?apikey=%s&amp;file=x", mt.srv.URL, n, trackerKey)
			return fmt.Sprintf(`<item><title>%s</title><guid>%s</guid><enclosure url="%s" type="application/x-bittorrent"/><size>%d</size>
				<torznab:attr name="seeders" value="%d"/></item>`, title, link, link, size, 10+n)
		}
		fmt.Fprint(w, `<rss xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>`+
			item(1, "Radiohead - OK Computer (1997) [FLAC]", 400<<20)+
			item(2, "Radiohead - OK Computer (1997) [MP3 320]", 110<<20)+
			item(3, "Radiohead - OK Computer Live 1998 1080p BluRay FLAC 2.0 x264", 8<<30)+
			item(4, "Radiohead - OK Computer (1997) [FLAC] tiny", 20<<20)+
			item(5, "Muse - Absolution (2003) [FLAC]", 400<<20)+
			item(6, "Radiohead - OK Computer (1997) [MP3 128]", 45<<20)+
			`</channel></rss>`)
	}))
	t.Cleanup(mt.srv.Close)
	return mt
}

func (mt *musicTracker) fetches() []string {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	return append([]string(nil), mt.fetched...)
}

// musicSearchServer is the real router with Music on, one artist and album, a profile
// that takes FLAC and MP3 320 (not 128), the music tracker, a fake qBittorrent and a job
// runner.
func musicSearchServer(t *testing.T) (*routeServer, *musicTracker, *music.Service, *jobs.Runner, music.Artist, music.Album) {
	t.Helper()
	mt := newMusicTracker(t)
	q := &fakeQbit{}
	qsrv := q.server(t)
	var svc *music.Service
	var runner *jobs.Runner
	var profile string
	s := newRouteServer(t, func(d *Deps) {
		ctx := context.Background()
		db := d.Store.DB()
		ix := indexer.NewService(db, d.Log, nil)
		if _, err := ix.Create(ctx, indexer.Indexer{Name: "Tracker", Kind: indexer.KindTorznab, URL: mt.srv.URL, APIKey: trackerKey, Priority: 10, Enabled: true}); err != nil {
			t.Fatal(err)
		}
		dl := download.NewService(db, d.Log)
		if _, err := dl.Create(ctx, download.Client{Name: "qb", Kind: download.KindQbittorrent, URL: qsrv.URL, Enabled: true}); err != nil {
			t.Fatal(err)
		}
		qs := quality.NewServiceWith(db, d.Settings)
		sp, err := qs.Create(ctx, quality.StoredProfile{MediaType: quality.MediaMusic, Name: "Lossless or 320",
			FormatScores: map[string]int{"FLAC": 120, "MP3-320": 60}, MinFormatScore: 1, UpgradesEnabled: true})
		if err != nil {
			t.Fatal(err)
		}
		profile = fmt.Sprintf("custom:%d", sp.ID)
		svc = music.NewService(db, searchProvider{}, d.Log)
		co := automation.New(nil, ix, dl, qs, db, eventbus.New(d.Log), d.Log, t.TempDir())
		co.SetMusic(svc)
		d.Indexers, d.Downloads, d.Quality, d.Music, d.Automation = ix, dl, qs, svc, co
		if err := d.Settings.SetBool(ctx, settings.KeyModuleMusic, true); err != nil {
			t.Fatal(err)
		}
		r, err := jobs.New(context.Background(), db, d.Log, nil)
		if err != nil {
			t.Fatal(err)
		}
		runner = r
		d.Jobs = r
	})
	t.Cleanup(func() { runner.Shutdown(time.Second) })
	a, err := svc.AddArtist(context.Background(), "a-rh", profile, true)
	if err != nil {
		t.Fatal(err)
	}
	return s, mt, svc, runner, a, a.Albums[0]
}

// MUS-09: the artist's Search now runs as a job over its wanted albums and records each
// album's outcome; the album's search state then names it.
func TestArtistSearchNowAndAlbumState(t *testing.T) {
	s, _, svc, runner, a, al := musicSearchServer(t)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)

	rec := s.do("POST", fmt.Sprintf("/api/v1/music/artists/%d/search", a.ID), mgr)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("search: HTTP %d: %s", rec.Code, rec.Body)
	}
	var started struct {
		JobID int64 `json:"job_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil || started.JobID == 0 {
		t.Fatalf("no job: %s", rec.Body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := runner.Wait(ctx, started.JobID); err != nil {
		t.Fatal(err)
	}
	job, err := runner.Get(ctx, started.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(job.Message, "Searched 1 album: grabbed 1") {
		t.Errorf("job message = %q", job.Message)
	}
	events, _ := svc.Events(context.Background(), a.ID, 20)
	grabbed := false
	for _, e := range events {
		grabbed = grabbed || (e.Event == "grabbed" && strings.Contains(e.Detail, "[FLAC]"))
	}
	if !grabbed {
		t.Errorf("history %+v, want the FLAC grab", events)
	}

	rec = s.do("GET", fmt.Sprintf("/api/v1/music/albums/%d/search-state", al.ID), mgr)
	if rec.Code != http.StatusOK {
		t.Fatalf("search-state: HTTP %d: %s", rec.Code, rec.Body)
	}
	var st struct {
		LastSearch *automation.AttemptSummary `json:"last_search"`
		Upgrade    automation.AlbumUpgrade    `json:"upgrade"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.LastSearch == nil || st.LastSearch.Latest.Outcome != automation.OutcomeGrabbed {
		t.Errorf("last search = %+v, want the grab", st.LastSearch)
	}
	if st.Upgrade.State != "incomplete" || st.Upgrade.Cutoff != music.QualityFLAC {
		t.Errorf("upgrade = %+v, want incomplete with FLAC as the cutoff", st.Upgrade)
	}

	// A second Search now while the grab is downloading searches nothing.
	rec = s.do("POST", fmt.Sprintf("/api/v1/music/artists/%d/search", a.ID), mgr)
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil || started.JobID == 0 {
		t.Fatalf("no job: %s", rec.Body)
	}
	if err := runner.Wait(ctx, started.JobID); err != nil {
		t.Fatal(err)
	}
	if job, _ := runner.Get(ctx, started.JobID); !strings.Contains(job.Message, "already downloading") {
		t.Errorf("second search message = %q, want it to leave the downloading album alone", job.Message)
	}
}

// Every new music search route is 404 while Music is off.
func TestMusicSearchRoutesOffWhenModuleOff(t *testing.T) {
	s, _, _, _, a, al := musicSearchServer(t)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	if err := s.deps.Settings.SetBool(context.Background(), settings.KeyModuleMusic, false); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ method, path string }{
		{"POST", fmt.Sprintf("/api/v1/music/artists/%d/search", a.ID)},
		{"GET", fmt.Sprintf("/api/v1/music/albums/%d/releases", al.ID)},
		{"POST", fmt.Sprintf("/api/v1/music/albums/%d/grab", al.ID)},
		{"GET", fmt.Sprintf("/api/v1/music/albums/%d/search-state", al.ID)},
	} {
		if rec := s.doJSON(c.method, c.path, mgr, `{}`); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s while off = %d, want 404", c.method, c.path, rec.Code)
		}
	}
}
