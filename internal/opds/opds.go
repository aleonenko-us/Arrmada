// Package opds serves the book library as an OPDS 1.2 catalogue, so e-reader apps
// (KOReader, Moon+ Reader, Librera, Thorium) can browse it and download ebooks straight
// onto the device, the way listening apps reach the audiobooks.
//
// Readers sign in with HTTP Basic on every request: the person's Arrmada username and
// their audiobook password (one password for listening and reading apps, kept by the
// audiobook server). Their Arrmada password is never accepted here. The catalogue shows
// what My shelf shows — every library book that has an ebook — minus anything the
// always-on adult filter catches.
//
// Privacy: nothing here logs a book, a search term or who downloaded what. The request
// log records route patterns ("GET /opds/books/{id}/file"), never the path itself.
package opds

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/tristenlammi/arrmada/internal/audioserver"
	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/books"
)

// Library is the part of the books service the catalogue reads.
type Library interface {
	List(ctx context.Context) ([]books.Book, error)
	Get(ctx context.Context, id int64) (books.Book, error)
	ListAuthors(ctx context.Context) ([]books.Author, error)
	KeyIndex(ctx context.Context, list []books.Book) map[string]books.Book
}

// Authenticator checks a username and audiobook password (audioserver.Accounts). It
// answers audioserver.ErrBadLogin for a wrong pair and audioserver.ErrNoAccess for an
// account that may not connect.
type Authenticator interface {
	Authenticate(ctx context.Context, username, password string) (*auth.User, error)
}

// Throttle is the sign-in limiter, shared with Arrmada's own login. Wait says whether a
// sign-in for username must wait first (and for how long); Failed records a wrong
// password; Succeeded clears that username's failures.
type Throttle interface {
	Wait(r *http.Request, username string) (bool, time.Duration)
	Failed(r *http.Request, username string)
	Succeeded(r *http.Request, username string)
}

// Deps is what the catalogue needs from the rest of the app.
type Deps struct {
	Library Library
	Auth    Authenticator
	// Throttle may be nil (tests): no limit.
	Throttle Throttle
	// Enabled reports whether the Books module is switched on; off, every route is a 404.
	Enabled func(ctx context.Context) bool
	// Requested lists the books behind the caller's own book requests: by library row,
	// and by catalogue key for an older request not linked to one.
	Requested func(ctx context.Context, userID int64) (ids map[int64]bool, keys map[string]bool, err error)
	// Cover sends a book's cover image (or a 404).
	Cover func(w http.ResponseWriter, r *http.Request, b books.Book)
	// BaseURL is the scheme and host the reader reached us on ("https://books.example"),
	// for the one link that must be absolute (the OpenSearch template).
	BaseURL func(r *http.Request) string
	Log     *slog.Logger
	Now     func() time.Time
}

// Handler is the catalogue.
type Handler struct {
	d   Deps
	mux *http.ServeMux // for ServeHTTP
}

// New builds the catalogue.
func New(d Deps) *Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	h := &Handler{d: d, mux: http.NewServeMux()}
	for _, rt := range h.Routes() {
		h.mux.HandleFunc(rt.Pattern, rt.Handler)
	}
	return h
}

// Route is one catalogue route: a ServeMux pattern and its handler. The handler does its
// own sign-in, so the route is registered as public and reachable from outside the LAN.
type Route struct {
	Pattern string
	Handler http.HandlerFunc
}

// Routes lists every route. Each is registered on Arrmada's router by its own pattern so
// the request log names the pattern, never a book id.
func (h *Handler) Routes() []Route {
	root := h.signedIn(h.root)
	return []Route{
		{"GET /opds", root},
		{"GET /opds/opensearch.xml", h.signedIn(h.openSearch)},
		{"GET /opds/new", h.signedIn(h.newest)},
		{"GET /opds/all", h.signedIn(h.all)},
		{"GET /opds/mine", h.signedIn(h.mine)},
		{"GET /opds/authors", h.signedIn(h.authors)},
		{"GET /opds/authors/{id}", h.signedIn(h.author)},
		{"GET /opds/series", h.signedIn(h.seriesList)},
		{"GET /opds/series/{id}", h.signedIn(h.series)},
		{"GET /opds/search", h.signedIn(h.search)},
		{"GET /opds/books/{id}/file", h.signedIn(h.file)},
		{"GET /opds/books/{id}/cover", h.signedIn(h.cover)},
		// "/opds/" is the root too; anything else under it is a plain 404, not the web
		// app's page.
		{"GET /opds/", h.gated(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/opds/" {
				root(w, r)
				return
			}
			http.NotFound(w, r)
		})},
	}
}

// ServeHTTP serves the routes from a mux of their own (tests; Arrmada registers Routes).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// realm names what the reader is signing in to; readers show it in their prompt.
const realm = `Basic realm="Arrmada Books", charset="UTF-8"`

type userHandler func(w http.ResponseWriter, r *http.Request, u *auth.User)

// gated answers 404 while the Books module is off: no sign-in prompt, nothing to find.
func (h *Handler) gated(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.d.Library == nil || h.d.Auth == nil || (h.d.Enabled != nil && !h.d.Enabled(r.Context())) {
			http.NotFound(w, r)
			return
		}
		next(w, r)
	}
}

// signedIn checks the reader's Basic credentials on every request. No credentials is
// the reader asking how to sign in (401 with the challenge, not a failure); a wrong pair
// counts against the limiter; an account that may not connect gets 403.
func (h *Handler) signedIn(next userHandler) http.HandlerFunc {
	return h.gated(func(w http.ResponseWriter, r *http.Request) {
		name, pass, ok := r.BasicAuth()
		if !ok || name == "" {
			challenge(w)
			return
		}
		if h.d.Throttle != nil {
			if wait, retry := h.d.Throttle.Wait(r, name); wait {
				w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
				http.Error(w, "Too many sign-in attempts. Try again in a bit.", http.StatusTooManyRequests)
				return
			}
		}
		u, err := h.d.Auth.Authenticate(r.Context(), name, pass)
		switch {
		case err == nil:
		case errors.Is(err, audioserver.ErrNoAccess):
			http.Error(w, "This account can't use the e-reader catalogue. Ask an admin.", http.StatusForbidden)
			return
		case errors.Is(err, audioserver.ErrBadLogin):
			if h.d.Throttle != nil {
				h.d.Throttle.Failed(r, name)
			}
			challenge(w)
			return
		default:
			h.d.Log.Warn("opds: sign-in check failed", "err", err)
			http.Error(w, "Couldn't check the sign-in.", http.StatusInternalServerError)
			return
		}
		if h.d.Throttle != nil {
			h.d.Throttle.Succeeded(r, name)
		}
		next(w, r, u)
	})
}

func challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", realm)
	http.Error(w, "Sign in with your username and your audiobook & e-reader password.", http.StatusUnauthorized)
}
