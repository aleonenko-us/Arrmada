package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tristenlammi/arrmada/internal/apikeys"
	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/plex"
)

// wizardServer is a fresh install (no account) with the services the wizard's calls touch,
// and library folders (inside a temp dir) the setup session may point at.
type wizardEnv struct {
	*routeServer
	root string
}

func wizardServer(t *testing.T, tweak func(*Deps)) *wizardEnv {
	t.Helper()
	root := t.TempDir()
	s := newRouteServer(t, func(d *Deps) {
		d.APIKeys = apikeys.NewStore(d.Settings)
		d.Config.MoviesDir = filepath.Join(root, "movies")
		d.Config.TVDir = filepath.Join(root, "tv")
		d.Config.DownloadsDir = filepath.Join(root, "downloads")
		d.Config.ExternalHeader = "Cf-Connecting-Ip"
		if tweak != nil {
			tweak(d)
		}
	})
	for _, d := range []string{"movies", "tv", "downloads"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &wizardEnv{routeServer: s, root: root}
}

// startSetup opens a setup session from the LAN and returns its cookie.
func (s *wizardEnv) startSetup(t *testing.T) *http.Cookie {
	t.Helper()
	rec := s.doFrom("POST", "/api/v1/setup/session", "", lan)
	if rec.Code != http.StatusCreated {
		t.Fatalf("start setup session: HTTP %d %s", rec.Code, rec.Body)
	}
	c := cookieNamed(rec, setupCookieName)
	if c == nil {
		t.Fatal("no setup cookie")
	}
	return c
}

// The cookie is the only place the token goes, and it is shaped to stay in the browser that
// asked: HttpOnly, SameSite=Strict, scoped to the API, about two hours, and the body never
// carries it.
func TestSetupSessionCookieShape(t *testing.T) {
	s := wizardServer(t, nil)
	rec := s.doFrom("POST", "/api/v1/setup/session", "", lan)
	if rec.Code != http.StatusCreated {
		t.Fatalf("HTTP %d %s", rec.Code, rec.Body)
	}
	c := cookieNamed(rec, setupCookieName)
	if c == nil {
		t.Fatal("no cookie")
	}
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/api/v1" {
		t.Errorf("cookie shape: %+v", c)
	}
	if c.MaxAge != int(auth.SetupSessionTTL.Seconds()) {
		t.Errorf("MaxAge = %d, want %d", c.MaxAge, int(auth.SetupSessionTTL.Seconds()))
	}
	if strings.Contains(rec.Body.String(), c.Value) {
		t.Errorf("the token is in the response body: %s", rec.Body)
	}
	// Over HTTPS (a proxy says so) the cookie is Secure.
	r := httptest.NewRequest("POST", "http://arrmada.local/api/v1/setup/session", nil)
	r.RemoteAddr = lan + ":5000"
	r.Header.Set("X-Forwarded-Proto", "https")
	rec2 := httptest.NewRecorder()
	s.h.ServeHTTP(rec2, r)
	if c2 := cookieNamed(rec2, setupCookieName); c2 == nil || !c2.Secure {
		t.Errorf("over https: %+v", c2)
	}
	// Its issuing is cross-site-proof like every state-changing route.
	r = httptest.NewRequest("POST", "http://arrmada.local/api/v1/setup/session", nil)
	r.RemoteAddr = lan + ":5000"
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	rec3 := httptest.NewRecorder()
	s.h.ServeHTTP(rec3, r)
	if rec3.Code != http.StatusForbidden || cookieNamed(rec3, setupCookieName) != nil {
		t.Errorf("cross-site issue: HTTP %d, cookie %v", rec3.Code, cookieNamed(rec3, setupCookieName))
	}
}

