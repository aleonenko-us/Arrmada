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
