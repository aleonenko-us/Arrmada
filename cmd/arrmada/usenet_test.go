package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/store"
)

// nopUsenet is a usenet client that takes nothing; it only has to exist.
type nopUsenet struct{}

func (nopUsenet) Add(context.Context, download.Client, download.AddRequest) error { return nil }
func (nopUsenet) List(context.Context, download.Client) ([]download.Item, error) {
	return nil, nil
}
func (nopUsenet) Remove(context.Context, download.Client, string, bool) error { return nil }
func (nopUsenet) Pause(context.Context, download.Client, string) error        { return nil }
func (nopUsenet) Resume(context.Context, download.Client, string) error       { return nil }
func (nopUsenet) TorrentAction(context.Context, download.Client, string, string) error {
	return nil
}
func (nopUsenet) Test(context.Context, download.Client) error { return nil }

// A Newznab indexer is searched exactly while a usenet download client exists: not before
// SABnzbd is added, from the next search after, and not once it's switched off again.
func TestWireUsenetFollowsTheDownloadClients(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var hits atomic.Int32
	nzb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`<rss><channel></channel></rss>`))
	}))
	t.Cleanup(nzb.Close)

	indexers := indexer.NewService(st.DB(), log, nil)
	downloads := download.NewService(st.DB(), log)
	downloads.SetDownloader(download.KindSABnzbd, nopUsenet{})
	wireUsenet(indexers, downloads)
	idx, err := indexers.Create(ctx, indexer.Indexer{Name: "NZB", Kind: indexer.KindNewznab, URL: nzb.URL, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	q := indexer.SearchQuery{Text: "Dune", MediaType: indexer.MediaMovie, Limit: 10}

	if _, err := indexers.Search(ctx, q); err == nil || hits.Load() != 0 || indexers.Searched(idx) {
		t.Fatalf("without a usenet client: err %v, hits %d", err, hits.Load())
	}
	sab, err := downloads.Create(ctx, download.Client{Name: "SABnzbd", Kind: download.KindSABnzbd, URL: "http://sab.invalid", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := indexers.Search(ctx, q); err != nil || hits.Load() == 0 || !indexers.Searched(idx) {
		t.Fatalf("with a usenet client: err %v, hits %d", err, hits.Load())
	}
	sab.Enabled = false
	if _, err := downloads.Update(ctx, sab); err != nil {
		t.Fatal(err)
	}
	if indexers.Searched(idx) {
		t.Fatal("still searched with the usenet client switched off")
	}
}
