package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/download"
)

// fakeUsenetClient is a usenet download client that records the commands it receives.
type fakeUsenetClient struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeUsenetClient) note(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func (f *fakeUsenetClient) log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeUsenetClient) Add(context.Context, download.Client, download.AddRequest) error {
	return nil
}
func (f *fakeUsenetClient) List(context.Context, download.Client) ([]download.Item, error) {
	return nil, nil
}
func (f *fakeUsenetClient) Remove(_ context.Context, _ download.Client, key string, _ bool) error {
	f.note("remove " + key)
	return nil
}
func (f *fakeUsenetClient) Pause(_ context.Context, _ download.Client, key string) error {
	f.note("pause " + key)
	return nil
}
func (f *fakeUsenetClient) Resume(_ context.Context, _ download.Client, key string) error {
	f.note("resume " + key)
	return nil
}
func (f *fakeUsenetClient) TorrentAction(_ context.Context, _ download.Client, key, action string) error {
	f.note(action + " " + key)
	return nil
}
func (f *fakeUsenetClient) Test(context.Context, download.Client) error { return nil }

// The queue routes take a usenet job's "sab-…" key as they take a torrent hash, send it
// only to the usenet client, and still refuse anything that names more than one download.
func TestQueueRoutesTakeUsenetKeys(t *testing.T) {
	s, q, mgr := downloadServer(t)
	sab := &fakeUsenetClient{}
	s.deps.Downloads.SetDownloader(download.KindSABnzbd, sab)
	if _, err := s.deps.Downloads.Create(context.Background(), download.Client{Name: "sab", Kind: download.KindSABnzbd, URL: "http://sab.invalid", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	key := "sab-sabnzbd_nzo_p86tgx"
	for _, r := range []struct{ method, path, body string }{
		{"POST", "/api/v1/queue/" + key + "/pause", ""},
		{"POST", "/api/v1/queue/" + key + "/resume", ""},
		{"POST", "/api/v1/queue/" + key + "/action", `{"action":"recheck"}`},
		{"DELETE", "/api/v1/queue/" + key, ""},
	} {
		rec := s.doBody(r.method, r.path, r.body, mgr)
		if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
			t.Errorf("%s %s: HTTP %d %s", r.method, r.path, rec.Code, rec.Body)
		}
	}
	want := []string{"pause " + key, "resume " + key, "recheck " + key, "remove " + key}
	if got := sab.log(); strings.Join(got, ";") != strings.Join(want, ";") {
		t.Errorf("usenet client calls = %q, want %q", got, want)
	}
	if paths, _ := q.snapshot(); len(paths) != 0 {
		t.Errorf("a usenet key reached the torrent client: %v", paths)
	}

	for _, p := range []string{
		"/api/v1/queue/sab-",
		"/api/v1/queue/sab-a%2Csab-b",
		"/api/v1/queue/sab-a%7Csab-b",
		"/api/v1/queue/SAB-SABNZBD_NZO_P86TGX",
		"/api/v1/queue/sabnzbd_nzo_p86tgx",
	} {
		if rec := s.do("DELETE", p, mgr); rec.Code != http.StatusBadRequest {
			t.Errorf("DELETE %s: HTTP %d, want 400", p, rec.Code)
		}
	}
	if n := len(sab.log()); n != len(want) {
		t.Errorf("a refused key reached the usenet client: %q", sab.log()[len(want):])
	}
}

// The API still adds and edits only qBittorrent clients, and an edit can't change a
// client's kind.
func TestDownloadClientKindCheck(t *testing.T) {
	s, dl := newClientServer(t)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	rec := s.doBody("POST", "/api/v1/downloadclients", `{"name":"sab","kind":"sabnzbd","url":"http://sab:8080"}`, mgr)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "kind must be 'qbittorrent'") {
		t.Errorf("create sabnzbd: HTTP %d %s", rec.Code, rec.Body)
	}
	c, err := dl.Create(context.Background(), download.Client{Name: "qb", Kind: download.KindQbittorrent, URL: "http://qb:8080", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/downloadclients/%d", c.ID)
	if rec := s.doBody("PUT", path, `{"name":"qb","kind":"sabnzbd","url":"http://qb:8080"}`, mgr); rec.Code != http.StatusBadRequest {
		t.Errorf("edit to sabnzbd: HTTP %d %s", rec.Code, rec.Body)
	}
	if rec := s.doBody("PUT", path, `{"name":"qb2","kind":"qbittorrent","url":"http://qb:8080"}`, mgr); rec.Code != http.StatusOK {
		t.Errorf("plain edit: HTTP %d %s", rec.Code, rec.Body)
	}
	if err := checkClientKind("qbittorrent"); err != nil {
		t.Error(err)
	}
	for _, k := range []string{"", "sabnzbd", "QBITTORRENT", "nzbget"} {
		if err := checkClientKind(k); err == nil {
			t.Errorf("kind %q accepted", k)
		}
	}
}
