package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/mediaserver"
	"github.com/tristenlammi/arrmada/internal/requests"
)

// readyServer is one media server's index for the ready locator: titles by TMDB id or, for
// a server that finds by folder, by folder.
type readyServer struct {
	kind     mediaserver.Kind
	conf     bool
	built    time.Time
	tmdb     map[int]string
	folders  map[string]string
	stale    []time.Duration
	listened int
}

func (f *readyServer) Kind() mediaserver.Kind          { return f.kind }
func (f *readyServer) Name(context.Context) string     { return "" }
func (f *readyServer) Configured(context.Context) bool { return f.conf }
func (f *readyServer) IndexReady(context.Context) bool { return f.conf && !f.built.IsZero() }
func (f *readyServer) IndexBuiltAt() time.Time         { return f.built }
func (f *readyServer) IndexStale(after time.Duration)  { f.stale = append(f.stale, after) }
func (f *readyServer) OnIndexRebuilt(func())           { f.listened++ }
func (f *readyServer) RecentlyAddedTMDB(context.Context, int) ([]mediaserver.RecentTMDB, error) {
	return nil, nil
}
func (f *readyServer) WatchURL(_ context.Context, _ string, ids mediaserver.ExternalIDs) string {
	if u := f.tmdb[ids.TMDB]; u != "" {
		return u
	}
	return f.folders[ids.Folder]
}
func (f *readyServer) Locate(ctx context.Context, media string, ids mediaserver.ExternalIDs) (mediaserver.Item, bool) {
	return mediaserver.Item{Server: f.kind}, f.WatchURL(ctx, media, ids) != ""
}

// With only Plex set up the locator answers as Plex alone always did: its link, its
// index time, its refresh — Jellyfin (not set up) is never asked about.
func TestMediaReadyLocatorPlexOnly(t *testing.T) {
	ctx := context.Background()
	at := time.Unix(1700000000, 0)
	plex := &readyServer{kind: mediaserver.Plex, conf: true, built: at, tmdb: map[int]string{603: "https://app.plex.tv/p603"}}
	jf := &readyServer{kind: mediaserver.Jellyfin, tmdb: map[int]string{603: "https://jf/j603"}}
	loc := mediaReadyLocator{mediaserver.NewRegistry(plex, jf)}
	if !loc.Configured(ctx) || !reflect.DeepEqual(loc.Servers(ctx), []mediaserver.Kind{mediaserver.Plex}) {
		t.Fatalf("configured %v servers %v", loc.Configured(ctx), loc.Servers(ctx))
	}
	got := loc.Find(ctx, "movie", requests.PlexIDs{TMDB: 603})
	want := []requests.Sighting{{Link: mediaserver.WatchLink{Server: mediaserver.Plex, Label: "Plex", URL: "https://app.plex.tv/p603"}, IndexBuiltAt: at}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("find = %+v, want %+v", got, want)
	}
	if got := loc.Find(ctx, "movie", requests.PlexIDs{TMDB: 604}); len(got) != 0 {
		t.Errorf("unknown title found: %+v", got)
	}
	if !loc.IndexBuiltAt().Equal(at) {
		t.Errorf("index built %v, want Plex's %v", loc.IndexBuiltAt(), at)
	}
	loc.Refresh(5 * time.Second)
	if !reflect.DeepEqual(plex.stale, []time.Duration{5 * time.Second}) {
		t.Errorf("plex refreshes = %v", plex.stale)
	}
	// Nothing set up: 'ready' doesn't wait.
	if (mediaReadyLocator{mediaserver.NewRegistry(&readyServer{kind: mediaserver.Plex})}).Configured(ctx) {
		t.Error("an unconfigured Plex makes ready wait")
	}
}

// Both set up: a sighting per server that has it (Jellyfin also by folder), the oldest
// index, and a refresh asked of both.
func TestMediaReadyLocatorBothServers(t *testing.T) {
	ctx := context.Background()
	old, fresh := time.Unix(1700000000, 0), time.Unix(1700000900, 0)
	plex := &readyServer{kind: mediaserver.Plex, conf: true, built: old, tmdb: map[int]string{}}
	jf := &readyServer{kind: mediaserver.Jellyfin, conf: true, built: fresh,
		tmdb: map[int]string{}, folders: map[string]string{"/movies/Dune (2021)": "https://watch.example.com/dune"}}
	loc := mediaReadyLocator{mediaserver.NewRegistry(jf, plex)}
	if !reflect.DeepEqual(loc.Servers(ctx), []mediaserver.Kind{mediaserver.Plex, mediaserver.Jellyfin}) {
		t.Errorf("servers = %v", loc.Servers(ctx))
	}
	if got := loc.Find(ctx, "movie", requests.PlexIDs{TMDB: 438631}); len(got) != 0 {
		t.Errorf("found without the folder: %+v", got)
	}
	got := loc.Find(ctx, "movie", requests.PlexIDs{TMDB: 438631, Folder: "/movies/Dune (2021)"})
	if len(got) != 1 || got[0].Link.Server != mediaserver.Jellyfin || got[0].Link.URL != "https://watch.example.com/dune" || !got[0].IndexBuiltAt.Equal(fresh) {
		t.Errorf("find by folder = %+v", got)
	}
	if !loc.IndexBuiltAt().Equal(old) {
		t.Errorf("index built %v, want the oldest", loc.IndexBuiltAt())
	}
	loc.Refresh(0)
	if len(plex.stale) != 1 || len(jf.stale) != 1 {
		t.Errorf("refreshes plex %v jellyfin %v", plex.stale, jf.stale)
	}
	// One server not read yet: the oldest is "not yet".
	jf.built = time.Time{}
	if !loc.IndexBuiltAt().IsZero() {
		t.Error("an unread index counts as read")
	}
	if homeServer(ctx, 1) != mediaserver.Plex {
		t.Error("before accounts can link Jellyfin, everyone is Plex-first")
	}
}
