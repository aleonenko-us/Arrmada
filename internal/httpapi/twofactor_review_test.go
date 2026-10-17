package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/eventbus"
	"github.com/tristenlammi/arrmada/internal/notify"
)

// The security review's findings on two-factor sign-in, one test each.

// A burst of simultaneous wrong codes can't get more checked than the limit allows: each
// attempt reserves its failure before the code is looked at, and only one attempt per
// account is checked at a time (the rest are turned away). Ported from the review's
// reproduction, where 300 at once used to get 40 through against a limit of 10.
func TestTwoFactorCodeLimitConcurrent(t *testing.T) {
	f := newTFFixture(t)
	_, cookie := f.user(t, "mum@example.com", auth.RoleRequester)
	secret, _ := f.enrolMe(t, cookie, "password123")
	tk := decodeOK[ticketAnswer](t, f.login("mum@example.com", "password123"))
	now := f.clock.now()
	valid := map[string]bool{}
	for _, d := range []time.Duration{-30 * time.Second, 0, 30 * time.Second} {
		valid[totpAt(t, secret, now.Add(d))] = true
	}
	const n = 300
	sent := 0
	var wg sync.WaitGroup
	var mu sync.Mutex
	counts := map[int]int{}
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		code := fmt.Sprintf("%06d", 100000+i)
		if valid[code] {
			continue
		}
		sent++
		wg.Add(1)
		go func(code string) {
			defer wg.Done()
			<-start
			r := f.post("/api/v1/auth/login/code", map[string]any{"ticket": tk.Ticket, "code": code})
			mu.Lock()
			counts[r.Code]++
			mu.Unlock()
		}(code)
	}
	close(start)
	wg.Wait()
	t.Logf("status counts for %d concurrent wrong codes: %v (401 = code actually checked)", n, counts)
	if got := counts[http.StatusUnauthorized]; got > codeMax || got == 0 {
		t.Errorf("%d codes were checked, want between 1 and %d", got, codeMax)
	}
	if counts[http.StatusOK] != 0 || counts[http.StatusUnauthorized]+counts[http.StatusTooManyRequests] != sent {
		t.Errorf("unexpected answers: %v", counts)
	}
	// Every one checked was counted: as many more as make five are allowed before the pause,
	// and no more.
	bad := "000000"
	if valid[bad] {
		bad = "111111"
	}
	for i := counts[http.StatusUnauthorized]; i < codeFree; i++ {
		if r := f.post("/api/v1/auth/login/code", map[string]any{"ticket": tk.Ticket, "code": bad}); r.Code != http.StatusUnauthorized {
			t.Fatalf("wrong code %d after the burst: HTTP %d", i+1, r.Code)
		}
	}
	if r := f.post("/api/v1/auth/login/code", map[string]any{"ticket": tk.Ticket, "code": totpAt(t, secret, now)}); r.Code != http.StatusTooManyRequests {
		t.Errorf("after five wrong codes in all, the right one: HTTP %d, want 429", r.Code)
	}
}

// Same for the Me page's code checks (turning it off): one at a time, reserved first.
func TestTwoFactorMeCodeLimitConcurrent(t *testing.T) {
	f := newTFFixture(t)
	mum, cookie := f.user(t, "mum@example.com", auth.RoleRequester)
	f.enrolMe(t, cookie, "password123")
	var wg sync.WaitGroup
	var mu sync.Mutex
	checked := 0
	start := make(chan struct{})
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			r := f.post("/api/v1/me/2fa/disable", map[string]string{"password": "password123", "code": fmt.Sprintf("%06d", 900000+i)}, cookie)
			if r.Code == http.StatusBadRequest && strings.Contains(r.Body.String(), "code") {
				mu.Lock()
				checked++
				mu.Unlock()
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if checked > codeMax {
		t.Errorf("%d codes checked at once, want at most %d", checked, codeMax)
	}
	if on, _ := f.auth.TwoFactorEnabled(context.Background(), mum.ID); !on {
		t.Error("two-factor went off")
	}
}

// A setup ticket for an account that turned two-factor on meanwhile (another tab) sends
// the page back to the password, rather than answering a server error.
func TestTwoFactorStaleSetupTicket(t *testing.T) {
	f := newTFFixture(t)
	if r := f.doFrom("PUT", "/api/v1/settings", `{"two_factor_admins":true}`, lanIP, f.admin); r.Code != http.StatusOK {
		t.Fatalf("policy: HTTP %d", r.Code)
	}
	_, _ = f.user(t, "boss@example.com", auth.RoleAdmin)
	first := decodeOK[ticketAnswer](t, f.login("boss@example.com", "password123"))
	second := decodeOK[ticketAnswer](t, f.login("boss@example.com", "password123"))
	if first.TwoFactor != "setup" || second.TwoFactor != "setup" {
		t.Fatalf("answers = %+v %+v", first, second)
	}
	s := decodeOK[setupAnswer](t, f.post("/api/v1/auth/login/setup", map[string]string{"ticket": first.Ticket}))
	r := f.post("/api/v1/auth/login/setup/confirm", map[string]string{"ticket": first.Ticket, "code": totpAt(t, s.Secret, f.clock.now())})
	if r.Code != http.StatusOK {
		t.Fatalf("first tab: HTTP %d %s", r.Code, r.Body)
	}
	if cc := r.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("recovery codes answered with Cache-Control %q", cc)
	}
	f.clock.next()
	for path, body := range map[string]map[string]string{
		"/api/v1/auth/login/setup":         {"ticket": second.Ticket},
		"/api/v1/auth/login/setup/confirm": {"ticket": second.Ticket, "code": totpAt(t, s.Secret, f.clock.now())},
	} {
		r := f.post(path, body)
		if r.Code != http.StatusUnauthorized || !strings.Contains(r.Body.String(), `"reason":"ticket"`) {
			t.Errorf("%s with the stale ticket: HTTP %d %s", path, r.Code, r.Body)
		}
	}
}

