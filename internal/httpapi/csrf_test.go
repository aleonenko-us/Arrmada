package httpapi

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
)

// csrfReq is a request to the full handler from a LAN address with the given headers.
func (s *routeServer) csrfReq(method, path, body string, c *http.Cookie, remote string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://arrmada.local:7878"+path, strings.NewReader(body))
	r.RemoteAddr = remote
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

const newAdminBody = `{"email":"evil@example.com","password":"long-enough-1","role":"admin"}`

// A page on another port of the same host (qBittorrent, Jellyfin) or a sibling subdomain
// is "same-site": the browser sends the Lax session cookie with its form posts. Such a
// post must not create an admin, start an action or sign anyone in.
func TestCrossSiteFormPostsAreRefused(t *testing.T) {
	s := newRouteServer(t, nil)
	ctx := context.Background()
	_, admin := s.user(t, "owner@example.com", auth.RoleAdmin)
	const lan = "192.168.1.20:5000"
	textPlain := "text/plain"
	for _, c := range []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"Sec-Fetch-Site same-site (another port)", map[string]string{"Sec-Fetch-Site": "same-site", "Content-Type": textPlain}, http.StatusForbidden},
		{"Sec-Fetch-Site cross-site", map[string]string{"Sec-Fetch-Site": "cross-site", "Content-Type": "application/json"}, http.StatusForbidden},
		{"old browser, Origin on another port", map[string]string{"Origin": "http://arrmada.local:8080", "Content-Type": textPlain}, http.StatusForbidden},
		{"old browser, Origin a sibling subdomain", map[string]string{"Origin": "https://qbit.arrmada.local", "Content-Type": "application/json"}, http.StatusForbidden},
		{"Origin null (sandboxed page)", map[string]string{"Origin": "null", "Content-Type": "application/json"}, http.StatusForbidden},
		{"no headers, but a form's body type", map[string]string{"Content-Type": textPlain}, http.StatusUnsupportedMediaType},
		{"no headers, urlencoded form", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, http.StatusUnsupportedMediaType},
		{"no headers, no type at all", nil, http.StatusUnsupportedMediaType},
		{"same origin, but multipart on a JSON route", map[string]string{"Sec-Fetch-Site": "same-origin", "Content-Type": "multipart/form-data; boundary=x"}, http.StatusUnsupportedMediaType},
	} {
		if rec := s.csrfReq("POST", "/api/v1/users", newAdminBody, admin, lan, c.hdr); rec.Code != c.want {
			t.Errorf("%s: HTTP %d %s, want %d", c.name, rec.Code, rec.Body, c.want)
		}
	}
	if u, _ := s.auth.UserByUsername(ctx, "evil@example.com"); u != nil {
		t.Fatal("a cross-site post created an account")
	}

	// A body-less post (start a search, sign out) is refused the same way.
	if rec := s.csrfReq("POST", "/api/v1/system/restart", "", admin, lan, map[string]string{"Sec-Fetch-Site": "same-site"}); rec.Code != http.StatusForbidden {
		t.Errorf("body-less cross-site post: HTTP %d", rec.Code)
	}
	if rec := s.csrfReq("DELETE", "/api/v1/users/1", "", admin, lan, map[string]string{"Origin": "http://arrmada.local:8080"}); rec.Code != http.StatusForbidden {
		t.Errorf("cross-site delete: HTTP %d", rec.Code)
	}

	// Login CSRF: signing a visitor into the attacker's account is refused too.
	if _, err := s.auth.CreateUser(ctx, "attacker@example.com", "password123", auth.RoleRequester, false); err != nil {
		t.Fatal(err)
	}
	rec := s.csrfReq("POST", "/api/v1/auth/login", `{"username":"attacker@example.com","password":"password123"}`, nil, lan,
		map[string]string{"Sec-Fetch-Site": "same-site", "Content-Type": textPlain})
	if rec.Code != http.StatusForbidden || cookieNamed(rec, sessionCookieName) != nil {
		t.Errorf("cross-site sign-in: HTTP %d, cookie %v", rec.Code, cookieNamed(rec, sessionCookieName))
	}
}