// An allowlisted wizard call works on the setup session, and what it answers has no secret
// hint in it.
func TestSetupSessionAllowedEndpoint(t *testing.T) {
	s := wizardServer(t, nil)
	ctx := context.Background()
	if err := s.deps.APIKeys.Set(ctx, "tmdb", "supersecretvalue1234"); err != nil {
		t.Fatal(err)
	}
	if err := s.deps.APIKeys.Set(ctx, "opensubtitles_username", "alice-private-name"); err != nil {
		t.Fatal(err)
	}
	// Without the session: refused.
	if rec := s.doFrom("GET", "/api/v1/setup", "", lan); rec.Code != http.StatusUnauthorized {
		t.Errorf("no session: HTTP %d, want 401", rec.Code)
	}
	c := s.startSetup(t)
	rec := s.doFrom("GET", "/api/v1/setup", "", lan, c)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /setup on the session: HTTP %d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, secret := range []string{"supersecretvalue1234", "1234", "alice-private-name"} {
		if strings.Contains(body, secret) {
			t.Errorf("the setup state shows %q", secret)
		}
	}
	var st struct {
		TMDB bool `json:"tmdb_configured"`
		Keys []struct {
			ID         string `json:"id"`
			Configured bool   `json:"configured"`
		} `json:"keys"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if !st.TMDB {
		t.Error("tmdb_configured should still be true")
	}
	for _, k := range st.Keys {
		if !wizardKeyIDs[k.ID] {
			t.Errorf("key %q is listed to a setup session", k.ID)
		}
	}
}

// Everything that isn't on the wizard's list answers a setup session like it would anyone
// not signed in, and the setup token is no use as a login session or as an API key.
func TestSetupSessionOtherEndpointsRefused(t *testing.T) {
	s := wizardServer(t, nil)
	c := s.startSetup(t)
	for _, call := range []string{
		"GET /api/v1/users", "POST /api/v1/users", "GET /api/v1/apikeys", "GET /api/v1/users/api-keys",
		"GET /api/v1/settings", "GET /api/v1/system/library", "POST /api/v1/system/restart",
		"GET /api/v1/setup/checklist", "POST /api/v1/setup/complete", "POST /api/v1/setup/checklist/dismiss",
		"GET /api/v1/auth/me", "GET /api/v1/me/api-keys", "POST /api/v1/me/api-keys", "GET /api/v1/me/account",
		"GET /api/v1/system/backups", "POST /api/v1/system/backups", "GET /api/v1/logs", "GET /api/v1/system/status",
		"DELETE /api/v1/jellyfin", "DELETE /api/v1/emby", "DELETE /api/v1/apikeys/tmdb", "DELETE /api/v1/indexers/1",
		"GET /api/v1/indexers", "PUT /api/v1/indexers/1", "GET /api/v1/downloadclients", "GET /api/v1/movies",
		"GET /api/v1/discover/trending", "GET /api/v1/requests", "PUT /api/v1/insights/plex/monitoring",
		"POST /api/v1/system/library/folders", "GET /api/v1/settings/smtp", "GET /api/v1/audioserver",
	} {
		method, path, _ := strings.Cut(call, " ")
		body := ""
		if method != "GET" && method != "DELETE" {
			body = "{}"
		}
		rec := s.doFrom(method, path, body, lan, c)
		if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s on a setup session: HTTP %d %s, want it refused", call, rec.Code, rec.Body)
		}
		if rec.Code == http.StatusOK || rec.Code == http.StatusCreated {
			t.Errorf("%s was let through", call)
		}
	}
	// The setup token offered as a login session, or as an API key, is nothing.
	asLogin := &http.Cookie{Name: sessionCookieName, Value: c.Value}
	if rec := s.doFrom("GET", "/api/v1/auth/me", "", lan, asLogin); rec.Code != http.StatusUnauthorized {
		t.Errorf("setup token as a login session: HTTP %d, want 401", rec.Code)
	}
	if rec := s.doFrom("GET", "/api/v1/setup", "", lan, asLogin); rec.Code != http.StatusUnauthorized {
		t.Errorf("setup token in the login cookie reached a wizard route: HTTP %d", rec.Code)
	}
	r := httptest.NewRequest("GET", "http://arrmada.local/api/v1/setup", nil)
	r.RemoteAddr = lan + ":5000"
	r.Header.Set("X-Api-Key", "arrk_"+c.Value)
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, r)
	if rec.Code == http.StatusOK {
		t.Errorf("setup token as an API key: HTTP %d", rec.Code)
	}
}

// The whole route table, called with a live setup cookie: exactly the routes marked setup
// in routes.golden reach their handler, and every other route that needs an account refuses
// (401), none by a lucky 404. A route added later without viaSetup() is therefore closed to
// a setup session by default, and adding it to the list is a visible, tested choice.
func TestSetupSessionReachesOnlyMarkedRoutes(t *testing.T) {
	s := wizardServer(t, nil)
	c := s.startSetup(t)
	a := &api{deps: s.deps}
	rt := newRouter(a)
	rt.sentinel = true
	a.registerRoutes(rt)
	marked := 0
	for _, spec := range rt.specs {
		if spec.Scope == scopePublic {
			continue
		}
		method := spec.Method
		if method == "" {
			continue
		}
		r := httptest.NewRequest(method, samplePath(spec.Pattern), nil)
		r.RemoteAddr = lan + ":5000"
		r.AddCookie(c)
		rec := httptest.NewRecorder()
		rt.ServeHTTP(rec, r)
		want := http.StatusUnauthorized
		if spec.Setup {
			want = sentinelStatus
			marked++
		}
		if rec.Code != want {
			t.Errorf("%s with a setup cookie: HTTP %d, want %d", spec, rec.Code, want)
		}
	}
	if marked < 15 {
		t.Errorf("only %d routes are marked for the wizard; the route walk found too few", marked)
	}
	// Without a cookie, or once an admin exists, the marked routes refuse too.
	for _, admin := range []bool{false, true} {
		if admin {
			if _, err := s.auth.CreateFirstAdmin(context.Background(), "boss@example.com", "long-enough-1"); err != nil {
				t.Fatal(err)
			}
		}
		for _, spec := range rt.specs {
			if !spec.Setup {
				continue
			}
			r := httptest.NewRequest(spec.Method, samplePath(spec.Pattern), nil)
			r.RemoteAddr = lan + ":5000"
			if admin {
				r.AddCookie(c)
			}
			rec := httptest.NewRecorder()
			rt.ServeHTTP(rec, r)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s (admin made: %v): HTTP %d, want 401", spec, admin, rec.Code)
			}
		}
	}
}

// Even the listed routes that write narrow themselves for a setup session: only the
// wizard's settings, its own key ids, folders under the media roots, and nothing secret
// comes back.
func TestSetupSessionNarrowedRoutes(t *testing.T) {
	s := wizardServer(t, nil)
	ctx := context.Background()
	c := s.startSetup(t)

	// Settings: the family switches yes, anything else (here, an admin-only module toggle
	// and a path setting) a 403 that saves nothing, even with an allowed key beside it.
	rec := s.doFrom("PUT", "/api/v1/settings", `{"plex_login_enabled":true,"plex_login_auto_approve_types":"movie"}`, lan, c)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"status":"ok"}` {
		t.Errorf("wizard settings: HTTP %d %s, want 200 {status:ok} and no settings view", rec.Code, rec.Body)
	}
	if !s.deps.Settings.GetBool(ctx, "plex_login_enabled", false) {
		t.Error("plex_login_enabled wasn't saved")
	}
	for _, bad := range []string{
		`{"books_enabled":false}`, `{"plex_login_enabled":true,"disk_guard_measure_path":"/etc"}`,
		`{"plex_signin_staff":true}`, `{"public_url":"http://evil.example"}`, `not json`,
	} {
		rec := s.doFrom("PUT", "/api/v1/settings", bad, lan, c)
		if rec.Code != http.StatusForbidden && rec.Code != http.StatusBadRequest {
			t.Errorf("settings %s: HTTP %d, want refused", bad, rec.Code)
		}
	}
	if s.deps.Settings.GetBool(ctx, keyPlexSignInStaff, false) {
		t.Error("a setup session switched on staff Plex sign-in")
	}

	// Keys: the wizard's ids only, and a save answers without hints.
	rec = s.doFrom("PUT", "/api/v1/apikeys/tmdb", `{"value":"abcdef0123456789"}`, lan, c)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "6789") || strings.Contains(rec.Body.String(), "hint") {
		t.Errorf("save a key: HTTP %d %s", rec.Code, rec.Body)
	}
	if got := s.deps.APIKeys.Value(ctx, "tmdb"); got != "abcdef0123456789" {
		t.Errorf("the key wasn't saved (%q)", got)
	}
	for _, id := range []string{"acoustid", "flaresolverr", "emby", "../tmdb", "nope"} {
		if rec := s.doFrom("PUT", "/api/v1/apikeys/"+id, `{"value":"x"}`, lan, c); rec.Code == http.StatusOK {
			t.Errorf("key %q was saved from a setup session", id)
		}
		if rec := s.doFrom("POST", "/api/v1/apikeys/"+id+"/test", `{"value":"x"}`, lan, c); rec.Code == http.StatusOK {
			t.Errorf("key %q was tested from a setup session", id)
		}
	}

	// Folders: inside the configured roots only, in browse, check and save.
	inside := filepath.Join(s.root, "movies")
	if rec := s.doFrom("GET", "/api/v1/system/browse?path="+s.root, "", lan, c); rec.Code != http.StatusOK {
		t.Errorf("browse inside the roots: HTTP %d %s", rec.Code, rec.Body)
	}
	for _, p := range []string{"/", "/etc", s.root + "/../..", inside + "/../../..", filepath.Dir(s.root)} {
		if rec := s.doFrom("GET", "/api/v1/system/browse?path="+p, "", lan, c); rec.Code != http.StatusForbidden {
			t.Errorf("browse %q: HTTP %d, want 403", p, rec.Code)
		}
		if rec := s.doFrom("GET", "/api/v1/system/library/check?kind=movies&path="+p, "", lan, c); rec.Code != http.StatusForbidden {
			t.Errorf("check %q: HTTP %d, want 403", p, rec.Code)
		}
		if rec := s.doFrom("PUT", "/api/v1/system/library", fmt.Sprintf(`{"movies":%q}`, p), lan, c); rec.Code != http.StatusForbidden {
			t.Errorf("save %q: HTTP %d, want 403", p, rec.Code)
		}
	}
	if rec := s.doFrom("GET", "/api/v1/system/browse?path="+s.root+"&x=1&path=/etc", "", lan, c); rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), `"path":"/etc"`) {
		t.Error("a second path parameter slipped past the root check")
	}
	if rec := s.doFrom("PUT", "/api/v1/system/library", fmt.Sprintf(`{"movies":%q,"tv":%q,"downloads":%q}`, inside, filepath.Join(s.root, "tv"), filepath.Join(s.root, "downloads")), lan, c); rec.Code != http.StatusOK {
		t.Errorf("save inside the roots: HTTP %d %s", rec.Code, rec.Body)
	}
	// With no setup session, the wizard's route is just an admin route.
	if rec := s.doFrom("GET", "/api/v1/system/browse?path=/etc", "", lan); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous browse: HTTP %d, want 401", rec.Code)
	}
}

