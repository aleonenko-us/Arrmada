package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/netutil"
)

// appSignIn applies Arrmada's sign-in limits to the apps that sign in with the audiobook
// password: e-readers on the OPDS catalogue and listening apps on the audiobook server.
// Both use one set of keys, so a guesser gets no extra tries by switching between them,
// and a right audiobook password never clears failures recorded against someone's
// Arrmada password (those keys are the main sign-in's own).
//
//   - Per address: 10 wrong passwords in 15 minutes, then 429 until they age out.
//   - Per username, from outside the LAN: after 5 wrong in an hour, a pause that doubles
//     from 30 seconds (capped at 15 minutes) after the last one; every 20 wrong raise the
//     security.login_failures alert.
//
// The username pause is checked before the password, so on its own it let anyone who knew
// a username keep that person's e-reader locked out by guessing. An address the person
// has signed in from with the right password in the last 30 days is let past the pause:
// their own devices carry on, while a guesser elsewhere still waits. For the same reason a
// right password doesn't clear the username's failures (a reader polling the catalogue
// every few minutes would otherwise wipe out the record of guessing from elsewhere); only
// that username's failures leave the address's own record.
//
// "Let an address with no recent failures through" was the other way to do it, but a
// guesser using a new address for every try would have had no username pause at all.
type appSignIn struct{ a *api }

func appSignInKeys(r *http.Request, username string) (ip, name, ipKey, userKey, knownKey string) {
	ip = netutil.ClientIP(r)
	name = strings.ToLower(strings.TrimSpace(username))
	return ip, name, "app:" + ip, "app-user:" + name, "app-known:" + name + "\x00" + ip
}

// Wait says whether a sign-in for username from r must wait, and for how long, before its
// password is checked.
func (t appSignIn) Wait(r *http.Request, username string) (bool, time.Duration) {
	l := t.a.loginLimiter
	if l == nil {
		return false, 0
	}
	_, _, ipKey, userKey, knownKey := appSignInKeys(r, username)
	if ok, retry := l.blocked(ipKey); ok {
		return true, retry
	}
	if t.a.classifyExternal(r) && !l.isKnown(knownKey) {
		return l.delayed(userKey)
	}
	return false, 0
}

// Failed records a wrong password for username from r.
func (t appSignIn) Failed(r *http.Request, username string) {
	l := t.a.loginLimiter
	if l == nil {
		return
	}
	ip, name, ipKey, userKey, _ := appSignInKeys(r, username)
	l.fail(ipKey, name)
	if n := l.fail(userKey, name); n >= loginAlertAt && n%loginAlertAt == 0 {
		t.a.loginFailuresAlert(name, n, ip)
	}
}

// Succeeded records a right password for username from r.
func (t appSignIn) Succeeded(r *http.Request, username string) {
	l := t.a.loginLimiter
	if l == nil {
		return
	}
	_, name, ipKey, _, knownKey := appSignInKeys(r, username)
	l.reset(ipKey, name)
	l.remember(knownKey)
}
