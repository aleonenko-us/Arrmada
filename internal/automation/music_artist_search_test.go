package automation

import (
	"testing"

	"github.com/tristenlammi/arrmada/internal/indexer"
)

// MUS-09: an album's search writes one History line per change of result, not one per
// sweep; a grab writes its own.
func TestAlbumSearchedEventsOnlyOnChange(t *testing.T) {
	h := musicTestCoord(t)
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	sweep := WithSearchTrigger(h.ctx, TriggerSweep)

	h.releases = found(rel("Muse - Absolution (2003) [FLAC]"))
	h.c.grabAlbum(sweep, a, al)
	h.c.grabAlbum(sweep, a, al)
	if n := artistEvents(t, h.svc, a.ID, "OK Computer:"); n != 1 {
		t.Fatalf("searched lines after two identical searches = %d, want 1", n)
	}
	h.releases = func(indexer.SearchQuery) (indexer.SearchResult, error) { return indexer.SearchResult{}, nil }
	h.c.grabAlbum(sweep, a, al)
	if n := artistEvents(t, h.svc, a.ID, "OK Computer:"); n != 2 {
		t.Fatalf("searched lines after a different result = %d, want 2", n)
	}
	h.releases = found(rel("Radiohead - OK Computer (1997) [FLAC]"))
	h.c.grabAlbum(sweep, a, al)
	if n := artistEvents(t, h.svc, a.ID, "OK Computer:"); n != 2 {
		t.Errorf("a grab added a searched line (%d), want only its grabbed line", n)
	}
}