// The setup session is dead the instant an admin exists, however the admin came to be, and
// the cookie of someone who started setup earlier opens nothing afterwards.
func TestSetupSessionDiesWithAdmin(t *testing.T) {
	for _, how := range []string{"account step", "legacy /auth/setup", "directly in the database"} {
		t.Run(how, func(t *testing.T) {
			s := wizardServer(t, nil)
			browserA := s.startSetup(t)
			browserB := s.startSetup(t)
			for _, c := range []*http.Cookie{browserA, browserB} {
				if rec := s.doFrom("GET", "/api/v1/setup", "", lan, c); rec.Code != http.StatusOK {
					t.Fatalf("before: HTTP %d", rec.Code)
				}
			}
			switch how {
			case "account step":
				rec := s.doFrom("POST", "/api/v1/setup/account", `{"username":"owner@example.com","password":"long-enough-1"}`, lan, browserA)
				if rec.Code != http.StatusCreated {
					t.Fatalf("account: HTTP %d %s", rec.Code, rec.Body)
				}
			case "legacy /auth/setup":
				rec := s.doFrom("POST", "/api/v1/auth/setup", `{"username":"owner@example.com","password":"long-enough-1"}`, lan)
				if rec.Code != http.StatusCreated {
					t.Fatalf("legacy setup: HTTP %d %s", rec.Code, rec.Body)
				}
			case "directly in the database":
				if _, err := s.auth.CreateUser(context.Background(), "owner@example.com", "long-enough-1", auth.RoleAdmin, true); err != nil {
					t.Fatal(err)
				}
			}
			for name, c := range map[string]*http.Cookie{"A": browserA, "B": browserB} {
				for _, call := range []string{"GET /api/v1/setup", "PUT /api/v1/apikeys/tmdb", "POST /api/v1/indexers/test", "GET /api/v1/system/browse"} {
					method, path, _ := strings.Cut(call, " ")
					body := ""
					if method != "GET" {
						body = `{"value":"x"}`
					}
					if rec := s.doFrom(method, path, body, lan, c); rec.Code != http.StatusUnauthorized {
						t.Errorf("browser %s, %s after the admin exists: HTTP %d, want 401", name, call, rec.Code)
					}
				}
				// The account step is closed too, and no new session is offered.
				if rec := s.doFrom("POST", "/api/v1/setup/account", `{"username":"intruder@example.com","password":"long-enough-1"}`, lan, c); rec.Code != http.StatusConflict {
					t.Errorf("browser %s, second account: HTTP %d, want 409", name, rec.Code)
				}
			}
			rec := s.doFrom("POST", "/api/v1/setup/session", "", lan)
			if rec.Code != http.StatusConflict || cookieNamed(rec, setupCookieName) != nil {
				t.Errorf("a setup session after the admin exists: HTTP %d, cookie %v", rec.Code, cookieNamed(rec, setupCookieName))
			}
			if n, _ := s.auth.CountAdmins(context.Background()); n != 1 {
				t.Errorf("%d admins, want 1", n)
			}
		})
	}
}

