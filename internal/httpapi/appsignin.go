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
// Arrmada password (those keys are the main sign-in's own). An IPv6 address counts by its
// /64 (addrKey).
//
//   - One password check at a time per address and per username (turnGate), so guesses
//     sent all at once are counted before the next one is let through.
//   - Per address: 10 wrong passwords in 15 minutes, then 429 until they age out.
//   - Per username, from outside the LAN: after 5 wrong in an hour, a pause that doubles
//     from 30 seconds (capped at 15 minutes) after the last one; every 20 wrong raise the
//     security.login_failures alert.
//   - The same wrong password again from the same address counts once (wrongSeen): a
//     device still sending an old password mustn't lock its household out.
//
// The username pause is checked before the password, so on its own it let anyone who knew
// a username keep that person's e-reader locked out by guessing. An address the person
// has signed in from with the right password in the last 30 days (knownAddrs) is let past
// the pause: their own devices carry on, while a guesser elsewhere still waits. For the
// same reason a right password doesn't clear the username's failures (a reader polling
// the catalogue would otherwise wipe out the record of guessing from elsewhere); only that
// username's failures leave the address's own record.
//
// "Let an address with no recent failures through" was the other way to do it, but a
// guesser using a new address for every try would have had no username pause at all.
type appSignIn struct{ a *api }

type appKeys struct {
	ip, name, addr, ipKey, userKey string
}

func appSignInKeys(r *http.Request, username string) appKeys {
	ip := netutil.ClientIP(r)
	addr := addrKey(ip)
	name := strings.ToLower(strings.TrimSpace(username))
	return appKeys{ip: ip, name: name, addr: addr, ipKey: "app:" + addr, userKey: "app-user:" + name}
}

// Turn waits for this sign-in's turn, then says whether it must wait instead of having its
// password checked (wait, for retry). When it may go ahead, done must be called once the
// answer is recorded (Failed or Succeeded) or the check gave up.
func (t appSignIn) Turn(r *http.Request, username string) (done func(), wait bool, retry time.Duration) {
	l := t.a.loginLimiter
	if l == nil {
		return func() {}, false, 0
	}
	k := appSignInKeys(r, username)
	release, ok := t.a.turns.acquire(r.Context(), k.ipKey, k.userKey)
	if !ok {
		return nil, true, time.Second
	}
	if blocked, retry := l.blocked(k.ipKey); blocked {
		release()
		return nil, true, retry
	}
	if t.a.classifyExternal(r) && !t.a.appKnown.isKnown(k.name, k.addr) {
		if delayed, retry := l.delayed(k.userKey); delayed {
			release()
			return nil, true, retry
		}
	}
	return release, false, 0
}

// Failed records a wrong password for username from r: once per distinct password.
func (t appSignIn) Failed(r *http.Request, username, password string) {
	l := t.a.loginLimiter
	if l == nil {
		return
	}
	k := appSignInKeys(r, username)
	if t.a.appWrong.repeat(k.ipKey+"\x00"+k.name, password) {
		return
	}
	l.fail(k.ipKey, k.name)
	if n := l.fail(k.userKey, k.name); n >= loginAlertAt && n%loginAlertAt == 0 {
		t.a.loginFailuresAlert(k.name, n, k.ip)
	}
}

// Succeeded records a right password for username from r.
func (t appSignIn) Succeeded(r *http.Request, username string) {
	l := t.a.loginLimiter
	if l == nil {
		return
	}
	k := appSignInKeys(r, username)
	l.reset(k.ipKey, k.name)
	t.a.appKnown.remember(k.name, k.addr)
}
