package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/aio"
	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/download"
)

// The all-in-one image's built-in qBittorrent: its card carries the port its own page is
// on and says a login can be looked up; only an admin can look it up, and the client list
// never carries the password itself.
func TestBundledQbitLogin(t *testing.T) {
	t.Setenv("ARRMADA_QBIT_WEBUI_HOST_PORT", "8092")
	profile := t.TempDir()
	if _, err := aio.SeedQbittorrent(aio.SeedOptions{Profile: profile, Downloads: "/storage/downloads", UID: os.Getuid(), GID: os.Getgid()}); err != nil {
		t.Fatal(err)
	}
	login, err := aio.ReadLogin(aio.LoginPath(profile))
	if err != nil {
		t.Fatal(err)
	}
	var dl *download.Service
	s := newRouteServer(t, func(d *Deps) {
		d.Config.QbittorrentURL = aio.LocalURL
		d.Config.AIOQbittorrentURL = aio.LocalURL
		d.Config.QbittorrentLogin = aio.LoginPath(profile)
		dl = download.NewService(d.Store.DB(), d.Log)
		d.Downloads = dl
	})
	dl.SetFlags(s.deps.Settings)
	if err := dl.EnsureBundledLogin(context.Background(), aio.LocalURL, login.Username, login.Password); err != nil {
		t.Fatal(err)
	}
	_, admin := s.user(t, "admin@example.com", auth.RoleAdmin)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)

	// Removed and restored from the page, it comes back signing in with the login.
	c, _, _ := dl.BundledClient(context.Background(), download.KindQbittorrent)
	if rec := s.do("DELETE", "/api/v1/downloadclients/"+strconv.FormatInt(c.ID, 10), mgr); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: HTTP %d", rec.Code)
	}
	if rec := s.do("POST", "/api/v1/downloadclients/restore-bundled", mgr); rec.Code != http.StatusOK {
		t.Fatalf("restore: HTTP %d %s", rec.Code, rec.Body)
	}
	if c, ok, _ := dl.BundledClient(context.Background(), download.KindQbittorrent); !ok || c.Username != login.Username || c.Password != login.Password {
		t.Fatalf("restored without its login: %+v", c)
	}

	rec := s.do("GET", "/api/v1/downloadclients", mgr)
	if strings.Contains(rec.Body.String(), login.Password) {
		t.Fatal("the client list carries the qBittorrent password")
	}
	var l struct {
		Clients []struct {
			Bundled  bool   `json:"bundled"`
			UIPort   string `json:"ui_port"`
			HasLogin bool   `json:"has_login"`
		} `json:"clients"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil || len(l.Clients) != 1 {
		t.Fatalf("list: %v %s", err, rec.Body)
	}
	if c := l.Clients[0]; !c.Bundled || c.UIPort != "8092" || !c.HasLogin {
		t.Errorf("card = %+v", c)
	}

	if rec := s.do("GET", "/api/v1/downloadclients/bundled-login", mgr); rec.Code != http.StatusForbidden {
		t.Errorf("a manager read the login: HTTP %d", rec.Code)
	}
	rec = s.do("GET", "/api/v1/downloadclients/bundled-login", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin: HTTP %d %s", rec.Code, rec.Body)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q", cc)
	}
	var got aio.Login
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got != login {
		t.Errorf("login = %+v (%v), want %+v", got, err, login)
	}

	// The file gone (appdata restored without it, say): a plain "not there", no crash.
	if err := os.Remove(aio.LoginPath(profile)); err != nil {
		t.Fatal(err)
	}
	if rec := s.do("GET", "/api/v1/downloadclients/bundled-login", admin); rec.Code != http.StatusNotFound {
		t.Errorf("missing login file: HTTP %d", rec.Code)
	}
}

// Everywhere but the all-in-one image there's no login to look up, and the Compose
// bundle's card doesn't offer one.
func TestBundledQbitLoginOutsideAIO(t *testing.T) {
	var dl *download.Service
	s := newRouteServer(t, func(d *Deps) {
		d.Config.QbittorrentURL = bundledQbitURL
		dl = download.NewService(d.Store.DB(), d.Log)
		d.Downloads = dl
	})
	dl.SetFlags(s.deps.Settings)
	if err := dl.EnsureBundled(context.Background(), bundledQbitURL); err != nil {
		t.Fatal(err)
	}
	_, admin := s.user(t, "admin@example.com", auth.RoleAdmin)
	if rec := s.do("GET", "/api/v1/downloadclients/bundled-login", admin); rec.Code != http.StatusNotFound {
		t.Errorf("HTTP %d", rec.Code)
	}
	rec := s.do("GET", "/api/v1/downloadclients", admin)
	if strings.Contains(rec.Body.String(), "has_login") || strings.Contains(rec.Body.String(), "ui_port") {
		t.Errorf("the Compose bundle's card offers a login: %s", rec.Body)
	}
}
