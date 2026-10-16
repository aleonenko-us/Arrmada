package download

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fakeQbit is a qBittorrent WebUI with the parts the switch to a password depends on, as
// qBittorrent 5.2 behaves (checked against the real one): the subnet whitelist lets a
// whitelisted address in whatever it sends, a session cookie handed out then keeps working
// after the whitelist goes off, X-Forwarded-For is believed only from a trusted reverse
// proxy, a wrong login is 401, and CSRF protection refuses a Referer or Origin from another
// host. Every request comes from peer (Arrmada's address on the Docker network).
type fakeQbit struct {
	mu   sync.Mutex
	peer string
	user string
	pass string
	// preferences
	whitelistOn  bool
	whitelist    string
	localBypass  bool
	csrf         bool
	proxyOn      bool
	proxyList    string
	sessions     map[string]bool
	failedLogins map[string]int
	// fail, when set, can refuse a setPreferences (500) before it's applied.
	fail func(prefs map[string]any) bool
	// ignorePassword drops web_ui_password from setPreferences, as if it never took.
	ignorePassword bool
	// noProxySupport: X-Forwarded-For is never believed (a qBittorrent without
	// reverse-proxy support), so the check can't tell a real login from the whitelist.
	noProxySupport bool
	// loginsFrom lists, per username, the addresses each sign-in was judged to come from.
	loginsFrom map[string][]string
	// csrfRefused counts requests CSRF protection turned away.
	csrfRefused int
	torrents    []string
	srv         *httptest.Server
}

