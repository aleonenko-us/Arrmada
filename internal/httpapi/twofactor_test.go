package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/applog"
	"github.com/tristenlammi/arrmada/internal/auth"
)

// totpAt is what an authenticator app shows for secret at t: RFC 6238 with SHA-1, six
// digits and a 30-second step, written out here independently of internal/auth.
func totpAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatalf("secret %q: %v", secret, err)
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(at.Unix()/30))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[off:off+4])&0x7fffffff)%1_000_000)
}

// stepClock is the auth service's clock in these tests, moved a 30-second step at a time
// so every code is for a known step.
type stepClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *stepClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *stepClock) next() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(30 * time.Second)
	return c.t
}

// tfFixture is a route server with the log captured, a stepped clock and an admin.
type tfFixture struct {
	*logFixture
	clock *stepClock
}

func newTFFixture(t *testing.T) *tfFixture {
	t.Helper()
	f := newLogFixture(t, nil, nil)
	c := &stepClock{t: time.Now().Truncate(30 * time.Second).Add(time.Second)}
	f.auth.SetClock(c.now)
	return &tfFixture{logFixture: f, clock: c}
}

const lanIP = "192.168.1.20"

func (f *tfFixture) post(path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	return f.doFrom("POST", path, string(b), lanIP, cookies...)
}

func (f *tfFixture) login(name, pw string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	return f.post("/api/v1/auth/login", map[string]string{"username": name, "password": pw}, cookies...)
}

// decodeOK reads a JSON answer, failing on a non-200.
func decodeOK[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("%v: %s", err, rec.Body)
	}
	return v
}

type setupAnswer struct {
	Secret string `json:"secret"`
	URI    string `json:"uri"`
	QR     struct {
		Size int    `json:"size"`
		Path string `json:"path"`
	} `json:"qr"`
}

type ticketAnswer struct {
	TwoFactor string     `json:"two_factor"`
	Ticket    string     `json:"ticket"`
	User      *auth.User `json:"user"`
}

// enrolMe sets two-factor up through the Me page for the account signed in with cookie
// (password pw) and returns the secret and recovery codes. The clock is moved on a step,
// so the next code is fresh.
func (f *tfFixture) enrolMe(t *testing.T, cookie *http.Cookie, pw string) (string, []string) {
	t.Helper()
	s := decodeOK[setupAnswer](t, f.post("/api/v1/me/2fa/setup", map[string]string{"password": pw}, cookie))
	if s.Secret == "" || !strings.HasPrefix(s.URI, "otpauth://totp/Arrmada:") || s.QR.Size < 21 || !strings.HasPrefix(s.QR.Path, "M") {
		t.Fatalf("setup answer = %+v", s)
	}
	out := decodeOK[struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}](t, f.post("/api/v1/me/2fa/confirm", map[string]string{"code": totpAt(t, s.Secret, f.clock.now())}, cookie))
	if len(out.RecoveryCodes) != auth.RecoveryCodeCount {
		t.Fatalf("recovery codes = %v", out.RecoveryCodes)
	}
	f.clock.next()
	return s.Secret, out.RecoveryCodes
}

