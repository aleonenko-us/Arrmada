package httpapi

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rsc.io/qr"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/netutil"
	"github.com/tristenlammi/arrmada/internal/notify"
	"github.com/tristenlammi/arrmada/internal/settings"
)

// Two-factor sign-in (TOTP). Signing in with a password on an account that has it on
// answers {two_factor: "code", ticket} instead of a session; the sign-in page trades the
// ticket and a code for the session at POST /auth/login/code. An admin who must use it
// (the "Require two-factor for admins" setting) and hasn't set it up gets
// {two_factor: "setup", ticket} and sets it up before any session exists.
//
// The ticket is never a cookie and nothing that checks sessions reads it (auth/ticket.go).
// API keys and the audiobook apps never reach any of this: they don't use the Arrmada
// password. Plex and Jellyfin sign-in into a staff account does (finishExternalSignIn).

// keyTwoFactorAdmins is the "Require two-factor for admins" setting.
const keyTwoFactorAdmins = settings.KeyTwoFactorAdmins

// trustCookieName is the "trust this device for 30 days" cookie (auth/trust.go).
const trustCookieName = "arrmada_trusted"

// twoFactorAlertAt is how many wrong codes for one account within the hour log a warning
// (again at each further multiple): whoever is typing them knows the password.
const twoFactorAlertAt = 5

// twoFactorRequired reports whether u must use two-factor sign-in: an admin, while the
// admin policy is on.
func (a *api) twoFactorRequired(ctx context.Context, u *auth.User) bool {
	return u != nil && u.Role == auth.RoleAdmin && a.deps.Settings != nil && a.deps.Settings.GetBool(ctx, keyTwoFactorAdmins, false)
}

// finishPasswordSignIn is the last step of a right password: a session, unless the account
// needs a code (or, under the admin policy, must set two-factor up) first.
func (a *api) finishPasswordSignIn(w http.ResponseWriter, r *http.Request, u *auth.User) {
	ctx := r.Context()
	on, err := a.deps.Auth.TwoFactorEnabled(ctx, u.ID)
	if err != nil {
		a.deps.Log.Error("auth: couldn't read two-factor state", "user_id", u.ID, "err", err)
		a.writeError(w, http.StatusInternalServerError, "authentication failed")
		return
	}
	purpose := auth.TicketPurpose("")
	switch {
	case on && a.deps.Auth.DeviceTrusted(ctx, u.ID, trustToken(r)):
		// A browser trusted after an earlier code: the password is enough here.
	case on:
		purpose = auth.TicketVerify
	case a.twoFactorRequired(ctx, u):
		purpose = auth.TicketEnrol
	}
	if purpose == "" {
		a.startSession(w, r, u, http.StatusOK)
		return
	}
	ticket, err := a.deps.Auth.IssueLoginTicket(ctx, u.ID, purpose)
	if err != nil {
		a.deps.Log.Error("auth: couldn't make a sign-in ticket", "user_id", u.ID, "err", err)
		a.writeError(w, http.StatusInternalServerError, "authentication failed")
		return
	}
	step := "code"
	if purpose == auth.TicketEnrol {
		step = "setup"
	}
	// Deliberately no user: nothing about the account is handed out before the code.
	a.writeJSON(w, http.StatusOK, map[string]any{"two_factor": step, "ticket": ticket, "expires_in": int(auth.TicketTTL.Seconds())})
}

// finishExternalSignIn is the last step of a Plex or Jellyfin sign-in. A requester gets the
// session straight away: the media server has said who they are. A staff account (only
// reachable when the owner allowed staff sign-in that way) goes through the same step as a
// right password — the code when two-factor is on, setting it up first when "Require
// two-factor for admins" applies — or taking over someone's Plex or Jellyfin account would
// skip the code that guards the staff console.
func (a *api) finishExternalSignIn(w http.ResponseWriter, r *http.Request, u *auth.User) {
	if u.Role.AtLeast(auth.RoleManager) {
		a.finishPasswordSignIn(w, r, u)
		return
	}
	a.startSession(w, r, u, http.StatusOK)
}

