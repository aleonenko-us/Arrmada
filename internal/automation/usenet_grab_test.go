package automation

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/indexer"
)

// fakeUsenet is a usenet download client: it takes NZB files, names each job it took, and
// lists whatever the test puts in its queue. It records every call.
type fakeUsenet struct {
	mu    sync.Mutex
	items []download.Item
	calls []string
	files [][]byte
	urls  []string
	next  int
}

func (f *fakeUsenet) note(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func (f *fakeUsenet) callLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeUsenet) AddKeyed(_ context.Context, _ download.Client, req download.AddRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	f.files = append(f.files, req.File)
	f.urls = append(f.urls, req.URL)
	f.calls = append(f.calls, "add "+req.Name+" "+req.Category)
	return download.UsenetKey("SABnzbd_nzo_job" + string(rune('0'+f.next))), nil
}
func (f *fakeUsenet) Add(ctx context.Context, dc download.Client, req download.AddRequest) error {
	_, err := f.AddKeyed(ctx, dc, req)
	return err
}
func (f *fakeUsenet) List(context.Context, download.Client) ([]download.Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]download.Item(nil), f.items...), nil
}
func (f *fakeUsenet) Remove(_ context.Context, _ download.Client, key string, deleteData bool) error {
	f.note("remove " + key)
	return nil
}
func (f *fakeUsenet) Pause(_ context.Context, _ download.Client, key string) error {
	f.note("pause " + key)
	return nil
}
func (f *fakeUsenet) Resume(_ context.Context, _ download.Client, key string) error {
	f.note("resume " + key)
	return nil
}
func (f *fakeUsenet) TorrentAction(_ context.Context, _ download.Client, key, action string) error {
	f.note(action + " " + key)
	return nil
}
func (f *fakeUsenet) Test(context.Context, download.Client) error { return nil }

// usenetHarness is the lifecycle harness (a series with episodes, an importer, the fake
// qBittorrent behind the real download service) plus a usenet client and a Newznab
// indexer whose NZB fetch is stubbed. Removals go through the real download service.
type usenetHarness struct {
	*lifecycleHarness
	sab   *fakeUsenet
	sabID int64
	nzb   []byte
}

const testNZB = `<?xml version="1.0" encoding="UTF-8"?>
<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><file subject="x"><segments><segment number="1">a@b</segment></segments></file></nzb>`

