package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/applog"
	"github.com/tristenlammi/arrmada/internal/auth"
)

// keyReq sends a request carrying a personal API key the way scripts do: in X-Api-Key, or
// as Authorization: Bearer when bearer is set. From a LAN address unless ip says otherwise.
type keyReq struct {
	method, path, body, key string
	bearer                  bool
	ip                      string
	cookie                  *http.Cookie
}

func (s *routeServer) withKey(q keyReq) *httptest.ResponseRecorder {
	r := httptest.NewRequest(q.method, "http://arrmada.local"+q.path, strings.NewReader(q.body))
	r.RemoteAddr = "192.168.1.20:5000"
	if q.ip != "" {
		r.RemoteAddr = q.ip + ":5000"
	}
	if q.body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if q.key != "" {
		if q.bearer {
			r.Header.Set("Authorization", "Bearer "+q.key)
		} else {
			r.Header.Set("X-Api-Key", q.key)
		}
	}
	if q.cookie != nil {
		r.AddCookie(q.cookie)
	}
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, r)
	return rec
}

type madeKey struct {
	Key    string      `json:"key"`
	APIKey auth.APIKey `json:"api_key"`
}

// makeKey creates a key through the API as the session's owner (password "password123").
func (s *routeServer) makeKey(t *testing.T, c *http.Cookie, name, scope string, days int) madeKey {
	t.Helper()
	body := `{"name":` + strconv.Quote(name) + `,"scope":"` + scope + `","expires_in_days":` + strconv.Itoa(days) + `,"password":"password123"}`
	rec := s.doJSON("POST", "/api/v1/me/api-keys", c, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create key: HTTP %d %s", rec.Code, rec.Body)
	}
	var m madeKey
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// The table: every route marked for keys exists, is one already open to callers from
// outside the network, and none of them is about who you are or how you sign in.
func TestAPIKeyRoutesTable(t *testing.T) {
	rt := testRouter(t, true)
	registered := map[string]routeSpec{}
	for _, s := range rt.specs {
		registered[s.Method+" "+s.Pattern] = s
	}
	for pattern := range apiKeyRoutes {
		spec, ok := registered[pattern]
		if !ok {
			t.Errorf("apiKeyRoutes lists %q, which isn't a route", pattern)
			continue
		}
		if !spec.External {
			t.Errorf("%q is marked for API keys but not ext(): keys reach only the outside-facing surface", pattern)
		}
		if spec.Scope == scopeAdmin {
			t.Errorf("%q is admin-only: API keys never reach admin routes", pattern)
		}
	}
}

// Account security stays a signed-in person's business: no key reaches sign-in, sessions,
// passwords, two-factor, key management, the support bundle or outside services' keys —
// including routes that other work adds later under those names.
func TestAPIKeyRoutesExcludeAccountSecurity(t *testing.T) {
	banned := []string{
		"/auth/login", "/auth/logout", "/auth/setup", "/auth/plex", "/auth/jellyfin",
		"/sessions", "/password", "/2fa", "/totp", "/two-factor", "/mfa", "/otp", "/recovery",
		"/api-keys", "/apikeys", "/access-keys", "/support-bundle", "/logs", "/backups",
		"/me/audio", "/me/push", "/me/apprise", "/me/plex", "/me/jellyfin", "/me/kindle",
		"/me/account", "/me/notify-prefs", "/me/calendar-feed", "/users", "/people", "/ws",
	}
	for _, s := range testRouter(t, true).specs {
		if !s.APIKey {
			continue
		}
		for _, b := range banned {
			if strings.Contains(s.Pattern, b) {
				t.Errorf("%s %s is open to API keys but is account security (%s)", s.Method, s.Pattern, b)
			}
		}
	}
}

// Every route, called with a key of every role and both scopes: a route not marked for
// keys answers 403 whatever the role, a read-only key gets 403 from anything but GET, and
// otherwise the role rules are exactly a session's.
func TestRouteAuthzWithAPIKeys(t *testing.T) {
	rt := testRouter(t, true)
	reached := 0
	for _, spec := range rt.specs {
		method := spec.Method
		if method == "" {
			method = http.MethodGet
		}
		path := samplePath(spec.Pattern)
		if spec.Method == "" {
			path = "/movies"
		}
		for _, who := range walkRoles[1:] {
			for _, scope := range []auth.KeyScope{auth.KeyScopeRead, auth.KeyScopeFull} {
				r := httptest.NewRequest(method, path, nil)
				r = withKeyUse(r, &auth.KeyUse{User: &auth.User{ID: 7, Username: "someone", Role: who.role}, KeyID: 1, Scope: scope})
				rec := httptest.NewRecorder()
				rt.ServeHTTP(rec, r)
				want := sentinelStatus
				switch {
				case !spec.APIKey:
					want = http.StatusForbidden
				case scope == auth.KeyScopeRead && method != http.MethodGet:
					want = http.StatusForbidden
				case spec.Scope != scopePublic && !who.role.AtLeast(spec.Scope.role()):
					want = http.StatusForbidden
				}
				if rec.Code != want {
					t.Errorf("%s as a %s %s key: HTTP %d, want %d", spec, scope, who.name, rec.Code, want)
				}
				if rec.Code == sentinelStatus {
					reached++
				}
			}
		}
	}
	if reached == 0 {
		t.Fatal("no route let a key through at all")
	}
}

// Making, listing, using and revoking a key, end to end.
func TestPersonalAPIKeyLifecycle(t *testing.T) {
	s := newRouteServer(t, nil)
	u, c := s.user(t, "kid@example.com", auth.RoleRequester)

	// The account's password is asked for, and a wrong one refused.
	if rec := s.doJSON("POST", "/api/v1/me/api-keys", c, `{"name":"n8n","scope":"full","expires_in_days":0,"password":"nope"}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "password is wrong") {
		t.Fatalf("wrong password: HTTP %d %s", rec.Code, rec.Body)
	}
	for _, bad := range []string{
		`{"name":"","scope":"full","expires_in_days":0,"password":"password123"}`,
		`{"name":"x","scope":"admin","expires_in_days":0,"password":"password123"}`,
		`{"name":"x","scope":"full","expires_in_days":7,"password":"password123"}`,
	} {
		if rec := s.doJSON("POST", "/api/v1/me/api-keys", c, bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: HTTP %d, want 400", bad, rec.Code)
		}
	}

	m := s.makeKey(t, c, "n8n workflow", "full", 90)
	if !strings.HasPrefix(m.Key, "arrk_") || !auth.LooksLikeAPIKey(m.Key) {
		t.Fatalf("key %q hasn't the arrk_ shape", m.Key)
	}
	if want := "arrk_…" + m.Key[len(m.Key)-4:]; m.APIKey.Hint != want {
		t.Errorf("hint = %q, want %q", m.APIKey.Hint, want)
	}
	if m.APIKey.ExpiresAt == nil || m.APIKey.Role != auth.RoleRequester || m.APIKey.Scope != auth.KeyScopeFull {
		t.Errorf("listing = %+v", m.APIKey)
	}
	// Only the hash is stored.
	var stored string
	if err := s.st.DB().QueryRow(`SELECT key_hash FROM api_keys WHERE id = ?`, m.APIKey.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != sha(m.Key) {
		t.Errorf("stored %q, want the key's SHA-256", stored)
	}
	var plain int
	if err := s.st.DB().QueryRow(`SELECT COUNT(*) FROM api_keys WHERE key_hash = ? OR name = ? OR last4 = ?`, m.Key, m.Key, m.Key).Scan(&plain); err != nil || plain != 0 {
		t.Errorf("the key itself is in the table (%d, %v)", plain, err)
	}

	// Shown once: the list carries neither the key nor its hash.
	rec := s.do("GET", "/api/v1/me/api-keys", c)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: HTTP %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, m.Key) || strings.Contains(body, stored) || strings.Contains(body, m.Key[5:20]) {
		t.Errorf("the list gives the key away: %s", body)
	}
	if !strings.Contains(body, `"name":"n8n workflow"`) || !strings.Contains(body, `"password_required":true`) {
		t.Errorf("list: %s", body)
	}

	// It works in either header, as its owner.
	for _, bearer := range []bool{false, true} {
		rec := s.withKey(keyReq{method: "GET", path: "/api/v1/auth/me", key: m.Key, bearer: bearer})
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"username":"kid@example.com"`) {
			t.Errorf("bearer=%v: HTTP %d %s", bearer, rec.Code, rec.Body)
		}
	}
	// Last used, with the coarse network, shows in the owner's list.
	var listed struct{ Keys []auth.APIKey }
	_ = json.Unmarshal(s.do("GET", "/api/v1/me/api-keys", c).Body.Bytes(), &listed)
	if len(listed.Keys) != 1 || listed.Keys[0].LastUsedAt == nil || listed.Keys[0].LastUsedNetwork != "local" {
		t.Errorf("after use: %+v", listed.Keys)
	}

	// Never in the address.
	for _, p := range []string{"/api/v1/auth/me?apikey=" + m.Key, "/api/v1/auth/me?api_key=" + m.Key, "/api/v1/discover/search?q=x&token=" + m.Key} {
		if rec := s.do("GET", p, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s: HTTP %d, want 400", strings.Replace(p, m.Key, "arrk_…", 1), rec.Code)
		}
	}

	// A key can't manage keys, sessions or the password, or reach routes not marked for it.
	for _, call := range []struct{ method, path, body string }{
		{"GET", "/api/v1/me/api-keys", ""},
		{"POST", "/api/v1/me/api-keys", `{"name":"more","scope":"full","expires_in_days":0,"password":"password123"}`},
		{"DELETE", "/api/v1/me/api-keys/" + strconv.FormatInt(m.APIKey.ID, 10), ""},
		{"GET", "/api/v1/me/sessions", ""},
		{"POST", "/api/v1/me/sessions/revoke-others", ""},
		{"POST", "/api/v1/me/password", `{"current":"password123","new":"hijacked123"}`},
		{"GET", "/api/v1/me/account", ""},
		{"POST", "/api/v1/auth/logout", ""},
		{"GET", "/api/v1/calendar", ""},
		{"GET", "/movies", ""},
	} {
		if rec := s.withKey(keyReq{method: call.method, path: call.path, body: call.body, key: m.Key}); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with a key: HTTP %d, want 403", call.method, call.path, rec.Code)
		}
	}
	if n := countKeys(t, s, u.ID); n != 1 {
		t.Errorf("the key made %d keys", n-1)
	}
	if !s.auth.CheckPassword(context.Background(), u.ID, "password123") {
		t.Error("the key changed the password")
	}

	// Revoking takes effect on the very next request.
	if rec := s.do("DELETE", "/api/v1/me/api-keys/"+strconv.FormatInt(m.APIKey.ID, 10), c); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: HTTP %d %s", rec.Code, rec.Body)
	}
	if rec := s.withKey(keyReq{method: "GET", path: "/api/v1/auth/me", key: m.Key}); rec.Code != http.StatusUnauthorized {
		t.Errorf("revoked key: HTTP %d, want 401", rec.Code)
	}
	if rec := s.do("DELETE", "/api/v1/me/api-keys/"+strconv.FormatInt(m.APIKey.ID, 10), c); rec.Code != http.StatusNotFound {
		t.Errorf("revoke twice: HTTP %d, want 404", rec.Code)
	}
}

