package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/eventbus"
	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/quality"
)

const fakeNZB = `<?xml version="1.0" encoding="UTF-8"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><file subject="x"><segments><segment number="1">a@b</segment></segments></file></nzb>`

// fakeNewznabIndexer serves a search with two releases whose links carry the indexer's
// key — one NZB it hands over, one it refuses with a download limit — and records every
// NZB download.
type fakeNewznabIndexer struct {
	mu      sync.Mutex
	fetched []string
	srv     *httptest.Server
}

func newFakeNewznab(t *testing.T) *fakeNewznabIndexer {
	f := &fakeNewznabIndexer{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/getnzb/") {
			f.mu.Lock()
			f.fetched = append(f.fetched, r.URL.RequestURI())
			f.mu.Unlock()
			if strings.HasPrefix(r.URL.Path, "/getnzb/limited") {
				w.WriteHeader(http.StatusTooManyRequests)
				fmt.Fprint(w, `<error code="429" description="Request limit reached for key `+trackerKey+`"/>`)
				return
			}
			fmt.Fprint(w, fakeNZB)
			return
		}
		item := func(id, title string) string {
			link := fmt.Sprintf("%s/getnzb/%s.nzb?i=1&amp;r=%s", f.srv.URL, id, trackerKey)
			return fmt.Sprintf(`<item><title>%s</title><guid>%s/details/%s?apikey=%s</guid><link>%s</link><enclosure url="%s" type="application/x-nzb"/><size>9000000000</size></item>`,
				title, f.srv.URL, id, trackerKey, link, link)
		}
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>`+
			item("good", "Alpha.2001.2160p.BluRay.REMUX.x265-NZB")+
			item("limited", "Alpha.2001.2160p.BluRay.x265-LIMITED")+
			`</channel></rss>`)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeNewznabIndexer) fetches() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.fetched...)
}

// recordingSAB is a usenet client that keeps every add it's given.
type recordingSAB struct {
	fakeUsenetClient
	mu   sync.Mutex
	adds []download.AddRequest
}

func (s *recordingSAB) Add(_ context.Context, _ download.Client, req download.AddRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adds = append(s.adds, req)
	return nil
}

func (s *recordingSAB) added() []download.AddRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]download.AddRequest(nil), s.adds...)
}

// usenetGrabServer is the real router over one movie (1, "Alpha"), a Newznab indexer and a
// usenet client, wired the way main wires them.
func usenetGrabServer(t *testing.T) (*routeServer, *fakeNewznabIndexer, *recordingSAB) {
	t.Helper()
	nz := newFakeNewznab(t)
	sab := &recordingSAB{}
	s := newRouteServer(t, func(d *Deps) {
		ctx := context.Background()
		db := d.Store.DB()
		ix := indexer.NewService(db, d.Log, nil)
		dl := download.NewService(db, d.Log)
		dl.SetDownloader(download.KindSABnzbd, sab)
		if _, err := dl.Create(ctx, download.Client{Name: "sab", Kind: download.KindSABnzbd, URL: "http://sab.invalid", Enabled: true}); err != nil {
			t.Fatal(err)
		}
		ix.SetUsenetAvailable(func() bool { return dl.HasTransport(context.Background(), download.TransportUsenet) })
		if _, err := ix.Create(ctx, indexer.Indexer{Name: "Geek", Kind: indexer.KindNewznab, URL: nz.srv.URL, APIKey: trackerKey, Priority: 10, Enabled: true}); err != nil {
			t.Fatal(err)
		}
		mv := movies.NewService(db, nil, nil, t.TempDir(), "", nil, d.Log)
		co := automation.New(mv, ix, dl, quality.NewService(db), db, eventbus.New(d.Log), d.Log, t.TempDir())
		d.Indexers, d.Downloads, d.Movies, d.Automation = ix, dl, mv, co
		if _, err := db.Exec(`INSERT INTO movies (id, tmdb_id, title, year, monitored, min_availability) VALUES (1, 1, 'Alpha', 2001, 1, 'announced')`); err != nil {
			t.Fatal(err)
		}
	})
	return s, nz, sab
}

// A Newznab release's listing carries a token and nothing of its link or key; grabbing it
// downloads the NZB on the server and hands the usenet client the file, never the link —
// and a refused download says why without the key in it.
func TestNewznabReleaseCarriesNoSecrets(t *testing.T) {
	s, nz, sab := usenetGrabServer(t)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)

	rec := s.do("GET", "/api/v1/movies/1/releases", mgr)
	rels := releasesOf(t, rec, true)
	noSecrets(t, "newznab releases", rec.Body.String())
	if strings.Contains(rec.Body.String(), "getnzb") {
		t.Fatalf("the NZB link reached the browser: %s", rec.Body)
	}
	good := tokenFor(t, rels, "Alpha.2001.2160p.BluRay.REMUX.x265-NZB")
	for _, r := range rels {
		if r.Transport != string(indexer.TransportUsenet) || r.Seeders != 0 {
			t.Errorf("row %+v", r)
		}
	}
	if len(nz.fetches()) != 0 {
		t.Fatal("listing releases downloaded an NZB")
	}

	rec = s.doJSON("POST", "/api/v1/grab", mgr, `{"token":"`+good+`","movie_id":1}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("grab: HTTP %d: %s", rec.Code, rec.Body)
	}
	noSecrets(t, "grab", rec.Body.String())
	adds := sab.added()
	if len(adds) != 1 || string(adds[0].File) != fakeNZB || adds[0].URL != "" || adds[0].Transport != download.TransportUsenet {
		t.Fatalf("usenet client got %+v", adds)
	}
	if strings.Contains(adds[0].Filename, trackerKey) || !strings.HasSuffix(adds[0].Filename, ".nzb") {
		t.Errorf("NZB filename %q", adds[0].Filename)
	}
	if f := nz.fetches(); len(f) != 1 || !strings.Contains(f[0], "r="+trackerKey) {
		t.Fatalf("NZB downloads %v, want the stored link fetched server-side", f)
	}

	limited := tokenFor(t, releasesOf(t, s.do("GET", "/api/v1/movies/1/releases", mgr), true), "Alpha.2001.2160p.BluRay.x265-LIMITED")
	rec = s.doJSON("POST", "/api/v1/grab", mgr, `{"token":"`+limited+`","movie_id":1}`)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "limit is reached") {
		t.Fatalf("limited grab: HTTP %d: %s", rec.Code, rec.Body)
	}
	noSecrets(t, "limited grab", rec.Body.String())
	if len(sab.added()) != 1 {
		t.Fatal("a refused NZB still reached the usenet client")
	}
}

// download_network_preference takes best, usenet or torrent, and a manager may set it (it
// sits with the other download settings).
func TestNetworkPreferenceSetting(t *testing.T) {
	a := settingsAPI(t)
	_, got := getSettings(t, a)
	if got["download_network_preference"] != quality.NetworkBest {
		t.Fatalf("default = %v", got["download_network_preference"])
	}
	for _, bad := range []string{`"both"`, `""`, `"nzb"`} {
		if w := putSettings(t, a, `{"download_network_preference":`+bad+`}`); w.Code != http.StatusBadRequest {
			t.Errorf("%s: HTTP %d", bad, w.Code)
		}
	}
	if w := putSettingsAs(t, a, auth.RoleManager, `{"download_network_preference":"torrent"}`); w.Code != http.StatusOK {
		t.Fatalf("manager: HTTP %d %s", w.Code, w.Body.String())
	}
	if _, got := getSettings(t, a); got["download_network_preference"] != quality.NetworkTorrent {
		t.Fatalf("saved = %v", got["download_network_preference"])
	}
}
