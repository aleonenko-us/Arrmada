package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func init() {
	// Ten bcrypt hashes per setup at the real cost would make these tests crawl under -race.
	recoveryCost = bcrypt.MinCost
}

// clock is a settable fake time for a Service.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }
func withClock(s *Service, at time.Time) *clock {
	c := &clock{t: at}
	s.now = c.now
	return c
}

// codeFor is what the person's authenticator app shows at t.
func codeFor(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		t.Fatal(err)
	}
	return totpCode(key, totpStepAt(at))
}

// enrol sets two-factor up for u at the clock's time and returns the secret and the
// recovery codes.
func enrol(t *testing.T, s *Service, c *clock, userID int64) (string, []string) {
	t.Helper()
	ctx := context.Background()
	setup, err := s.BeginTOTP(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := s.ConfirmTOTP(ctx, userID, codeFor(t, setup.Secret, c.now()))
	if err != nil {
		t.Fatal(err)
	}
	return setup.Secret, codes
}

// Setting up: a wrong code leaves it off; the right one turns it on and hands out ten
// one-time recovery codes, stored only as bcrypt hashes.
func TestTwoFactorEnrol(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	c := withClock(s, time.Unix(1_700_000_010, 0))
	u, _ := s.CreateUser(ctx, "mum@example.com", "password1", RoleRequester, false)

	setup, err := s.BeginTOTP(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(setup.URI, "issuer=Arrmada") || !strings.Contains(setup.URI, "secret="+setup.Secret) {
		t.Errorf("uri = %s", setup.URI)
	}
	if _, err := s.ConfirmTOTP(ctx, u.ID, "000000"); !errors.Is(err, ErrBadCode) {
		t.Errorf("wrong code: %v, want ErrBadCode", err)
	}
	if on, _ := s.TwoFactorEnabled(ctx, u.ID); on {
		t.Fatal("on before a right code")
	}
	// Starting again makes a new secret; the first one's codes no longer turn it on.
	again, err := s.BeginTOTP(ctx, u.ID)
	if err != nil || again.Secret == setup.Secret {
		t.Fatalf("restart: %v (same secret %v)", err, again.Secret == setup.Secret)
	}
	if _, err := s.ConfirmTOTP(ctx, u.ID, codeFor(t, setup.Secret, c.now())); !errors.Is(err, ErrBadCode) {
		t.Errorf("the replaced secret's code: %v", err)
	}
	codes, err := s.ConfirmTOTP(ctx, u.ID, codeFor(t, again.Secret, c.now()))
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != RecoveryCodeCount {
		t.Fatalf("%d recovery codes, want %d", len(codes), RecoveryCodeCount)
	}
	seen := map[string]bool{}
	for _, rc := range codes {
		if len(rc) != 11 || rc[5] != '-' || seen[rc] {
			t.Errorf("recovery code %q", rc)
		}
		seen[rc] = true
	}
	st, err := s.TwoFactor(ctx, u.ID)
	if err != nil || !st.Enabled || st.RecoveryCodesLeft != 10 || st.EnabledAt == nil {
		t.Errorf("status = %+v (%v)", st, err)
	}
	var stored string
	_ = s.db.QueryRow(`SELECT group_concat(code_hash, ' ') FROM user_recovery_codes WHERE user_id = ?`, u.ID).Scan(&stored)
	for _, rc := range codes {
		if strings.Contains(stored, rc) || strings.Contains(stored, strings.ReplaceAll(rc, "-", "")) {
			t.Fatalf("recovery code %s stored as written", rc)
		}
	}
	if !strings.HasPrefix(stored, "$2") {
		t.Errorf("recovery codes aren't bcrypt hashes: %.20s…", stored)
	}
	// Once on, setting up again is refused until it's turned off: nothing replaces the
	// secret behind the owner's back.
	if _, err := s.BeginTOTP(ctx, u.ID); !errors.Is(err, ErrTwoFactorOn) {
		t.Errorf("begin while on: %v", err)
	}
	if _, err := s.ConfirmTOTP(ctx, u.ID, codeFor(t, again.Secret, c.now())); !errors.Is(err, ErrTwoFactorOn) {
		t.Errorf("confirm while on: %v", err)
	}
}

// A code is accepted ±30 seconds, and once: the step used at setup, and every step used
// at sign-in, can't be used again.
func TestTwoFactorWindowAndReuse(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	c := withClock(s, time.Unix(1_700_000_010, 0))
	u, _ := s.CreateUser(ctx, "mum@example.com", "password1", RoleRequester, false)
	secret, _ := enrol(t, s, c, u.ID)
	setupCode := codeFor(t, secret, c.now())

	c.add(totpStep) // the next step
	if _, err := s.VerifySecondFactor(ctx, u.ID, setupCode); !errors.Is(err, ErrBadCode) {
		t.Errorf("the setup code again (previous step, already used): %v", err)
	}
	now := c.now()
	if _, err := s.VerifySecondFactor(ctx, u.ID, codeFor(t, secret, now.Add(2*totpStep))); !errors.Is(err, ErrBadCode) {
		t.Errorf("two steps ahead: %v", err)
	}
	code := codeFor(t, secret, now)
	if rec, err := s.VerifySecondFactor(ctx, u.ID, code[:3]+" "+code[3:]); err != nil || rec {
		t.Fatalf("the current code (typed with a space): recovery=%v %v", rec, err)
	}
	if _, err := s.VerifySecondFactor(ctx, u.ID, code); !errors.Is(err, ErrBadCode) {
		t.Errorf("the same code twice: %v", err)
	}
	// The phone a step ahead: accepted, and then the current step (older) is spent too.
	if _, err := s.VerifySecondFactor(ctx, u.ID, codeFor(t, secret, now.Add(totpStep))); err != nil {
		t.Errorf("one step ahead: %v", err)
	}
	c.add(time.Hour)
	if _, err := s.VerifySecondFactor(ctx, u.ID, codeFor(t, secret, c.now().Add(-totpStep))); err != nil {
		t.Errorf("one step behind, an hour later: %v", err)
	}
	// Racing sign-ins with one code: exactly one wins.
	c.add(time.Hour)
	code = codeFor(t, secret, c.now())
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.VerifySecondFactor(ctx, u.ID, code); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Errorf("one code made %d sign-ins, want 1", wins)
	}
}

// A recovery code works once, typed any way round; regenerating retires the old set.
func TestRecoveryCodesSingleUse(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	c := withClock(s, time.Unix(1_700_000_010, 0))
	u, _ := s.CreateUser(ctx, "mum@example.com", "password1", RoleRequester, false)
	other, _ := s.CreateUser(ctx, "dad@example.com", "password1", RoleRequester, false)
	_, codes := enrol(t, s, c, u.ID)
	enrol(t, s, c, other.ID)

	if _, err := s.VerifySecondFactor(ctx, other.ID, codes[0]); !errors.Is(err, ErrBadCode) {
		t.Errorf("someone else's recovery code: %v", err)
	}
	if rec, err := s.VerifySecondFactor(ctx, u.ID, " "+strings.ToUpper(codes[0])+" "); err != nil || !rec {
		t.Fatalf("recovery code: recovery=%v %v", rec, err)
	}
	if _, err := s.VerifySecondFactor(ctx, u.ID, codes[0]); !errors.Is(err, ErrBadCode) {
		t.Errorf("the same recovery code twice: %v", err)
	}
	if st, _ := s.TwoFactor(ctx, u.ID); st.RecoveryCodesLeft != 9 {
		t.Errorf("left = %d, want 9", st.RecoveryCodesLeft)
	}
	for _, bad := range []string{"", "nope", strings.Repeat("a", 200), "aaaaa-aaaaa"} {
		if _, err := s.VerifySecondFactor(ctx, u.ID, bad); !errors.Is(err, ErrBadCode) {
			t.Errorf("%q: %v", bad, err)
		}
	}

	fresh, err := s.RegenerateRecoveryCodes(ctx, u.ID)
	if err != nil || len(fresh) != RecoveryCodeCount {
		t.Fatalf("regenerate: %d codes, %v", len(fresh), err)
	}
	if _, err := s.VerifySecondFactor(ctx, u.ID, codes[1]); !errors.Is(err, ErrBadCode) {
		t.Errorf("an old, unused code after regenerating: %v", err)
	}
	if _, err := s.VerifySecondFactor(ctx, u.ID, fresh[3]); err != nil {
		t.Errorf("a new code: %v", err)
	}
	if st, _ := s.TwoFactor(ctx, u.ID); st.RecoveryCodesLeft != 9 {
		t.Errorf("left after regenerating and one use = %d, want 9", st.RecoveryCodesLeft)
	}
}

// Turning it off clears the secret, the recovery codes and the trusted devices; codes then
// fail as "off" and it can be set up afresh.
func TestDisableTwoFactorClearsEverything(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	c := withClock(s, time.Unix(1_700_000_010, 0))
	u, _ := s.CreateUser(ctx, "mum@example.com", "password1", RoleRequester, false)
	secret, codes := enrol(t, s, c, u.ID)
	trust, _, err := s.TrustDevice(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	was, err := s.DisableTwoFactor(ctx, u.ID)
	if err != nil || !was {
		t.Fatalf("disable: was=%v %v", was, err)
	}
	for _, table := range []string{"user_totp", "user_recovery_codes", "user_trusted_devices"} {
		var n int
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE user_id = ?`, u.ID).Scan(&n)
		if n != 0 {
			t.Errorf("%s keeps %d rows", table, n)
		}
	}
	if s.DeviceTrusted(ctx, u.ID, trust) {
		t.Error("a trusted device survived turning two-factor off")
	}
	c.add(totpStep)
	if _, err := s.VerifySecondFactor(ctx, u.ID, codeFor(t, secret, c.now())); !errors.Is(err, ErrTwoFactorOff) {
		t.Errorf("code after off: %v", err)
	}
	if _, err := s.VerifySecondFactor(ctx, u.ID, codes[0]); !errors.Is(err, ErrTwoFactorOff) {
		t.Errorf("recovery code after off: %v", err)
	}
	if was, _ := s.DisableTwoFactor(ctx, u.ID); was {
		t.Error("turning it off twice said it was on")
	}
	enrol(t, s, c, u.ID)
	// Deleting the account takes every row with it.
	if err := s.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM user_totp) + (SELECT COUNT(*) FROM user_recovery_codes)`).Scan(&n)
	if n != 0 {
		t.Errorf("%d two-factor rows outlived the account", n)
	}
}

// The ticket between password and code: right purpose only, five minutes, once, and dead
// when the password changes, the account is disabled, or it's forged or from another run.
func TestLoginTicket(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	c := withClock(s, time.Unix(1_700_000_010, 0))
	u, _ := s.CreateUser(ctx, "mum@example.com", "password1", RoleRequester, false)

	tk, err := s.IssueLoginTicket(ctx, u.ID, TicketVerify)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.CheckLoginTicket(ctx, tk, TicketVerify); err != nil || got.ID != u.ID {
		t.Fatalf("check: %v %v", got, err)
	}
	if _, err := s.CheckLoginTicket(ctx, tk, TicketEnrol); !errors.Is(err, ErrTicket) {
		t.Errorf("other purpose: %v", err)
	}
	payload, mac, _ := strings.Cut(tk, ".")
	for name, bad := range map[string]string{
		"empty":      "",
		"no mac":     payload,
		"zeroed mac": payload + "." + strings.Repeat("A", len(mac)),
		"junk":       "not-a-ticket.at-all",
	} {
		if _, err := s.CheckLoginTicket(ctx, bad, TicketVerify); !errors.Is(err, ErrTicket) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Another process (a restart) has another key.
	if _, err := NewService(s.db).CheckLoginTicket(ctx, tk, TicketVerify); !errors.Is(err, ErrTicket) {
		t.Errorf("another key accepted it: %v", err)
	}

	// Spent once, never again.
	if !s.SpendLoginTicket(tk) {
		t.Fatal("first spend refused")
	}
	if s.SpendLoginTicket(tk) {
		t.Error("spent twice")
	}
	if _, err := s.CheckLoginTicket(ctx, tk, TicketVerify); !errors.Is(err, ErrTicket) {
		t.Errorf("spent ticket checked fine: %v", err)
	}

	// Expiry.
	tk, _ = s.IssueLoginTicket(ctx, u.ID, TicketVerify)
	c.add(TicketTTL - time.Second)
	if _, err := s.CheckLoginTicket(ctx, tk, TicketVerify); err != nil {
		t.Errorf("just inside five minutes: %v", err)
	}
	c.add(time.Second)
	if _, err := s.CheckLoginTicket(ctx, tk, TicketVerify); !errors.Is(err, ErrTicket) {
		t.Errorf("expired: %v", err)
	}

	// A password change kills outstanding tickets.
	tk, _ = s.IssueLoginTicket(ctx, u.ID, TicketVerify)
	if err := s.SetPassword(ctx, u.ID, "password2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CheckLoginTicket(ctx, tk, TicketVerify); !errors.Is(err, ErrTicket) {
		t.Errorf("after a password change: %v", err)
	}
	// So does disabling the account.
	tk, _ = s.IssueLoginTicket(ctx, u.ID, TicketVerify)
	if err := s.SetDisabled(ctx, u.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CheckLoginTicket(ctx, tk, TicketVerify); !errors.Is(err, ErrTicket) {
		t.Errorf("disabled account: %v", err)
	}
	// And it is no session.
	if _, err := s.ValidateSession(ctx, tk); err == nil {
		t.Error("a ticket validated as a session")
	}
	if _, err := s.ValidateAPIKey(ctx, tk); err == nil {
		t.Error("a ticket validated as an API key")
	}
}

// A trusted device skips the code for its own account only, for thirty days, and is
// forgotten by a password change, an admin's sign-out-everywhere, or disabling.
func TestTrustedDevices(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	c := withClock(s, time.Unix(1_700_000_010, 0))
	u, _ := s.CreateUser(ctx, "mum@example.com", "password1", RoleRequester, false)
	other, _ := s.CreateUser(ctx, "dad@example.com", "password1", RoleRequester, false)

	tok, exp, err := s.TrustDevice(ctx, u.ID)
	if err != nil || exp.Sub(c.now()) != TrustedDeviceTTL {
		t.Fatalf("trust: %v (expires in %v)", err, exp.Sub(c.now()))
	}
	if !s.DeviceTrusted(ctx, u.ID, tok) || s.DeviceTrusted(ctx, other.ID, tok) || s.DeviceTrusted(ctx, u.ID, "") {
		t.Error("trust isn't tied to its own account")
	}
	var stored int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM user_trusted_devices WHERE token_hash = ?`, tok).Scan(&stored)
	if stored != 0 {
		t.Error("the raw trust token is stored")
	}
	c.add(TrustedDeviceTTL)
	if s.DeviceTrusted(ctx, u.ID, tok) {
		t.Error("trusted past thirty days")
	}

	// Forgetting keeps the browser asking, if given.
	here, _, _ := s.TrustDevice(ctx, u.ID)
	there, _, _ := s.TrustDevice(ctx, u.ID)
	if n, err := s.ForgetTrustedDevices(ctx, u.ID, here); err != nil || n != 1 {
		t.Errorf("forget others: %d %v", n, err)
	}
	if !s.DeviceTrusted(ctx, u.ID, here) || s.DeviceTrusted(ctx, u.ID, there) {
		t.Error("forgot the wrong device")
	}

	for name, forget := range map[string]func() error{
		"change password": func() error { _, err := s.ChangePassword(ctx, u.ID, "password9", ""); return err },
		"admin reset":     func() error { return s.SetPassword(ctx, u.ID, "password8") },
		"sign out everywhere": func() error {
			_, err := s.RevokeUserSessions(ctx, u.ID)
			return err
		},
		"disable": func() error { return s.SetDisabled(ctx, u.ID, true) },
	} {
		_ = s.SetDisabled(ctx, u.ID, false)
		tok, _, _ := s.TrustDevice(ctx, u.ID)
		if err := forget(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if s.DeviceTrusted(ctx, u.ID, tok) {
			t.Errorf("%s kept a trusted device", name)
		}
	}
}

// A merged duplicate's two-factor goes with it (the preview says so); the target's stays.
func TestMergeDropsDuplicateTwoFactor(t *testing.T) {
	ctx := context.Background()
	f := newMergeFixture(t)
	c := withClock(f.s, time.Unix(1_700_000_010, 0))
	enrol(t, f.s, c, f.dup.ID)
	if _, _, err := f.s.TrustDevice(ctx, f.dup.ID); err != nil {
		t.Fatal(err)
	}
	enrol(t, f.s, c, f.admin.ID)

	p, err := f.s.MergePreview(ctx, f.admin.ID, f.dup.ID)
	if err != nil || !p.TwoFactor {
		t.Fatalf("preview two_factor = %v (%v)", p.TwoFactor, err)
	}
	if err := f.s.MergeDuplicate(ctx, f.admin.ID, f.dup.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"user_totp", "user_recovery_codes", "user_trusted_devices"} {
		if n := f.count(t, `SELECT COUNT(*) FROM `+table+` WHERE user_id = ?`, f.dup.ID); n != 0 {
			t.Errorf("%s: %d of the duplicate's rows left", table, n)
		}
	}
	if st, _ := f.s.TwoFactor(ctx, f.admin.ID); !st.Enabled || st.RecoveryCodesLeft != RecoveryCodeCount {
		t.Errorf("the target's own two-factor changed: %+v", st)
	}
	// A duplicate without it previews false.
	g := newMergeFixture(t)
	if p, _ := g.s.MergePreview(ctx, g.admin.ID, g.dup.ID); p.TwoFactor {
		t.Error("preview says two-factor for a duplicate without it")
	}
}