func countKeys(t *testing.T, s *routeServer, userID int64) int {
	t.Helper()
	var n int
	if err := s.st.DB().QueryRow(`SELECT COUNT(*) FROM api_keys WHERE user_id = ?`, userID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A presented key wins over a browser cookie riding along, so a read-only key can't be
// widened by a session, and a bad key isn't rescued by one.
func TestAPIKeyWinsOverCookie(t *testing.T) {
	s := newRouteServer(t, nil)
	_, c := s.user(t, "kid@example.com", auth.RoleRequester)
	m := s.makeKey(t, c, "read", "read", 0)
	if rec := s.withKey(keyReq{method: "POST", path: "/api/v1/requests", body: `{}`, key: m.Key, cookie: c}); rec.Code != http.StatusForbidden {
		t.Errorf("read-only key with a cookie: HTTP %d, want 403", rec.Code)
	}
	bad := "arrk_" + strings.Repeat("a", 52)
	if rec := s.withKey(keyReq{method: "GET", path: "/api/v1/auth/me", key: bad, cookie: c}); rec.Code != http.StatusUnauthorized {
		t.Errorf("bad key with a cookie: HTTP %d, want 401", rec.Code)
	}
}

// Scope and role: a read-only key only reads; a key is capped by the role it was made
// under (a promotion never widens it) and follows a demotion at once.
func TestAPIKeyScopeAndRoleCap(t *testing.T) {
	s := newRouteServer(t, nil)
	ctx := context.Background()
	kid, kc := s.user(t, "kid@example.com", auth.RoleRequester)
	mgr, mc := s.user(t, "mgr@example.com", auth.RoleManager)

	read := s.makeKey(t, kc, "read only", "read", 0)
	if rec := s.withKey(keyReq{method: "GET", path: "/api/v1/requests", key: read.Key}); rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
		t.Errorf("read-only GET: HTTP %d", rec.Code)
	}
	rec := s.withKey(keyReq{method: "POST", path: "/api/v1/requests", body: `{"media_type":"movie","tmdb_id":1}`, key: read.Key})
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "read-only") {
		t.Errorf("read-only POST: HTTP %d %s, want 403", rec.Code, rec.Body)
	}

	// A requester's full key still can't do staff things.
	full := s.makeKey(t, kc, "full", "full", 0)
	if rec := s.withKey(keyReq{method: "POST", path: "/api/v1/requests/1/approve", body: `{}`, key: full.Key}); rec.Code != http.StatusForbidden {
		t.Errorf("requester key approving: HTTP %d, want 403", rec.Code)
	}
	// Promoted after the key was made: the key stays a requester's.
	if err := s.auth.UpdateUser(ctx, kid.ID, auth.RoleManager, auth.AutoApproval{}); err != nil {
		t.Fatal(err)
	}
	if rec := s.withKey(keyReq{method: "POST", path: "/api/v1/requests/1/approve", body: `{}`, key: full.Key}); rec.Code != http.StatusForbidden {
		t.Errorf("promotion widened the key: HTTP %d, want 403", rec.Code)
	}
	if rec := s.withKey(keyReq{method: "GET", path: "/api/v1/auth/me", key: full.Key}); !strings.Contains(rec.Body.String(), `"role":"requester"`) {
		t.Errorf("a promoted account's old key acts as %s", rec.Body)
	}

	// A manager's key approves (the route lets it through; there's no request 1)…
	mk := s.makeKey(t, mc, "staff", "full", 0)
	if rec := s.withKey(keyReq{method: "POST", path: "/api/v1/requests/1/approve", body: `{}`, key: mk.Key}); rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
		t.Errorf("manager key approving: HTTP %d %s", rec.Code, rec.Body)
	}
	// …but still can't use a staff route that isn't open to keys.
	if rec := s.withKey(keyReq{method: "GET", path: "/api/v1/queue", key: mk.Key}); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "API keys can't") {
		t.Errorf("manager key on the queue: HTTP %d %s, want 403", rec.Code, rec.Body)
	}
	// Demoted: the key follows at once.
	if err := s.auth.UpdateUser(ctx, mgr.ID, auth.RoleRequester, auth.AutoApproval{}); err != nil {
		t.Fatal(err)
	}
	if rec := s.withKey(keyReq{method: "POST", path: "/api/v1/requests/1/approve", body: `{}`, key: mk.Key}); rec.Code != http.StatusForbidden {
		t.Errorf("demoted manager's key approving: HTTP %d, want 403", rec.Code)
	}
}

