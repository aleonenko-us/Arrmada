package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"sync"
	"time"
)

// A Quick Connect sign-in: Arrmada asks Jellyfin for a code, the person types it into a
// Jellyfin app they're already signed in to, and the request's secret then trades for a
// sign-in. The secret is the credential — Jellyfin keeps honouring an approved one until
// it expires — so it never leaves the server: the browser gets the code to show and a
// random id to poll with. Like a Plex PIN, each request is bound to the browser that
// started it (a cookie) and, for linking, to the account; and its secret is used once and
// forgotten, whoever polls first.

const (
	jfFlowLogin = "login" // Sign in with Jellyfin on the login page
	jfFlowLink  = "link"  // link a Jellyfin user to the signed-in account

	// jfQCTTL matches Jellyfin's own Quick Connect timeout (10 minutes).
	jfQCTTL = 10 * time.Minute
	// jfQCMax bounds the table; starting is throttled per address, so this is a backstop.
	jfQCMax = 500
)

type jfQCEntry struct {
	kind    string
	userID  int64    // 0 for the login flow
	cookie  [32]byte // sha256 of the browser's cookie value
	secret  string   // Jellyfin's Quick Connect secret; never sent anywhere but Jellyfin
	expires time.Time
}

// jfQCGuard holds the Quick Connect requests in flight. In memory on purpose: a restart in
// the middle just means starting again.
type jfQCGuard struct {
	mu  sync.Mutex
	m   map[string]jfQCEntry
	now func() time.Time
}

func newJFQCGuard() *jfQCGuard { return &jfQCGuard{m: map[string]jfQCEntry{}, now: time.Now} }

func jfQCCookieName(kind string) string { return "arrmada_jfqc_" + kind }

func (g *jfQCGuard) add(id string, e jfQCEntry) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	var oldestID string
	var oldest time.Time
	for k, x := range g.m {
		if now.After(x.expires) {
			delete(g.m, k)
			continue
		}
		if oldest.IsZero() || x.expires.Before(oldest) {
			oldestID, oldest = k, x.expires
		}
	}
	if len(g.m) >= jfQCMax && oldestID != "" {
		delete(g.m, oldestID)
	}
	g.m[id] = e
}

// secretFor is the request's secret when this browser (cookie) and account started it
// for kind; "" otherwise.
func (g *jfQCGuard) secretFor(id, kind string, userID int64, cookie string) string {
	if g == nil || cookie == "" || id == "" {
		return ""
	}
	g.mu.Lock()
	e, ok := g.m[id]
	g.mu.Unlock()
	if !ok || e.kind != kind || e.userID != userID || g.now().After(e.expires) {
		return ""
	}
	sum := sha256.Sum256([]byte(cookie))
	if subtle.ConstantTimeCompare(sum[:], e.cookie[:]) != 1 {
		return ""
	}
	return e.secret
}

// claim removes the request and reports whether this caller was the one to remove it, so
// of two polls racing on an approved code only one signs in.
func (g *jfQCGuard) claim(id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.m[id]; !ok {
		return false
	}
	delete(g.m, id)
	return true
}

func (g *jfQCGuard) forget(id string) {
	g.mu.Lock()
	delete(g.m, id)
	g.mu.Unlock()
}

// bindJFQC keeps a fresh Quick Connect secret under a new random id, bound to this
// browser by a short-lived cookie (and to userID for linking). It returns the id.
func (a *api) bindJFQC(w http.ResponseWriter, r *http.Request, kind string, userID int64, secret string) (string, error) {
	if a.jfQC == nil {
		return "", errors.New("quick connect isn't wired")
	}
	idb := make([]byte, 16)
	cb := make([]byte, 24)
	if _, err := rand.Read(idb); err != nil {
		return "", err
	}
	if _, err := rand.Read(cb); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(idb)
	cookie := base64.RawURLEncoding.EncodeToString(cb)
	a.jfQC.add(id, jfQCEntry{kind: kind, userID: userID, cookie: sha256.Sum256([]byte(cookie)), secret: secret, expires: a.jfQC.now().Add(jfQCTTL)})
	http.SetCookie(w, &http.Cookie{
		Name:     jfQCCookieName(kind),
		Value:    cookie,
		Path:     "/api/v1/",
		MaxAge:   int(jfQCTTL / time.Second),
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
	return id, nil
}

// jfQCSecret is the check every poll makes before asking Jellyfin about a request.
func (a *api) jfQCSecret(r *http.Request, kind string, userID int64, id string) string {
	c, err := r.Cookie(jfQCCookieName(kind))
	if err != nil {
		return ""
	}
	return a.jfQC.secretFor(id, kind, userID, c.Value)
}

const errJFQCElsewhere = "This Quick Connect code was started somewhere else, or has expired — start again."
