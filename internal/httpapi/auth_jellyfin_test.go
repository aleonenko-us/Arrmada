package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
)

const lan = "192.168.1.20"

func jfLogin(s *routeServer, ip, user, pw string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"username": user, "password": pw})
	return s.doFrom("POST", "/api/v1/auth/jellyfin", string(body), ip)
}

func signedInAs(t *testing.T, rec *httptest.ResponseRecorder) *auth.User {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("sign-in: HTTP %d %s", rec.Code, rec.Body)
	}
	if cookieNamed(rec, sessionCookieName) == nil {
		t.Fatal("no session cookie")
	}
	var out struct {
		User auth.User `json:"user"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return &out.User
}

func noSession(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if cookieNamed(rec, sessionCookieName) != nil {
		t.Errorf("a session cookie was set (HTTP %d %s)", rec.Code, rec.Body)
	}
}

// A first Jellyfin sign-in makes a requester (movies auto-approved by default) linked to
// the Jellyfin user; the next finds the same account. The sign-in call never carries the
// API key, and the session it opens in Jellyfin is logged out at once.
func TestJellyfinSignInCreatesRequester(t *testing.T) {
	s, f, _ := jellyfinLoginServer(t)
	ctx := context.Background()
	if rec := s.do("GET", "/api/v1/status", nil); !strings.Contains(rec.Body.String(), `"jellyfin_login":true`) || !strings.Contains(rec.Body.String(), `"jellyfin_quickconnect":true`) {
		t.Errorf("status doesn't offer Jellyfin sign-in: %s", rec.Body)
	}
	u := signedInAs(t, jfLogin(s, lan, "Kid", "kid-password-1"))
	if u.Role != auth.RoleRequester || !u.JellyfinLinked || u.JellyfinUsername != "kid" || !u.AutoApproveMovie || u.AutoApproveSeries {
		t.Errorf("new account = %+v", u)
	}
	again := signedInAs(t, jfLogin(s, lan, "kid", "kid-password-1"))
	if again.ID != u.ID {
		t.Errorf("second sign-in made account %d, want %d", again.ID, u.ID)
	}
	if got := s.auth.JellyfinIDForUser(ctx, u.ID); got != "jf-kid" {
		t.Errorf("jellyfin id = %q", got)
	}
	if auths, _, keyed := f.counts(); auths != 2 || keyed != 0 {
		t.Errorf("auth calls = %d, with a key = %d (want 2, 0)", auths, keyed)
	}
	if n := f.live(); n != 0 {
		t.Errorf("%d Jellyfin sessions left signed in", n)
	}
	// The owner's choice of types applies to the next new account.
	_ = s.deps.Settings.Set(ctx, keyJellyfinAutoApproveTypes, "series")
	f.user("mum", "mum-password-1", "jf-mum", false, false)
	if m := signedInAs(t, jfLogin(s, lan, "mum", "mum-password-1")); m.AutoApproveMovie || !m.AutoApproveSeries {
		t.Errorf("auto-approve = %+v, want series only", m)
	}
}

// Off, or with no Jellyfin connected, the route refuses and /status hides the button.
func TestJellyfinSignInOff(t *testing.T) {
	s, f, _ := jellyfinLoginServer(t)
	ctx := context.Background()
	_ = s.deps.Settings.SetBool(ctx, keyJellyfinLoginEnabled, false)
	if rec := jfLogin(s, lan, "kid", "kid-password-1"); rec.Code != http.StatusForbidden {
		t.Errorf("off: HTTP %d, want 403", rec.Code)
	}
	if rec := s.do("GET", "/api/v1/status", nil); !strings.Contains(rec.Body.String(), `"jellyfin_login":false`) {
		t.Errorf("status = %s", rec.Body)
	}
	_ = s.deps.Settings.SetBool(ctx, keyJellyfinLoginEnabled, true)
	_ = s.deps.Settings.SetBool(ctx, keyJellyfinQuickConnect, false)
	if rec := s.do("GET", "/api/v1/status", nil); !strings.Contains(rec.Body.String(), `"jellyfin_quickconnect":false`) {
		t.Errorf("status with Quick Connect off = %s", rec.Body)
	}
	if rec := s.doFrom("POST", "/api/v1/auth/jellyfin/quickconnect", "", lan); rec.Code != http.StatusForbidden {
		t.Errorf("Quick Connect off: HTTP %d, want 403", rec.Code)
	}
	if auths, _, _ := f.counts(); auths != 0 {
		t.Errorf("Jellyfin was asked %d times while sign-in was off", auths)
	}
	// No server saved: nothing to sign in against.
	_ = s.deps.Settings.Set(ctx, "jellyfin_url", "")
	if rec := s.do("GET", "/api/v1/status", nil); !strings.Contains(rec.Body.String(), `"jellyfin_login":false`) {
		t.Errorf("status without a server = %s", rec.Body)
	}
}

// A Jellyfin administrator is never made a requester. Linked to a staff account, they get
// in only when the owner allowed staff Jellyfin sign-in.
func TestJellyfinAdminNotMadeRequester(t *testing.T) {
	s, _, _ := jellyfinLoginServer(t)
	ctx := context.Background()
	before, _ := s.auth.UserCount(ctx)
	rec := jfLogin(s, lan, "boss", "boss-password-1")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "administrator") {
		t.Fatalf("unlinked Jellyfin admin: HTTP %d %s, want 409", rec.Code, rec.Body)
	}
	noSession(t, rec)
	if after, _ := s.auth.UserCount(ctx); after != before {
		t.Errorf("an account was made for a Jellyfin admin (%d → %d)", before, after)
	}

	admin, _ := s.user(t, "owner@example.com", auth.RoleAdmin)
	if err := s.auth.LinkJellyfin(ctx, admin.ID, "jf-boss", "boss"); err != nil {
		t.Fatal(err)
	}
	rec = jfLogin(s, lan, "boss", "boss-password-1")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "staff account") {
		t.Fatalf("staff sign-in off: HTTP %d %s, want 403", rec.Code, rec.Body)
	}
	noSession(t, rec)
	_ = s.deps.Settings.SetBool(ctx, keyJellyfinSignInStaff, true)
	if u := signedInAs(t, jfLogin(s, lan, "boss", "boss-password-1")); u.ID != admin.ID {
		t.Errorf("staff sign-in landed in %d, want the admin %d", u.ID, admin.ID)
	}
}

// Disabled in Jellyfin, blocked here, or a disabled account: refused, no session.
func TestJellyfinSignInRefusals(t *testing.T) {
	s, f, _ := jellyfinLoginServer(t)
	ctx := context.Background()
	f.user("gone", "gone-password-1", "jf-gone", false, true)
	rec := jfLogin(s, lan, "gone", "gone-password-1")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "disabled in Jellyfin") {
		t.Errorf("disabled in Jellyfin: HTTP %d %s", rec.Code, rec.Body)
	}
	noSession(t, rec)
	if u, _ := s.auth.UserByJellyfinID(ctx, "jf-gone"); u != nil {
		t.Error("an account was made for a disabled Jellyfin user")
	}

	if _, err := s.newJellyfinBlock(ctx, "jf-kid"); err != nil {
		t.Fatal(err)
	}
	rec = jfLogin(s, lan, "kid", "kid-password-1")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "isn't allowed") {
		t.Errorf("blocked: HTTP %d %s", rec.Code, rec.Body)
	}
	noSession(t, rec)
	if u, _ := s.auth.UserByJellyfinID(ctx, "jf-kid"); u != nil {
		t.Error("an account was made for a blocked Jellyfin user")
	}

	f.user("mum", "mum-password-1", "jf-mum", false, false)
	mum := signedInAs(t, jfLogin(s, lan, "mum", "mum-password-1"))
	if err := s.auth.SetDisabled(ctx, mum.ID, true); err != nil {
		t.Fatal(err)
	}
	rec = jfLogin(s, lan, "mum", "mum-password-1")
	if rec.Code != http.StatusForbidden {
		t.Errorf("disabled account: HTTP %d, want 403", rec.Code)
	}
	noSession(t, rec)

	// No password is never enough, even for a Jellyfin user that has none.
	if rec := jfLogin(s, lan, "kid", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("blank password: HTTP %d, want 400", rec.Code)
	}
}

func (s *routeServer) newJellyfinBlock(ctx context.Context, id string) (bool, error) {
	return (&api{deps: s.deps}).addJellyfinBlock(ctx, id, "someone")
}

// Wrong passwords are limited per username from an address: after five, that pair waits
// (without Jellyfin being asked) while the same name from elsewhere on the LAN, and other
// names from the same address, still get their tries.
func TestJellyfinSignInThrottle(t *testing.T) {
	s, f, _ := jellyfinLoginServer(t)
	const a, b = "192.168.1.30", "192.168.1.31"
	for i := 0; i < 5; i++ {
		if rec := jfLogin(s, a, "kid", "wrong"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("try %d: HTTP %d %s, want 401", i+1, rec.Code, rec.Body)
		}
	}
	auths, _, _ := f.counts()
	rec := jfLogin(s, a, "kid", "kid-password-1")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("6th try: HTTP %d, want 429 with Retry-After", rec.Code)
	}
	if now, _, _ := f.counts(); now != auths {
		t.Error("Jellyfin was asked while the username was paused")
	}
	if rec := jfLogin(s, b, "kid", "kid-password-1"); rec.Code != http.StatusOK {
		t.Errorf("same name, another LAN address: HTTP %d, want 200", rec.Code)
	}
	f.user("mum", "mum-password-1", "jf-mum", false, false)
	if rec := jfLogin(s, a, "mum", "mum-password-1"); rec.Code != http.StatusOK {
		t.Errorf("another name, same address: HTTP %d, want 200", rec.Code)
	}
	// Ten wrong passwords from one address shut it for every name.
	for i := 0; i < 5; i++ {
		jfLogin(s, a, "nobody-"+string(rune('a'+i)), "wrong")
	}
	if rec := jfLogin(s, a, "mum", "mum-password-1"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("after 10 failures from one address: HTTP %d, want 429", rec.Code)
	}
	// From outside the LAN the username itself pauses after five, whatever the address.
	for i := 0; i < 5; i++ {
		jfLogin(s, "203.0.113."+string(rune('1'+i)), "boss", "wrong")
	}
	if rec := jfLogin(s, "203.0.113.9", "boss", "boss-password-1"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("external, 6th address: HTTP %d, want 429", rec.Code)
	}
}

// The password goes to Jellyfin and nowhere else: not into a log line, an answer or a
// cookie, whether it's right, wrong, or the server answers with an error echoing it.
func TestJellyfinPasswordNeverLogged(t *testing.T) {
	s, f, logs := jellyfinLoginServer(t)
	const pw = "kid-password-1"
	var bodies []string
	keep := func(rec *httptest.ResponseRecorder) {
		bodies = append(bodies, rec.Body.String())
		for _, c := range rec.Result().Cookies() {
			bodies = append(bodies, c.Value)
		}
	}
	keep(jfLogin(s, lan, "kid", pw+"-wrong"))
	keep(jfLogin(s, lan, "kid", pw))
	_, cookie := s.user(t, "mum@example.com", auth.RoleRequester)
	keep(s.doFrom("POST", "/api/v1/me/jellyfin/link", `{"username":"kid","password":"`+pw+`"}`, lan, cookie))
	f.mu.Lock()
	f.failWith = http.StatusInternalServerError
	f.mu.Unlock()
	rec := jfLogin(s, lan, "kid", pw)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("server error: HTTP %d, want 502", rec.Code)
	}
	keep(rec)
	f.srv.Close()
	keep(jfLogin(s, lan, "kid", pw))
	if !strings.Contains(logs.String(), "Jellyfin sign-in call failed") {
		t.Fatalf("the failures weren't logged at all:\n%s", logs)
	}
	for _, secret := range []string{pw, pw + "-wrong"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("the log holds a password:\n%s", logs)
		}
		for _, b := range bodies {
			if strings.Contains(b, secret) {
				t.Errorf("an answer holds a password: %s", b)
			}
		}
	}
}

// Quick Connect: the code goes to the browser, the secret stays on the server, only the
// browser that started it can collect it, and an approved secret signs in exactly once.
func TestJellyfinQuickConnectSignIn(t *testing.T) {
	s, f, _ := jellyfinLoginServer(t)
	start := func() (string, string, *http.Cookie) {
		rec := s.doFrom("POST", "/api/v1/auth/jellyfin/quickconnect", "", lan)
		if rec.Code != http.StatusOK {
			t.Fatalf("start: HTTP %d %s", rec.Code, rec.Body)
		}
		var out struct{ ID, Code string }
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		c := cookieNamed(rec, jfQCCookieName(jfFlowLogin))
		if out.ID == "" || out.Code == "" || c == nil {
			t.Fatalf("start = %s (cookie %v)", rec.Body, c)
		}
		for _, sec := range f.secrets() {
			if strings.Contains(rec.Body.String(), sec) || strings.Contains(c.Value, sec) {
				t.Fatal("the Quick Connect secret reached the browser")
			}
		}
		return out.ID, out.Code, c
	}
	id, code, mine := start()
	_, _, other := start() // another browser's cookie
	poll := "/api/v1/auth/jellyfin/quickconnect/" + id
	if rec := s.doFrom("GET", poll, "", lan); rec.Code != http.StatusForbidden {
		t.Errorf("no cookie: HTTP %d, want 403", rec.Code)
	}
	if rec := s.doFrom("GET", poll, "", lan, other); rec.Code != http.StatusForbidden {
		t.Errorf("another browser's cookie: HTTP %d, want 403", rec.Code)
	}
	if rec := s.doFrom("GET", poll, "", lan, mine); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"pending":true`) {
		t.Fatalf("pending: HTTP %d %s", rec.Code, rec.Body)
	}
	if !f.approve(code, "kid") {
		t.Fatal("code unknown to the fake")
	}
	// The other browser still can't take it once approved.
	if rec := s.doFrom("GET", poll, "", lan, other); rec.Code != http.StatusForbidden {
		t.Errorf("another browser after approval: HTTP %d, want 403", rec.Code)
	}
	u := signedInAs(t, s.doFrom("GET", poll, "", lan, mine))
	if u.JellyfinUsername != "kid" || u.Role != auth.RoleRequester {
		t.Errorf("signed in as %+v", u)
	}
	// Used once: the same browser polling again gets nothing, and Jellyfin was asked once.
	rec := s.doFrom("GET", poll, "", lan, mine)
	if rec.Code != http.StatusForbidden {
		t.Errorf("second poll: HTTP %d %s, want 403", rec.Code, rec.Body)
	}
	noSession(t, rec)
	if _, qc, keyed := f.counts(); qc != 1 || keyed != 0 {
		t.Errorf("Quick Connect sign-ins = %d (want 1), keyed calls = %d", qc, keyed)
	}
	if n := f.live(); n != 0 {
		t.Errorf("%d Jellyfin sessions left signed in", n)
	}
}