// Disabled, deleted or expired: the key stops working on the next request.
func TestAPIKeyStopsWithItsAccount(t *testing.T) {
	s := newRouteServer(t, nil)
	ctx := context.Background()
	u, c := s.user(t, "kid@example.com", auth.RoleRequester)
	m := s.makeKey(t, c, "n8n", "full", 30)
	ok := func(want int, why string) {
		t.Helper()
		if rec := s.withKey(keyReq{method: "GET", path: "/api/v1/auth/me", key: m.Key}); rec.Code != want {
			t.Errorf("%s: HTTP %d, want %d", why, rec.Code, want)
		}
	}
	ok(http.StatusOK, "fresh")
	if err := s.auth.SetDisabled(ctx, u.ID, true); err != nil {
		t.Fatal(err)
	}
	ok(http.StatusUnauthorized, "disabled")
	if err := s.auth.SetDisabled(ctx, u.ID, false); err != nil {
		t.Fatal(err)
	}
	ok(http.StatusOK, "enabled again")
	// Disabling signed the browser out; sign in again.
	tok, _, _ := s.auth.CreateSession(ctx, u.ID)
	c = &http.Cookie{Name: sessionCookieName, Value: tok}
	if _, err := s.st.DB().Exec(`UPDATE api_keys SET expires_at = '2001-01-01 00:00:00' WHERE id = ?`, m.APIKey.ID); err != nil {
		t.Fatal(err)
	}
	ok(http.StatusUnauthorized, "expired")
	var listed struct{ Keys []auth.APIKey }
	_ = json.Unmarshal(s.do("GET", "/api/v1/me/api-keys", c).Body.Bytes(), &listed)
	if len(listed.Keys) != 1 || !listed.Keys[0].Expired {
		t.Errorf("an expired key should be listed as expired: %+v", listed.Keys)
	}

	m2 := s.makeKey(t, c, "another", "full", 0)
	if err := s.auth.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if rec := s.withKey(keyReq{method: "GET", path: "/api/v1/auth/me", key: m2.Key}); rec.Code != http.StatusUnauthorized {
		t.Errorf("deleted account's key: HTTP %d, want 401", rec.Code)
	}
	if n := countKeys(t, s, u.ID); n != 0 {
		t.Errorf("%d keys outlived their account", n)
	}
}

