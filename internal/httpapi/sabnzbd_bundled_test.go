package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/download"
)

const bundledSABKeyForTest = "00112233445566778899aabbccddeeff"

// fakeBundledSAB answers the few calls the card, the checklist and health make; servers is
// how many news servers it says it has.
func fakeBundledSAB(t *testing.T, servers *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("apikey") != bundledSABKeyForTest && r.PostForm.Get("mode") != "version" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "API Key Incorrect")
			return
		}
		switch r.PostForm.Get("mode") {
		case "version":
			_, _ = io.WriteString(w, `{"version":"5.1.3"}`)
		case "get_cats":
			_, _ = io.WriteString(w, `{"categories":["*","arrmada","arrmada-tv","arrmada-books","arrmada-music"]}`)
		case "fullstatus":
			var sv []string
			for i := int32(0); i < servers.Load(); i++ {
				sv = append(sv, fmt.Sprintf(`{"servername":"news%d","servererror":""}`, i))
			}
			_, _ = io.WriteString(w, `{"status":{"version":"5.1.3","completedir":"/media/downloads/usenet/complete","servers":[`+strings.Join(sv, ",")+`]}}`)
		case "get_config":
			if r.PostForm.Get("section") == "categories" {
				_, _ = io.WriteString(w, `{"config":{"categories":[]}}`)
			} else {
				_, _ = io.WriteString(w, `{"config":{"misc":{"language":"en"}}}`)
			}
		default:
			_, _ = io.WriteString(w, `{"status":true}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeSABIni(t *testing.T, key string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sabnzbd.ini")
	if err := os.WriteFile(p, []byte("__version__ = 19\n[misc]\nlanguage = en\napi_key = "+key+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The bundled SABnzbd's card carries the port its own page is on; deleted, it stays
// deleted and is offered back; restored, it comes back with the key from its config. A
// bundled SABnzbd doesn't hide a removed bundled qBittorrent's restore, and neither offer
// appears without its companion.
func TestBundledSABRemoveAndRestore(t *testing.T) {
	t.Setenv("ARRMADA_SABNZBD_HOST_PORT", "8086")
	var servers atomic.Int32
	sab := fakeBundledSAB(t, &servers)
	ini := writeSABIni(t, bundledSABKeyForTest)
	var dl *download.Service
	s := newRouteServer(t, func(d *Deps) {
		d.Config.QbittorrentURL = bundledQbitURL
		d.Config.SABnzbdURL = sab.URL
		d.Config.SABnzbdConfig = ini
		dl = download.NewService(d.Store.DB(), d.Log)
		d.Downloads = dl
	})
	dl.SetFlags(s.deps.Settings)
	ctx := context.Background()
	if err := dl.EnsureBundled(ctx, bundledQbitURL); err != nil {
		t.Fatal(err)
	}
	if _, err := dl.EnsureBundledSAB(ctx, sab.URL, bundledSABKeyForTest); err != nil {
		t.Fatal(err)
	}
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)

	type listed struct {
		Clients []struct {
			ID      int64  `json:"id"`
			Kind    string `json:"kind"`
			Bundled bool   `json:"bundled"`
			UIPort  string `json:"ui_port"`
		} `json:"clients"`
		CanRestore    bool `json:"can_restore_bundled"`
		CanRestoreSAB bool `json:"can_restore_bundled_sabnzbd"`
	}
	list := func() listed {
		t.Helper()
		var l listed
		rec := s.do("GET", "/api/v1/downloadclients", mgr)
		if strings.Contains(rec.Body.String(), bundledSABKeyForTest) {
			t.Fatal("the list carries the bundled SABnzbd's key")
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
			t.Fatalf("list: %v %s", err, rec.Body)
		}
		return l
	}
	byKind := func(l listed, kind string) (int64, string, bool) {
		for _, c := range l.Clients {
			if c.Kind == kind && c.Bundled {
				return c.ID, c.UIPort, true
			}
		}
		return 0, "", false
	}

	l := list()
	sabID, port, ok := byKind(l, "sabnzbd")
	qbID, qbPort, _ := byKind(l, "qbittorrent")
	if !ok || port != "8086" || qbPort != "" || l.CanRestore || l.CanRestoreSAB {
		t.Fatalf("list = %+v", l)
	}

	// The bundled qBittorrent removed: offered back although a bundled SABnzbd is there.
	if rec := s.do("DELETE", fmt.Sprintf("/api/v1/downloadclients/%d", qbID), mgr); rec.Code != http.StatusNoContent {
		t.Fatalf("delete qBittorrent: HTTP %d", rec.Code)
	}
	if l = list(); !l.CanRestore || l.CanRestoreSAB {
		t.Errorf("after removing qBittorrent: %+v", l)
	}

	if rec := s.do("DELETE", fmt.Sprintf("/api/v1/downloadclients/%d", sabID), mgr); rec.Code != http.StatusNoContent {
		t.Fatalf("delete SABnzbd: HTTP %d", rec.Code)
	}
	if _, err := dl.EnsureBundledSAB(ctx, sab.URL, bundledSABKeyForTest); err != nil { // a restart
		t.Fatal(err)
	}
	if l = list(); len(l.Clients) != 0 || !l.CanRestoreSAB {
		t.Fatalf("after removing SABnzbd and a restart: %+v", l)
	}

	if rec := s.do("POST", "/api/v1/downloadclients/restore-bundled?kind=sabnzbd", mgr); rec.Code != http.StatusOK {
		t.Fatalf("restore SABnzbd: HTTP %d %s", rec.Code, rec.Body)
	}
	l = list()
	if _, _, ok := byKind(l, "sabnzbd"); !ok || l.CanRestoreSAB || !l.CanRestore {
		t.Errorf("after restoring SABnzbd: %+v", l)
	}
	if c, ok, _ := dl.BundledClient(ctx, download.KindSABnzbd); !ok || c.Password != bundledSABKeyForTest {
		t.Error("the restored SABnzbd lacks its key")
	}

	// No config to read the key from (the profile is off): nothing to restore.
	sabID, _, _ = byKind(l, "sabnzbd")
	if rec := s.do("DELETE", fmt.Sprintf("/api/v1/downloadclients/%d", sabID), mgr); rec.Code != http.StatusNoContent {
		t.Fatalf("delete SABnzbd again: HTTP %d", rec.Code)
	}
	if err := os.Remove(ini); err != nil {
		t.Fatal(err)
	}
	if l = list(); l.CanRestoreSAB {
		t.Error("restore offered with no config")
	}
	if rec := s.do("POST", "/api/v1/downloadclients/restore-bundled?kind=sabnzbd", mgr); rec.Code != http.StatusBadRequest {
		t.Errorf("restore without a bundled SABnzbd: HTTP %d, want 400", rec.Code)
	}
}

// Getting started has an "Add your Usenet provider" step only while a switched-on bundled
// SABnzbd exists, ticked once SABnzbd has a news server.
func TestSetupChecklistUsenetProvider(t *testing.T) {
	a := checklistAPI(t)
	ctx := context.Background()
	var servers atomic.Int32
	sab := fakeBundledSAB(t, &servers)
	has := func() (bool, bool) {
		for _, it := range getChecklist(t, a).Items {
			if it.Key == "usenet_provider" {
				return true, it.Done
			}
		}
		return false, false
	}
	if shown, _ := has(); shown {
		t.Fatal("shown without a bundled SABnzbd")
	}
	// Someone's own SABnzbd isn't the bundled one: its provider is already their business.
	own, err := a.deps.Downloads.Create(ctx, download.Client{Name: "Mine", Kind: download.KindSABnzbd, URL: sab.URL, Password: bundledSABKeyForTest, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if shown, _ := has(); shown {
		t.Fatal("shown for someone's own SABnzbd")
	}
	if err := a.deps.Downloads.Delete(ctx, own.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.deps.Downloads.EnsureBundledSAB(ctx, sab.URL, bundledSABKeyForTest); err != nil {
		t.Fatal(err)
	}
	c := getChecklist(t, a)
	if shown, done := has(); !shown || done {
		t.Fatalf("bundled SABnzbd with no server: shown=%v done=%v", shown, done)
	}
	for i, it := range c.Items {
		if it.Key == "usenet_provider" {
			prev := c.Items[i-1].Key
			if prev != "download_client" || !strings.HasPrefix(it.Link, "/settings/connections#download_client-") {
				t.Errorf("step = %+v after %s", it, prev)
			}
		}
	}
	servers.Store(1)
	if shown, done := has(); !shown || !done {
		t.Errorf("bundled SABnzbd with a server: shown=%v done=%v", shown, done)
	}
}

// The bundled SABnzbd's page login, read from its config: on its card as has_login, and to
// an admin (only) from bundled-login?kind=sabnzbd. Never in the client list.
func TestBundledSABLogin(t *testing.T) {
	var servers atomic.Int32
	sab := fakeBundledSAB(t, &servers)
	ini := writeSABIni(t, bundledSABKeyForTest)
	var dl *download.Service
	s := newRouteServer(t, func(d *Deps) {
		d.Config.SABnzbdURL = sab.URL
		d.Config.SABnzbdConfig = ini
		dl = download.NewService(d.Store.DB(), d.Log)
		d.Downloads = dl
	})
	dl.SetFlags(s.deps.Settings)
	if _, err := dl.EnsureBundledSAB(context.Background(), sab.URL, bundledSABKeyForTest); err != nil {
		t.Fatal(err)
	}
	_, admin := s.user(t, "admin@example.com", auth.RoleAdmin)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)

	// No login in its config yet.
	if rec := s.do("GET", "/api/v1/downloadclients/bundled-login?kind=sabnzbd", admin); rec.Code != http.StatusNotFound {
		t.Errorf("before: HTTP %d", rec.Code)
	}
	if body := s.do("GET", "/api/v1/downloadclients", mgr).Body.String(); strings.Contains(body, "has_login") {
		t.Errorf("before: the card offers a login: %s", body)
	}

	const pw = "SabPagePassword-0123456789abcdef"
	if err := os.WriteFile(ini, []byte("__version__ = 19\n[misc]\napi_key = "+bundledSABKeyForTest+"\nusername = arrmada\npassword = "+pw+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body := s.do("GET", "/api/v1/downloadclients", mgr).Body.String()
	if strings.Contains(body, pw) || !strings.Contains(body, `"has_login":true`) {
		t.Errorf("after: %s", body)
	}
	if rec := s.do("GET", "/api/v1/downloadclients/bundled-login?kind=sabnzbd", mgr); rec.Code != http.StatusForbidden {
		t.Errorf("a manager read the login: HTTP %d", rec.Code)
	}
	rec := s.do("GET", "/api/v1/downloadclients/bundled-login?kind=sabnzbd", admin)
	var got struct{ Username, Password string }
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Username != "arrmada" || got.Password != pw {
		t.Fatalf("admin: HTTP %d %s", rec.Code, rec.Body)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control %q", cc)
	}
}
