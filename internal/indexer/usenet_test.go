package indexer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Without a usenet download client a Newznab indexer gets no search or feed requests: its
// results could never be downloaded. A search with nothing else to ask is "no indexer",
// not an empty result.
func TestUsenetIndexerNotQueriedWithoutClient(t *testing.T) {
	var hits atomic.Int32
	nzb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`<rss><channel></channel></rss>`))
	}))
	t.Cleanup(nzb.Close)
	s, _, _ := healthService(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, Indexer{Name: "NZB", Kind: KindNewznab, URL: nzb.URL, Enabled: true}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Search(WithInteractive(ctx), dune); !errors.Is(err, ErrNoIndexers) {
		t.Fatalf("search = %v", err)
	}
	_, _ = s.Recent(ctx, 50)
	if hits.Load() != 0 {
		t.Fatalf("the usenet indexer got %d requests", hits.Load())
	}

	// Were a usenet client ever added, it would be asked again.
	s.SetUsenetAvailable(func() bool { return true })
	if _, err := s.Search(ctx, dune); err != nil {
		t.Fatal(err)
	}
	if hits.Load() == 0 {
		t.Fatal("with a usenet client the indexer should be searched")
	}
}

// With a usenet download client, a Prowlarr sync imports Prowlarr's usenet indexers as
// Newznab rows (no seeding); without one they stay out, counted as skipped.
func TestProwlarrImportsUsenetIndexersOnlyWithAClient(t *testing.T) {
	f := newFakeProwlarr(t)
	s, _, _ := healthService(t)
	if res := f.sync(t, s); res.SkippedUsenet != 1 {
		t.Fatalf("without a client: %+v", res)
	}
	if _, ok := byProwlarrID(t, s)[3]; ok {
		t.Fatal("a usenet indexer was imported without a usenet client")
	}

	s.SetUsenetAvailable(func() bool { return true })
	res := f.sync(t, s)
	if res.SkippedUsenet != 0 || res.Added != 1 {
		t.Fatalf("with a client: %+v", res)
	}
	row, ok := byProwlarrID(t, s)[3]
	if !ok {
		t.Fatal("the usenet indexer wasn't imported")
	}
	if row.Kind != KindNewznab || row.Transport() != TransportUsenet || !row.Enabled || row.SeedEnabled || row.URL != f.srv.URL+"/3/api" {
		t.Fatalf("usenet row = %+v", row)
	}
	if !s.Searched(row) {
		t.Error("the imported usenet row isn't searched")
	}
}

// A usenet row a sync switched off for want of a client — synced as Torznab long ago —
// comes back on, as Newznab, at the first sync after a usenet client is added. One the
// owner switched off stays off.
func TestProwlarrReenablesUsenetRowOnceAClientExists(t *testing.T) {
	f := newFakeProwlarr(t)
	s, _, _ := healthService(t)
	ctx := context.Background()
	old, err := s.Create(ctx, Indexer{Name: "Prowlarr · NZBgeek", Kind: KindTorznab, URL: f.srv.URL + "/3/api", APIKey: "prowlarr-key", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	f.sync(t, s)
	if got, _ := s.Get(ctx, old.ID); got.Enabled || got.ManagedNote != NoteNeedsUsenetClient {
		t.Fatalf("without a client: %+v", got)
	}

	s.SetUsenetAvailable(func() bool { return true })
	res := f.sync(t, s)
	if res.Reenabled != 1 || res.SkippedUsenet != 0 || res.Added != 0 {
		t.Fatalf("with a client: %+v", res)
	}
	got, _ := s.Get(ctx, old.ID)
	if !got.Enabled || got.ManagedNote != "" || got.DisabledBy != "" || got.Kind != KindNewznab {
		t.Fatalf("usenet row after the client was added = %+v", got)
	}

	// The owner's own switch-off is theirs.
	if err := setManagedState(ctx, s.repo.db, old.ID, false, DisabledByUser, ""); err != nil {
		t.Fatal(err)
	}
	f.sync(t, s)
	if got, _ := s.Get(ctx, old.ID); got.Enabled {
		t.Fatal("a sync switched on a usenet row the owner turned off")
	}
}