// checkCode runs check (a code verification for u) under the code limits: the address's
// failures, shared with wrong passwords (ten in fifteen minutes), and the account's own
// (twofactor_limit.go: reserved before the check, so simultaneous guesses can't slip
// through, and one attempt per account at a time). It returns check's error, or ok=false
// when the attempt was turned away (the 429 is already written). Unlike the password's
// per-name pause, the account's limits apply on the LAN too: only someone who knows the
// password gets this far.
func (a *api) checkCode(w http.ResponseWriter, r *http.Request, u *auth.User, check func() error) (ok bool, err error) {
	ip := netutil.ClientIP(r)
	if l := a.loginLimiter; l != nil {
		if blocked, retry := l.blocked("login:" + ip); blocked {
			a.tooManyAttempts(w, retry)
			return false, nil
		}
	}
	if allowed, retry := a.codeLimit.begin(u.ID); !allowed {
		a.tooManyAttempts(w, retry)
		return false, nil
	}
	out := codeNeutral
	defer func() {
		n := a.codeLimit.end(u.ID, out)
		switch out {
		case codeWrong:
			a.codeFailed(r, u, ip, n)
		case codeRight:
			if l := a.loginLimiter; l != nil {
				l.reset("login:"+ip, strings.ToLower(u.Username))
			}
		}
	}()
	err = check()
	switch {
	case err == nil:
		out = codeRight
	case errors.Is(err, auth.ErrBadCode):
		out = codeWrong
	}
	return true, err
}

// codeFailed records a wrong code against the address, and at every twoFactorAlertAt
// wrong codes in the hour for one account warns: its password was right each time, so
// someone else probably knows it. The warning goes to the log, the staff's live feed
// (security.two_factor_failures, like security.login_failures) and the alert catalog
// (once per account, hour and count).
func (a *api) codeFailed(r *http.Request, u *auth.User, ip string, n int) {
	if l := a.loginLimiter; l != nil {
		l.fail("login:"+ip, strings.ToLower(u.Username))
	}
	if n == 0 || n%twoFactorAlertAt != 0 {
		return
	}
	a.deps.Log.Warn("many wrong two-factor codes for one account: its password is right, so someone may know it",
		"user_id", u.ID, "failures_last_hour", n, "last_ip", ip)
	if a.deps.Bus != nil {
		a.deps.Bus.Publish(notify.EventTwoFactorFailures, map[string]any{"user_id": u.ID, "count": n})
	}
	if a.deps.Notify != nil {
		dedupe := fmt.Sprintf("%s:%d:%d:%d", notify.EventTwoFactorFailures, u.ID, time.Now().Unix()/3600, n)
		if _, err := a.deps.Notify.EmitOnce(r.Context(), notify.EventTwoFactorFailures, dedupe, map[string]any{"count": n}); err != nil {
			a.deps.Log.Warn("couldn't send the wrong two-factor codes alert", "err", err)
		}
	}
}

// writeCodeError answers a failed code check.
func (a *api) writeCodeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrBadCode):
		a.writeError(w, http.StatusUnauthorized, "That code isn't right. Check the time on your phone, or use a recovery code.")
	// ErrTwoFactorOn: a setup ticket for an account that turned it on meanwhile (another
	// tab finished first).
	case errors.Is(err, auth.ErrTicket), errors.Is(err, auth.ErrTwoFactorOff), errors.Is(err, auth.ErrTwoFactorSetup), errors.Is(err, auth.ErrTwoFactorOn):
		// reason tells the sign-in page to go back to the password rather than ask again.
		a.writeJSON(w, http.StatusUnauthorized, map[string]string{"status": "error", "message": auth.ErrTicket.Error(), "reason": "ticket"})
	default:
		a.deps.Log.Error("auth: two-factor check failed", "err", err)
		a.writeError(w, http.StatusInternalServerError, "authentication failed")
	}
}

// handleLoginCode — POST /api/v1/auth/login/code {ticket, code, trust_device}: the second
// half of a two-factor sign-in. A right authenticator code (or an unused recovery code)
// makes the session; trust_device also remembers this browser for 30 days.
func (a *api) handleLoginCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Ticket      string `json:"ticket"`
		Code        string `json:"code"`
		TrustDevice bool   `json:"trust_device"`
	}
	if !a.decodeJSON(w, r, &body) {
		return
	}
	ctx := r.Context()
	u, err := a.deps.Auth.CheckLoginTicket(ctx, body.Ticket, auth.TicketVerify)
	if err != nil {
		a.writeCodeError(w, err)
		return
	}
	var recovery bool
	ok, err := a.checkCode(w, r, u, func() (err error) {
		recovery, err = a.deps.Auth.VerifySecondFactor(ctx, u.ID, body.Code)
		return err
	})
	if !ok {
		return
	}
	if err != nil {
		a.writeCodeError(w, err)
		return
	}
	if !a.deps.Auth.SpendLoginTicket(body.Ticket) {
		a.writeCodeError(w, auth.ErrTicket)
		return
	}
	if recovery {
		left := 0
		if st, err := a.deps.Auth.TwoFactor(ctx, u.ID); err == nil {
			left = st.RecoveryCodesLeft
		}
		a.deps.Log.Info("auth: signed in with a recovery code", "user_id", u.ID, "recovery_codes_left", left)
	}
	if body.TrustDevice {
		if tok, exp, err := a.deps.Auth.TrustDevice(ctx, u.ID); err == nil {
			a.setTrustCookie(w, r, tok, exp)
		}
	}
	a.startSession(w, r, u, http.StatusOK)
}