// An expired or unknown request answers 410 and is forgotten; Quick Connect switched off in
// Jellyfin is said plainly.
func TestJellyfinQuickConnectExpiredAndOff(t *testing.T) {
	s, f, _ := jellyfinLoginServer(t)
	rec := s.doFrom("POST", "/api/v1/auth/jellyfin/quickconnect", "", lan)
	var out struct{ ID string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	c := cookieNamed(rec, jfQCCookieName(jfFlowLogin))
	f.mu.Lock()
	f.qc = map[string]*fakeQC{} // Jellyfin forgot it (expired)
	f.qcOff = true
	f.mu.Unlock()
	if rec := s.doFrom("GET", "/api/v1/auth/jellyfin/quickconnect/"+out.ID, "", lan, c); rec.Code != http.StatusGone {
		t.Errorf("expired: HTTP %d %s, want 410", rec.Code, rec.Body)
	}
	if rec := s.doFrom("GET", "/api/v1/auth/jellyfin/quickconnect/"+out.ID, "", lan, c); rec.Code != http.StatusForbidden {
		t.Errorf("after expiry: HTTP %d, want 403 (forgotten)", rec.Code)
	}
	if rec := s.doFrom("POST", "/api/v1/auth/jellyfin/quickconnect", "", lan); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "switched off in Jellyfin") {
		t.Errorf("off in Jellyfin: HTTP %d %s", rec.Code, rec.Body)
	}
}

