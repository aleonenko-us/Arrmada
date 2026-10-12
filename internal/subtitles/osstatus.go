package subtitles

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// What OpenSubtitles actually said, rather than whether three settings are filled in:
// the last login (or credentials Test) for the credentials in use now, a short rate-limit
// wait, and the account details a Test reads. Kept in memory: after a restart the state
// is "unverified" until the first login or Test.

// Provider states, for Settings.ProviderState.
const (
	ProviderNotConfigured = "not_configured" // no API key
	ProviderNeedsAccount  = "needs_account"  // a key, but no username/password to download with
	ProviderUnverified    = "unverified"     // credentials set, no login seen yet
	ProviderOK            = "ok"             // a login with these credentials worked
	ProviderAuthFailed    = "auth_failed"    // the key or the login was rejected
	ProviderQuotaSpent    = "quota_spent"    // the daily download allowance is used up
	ProviderRateLimited   = "rate_limited"   // asked to slow down for a few seconds
)

// ProviderStatus is the download source's state for the Subtitles header and Settings.
type ProviderStatus struct {
	State   string // one of the Provider* states
	Detail  string // the state in words: "OpenSubtitles login rejected (401)"
	VIP     bool
	Allowed int // daily downloads the account gets; -1 = unknown
}

// statusReporter is implemented by providers that know more than "configured".
type statusReporter interface{ Status() ProviderStatus }

// ErrRateLimited means the provider asked us to slow down (HTTP 429). It is a wait of
// seconds — never a quota pause.
var ErrRateLimited = errors.New("opensubtitles: rate limited")

// RateLimitError is an ErrRateLimited with how long the provider asked us to wait.
type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("OpenSubtitles rate limit — retry in %s", e.RetryAfter.Round(time.Second))
}

func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

// Rate-limit waits: the provider's Retry-After when it sends one, within these bounds.
const (
	defaultRetryAfter = 10 * time.Second
	maxRetryAfter     = 10 * time.Minute
)

// retryAfter reads how long a 429 asks us to wait: Retry-After (seconds or a date), else
// ratelimit-reset (seconds), else 10 s; never more than 10 minutes.
func retryAfter(h http.Header, now time.Time) time.Duration {
	d := time.Duration(0)
	if v := strings.TrimSpace(h.Get("Retry-After")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			d = time.Duration(n) * time.Second
		} else if t, err := http.ParseTime(v); err == nil {
			d = t.Sub(now)
		}
	}
	if d <= 0 {
		if n, err := strconv.Atoi(strings.TrimSpace(h.Get("Ratelimit-Reset"))); err == nil && n > 0 {
			d = time.Duration(n) * time.Second
		}
	}
	if d <= 0 {
		d = defaultRetryAfter
	}
	return min(d, maxRetryAfter)
}

// verifyGap is how often the credentials Test may really sign in: OpenSubtitles limits
// logins, so a second press inside it gets the first answer again.
const verifyGap = 2 * time.Second

// osState is the provider's live view of the account.
type osState struct {
	mu        sync.Mutex
	authFP    string // accountFingerprint the auth result is for; "" = none seen
	authOK    bool
	authMsg   string
	rateUntil time.Time
	acctFP    string // the account details below are for this fingerprint
	vip       bool
	allowed   int // 0 = unknown (an account always gets at least one download)

	verifyFP     string // the last Test: what it tested, when, and what it said
	verifyAt     time.Time
	verifyDetail string
	verifyErr    error
}

// noteAuth records a login (or Test) result for the credentials in use now.
func (o *OpenSubtitles) noteAuth(ok bool, msg string) {
	fp := o.accountFingerprint()
	o.st.mu.Lock()
	o.st.authFP, o.st.authOK, o.st.authMsg = fp, ok, msg
	o.st.mu.Unlock()
}

// authRejected reports whether the credentials in use now were rejected, and how. A
// change to any of them clears it (the fingerprint no longer matches).
func (o *OpenSubtitles) authRejected() (bool, string) {
	fp := o.accountFingerprint()
	o.st.mu.Lock()
	defer o.st.mu.Unlock()
	if o.st.authFP == fp && !o.st.authOK {
		return true, o.st.authMsg
	}
	return false, ""
}

