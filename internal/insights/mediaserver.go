package insights

import (
	"context"
	"time"

	"github.com/tristenlammi/arrmada/internal/mediaserver"
	"github.com/tristenlammi/arrmada/internal/plex"
)

// PlexServer is the Plex connection as a mediaserver.Server: thin names over the Plex
// library index and connection this service already keeps. It's a wrapper rather than
// methods on Service because Service.Locate answers with a plex.Item, which the Plex
// pages read; this one answers with the neutral item. Nothing here changes what Plex
// does.
type PlexServer struct{ *Service }

var _ mediaserver.Server = PlexServer{}

// MediaServer is the Plex connection as one of the media servers.
func (s *Service) MediaServer() PlexServer { return PlexServer{s} }

func (PlexServer) Kind() mediaserver.Kind { return mediaserver.Plex }

// Name is the Plex server's own name, as last read from it.
func (p PlexServer) Name(ctx context.Context) string { return p.Config(ctx).ServerName }

func (p PlexServer) IndexReady(ctx context.Context) bool { return p.PlexIndexReady(ctx) }

func (p PlexServer) IndexBuiltAt() time.Time { return p.PlexIndexBuiltAt() }

func (p PlexServer) IndexStale(after time.Duration) { p.PlexIndexStale(after) }

func (p PlexServer) OnIndexRebuilt(fn func()) { p.OnPlexIndexRebuilt(fn) }

// Locate is Service.Locate with the neutral item.
func (p PlexServer) Locate(ctx context.Context, media string, ids mediaserver.ExternalIDs) (mediaserver.Item, bool) {
	it, ok := p.Service.Locate(ctx, media, ids)
	if !ok {
		return mediaserver.Item{}, false
	}
	return plexItem(it), true
}

func plexItem(it plex.Item) mediaserver.Item {
	media := "movie"
	if it.Type == "show" {
		media = "series"
	}
	out := mediaserver.Item{
		Server: mediaserver.Plex, ID: it.RatingKey, Media: media, Title: it.Title, Year: it.Year,
		IDs: mediaserver.ExternalIDs{TMDB: it.TMDB, TVDB: it.TVDB, IMDB: it.IMDB},
	}
	if it.AddedAt > 0 {
		out.AddedAt = time.Unix(it.AddedAt, 0)
	}
	return out
}