// The whole sign-in: a right password answers a ticket and no session; the ticket opens
// nothing; a wrong code is refused; the right one makes the session; the ticket then
// can't make a second one, and the same code can't be used again.
func TestTwoFactorLoginFlow(t *testing.T) {
	f := newTFFixture(t)
	_, cookie := f.user(t, "mum@example.com", auth.RoleRequester)
	secret, _ := f.enrolMe(t, cookie, "password123")

	// A wrong password is still just that.
	if rec := f.login("mum@example.com", "wrong-password"); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong password: HTTP %d", rec.Code)
	}
	rec := f.login("mum@example.com", "password123")
	if sessionCookieOn(rec) != nil {
		t.Fatal("a right password made a session before the code")
	}
	tk := decodeOK[ticketAnswer](t, rec)
	if tk.TwoFactor != "code" || tk.Ticket == "" || tk.User != nil {
		t.Fatalf("login answer = %+v", tk)
	}

	// The ticket is not a session: not as the session cookie, not as an API key.
	if r := f.doFrom("GET", "/api/v1/auth/me", "", lanIP, &http.Cookie{Name: sessionCookieName, Value: tk.Ticket}); r.Code != http.StatusUnauthorized {
		t.Errorf("ticket as the session cookie: HTTP %d", r.Code)
	}
	req := httptest.NewRequest("GET", "http://arrmada.local/api/v1/auth/me", nil)
	req.RemoteAddr = lanIP + ":5000"
	req.Header.Set("Authorization", "Bearer "+tk.Ticket)
	r := httptest.NewRecorder()
	f.h.ServeHTTP(r, req)
	if r.Code != http.StatusUnauthorized {
		t.Errorf("ticket as a bearer key: HTTP %d", r.Code)
	}
	// Nor is it a ticket for setting two-factor up.
	if r := f.post("/api/v1/auth/login/setup", map[string]string{"ticket": tk.Ticket}); r.Code != http.StatusUnauthorized {
		t.Errorf("verify ticket used for setup: HTTP %d", r.Code)
	}

	code := totpAt(t, secret, f.clock.now())
	wrong := "000000"
	if wrong == code {
		wrong = "111111"
	}
	// A wrong code is said as such (the page asks again); it isn't the ticket's end.
	if r := f.post("/api/v1/auth/login/code", map[string]any{"ticket": tk.Ticket, "code": wrong}); r.Code != http.StatusUnauthorized || sessionCookieOn(r) != nil || strings.Contains(r.Body.String(), `"reason"`) {
		t.Errorf("wrong code: HTTP %d %s", r.Code, r.Body)
	}
	r = f.post("/api/v1/auth/login/code", map[string]any{"ticket": tk.Ticket, "code": code})
	sess := sessionCookieOn(r)
	if r.Code != http.StatusOK || sess == nil {
		t.Fatalf("right code: HTTP %d %s", r.Code, r.Body)
	}
	if me := f.do("GET", "/api/v1/auth/me", sess); me.Code != http.StatusOK || !strings.Contains(me.Body.String(), "mum@example.com") {
		t.Errorf("the new session: HTTP %d %s", me.Code, me.Body)
	}
	if cookieNamed(r, trustCookieName) != nil {
		t.Error("trusted the device without being asked")
	}
	f.clock.next()
	// The ticket is spent: the page is told to go back to the password.
	if r := f.post("/api/v1/auth/login/code", map[string]any{"ticket": tk.Ticket, "code": totpAt(t, secret, f.clock.now())}); r.Code != http.StatusUnauthorized || !strings.Contains(r.Body.String(), `"reason":"ticket"`) {
		t.Errorf("ticket used twice: HTTP %d %s", r.Code, r.Body)
	}
	// And a code works once: a fresh ticket with the code already used is refused.
	tk = decodeOK[ticketAnswer](t, f.login("mum@example.com", "password123"))
	if r := f.post("/api/v1/auth/login/code", map[string]any{"ticket": tk.Ticket, "code": code}); r.Code != http.StatusUnauthorized {
		t.Errorf("a used code again: HTTP %d", r.Code)
	}
	// Someone without two-factor signs in as before.
	_, _ = f.user(t, "dad@example.com", auth.RoleRequester)
	if r := f.login("dad@example.com", "password123"); r.Code != http.StatusOK || sessionCookieOn(r) == nil {
		t.Errorf("no two-factor: HTTP %d %s", r.Code, r.Body)
	}
}