// An expired setup session is refused even though nothing else changed.
func TestSetupSessionExpires(t *testing.T) {
	s := wizardServer(t, nil)
	c := s.startSetup(t)
	if rec := s.doFrom("GET", "/api/v1/setup", "", lan, c); rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d", rec.Code)
	}
	if _, err := s.st.DB().Exec(`UPDATE setup_sessions SET expires_at = ?`, "2000-01-01 00:00:00"); err != nil {
		t.Fatal(err)
	}
	if rec := s.doFrom("GET", "/api/v1/setup", "", lan, c); rec.Code != http.StatusUnauthorized {
		t.Errorf("expired: HTTP %d, want 401", rec.Code)
	}
	if rec := s.doFrom("POST", "/api/v1/setup/account", `{"username":"owner@example.com","password":"long-enough-1"}`, lan, c); rec.Code != http.StatusUnauthorized {
		t.Errorf("account step on an expired session: HTTP %d, want 401", rec.Code)
	}
	if n, _ := s.auth.CountAdmins(context.Background()); n != 0 {
		t.Error("an admin was made on an expired setup session")
	}
}

// First-run setup stays inside the LAN rule: no session issued to a request from outside
// (a public address, or a tunnel's header), none used from outside (a cookie taken from the
// LAN and replayed), and a forwarded-for header can't make a public peer look local.
// ARRMADA_ALLOW_REMOTE_SETUP lifts it, for issue and use together.
func TestSetupSessionLANOnly(t *testing.T) {
	s := wizardServer(t, nil)
	tunnel := map[string]string{"Cf-Connecting-Ip": "203.0.113.9"}
	do := func(method, path, body, remote string, hdr map[string]string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://arrmada.local"+path, strings.NewReader(body))
		r.RemoteAddr = remote
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		if c != nil {
			r.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		s.h.ServeHTTP(rec, r)
		return rec
	}
	outside := []struct {
		name   string
		remote string
		hdr    map[string]string
	}{
		{"public address", "203.0.113.50:5000", nil},
		{"through a tunnel header", "172.18.0.4:5000", tunnel},
		{"public peer claiming a private client", "203.0.113.50:5000", map[string]string{"X-Forwarded-For": "192.168.1.5"}},
		{"local proxy forwarding a public client", "172.18.0.4:5000", map[string]string{"X-Forwarded-For": "203.0.113.77"}},
		{"private forged in front of a public one", "172.18.0.4:5000", map[string]string{"X-Forwarded-For": "192.168.1.5, 203.0.113.77"}},
	}
	for _, o := range outside {
		rec := do("POST", "/api/v1/setup/session", "", o.remote, o.hdr, nil)
		if rec.Code != http.StatusForbidden || cookieNamed(rec, setupCookieName) != nil {
			t.Errorf("%s: session issue HTTP %d (cookie %v), want 403 and none", o.name, rec.Code, cookieNamed(rec, setupCookieName))
		}
	}
	c := s.startSetup(t) // from the LAN
	for _, o := range outside {
		if rec := do("GET", "/api/v1/setup", "", o.remote, o.hdr, c); rec.Code == http.StatusOK {
			t.Errorf("%s: a LAN setup cookie worked from outside", o.name)
		}
		if rec := do("POST", "/api/v1/setup/account", `{"username":"owner@example.com","password":"long-enough-1"}`, o.remote, o.hdr, c); rec.Code == http.StatusCreated {
			t.Errorf("%s: the account was made from outside", o.name)
		}
	}
	if n, _ := s.auth.CountAdmins(context.Background()); n != 0 {
		t.Fatal("an admin was made from outside")
	}
	if rec := do("GET", "/api/v1/setup", "", lan+":5000", nil, c); rec.Code != http.StatusOK {
		t.Errorf("the cookie stopped working on the LAN: HTTP %d", rec.Code)
	}

	// The escape hatch works for both ends, and still needs the cookie.
	open := wizardServer(t, func(d *Deps) { d.Config.AllowRemoteSetup = true })
	r := httptest.NewRequest("POST", "http://arrmada.local/api/v1/setup/session", nil)
	r.RemoteAddr = "172.18.0.4:5000"
	r.Header.Set("Cf-Connecting-Ip", "203.0.113.9")
	rec := httptest.NewRecorder()
	open.h.ServeHTTP(rec, r)
	rc := cookieNamed(rec, setupCookieName)
	if rec.Code != http.StatusCreated || rc == nil {
		t.Fatalf("remote setup allowed: issue HTTP %d", rec.Code)
	}
	get := func(c *http.Cookie) int {
		r := httptest.NewRequest("GET", "http://arrmada.local/api/v1/setup", nil)
		r.RemoteAddr = "172.18.0.4:5000"
		r.Header.Set("Cf-Connecting-Ip", "203.0.113.9")
		if c != nil {
			r.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		open.h.ServeHTTP(rec, r)
		return rec.Code
	}
	if code := get(rc); code != http.StatusOK {
		t.Errorf("remote setup allowed, with the cookie: HTTP %d, want 200", code)
	}
	if code := get(nil); code != http.StatusUnauthorized {
		t.Errorf("remote setup allowed, no cookie: HTTP %d, want 401", code)
	}
}

// A page on another origin can't drive the wizard with the setup cookie: the same-origin
// rules every state-changing route has apply, and SameSite=Strict backs them.
func TestSetupSessionCSRF(t *testing.T) {
	s := wizardServer(t, nil)
	c := s.startSetup(t)
	send := func(hdr map[string]string, ctype string) int {
		r := httptest.NewRequest("PUT", "http://arrmada.local/api/v1/apikeys/tmdb", strings.NewReader(`{"value":"abc123"}`))
		r.RemoteAddr = lan + ":5000"
		if ctype != "" {
			r.Header.Set("Content-Type", ctype)
		}
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		r.AddCookie(c)
		rec := httptest.NewRecorder()
		s.h.ServeHTTP(rec, r)
		return rec.Code
	}
	if code := send(map[string]string{"Sec-Fetch-Site": "cross-site"}, "application/json"); code != http.StatusForbidden {
		t.Errorf("cross-site: HTTP %d, want 403", code)
	}
	if code := send(map[string]string{"Sec-Fetch-Site": "same-site"}, "application/json"); code != http.StatusForbidden {
		t.Errorf("same-site (another port): HTTP %d, want 403", code)
	}
	if code := send(map[string]string{"Origin": "http://evil.example"}, "application/json"); code != http.StatusForbidden {
		t.Errorf("foreign Origin: HTTP %d, want 403", code)
	}
	if code := send(nil, "text/plain"); code != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain form post: HTTP %d, want 415", code)
	}
	if code := send(map[string]string{"Sec-Fetch-Site": "same-origin"}, "application/json"); code != http.StatusOK {
		t.Errorf("same-origin: HTTP %d, want 200", code)
	}
	// The account step too.
	r := httptest.NewRequest("POST", "http://arrmada.local/api/v1/setup/account", strings.NewReader(`{"username":"owner@example.com","password":"long-enough-1"}`))
	r.RemoteAddr = lan + ":5000"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.AddCookie(c)
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site account step: HTTP %d, want 403", rec.Code)
	}
	if n, _ := s.auth.CountAdmins(context.Background()); n != 0 {
		t.Error("an admin was made cross-site")
	}
}

