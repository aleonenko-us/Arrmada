package metadata

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/httpua"
	"github.com/tristenlammi/arrmada/internal/store"
)

// mbFake is a MusicBrainz stand-in whose answer can be switched (ok, 503, 500, 404, a
// hang) and which counts the requests that reach it.
type mbFake struct {
	mu       sync.Mutex
	mode     string // "", "503", "500", "404", "hang"
	bodies   map[string]string
	requests atomic.Int64
	ua       atomic.Value
}

func (f *mbFake) set(mode string) {
	f.mu.Lock()
	f.mode = mode
	f.mu.Unlock()
}

func (f *mbFake) handler(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	f.ua.Store(r.UserAgent())
	f.mu.Lock()
	mode, body := f.mode, f.bodies[r.URL.Path]
	f.mu.Unlock()
	switch mode {
	case "503":
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	case "500":
		w.WriteHeader(http.StatusInternalServerError)
		return
	case "404":
		w.WriteHeader(http.StatusNotFound)
		return
	case "hang":
		<-r.Context().Done()
		return
	}
	if body == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_, _ = io.WriteString(w, body)
}

// cachedMB is a client over a real cache table and the fake, with a clock the test moves.
func cachedMB(t *testing.T) (*MusicBrainz, *mbFake, *time.Time) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	f := &mbFake{bodies: map[string]string{
		"/artist/abc": `{"id":"abc","name":"Radiohead","sort-name":"Radiohead"}`,
		"/release-group": `{"release-groups":[
			{"id":"rg1","title":"OK Computer","primary-type":"Album","first-release-date":"1997-05-21"}]}`,
	}}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	m := NewMusicBrainz(st.DB(), nil)
	m.http = &http.Client{Timeout: 2 * time.Second}
	m.base = srv.URL
	m.minInterval = 0
	m.gate = newMBLimiter()
	now := time.Now()
	m.now = func() time.Time { return now }
	t.Cleanup(m.bg.Wait) // runs first: background refreshes finish before the DB closes
	return m, f, &now
}

// Opening the same artist twice makes one call; a stale answer is served at once and
// refreshed behind it.
func TestMBCacheFreshAndStale(t *testing.T) {
	m, f, now := cachedMB(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if a, err := m.GetArtist(ctx, "abc"); err != nil || a.Name != "Radiohead" {
			t.Fatalf("GetArtist: %+v %v", a, err)
		}
	}
	if n := f.requests.Load(); n != 1 {
		t.Fatalf("requests = %d, want 1 (the second came from the cache)", n)
	}
	// Eight days on, the artist is stale: answered from the cache, refreshed behind.
	*now = now.Add(8 * 24 * time.Hour)
	f.mu.Lock()
	f.bodies["/artist/abc"] = `{"id":"abc","name":"Radiohead (renamed)"}`
	f.mu.Unlock()
	if a, err := m.GetArtist(ctx, "abc"); err != nil || a.Name != "Radiohead" {
		t.Fatalf("stale read = %+v %v, want the cached answer at once", a, err)
	}
	m.bg.Wait()
	if f.requests.Load() != 2 {
		t.Fatalf("requests = %d, want a background refresh", f.requests.Load())
	}
	if a, _ := m.GetArtist(ctx, "abc"); a.Name != "Radiohead (renamed)" {
		t.Errorf("after the refresh = %q, want the new answer", a.Name)
	}
}

// With MusicBrainz refusing, erroring or hanging, everything already cached keeps working
// and nothing cached is lost; only questions never asked before fail, as unavailable.
func TestMBCacheServesWhenDown(t *testing.T) {
	for _, mode := range []string{"503", "500", "hang", "404"} {
		t.Run(mode, func(t *testing.T) {
			m, f, now := cachedMB(t)
			ctx := context.Background()
			if _, err := m.GetArtist(ctx, "abc"); err != nil {
				t.Fatal(err)
			}
			if albums, err := m.ArtistAlbums(ctx, "abc"); err != nil || len(albums) != 1 {
				t.Fatalf("albums: %v %v", albums, err)
			}
			f.set(mode)
			*now = now.Add(60 * 24 * time.Hour) // everything stale

			a, err := m.GetArtist(ctx, "abc")
			if err != nil || a.Name != "Radiohead" {
				t.Errorf("artist while down = %+v %v, want the cached one", a, err)
			}
			albums, err := m.ArtistAlbums(ctx, "abc")
			if err != nil || len(albums) != 1 {
				t.Errorf("albums while down = %v %v, want the cached one", albums, err)
			}
			m.bg.Wait()
			// The refresh task changes nothing either.
			if _, err := m.RefreshStale(ctx, 50); err != nil {
				t.Errorf("RefreshStale: %v", err)
			}
			if s, _ := m.CacheStats(ctx); s.Entries != 2 {
				t.Errorf("cache entries = %d after the outage, want 2", s.Entries)
			}
			if a, err := m.GetArtist(ctx, "abc"); err != nil || a.Name != "Radiohead" {
				t.Errorf("after the refresh tried = %+v %v", a, err)
			}
			if mode == "404" {
				return // a 404 for something never asked is an answer, not an outage
			}
			hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if _, err := m.GetArtist(hctx, "never-seen"); !errors.Is(err, ErrMusicBrainzUnavailable) {
				t.Errorf("an uncached lookup while down = %v, want ErrMusicBrainzUnavailable", err)
			}
		})
	}
}

