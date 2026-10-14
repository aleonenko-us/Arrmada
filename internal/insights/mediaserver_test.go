package insights

import (
	"context"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/mediaserver"
	"github.com/tristenlammi/arrmada/internal/plex"
)

// The Plex connection as a mediaserver.Server answers from the same index as the Plex
// pages do, with the neutral item.
func TestPlexServerAdapter(t *testing.T) {
	ctx := context.Background()
	s := newLinkService(t, "http://plex.invalid:32400")
	built := time.Now().Add(-time.Minute)
	s.links.idx = &plexIndex{
		machineID:   "m1",
		movieByTMDB: map[int]plex.Item{603: {RatingKey: "101", Type: "movie", Title: "The Matrix", Year: 1999, TMDB: 603, IMDB: "tt0133093", AddedAt: 1700000000}},
		showByTVDB:  map[int]plex.Item{73244: {RatingKey: "201", Type: "show", Title: "The Office", TVDB: 73244}},
		builtAt:     built,
	}
	var srv mediaserver.Server = s.MediaServer()
	if srv.Kind() != mediaserver.Plex || !srv.Configured(ctx) || !srv.IndexReady(ctx) || !srv.IndexBuiltAt().Equal(built) {
		t.Fatalf("kind %q configured %v ready %v built %v", srv.Kind(), srv.Configured(ctx), srv.IndexReady(ctx), srv.IndexBuiltAt())
	}
	it, ok := srv.Locate(ctx, "movie", mediaserver.ExternalIDs{TMDB: 603})
	want := mediaserver.Item{Server: mediaserver.Plex, ID: "101", Media: "movie", Title: "The Matrix", Year: 1999,
		IDs: mediaserver.ExternalIDs{TMDB: 603, IMDB: "tt0133093"}, AddedAt: time.Unix(1700000000, 0)}
	if !ok || it != want {
		t.Fatalf("locate = %+v %v", it, ok)
	}
	if it, ok := srv.Locate(ctx, "series", mediaserver.ExternalIDs{TVDB: 73244}); !ok || it.Media != "series" || it.ID != "201" {
		t.Fatalf("show = %+v %v", it, ok)
	}
	// The same link the Plex pages give.
	if got, want := srv.WatchURL(ctx, "movie", mediaserver.ExternalIDs{TMDB: 603}), s.WatchURL(ctx, "movie", ExternalIDs{TMDB: 603}); got == "" || got != want {
		t.Fatalf("watch url %q, want %q", got, want)
	}
	rebuilt := 0
	srv.OnIndexRebuilt(func() { rebuilt++ })
	if len(s.links.listeners) != 1 {
		t.Fatal("OnIndexRebuilt didn't register with the Plex index")
	}
	srv.IndexStale(time.Minute)
	if s.links.staleAt.IsZero() {
		t.Fatal("IndexStale didn't reach the Plex index")
	}
}