// Wrong keys are throttled per address like wrong passwords, on their own count: ten in
// fifteen minutes, then even a right key from there waits — and that address can still
// sign in, and other addresses aren't affected.
func TestAPIKeyRateLimit(t *testing.T) {
	s := newRouteServer(t, nil)
	_, c := s.user(t, "kid@example.com", auth.RoleRequester)
	m := s.makeKey(t, c, "good", "read", 0)
	const attacker = "203.0.113.9"
	for i := 0; i < 10; i++ {
		bad := "arrk_" + strings.Repeat(string(rune('a'+i)), 52)
		if rec := s.withKey(keyReq{method: "GET", path: "/api/v1/auth/me", key: bad, ip: attacker}); rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong key %d: HTTP %d", i, rec.Code)
		}
	}
	rec := s.withKey(keyReq{method: "GET", path: "/api/v1/auth/me", key: m.Key, ip: attacker})
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("after ten wrong keys: HTTP %d, want 429 with Retry-After", rec.Code)
	}
	if rec := s.withKey(keyReq{method: "GET", path: "/api/v1/auth/me", key: m.Key, ip: "198.51.100.4"}); rec.Code != http.StatusOK {
		t.Errorf("another address: HTTP %d", rec.Code)
	}
	if rec := s.doFrom("POST", "/api/v1/auth/login", `{"username":"kid@example.com","password":"password123"}`, attacker); rec.Code != http.StatusOK {
		t.Errorf("signing in from the throttled address: HTTP %d %s", rec.Code, rec.Body)
	}
}

