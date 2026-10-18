package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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

// The Compose bundle's qBittorrent: no login to look up until Arrmada has given it a
// password; after that its card offers it (to admins only) with the published port, and the
// password is never in the client list.
func TestBundledQbitLoginCompose(t *testing.T) {
	t.Setenv("ARRMADA_QBIT_WEBUI_HOST_PORT", "8081")
	loginPath := filepath.Join(t.TempDir(), "qbittorrent-login.json")
	var dl *download.Service
	s := newRouteServer(t, func(d *Deps) {
		d.Config.QbittorrentURL = bundledQbitURL
		d.Config.QbittorrentLogin = loginPath
		dl = download.NewService(d.Store.DB(), d.Log)
		d.Downloads = dl
	})
	dl.SetFlags(s.deps.Settings)
	ctx := context.Background()
	if err := dl.EnsureBundled(ctx, bundledQbitURL); err != nil {
		t.Fatal(err)
	}
	_, admin := s.user(t, "admin@example.com", auth.RoleAdmin)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)

	// Before the switch: nothing to show.
	if rec := s.do("GET", "/api/v1/downloadclients/bundled-login", admin); rec.Code != http.StatusNotFound {
		t.Errorf("before: HTTP %d", rec.Code)
	}
	rec := s.do("GET", "/api/v1/downloadclients", admin)
	if strings.Contains(rec.Body.String(), "has_login") || strings.Contains(rec.Body.String(), "ui_port") {
		t.Errorf("before: the card offers a login: %s", rec.Body)
	}
	// Restoring it then brings it back without a login (the whitelist still lets it in).
	c, _, _ := dl.BundledClient(ctx, download.KindQbittorrent)
	if rec := s.do("DELETE", "/api/v1/downloadclients/"+strconv.FormatInt(c.ID, 10), mgr); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: HTTP %d", rec.Code)
	}
	if rec := s.do("POST", "/api/v1/downloadclients/restore-bundled", mgr); rec.Code != http.StatusOK {
		t.Fatalf("restore: HTTP %d %s", rec.Code, rec.Body)
	}

	// After: Arrmada saved the login and gave it to the client.
	login := aio.Login{Username: "admin", Password: "ComposeLoginPw-77xy"}
	if err := aio.SaveLogin(loginPath, login); err != nil {
		t.Fatal(err)
	}
	if err := dl.EnsureBundledLogin(ctx, bundledQbitURL, login.Username, login.Password); err != nil {
		t.Fatal(err)
	}
	rec = s.do("GET", "/api/v1/downloadclients", mgr)
	if strings.Contains(rec.Body.String(), login.Password) {
		t.Fatal("the client list carries the qBittorrent password")
	}
	var l struct {
		Clients []struct {
			UIPort   string `json:"ui_port"`
			HasLogin bool   `json:"has_login"`
			BuiltIn  bool   `json:"built_in"`
		} `json:"clients"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil || len(l.Clients) != 1 {
		t.Fatalf("list: %v %s", err, rec.Body)
	}
	if c := l.Clients[0]; c.UIPort != "8081" || !c.HasLogin || c.BuiltIn {
		t.Errorf("card = %+v", c)
	}
	if strings.Contains(rec.Body.String(), "ui_local") {
		t.Errorf("a page published on every interface reads as this server's only: %s", rec.Body)
	}
	// docker-compose.yml publishes it on 127.0.0.1 unless .env says otherwise: the card
	// says so rather than linking a page another computer can't open.
	for bind, local := range map[string]bool{"127.0.0.1": true, "localhost": true, "[::1]": true, "0.0.0.0": false, "192.168.1.10": false} {
		t.Setenv("ARRMADA_QBIT_WEBUI_HOST_BIND", bind)
		body := s.do("GET", "/api/v1/downloadclients", mgr).Body.String()
		if got := strings.Contains(body, `"ui_local":true`); got != local {
			t.Errorf("bound to %s: ui_local %v, want %v", bind, got, local)
		}
	}
	t.Setenv("ARRMADA_QBIT_WEBUI_HOST_BIND", "")
	if rec := s.do("GET", "/api/v1/downloadclients/bundled-login", mgr); rec.Code != http.StatusForbidden {
		t.Errorf("a manager read the login: HTTP %d", rec.Code)
	}
	rec = s.do("GET", "/api/v1/downloadclients/bundled-login", admin)
	var got aio.Login
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got != login {
		t.Fatalf("admin: HTTP %d %s", rec.Code, rec.Body)
	}

	// Removed and restored after the switch: back with the login.
	c, _, _ = dl.BundledClient(ctx, download.KindQbittorrent)
	if rec := s.do("DELETE", "/api/v1/downloadclients/"+strconv.FormatInt(c.ID, 10), mgr); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: HTTP %d", rec.Code)
	}
	if rec := s.do("POST", "/api/v1/downloadclients/restore-bundled", mgr); rec.Code != http.StatusOK {
		t.Fatalf("restore: HTTP %d %s", rec.Code, rec.Body)
	}
	if c, ok, _ := dl.BundledClient(ctx, download.KindQbittorrent); !ok || c.Password != login.Password {
		t.Fatalf("restored without its login: %+v", c)
	}

	// The copy-paste Compose file publishes no port for it: the login, without a link.
	t.Setenv("ARRMADA_QBIT_WEBUI_HOST_PORT", "")
	rec = s.do("GET", "/api/v1/downloadclients", admin)
	if strings.Contains(rec.Body.String(), "ui_port") || !strings.Contains(rec.Body.String(), `"has_login":true`) {
		t.Errorf("no published port: %s", rec.Body)
	}
}

// The login Arrmada made never leaves in a support bundle, even when the card now has
// another password (so the download_clients scrub doesn't cover it).
func TestSupportBundleWithholdsBundledQbitLogin(t *testing.T) {
	loginPath := filepath.Join(t.TempDir(), "qbittorrent-login.json")
	const made = "MadeByArrmadaQbit-55qq"
	if err := aio.SaveLogin(loginPath, aio.Login{Username: "admin", Password: made}); err != nil {
		t.Fatal(err)
	}
	f := newLogFixture(t, func(d *Deps) {
		d.Config.QbittorrentURL = bundledQbitURL
		d.Config.QbittorrentLogin = loginPath
	}, nil)
	f.log.Warn("qbittorrent: a careless line quoting " + made)
	rec := f.do("GET", "/api/v1/system/support-bundle", f.admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d", rec.Code)
	}
	for name, body := range readZip(t, rec.Body.Bytes()) {
		if strings.Contains(body, made) {
			t.Errorf("%s carries the qBittorrent login", name)
		}
	}
}