// Arrmada's own pages, however they're reached, still work: the browser's same-origin
// verdict, an Origin naming the host it addressed (directly or through a proxy that
// rewrites Host), and clients that send neither header.
func TestSameOriginRequestsPass(t *testing.T) {
	s := newRouteServer(t, nil)
	_, admin := s.user(t, "owner@example.com", auth.RoleAdmin)
	const lan = "192.168.1.20:5000"
	n := 0
	create := func(remote string, hdr map[string]string) int {
		n++
		body := strings.Replace(newAdminBody, "evil@", "ok"+string(rune('a'+n))+"@", 1)
		return s.csrfReq("POST", "/api/v1/users", body, admin, remote, hdr).Code
	}
	for _, c := range []struct {
		name   string
		remote string
		hdr    map[string]string
	}{
		{"Sec-Fetch-Site same-origin", lan, map[string]string{"Sec-Fetch-Site": "same-origin", "Content-Type": "application/json"}},
		{"Sec-Fetch-Site none (typed by the person)", lan, map[string]string{"Sec-Fetch-Site": "none", "Content-Type": "application/json; charset=utf-8"}},
		{"Origin matches Host", lan, map[string]string{"Origin": "http://arrmada.local:7878", "Content-Type": "application/json"}},
		{"Origin matches X-Forwarded-Host from a local proxy", "127.0.0.1:5000", map[string]string{"Origin": "https://arrmada.example.org", "X-Forwarded-Host": "arrmada.example.org", "Content-Type": "application/json"}},
		{"Origin with the scheme's default port", "127.0.0.1:5000", map[string]string{"Origin": "https://arrmada.example.org", "X-Forwarded-Host": "arrmada.example.org:443", "Content-Type": "application/json"}},
		{"neither header (a script with a cookie)", lan, map[string]string{"Content-Type": "application/json"}},
	} {
		if code := create(c.remote, c.hdr); code != http.StatusCreated {
			t.Errorf("%s: HTTP %d, want 201", c.name, code)
		}
	}
	// X-Forwarded-Host only counts from a local proxy.
	if code := create("203.0.113.9:5000", map[string]string{"Origin": "https://evil.example", "X-Forwarded-Host": "evil.example", "Content-Type": "application/json"}); code != http.StatusForbidden {
		t.Errorf("X-Forwarded-Host from a public peer: HTTP %d, want 403", code)
	}
	// Body-less posts need no type.
	if rec := s.csrfReq("POST", "/api/v1/auth/logout", "", nil, lan, map[string]string{"Sec-Fetch-Site": "same-origin"}); rec.Code != http.StatusOK {
		t.Errorf("same-origin sign-out: HTTP %d", rec.Code)
	}
	// Reading is never checked.
	if rec := s.csrfReq("GET", "/api/v1/auth/me", "", admin, lan, map[string]string{"Sec-Fetch-Site": "cross-site"}); rec.Code != http.StatusOK {
		t.Errorf("GET from anywhere: HTTP %d", rec.Code)
	}
}

// Upload routes take multipart from Arrmada's pages; everywhere else it's JSON only.
func TestUploadRoutesTakeMultipart(t *testing.T) {
	s := newRouteServer(t, nil)
	_, admin := s.user(t, "owner@example.com", auth.RoleAdmin)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "cover.txt") // refused by the handler before any service is touched
	_, _ = fw.Write([]byte("png"))
	_ = mw.Close()
	rec := s.csrfReq("POST", "/api/v1/books/1/cover", buf.String(), admin, "192.168.1.20:5000",
		map[string]string{"Sec-Fetch-Site": "same-origin", "Content-Type": mw.FormDataContentType()})
	if rec.Code == http.StatusUnsupportedMediaType || rec.Code == http.StatusForbidden {
		t.Errorf("multipart cover upload: HTTP %d %s", rec.Code, rec.Body)
	}
	rec = s.csrfReq("POST", "/api/v1/books/1/cover", buf.String(), admin, "192.168.1.20:5000",
		map[string]string{"Sec-Fetch-Site": "same-site", "Content-Type": mw.FormDataContentType()})
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site multipart upload: HTTP %d, want 403", rec.Code)
	}
}

// A personal API key comes in a header no form can set, so its requests skip both checks:
// scripts keep working whatever they send.
func TestAPIKeyRequestsSkipTheSameOriginChecks(t *testing.T) {
	s := newRouteServer(t, nil)
	_, c := s.user(t, "kid@example.com", auth.RoleRequester)
	m := s.makeKey(t, c, "script", "full", 0)
	r := httptest.NewRequest("POST", "http://arrmada.local/api/v1/requests", strings.NewReader(`{}`))
	r.RemoteAddr = "192.168.1.20:5000"
	r.Header.Set("X-Api-Key", m.Key)
	r.Header.Set("Content-Type", "text/plain")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, r)
	if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnsupportedMediaType {
		t.Errorf("API key request: HTTP %d %s — the same-origin checks applied", rec.Code, rec.Body)
	}
}

func TestSameHost(t *testing.T) {
	for _, c := range []struct {
		origin, host string
		want         bool
	}{
		{"https://a.example", "a.example", true},
		{"https://a.example", "a.example:443", true},
		{"https://a.example", "A.EXAMPLE", true},
		{"http://a.example:7878", "a.example:7878", true},
		{"http://a.example", "a.example:80", true},
		{"http://a.example", "a.example:8080", false},
		{"https://a.example", "a.example:80", false},
		{"http://[::1]:7878", "[::1]:7878", true},
		{"http://b.a.example", "a.example", false},
	} {
		u, _ := url.Parse(c.origin)
		if got := sameHost(u, c.host); got != c.want {
			t.Errorf("sameHost(%s, %s) = %v, want %v", c.origin, c.host, got, c.want)
		}
	}
}