// Other things in the same headers — an audiobook app's bearer token, another service's
// key — aren't personal API keys: they're left alone, not refused or counted.
func TestOtherBearerTokensAreLeftAlone(t *testing.T) {
	s := newRouteServer(t, nil)
	for i := 0; i < 15; i++ {
		rec := s.withKey(keyReq{method: "GET", path: "/api/v1/status", key: "eyJhbGciOiJIUzI1NiJ9.audio-app-token", bearer: true, ip: "203.0.113.9"})
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d with a foreign bearer token: HTTP %d", i, rec.Code)
		}
	}
	if rec := s.withKey(keyReq{method: "GET", path: "/api/v1/status", key: "widget-key-123"}); rec.Code != http.StatusOK {
		t.Errorf("a foreign X-Api-Key: HTTP %d", rec.Code)
	}
}

// The admin sees everyone's keys (whose, never from where) and can revoke any; nobody else
// can see that list.
func TestAdminListsAndRevokesKeys(t *testing.T) {
	s := newRouteServer(t, nil)
	_, kc := s.user(t, "kid@example.com", auth.RoleRequester)
	_, mc := s.user(t, "mgr@example.com", auth.RoleManager)
	_, ac := s.user(t, "owner", auth.RoleAdmin)
	m := s.makeKey(t, kc, "home assistant", "read", 0)
	s.withKey(keyReq{method: "GET", path: "/api/v1/auth/me", key: m.Key, ip: "203.0.113.9"})

	for _, c := range []*http.Cookie{kc, mc} {
		if rec := s.do("GET", "/api/v1/users/api-keys", c); rec.Code != http.StatusForbidden {
			t.Errorf("non-admin listing everyone's keys: HTTP %d", rec.Code)
		}
	}
	rec := s.do("GET", "/api/v1/users/api-keys", ac)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `"username":"kid@example.com"`) || !strings.Contains(body, `"name":"home assistant"`) {
		t.Fatalf("admin list: HTTP %d %s", rec.Code, body)
	}
	if strings.Contains(body, "203.0.113") || strings.Contains(body, "last_used_network") || strings.Contains(body, m.Key) || strings.Contains(body, sha(m.Key)) {
		t.Errorf("admin list shows where from, or the key: %s", body)
	}
	// An admin's key can't reach the admin's list either.
	ak := s.makeKey(t, ac, "admin script", "full", 0)
	if rec := s.withKey(keyReq{method: "GET", path: "/api/v1/users/api-keys", key: ak.Key}); rec.Code != http.StatusForbidden {
		t.Errorf("admin key on the key list: HTTP %d, want 403", rec.Code)
	}
	if rec := s.withKey(keyReq{method: "GET", path: "/api/v1/system/support-bundle", key: ak.Key}); rec.Code != http.StatusForbidden {
		t.Errorf("admin key on the support bundle: HTTP %d, want 403", rec.Code)
	}
	if rec := s.withKey(keyReq{method: "GET", path: "/api/v1/apikeys", key: ak.Key}); rec.Code != http.StatusForbidden {
		t.Errorf("admin key on outside services' keys: HTTP %d, want 403", rec.Code)
	}
	// The requester can't revoke through the admin route; the admin can.
	id := strconv.FormatInt(m.APIKey.ID, 10)
	if rec := s.do("DELETE", "/api/v1/users/api-keys/"+id, kc); rec.Code != http.StatusForbidden {
		t.Errorf("requester on the admin revoke: HTTP %d", rec.Code)
	}
	// Nor through their own route for someone else's key.
	if rec := s.do("DELETE", "/api/v1/me/api-keys/"+strconv.FormatInt(ak.APIKey.ID, 10), kc); rec.Code != http.StatusNotFound {
		t.Errorf("revoking another account's key as your own: HTTP %d, want 404", rec.Code)
	}
	if rec := s.do("DELETE", "/api/v1/users/api-keys/"+id, ac); rec.Code != http.StatusNoContent {
		t.Fatalf("admin revoke: HTTP %d %s", rec.Code, rec.Body)
	}
	if rec := s.withKey(keyReq{method: "GET", path: "/api/v1/auth/me", key: m.Key}); rec.Code != http.StatusUnauthorized {
		t.Errorf("admin-revoked key: HTTP %d, want 401", rec.Code)
	}
}