// The QC guard: a claim succeeds once; the secret is only handed to the right browser,
// flow and account.
func TestJFQCGuard(t *testing.T) {
	g := newJFQCGuard()
	a := &api{jfQC: g}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", nil)
	id, err := a.bindJFQC(rec, r, jfFlowLink, 7, "the-secret")
	if err != nil {
		t.Fatal(err)
	}
	c := cookieNamed(rec, jfQCCookieName(jfFlowLink))
	if got := g.secretFor(id, jfFlowLink, 7, c.Value); got != "the-secret" {
		t.Errorf("owner: %q", got)
	}
	for name, got := range map[string]string{
		"other account": g.secretFor(id, jfFlowLink, 8, c.Value),
		"other flow":    g.secretFor(id, jfFlowLogin, 7, c.Value),
		"other cookie":  g.secretFor(id, jfFlowLink, 7, c.Value+"x"),
		"no cookie":     g.secretFor(id, jfFlowLink, 7, ""),
		"unknown id":    g.secretFor("nope", jfFlowLink, 7, c.Value),
	} {
		if got != "" {
			t.Errorf("%s got the secret", name)
		}
	}
	if !g.claim(id) || g.claim(id) {
		t.Error("claim should succeed exactly once")
	}
	if g.secretFor(id, jfFlowLink, 7, c.Value) != "" {
		t.Error("a claimed request still hands out its secret")
	}
}

