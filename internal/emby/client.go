package emby

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The kinds of failure callers act on. Every error the client returns for a failed call is
// an *Error that wraps one of these (errors.Is works), or ErrNotConfigured on its own.
var (
	// ErrNotConfigured means no server URL is set, or a call that needs a key has none.
	ErrNotConfigured = errors.New("emby is not configured")
	// ErrUnauthorized is Emby refusing the key or token (HTTP 401): revoked, mistyped,
	// or a wrong password at sign-in. Retrying won't help.
	ErrUnauthorized = errors.New("emby rejected the key or sign-in")
	// ErrForbidden is a valid token without the right (HTTP 403): a non-admin user's token
	// on an admin call, or Quick Connect switched off.
	ErrForbidden = errors.New("emby refused the request")
	// ErrNotFound is HTTP 404: the item, user or Quick Connect request doesn't exist.
	ErrNotFound = errors.New("emby has no such item")
	// ErrUnreachable is no answer at all: refused, timed out, DNS, TLS.
	ErrUnreachable = errors.New("emby isn't reachable")
)

// Error is one failed call. Its message names the call (method and path, never the query,
// which can carry a Quick Connect secret) and what went wrong in plain words. It never holds
// the API key, a token or a password.
type Error struct {
	Op     string // "GET /Items"
	Status int    // HTTP status; 0 when there was no answer
	Kind   error  // one of the Err* values above, or nil for any other status
	Detail string // the network failure, or a short server message
}

func (e *Error) Error() string {
	var b strings.Builder
	switch {
	case e.Kind == ErrUnreachable:
		b.WriteString(ErrUnreachable.Error())
	case e.Kind != nil:
		b.WriteString(e.Kind.Error())
		fmt.Fprintf(&b, " (HTTP %d)", e.Status)
	default:
		fmt.Fprintf(&b, "emby returned HTTP %d", e.Status)
	}
	if e.Op != "" {
		b.WriteString(" on " + e.Op)
	}
	if e.Detail != "" {
		b.WriteString(": " + e.Detail)
	}
	return b.String()
}

func (e *Error) Unwrap() error { return e.Kind }

// Client talks to one Emby server. A Client is safe for concurrent use; WithToken
// derives one that acts as a signed-in user instead of with the API key.
type Client struct {
	base     string
	token    string // the API key, or a user's access token
	deviceID string
	version  string
	http     *http.Client
}

// ClientName is how Arrmada names itself to Emby: the dashboard's device list and
// session list show it.
const ClientName = "Arrmada"

// New builds a client for the server at baseURL using an API key (Dashboard → API Keys).
// deviceID should be stable per Arrmada install: Emby files sessions and tokens under
// it, and signing in again from the same device replaces that user's earlier token there.
// version is Arrmada's build. hc may be nil for the default client (15 s per call).
func New(baseURL, apiKey, deviceID, version string, hc *http.Client) *Client {
	if hc == nil {
		hc = defaultHTTPClient()
	}
	if deviceID == "" {
		deviceID = "arrmada"
	}
	if version == "" {
		version = "0"
	}
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	// Emby's REST API is mounted under /emby. Reverse proxies commonly expose a
	// different prefix, so preserve any configured path and append /emby unless
	// the user already supplied that API prefix.
	if base != "" {
		if u, err := url.Parse(base); err == nil {
			if !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/emby") && strings.TrimRight(u.Path, "/") != "/emby" {
				u.Path = strings.TrimRight(u.Path, "/") + "/emby"
				base = strings.TrimRight(u.String(), "/")
			}
		}
	}
	return &Client{
		base:     base,
		token:    apiKey,
		deviceID: deviceID,
		version:  version,
		http:     hc,
	}
}

// defaultHTTPClient fails fast on a dead address (5 s to connect) but gives a big library
// page time to arrive.
func defaultHTTPClient() *http.Client {
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if dt, ok := http.DefaultTransport.(*http.Transport); ok {
		tr = dt.Clone()
	}
	tr.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	tr.TLSHandshakeTimeout = 10 * time.Second
	tr.ResponseHeaderTimeout = 15 * time.Second
	tr.MaxIdleConnsPerHost = 4
	return &http.Client{Timeout: 30 * time.Second, Transport: tr}
}

// WithToken returns a client that acts as the user who owns token (from AuthenticateByName
// or Quick Connect) — for Logout, or anything else that must run as that user.
func (c *Client) WithToken(token string) *Client {
	cp := *c
	cp.token = token
	return &cp
}

// BaseURL is the server address the client talks to (no trailing slash).
func (c *Client) BaseURL() string { return c.base }

// authorization identifies Arrmada to Emby for unauthenticated sign-in calls.
func (c *Client) authorization() string {
	var b strings.Builder
	b.WriteString("Emby ")
	b.WriteString(`Client="` + ClientName + `", Device="` + ClientName + `", DeviceId="` + headerValue(c.deviceID) +
		`", Version="` + headerValue(c.version) + `"`)
	return b.String()
}

// setAuth uses Emby's documented API-key header for requests with a token. The
// X-Emby-Authorization metadata is used on sign-in requests that have no token yet.
func (c *Client) setAuth(req *http.Request) {
	req.Header.Set("X-Emby-Authorization", c.authorization())
	if c.token != "" {
		req.Header.Set("X-Emby-Token", c.token)
	}
}

