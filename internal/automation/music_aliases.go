package automation

import (
	"context"

	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/music"
)

// maxAliasQueries caps the extra searches one album search makes under the artist's
// aliases (MUS-38). Every query is an indexer request against the shared throttle, and the
// artist's own name has already been tried: two more cover a romanised name and a legal or
// old one.
const maxAliasQueries = 2

// searchAliases searches again under the artist's aliases when nothing the artist's own
// name found names this album: a Japanese artist's releases are usually listed under the
// romanised name, a band's early ones under the name it had then. It stops at the first
// alias whose releases name the album, after maxAliasQueries, or when the context ends;
// what it finds is added to res. A failed alias search changes nothing: the first search's
// answer (and its errors) is what the outcome is judged on.
func (c *Coordinator) searchAliases(ctx context.Context, res indexer.SearchResult, names music.ArtistNames, al music.Album) indexer.SearchResult {
	if len(names.Search) == 0 {
		return res
	}
	if namesAlbum(res.Releases, names, al) {
		return res
	}
	if len(res.Releases) == 0 && len(res.Errors) > 0 && len(res.Errors) >= res.Asked {
		return res // every indexer failed: asking them again under another name won't help
	}
	seen := map[string]bool{}
	for _, r := range res.Releases {
		seen[r.Indexer+"\x00"+r.Title] = true
	}
	tried := 0
	for _, alias := range names.Search {
		if tried >= maxAliasQueries || ctx.Err() != nil {
			break
		}
		tried++
		more, err := c.searchMusic(ctx, indexer.SearchQuery{
			Text: alias + " " + al.Title, Artist: alias, Album: al.Title, MediaType: indexer.MediaMusic, Limit: 100,
		})
		if err != nil {
			c.log.Info("music: a search under the artist's alias failed", "alias", alias, "album", al.Title, "err", err)
			continue
		}
		for _, r := range more.Releases {
			if k := r.Indexer + "\x00" + r.Title; !seen[k] {
				seen[k] = true
				res.Releases = append(res.Releases, r)
			}
		}
		if namesAlbum(more.Releases, names, al) {
			c.log.Info("music: found the album under the artist's alias", "artist", names.Name, "alias", alias, "album", al.Title)
			break
		}
	}
	return res
}

// namesAlbum reports whether any release names the album under one of the artist's names.
func namesAlbum(rels []indexer.Release, names music.ArtistNames, al music.Album) bool {
	for _, r := range rels {
		if names.ReleaseIsForAlbum(r.Title, al.Title) {
			return true
		}
	}
	return false
}