// A requester's key sees exactly what their session sees: requests are shaped the same
// way (their own and followed ones, nobody else's).
func TestRequesterKeySeesWhatTheSessionSees(t *testing.T) {
	s := bookCardServer(t)
	kid, kc := s.user(t, "kid@example.com", auth.RoleRequester)
	aunt, _ := s.user(t, "aunt@example.com", auth.RoleRequester)
	for _, q := range []struct {
		title string
		by    int64
	}{{"Kid's Movie", kid.ID}, {"Aunt's Secret Movie", aunt.ID}} {
		if _, err := s.st.DB().Exec(`INSERT INTO requests (media_type, tmdb_id, title, status, requested_by, requested_by_name, note) VALUES ('movie', ?, ?, 'pending', ?, 'x', 'private note')`,
			len(q.title), q.title, q.by); err != nil {
			t.Fatal(err)
		}
	}
	m := s.makeKey(t, kc, "script", "read", 0)
	byKey := s.withKey(keyReq{method: "GET", path: "/api/v1/requests", key: m.Key})
	bySession := s.do("GET", "/api/v1/requests", kc)
	if byKey.Code != http.StatusOK || byKey.Body.String() != bySession.Body.String() {
		t.Fatalf("key and session differ:\nkey     %d %s\nsession %d %s", byKey.Code, byKey.Body, bySession.Code, bySession.Body)
	}
	if strings.Contains(byKey.Body.String(), "Aunt's Secret Movie") || !strings.Contains(byKey.Body.String(), "Kid's Movie") {
		t.Errorf("requester key list: %s", byKey.Body)
	}
}