// Linking from your account: by password or Quick Connect, bound to you; a Jellyfin user
// held elsewhere is a 409 (an admin learns the id to merge); unlinking your only way in is
// refused.
func TestMeJellyfinLink(t *testing.T) {
	s, f, _ := jellyfinLoginServer(t)
	ctx := context.Background()
	admin, adminCookie := s.user(t, "owner@example.com", auth.RoleAdmin)
	_, mumCookie := s.user(t, "mum@example.com", auth.RoleRequester)

	rec := s.doFrom("GET", "/api/v1/me/jellyfin", "", lan, adminCookie)
	if !strings.Contains(rec.Body.String(), `"linked":false`) || !strings.Contains(rec.Body.String(), `"available":true`) || !strings.Contains(rec.Body.String(), `"quickconnect":true`) {
		t.Errorf("me/jellyfin before = %s", rec.Body)
	}
	if rec := s.doFrom("POST", "/api/v1/me/jellyfin/link", `{"username":"boss","password":"nope"}`, lan, adminCookie); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong password: HTTP %d", rec.Code)
	}
	rec = s.doFrom("POST", "/api/v1/me/jellyfin/link", `{"username":"boss","password":"boss-password-1"}`, lan, adminCookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"jellyfin_username":"boss"`) {
		t.Fatalf("link: HTTP %d %s", rec.Code, rec.Body)
	}
	if s.auth.JellyfinIDForUser(ctx, admin.ID) != "jf-boss" {
		t.Error("not linked")
	}
	if rec := s.doFrom("GET", "/api/v1/me/jellyfin", "", lan, adminCookie); !strings.Contains(rec.Body.String(), `"can_unlink":true`) {
		t.Errorf("me/jellyfin after = %s", rec.Body)
	}

	// kid's Jellyfin user is held by a requester account made by sign-in.
	kid := signedInAs(t, jfLogin(s, lan, "kid", "kid-password-1"))
	rec = s.doFrom("POST", "/api/v1/me/jellyfin/link", `{"username":"kid","password":"kid-password-1"}`, lan, mumCookie)
	if rec.Code != http.StatusConflict || strings.Contains(rec.Body.String(), "user_id") {
		t.Errorf("requester conflict: HTTP %d %s (must not carry ids)", rec.Code, rec.Body)
	}
	if rec := s.doFrom("DELETE", "/api/v1/me/jellyfin/link", "", lan, adminCookie); rec.Code != http.StatusOK {
		t.Fatalf("unlink: HTTP %d", rec.Code)
	}

	// Quick Connect linking, bound to the account that started it.
	rec = s.doFrom("POST", "/api/v1/me/jellyfin/link/quickconnect", "", lan, adminCookie)
	var out struct{ ID, Code string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	qcCookie := cookieNamed(rec, jfQCCookieName(jfFlowLink))
	if rec.Code != http.StatusOK || qcCookie == nil {
		t.Fatalf("link start: HTTP %d %s", rec.Code, rec.Body)
	}
	f.approve(out.Code, "kid")
	poll := "/api/v1/me/jellyfin/link/quickconnect/" + out.ID
	if rec := s.doFrom("GET", poll, "", lan, mumCookie, qcCookie); rec.Code != http.StatusForbidden {
		t.Errorf("another account with the cookie: HTTP %d, want 403", rec.Code)
	}
	rec = s.doFrom("GET", poll, "", lan, adminCookie, qcCookie)
	var conflict map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &conflict)
	if rec.Code != http.StatusConflict || conflict["user_id"] != float64(kid.ID) || conflict["mergeable"] != true {
		t.Fatalf("admin conflict: HTTP %d %s", rec.Code, rec.Body)
	}
	// The admin merges the duplicate in through the general merge route.
	if rec := s.doFrom("GET", "/api/v1/users/"+itoa64(admin.ID)+"/merge?from="+itoa64(kid.ID), "", lan, adminCookie); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"jellyfin_link":true`) {
		t.Errorf("merge preview: HTTP %d %s", rec.Code, rec.Body)
	}

	// Jellyfin is kid's only way in: kid can't unlink it; an admin can.
	tok, _, _ := s.auth.CreateSession(ctx, kid.ID)
	kidCookie := &http.Cookie{Name: sessionCookieName, Value: tok}
	if rec := s.doFrom("DELETE", "/api/v1/me/jellyfin/link", "", lan, kidCookie); rec.Code != http.StatusConflict {
		t.Errorf("unlink the only way in: HTTP %d, want 409", rec.Code)
	}
	if rec := s.do("GET", "/api/v1/people", adminCookie); !strings.Contains(rec.Body.String(), `"jellyfin_only":true`) {
		t.Errorf("people doesn't flag the Jellyfin-only account: %s", rec.Body)
	}
	if rec := s.doBody("PUT", "/api/v1/users/"+itoa64(kid.ID), `{"jellyfin_unlink":true}`, adminCookie); rec.Code != http.StatusOK {
		t.Fatalf("admin unlink: HTTP %d %s", rec.Code, rec.Body)
	}
	if s.auth.JellyfinIDForUser(ctx, kid.ID) != "" {
		t.Error("admin unlink left the link")
	}
}