// Two browsers reaching the account step at once: one admin, one 409.
func TestSetupAccountRace(t *testing.T) {
	s := wizardServer(t, nil)
	a, b := s.startSetup(t), s.startSetup(t)
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i, c := range []*http.Cookie{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := fmt.Sprintf(`{"username":"owner%d@example.com","password":"long-enough-1"}`, i)
			codes[i] = s.doFrom("POST", "/api/v1/setup/account", body, lan, c).Code
		}()
	}
	wg.Wait()
	created, conflict := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflict++
		}
	}
	if created != 1 || conflict != 1 {
		t.Errorf("codes %v: want one 201 and one 409", codes)
	}
	if n, _ := s.auth.CountAdmins(context.Background()); n != 1 {
		t.Errorf("%d admins, want 1", n)
	}
}

// Bad input at the account step makes nothing and leaves the setup session usable.
func TestSetupAccountValidation(t *testing.T) {
	s := wizardServer(t, nil)
	c := s.startSetup(t)
	for body, want := range map[string]int{
		`{"username":"owner@example.com","password":"short"}`:        http.StatusBadRequest,
		`{"username":"","password":"long-enough-1"}`:                 http.StatusBadRequest,
		`{"username":"x","password":"long-enough-1","role":"admin"}`: http.StatusBadRequest,
		`nope`: http.StatusBadRequest,
	} {
		if rec := s.doFrom("POST", "/api/v1/setup/account", body, lan, c); rec.Code != want {
			t.Errorf("%s: HTTP %d, want %d", body, rec.Code, want)
		}
	}
	if rec := s.doFrom("POST", "/api/v1/setup/account", `{"username":"owner@example.com","password":"long-enough-1"}`, lan); rec.Code != http.StatusUnauthorized {
		t.Errorf("no setup cookie: HTTP %d, want 401", rec.Code)
	}
	if n, _ := s.auth.CountAdmins(context.Background()); n != 0 {
		t.Fatal("an admin was made from bad input")
	}
	if rec := s.doFrom("GET", "/api/v1/setup", "", lan, c); rec.Code != http.StatusOK {
		t.Errorf("session after a rejected account: HTTP %d, want 200", rec.Code)
	}
}

