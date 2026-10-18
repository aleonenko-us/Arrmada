package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/netutil"
	"github.com/tristenlammi/arrmada/internal/opds"
	"github.com/tristenlammi/arrmada/internal/requests"
)

// The OPDS catalogue for e-reader apps (internal/opds) lives at /opds. Its routes are
// public and reachable from outside the network as far as the router is concerned: the
// catalogue signs every request in itself, with HTTP Basic and the person's audiobook
// password. Wrong passwords go through the same limiter as Arrmada's own sign-in, under
// keys of their own.

// registerOPDS adds the catalogue's routes, each by its own pattern so the request log
// names "GET /opds/books/{id}/file", never the book.
func (a *api) registerOPDS(mux *router) {
	d := opds.Deps{
		Throttle:  opdsThrottle{a},
		Enabled:   a.booksEnabled,
		Requested: a.opdsRequested,
		BaseURL:   baseURL,
		Log:       a.deps.Log,
	}
	// Interfaces only when the service is there: a nil pointer in an interface isn't nil,
	// and the catalogue answers 404 when either is missing.
	if a.deps.Books != nil {
		d.Library = a.deps.Books
	}
	if a.deps.Kepub != nil {
		d.Kepub = a.deps.Kepub
	}
	if a.deps.AudioServer != nil {
		d.Auth = a.deps.AudioServer.Accounts
		srv := a.deps.AudioServer
		d.Cover = func(w http.ResponseWriter, r *http.Request, b books.Book) {
			srv.ServeBookCover(w, r, b, "private, max-age=86400")
		}
	}
	for _, rt := range opds.New(d).Routes() {
		mux.HandleFunc(rt.Pattern, a.public(rt.Handler).ext())
	}
}

// opdsRequested is the caller's own book requests, as My shelf reads them: the library
// row each is linked to, else the catalogue key it was made under.
func (a *api) opdsRequested(ctx context.Context, userID int64) (map[int64]bool, map[string]bool, error) {
	ids, keys := map[int64]bool{}, map[string]bool{}
	if a.deps.Requests == nil {
		return ids, keys, nil
	}
	reqs, _, err := a.deps.Requests.List(ctx, requests.ListFilter{UserID: userID, MediaType: "book"})
	if err != nil {
		return nil, nil, err
	}
	for _, rq := range reqs {
		switch {
		case rq.MediaType != "book":
		case rq.BookID > 0:
			ids[rq.BookID] = true
		case rq.OLKey != "":
			keys[rq.OLKey] = true
		}
	}
	return ids, keys, nil
}

// baseURL is the scheme and host the request reached us on, for the OpenSearch template.
func baseURL(r *http.Request) string {
	scheme := "http"
	if requestIsHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// opdsThrottle applies Arrmada's login limits to e-reader sign-ins: 10 wrong passwords
// per address in 15 minutes, and from outside the LAN a growing pause per username after
// 5 in an hour. The keys are the catalogue's own, so a right audiobook password never
// clears failures recorded against someone's Arrmada password.
type opdsThrottle struct{ a *api }

func opdsKeys(r *http.Request, username string) (ip, name, ipKey, userKey string) {
	ip = netutil.ClientIP(r)
	name = strings.ToLower(strings.TrimSpace(username))
	return ip, name, "opds:" + ip, "opds-user:" + name
}

func (t opdsThrottle) Wait(r *http.Request, username string) (bool, time.Duration) {
	l := t.a.loginLimiter
	if l == nil {
		return false, 0
	}
	_, _, ipKey, userKey := opdsKeys(r, username)
	if ok, retry := l.blocked(ipKey); ok {
		return true, retry
	}
	if t.a.classifyExternal(r) {
		return l.delayed(userKey)
	}
	return false, 0
}

func (t opdsThrottle) Failed(r *http.Request, username string) {
	l := t.a.loginLimiter
	if l == nil {
		return
	}
	ip, name, ipKey, userKey := opdsKeys(r, username)
	l.fail(ipKey, name)
	if n := l.fail(userKey, name); n >= loginAlertAt && n%loginAlertAt == 0 {
		t.a.loginFailuresAlert(name, n, ip)
	}
}

func (t opdsThrottle) Succeeded(r *http.Request, username string) {
	l := t.a.loginLimiter
	if l == nil {
		return
	}
	_, name, ipKey, userKey := opdsKeys(r, username)
	l.reset(userKey, "")
	l.reset(ipKey, name)
}