// Blocks: an admin blocks a user's Jellyfin user, the list shows it, unblock lifts it.
func TestJellyfinBlocks(t *testing.T) {
	s, _, _ := jellyfinLoginServer(t)
	_, adminCookie := s.user(t, "owner@example.com", auth.RoleAdmin)
	kid := signedInAs(t, jfLogin(s, lan, "kid", "kid-password-1"))
	if rec := s.doBody("POST", "/api/v1/users/"+itoa64(kid.ID)+"/block-jellyfin", "", adminCookie); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"jellyfin_id":"jf-kid"`) {
		t.Fatalf("block: HTTP %d %s", rec.Code, rec.Body)
	}
	if rec := s.do("GET", "/api/v1/people", adminCookie); !strings.Contains(rec.Body.String(), `"jellyfin_blocked":true`) {
		t.Errorf("people doesn't show the block: %s", rec.Body)
	}
	if rec := jfLogin(s, lan, "kid", "kid-password-1"); rec.Code != http.StatusForbidden {
		t.Errorf("blocked sign-in: HTTP %d", rec.Code)
	}
	if rec := s.doBody("DELETE", "/api/v1/users/jellyfin-blocks/jf-kid", "", adminCookie); rec.Code != http.StatusOK {
		t.Fatalf("unblock: HTTP %d", rec.Code)
	}
	signedInAs(t, jfLogin(s, lan, "kid", "kid-password-1"))
}

// The Jellyfin sign-in settings are the owner's: a manager can't change them.
func TestJellyfinSettingsAdminOnly(t *testing.T) {
	s, _, _ := jellyfinLoginServer(t)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	_, admin := s.user(t, "owner@example.com", auth.RoleAdmin)
	for _, body := range []string{`{"jellyfin_signin_staff":true}`, `{"jellyfin_quickconnect_enabled":false}`, `{"jellyfin_login_enabled":false}`, `{"jellyfin_login_auto_approve_types":"movie,series,book"}`} {
		if rec := s.doBody("PUT", "/api/v1/settings", body, mgr); rec.Code != http.StatusForbidden {
			t.Errorf("manager %s: HTTP %d, want 403", body, rec.Code)
		}
	}
	if rec := s.doBody("PUT", "/api/v1/settings", `{"jellyfin_signin_staff":true}`, admin); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"jellyfin_signin_staff":true`) {
		t.Errorf("admin: HTTP %d %s", rec.Code, rec.Body)
	}
}