// A recovery code signs in once; the second try with it fails.
func TestTwoFactorLoginRecoveryCode(t *testing.T) {
	f := newTFFixture(t)
	_, cookie := f.user(t, "mum@example.com", auth.RoleRequester)
	_, codes := f.enrolMe(t, cookie, "password123")

	tk := decodeOK[ticketAnswer](t, f.login("mum@example.com", "password123"))
	r := f.post("/api/v1/auth/login/code", map[string]any{"ticket": tk.Ticket, "code": strings.ToUpper(codes[2])})
	if r.Code != http.StatusOK || sessionCookieOn(r) == nil {
		t.Fatalf("recovery code: HTTP %d %s", r.Code, r.Body)
	}
	tk = decodeOK[ticketAnswer](t, f.login("mum@example.com", "password123"))
	if r := f.post("/api/v1/auth/login/code", map[string]any{"ticket": tk.Ticket, "code": codes[2]}); r.Code != http.StatusUnauthorized {
		t.Errorf("the same recovery code again: HTTP %d", r.Code)
	}
	st := decodeOK[map[string]any](t, f.do("GET", "/api/v1/me/2fa", cookie))
	if st["recovery_codes_left"] != float64(9) {
		t.Errorf("recovery codes left = %v", st["recovery_codes_left"])
	}
}

// Wrong codes are limited like wrong passwords: five free, then a pause — on the LAN too,
// since only someone with the password gets here — and a right code clears the record.
func TestTwoFactorCodeRateLimit(t *testing.T) {
	f := newTFFixture(t)
	_, cookie := f.user(t, "mum@example.com", auth.RoleRequester)
	secret, _ := f.enrolMe(t, cookie, "password123")
	tk := decodeOK[ticketAnswer](t, f.login("mum@example.com", "password123"))
	good := totpAt(t, secret, f.clock.now())
	bad := "000000"
	if bad == good {
		bad = "111111"
	}
	for i := range 5 {
		if r := f.post("/api/v1/auth/login/code", map[string]any{"ticket": tk.Ticket, "code": bad}); r.Code != http.StatusUnauthorized {
			t.Fatalf("wrong code %d: HTTP %d", i+1, r.Code)
		}
	}
	r := f.post("/api/v1/auth/login/code", map[string]any{"ticket": tk.Ticket, "code": good})
	if r.Code != http.StatusTooManyRequests || r.Header().Get("Retry-After") == "" {
		t.Fatalf("after five wrong codes, even the right one: HTTP %d (Retry-After %q)", r.Code, r.Header().Get("Retry-After"))
	}
	// A fresh ticket doesn't reset it: the limit is the account's.
	tk2 := decodeOK[ticketAnswer](t, f.login("mum@example.com", "password123"))
	if r := f.post("/api/v1/auth/login/code", map[string]any{"ticket": tk2.Ticket, "code": good}); r.Code != http.StatusTooManyRequests {
		t.Errorf("a new ticket got round the limit: HTTP %d", r.Code)
	}
	// Warned in the log, without the code or the secret.
	logs := f.ring.Snapshot(applog.Filter{Query: "wrong two-factor codes"})
	if len(logs) == 0 {
		t.Error("no warning about repeated wrong codes")
	}
	for _, e := range f.ring.Snapshot(applog.Filter{}) {
		if strings.Contains(e.Message+e.Attrs, secret) || strings.Contains(e.Message+e.Attrs, good) {
			t.Errorf("a log line carries the secret or a code: %s %s", e.Message, e.Attrs)
		}
	}
}