// The whole first run: a setup session saves a key and the library folders, the account
// step makes the admin and signs them in, and from then on the admin's session works while
// the setup cookie opens nothing.
func TestSetupHappyPath(t *testing.T) {
	s := wizardServer(t, nil)
	ctx := context.Background()
	c := s.startSetup(t)

	if rec := s.doFrom("PUT", "/api/v1/apikeys/tmdb", `{"value":"tmdbkey0123456789"}`, lan, c); rec.Code != http.StatusOK {
		t.Fatalf("save key: HTTP %d %s", rec.Code, rec.Body)
	}
	folders := fmt.Sprintf(`{"movies":%q,"tv":%q,"downloads":%q}`, filepath.Join(s.root, "movies"), filepath.Join(s.root, "tv"), filepath.Join(s.root, "downloads"))
	if rec := s.doFrom("PUT", "/api/v1/system/library", folders, lan, c); rec.Code != http.StatusOK {
		t.Fatalf("save folders: HTTP %d %s", rec.Code, rec.Body)
	}
	// Nobody is signed in yet.
	if rec := s.doFrom("GET", "/api/v1/auth/me", "", lan, c); rec.Code != http.StatusUnauthorized {
		t.Errorf("/auth/me on the setup session: HTTP %d, want 401", rec.Code)
	}
	if rec := s.doFrom("GET", "/api/v1/status", "", lan, c); !strings.Contains(rec.Body.String(), `"needs_setup":true`) {
		t.Errorf("status before the account: %s", rec.Body)
	}

	rec := s.doFrom("POST", "/api/v1/setup/account", `{"username":"Owner@Example.com","password":"long-enough-1"}`, lan, c)
	if rec.Code != http.StatusCreated {
		t.Fatalf("account: HTTP %d %s", rec.Code, rec.Body)
	}
	session := cookieNamed(rec, sessionCookieName)
	if session == nil {
		t.Fatal("the account step didn't sign the admin in")
	}
	if !session.HttpOnly || session.SameSite != http.SameSiteLaxMode {
		t.Errorf("session cookie shape: %+v", session)
	}
	var out struct {
		User auth.User `json:"user"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.User.Role != auth.RoleAdmin || out.User.Username != "owner@example.com" {
		t.Errorf("created %+v, want the admin owner@example.com", out.User)
	}
	// The setup cookie was cleared in the same answer.
	var cleared bool
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == setupCookieName && ck.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("the setup cookie wasn't cleared")
	}
	if !s.deps.Settings.GetBool(ctx, keySetupComplete, false) {
		t.Error("setup_complete wasn't recorded")
	}

	// The admin's session works for admin routes, and what the wizard saved is there.
	if rec := s.doFrom("GET", "/api/v1/auth/me", "", lan, session); rec.Code != http.StatusOK {
		t.Errorf("/auth/me as the new admin: HTTP %d", rec.Code)
	}
	rec = s.doFrom("GET", "/api/v1/setup", "", lan, session)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"tmdb_configured":true`) || !strings.Contains(rec.Body.String(), `"libraries_chosen":true`) {
		t.Errorf("setup state as admin: HTTP %d %s", rec.Code, rec.Body)
	}
	if rec := s.doFrom("GET", "/api/v1/apikeys", "", lan, session); rec.Code != http.StatusOK {
		t.Errorf("GET /apikeys as the new admin: HTTP %d", rec.Code)
	}
	if rec := s.doFrom("GET", "/api/v1/status", "", lan, session); !strings.Contains(rec.Body.String(), `"needs_setup":false`) {
		t.Errorf("status after: %s", rec.Body)
	}
	// And the setup cookie, alone, opens nothing now.
	for _, call := range []string{"GET /api/v1/setup", "GET /api/v1/auth/me", "GET /api/v1/apikeys"} {
		method, path, _ := strings.Cut(call, " ")
		if rec := s.doFrom(method, path, "", lan, c); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s on the dead setup cookie: HTTP %d, want 401", call, rec.Code)
		}
	}
	// The password signs in.
	if rec := s.doFrom("POST", "/api/v1/auth/login", `{"username":"owner@example.com","password":"long-enough-1"}`, lan); rec.Code != http.StatusOK {
		t.Errorf("login with the password: HTTP %d %s", rec.Code, rec.Body)
	}
}