// An empty list where the cache had albums is MusicBrainz having a bad moment: kept.
func TestMBCacheEmptyListKeepsOld(t *testing.T) {
	m, f, now := cachedMB(t)
	ctx := context.Background()
	if albums, _ := m.ArtistAlbums(ctx, "abc"); len(albums) != 1 {
		t.Fatal("seed")
	}
	f.mu.Lock()
	f.bodies["/release-group"] = `{"release-groups":[]}`
	f.mu.Unlock()
	*now = now.Add(4 * 24 * time.Hour)
	if n, err := m.RefreshStale(ctx, 50); err != nil || n != 1 {
		t.Fatalf("RefreshStale = %d %v", n, err)
	}
	if albums, _ := m.ArtistAlbums(ctx, "abc"); len(albums) != 1 {
		t.Errorf("albums after an empty answer = %v, want the one we had", albums)
	}
	// Checked, though: it isn't stale any more.
	if s, _ := m.CacheStats(ctx); s.Stale != 0 {
		t.Errorf("stale = %d, want 0 after the check", s.Stale)
	}
}

// A merged artist (asked for by its old id, answered under the new) is cached under both.
func TestMBCacheMergedID(t *testing.T) {
	m, f, _ := cachedMB(t)
	ctx := context.Background()
	f.mu.Lock()
	f.bodies["/artist/old"] = `{"id":"new","name":"Merged"}`
	f.mu.Unlock()
	if _, err := m.GetArtist(ctx, "old"); err != nil {
		t.Fatal(err)
	}
	before := f.requests.Load()
	if a, err := m.GetArtist(ctx, "new"); err != nil || a.Name != "Merged" || f.requests.Load() != before {
		t.Errorf("the new id should come from the cache: %+v %v (requests %d → %d)", a, err, before, f.requests.Load())
	}
}

// The User-Agent says Arrmada, its real build version and how to reach whoever runs it.
func TestMBUserAgent(t *testing.T) {
	m, f, _ := cachedMB(t)
	if _, err := m.GetArtist(context.Background(), "abc"); err != nil {
		t.Fatal(err)
	}
	want := "Arrmada/" + httpua.Version() + " ( https://github.com/tristenlammi/Arrmada )"
	if got := f.ua.Load(); got != want {
		t.Errorf("User-Agent = %q, want %q", got, want)
	}
	if strings.Contains(want, "Arrmada/1.0") {
		t.Error("the version must be the build's, never a hard-coded 1.0")
	}
	m.config = func() MBConfig { return MBConfig{Contact: "owner@example.com"} }
	if got := m.userAgent(); got != "Arrmada/"+httpua.Version()+" ( https://github.com/tristenlammi/Arrmada; owner@example.com )" {
		t.Errorf("with a contact = %q", got)
	}
}

// The address and the pacing: the public server is always 1.1 s apart whatever the mirror
// rate says; a mirror gets its own rate.
func TestMBAddressAndPacing(t *testing.T) {
	m := NewMusicBrainz(nil, func() MBConfig { return MBConfig{BaseURL: "http://192.168.1.20:5000", MirrorRate: 20} })
	if r := m.root(); r != "http://192.168.1.20:5000/ws/2" {
		t.Errorf("root = %q", r)
	}
	if d := m.interval(m.root()); d != 50*time.Millisecond {
		t.Errorf("mirror interval = %v, want 50ms", d)
	}
	m.config = func() MBConfig { return MBConfig{BaseURL: "https://musicbrainz.org", MirrorRate: 50} }
	if d := m.interval(m.root()); d != mbPublicInterval {
		t.Errorf("public interval = %v, want %v whatever the mirror rate", d, mbPublicInterval)
	}
	m.config = func() MBConfig { return MBConfig{BaseURL: "http://mirror.lan/ws/2"} }
	if d := m.interval(m.root()); d != mbPublicInterval || m.root() != "http://mirror.lan/ws/2" {
		t.Errorf("a mirror with no rate set = %v at %q, want the default pace", d, m.root())
	}
	m.config = func() MBConfig { return MBConfig{} }
	if m.root() != "https://musicbrainz.org/ws/2" {
		t.Errorf("default root = %q", m.root())
	}

	for _, ok := range []string{"", "https://musicbrainz.org", "http://192.168.1.20:5000/", "http://mirror.lan/ws/2"} {
		if _, err := NormalizeMBBaseURL(ok); err != nil {
			t.Errorf("NormalizeMBBaseURL(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"ftp://mirror.lan", "mirror.lan", "javascript:alert(1)", "http://", "http://u:p@mirror.lan", "http://mirror.lan/?x=1"} {
		if _, err := NormalizeMBBaseURL(bad); err == nil {
			t.Errorf("NormalizeMBBaseURL(%q) accepted", bad)
		}
	}
}

// Every request goes to the configured mirror.
func TestMBUsesMirror(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if !strings.HasPrefix(r.URL.Path, "/ws/2/") {
			t.Errorf("path %q, want under /ws/2", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":"abc","name":"Radiohead"}`)
	}))
	defer srv.Close()
	m := NewMusicBrainz(nil, func() MBConfig { return MBConfig{BaseURL: srv.URL, MirrorRate: 50} })
	m.gate = newMBLimiter()
	if _, err := m.GetArtist(context.Background(), "abc"); err != nil || hits.Load() != 1 {
		t.Fatalf("err %v, hits %d", err, hits.Load())
	}
	if err := m.Test(context.Background(), srv.URL); err != nil {
		t.Errorf("Test against the mirror: %v", err)
	}
}
