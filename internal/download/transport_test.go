package download

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// recClient is a fake download client that records every call by client name. One
// instance serves every client of its kind (as the registry does); the Client it is
// handed says which one is being asked. Clients named in down never answer.
type recClient struct {
	mu    sync.Mutex
	calls []string                      // "<client> <op> <key>"
	items map[string][]Item             // what each client holds, by name
	down  map[string]bool               // clients that can't be connected to
	key   func(dc Client, n int) string // the key AddKeyed answers; nil = not a keyed adder
	adds  int
}

func (f *recClient) note(dc Client, op, key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, dc.Name+" "+op+" "+key)
}

func (f *recClient) log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *recClient) List(_ context.Context, dc Client) ([]Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down[dc.Name] {
		return nil, notSent(errors.New("connection refused"))
	}
	return append([]Item(nil), f.items[dc.Name]...), nil
}
func (f *recClient) Add(_ context.Context, dc Client, req AddRequest) error {
	if f.down[dc.Name] {
		return notSent(errors.New("connection refused"))
	}
	f.note(dc, "add", req.Name)
	return nil
}
func (f *recClient) Remove(_ context.Context, dc Client, key string, _ bool) error {
	f.note(dc, "remove", key)
	return nil
}
func (f *recClient) Pause(_ context.Context, dc Client, key string) error {
	f.note(dc, "pause", key)
	return nil
}
func (f *recClient) Resume(_ context.Context, dc Client, key string) error {
	f.note(dc, "resume", key)
	return nil
}
func (f *recClient) TorrentAction(_ context.Context, dc Client, key, action string) error {
	f.note(dc, action, key)
	return nil
}
func (f *recClient) SetCategory(_ context.Context, dc Client, key, category string) error {
	f.note(dc, "category", key)
	return nil
}
func (f *recClient) Test(context.Context, Client) error { return nil }

// keyedRecClient is recClient as a usenet client: it names the job it took.
type keyedRecClient struct{ *recClient }

func (f keyedRecClient) AddKeyed(ctx context.Context, dc Client, req AddRequest) (string, error) {
	if err := f.Add(ctx, dc, req); err != nil {
		return "", err
	}
	f.mu.Lock()
	f.adds++
	n := f.adds
	f.mu.Unlock()
	return f.key(dc, n), nil
}

// transportFixture is a service with a fake torrent client kind and a fake usenet kind.
func transportFixture(t *testing.T) (*Service, *recClient, *recClient) {
	t.Helper()
	svc, _ := newTestService(t)
	tor := &recClient{items: map[string][]Item{}, down: map[string]bool{}}
	nzb := &recClient{items: map[string][]Item{}, down: map[string]bool{},
		key: func(dc Client, n int) string { return fmt.Sprintf("sab-%s_nzo_%d", strings.ToLower(dc.Name), n) }}
	svc.SetDownloader(KindQbittorrent, tor)
	svc.SetDownloader(KindSABnzbd, keyedRecClient{nzb})
	return svc, tor, nzb
}

