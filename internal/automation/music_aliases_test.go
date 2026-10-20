package automation

import (
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/music"
)

// withAliases gives an artist already in the harness its MusicBrainz aliases, the way a
// refresh would.
func (h *musicHarness) withAliases(t *testing.T, a music.Artist, aliases ...metadata.ArtistAlias) {
	t.Helper()
	h.fake.mu.Lock()
	d := h.fake.artists[a.MBID]
	d.Aliases = aliases
	h.fake.artists[a.MBID] = d
	h.fake.mu.Unlock()
	if _, err := h.svc.RefreshArtist(h.ctx, a.ID, music.RefreshOptions{ListOnly: true}); err != nil {
		t.Fatal(err)
	}
}

// MUS-38: a Japanese artist's album is listed under the romanised name. The search under
// the artist's own name finds nothing, so it searches again under the alias and grabs what
// that finds; a release by another artist whose name merely contains the alias is never
// taken.
func TestGrabAlbumSearchesUnderAlias(t *testing.T) {
	h := musicTestCoord(t)
	a := h.addArtist(t, "宇多田ヒカル", "", []string{"First Love"}, nil, "")
	h.withAliases(t, a, metadata.ArtistAlias{Name: "Utada Hikaru", Type: "Artist name", Primary: true})
	al := h.album(t, a.ID, "First Love")

	h.releases = func(q indexer.SearchQuery) (indexer.SearchResult, error) {
		if strings.Contains(q.Text, "Utada Hikaru") {
			return indexer.SearchResult{Releases: []indexer.Release{rel("Utada Hikaru - First Love (1999) [FLAC]")}}, nil
		}
		return indexer.SearchResult{}, nil
	}
	if out := h.c.grabAlbum(h.ctx, a, al); out.Code != outcomeGrabbed || len(h.grabs) != 1 {
		t.Fatalf("grab = %+v, grabs %v, queries %v; want the alias's release grabbed", out, h.grabs, h.queries)
	}
	if len(h.queries) != 2 || !strings.Contains(h.queries[1], "Utada Hikaru") {
		t.Errorf("queries = %v, want the name, then the alias", h.queries)
	}

	// Another artist whose name has the alias in it: found, never grabbed.
	h.grabs, h.queries = nil, nil
	al = h.album(t, a.ID, "First Love")
	h.releases = found(rel("Utada Hikaru Tribute Band - First Love [FLAC]"))
	if out := h.c.grabAlbum(h.ctx, a, al); out.Code != outcomeNoMatch || len(h.grabs) != 0 {
		t.Errorf("tribute band: %+v, grabs %v; want no match", out, h.grabs)
	}
}

// The extra searches are capped, and none is made when the artist's own name already found
// the album.
func TestAliasSearchesAreCapped(t *testing.T) {
	h := musicTestCoord(t)
	a := h.addArtist(t, "The Band", "", []string{"Album"}, nil, "")
	h.withAliases(t, a,
		metadata.ArtistAlias{Name: "Old Name One", Type: "Artist name"},
		metadata.ArtistAlias{Name: "Old Name Two", Type: "Artist name"},
		metadata.ArtistAlias{Name: "Old Name Three", Type: "Artist name"},
		metadata.ArtistAlias{Name: "Bnad", Type: "Search hint"})
	al := h.album(t, a.ID, "Album")
	h.c.grabAlbum(h.ctx, a, al)
	if len(h.queries) != 1+maxAliasQueries {
		t.Errorf("queries = %v, want the name and %d aliases", h.queries, maxAliasQueries)
	}
	for _, q := range h.queries {
		if strings.Contains(q, "Bnad") {
			t.Errorf("a search hint was searched: %v", h.queries)
		}
	}

	h.queries = nil
	al = h.album(t, a.ID, "Album")
	h.releases = found(rel("The Band - Album [FLAC]"))
	h.c.grabAlbum(h.ctx, a, al)
	if len(h.queries) != 1 {
		t.Errorf("queries = %v, want only the name's when it found the album", h.queries)
	}
}

// An import named by the alias resolves to the artist's album, but a name another library
// artist is called by stays that artist's.
func TestAlbumForReleaseByAlias(t *testing.T) {
	h := musicTestCoord(t)
	a := h.addArtist(t, "宇多田ヒカル", "", []string{"First Love"}, nil, "")
	h.withAliases(t, a, metadata.ArtistAlias{Name: "Utada Hikaru", Type: "Artist name"})
	if al, art, ok := h.c.albumForRelease(h.ctx, "Utada Hikaru - First Love (1999) [FLAC]"); !ok || art.ID != a.ID || al.Title != "First Love" {
		t.Errorf("by alias = %+v %+v %v", al, art, ok)
	}
	if _, _, ok := h.c.albumForRelease(h.ctx, "Utada Hikaru Tribute Band - First Love"); ok {
		t.Error("another artist's release resolved by an alias inside its name")
	}
}