// handleLoginSetup — POST /api/v1/auth/login/setup {ticket}: an admin the policy requires
// to use two-factor starts setting it up, with the ticket from their right password and
// no session yet. Answers the secret and its QR code, shown once.
func (a *api) handleLoginSetup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Ticket string `json:"ticket"`
	}
	if !a.decodeJSON(w, r, &body) {
		return
	}
	u, err := a.deps.Auth.CheckLoginTicket(r.Context(), body.Ticket, auth.TicketEnrol)
	if err == nil {
		// Set up meanwhile (another tab): this ticket has nothing left to do.
		if on, onErr := a.deps.Auth.TwoFactorEnabled(r.Context(), u.ID); onErr != nil || on {
			err = cmp.Or(onErr, auth.ErrTwoFactorOn)
		}
	}
	if err != nil {
		a.writeCodeError(w, err)
		return
	}
	a.beginTOTP(w, r, u)
}

// handleLoginSetupConfirm — POST /api/v1/auth/login/setup/confirm {ticket, code}: the
// first code from the new secret turns two-factor on and makes the session. Answers the
// recovery codes, shown once.
func (a *api) handleLoginSetupConfirm(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Ticket string `json:"ticket"`
		Code   string `json:"code"`
	}
	if !a.decodeJSON(w, r, &body) {
		return
	}
	ctx := r.Context()
	u, err := a.deps.Auth.CheckLoginTicket(ctx, body.Ticket, auth.TicketEnrol)
	if err != nil {
		a.writeCodeError(w, err)
		return
	}
	var codes []string
	ok, err := a.checkCode(w, r, u, func() (err error) {
		codes, err = a.deps.Auth.ConfirmTOTP(ctx, u.ID, body.Code)
		return err
	})
	if !ok {
		return
	}
	if err != nil {
		a.writeCodeError(w, err)
		return
	}
	if !a.deps.Auth.SpendLoginTicket(body.Ticket) {
		a.writeCodeError(w, auth.ErrTicket)
		return
	}
	a.deps.Log.Info("account: two-factor sign-in turned on at sign-in (required for admins)", "user_id", u.ID)
	token, expires, err := a.deps.Auth.CreateSessionFrom(ctx, u.ID, auth.SessionClient{UserAgent: r.UserAgent(), IP: netutil.ClientIP(r)})
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	a.setSessionCookie(w, r, token, expires)
	w.Header().Set("Cache-Control", "no-store") // the recovery codes, shown once
	a.writeJSON(w, http.StatusOK, map[string]any{"user": u, "recovery_codes": codes})
}

// beginTOTP answers a new secret for u: its text, the otpauth link, and the QR code as an
// SVG path (dark modules) over a size×size grid.
func (a *api) beginTOTP(w http.ResponseWriter, r *http.Request, u *auth.User) {
	setup, err := a.deps.Auth.BeginTOTP(r.Context(), u.ID)
	if errors.Is(err, auth.ErrTwoFactorOn) {
		a.writeError(w, http.StatusConflict, "Two-factor sign-in is already on. Turn it off first to set it up again.")
		return
	}
	if err != nil {
		a.deps.Log.Error("account: couldn't start two-factor setup", "user_id", u.ID, "err", err)
		a.writeError(w, http.StatusInternalServerError, "could not start two-factor setup")
		return
	}
	size, path, err := qrPath(setup.URI)
	if err != nil {
		a.deps.Log.Error("account: couldn't draw the two-factor QR code", "user_id", u.ID, "err", err)
		a.writeError(w, http.StatusInternalServerError, "could not start two-factor setup")
		return
	}
	// The setup secret must not be cached anywhere on the way back.
	w.Header().Set("Cache-Control", "no-store")
	a.writeJSON(w, http.StatusOK, map[string]any{
		"secret": setup.Secret, "uri": setup.URI,
		"qr": map[string]any{"size": size, "path": path},
	})
}