// "Trust this device": the next password sign-in from that browser needs no code; other
// browsers still do; signing out other devices forgets theirs and keeps this one's.
func TestTwoFactorTrustDevice(t *testing.T) {
	f := newTFFixture(t)
	_, cookie := f.user(t, "mum@example.com", auth.RoleRequester)
	secret, _ := f.enrolMe(t, cookie, "password123")

	signIn := func(trust bool) *http.Cookie {
		t.Helper()
		tk := decodeOK[ticketAnswer](t, f.login("mum@example.com", "password123"))
		r := f.post("/api/v1/auth/login/code", map[string]any{"ticket": tk.Ticket, "code": totpAt(t, secret, f.clock.now()), "trust_device": trust})
		f.clock.next()
		if r.Code != http.StatusOK {
			t.Fatalf("sign in: HTTP %d %s", r.Code, r.Body)
		}
		c := cookieNamed(r, trustCookieName)
		if trust && (c == nil || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/api/v1" || time.Until(c.Expires) < 29*24*time.Hour) {
			t.Fatalf("trust cookie = %+v", c)
		}
		return c
	}
	phone := signIn(true)
	laptop := signIn(true)

	r := f.login("mum@example.com", "password123", phone)
	if r.Code != http.StatusOK || sessionCookieOn(r) == nil {
		t.Fatalf("trusted device: HTTP %d %s", r.Code, r.Body)
	}
	phoneSession := sessionCookieOn(r)
	if r := f.login("mum@example.com", "wrong-password", phone); r.Code != http.StatusUnauthorized {
		t.Errorf("trusted device, wrong password: HTTP %d", r.Code)
	}
	if tk := decodeOK[ticketAnswer](t, f.login("mum@example.com", "password123")); tk.TwoFactor != "code" {
		t.Errorf("an untrusted browser skipped the code: %+v", tk)
	}
	// Someone else's account on the trusted browser still needs its own code.
	_, dadC := f.user(t, "dad@example.com", auth.RoleRequester)
	f.enrolMe(t, dadC, "password123")
	if tk := decodeOK[ticketAnswer](t, f.login("dad@example.com", "password123", phone)); tk.TwoFactor != "code" {
		t.Errorf("another account rode the trust: %+v", tk)
	}

	// Sign out the other devices from the phone: the laptop's trust goes, the phone's stays.
	if r := f.doFrom("POST", "/api/v1/me/sessions/revoke-others", "", lanIP, phoneSession, phone); r.Code != http.StatusOK {
		t.Fatalf("revoke others: HTTP %d %s", r.Code, r.Body)
	}
	if tk := decodeOK[ticketAnswer](t, f.login("mum@example.com", "password123", laptop)); tk.TwoFactor != "code" {
		t.Errorf("the laptop is still trusted after signing out other devices: %+v", tk)
	}
	if r := f.login("mum@example.com", "password123", phone); sessionCookieOn(r) == nil {
		t.Errorf("the phone lost its own trust: HTTP %d %s", r.Code, r.Body)
	}
}

