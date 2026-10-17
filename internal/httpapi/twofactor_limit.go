package httpapi

import (
	"sync"
	"time"
)

// codeLimiter limits two-factor code attempts per account. Only someone who already knows
// the password gets to type codes, so these limits are what stand between them and
// guessing one of the million codes.
//
// Every attempt reserves its failure before the code is checked, under one lock, and gives
// it back only if the code was right: a burst of simultaneous guesses can't all pass the
// check before any of them is counted. One attempt per account at a time; a second that
// arrives while one is being checked is turned away (429) rather than queued.
//
// The limits: ten wrong codes in fifteen minutes block the account's code step until the
// oldest ages out, and after five in the hour each further try waits 30s, 1m, 2m … (capped
// at 15m) from the last wrong one. A right code doesn't wipe the record: it forgives the
// older half of it, so an owner who signs in between someone else's guesses doesn't hand
// them a fresh set of free tries. The rest ages out after an hour.
//
// The record is in memory only: a restart forgets it, as it forgets the password limits.
type codeLimiter struct {
	mu    sync.Mutex
	accts map[int64]*codeAccount
	now   func() time.Time
}

type codeAccount struct {
	fails    []time.Time // wrong codes (and the attempt in flight), oldest first, within codeMemory
	inflight bool
}

const (
	codeWindow    = 15 * time.Minute
	codeMax       = 10
	codeMemory    = time.Hour
	codeFree      = 5
	codeDelayBase = 30 * time.Second
	codeDelayCap  = 15 * time.Minute
	// codeBusyRetry is the Retry-After for an attempt turned away because another one for
	// the same account is being checked.
	codeBusyRetry = time.Second
)

// codeOutcome is how a checked attempt ended.
type codeOutcome int

const (
	codeWrong   codeOutcome = iota // a wrong code: the reserved failure stands
	codeRight                      // a right code: the reservation is given back
	codeNeutral                    // no answer about the code (expired ticket, an error)
)

func newCodeLimiter() *codeLimiter {
	return &codeLimiter{accts: map[int64]*codeAccount{}, now: time.Now}
}

// prune drops failures older than codeMemory. Called with mu held.
func (c *codeLimiter) prune(a *codeAccount, now time.Time) {
	i := 0
	for i < len(a.fails) && now.Sub(a.fails[i]) >= codeMemory {
		i++
	}
	a.fails = append([]time.Time(nil), a.fails[i:]...)
}

// begin reserves an attempt for userID. It refuses, with how long to wait, while another
// attempt for the account is in flight, while the account is blocked, or during its pause.
// On ok the caller must call end exactly once.
func (c *codeLimiter) begin(userID int64) (ok bool, retry time.Duration) {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	a := c.accts[userID]
	if a == nil {
		a = &codeAccount{}
		c.accts[userID] = a
	}
	if a.inflight {
		return false, codeBusyRetry
	}
	c.prune(a, now)
	var recent []time.Time
	for _, t := range a.fails {
		if now.Sub(t) < codeWindow {
			recent = append(recent, t)
		}
	}
	if len(recent) >= codeMax {
		return false, recent[len(recent)-codeMax].Add(codeWindow).Sub(now)
	}
	if n := len(a.fails); n >= codeFree {
		wait := codeDelayBase
		for i := codeFree; i < n && wait < codeDelayCap; i++ {
			wait *= 2
		}
		wait = min(wait, codeDelayCap)
		if until := a.fails[n-1].Add(wait); now.Before(until) {
			return false, until.Sub(now)
		}
	}
	a.fails = append(a.fails, now)
	a.inflight = true
	return true, 0
}

// end finishes userID's attempt and says how many wrong codes the account has had in the
// last hour.
func (c *codeLimiter) end(userID int64, out codeOutcome) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	a := c.accts[userID]
	if a == nil {
		return 0
	}
	a.inflight = false
	if out != codeWrong && len(a.fails) > 0 {
		// Give back this attempt's reservation (the newest entry).
		a.fails = a.fails[:len(a.fails)-1]
	}
	if out == codeRight {
		// Forgive the older half; the rest still counts, and ages out on its own.
		a.fails = append([]time.Time(nil), a.fails[len(a.fails)/2:]...)
	}
	n := len(a.fails)
	if n == 0 {
		delete(c.accts, userID)
	}
	return n
}