// Every answer that carries recovery codes says not to store it.
func TestTwoFactorRecoveryCodesNoStore(t *testing.T) {
	f := newTFFixture(t)
	_, cookie := f.user(t, "mum@example.com", auth.RoleRequester)
	s := decodeOK[setupAnswer](t, f.post("/api/v1/me/2fa/setup", map[string]string{"password": "password123"}, cookie))
	r := f.post("/api/v1/me/2fa/confirm", map[string]string{"code": totpAt(t, s.Secret, f.clock.now())}, cookie)
	if r.Code != http.StatusOK || r.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("confirm: HTTP %d, Cache-Control %q", r.Code, r.Header().Get("Cache-Control"))
	}
	f.clock.next()
	r = f.post("/api/v1/me/2fa/recovery-codes", map[string]string{"password": "password123", "code": totpAt(t, s.Secret, f.clock.now())}, cookie)
	if r.Code != http.StatusOK || r.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("new codes: HTTP %d, Cache-Control %q", r.Code, r.Header().Get("Cache-Control"))
	}
}

// Five wrong codes in the hour for one account raise an alert through the catalog (once:
// it carries a dedupe key) and a staff event, saying the password is likely known, and
// naming no account.
func TestTwoFactorWrongCodesAlert(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := eventbus.New(log)
	events, cancel := bus.Subscribe(notify.EventTwoFactorFailures)
	defer cancel()
	f := newLogFixture(t, func(d *Deps) {
		d.Bus = bus
		d.Notify = notify.NewService(d.Store.DB(), eventbus.New(log), log)
	}, nil)
	c := &stepClock{t: time.Now().Truncate(30 * time.Second).Add(time.Second)}
	f.auth.SetClock(c.now)
	tf := &tfFixture{logFixture: f, clock: c}
	conn, err := f.deps.Notify.Create(context.Background(), notify.Connection{Name: "owner", URL: "json://example.invalid/hook", Events: []string{notify.EventTwoFactorFailures}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, cookie := tf.user(t, "mum@example.com", auth.RoleRequester)
	secret, _ := tf.enrolMe(t, cookie, "password123")
	tk := decodeOK[ticketAnswer](t, tf.login("mum@example.com", "password123"))
	right := map[string]bool{}
	for _, d := range []time.Duration{-30 * time.Second, 0, 30 * time.Second} {
		right[totpAt(t, secret, c.now().Add(d))] = true
	}
	wrong := 0
	for i := 0; wrong < twoFactorAlertAt; i++ {
		code := fmt.Sprintf("%06d", 300000+i)
		if right[code] {
			continue
		}
		if r := tf.post("/api/v1/auth/login/code", map[string]any{"ticket": tk.Ticket, "code": code}); r.Code != http.StatusUnauthorized {
			t.Fatalf("wrong code %d: HTTP %d", wrong+1, r.Code)
		}
		wrong++
	}
	select {
	case ev := <-events:
		data, _ := json.Marshal(ev.Data)
		if !strings.Contains(string(data), `"count":5`) || strings.Contains(string(data), "mum@") {
			t.Errorf("event = %s", data)
		}
	default:
		t.Fatal("no security event after five wrong codes")
	}
	var n int
	var body string
	if err := f.st.DB().QueryRow(`SELECT COUNT(*), COALESCE(MAX(body), '') FROM notification_deliveries WHERE connection_id = ? AND event_key = ?`, conn.ID, notify.EventTwoFactorFailures).Scan(&n, &body); err != nil {
		t.Fatal(err)
	}
	if n != 1 || !strings.Contains(body, "likely known") || strings.Contains(body, "mum") {
		t.Errorf("alert queued %d times, body %q", n, body)
	}
}

// The QR code draws for any username length and alphabet the account might have (the
// review's fuzz); an over-long one is an error, never a panic.
func TestQRPathAnyUsername(t *testing.T) {
	alpha := []rune("abcdefghijklmnopqrstuvwxyz@._-+éüß漢字 0123456789")
	rng := rand.New(rand.NewSource(1))
	for n := 1; n <= 400; n += 3 {
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteRune(alpha[rng.Intn(len(alpha))])
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("panic at username length %d: %v", n, r)
				}
			}()
			_, _, _ = qrPath(otpauthURIForTest(b.String()))
		}()
	}
}

func otpauthURIForTest(name string) string {
	return "otpauth://totp/Arrmada:" + name + "?secret=ABCDEFGHIJKLMNOPQRSTUVWXYZ234567&issuer=Arrmada"
}