// qrPath draws text as a QR code: the grid's size and an SVG path with one rectangle per
// run of dark modules in a row. The page adds the quiet zone and colours.
func qrPath(text string) (int, string, error) {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return 0, "", err
	}
	var b strings.Builder
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; {
			if !c.Black(x, y) {
				x++
				continue
			}
			start := x
			for x < c.Size && c.Black(x, y) {
				x++
			}
			n := strconv.Itoa(x - start)
			b.WriteString("M" + strconv.Itoa(start) + " " + strconv.Itoa(y) + "h" + n + "v1h-" + n + "z")
		}
	}
	return c.Size, b.String(), nil
}

// --- the Me page ---

// handleMyTwoFactor — GET /api/v1/me/2fa: whether it's on, how many recovery codes are
// left, whether the account has a password (it's what two-factor protects) and whether
// the admin policy requires it. Never the secret.
func (a *api) handleMyTwoFactor(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u == nil {
		a.writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	ctx := r.Context()
	st, err := a.deps.Auth.TwoFactor(ctx, u.ID)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not load two-factor sign-in")
		return
	}
	has, err := a.deps.Auth.HasPassword(ctx, u.ID)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not load two-factor sign-in")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"enabled": st.Enabled, "enabled_at": st.EnabledAt, "recovery_codes_left": st.RecoveryCodesLeft,
		"password_set": has, "required": a.twoFactorRequired(ctx, u),
	})
}

// checkMyPassword checks the caller's current password under the same per-account limit
// as changing it. It writes the answer and returns false when it's wrong or limited.
func (a *api) checkMyPassword(w http.ResponseWriter, r *http.Request, u *auth.User, password string) bool {
	key := "pw:" + strconv.FormatInt(u.ID, 10)
	if l := a.loginLimiter; l != nil {
		if blocked, retry := l.blocked(key); blocked {
			a.tooManyAttempts(w, retry)
			return false
		}
	}
	if !a.deps.Auth.CheckPassword(r.Context(), u.ID, password) {
		if l := a.loginLimiter; l != nil {
			l.fail(key, "")
		}
		a.writeError(w, http.StatusBadRequest, "Your password is wrong")
		return false
	}
	if l := a.loginLimiter; l != nil {
		l.reset(key, "")
	}
	return true
}

// checkMyCode checks a current authenticator or recovery code for the caller, under the
// sign-in code limits. It writes the answer and returns false when it's wrong or limited.
func (a *api) checkMyCode(w http.ResponseWriter, r *http.Request, u *auth.User, code string) bool {
	ok, err := a.checkCode(w, r, u, func() error {
		_, err := a.deps.Auth.VerifySecondFactor(r.Context(), u.ID, code)
		return err
	})
	if !ok {
		return false
	}
	if err != nil {
		if errors.Is(err, auth.ErrBadCode) {
			a.writeError(w, http.StatusBadRequest, "That code isn't right")
			return false
		}
		if errors.Is(err, auth.ErrTwoFactorOff) {
			a.writeError(w, http.StatusConflict, "Two-factor sign-in isn't on")
			return false
		}
		a.writeError(w, http.StatusInternalServerError, "could not check the code")
		return false
	}
	return true
}

// handleMyTwoFactorSetup — POST /api/v1/me/2fa/setup {password}: starts setting up
// two-factor sign-in for the caller. The password is asked for so a session left open
// can't tie the account to someone else's phone. An account nobody knows a password for
// (Plex or Jellyfin sign-in only) has nothing for it to protect.
func (a *api) handleMyTwoFactorSetup(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u == nil {
		a.writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !a.decodeJSON(w, r, &body) {
		return
	}
	has, err := a.deps.Auth.HasPassword(r.Context(), u.ID)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not start two-factor setup")
		return
	}
	if !has {
		a.writeError(w, http.StatusBadRequest, "Two-factor sign-in protects signing in with a password, and this account signs in with Plex or Jellyfin. Set a password first.")
		return
	}
	if !a.checkMyPassword(w, r, u, body.Password) {
		return
	}
	a.beginTOTP(w, r, u)
}