func mustCreate(t *testing.T, svc *Service, c Client) Client {
	t.Helper()
	c.Enabled = true
	got, err := svc.Create(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

const testMagnetHash = "0123456789abcdef0123456789abcdef01234567"

// A torrent never goes to a usenet client and an NZB never to a torrent client, even when
// that client is first in the order, or the only one there is.
func TestAddDownloadStaysOnItsTransport(t *testing.T) {
	ctx := context.Background()
	torrent := AddRequest{Name: "Film.2020.1080p", URL: "magnet:?xt=urn:btih:" + testMagnetHash}
	nzb := AddRequest{Name: "Film.2020.1080p.NZB", File: []byte("<nzb/>"), Filename: "f.nzb", Transport: TransportUsenet}

	t.Run("only a usenet client: the torrent is refused", func(t *testing.T) {
		svc, tor, sab := transportFixture(t)
		mustCreate(t, svc, Client{Name: "SAB", Kind: KindSABnzbd, URL: "http://sab"})
		if err := svc.Add(ctx, torrent); err == nil {
			t.Fatal("a torrent was accepted with only a usenet client")
		}
		if len(sab.log()) != 0 || len(tor.log()) != 0 {
			t.Errorf("calls: torrent %q, usenet %q", tor.log(), sab.log())
		}
	})

	t.Run("only a torrent client: the NZB is refused", func(t *testing.T) {
		svc, tor, sab := transportFixture(t)
		mustCreate(t, svc, Client{Name: "QB", Kind: KindQbittorrent, URL: "http://qb"})
		if _, err := svc.AddDownload(ctx, nzb); !errors.Is(err, ErrNoUsenetClient) {
			t.Fatalf("err = %v, want ErrNoUsenetClient", err)
		}
		if len(sab.log()) != 0 || len(tor.log()) != 0 {
			t.Errorf("calls: torrent %q, usenet %q", tor.log(), sab.log())
		}
	})

	t.Run("both: each goes to its own, by key", func(t *testing.T) {
		svc, tor, sab := transportFixture(t)
		// The usenet client is first in the order: that must not matter to a torrent.
		s := mustCreate(t, svc, Client{Name: "SAB", Kind: KindSABnzbd, URL: "http://sab", Priority: 1})
		q := mustCreate(t, svc, Client{Name: "QB", Kind: KindQbittorrent, URL: "http://qb", Priority: 2})
		got, err := svc.AddDownload(ctx, torrent)
		if err != nil {
			t.Fatal(err)
		}
		if got.ClientID != q.ID || got.Key != testMagnetHash {
			t.Errorf("torrent added = %+v, want client %d key %s", got, q.ID, testMagnetHash)
		}
		got, err = svc.AddDownload(ctx, nzb)
		if err != nil {
			t.Fatal(err)
		}
		if got.ClientID != s.ID || got.Key != "sab-sab_nzo_1" {
			t.Errorf("nzb added = %+v, want client %d key sab-sab_nzo_1", got, s.ID)
		}
		if l := tor.log(); len(l) != 1 || l[0] != "QB add Film.2020.1080p" {
			t.Errorf("torrent client calls = %q", l)
		}
		if l := sab.log(); len(l) != 1 || l[0] != "SAB add Film.2020.1080p.NZB" {
			t.Errorf("usenet client calls = %q", l)
		}
	})

	t.Run("fail-over stays within the transport", func(t *testing.T) {
		svc, tor, sab := transportFixture(t)
		mustCreate(t, svc, Client{Name: "QB", Kind: KindQbittorrent, URL: "http://qb", Priority: 1})
		mustCreate(t, svc, Client{Name: "SAB1", Kind: KindSABnzbd, URL: "http://sab1", Priority: 2})
		s2 := mustCreate(t, svc, Client{Name: "SAB2", Kind: KindSABnzbd, URL: "http://sab2", Priority: 3})
		sab.down["SAB1"] = true
		got, err := svc.AddDownload(ctx, nzb)
		if err != nil {
			t.Fatal(err)
		}
		if got.ClientID != s2.ID || !strings.HasPrefix(got.Key, "sab-sab2_nzo_") {
			t.Errorf("added = %+v, want the second usenet client", got)
		}
		// Every usenet client down: an error, and still nothing to the torrent client.
		sab.down["SAB2"] = true
		if _, err := svc.AddDownload(ctx, nzb); err == nil || !NeverReached(err) {
			t.Errorf("err = %v, want unreachable", err)
		}
		if l := tor.log(); len(l) != 0 {
			t.Errorf("an NZB reached the torrent client: %q", l)
		}
		// And the other way: the torrent client down never sends a torrent to usenet.
		tor.down["QB"] = true
		sab.down = map[string]bool{}
		before := len(sab.log())
		if err := svc.Add(ctx, torrent); err == nil || !NeverReached(err) {
			t.Errorf("err = %v, want unreachable", err)
		}
		if len(sab.log()) != before {
			t.Errorf("a torrent reached a usenet client: %q", sab.log()[before:])
		}
	})

	t.Run("an unusable key from the client is dropped, not trusted", func(t *testing.T) {
		svc, _, sab := transportFixture(t)
		mustCreate(t, svc, Client{Name: "SAB", Kind: KindSABnzbd, URL: "http://sab"})
		for _, bad := range []string{"all", "sab-", strings.Repeat("ab", 20), "sab-a,sab-b"} {
			sab.key = func(Client, int) string { return bad }
			got, err := svc.AddDownload(ctx, nzb)
			if err != nil {
				t.Fatalf("%q: %v", bad, err)
			}
			if got.Key != "" {
				t.Errorf("client answered %q: key = %q, want none", bad, got.Key)
			}
		}
	})
}

// Commands go to the client holding the download, and only that one; Pause all and
// Resume all reach every client.
func TestCommandsGoToTheHoldingClient(t *testing.T) {
	ctx := context.Background()
	svc, tor, sab := transportFixture(t)
	mustCreate(t, svc, Client{Name: "QB1", Kind: KindQbittorrent, URL: "http://qb1", Priority: 1})
	mustCreate(t, svc, Client{Name: "QB2", Kind: KindQbittorrent, URL: "http://qb2", Priority: 2})
	mustCreate(t, svc, Client{Name: "SAB", Kind: KindSABnzbd, URL: "http://sab", Priority: 3})
	inQB2 := strings.Repeat("b2", 20)
	job := "sab-sabnzbd_nzo_x1"
	tor.items["QB1"] = []Item{{Hash: strings.Repeat("a1", 20), Name: "One"}}
	tor.items["QB2"] = []Item{{Hash: inQB2, Name: "Two"}}
	sab.items["SAB"] = []Item{{Hash: job, Name: "Three"}}
	snap, err := svc.Snapshot(ctx)
	if err != nil || len(snap.Items) != 3 {
		t.Fatalf("snapshot: %+v %v", snap.Items, err)
	}
	for _, it := range snap.Items {
		want := TransportTorrent
		if it.Hash == job {
			want = TransportUsenet
		}
		if it.Transport != want {
			t.Errorf("item %s transport = %q, want %q", it.Hash, it.Transport, want)
		}
	}

	for name, run := range map[string]func(key string) error{
		"pause":    func(k string) error { return svc.Pause(ctx, k) },
		"resume":   func(k string) error { return svc.Resume(ctx, k) },
		"recheck":  func(k string) error { return svc.Action(ctx, k, "recheck") },
		"category": func(k string) error { return svc.SetCategory(ctx, k, CategoryTV) },
		"remove":   func(k string) error { return svc.Remove(ctx, k, false) },
	} {
		tor.calls, sab.calls = nil, nil
		if err := run(inQB2); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if l := tor.log(); len(l) != 1 || l[0] != "QB2 "+name+" "+inQB2 {
			t.Errorf("%s of QB2's torrent: torrent calls %q", name, l)
		}
		if l := sab.log(); len(l) != 0 {
			t.Errorf("%s of a torrent reached the usenet client: %q", name, l)
		}
		tor.calls, sab.calls = nil, nil
		if err := run(job); err != nil {
			t.Fatalf("%s job: %v", name, err)
		}
		if l := sab.log(); len(l) != 1 || l[0] != "SAB "+name+" "+job {
			t.Errorf("%s of the usenet job: usenet calls %q", name, l)
		}
		if l := tor.log(); len(l) != 0 {
			t.Errorf("%s of a usenet job reached a torrent client: %q", name, l)
		}
	}

	// Pause all / Resume all: every client, whatever its transport.
	tor.calls, sab.calls = nil, nil
	if err := svc.Pause(ctx, PauseAll); err != nil {
		t.Fatal(err)
	}
	if err := svc.Resume(ctx, PauseAll); err != nil {
		t.Fatal(err)
	}
	if l := tor.log(); len(l) != 4 {
		t.Errorf("pause/resume all on the torrent clients = %q, want both clients twice", l)
	}
	if l := sab.log(); len(l) != 2 || l[0] != "SAB pause all" || l[1] != "SAB resume all" {
		t.Errorf("pause/resume all on the usenet client = %q", l)
	}

	// A download no read has seen goes only to clients of its own transport.
	tor.calls, sab.calls = nil, nil
	if err := svc.Remove(ctx, "sab-unknown_job", true); err != nil {
		t.Fatal(err)
	}
	if l := tor.log(); len(l) != 0 {
		t.Errorf("an unseen usenet key reached a torrent client: %q", l)
	}
	if l := sab.log(); len(l) != 1 || l[0] != "SAB remove sab-unknown_job" {
		t.Errorf("unseen usenet key: usenet calls %q", l)
	}
	tor.calls, sab.calls = nil, nil
	unseen := strings.Repeat("cc", 20)
	if err := svc.Remove(ctx, unseen, true); err != nil {
		t.Fatal(err)
	}
	if l := sab.log(); len(l) != 0 {
		t.Errorf("an unseen torrent reached the usenet client: %q", l)
	}
	if l := tor.log(); len(l) != 2 {
		t.Errorf("an unseen torrent with two torrent clients: calls %q, want both asked", l)
	}

	// ResumeMany: torrents joined to the torrent clients, the job to its own client alone.
	tor.calls, sab.calls = nil, nil
	if err := svc.ResumeMany(ctx, []string{inQB2, job}); err != nil {
		t.Fatal(err)
	}
	if l := sab.log(); len(l) != 1 || l[0] != "SAB resume "+job {
		t.Errorf("ResumeMany usenet calls = %q", l)
	}
	for _, c := range tor.log() {
		if strings.Contains(c, "sab-") {
			t.Errorf("a usenet key reached a torrent client: %q", c)
		}
	}
}

// HasTransport is cached, and creating, editing or deleting a client drops the cache.
func TestHasTransportCache(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := transportFixture(t)
	if svc.HasTransport(ctx, TransportUsenet) || svc.HasTransport(ctx, TransportTorrent) {
		t.Fatal("no clients, but a transport is available")
	}
	sab := mustCreate(t, svc, Client{Name: "SAB", Kind: KindSABnzbd, URL: "http://sab"})
	if !svc.HasTransport(ctx, TransportUsenet) || svc.HasTransport(ctx, TransportTorrent) {
		t.Fatal("after adding a usenet client")
	}

	// A row written behind the service's back isn't seen: the answer is cached.
	if _, err := svc.repo.Create(ctx, Client{Name: "QB", Kind: KindQbittorrent, URL: "http://qb", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if svc.HasTransport(ctx, TransportTorrent) {
		t.Fatal("HasTransport isn't cached")
	}

	// An edit drops the cache: switching the usenet client off.
	if _, err := svc.Update(ctx, Client{ID: sab.ID, Name: "SAB", URL: "http://sab", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if svc.HasTransport(ctx, TransportUsenet) || !svc.HasTransport(ctx, TransportTorrent) {
		t.Fatal("after switching the usenet client off")
	}
	if _, err := svc.Update(ctx, Client{ID: sab.ID, Name: "SAB", URL: "http://sab", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if !svc.HasTransport(ctx, TransportUsenet) {
		t.Fatal("after switching it back on")
	}
	if err := svc.Delete(ctx, sab.ID); err != nil {
		t.Fatal(err)
	}
	if svc.HasTransport(ctx, TransportUsenet) {
		t.Fatal("after deleting the usenet client")
	}

	// A kind with no implementation can't take anything.
	bare, _ := newTestService(t)
	delete(bare.registry.impls, KindSABnzbd)
	mustCreate(t, bare, Client{Name: "SAB", Kind: KindSABnzbd, URL: "http://sab"})
	if bare.HasTransport(ctx, TransportUsenet) {
		t.Error("a usenet client with no implementation counted as available")
	}
	var nilSvc *Service
	if nilSvc.HasTransport(ctx, TransportTorrent) {
		t.Error("a nil service has a transport")
	}
}

// Each kind has its own bundled client and removed flag. The qBittorrent key is the one it
// has always been, and the qBittorrent tuning never touches a bundled SABnzbd.
func TestBundledPerKind(t *testing.T) {
	ctx := context.Background()
	svc, flags := newTestService(t)
	const qbURL, sabURL = "http://arrmada-qbittorrent:8080", "http://arrmada-sabnzbd:8080"
	if err := svc.EnsureBundledOf(ctx, KindSABnzbd, sabURL); err != nil {
		t.Fatal(err)
	}
	// Only a bundled SABnzbd: the qBittorrent tuning has nothing to act on.
	if err := svc.SetBundledPort(ctx, 6881); err != ErrBundledInactive {
		t.Errorf("SetBundledPort with only a bundled SABnzbd = %v, want ErrBundledInactive", err)
	}
	if _, ok, _ := svc.repo.Bundled(ctx); ok {
		t.Error("Repo.Bundled found the SABnzbd row")
	}
	// The bundled SABnzbd doesn't stand in for the bundled qBittorrent.
	if err := svc.EnsureBundled(ctx, qbURL); err != nil {
		t.Fatal(err)
	}
	all, _ := svc.List(ctx)
	if len(all) != 2 {
		t.Fatalf("clients = %+v, want both bundled", all)
	}
	sab, ok, err := svc.repo.BundledOf(ctx, KindSABnzbd)
	if err != nil || !ok || sab.URL != sabURL || sab.Name != "SABnzbd (bundled)" {
		t.Fatalf("bundled SABnzbd = %+v %v %v", sab, ok, err)
	}
	qb, ok, _ := svc.repo.Bundled(ctx)
	if !ok || qb.Kind != KindQbittorrent || qb.Name != "qBittorrent (bundled)" {
		t.Fatalf("bundled qBittorrent = %+v", qb)
	}

	// Deleting the bundled SABnzbd sets its own flag, not qBittorrent's.
	if err := svc.Delete(ctx, sab.ID); err != nil {
		t.Fatal(err)
	}
	if flags.Get(ctx, KeyBundledRemovedSABnzbd, "") != "1" || flags.Get(ctx, KeyBundledRemoved, "") != "" {
		t.Errorf("flags after removing the bundled SABnzbd: %v", flags.m)
	}
	if err := svc.EnsureBundledOf(ctx, KindSABnzbd, sabURL); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := svc.repo.BundledOf(ctx, KindSABnzbd); ok {
		t.Error("a removed bundled SABnzbd came back")
	}
	if err := svc.RestoreBundledOf(ctx, KindSABnzbd, sabURL); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := svc.repo.BundledOf(ctx, KindSABnzbd); !ok {
		t.Error("restore didn't bring the bundled SABnzbd back")
	}

	// A qBittorrent at the SABnzbd's URL isn't taken for it (URL match is per kind).
	svc2, _ := newTestService(t)
	mustCreate(t, svc2, Client{Name: "odd", Kind: KindQbittorrent, URL: sabURL})
	if err := svc2.EnsureBundledOf(ctx, KindSABnzbd, sabURL); err != nil {
		t.Fatal(err)
	}
	if c, ok, _ := svc2.repo.BundledOf(ctx, KindSABnzbd); !ok || c.Kind != KindSABnzbd {
		t.Errorf("bundled SABnzbd = %+v, %v", c, ok)
	}
	if _, ok, _ := svc2.repo.Bundled(ctx); ok {
		t.Error("the qBittorrent row was marked as the bundled qBittorrent by a SABnzbd URL")
	}
}