// A Plex-only account (no password anyone knows) makes keys on its session alone.
func TestPlexOnlyAccountMakesKeysWithoutPassword(t *testing.T) {
	s := newRouteServer(t, nil)
	ctx := context.Background()
	u, err := s.auth.FindOrCreatePlexUser(ctx, "4242", "kidplex", auth.RoleRequester, auth.AutoApproval{})
	if err != nil {
		t.Fatal(err)
	}
	tok, _, _ := s.auth.CreateSession(ctx, u.ID)
	c := &http.Cookie{Name: sessionCookieName, Value: tok}
	if rec := s.do("GET", "/api/v1/me/api-keys", c); !strings.Contains(rec.Body.String(), `"password_required":false`) {
		t.Errorf("list: %s", rec.Body)
	}
	if rec := s.doJSON("POST", "/api/v1/me/api-keys", c, `{"name":"x","scope":"read","expires_in_days":0}`); rec.Code != http.StatusCreated {
		t.Errorf("create: HTTP %d %s", rec.Code, rec.Body)
	}
}

// Neither a key nor its hash turns up in the log or the support bundle, and a key some
// other tool wrote into a log line is blanked there.
func TestAPIKeysStayOutOfLogsAndBundle(t *testing.T) {
	f := newLogFixture(t, nil, nil)
	_, kc := f.user(t, "kid@example.com", auth.RoleRequester)
	m := f.makeKey(t, kc, "n8n", "full", 0)
	f.withKey(keyReq{method: "GET", path: "/api/v1/auth/me", key: m.Key})
	f.withKey(keyReq{method: "GET", path: "/api/v1/me/api-keys", key: m.Key})                         // refused: logged as a 403
	f.withKey(keyReq{method: "GET", path: "/api/v1/auth/me", key: "arrk_" + strings.Repeat("q", 52)}) // a wrong key
	f.do("GET", "/api/v1/auth/me?apikey="+m.Key, nil)
	f.log.Warn("n8n: workflow failed calling with key " + m.Key)
	hash := sha(m.Key)

	for _, e := range f.ring.Snapshot(applog.Filter{Min: slog.LevelDebug}) {
		line := e.Message + " " + e.Attrs
		if strings.Contains(line, hash) {
			t.Errorf("log line has the key's hash: %s", line)
		}
		if strings.Contains(line, m.Key) && !strings.Contains(e.Message, "n8n: workflow failed") {
			t.Errorf("log line has the key: %s", line)
		}
	}

	rec := f.do("GET", "/api/v1/system/support-bundle", f.admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("bundle: HTTP %d %s", rec.Code, rec.Body)
	}
	for name, body := range readZip(t, rec.Body.Bytes()) {
		if strings.Contains(body, m.Key) || strings.Contains(body, m.Key[5:]) || strings.Contains(body, hash) {
			t.Errorf("%s in the support bundle carries the key or its hash", name)
		}
	}
}
