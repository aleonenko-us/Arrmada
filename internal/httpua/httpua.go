// Package httpua gives Arrmada's outbound requests one honest User-Agent:
//
//	Arrmada/<version> (+https://github.com/tristenlammi/Arrmada)
//
// Go's own default ("Go-http-client/1.1") is what scrapers and bots send, and sites behind
// Cloudflare and the like turn it away with a block page. Saying plainly which program is
// asking — and where to read about it — lets an indexer or a proxy tell Arrmada apart from
// a bot and admit it on purpose. It never claims to be another program (SABnzbd, Prowlarr,
// a browser): the clients that must look like a browser to get past a challenge
// (FlareSolverr sessions) set their own header and are left alone.
package httpua

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/buildinfo"
)

// Homepage is where the User-Agent points anyone who wants to know what Arrmada is.
const Homepage = "https://github.com/tristenlammi/Arrmada"

// UserAgent is the header value. A release build says its version; a local build
// ("0.0.0-dev") adds its commit when it was stamped with one, so a log line on the other
// side still says which build asked.
func UserAgent() string {
	v := strings.TrimSpace(buildinfo.Version)
	if v == "" {
		v = "0.0.0-dev"
	}
	if c := strings.TrimSpace(buildinfo.Commit); strings.HasSuffix(v, "-dev") && c != "" && c != "unknown" {
		v += "+" + c
	}
	return "Arrmada/" + v + " (+" + Homepage + ")"
}

// Set puts the User-Agent on req unless it already carries one.
func Set(req *http.Request) {
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", UserAgent())
	}
}

// Transport wraps rt (nil: http.DefaultTransport) so every request it sends — redirects
// included — carries the User-Agent, unless the request set its own.
func Transport(rt http.RoundTripper) http.RoundTripper {
	if rt == nil {
		rt = http.DefaultTransport
	}
	if _, ok := rt.(transport); ok {
		return rt
	}
	return transport{next: rt}
}

// Client is an http.Client with the given timeout (0: none) that sends the User-Agent.
func Client(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: Transport(nil)}
}

// KeyedClient is Client for a service called with a key or token in a header: it follows a
// redirect only as far as NoCredentialRedirects allows.
func KeyedClient(timeout time.Duration) *http.Client {
	c := Client(timeout)
	c.CheckRedirect = NoCredentialRedirects
	return c
}

// credentialHeaders are the headers Arrmada's clients carry keys and tokens in.
var credentialHeaders = []string{"X-Api-Key", "X-Plex-Token", "Api-Key", "Authorization", "Cookie"}

// NoCredentialRedirects is an http.Client CheckRedirect for requests that carry a key or
// token in a header. Go copies custom headers such as X-Api-Key and X-Plex-Token onto every
// redirect, wherever it points (it drops only Authorization and Cookie itself), so a
// service, or anything pretending to be it, could bounce the key to another server. While
// the first request carries one, a redirect to another host, or from https down to http,
// is refused. Requests without one (a file download from a CDN) follow redirects as usual.
func NoCredentialRedirects(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	first := via[0]
	keyed := false
	for _, h := range credentialHeaders {
		if first.Header.Get(h) != "" {
			keyed = true
			break
		}
	}
	if !keyed {
		return nil
	}
	if !strings.EqualFold(req.URL.Host, first.URL.Host) {
		return fmt.Errorf("refused a redirect to another server (%s) that would have carried the key", req.URL.Hostname())
	}
	if first.URL.Scheme == "https" && req.URL.Scheme != "https" {
		return errors.New("refused a redirect from https to http that would have carried the key")
	}
	return nil
}

type transport struct{ next http.RoundTripper }

func (t transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") != "" {
		return t.next.RoundTrip(req)
	}
	// A RoundTripper must not change the request it was given.
	r := req.Clone(req.Context())
	r.Header.Set("User-Agent", UserAgent())
	return t.next.RoundTrip(r)
}
