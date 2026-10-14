package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// The bundled SABnzbd is registered once its config appears (the profile switched on),
// the Prowlarr sync runs for it once, a key changed in SABnzbd is taken up, and nothing
// happens while there's no config.
func TestBundledSABWatcher(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	downloads := download.NewService(st.DB(), log)
	downloads.SetDownloader(download.KindSABnzbd, nopUsenet{})
	ini := filepath.Join(t.TempDir(), "sabnzbd.ini")
	var syncs atomic.Int32
	b := &bundledSAB{
		url: "http://arrmada-sabnzbd:8080", config: ini, downloads: downloads,
		downloadsDir: func(context.Context) string { return "" }, log: log, tuneTries: 3,
		added: func() { syncs.Add(1) },
	}
	write := func(key string) {
		t.Helper()
		if err := os.WriteFile(ini, []byte("[misc]\napi_key = "+key+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	b.step(ctx)
	if list, _ := downloads.List(ctx); len(list) != 0 || syncs.Load() != 0 {
		t.Fatalf("without a config: %+v, %d syncs", list, syncs.Load())
	}
	key1, key2 := strings.Repeat("1", 32), strings.Repeat("2", 32)
	write(key1)
	b.step(ctx)
	b.step(ctx)
	c, ok, err := downloads.BundledClient(ctx, download.KindSABnzbd)
	if err != nil || !ok || c.Password != key1 || c.URL != b.url || syncs.Load() != 1 {
		t.Fatalf("after the config appeared: %+v %v %v, %d syncs", c, ok, err, syncs.Load())
	}
	write(key2)
	b.step(ctx)
	if c, _ = downloads.Get(ctx, c.ID); c.Password != key2 || syncs.Load() != 1 {
		t.Errorf("after a key change: key taken %v, %d syncs", c.Password == key2, syncs.Load())
	}
}
