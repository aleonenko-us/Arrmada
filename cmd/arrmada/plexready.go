package main

import (
	"context"
	"time"

	"github.com/tristenlammi/arrmada/internal/mediaserver"
	"github.com/tristenlammi/arrmada/internal/requests"
)

// mediaReadyLocator is the media servers' library indexes (Plex, then Jellyfin) as the
// Requests module asks them "is this title on a server yet?" before saying a request is
// ready. Owner decision: ready as soon as EITHER server has it. Memory only: it never
// calls a server. With only Plex set up it answers exactly as Plex alone did.
type mediaReadyLocator struct{ reg *mediaserver.Registry }

var _ requests.PlexLocator = mediaReadyLocator{}

func (m mediaReadyLocator) Configured(ctx context.Context) bool { return m.reg.Configured(ctx) }

func (m mediaReadyLocator) Servers(ctx context.Context) []mediaserver.Kind {
	var out []mediaserver.Kind
	for _, s := range m.reg.ConfiguredServers(ctx) {
		out = append(out, s.Kind())
	}
	return out
}

// Find asks every set-up server, with the folder too: Jellyfin finds a movie it matched no
// id for (a multi-version folder) by it; Plex ignores it.
func (m mediaReadyLocator) Find(ctx context.Context, media string, ids requests.PlexIDs) []requests.Sighting {
	x := mediaserver.ExternalIDs{TMDB: ids.TMDB, TVDB: ids.TVDB, IMDB: ids.IMDB, Folder: ids.Folder}
	var out []requests.Sighting
	for _, s := range m.reg.ConfiguredServers(ctx) {
		if _, ok := s.Locate(ctx, media, x); !ok {
			continue
		}
		out = append(out, requests.Sighting{
			Link:         mediaserver.WatchLink{Server: s.Kind(), Label: s.Kind().Label(), URL: s.WatchURL(ctx, media, x)},
			IndexBuiltAt: s.IndexBuiltAt(),
		})
	}
	return out
}

// IndexBuiltAt is the oldest index among the set-up servers. The ready check runs off the
// request path (a timer and the index workers), never a handler's context.
func (m mediaReadyLocator) IndexBuiltAt() time.Time { return m.reg.IndexBuiltAt(context.Background()) }

func (m mediaReadyLocator) Refresh(after time.Duration) { m.reg.IndexStale(after) }

// homeServer is the media server a person watches on: their Watch links, the push's Watch
// action and their Apprise message lead with it.
//
// J4 HOOK: users.jellyfin_id arrives with lane J4 (Jellyfin sign-in and accounts). Until
// then nobody can be linked to Jellyfin, so everyone is Plex-first. J4 replaces this body
// with mediaserver.Home(plexLinked, jellyfinLinked) read from the account (one indexed
// row by id; a read error answers Plex).
func homeServer(context.Context, int64) mediaserver.Kind { return mediaserver.Plex }