// handleMyTwoFactorConfirm — POST /api/v1/me/2fa/confirm {code}: the first code from the
// new secret turns two-factor on. Answers the recovery codes, shown once. Sessions already
// signed in stay signed in; the page offers to sign the others out.
func (a *api) handleMyTwoFactorConfirm(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u == nil {
		a.writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if !a.decodeJSON(w, r, &body) {
		return
	}
	var codes []string
	ok, err := a.checkCode(w, r, u, func() (err error) {
		codes, err = a.deps.Auth.ConfirmTOTP(r.Context(), u.ID, body.Code)
		return err
	})
	if !ok {
		return
	}
	switch {
	case errors.Is(err, auth.ErrBadCode):
		a.writeError(w, http.StatusBadRequest, "That code isn't right. Check the time on your phone and try the newest code.")
		return
	case errors.Is(err, auth.ErrTwoFactorOn):
		a.writeError(w, http.StatusConflict, "Two-factor sign-in is already on")
		return
	case errors.Is(err, auth.ErrTwoFactorSetup):
		a.writeError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		a.writeError(w, http.StatusInternalServerError, "could not turn on two-factor sign-in")
		return
	}
	a.deps.Log.Info("account: two-factor sign-in turned on by its owner", "user_id", u.ID)
	w.Header().Set("Cache-Control", "no-store") // the recovery codes, shown once
	a.writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

// handleMyTwoFactorDisable — POST /api/v1/me/2fa/disable {password, code}: turns it off.
// Both the password and a current code (or a recovery code) are needed, so neither a
// session left open nor a known password alone can take it off.
func (a *api) handleMyTwoFactorDisable(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u == nil {
		a.writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	var body struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !a.decodeJSON(w, r, &body) {
		return
	}
	if !a.checkMyPassword(w, r, u, body.Password) || !a.checkMyCode(w, r, u, body.Code) {
		return
	}
	if _, err := a.deps.Auth.DisableTwoFactor(r.Context(), u.ID); err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not turn off two-factor sign-in")
		return
	}
	a.deps.Log.Info("account: two-factor sign-in turned off by its owner", "user_id", u.ID)
	a.writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
}

// handleMyRecoveryCodes — POST /api/v1/me/2fa/recovery-codes {password, code}: a new set
// of recovery codes (the old ones stop working), shown once.
func (a *api) handleMyRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u == nil {
		a.writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	var body struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !a.decodeJSON(w, r, &body) {
		return
	}
	if !a.checkMyPassword(w, r, u, body.Password) || !a.checkMyCode(w, r, u, body.Code) {
		return
	}
	codes, err := a.deps.Auth.RegenerateRecoveryCodes(r.Context(), u.ID)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not make new recovery codes")
		return
	}
	a.deps.Log.Info("account: new two-factor recovery codes made by their owner", "user_id", u.ID)
	w.Header().Set("Cache-Control", "no-store") // the recovery codes, shown once
	a.writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

// handleResetUserTwoFactor — POST /api/v1/users/{id}/2fa/reset (admin): turns off someone's
// two-factor sign-in, for a person who lost their phone and their recovery codes. Their
// password still works; their sessions stay. Recorded in the log: who, by whom.
func (a *api) handleResetUserTwoFactor(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	me, _ := userFrom(r)
	if me != nil && me.ID == id {
		a.writeError(w, http.StatusBadRequest, "Turn your own two-factor sign-in off from your Me page")
		return
	}
	if _, err := a.deps.Auth.UserByID(r.Context(), id); err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) || errors.Is(err, auth.ErrNotFound) {
			a.writeError(w, http.StatusNotFound, "user not found")
			return
		}
		a.writeError(w, http.StatusInternalServerError, "could not load the user")
		return
	}
	was, err := a.deps.Auth.DisableTwoFactor(r.Context(), id)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not reset two-factor sign-in")
		return
	}
	var by int64
	if me != nil {
		by = me.ID
	}
	a.deps.Log.Warn("users: two-factor sign-in reset by an admin", "user_id", id, "by_user_id", by, "was_on", was)
	a.writeJSON(w, http.StatusOK, map[string]any{"reset": was})
}

// trustToken is this browser's trusted-device token ("" when none).
func trustToken(r *http.Request) string {
	if c, err := r.Cookie(trustCookieName); err == nil {
		return c.Value
	}
	return ""
}

// setTrustCookie remembers this browser for two-factor sign-in. It is sent to the API only
// (sign-in reads it; "sign out your other devices" keeps this one's), never to scripts.
func (a *api) setTrustCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     trustCookieName,
		Value:    token,
		Path:     "/api/v1",
		Expires:  expires,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}