func newFakeQbit(t *testing.T) *fakeQbit {
	t.Helper()
	f := &fakeQbit{
		peer: "172.18.0.5", user: "admin",
		whitelistOn: true, whitelist: "10.0.0.0/8\n172.16.0.0/12\n192.168.0.0/16",
		localBypass: true, sessions: map[string]bool{}, failedLogins: map[string]int{},
		torrents: []string{"aaaa"},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func inSubnets(ip, list string) bool {
	addr := net.ParseIP(ip)
	for _, s := range splitSubnets(list) {
		if _, n, err := net.ParseCIDR(s); err == nil && n.Contains(addr) {
			return true
		}
		if s == ip {
			return true
		}
	}
	return false
}

func (f *fakeQbit) client(r *http.Request) string {
	if f.proxyOn && !f.noProxySupport && inSubnets(f.peer, f.proxyList) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	return f.peer
}

func (f *fakeQbit) crossSite(r *http.Request) bool {
	for _, h := range []string{"Origin", "Referer"} {
		if v := r.Header.Get(h); v != "" {
			u, err := url.Parse(v)
			return err != nil || u.Host != r.Host
		}
	}
	return false // neither: qBittorrent lets it through
}

func (f *fakeQbit) newSession(w http.ResponseWriter) {
	sid := fmt.Sprintf("sid%d", len(f.sessions)+1)
	f.sessions[sid] = true
	http.SetCookie(w, &http.Cookie{Name: "QBT_SID_8080", Value: sid, Path: "/"})
}

func (f *fakeQbit) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.csrf && f.crossSite(r) {
		f.csrfRefused++
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	client := f.client(r)
	bypass := f.whitelistOn && inSubnets(client, f.whitelist)
	authed := bypass
	if c, err := r.Cookie("QBT_SID_8080"); err == nil && f.sessions[c.Value] {
		authed = true
	}
	if r.URL.Path == "/api/v2/auth/login" {
		_ = r.ParseForm()
		if f.loginsFrom == nil {
			f.loginsFrom = map[string][]string{}
		}
		u := r.PostForm.Get("username")
		f.loginsFrom[u] = append(f.loginsFrom[u], client)
		switch {
		case f.failedLogins[client] >= 5:
			http.Error(w, "Forbidden", http.StatusForbidden)
		case bypass:
			f.newSession(w)
			w.WriteHeader(http.StatusNoContent)
		case r.PostForm.Get("username") == f.user && f.pass != "" && r.PostForm.Get("password") == f.pass:
			delete(f.failedLogins, client)
			f.newSession(w)
			w.WriteHeader(http.StatusNoContent)
		default:
			f.failedLogins[client]++
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		}
		return
	}
	if !authed {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	switch r.URL.Path {
	case "/api/v2/app/preferences":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"bypass_auth_subnet_whitelist_enabled": f.whitelistOn,
			"bypass_auth_subnet_whitelist":         f.whitelist,
			"bypass_local_auth":                    f.localBypass,
			"web_ui_csrf_protection_enabled":       f.csrf,
			"web_ui_username":                      f.user,
			"web_ui_reverse_proxy_enabled":         f.proxyOn,
			"web_ui_reverse_proxies_list":          f.proxyList,
			"listen_port":                          6881,
			"save_path":                            "/media/downloads",
		})
	case "/api/v2/app/setPreferences":
		_ = r.ParseForm()
		var p map[string]any
		if err := json.Unmarshal([]byte(r.PostForm.Get("json")), &p); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if f.fail != nil && f.fail(p) {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		for k, v := range p {
			switch k {
			case "bypass_auth_subnet_whitelist_enabled":
				f.whitelistOn = v.(bool)
			case "bypass_local_auth":
				f.localBypass = v.(bool)
			case "web_ui_csrf_protection_enabled":
				f.csrf = v.(bool)
			case "web_ui_reverse_proxy_enabled":
				f.proxyOn = v.(bool)
			case "web_ui_reverse_proxies_list":
				f.proxyList = v.(string)
			case "web_ui_username":
				f.user = v.(string)
			case "web_ui_password":
				if !f.ignorePassword {
					f.pass = v.(string)
				}
			}
		}
	case "/api/v2/app/version":
		_, _ = io.WriteString(w, "v5.2.3")
	case "/api/v2/torrents/info":
		var out []map[string]any
		for _, h := range f.torrents {
			out = append(out, map[string]any{"hash": h, "name": h, "state": "stalledUP", "progress": 1})
		}
		_ = json.NewEncoder(w).Encode(out)
	case "/api/v2/torrents/add":
		f.torrents = append(f.torrents, "added")
	default:
		w.WriteHeader(http.StatusOK)
	}
}

// anonymous is a request from another container on the network with no login, as
// FlareSolverr's browser could make: what does qBittorrent answer?
func (f *fakeQbit) anonymous(t *testing.T) int {
	t.Helper()
	resp, err := http.Get(f.srv.URL + "/api/v2/torrents/info")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// fakeQbitView is the fake's settings at one moment.
type fakeQbitView struct {
	user, pass, whitelist, proxyList string
	whitelistOn, localBypass, csrf   bool
	proxyOn                          bool
}

func (f *fakeQbit) snapshot() fakeQbitView {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakeQbitView{user: f.user, pass: f.pass, whitelistOn: f.whitelistOn, whitelist: f.whitelist, localBypass: f.localBypass,
		csrf: f.csrf, proxyOn: f.proxyOn, proxyList: f.proxyList}
}

func (f *fakeQbit) set(fn func(f *fakeQbit)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

// memKeeper keeps the login in memory, as the file in the data folder would.
type memKeeper struct {
	user, pass string
	failMake   bool
	made       int
}

func (k *memKeeper) Load() (string, string, bool, error) {
	return k.user, k.pass, k.pass != "", nil
}

func (k *memKeeper) Make(username string) (string, error) {
	if k.failMake {
		return "", errors.New("disk full")
	}
	k.made++
	k.user, k.pass = username, fmt.Sprintf("generated-%d-xxxxxxxxxxxxxxxx", k.made)
	return k.pass, nil
}

func registerBundled(t *testing.T, svc *Service, url, user, pass string) Client {
	t.Helper()
	ctx := context.Background()
	if err := svc.EnsureBundled(ctx, url); err != nil {
		t.Fatal(err)
	}
	c, _, err := svc.BundledClient(ctx, KindQbittorrent)
	if err != nil {
		t.Fatal(err)
	}
	if user != "" || pass != "" {
		c.Username, c.Password = user, pass
		if c, err = svc.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func bundledRow(t *testing.T, svc *Service) Client {
	t.Helper()
	c, ok, err := svc.BundledClient(context.Background(), KindQbittorrent)
	if err != nil || !ok {
		t.Fatalf("bundled client: %v %v", ok, err)
	}
	return c
}

// assertSecured: no way in without the login, the client signs in with it and still drives
// qBittorrent (torrents intact), CSRF is on, and the borrowed proxy settings went back.
func assertSecured(t *testing.T, svc *Service, f *fakeQbit, wantPass string) {
	t.Helper()
	s := f.snapshot()
	if s.whitelistOn || s.localBypass || !s.csrf {
		t.Errorf("not secured: whitelist %v, local bypass %v, csrf %v", s.whitelistOn, s.localBypass, s.csrf)
	}
	if s.pass != wantPass {
		t.Errorf("qBittorrent's password %q, want %q", s.pass, wantPass)
	}
	if s.proxyOn || s.proxyList != "" {
		t.Errorf("reverse-proxy settings left borrowed: %v %q", s.proxyOn, s.proxyList)
	}
	if code := f.anonymous(t); code != http.StatusForbidden {
		t.Errorf("an anonymous request from the network got HTTP %d, want 403", code)
	}
	c := bundledRow(t, svc)
	if c.Password != wantPass {
		t.Errorf("the client's password %q, want %q", c.Password, wantPass)
	}
	items, err := NewQBittorrent().List(context.Background(), c)
	if err != nil || len(items) == 0 || items[0].Hash != "aaaa" {
		t.Errorf("the client can't read the torrents after the switch: %v %+v", err, items)
	}
}

func TestSecureBundledQbitNewInstall(t *testing.T) {
	svc, flags := newTestService(t)
	f := newFakeQbit(t)
	registerBundled(t, svc, f.srv.URL, "", "")
	if code := f.anonymous(t); code != http.StatusOK {
		t.Fatalf("precondition: the seeded whitelist lets anyone in (got %d)", code)
	}
	k := &memKeeper{}
	// Giving the client its login is Arrmada's change, not the owner's edit: it leaves
	// notes kept for the owner's edits (the all-in-one switch's) alone.
	_ = flags.Set(context.Background(), KeyBundledSwitchedOff, "1")
	out, err := svc.SecureBundledQbit(context.Background(), f.srv.URL, k)
	if err != nil || out != SecureNewPassword {
		t.Fatalf("SecureBundledQbit = %v, %v", out, err)
	}
	if flags.Get(context.Background(), KeyBundledSwitchedOff, "") != "1" {
		t.Error("giving the client its login counted as the owner's edit")
	}
	assertSecured(t, svc, f, k.pass)
	if k.user != "admin" || bundledRow(t, svc).Username != "admin" {
		t.Errorf("username: kept %q, client %q", k.user, bundledRow(t, svc).Username)
	}
	if !strings.Contains(flags.Get(context.Background(), KeyBundledQbitAuth, ""), qbitAuthSecured) {
		t.Error("not recorded as done")
	}
	// The next start's EnsureBundledLogin sees the login it would give is already given.
	if flags.Get(context.Background(), KeyBundledLogin, "") == "" {
		t.Error("the login given to the client wasn't recorded")
	}

	// Again (every start): nothing to do, nothing changed, no new password.
	out, err = svc.SecureBundledQbit(context.Background(), f.srv.URL, k)
	if err != nil || out != SecureAlready || k.made != 1 {
		t.Fatalf("second run = %v, %v (passwords made %d)", out, err, k.made)
	}
	assertSecured(t, svc, f, k.pass)
}

// The owner had typed qBittorrent's real login on the card: it stays the password.
func TestSecureBundledQbitKeepsOwnersPassword(t *testing.T) {
	svc, _ := newTestService(t)
	f := newFakeQbit(t)
	f.set(func(f *fakeQbit) { f.user, f.pass = "me", "owners-own-password" })
	registerBundled(t, svc, f.srv.URL, "me", "owners-own-password")
	k := &memKeeper{}
	out, err := svc.SecureBundledQbit(context.Background(), f.srv.URL, k)
	if err != nil || out != SecureKeptPassword {
		t.Fatalf("SecureBundledQbit = %v, %v", out, err)
	}
	if k.made != 0 {
		t.Error("a password was made although the owner's works")
	}
	assertSecured(t, svc, f, "owners-own-password")
	if f.snapshot().user != "me" {
		t.Error("the owner's username changed")
	}
}

// A login on the card that qBittorrent doesn't accept (typed long ago, never checked under
// the whitelist): Arrmada can't rely on it, so it makes its own.
func TestSecureBundledQbitStaleCardLogin(t *testing.T) {
	svc, _ := newTestService(t)
	f := newFakeQbit(t)
	f.set(func(f *fakeQbit) { f.pass = "the-real-one" })
	registerBundled(t, svc, f.srv.URL, "admin", "stale")
	k := &memKeeper{}
	out, err := svc.SecureBundledQbit(context.Background(), f.srv.URL, k)
	if err != nil || out != SecureNewPassword {
		t.Fatalf("SecureBundledQbit = %v, %v", out, err)
	}
	assertSecured(t, svc, f, k.pass)
	// The checks' failed logins were counted against the outside addresses, never Arrmada's.
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failedLogins[f.peer] != 0 {
		t.Errorf("Arrmada's address has %d failed logins against it", f.failedLogins[f.peer])
	}
}

// Every step that can fail leaves the whitelist on (or Arrmada with a login that works),
// and the next start finishes the job.
func TestSecureBundledQbitFailureAtEachStep(t *testing.T) {
	steps := []struct {
		name string
		fail func(p map[string]any) bool
	}{
		{"set password", func(p map[string]any) bool { _, ok := p["web_ui_password"]; return ok }},
		{"borrow proxy", func(p map[string]any) bool { return p["web_ui_reverse_proxy_enabled"] == true }},
		{"put proxy back", func(p map[string]any) bool {
			return p["web_ui_reverse_proxy_enabled"] == false && p["bypass_auth_subnet_whitelist_enabled"] == nil
		}},
		{"switch whitelist off", func(p map[string]any) bool { return p["bypass_auth_subnet_whitelist_enabled"] == false }},
	}
	for _, tc := range steps {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newTestService(t)
			f := newFakeQbit(t)
			registerBundled(t, svc, f.srv.URL, "", "")
			k := &memKeeper{}
			f.set(func(f *fakeQbit) { f.fail = tc.fail })
			if _, err := svc.SecureBundledQbit(context.Background(), f.srv.URL, k); err == nil {
				t.Fatal("no error reported")
			}
			// Arrmada still gets in with what the client has.
			if _, err := NewQBittorrent().List(context.Background(), bundledRow(t, svc)); err != nil {
				t.Fatalf("Arrmada was shut out after a failed %s: %v", tc.name, err)
			}
			f.set(func(f *fakeQbit) { f.fail = nil })
			out, err := svc.SecureBundledQbit(context.Background(), f.srv.URL, k)
			if err != nil || out != SecureNewPassword {
				t.Fatalf("next start = %v, %v", out, err)
			}
			if k.made != 1 {
				t.Errorf("the saved login wasn't reused: %d made", k.made)
			}
			assertSecured(t, svc, f, k.pass)
		})
	}

	t.Run("save login", func(t *testing.T) {
		svc, _ := newTestService(t)
		f := newFakeQbit(t)
		registerBundled(t, svc, f.srv.URL, "", "")
		if _, err := svc.SecureBundledQbit(context.Background(), f.srv.URL, &memKeeper{failMake: true}); err == nil {
			t.Fatal("no error")
		}
		if s := f.snapshot(); !s.whitelistOn || s.pass != "" {
			t.Errorf("qBittorrent changed without a saved login: %+v", s)
		}
	})

	t.Run("password never took", func(t *testing.T) {
		svc, _ := newTestService(t)
		f := newFakeQbit(t)
		registerBundled(t, svc, f.srv.URL, "", "")
		f.set(func(f *fakeQbit) { f.ignorePassword = true })
		if _, err := svc.SecureBundledQbit(context.Background(), f.srv.URL, &memKeeper{}); err == nil {
			t.Fatal("no error")
		}
		if s := f.snapshot(); !s.whitelistOn || s.proxyOn {
			t.Errorf("the whitelist went off (or the proxy settings stayed) although the login was never accepted: %+v", s)
		}
		if _, err := NewQBittorrent().List(context.Background(), bundledRow(t, svc)); err != nil {
			t.Fatalf("Arrmada was shut out: %v", err)
		}
	})

	t.Run("the check can't be trusted", func(t *testing.T) {
		svc, _ := newTestService(t)
		f := newFakeQbit(t)
		// No reverse-proxy support: the check's sign-ins still come from Arrmada's
		// whitelisted address, so the wrong login gets in too.
		f.set(func(f *fakeQbit) { f.noProxySupport = true })
		registerBundled(t, svc, f.srv.URL, "", "")
		if _, err := svc.SecureBundledQbit(context.Background(), f.srv.URL, &memKeeper{}); err == nil {
			t.Fatal("no error")
		}
		if s := f.snapshot(); !s.whitelistOn {
			t.Error("the whitelist went off on an unproven login")
		}
	})
}

// A restart in the middle: after the whitelist went off but before CSRF came on, the next
// start finishes; one while the proxy settings were borrowed puts them back first.
func TestSecureBundledQbitResumes(t *testing.T) {
	ctx := context.Background()
	t.Run("after the whitelist went off", func(t *testing.T) {
		svc, flags := newTestService(t)
		f := newFakeQbit(t)
		k := &memKeeper{user: "admin", pass: "kept-login-xxxxxxxxxxxx"}
		registerBundled(t, svc, f.srv.URL, "admin", k.pass)
		f.set(func(f *fakeQbit) { f.pass, f.whitelistOn = k.pass, false })
		_ = flags.Set(ctx, KeyBundledQbitAuth, `{"phase":"switching"}`)
		out, err := svc.SecureBundledQbit(ctx, f.srv.URL, k)
		if err != nil || out != SecureNewPassword {
			t.Fatalf("= %v, %v", out, err)
		}
		assertSecured(t, svc, f, k.pass)
	})
	t.Run("while the proxy settings were borrowed", func(t *testing.T) {
		svc, flags := newTestService(t)
		f := newFakeQbit(t)
		f.set(func(f *fakeQbit) { f.proxyOn, f.proxyList = true, "10.0.0.0/8;172.16.0.0/12;192.168.0.0/16" })
		registerBundled(t, svc, f.srv.URL, "", "")
		_ = flags.Set(ctx, KeyBundledQbitAuth, `{"phase":"verifying"}`)
		k := &memKeeper{}
		if out, err := svc.SecureBundledQbit(ctx, f.srv.URL, k); err != nil || out != SecureNewPassword {
			t.Fatalf("= %v, %v", out, err)
		}
		assertSecured(t, svc, f, k.pass)
	})
	t.Run("the owner's own proxy settings come back", func(t *testing.T) {
		svc, _ := newTestService(t)
		f := newFakeQbit(t)
		f.set(func(f *fakeQbit) { f.proxyOn, f.proxyList = true, "172.20.0.9" })
		registerBundled(t, svc, f.srv.URL, "", "")
		if _, err := svc.SecureBundledQbit(ctx, f.srv.URL, &memKeeper{}); err != nil {
			t.Fatal(err)
		}
		if s := f.snapshot(); !s.proxyOn || s.proxyList != "172.20.0.9" {
			t.Errorf("the owner's reverse-proxy settings weren't put back: %v %q", s.proxyOn, s.proxyList)
		}
	})
}

// Not the bundled qBittorrent of this install: never touched.
func TestSecureBundledQbitLeavesOthersAlone(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, svc *Service, url string)
	}{
		{"pointed at the owner's own", func(t *testing.T, svc *Service, url string) {
			c := registerBundled(t, svc, url, "", "")
			c.URL = "http://192.168.1.20:8080"
			if _, err := svc.Update(ctx, c); err != nil {
				t.Fatal(err)
			}
		}},
		{"switched off", func(t *testing.T, svc *Service, url string) {
			c := registerBundled(t, svc, url, "", "")
			c.Enabled = false
			if _, err := svc.Update(ctx, c); err != nil {
				t.Fatal(err)
			}
		}},
		{"removed", func(t *testing.T, svc *Service, url string) {
			c := registerBundled(t, svc, url, "", "")
			if err := svc.Delete(ctx, c.ID); err != nil {
				t.Fatal(err)
			}
		}},
		{"only an owner-added client", func(t *testing.T, svc *Service, url string) {
			if _, err := svc.Create(ctx, Client{Name: "mine", Kind: KindQbittorrent, URL: url, Enabled: true}); err != nil {
				t.Fatal(err)
			}
			if err := svc.flags.Set(ctx, KeyBundledRemoved, "1"); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newTestService(t)
			f := newFakeQbit(t)
			tc.setup(t, svc, f.srv.URL)
			out, err := svc.SecureBundledQbit(ctx, f.srv.URL, &memKeeper{})
			if err != nil || out != SecureSkipped {
				t.Fatalf("= %v, %v", out, err)
			}
			if s := f.snapshot(); !s.whitelistOn || s.pass != "" || s.csrf {
				t.Errorf("touched: %+v", s)
			}
		})
	}
}

// Once done, a whitelist the owner sets up is theirs; a fresh config with the seeded one
// (qBittorrent's settings started over) is secured again with the saved login.
func TestSecureBundledQbitAfterDone(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	f := newFakeQbit(t)
	registerBundled(t, svc, f.srv.URL, "", "")
	k := &memKeeper{}
	if _, err := svc.SecureBundledQbit(ctx, f.srv.URL, k); err != nil {
		t.Fatal(err)
	}
	f.set(func(f *fakeQbit) { f.whitelistOn, f.whitelist = true, "192.168.1.0/24" })
	if out, err := svc.SecureBundledQbit(ctx, f.srv.URL, k); err != nil || out != SecureOwnWhitelist {
		t.Fatalf("owner's whitelist: %v, %v", out, err)
	}
	if !f.snapshot().whitelistOn {
		t.Error("the owner's whitelist was switched off")
	}
	// qBittorrent's config started over: seeded whitelist, no password.
	f.set(func(f *fakeQbit) {
		f.whitelistOn, f.whitelist, f.pass, f.csrf, f.localBypass = true, "10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16", "", false, true
	})
	if out, err := svc.SecureBundledQbit(ctx, f.srv.URL, k); err != nil || out != SecureNewPassword {
		t.Fatalf("fresh config: %v, %v", out, err)
	}
	if k.made != 1 {
		t.Errorf("the saved login wasn't reused (%d made)", k.made)
	}
	assertSecured(t, svc, f, k.pass)
}

// Locked out already (a password Arrmada doesn't have): it says so and changes nothing.
func TestSecureBundledQbitRefused(t *testing.T) {
	svc, _ := newTestService(t)
	f := newFakeQbit(t)
	f.set(func(f *fakeQbit) { f.whitelistOn, f.pass = false, "unknown-to-arrmada" })
	registerBundled(t, svc, f.srv.URL, "", "")
	_, err := svc.SecureBundledQbit(context.Background(), f.srv.URL, &memKeeper{})
	if !errors.Is(err, ErrQbitLoginRefused) {
		t.Fatalf("err = %v, want ErrQbitLoginRefused", err)
	}
}

func TestSecureBundledQbitUnreachable(t *testing.T) {
	svc, _ := newTestService(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close()
	registerBundled(t, svc, u, "", "")
	_, err := svc.SecureBundledQbit(context.Background(), u, &memKeeper{})
	if !errors.Is(err, ErrQbitUnreachable) {
		t.Fatalf("err = %v, want ErrQbitUnreachable", err)
	}
}

// The passwords never reach the log, whatever happens.
func TestSecureBundledQbitLogsNoPassword(t *testing.T) {
	var buf bytes.Buffer
	svc, _ := newTestService(t)
	svc.log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	f := newFakeQbit(t)
	f.set(func(f *fakeQbit) { f.pass = "the-real-one-zzzz" })
	registerBundled(t, svc, f.srv.URL, "admin", "stale-card-password")
	k := &memKeeper{}
	_, err := svc.SecureBundledQbit(context.Background(), f.srv.URL, k)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{k.pass, "stale-card-password", "the-real-one-zzzz"} {
		if strings.Contains(buf.String(), secret) || strings.Contains(fmt.Sprint(err), secret) {
			t.Errorf("a password reached the log: %s", buf.String())
		}
	}
}

// The first run finds a whitelist the owner set up (not the seed's): it's theirs, nothing
// changes, and it's recorded as done so a later start leaves it alone too.
func TestSecureBundledQbitOwnersWhitelistFirstRun(t *testing.T) {
	ctx := context.Background()
	svc, flags := newTestService(t)
	f := newFakeQbit(t)
	f.set(func(f *fakeQbit) { f.whitelist = "172.16.0.0/12\n192.168.1.0/24" })
	registerBundled(t, svc, f.srv.URL, "", "")
	k := &memKeeper{}
	out, err := svc.SecureBundledQbit(ctx, f.srv.URL, k)
	if err != nil || out != SecureOwnWhitelist {
		t.Fatalf("= %v, %v", out, err)
	}
	if s := f.snapshot(); !s.whitelistOn || s.pass != "" || s.csrf || k.made != 0 {
		t.Errorf("the owner's setup was changed: %+v (made %d)", s, k.made)
	}
	if !strings.Contains(flags.Get(ctx, KeyBundledQbitAuth, ""), qbitAuthSecured) {
		t.Error("not recorded as done")
	}
	if out, err := svc.SecureBundledQbit(ctx, f.srv.URL, k); err != nil || out != SecureOwnWhitelist {
		t.Errorf("next start = %v, %v", out, err)
	}
}

// The wrong login and the real one are judged from different outside addresses, neither
// of them Arrmada's.
func TestSecureBundledQbitCheckAddresses(t *testing.T) {
	svc, _ := newTestService(t)
	f := newFakeQbit(t)
	registerBundled(t, svc, f.srv.URL, "", "")
	if _, err := svc.SecureBundledQbit(context.Background(), f.srv.URL, &memKeeper{}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	canary, real := f.loginsFrom["arrmada-check"], f.loginsFrom["admin"]
	if len(canary) != 1 {
		t.Fatalf("wrong-login checks from %v", canary)
	}
	var outside []string
	for _, ip := range real {
		if ip != f.peer {
			outside = append(outside, ip)
		}
	}
	if len(outside) != 1 || outside[0] == canary[0] || canary[0] == f.peer {
		t.Errorf("the check's addresses: wrong login %v, real login %v (Arrmada %s)", canary, real, f.peer)
	}
}

// The owner already switched the whitelist off but left the seed's CSRF and localhost
// settings: the first run switches those on and touches nothing else.
func TestSecureBundledQbitWhitelistAlreadyOff(t *testing.T) {
	svc, _ := newTestService(t)
	f := newFakeQbit(t)
	f.set(func(f *fakeQbit) { f.whitelistOn, f.pass = false, "owners-pw-xxxxxxxx" })
	registerBundled(t, svc, f.srv.URL, "admin", "owners-pw-xxxxxxxx")
	k := &memKeeper{}
	out, err := svc.SecureBundledQbit(context.Background(), f.srv.URL, k)
	if err != nil || out != SecureAlready {
		t.Fatalf("= %v, %v", out, err)
	}
	if s := f.snapshot(); !s.csrf || s.localBypass || s.whitelistOn || s.pass != "owners-pw-xxxxxxxx" || k.made != 0 {
		t.Errorf("after: %+v (made %d)", s, k.made)
	}
	// Done: an owner who turns CSRF off again afterwards keeps it off.
	f.set(func(f *fakeQbit) { f.csrf = false })
	if _, err := svc.SecureBundledQbit(context.Background(), f.srv.URL, k); err != nil || f.snapshot().csrf {
		t.Errorf("a later start changed the owner's choice (err %v)", err)
	}
}

// Reverse-proxy trust of exactly the seed subnets, left by a check whose progress record
// was lost (a database rolled back), is switched off on the first run.
func TestSecureBundledQbitLeftoverProxyTrust(t *testing.T) {
	svc, _ := newTestService(t)
	f := newFakeQbit(t)
	f.set(func(f *fakeQbit) { f.proxyOn, f.proxyList = true, "10.0.0.0/8;172.16.0.0/12;192.168.0.0/16" })
	registerBundled(t, svc, f.srv.URL, "", "")
	k := &memKeeper{}
	if out, err := svc.SecureBundledQbit(context.Background(), f.srv.URL, k); err != nil || out != SecureNewPassword {
		t.Fatalf("= %v, %v", out, err)
	}
	assertSecured(t, svc, f, k.pass) // checks the proxy trust is off and the list empty
}

func TestOutsideAddress(t *testing.T) {
	for i := 0; i < 50; i++ {
		ip, err := outsideAddress("10.0.0.0/8\n172.16.0.0/12\n192.168.0.0/16")
		if err != nil || ip == "" || inSubnets(ip, "10.0.0.0/8\n172.16.0.0/12\n192.168.0.0/16") {
			t.Fatalf("outsideAddress = %q, %v", ip, err)
		}
	}
	if ip, err := outsideAddress("192.0.2.0/24\n198.51.100.0/24"); err != nil || !strings.HasPrefix(ip, "203.0.113.") {
		t.Errorf("with two ranges whitelisted: %q, %v", ip, err)
	}
	if _, err := outsideAddress("0.0.0.0/0"); err == nil {
		t.Error("no error when the whitelist covers everything")
	}
}

// With CSRF protection on (as the switch leaves it), every call the client makes is let
// through: each one carries a Referer from qBittorrent's own address.
func TestQbittorrentClientPassesCSRF(t *testing.T) {
	ctx := context.Background()
	f := newFakeQbit(t)
	f.set(func(f *fakeQbit) { f.whitelistOn, f.csrf, f.pass = false, true, "pw-for-the-csrf-test" })
	q := NewQBittorrent()
	dc := Client{ID: 7, Kind: KindQbittorrent, URL: f.srv.URL, Username: "admin", Password: "pw-for-the-csrf-test"}
	calls := map[string]func() error{
		"Test": func() error { return q.Test(ctx, dc) },
		"Ping": func() error { return q.Ping(ctx, dc) },
		"Add": func() error {
			return q.Add(ctx, dc, AddRequest{URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"})
		},
		"AddFile":         func() error { return q.Add(ctx, dc, AddRequest{File: []byte("d4:infod4:name1:aee")}) },
		"Remove":          func() error { return q.Remove(ctx, dc, "aaaa", false) },
		"Pause":           func() error { return q.Pause(ctx, dc, "aaaa") },
		"Resume":          func() error { return q.Resume(ctx, dc, "aaaa") },
		"SetCategory":     func() error { return q.SetCategory(ctx, dc, "aaaa", "arrmada") },
		"TorrentAction":   func() error { return q.TorrentAction(ctx, dc, "aaaa", "recheck") },
		"GetSettings":     func() error { _, err := q.GetSettings(ctx, dc); return err },
		"SetSettings":     func() error { return q.SetSettings(ctx, dc, ClientSettings{}) },
		"SetListenPort":   func() error { return q.SetListenPort(ctx, dc, 6881) },
		"SetSavePath":     func() error { return q.SetSavePath(ctx, dc, "/media/downloads") },
		"ListenPort":      func() error { _, err := q.ListenPort(ctx, dc); return err },
		"DefaultSavePath": func() error { _, err := q.DefaultSavePath(ctx, dc); return err },
		"List":            func() error { _, err := q.List(ctx, dc); return err },
	}
	for name, call := range calls {
		if err := call(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	f.mu.Lock()
	refused := f.csrfRefused
	f.mu.Unlock()
	if refused != 0 {
		t.Errorf("CSRF protection turned away %d of the client's requests", refused)
	}

	// The fake does enforce it: a page on another site is refused.
	req, _ := http.NewRequest(http.MethodGet, f.srv.URL+"/api/v2/app/version", nil)
	req.Header.Set("Origin", "http://evil.example:8080")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a cross-site request got HTTP %d", resp.StatusCode)
	}

	// A wrong password reads as one, on qBittorrent 5's 401 as on 4.x's "Fails.".
	bad := dc
	bad.ID, bad.Password = 8, "wrong"
	if err := q.Test(ctx, bad); err == nil || !strings.Contains(err.Error(), "invalid username or password") {
		t.Errorf("wrong password: %v", err)
	}
}
