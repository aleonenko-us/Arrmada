package indexer

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/store"
)

// INT-20: Recent is the browse API's "new" filter, newest first, in the indexer's
// categories, through the logged-in session — and it honours the limit.
func TestTorrentLeechRecentAsksForNewUploads(t *testing.T) {
	var _ Recenter = (*TorrentLeechSearcher)(nil)
	st, tl := newTLStandIn(t)
	idx := Indexer{ID: 7, Name: "TorrentLeech", Kind: KindTorrentLeech, Username: "u", Password: "right"}

	rels, err := tl.Recent(context.Background(), idx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 2 {
		t.Fatalf("got %d releases, want 2", len(rels))
	}
	if st.logins.Load() != 1 {
		t.Fatalf("logins = %d, want one session", st.logins.Load())
	}
	st.mu.Lock()
	path := st.paths[0]
	st.mu.Unlock()
	if !strings.Contains(path, "/newfilter/2") || !strings.HasSuffix(path, "/orderby/added/order/desc") || strings.Contains(path, "/query/") {
		t.Fatalf("browse path = %q, want the new-uploads filter, newest first, no query", path)
	}
	if !strings.Contains(path, "/categories/"+tlDefaultCats) {
		t.Fatalf("browse path = %q, want the default Movies + TV categories", path)
	}

	// A scoped indexer's categories are used, and the limit trims the page.
	idx.Categories = []int{14}
	rels, err = tl.Recent(context.Background(), idx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 1 {
		t.Fatalf("limit 1 gave %d releases", len(rels))
	}
	st.mu.Lock()
	path = st.paths[1]
	st.mu.Unlock()
	if !strings.Contains(path, "/categories/14/newfilter/2") {
		t.Fatalf("scoped browse path = %q", path)
	}
}

// The service's feed now includes TorrentLeech: RSS sync no longer skips it.
func TestFetchRecentIncludesTorrentLeech(t *testing.T) {
	_, standIn := newTLStandIn(t)
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := NewService(db.DB(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	searcher, err := s.registry.For(KindTorrentLeech)
	if err != nil {
		t.Fatal(err)
	}
	tl := searcher.(*TorrentLeechSearcher)
	tl.base, tl.delay = standIn.base, 0
	if _, err := s.Create(context.Background(), Indexer{Name: "TorrentLeech", Kind: KindTorrentLeech, Username: "u", Password: "right", Priority: 10, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	res, err := s.Recent(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Releases) != 2 || res.Releases[0].Indexer != "TorrentLeech" {
		t.Fatalf("feed = %+v, want TorrentLeech's 2", res.Releases)
	}
}

// TorrentLeech keeps no "RSS key": one sent on create or edit isn't stored.
func TestTorrentLeechStoresNoRSSKey(t *testing.T) {
	s, _, _ := healthService(t)
	ctx := context.Background()
	idx, err := s.Create(ctx, Indexer{Name: "TL", Kind: KindTorrentLeech, Username: "u", Password: "p", APIKey: "rss-secret", Priority: 10, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, idx.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.APIKey != "" {
		t.Fatal("a TorrentLeech RSS key was stored")
	}
	got.APIKey = "rss-secret-2"
	if err := s.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.Get(ctx, idx.ID); again.APIKey != "" {
		t.Fatal("an edit stored a TorrentLeech RSS key")
	}
	// Other kinds keep their keys.
	tz, err := s.Create(ctx, Indexer{Name: "Feed", Kind: KindTorznab, URL: "http://x.test/api", APIKey: "torznab-key", Priority: 10, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, tz.ID); got.APIKey != "torznab-key" {
		t.Fatal("a Torznab key was dropped")
	}
}