// headerValue keeps a value from breaking out of its quotes in the header. Emby
// URL-decodes each value, so escaping is lossless.
func headerValue(s string) string {
	if !strings.ContainsAny(s, "\",\\\r\n") {
		return s
	}
	return url.QueryEscape(s)
}

// call sends one request and decodes a JSON answer into out (nil to discard it). needKey is
// false only for the calls Emby answers without a token (public info, sign-in, Quick
// Connect's own steps).
func (c *Client) call(ctx context.Context, method, path string, query url.Values, body, out any, needKey bool) error {
	if c.base == "" || (needKey && c.token == "") {
		return ErrNotConfigured
	}
	op := method + " " + path
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return &Error{Op: op, Detail: "could not encode the request"}
		}
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return &Error{Op: op, Kind: ErrNotConfigured, Detail: "the server address isn't a valid URL"}
	}
	req.Header.Set("Accept", "application/json")
	c.setAuth(req)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return &Error{Op: op, Kind: ErrUnreachable, Detail: netDetail(err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		// Read a little so the connection is reused, and keep a short message for the
		// log. Emby's error bodies are plain text or problem+json, never an echo of
		// the credentials we sent.
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return statusError(op, resp.StatusCode, msg)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return &Error{Op: op, Status: resp.StatusCode, Detail: "unexpected answer: " + err.Error()}
	}
	return nil
}

func statusError(op string, status int, body []byte) *Error {
	e := &Error{Op: op, Status: status}
	switch status {
	case http.StatusUnauthorized:
		e.Kind = ErrUnauthorized
	case http.StatusForbidden:
		e.Kind = ErrForbidden
	case http.StatusNotFound:
		e.Kind = ErrNotFound
	default:
		e.Detail = shortMessage(body)
	}
	return e
}

// shortMessage pulls a one-line reason out of an error body: problem+json's title, else
// the first line of plain text, capped.
func shortMessage(body []byte) string {
	var p struct {
		Title string `json:"title"`
	}
	s := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &p) == nil && p.Title != "" {
		s = p.Title
	}
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

// netDetail is the network failure without the URL that *url.Error prepends (it would
// carry a Quick Connect secret from the query).
func netDetail(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	return err.Error()
}

// SystemInfo is what the server says about itself. /System/Info/Public fills only the
// identity fields; /System/Info (needs the key) fills the rest.
type SystemInfo struct {
	ID                     string `json:"Id"`
	ServerName             string `json:"ServerName"`
	Version                string `json:"Version"`
	ProductName            string `json:"ProductName"`
	OperatingSystem        string `json:"OperatingSystem"`
	LocalAddress           string `json:"LocalAddress"`
	StartupWizardCompleted bool   `json:"StartupWizardCompleted"`
	HasPendingRestart      bool   `json:"HasPendingRestart"`
	IsShuttingDown         bool   `json:"IsShuttingDown"`
	HasUpdateAvailable     bool   `json:"HasUpdateAvailable"`
}

// SystemInfo validates the key and returns the server's identity (Id is what watch links
// carry as serverId).
func (c *Client) SystemInfo(ctx context.Context) (SystemInfo, error) {
	var si SystemInfo
	err := c.call(ctx, http.MethodGet, "/System/Info", nil, nil, &si, true)
	return si, err
}

// PublicSystemInfo needs no key: it tells a Emby server apart from anything else at the
// address before a key is entered.
func (c *Client) PublicSystemInfo(ctx context.Context) (SystemInfo, error) {
	var si SystemInfo
	err := c.call(ctx, http.MethodGet, "/System/Info/Public", nil, nil, &si, false)
	return si, err
}

// ticksPerSecond: Emby positions and runtimes are .NET ticks (100 ns).
const ticksPerSecond = 10_000_000

// TicksToDuration converts a Emby tick count to a duration.
func TicksToDuration(ticks int64) time.Duration { return time.Duration(ticks) * 100 }

// DurationToTicks converts a duration to Emby ticks.
func DurationToTicks(d time.Duration) int64 { return int64(d / 100) }

// ProviderIDs are the outside ids Emby matched an item to, from its ProviderIds map
// (keys "Tmdb", "Tvdb", "Imdb"; 0 / "" when absent). Raw keeps the whole map.
type ProviderIDs struct {
	TMDB int
	TVDB int
	IMDB string
	Raw  map[string]string
}

func (p *ProviderIDs) UnmarshalJSON(b []byte) error {
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		// A provider map with a non-string value isn't fatal to the page it came in.
		m = nil
	}
	*p = providerIDs(m)
	return nil
}

func providerIDs(m map[string]string) ProviderIDs {
	p := ProviderIDs{Raw: m}
	for k, v := range m {
		v = strings.TrimSpace(v)
		switch strings.ToLower(k) {
		case "tmdb":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				p.TMDB = n
			}
		case "tvdb":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				p.TVDB = n
			}
		case "imdb":
			if strings.HasPrefix(v, "tt") {
				p.IMDB = v
			}
		}
	}
	return p
}