// The Me page: setting up needs the password and a right first code; the status never
// carries the secret; turning off and new recovery codes need the password and a code.
func TestTwoFactorMePage(t *testing.T) {
	f := newTFFixture(t)
	_, cookie := f.user(t, "mum@example.com", auth.RoleRequester)

	st := decodeOK[map[string]any](t, f.do("GET", "/api/v1/me/2fa", cookie))
	if st["enabled"] != false || st["password_set"] != true || st["required"] != false {
		t.Errorf("status before = %v", st)
	}
	if r := f.post("/api/v1/me/2fa/setup", map[string]string{"password": "nope-nope"}, cookie); r.Code != http.StatusBadRequest {
		t.Errorf("setup with a wrong password: HTTP %d", r.Code)
	}
	s := decodeOK[setupAnswer](t, f.post("/api/v1/me/2fa/setup", map[string]string{"password": "password123"}, cookie))
	if r := f.post("/api/v1/me/2fa/confirm", map[string]string{"code": "12345"}, cookie); r.Code != http.StatusBadRequest {
		t.Errorf("confirm with a bad code: HTTP %d", r.Code)
	}
	if on := decodeOK[map[string]any](t, f.do("GET", "/api/v1/me/2fa", cookie)); on["enabled"] != false {
		t.Error("on before a right code")
	}
	codes := decodeOK[struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}](t, f.post("/api/v1/me/2fa/confirm", map[string]string{"code": totpAt(t, s.Secret, f.clock.now())}, cookie)).RecoveryCodes
	f.clock.next()
	// Existing sessions stay signed in.
	if r := f.do("GET", "/api/v1/auth/me", cookie); r.Code != http.StatusOK {
		t.Errorf("this session after turning it on: HTTP %d", r.Code)
	}
	// Setting up again while on is refused (it would replace the secret).
	if r := f.post("/api/v1/me/2fa/setup", map[string]string{"password": "password123"}, cookie); r.Code != http.StatusConflict {
		t.Errorf("setup while on: HTTP %d", r.Code)
	}
	st = decodeOK[map[string]any](t, f.do("GET", "/api/v1/me/2fa", cookie))
	if st["enabled"] != true || st["recovery_codes_left"] != float64(10) {
		t.Errorf("status after = %v", st)
	}

	// New recovery codes: password and a code.
	if r := f.post("/api/v1/me/2fa/recovery-codes", map[string]string{"password": "password123", "code": "000000"}, cookie); r.Code != http.StatusBadRequest {
		t.Errorf("regenerate with a wrong code: HTTP %d", r.Code)
	}
	if r := f.post("/api/v1/me/2fa/recovery-codes", map[string]string{"password": "wrong-one", "code": totpAt(t, s.Secret, f.clock.now())}, cookie); r.Code != http.StatusBadRequest {
		t.Errorf("regenerate with a wrong password: HTTP %d", r.Code)
	}
	fresh := decodeOK[struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}](t, f.post("/api/v1/me/2fa/recovery-codes", map[string]string{"password": "password123", "code": totpAt(t, s.Secret, f.clock.now())}, cookie)).RecoveryCodes
	f.clock.next()
	if len(fresh) != 10 || fresh[0] == codes[0] {
		t.Errorf("new codes = %v", fresh)
	}

	// Turning off: the password alone isn't enough, nor a code alone.
	if r := f.post("/api/v1/me/2fa/disable", map[string]string{"password": "password123"}, cookie); r.Code != http.StatusBadRequest {
		t.Errorf("disable without a code: HTTP %d", r.Code)
	}
	if r := f.post("/api/v1/me/2fa/disable", map[string]string{"code": totpAt(t, s.Secret, f.clock.now())}, cookie); r.Code != http.StatusBadRequest {
		t.Errorf("disable without the password: HTTP %d", r.Code)
	}
	// An old recovery code from before regenerating doesn't work; a new one does.
	if r := f.post("/api/v1/me/2fa/disable", map[string]string{"password": "password123", "code": codes[0]}, cookie); r.Code != http.StatusBadRequest {
		t.Errorf("disable with a retired recovery code: HTTP %d", r.Code)
	}
	if r := f.post("/api/v1/me/2fa/disable", map[string]string{"password": "password123", "code": fresh[0]}, cookie); r.Code != http.StatusOK {
		t.Fatalf("disable: HTTP %d %s", r.Code, r.Body)
	}
	if r := f.login("mum@example.com", "password123"); sessionCookieOn(r) == nil {
		t.Errorf("after turning it off the password signs in alone: HTTP %d %s", r.Code, r.Body)
	}

	// An account with no password anyone knows (Plex sign-in) has nothing to protect.
	plex, err := f.auth.FindOrCreatePlexUser(context.Background(), "777", "plexkid", auth.RoleRequester, auth.AutoApproval{})
	if err != nil {
		t.Fatal(err)
	}
	tok, _, _ := f.auth.CreateSession(context.Background(), plex.ID)
	pc := &http.Cookie{Name: sessionCookieName, Value: tok}
	if r := f.post("/api/v1/me/2fa/setup", map[string]string{"password": ""}, pc); r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), "Plex") {
		t.Errorf("Plex-only setup: HTTP %d %s", r.Code, r.Body)
	}
}

