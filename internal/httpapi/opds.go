package httpapi

import (
	"context"
	"net/http"

	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/opds"
	"github.com/tristenlammi/arrmada/internal/requests"
)

// The OPDS catalogue for e-reader apps (internal/opds) lives at /opds. Its routes are
// public and reachable from outside the network as far as the router is concerned: the
// catalogue signs every request in itself, with HTTP Basic and the person's audiobook
// password. Wrong passwords go through the same limiter as Arrmada's own sign-in, under
// the keys the audiobook server's sign-in shares (appsignin.go).

// registerOPDS adds the catalogue's routes, each by its own pattern so the request log
// names "GET /opds/books/{id}/file", never the book.
func (a *api) registerOPDS(mux *router) {
	d := opds.Deps{
		Throttle:  appSignIn{a},
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