func newUsenetHarness(t *testing.T) *usenetHarness {
	t.Helper()
	h := newLifecycleHarness(t)
	h.c.removeTorrent = nil
	sab := &fakeUsenet{}
	h.c.downloads.SetDownloader(download.KindSABnzbd, sab)
	cl, err := h.c.downloads.Create(h.ctx, download.Client{Name: "sab", Kind: download.KindSABnzbd, URL: "http://sab.invalid", Enabled: true, Priority: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.c.indexers.Create(h.ctx, indexer.Indexer{Name: "NZBIdx", Kind: indexer.KindNewznab, URL: "http://nzb.invalid", Priority: 10, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	u := &usenetHarness{lifecycleHarness: h, sab: sab, sabID: cl.ID, nzb: []byte(testNZB)}
	h.c.fetchRelease = func(_ context.Context, name, _ string) (indexer.FetchResult, error) {
		if name != "NZBIdx" {
			return indexer.FetchResult{}, errors.New("unexpected fetch from " + name)
		}
		return indexer.FetchResult{File: u.nzb, Filename: "release.nzb"}, nil
	}
	return u
}

type grabRow struct {
	hash, transport, status string
	seed                    bool
	clientID                int64
}

func grabRowOf(t *testing.T, c *Coordinator, title string) grabRow {
	t.Helper()
	var g grabRow
	var seed int
	if err := c.db.QueryRow(`SELECT info_hash, transport, status, seed_enabled, client_id FROM grabs WHERE title = ? ORDER BY id DESC LIMIT 1`, title).
		Scan(&g.hash, &g.transport, &g.status, &seed, &g.clientID); err != nil {
		t.Fatalf("grab for %q: %v", title, err)
	}
	g.seed = seed != 0
	return g
}

// A usenet grab is known by its "sab-…" key from the add to the clean-up: the grab
// records it (usenet, no seeding), the queue read pairs the job with it, the import is
// recorded under it, and clean-up removes the job from the usenet client — never asking
// the torrent client about any of it.
func TestUsenetGrabFlowsByKey(t *testing.T) {
	h := newUsenetHarness(t)
	ctx := h.ctx
	name := "Show.S01.1080p.WEB-DL-GRP"

	key, err := h.c.grabTo(ctx, "NZBIdx", "https://nzb.invalid/api?t=get&id=1&apikey=SECRET", name, seriesCategory)
	if err != nil {
		t.Fatal(err)
	}
	if download.KeyTransport(key) != download.TransportUsenet {
		t.Fatalf("grab key = %q, want a usenet key", key)
	}
	if len(h.sab.files) != 1 || string(h.sab.files[0]) != testNZB || h.sab.urls[0] != "" {
		t.Errorf("the usenet client got files %d, url %q; want the NZB as a file and no link", len(h.sab.files), h.sab.urls)
	}
	if calls := h.qbit.callLog(); len(calls) != 0 {
		t.Fatalf("the torrent client was asked: %q", calls)
	}

	h.c.recordSeriesGrab(ctx, h.seriesID, name, "NZBIdx", "", key)
	if g := grabRowOf(t, h.c, name); g.hash != key || g.transport != "usenet" || g.seed {
		t.Fatalf("grab row = %+v, want key %s, usenet, no seeding", g, key)
	}

	// The job finishes in the usenet client.
	dir := h.download(t, name, "Show.S01E01.1080p.WEB-DL-GRP.mkv", "Show.S01E02.1080p.WEB-DL-GRP.mkv")
	h.sab.mu.Lock()
	h.sab.items = []download.Item{{
		Hash: key, Name: name, State: "completed", RawState: "Completed", Progress: 1,
		SizeBytes: 120 << 20, DownloadedBytes: 120 << 20, Category: seriesCategory, ContentPath: dir,
	}}
	h.sab.mu.Unlock()
	h.c.downloads.InvalidateSnapshot()

	if _, err := h.c.ReconcileAcquisitions(ctx); err != nil {
		t.Fatal(err)
	}
	if g := grabRowOf(t, h.c, name); g.clientID != h.sabID {
		t.Errorf("reconciled grab client = %d, want the usenet client %d", g.clientID, h.sabID)
	}

	h.c.ImportSeriesDownloads(ctx)
	if !episodeHasFile(t, h.lifecycleHarness, 1, 1) || !episodeHasFile(t, h.lifecycleHarness, 1, 2) {
		t.Fatal("the usenet season pack didn't import")
	}
	if g := grabRowOf(t, h.c, name); g.status != grabStatusImported {
		t.Errorf("grab status = %q, want imported", g.status)
	}
	var imported int
	if err := h.c.db.QueryRow(`SELECT COUNT(*) FROM imports WHERE lower(download_hash) = lower(?)`, key).Scan(&imported); err != nil {
		t.Fatal(err)
	}
	if imported == 0 {
		t.Error("no import recorded under the usenet key")
	}

	// No seed goal: clean-up takes the job out of the usenet client at once.
	h.c.downloads.InvalidateSnapshot()
	h.c.ManageSeeding(ctx)
	removed := false
	for _, c := range h.sab.callLog() {
		if c == "remove "+key {
			removed = true
		}
	}
	if !removed {
		t.Errorf("clean-up didn't remove the job from the usenet client: %q", h.sab.callLog())
	}
	for _, c := range h.qbit.callLog() {
		if strings.Contains(c, "sab-") {
			t.Errorf("a usenet key reached the torrent client: %q", c)
		}
	}
}

// A usenet download held for review is found, imported and closed out by its key, like a
// torrent's hash.
func TestUsenetReviewByKey(t *testing.T) {
	h := newUsenetHarness(t)
	name := "Show.S01.1080p.WEB-DL-GRP"
	key, err := h.c.grabTo(h.ctx, "NZBIdx", "https://nzb.invalid/get", name, seriesCategory)
	if err != nil {
		t.Fatal(err)
	}
	h.c.recordSeriesGrab(h.ctx, h.seriesID, name, "NZBIdx", "", key)
	dir := h.download(t, name, "Show.S01E01.1080p.WEB-DL-GRP.mkv", "Show.S01E02.1080p.WEB-DL-GRP.mkv")
	rid := h.hold(t, key, name, dir)
	if !h.c.hasReview(h.ctx, key) {
		t.Fatal("the review isn't found by the usenet key")
	}
	if s := grabRowOf(t, h.c, name).status; s != grabStatusHeld {
		t.Fatalf("grab after the hold = %q, want held", s)
	}
	if err := h.c.ImportReview(h.ctx, rid, 0, ""); err != nil {
		t.Fatal(err)
	}
	if s := grabRowOf(t, h.c, name).status; s != grabStatusImported {
		t.Errorf("grab after the review import = %q, want imported", s)
	}
	if !episodeHasFile(t, h.lifecycleHarness, 1, 1) {
		t.Error("S01E01 has no file after the review import")
	}
}

// A torrent grab never reaches the usenet client, even when that client is first in the
// order, and is recorded exactly as before: its hash, torrent, the indexer's seed rule.
func TestTorrentGrabStaysOffUsenet(t *testing.T) {
	h := newUsenetHarness(t)
	name := "Show.S01E01.1080p.WEB-DL-GRP"
	hash, err := h.c.grabTo(h.ctx, "Fake", magnetFor(name), name, seriesCategory)
	if err != nil {
		t.Fatal(err)
	}
	if hash != hashFor(name) {
		t.Errorf("hash = %q, want %q", hash, hashFor(name))
	}
	if calls := h.sab.callLog(); len(calls) != 0 {
		t.Fatalf("a torrent reached the usenet client: %q", calls)
	}
	if calls := h.qbit.callLog(); len(calls) != 1 || !strings.HasPrefix(calls[0], "add magnet:") {
		t.Fatalf("torrent client calls = %q", calls)
	}
	h.c.recordSeriesGrab(h.ctx, h.seriesID, name, "Fake", "", hash)
	wantSeed, _, _ := h.c.seedRules(h.ctx, "Fake")
	if g := grabRowOf(t, h.c, name); g.transport != "torrent" || g.seed != wantSeed || g.hash != hash {
		t.Errorf("torrent grab row = %+v, want torrent with the indexer's seed rule (%v)", g, wantSeed)
	}
}

// A Newznab release with no usenet client is refused before anything is fetched or sent,
// with the message it has always had.
func TestUsenetGrabNeedsAUsenetClient(t *testing.T) {
	h := newLifecycleHarness(t)
	if _, err := h.c.indexers.Create(h.ctx, indexer.Indexer{Name: "NZBIdx", Kind: indexer.KindNewznab, URL: "http://nzb.invalid", Priority: 10, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	fetched := false
	h.c.fetchRelease = func(context.Context, string, string) (indexer.FetchResult, error) {
		fetched = true
		return indexer.FetchResult{File: []byte(testNZB)}, nil
	}
	_, err := h.c.grabTo(h.ctx, "NZBIdx", "https://nzb.invalid/get", "Show.S01.1080p-GRP", seriesCategory)
	if err == nil || !strings.Contains(err.Error(), "no usenet download client is configured") {
		t.Fatalf("err = %v", err)
	}
	if fetched || len(h.qbit.callLog()) != 0 {
		t.Errorf("fetched %v, torrent client calls %q", fetched, h.qbit.callLog())
	}
}

// The NZB link is never handed to a client: a fetch that yields no file fails the grab.
func TestUsenetGrabNeverSendsTheLink(t *testing.T) {
	h := newUsenetHarness(t)
	h.c.fetchRelease = func(_ context.Context, _, u string) (indexer.FetchResult, error) {
		return indexer.FetchResult{URL: u}, nil
	}
	if _, err := h.c.grabTo(h.ctx, "NZBIdx", "https://nzb.invalid/get?apikey=SECRET", "Show.S01.1080p-GRP", seriesCategory); err == nil {
		t.Fatal("a grab with no NZB file succeeded")
	}
	if len(h.sab.callLog()) != 0 || len(h.qbit.callLog()) != 0 {
		t.Errorf("calls: usenet %q, torrent %q", h.sab.callLog(), h.qbit.callLog())
	}
}

// An uploaded .nzb goes to the usenet client, and its grab doesn't seed even though a
// manual grab otherwise defaults to seeding.
func TestManualNZBUploadIsUsenet(t *testing.T) {
	h := newUsenetHarness(t)
	title := "Show.S01E02.1080p.WEB-DL-GRP"
	if err := h.c.GrabSeriesTorrent(h.ctx, h.seriesID, []byte(testNZB), "upload.nzb", title); err != nil {
		t.Fatal(err)
	}
	if calls := h.sab.callLog(); len(calls) != 1 {
		t.Fatalf("usenet client calls = %q", calls)
	}
	if calls := h.qbit.callLog(); len(calls) != 0 {
		t.Fatalf("an NZB reached the torrent client: %q", calls)
	}
	g := grabRowOf(t, h.c, title)
	if g.transport != "usenet" || g.seed || download.KeyTransport(g.hash) != download.TransportUsenet {
		t.Errorf("manual NZB grab = %+v, want a usenet key, usenet, no seeding", g)
	}

	// An uploaded .torrent still goes to the torrent client and seeds by default.
	tor := []byte("d4:infod4:name4:testee")
	if err := h.c.GrabSeriesTorrent(h.ctx, h.seriesID, tor, "x.torrent", "Show.S01E01.Torrent-GRP"); err != nil {
		t.Fatal(err)
	}
	if len(h.sab.callLog()) != 1 || len(h.qbit.callLog()) != 1 {
		t.Errorf("after a .torrent upload: usenet %q, torrent %q", h.sab.callLog(), h.qbit.callLog())
	}
	if g := grabRowOf(t, h.c, "Show.S01E01.Torrent-GRP"); g.transport != "torrent" || !g.seed {
		t.Errorf("manual torrent grab = %+v", g)
	}
}

// Removing a usenet download from the Downloads page goes to the usenet client by its key;
// a key that names more than one download is refused.
func TestRemoveDownloadByUsenetKey(t *testing.T) {
	h := newUsenetHarness(t)
	key := download.UsenetKey("SABnzbd_nzo_abc")
	if _, err := h.c.RemoveDownload(h.ctx, key, "Show.S01-GRP", RemoveKeepFiles, false); err != nil {
		t.Fatal(err)
	}
	if calls := h.sab.callLog(); len(calls) != 1 || calls[0] != "remove "+key {
		t.Errorf("usenet client calls = %q", calls)
	}
	for _, bad := range []string{"sab-", "sab-a,sab-b", "all", "SAB-ABC"} {
		if _, err := h.c.RemoveDownload(h.ctx, bad, "", RemoveDeleteFiles, false); !errors.Is(err, download.ErrInvalidHash) {
			t.Errorf("RemoveDownload(%q) err = %v", bad, err)
		}
	}
	if len(h.qbit.callLog()) != 0 {
		t.Errorf("torrent client calls = %q", h.qbit.callLog())
	}
}