// An admin can turn off someone else's two-factor (logged with who did it); not their
// own, and nobody else can.
func TestTwoFactorAdminReset(t *testing.T) {
	f := newTFFixture(t)
	mum, cookie := f.user(t, "mum@example.com", auth.RoleRequester)
	_, mgr := f.user(t, "mgr@example.com", auth.RoleManager)
	f.enrolMe(t, cookie, "password123")
	path := fmt.Sprintf("/api/v1/users/%d/2fa/reset", mum.ID)

	for name, c := range map[string]*http.Cookie{"requester": cookie, "manager": mgr} {
		if r := f.do("POST", path, c); r.Code != http.StatusForbidden {
			t.Errorf("%s: HTTP %d, want 403", name, r.Code)
		}
	}
	if r := f.do("POST", "/api/v1/users/99999/2fa/reset", f.admin); r.Code != http.StatusNotFound {
		t.Errorf("unknown user: HTTP %d", r.Code)
	}
	admin, _ := f.auth.UserByUsername(context.Background(), "owner-admin")
	if r := f.do("POST", fmt.Sprintf("/api/v1/users/%d/2fa/reset", admin.ID), f.admin); r.Code != http.StatusBadRequest {
		t.Errorf("own reset: HTTP %d", r.Code)
	}
	people := decodeOK[struct {
		People []personRow `json:"people"`
	}](t, f.do("GET", "/api/v1/people", f.admin))
	for _, p := range people.People {
		if p.ID == mum.ID && !p.TwoFactor {
			t.Error("People doesn't show mum's two-factor")
		}
	}
	r := f.do("POST", path, f.admin)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"reset":true`) {
		t.Fatalf("reset: HTTP %d %s", r.Code, r.Body)
	}
	if on, _ := f.auth.TwoFactorEnabled(context.Background(), mum.ID); on {
		t.Error("still on after the reset")
	}
	if logs := f.ring.Snapshot(applog.Filter{Query: "two-factor sign-in reset by an admin"}); len(logs) != 1 || !strings.Contains(logs[0].Attrs, fmt.Sprintf("by_user_id=%d", admin.ID)) {
		t.Errorf("reset log = %+v", logs)
	}
	if r := f.login("mum@example.com", "password123"); sessionCookieOn(r) == nil {
		t.Errorf("after the reset: HTTP %d %s", r.Code, r.Body)
	}
}

// "Require two-factor for admins": an admin without it sets it up at sign-in, before any
// session exists; everyone else, and admins who have it, sign in as usual. Only an admin
// can switch it.
func TestTwoFactorRequiredForAdmins(t *testing.T) {
	f := newTFFixture(t)
	_, mgr := f.user(t, "mgr@example.com", auth.RoleManager)
	if r := f.doFrom("PUT", "/api/v1/settings", `{"two_factor_admins":true}`, lanIP, mgr); r.Code != http.StatusForbidden {
		t.Errorf("manager switching it: HTTP %d", r.Code)
	}
	if r := f.doFrom("PUT", "/api/v1/settings", `{"two_factor_admins":true}`, lanIP, f.admin); r.Code != http.StatusOK {
		t.Fatalf("admin switching it: HTTP %d %s", r.Code, r.Body)
	}
	if st := decodeOK[map[string]any](t, f.do("GET", "/api/v1/settings", f.admin)); st["two_factor_admins"] != true {
		t.Errorf("setting = %v", st["two_factor_admins"])
	}
	// The admin's existing session carries on, and the Me page says it's required.
	if st := decodeOK[map[string]any](t, f.do("GET", "/api/v1/me/2fa", f.admin)); st["required"] != true {
		t.Errorf("me/2fa required = %v", st["required"])
	}

	_, _ = f.user(t, "boss@example.com", auth.RoleAdmin)
	rec := f.login("boss@example.com", "password123")
	if sessionCookieOn(rec) != nil {
		t.Fatal("an admin without two-factor got a session")
	}
	tk := decodeOK[ticketAnswer](t, rec)
	if tk.TwoFactor != "setup" || tk.Ticket == "" {
		t.Fatalf("login answer = %+v", tk)
	}
	// The setup ticket is no code ticket, and no session.
	if r := f.post("/api/v1/auth/login/code", map[string]any{"ticket": tk.Ticket, "code": "123456"}); r.Code != http.StatusUnauthorized {
		t.Errorf("setup ticket used for a code: HTTP %d", r.Code)
	}
	if r := f.doFrom("GET", "/api/v1/auth/me", "", lanIP, &http.Cookie{Name: sessionCookieName, Value: tk.Ticket}); r.Code != http.StatusUnauthorized {
		t.Errorf("setup ticket as a session: HTTP %d", r.Code)
	}
	s := decodeOK[setupAnswer](t, f.post("/api/v1/auth/login/setup", map[string]string{"ticket": tk.Ticket}))
	if r := f.post("/api/v1/auth/login/setup/confirm", map[string]string{"ticket": tk.Ticket, "code": "000000"}); r.Code != http.StatusUnauthorized || sessionCookieOn(r) != nil {
		t.Errorf("wrong first code: HTTP %d", r.Code)
	}
	r := f.post("/api/v1/auth/login/setup/confirm", map[string]string{"ticket": tk.Ticket, "code": totpAt(t, s.Secret, f.clock.now())})
	f.clock.next()
	if r.Code != http.StatusOK || sessionCookieOn(r) == nil {
		t.Fatalf("confirm: HTTP %d %s", r.Code, r.Body)
	}
	out := decodeOK[struct {
		User          auth.User `json:"user"`
		RecoveryCodes []string  `json:"recovery_codes"`
	}](t, r)
	if out.User.Username != "boss@example.com" || len(out.RecoveryCodes) != 10 {
		t.Errorf("confirm answer = %+v", out)
	}
	// From now on, the code.
	if tk := decodeOK[ticketAnswer](t, f.login("boss@example.com", "password123")); tk.TwoFactor != "code" {
		t.Errorf("next sign-in = %+v", tk)
	}
	// A manager isn't an admin: no change for them.
	if r := f.login("mgr@example.com", "password123"); sessionCookieOn(r) == nil {
		t.Errorf("manager under the admin policy: HTTP %d %s", r.Code, r.Body)
	}
}

// The secret leaves the server once, in the setup answer: never in a later response, the
// people list, the log, or the support bundle (even if a line quoted it by mistake).
func TestTwoFactorSecretNeverShown(t *testing.T) {
	f := newTFFixture(t)
	mum, cookie := f.user(t, "mum@example.com", auth.RoleRequester)
	secret, _ := f.enrolMe(t, cookie, "password123")
	tk := decodeOK[ticketAnswer](t, f.login("mum@example.com", "password123"))
	sign := f.post("/api/v1/auth/login/code", map[string]any{"ticket": tk.Ticket, "code": totpAt(t, secret, f.clock.now())})
	if sign.Code != http.StatusOK {
		t.Fatalf("sign in: HTTP %d", sign.Code)
	}
	for _, path := range []string{"/api/v1/me/2fa", "/api/v1/auth/me", "/api/v1/me/account"} {
		if r := f.do("GET", path, cookie); strings.Contains(r.Body.String(), secret) {
			t.Errorf("%s carries the secret", path)
		}
	}
	for _, path := range []string{"/api/v1/people", "/api/v1/users", "/api/v1/settings", fmt.Sprintf("/api/v1/users/%d/impact", mum.ID)} {
		if r := f.do("GET", path, f.admin); strings.Contains(r.Body.String(), secret) {
			t.Errorf("%s carries the secret", path)
		}
	}
	for _, e := range f.ring.Snapshot(applog.Filter{}) {
		if strings.Contains(e.Message+" "+e.Attrs, secret) {
			t.Errorf("logged: %s %s", e.Message, e.Attrs)
		}
	}
	// A careless line elsewhere quoting it: the bundle blanks it anyway.
	f.log.Info("debug: totp for mum is " + secret)
	rec := f.do("GET", "/api/v1/system/support-bundle", f.admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("bundle: HTTP %d %s", rec.Code, rec.Body)
	}
	for name, body := range readZip(t, rec.Body.Bytes()) {
		if strings.Contains(body, secret) {
			t.Errorf("the support bundle's %s carries the secret", name)
		}
	}
}