// The wizard's Plex step needs plex.tv to answer for the owner token; Plex's owner claim
// is the same login the login page uses, accepted while a setup session is open.
func TestSetupClaimWithPlex(t *testing.T) {
	setup := func(t *testing.T) (*routeServer, *fakePlexTV, *http.Cookie) {
		s, tv := plexLoginServer(t)
		rec := s.doFrom("POST", "/api/v1/setup/session", "", lan)
		c := cookieNamed(rec, setupCookieName)
		if rec.Code != http.StatusCreated || c == nil {
			t.Fatalf("session: HTTP %d", rec.Code)
		}
		return s, tv, c
	}
	signIn := func(t *testing.T, s *routeServer, tv *fakePlexTV, token string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		id, _, pinCookie := startPin(t, s, "/api/v1/auth/plex/pin", nil, nil)
		tv.approve(id, token)
		return s.doCookies("GET", fmt.Sprintf("/api/v1/auth/plex/pin/%d", id), "", append([]*http.Cookie{pinCookie}, cookies...)...)
	}

	t.Run("the server's owner becomes the admin", func(t *testing.T) {
		s, tv, c := setup(t)
		rec := signIn(t, s, tv, "owner-token", c)
		u := signedInAs(t, rec)
		if u.Role != auth.RoleAdmin || !u.PlexLinked || u.PlexUsername != "owner" {
			t.Fatalf("signed in as %+v, want the Plex-linked admin", u)
		}
		if !s.deps.Settings.GetBool(context.Background(), keyPlexSignInStaff, false) {
			t.Error("staff Plex sign-in wasn't switched on, so the owner couldn't come back this way")
		}
		if !s.deps.Settings.GetBool(context.Background(), keySetupComplete, false) {
			t.Error("setup_complete wasn't recorded")
		}
		if rec := s.doCookies("GET", "/api/v1/setup", "", c); rec.Code != http.StatusUnauthorized {
			t.Errorf("the setup cookie after the claim: HTTP %d, want 401", rec.Code)
		}
		// They sign in again later, on the login page, with no setup cookie.
		again := signedInAs(t, signIn(t, s, tv, "owner-token"))
		if again.ID != u.ID {
			t.Errorf("second Plex sign-in is account %d, want %d", again.ID, u.ID)
		}
	})
	t.Run("without a setup session the owner is not made an admin", func(t *testing.T) {
		s, tv, _ := setup(t)
		rec := signIn(t, s, tv, "owner-token")
		if rec.Code != http.StatusConflict {
			t.Errorf("HTTP %d %s, want 409", rec.Code, rec.Body)
		}
		noSession(t, rec)
		if n, _ := s.auth.CountAdmins(context.Background()); n != 0 {
			t.Error("an admin was made without a setup session")
		}
	})
	t.Run("a friend with access is a requester, never the admin", func(t *testing.T) {
		s, tv, c := setup(t)
		tv.account("kid-token", plex.Account{ID: 555, Username: "kid"}, fakeResource{Name: "Home", ID: "M1"})
		u := signedInAs(t, signIn(t, s, tv, "kid-token", c))
		if u.Role != auth.RoleRequester {
			t.Errorf("a friend signed in as %s", u.Role)
		}
		if n, _ := s.auth.CountAdmins(context.Background()); n != 0 {
			t.Error("a friend's sign-in made an admin")
		}
	})
	t.Run("an account with no access gets nothing", func(t *testing.T) {
		s, tv, c := setup(t)
		tv.account("stranger-token", plex.Account{ID: 9, Username: "stranger"}, fakeResource{Name: "Elsewhere", ID: "OTHER", Owned: true})
		rec := signIn(t, s, tv, "stranger-token", c)
		if rec.Code != http.StatusForbidden {
			t.Errorf("HTTP %d, want 403", rec.Code)
		}
		if n, _ := s.auth.CountAdmins(context.Background()); n != 0 {
			t.Error("an admin was made from a stranger's Plex account")
		}
	})
	t.Run("after the admin exists a stale setup cookie claims nothing", func(t *testing.T) {
		s, tv, c := setup(t)
		if _, err := s.auth.CreateFirstAdmin(context.Background(), "boss@example.com", "long-enough-1"); err != nil {
			t.Fatal(err)
		}
		rec := signIn(t, s, tv, "owner-token", c)
		if rec.Code != http.StatusConflict {
			t.Errorf("HTTP %d %s, want 409", rec.Code, rec.Body)
		}
		if n, _ := s.auth.CountAdmins(context.Background()); n != 1 {
			t.Errorf("%d admins, want 1", n)
		}
	})
}

