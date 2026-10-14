package mediaserver

import (
	"context"
	"reflect"
	"testing"
	"time"
)

type fakeServer struct {
	kind     Kind
	conf     bool
	built    time.Time
	have     map[int]string // tmdb → watch url
	stale    []time.Duration
	rebuilts int
}

func (f *fakeServer) Kind() Kind                      { return f.kind }
func (f *fakeServer) Name(context.Context) string     { return string(f.kind) + "-box" }
func (f *fakeServer) Configured(context.Context) bool { return f.conf }
func (f *fakeServer) IndexReady(context.Context) bool { return f.conf && !f.built.IsZero() }
func (f *fakeServer) IndexBuiltAt() time.Time         { return f.built }
func (f *fakeServer) IndexStale(after time.Duration)  { f.stale = append(f.stale, after) }
func (f *fakeServer) OnIndexRebuilt(fn func())        { f.rebuilts++ }
func (f *fakeServer) RecentlyAddedTMDB(context.Context, int) ([]RecentTMDB, error) {
	return nil, nil
}
func (f *fakeServer) Locate(_ context.Context, media string, ids ExternalIDs) (Item, bool) {
	if _, ok := f.have[ids.TMDB]; ok {
		return Item{Server: f.kind, ID: "x", Media: media, IDs: ids}, true
	}
	return Item{}, false
}
func (f *fakeServer) WatchURL(_ context.Context, _ string, ids ExternalIDs) string {
	return f.have[ids.TMDB]
}

func TestRegistryOrderAndLookups(t *testing.T) {
	ctx := context.Background()
	t0 := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	jf := &fakeServer{kind: Jellyfin, conf: true, built: t0, have: map[int]string{1: "https://jf/1", 2: "https://jf/2"}}
	px := &fakeServer{kind: Plex, conf: true, built: t0.Add(time.Minute), have: map[int]string{2: "https://plex/2"}}
	r := NewRegistry(jf, nil, px) // passed out of order: Plex still comes first
	if s := r.Servers(); len(s) != 2 || s[0].Kind() != Plex || s[1].Kind() != Jellyfin {
		t.Fatalf("order = %v", s)
	}
	if !r.Configured(ctx) || !r.IndexReady(ctx) {
		t.Fatal("configured servers")
	}
	if it, ok := r.LocateAny(ctx, "movie", ExternalIDs{TMDB: 1}); !ok || it.Server != Jellyfin {
		t.Errorf("tmdb 1 = %+v %v, want Jellyfin's", it, ok)
	}
	if it, ok := r.LocateAny(ctx, "movie", ExternalIDs{TMDB: 2}); !ok || it.Server != Plex {
		t.Errorf("tmdb 2 = %+v %v, want Plex's (first in order)", it, ok)
	}
	if _, ok := r.LocateAny(ctx, "movie", ExternalIDs{TMDB: 3}); ok {
		t.Error("tmdb 3 found")
	}
	want := []WatchLink{{Server: Plex, Label: "Plex", URL: "https://plex/2"}, {Server: Jellyfin, Label: "Jellyfin", URL: "https://jf/2"}}
	if got := r.WatchLinks(ctx, "movie", ExternalIDs{TMDB: 2}); !reflect.DeepEqual(got, want) {
		t.Errorf("links = %+v", got)
	}
	if got := r.IndexBuiltAt(ctx); !got.Equal(t0) {
		t.Errorf("built at = %v, want the older index (%v)", got, t0)
	}
	r.IndexStale(time.Minute)
	r.OnIndexRebuilt(func() {})
	if len(jf.stale) != 1 || len(px.stale) != 1 || jf.rebuilts != 1 || px.rebuilts != 1 {
		t.Error("stale/rebuilt not passed to every server")
	}

	// A server that isn't set up is skipped everywhere; one whose index isn't built yet
	// makes the combined build time zero.
	jf.conf = false
	if got := r.WatchLinks(ctx, "movie", ExternalIDs{TMDB: 1}); len(got) != 0 {
		t.Errorf("unconfigured server linked: %+v", got)
	}
	if got := r.IndexBuiltAt(ctx); !got.Equal(t0.Add(time.Minute)) {
		t.Errorf("built at with Jellyfin unset = %v", got)
	}
	jf.conf, jf.built = true, time.Time{}
	if !r.IndexBuiltAt(ctx).IsZero() {
		t.Error("an unbuilt index must make the combined time zero")
	}
	if r.Get(Jellyfin) != jf || r.Get("emby") != nil {
		t.Error("Get")
	}
}

func TestNilRegistry(t *testing.T) {
	var r *Registry
	ctx := context.Background()
	if r.Configured(ctx) || r.IndexReady(ctx) || len(r.Servers()) != 0 || r.Get(Plex) != nil || !r.IndexBuiltAt(ctx).IsZero() {
		t.Fatal("a nil registry is empty")
	}
	if _, ok := r.LocateAny(ctx, "movie", ExternalIDs{TMDB: 1}); ok {
		t.Fatal("found in a nil registry")
	}
	r.IndexStale(time.Second)
	r.OnIndexRebuilt(func() {})
}

func TestKindLabel(t *testing.T) {
	for k, want := range map[Kind]string{Plex: "Plex", "": "Plex", Jellyfin: "Jellyfin", "emby": "emby"} {
		if got := k.Label(); got != want {
			t.Errorf("%q.Label() = %q", k, got)
		}
	}
}