// ResetAuth forgets the last login result and the cached token, after the credentials
// were saved again: the next job (or Test) signs in fresh.
func (o *OpenSubtitles) ResetAuth() {
	o.st.mu.Lock()
	o.st.authFP, o.st.authOK, o.st.authMsg = "", false, ""
	o.st.verifyFP = ""
	o.st.mu.Unlock()
	o.mu.Lock()
	o.token, o.tokenFor = "", ""
	o.mu.Unlock()
}

// rateLimit records a 429 and returns the error that carries the wait.
func (o *OpenSubtitles) rateLimit(h http.Header) error {
	now := time.Now()
	d := retryAfter(h, now)
	o.st.mu.Lock()
	o.st.rateUntil = now.Add(d)
	o.st.mu.Unlock()
	return &RateLimitError{RetryAfter: d}
}

// noteAccount records what /infos/user said about the account.
func (o *OpenSubtitles) noteAccount(vip bool, allowed int) {
	fp := o.accountFingerprint()
	o.st.mu.Lock()
	o.st.acctFP, o.st.vip, o.st.allowed = fp, vip, allowed
	o.st.mu.Unlock()
}

// PauseReason says why Paused is true, as the outcome a skipped download rung records.
func (o *OpenSubtitles) PauseReason() (Result, string) {
	if bad, msg := o.authRejected(); bad {
		return ResultAuthFailed, msg
	}
	return ResultQuota, "quota spent"
}

// Status is the provider's real state: whether these credentials have signed in, been
// rejected, spent the quota or been told to slow down.
func (o *OpenSubtitles) Status() ProviderStatus {
	st := ProviderStatus{Allowed: -1}
	fp := o.accountFingerprint()
	o.st.mu.Lock()
	authFP, authOK, authMsg, rateUntil := o.st.authFP, o.st.authOK, o.st.authMsg, o.st.rateUntil
	if o.st.acctFP == fp && o.st.allowed > 0 {
		st.VIP, st.Allowed = o.st.vip, o.st.allowed
	}
	o.st.mu.Unlock()
	_, reset := o.Quota()
	switch {
	case o.apiKey() == "":
		st.State, st.Detail = ProviderNotConfigured, "Add an OpenSubtitles API key to search, plus the account's username and password to download."
	case authFP == fp && !authOK:
		st.State, st.Detail = ProviderAuthFailed, authMsg
	case !reset.IsZero():
		st.State, st.Detail = ProviderQuotaSpent, "Daily quota used — resumes "+reset.Local().Format("Jan 2 15:04")+"."
	case time.Now().Before(rateUntil):
		st.State, st.Detail = ProviderRateLimited, fmt.Sprintf("Rate-limited — retrying in %s.", time.Until(rateUntil).Round(time.Second))
	case !o.CanDownload():
		st.State, st.Detail = ProviderNeedsAccount, "Downloading needs the OpenSubtitles username and password too."
	case authFP == fp && authOK:
		st.State, st.Detail = ProviderOK, "Logged in."
	default:
		st.State, st.Detail = ProviderUnverified, "Not checked yet — Test signs in, or the next download will."
	}
	return st
}

// verifyCached returns the last Test's answer when the same credentials were tested
// within verifyGap, so a burst of presses is one login.
func (o *OpenSubtitles) verifyCached(fp string) (detail string, cached bool, err error) {
	o.st.mu.Lock()
	defer o.st.mu.Unlock()
	if o.st.verifyFP == fp && time.Since(o.st.verifyAt) < verifyGap {
		return o.st.verifyDetail, true, o.st.verifyErr
	}
	return "", false, nil
}

func (o *OpenSubtitles) rememberVerify(fp, detail string, err error) {
	o.st.mu.Lock()
	o.st.verifyFP, o.st.verifyAt, o.st.verifyDetail, o.st.verifyErr = fp, time.Now(), detail, err
	o.st.mu.Unlock()
}