// The Jellyfin owner claim: a Jellyfin administrator, verified by the connected server.
func TestSetupClaimWithJellyfin(t *testing.T) {
	setup := func(t *testing.T) (*routeServer, *http.Cookie) {
		s, _, _ := jellyfinLoginServer(t)
		rec := s.doFrom("POST", "/api/v1/setup/session", "", lan)
		c := cookieNamed(rec, setupCookieName)
		if rec.Code != http.StatusCreated || c == nil {
			t.Fatalf("session: HTTP %d", rec.Code)
		}
		return s, c
	}
	t.Run("a Jellyfin administrator becomes the admin", func(t *testing.T) {
		s, c := setup(t)
		rec := s.doFrom("POST", "/api/v1/auth/jellyfin", `{"username":"boss","password":"boss-password-1"}`, lan, c)
		u := signedInAs(t, rec)
		if u.Role != auth.RoleAdmin || !u.JellyfinLinked || u.JellyfinUsername != "boss" {
			t.Fatalf("signed in as %+v", u)
		}
		if !s.deps.Settings.GetBool(context.Background(), keyJellyfinSignInStaff, false) {
			t.Error("staff Jellyfin sign-in wasn't switched on")
		}
		if rec := s.doFrom("GET", "/api/v1/setup", "", lan, c); rec.Code != http.StatusUnauthorized {
			t.Errorf("the setup cookie after the claim: HTTP %d, want 401", rec.Code)
		}
		again := signedInAs(t, s.doFrom("POST", "/api/v1/auth/jellyfin", `{"username":"boss","password":"boss-password-1"}`, lan))
		if again.ID != u.ID {
			t.Errorf("second sign-in is account %d, want %d", again.ID, u.ID)
		}
	})
	t.Run("without a setup session an administrator is turned away", func(t *testing.T) {
		s, _ := setup(t)
		rec := s.doFrom("POST", "/api/v1/auth/jellyfin", `{"username":"boss","password":"boss-password-1"}`, lan)
		if rec.Code != http.StatusConflict {
			t.Errorf("HTTP %d %s, want 409", rec.Code, rec.Body)
		}
		noSession(t, rec)
		if n, _ := s.auth.CountAdmins(context.Background()); n != 0 {
			t.Error("an admin was made without a setup session")
		}
	})
	t.Run("a plain Jellyfin user is a requester, never the admin", func(t *testing.T) {
		s, c := setup(t)
		u := signedInAs(t, s.doFrom("POST", "/api/v1/auth/jellyfin", `{"username":"kid","password":"kid-password-1"}`, lan, c))
		if u.Role != auth.RoleRequester {
			t.Errorf("signed in as %s", u.Role)
		}
		if n, _ := s.auth.CountAdmins(context.Background()); n != 0 {
			t.Error("a plain user's sign-in made an admin")
		}
	})
	t.Run("a wrong password makes nothing", func(t *testing.T) {
		s, c := setup(t)
		rec := s.doFrom("POST", "/api/v1/auth/jellyfin", `{"username":"boss","password":"wrong"}`, lan, c)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("HTTP %d, want 401", rec.Code)
		}
		if n, _ := s.auth.CountAdmins(context.Background()); n != 0 {
			t.Error("an admin was made from a wrong password")
		}
	})
}
